package declarative

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func manifestFixture() BackupManifest {
	return BackupManifest{SourceRoot: "/source", Version: 1, Stage: "captured", Revision: digest([]byte("config")), CreatedAt: time.Now().UTC(), Files: []BackupManifestFile{{
		Type: "file", BundlePath: "server-man.yaml", SizeBytes: 6, SHA256: digest([]byte("config")), Mode: "0600", MTime: time.Now().UTC().Format(time.RFC3339Nano), ATime: time.Now().UTC().Format(time.RFC3339Nano), LinkCount: 1, CapturedLinks: 1, XattrCoverage: "visible",
	}}}
}

func TestBackupManifestRejectsInvalidGraphs(t *testing.T) {
	for name, mutate := range map[string]func(*BackupManifest){
		"version":                       func(m *BackupManifest) { m.Version++ },
		"missing source root":           func(m *BackupManifest) { m.SourceRoot = "" },
		"relative source root":          func(m *BackupManifest) { m.SourceRoot = "relative" },
		"unclean source root":           func(m *BackupManifest) { m.SourceRoot = "/source/../other" },
		"invalid source root":           func(m *BackupManifest) { m.SourceRoot = "/source\x00" },
		"prepared without verification": func(m *BackupManifest) { m.Stage = "prepared" },
		"revision":                      func(m *BackupManifest) { m.Revision = digest([]byte("wrong")) },
		"duplicate":                     func(m *BackupManifest) { m.Files = append(m.Files, m.Files[0]) },
		"traversal":                     func(m *BackupManifest) { m.Files[0].BundlePath = "../private" },
		"reserved":                      func(m *BackupManifest) { m.Files[0].BundlePath = "native/payload" },
		"metadata path":                 func(m *BackupManifest) { m.Files[0].BundlePath = "manifest.json" },
		"metadata descendant":           func(m *BackupManifest) { m.Files[0].BundlePath = "manifest.json/child" },
		"ancestor is file":              func(m *BackupManifest) { f := m.Files[0]; f.BundlePath += "/child"; m.Files = append(m.Files, f) },
		"invalid type":                  func(m *BackupManifest) { m.Files[0].Type = "device" },
		"mode":                          func(m *BackupManifest) { m.Files[0].Mode = "8888" },
		"negative size":                 func(m *BackupManifest) { m.Files[0].SizeBytes = -1 },
		"bad timestamp":                 func(m *BackupManifest) { m.Files[0].ATime = "bad" },
		"missing link target": func(m *BackupManifest) {
			f := m.Files[0]
			f.BundlePath = "data/hard"
			f.Type = "hardlink"
			f.HardlinkTo = "data/missing"
			m.Files = append(m.Files, f)
		},
		"link count":         func(m *BackupManifest) { m.Files[0].CapturedLinks = 2 },
		"attribute coverage": func(m *BackupManifest) { m.Files[0].XattrCoverage = "complete" },
		"attribute bound": func(m *BackupManifest) {
			m.Files[0].Xattrs = map[string][]byte{"user.private": make([]byte, maxBackupXattrBytes+1)}
		},
		"escaping symlink": func(m *BackupManifest) {
			f := m.Files[0]
			f.Type = "symlink"
			f.BundlePath = "link"
			f.SizeBytes = 0
			f.SHA256 = ""
			f.CapturedLinks = 0
			f.LinkTarget = "../outside"
			m.Files = append(m.Files, f)
		},
		"native identity": func(m *BackupManifest) {
			m.NativeExports = []BackupManifestNativeExport{{BundlePath: "native/x", SHA256: m.Revision, Kind: "export", Backend: "incus", Resources: []string{"service/a"}, Native: &NativeBackupObject{Backend: "libvirt", Name: "sm-a"}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := manifestFixture()
			mutate(&m)
			if err := validateBackupManifest(context.Background(), m); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

func TestBackupManifestHardlinkMetadataAndCounts(t *testing.T) {
	m := manifestFixture()
	m.Files[0].LinkCount, m.Files[0].CapturedLinks = 2, 2
	f := m.Files[0]
	f.Type, f.BundlePath, f.HardlinkTo = "hardlink", "data/alias", "server-man.yaml"
	m.Files = append(m.Files, f)
	if err := validateBackupManifest(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	m.Files[1].UID++
	if err := validateBackupManifest(context.Background(), m); err == nil {
		t.Fatal("accepted divergent inode metadata")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateBackupManifest(ctx, manifestFixture()); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestReadBackupManifestStrictPrivateMetadata(t *testing.T) {
	for _, name := range []string{"valid", "unknown", "duplicate", "nested duplicate", "trailing", "symlink", "public", "oversized", "invalid utf8"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			m := manifestFixture()
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "unknown":
				raw = append([]byte(`{"privateSecret":"do-not-echo",`), raw[1:]...)
			case "duplicate":
				raw = append([]byte(`{"version":1,`), raw[1:]...)
			case "nested duplicate":
				raw = []byte(strings.Replace(string(raw), `"uid":0`, `"uid":0,"uid":0`, 1))
			case "trailing":
				raw = append(raw, []byte(` {}`)...)
			case "invalid utf8":
				raw = append([]byte(`{"bad":"`+string([]byte{255})+`",`), raw[1:]...)
			}
			path := filepath.Join(dir, "manifest.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if name == "public" {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if name == "symlink" {
				if err := os.Rename(path, filepath.Join(dir, "real")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("real", path); err != nil {
					t.Fatal(err)
				}
			}
			if name == "oversized" {
				if err := os.Truncate(path, backupMetadataReserve+1); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			got, checksum, err := readBackupManifest(context.Background(), root)
			if name == "valid" {
				if err != nil || got.Revision != m.Revision || checksum != digest(raw) {
					t.Fatalf("valid: checksum=%s error=%v", checksum, err)
				}
			} else if err == nil || checksum != "" || got.Version != 0 || strings.Contains(err.Error(), "do-not-echo") {
				t.Fatalf("unsafe metadata accepted or leaked: checksum=%q error=%v", checksum, err)
			}
		})
	}
}
