package declarative

import (
	"context"
	"slices"
	"testing"
)

func TestContainerGroupStateIsSeparateFromDefinitionDrift(t *testing.T) {
	_, cfg, _ := coordinatorFixture(t)
	s := cfg.Services["website"]
	s.Containers["worker"] = s.Containers["web"]
	cfg.Services["website"] = s
	fp := runtimeFingerprint(cfg, "service/website")
	for _, tc := range []struct {
		name        string
		containers  []managedContainer
		active      bool
		state, code string
		matches     bool
	}{
		{"stopped", nil, false, "stopped", "", true},
		{"one running", []managedContainer{{"web", "running", fp}}, false, "partially_running", "partial_container_group", true},
		{"partial old revision", []managedContainer{{"web", "running", "old"}}, false, "partially_running", "partial_container_group", false},
		{"all running", []managedContainer{{"web", "running", fp}, {"worker", "running", fp}}, false, "running", "", true},
		{"live unit without containers", nil, true, "unknown", "container_state_mismatch", false},
		{"old member", []managedContainer{{"retired", "running", "old"}}, false, "unknown", "container_state_mismatch", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obs := Observation{Matches: true, ActiveMatches: true, Active: tc.active}
			if err := observeContainerGroup(&obs, cfg, "service/website", tc.containers); err != nil {
				t.Fatal(err)
			}
			if obs.State != tc.state || obs.ActiveMatches != tc.matches || !obs.Matches {
				t.Fatal(obs)
			}
			if tc.code == "" {
				if len(obs.Issues) != 0 {
					t.Fatal(obs.Issues)
				}
			} else if len(obs.Issues) != 1 || obs.Issues[0].Code != tc.code || obs.Issues[0].Severity != "warning" {
				t.Fatal(obs.Issues)
			}
		})
	}
	obs := Observation{ActiveMatches: true}
	if err := observeContainerGroup(&obs, cfg, "service/website", []managedContainer{{"web", "running", fp}, {"web", "running", fp}}); err == nil {
		t.Fatal("duplicate container identity accepted")
	}
}

type statusOnlyAdapter struct{ Adapter }

func (statusOnlyAdapter) Prerequisites(context.Context) []Issue { return nil }

func TestBothContainerAdaptersExposePartialStatusAndRecovery(t *testing.T) {
	for _, backend := range []string{"compose", "quadlet"} {
		t.Run(backend, func(t *testing.T) {
			c, cfg, _ := coordinatorFixture(t)
			s := cfg.Services["website"]
			s.Backend = backend
			s.Containers["worker"] = s.Containers["web"]
			cfg.Services["website"] = s
			if _, err := c.Store.Save(cfg, "missing"); err != nil {
				t.Fatal(err)
			}
			fp := runtimeFingerprint(cfg, "service/website")
			var adapter Adapter
			var set func([]managedContainer)
			if backend == "compose" {
				p, _, r := composeFixture(t)
				p.Store = c.Store
				r.enabled = s.Enabled
				if err := p.Apply(context.Background(), Config{Version: 1}, cfg, "service/website", "now"); err != nil {
					t.Fatal(err)
				}
				adapter = p
				set = func(rows []managedContainer) { r.containers = rows }
			} else {
				r := &quadletRecorder{fingerprint: fp}
				q := &Quadlet{Store: c.Store, Directory: t.TempDir(), Systemctl: "systemctl", Podman: "podman", Runner: r}
				files, err := RenderQuadlet(c.Store, cfg, "service/website")
				if err != nil {
					t.Fatal(err)
				}
				if err = publishArtifacts(q.resourceDir("service/website"), nil, files); err != nil {
					t.Fatal(err)
				}
				adapter = q
				set = func(rows []managedContainer) { r.containers = rows }
			}
			c.Adapters = map[string]Adapter{"podman/" + backend: statusOnlyAdapter{adapter}}
			c.HealthCache = &HealthCache{}
			for _, complete := range []bool{false, true} {
				rows := []managedContainer{{"web", "running", fp}}
				want := "partially_running"
				if complete {
					rows = append(rows, managedContainer{"worker", "running", fp})
					want = "running"
				}
				set(rows)
				result, err := c.ServiceStatuses(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				services := result.(map[string]any)["services"].([]ServiceStatus)
				if len(services) != 1 || services[0].State != want || services[0].Configuration != "registered" {
					t.Fatal(services)
				}
				if slices.ContainsFunc(services[0].Issues, func(i Issue) bool { return i.Code == "partial_container_group" }) == complete {
					t.Fatal(services)
				}
			}
		})
	}
}
