package declarative

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIncusBundleRequiresCurrentAndHistoricalDeviceExports(t *testing.T) {
	for _, scenario := range []string{"current", "historical", "expanded historical", "missing volume", "unreferenced volume", "consumer mismatch", "ambiguous namespace", "foreign namespace", "host data", "missing expansion", "passthrough", "missing root"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := Config{Services: map[string]Service{"guest": {Kind: "vm", Backend: "incus", VM: &VM{UUID: "identity"}}}}
			guest := NativeBackupObject{Backend: "incus", Name: "sm-guest", Pool: "pool", Project: "managed", ContentType: "block"}
			volume := NativeBackupObject{Backend: "incus", Name: "data", Pool: "storage", Project: "default", ContentType: "filesystem"}
			guestRaw, err := fixtureIncusArchive(guest, "incus_instance_export", "guest", "identity", []byte("guest"), "old")
			if err != nil {
				t.Fatal(err)
			}
			guestRaw = rewriteIncusIndex(t, guestRaw, func(index *incusArchiveIndex) {
				target := index.Config.Container
				if scenario == "historical" || scenario == "expanded historical" || scenario == "missing expansion" {
					target = index.Config.Snapshots[0]
					target.Devices = map[string]map[string]string{"root": {"type": "disk", "path": "/", "pool": "historical-root"}}
				}
				if scenario != "unreferenced volume" {
					target.Devices["data"] = map[string]string{"type": "disk", "pool": "storage", "source": "data/subdir", "path": "/data"}
				}
				if scenario == "expanded historical" {
					target.Profiles = []string{"historical-profile"}
					target.ExpandedDevices = target.Devices
					target.Devices = nil
				}
				if scenario == "missing expansion" {
					target.Profiles = []string{"historical-profile"}
				}
				if scenario == "host data" {
					target.Devices["data"] = map[string]string{"type": "disk", "source": "/private/never-read", "path": "/data"}
				}
				if scenario == "passthrough" {
					target.Devices["gpu"] = map[string]string{"type": "gpu"}
				}
				if scenario == "missing root" {
					delete(target.Devices, "root")
				}
			})
			volumeRaw, err := fixtureIncusArchive(volume, "incus_custom_volume", "", "", []byte("volume"))
			if err != nil {
				t.Fatal(err)
			}
			exports := []BackupManifestNativeExport{{Backend: "incus", Kind: "incus_instance_export", Native: &guest, Resources: []string{"service/guest"}, BundlePath: "native/incus/guest.tar", SizeBytes: int64(len(guestRaw)), SHA256: digest(guestRaw)}}
			if scenario != "missing volume" {
				exports = append(exports, BackupManifestNativeExport{Backend: "incus", Kind: "incus_custom_volume", Native: &volume, Resources: []string{"service/guest"}, BundlePath: "native/incus/volume.tar", SizeBytes: int64(len(volumeRaw)), SHA256: digest(volumeRaw)})
			}
			if scenario == "consumer mismatch" {
				exports[1].Resources = []string{"service/other"}
			}
			if scenario == "foreign namespace" {
				volume.Project = "foreign"
			}
			if scenario == "ambiguous namespace" {
				other := volume
				other.Project = "managed"
				duplicate := exports[1]
				duplicate.Native = &other
				duplicate.BundlePath = "native/incus/duplicate.tar"
				exports = append(exports, duplicate)
			}
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "native/incus"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, export := range exports {
				data := volumeRaw
				if export.Kind == "incus_instance_export" {
					data = guestRaw
				}
				if err := os.WriteFile(filepath.Join(dir, export.BundlePath), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			err = validateIncusBundleDependencies(context.Background(), root, exports, cfg)
			valid := scenario == "current" || scenario == "historical" || scenario == "expanded historical"
			if valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unresolved or conflicting device data accepted")
			}
		})
	}
}

func TestIncusBundleKeepsSameNamedVolumeNamespacesDistinct(t *testing.T) {
	cfg := Config{Services: map[string]Service{"a": {Kind: "vm", Backend: "incus", VM: &VM{UUID: "identity-a"}}, "b": {Kind: "vm", Backend: "incus", VM: &VM{UUID: "identity-b"}}}}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "native/incus"), 0700); err != nil {
		t.Fatal(err)
	}
	exports := []BackupManifestNativeExport{}
	for _, id := range []string{"a", "b"} {
		project, volumeProject := "first", "default"
		if id == "b" {
			project, volumeProject = "managed", "managed"
		}
		guest := NativeBackupObject{Backend: "incus", Name: "sm-" + id, Pool: "pool", Project: project, ContentType: "block"}
		devices := map[string]map[string]string{"root": {"type": "disk", "path": "/", "pool": "pool"}, "data": {"type": "disk", "pool": "storage", "source": "data", "path": "/data"}}
		raw, err := fixtureIncusArchiveDevices(guest, "incus_instance_export", id, "identity-"+id, devices, []byte("guest"))
		if err != nil {
			t.Fatal(err)
		}
		guestPath := "native/incus/guest-" + id + ".tar"
		if err := os.WriteFile(filepath.Join(dir, guestPath), raw, 0600); err != nil {
			t.Fatal(err)
		}
		exports = append(exports, BackupManifestNativeExport{Backend: "incus", Kind: "incus_instance_export", Native: &guest, Resources: []string{"service/" + id}, BundlePath: guestPath, SizeBytes: int64(len(raw)), SHA256: digest(raw)})
		volume := NativeBackupObject{Backend: "incus", Name: "data", Pool: "storage", Project: volumeProject, ContentType: "filesystem"}
		raw, err = fixtureIncusArchive(volume, "incus_custom_volume", "", "", []byte("volume"))
		if err != nil {
			t.Fatal(err)
		}
		volumePath := "native/incus/volume-" + id + ".tar"
		if err := os.WriteFile(filepath.Join(dir, volumePath), raw, 0600); err != nil {
			t.Fatal(err)
		}
		exports = append(exports, BackupManifestNativeExport{Backend: "incus", Kind: "incus_custom_volume", Native: &volume, Resources: []string{"service/" + id}, BundlePath: volumePath, SizeBytes: int64(len(raw)), SHA256: digest(raw)})
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := validateIncusBundleDependencies(context.Background(), root, exports, cfg); err != nil {
		t.Fatal("scoped consumer provenance was discarded", err)
	}
}
