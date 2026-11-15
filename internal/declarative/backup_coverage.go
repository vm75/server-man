package declarative

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Coverage is derived from the bundled authoritative configuration, never the
// current host or a second copy of user intent in the manifest. Native archive
// interpretation must additionally establish discovered volume dependencies.
func validateBackupCoverage(ctx context.Context, root *os.Root, m BackupManifest) error {
	invalid := errors.New("backup configuration or declared payload coverage did not verify")
	raw, err := readBackupPrivateFile(ctx, root, "server-man.yaml", MaxConfigBytes, m.Revision)
	if err != nil {
		return err
	}
	cfg, err := Parse(raw)
	if err != nil {
		return invalid
	}
	files := make(map[string]BackupManifestFile, len(m.Files))
	for _, entry := range m.Files {
		files[entry.BundlePath] = entry
	}
	exact, directories := map[string]bool{}, map[string]bool{}
	for _, input := range declaredBackupInputs(cfg) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if input.Strategy != "bundle_copy" {
			return errors.New("backup configuration contains unresolved external data")
		}
		entry, found := files[input.BundlePath]
		if !found {
			return invalid
		}
		if input.Kind == "directory" {
			if entry.Type != "directory" {
				return invalid
			}
			directories[input.BundlePath] = true
		} else if entry.Type != "file" && entry.Type != "hardlink" {
			return invalid
		}
		if input.Kind == "private_file" {
			mode, err := strconv.ParseUint(entry.Mode, 8, 12)
			if err != nil || mode&0077 != 0 {
				return invalid
			}
		}
		if strings.HasPrefix(input.ID, "asset/") && entry.SHA256 != cfg.Storage[strings.TrimPrefix(input.ID, "asset/")].Source.SHA256 {
			return invalid
		}
		exact[input.BundlePath] = true
	}
	for path, entry := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		covered := exact[path]
		linkContained := entry.Type != "symlink"
		resolved := filepath.Join(filepath.Dir(path), entry.LinkTarget)
		for parent := filepath.Dir(path); parent != "." && (!covered || !linkContained); parent = filepath.Dir(parent) {
			if directories[parent] {
				covered = true
				if resolved == parent || strings.HasPrefix(resolved, parent+string(filepath.Separator)) {
					linkContained = true
				}
			}
		}
		if !covered || !linkContained {
			return errors.New("backup contains filesystem data not covered by its configuration")
		}
	}
	if err := validateBackupNativeCoverage(ctx, cfg, m.NativeExports); err != nil {
		return err
	}
	for _, export := range m.NativeExports {
		if export.Kind != "libvirt_guest_state" {
			continue
		}
		raw, err := readBackupPrivateFile(ctx, root, export.BundlePath, MaxConfigBytes, export.SHA256)
		if err != nil {
			return err
		}
		id := strings.TrimPrefix(export.Resources[0], "service/")
		if err := validateLibvirtBackupXML(ctx, raw, cfg, id, m.SourceRoot); err != nil {
			return err
		}
	}
	running, resumed := map[string]bool{}, map[string]bool{}
	if len(m.RunningWorkloadsAtBackup) > len(cfg.Services) || len(m.ResumedWorkloads) > len(cfg.Services) || len(m.FailedResume) > len(cfg.Services) {
		return invalid
	}
	for _, id := range m.RunningWorkloadsAtBackup {
		if _, found := cfg.Services[id]; !found || running[id] {
			return invalid
		}
		running[id] = true
	}
	for _, id := range m.ResumedWorkloads {
		if !running[id] || resumed[id] {
			return invalid
		}
		resumed[id] = true
	}
	failed := map[string]bool{}
	for _, id := range m.FailedResume {
		if !running[id] || resumed[id] || failed[id] {
			return invalid
		}
		failed[id] = true
	}
	if len(resumed)+len(failed) != len(running) {
		return invalid
	}
	return ctx.Err()
}

func validateBackupNativeCoverage(ctx context.Context, cfg Config, exports []BackupManifestNativeExport) error {
	invalid := errors.New("backup native export coverage disagrees with its configuration")
	guests := map[string]bool{}
	type identity struct {
		kind   string
		object NativeBackupObject
	}
	objects := map[identity]bool{}
	for _, export := range exports {
		if err := ctx.Err(); err != nil {
			return err
		}
		if export.Native == nil || export.Backend != export.Native.Backend || len(export.Resources) == 0 {
			return invalid
		}
		object := *export.Native
		key := identity{export.Kind, object}
		if objects[key] {
			return invalid
		}
		objects[key] = true
		if export.Kind == "incus_custom_volume" {
			if object.Backend != "incus" || !bindingNamePattern.MatchString(object.Name) || !bindingNamePattern.MatchString(object.Pool) || !bindingNamePattern.MatchString(object.Project) || !slices.Contains([]string{"filesystem", "block", "iso"}, object.ContentType) {
				return invalid
			}
			consumers := map[string]bool{}
			for _, resource := range export.Resources {
				kind, id, ok := strings.Cut(resource, "/")
				service, found := cfg.Services[id]
				if !ok || kind != "service" || !found || service.Kind != "vm" || service.Backend != "incus" || consumers[resource] {
					return invalid
				}
				consumers[resource] = true
			}
			continue
		}
		if len(export.Resources) != 1 {
			return invalid
		}
		kind, id, ok := strings.Cut(export.Resources[0], "/")
		service, found := cfg.Services[id]
		if !ok || kind != "service" || !found || service.Kind != "vm" || guests[id] {
			return invalid
		}
		switch service.Backend {
		case "libvirt":
			if export.Kind != "libvirt_guest_state" || object != (NativeBackupObject{Backend: "libvirt", Name: vmDomainName(id)}) {
				return invalid
			}
		case "incus":
			project, err := incusProject(cfg, id)
			pool, found := cfg.Bindings[service.VM.Pool]
			if err != nil || !found || pool.Kind != "pool" || export.Kind != "incus_instance_export" || object != (NativeBackupObject{Backend: "incus", Name: incusInstance(id), Project: project, Pool: pool.Name, ContentType: "block"}) {
				return invalid
			}
		default:
			return invalid
		}
		guests[id] = true
	}
	for id, service := range cfg.Services {
		if service.Kind == "vm" && !guests[id] {
			return invalid
		}
	}
	return ctx.Err()
}
