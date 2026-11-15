package declarative

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

type BackupArtifact struct {
	Stage            string `json:"stage"`
	StagingPath      string `json:"stagingPath"`
	Device           uint64 `json:"device"`
	Inode            uint64 `json:"inode"`
	ParentDevice     uint64 `json:"parentDevice"`
	ParentInode      uint64 `json:"parentInode"`
	ManifestChecksum string `json:"manifestChecksum,omitempty"`
}

func backupArtifactPending(a *BackupArtifact) bool {
	return a != nil && a.Stage != "published" && a.Stage != "discarded" && a.Stage != "released"
}

type backupPublication struct {
	parent                         *os.Root
	parentFile                     *os.File
	parentInfo                     os.FileInfo
	stageInfo                      os.FileInfo
	destinationInfo                os.FileInfo
	parentPath, destination, stage string
	published                      bool
}

func newBackupPublication(destination string, expected os.FileInfo) (*backupPublication, error) {
	parentPath := filepath.Dir(destination)
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, err
	}
	directory, err := parent.Open(".")
	if err != nil {
		parent.Close()
		return nil, err
	}
	info, err := directory.Stat()
	if err != nil {
		directory.Close()
		parent.Close()
		return nil, err
	}
	p := &backupPublication{parent: parent, parentFile: directory, parentInfo: info, parentPath: parentPath, destination: filepath.Base(destination), destinationInfo: expected}
	success := false
	defer func() {
		if !success {
			p.close()
		}
	}()
	if err := p.checkDestination(); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	p.stage = ".staging-backup-" + hex.EncodeToString(nonce[:])
	if err := parent.Mkdir(p.stage, 0700); err != nil {
		return nil, err
	}
	p.stageInfo, err = parent.Lstat(p.stage)
	if err != nil {
		parent.Remove(p.stage)
		return nil, err
	}
	if err := directory.Sync(); err != nil {
		parent.Remove(p.stage)
		return nil, err
	}
	success = true
	return p, nil
}
func (p *backupPublication) close() { p.parentFile.Close(); p.parent.Close() }

// The final component is real while the held parent descriptor pins namespace
// resolution for collectors that accept a local path.
func (p *backupPublication) path() string {
	return filepath.Join(descriptorPath(p.parentFile), p.stage)
}
func (p *backupPublication) artifact() *BackupArtifact {
	source := p.stageInfo.Sys().(*syscall.Stat_t)
	parent := p.parentInfo.Sys().(*syscall.Stat_t)
	return &BackupArtifact{Stage: "building", StagingPath: filepath.Join(p.parentPath, p.stage), Device: uint64(source.Dev), Inode: source.Ino, ParentDevice: uint64(parent.Dev), ParentInode: parent.Ino}
}
func (p *backupPublication) checkParent() error {
	info, err := os.Stat(p.parentPath)
	if err != nil || !info.IsDir() || !os.SameFile(info, p.parentInfo) {
		return errors.New("capture destination parent changed; inspect the recorded staging location")
	}
	return nil
}
func (p *backupPublication) checkStage() error {
	info, err := p.parent.Lstat(p.stage)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !os.SameFile(info, p.stageInfo) {
		return errors.New("capture staging identity changed; automatic publication/cleanup refused")
	}
	return nil
}
func (p *backupPublication) checkDestination() error {
	info, err := p.parent.Lstat(p.destination)
	if p.destinationInfo == nil {
		if !os.IsNotExist(err) {
			return errors.New("capture destination appeared after review")
		}
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !os.SameFile(info, p.destinationInfo) {
		return errors.New("capture destination identity or private permissions changed")
	}
	dir, err := p.parent.Open(p.destination)
	if err != nil {
		return err
	}
	defer dir.Close()
	actual, err := dir.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return errors.New("capture destination changed while checking emptiness")
	}
	entries, err := dir.ReadDir(1)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) != 0 {
		return errors.New("capture destination is no longer empty")
	}
	return nil
}
func (p *backupPublication) sync(ctx context.Context) error {
	if err := p.checkParent(); err != nil {
		return err
	}
	if err := p.checkStage(); err != nil {
		return err
	}
	root, err := p.parent.OpenRoot(p.stage)
	if err != nil {
		return err
	}
	defer root.Close()
	count := 0
	if err := syncBackupDirectory(ctx, root, &count, 0, ""); err != nil {
		return err
	}
	return p.parentFile.Sync()
}
func syncBackupDirectory(ctx context.Context, root *os.Root, count *int, depth int, mount string) error {
	if depth > maxBackupDepth+4 {
		return errors.New("capture sync directory depth exceeds limit")
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	actualMount, err := backupMountID(dir)
	if err != nil {
		return err
	}
	if mount == "" {
		mount = actualMount
	} else if actualMount != mount {
		return errors.New("private capture contains an unexpected nested mount")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := dir.ReadDir(64)
		if err != nil && err != io.EOF {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			*count++
			if *count > maxBackupEntries+8192 {
				return errors.New("capture sync entry count exceeds limit")
			}
			info, e := root.Lstat(entry.Name())
			if e != nil {
				return e
			}
			switch {
			case info.IsDir():
				child, e := root.OpenRoot(entry.Name())
				if e != nil {
					return e
				}
				actual, e := child.Stat(".")
				if e != nil || !os.SameFile(info, actual) {
					child.Close()
					return errors.New("capture directory changed during sync")
				}
				e = syncBackupDirectory(ctx, child, count, depth+1, mount)
				child.Close()
				if e != nil {
					return e
				}
			case info.Mode().IsRegular():
				file, e := root.OpenFile(entry.Name(), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
				if e != nil {
					return e
				}
				actual, e := file.Stat()
				if e == nil && (!actual.Mode().IsRegular() || !os.SameFile(info, actual)) {
					e = errors.New("capture file changed during sync")
				}
				if e == nil {
					var actual string
					actual, e = backupMountID(file)
					if e == nil && actual != mount {
						e = errors.New("capture file mount identity changed")
					}
				}
				if e == nil {
					e = file.Sync()
				}
				file.Close()
				if e != nil {
					return e
				}
			case info.Mode()&os.ModeSymlink != 0:
				// The containing directory fsync persists the link; never follow its target.
			default:
				return errors.New("unexpected special file in private capture")
			}
		}
		if err == io.EOF {
			break
		}
	}
	return dir.Sync()
}
func (p *backupPublication) rename() error {
	if err := p.checkParent(); err != nil {
		return err
	}
	if err := p.checkStage(); err != nil {
		return err
	}
	if err := p.checkDestination(); err != nil {
		return err
	}
	flags := uint(0)
	if p.destinationInfo == nil {
		flags = unix.RENAME_NOREPLACE
	}
	if err := unix.Renameat2(int(p.parentFile.Fd()), p.stage, int(p.parentFile.Fd()), p.destination, flags); err != nil {
		return errors.New("capture publication failed; destination must remain absent or the reviewed empty directory")
	}
	p.published = true // Never remove a published/uncertain result during cleanup.
	info, err := p.parent.Lstat(p.destination)
	if err != nil || !info.IsDir() || !os.SameFile(info, p.stageInfo) {
		return errors.New("capture was renamed but destination identity changed; inspect publication")
	}
	if err := p.checkParent(); err != nil {
		return err
	}

	return nil
}
func (p *backupPublication) discard() error {
	if p.published {
		return nil
	}
	if err := p.checkStage(); err != nil {
		return err
	}
	if err := p.parent.RemoveAll(p.stage); err != nil {
		return err
	}
	return p.parentFile.Sync()
}
