package declarative

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"server-man/internal/core"
)

type userBusRecorder struct {
	output   string
	failure  error
	commands []core.Command
}

func (*userBusRecorder) Run(context.Context, core.Command, io.Writer) error {
	return errors.New("unexpected mutation")
}
func (r *userBusRecorder) Capture(_ context.Context, cmd core.Command) (string, error) {
	r.commands = append(r.commands, cmd)
	if slices.Contains(cmd.Args, "dbus.socket") {
		return r.output, r.failure
	}
	return "yes", nil
}

func TestUserBusReadinessIsScopedBoundedAndBlocksActivation(t *testing.T) {
	for _, tc := range []struct {
		name, output, code, severity string
		failure                      error
	}{
		{name: "active", output: "LoadState=loaded\nActiveState=active\n"},
		{name: "missing", output: "LoadState=not-found\nActiveState=inactive\n", code: "user_bus_unavailable", severity: "error"},
		{name: "inactive", output: "LoadState=loaded\nActiveState=inactive\n", code: "user_bus_unavailable", severity: "error"},
		{name: "masked", output: "LoadState=masked\nActiveState=inactive\n", code: "user_bus_unavailable", severity: "error"},
		{name: "malformed", output: "private-native-value", code: "user_bus_unknown", severity: "unknown"},
		{name: "failed", failure: errors.New("private-native-value"), code: "user_bus_unknown", severity: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &userBusRecorder{output: tc.output, failure: tc.failure}
			issues := userBusIssues(context.Background(), "systemctl", r, "podman/quadlet")
			if tc.code == "" {
				if len(issues) != 0 {
					t.Fatal(issues)
				}
			} else {
				if len(issues) != 1 || issues[0].Code != tc.code || issues[0].Severity != tc.severity || issues[0].Field != "systemd" {
					t.Fatal(issues)
				}
				if strings.Contains(fmt.Sprint(issues), "private-native-value") {
					t.Fatal("native diagnostics exposed")
				}
				c, cfg, _ := coordinatorFixture(t)
				c.Adapters = map[string]Adapter{"podman/quadlet": &probeAdapter{issues: issues}}
				for _, boot := range []bool{false, true} {
					if c.readinessError(context.Background(), cfg, "website", boot) == nil {
						t.Fatal("unavailable bus allowed activation", boot)
					}
				}
			}
			count := 0
			for _, cmd := range r.commands {
				if slices.Contains(cmd.Args, "dbus.socket") {
					count++
					if !slices.Contains(cmd.Args, "--user") || cmd.Timeout != 3*time.Second || !slices.Contains(cmd.Args, "show") {
						t.Fatal(cmd)
					}
				}
			}
			if count != 1 {
				t.Fatal("missing shared user-bus query", r.commands)
			}
		})
	}
}

func TestUserBusRequirementIsSharedByPodmanAndNotDocker(t *testing.T) {
	for _, target := range []string{"quadlet", "podman-compose", "docker-compose"} {
		t.Run(target, func(t *testing.T) {
			r := &userBusRecorder{output: "LoadState=not-found\nActiveState=inactive\n"}
			var probe PrerequisiteAdapter
			if target == "quadlet" {
				probe = &Quadlet{Podman: "podman", Generator: "quadlet", Systemctl: "systemctl", Runner: r}
			} else {
				engine := strings.TrimSuffix(target, "-compose")
				probe = &Compose{Engine: engine, Program: engine, Systemctl: "systemctl", Runner: r}
			}
			issues := probe.Prerequisites(context.Background())
			found := slices.ContainsFunc(issues, func(i Issue) bool { return i.Code == "user_bus_unavailable" })
			if found != (target != "docker-compose") {
				t.Fatal("incorrect backend user-bus requirement", target, issues)
			}
		})
	}
}
