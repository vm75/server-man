package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"server-man/internal/core"
)

func TestLibvirtBackupDiscoversLivePersistentAndGuestStateWithoutMutation(t *testing.T) {
	l, c, r := libvirtFixture(t)
	raw, err := RenderLibvirt(l.Store, c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	r.xml = string(raw)
	r.exists = true
	r.state = "running"
	r.xml = strings.Replace(r.xml, "</os>", "<nvram>/native/guest-vars.fd</nvram></os>", 1)
	r.xml = strings.Replace(r.xml, "</disk>", "<backingStore type='file'><source file='/native/base.qcow2'><dataStore type='file'><source file='/native/base-data.raw'/></dataStore></source></backingStore></disk>", 1)
	r.xml = strings.Replace(r.xml, "</devices>", "<tpm><backend type='emulator'><source type='dir' path='/native/tpm-state'/><encryption secret='PRIVATE_METADATA_SENTINEL'/></backend></tpm></devices>", 1)
	r.activeXML = strings.Replace(r.xml, l.Store.Resolve(c.Storage["root"].Path), "/native/live-overlay.qcow2", 1)
	discovered, err := l.BackupDependencies(context.Background(), c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, input := range discovered.Inputs {
		paths[input.Path] = true
		if input.Inspection != "native_path_discovered" {
			t.Fatal(input)
		}
	}
	for _, path := range []string{"/native/guest-vars.fd", "/native/base.qcow2", "/native/base-data.raw", "/native/tpm-state", "/native/live-overlay.qcow2"} {
		if !paths[path] {
			t.Fatal("native state dependency missing", path, paths)
		}
	}
	if discovered.State != "running" {
		t.Fatal(discovered.State)
	}
	data, _ := json.Marshal(discovered)
	if strings.Contains(string(data), "PRIVATE_METADATA_SENTINEL") {
		t.Fatal("raw native metadata leaked")
	}
	for _, command := range r.commands {
		if len(command.Args) > 2 && command.Args[2] != "list" && command.Args[2] != "dumpxml" && command.Args[2] != "domstate" {
			t.Fatal("discovery mutated guest", command.Args)
		}
	}
}
func TestLibvirtBackupRefusesOwnershipAndDoesNotGuessTPMState(t *testing.T) {
	l, c, r := libvirtFixture(t)
	raw, err := RenderLibvirt(l.Store, c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	r.exists = true
	r.xml = strings.Replace(string(raw), "</devices>", "<tpm><backend type='emulator' persistent_state='yes'/></tpm></devices>", 1)
	discovered, err := l.BackupDependencies(context.Background(), c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range discovered.Issues {
		if issue.Code == "backup_tpm_state" {
			found = true
		}
	}
	if !found {
		t.Fatal("implicit TPM location was treated as covered", discovered)
	}
	r.xml = strings.Replace(r.xml, `id="guest"`, `id="foreign"`, 1)
	if _, err = l.BackupDependencies(context.Background(), c, "service/guest"); err == nil {
		t.Fatal("foreign ownership accepted")
	}
}

type incusBackupRecorder struct {
	*incusRecorder
	volumeContentType string
	volumeProject     string
	volumeUsers       []string
	projectVolumes    bool
	failVolume        bool
	clustered         bool
	omitServerProof   bool
	snapshotRecords   map[string][]*incusArchiveGuest
	volumeRecords     map[string]incusTestVolume
}

type incusTestVolume struct {
	Name, ContentType string
	UsedBy            []string
}

func (r *incusBackupRecorder) Capture(ctx context.Context, cmd core.Command) (string, error) {
	method, path := incusRequest(cmd)
	u, _ := url.Parse(path)
	if method != "GET" {
		return "", errors.New("mutation in read-only fixture")
	}
	var metadata any
	if record, found := r.volumeRecords[u.Path+"?project="+u.Query().Get("project")]; found {
		metadata = map[string]any{"name": record.Name, "type": "custom", "content_type": record.ContentType, "used_by": record.UsedBy}
		r.commands = append(r.commands, cmd)
		raw, _ := json.Marshal(map[string]any{"type": "sync", "error_code": 0, "metadata": metadata})
		return string(raw), nil
	}
	if strings.HasSuffix(u.Path, "/snapshots") {
		snapshots := r.snapshotRecords[u.Path]
		if snapshots == nil {
			snapshots = []*incusArchiveGuest{}
		}
		metadata = snapshots
		r.commands = append(r.commands, cmd)
		raw, _ := json.Marshal(map[string]any{"type": "sync", "error_code": 0, "metadata": metadata})
		return string(raw), nil
	}
	switch u.Path {
	case "/1.0":
		environment := map[string]any{"server_name": "fixture-host"}
		if !r.omitServerProof {
			environment["server_clustered"] = r.clustered
		}
		metadata = map[string]any{"environment": environment}
	case "/1.0/projects/managed":
		metadata = map[string]any{"name": "managed", "config": map[string]string{"features.storage.volumes": map[bool]string{true: "true", false: "false"}[r.projectVolumes]}}
	case "/1.0/storage-pools/default/volumes/custom/data":
		r.volumeProject = u.Query().Get("project")
		if r.failVolume {
			return "", errors.New("private native diagnostic")
		}
		contentType := r.volumeContentType
		if contentType == "" {
			contentType = "filesystem"
		}
		metadata = map[string]any{"name": "data", "type": "custom", "content_type": contentType, "used_by": r.volumeUsers}
	default:
		return r.incusRecorder.Capture(ctx, cmd)
	}
	r.commands = append(r.commands, cmd)
	raw, _ := json.Marshal(map[string]any{"type": "sync", "error_code": 0, "metadata": metadata})
	return string(raw), nil
}
func nativeIncusBackupFixture(t *testing.T) (*Incus, Config, *incusBackupRecorder) {
	t.Helper()
	i, c, base := incusFixture(t)
	desired, err := RenderIncus(c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	base.actual = incusInstanceInfo{incusDefinition: desired, Name: incusInstance("guest"), Type: "virtual-machine", StatusCode: 102}
	base.exists = true
	base.actual.Config["user.user-data"] = "PRIVATE_METADATA_SENTINEL"
	base.actual.Devices["data"] = map[string]string{"type": "disk", "path": "/data", "pool": "default", "source": "data/subdir"}
	recorder := &incusBackupRecorder{incusRecorder: base, volumeUsers: []string{"/1.0/instances/sm-guest?project=managed"}}
	i.Runner = recorder
	return i, c, recorder
}
func TestIncusBackupDiscoversCustomVolumesInEffectiveProject(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared-default", true: "project-volumes"}[separate], func(t *testing.T) {
			i, c, r := nativeIncusBackupFixture(t)
			r.projectVolumes = separate
			discovered, err := i.BackupDependencies(context.Background(), c, "service/guest")
			if err != nil {
				t.Fatal(err)
			}
			expected := "default"
			if separate {
				expected = "managed"
			}
			if r.volumeProject != expected || len(discovered.Inputs) != 1 || discovered.Inputs[0].Native.Project != expected || discovered.Inputs[0].Native.Name != "data" {
				t.Fatal(discovered, r.volumeProject)
			}
			if discovered.State != "stopped" || len(discovered.Issues) != 0 {
				t.Fatal(discovered)
			}
			data, _ := json.Marshal(discovered)
			if strings.Contains(string(data), "PRIVATE_METADATA_SENTINEL") {
				t.Fatal("native guest configuration leaked")
			}
			for _, command := range r.commands {
				method, _ := incusRequest(command)
				if method != "GET" {
					t.Fatal("discovery used a native mutation")
				}
				if command.Args[0] != "--force-local" {
					t.Fatal("ambient remote was used")
				}
			}
		})
	}
}
func TestIncusBackupFlagsUnmanagedWritersAndUnavailableVolumeMetadata(t *testing.T) {
	i, c, r := nativeIncusBackupFixture(t)
	r.volumeUsers = append(r.volumeUsers, "/1.0/instances/unmanaged?project=other")
	discovered, err := i.BackupDependencies(context.Background(), c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range discovered.Issues {
		if issue.Code == "backup_volume_users" {
			found = true
		}
	}
	if !found {
		t.Fatal("shared writer was not surfaced", discovered)
	}
	r.failVolume = true
	if _, err = i.BackupDependencies(context.Background(), c, "service/guest"); err == nil || strings.Contains(err.Error(), "private native") {
		t.Fatal(err)
	}
}
func TestBackupInventoryUsesNativeDiscoveryAndRetainsUnresolvedCaptureRequirements(t *testing.T) {
	i, c, _ := nativeIncusBackupFixture(t)
	if _, err := i.Store.Save(c, "missing"); err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{Store: i.Store, Adapters: map[string]Adapter{"incus": i}}
	inventory, err := coordinator.BackupInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundRoot, foundVolume := false, false
	for _, input := range inventory.Inputs {
		if input.ID == "native/guest" {
			foundRoot = input.Inspection == "native_dependencies_discovered" && input.Native.Project == "managed"
		}
		if input.Kind == "incus_custom_volume" {
			foundVolume = true
		}
	}
	if !foundRoot || !foundVolume || inventory.Stage != "inventory" {
		t.Fatal(inventory)
	}
}

type waitingBackupAdapter struct{ fakeAdapter }

func (a *waitingBackupAdapter) BackupDependencies(ctx context.Context, _ Config, _ string) (NativeBackupDependencies, error) {
	<-ctx.Done()
	return NativeBackupDependencies{}, ctx.Err()
}
func TestNativeBackupProbeTimeoutStaysUnknownWithinInventory(t *testing.T) {
	c, _ := backupFixture(t)
	c.Adapters = map[string]Adapter{"incus": &waitingBackupAdapter{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.BackupInventory(ctx)
	if err != nil {
		t.Fatal("per-guest timeout consumed the whole request", err)
	}
	found := false
	for _, issue := range result.Issues {
		if issue.Resource == "service/incus" && issue.Code == "backup_native_dependencies" && issue.Severity == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatal("timed-out native coverage was reported healthy")
	}
}

type sharedVolumeBackupAdapter struct{ fakeAdapter }

func (a *sharedVolumeBackupAdapter) BackupDependencies(_ context.Context, _ Config, key string) (NativeBackupDependencies, error) {
	return NativeBackupDependencies{State: "stopped", Object: &NativeBackupObject{Backend: "incus", Name: strings.TrimPrefix(key, "service/"), Project: "default"}, Inputs: []BackupInput{{ID: "native/" + strings.TrimPrefix(key, "service/") + "/volume/shared", Kind: "incus_custom_volume", Strategy: "native_export", Native: &NativeBackupObject{Backend: "incus", Name: "shared", Project: "default", Pool: "pool", ContentType: "filesystem"}, Resources: []string{key}, Required: true, Sensitive: true, Inspection: "native_volume_discovered"}}}, nil
}
func TestNativeBackupVolumeDedupRetainsEveryManagedConsumer(t *testing.T) {
	c, cfg := backupFixture(t)
	other := cfg.Services["incus"]
	vm := *other.VM
	vm.UUID = ""
	vm.MAC = ""
	other.VM = &vm
	cfg.Services["other"] = other
	current, _ := c.Store.Read()
	if _, err := c.Store.Save(cfg, current.Revision); err != nil {
		t.Fatal(err)
	}
	c.Adapters = map[string]Adapter{"incus": &sharedVolumeBackupAdapter{}}
	result, err := c.BackupInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	primary, covered := 0, 0
	for _, input := range result.Inputs {
		if input.Kind == "incus_custom_volume" {
			if input.IncludedBy != "" {
				covered++
			} else {
				primary++
				joined := strings.Join(input.Resources, ",")
				if !strings.Contains(joined, "service/incus") || !strings.Contains(joined, "service/other") {
					t.Fatal("deduplication lost a writer", input)
				}
			}
		}
	}
	if primary != 1 || covered != 1 {
		t.Fatal(primary, covered)
	}
}

func TestIncusBackupVerifiesLocalScopeBeforeQueryingGuests(t *testing.T) {
	for _, scenario := range []string{"cluster", "unverified"} {
		t.Run(scenario, func(t *testing.T) {
			i, c, r := nativeIncusBackupFixture(t)
			r.clustered = scenario == "cluster"
			r.omitServerProof = scenario == "unverified"
			if _, err := i.BackupDependencies(context.Background(), c, "service/guest"); err == nil {
				t.Fatal("unverified/clustered scope accepted")
			}
			for _, command := range r.commands {
				_, path := incusRequest(command)
				if strings.Contains(path, "/instances") {
					t.Fatal("guest queried before verifying local scope")
				}
			}
		})
	}
}
