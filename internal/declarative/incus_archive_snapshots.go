package declarative

import "strings"

// Historical guest settings may differ from today's desired hardware. Validate
// snapshot membership and ownership without rewriting or comparing fingerprints
// to the current definition. Target state/device compatibility is a restore gate.
func incusSnapshotRecordsMatch(index incusArchiveIndex, names map[string]bool, export BackupManifestNativeExport, cfg Config) bool {
	if len(index.Config.VolumeSnapshots) != len(names) {
		return false
	}
	volumes := map[string]bool{}
	for _, snapshot := range index.Config.VolumeSnapshots {
		if snapshot == nil || !names[snapshot.Name] || volumes[snapshot.Name] || snapshot.ContentType != export.Native.ContentType || snapshot.Project != "" && snapshot.Project != export.Native.Project {
			return false
		}
		volumes[snapshot.Name] = true
	}
	if export.Kind == "incus_custom_volume" {
		return index.Config.Container == nil && len(index.Config.Snapshots) == 0
	}
	if len(index.Config.Snapshots) != len(names) || len(export.Resources) != 1 {
		return false
	}
	id := strings.TrimPrefix(export.Resources[0], "service/")
	service, found := cfg.Services[id]
	if !found || service.VM == nil {
		return false
	}
	guests := map[string]bool{}
	for _, snapshot := range index.Config.Snapshots {
		if snapshot == nil || !names[snapshot.Name] || guests[snapshot.Name] || !incusArchiveGuestOwned(snapshot, id, service.VM.UUID) {
			return false
		}
		guests[snapshot.Name] = true
	}
	return true
}

func incusArchiveGuestOwned(guest *incusArchiveGuest, id, uuid string) bool {
	return guest != nil && uuid != "" && guest.Config["user.server-man.id"] == id && guest.Config["user.server-man.identity"] == uuid
}
