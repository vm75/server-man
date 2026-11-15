package declarative

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	return p, cfg, r
}

type composeRecorder struct {
	commands   []core.Command
	containers []managedContainer
	enabled    bool
}

func (r *composeRecorder) Run(_ context.Context, cmd core.Command, _ io.Writer) error {
	r.commands = append(r.commands, cmd)
	return nil
}
func (r *composeRecorder) Capture(_ context.Context, cmd core.Command) (string, error) {
	r.commands = append(r.commands, cmd)
	if strings.Contains(strings.Join(cmd.Args, " "), "--property=LoadState") {
		enabled := "linked"
		if r.enabled {
			enabled = "enabled"
		}
		return "LoadState=loaded\nUnitFileState=" + enabled + "\nNeedDaemonReload=no\n", nil
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
	if strings.Contains(text, "Restart=") || strings.Contains(text, "ExecStop=") || !strings.Contains(text, "Type=oneshot") || !strings.Contains(text, "RemainAfterExit=no") {
		t.Fatal(text)
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
