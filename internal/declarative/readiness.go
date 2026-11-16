package declarative

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"sync"
	"time"
)

// PrerequisiteAdapter checks native requirements once per backend, independently
// of per-resource definition syntax and without activating resources.
type PrerequisiteAdapter interface{ Prerequisites(context.Context) []Issue }
type ConfigurationPrerequisiteAdapter interface {
	ConfigurationPrerequisites(context.Context, Config) []Issue
}

type ResourcePrerequisiteAdapter interface {
	ResourcePrerequisites(context.Context, Config, string) []Issue
}
type HealthCache struct {
	mu     sync.Mutex
	key    string
	at     time.Time
	issues []Issue
}

func (c Coordinator) Preflight(ctx context.Context, cfg Config, refresh bool) Health {
	h := c.Store.CheckFiles(cfg)
	if c.serverRestartRequired(cfg) {
		h.Issues = append(h.Issues, Issue{"error", "configuration", "server", "restart_required", "Server settings differ from the running process.", "Restart server-man to load the saved server settings before managing resources."})
	}
	// A previous partial apply is a configuration-wide startup blocker. Keep
	// it visible even when native prerequisites were recently cached.
	if journal, err := c.ReadJournal(); err == nil {
		if journal.Stage != "complete" && journal.Stage != "aborted" {
			h.Issues = append(h.Issues, Issue{"error", "configuration", "apply", "apply_incomplete", "An earlier configuration apply has not completed.", "Open Configuration to inspect apply status and explicitly retry or revert it before starting workloads."})
		}
	} else if !os.IsNotExist(err) {
		h.Issues = append(h.Issues, Issue{"unknown", "configuration", "apply", "apply_unavailable", "Previous apply status cannot be verified.", "Inspect the private apply journal before managing workloads."})
	}
	data, _ := Encode(cfg)
	key := digest(append(data, []byte(c.Store.Path)...))
	var native []Issue
	if c.HealthCache != nil {
		c.HealthCache.mu.Lock()
		defer c.HealthCache.mu.Unlock()
		if !refresh && c.HealthCache.key == key && time.Since(c.HealthCache.at) < 10*time.Second {
			native = slices.Clone(c.HealthCache.issues)
			h.RuntimeCheckedAt = c.HealthCache.at
			return propagateReadiness(cfg, h, native)
		}
	}
	targets := map[string]bool{}
	for _, resource := range orderedResources(cfg) {
		targets[backend(cfg, resource)] = true
	}
	for _, target := range slices.Sorted(maps.Keys(targets)) {
		needed := false
		for _, key := range orderedResources(cfg) {
			if backend(cfg, key) == target {
				needed = true
				break
			}
		}
		if !needed {
			continue
		}
		if ctx.Err() != nil {
			native = append(native, Issue{"unknown", "backend/" + target, "runtime", "check_cancelled", "Backend readiness was not checked.", "Run Check configuration again."})
			continue
		}
		if c.Adapters[target] == nil {
			native = append(native, Issue{"error", "backend/" + target, "backend", "missing_adapter", "The selected backend is unavailable in this installation.", "Install/configure a supported backend or change the selected backend."})
			continue
		}
		if probe, ok := c.Adapters[target].(PrerequisiteAdapter); ok {
			native = append(native, probe.Prerequisites(ctx)...)
		} else {
			native = append(native, Issue{"unknown", "backend/" + target, "runtime", "check_unavailable", "Backend prerequisite checks are not implemented.", "Verify backend access before activating services."})
		}
	}
	for _, target := range slices.Sorted(maps.Keys(targets)) {
		if probe, ok := c.Adapters[target].(ConfigurationPrerequisiteAdapter); ok {
			if ctx.Err() != nil {
				native = append(native, Issue{"unknown", "backend/" + target, "runtime", "check_cancelled", "Configuration prerequisites were not checked.", "Recheck when backend access is available."})
				continue
			}
			native = append(native, probe.ConfigurationPrerequisites(ctx, cfg)...)
		}
	}
	for _, resource := range orderedResources(cfg) {
		if _, batch := c.Adapters[backend(cfg, resource)].(ConfigurationPrerequisiteAdapter); batch {
			continue
		}
		if probe, ok := c.Adapters[backend(cfg, resource)].(ResourcePrerequisiteAdapter); ok {
			if ctx.Err() != nil {
				native = append(native, Issue{"unknown", resource, "runtime", "check_cancelled", "Resource prerequisites were not checked.", "Recheck configuration when backend access is available."})
			} else {
				native = append(native, probe.ResourcePrerequisites(ctx, cfg, resource)...)
			}
		}
	}
	h.RuntimeCheckedAt = time.Now().UTC()
	if c.HealthCache != nil {
		c.HealthCache.key = key
		c.HealthCache.at = h.RuntimeCheckedAt
		c.HealthCache.issues = slices.Clone(native)
	}
	return propagateReadiness(cfg, h, native)
}
func propagateReadiness(cfg Config, h Health, native []Issue) Health {
	h.Issues = append(h.Issues, native...)
	sourceIssues := slices.Clone(h.Issues)
	// Backend and configuration-wide issues also belong on affected resources
	// so inventory controls and dependency checks reflect the same blockers.
	for _, key := range orderedResources(cfg) {
		target := backend(cfg, key)
		for _, issue := range sourceIssues {
			if issue.Resource == "backend/"+target || issue.Resource == "configuration" {
				copy := issue
				copy.Resource = key
				h.Issues = append(h.Issues, copy)
			}
		}
	}
	blocked := map[string]bool{}
	for _, issue := range h.Issues {
		if issue.Severity != "warning" && issue.Field != "autostart" {
			blocked[issue.Resource] = true
		}
	}
	for _, key := range orderedResources(cfg) {
		if len(key) < 8 || key[:8] != "service/" {
			continue
		}
		for _, dep := range cfg.Services[key[8:]].DependsOn {
			if blocked["service/"+dep] {
				h.Issues = append(h.Issues, Issue{"error", key, "depends_on", "blocked_dependency", "A required service has startup blockers.", "Repair the dependency's configuration issues and recheck."})
				blocked[key] = true
				break
			}
		}
	}
	return h
}
func (c Coordinator) readinessError(ctx context.Context, cfg Config, id string, boot bool) error {
	for _, issue := range c.Preflight(ctx, cfg, true).Issues {
		if issue.Resource == "service/"+id && issue.Severity != "warning" && (boot || issue.Field != "autostart") {
			return errors.New("service has startup blockers; inspect configuration health")
		}
	}
	return nil
}
