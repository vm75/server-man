package declarative

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMountInfoReadOnlyAndVisibleOvermount(t *testing.T) {
	const inventory = "" +
		"24 1 8:1 / / rw - ext4 /dev/root rw\n" +
		"25 24 8:2 / /data rw - ext4 /dev/data ro\n" +
		"26 25 8:3 / /data/writable rw - ext4 /dev/other rw\n" +
		"27 26 8:4 / /data/writable ro - ext4 /dev/overlay rw\n"
	mounts, err := ParseMountInfo(strings.NewReader(inventory))
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		path, source string
		readOnly     bool
	}{
		{"/", "/dev/root", false},
		{"/data/item", "/dev/data", true}, // superblock ro despite rw mount
		{"/data/writable/item", "/dev/overlay", true}, // mount ro despite rw superblock
		{"/data-other/item", "/dev/root", false}, // component boundary
	} {
		m, ok := mountedFilesystem(check.path, mounts)
		if !ok || m.Source != check.source || m.ReadOnly != check.readOnly {
			t.Fatalf("%s: got %+v, found %v", check.path, m, ok)
		}
	}
	// An overmount at the same mountpoint replaces the previously visible one.
	mounts = append(mounts, MountInfo{Point: "/data/writable", Source: "/dev/replacement"})
	if m, ok := mountedFilesystem("/data/writable/item", mounts); !ok || m.Source != "/dev/replacement" || m.ReadOnly {
		t.Fatalf("last overmount was ignored: %+v, found %v", m, ok)
	}
	for _, invalid := range []string{
		"24 1 8:1 / / rw - ext4 /dev/root\n", // missing superblock options
		"bad\n",
	} {
		if _, err := ParseMountInfo(strings.NewReader(invalid)); err == nil {
			t.Fatal("accepted invalid mountinfo")
		}
	}
}

func TestWritableMountPortabilityIssuesDoNotMutateData(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "existing")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(data)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		name string
		mounts []MountInfo
		inventoryErr error
		code, severity string
	}{
		{"writable", []MountInfo{{Point: root, Source: "/dev/test", ReadOnly: false}}, nil, "", ""},
		{"read-only", []MountInfo{{Point: root, Source: "/dev/test", ReadOnly: true}}, nil, "read_only_mount", "error"},
		{"no-visible-mount", nil, nil, "mount_state_unavailable", "unknown"},
		{"unreadable-inventory", nil, errors.New("denied"), "mount_state_unavailable", "unknown"},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			issue := writableMountIssue("storage/media", data, f.mounts, f.inventoryErr)
			if f.code == "" && issue != nil {
				t.Fatalf("writable mount blocked: %+v", issue)
			}
			if f.code != "" && (issue == nil || issue.Code != f.code || issue.Severity != f.severity || issue.Resource != "storage/media") {
				t.Fatalf("unexpected diagnostics: %+v", issue)
			}
		})
	}
	// A private portable symlink still resolves to the actual mounted filesystem.
	link := filepath.Join(root, "linked")
	if err := os.Symlink(data, link); err != nil {
		t.Fatal(err)
	}
	if issue := writableMountIssue("storage/media", link, []MountInfo{{Point: data, ReadOnly: true}}, nil); issue == nil || issue.Code != "read_only_mount" {
		t.Fatalf("symlink target mount not checked: %+v", issue)
	}
	after, err := os.Stat(data)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("read-only preflight mutated stored data", err)
	}
}
