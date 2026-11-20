package declarative

import (
	"maps"
	"slices"
)

// serviceDataReferences is the shared list used by fingerprints, startup
// diagnostics, provisioning and backup inventory. Keep model dependencies here.
func serviceDataReferences(service Service) []string {
	refs := map[string]bool{}
	for _, container := range service.Containers {
		for _, mount := range container.Mounts {
			refs["storage/"+mount.Storage] = true
		}
		for _, reference := range container.EnvFiles {
			refs["secret/"+reference] = true
		}
	}
	if service.VM != nil {
		for _, disk := range service.VM.Disks {
			refs["storage/"+disk] = true
		}
		if service.VM.Firecracker != nil { refs["storage/"+service.VM.Firecracker.Kernel] = true }
	}
	return slices.Sorted(maps.Keys(refs))
}
