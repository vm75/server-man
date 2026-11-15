package declarative

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"server-man/internal/core"
)

// Native archives stream to an exclusively created held private file, never to
// text capture or job output. No native command receives a destination path.
func streamBackupArtifact(ctx context.Context, command core.Command, directory, path, kind string, object *NativeBackupObject, stream core.StreamingExecutor) (BackupExport, error) {
	return writeBackupArtifact(ctx, directory, path, kind, object, func(writer io.Writer) error { return stream.Stream(ctx, command, writer, io.Discard) })
}
func writeBackupArtifact(ctx context.Context, directory, path, kind string, object *NativeBackupObject, transfer func(io.Writer) error) (BackupExport, error) {
	if !portablePath(path) || !strings.HasPrefix(path, "native/") {
		return BackupExport{}, errors.New("invalid native export path")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return BackupExport{}, errors.New("native exports require a private real staging directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return BackupExport{}, err
	}
	defer root.Close()
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return BackupExport{}, errors.New("native export staging changed")
	}
	parts := strings.Split(filepath.ToSlash(filepath.Dir(path)), "/")
	parent := ""
	for _, part := range parts {
		parent = filepath.Join(parent, part)
		if err := root.Mkdir(parent, 0700); err != nil && !os.IsExist(err) {
			return BackupExport{}, err
		}
		dir, e := root.Lstat(parent)
		if e != nil || !dir.IsDir() || dir.Mode().Perm()&0077 != 0 {
			return BackupExport{}, errors.New("native export parent is unsafe")
		}
	}
	if _, err := root.Lstat(path); !os.IsNotExist(err) {
		return BackupExport{}, errors.New("native export target must be absent")
	}
	f, err := root.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return BackupExport{}, errors.New("cannot exclusively create native export")
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			root.Remove(path)
		}
	}()
	budget, err := backupAvailableSpace(f)
	if err != nil {
		return BackupExport{}, err
	}
	if budget > math.MaxInt64 {
		budget = math.MaxInt64
	}
	writer := &backupStreamWriter{ctx: ctx, file: f, hash: sha256.New(), remaining: int64(budget)}
	err = transfer(writer)
	if writer.failure != nil {
		return BackupExport{}, writer.failure
	}
	if err != nil {
		if ctx.Err() != nil {
			return BackupExport{}, ctx.Err()
		}
		return BackupExport{}, errors.New("native export stream failed; inspect native backend state before retry")
	}
	if ctx.Err() != nil {
		return BackupExport{}, ctx.Err()
	}
	if writer.size == 0 {
		return BackupExport{}, errors.New("native export produced an empty artifact")
	}
	if err := f.Sync(); err != nil {
		return BackupExport{}, err
	}
	final, err := f.Stat()
	entry, entryErr := root.Lstat(path)
	if err != nil || entryErr != nil || !entry.Mode().IsRegular() || !os.SameFile(final, entry) || final.Size() != writer.size || final.Mode().Perm()&0077 != 0 {
		return BackupExport{}, errors.New("native export file changed while streaming")
	}
	if err := f.Close(); err != nil {
		return BackupExport{}, err
	}
	complete = true
	copyObject := *object
	return BackupExport{BundlePath: path, Kind: kind, Native: &copyObject, SizeBytes: writer.size, SHA256: hex.EncodeToString(writer.hash.Sum(nil))}, nil
}

type backupStreamWriter struct {
	ctx             context.Context
	file            io.Writer
	hash            hash.Hash
	remaining, size int64
	failure         error
}

func (w *backupStreamWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		w.failure = err
		return 0, err
	}
	if int64(len(data)) > w.remaining {
		w.failure = errors.New("native export exceeds available storage budget")
		return 0, w.failure
	}
	n, err := w.file.Write(data)
	w.hash.Write(data[:n])
	w.size += int64(n)
	w.remaining -= int64(n)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.failure = errors.New("native export storage write failed")
	}
	return n, err
}

func discardBackupArtifact(directory, path string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(path)
}

func backupAvailableSpace(file *os.File) (uint64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Fstatfs(int(file.Fd()), &fs); err != nil || fs.Bsize <= 0 || fs.Flags&1 != 0 {
		return 0, errors.New("capture filesystem capacity could not be verified")
	}
	if fs.Bavail > math.MaxUint64/uint64(fs.Bsize) {
		return 0, errors.New("invalid capture filesystem capacity")
	}
	available := fs.Bavail * uint64(fs.Bsize)
	if available <= backupMetadataReserve {
		return 0, errors.New("insufficient free space reserved for private capture metadata")
	}
	return available - backupMetadataReserve, nil
}
