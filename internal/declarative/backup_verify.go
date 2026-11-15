package declarative

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type backupPayload struct {
	kind, checksum, link, canonical string
	size                            int64
	links                           uint64
}

type backupVerifier struct {
	ctx      context.Context
	expected map[string]backupPayload
	seen     map[string]os.FileInfo
	files    map[backupFileID]verifiedBackupFile
	count    int
}

type verifiedBackupFile struct {
	canonical string
	info      os.FileInfo
}

// verifyBackupBundle checks an exact held-root tree against a reviewed manifest
// checksum. Returned private metadata stays internal. This proves observed byte
// and link agreement, not authenticity, native importability or restorability.
func verifyBackupBundle(ctx context.Context, root *os.Root, checksum string) (BackupManifest, error) {
	if !revisionPattern.MatchString(checksum) {
		return BackupManifest{}, errors.New("a reviewed backup manifest checksum is required")
	}
	m, actual, err := readBackupManifest(ctx, root)
	if err != nil {
		return BackupManifest{}, err
	}
	if actual != checksum {
		return BackupManifest{}, errors.New("backup manifest changed since review")
	}
	v := backupVerifier{ctx: ctx, expected: map[string]backupPayload{}, seen: map[string]os.FileInfo{}, files: map[backupFileID]verifiedBackupFile{}}
	add := func(path string, payload backupPayload) error {
		v.expected[path] = payload
		for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
			if _, found := v.expected[parent]; !found {
				v.expected[parent] = backupPayload{kind: "directory"}
			}
			if len(v.expected) > maxBackupEntries+8192 {
				return errors.New("backup verification path count exceeds limit")
			}
		}
		return ctx.Err()
	}
	for _, f := range m.Files {
		if err := add(f.BundlePath, backupPayload{kind: f.Type, checksum: f.SHA256, link: f.LinkTarget, canonical: f.HardlinkTo, size: f.SizeBytes, links: f.CapturedLinks}); err != nil {
			return BackupManifest{}, err
		}
	}
	for _, n := range m.NativeExports {
		if err := add(n.BundlePath, backupPayload{kind: "file", checksum: n.SHA256, size: n.SizeBytes, links: 1}); err != nil {
			return BackupManifest{}, err
		}
	}
	// Metadata has already been bounded and read; rehash during traversal to
	// detect a replacement between manifest interpretation and payload checking.
	info, err := root.Lstat("manifest.json")
	if err != nil {
		return BackupManifest{}, errors.New("private backup manifest disappeared")
	}
	v.expected["manifest.json"] = backupPayload{kind: "file", checksum: checksum, size: info.Size(), links: 1}
	if err := v.directory(root, "", 0, ""); err != nil {
		return BackupManifest{}, err
	}
	if len(v.seen) != len(v.expected) {
		return BackupManifest{}, errors.New("backup is missing a recorded payload")
	}
	for path, payload := range v.expected {
		if err := ctx.Err(); err != nil {
			return BackupManifest{}, err
		}
		current, err := root.Lstat(path)
		if err != nil || !stableBackupNode(v.seen[path], current) {
			return BackupManifest{}, errors.New("backup tree changed during verification")
		}
		if payload.kind == "hardlink" && !os.SameFile(v.seen[path], v.seen[payload.canonical]) {
			return BackupManifest{}, errors.New("backup hardlink identity does not match its manifest")
		}
	}
	if err := ctx.Err(); err != nil {
		return BackupManifest{}, err
	}
	if err := validateBackupCoverage(ctx, root, m); err != nil {
		return BackupManifest{}, err
	}
	return m, nil
}

func (v *backupVerifier) directory(root *os.Root, prefix string, depth int, mount string) error {
	invalid := errors.New("backup tree is unsafe, changed or disagrees with its manifest")
	if depth > maxBackupDepth+4 {
		return invalid
	}
	before, err := root.Stat(".")
	if err != nil || !before.IsDir() || before.Mode().Perm()&0077 != 0 {
		return invalid
	}
	dir, err := root.Open(".")
	if err != nil {
		return invalid
	}
	defer dir.Close()
	actualMount, err := backupMountID(dir)
	if err != nil || mount != "" && mount != actualMount {
		return invalid
	}
	for {
		if err := v.ctx.Err(); err != nil {
			return err
		}
		entries, err := dir.ReadDir(64)
		if err != nil && err != io.EOF {
			return invalid
		}
		for _, entry := range entries {
			if err := v.ctx.Err(); err != nil {
				return err
			}
			v.count++
			if v.count > maxBackupEntries+8192 || !backupRelativePath(entry.Name()) || strings.Contains(entry.Name(), string(filepath.Separator)) {
				return invalid
			}
			path := filepath.Join(prefix, entry.Name())
			payload, found := v.expected[path]
			if _, duplicate := v.seen[path]; !found || duplicate {
				return invalid
			}
			info, err := root.Lstat(entry.Name())
			if err != nil {
				return invalid
			}
			v.seen[path] = info
			switch payload.kind {
			case "directory":
				if !info.IsDir() {
					return invalid
				}
				child, err := root.OpenRoot(entry.Name())
				if err != nil {
					return invalid
				}
				opened, e := child.Stat(".")
				if e == nil && stableBackupNode(info, opened) {
					e = v.directory(child, path, depth+1, actualMount)
				} else {
					e = invalid
				}
				child.Close()
				if e != nil {
					return e
				}
			case "file", "hardlink":
				if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != payload.size {
					return invalid
				}
				f, err := root.OpenFile(entry.Name(), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
				if err != nil {
					return invalid
				}
				err = v.file(f, info, path, payload, actualMount)
				f.Close()
				if err != nil {
					return err
				}
			case "symlink":
				if info.Mode()&os.ModeSymlink == 0 {
					return invalid
				}
				link, err := root.Readlink(entry.Name())
				if err != nil || link != payload.link {
					return invalid
				}
			default:
				return invalid
			}
			current, err := root.Lstat(entry.Name())
			if err != nil || !stableBackupNode(info, current) {
				return invalid
			}
		}
		if err == io.EOF {
			break
		}
	}
	after, err := root.Stat(".")
	if err != nil || !stableBackupNode(before, after) {
		return invalid
	}
	return v.ctx.Err()
}

func (v *backupVerifier) file(f *os.File, before os.FileInfo, path string, payload backupPayload, mount string) error {
	invalid := errors.New("backup payload identity, size, privacy or checksum did not verify")
	opened, err := f.Stat()
	if err != nil || !stableBackupNode(before, opened) {
		return invalid
	}
	actualMount, err := backupMountID(f)
	stat, ok := opened.Sys().(*syscall.Stat_t)
	if err != nil || actualMount != mount || !ok || uint64(stat.Nlink) != payload.links {
		return invalid
	}
	canonical := path
	if payload.kind == "hardlink" {
		canonical = payload.canonical
	}
	identity := backupFileID{uint64(stat.Dev), stat.Ino}
	if prior, found := v.files[identity]; found {
		if prior.canonical != canonical || !stableBackupNode(prior.info, opened) {
			return invalid
		}
		return v.ctx.Err()
	}
	hash, err := hashBackupFile(v.ctx, f)
	if err != nil {
		if v.ctx.Err() != nil {
			return v.ctx.Err()
		}
		return invalid
	}
	if hash != payload.checksum {
		return invalid
	}
	v.files[identity] = verifiedBackupFile{canonical: canonical, info: opened}
	return nil
}
