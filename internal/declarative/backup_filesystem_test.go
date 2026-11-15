package declarative

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func filesystemFixture(t *testing.T, limit int) (*backupFilesystem, string, string) {
	t.Helper()
	source, target := t.TempDir(), privateExportDirectory(t)
	b, err := newBackupFilesystem(context.Background(), source, target, limit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.close)
	return b, source, target
}
func TestFilesystemCapturePreservesMetadataLinksEmptyDirectoriesAndBinaryAttributes(t *testing.T) {
	b, source, target := filesystemFixture(t, 100)
	tree := filepath.Join(source, "tree")
	if err := os.MkdirAll(filepath.Join(tree, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tree, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte{0, 1, 2, 255}, 1000)
	path := filepath.Join(tree, "payload")
	if err := os.WriteFile(path, data, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, os.ModeSetuid|0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(tree, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../payload", filepath.Join(tree, "sub", "link")); err != nil {
		t.Fatal(err)
	}
	attribute := []byte{0, 255, 1, 128, 2}
	if err := syscall.Setxattr(path, "user.server_man_test", attribute, 0); err != nil {
		t.Fatal("temporary filesystem xattrs unavailable", err)
	}
	atime, mtime := time.Unix(1700000000, 123), time.Unix(1700000001, 456)
	if err := os.Chtimes(path, atime, mtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tree, os.ModeSetgid|0770); err != nil {
		t.Fatal(err)
	}
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.capture("tree", "data/tree", "directory"); err != nil {
		t.Fatal(err)
	}
	entries := b.finish()
	if len(entries) != 6 {
		t.Fatal("directory/link metadata omitted", len(entries))
	}
	files, links := 0, 0
	for _, entry := range entries {
		if entry.XattrCoverage != "visible" {
			t.Fatal("attribute coverage missing", entry.BundlePath, entry.XattrCoverage)
		}
		if entry.Type == "file" {
			files++
			stat := original.Sys().(*syscall.Stat_t)
			if entry.UID != stat.Uid || entry.GID != stat.Gid || entry.Mode != "4750" || entry.LinkCount != 2 || entry.SHA256 != digest(data) || entry.ATime != atime.UTC().Format(time.RFC3339Nano) || entry.MTime != mtime.UTC().Format(time.RFC3339Nano) {
				t.Fatal("source inode metadata changed", entry)
			}
			if !bytes.Equal(entry.Xattrs["user.server_man_test"], attribute) {
				t.Fatal("binary attributes omitted")
			}
			actual, err := os.Stat(filepath.Join(target, entry.BundlePath))
			if err != nil || actual.Mode().Perm() != 0600 || actual.Mode()&os.ModeSetuid != 0 {
				t.Fatal("source executable/security modes applied to capture", actual, err)
			}
		}
		if entry.Type == "hardlink" {
			links++
			if entry.HardlinkTo == "" || entry.SHA256 != digest(data) {
				t.Fatal("hardlink relation omitted", entry)
			}
		}
		if entry.Type == "symlink" && entry.LinkTarget != "../payload" {
			t.Fatal("relative link changed", entry)
		}
	}
	if files != 1 || links != 1 {
		t.Fatal("hardlinked content duplicated", files, links)
	}
	a, _ := os.Stat(filepath.Join(target, "data/tree/payload"))
	z, _ := os.Stat(filepath.Join(target, "data/tree/alias"))
	if !os.SameFile(a, z) {
		t.Fatal("captured hardlink identity lost")
	}
	if !slices.ContainsFunc(entries, func(e BackupManifestFile) bool { return e.BundlePath == "data/tree/empty" && e.Type == "directory" }) {
		t.Fatal("empty directory absent from manifest")
	}
	root := entries[0]
	if root.Mode != "2770" {
		t.Fatal("setgid directory metadata lost", root.Mode)
	}
}

func TestFilesystemCaptureRecordsPOSIXACLWithoutApplyingItToPayload(t *testing.T) {
	b, source, target := filesystemFixture(t, 100)
	path := filepath.Join(source, "file")
	if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	// Linux POSIX ACL version 2: owner, named user, owning group, mask, other.
	acl := make([]byte, 4+5*8)
	binary.LittleEndian.PutUint32(acl, 2)
	tags := []uint16{1, 2, 4, 16, 32}
	permissions := []uint16{6, 4, 0, 4, 0}
	for n, tag := range tags {
		offset := 4 + n*8
		binary.LittleEndian.PutUint16(acl[offset:], tag)
		binary.LittleEndian.PutUint16(acl[offset+2:], permissions[n])
		id := uint32(0xffffffff)
		if tag == 2 {
			id = uint32(os.Getuid())
		}
		binary.LittleEndian.PutUint32(acl[offset+4:], id)
	}
	if err := syscall.Setxattr(path, "system.posix_acl_access", acl, 0); err != nil {
		t.Fatal("temporary filesystem POSIX ACL setup failed", err)
	}
	entry, err := b.capture("file", "data/file", "file")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(entry.Xattrs["system.posix_acl_access"], acl) {
		t.Fatal("POSIX ACL not recorded")
	}
	if _, err := syscall.Getxattr(filepath.Join(target, "data/file"), "system.posix_acl_access", nil); !errors.Is(err, syscall.ENODATA) {
		t.Fatal("source ACL applied to private payload", err)
	}
}

func TestFilesystemBudgetIsSharedAcrossDeclaredInputs(t *testing.T) {
	b, source, _ := filesystemFixture(t, 4)
	for _, dir := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(source, dir), 0700); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"a", "b"} {
			if err := os.WriteFile(filepath.Join(source, dir, file), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := b.capture("one", "data/one", "directory"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.capture("two", "data/two", "directory"); err == nil {
		t.Fatal("entry budget reset for second input")
	}
	if len(b.entries) > 4 {
		t.Fatal("entry bound exceeded")
	}
}

func TestFilesystemCaptureUsesHeldRootAfterSourcePathReplacement(t *testing.T) {
	b, source, target := filesystemFixture(t, 10)
	if err := os.Mkdir(filepath.Join(source, "tree"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "tree", "data"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "tree"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "tree", "data"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, source+".held"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(source); os.Rename(source+".held", source) })
	if err := os.Symlink(outside, source); err != nil {
		t.Fatal(err)
	}
	if _, err := b.capture("tree", "data/tree", "directory"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "data/tree/data"))
	if err != nil || string(got) != "owned" {
		t.Fatal("source root replacement escaped held root", string(got), err)
	}
}

func TestFilesystemCaptureRejectsEscapingLinksSpecialFilesAndInvalidNames(t *testing.T) {
	for _, scenario := range []string{"escaping link", "absolute link", "directory symlink", "fifo", "invalid UTF8"} {
		t.Run(scenario, func(t *testing.T) {
			b, source, _ := filesystemFixture(t, 100)
			tree := filepath.Join(source, "tree")
			if err := os.Mkdir(tree, 0700); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "escaping link":
				if err := os.Symlink("../outside", filepath.Join(tree, "link")); err != nil {
					t.Fatal(err)
				}
			case "absolute link":
				if err := os.Symlink(t.TempDir(), filepath.Join(tree, "link")); err != nil {
					t.Fatal(err)
				}
			case "directory symlink":
				if err := os.Remove(tree); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), tree); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := syscall.Mkfifo(filepath.Join(tree, "fifo"), 0600); err != nil {
					t.Fatal(err)
				}
			case "invalid UTF8":
				if err := os.WriteFile(filepath.Join(tree, string([]byte{255})), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := b.capture("tree", "data/tree", "directory"); err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestFilesystemCaptureRecordsUnselectedHardlinkAliases(t *testing.T) {
	b, source, _ := filesystemFixture(t, 10)
	if err := os.WriteFile(filepath.Join(source, "selected"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(source, "selected"), filepath.Join(source, "outside-selection")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.capture("selected", "data/selected", "file"); err != nil {
		t.Fatal(err)
	}
	entry := b.finish()[0]
	if entry.LinkCount != 2 || entry.CapturedLinks != 1 {
		t.Fatal("unselected alias count omitted", entry)
	}
}

func TestFilesystemCaptureRejectsUnexpectedMountIdentityBeforeReadingData(t *testing.T) {
	b, source, target := filesystemFixture(t, 10)
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := b.source.Lstat("file")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.file(b.source, "file", "file", info, "not-the-selected-mount"); err == nil {
		t.Fatal("mount boundary ignored")
	}
	if _, err := os.Stat(filepath.Join(target, "file")); !os.IsNotExist(err) {
		t.Fatal("unexpected mount data was copied", err)
	}
}

func TestFilesystemCaptureSupportsJSONEscapedMemberNames(t *testing.T) {
	b, source, target := filesystemFixture(t, 10)
	if err := os.Mkdir(filepath.Join(source, "tree"), 0700); err != nil {
		t.Fatal(err)
	}
	name := "line\nbreak"
	if err := os.WriteFile(filepath.Join(source, "tree", name), []byte("value"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.capture("tree", "data/tree", "directory"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "data/tree", name))
	if err != nil || string(got) != "value" {
		t.Fatal("member name changed", err)
	}
}

func TestFilesystemCaptureMetadataBudgetAndCancellation(t *testing.T) {
	b, source, _ := filesystemFixture(t, 10)
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("value"), 0600); err != nil {
		t.Fatal(err)
	}
	b.metadataBytes = maxBackupMetadataBytes
	if _, err := b.capture("file", "data/file", "file"); err == nil {
		t.Fatal("metadata budget bypassed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.ctx = ctx
	if _, err := b.capture("file", "data/other", "file"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
}

func TestFilesystemMetadataStaysInsidePrivateBundle(t *testing.T) {
	coordinator, adapter, source := setupBackupFixture(t)
	adapter.activeStates = map[string]bool{}
	secret := []byte("PRIVATE_ATTRIBUTE_VALUE_SENTINEL")
	if err := syscall.Setxattr(filepath.Join(source, "data/documents/notes.txt"), "user.server_man_private", secret, 0); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "bundle")
	summary, err := coordinator.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coordinator), Destination: destination}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	public, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(public, secret) || bytes.Contains(public, []byte("xattrs")) {
		t.Fatal("private attribute metadata leaked into routine result")
	}
	raw, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Principal.UIDMap == "" || manifest.Principal.GIDMap == "" {
		t.Fatal("capture namespace mappings omitted")
	}
	if !slices.ContainsFunc(manifest.Files, func(entry BackupManifestFile) bool {
		return bytes.Equal(entry.Xattrs["user.server_man_private"], secret)
	}) {
		t.Fatal("private attribute missing from bundle")
	}
	if summary.TotalEntries != len(manifest.Files)+len(manifest.NativeExports)+1 {
		t.Fatal("entry count omitted directory/link records")
	}
	info, err := os.Stat(filepath.Join(destination, "manifest.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("manifest is not private", err)
	}
}
