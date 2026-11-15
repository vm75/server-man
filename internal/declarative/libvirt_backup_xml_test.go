package declarative

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLibvirtBackupXMLChecksPrivateContents(t *testing.T) {
	l, cfg, _ := libvirtFixture(t)
	raw, err := RenderLibvirt(l.Store, cfg, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	base := string(raw)
	for name, change := range map[string]func(string) string{
		"valid": func(s string) string { return s },
		"native hardware normalization": func(s string) string {
			old := `<memory unit="MiB">` + strconv.Itoa(cfg.Services["guest"].VM.MemoryMiB) + `</memory>`
			converted := `<memory unit="KiB">` + strconv.Itoa(cfg.Services["guest"].VM.MemoryMiB*1024) + `</memory>`
			return strings.Replace(strings.Replace(s, old, converted, 1), `machine="q35"`, `machine="pc-q35-10.0"`, 1)
		},
		"native defaults": func(s string) string {
			return strings.Replace(s, "</devices>", "<controller type='pci' index='0'/><controller type='pci' index='1'/><input type='mouse'/><input type='keyboard'/></devices>", 1)
		},
		"empty backing": func(s string) string { return strings.Replace(s, "</disk>", "<backingStore/></disk>", 1) },
		"foreign uuid": func(s string) string {
			return strings.Replace(s, cfg.Services["guest"].VM.UUID, "00000000-0000-4000-8000-000000000000", 1)
		},
		"name": func(s string) string { return strings.Replace(s, "<name>sm-guest</name>", "<name>other</name>", 1) },
		"disk path": func(s string) string {
			return strings.Replace(s, l.Store.Resolve(cfg.Storage["root"].Path), "/private/missing/disk", 1)
		},
		"fingerprint": func(s string) string {
			return strings.Replace(s, resourceFingerprint(cfg, "service/guest"), strings.Repeat("b", 64), 1)
		},
		"duplicate name": func(s string) string { return strings.Replace(s, "</name>", "</name><name>sm-guest</name>", 1) },
		"duplicate uuid": func(s string) string {
			return strings.Replace(s, "</uuid>", "</uuid><uuid>"+cfg.Services["guest"].VM.UUID+"</uuid>", 1)
		},
		"duplicate interface": func(s string) string {
			a := strings.Index(s, "<interface")
			b := strings.Index(s, "</interface>") + len("</interface>")
			return strings.Replace(s, "</devices>", s[a:b]+"</devices>", 1)
		},
		"duplicate attributes": func(s string) string {
			return strings.Replace(s, `<domain type="kvm">`, `<domain type="kvm" type="kvm">`, 1)
		},
		"disk fdgroup": func(s string) string {
			return strings.Replace(s, "<source file=", "<source fdgroup='private' file=", 1)
		},
		"qualified source attribute": func(s string) string {
			return strings.Replace(s, "<source file=", "<source xmlns:x='foreign' x:file='/private/other' file=", 1)
		},
		"disk data store": func(s string) string {
			return strings.Replace(s, "</disk>", "<source file='/private/disk'><dataStore><source file='/private/data'/></dataStore></source></disk>", 1)
		},
		"backing chain": func(s string) string {
			return strings.Replace(s, "</disk>", "<backingStore type='file'><source file='/private/base'/></backingStore></disk>", 1)
		},
		"encrypted disk": func(s string) string { return strings.Replace(s, "</disk>", "<encryption format='luks'/></disk>", 1) },
		"nvram":          func(s string) string { return strings.Replace(s, "</os>", "<nvram>/private/nvram</nvram></os>", 1) },
		"kernel":         func(s string) string { return strings.Replace(s, "</os>", "<kernel>/private/kernel</kernel></os>", 1) },
		"tpm": func(s string) string {
			return strings.Replace(s, "</devices>", "<tpm><backend type='emulator'/></tpm></devices>", 1)
		},
		"host device": func(s string) string {
			return strings.Replace(s, "</devices>", "<hostdev mode='subsystem'/></devices>", 1)
		},
		"qemu passthrough": func(s string) string {
			return strings.Replace(s, "</domain>", "<qemu:commandline xmlns:qemu='http://libvirt.org/schemas/domain/qemu/1.0'><qemu:arg value='PRIVATE_XML_SENTINEL'/></qemu:commandline></domain>", 1)
		},
		"namespace override": func(s string) string { return strings.Replace(s, "<uuid>", "<uuid xmlns='foreign'>", 1) },
		"doctype": func(s string) string {
			return "<!DOCTYPE domain [<!ENTITY private SYSTEM 'file:///private/never-read'>]>" + s
		},
		"trailing root": func(s string) string { return s + "<domain/>" },
		"trailing data": func(s string) string { return s + "PRIVATE_XML_SENTINEL" },
		"oversized":     func(s string) string { return s + strings.Repeat(" ", MaxConfigBytes) },
		"excessive token work": func(s string) string {
			return strings.Replace(s, "</metadata>", "<list>"+strings.Repeat("<x/>", 33000)+"</list></metadata>", 1)
		},
		"deep metadata": func(s string) string {
			return strings.Replace(s, "</metadata>", "<x>"+strings.Repeat("<x>", 64)+strings.Repeat("</x>", 64)+"</x></metadata>", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateLibvirtBackupXML(context.Background(), []byte(change(base)), cfg, "guest", filepath.Dir(l.Store.Path))
			valid := name == "valid" || name == "native defaults" || name == "empty backing" || name == "native hardware normalization"
			if valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || strings.Contains(err.Error(), "PRIVATE_XML_SENTINEL") || strings.Contains(err.Error(), "/private/") {
				t.Fatal("unsafe XML accepted or private data leaked", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateLibvirtBackupXML(ctx, raw, cfg, "guest", filepath.Dir(l.Store.Path)); err != context.Canceled {
		t.Fatal("cancellation", err)
	}
	// Interpretation must not access old host paths, even when no such root exists.
	offline := Store{Path: "/unavailable/previous-host/server-man.yaml"}
	raw, err = RenderLibvirt(offline, cfg, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLibvirtBackupXML(context.Background(), raw, cfg, "guest", filepath.Dir(offline.Path)); err != nil {
		t.Fatal("offline paths were accessed", err)
	}
}

func TestLibvirtBundleXMLTamperingFailsAfterRecomputedHashes(t *testing.T) {
	l, cfg, _, _ := libvirtExportFixture(t)
	snapshot, err := l.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	c := Coordinator{Store: l.Store, Adapters: map[string]Adapter{"libvirt": l}}
	destination := filepath.Join(t.TempDir(), "bundle")
	summary, err := c.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: destination}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// Move the bundle and remove access to original source paths.
	source := filepath.Dir(l.Store.Path)
	if err := os.Rename(source, source+"-away"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(source + "-away") })
	root, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := verifyBackupBundle(context.Background(), root, summary.ManifestChecksum); err != nil {
		t.Fatal("valid relocated bundle failed", err)
	}
	raw, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m BackupManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	originalRoot := m.SourceRoot
	m.SourceRoot = "/different-source-root"
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyBackupBundle(context.Background(), root, digest(raw)); err == nil {
		t.Fatal("wrong source interpretation root accepted")
	}
	m.SourceRoot = originalRoot
	export := &m.NativeExports[0]
	raw, err = os.ReadFile(filepath.Join(destination, export.BundlePath))
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "<name>sm-guest</name>", "<name>other</name>", 1))
	if err := os.WriteFile(filepath.Join(destination, export.BundlePath), raw, 0600); err != nil {
		t.Fatal(err)
	}
	export.SizeBytes, export.SHA256 = int64(len(raw)), digest(raw)
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyBackupBundle(context.Background(), root, digest(raw)); err == nil {
		t.Fatal("wrong guest XML accepted with self-consistent hashes")
	}
}

func TestBackupPrivateReaderRejectsParentRetargeting(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "native"), 0700); err != nil {
		t.Fatal(err)
	}
	mustBackupWrite(t, filepath.Join(dir, "native/state"), "private state")
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx := &backupHashContext{Context: context.Background(), skip: 1, onRead: func() {
		if err := os.Rename(filepath.Join(dir, "native"), filepath.Join(dir, "moved")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("moved", filepath.Join(dir, "native")); err != nil {
			t.Fatal(err)
		}
	}}
	if raw, err := readBackupPrivateFile(ctx, root, "native/state", MaxConfigBytes, digest([]byte("private state"))); err == nil || len(raw) != 0 {
		t.Fatal("retargeted private parent accepted", err)
	}
}
