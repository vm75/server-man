package declarative

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func payloadFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"data/empty", "native/incus"} {
		if err := os.MkdirAll(filepath.Join(dir, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"server-man.yaml": "config", "data/file\nname": "private payload", "native/incus/export.tar": "private native export"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(dir, "data/file\nname"), filepath.Join(dir, "data/alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file\nname", filepath.Join(dir, "data/link")); err != nil {
		t.Fatal(err)
	}
	m := manifestFixture()
	f := m.Files[0]
	f.BundlePath, f.SizeBytes, f.SHA256, f.LinkCount, f.CapturedLinks = "data/file\nname", int64(len("private payload")), digest([]byte("private payload")), 2, 2
	m.Files = append(m.Files, f)
	f.Type, f.BundlePath, f.HardlinkTo = "hardlink", "data/alias", f.BundlePath
	m.Files = append(m.Files, f)
	f = m.Files[0]
	f.Type, f.BundlePath, f.SizeBytes, f.SHA256, f.CapturedLinks = "directory", "data/empty", 0, "", 0
	m.Files = append(m.Files, f)
	f.Type, f.BundlePath, f.LinkTarget = "symlink", "data/link", "file\nname"
	m.Files = append(m.Files, f)
	m.NativeExports = []BackupManifestNativeExport{{Backend: "incus", Kind: "incus_vm", BundlePath: "native/incus/export.tar", SizeBytes: int64(len("private native export")), SHA256: digest([]byte("private native export")), Native: &NativeBackupObject{Backend: "incus", Name: "sm-example", Project: "default"}, Resources: []string{"service/example"}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, digest(raw)
}

func TestBackupPayloadVerification(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string){
		"valid": func(*testing.T, string) {},
		"content": func(t *testing.T, dir string) {
			mustBackupWrite(t, filepath.Join(dir, "data/file\nname"), "changed payload")
		},
		"missing": func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "data/link")); err != nil {
				t.Fatal(err)
			}
		},
		"unexpected": func(t *testing.T, dir string) {
			mustBackupWrite(t, filepath.Join(dir, "data/unlisted"), "private secret")
		},
		"native bytes": func(t *testing.T, dir string) {
			mustBackupWrite(t, filepath.Join(dir, "native/incus/export.tar"), "changed native export")
		},
		"public payload": func(t *testing.T, dir string) {
			if err := os.Chmod(filepath.Join(dir, "data/file\nname"), 0644); err != nil {
				t.Fatal(err)
			}
		},
		"public root": func(t *testing.T, dir string) {
			if err := os.Chmod(dir, 0755); err != nil {
				t.Fatal(err)
			}
		},
		"public parent": func(t *testing.T, dir string) {
			if err := os.Chmod(filepath.Join(dir, "native"), 0755); err != nil {
				t.Fatal(err)
			}
		},
		"changed link": func(t *testing.T, dir string) {
			path := filepath.Join(dir, "data/link")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../server-man.yaml", path); err != nil {
				t.Fatal(err)
			}
		},
		"parent symlink": func(t *testing.T, dir string) {
			if err := os.Rename(filepath.Join(dir, "data"), filepath.Join(dir, "other")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("other", filepath.Join(dir, "data")); err != nil {
				t.Fatal(err)
			}
		},
		"leaf symlink": func(t *testing.T, dir string) {
			path := filepath.Join(dir, "native/incus/export.tar")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../server-man.yaml", path); err != nil {
				t.Fatal(err)
			}
		},
		"copied hardlink": func(t *testing.T, dir string) {
			path := filepath.Join(dir, "data/alias")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			mustBackupWrite(t, path, "private payload")
		},
		"external alias": func(t *testing.T, dir string) {
			if err := os.Link(filepath.Join(dir, "data/file\nname"), filepath.Join(t.TempDir(), "outside")); err != nil {
				t.Fatal(err)
			}
		},
		"special file": func(t *testing.T, dir string) {
			path := filepath.Join(dir, "native/incus/export.tar")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
		},
		"manifest changed": func(t *testing.T, dir string) {
			path := filepath.Join(dir, "manifest.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, checksum := payloadFixture(t)
			mutate(t, dir)
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			m, err := verifyBackupBundle(context.Background(), root, checksum)
			if name == "valid" {
				if err != nil || m.Revision != digest([]byte("config")) {
					t.Fatalf("valid bundle: %v", err)
				}
			} else if err == nil || m.Version != 0 || strings.Contains(err.Error(), "private payload") || strings.Contains(err.Error(), "private secret") {
				t.Fatalf("tampering accepted or data leaked: %v", err)
			}
		})
	}
}

func mustBackupWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

type backupHashContext struct {
	context.Context
	onRead func()
	skip   int
}

func (c *backupHashContext) Err() error {
	if c.skip > 0 {
		c.skip--
		return c.Context.Err()
	}
	if c.onRead != nil {
		action := c.onRead
		c.onRead = nil
		action()
	}
	return c.Context.Err()
}

func TestBackupHashRejectsConcurrentGrowth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	mustBackupWrite(t, path, "first bytes")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx := &backupHashContext{Context: context.Background(), skip: 1, onRead: func() {
		writer, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		if _, err := writer.WriteString(strings.Repeat("growth", 65536)); err != nil {
			t.Fatal(err)
		}
	}}
	if _, err := hashBackupFile(ctx, f); err == nil {
		t.Fatal("concurrent growth was accepted")
	}
}

func TestBackupVerifyCancellationAndReview(t *testing.T) {
	dir, checksum := payloadFixture(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifyBackupBundle(ctx, root, checksum); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
	for _, checksum := range []string{"", digest([]byte("stale"))} {
		if _, err := verifyBackupBundle(context.Background(), root, checksum); err == nil {
			t.Fatal("unreviewed manifest accepted")
		}
	}
}

func TestBackupVerifyUsesHeldBundleRoot(t *testing.T) {
	dir, checksum := payloadFixture(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	moved := dir + "-held"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	mustBackupWrite(t, filepath.Join(dir, "manifest.json"), "replacement")
	if _, err := verifyBackupBundle(context.Background(), root, checksum); err != nil {
		t.Fatalf("held root followed a replacement pathname: %v", err)
	}
}
