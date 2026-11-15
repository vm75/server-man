package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxBackupEntries = 50000
const maxBackupMetadataBytes = 64 << 20
const maxBackupDepth = 128
const backupMetadataReserve = maxBackupMetadataBytes + (8 << 20)

type backupFileID struct{ dev, ino uint64 }
type backupLink struct {
	path, checksum string
	info           os.FileInfo
}
type backupFilesystem struct {
	ctx            context.Context
	source, target *os.Root
	entries        []BackupManifestFile
	metadataBytes  int
	limit          int
	links          map[backupFileID]backupLink
	paths          map[string]bool
}

func newBackupFilesystem(ctx context.Context, source, target string, limit int) (*backupFilesystem, error) {
	src, err := os.OpenRoot(source)
	if err != nil {
		return nil, err
	}
	dst, err := os.OpenRoot(target)
	if err != nil {
		src.Close()
		return nil, err
	}
	return &backupFilesystem{ctx: ctx, source: src, target: dst, limit: limit, links: map[backupFileID]backupLink{}, paths: map[string]bool{}}, nil
}
func (b *backupFilesystem) close() { b.source.Close(); b.target.Close() }
func backupMetadata(info os.FileInfo) (BackupManifestFile, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return BackupManifestFile{}, errors.New("Linux file metadata is unavailable")
	}
	return BackupManifestFile{Mode: fmt.Sprintf("%04o", stat.Mode&07777), UID: stat.Uid, GID: stat.Gid, MTime: info.ModTime().UTC().Format(time.RFC3339Nano), ATime: time.Unix(stat.Atim.Sec, stat.Atim.Nsec).UTC().Format(time.RFC3339Nano), LinkCount: uint64(stat.Nlink), AllocatedBytes: stat.Blocks * 512}, nil
}
func stableBackupNode(before, after os.FileInfo) bool {
	a, ok := before.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	z, ok := after.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return os.SameFile(before, after) && a.Mode == z.Mode && a.Uid == z.Uid && a.Gid == z.Gid && a.Nlink == z.Nlink && a.Size == z.Size && a.Mtim == z.Mtim && a.Ctim == z.Ctim
}
func (b *backupFilesystem) append(entry BackupManifestFile) error {
	if len(b.entries) >= b.limit {
		return errors.New("capture-wide filesystem entry limit exceeded")
	}
	if b.paths[entry.BundlePath] {
		return errors.New("duplicate capture path")
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return errors.New("file metadata cannot be serialized")
	}
	if len(raw) > maxBackupMetadataBytes-b.metadataBytes {
		return errors.New("capture-wide metadata limit exceeded")
	}
	b.metadataBytes += len(raw)
	b.paths[entry.BundlePath] = true
	b.entries = append(b.entries, entry)
	return nil
}
func backupMountID(file *os.File) (string, error) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/fdinfo/%d", os.Getpid(), file.Fd()))
	if err != nil {
		return "", errors.New("capture requires readable proc descriptor metadata")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(raw) > 8192 {
		return "", errors.New("proc descriptor metadata exceeds capture limit")
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			if _, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return fields[1], nil
			}
		}
	}
	return "", errors.New("source mount identity is unavailable")
}
func backupRealParent(root *os.Root, path string, create bool) (*os.Root, string, error) {
	if !portablePath(path) || !utf8.ValidString(path) || path == "." {
		return nil, "", errors.New("invalid capture path")
	}
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, "", err
	}
	success := false
	defer func() {
		if !success {
			current.Close()
		}
	}()
	for _, part := range strings.Split(filepath.Dir(path), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		if create {
			if err := current.Mkdir(part, 0700); err != nil && !os.IsExist(err) {
				return nil, "", err
			}
		}
		info, err := current.Lstat(part)
		if err != nil || !info.IsDir() {
			return nil, "", errors.New("capture parents must be real directories")
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			return nil, "", err
		}
		actual, e := next.Stat(".")
		entry, entryErr := current.Lstat(part)
		if e != nil || entryErr != nil || !entry.IsDir() || !os.SameFile(info, actual) || !os.SameFile(entry, actual) {
			next.Close()
			return nil, "", errors.New("capture parent changed")
		}
		current.Close()
		current = next
	}
	success = true
	return current, filepath.Base(path), nil
}

func (b *backupFilesystem) capture(sourcePath, targetPath, kind string) (BackupManifestFile, error) {
	if len(b.entries) >= b.limit {
		return BackupManifestFile{}, errors.New("capture-wide filesystem entry limit exceeded")
	}
	source, name, err := backupRealParent(b.source, sourcePath, false)
	if err != nil {
		return BackupManifestFile{}, err
	}
	defer source.Close()
	if err := b.ensureTargetParent(targetPath); err != nil {
		return BackupManifestFile{}, err
	}
	info, err := source.Lstat(name)
	if err != nil {
		return BackupManifestFile{}, err
	}
	index := len(b.entries)
	if kind == "directory" {
		if !info.IsDir() {
			return BackupManifestFile{}, errors.New("declared directory changed type")
		}
		root, err := source.OpenRoot(name)
		if err != nil {
			return BackupManifestFile{}, err
		}
		defer root.Close()
		actual, e := root.Stat(".")
		if e != nil || !os.SameFile(info, actual) {
			return BackupManifestFile{}, errors.New("declared directory changed")
		}
		file, e := root.Open(".")
		if e != nil {
			return BackupManifestFile{}, e
		}
		mount, e := backupMountID(file)
		file.Close()
		if e != nil {
			return BackupManifestFile{}, e
		}
		if err := b.walk(root, targetPath, ".", mount, 0); err != nil {
			return BackupManifestFile{}, err
		}
		current, e := source.Lstat(name)
		if e != nil || !current.IsDir() || !stableBackupNode(info, current) {
			return BackupManifestFile{}, errors.New("declared source directory changed during capture")
		}
	} else {
		if !info.Mode().IsRegular() {
			return BackupManifestFile{}, errors.New("declared file changed type")
		}
		if err := b.file(source, name, targetPath, info, ""); err != nil {
			return BackupManifestFile{}, err
		}
	}
	return b.entries[index], nil
}
func (b *backupFilesystem) ensureTargetParent(path string) error {
	root, _, err := backupRealParent(b.target, path, true)
	if root != nil {
		root.Close()
	}
	return err
}

func (b *backupFilesystem) walk(source *os.Root, target, relative, mount string, depth int) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if depth > maxBackupDepth {
		return errors.New("capture directory depth exceeds limit")
	}
	dir, err := source.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	before, err := dir.Stat()
	if err != nil {
		return err
	}
	actualMount, err := backupMountID(dir)
	if err != nil || actualMount != mount {
		return errors.New("nested mounts require separate declared storage capture")
	}
	entry, err := backupMetadata(before)
	if err != nil {
		return err
	}
	entry.Type = "directory"
	entry.BundlePath = target
	entry.Xattrs, entry.XattrCoverage, err = readBackupXattrs(dir, "")
	if err != nil {
		return err
	}
	if err := b.append(entry); err != nil {
		return err
	}
	if err := b.target.Mkdir(target, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	existing, err := b.target.Lstat(target)
	if err != nil || !existing.IsDir() {
		return errors.New("capture directory target is unsafe")
	}
	for {
		if err := b.ctx.Err(); err != nil {
			return err
		}
		children, e := dir.ReadDir(64)
		if e != nil && e != io.EOF {
			return e
		}
		for _, child := range children {
			if err := b.ctx.Err(); err != nil {
				return err
			}
			if len(b.entries) >= b.limit {
				return errors.New("capture-wide filesystem entry limit exceeded")
			}
			name := child.Name()
			if !utf8.ValidString(name) || !backupRelativePath(name) || strings.Contains(name, string(filepath.Separator)) {
				return errors.New("source name cannot be represented as a portable capture path")
			}
			info, err := source.Lstat(name)
			if err != nil {
				return err
			}
			to := filepath.Join(target, name)
			rel := filepath.Join(relative, name)
			switch {
			case info.IsDir():
				nested, err := source.OpenRoot(name)
				if err != nil {
					return err
				}
				opened, e := nested.Stat(".")
				if e != nil || !os.SameFile(info, opened) {
					nested.Close()
					return errors.New("source directory changed")
				}
				err = b.walk(nested, to, rel, mount, depth+1)
				nested.Close()
				if err != nil {
					return err
				}
				current, e := source.Lstat(name)
				if e != nil || !current.IsDir() || !stableBackupNode(info, current) {
					return errors.New("source directory entry changed during capture")
				}
			case info.Mode().IsRegular():
				if err := b.file(source, name, to, info, mount); err != nil {
					return err
				}
			case info.Mode()&os.ModeSymlink != 0:
				if err := b.symlink(source, dir, name, to, rel, info); err != nil {
					return err
				}
			default:
				return errors.New("special files need an explicit capture strategy")
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	after, err := dir.Stat()
	if err != nil || !stableBackupNode(before, after) {
		return errors.New("source directory metadata changed during capture")
	}
	attrs, coverage, err := readBackupXattrs(dir, "")
	if err != nil || coverage != entry.XattrCoverage || !reflect.DeepEqual(attrs, entry.Xattrs) {
		return errors.New("source directory attributes changed during capture")
	}
	return nil
}
func (b *backupFilesystem) file(parent *os.Root, name, target string, before os.FileInfo, mount string) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	f, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_NOATIME, 0)
	if errors.Is(err, syscall.EPERM) {
		f, err = parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return errors.New("source file changed before capture")
	}
	if mount != "" {
		actual, e := backupMountID(f)
		if e != nil || actual != mount {
			return errors.New("nested file mounts require explicit capture")
		}
	}
	entry, err := backupMetadata(info)
	if err != nil {
		return err
	}
	entry.Type = "file"
	entry.BundlePath = target
	entry.Xattrs, entry.XattrCoverage, err = readBackupXattrs(f, "")
	if err != nil {
		return err
	}
	stat := info.Sys().(*syscall.Stat_t)
	identity := backupFileID{uint64(stat.Dev), stat.Ino}
	if prior, found := b.links[identity]; found {
		if !stableBackupNode(prior.info, info) {
			return errors.New("hardlinked source changed during capture")
		}
		if err := b.target.Link(prior.path, target); err != nil {
			return err
		}
		entry.Type = "hardlink"
		entry.HardlinkTo = prior.path
		entry.SizeBytes = info.Size()
		entry.SHA256 = prior.checksum
	} else {
		destination, err := b.target.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		copied, e := copyBackupHandles(b.ctx, f, destination, 0600)
		closeErr := destination.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		entry.SizeBytes = copied.SizeBytes
		entry.SHA256 = copied.SHA256
		b.links[identity] = backupLink{target, entry.SHA256, info}
	}
	after, err := f.Stat()
	current, e := parent.Lstat(name)
	if err != nil || e != nil || !current.Mode().IsRegular() || !stableBackupNode(info, after) || !os.SameFile(current, info) {
		return errors.New("source file changed during capture")
	}
	attrs, coverage, err := readBackupXattrs(f, "")
	if err != nil || coverage != entry.XattrCoverage || !reflect.DeepEqual(attrs, entry.Xattrs) {
		return errors.New("source file attributes changed during capture")
	}
	return b.append(entry)
}
func (b *backupFilesystem) symlink(parent *os.Root, dir *os.File, name, target, relative string, info os.FileInfo) error {
	link, err := parent.Readlink(name)
	if err != nil {
		return err
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(relative), link))
	if filepath.IsAbs(link) || !utf8.ValidString(link) || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return errors.New("symlink escapes its declared directory")
	}
	entry, err := backupMetadata(info)
	if err != nil {
		return err
	}
	entry.Type = "symlink"
	entry.BundlePath = target
	entry.LinkTarget = link
	entry.Xattrs, entry.XattrCoverage, err = readBackupXattrs(nil, filepath.Join(descriptorPath(dir), name))
	if err != nil {
		return err
	}
	if err := b.target.Symlink(link, target); err != nil {
		return err
	}
	after, err := parent.Lstat(name)
	current, e := parent.Readlink(name)
	if err != nil || e != nil || !stableBackupNode(info, after) || current != link {
		return errors.New("source symlink changed during capture")
	}
	return b.append(entry)
}

func (b *backupFilesystem) finish() []BackupManifestFile {
	entries := slices.Clone(b.entries)
	counts := map[string]uint64{}
	for _, entry := range entries {
		if entry.Type == "file" {
			counts[entry.BundlePath]++
		} else if entry.Type == "hardlink" {
			counts[entry.HardlinkTo]++
		}
	}
	for index := range entries {
		entry := &entries[index]
		if entry.Type == "file" {
			entry.CapturedLinks = counts[entry.BundlePath]
		} else if entry.Type == "hardlink" {
			entry.CapturedLinks = counts[entry.HardlinkTo]
		}
	}
	return entries
}

func backupRelativePath(path string) bool {
	return path != "" && utf8.ValidString(path) && !filepath.IsAbs(path) && filepath.Clean(path) == path && path != "." && path != ".." && !strings.HasPrefix(path, "../") && !strings.ContainsRune(path, 0)
}

type BackupPrincipal struct {
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Groups []int  `json:"groups"`
	UIDMap string `json:"uidMap"`
	GIDMap string `json:"gidMap"`
}

func captureBackupPrincipal() (BackupPrincipal, error) {
	groups, err := os.Getgroups()
	if err != nil {
		return BackupPrincipal{}, errors.New("capture account groups are unavailable")
	}
	principal := BackupPrincipal{UID: os.Geteuid(), GID: os.Getegid(), Groups: groups}
	for path, destination := range map[string]*string{"/proc/self/uid_map": &principal.UIDMap, "/proc/self/gid_map": &principal.GIDMap} {
		f, err := os.Open(path)
		if err != nil {
			return BackupPrincipal{}, errors.New("capture UID/GID mapping is unavailable")
		}
		raw, e := io.ReadAll(io.LimitReader(f, 65537))
		f.Close()
		if e != nil || len(raw) == 0 || len(raw) > 65536 {
			return BackupPrincipal{}, errors.New("capture UID/GID mapping is invalid or exceeds limit")
		}
		*destination = string(raw)
	}
	return principal, nil
}
