package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"server-man/internal/core"
)

type managedLogRecorder struct {
	commands []core.Command
	output string
	fail bool
}
func (r *managedLogRecorder) Capture(context.Context, core.Command) (string, error) { return "", nil }
func (r *managedLogRecorder) Run(_ context.Context, cmd core.Command, out io.Writer) error {
	r.commands = append(r.commands, cmd)
	if r.fail { return errors.New("SECRET backend error must not reach API") }
	_, _ = io.WriteString(out, r.output)
	return nil
}
func TestManagedLogsUseExactComposeIdentityAndBoundOutput(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	service := cfg.Services["website"]
	service.Backend = "compose"
	cfg.Services["website"] = service
	if _, err := c.Store.Save(cfg, "missing"); err != nil { t.Fatal(err) }
	recorder := &managedLogRecorder{output: strings.Repeat("log entry\n", 20000)}
	p := &Compose{Store:c.Store, Engine:"podman", Program:"/usr/bin/podman", Runner:recorder}
	c.Adapters["podman/compose"] = p
	logs, err := c.ServiceLogs(context.Background(), "website", 100)
	if err != nil || !logs.Truncated || len(logs.Text) > 128 << 10 || logs.Source != "podman-compose" {
		t.Fatal(logs.Source, logs.Truncated, len(logs.Text), err)
	}
	if len(recorder.commands) != 1 { t.Fatal(recorder.commands) }
	cmd := recorder.commands[0]
	if cmd.Program != "/usr/bin/podman" || cmd.Timeout.Seconds() != 10 || !slices.Contains(cmd.Env, "PODMAN_COMPOSE_PROVIDER=podman-compose") {
		t.Fatal(cmd)
	}
	expected := []string{"compose", "--project-name", composeProject("website"), "--env-file", "/dev/null", "--file", p.file("service/website"), "logs", "--tail", "100"}
	if !slices.Equal(cmd.Args, expected) { t.Fatal(cmd.Args, expected) }
	if _, err = c.ServiceLogs(context.Background(), "missing", 1); err == nil { t.Fatal("missing resource read accepted") }
	if _, err = c.ServiceLogs(context.Background(), "website", 2001); err == nil { t.Fatal("unbounded tail accepted") }
	if _, err = c.ServiceLogs(context.Background(), "../website", 1); err == nil { t.Fatal("unsafe ID accepted") }
	if len(recorder.commands) != 1 { t.Fatal("invalid requests invoked native logs") }
	recorder.fail = true
	if _, err = c.ServiceLogs(context.Background(), "website", 1); err == nil || strings.Contains(err.Error(), "SECRET") { t.Fatal("native error exposed", err) }
}
func TestQuadletLogsSelectOnlyConfiguredUserUnits(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	recorder := &managedLogRecorder{output:"one\ntwo\n"}
	q := &Quadlet{Store:c.Store, Journalctl:"/usr/bin/journalctl", Runner:recorder}
	got, err := q.Logs(context.Background(), cfg, "service/website", 17)
	if err != nil || got.Text != recorder.output || got.Source != "systemd-journal" { t.Fatal(got, err) }
	command := recorder.commands[0]
	expected := []string{"--user", "--no-pager", "--quiet", "--output=short-iso", "--lines", "17"}
	for _, unit := range quadletUnits(cfg,"website") { expected=append(expected,"--unit",unit) }
	if !slices.Equal(command.Args, expected) { t.Fatal(command.Args, expected) }
	if slices.Contains(command.Args, "sshd.service") { t.Fatal("unrelated host logs requested") }
}
func TestManagedLogsRegistryIsReadOnlyAndRespectsConfiguration(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	if _, err := c.Store.Save(cfg,"missing"); err != nil { t.Fatal(err) }
	recorder := &managedLogRecorder{output:"from journal\n"}
	c.Adapters["podman/quadlet"] = &Quadlet{Store:c.Store, Journalctl:"journalctl", Runner:recorder}
	r := core.NewRegistry(false,nil)
	if err := RegisterManagedLogs(r,c); err != nil { t.Fatal(err) }
	request,_ := json.Marshal(map[string]any{"service":"website"})
	value,err := r.Invoke(context.Background(),"managed_service_logs",request,"test")
	if err != nil || value.(ManagedLogs).Text != recorder.output { t.Fatal(value,err) }
	if len(recorder.commands)!=1 || !slices.Contains(recorder.commands[0].Args,"200") { t.Fatal("default tail missing",recorder.commands) }
	if err=os.WriteFile(c.Store.Path,[]byte("invalid: ["),0600); err!=nil { t.Fatal(err) }
	if _,err=r.Invoke(context.Background(),"managed_service_logs",request,"test");err==nil { t.Fatal("broken configuration allowed managed native log read") }
	if len(recorder.commands)!=1 { t.Fatal("broken configuration invoked native log reader") }
}

func TestIncusNativeLogsKeepOnlyRequestedRecentLines(t *testing.T) {
	for _, tc := range []struct{
		text string
		n int
		want string
		clipped bool
	}{
		{"first\nsecond\nthird\n",2,"second\nthird\n",true},
		{"first\nsecond\nthird",1,"third",true},
		{"first\nsecond\n",2,"first\nsecond\n",false},
		{"single",1,"single",false},
	} {
		got, clipped := lastManagedLines(tc.text,tc.n)
		if got != tc.want || clipped != tc.clipped { t.Fatalf("%q/%d => %q/%t",tc.text,tc.n,got,clipped) }
	}
}
