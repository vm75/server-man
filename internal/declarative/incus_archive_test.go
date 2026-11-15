package declarative

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func fixtureIncusArchive(object NativeBackupObject, kind, id, uuid string, data []byte, snapshots ...string) ([]byte, error) {
	return fixtureIncusArchiveDevices(object, kind, id, uuid, nil, data, snapshots...)
}

func fixtureIncusArchiveDevices(object NativeBackupObject, kind, id, uuid string, devices map[string]map[string]string, data []byte, snapshots ...string) ([]byte, error) {
	if object.ContentType == "iso" {
		return data, nil
	}
	index := map[string]any{"name": object.Name, "pool": object.Pool, "backend": "dir", "type": "custom", "optimized": false, "optimized_header": false, "snapshots": []string{}, "config": map[string]any{"pool": map[string]any{"name": object.Pool, "driver": "dir"}, "volume": map[string]any{"name": object.Name, "type": "custom", "content_type": object.ContentType}}}
	prefix := "volume"
	if kind == "incus_instance_export" {
		prefix = "virtual-machine"
		if devices == nil {
			devices = map[string]map[string]string{"root": {"type": "disk", "path": "/", "pool": object.Pool}}
		}
		index["type"] = "virtual-machine"
		index["config"].(map[string]any)["volume"].(map[string]any)["type"] = "virtual-machine"
		index["config"].(map[string]any)["container"] = map[string]any{"name": object.Name, "type": "virtual-machine", "devices": devices, "config": map[string]string{"user.server-man.id": id, "user.server-man.identity": uuid}}
	} else {
		// The standalone custom-volume producer omits config.pool; pool identity
		// remains in the top-level index, unlike instance/dependent-volume config.
		delete(index["config"].(map[string]any), "pool")
	}
	index["snapshots"] = append([]string{}, snapshots...)
	volumeSnapshots := []map[string]any{}
	guestSnapshots := []map[string]any{}
	for _, snapshot := range snapshots {
		volumeSnapshots = append(volumeSnapshots, map[string]any{"name": snapshot, "content_type": object.ContentType})
		if kind == "incus_instance_export" {
			guestSnapshots = append(guestSnapshots, map[string]any{"name": snapshot, "devices": devices, "config": map[string]string{"user.server-man.id": id, "user.server-man.identity": uuid, "user.server-man.fingerprint": "historical"}})
		}
	}
	index["config"].(map[string]any)["volume_snapshots"] = volumeSnapshots
	if kind == "incus_instance_export" {
		index["config"].(map[string]any)["snapshots"] = guestSnapshots
	}
	raw, err := yaml.Marshal(index)
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	if err := writer.WriteHeader(&tar.Header{Name: "backup/index.yaml", Mode: 0600, Size: int64(len(raw))}); err != nil {
		return nil, err
	}
	if _, err := writer.Write(raw); err != nil {
		return nil, err
	}
	writePayload := func(base string) error {
		if object.ContentType == "filesystem" || kind == "incus_instance_export" {
			if err := writer.WriteHeader(&tar.Header{Name: "backup/" + base, Typeflag: tar.TypeDir, Mode: 0700}); err != nil {
				return err
			}
		}
		file := "backup/" + base + "/payload"
		if object.ContentType == "block" {
			file = "backup/" + base + ".img"
		}
		if err := writer.WriteHeader(&tar.Header{Name: file, Mode: 0600, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err := writer.Write(data)
		return err
	}
	snapshotPrefix := "volume-snapshots"
	if kind == "incus_instance_export" {
		snapshotPrefix = "virtual-machine-snapshots"
	}
	for _, snapshot := range snapshots {
		if err := writePayload(snapshotPrefix + "/" + snapshot); err != nil {
			return nil, err
		}
	}
	if err := writePayload(prefix); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func TestIncusArchiveScannerRejectsUnsafeMembers(t *testing.T) {
	for _, scenario := range []string{"valid", "guest absolute link", "special metadata", "hardlink valid", "traversal", "absolute", "duplicate", "symlink parent", "hardlink missing", "truncated", "trailing archive", "missing index", "bad index", "compressed", "no footer"} {
		t.Run(scenario, func(t *testing.T) {
			object := NativeBackupObject{Backend: "incus", Name: "data", Pool: "pool", Project: "default", ContentType: "filesystem"}
			raw, err := fixtureIncusArchive(object, "incus_custom_volume", "", "", []byte("payload"))
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "truncated" {
				raw = raw[:len(raw)-1500]
			} else if scenario == "trailing archive" {
				raw = append(raw, raw...)
			} else if scenario == "compressed" {
				raw = []byte{31, 139, 8, 0}
			} else if scenario == "no footer" {
				raw = raw[:len(raw)-1024]
			} else if scenario != "valid" {
				var buffer bytes.Buffer
				writer := tar.NewWriter(&buffer)
				reader := tar.NewReader(bytes.NewReader(raw))
				for {
					h, err := reader.Next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if h.Name == "backup/index.yaml" && scenario == "missing index" {
						continue
					}
					data, err := io.ReadAll(reader)
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "bad index" && h.Name == "backup/index.yaml" {
						data = []byte("name: secret\nname: repeated\n")
						h.Size = int64(len(data))
					}
					if err := writer.WriteHeader(h); err != nil {
						t.Fatal(err)
					}
					if _, err := writer.Write(data); err != nil {
						t.Fatal(err)
					}
				}
				var headers []*tar.Header
				switch scenario {
				case "guest absolute link":
					headers = []*tar.Header{{Name: "backup/volume/guest-link", Typeflag: tar.TypeSymlink, Linkname: "/guest/data"}}
				case "special metadata":
					headers = []*tar.Header{{Name: "backup/volume/device", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3}}
				case "hardlink valid":
					headers = []*tar.Header{{Name: "backup/volume/alias", Typeflag: tar.TypeLink, Linkname: "backup/volume/payload"}}
				case "traversal":
					headers = []*tar.Header{{Name: "backup/../outside"}}
				case "absolute":
					headers = []*tar.Header{{Name: "/outside"}}
				case "duplicate":
					headers = []*tar.Header{{Name: "backup/index.yaml"}}
				case "symlink parent":
					headers = []*tar.Header{{Name: "backup/volume/link", Typeflag: tar.TypeSymlink, Linkname: "/outside"}, {Name: "backup/volume/link/child"}}
				case "hardlink missing":
					headers = []*tar.Header{{Name: "backup/volume/link", Typeflag: tar.TypeLink, Linkname: "backup/volume/missing"}}
				}
				for _, h := range headers {
					if err := writer.WriteHeader(h); err != nil {
						t.Fatal(err)
					}
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				raw = buffer.Bytes()
			}
			_, _, err = scanIncusArchive(context.Background(), bytes.NewReader(raw))
			if scenario == "valid" || scenario == "guest absolute link" || scenario == "special metadata" || scenario == "hardlink valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestIncusArchiveIdentityAndSnapshotCoverage(t *testing.T) {
	cfg := Config{Services: map[string]Service{"guest": {Kind: "vm", Backend: "incus", VM: &VM{UUID: "identity"}}}}
	object := NativeBackupObject{Backend: "incus", Name: "sm-guest", Pool: "pool", Project: "managed", ContentType: "block"}
	export := BackupManifestNativeExport{Backend: "incus", Kind: "incus_instance_export", Native: &object, Resources: []string{"service/guest"}}
	raw, err := fixtureIncusArchive(object, export.Kind, "guest", "identity", []byte("block"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"valid", "snapshot present", "name", "pool", "optimized", "uuid", "snapshot missing", "unknown member"} {
		t.Run(scenario, func(t *testing.T) {
			index, members, err := scanIncusArchive(context.Background(), bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "snapshot present":
				index.Snapshots = []string{"snap0"}
				index.Config.Snapshots = []*incusArchiveGuest{{Name: "snap0", Devices: index.Config.Container.Devices, Config: map[string]string{"user.server-man.id": "guest", "user.server-man.identity": "identity"}}}
				index.Config.VolumeSnapshots = []*incusArchiveVolume{{Name: "snap0", ContentType: "block"}}
				members["backup/virtual-machine-snapshots/snap0.img"] = incusArchiveMember{kind: tar.TypeReg, size: 5}
				members["backup/virtual-machine-snapshots/snap0"] = incusArchiveMember{kind: tar.TypeDir}
			case "name":
				index.Name = "foreign"
			case "pool":
				index.Pool = "other"
			case "optimized":
				flag := true
				index.Optimized = &flag
			case "uuid":
				index.Config.Container.Config["user.server-man.identity"] = "foreign"
			case "snapshot missing":
				index.Snapshots = []string{"snap0"}
			case "unknown member":
				members["backup/other"] = incusArchiveMember{kind: tar.TypeDir}
			}
			err = checkIncusArchiveIndex(index, members, export, cfg)
			if scenario == "valid" || scenario == "snapshot present" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("wrong identity or coverage accepted")
			}
		})
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "native/incus"), 0700); err != nil {
		t.Fatal(err)
	}
	export.BundlePath = "native/incus/archive.tar"
	export.SizeBytes = int64(len(raw))
	export.SHA256 = digest(raw)
	if err := os.WriteFile(filepath.Join(dir, export.BundlePath), raw, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := verifyIncusArchive(context.Background(), root, export, cfg); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := verifyIncusArchive(ctx, root, export, cfg); err != context.Canceled {
		t.Fatal(err)
	}
	export.SHA256 = strings.Repeat("b", 64)
	if err := verifyIncusArchive(context.Background(), root, export, cfg); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}

func TestIncusIndexUsesBoundedUnambiguousYAML(t *testing.T) {
	for _, raw := range []string{"name: data\nname: other\n", "name: &x data\npool: *x\n", "name: !private data\n", "1: data\n", "name: data\n---\nname: other\n", "config: " + strings.Repeat("{x: ", 70) + "value" + strings.Repeat("}", 70)} {
		var index incusArchiveIndex
		if err := decodeIncusArchiveIndex(context.Background(), []byte(raw), &index); err == nil {
			t.Fatal("ambiguous native metadata accepted")
		}
	}
}

func TestIncusISOBytesAreVerifiedWithoutTarInterpretation(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "native/incus"), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("raw ISO fixture")
	export := BackupManifestNativeExport{Backend: "incus", Kind: "incus_custom_volume", Native: &NativeBackupObject{Backend: "incus", Name: "media", Project: "default", Pool: "pool", ContentType: "iso"}, BundlePath: "native/incus/media.iso", SizeBytes: int64(len(data)), SHA256: digest(data)}
	if err := os.WriteFile(filepath.Join(dir, export.BundlePath), data, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := verifyIncusArchive(context.Background(), root, export, Config{}); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	if err := os.WriteFile(filepath.Join(dir, export.BundlePath), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyIncusArchive(context.Background(), root, export, Config{}); err == nil {
		t.Fatal("changed ISO bytes accepted")
	}
}
