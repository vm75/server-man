package declarative

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"server-man/internal/core"
)

func backupFixture(t *testing.T) (Coordinator, Config) {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"data/shared", "secrets", "assets"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{"data/shared/application.env": "PRIVATE_CONTENT_SENTINEL", "secrets/service.env": "PRIVATE_CONTENT_SENTINEL", "assets/base.raw": "initial image", "data/disk.raw": "guest data"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{Version: 1, Storage: map[string]Storage{
		"shared":   {Kind: "directory", Path: "data/shared"},
		"disk":     {Kind: "file", Path: "data/disk.raw", Format: "raw", Source: &DiskSource{Path: "assets/base.raw", Format: "raw", SHA256: digest([]byte("initial image"))}},
		"external": {Kind: "directory", Path: filepath.Join(root, "external-data"), External: true},
	}, Secrets: map[string]string{"nested": "data/shared/application.env", "service": "secrets/service.env"}, Bindings: map[string]Binding{"lan": {Kind: "network", Name: "default"}, "pool": {Kind: "pool", Name: "default"}, "project": {Kind: "project", Name: "default"}}, Shares: map[string]Share{"files": {Protocol: "samba", Storage: "shared", Enabled: true, Guest: true, ReadOnly: true}}, Services: map[string]Service{
		"app":   {Kind: "container", Engine: "podman", Backend: "quadlet", Containers: map[string]Container{"web": {Image: "example/image:1", EnvFiles: []string{"service", "nested"}, Mounts: []Mount{{Storage: "shared", Target: "/data"}}}}},
		"kvm":   {Kind: "vm", Backend: "libvirt", VM: &VM{CPUs: 1, MemoryMiB: 512, Network: "lan", Disks: []string{"disk"}}},
		"incus": {Kind: "vm", Backend: "incus", VM: &VM{CPUs: 1, MemoryMiB: 512, Network: "lan", Pool: "pool", Project: "project", Image: "base"}},
	}}
	c := Coordinator{Store: Store{Path: filepath.Join(root, "workloads.yaml")}}
	saved, err := c.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	return c, saved.Config
}
func TestBackupInventoryCoversDefinitionsWithoutReadingSecretContents(t *testing.T) {
	c, _ := backupFixture(t)
	before, revision, err := c.Store.ReadRaw()
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := c.BackupInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Stage != "inventory" || inventory.Revision != revision {
		t.Fatal(inventory)
	}
	inputs := map[string]BackupInput{}
	for _, input := range inventory.Inputs {
		inputs[input.ID] = input
	}
	for _, key := range []string{"configuration", "storage/shared", "storage/disk", "storage/external", "asset/disk", "secret/nested", "secret/service", "native/kvm", "native/incus"} {
		if _, ok := inputs[key]; !ok {
			t.Fatal("missing backup input", key)
		}
	}
	if inputs["configuration"].BundlePath != "server-man.yaml" || inputs["secret/nested"].IncludedBy != "storage/shared" || inputs["storage/external"].Strategy != "external_data" || inputs["native/incus"].Strategy != "native_export" {
		t.Fatal(inputs)
	}
	if !strings.Contains(strings.Join(inputs["storage/shared"].Resources, ","), "share/files") || !strings.Contains(strings.Join(inputs["secret/service"].Resources, ","), "service/app") {
		t.Fatal("consumer references lost")
	}
	nativeUnknown := 0
	for _, issue := range inventory.Issues {
		if issue.Code == "backup_native_dependencies" && issue.Severity == "unknown" {
			nativeUnknown++
		}
	}
	if nativeUnknown != 2 {
		t.Fatal("native state was presented as complete", inventory.Issues)
	}
	data, err := json.Marshal(inventory)
	if err != nil || strings.Contains(string(data), "PRIVATE_CONTENT_SENTINEL") {
		t.Fatal("private file contents were exposed", err)
	}
	after, current, err := c.Store.ReadRaw()
	if err != nil || current != revision || string(before) != string(after) {
		t.Fatal("inventory modified configuration", err)
	}
	entries, err := os.ReadDir(filepath.Dir(c.Store.Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "backup") || strings.HasSuffix(entry.Name(), ".apply.json") {
			t.Fatal("inventory created backup/runtime state", entry.Name())
		}
	}
}
func TestBackupInventoryMissingFilesAndSymlinkEscapeAreExplicit(t *testing.T) {
	c, _ := backupFixture(t)
	root := filepath.Dir(c.Store.Path)
	if err := os.Remove(filepath.Join(root, "secrets/service.env")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "assets/base.raw")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "data/shared")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "application.env"), []byte("PRIVATE_CONTENT_SENTINEL"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "data/shared")); err != nil {
		t.Fatal(err)
	}
	inventory, err := c.BackupInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	issues := map[string]bool{}
	for _, issue := range inventory.Issues {
		issues[issue.Resource] = true
	}
	for _, id := range []string{"secret/service", "asset/disk", "storage/shared", "secret/nested"} {
		if !issues[id] {
			t.Fatal("missing unsafe/unavailable input diagnostic", id)
		}
	}
	for _, input := range inventory.Inputs {
		if input.ID == "secret/nested" && input.Inspection == "metadata_only" {
			t.Fatal("outside-root parent link was followed")
		}
	}
	if _, err = os.Stat(filepath.Join(root, "secrets/service.env")); !os.IsNotExist(err) {
		t.Fatal("missing secret was recreated", err)
	}
}
func TestBackupInventoryPrivatePolicyAndCancellation(t *testing.T) {
	c, _ := backupFixture(t)
	r := core.NewRegistry(false, nil)
	if err := RegisterBackup(r, c); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(context.Background(), "managed_backup_inventory", []byte(`{}`), io.Discard); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatal("private inventory bypassed write policy", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.BackupInventory(ctx); err == nil {
		t.Fatal("cancelled inventory was presented as successful")
	}
}

func TestBackupInventoryBoundsAndSpecialFileHandling(t *testing.T) {
	c, cfg := backupFixture(t)
	cfg.Services = map[string]Service{}
	for serviceIndex := range 64 {
		containers := map[string]Container{}
		for containerIndex := range 32 {
			containers[fmt.Sprintf("container%d", containerIndex)] = Container{Image: "example/image:1"}
		}
		cfg.Services[fmt.Sprintf("application%d", serviceIndex)] = Service{Kind: "container", Engine: "podman", Backend: "quadlet", Containers: containers}
	}
	current, _ := c.Store.Read()
	if _, err := c.Store.Save(cfg, current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := c.BackupInventory(context.Background()); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatal("unbounded inventory was returned", err)
	}
	c, _ = backupFixture(t)
	path := filepath.Join(filepath.Dir(c.Store.Path), "secrets/service.env")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := c.BackupInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, input := range result.Inputs {
		if input.ID == "secret/service" && input.Inspection == "wrong_type" {
			found = true
		}
	}
	if !found {
		t.Fatal("FIFO input was not rejected without opening it")
	}
}
