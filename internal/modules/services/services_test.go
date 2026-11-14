package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"serverman/internal/core"
)

type recorder struct {
	commands []core.Command
	capture  func(core.Command) (string, error)
	run      func(core.Command, io.Writer) error
}

func (r *recorder) Capture(ctx context.Context, c core.Command) (string, error) {
	r.commands = append(r.commands, c)
	return r.capture(c)
}
func (r *recorder) Run(ctx context.Context, c core.Command, out io.Writer) error {
	r.commands = append(r.commands, c)
	if r.run != nil {
		return r.run(c, out)
	}
	_, err := io.WriteString(out, "fixture service operation\n")
	return err
}

const unitFiles = `[{"unit_file":"demo.service","state":"enabled","preset":"enabled"},{"unit_file":"idle.service","state":"disabled","preset":"enabled"}]`
const units = `[{"unit":"demo.service","load":"loaded","active":"active","sub":"running","description":"Demo daemon","job_id":0,"private":"PRIVATE"},{"unit":"transient.service","load":"loaded","active":"failed","sub":"failed","description":"Transient"}]`
const info = "Id=demo.service\nDescription=Demo daemon\nLoadState=loaded\nActiveState=active\nSubState=running\nUnitFileState=enabled\nMainPID=123\nResult=success\nExecMainCode=0\nExecMainStatus=0\nActiveEnterTimestamp=Fri 2026-10-16 00:00:00 UTC\nInactiveEnterTimestamp=\nFragmentPath=/etc/systemd/system/demo.service\nCanStart=yes\nCanStop=yes\nCanReload=no\nEnvironment=PRIVATE=value\nExecStart=PRIVATE\nStatusText=PRIVATE\n"

func fixture(scope string) (*Backend, *recorder) {
	r := &recorder{capture: func(c core.Command) (string, error) {
		switch {
		case slices.Contains(c.Args, "list-unit-files"):
			return unitFiles, nil
		case slices.Contains(c.Args, "list-units"):
			return units, nil
		default:
			return info, nil
		}
	}}
	b := New(scope, r)
	b.systemctl = "systemctl"
	b.journalctl = "journalctl"
	return b, r
}

func TestInventoryMergeContextsAndWhitelistedDetails(t *testing.T) {
	for _, scope := range []string{"system", "user"} {
		t.Run(scope, func(t *testing.T) {
			b, r := fixture(scope)
			inv, err := b.List(context.Background())
			if err != nil || len(inv.Services) != 3 || inv.Scope != scope || !inv.Available || len(inv.Warnings) != 0 {
				t.Fatal(inv, err)
			}
			if inv.Services[0].Name != "demo.service" || inv.Services[0].Active != "active" || inv.Services[0].Enabled != "enabled" || inv.Services[1].Active != "unknown" {
				t.Fatal(inv.Services)
			}
			for _, c := range r.commands {
				if c.Program != "systemctl" || c.Args[0] != "--"+scope || c.Args[1] != "--no-pager" || c.Args[2] != "--no-ask-password" || c.Timeout != 12*time.Second {
					t.Fatal(c)
				}
			}
			d, err := b.Details(context.Background(), "demo.service")
			if err != nil || d.MainPID == nil || *d.MainPID != 123 || d.CanReload || !d.CanStart || !d.CanStop || d.Result != "success" {
				t.Fatal(d, err)
			}
			data, _ := json.Marshal(d)
			if strings.Contains(string(data), "PRIVATE") || strings.Contains(string(data), "Environment") || strings.Contains(string(data), "ExecStart") {
				t.Fatal("private properties leaked")
			}
			c := r.commands[2]
			want := []string{"--" + scope, "--no-pager", "--no-ask-password", "show", "--property=" + strings.Join(properties, ","), "--", "demo.service"}
			if !reflect.DeepEqual(c.Args, want) {
				t.Fatal(c)
			}
		})
	}
}

func TestEveryLifecycleUsesExactUnitAndEnablementDoesNotChangeRuntime(t *testing.T) {
	for _, scope := range []string{"system", "user"} {
		for _, action := range []string{"start", "stop", "restart", "reload", "enable", "disable"} {
			b, r := fixture(scope)
			if _, err := b.Action(context.Background(), "demo@one.service", action, io.Discard); err != nil {
				t.Fatal(err)
			}
			want := []string{"--" + scope, "--no-pager", "--no-ask-password", action, "--", "demo@one.service"}
			if len(r.commands) != 1 || !reflect.DeepEqual(r.commands[0].Args, want) {
				t.Fatal(r.commands)
			}
			if slices.Contains(r.commands[0].Args, "--now") {
				t.Fatal("enablement changed current runtime")
			}
		}
	}
}

func TestUnitValidationRejectsPatternsPathsFlagsAndOtherUnitTypes(t *testing.T) {
	b, r := fixture("system")
	for _, name := range []string{"ssh.service", "worker@a-b.service", "template@.service", `container\x2dexample.service`, "dbus-:1.20-org.example.service"} {
		if !validUnit(name) {
			t.Fatalf("valid unit rejected: %q", name)
		}
	}
	for _, name := range []string{"ssh", "--all.service", "*.service", "foo?.service", "/etc/systemd/foo.service", "foo.socket", "foo.service bar.service", "foo.service\nrestart", `foo\bar.service`, strings.Repeat("a", 256) + ".service", ""} {
		if validUnit(name) {
			t.Fatalf("invalid unit accepted: %q", name)
		}
		if _, err := b.Details(context.Background(), name); err == nil {
			t.Fatal("invalid details accepted")
		}
		if _, err := b.Action(context.Background(), name, "start", io.Discard); err == nil {
			t.Fatal("invalid action accepted")
		}
		if _, err := b.Logs(context.Background(), name, 100); err == nil {
			t.Fatal("invalid logs accepted")
		}
	}
	if _, err := b.Action(context.Background(), "demo.service", "mask", io.Discard); err == nil {
		t.Fatal("unsupported action accepted")
	}
	if len(r.commands) != 0 {
		t.Fatal("invalid request executed")
	}
}

func TestJSONAndLegacyInventories(t *testing.T) {
	files, err := parseList("demo.service enabled enabled\nidle.service disabled -\n", true)
	if err != nil || len(files) != 2 || files[0].Enabled != "enabled" {
		t.Fatal(files, err)
	}
	loaded, err := parseList("demo.service loaded active running Demo daemon\ntransient.service loaded activating start start Transient with pending job\n", false)
	if err != nil || len(loaded) != 2 || loaded[1].Active != "activating" || loaded[1].Description != "" {
		t.Fatal(loaded, err)
	}
	for _, files := range []bool{true, false} {
		for _, bad := range []string{"null", "{}", `[{"unit":"demo.service"}]`, "invalid output", "foo.socket loaded active running"} {
			if _, err := parseList(bad, files); err == nil {
				t.Fatalf("invalid inventory accepted: %s", bad)
			}
		}
		for _, empty := range []string{"", "[]"} {
			items, err := parseList(empty, files)
			if err != nil || len(items) != 0 {
				t.Fatal(items, err)
			}
		}
	}
	if _, err := parseList(strings.Repeat("demo.service enabled -\n", 4097), true); err == nil {
		t.Fatal("unbounded inventory")
	}
}

func TestPartialRuntimeFailureAndMissingTools(t *testing.T) {
	b, r := fixture("system")
	original := r.capture
	r.capture = func(c core.Command) (string, error) {
		if slices.Contains(c.Args, "list-units") {
			return "", errors.New("PRIVATE bus failure")
		}
		return original(c)
	}
	inv, err := b.List(context.Background())
	if err != nil || len(inv.Services) != 2 || len(inv.Warnings) != 1 || inv.Services[0].Active != "unknown" || strings.Contains(inv.Warnings[0], "PRIVATE") {
		t.Fatal(inv, err)
	}
	r.capture = func(core.Command) (string, error) { return "unsupported output", nil }
	inv, err = b.List(context.Background())
	if err != nil || len(inv.Warnings) != 2 || len(inv.Services) != 0 {
		t.Fatal(inv, err)
	}
	b.systemctl = ""
	b.journalctl = ""
	r.commands = nil
	inv, err = b.List(context.Background())
	if err != nil || inv.Available || inv.JournalAvailable || inv.Reason == "" {
		t.Fatal(inv, err)
	}
	if _, err := b.Action(context.Background(), "demo.service", "start", io.Discard); err == nil {
		t.Fatal("missing backend accepted")
	}
	if _, err := b.Details(context.Background(), "demo.service"); err == nil {
		t.Fatal("missing details accepted")
	}
	if _, err := b.Logs(context.Background(), "demo.service", 100); err == nil {
		t.Fatal("missing journal accepted")
	}
	if len(r.commands) != 0 {
		t.Fatal("missing tools executed")
	}
}

func TestBoundedJournalUsesConfiguredScope(t *testing.T) {
	for _, scope := range []string{"system", "user"} {
		b, r := fixture(scope)
		r.run = func(c core.Command, out io.Writer) error {
			_, err := io.WriteString(out, strings.Repeat("x", 140<<10)+"RECENT")
			return err
		}
		logs, err := b.Logs(context.Background(), "demo.service", 0)
		if err != nil || !logs.Truncated || len(logs.Text) != 128<<10 || !strings.HasSuffix(logs.Text, "RECENT") {
			t.Fatal(logs.Truncated, len(logs.Text), err)
		}
		want := []string{"--" + scope, "--no-pager", "--quiet", "--output=short-iso", "--unit", "demo.service", "--lines", "200"}
		if !reflect.DeepEqual(r.commands[0].Args, want) || r.commands[0].Program != "journalctl" || r.commands[0].Timeout != 10*time.Second {
			t.Fatal(r.commands[0])
		}
		for _, tail := range []int{-1, 2001} {
			if _, err := b.Logs(context.Background(), "demo.service", tail); err == nil {
				t.Fatal("invalid line limit accepted")
			}
		}
		r.run = func(core.Command, io.Writer) error { return errors.New("fixture failed") }
		if _, err := b.Logs(context.Background(), "demo.service", 100); err == nil {
			t.Fatal("journal failure hidden")
		}
		if _, err := b.Action(context.Background(), "demo.service", "restart", io.Discard); err == nil {
			t.Fatal("command failure hidden")
		}
	}
}

func TestMissingAndMalformedDetails(t *testing.T) {
	b, r := fixture("system")
	alias, err := b.Details(context.Background(), "alias.service")
	if err != nil || alias.Name != "demo.service" || r.commands[0].Args[len(r.commands[0].Args)-1] != "alias.service" {
		t.Fatal("alias did not resolve to canonical Id", alias, err)
	}
	for _, text := range []string{"Id=demo.service\nLoadState=not-found\n", "garbage", "Id=demo.socket\nLoadState=loaded\nActiveState=active\n"} {
		r.capture = func(core.Command) (string, error) { return text, nil }
		if _, err := b.Details(context.Background(), "demo.service"); err == nil {
			t.Fatal("invalid details accepted")
		}
	}
}

func TestServiceRegistryPolicy(t *testing.T) {
	b, r := fixture("user")
	jobs, err := core.NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	registry := core.NewRegistry(true, jobs)
	if err := b.Register(registry); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Invoke(context.Background(), "services_stop", json.RawMessage(`{"unit":"demo.service"}`), "test"); err == nil {
		t.Fatal("unconfirmed stop accepted")
	}
	if len(r.commands) != 0 {
		t.Fatal("unconfirmed action executed")
	}
	result, err := registry.Invoke(context.Background(), "services_enable", json.RawMessage(`{"unit":"demo.service"}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["job"].(core.Job).ID
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, err := jobs.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			if job.State != "succeeded" {
				t.Fatal(job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job timeout")
		}
		time.Sleep(time.Millisecond)
	}
	if len(r.commands) != 1 {
		t.Fatal(r.commands)
	}
}
