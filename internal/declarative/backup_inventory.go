package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// BackupInput describes required inputs, not a completed snapshot. Native state
// and directory trees need dedicated collection before any bundle can be complete.
type BackupInput struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Strategy       string   `json:"strategy"`
	Path           string   `json:"path,omitempty"`
	BundlePath     string   `json:"bundlePath,omitempty"`
	Resources      []string `json:"resources"`
	Required       bool     `json:"required"`
	Sensitive      bool     `json:"sensitive"`
	IncludedBy     string   `json:"includedBy,omitempty"`
	Inspection     string   `json:"inspection"`
	LogicalBytes   *int64   `json:"logicalBytes,omitempty"`
	AllocatedBytes *int64   `json:"allocatedBytes,omitempty"`
}
type BackupRequirement struct {
	Resource    string `json:"resource"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}
type BackupExclusion struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}
type BackupInventory struct {
	Stage        string              `json:"stage"`
	Revision     string              `json:"revision"`
	CheckedAt    time.Time           `json:"checkedAt"`
	Inputs       []BackupInput       `json:"inputs"`
	Requirements []BackupRequirement `json:"requirements"`
	Exclusions   []BackupExclusion   `json:"exclusions"`
	Issues       []Issue             `json:"issues"`
}

func (c Coordinator) BackupInventory(ctx context.Context) (BackupInventory, error) {
	snapshot, err := c.Store.Read()
	if err != nil {
		return BackupInventory{}, errors.New("a valid private configuration is required to inventory a backup")
	}
	rootPath := filepath.Dir(c.Store.Path)
	rootHandle, err := os.OpenRoot(rootPath)
	if err != nil {
		return BackupInventory{}, errors.New("configuration root cannot be inspected")
	}
	defer rootHandle.Close()
	cfg := snapshot.Config
	result := BackupInventory{Stage: "inventory", Revision: snapshot.Revision, CheckedAt: time.Now().UTC(), Inputs: []BackupInput{}, Requirements: []BackupRequirement{}, Exclusions: []BackupExclusion{
		{"derived_native_state", "Generated Quadlet/Compose/XML/share fragments, apply journals and caches must be regenerated from authoritative configuration."},
		{"configuration_history", "Only the selected current configuration revision is inventoried; private revision history is excluded."},
		{"host_configuration", "Bootstrap authentication, packages, system services, host accounts, credentials and network/pool setup remain host requirements."},
		{"container_writable_layers", "Only declared persistent storage is included. Container writable layers, unmanaged volumes and local engine image caches are excluded."},
	}, Issues: []Issue{}}
	addIssue := func(severity, resource, field, code, message, remedy string) {
		result.Issues = append(result.Issues, Issue{severity, resource, field, code, message, remedy})
	}
	limitExceeded := false
	addRequirement := func(resource, kind, description string) {
		if len(result.Inputs)+len(result.Requirements) >= 2048 {
			limitExceeded = true
			return
		}
		result.Requirements = append(result.Requirements, BackupRequirement{resource, kind, description})
	}
	addInput := func(input BackupInput) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(result.Inputs)+len(result.Requirements) >= 2048 {
			return errors.New("backup inventory entry limit exceeded")
		}
		input.Resources = slices.Compact(slices.Sorted(slices.Values(input.Resources)))
		if input.Path != "" && input.Strategy != "external_data" {
			relative, e := filepath.Rel(rootPath, input.Path)
			var info os.FileInfo
			if e == nil {
				info, e = rootHandle.Lstat(relative)
			}
			if e != nil {
				input.Inspection = "unavailable"
				addIssue("error", input.ID, "path", "backup_path_unavailable", "A required backup input is missing, inaccessible or outside the allowed root.", "Restore or rebind the input before preparing a complete backup.")
			} else if info.Mode()&os.ModeSymlink != 0 {
				input.Inspection = "unverified_symlink"
				addIssue("unknown", input.ID, "path", "backup_symlink", "A backup input is a symlink; portable target coverage has not been established.", "Review its target and link metadata before preparing the bundle; never follow links outside declared data implicitly.")
			} else if input.Kind == "directory" && !info.IsDir() || input.Kind != "directory" && !info.Mode().IsRegular() {
				input.Inspection = "wrong_type"
				addIssue("error", input.ID, "path", "backup_path_type", "A backup input has the wrong file type.", "Select the required directory or regular file.")
			} else {
				input.Inspection = "metadata_only"
				if !info.IsDir() {
					size := info.Size()
					input.LogicalBytes = &size
					if stat, ok := info.Sys().(*syscall.Stat_t); ok {
						allocated := stat.Blocks * 512
						input.AllocatedBytes = &allocated
					}
				}
				mode := uint32(4)
				if info.IsDir() {
					mode |= 1
				}
				if syscall.Access(input.Path, mode) != nil {
					addIssue("error", input.ID, "path", "backup_access", "The application account cannot read or traverse a required backup input.", "Provide explicit read access; backup inventory never changes ownership or permissions.")
				}
				if input.Sensitive && input.Kind == "private_file" && info.Mode().Perm()&0077 != 0 {
					addIssue("error", input.ID, "path", "backup_private_permissions", "A referenced private file allows group/other access.", "Restore owner-only permissions before backup preparation.")
				}
				if input.Kind == "directory" {
					addRequirement(input.ID, "tree_scan", "Recursively inventory contents and validate links, filesystem boundaries and metadata with bounded scanning before copy.")
				}
			}
		}
		result.Inputs = append(result.Inputs, input)
		return nil
	}
	addRequirement("bundle", "private_destination", "The prepared bundle will contain private configuration/data and referenced secrets; require an owner-only destination and never expose contents through routine APIs/logs.")
	addRequirement("bundle", "filesystem_metadata", "Preserve/review UID/GID mappings, ACLs, extended attributes, SELinux labels, links and sparse-file allocation; metadata-only inspection does not establish complete coverage.")
	if err = addInput(BackupInput{ID: "configuration", Kind: "configuration", Strategy: "bundle_copy", Path: c.Store.Path, BundlePath: "server-man.yaml", Resources: []string{"configuration"}, Required: true, Sensitive: true}); err != nil {
		return result, err
	}
	root := filepath.Dir(c.Store.Path)
	for _, id := range slices.Sorted(maps.Keys(cfg.Storage)) {
		storage := cfg.Storage[id]
		kind := storage.Kind
		if kind == "" {
			kind = "directory"
		}
		resources := append([]string{"storage/" + id}, storageConsumers(cfg, id)...)
		strategy, bundlePath, inspection := "bundle_copy", storage.Path, ""
		if storage.External {
			strategy, bundlePath, inspection = "external_data", "", "excluded_pending_review"
			addIssue("unknown", "storage/"+id, "external", "backup_external_data", "External storage is required but excluded until an explicit inclusion/rebinding decision is made.", "Review how this data will be backed up and mapped on the destination host.")
		}
		if err = addInput(BackupInput{ID: "storage/" + id, Kind: kind, Strategy: strategy, Path: c.Store.Resolve(storage.Path), BundlePath: bundlePath, Resources: resources, Required: true, Sensitive: true, Inspection: inspection}); err != nil {
			return result, err
		}
		if storage.Mount != "" {
			addRequirement("storage/"+id, "expected_mount", "Verify the declared mounted filesystem before reading data; an empty mountpoint cannot substitute for it.")
		}
		if storage.Kind == "file" && storage.Format != "" {
			addRequirement("storage/"+id, "disk_dependencies", "Inspect disk format, backing chains, external data and sparse allocation before copying; a single filename does not establish complete guest data.")
		}
		if storage.Source != nil {
			if err = addInput(BackupInput{ID: "asset/" + id, Kind: "image_asset", Strategy: "bundle_copy", Path: c.Store.Resolve(storage.Source.Path), BundlePath: storage.Source.Path, Resources: resources, Required: true, Sensitive: true}); err != nil {
				return result, err
			}
			addRequirement("asset/"+id, "asset_checksum", "Verify the configured initial-image checksum during backup preparation; metadata inspection does not read image contents.")
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
		if err = addInput(BackupInput{ID: "secret/" + id, Kind: "private_file", Strategy: "bundle_copy", Path: c.Store.Resolve(path), BundlePath: path, Resources: resources, Required: true, Sensitive: true}); err != nil {
			return result, err
		}
	}
	for _, key := range orderedResources(cfg) {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		kind, id, _ := strings.Cut(key, "/")
		if kind == "share" {
			addRequirement(key, "share_host_setup", "Recreate explicit native share setup, access identities/credentials and directory permission/ACL/SELinux requirements on the destination.")
			if cfg.Shares[id].Enabled {
				addRequirement(key, "share_quiescence", "Coordinate client writes and quiesce the published share before consistent data capture; unpublication alone does not disconnect existing Samba sessions.")
			}
			continue
		}
		service := cfg.Services[id]
		addRequirement(key, "workload_consistency", "Inspect current state and stop/quiesce this workload when preparing its data; record prior running state for controlled resume.")
		if service.Kind == "container" {
			for _, containerID := range slices.Sorted(maps.Keys(service.Containers)) {
				addRequirement(key, "container_image", "Resolve the configured image for container "+containerID+" and review exact image availability and registry credentials for restore; image caches are not inventoried as data.")
			}
			continue
		}
		nativeKind := "libvirt_guest_state"
		if service.Backend == "incus" {
			nativeKind = "incus_instance_export"
		}
		if err = addInput(BackupInput{ID: "native/" + id, Kind: nativeKind, Strategy: "native_export", Resources: []string{key}, Required: true, Sensitive: true, Inspection: "native_dependencies_unchecked"}); err != nil {
			return result, err
		}
		remedy := "Enumerate the owned domain's NVRAM/TPM, backing chains and other guest state before collecting a stopped, consistent export."
		if service.Backend == "incus" {
			remedy = "Enumerate the owned local instance and attached custom volumes, then use native portable exports; never copy internal daemon storage."
		}
		addIssue("unknown", key, "native_state", "backup_native_dependencies", "Native guest dependencies have not yet been enumerated for this backup inventory.", remedy)
	}
	for _, id := range slices.Sorted(maps.Keys(cfg.Bindings)) {
		addRequirement("binding/"+id, "host_binding", "Review/rebind the declared "+cfg.Bindings[id].Kind+" and recreate its native host setup before activation on the destination.")
	}
	// Mark overlapping paths as covered by a selected directory or exact prior
	// input. Future copy code must not duplicate secret files or shared storage.
	indices := make([]int, 0, len(result.Inputs))
	for index, input := range result.Inputs {
		if input.Strategy == "bundle_copy" && input.Inspection == "metadata_only" {
			indices = append(indices, index)
		}
	}
	slices.SortFunc(indices, func(a, b int) int {
		left, right := result.Inputs[a], result.Inputs[b]
		if len(left.Path) != len(right.Path) {
			return len(left.Path) - len(right.Path)
		}
		return strings.Compare(left.ID, right.ID)
	})
	for position, index := range indices {
		input := &result.Inputs[index]
		for _, parentIndex := range indices[:position] {
			parent := result.Inputs[parentIndex]
			if parent.IncludedBy != "" {
				continue
			}
			relative, e := filepath.Rel(parent.Path, input.Path)
			if e == nil && (relative == "." || parent.Kind == "directory" && relative != ".." && !strings.HasPrefix(relative, "../")) {
				input.IncludedBy = parent.ID
				break
			}
		}
		relative, e := filepath.Rel(root, input.Path)
		if e != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			addIssue("error", input.ID, "path", "backup_path_escape", "An owned backup input escapes the configuration root.", "Declare external storage explicitly and review its inclusion.")
		}
		first := strings.Split(input.BundlePath, string(filepath.Separator))[0]
		if input.ID != "configuration" && strings.HasPrefix(first, filepath.Base(c.Store.Path)+".") {
			addIssue("error", input.ID, "path", "backup_derived_state_collision", "A required data input overlaps state that must be regenerated.", "Rebind workload data away from generated definitions, history and apply journals.")
		}
		if input.ID != "configuration" && (input.Path == c.Store.Path || input.BundlePath == "server-man.yaml") {
			addIssue("error", input.ID, "path", "backup_configuration_collision", "A data reference collides with the authoritative configuration in the bundle.", "Rebind data/private references away from the configuration file.")
		}
	}
	if limitExceeded || len(result.Inputs)+len(result.Requirements) > 2048 {
		return result, errors.New("backup inventory entry limit exceeded")
	}
	if _, revision, e := c.Store.ReadRaw(); e != nil || revision != snapshot.Revision {
		return result, ErrConflict
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > MaxConfigBytes {
		return result, errors.New("backup inventory output limit exceeded")
	}
	return result, nil
}
