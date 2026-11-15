package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"server-man/internal/core"
)

func provisionFixture(t *testing.T, kind, format string) (Provisioner, string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := Coordinator{Store: Store{Path: filepath.Join(root, "server-man.yaml")}}
	cfg := Config{Version: 1, Storage: map[string]Storage{"new-data": {Path: "new-data", Kind: kind, Format: format}}}
	saved, err := c.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	return NewProvisioner(c, core.Runner{}), saved.Revision, filepath.Join(root, "new-data")
}
func TestProvisionDirectoryIsReviewedPrivateAndNeverReplacesData(t *testing.T) {
	p, revision, path := provisionFixture(t, "directory", "")
	ctx := context.Background()
	plan, err := p.Plan(ctx, "new-data", revision, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("preview created data", err)
	}
	if _, err = p.Create(ctx, "new-data", revision, "wrong-token", 0); err == nil {
		t.Fatal("unreviewed creation accepted")
	}
	if _, err = p.Create(ctx, "new-data", "missing", plan.ReviewToken, 0); err != ErrConflict {
		t.Fatal("stale config accepted", err)
	}
	if _, err = p.Create(ctx, "new-data", revision, plan.ReviewToken, 0); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
	data := filepath.Join(path, "keep")
	if err = os.WriteFile(data, []byte("existing data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Create(ctx, "new-data", revision, plan.ReviewToken, 0); err == nil {
		t.Fatal("existing directory accepted")
	}
	if raw, err := os.ReadFile(data); err != nil || string(raw) != "existing data" {
		t.Fatal("existing data changed", err)
	}
	current, _ := p.Coordinator.Store.Read()
	if current.Revision != revision {
		t.Fatal("provisioning changed desired configuration")
	}
}
func TestProvisionRawDiskIsSparseAndBounded(t *testing.T) {
	p, revision, path := provisionFixture(t, "file", "raw")
	ctx := context.Background()
	plan, err := p.Plan(ctx, "new-data", revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Create(ctx, "new-data", revision, plan.ReviewToken, 1); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 1<<30 || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if stat := info.Sys().(*syscall.Stat_t); stat.Blocks*512 >= info.Size()/10 {
		t.Fatal("raw disk was not sparse", stat.Blocks)
	}
	for _, size := range []int{0, -1, 4097} {
		if _, err = p.Plan(ctx, "new-data", revision, size); err == nil {
			t.Fatal("invalid disk size accepted", size)
		}
	}
}
func TestNativeQCOW2ProvisionOnlyTouchesTemporaryNewDisk(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("native qemu-img is unavailable")
	}
	p, revision, path := provisionFixture(t, "file", "qcow2")
	ctx := context.Background()
	plan, err := p.Plan(ctx, "new-data", revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Create(ctx, "new-data", revision, plan.ReviewToken, 1); err != nil {
		t.Fatal(err)
	}
	raw, err := p.Runner.Capture(ctx, core.Command{Program: p.QemuImg, Args: []string{"info", "--output=json", path}})
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Format string `json:"format"`
		Size   int64  `json:"virtual-size"`
	}
	if json.Unmarshal([]byte(raw), &info) != nil || info.Format != "qcow2" || info.Size != 1<<30 {
		t.Fatal(info)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".server-man-new-storage-") {
			t.Fatal("staging file retained")
		}
	}
}

type failingImageRunner struct{}

func (failingImageRunner) Run(context.Context, core.Command, io.Writer) error {
	return errors.New("private native diagnostic")
}
func (failingImageRunner) Capture(context.Context, core.Command) (string, error) {
	return "", errors.New("private native diagnostic")
}
func TestProvisionFailureDoesNotPublishDiskOrExposeNativeOutput(t *testing.T) {
	p, revision, path := provisionFixture(t, "file", "qcow2")
	p.QemuImg = "/fixture/qemu-img"
	p.Runner = failingImageRunner{}
	plan, err := p.Plan(context.Background(), "new-data", revision, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Create(context.Background(), "new-data", revision, plan.ReviewToken, 1); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("partial disk was published", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".server-man-new-storage-") {
			t.Fatal("failed staging file retained")
		}
	}
}
func TestProvisionRejectsSymlinksMissingParentsExternalPathsAndMounts(t *testing.T) {
	for _, scenario := range []string{"symlink-parent", "missing-parent", "external", "mount", "private-reference", "existing-symlink", "writable-parent"} {
		t.Run(scenario, func(t *testing.T) {
			p, revision, path := provisionFixture(t, "directory", "")
			snap, _ := p.Coordinator.Store.Read()
			storage := snap.Config.Storage["new-data"]
			switch scenario {
			case "symlink-parent":
				if err := os.Symlink(t.TempDir(), filepath.Join(filepath.Dir(path), "alias")); err != nil {
					t.Fatal(err)
				}
				storage.Path = "alias/new"
			case "missing-parent":
				storage.Path = "missing/new"
			case "external":
				storage.Path = filepath.Join(t.TempDir(), "new")
				storage.External = true
			case "mount":
				storage.Mount = "/dev/not-the-mounted-filesystem"
			case "private-reference":
				snap.Config.Secrets = map[string]string{"env": storage.Path}
			case "existing-symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
					t.Fatal(err)
				}
			case "writable-parent":
				if err := os.Chmod(filepath.Dir(path), 0777); err != nil {
					t.Fatal(err)
				}
			}
			snap.Config.Storage["new-data"] = storage
			saved, err := p.Coordinator.Store.Save(snap.Config, revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Plan(context.Background(), "new-data", saved.Revision, 0); err == nil {
				t.Fatal("unsafe provisioning allowed", scenario)
			}
		})
	}
}

func TestProvisionRechecksParentIdentityAndCancellation(t *testing.T) {
	p, revision, path := provisionFixture(t, "directory", "")
	snap, _ := p.Coordinator.Store.Read()
	storage := snap.Config.Storage["new-data"]
	storage.Path = "parent/new-data"
	snap.Config.Storage["new-data"] = storage
	saved, err := p.Coordinator.Store.Save(snap.Config, revision)
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(filepath.Dir(path), "parent")
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(context.Background(), "new-data", saved.Revision, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Create(context.Background(), "new-data", saved.Revision, plan.ReviewToken, 0); err == nil {
		t.Fatal("parent substitution accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Create(ctx, "new-data", saved.Revision, plan.ReviewToken, 0); err == nil {
		t.Fatal("cancelled creation accepted")
	}
	if _, err = os.Stat(filepath.Join(parent, "new-data")); !os.IsNotExist(err) {
		t.Fatal("unreviewed data created", err)
	}
}
func TestProvisionRequiresStoppedServicesAndUnpublishedShares(t *testing.T) {
	p, revision, _ := provisionFixture(t, "directory", "")
	snap, _ := p.Coordinator.Store.Read()
	snap.Config.Services = map[string]Service{"application": {Kind: "container", Engine: "podman", Backend: "quadlet", Containers: map[string]Container{"web": {Image: "example/image:1", Mounts: []Mount{{Storage: "new-data", Target: "/data"}}}}}}
	saved, err := p.Coordinator.Store.Save(snap.Config, revision)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeAdapter{active: true, matches: map[string]bool{}}
	p.Coordinator.Adapters = map[string]Adapter{"podman/quadlet": adapter}
	if _, err = p.Plan(context.Background(), "new-data", saved.Revision, 0); err == nil {
		t.Fatal("active consumer allowed")
	}
	adapter.active = false
	plan, err := p.Plan(context.Background(), "new-data", saved.Revision, 0)
	if err != nil || len(plan.Consumers) != 1 {
		t.Fatal(plan, err)
	}
	snap.Config.Shares = map[string]Share{"files": {Protocol: "samba", Storage: "new-data", Enabled: true, ReadOnly: true, Guest: true}}
	saved, err = p.Coordinator.Store.Save(snap.Config, saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Plan(context.Background(), "new-data", saved.Revision, 0); err == nil {
		t.Fatal("published consumer allowed")
	}
	if len(adapter.calls) != 0 {
		t.Fatal("preflight mutated consumer")
	}
}
