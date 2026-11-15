package declarative

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"server-man/internal/core"
)

func (i *Incus) BackupDependencies(ctx context.Context, c Config, key string) (NativeBackupDependencies, error) {
	result := NativeBackupDependencies{}
	_, id, _ := strings.Cut(key, "/")
	if i.Program == "" || i.Runner == nil {
		return result, errors.New("Incus backup discovery is unavailable")
	}
	project, err := incusProject(c, id)
	if err != nil {
		return result, err
	}
	var server struct {
		Environment struct {
			Name      string `json:"server_name"`
			Clustered *bool  `json:"server_clustered"`
		} `json:"environment"`
	}
	if err = i.get(ctx, "", "/1.0", &server); err != nil {
		return result, err
	}
	if server.Environment.Clustered == nil || *server.Environment.Clustered || server.Environment.Name == "" {
		return result, &backupDiscoveryError{"Backup discovery requires a verified local non-clustered Incus daemon.", "Verify local daemon access and scope; cluster exports require a separate explicitly supported workflow."}
	}
	actual, exists, err := i.read(ctx, c, key)
	if err != nil {
		return result, err
	}
	result.Object = &NativeBackupObject{Backend: "incus", Name: incusInstance(id), Project: project}
	if !exists {
		result.State = "not_created"
		result.issue(key, "backup_guest_absent", "The configured Incus guest is not registered.", "Review whether this is an unprovisioned definition or missing guest storage before backup.")
		return result, nil
	}
	if actual.Location != "" && actual.Location != "none" && actual.Location != server.Environment.Name {
		return result, errors.New("Incus instance is not local to the verified daemon")
	}
	switch actual.StatusCode {
	case 102:
		result.State = "stopped"
	case 103:
		result.State = "running"
	default:
		result.State = "unknown"
		result.issue(key, "backup_native_state", "The native guest is in a transitional or unrecognized state.", "Inspect and stabilize its state before choosing a backup boundary.")
	}
	devices := actual.Devices
	if actual.ExpandedDevices != nil {
		devices = actual.ExpandedDevices
	} else if len(actual.Profiles) > 0 {
		return result, errors.New("expanded profile devices are unavailable")
	}
	if len(devices) > 64 {
		return result, errors.New("Incus backup device limit exceeded")
	}
	if len(actual.Profiles) > 0 {
		result.issue(key, "backup_native_profiles", "Native profiles add settings outside the central definition.", "Review and preserve native dependencies before reconciling profile-free managed definitions.")
	}
	knownInstances := map[string]string{}
	for _, otherID := range slices.Sorted(maps.Keys(c.Services)) {
		other := c.Services[otherID]
		if other.Kind != "vm" || other.Backend != "incus" {
			continue
		}
		otherProject, e := incusProject(c, otherID)
		if e == nil {
			knownInstances[otherProject+incusPath(otherID)] = otherID
		}
	}
	roots := 0
	volumeProject := ""
	projectChecked := false
	seen := map[string]bool{}
	for _, deviceID := range slices.Sorted(maps.Keys(devices)) {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		device := devices[deviceID]
		if device["type"] != "disk" {
			if device["type"] != "nic" {
				result.issue(key, "backup_native_devices", "A native passthrough/non-disk device needs an explicit backup policy.", "Review external persistent state before completing a guest backup.")
			}
			continue
		}
		if device["path"] == "/" {
			roots++
			if !bindingNamePattern.MatchString(device["pool"]) || device["source"] != "" {
				return result, errors.New("unsupported Incus root storage")
			}
			result.Object.Pool = device["pool"]
			result.Object.ContentType = "block"
			continue
		}
		pool, source := device["pool"], device["source"]
		if pool == "" {
			if nativeBackupPath(source) {
				result.Inputs = append(result.Inputs, BackupInput{ID: "native/" + id + "/host/" + digest([]byte(source))[:16], Kind: "incus_host_disk", Strategy: "external_data", Path: source, Resources: []string{key}, Required: true, Sensitive: true, Inspection: "excluded_pending_review"})
			}
			result.issue(key, "backup_incus_host_disk", "A native disk uses host/external data outside the managed storage bindings.", "Declare and review portable inclusion of this data; an instance export does not include host mounts.")
			continue
		}
		volume, subpath, hasSubpath := strings.Cut(source, "/")
		if !bindingNamePattern.MatchString(pool) || !bindingNamePattern.MatchString(volume) || (hasSubpath && !portablePath(subpath)) {
			return result, errors.New("unsupported Incus custom-volume source")
		}
		if !projectChecked {
			var info struct {
				Name   string            `json:"name"`
				Config map[string]string `json:"config"`
			}
			if err = i.get(ctx, "", "/1.0/projects/"+url.PathEscape(project), &info); err != nil {
				return result, err
			}
			if info.Name != project {
				return result, errors.New("Incus project identity cannot be verified")
			}
			switch info.Config["features.storage.volumes"] {
			case "true":
				volumeProject = project
			case "false", "":
				volumeProject = "default"
			default:
				return result, errors.New("Incus storage project feature is unknown")
			}
			projectChecked = true
		}
		identity := volumeProject + "/" + pool + "/" + volume
		if seen[identity] {
			continue
		}
		seen[identity] = true
		var info struct {
			Name        string   `json:"name"`
			Type        string   `json:"type"`
			ContentType string   `json:"content_type"`
			UsedBy      []string `json:"used_by"`
		}
		path := "/1.0/storage-pools/" + url.PathEscape(pool) + "/volumes/custom/" + url.PathEscape(volume)
		if err = i.get(ctx, volumeProject, path, &info); err != nil {
			return result, err
		}
		if info.Name != volume || info.Type != "custom" || (info.ContentType != "filesystem" && info.ContentType != "block" && info.ContentType != "iso") || len(info.UsedBy) > 1024 {
			return result, errors.New("unsupported Incus volume metadata")
		}
		resources := []string{key}
		usageConfirmed := false
		for _, usage := range info.UsedBy {
			uri, e := url.Parse(usage)
			if e != nil || uri.Scheme != "" || uri.Host != "" || !strings.HasPrefix(uri.Path, "/1.0/instances/") {
				result.issue(key, "backup_volume_users", "A custom volume has consumers outside the inventoried instances.", "Inspect and quiesce every native consumer, including profile/unmanaged users, before export.")
				continue
			}
			usedProject := uri.Query().Get("project")
			if usedProject == "" {
				usedProject = "default"
			}
			if err = ctx.Err(); err != nil {
				return result, err
			}
			otherID, found := knownInstances[usedProject+uri.Path]
			if found {
				resources = append(resources, "service/"+otherID)
				if otherID == id {
					usageConfirmed = true
				}
			}

			if !found {
				result.issue(key, "backup_volume_users", "A shared custom volume is used by an unmanaged or differently scoped instance.", "Coordinate that writer explicitly; stopping the selected managed VM is not sufficient.")
			}
		}
		if !usageConfirmed {
			result.issue(key, "backup_volume_usage_unverified", "Native volume usage did not confirm this guest's attachment.", "Recheck native device/volume metadata before choosing a consistent export boundary.")
		}
		result.Inputs = append(result.Inputs, BackupInput{ID: "native/" + id + "/volume/" + digest([]byte(identity))[:16], Kind: "incus_custom_volume", Strategy: "native_export", Resources: resources, Required: true, Sensitive: true, Inspection: "native_volume_discovered", Native: &NativeBackupObject{Backend: "incus", Name: volume, Project: volumeProject, Pool: pool, ContentType: info.ContentType}})
		result.require(key, "custom_volume_export", "Export each attached custom volume separately in portable native format, including snapshots where required; shared-volume writers must be quiesced together.")
	}
	if roots != 1 {
		return result, errors.New("Incus VM root disk identity is missing or ambiguous")
	}
	result.require(key, "instance_export", "Validate native export capabilities and prepare a stopped portable instance export, retaining guest identity/state; never use internal daemon storage copies or optimized driver-specific exports implicitly.")
	return result, nil
}

func (i *Incus) BackupExport(ctx context.Context, c Config, key string, bundleDir string) ([]BackupExport, error) {
	_, id, _ := strings.Cut(key, "/")
	service, ok := c.Services[id]
	if !ok || service.VM == nil || service.Backend != "incus" {
		return nil, nil
	}
	if i.Program == "" || i.Runner == nil {
		return nil, errors.New("Incus export is unavailable")
	}
	project, err := incusProject(c, id)
	if err != nil {
		return nil, err
	}
	var exports []BackupExport
	instName := incusInstance(id)
	tarRel := filepath.ToSlash(filepath.Join("native", "incus", instName+".tar.gz"))
	tarDst := filepath.Join(bundleDir, "native", "incus", instName+".tar.gz")
	_ = os.MkdirAll(filepath.Dir(tarDst), 0700)

	args := []string{"export", instName, tarDst}
	if project != "" {
		args = append(args, "--project", project)
	}
	cmd := core.Command{Program: i.Program, Args: args, Timeout: 10 * time.Minute}
	if _, err := i.Runner.Capture(ctx, cmd); err != nil {
		return nil, fmt.Errorf("Incus instance export failed: %w", err)
	}
	info, err := os.Stat(tarDst)
	if err != nil {
		return nil, err
	}
	h, err := fileHash(tarDst)
	if err != nil {
		return nil, err
	}
	exports = append(exports, BackupExport{
		BundlePath: tarRel,
		SizeBytes:  info.Size(),
		SHA256:     h,
		Kind:       "incus_instance_export",
		Native:     &NativeBackupObject{Backend: "incus", Name: instName, Project: project},
	})

	deps, err := i.BackupDependencies(ctx, c, key)
	if err == nil {
		for _, input := range deps.Inputs {
			if input.Strategy == "native_export" && input.Native != nil && input.Native.Pool != "" {
				volRel := filepath.ToSlash(filepath.Join("native", "incus", "volumes", input.Native.Pool, input.Native.Name+".tar.gz"))
				volDst := filepath.Join(bundleDir, volRel)
				_ = os.MkdirAll(filepath.Dir(volDst), 0700)
				vArgs := []string{"storage", "volume", "export", input.Native.Pool, input.Native.Name, volDst}
				if input.Native.Project != "" {
					vArgs = append(vArgs, "--project", input.Native.Project)
				}
				vCmd := core.Command{Program: i.Program, Args: vArgs, Timeout: 10 * time.Minute}
				if _, err := i.Runner.Capture(ctx, vCmd); err == nil {
					if vInfo, err := os.Stat(volDst); err == nil {
						if vH, err := fileHash(volDst); err == nil {
							exports = append(exports, BackupExport{
								BundlePath: volRel,
								SizeBytes:  vInfo.Size(),
								SHA256:     vH,
								Kind:       "incus_volume_export",
								Native:     input.Native,
							})
						}
					}
				}
			}
		}
	}
	return exports, nil
}

