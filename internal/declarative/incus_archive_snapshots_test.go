package declarative

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Recompute the physical archive and checksum too: metadata failure must be
// detected independently of byte tampering and tar structural validation.
func rewriteIncusIndex(t *testing.T, raw []byte, mutate func(*incusArchiveIndex)) []byte {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(raw))
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "backup/index.yaml" {
			var index incusArchiveIndex
			if err := decodeIncusArchiveIndex(context.Background(), data, &index); err != nil {
				t.Fatal(err)
			}
			mutate(&index)
			data, err = yaml.Marshal(index)
			if err != nil {
				t.Fatal(err)
			}
			header.Size = int64(len(data))
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestIncusSnapshotRecordsRequireExactCoverage(t *testing.T) {
	for _, kind := range []string{"incus_instance_export", "incus_custom_volume"} {
		t.Run(kind, func(t *testing.T) {
			object := NativeBackupObject{Backend: "incus", Name: "data", Pool: "pool", Project: "managed", ContentType: "filesystem"}
			cfg := Config{Services: map[string]Service{"guest": {Kind: "vm", Backend: "incus", VM: &VM{UUID: "identity"}}}}
			if kind == "incus_instance_export" {
				object.Name = "sm-guest"
				object.ContentType = "block"
			}
			base, err := fixtureIncusArchive(object, kind, "guest", "identity", []byte("snapshot data"), "snap0", "snap1")
			if err != nil {
				t.Fatal(err)
			}
			scenarios := []string{"valid", "reordered", "missing volume metadata", "nil volume metadata", "duplicate volume metadata", "unknown volume snapshot", "wrong content type", "missing primary volume", "foreign primary project", "dependent volumes"}
			if kind == "incus_instance_export" {
				scenarios = append(scenarios, "missing guest metadata", "nil guest metadata", "duplicate guest metadata", "unknown guest snapshot", "foreign snapshot owner", "foreign snapshot uuid")
			} else {
				scenarios = append(scenarios, "unexpected guest metadata")
			}
			for _, scenario := range scenarios {
				t.Run(scenario, func(t *testing.T) {
					raw := rewriteIncusIndex(t, base, func(index *incusArchiveIndex) {
						switch scenario {
						case "reordered":
							slices.Reverse(index.Config.VolumeSnapshots)
							slices.Reverse(index.Config.Snapshots)
						case "missing volume metadata":
							index.Config.VolumeSnapshots = index.Config.VolumeSnapshots[:1]
						case "nil volume metadata":
							index.Config.VolumeSnapshots[0] = nil
						case "duplicate volume metadata":
							index.Config.VolumeSnapshots[1] = index.Config.VolumeSnapshots[0]
						case "unknown volume snapshot":
							index.Config.VolumeSnapshots[0].Name = "unknown"
						case "wrong content type":
							index.Config.VolumeSnapshots[0].ContentType = "iso"
						case "missing primary volume":
							index.Config.Volume = nil
						case "foreign primary project":
							index.Config.Volume.Project = "foreign"
						case "dependent volumes":
							index.Config.DependentVolumes = []yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}}
						case "missing guest metadata":
							index.Config.Snapshots = index.Config.Snapshots[:1]
						case "nil guest metadata":
							index.Config.Snapshots[0] = nil
						case "duplicate guest metadata":
							index.Config.Snapshots[1] = index.Config.Snapshots[0]
						case "unknown guest snapshot":
							index.Config.Snapshots[0].Name = "unknown"
						case "foreign snapshot owner":
							index.Config.Snapshots[0].Config["user.server-man.id"] = "foreign"
						case "foreign snapshot uuid":
							index.Config.Snapshots[0].Config["user.server-man.identity"] = "foreign"
						case "unexpected guest metadata":
							index.Config.Container = &incusArchiveGuest{Name: "unexpected"}
						}
					})
					dir := t.TempDir()
					if err := os.MkdirAll(filepath.Join(dir, "native/incus"), 0700); err != nil {
						t.Fatal(err)
					}
					export := BackupManifestNativeExport{Backend: "incus", Kind: kind, Native: &object, Resources: []string{"service/guest"}, BundlePath: "native/incus/export.tar", SizeBytes: int64(len(raw)), SHA256: digest(raw)}
					if err := os.WriteFile(filepath.Join(dir, export.BundlePath), raw, 0600); err != nil {
						t.Fatal(err)
					}
					root, err := os.OpenRoot(dir)
					if err != nil {
						t.Fatal(err)
					}
					defer root.Close()
					err = verifyIncusArchive(context.Background(), root, export, cfg)
					valid := scenario == "valid" || scenario == "reordered"
					if valid {
						if err != nil {
							t.Fatal(err)
						}
					} else if err == nil {
						t.Fatal("mismatched snapshot metadata accepted")
					}
				})
			}
		})
	}
}

func TestIncusCaptureRejectsIncompleteSnapshotRecords(t *testing.T) {
	for _, kind := range []string{"incus_instance_export", "incus_custom_volume"} {
		t.Run(kind, func(t *testing.T) {
			i, cfg, recorder, instance, volume := incusExportFixture(t)
			recorder.snapshotNames = []string{"snap0"}
			input := instance
			if kind == "incus_custom_volume" {
				input = volume
			}
			good := privateExportDirectory(t)
			if _, err := i.BackupExport(context.Background(), cfg, input, good); err != nil {
				t.Fatal("valid native snapshot archive rejected", err)
			}
			recorder.archiveMutate = func(raw []byte) []byte {
				return rewriteIncusIndex(t, raw, func(index *incusArchiveIndex) { index.Config.VolumeSnapshots[0] = nil })
			}
			bad := privateExportDirectory(t)
			if _, err := i.BackupExport(context.Background(), cfg, input, bad); err == nil {
				t.Fatal("incomplete snapshot capture succeeded")
			}
			paths, err := filepath.Glob(filepath.Join(bad, "native/incus/*"))
			if err != nil || len(paths) != 0 {
				t.Fatal("failed native artifact was retained", paths, err)
			}
		})
	}
}
