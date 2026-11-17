package declarative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	"server-man/internal/core"
)

const (
	incusRuntimeKey       = "user.server-man.runtime"
	incusActiveRuntimeKey = "user.server-man.active-runtime"
)

type Incus struct {
	Store                    Store
	Program, QEMU, Directory string
	Runner                   core.Executor
}

func NewIncus(store Store, runner core.Executor) *Incus {
	program, _ := exec.LookPath("incus")
	qemuName := map[string]string{"amd64": "qemu-system-x86_64", "arm64": "qemu-system-aarch64"}[runtime.GOARCH]
	qemu, _ := exec.LookPath(qemuName)
	return &Incus{Store: store, Program: program, QEMU: qemu, Directory: store.Path + ".generated/incus", Runner: runner}
}
func (i *Incus) queryCommand(project, path string) core.Command {
	if project != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "project=" + url.QueryEscape(project)
	}
	return core.Command{Program: i.Program, Args: []string{"--force-local", "query", "--request", "GET", "--raw", "local:" + path}, Timeout: 5 * time.Second}
}
func (i *Incus) get(ctx context.Context, project, path string, dest any) error {
	cmd := i.queryCommand(project, path)
	output, err := i.Runner.Capture(ctx, cmd)
	if err != nil {
		return errors.New("Incus local query failed")
	}
	var response struct {
		Type      string          `json:"type"`
		ErrorCode int             `json:"error_code"`
		Metadata  json.RawMessage `json:"metadata"`
	}
	if json.Unmarshal([]byte(output), &response) != nil || response.Type != "sync" || response.ErrorCode != 0 {
		return errors.New("invalid or failed Incus response")
	}
	if json.Unmarshal(response.Metadata, dest) != nil {
		return errors.New("unsupported Incus response metadata")
	}
	return nil
}

// Native init/config edit accept JSON (YAML subset) on stdin and wait for the
// operation. This avoids query flags absent from packaged Incus clients and
// keeps private definition payloads out of process arguments.
func (i *Incus) run(ctx context.Context, project string, definition *incusDefinition, args ...string) error {
	cmd := core.Command{Program: i.Program, Args: append([]string{"--force-local", "--project", project}, args...), Timeout: 20 * time.Minute}
	if definition != nil {
		data, err := json.Marshal(definition)
		if err != nil {
			return err
		}
		cmd.Stdin = bytes.NewReader(data)
	}
	if err := i.Runner.Run(ctx, cmd, io.Discard); err != nil {
		return errors.New("Incus native operation failed")
	}
	return nil
}

type incusInstanceInfo struct {
	incusDefinition
	ExpandedDevices map[string]map[string]string `json:"expanded_devices"`
	Location        string                       `json:"location"`
	Name            string                       `json:"name"`
	Type            string                       `json:"type"`
	Status          string                       `json:"status"`
	StatusCode      int                          `json:"status_code"`
}

func incusPath(id string) string { return "/1.0/instances/" + url.PathEscape(incusInstance(id)) }
func (i *Incus) read(ctx context.Context, c Config, key string) (incusInstanceInfo, bool, error) {
	_, id, _ := strings.Cut(key, "/")
	project, err := incusProject(c, id)
	if err != nil {
		return incusInstanceInfo{}, false, err
	}
	var instances []string
	if err = i.get(ctx, project, "/1.0/instances", &instances); err != nil {
		return incusInstanceInfo{}, false, err
	}
	if len(instances) > 1024 {
		return incusInstanceInfo{}, false, errors.New("Incus instance limit exceeded")
	}
	found := false
	for _, path := range instances {
		u, e := url.Parse(path)
		if e == nil && u.Path == incusPath(id) {
			found = true
			break
		}
	}
	if !found {
		return incusInstanceInfo{}, false, nil
	}
	var actual incusInstanceInfo
	if err = i.get(ctx, project, incusPath(id), &actual); err != nil {
		return actual, false, err
	}
	s := c.Services[id]
	if actual.Name != incusInstance(id) || actual.Type != "virtual-machine" || actual.Config["user.server-man.id"] != id || actual.Config["user.server-man.identity"] != s.VM.UUID {
		return actual, true, errors.New("Incus instance ownership does not match the desired definition")
	}
	return actual, true, nil
}
func incusMatches(want incusDefinition, actual incusInstanceInfo) bool {
	if actual.Ephemeral || len(actual.Profiles) != 0 || want.Description != actual.Description {
		return false
	}
	if want.Architecture != "" && want.Architecture != actual.Architecture {
		return false
	}
	for k, v := range want.Config {
		if actual.Config[k] != v {
			return false
		}
	}
	return reflect.DeepEqual(want.Devices, actual.Devices)
}
func (i *Incus) Validate(ctx context.Context, c Config, key string) error {
	if i.Program == "" {
		return errors.New("Incus client is required")
	}
	desired, err := RenderIncus(c, key)
	if err != nil {
		return err
	}
	_, id, _ := strings.Cut(key, "/")
	project, _ := incusProject(c, id)
	// Query explicit bindings without relying on profiles or ambient defaults.
	var ignored map[string]any
	if err = i.get(ctx, "", "/1.0/projects/"+url.PathEscape(project), &ignored); err != nil {
		return errors.New("Incus project binding is unavailable")
	}
	if err = i.get(ctx, project, "/1.0/storage-pools/"+url.PathEscape(desired.Devices["root"]["pool"]), &ignored); err != nil {
		return errors.New("Incus storage-pool binding is unavailable")
	}
	if network := desired.Devices["eth0"]["network"]; network != "" {
		if err = i.get(ctx, project, "/1.0/networks/"+url.PathEscape(network), &ignored); err != nil {
			return errors.New("Incus network binding is unavailable")
		}
	}
	return nil
}
func (i *Incus) resourceDir(key string) string {
	_, id, _ := strings.Cut(key, "/")
	return filepath.Join(i.Directory, id)
}
func (i *Incus) Inspect(ctx context.Context, c Config, key string) (Observation, error) {
	desired, err := RenderIncus(c, key)
	if err != nil {
		return Observation{}, err
	}
	actual, exists, err := i.read(ctx, c, key)
	if err != nil {
		return Observation{}, err
	}
	if !exists {
		return Observation{ActiveMatches: true}, nil
	}
	// Incus exposes persistent config but not a general live hardware definition.
	// Stopped publication prepares the marker for native boot; running publication
	// retains the prior marker, conservatively requiring restart for changed settings.
	active := actual.StatusCode != 102 // Incus Stopped
	return Observation{Registered: true, Active: active, Matches: incusMatches(desired, actual), ActiveMatches: !active || actual.Config[incusActiveRuntimeKey] == desired.Config[incusRuntimeKey]}, nil
}
func mergeIncusVolatile(desired incusDefinition, actual incusInstanceInfo) incusDefinition {
	desired.Config = maps.Clone(desired.Config)
	for key, value := range actual.Config {
		if strings.HasPrefix(key, "volatile.") || key == incusActiveRuntimeKey {
			desired.Config[key] = value
		}
	}
	if desired.Architecture == "" {
		desired.Architecture = actual.Architecture
	}
	if actual.Name == "" || actual.StatusCode == 102 {
		// A stopped definition will load these settings even when Incus itself
		// starts it at boot, without a server-man Start operation.
		desired.Config[incusActiveRuntimeKey] = desired.Config[incusRuntimeKey]
	} else if desired.Config[incusActiveRuntimeKey] == "" {
		// Never stamp a running guest with a new desired runtime merely because
		// its prior native start did not leave a tracking marker.
		loaded := actual.Config[incusRuntimeKey]
		if !revisionPattern.MatchString(loaded) {
			loaded = "unverified"
		}
		desired.Config[incusActiveRuntimeKey] = loaded
	}
	return desired
}
func (i *Incus) Apply(ctx context.Context, previous, desired Config, key, mode string) error {
	_, id, _ := strings.Cut(key, "/")
	_, exists := desired.Services[id]
	if !exists {
		_, found, err := i.read(ctx, previous, key)
		if err != nil {
			return err
		}
		if found {
			return errors.New("Incus definition removal requires an explicit data-preserving export workflow")
		}
		// Failed creation has no native data to export. Remove only its checked
		// generated definition, never an instance or storage object.
		old, err := RenderIncus(previous, key)
		if err != nil {
			return err
		}
		data, err := encodeIncus(old)
		if err != nil {
			return err
		}
		return publishArtifacts(i.resourceDir(key), map[string][]byte{"instance.yaml": data}, map[string][]byte{})
	}
	definition, err := RenderIncus(desired, key)
	if err != nil {
		return err
	}
	project, _ := incusProject(desired, id)
	if old, ok := previous.Services[id]; ok && old.VM != nil {
		if old.VM.Image != desired.Services[id].VM.Image {
			return errors.New("changing an existing Incus image requires explicit reprovisioning")
		}
		oldSize, newSize := old.VM.RootSizeGiB, desired.Services[id].VM.RootSizeGiB
		if oldSize == 0 {
			oldSize = 20
		}
		if newSize == 0 {
			newSize = 20
		}
		if newSize < oldSize {
			return errors.New("shrinking Incus root storage is unsupported")
		}
		oldProject, _ := incusProject(previous, id)
		if project != oldProject || incusPoolChanged(desired, previous, id) {
			return errors.New("changing Incus project/pool requires explicit storage relocation")
		}
	}
	actual, found, err := i.read(ctx, desired, key)
	if err != nil {
		return err
	}
	if found && actual.StatusCode != 102 && mode == "now" && actual.Config[incusActiveRuntimeKey] != definition.Config[incusRuntimeKey] {
		return errors.New("running Incus VM updates require shutdown/start or Save for next start")
	}
	before, after := map[string][]byte{}, map[string][]byte{}
	if _, ok := previous.Services[id]; ok {
		old, e := RenderIncus(previous, key)
		if e != nil {
			return e
		}
		data, e := encodeIncus(old)
		if e != nil {
			return e
		}
		before["instance.yaml"] = data
	}
	data, err := encodeIncus(definition)
	if err != nil {
		return err
	}
	after["instance.yaml"] = data
	if err = checkArtifactOwnership(i.resourceDir(key), before, after); err != nil {
		return err
	}
	payload := mergeIncusVolatile(definition, actual)
	if found {
		if err = i.run(ctx, project, &payload, "config", "edit", "local:"+incusInstance(id)); err != nil {
			return err
		}
	} else {
		image := desired.Services[id].VM.Image
		if image == "" || strings.HasPrefix(image, "-") || strings.ContainsAny(image, ":\x00\r\n") {
			return errors.New("initial Incus creation requires a locally available image alias or fingerprint")
		}
		if err = i.run(ctx, project, &payload, "init", "local:"+image, "local:"+incusInstance(id), "--vm", "--no-profiles"); err != nil {
			return err
		}
	}
	return publishArtifacts(i.resourceDir(key), before, after)
}

func (i *Incus) VerifyRemoved(ctx context.Context, previous Config, key string) (bool, error) {
	_, found, err := i.read(ctx, previous, key)
	if err != nil || found {
		return false, err
	}
	entries, err := os.ReadDir(i.resourceDir(key))
	if os.IsNotExist(err) {
		return true, nil
	}
	return err == nil && len(entries) == 0, err
}
func incusPoolChanged(c, previous Config, id string) bool {
	a, b := c.Services[id].VM, previous.Services[id].VM
	if a == nil || b == nil {
		return false
	}
	return c.Bindings[a.Pool] != previous.Bindings[b.Pool]
}
func (i *Incus) Action(ctx context.Context, c Config, key, action string) error {
	_, id, _ := strings.Cut(key, "/")
	definition, err := RenderIncus(c, key)
	if err != nil {
		return err
	}
	actual, exists, err := i.read(ctx, c, key)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("Incus VM does not exist")
	}
	project, _ := incusProject(c, id)
	switch action {
	case "start":
		if actual.StatusCode != 102 {
			return errors.New("Incus VM is not stopped")
		}
		// Store the known start revision without editing volatile native identities.
		payload := mergeIncusVolatile(definition, actual)
		if err = i.run(ctx, project, &payload, "config", "edit", "local:"+incusInstance(id)); err != nil {
			return err
		}
		if err = i.run(ctx, project, nil, "start", "local:"+incusInstance(id)); err != nil {
			return err
		}
		started, found, e := i.read(ctx, c, key)
		if e != nil {
			return e
		}
		if !found || started.StatusCode != 103 {
			return errors.New("Incus VM did not reach running state")
		}
		return nil
	case "stop", "restart":
		if actual.StatusCode != 102 {
			if err = i.run(ctx, project, nil, "stop", "local:"+incusInstance(id), "--timeout", "90"); err != nil {
				return err
			}
		}
		stopped, found, e := i.read(ctx, c, key)
		if e != nil {
			return e
		}
		if !found || stopped.StatusCode != 102 {
			return errors.New("Incus VM did not reach stopped state")
		}
		// Update only tracking metadata, using the native persistent runtime.
		// Stop must not apply a desired hardware change left by a failed save.
		loaded := stopped.Config[incusRuntimeKey]
		if !revisionPattern.MatchString(loaded) {
			return errors.New("Incus VM stopped, but native runtime tracking is invalid; inspect its definition before restart")
		}
		if stopped.Config[incusActiveRuntimeKey] != loaded {
			trackingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = i.run(trackingCtx, project, nil, "config", "set", "local:"+incusInstance(id), incusActiveRuntimeKey+"="+loaded)
			cancel()
			if err != nil {
				return errors.New("Incus VM stopped, but its runtime tracking could not be updated")
			}
		}
		if action == "restart" {
			return i.Action(ctx, c, key, "start")
		}
		return nil
	default:
		return errors.New("unsupported Incus action")
	}
}
func (i *Incus) Prerequisites(ctx context.Context) []Issue {
	if i.Program == "" {
		return []Issue{{"error", "backend/incus", "incus", "missing_dependency", "The Incus client is missing.", "Install Incus through Software and initialize it explicitly."}}
	}
	var server map[string]any
	if err := i.get(ctx, "", "/1.0", &server); err != nil {
		return []Issue{{"error", "backend/incus", "daemon", "incus_unavailable", "The local Incus daemon is unavailable.", "Check daemon setup, the local socket and account permissions."}}
	}
	if i.QEMU == "" {
		return []Issue{{"error", "backend/incus", "qemu", "missing_dependency", "The native QEMU executable required for Incus VMs is missing.", "Install Incus VM dependencies through Software or rerun the installer with Incus selected, then recheck."}}
	}
	// Help only: verifies the modular SPICE option required by Incus without
	// creating a VM, opening KVM, or assuming recommended packages were installed.
	output, err := i.Runner.Capture(ctx, core.Command{Program: i.QEMU, Args: []string{"-spice", "help"}, Env: []string{"LC_ALL=C"}, Timeout: 3 * time.Second})
	available := strings.HasPrefix(output, "spice options:\n") && strings.Contains(output, "\n  unix=<bool") && strings.Contains(output, "\n  disable-ticketing=<bool")
	if err != nil {
		// QEMU prints complete option help to stdout and exits 1. Missing
		// modules instead emit an error without the option declarations.
		var exit *exec.ExitError
		available = available && errors.As(err, &exit) && exit.ExitCode() == 1
	}
	if !available {
		return []Issue{{"error", "backend/incus", "qemu", "qemu_spice_unavailable", "QEMU's SPICE support required for Incus VMs is unavailable.", "Install Incus VM dependencies (including qemu-system-modules-spice on Debian/Ubuntu) through Software or rerun the installer, then recheck."}}
	}
	return nil
}

func (i *Incus) ResourcePrerequisites(ctx context.Context, c Config, key string) []Issue {
	if err := i.Validate(ctx, c, key); err != nil {
		return []Issue{{"error", key, "bindings", "incus_prerequisite", err.Error(), "Check the explicit Incus project, pool, network and supported VM settings."}}
	}
	return nil
}
