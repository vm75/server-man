package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testAdminHash() string {
	return "pbkdf2-sha256$600000$" +
		base64.RawStdEncoding.EncodeToString(make([]byte, 16)) + "$" +
		base64.RawStdEncoding.EncodeToString(make([]byte, 32))
}

func TestAdminSettingsArePairedAndPrivate(t *testing.T) {
	cfg, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	cfg.AdminUsername = "root"
	if err := cfg.Validate(); err == nil {
		t.Fatal("username without a password hash accepted")
	}
	cfg.AdminPasswordHash = "plaintext"
	if err := cfg.Validate(); err == nil {
		t.Fatal("raw password accepted as a hash")
	}
	cfg.AdminPasswordHash = testAdminHash()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.AdminUsername = "not valid!"
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid username accepted")
	}
}

func TestSetupAdminPreservesWorkloadsAndIsOneTime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "server-man.yaml")
	original := "version: 1\nserver:\n  listen: 127.0.0.1:8181\n  allow_writes: true\nservices:\n  example:\n    enabled: false\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetupAdmin(path, "admin", testAdminHash()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "example:") || !strings.Contains(string(data), "allow_writes: true") {
		t.Fatal("unrelated YAML was lost")
	}
	cfg, err := Load(path)
	if err != nil || cfg.AdminUsername != "admin" || cfg.AdminPasswordHash != testAdminHash() || cfg.Listen != "127.0.0.1:8181" {
		t.Fatalf("admin credentials not saved: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private configuration was not preserved", err)
	}
	if err := SetupAdmin(path, "other", testAdminHash()); err != ErrAdminConfigured {
		t.Fatalf("second administrator setup accepted: %v", err)
	}
}

func TestSetupAdminAbsentAndEmptyFields(t *testing.T) {
	for _, body := range []string{"", "version: 1\nserver:\n  admin_username: ''\n  admin_password_hash: ''\n"} {
		path := filepath.Join(t.TempDir(), "private", "server-man.yaml")
		if body != "" {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil { t.Fatal(err) }
			if err := os.WriteFile(path, []byte(body), 0600); err != nil { t.Fatal(err) }
		}
		if err := SetupAdmin(path, "admin", testAdminHash()); err != nil {
			t.Fatalf("setup with %q: %v", body, err)
		}
		loaded, err := Load(path)
		if err != nil || loaded.AdminUsername != "admin" {
			t.Fatal("admin setup invalid", err)
		}
	}
}

func TestSetupAdminRejectsUnsafeFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("version: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SetupAdmin(link, "admin", testAdminHash()); err == nil {
		t.Fatal("symlink configuration accepted")
	}
	if err := os.Chmod(target, 0644); err != nil {
		t.Fatal(err)
	}
	if err := SetupAdmin(target, "admin", testAdminHash()); err == nil {
		t.Fatal("insecure configuration accepted")
	}
}
