package declarative

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"testing"
)

func incusMemoryChange(c Config) Config {
	c.Services = maps.Clone(c.Services)
	s := c.Services["guest"]
	vm := *s.VM
	vm.MemoryMiB += 512
	s.VM = &vm
	c.Services["guest"] = s
	return c
}

func TestIncusStoppedDefinitionsLoadOnNativeAutostart(t *testing.T) {
	for _, update := range []bool{false, true} {
		t.Run(map[bool]string{false: "creation", true: "stopped edit"}[update], func(t *testing.T) {
			i, cfg, r := incusFixture(t)
			s := cfg.Services["guest"]
			s.Enabled = true
			cfg.Services["guest"] = s
			if err := i.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
				t.Fatal(err)
			}
			if update {
				if err := i.Action(context.Background(), cfg, "service/guest", "start"); err != nil {
					t.Fatal(err)
				}
				// A native stop need not have passed through server-man's Stop.
				r.actual.StatusCode, r.actual.Status = 102, "Stopped"
				next := incusMemoryChange(cfg)
				r.commands = nil
				if err := i.Apply(context.Background(), cfg, next, "service/guest", "now"); err != nil {
					t.Fatal(err)
				}
				cfg = next
				if r.actual.StatusCode != 102 {
					t.Fatal("stopped update started the VM")
				}
				for _, command := range r.commands {
					if len(command.Args) > 3 && command.Args[3] == "start" {
						t.Fatal("update requested start")
					}
				}
			}
			// Incus starts this guest independently of a server-man Start call.
			r.actual.StatusCode, r.actual.Status = 103, "Running"
			observed, err := i.Inspect(context.Background(), cfg, "service/guest")
			if err != nil || !observed.Active || !observed.Matches || !observed.ActiveMatches {
				t.Fatal("fresh native boot falsely needs restart", observed, err)
			}
			if r.actual.Config["volatile.uuid"] != "daemon-owned-uuid" {
				t.Fatal("native identity changed")
			}
		})
	}
}

func TestIncusRunningUpdateRetainsLoadedRuntimeWhenMarkerAbsent(t *testing.T) {
	for _, known := range []bool{true, false} {
		i, cfg, r := incusFixture(t)
		if err := i.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
			t.Fatal(err)
		}
		r.actual.StatusCode, r.actual.Status = 103, "Running"
		delete(r.actual.Config, incusActiveRuntimeKey)
		loaded := r.actual.Config[incusRuntimeKey]
		if !known {
			delete(r.actual.Config, incusRuntimeKey)
			loaded = "unverified"
		}
		next := incusMemoryChange(cfg)
		if err := i.Apply(context.Background(), cfg, next, "service/guest", "next_start"); err != nil {
			t.Fatal(err)
		}
		observed, err := i.Inspect(context.Background(), next, "service/guest")
		if err != nil || !observed.Matches || observed.ActiveMatches || r.actual.Config[incusActiveRuntimeKey] != loaded {
			t.Fatal("running update falsely stamped desired settings as loaded", observed, err)
		}
	}
}

func TestIncusStopTracksNativeSettingsWithoutApplyingFailedDesiredHardware(t *testing.T) {
	i, cfg, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	if err := i.Action(context.Background(), cfg, "service/guest", "start"); err != nil {
		t.Fatal(err)
	}
	loaded, memory := r.actual.Config[incusRuntimeKey], r.actual.Config["limits.memory"]
	r.actual.Config[incusActiveRuntimeKey] = "stale"
	r.actual.Devices["external"] = map[string]string{"type": "disk", "source": "/fixture/unmanaged", "path": "/extra"}
	devices, _ := json.Marshal(r.actual.Devices)
	if err := i.Action(context.Background(), incusMemoryChange(cfg), "service/guest", "stop"); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(r.actual.Devices)
	if r.actual.StatusCode != 102 || r.actual.Config["limits.memory"] != memory || r.actual.Config[incusActiveRuntimeKey] != loaded || string(after) != string(devices) {
		t.Fatal("Stop changed hardware or lost runtime tracking")
	}
}

func TestIncusTrackingFailureLeavesVMStoppedAndDiagnosticsPrivate(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		i, cfg, r := incusFixture(t)
		if err := i.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
			t.Fatal(err)
		}
		if err := i.Action(context.Background(), cfg, "service/guest", "start"); err != nil {
			t.Fatal(err)
		}
		r.actual.Config[incusActiveRuntimeKey] = "stale"
		if invalid {
			r.actual.Config[incusRuntimeKey] = "private-invalid-native-value"
		} else {
			r.failTracking = true
		}
		r.commands = nil
		err := i.Action(context.Background(), cfg, "service/guest", "restart")
		if err == nil || !strings.Contains(err.Error(), "VM stopped") || strings.Contains(err.Error(), "private") || r.actual.StatusCode != 102 {
			t.Fatal(err, r.actual.StatusCode)
		}
		for _, command := range r.commands {
			if strings.Contains(strings.Join(command.Args, " "), "private") || len(command.Args) > 3 && command.Args[3] == "start" {
				t.Fatal("invalid tracking leaked or caused a restart")
			}
		}
	}
}
