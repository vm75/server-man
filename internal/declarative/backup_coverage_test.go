package declarative

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func rewriteCoverageFixture(t *testing.T, dir string, mutate func(*Config, *BackupManifest)) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "server-man.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m BackupManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	mutate(&cfg, &m)
	// Deliberately permit invalid configuration here: the verifier must reject
	// it even with a matching file hash and reviewed manifest checksum.
	raw, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server-man.yaml"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	m.Revision = digest(raw)
	m.Files[0].SHA256, m.Files[0].SizeBytes = m.Revision, int64(len(raw))
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return digest(raw)
}

func coverageAddFile(t *testing.T, dir, path, contents string, m *BackupManifest) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0700); err != nil {
		t.Fatal(err)
	}
	mustBackupWrite(t, filepath.Join(dir, path), contents)
	f := m.Files[0]
	f.BundlePath, f.SizeBytes, f.SHA256 = path, int64(len(contents)), digest([]byte(contents))
	m.Files = append(m.Files, f)
}

func coverageAddVolume(t *testing.T, dir string, m *BackupManifest) {
	t.Helper()
	path := "native/incus/volume.tar"
	mustBackupWrite(t, filepath.Join(dir, path), "volume")
	m.NativeExports = append(m.NativeExports, BackupManifestNativeExport{Backend: "incus", Kind: "incus_custom_volume", BundlePath: path, SizeBytes: 6, SHA256: digest([]byte("volume")), Resources: []string{"service/example"}, Native: &NativeBackupObject{Backend: "incus", Name: "volume", Project: "default", Pool: "pool", ContentType: "filesystem"}})
}

func TestBackupCoverageRejectsSelfConsistentIncompleteBundles(t *testing.T) {
	for _, scenario := range []string{"valid", "nested data", "volume", "overlapping storage link", "link outside data", "missing root record", "missing secret", "wrong storage type", "external storage", "extra data", "missing asset", "asset checksum", "private mode", "missing guest export", "guest name", "guest pool", "guest project", "export kind", "unknown consumer", "duplicate export", "volume consumer", "volume identity", "invalid config", "running identity", "resume identity", "resume missing", "resume overlap"} {
		t.Run(scenario, func(t *testing.T) {
			dir, _ := payloadFixture(t)
			checksum := rewriteCoverageFixture(t, dir, func(cfg *Config, m *BackupManifest) {
				switch scenario {
				case "nested data":
					coverageAddFile(t, dir, "data/nested", "included", m)
				case "volume":
					coverageAddVolume(t, dir, m)
				case "missing root record":
					m.Files = slices.DeleteFunc(m.Files, func(f BackupManifestFile) bool { return f.BundlePath == "data" })
				case "missing secret":
					cfg.Secrets = map[string]string{"missing": "secrets/missing"}
				case "wrong storage type":
					cfg.Storage["files"] = Storage{Kind: "file", Path: "data"}
				case "external storage":
					cfg.Storage["external"] = Storage{Path: "/missing/external", External: true}
				case "extra data":
					coverageAddFile(t, dir, "unreferenced", "private data", m)
				case "missing asset", "asset checksum":
					cfg.Storage["disk"] = Storage{Kind: "file", Format: "raw", Path: "data/disk.raw", Source: &DiskSource{Path: "assets/base.raw", Format: "raw", SHA256: digest([]byte("expected asset"))}}
					coverageAddFile(t, dir, "data/disk.raw", "disk", m)
					if scenario == "asset checksum" {
						coverageAddFile(t, dir, "assets/base.raw", "wrong asset", m)
					}
				case "private mode":
					cfg.Secrets = map[string]string{"private": "data/alias"}
					for index := range m.Files {
						if m.Files[index].BundlePath == "data/alias" || m.Files[index].BundlePath == "data/file\nname" {
							m.Files[index].Mode = "0644"
						}
					}
				case "link outside data":
					path := filepath.Join(dir, "data/link")
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("../server-man.yaml", path); err != nil {
						t.Fatal(err)
					}
					for index := range m.Files {
						if m.Files[index].BundlePath == "data/link" {
							m.Files[index].LinkTarget = "../server-man.yaml"
						}
					}
				case "overlapping storage link":
					cfg.Storage["sub"] = Storage{Path: "data/sub"}
					if err := os.Mkdir(filepath.Join(dir, "data/sub"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("../alias", filepath.Join(dir, "data/sub/link")); err != nil {
						t.Fatal(err)
					}
					for _, entry := range slices.Clone(m.Files) {
						if entry.BundlePath == "data" {
							entry.BundlePath = "data/sub"
							m.Files = append(m.Files, entry)
						}
						if entry.BundlePath == "data/link" {
							entry.BundlePath, entry.LinkTarget = "data/sub/link", "../alias"
							m.Files = append(m.Files, entry)
						}
					}
				case "missing guest export":
					m.NativeExports = nil
					if err := os.RemoveAll(filepath.Join(dir, "native")); err != nil {
						t.Fatal(err)
					}
				case "guest name":
					m.NativeExports[0].Native.Name = "sm-other"
				case "guest pool":
					m.NativeExports[0].Native.Pool = "other"
				case "guest project":
					m.NativeExports[0].Native.Project = "other"
				case "export kind":
					m.NativeExports[0].Kind = "unknown"
				case "unknown consumer":
					m.NativeExports[0].Resources = []string{"service/missing"}
				case "duplicate export":
					export := m.NativeExports[0]
					export.BundlePath = "native/incus/duplicate.tar"
					mustBackupWrite(t, filepath.Join(dir, export.BundlePath), "private native export")
					m.NativeExports = append(m.NativeExports, export)
				case "volume consumer":
					coverageAddVolume(t, dir, m)
					m.NativeExports[1].Resources = []string{"storage/files"}
				case "volume identity":
					coverageAddVolume(t, dir, m)
					m.NativeExports[1].Native.ContentType = "unknown"
				case "invalid config":
					cfg.Version = 2
				case "running identity":
					m.RunningWorkloadsAtBackup = []string{"missing"}
				case "resume identity":
					m.ResumedWorkloads = []string{"example"}
				case "resume missing":
					m.RunningWorkloadsAtBackup = []string{"example"}
				case "resume overlap":
					m.RunningWorkloadsAtBackup = []string{"example"}
					m.ResumedWorkloads = []string{"example"}
					m.FailedResume = []string{"example"}
				}
			})
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			m, err := verifyBackupBundle(context.Background(), root, checksum)
			valid := scenario == "valid" || scenario == "nested data" || scenario == "volume" || scenario == "overlapping storage link"
			if valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || m.Version != 0 || strings.Contains(err.Error(), "private data") {
				t.Fatal("incomplete or unrelated data accepted", err)
			}
		})
	}
}

func TestLibvirtBackupCoverageRequiresExactGuestIdentity(t *testing.T) {
	cfg := Config{Services: map[string]Service{"guest": {Kind: "vm", Backend: "libvirt", VM: &VM{}}}}
	export := BackupManifestNativeExport{Backend: "libvirt", Kind: "libvirt_guest_state", Resources: []string{"service/guest"}, Native: &NativeBackupObject{Backend: "libvirt", Name: vmDomainName("guest")}}
	if err := validateBackupNativeCoverage(context.Background(), cfg, []BackupManifestNativeExport{export}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"backend", "name", "scope", "resources"} {
		t.Run(field, func(t *testing.T) {
			n := export
			object := *export.Native
			n.Native = &object
			switch field {
			case "backend":
				n.Backend = "incus"
			case "name":
				n.Native.Name = "other"
			case "scope":
				n.Native.Project = "unexpected"
			case "resources":
				n.Resources = []string{"service/guest", "service/other"}
			}
			if err := validateBackupNativeCoverage(context.Background(), cfg, []BackupManifestNativeExport{n}); err == nil {
				t.Fatal("incorrect libvirt identity accepted")
			}
		})
	}
}
