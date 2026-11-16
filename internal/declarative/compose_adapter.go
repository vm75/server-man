package declarative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"server-man/internal/core"
)

type Compose struct {
	Store                                                Store
	Engine, Program, Systemctl, Directory, UnitDirectory string
	Runner                                               core.Executor
}

func NewCompose(store Store, engine string, runner core.Executor) *Compose {
	program, _ := exec.LookPath(engine)
	systemctl, _ := exec.LookPath("systemctl")
	// systemctl link refuses files anywhere under its unit search hierarchy,
	// including subdirectories which the manager does not itself search.
	return &Compose{Store: store, Engine: engine, Program: program, Systemctl: systemctl, Directory: store.Path + ".generated/" + engine + "-compose", UnitDirectory: store.Path + ".generated/" + engine + "-compose-units", Runner: runner}
}
func (p *Compose) resourceDir(key string) string {
	_, id, _ := strings.Cut(key, "/")
	return filepath.Join(p.Directory, id)
}
func (p *Compose) file(key string) string { return filepath.Join(p.resourceDir(key), "compose.yaml") }
func (p *Compose) unitFile(key string) string {
	_, id, _ := strings.Cut(key, "/")
	return filepath.Join(p.UnitDirectory, id, composeUnit(id))
}
func (p *Compose) command(file, id string, args ...string) core.Command {
	return core.Command{Program: p.Program, Args: composeArguments(file, id, args...), Dir: filepath.Dir(file), Env: composeEnvironment(p.Engine), Timeout: 20 * time.Minute}
}
func (p *Compose) userCommand(args ...string) core.Command {
	return userSystemdCommand(p.Systemctl, args...)
}
func (p *Compose) Validate(ctx context.Context, c Config, key string) error {
	if p.Program == "" || p.Systemctl == "" {
		return errors.New("container engine, compatible Compose provider and user systemd are required")
	}
	data, err := RenderCompose(p.Store, c, key)
	if err != nil {
		return err
	}
	if _, err = RenderComposeBoot(c, key, p.Program, p.file(key)); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "server-man-compose-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "compose.yaml")
	if err = os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	_, id, _ := strings.Cut(key, "/")
	cmd := p.command(path, id, "config", "--quiet")
	cmd.Timeout = 15 * time.Second
	if _, err = p.Runner.Capture(ctx, cmd); err != nil {
		return errors.New("installed Compose provider rejected the generated definition")
	}
	return nil
}

type managedContainer struct{ Name, State, Fingerprint string }

func parseManagedContainers(output string) ([]managedContainer, error) {
	var rows []json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(output), "[") {
		if json.Unmarshal([]byte(output), &rows) != nil {
			return nil, errors.New("invalid engine inventory")
		}
	} else {
		decoder := json.NewDecoder(strings.NewReader(output))
		for {
			var row json.RawMessage
			err := decoder.Decode(&row)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, errors.New("invalid engine inventory")
			}
			rows = append(rows, row)
			if len(rows) > 4096 {
				return nil, errors.New("engine inventory limit exceeded")
			}
		}
	}
	if len(rows) > 4096 {
		return nil, errors.New("engine inventory limit exceeded")
	}
	result := []managedContainer{}
	for _, row := range rows {
		var item struct {
			Labels json.RawMessage
			State  string
		}
		if json.Unmarshal(row, &item) != nil {
			return nil, errors.New("invalid container inventory row")
		}
		labels := map[string]string{}
		if json.Unmarshal(item.Labels, &labels) != nil {
			var labelString string
			if json.Unmarshal(item.Labels, &labelString) != nil {
				return nil, errors.New("unsupported engine labels")
			}
			for _, pair := range strings.Split(labelString, ",") {
				k, v, ok := strings.Cut(pair, "=")
				if ok {
					labels[k] = v
				}
			}
		}
		result = append(result, managedContainer{labels["io.server-man.container"], strings.ToLower(item.State), labels["io.server-man.fingerprint"]})
	}
	return result, nil
}
func (p *Compose) containers(ctx context.Context, id string) ([]managedContainer, error) {
	output, err := p.Runner.Capture(ctx, core.Command{Program: p.Program, Args: []string{"ps", "--all", "--filter", "label=io.server-man.service=" + id, "--format", "json"}, Timeout: 5 * time.Second})
	if err != nil {
		return nil, errors.New("engine inventory is unavailable")
	}
	return parseManagedContainers(output)
}
func (p *Compose) Inspect(ctx context.Context, c Config, key string) (Observation, error) {
	_, id, _ := strings.Cut(key, "/")
	_, exists := c.Services[id]
	obs := Observation{Matches: true, ActiveMatches: true}
	containers, err := p.containers(ctx, id)
	if err != nil {
		return obs, err
	}
	if !exists {
		obs.Matches = len(containers) == 0
		obs.Registered = len(containers) > 0
		return obs, nil
	}
	data, err := RenderCompose(p.Store, c, key)
	if err != nil {
		return obs, err
	}
	boot, err := RenderComposeBoot(c, key, p.Program, p.file(key))
	if err != nil {
		return obs, err
	}
	for path, want := range map[string][]byte{p.file(key): data, p.unitFile(key): boot} {
		root, e := os.OpenRoot(filepath.Dir(path))
		if os.IsNotExist(e) {
			obs.Matches = false
			continue
		}
		if e != nil {
			return obs, e
		}
		actual, e := readArtifact(root, filepath.Base(path))
		root.Close()
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
	obs.Registered = obs.Matches
	// Native boot link state is part of registered configuration, not runtime state.
	cmd := p.userCommand("show", composeUnit(id), "--property=LoadState,UnitFileState,NeedDaemonReload")
	output, err := p.Runner.Capture(ctx, cmd)
	if err != nil {
		return obs, errors.New("cannot inspect Compose boot unit")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			fields[k] = v
		}
	}
	if fields["LoadState"] != "loaded" || fields["NeedDaemonReload"] != "no" {
		obs.Matches = false
	}
	if c.Services[id].Enabled {
		if fields["UnitFileState"] != "enabled" {
			obs.Matches = false
		}
	} else if fields["UnitFileState"] == "enabled" {
		obs.Matches = false
	}
	seen := map[string]bool{}
	for _, container := range containers {
		if seen[container.Name] {
			return obs, errors.New("duplicate managed container identity")
		}
		seen[container.Name] = true
		if container.State == "running" {
			obs.Active = true
			if container.Fingerprint != runtimeFingerprint(c, key) {
				obs.ActiveMatches = false
			}
		}
	}
	return obs, nil
}
func (p *Compose) Apply(ctx context.Context, previous, desired Config, key, mode string) error {
	_, id, _ := strings.Cut(key, "/")
	_, had := previous.Services[id]
	_, exists := desired.Services[id]
	containers, err := p.containers(ctx, id)
	if err != nil {
		return err
	}
	active := []string{}
	for _, container := range containers {
		if container.State == "running" {
			active = append(active, container.Name)
		}
	}
	if !exists && len(active) > 0 {
		return errors.New("stop application before removing its definition")
	}
	before, after := map[string][]byte{}, map[string][]byte{}
	oldBoot, newBoot := map[string][]byte{}, map[string][]byte{}
	if had {
		data, e := RenderCompose(p.Store, previous, key)
		if e != nil {
			return e
		}
		before["compose.yaml"] = data
		boot, e := RenderComposeBoot(previous, key, p.Program, p.file(key))
		if e != nil {
			return e
		}
		oldBoot[composeUnit(id)] = boot
	}
	if exists {
		data, e := RenderCompose(p.Store, desired, key)
		if e != nil {
			return e
		}
		after["compose.yaml"] = data
		boot, e := RenderComposeBoot(desired, key, p.Program, p.file(key))
		if e != nil {
			return e
		}
		newBoot[composeUnit(id)] = boot
	}
	if err = checkArtifactOwnership(p.resourceDir(key), before, after); err != nil {
		return err
	}
	if err = checkArtifactOwnership(filepath.Dir(p.unitFile(key)), oldBoot, newBoot); err != nil {
		return err
	}
	if exists && had {
		for name := range previous.Services[id].Containers {
			if _, ok := desired.Services[id].Containers[name]; ok {
				continue
			}
			for _, container := range containers {
				if container.Name == name && container.State == "running" {
					return errors.New("stop removed containers before applying their removal")
				}
			}
			if mode == "next_start" {
				return errors.New("removing containers requires immediate apply")
			}
			if err = p.Runner.Run(ctx, p.command(p.file(key), id, "rm", "--force", name), io.Discard); err != nil {
				return errors.New("stopped container removal failed")
			}
		}
	}
	if !exists {
		// Native down uses only the previously owned project file; never volume flags.
		if had {
			if err = p.Runner.Run(ctx, p.command(p.file(key), id, "down"), io.Discard); err != nil {
				return errors.New("Compose project removal failed")
			}
		}
		if err = p.Runner.Run(ctx, p.userCommand("disable", composeUnit(id)), io.Discard); err != nil {
			return errors.New("boot unit removal failed")
		}
	}
	if err = publishArtifacts(p.resourceDir(key), before, after); err != nil {
		return err
	}
	if err = publishArtifacts(filepath.Dir(p.unitFile(key)), oldBoot, newBoot); err != nil {
		return err
	}
	if exists {
		if err = p.Runner.Run(ctx, p.userCommand("link", p.unitFile(key)), io.Discard); err != nil {
			return errors.New("boot unit registration failed")
		}
		operation := "disable"
		if desired.Services[id].Enabled {
			operation = "enable"
		}
		if err = p.Runner.Run(ctx, p.userCommand(operation, composeUnit(id)), io.Discard); err != nil {
			return errors.New("boot policy update failed")
		}
		if operation == "disable" {
			if err = p.Runner.Run(ctx, p.userCommand("link", p.unitFile(key)), io.Discard); err != nil {
				return errors.New("disabled boot unit registration failed")
			}
		}
	}
	if err = p.Runner.Run(ctx, p.userCommand("daemon-reload"), io.Discard); err != nil {
		return errors.New("user-manager reload failed")
	}
	if exists && mode == "now" && len(active) > 0 {
		wanted := desired.Services[id].Containers
		survivors := []string{}
		for _, name := range active {
			if _, ok := wanted[name]; ok {
				survivors = append(survivors, name)
			} else {
				return errors.New("stop removed containers before applying their removal")
			}
		}
		changed := !had || runtimeFingerprint(previous, key) != runtimeFingerprint(desired, key)
		for _, container := range containers {
			if container.State == "running" && container.Fingerprint != runtimeFingerprint(desired, key) {
				changed = true
			}
		}
		if changed && len(survivors) > 0 {
			args := append([]string{"up", "--detach", "--no-build", "--no-deps"}, survivors...)
			if err = p.Runner.Run(ctx, p.command(p.file(key), id, args...), io.Discard); err != nil {
				return errors.New("running Compose application reconciliation failed")
			}
		}
	}
	return nil
}
func (p *Compose) actionOne(ctx context.Context, c Config, key, action string) error {
	_, id, _ := strings.Cut(key, "/")
	if action != "start" && action != "stop" && action != "restart" {
		return errors.New("unsupported Compose action")
	}
	if action != "stop" {
		if err := p.Validate(ctx, c, key); err != nil {
			return err
		}
	}
	args := []string{action}
	if action == "start" {
		args = []string{"up", "--detach", "--no-build"}
	}
	if action == "restart" {
		args = []string{"up", "--detach", "--no-build", "--force-recreate"}
	}
	if err := p.Runner.Run(ctx, p.command(p.file(key), id, args...), io.Discard); err != nil {
		return errors.New("Compose lifecycle operation failed")
	}
	containers, err := p.containers(ctx, id)
	if err != nil {
		return err
	}
	running := map[string]bool{}
	for _, container := range containers {
		running[container.Name] = container.State == "running"
	}
	for name := range c.Services[id].Containers {
		if action == "stop" {
			if running[name] {
				return errors.New("Compose containers remain running")
			}
		} else if !running[name] {
			return errors.New("Compose group did not fully start")
		}
	}
	return nil
}
func (p *Compose) Prerequisites(ctx context.Context) []Issue {
	issues := []Issue{}
	add := func(field, code, message, remedy string) {
		issues = append(issues, Issue{"error", "backend/" + p.Engine + "/compose", field, code, message, remedy})
	}
	if p.Program == "" {
		add("engine", "missing_dependency", "The container engine executable is missing.", "Install the selected engine through Software and recheck.")
		return issues
	}
	for _, probe := range []struct {
		args          []string
		code, message string
	}{{[]string{"compose", "version"}, "compose_unavailable", "A compatible Compose provider is unavailable."}, {[]string{"info", "--format", "json"}, "engine_unavailable", "The engine runtime is inaccessible."}} {
		if output, err := p.Runner.Capture(ctx, core.Command{Program: p.Program, Args: probe.args, Env: composeEnvironment(p.Engine), Timeout: 4 * time.Second}); err != nil {
			add("runtime", probe.code, probe.message, "Check engine/provider installation, socket access and the service account.")
		} else if p.Engine == "podman" && probe.args[0] == "info" {
			issues = append(issues, podmanInfoIssues(output, "compose", false)...)
		}
	}
	issues = append(issues, userManagerIssues(ctx, p.Systemctl, p.Runner, p.Engine+"/compose")...)
	if p.Engine == "docker" && p.Systemctl != "" {
		issues = append(issues, p.dockerBootIssues(ctx)...)
	}
	return issues
}

// The system socket participates in early host boot; user units cannot order
// themselves against system-manager daemon units. Current-run access is checked
// separately, so this prerequisite only blocks automatic startup policy.
func (p *Compose) dockerBootIssues(ctx context.Context) []Issue {
	issue := Issue{"unknown", "backend/docker/compose", "autostart", "docker_socket_unknown", "Docker's boot socket could not be checked.", "Check the system docker.socket unit or rerun the installer with Docker selected, then recheck."}
	result, err := p.Runner.Capture(ctx, core.Command{Program: p.Systemctl, Args: []string{"--no-pager", "--no-ask-password", "show", "docker.socket", "--property=LoadState,UnitFileState"}, Timeout: 3 * time.Second})
	if err != nil {
		return []Issue{issue}
	}
	properties := map[string]string{}
	for _, line := range strings.Split(result, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = value
		}
	}
	if properties["LoadState"] == "loaded" && properties["UnitFileState"] == "enabled" {
		return nil
	}
	_, loadKnown := properties["LoadState"]
	_, stateKnown := properties["UnitFileState"]
	if !loadKnown || !stateKnown {
		return []Issue{issue}
	}
	issue.Severity = "error"
	issue.Code = "docker_socket_disabled"
	issue.Message = "Docker's system socket is missing or not persistently enabled for boot."
	return []Issue{issue}
}

func (p *Compose) Action(ctx context.Context, c Config, key, action string) error {
	if action != "start" && action != "stop" && action != "restart" {
		return errors.New("unsupported Compose action")
	}
	if action == "stop" {
		return p.actionOne(ctx, c, key, action)
	}
	_, id, _ := strings.Cut(key, "/")
	needed := map[string]bool{}
	var collect func(string)
	collect = func(id string) {
		if needed[id] {
			return
		}
		needed[id] = true
		for _, dep := range c.Services[id].DependsOn {
			collect(dep)
		}
	}
	collect(id)
	for _, resource := range orderedResources(c) {
		if !strings.HasPrefix(resource, "service/") {
			continue
		}
		_, dep, _ := strings.Cut(resource, "/")
		if !needed[dep] {
			continue
		}
		s := c.Services[dep]
		if s.Engine != p.Engine || s.Backend != "compose" {
			return errors.New("Compose dependency backend is unsupported")
		}
		op := "start"
		if dep == id {
			op = action
		}
		if err := p.actionOne(ctx, c, resource, op); err != nil {
			return err
		}
	}
	return nil
}
