package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"server-man/internal/core"
)

// One inventory object per call ensures shared volumes are exported once and
// every artifact has an exact identity and consumer set for future restore.
func (i *Incus) BackupExport(ctx context.Context, cfg Config, input BackupInput, directory string) (BackupExport, error) {

	command, stream, err := i.exportCommand(ctx, input)
	if err != nil {
		return BackupExport{}, err
	}
	if err := i.verifyExportConsumers(ctx, cfg, input); err != nil {
		return BackupExport{}, err
	}
	object := input.Native
	raw, _ := json.Marshal(object)
	extension := ".tar"
	if object.ContentType == "iso" {
		extension = ".iso"
	}
	path := "native/incus/" + digest(raw) + extension
	export, err := streamBackupArtifact(ctx, command, directory, path, input.Kind, object, stream)
	if err != nil {
		return BackupExport{}, err
	}
	// Recheck scope, ownership, attachments and stopped consumers after streaming.
	if err := i.verifyExportConsumers(ctx, cfg, input); err != nil {
		root, openErr := os.OpenRoot(directory)
		if openErr != nil {
			return BackupExport{}, errors.Join(err, openErr)
		}
		removeErr := root.Remove(path)
		root.Close()
		return BackupExport{}, errors.Join(err, removeErr)
	}
	return export, nil
}
func (i *Incus) ValidateBackupExport(ctx context.Context, _ Config, input BackupInput) error {
	_, _, err := i.exportCommand(ctx, input)
	return err
}
func (i *Incus) exportCommand(ctx context.Context, input BackupInput) (core.Command, core.StreamingExecutor, error) {
	object := input.Native
	if i.Program == "" || i.Runner == nil || object == nil || object.Backend != "incus" || object.Project == "" || !bindingNamePattern.MatchString(object.Project) || !bindingNamePattern.MatchString(object.Pool) || len(input.Resources) == 0 || len(input.Resources) > 128 {
		return core.Command{}, nil, errors.New("Incus export identity, scope or runtime is unavailable")
	}
	stream, ok := i.Runner.(core.StreamingExecutor)
	if !ok {
		return core.Command{}, nil, errors.New("Incus export requires separate binary output streaming")
	}
	switch input.Kind {
	case "incus_instance_export":
		if !bindingNamePattern.MatchString(object.Name) || object.ContentType != "block" {
			return core.Command{}, nil, errors.New("unsupported Incus VM export identity")
		}
	case "incus_custom_volume":
		if !bindingNamePattern.MatchString(object.Name) || (object.ContentType != "block" && object.ContentType != "filesystem" && object.ContentType != "iso") {
			return core.Command{}, nil, errors.New("unsupported Incus custom-volume export identity")
		}
	default:
		return core.Command{}, nil, errors.New("unsupported Incus export kind")
	}
	args := []string{"--force-local", "--project", object.Project, "--quiet"}
	helpArgs := []string{"--force-local", "export", "--help"}
	if input.Kind == "incus_instance_export" {
		args = append(args, "export", "local:"+object.Name, "-", "--compression", "none", "--root-only")
	} else {
		args = append(args, "storage", "volume", "export", "local:"+object.Pool, object.Name, "-", "--compression", "none")
		helpArgs = []string{"--force-local", "storage", "volume", "export", "--help"}
	}
	help, err := i.Runner.Capture(ctx, core.Command{Program: i.Program, Args: helpArgs, Timeout: 5 * time.Second, Env: []string{"LC_ALL=C"}})
	if err != nil || !strings.Contains(help, "--compression") || input.Kind == "incus_instance_export" && !strings.Contains(help, "--root-only") {
		return core.Command{}, nil, errors.New("installed Incus client lacks required portable export flags; verify --compression and instance --root-only support")
	}

	return core.Command{Program: i.Program, Args: args, Timeout: 24 * time.Hour}, stream, nil
}

func (i *Incus) verifyExportConsumers(ctx context.Context, cfg Config, input BackupInput) error {
	resources := slices.Compact(slices.Sorted(slices.Values(input.Resources)))
	for _, key := range resources {
		kind, id, ok := strings.Cut(key, "/")
		service, exists := cfg.Services[id]
		if !ok || kind != "service" || !exists || service.Kind != "vm" || service.Backend != "incus" || service.VM == nil {
			return errors.New("Incus export consumer is not a managed VM")
		}
		fresh, err := i.BackupDependencies(ctx, cfg, key)
		if err != nil {
			return errors.New("Incus export ownership or dependency checks failed")
		}
		if fresh.State != "stopped" {
			return errors.New("all Incus export consumers must be verified stopped")
		}
		for _, issue := range fresh.Issues {
			if issue.Severity != "warning" {
				return errors.New("Incus export has unresolved native dependencies")
			}
		}
		found := input.Kind == "incus_instance_export" && fresh.Object != nil && *fresh.Object == *input.Native
		for _, dependency := range fresh.Inputs {
			if dependency.Kind == input.Kind && dependency.Native != nil && *dependency.Native == *input.Native {
				users := slices.Compact(slices.Sorted(slices.Values(dependency.Resources)))
				if !slices.Equal(users, resources) {
					return errors.New("Incus volume consumers changed since inventory")
				}
				found = true
			}
		}
		if !found {
			return errors.New("Incus export object no longer matches inventory")
		}
	}
	return nil
}
