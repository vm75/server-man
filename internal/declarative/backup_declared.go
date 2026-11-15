package declarative

import (
	"maps"
	"slices"
)

// Declared file inputs are shared by source inventory and offline bundle
// coverage checks. Paths here are configuration-relative, except explicit
// external storage; inventory resolves them against its selected manifest.
func declaredBackupInputs(cfg Config) []BackupInput {
	inputs := []BackupInput{{ID: "configuration", Kind: "configuration", Strategy: "bundle_copy", Path: "server-man.yaml", BundlePath: "server-man.yaml", Resources: []string{"configuration"}, Required: true, Sensitive: true}}
	for _, id := range slices.Sorted(maps.Keys(cfg.Storage)) {
		storage := cfg.Storage[id]
		kind := storage.Kind
		if kind == "" {
			kind = "directory"
		}
		resources := append([]string{"storage/" + id}, storageConsumers(cfg, id)...)
		input := BackupInput{ID: "storage/" + id, Kind: kind, Strategy: "bundle_copy", Path: storage.Path, BundlePath: storage.Path, Resources: resources, Required: true, Sensitive: true}
		if storage.External {
			input.Strategy, input.BundlePath, input.Inspection = "external_data", "", "excluded_pending_review"
		}
		inputs = append(inputs, input)
		if storage.Source != nil {
			inputs = append(inputs, BackupInput{ID: "asset/" + id, Kind: "image_asset", Strategy: "bundle_copy", Path: storage.Source.Path, BundlePath: storage.Source.Path, Resources: resources, Required: true, Sensitive: true})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(cfg.Secrets)) {
		resources := []string{"secret/" + id}
		for _, serviceID := range slices.Sorted(maps.Keys(cfg.Services)) {
			if slices.Contains(serviceDataReferences(cfg.Services[serviceID]), "secret/"+id) {
				resources = append(resources, "service/"+serviceID)
			}
		}
		path := cfg.Secrets[id]
		inputs = append(inputs, BackupInput{ID: "secret/" + id, Kind: "private_file", Strategy: "bundle_copy", Path: path, BundlePath: path, Resources: resources, Required: true, Sensitive: true})
	}
	return inputs
}
