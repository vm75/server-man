package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"server-man/internal/core"
)

func incusFixture(t *testing.T) (*Incus, Config, *incusRecorder) {
	t.Helper()
	c, err := Parse([]byte(vmExample))
	if err != nil {
		t.Fatal(err)
	}
	s := c.Services["guest"]
	s.Backend = "incus"
	s.VM.Image = "ubuntu-24.04"
	s.VM.Pool = "pool"
	s.VM.Project = "project"
	c.Services["guest"] = s
	c.Bindings["pool"] = Binding{Kind: "pool", Name: "default"}
	c.Bindings["project"] = Binding{Kind: "project", Name: "managed"}
	c, err = PrepareIdentities(c, Config{})
	if err != nil {
		t.Fatal(err)
	}
	r := &incusRecorder{}
	i := &Incus{Store: Store{Path: filepath.Join(t.TempDir(), "config.yaml")}, Program: "incus", QEMU: "qemu-system-x86_64", Directory: filepath.Join(t.TempDir(), "generated"), Runner: r}
	return i, c, r
}

type incusRecorder struct {
	commands       []core.Command
	actual         incusInstanceInfo
	exists         bool
	payloads       []map[string]any
	failStop       bool
	failSpice      bool
	spiceHelpError error
}

func incusRequest(c core.Command) (method, path string) {
	for j, arg := range c.Args {
		if arg == "--request" {
			method = c.Args[j+1]
		}
		if strings.HasPrefix(arg, "local:") {
			path = strings.TrimPrefix(arg, "local:")
		}
	}
	return
}
func (r *incusRecorder) Capture(_ context.Context, c core.Command) (string, error) {
	r.commands = append(r.commands, c)
	if c.Program == "qemu-system-x86_64" {
		if !slices.Equal(c.Args, []string{"-spice", "help"}) || c.Timeout != 3*time.Second {
			return "", errors.New("unsafe capability probe")
		}
		if r.failSpice {
			return "", errors.New("private-runtime-diagnostic")
		}
		return "spice options:\n  unix=<bool (on/off)>\n  disable-ticketing=<bool (on/off)>\n", r.spiceHelpError
	}
	_, path := incusRequest(c)
	u, _ := url.Parse(path)
	var metadata any = map[string]any{}
	switch u.Path {
	case "/1.0/instances":
		metadata = []string{}
		if r.exists {
			metadata = []string{"/1.0/instances/" + r.actual.Name + "?project=managed"}
		}
	case "/1.0/instances/sm-guest":
		if !r.exists {
			return "", errors.New("fixture missing")
		}
		metadata = r.actual
	}
	data, _ := json.Marshal(map[string]any{"type": "sync", "error_code": 0, "metadata": metadata})
	return string(data), nil
}
func (r *incusRecorder) Run(_ context.Context, c core.Command, _ io.Writer) error {
	r.commands = append(r.commands, c)
	if len(c.Args) < 5 || c.Args[0] != "--force-local" || c.Args[1] != "--project" || c.Args[2] != "managed" {
		return errors.New("unscoped fixture mutation")
	}
	operation := c.Args[3]
	var data []byte
	var fields map[string]any
	if operation == "init" || operation == "config" {
		var err error
		data, err = io.ReadAll(c.Stdin)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &fields); err != nil {
			return err
		}
	} else {
		if slices.Contains(c.Args, "--force") {
			return errors.New("forced operation forbidden")
		}
		fields = map[string]any{"action": operation, "force": false}
	}
	r.payloads = append(r.payloads, fields)
	if operation == "init" {
		if !slices.Contains(c.Args, "--vm") || !slices.Contains(c.Args, "--no-profiles") {
			return errors.New("unscoped guest creation")
		}
		if err := json.Unmarshal(data, &r.actual); err != nil {
			return err
		}
		r.actual.Name, r.actual.Type = strings.TrimPrefix(c.Args[5], "local:"), "virtual-machine"
		r.exists = true
		r.actual.StatusCode = 102
		r.actual.Status = "Stopped"
		r.actual.Config["volatile.uuid"] = "daemon-owned-uuid"
		r.actual.Config["volatile.vm.rtc_offset"] = "123"
	}
	if operation == "config" {
		if c.Args[4] != "edit" || c.Args[5] != "local:sm-guest" {
			return errors.New("unexpected configuration edit")
		}
		var definition incusDefinition
		if err := json.Unmarshal(data, &definition); err != nil {
			return err
		}
		r.actual.incusDefinition = definition
	}
	if operation == "start" || operation == "stop" {
		if c.Args[4] != "local:sm-guest" {
			return errors.New("unexpected state target")
		}
		if operation == "start" {
			r.actual.StatusCode = 103
			r.actual.Status = "Running"
		} else {
			if !slices.Equal(c.Args[5:], []string{"--timeout", "90"}) {
				return errors.New("unbounded or forced stop")
			}
			if r.failStop {
				return errors.New("fixture stop failure")
			}
			r.actual.StatusCode = 102
			r.actual.Status = "Stopped"
		}
	}
	return nil
}
func TestIncusStoppedCreationExplicitBindingsAndNoProfiles(t *testing.T) {
	i, c, r := incusFixture(t)
	s := c.Services["guest"]
	s.Description = "private-definition-value"
	c.Services["guest"] = s
	if err := i.Validate(context.Background(), c, "service/guest"); err != nil {
		t.Fatal(err)
	}
	if err := i.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	obs, err := i.Inspect(context.Background(), c, "service/guest")
	if err != nil || !obs.Matches || obs.Active {
		t.Fatal(obs, err)
	}
	if len(r.actual.Profiles) != 0 || r.actual.Devices["root"]["pool"] != "default" || r.actual.Devices["eth0"]["network"] != "default" {
		t.Fatal(r.actual)
	}
	for _, cmd := range r.commands {
		if strings.Contains(strings.Join(cmd.Args, " "), "private-definition-value") || slices.Contains(cmd.Args, "--data-file") {
			t.Fatal("private definition or unsupported data flag in arguments", cmd.Args)
		}
		if len(cmd.Args) == 0 || cmd.Args[0] != "--force-local" {
			t.Fatal("ambient remote allowed", cmd.Args)
		}
		_, path := incusRequest(cmd)
		if strings.Contains(path, "/instances") && !strings.Contains(path, "project=managed") {
			t.Fatal("ambient project allowed", path)
		}
		if cmd.Args[1] != "query" && (cmd.Args[1] != "--project" || cmd.Args[2] != "managed") {
			t.Fatal("ambient mutation project allowed", cmd.Args)
		}
	}
}

func TestIncusVMReadinessChecksQEMUSpiceWithoutStarting(t *testing.T) {
	i, _, r := incusFixture(t)
	if issues := i.Prerequisites(context.Background()); len(issues) != 0 {
		t.Fatal(issues)
	}
	r.spiceHelpError = exec.Command("false").Run()
	if r.spiceHelpError == nil {
		t.Fatal("exit fixture failed")
	}
	if issues := i.Prerequisites(context.Background()); len(issues) != 0 {
		t.Fatal("QEMU help's exit 1 must be accepted with complete expected options", issues)
	}
	r.spiceHelpError = errors.New("private-runtime-diagnostic")
	if issues := i.Prerequisites(context.Background()); len(issues) != 1 || issues[0].Code != "qemu_spice_unavailable" {
		t.Fatal("unexpected failure accepted", issues)
	}
	r.failSpice = true
	issues := i.Prerequisites(context.Background())
	if len(issues) != 1 || issues[0].Code != "qemu_spice_unavailable" || strings.Contains(issues[0].Message, "private-runtime") {
		t.Fatal(issues)
	}
	i.QEMU = ""
	issues = i.Prerequisites(context.Background())
	if len(issues) != 1 || issues[0].Field != "qemu" || issues[0].Code != "missing_dependency" {
		t.Fatal(issues)
	}
	if len(r.payloads) != 0 {
		t.Fatal("readiness mutated native state")
	}
}

func TestIncusAbsentCreationCanBeRemovedWithoutNativeDeletion(t *testing.T) {
	i, cfg, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	r.exists = false // a failed/missing native instance; owned generated file remains
	r.commands = nil
	if err := i.Apply(context.Background(), cfg, Config{Version: 1}, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	if removed, err := i.VerifyRemoved(context.Background(), cfg, "service/guest"); err != nil || !removed {
		t.Fatal(removed, err)
	}
	for _, cmd := range r.commands {
		method, _ := incusRequest(cmd)
		if method != "GET" {
			t.Fatal("absence cleanup mutated native state", cmd.Args)
		}
	}
	r.exists = true
	if removed, err := i.VerifyRemoved(context.Background(), cfg, "service/guest"); err != nil || removed {
		t.Fatal("native instance ignored", removed, err)
	}
}
func TestIncusUpdatePreservesVolatileAndDefersRunningHardware(t *testing.T) {
	i, c, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	if err := i.Action(context.Background(), c, "service/guest", "start"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode(c)
	next, _ := Parse(raw)
	next.Services["guest"].VM.MemoryMiB = 2048
	if err := i.Apply(context.Background(), c, next, "service/guest", "now"); err == nil {
		t.Fatal("running hardware implicitly changed")
	}
	if err := i.Apply(context.Background(), c, next, "service/guest", "next_start"); err != nil {
		t.Fatal(err)
	}
	if r.actual.Config["volatile.uuid"] != "daemon-owned-uuid" || r.actual.Config["volatile.vm.rtc_offset"] != "123" {
		t.Fatal("volatile identity changed")
	}
	obs, err := i.Inspect(context.Background(), next, "service/guest")
	if err != nil || !obs.Matches || !obs.Active || obs.ActiveMatches {
		t.Fatal(obs, err)
	}
	if err = i.Action(context.Background(), next, "service/guest", "restart"); err != nil {
		t.Fatal(err)
	}
	obs, err = i.Inspect(context.Background(), next, "service/guest")
	if err != nil || !obs.ActiveMatches {
		t.Fatal("full restart did not load configuration", obs, err)
	}
}
func TestIncusOwnershipAndRemovalProtectData(t *testing.T) {
	i, c, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	r.actual.Config["user.server-man.identity"] = "foreign"
	before := len(r.payloads)
	if err := i.Apply(context.Background(), c, c, "service/guest", "now"); err == nil {
		t.Fatal("foreign instance changed")
	}
	if len(r.payloads) != before {
		t.Fatal("foreign mutation reached daemon")
	}
	if err := i.Apply(context.Background(), c, Config{Version: 1}, "service/guest", "now"); err == nil {
		t.Fatal("Incus deletion bypassed retention workflow")
	}
	for _, cmd := range r.commands {
		method, _ := incusRequest(cmd)
		if method == "DELETE" {
			t.Fatal("instance root storage deleted")
		}
	}
}
func TestIncusBootPolicyDoesNotInterruptGuest(t *testing.T) {
	i, c, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	if err := i.Action(context.Background(), c, "service/guest", "start"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode(c)
	next, _ := Parse(raw)
	s := next.Services["guest"]
	s.Enabled = true
	next.Services["guest"] = s
	r.commands = nil
	if err := i.Apply(context.Background(), c, next, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.commands {
		if len(cmd.Args) > 3 && (cmd.Args[3] == "start" || cmd.Args[3] == "stop") {
			t.Fatal("boot policy changed execution")
		}
	}
	if r.actual.Config["boot.autostart"] != "true" {
		t.Fatal("native autostart not set")
	}
}
func TestIncusShutdownFailureNeverForcesStart(t *testing.T) {
	i, c, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	if err := i.Action(context.Background(), c, "service/guest", "start"); err != nil {
		t.Fatal(err)
	}
	r.failStop = true
	r.payloads = nil
	if err := i.Action(context.Background(), c, "service/guest", "restart"); err == nil {
		t.Fatal("shutdown failure ignored")
	}
	for _, payload := range r.payloads {
		if payload["action"] == "start" || payload["force"] == true {
			t.Fatal("failed shutdown led to forced restart")
		}
	}
}

func TestIncusReprovisionAndShrinkAreExplicitlyRejected(t *testing.T) {
	i, c, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"image", "shrink", "pool"} {
		raw, _ := Encode(c)
		next, _ := Parse(raw)
		switch change {
		case "image":
			next.Services["guest"].VM.Image = "different-image"
		case "shrink":
			next.Services["guest"].VM.RootSizeGiB = 1
		case "pool":
			next.Bindings["pool"] = Binding{Kind: "pool", Name: "another"}
		}
		before := len(r.payloads)
		if err := i.Apply(context.Background(), c, next, "service/guest", "next_start"); err == nil {
			t.Fatal(change, "was silently accepted")
		}
		if len(r.payloads) != before {
			t.Fatal("invalid reprovisioning reached native mutation")
		}
	}
}
func TestIncusPrerequisitesAreResourceScoped(t *testing.T) {
	i, c, _ := incusFixture(t)
	delete(c.Bindings, "pool")
	issues := i.ResourcePrerequisites(context.Background(), c, "service/guest")
	if len(issues) == 0 || issues[0].Resource != "service/guest" || issues[0].Remedy == "" {
		t.Fatal(issues)
	}
}

func TestIncusNativeNamesArePortableAndDistinct(t *testing.T) {
	for _, id := range []string{"guest", strings.Repeat("a", 63), "database_server"} {
		name := incusInstance(id)
		if len(name) > 63 || strings.Contains(name, "_") || strings.HasSuffix(name, "-") {
			t.Fatal(name)
		}
	}
	if incusInstance("database_server") == incusInstance("database-server") {
		t.Fatal("sanitized names collide")
	}
}
