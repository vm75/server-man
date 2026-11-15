package declarative

import (
	"context"
	"errors"
	"maps"
	"slices"
)

type incusDeviceInventory struct {
	devices    map[string]map[string]string
	historical map[string]bool
	snapshots  int
}

// Historical snapshots can retain disks absent from the current VM definition.
// Resolve them through native volume APIs, never through daemon storage paths.
func (i *Incus) backupDeviceInventory(ctx context.Context, cfg Config, id, project string, actual incusInstanceInfo) (incusDeviceInventory, error) {
	invalid := errors.New("Incus historical device inventory is unavailable or unsupported")
	current := &incusArchiveGuest{Devices: actual.Devices, ExpandedDevices: actual.ExpandedDevices, Profiles: actual.Profiles}
	devices, err := incusBackupDevices(current)
	if err != nil {
		return incusDeviceInventory{}, err
	}
	result := incusDeviceInventory{devices: maps.Clone(devices), historical: map[string]bool{}}
	if result.devices == nil {
		result.devices = map[string]map[string]string{}
	}
	var snapshots []*incusArchiveGuest
	if err := i.get(ctx, project, incusPath(id)+"/snapshots?recursion=1", &snapshots); err != nil {
		return result, invalid
	}
	if snapshots == nil || len(snapshots) > 2048 {
		return result, invalid
	}
	result.snapshots = len(snapshots)
	names := map[string]bool{}
	seen := map[incusDiskReference]bool{}
	for _, device := range devices {
		if device["type"] == "disk" {
			ref, err := parseIncusBackupDisk(device)
			if err == nil {
				seen[ref] = true
			}
		}
	}
	for _, snapshot := range snapshots {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if snapshot == nil || !bindingNamePattern.MatchString(snapshot.Name) || names[snapshot.Name] || !incusArchiveGuestOwned(snapshot, id, cfg.Services[id].VM.UUID) {
			return result, invalid
		}
		names[snapshot.Name] = true
		devices, err := incusBackupDevices(snapshot)
		if err != nil {
			return result, invalid
		}
		roots := 0
		for _, deviceID := range slices.Sorted(maps.Keys(devices)) {
			device := devices[deviceID]
			if device["type"] == "nic" {
				continue
			}
			if device["type"] == "disk" {
				reference, err := parseIncusBackupDisk(device)
				if err != nil {
					return result, invalid
				}
				if reference.kind == "root" {
					roots++
					continue
				}
				if seen[reference] {
					continue
				}
				seen[reference] = true
			}
			key := "snapshot/" + snapshot.Name + "/" + deviceID
			result.devices[key] = device
			result.historical[key] = true
			if len(result.devices) > 2048 {
				return result, invalid
			}
		}
		if roots != 1 {
			return result, invalid
		}
	}
	return result, nil
}
