package declarative

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

func historicalIncusFixture(t *testing.T) (*Incus, Config, *incusExportRecorder, *incusArchiveGuest) {
	t.Helper()
	i, cfg, recorder, _, _ := incusExportFixture(t)
	devices := map[string]map[string]string{}
	for name, device := range recorder.actual.Devices {
		devices[name] = maps.Clone(device)
	}
	devices["data"]["pool"], devices["data"]["source"] = "past-pool", "past/subdir"
	snapshot := &incusArchiveGuest{Name: "old", Config: maps.Clone(recorder.actual.Config), Devices: devices}
	recorder.snapshotRecords = map[string][]*incusArchiveGuest{incusPath("guest") + "/snapshots": {snapshot}}
	recorder.volumeRecords = map[string]incusTestVolume{"/1.0/storage-pools/past-pool/volumes/custom/past?project=default": {Name: "past", ContentType: "filesystem"}}
	recorder.snapshotNames = []string{"old"}
	recorder.archiveMutate = func(raw []byte) []byte {
		return rewriteIncusIndex(t, raw, func(index *incusArchiveIndex) {
			if index.Config.Container != nil {
				index.Config.Snapshots[0].Devices = snapshot.Devices
			}
		})
	}
	return i, cfg, recorder, snapshot
}

func TestIncusHistoricalOnlyVolumeIsDiscoveredAndCaptured(t *testing.T) {
	i, cfg, recorder, _ := historicalIncusFixture(t)
	discovered, err := i.BackupDependencies(context.Background(), cfg, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, input := range discovered.Inputs {
		if input.Native != nil && input.Native.Name == "past" {
			found = input.Native.Project == "default" && input.Native.Pool == "past-pool"
		}
	}
	if !found {
		t.Fatal("historical-only volume omitted", discovered)
	}
	snapshot, err := i.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	c := Coordinator{Store: i.Store, Adapters: map[string]Adapter{"incus": i}}
	destination := filepath.Join(t.TempDir(), "bundle")
	summary, err := c.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: destination}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Stage != "captured" || len(recorder.streams) != 3 {
		t.Fatal("historical archive not captured", summary, len(recorder.streams))
	}
	raw, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.NativeExports) != 3 {
		t.Fatal("missing historical export", manifest.NativeExports)
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := verifyBackupBundle(context.Background(), root, summary.ManifestChecksum); err != nil {
		t.Fatal(err)
	}
}

func TestIncusHistoryRejectsUnknownScopeAndUnmanagedWriters(t *testing.T) {
	for _, scenario := range []string{"foreign owner", "duplicate snapshot", "missing root", "unexpanded profile", "unmanaged writer", "host data", "snapshot consumer"} {
		t.Run(scenario, func(t *testing.T) {
			i, cfg, recorder, snapshot := historicalIncusFixture(t)
			switch scenario {
			case "foreign owner":
				snapshot.Config["user.server-man.id"] = "foreign"
			case "duplicate snapshot":
				recorder.snapshotRecords[incusPath("guest")+"/snapshots"] = []*incusArchiveGuest{snapshot, snapshot}
			case "missing root":
				delete(snapshot.Devices, "root")
			case "unexpanded profile":
				snapshot.Profiles = []string{"unknown"}
			case "unmanaged writer":
				record := recorder.volumeRecords["/1.0/storage-pools/past-pool/volumes/custom/past?project=default"]
				record.UsedBy = []string{"/1.0/instances/unmanaged?project=other"}
				recorder.volumeRecords["/1.0/storage-pools/past-pool/volumes/custom/past?project=default"] = record
			case "host data":
				snapshot.Devices["data"] = map[string]string{"type": "disk", "source": "/private/never-read", "path": "/data"}
			case "snapshot consumer":
				record := recorder.volumeRecords["/1.0/storage-pools/past-pool/volumes/custom/past?project=default"]
				record.UsedBy = []string{incusPath("guest") + "/snapshots/old?project=managed"}
				recorder.volumeRecords["/1.0/storage-pools/past-pool/volumes/custom/past?project=default"] = record
			}
			result, err := i.BackupDependencies(context.Background(), cfg, "service/guest")
			if scenario == "snapshot consumer" {
				if err != nil || len(result.Issues) != 0 {
					t.Fatal("owned snapshot consumer rejected", result, err)
				}
			} else if err == nil && len(result.Issues) == 0 {
				t.Fatal("unresolved historical data accepted")
			}
		})
	}
}
