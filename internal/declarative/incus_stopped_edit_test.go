package declarative

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestIncusStoppedEditGuardRunsBeforeJournalAndConfigurationSave(t *testing.T) {
	i, cfg, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	saved, err := i.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Action(context.Background(), cfg, "service/guest", "start"); err != nil {
		t.Fatal(err)
	}
	c := Coordinator{Store: i.Store, Adapters: map[string]Adapter{"incus": i}}
	next := incusMemoryChange(saved.Config)
	for _, mode := range []string{"now", "next_start"} {
		before := len(r.payloads)
		if _, err := c.Preview(context.Background(), next, saved.Revision); !errors.Is(err, errIncusStoppedUpdate) {
			t.Fatal("review accepted running memory edit", err)
		}
		if _, err := c.SaveApply(context.Background(), next, saved.Revision, mode); !errors.Is(err, errIncusStoppedUpdate) {
			t.Fatal("save accepted running memory edit", err)
		}
		if len(r.payloads) != before {
			t.Fatal("pre-save guard mutated native instance")
		}
		current, err := i.Store.Read()
		if err != nil || current.Revision != saved.Revision {
			t.Fatal("blocked candidate replaced authoritative config", err)
		}
		if _, err := c.ReadJournal(); !os.IsNotExist(err) {
			t.Fatal("blocked candidate started a journal", err)
		}
	}
	if err := i.Action(context.Background(), cfg, "service/guest", "stop"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Preview(context.Background(), next, saved.Revision); err != nil {
		t.Fatal("stopped edit failed review", err)
	}
	j, err := c.SaveApply(context.Background(), next, saved.Revision, "next_start")
	if err != nil || j.Stage != "complete" || r.actual.StatusCode != 102 {
		t.Fatal("stopped edit did not register without starting", j, err)
	}
}

func TestIncusRunningBootPolicyDoesNotEditHardware(t *testing.T) {
	i, cfg, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	if err := i.Action(context.Background(), cfg, "service/guest", "start"); err != nil {
		t.Fatal(err)
	}
	oldMemory := r.actual.Config["limits.memory"]
	for _, enabled := range []bool{true, false} {
		s := cfg.Services["guest"]
		s.Enabled = enabled
		next := cfg
		next.Services = map[string]Service{"guest": s}
		before := len(r.commands)
		if err := i.Apply(context.Background(), cfg, next, "service/guest", "next_start"); err != nil {
			t.Fatal("running boot toggle failed", err)
		}
		for _, cmd := range r.commands[before:] {
			if len(cmd.Args) > 4 && cmd.Args[3] == "config" && cmd.Args[4] == "edit" {
				t.Fatal("boot update sent a full hardware definition")
			}
			if len(cmd.Args) > 3 && (cmd.Args[3] == "start" || cmd.Args[3] == "stop") {
				t.Fatal("boot update changed guest execution")
			}
		}
		if r.actual.Config["limits.memory"] != oldMemory || r.actual.StatusCode != 103 {
			t.Fatal("boot policy changed hardware or guest runtime")
		}
		obs, err := i.Inspect(context.Background(), next, "service/guest")
		if err != nil || !obs.Matches || !obs.ActiveMatches || !obs.Active {
			t.Fatal("boot policy left native drift", obs, err)
		}
		cfg = next
	}
}

func TestIncusGuardRefusesSavedFailedHardwareWhileRunning(t *testing.T) {
	i, cfg, r := incusFixture(t)
	if err := i.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	r.actual.StatusCode, r.actual.Status = 103, "Running"
	// Simulate a prior failed save: desired and previous YAML already request
	// more memory, but the actual native running configuration still has less.
	next := incusMemoryChange(cfg)
	if err := i.ValidateTransition(context.Background(), next, next, "service/guest"); !errors.Is(err, errIncusStoppedUpdate) {
		t.Fatal("native drift bypassed stop rule", err)
	}
	if err := i.Apply(context.Background(), next, next, "service/guest", "next_start"); !errors.Is(err, errIncusStoppedUpdate) {
		t.Fatal("idempotent YAML bypassed live hardware guard", err)
	}
	if err := i.Action(context.Background(), cfg, "service/guest", "stop"); err != nil {
		t.Fatal(err)
	}
	if err := i.ValidateTransition(context.Background(), next, next, "service/guest"); err != nil {
		t.Fatal("stopped pending hardware could not be recovered", err)
	}
	if !strings.Contains(errIncusStoppedUpdate.Error(), "Stop") && !strings.Contains(errIncusStoppedUpdate.Error(), "stop") {
		t.Fatal("stop requirement is not actionable")
	}
}
