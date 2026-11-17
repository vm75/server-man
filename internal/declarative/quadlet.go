package declarative

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"server-man/internal/core"
)

type Quadlet struct {
	Store      Store
	Directory  string
	Generator  string
	Systemctl  string
	Journalctl string
	Podman     string
	Runner     core.Executor
}

func NewQuadlet(store Store, runner core.Executor) *Quadlet {
	config, _ := os.UserConfigDir()
	q := &Quadlet{Store: store, Directory: filepath.Join(config, "containers/systemd/server-man"), Runner: runner}
	q.Systemctl, _ = exec.LookPath("systemctl")
	q.Journalctl, _ = exec.LookPath("journalctl")
	q.Podman, _ = exec.LookPath("podman")
	for _, path := range []string{"/usr/lib/systemd/system-generators/podman-system-generator", "/usr/libexec/podman/quadlet"} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			q.Generator = path
			break
		}
	}
	return q
}
func (q *Quadlet) command(args ...string) core.Command {
	return userSystemdCommand(q.Systemctl, args...)
}
func (q *Quadlet) resourceDir(key string) string {
	_, id, _ := strings.Cut(key, "/")
	return filepath.Join(q.Directory, id)
}
func (q *Quadlet) Validate(ctx context.Context, c Config, key string) error {
	if q.Generator == "" || q.Systemctl == "" || q.Podman == "" {
		return errors.New("Podman, Quadlet generator and systemctl are required")
	}
	files, err := RenderQuadlet(q.Store, c, key)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "server-man-quadlet-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for name, data := range files {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return err
		}
	}
	output, err := q.Runner.Capture(ctx, core.Command{Program: q.Generator, Args: []string{"--user", "--dryrun"}, Env: []string{"QUADLET_UNIT_DIRS=" + dir}, Timeout: 15 * time.Second})
	if err != nil {
		return errors.New("installed Quadlet generator rejected the definition")
	}
	// Generators can return success after skipping an unsupported source file.
	_, id, _ := strings.Cut(key, "/")
	for _, unit := range quadletUnits(c, id) {
		if !strings.Contains(output, unit) {
			return errors.New("installed generator did not produce every expected service")
		}
	}
	return nil
}
func (q *Quadlet) unitStates(ctx context.Context, units []string) (map[string]map[string]string, error) {
	result := map[string]map[string]string{}
	if len(units) == 0 {
		return result, nil
	}
	args := append([]string{"show", "--property=Id,LoadState,ActiveState,NeedDaemonReload"}, units...)
	output, err := q.Runner.Capture(ctx, q.command(args...))
	if err != nil {
		return nil, errors.New("user service manager is unavailable")
	}
	for _, block := range strings.Split(strings.TrimSpace(output), "\n\n") {
		fields := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				fields[k] = v
			}
		}
		if fields["Id"] != "" {
			result[fields["Id"]] = fields
		}
	}
	for _, unit := range units {
		if _, ok := result[unit]; !ok {
			return nil, errors.New("service manager returned incomplete state")
		}
	}
	return result, nil
}
func (q *Quadlet) Inspect(ctx context.Context, c Config, key string) (Observation, error) {
	_, id, _ := strings.Cut(key, "/")
	s, exists := c.Services[id]
	if !exists {
		entries, err := os.ReadDir(q.resourceDir(key))
		if os.IsNotExist(err) {
			return Observation{Matches: true, ActiveMatches: true}, nil
		}
		if err != nil {
			return Observation{}, err
		}
		return Observation{Matches: len(entries) == 0, ActiveMatches: true}, nil
	}
	files, err := RenderQuadlet(q.Store, c, key)
	if err != nil {
		return Observation{}, err
	}
	obs := Observation{Matches: true, ActiveMatches: true}
	root, err := os.OpenRoot(q.resourceDir(key))
	if os.IsNotExist(err) {
		obs.Matches = false
	} else if err != nil {
		return obs, err
	} else {
		defer root.Close()
		for name, want := range files {
			actual, e := readArtifact(root, name)
			if os.IsNotExist(e) {
				obs.Matches = false
				continue
			}
			if e != nil {
				return obs, e
			}
			if !bytes.Equal(actual, want) {
				obs.Matches = false
			}
		}
		entries, e := os.ReadDir(q.resourceDir(key))
		if e != nil {
			return obs, e
		}
		if len(entries) != len(files) {
			obs.Matches = false
		}
	}
	states, err := q.unitStates(ctx, quadletUnits(c, id))
	if err != nil {
		return obs, err
	}
	for _, state := range states {
		if state["LoadState"] == "loaded" {
			obs.Registered = true
		} else {
			obs.Matches = false
		}
		if state["ActiveState"] == "active" || state["ActiveState"] == "activating" {
			obs.Active = true
		}
		if state["NeedDaemonReload"] != "no" {
			obs.Matches = false
		}
	}
	if obs.Active {
		output, err := q.Runner.Capture(ctx, core.Command{Program: q.Podman, Args: []string{"ps", "--filter", "label=io.server-man.service=" + id, "--format", "json"}, Timeout: 5 * time.Second})
		if err != nil {
			return obs, errors.New("cannot verify active container revisions")
		}
		containers, err := parseManagedContainers(output)
		if err != nil {
			return obs, errors.New("invalid container inventory")
		}
		if len(containers) > len(s.Containers) {
			return obs, errors.New("unexpected containers share managed identity")
		}
		if err := observeContainerGroup(&obs, c, key, containers); err != nil {
			return obs, err
		}
	}
	return obs, nil
}
func (q *Quadlet) Apply(ctx context.Context, previous, desired Config, key, mode string) error {
	_, id, _ := strings.Cut(key, "/")
	old, had := previous.Services[id]
	next, exists := desired.Services[id]
	before := map[string][]byte{}
	var err error
	if had {
		before, err = RenderQuadlet(q.Store, previous, key)
		if err != nil {
			return err
		}
	}
	after := map[string][]byte{}
	if exists {
		after, err = RenderQuadlet(q.Store, desired, key)
		if err != nil {
			return err
		}
	}
	oldUnits := quadletUnits(previous, id)
	states, err := q.unitStates(ctx, oldUnits)
	if err != nil {
		return err
	}
	active := []string{}
	for _, unit := range oldUnits {
		if states[unit]["ActiveState"] == "active" {
			active = append(active, unit)
		}
	}
	if !exists && len(active) > 0 {
		return errors.New("stop application before removing its definition")
	}
	if had && exists && mode == "next_start" {
		for name := range old.Containers {
			if _, ok := next.Containers[name]; !ok {
				return errors.New("removing containers requires immediate apply")
			}
		}
	}
	if err = publishArtifacts(q.resourceDir(key), before, after); err != nil {
		return err
	}
	if err = q.Runner.Run(ctx, q.command("daemon-reload"), io.Discard); err != nil {
		return errors.New("user service manager reload failed")
	}
	runtimeChanged := exists && had && runtimeFingerprint(previous, key) != runtimeFingerprint(desired, key)
	if exists && had && mode == "now" && len(active) > 0 && !runtimeChanged {
		observed, e := q.Inspect(ctx, desired, key)
		if e != nil {
			return e
		}
		runtimeChanged = !observed.ActiveMatches
	}
	if exists && had && mode == "now" && runtimeChanged {
		// Only previously running members are restarted; newly added members and a
		// completely stopped application remain stopped until explicit Start.
		desiredUnits := quadletUnits(desired, id)
		for _, unit := range active {
			action := "restart"
			if !slices.Contains(desiredUnits, unit) {
				action = "stop"
			}
			cmd := q.command(action, unit)
			cmd.Timeout = 20 * time.Minute
			if err = q.Runner.Run(ctx, cmd, io.Discard); err != nil {
				return errors.New("affected container reconciliation failed")
			}
		}
	}
	return nil
}

func (q *Quadlet) Action(ctx context.Context, c Config, key, action string) error {
	if action != "start" && action != "stop" && action != "restart" {
		return errors.New("unsupported Quadlet action")
	}
	if _, err := RenderQuadlet(q.Store, c, key); err != nil {
		return err
	}
	_, id, _ := strings.Cut(key, "/")
	units := quadletUnits(c, id)
	if action != "stop" {
		if err := q.Validate(ctx, c, key); err != nil {
			return err
		}
		version, err := q.Runner.Capture(ctx, core.Command{Program: q.Podman, Args: []string{"info", "--format", "{{.Host.CgroupsVersion}}"}, Timeout: 5 * time.Second})
		if err != nil || strings.TrimSpace(version) != "v2" {
			return errors.New("rootless Podman with cgroup v2 is required")
		}
	} else {
		slices.Reverse(units)
	}
	cmd := q.command(append([]string{action}, units...)...)
	cmd.Timeout = 20 * time.Minute
	if err := q.Runner.Run(ctx, cmd, io.Discard); err != nil {
		return errors.New("user service operation failed; inspect application logs")
	}
	states, err := q.unitStates(ctx, units)
	if err != nil {
		return err
	}
	for _, unit := range units {
		active := states[unit]["ActiveState"] == "active"
		if (action == "stop" && active) || (action != "stop" && !active) {
			return errors.New("container group did not reach the requested state")
		}
	}
	return nil
}

func (q *Quadlet) Prerequisites(ctx context.Context) []Issue {
	issues := []Issue{}
	add := func(severity, field, code, message, remedy string) {
		issues = append(issues, Issue{severity, "backend/podman/quadlet", field, code, message, remedy})
	}
	for _, tool := range []struct{ name, path string }{{"podman", q.Podman}, {"quadlet", q.Generator}} {
		if tool.path == "" {
			add("error", tool.name, "missing_dependency", "A required executable is missing: "+tool.name+".", "Install the native dependency through Software, then recheck configuration.")
		}
	}
	if q.Podman != "" {
		output, err := q.Runner.Capture(ctx, core.Command{Program: q.Podman, Args: []string{"info", "--format", "json"}, Timeout: 4 * time.Second})
		if err != nil {
			add("error", "podman", "engine_unavailable", "Podman cannot access its runtime context.", "Check the service account, user namespaces, runtime directory and Podman setup.")
		} else {
			issues = append(issues, podmanInfoIssues(output, "quadlet", true)...)
		}
	}
	issues = append(issues, userManagerIssues(ctx, q.Systemctl, q.Runner, "podman/quadlet")...)
	issues = append(issues, userBusIssues(ctx, q.Systemctl, q.Runner, "podman/quadlet")...)
	issues = append(issues, podmanBootIssues(ctx, q.Systemctl, q.Runner, "podman/quadlet")...)
	return issues
}
