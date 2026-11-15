package declarative

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-man/internal/config"
)

func TestUnifiedServerSettingsSurviveResourceEditing(t *testing.T) {
	c, _, _ := coordinatorFixture(t)
	cfg, err := Parse([]byte("version: 1\nserver:\n  listen: 127.0.0.1:8181\n  allow_writes: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.EditResource("storage", "documents", "save", `{"path":"data/documents"}`, snapshot.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SaveApply(context.Background(), next, snapshot.Revision, "next_start")
	if err != nil {
		t.Fatal(err)
	}
	disk, err := c.Store.Read()
	if err != nil || disk.Config.Server == nil || disk.Config.Server.Listen != "127.0.0.1:8181" || !disk.Config.Server.AllowWrites {
		t.Fatal("settings lost", err)
	}
	loaded, err := config.Load(c.Store.Path)
	if err != nil || loaded.Listen != "127.0.0.1:8181" {
		t.Fatal(loaded, err)
	}
}

func TestServerSettingsRequireReviewedRestartWithoutNativeDriftApply(t *testing.T) {
	c, cfg, adapter := coordinatorFixture(t)
	running, err := config.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	c.RunningServer = &running
	initial, err := c.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	desired := cfg
	settings := running
	settings.Listen = "127.0.0.1:8181"
	desired.Server = &settings
	plan, err := c.Preview(context.Background(), desired, initial.Revision)
	if err != nil || !plan.RestartRequired || len(plan.Changes) != 0 {
		t.Fatal(plan, err)
	}
	// The unchanged fixture resource is absent natively. A settings-only save must
	// not repair drift using the old process's adapter settings.
	journal, err := c.SaveApply(context.Background(), desired, initial.Revision, "now")
	if err != nil || journal.Stage != "complete" {
		t.Fatal(journal, err)
	}
	if len(adapter.calls) > 0 {
		t.Fatal("settings-only save applied resources", adapter.calls)
	}
	health := c.ConfigurationReadiness(context.Background())
	found := false
	for _, issue := range health.Health.Issues {
		if issue.Code == "restart_required" {
			found = true
		}
	}
	if !found {
		t.Fatal("restart not reported")
	}
	if err = c.ServiceAction(context.Background(), "website", "start"); err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatal("pending settings allowed lifecycle", err)
	}
	c.RunningServer = &settings
	if err = c.checkServerReady(); err != nil {
		t.Fatal("restart did not clear blocker", err)
	}
}

func TestServerAndResourceChangesCannotApplyTogether(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	running, _ := config.Defaults()
	c.RunningServer = &running
	snapshot, err := c.Store.Save(cfg, "missing")
	if err != nil {
		t.Fatal(err)
	}
	settings := running
	settings.Listen = "127.0.0.1:8181"
	cfg.Server = &settings
	service := cfg.Services["website"]
	service.Enabled = !service.Enabled
	cfg.Services["website"] = service
	if _, err = c.Preview(context.Background(), cfg, snapshot.Revision); err == nil {
		t.Fatal("combined change accepted")
	}
	if _, err = c.SaveApply(context.Background(), cfg, snapshot.Revision, "now"); err == nil {
		t.Fatal("combined change saved")
	}
	disk, err := c.Store.Read()
	if err != nil || disk.Revision != snapshot.Revision {
		t.Fatal("rejected save changed file", err)
	}
}

func TestUnifiedConfigurationRejectsInvalidServerSettings(t *testing.T) {
	for _, body := range []string{"version: 1\nserver: {unknown: private-value}", "version: 1\nserver: {listen: 'private-value'}", "version: 1\nserver: {allow_writes: true, allow_writes: false}", "version: 1\nmanifestPath: private-value"} {
		if _, err := Parse([]byte(body)); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid settings accepted or exposed", err)
		}
	}
	path := filepath.Join(t.TempDir(), "server-man.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nserver: {listen: '127.0.0.1:8181'}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil || loaded.Path != path || loaded.Listen != "127.0.0.1:8181" {
		t.Fatal(loaded, err)
	}
}

func TestPendingServerRestartAppearsOnEachResource(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	running, err := config.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	c.RunningServer = &running
	settings := running
	settings.Listen = "127.0.0.1:8181"
	cfg.Server = &settings
	cfg.Shares = map[string]Share{"documents": {Protocol: "nfs", Storage: "content", Clients: []string{"192.0.2.0/24"}}}
	if _, err = c.Store.Save(cfg, "missing"); err != nil {
		t.Fatal(err)
	}
	c.HealthCache = &HealthCache{}
	for _, refresh := range []bool{true, false} {
		health := c.Preflight(context.Background(), cfg, refresh)
		for _, resource := range []string{"configuration", "service/website", "share/documents"} {
			found := 0
			for _, issue := range health.Issues {
				if issue.Resource == resource && issue.Code == "restart_required" {
					found++
				}
			}
			if found != 1 {
				t.Fatalf("%s has %d restart blockers (refresh=%t)", resource, found, refresh)
			}
		}
	}
	status, err := c.ServiceStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	services := status.(map[string]any)["services"].([]ServiceStatus)
	if len(services) != 1 {
		t.Fatal(services)
	}
	found := false
	for _, issue := range services[0].Issues {
		if issue.Code == "restart_required" {
			found = true
		}
	}
	if !found {
		t.Fatal("application status omitted the global blocker")
	}
	c.RunningServer = &settings
	for _, issue := range c.Preflight(context.Background(), cfg, false).Issues {
		if issue.Code == "restart_required" {
			t.Fatal("old restart blocker retained in native cache")
		}
	}
}
