package declarative

import (
	"context"
	"strings"
	"testing"
)

func TestMountInventoryRequiresExpectedFilesystem(t *testing.T) {
	mounts, err := ParseMountInfo(strings.NewReader("24 1 8:1 / / rw - ext4 /dev/root rw\n25 24 8:2 / /srv/data\\040files rw - ext4 /dev/data rw\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := mountedSource("/srv/data files/project", mounts); got != "/dev/data" {
		t.Fatal(got)
	}
	if got := mountedSource("/srv/data files-other", mounts); got != "/dev/root" {
		t.Fatal("prefix escaped mount boundary", got)
	}
	if _, err = ParseMountInfo(strings.NewReader("bad")); err == nil {
		t.Fatal("malformed inventory accepted")
	}
}

type probeAdapter struct {
	fakeAdapter
	count  int
	issues []Issue
}

func (p *probeAdapter) Prerequisites(context.Context) []Issue { p.count++; return p.issues }
func TestNativeReadinessCachePropagationAndRefresh(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	probe := &probeAdapter{issues: []Issue{{"error", "backend/podman/quadlet", "podman", "missing_dependency", "Podman missing.", "Install Podman."}}}
	c.Adapters["podman/quadlet"] = probe
	c.HealthCache = &HealthCache{}
	svc := cfg.Services["website"]
	svc.DependsOn = []string{"database"}
	cfg.Services["website"] = svc
	svc.DependsOn = nil
	cfg.Services["database"] = svc
	first := c.Preflight(context.Background(), cfg, false)
	second := c.Preflight(context.Background(), cfg, false)
	if probe.count != 1 || first.RuntimeCheckedAt.IsZero() || !first.RuntimeCheckedAt.Equal(second.RuntimeCheckedAt) {
		t.Fatal("native checks not cached", probe.count)
	}
	found := false
	for _, issue := range first.Issues {
		if issue.Resource == "service/website" && issue.Code == "blocked_dependency" {
			found = true
		}
	}
	if !found {
		t.Fatal("dependency blocker missing", first)
	}
	c.Preflight(context.Background(), cfg, true)
	if probe.count != 2 {
		t.Fatal("explicit recheck not refreshed")
	}
}
func TestMissingAdapterProducesServiceBlocker(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	c.Adapters = nil
	h := c.Preflight(context.Background(), cfg, false)
	for _, i := range h.Issues {
		if i.Resource == "service/website" && i.Code == "missing_adapter" {
			return
		}
	}
	t.Fatal(h)
}

func TestMissingQuadletDependenciesAreActionable(t *testing.T) {
	q := &Quadlet{Runner: &quadletRecorder{}}
	issues := q.Prerequisites(context.Background())
	count := 0
	for _, issue := range issues {
		if issue.Code == "missing_dependency" {
			count++
			if issue.Resource != "backend/podman/quadlet" || issue.Remedy == "" {
				t.Fatal(issue)
			}
		}
	}
	if count != 3 {
		t.Fatal("missing dependencies not enumerated", issues)
	}
}
func TestDependencyUnknownIsNotReportedReady(t *testing.T) {
	c, cfg, _ := coordinatorFixture(t)
	h := c.Preflight(context.Background(), cfg, false)
	found := false
	for _, issue := range h.Issues {
		if issue.Resource == "service/website" && issue.Code == "check_unavailable" && issue.Severity == "unknown" {
			found = true
		}
	}
	if !found {
		t.Fatal("unchecked fixture backend reported ready", h)
	}
}
