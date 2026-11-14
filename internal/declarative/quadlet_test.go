package declarative

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"serverman/internal/core"
)

func TestQuadletRenderDeterministicAndStartupPolicy(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	files, err := RenderQuadlet(c.Store, cfg, "service/website")
	if err != nil {
		t.Fatal(err)
	}
	name := quadletBase("website", "web") + ".container"
	data := string(files[name])
	if !strings.Contains(data, "WantedBy=default.target") || !strings.Contains(data, "NoNewPrivileges=true") {
		t.Fatal(data)
	}
	svc := cfg.Services["website"]
	svc.Enabled = false
	cfg.Services["website"] = svc
	disabled, err := RenderQuadlet(c.Store, cfg, "service/website")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(disabled[name]), "WantedBy=") {
		t.Fatal("disabled service installed boot activation")
	}
	label := func(s string) string {
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "Label=io.server-man.fingerprint=") {
				return line
			}
		}
		return ""
	}
	if label(data) == "" || label(data) != label(string(disabled[name])) {
		t.Fatal("boot toggle changed runtime fingerprint")
	}
}
func TestNativeQuadletGeneratorWithTemporaryDefinitions(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	q := NewQuadlet(c.Store, core.Runner{})
	if q.Generator == "" {
		t.Skip("Quadlet generator is not installed")
	}
	if err := q.Validate(context.Background(), cfg, "service/website"); err != nil {
		t.Fatal(err)
	}
	// Validation invokes only isolated generator dry-run, never reload or Podman run.
}
func TestArtifactPublicationOwnership(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owned")
	first := map[string][]byte{"a.container": []byte(ownedHeader + "old\n")}
	next := map[string][]byte{"b.container": []byte(ownedHeader + "new\n")}
	if err := publishArtifacts(root, nil, first); err != nil {
		t.Fatal(err)
	}
	if err := publishArtifacts(root, first, next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.container")); !os.IsNotExist(err) {
		t.Fatal("stale owned file retained")
	}
	if err := publishArtifacts(root, first, next); err != nil {
		t.Fatal("interrupted retry rejected desired output", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.container"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishArtifacts(root, next, first); err == nil {
		t.Fatal("foreign edit overwritten")
	}
}

type quadletRecorder struct {
	commands    []core.Command
	fingerprint string
}

func (r *quadletRecorder) Run(_ context.Context, c core.Command, _ io.Writer) error {
	r.commands = append(r.commands, c)
	return nil
}
func (r *quadletRecorder) Capture(_ context.Context, c core.Command) (string, error) {
	r.commands = append(r.commands, c)
	if len(c.Args) > 3 && c.Args[3] == "show" {
		var out strings.Builder
		for _, arg := range c.Args[5:] {
			fmt.Fprintf(&out, "Id=%s\nLoadState=loaded\nActiveState=active\nNeedDaemonReload=no\n\n", arg)
		}
		return out.String(), nil
	}
	if c.Program == "podman" {
		data, _ := json.Marshal([]any{map[string]any{"Labels": map[string]string{"io.server-man.fingerprint": r.fingerprint}}})
		return string(data), nil
	}
	return "", nil
}
func TestQuadletToggleDoesNotRestartOrEnableGeneratedServices(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	r := &quadletRecorder{fingerprint: runtimeFingerprint(cfg, "service/website")}
	q := &Quadlet{Store: c.Store, Directory: t.TempDir(), Systemctl: "systemctl", Podman: "podman", Runner: r}
	svc := cfg.Services["website"]
	svc.Enabled = false
	desiredData, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	next, err := Parse(desiredData)
	if err != nil {
		t.Fatal(err)
	}
	next.Services["website"] = svc
	files, err := RenderQuadlet(c.Store, cfg, "service/website")
	if err != nil {
		t.Fatal(err)
	}
	if err = publishArtifacts(q.resourceDir("service/website"), nil, files); err != nil {
		t.Fatal(err)
	}
	if err = q.Apply(context.Background(), cfg, next, "service/website", "now"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.commands {
		for _, arg := range cmd.Args {
			if arg == "restart" || arg == "stop" || arg == "start" || arg == "enable" || arg == "disable" {
				t.Fatal("boot toggle changed running state", cmd.Args)
			}
		}
	}
}

func TestQuadletStoppedDefinitionPublicationDoesNotStart(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	r := &quadletRecorder{}
	q := &Quadlet{Store: c.Store, Directory: t.TempDir(), Systemctl: "systemctl", Podman: "podman", Runner: r}
	if err := q.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "now"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.commands {
		for _, arg := range cmd.Args {
			if arg == "start" || arg == "restart" || arg == "enable" {
				t.Fatal("new application was started", cmd.Args)
			}
		}
	}
}
func TestNativeQuadletEscaping(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	q := NewQuadlet(c.Store, core.Runner{})
	if q.Generator == "" {
		t.Skip("Quadlet generator unavailable")
	}
	svc := cfg.Services["website"]
	v := svc.Containers["web"]
	v.Command = []string{"printf", "$HOME %n ; touch /not-executed", "line\nnext"}
	v.Environment = map[string]string{"VALUE": "quotes\" \\ %n $HOME\n[Service]\nExecStartPre=malicious"}
	svc.Containers["web"] = v
	cfg.Services["website"] = svc
	if err := q.Validate(context.Background(), cfg, "service/website"); err != nil {
		t.Fatal(err)
	}
	files, err := RenderQuadlet(c.Store, cfg, "service/website")
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range files {
		if strings.Contains(string(data), "\nExecStartPre=") {
			t.Fatal("unescaped directive injection")
		}
	}
}
