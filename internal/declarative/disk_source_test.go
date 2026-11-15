package declarative

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"server-man/internal/core"
)

func imageFixture(t *testing.T, targetFormat string) (Provisioner, string, string, []byte) {
	t.Helper()
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("native qemu-img unavailable")
	}
	p, revision, path := provisionFixture(t, "file", targetFormat)
	root := filepath.Dir(path)
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("known guest disk contents\n"), 512)
	if err := os.WriteFile(filepath.Join(root, "assets/base.raw"), data, 0600); err != nil {
		t.Fatal(err)
	}
	snap, _ := p.Coordinator.Store.Read()
	st := snap.Config.Storage["new-data"]
	st.Source = &DiskSource{Path: "assets/base.raw", Format: "raw", SHA256: digest(data)}
	snap.Config.Storage["new-data"] = st
	saved, err := p.Coordinator.Store.Save(snap.Config, revision)
	if err != nil {
		t.Fatal(err)
	}
	return p, saved.Revision, path, data
}
func TestNativeImageProvisionCopiesDataWithoutTouchingSource(t *testing.T) {
	for _, sourceFormat := range []string{"raw", "qcow2"} {
		for _, format := range []string{"raw", "qcow2"} {
			t.Run(sourceFormat+"-to-"+format, func(t *testing.T) {
				p, revision, path, data := imageFixture(t, format)
				ctx := context.Background()
				if sourceFormat == "qcow2" {
					sourcePath := filepath.Join(filepath.Dir(path), "assets/base.qcow2")
					if err := p.Runner.Run(ctx, core.Command{Program: p.QemuImg, Args: []string{"convert", "-f", "raw", "-O", "qcow2", filepath.Join(filepath.Dir(path), "assets/base.raw"), sourcePath}}, io.Discard); err != nil {
						t.Fatal(err)
					}
					sourceData, err := os.ReadFile(sourcePath)
					if err != nil {
						t.Fatal(err)
					}
					snap, _ := p.Coordinator.Store.Read()
					st := snap.Config.Storage["new-data"]
					st.Source = &DiskSource{Path: "assets/base.qcow2", Format: "qcow2", SHA256: digest(sourceData)}
					snap.Config.Storage["new-data"] = st
					saved, err := p.Coordinator.Store.Save(snap.Config, revision)
					if err != nil {
						t.Fatal(err)
					}
					revision = saved.Revision
				}

				plan, err := p.Plan(ctx, "new-data", revision, 1)
				if err != nil {
					t.Fatal(err)
				}
				if plan.Source == nil || plan.SourceVirtualBytes != int64(len(data)) {
					t.Fatal(plan)
				}
				if _, err = p.Create(ctx, "new-data", revision, plan.ReviewToken, 1); err != nil {
					t.Fatal(err)
				}
				if format == "qcow2" {
					// Convert only the freshly provisioned temporary disk back to a temporary raw
					// file so this test checks guest-visible bytes across real format conversion.
					rawPath := filepath.Join(filepath.Dir(path), "verified.raw")
					if err = p.Runner.Run(ctx, core.Command{Program: p.QemuImg, Args: []string{"convert", "-f", "qcow2", "-O", "raw", path, rawPath}}, io.Discard); err != nil {
						t.Fatal(err)
					}
					path = rawPath
				}
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				actual := make([]byte, len(data))
				if _, err = io.ReadFull(f, actual); err != nil || !bytes.Equal(actual, data) {
					t.Fatal("guest-visible data differs", err)
				}
				source, err := os.ReadFile(filepath.Join(filepath.Dir(path), "assets/base.raw"))
				if err != nil || !bytes.Equal(source, data) {
					t.Fatal("source asset changed", err)
				}
			})
		}
	}

}
func TestImageProvisionChecksumMismatchLeavesDestinationAbsent(t *testing.T) {
	p, revision, path, _ := imageFixture(t, "raw")
	snap, _ := p.Coordinator.Store.Read()
	st := snap.Config.Storage["new-data"]
	st.Source.SHA256 = strings.Repeat("0", 64)
	snap.Config.Storage["new-data"] = st
	saved, err := p.Coordinator.Store.Save(snap.Config, revision)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), "new-data", saved.Revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Create(context.Background(), "new-data", saved.Revision, plan.ReviewToken, 1); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("bad-checksum destination published", err)
	}
}
func TestQCOW2ReferencesRejectedBeforeNativeInspection(t *testing.T) {
	for _, kind := range []string{"backing-offset", "backing-size", "encryption", "external-data", "unknown-feature", "dirty"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.qcow2")
			header := make([]byte, 104)
			binary.BigEndian.PutUint32(header, 0x514649fb)
			binary.BigEndian.PutUint32(header[4:], 3)
			switch kind {
			case "backing-offset":
				binary.BigEndian.PutUint64(header[8:], 104)
			case "backing-size":
				binary.BigEndian.PutUint32(header[16:], 20)
			case "encryption":
				binary.BigEndian.PutUint32(header[32:], 1)
			case "external-data":
				binary.BigEndian.PutUint64(header[72:], 4)
			case "unknown-feature":
				binary.BigEndian.PutUint64(header[72:], 1<<63)
			case "dirty":
				binary.BigEndian.PutUint64(header[72:], 1)
			}
			if err := os.WriteFile(path, header, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err = checkStandaloneImage(f, "qcow2"); err == nil {
				t.Fatal("unsafe image accepted", kind)
			}
		})
	}
}
func TestInitialSourceOnlyChangesDesiredFingerprintAndMissingAssetWarning(t *testing.T) {
	p, revision, path, _ := imageFixture(t, "raw")
	snap, _ := p.Coordinator.Store.Read()
	snap.Config.Services = map[string]Service{"guest": {Kind: "container", Engine: "podman", Backend: "quadlet", Containers: map[string]Container{"app": {Image: "example/image:1", Mounts: []Mount{{Storage: "new-data", Target: "/data"}}}}}}
	beforeRuntime := runtimeFingerprint(snap.Config, "service/guest")
	beforeDesired := resourceFingerprint(snap.Config, "service/guest")
	st := snap.Config.Storage["new-data"]
	st.Source.SHA256 = strings.Repeat("1", 64)
	snap.Config.Storage["new-data"] = st
	if runtimeFingerprint(snap.Config, "service/guest") != beforeRuntime || resourceFingerprint(snap.Config, "service/guest") == beforeDesired {
		t.Fatal("creation-only source changed runtime or failed to change desired definition")
	}
	if err := os.WriteFile(path, []byte("existing guest data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(path), "assets/base.raw")); err != nil {
		t.Fatal(err)
	}
	h := p.Coordinator.Store.CheckFiles(snap.Config)
	found := false
	for _, issue := range h.Issues {
		if issue.Resource == "service/guest" {
			found = true
			if issue.Severity != "warning" {
				t.Fatal("missing creation asset blocked existing data", issue)
			}
		}
	}
	if !found {
		t.Fatal("missing asset was not reported", h)
	}
	current, _ := p.Coordinator.Store.Read()
	if current.Revision != revision {
		t.Fatal("checking source altered configuration")
	}
}

func TestDiskSourceSchemaRejectsUnpinnedExternalAndStorageAliases(t *testing.T) {
	for _, scenario := range []string{"missing-checksum", "external-source", "unknown-format", "managed-disk-source", "private-source"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := Config{Version: 1, Storage: map[string]Storage{"disk": {Path: "data/guest.qcow2", Kind: "file", Format: "qcow2", Source: &DiskSource{Path: "assets/base.qcow2", Format: "qcow2", SHA256: strings.Repeat("a", 64)}}}}
			st := cfg.Storage["disk"]
			switch scenario {
			case "missing-checksum":
				st.Source.SHA256 = ""
			case "external-source":
				st.Source.Path = "/tmp/base.qcow2"
			case "unknown-format":
				st.Source.Format = "vmdk"
			case "managed-disk-source":
				cfg.Storage["other"] = Storage{Kind: "file", Format: "qcow2", Path: st.Source.Path}
			case "private-source":
				cfg.Secrets = map[string]string{"credentials": st.Source.Path}
			}
			cfg.Storage["disk"] = st
			if err := cfg.Validate(); err == nil {
				t.Fatal("invalid source accepted", scenario)
			}
		})
	}
}
func TestDiskSourceRejectsSymlinkParentsAndHardlinks(t *testing.T) {
	for _, scenario := range []string{"parent-link", "hardlink"} {
		t.Run(scenario, func(t *testing.T) {
			p, revision, path, _ := imageFixture(t, "raw")
			root := filepath.Dir(path)
			if scenario == "parent-link" {
				if err := os.Rename(filepath.Join(root, "assets"), filepath.Join(root, "real-assets")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "real-assets"), filepath.Join(root, "assets")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Link(filepath.Join(root, "assets/base.raw"), filepath.Join(root, "alias")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.Plan(context.Background(), "new-data", revision, 1); err == nil {
				t.Fatal("unsafe source accepted", scenario)
			}
		})
	}
}

type cleanupFailureRunner struct{ parent string }

func (r cleanupFailureRunner) Run(context.Context, core.Command, io.Writer) error {
	_ = os.Chmod(r.parent, 0500)
	return io.ErrUnexpectedEOF
}
func (r cleanupFailureRunner) Capture(context.Context, core.Command) (string, error) {
	return "", io.ErrUnexpectedEOF
}
func TestImageStagingCleanupFailureIsReported(t *testing.T) {
	p, revision, path := provisionFixture(t, "file", "qcow2")
	parent := filepath.Dir(path)
	defer os.Chmod(parent, 0700)
	p.QemuImg = "/fixture/qemu-img"
	p.Runner = cleanupFailureRunner{parent: parent}
	plan, err := p.Plan(context.Background(), "new-data", revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Create(context.Background(), "new-data", revision, plan.ReviewToken, 1); err == nil || !strings.Contains(err.Error(), "staging cleanup failed") {
		t.Fatal("cleanup failure was hidden", err)
	}
}
