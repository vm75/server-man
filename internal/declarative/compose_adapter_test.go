package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"server-man/internal/core"
)

func composeFixture(t *testing.T) (*Compose, Config, *composeRecorder) {
	t.Helper()
	c, cfg, _ := coordinatorFixture(t)
	service := cfg.Services["website"]
	service.Backend = "compose"
	cfg.Services["website"] = service
	r := &composeRecorder{}
	p := &Compose{Store: c.Store, Engine: "podman", Program: "/usr/bin/podman", Systemctl: "/usr/bin/systemctl", Directory: filepath.Join(t.TempDir(), "runtime"), UnitDirectory: filepath.Join(t.TempDir(), "units"), Runner: r}
	r.unitFile = p.unitFile("service/website")
	return p, cfg, r
}

type composeRecorder struct {
	commands     []core.Command
	containers   []managedContainer
	enabled      bool
	unitFile     string
	unitFields   map[string]string
	failBootStop bool
	onRun        func(core.Command)
}

func (r *composeRecorder) Run(_ context.Context, cmd core.Command, _ io.Writer) error {
	r.commands = append(r.commands, cmd)
	if r.onRun != nil {
		r.onRun(cmd)
	}
	if len(cmd.Args) > 4 && cmd.Args[3] == "link" {
		r.unitFile = cmd.Args[4]
	}
	if len(cmd.Args) > 3 && cmd.Args[3] == "stop" && r.failBootStop {
		return errors.New("fixture stop denied")
	}
	return nil
}
func (r *composeRecorder) Capture(_ context.Context, cmd core.Command) (string, error) {
	r.commands = append(r.commands, cmd)
	if strings.Contains(strings.Join(cmd.Args, " "), "--property=LoadState") {
		enabled := "linked"
		if r.enabled {
			enabled = "enabled"
		}
		fields := map[string]string{"LoadState": "loaded", "ActiveState": "inactive", "UnitFileState": enabled, "NeedDaemonReload": "no", "FragmentPath": r.unitFile, "DropInPaths": ""}
		for k, v := range r.unitFields {
			fields[k] = v
		}
		var output strings.Builder
		for k, v := range fields {
			output.WriteString(k + "=" + v + "\n")
		}
		return output.String(), nil
	}
	if len(cmd.Args) > 0 && cmd.Args[0] == "ps" {
		rows := []any{}
		for _, v := range r.containers {
			rows = append(rows, map[string]any{"State": v.State, "Labels": map[string]string{"io.server-man.container": v.Name, "io.server-man.fingerprint": v.Fingerprint}})
		}
		data, _ := json.Marshal(rows)
		return string(data), nil
	}
	return "", nil
}
func TestComposeLiteralRenderingAndNoRestartOwnerConflict(t *testing.T) {
	p, cfg, _ := composeFixture(t)
	s := cfg.Services["website"]
	v := s.Containers["web"]
	v.Command = []string{"printf", "$HOME ${TOKEN}"}
	v.Environment = map[string]string{"TEXT": "literal $HOME"}
	s.Containers["web"] = v
	cfg.Services["website"] = s
	data, err := RenderCompose(p.Store, cfg, "service/website")
	if err != nil {
		t.Fatal(err)
	}
	var model struct {
		Services map[string]composeContainer `yaml:"services"`
	}
	if err = yaml.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	got := model.Services["web"]
	if got.Restart != "no" || got.Command[1] != "$$HOME $${TOKEN}" || got.Environment["TEXT"] != "literal $$HOME" || got.Volumes[0].Bind["create_host_path"] {
		t.Fatal(got)
	}
	boot, err := RenderComposeBoot(cfg, "service/website", p.Program, p.file("service/website"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(boot)
	if strings.Contains(text, "Restart=") || strings.Contains(text, "ExecStop=") || !strings.Contains(text, "Type=oneshot") || !strings.Contains(text, "RemainAfterExit=yes") {
		t.Fatal(text)
	}
	if !strings.Contains(text, "Wants="+podmanNetworkOnlineUnit+"\nAfter="+podmanNetworkOnlineUnit+"\n") {
		t.Fatal("Podman user launcher lacks native host-network ordering", text)
	}
	s.Engine = "docker"
	cfg.Services["website"] = s
	boot, err = RenderComposeBoot(cfg, "service/website", "/usr/bin/docker", p.file("service/website"))
	if err != nil || strings.Contains(string(boot), podmanNetworkOnlineUnit) {
		t.Fatal("Docker acquired a Podman dependency", err)
	}
}

func TestComposeRemovalRetiresOnlyOwnedCompletedBootLauncher(t *testing.T) {
	for _, tc := range []struct {
		name             string
		fields           map[string]string
		failStop, refuse bool
	}{
		{"active completed launcher", map[string]string{"ActiveState": "active"}, false, false},
		{"linked managed fragment", nil, false, false},
		{"linked foreign fragment", nil, false, true},
		{"missing launcher", map[string]string{"LoadState": "not-found", "ActiveState": "inactive", "FragmentPath": ""}, false, false},
		{"foreign fragment", map[string]string{"FragmentPath": "/foreign/unit.service"}, false, true},
		{"unmanaged override", map[string]string{"DropInPaths": "/foreign/override.conf"}, false, true},
		{"pending boot launch", map[string]string{"ActiveState": "activating"}, false, true},
		{"stale manager", map[string]string{"NeedDaemonReload": "yes"}, false, true},
		{"stop failure", map[string]string{"ActiveState": "active"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, cfg, r := composeFixture(t)
			if err := p.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "now"); err != nil {
				t.Fatal(err)
			}
			if tc.name == "linked managed fragment" || tc.name == "linked foreign fragment" {
				target := p.unitFile("service/website")
				if tc.name == "linked foreign fragment" {
					data, err := os.ReadFile(target)
					if err != nil {
						t.Fatal(err)
					}
					target = filepath.Join(t.TempDir(), "foreign.service")
					if err = os.WriteFile(target, data, 0600); err != nil {
						t.Fatal(err)
					}
				}
				link := filepath.Join(t.TempDir(), "linked.service")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				tc.fields = map[string]string{"FragmentPath": link}
			}
			r.commands = nil
			r.unitFields = tc.fields
			r.failBootStop = tc.failStop
			err := p.Apply(context.Background(), cfg, Config{Version: 1}, "service/website", "now")
			if (err != nil) != tc.refuse {
				t.Fatal(err)
			}
			stop, down := -1, -1
			for n, cmd := range r.commands {
				if len(cmd.Args) > 3 && cmd.Args[3] == "stop" {
					stop = n
				}
				if len(cmd.Args) > 0 && cmd.Args[0] == "compose" && slices.Contains(cmd.Args, "down") {
					down = n
				}
			}
			if tc.refuse {
				if down >= 0 || (stop >= 0 && !tc.failStop) {
					t.Fatal("refused removal mutated native workload/unit", r.commands)
				}
				if _, err := os.Stat(p.file("service/website")); err != nil {
					t.Fatal("refusal removed source", err)
				}
				if _, err := os.Stat(p.unitFile("service/website")); err != nil {
					t.Fatal("refusal removed boot source", err)
				}
			} else {
				if down < 0 || (tc.name != "missing launcher" && (stop < 0 || stop > down)) {
					t.Fatal("boot envelope not retired before project removal", r.commands)
				}
				if _, err := os.Stat(p.unitFile("service/website")); !os.IsNotExist(err) {
					t.Fatal("boot source remains", err)
				}
			}
		})
	}
}
func TestComposeDefineDoesNotStartAndTogglePreservesExecution(t *testing.T) {
	p, cfg, r := composeFixture(t)
	if err := p.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "now"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.commands {
		for _, arg := range cmd.Args {
			if arg == "up" || arg == "start" || arg == "restart" || arg == "--now" {
				t.Fatal("definition launched containers", cmd.Args)
			}
		}
	}
	r.commands = nil
	r.containers = []managedContainer{{"web", "running", runtimeFingerprint(cfg, "service/website")}}
	nextData, _ := Encode(cfg)
	next, _ := Parse(nextData)
	s := next.Services["website"]
	s.Enabled = false
	next.Services["website"] = s
	if err := p.Apply(context.Background(), cfg, next, "service/website", "next_start"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.commands {
		for _, arg := range cmd.Args {
			if arg == "up" || arg == "stop" || arg == "restart" || arg == "--now" {
				t.Fatal("boot toggle interrupted application", cmd.Args)
			}
		}
	}
	obs, err := p.Inspect(context.Background(), next, "service/website")
	if err != nil || !obs.Matches || !obs.Active || !obs.ActiveMatches {
		t.Fatal(obs, err)
	}
}

func TestComposeBootFailureIsFreshWarningWithoutDefinitionDrift(t *testing.T) {
	p, cfg, r := composeFixture(t)
	if err := p.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "now"); err != nil {
		t.Fatal(err)
	}
	r.enabled = cfg.Services["website"].Enabled
	r.unitFields = map[string]string{"ActiveState": "failed"}
	for _, running := range []bool{false, true} {
		r.containers = nil
		if running {
			r.containers = []managedContainer{{"web", "running", runtimeFingerprint(cfg, "service/website")}}
		}
		r.commands = nil
		obs, err := p.Inspect(context.Background(), cfg, "service/website")
		if err != nil || !obs.Matches || !obs.Registered || obs.Active != running || len(obs.Issues) != 1 {
			t.Fatal(obs, err)
		}
		issue := obs.Issues[0]
		if issue.Severity != "warning" || issue.Resource != "service/website" || issue.Field != "autostart" || issue.Code != "boot_start_failed" || issue.Remedy == "" {
			t.Fatal(issue)
		}
		for _, cmd := range r.commands {
			if cmd.Args[0] != "ps" && !slices.Contains(cmd.Args, "show") {
				t.Fatal("inspection changed native state", cmd)
			}
		}
	}
	// A successful launcher clears the alert on the next observation; neither
	// readiness caching nor historical failures create permanent blockers.
	r.unitFields["ActiveState"] = "active"
	obs, err := p.Inspect(context.Background(), cfg, "service/website")
	if err != nil || len(obs.Issues) != 0 {
		t.Fatal(obs, err)
	}
	r.unitFields = map[string]string{"ActiveState": "failed", "FragmentPath": "/foreign/unit.service"}
	obs, err = p.Inspect(context.Background(), cfg, "service/website")
	if err != nil || len(obs.Issues) != 0 || obs.Matches {
		t.Fatal("attributed foreign launcher failure to owned application", obs, err)
	}
}

type statusComposeFixture struct{ *Compose }

func (statusComposeFixture) Prerequisites(context.Context) []Issue { return nil }

func TestManagedStatusReportsBootFailureAndAllowsCurrentStart(t *testing.T) {
	p, cfg, r := composeFixture(t)
	if _, err := p.Store.Save(cfg, "missing"); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "now"); err != nil {
		t.Fatal(err)
	}
	c := Coordinator{Store: p.Store, HealthCache: &HealthCache{}, Adapters: map[string]Adapter{"podman/compose": statusComposeFixture{p}}}
	r.enabled = cfg.Services["website"].Enabled
	r.unitFields = map[string]string{"ActiveState": "failed"}
	result, err := c.ServiceStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows := result.(map[string]any)["services"].([]ServiceStatus)
	if len(rows) != 1 || rows[0].State != "stopped" || rows[0].Configuration != "registered" || !slices.ContainsFunc(rows[0].Issues, func(i Issue) bool { return i.Code == "boot_start_failed" }) {
		t.Fatal(rows)
	}
	r.onRun = func(cmd core.Command) {
		if slices.Contains(cmd.Args, "up") {
			r.containers = []managedContainer{{"web", "running", runtimeFingerprint(cfg, "service/website")}}
		}
	}
	if err := c.ServiceAction(context.Background(), "website", "start"); err != nil {
		t.Fatal("boot failure blocked explicit current-run recovery", err)
	}
	r.unitFields["ActiveState"] = "active"
	result, err = c.ServiceStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rows = result.(map[string]any)["services"].([]ServiceStatus)
	if slices.ContainsFunc(rows[0].Issues, func(i Issue) bool { return i.Code == "boot_start_failed" }) {
		t.Fatal("runtime issue was retained by readiness cache", rows)
	}
}

func TestEngineInventoryShapes(t *testing.T) {
	for _, body := range []string{`[{"State":"running","Labels":{"io.server-man.container":"web","io.server-man.fingerprint":"hash"}}]`, `{"State":"running","Labels":"io.server-man.container=web,io.server-man.fingerprint=hash"}`} {
		rows, err := parseManagedContainers(body)
		if err != nil || len(rows) != 1 || rows[0].Name != "web" || rows[0].Fingerprint != "hash" {
			t.Fatal(rows, err)
		}
	}
	if _, err := parseManagedContainers(`{"Labels":2}`); err == nil {
		t.Fatal("invalid inventory accepted")
	}
}
func TestComposeValidationUsesPrivateTempAndFixedProviderArgs(t *testing.T) {
	p, cfg, r := composeFixture(t)
	if err := p.Validate(context.Background(), cfg, "service/website"); err != nil {
		t.Fatal(err)
	}
	if len(r.commands) != 1 {
		t.Fatal(r.commands)
	}
	cmd := r.commands[0]
	if cmd.Args[0] != "compose" || !strings.Contains(strings.Join(cmd.Args, " "), "--env-file /dev/null") {
		t.Fatal(cmd.Args)
	}
	if _, err := os.Stat(cmd.Dir); !os.IsNotExist(err) {
		t.Fatal("private candidate directory not cleaned")
	}
}

func TestComposeRemovalRejectsForeignFilesBeforeNativeDown(t *testing.T) {
	p, cfg, r := composeFixture(t)
	if err := p.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "now"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.file("service/website"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	r.commands = nil
	if err := p.Apply(context.Background(), cfg, Config{Version: 1}, "service/website", "now"); err == nil {
		t.Fatal("foreign config removal accepted")
	}
	for _, cmd := range r.commands {
		for _, arg := range cmd.Args {
			if arg == "down" {
				t.Fatal("native removal ran before ownership validation")
			}
		}
	}
}
func TestComposeRestartUsesRecreationToLoadSavedSettings(t *testing.T) {
	p, cfg, r := composeFixture(t)
	r.containers = []managedContainer{{"web", "running", runtimeFingerprint(cfg, "service/website")}}
	if err := p.Action(context.Background(), cfg, "service/website", "restart"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cmd := range r.commands {
		for _, arg := range cmd.Args {
			if arg == "--force-recreate" {
				found = true
			}
			if arg == "restart" {
				t.Fatal("restart would ignore updated Compose settings")
			}
		}
	}
	if !found {
		t.Fatal("recreation not requested")
	}
}

func TestComposeBothEnginesUseSamePortableDefinition(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		t.Run(engine, func(t *testing.T) {
			p, cfg, r := composeFixture(t)
			p.Engine = engine
			p.Program = "/fixture/" + engine
			s := cfg.Services["website"]
			s.Engine = engine
			cfg.Services["website"] = s
			if err := p.Validate(context.Background(), cfg, "service/website"); err != nil {
				t.Fatal(err)
			}
			if len(r.commands) != 1 || r.commands[0].Program != p.Program {
				t.Fatal(r.commands)
			}
			if err := p.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "next_start"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(p.file("service/website"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "create_host_path: false") {
				t.Fatal("provider definition permits implicit directory creation")
			}
		})
	}
}

func TestComposeBootSourceOutsideSystemdSearchHierarchy(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	store := Store{Path: filepath.Join(root, "server-man", "server-man.yaml")}
	for _, engine := range []string{"podman", "docker"} {
		p := NewCompose(store, engine, &composeRecorder{})
		if p.UnitDirectory != store.Path+".generated/"+engine+"-compose-units" {
			t.Fatal("boot source must be in owned generated storage outside the systemd hierarchy", p.UnitDirectory)
		}
	}
}

func TestComposeRemovesOnlyStoppedOwnedMembersWithoutVolumes(t *testing.T) {
	p, cfg, r := composeFixture(t)
	s := cfg.Services["website"]
	s.Containers["old"] = s.Containers["web"]
	cfg.Services["website"] = s
	if err := p.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "now"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode(cfg)
	next, _ := Parse(raw)
	s = next.Services["website"]
	delete(s.Containers, "old")
	next.Services["website"] = s
	r.commands = nil
	r.containers = []managedContainer{{"old", "exited", runtimeFingerprint(cfg, "service/website")}}
	if err := p.Apply(context.Background(), cfg, next, "service/website", "now"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cmd := range r.commands {
		if strings.Contains(strings.Join(cmd.Args, " "), "rm --force old") {
			found = true
		}
		for _, arg := range cmd.Args {
			if arg == "--volumes" || arg == "-v" || arg == "--remove-orphans" {
				t.Fatal("unscoped removal", cmd.Args)
			}
		}
	}
	if !found {
		t.Fatal("stale stopped member not removed")
	}
}

type dockerSocketRecorder struct {
	composeRecorder
	output  string
	failure error
}

func (r *dockerSocketRecorder) Capture(ctx context.Context, cmd core.Command) (string, error) {
	if slices.Contains(cmd.Args, "docker.socket") {
		r.commands = append(r.commands, cmd)
		return r.output, r.failure
	}
	return r.composeRecorder.Capture(ctx, cmd)
}

func TestDockerBootSocketReadinessIsBoundedAndNotARuntimeBlocker(t *testing.T) {
	for _, tc := range []struct {
		name, output, code, severity string
		failure                      error
	}{
		{name: "enabled", output: "LoadState=loaded\nUnitFileState=enabled\n"},
		{name: "disabled", output: "LoadState=loaded\nUnitFileState=disabled\n", code: "docker_socket_disabled", severity: "error"},
		{name: "runtime only", output: "LoadState=loaded\nUnitFileState=enabled-runtime\n", code: "docker_socket_disabled", severity: "error"},
		{name: "missing", output: "LoadState=not-found\nUnitFileState=\n", code: "docker_socket_disabled", severity: "error"},
		{name: "malformed", output: "private-probe-output", code: "docker_socket_unknown", severity: "unknown"},
		{name: "failed", failure: errors.New("private-probe-output"), code: "docker_socket_unknown", severity: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &dockerSocketRecorder{output: tc.output, failure: tc.failure}
			p := &Compose{Engine: "docker", Systemctl: "/usr/bin/systemctl", Runner: recorder}
			issues := p.dockerBootIssues(context.Background())
			if tc.code == "" {
				if len(issues) != 0 {
					t.Fatal(issues)
				}
			} else {
				if len(issues) != 1 || issues[0].Code != tc.code || issues[0].Severity != tc.severity || issues[0].Field != "autostart" || issues[0].Resource != "backend/docker/compose" {
					t.Fatal(issues)
				}
				if strings.Contains(issues[0].Message+issues[0].Remedy, "private-probe-output") {
					t.Fatal("probe output exposed")
				}
				c, cfg, _ := coordinatorFixture(t)
				service := cfg.Services["website"]
				service.Engine = "docker"
				service.Backend = "compose"
				cfg.Services["website"] = service
				c.Adapters = map[string]Adapter{"docker/compose": &probeAdapter{issues: issues}}
				if err := c.readinessError(context.Background(), cfg, "website", false); err != nil {
					t.Fatal("boot prerequisite blocked current-run start", err)
				}
				if err := c.readinessError(context.Background(), cfg, "website", true); err == nil {
					t.Fatal("boot prerequisite allowed enablement")
				}
			}
			if len(recorder.commands) != 1 {
				t.Fatal(recorder.commands)
			}
			cmd := recorder.commands[0]
			if cmd.Timeout > 3*time.Second || cmd.Timeout <= 0 || slices.Contains(cmd.Args, "--user") || !slices.Contains(cmd.Args, "docker.socket") {
				t.Fatal(cmd)
			}
		})
	}
}

func TestComposeRuntimeAndBootShareProviderArgumentsAndDirectory(t *testing.T) {
	p, cfg, _ := composeFixture(t)
	p.Directory = filepath.Join(t.TempDir(), "generated with $dollar %percent")
	file := p.file("service/website")
	command := p.command(file, "website", "up", "--detach", "--no-build")
	if command.Dir != filepath.Dir(file) || slices.Contains(command.Args, "--project-directory") {
		t.Fatal(command)
	}
	if !slices.Equal(command.Args, []string{"compose", "--project-name", composeProject("website"), "--env-file", "/dev/null", "--file", file, "up", "--detach", "--no-build"}) {
		t.Fatal(command.Args)
	}
	boot, err := RenderComposeBoot(cfg, "service/website", p.Program, file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(boot)
	if !strings.Contains(text, "WorkingDirectory="+strings.ReplaceAll(command.Dir, "%", "%%")+"\n") || strings.Contains(text, "--project-directory") {
		t.Fatal(text)
	}
	parts := []string{systemdExecArg(p.Program)}
	for _, arg := range command.Args {
		parts = append(parts, systemdExecArg(arg))
	}
	if !strings.Contains(text, "ExecStart="+strings.Join(parts, " ")+"\n") {
		t.Fatal("boot and normal Compose arguments differ", text)
	}
	if !strings.Contains(text, "COMPOSE_DISABLE_ENV_FILE=1") || strings.Contains(text, "Restart=") {
		t.Fatal("Compose environment/startup policy changed", text)
	}
	for _, env := range command.Env {
		if !strings.Contains(text, "Environment="+unitValue(env)+"\n") {
			t.Fatal("runtime and boot environment differ", env)
		}
	}
}

func TestComposeProviderSelectionAppliesToReadinessAndRuntime(t *testing.T) {
	for _, engine := range []string{"podman", "docker"} {
		p, cfg, r := composeFixture(t)
		p.Engine = engine
		s := cfg.Services["website"]
		s.Engine = engine
		cfg.Services["website"] = s
		p.Prerequisites(context.Background())
		command := p.command(p.file("service/website"), "website", "down")
		r.commands = append(r.commands, command)
		for _, cmd := range r.commands {
			if len(cmd.Args) > 0 && (cmd.Args[0] == "compose" || cmd.Args[0] == "info") {
				if slices.Contains(cmd.Env, "PODMAN_COMPOSE_PROVIDER=podman-compose") != (engine == "podman") {
					t.Fatal(engine, cmd)
				}
			}
		}
		boot, err := RenderComposeBoot(cfg, "service/website", p.Program, p.file("service/website"))
		if err != nil || strings.Contains(string(boot), "PODMAN_COMPOSE_PROVIDER=") != (engine == "podman") {
			t.Fatal(engine, err, string(boot))
		}
	}
}

func TestComposeBootRejectsAmbiguousDirectoryDirectives(t *testing.T) {
	p, cfg, _ := composeFixture(t)
	for _, directory := range []string{"/tmp/injected\nExecStart=/bin/false", "/tmp/back\\slash", "/tmp/trailing ", "/tmp/tab\tpath"} {
		if _, err := RenderComposeBoot(cfg, "service/website", p.Program, filepath.Join(directory, "compose.yaml")); err == nil {
			t.Fatal("ambiguous directory accepted")
		}
	}
}
