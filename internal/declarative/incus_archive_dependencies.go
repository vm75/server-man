package declarative

import (
	"context"
	"errors"
	"os"
	"slices"
)

func incusArchiveDiskReferences(index incusArchiveIndex, export BackupManifestNativeExport) ([]incusDiskReference, error) {
	invalid := errors.New("Incus current or historical device data is unresolved")
	guests := append([]*incusArchiveGuest{index.Config.Container}, index.Config.Snapshots...)
	seen := map[incusDiskReference]bool{}
	references := []incusDiskReference{}
	for at, guest := range guests {
		if guest == nil {
			return nil, invalid
		}
		devices, err := incusBackupDevices(guest)
		if err != nil {
			return nil, invalid
		}
		roots := 0
		for _, device := range devices {
			if device["type"] == "nic" {
				continue
			}
			reference, err := parseIncusBackupDisk(device)
			if err != nil || reference.kind == "external" {
				return nil, invalid
			}
			if reference.kind == "root" {
				roots++
				// Historical root bindings can differ after a native storage move;
				// their data is the snapshot payload already checked in this archive.
				if at == 0 && reference.pool != export.Native.Pool {
					return nil, invalid
				}
				continue
			}
			if !seen[reference] {
				seen[reference] = true
				references = append(references, reference)
			}
		}
		if roots != 1 {
			return nil, invalid
		}
	}
	return references, nil
}

// Archive metadata never resolves files on the old host. Every custom disk in
// every guest generation must instead resolve to one captured native volume.
func validateIncusBundleDependencies(ctx context.Context, root *os.Root, exports []BackupManifestNativeExport, cfg Config) error {
	invalid := errors.New("Incus current or historical volume exports do not cover the recorded device data")
	volumes := map[int]map[string]bool{}
	for at, export := range exports {
		if export.Backend == "incus" && export.Kind == "incus_custom_volume" {
			volumes[at] = map[string]bool{}
		}
	}
	for _, export := range exports {
		if export.Backend != "incus" {
			continue
		}
		index, err := inspectIncusArchive(ctx, root, export, cfg)
		if err != nil {
			return err
		}
		if export.Kind != "incus_instance_export" {
			continue
		}
		references, err := incusArchiveDiskReferences(index, export)
		if err != nil {
			return err
		}
		for _, reference := range references {
			if err := ctx.Err(); err != nil {
				return err
			}
			selected := -1
			for at := range volumes {
				object := exports[at].Native
				if object.Pool != reference.pool || object.Name != reference.name || object.Project != export.Native.Project && object.Project != "default" || !slices.Contains(exports[at].Resources, export.Resources[0]) {
					continue
				}
				if selected != -1 {
					return invalid
				}
				selected = at
			}
			if selected == -1 {
				return invalid
			}
			volumes[selected][export.Resources[0]] = true
		}
	}
	for at, consumers := range volumes {
		if len(consumers) != len(exports[at].Resources) {
			return invalid
		}
		for resource := range consumers {
			if !slices.Contains(exports[at].Resources, resource) {
				return invalid
			}
		}
	}
	return ctx.Err()
}
