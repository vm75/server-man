package declarative

import (
	"context"
	"os"

	"io"
	"server-man/internal/core"
)

type ServiceStatus struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Kind          string  `json:"kind"`
	Backend       string  `json:"backend"`
	Enabled       bool    `json:"enabled"`
	State         string  `json:"state"`
	Registered    bool    `json:"registered"`
	Configuration string  `json:"configuration"`
	Issues        []Issue `json:"issues"`
}

type ReadinessStatus struct {
	Configured bool    `json:"configured"`
	Valid      bool    `json:"valid"`
	Error      string  `json:"error,omitempty"`
	Health     *Health `json:"health,omitempty"`
}

// ConfigurationReadiness does not inspect running workloads. It supplies the
// same bounded prerequisites to global UI alerts and MCP clients.
func (c Coordinator) ConfigurationReadiness(ctx context.Context) ReadinessStatus {
	snapshot, err := c.Store.Read()
	if os.IsNotExist(err) {
		return ReadinessStatus{}
	}
	if err != nil {
		return ReadinessStatus{Configured: true, Error: "Configuration is invalid or inaccessible; open Configuration to repair it."}
	}
	health := c.Preflight(ctx, snapshot.Config, false)
	return ReadinessStatus{Configured: true, Valid: true, Health: &health}
}

func (c Coordinator) ServiceStatuses(ctx context.Context) (any, error) {
	snapshot, err := c.Store.Read()
	if os.IsNotExist(err) {
		return map[string]any{"configured": false, "services": []ServiceStatus{}}, nil
	}
	if err != nil {
		return map[string]any{"configured": true, "valid": false, "error": "Configuration is invalid or inaccessible; open the configuration editor to repair it.", "services": []ServiceStatus{}}, nil
	}
	health := c.Preflight(ctx, snapshot.Config, false)
	items := []ServiceStatus{}
	for _, key := range orderedResources(snapshot.Config) {
		if len(key) < 8 || key[:8] != "service/" {
			continue
		}
		id := key[8:]
		s := snapshot.Config.Services[id]
		item := ServiceStatus{ID: id, Name: s.Name, Kind: s.Kind, Backend: backend(snapshot.Config, key), Enabled: s.Enabled, State: "unknown", Configuration: "unknown", Issues: []Issue{}}
		blocked := false
		for _, issue := range health.Issues {
			if issue.Resource == key {
				item.Issues = append(item.Issues, issue)
				if issue.Severity != "warning" && issue.Field != "autostart" {
					blocked = true
				}
			}
		}
		if a := c.Adapters[item.Backend]; a != nil && !blocked && ctx.Err() == nil {
			obs, e := a.Inspect(ctx, snapshot.Config, key)
			if e == nil {
				item.Registered = obs.Registered
				item.State = "stopped"
				if obs.Active {
					item.State = "running"
				}
				item.Configuration = "pending"
				if obs.Matches {
					item.Configuration = "registered"
				}
				if obs.Active && obs.Matches && !obs.ActiveMatches {
					item.Configuration = "restart_required"
				}
			} else {
				item.Issues = append(item.Issues, Issue{"unknown", key, "runtime", "observation_failed", "Runtime state could not be read.", "Check backend access and retry."})
			}
		}
		items = append(items, item)
	}
	return map[string]any{"configured": true, "valid": true, "revision": snapshot.Revision, "health": health, "services": items}, nil
}
func RegisterStatus(r *core.Registry, c Coordinator) error {
	return r.AddModule(core.Module{ID: "managed_status", Title: "Managed application status", Available: true},
		core.Tool{Name: "managed_config_readiness", Title: "Configuration readiness", Description: "Read cached backend prerequisites and fresh file/mount checks without inspecting running workloads or changing host state. Missing or invalid configurations remain inspectable.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return c.ConfigurationReadiness(ctx), nil
		}},
		core.Tool{Name: "managed_services", Title: "Managed applications", Description: "Read configuration health and selected runtime status without secret values or backend mutations.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return c.ServiceStatuses(ctx) }},
		core.Tool{Name: "managed_config_check", Title: "Recheck configuration readiness", Description: "Refresh native prerequisites and file/mount checks. Never installs packages or creates missing data.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			snapshot, err := c.Store.Read()
			if err != nil {
				return nil, core.Unavailable("Configuration is unavailable or invalid")
			}
			return c.Preflight(ctx, snapshot.Config, true), nil
		}})
}
