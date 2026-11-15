package declarative

import (
	"context"
	"errors"
	"io"
	"os"

	"server-man/internal/core"
)

// RegisterInspection adds read/validate operations independently of backend
// availability, allowing a broken ported configuration to remain inspectable.
func RegisterInspection(r *core.Registry, store Store) error {
	return r.AddModule(core.Module{ID: "managed", Title: "Managed configuration", Available: true},
		core.Tool{Name: "managed_config_read", Title: "Inspect workload configuration", Description: "Read private desired configuration for editing; disabled with management writes. Secret file contents are never read.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if !r.AllowWrites {
				return nil, &core.Error{Code: "writes_disabled", Message: "Configuration editing is disabled with management writes.", Status: 403}
			}
			data, rev, err := store.ReadRaw()
			if os.IsNotExist(err) {
				return map[string]any{"revision": "missing", "content": "version: 1\n", "valid": true}, nil
			}
			if err != nil {
				return nil, core.Unavailable("Configuration cannot be read; check its path, type and private permissions")
			}
			_, parseErr := Parse(data)
			message := ""
			if parseErr != nil {
				message = parseErr.Error()
			}
			return map[string]any{"revision": rev, "content": string(data), "valid": parseErr == nil, "error": message}, nil
		}},
		core.Tool{Name: "managed_config_validate", Title: "Validate workload configuration", Description: "Validate a proposed YAML workload configuration without saving it or activating resources.", ReadOnly: true, Fields: map[string]core.Field{"content": core.StringField("Workload YAML", true, MaxConfigBytes)}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			c, err := Parse([]byte(in.String("content")))
			if err != nil {
				return nil, core.Invalid("%s", err)
			}
			return map[string]any{"valid": true, "files": store.CheckFiles(c), "runtimeChecked": false}, nil
		}},
		core.Tool{Name: "managed_config_health", Title: "Check workload configuration", Description: "Check configuration validity and required files without changing the host. Runtime readiness checks are reported separately.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			data, _, err := store.ReadRaw()
			if os.IsNotExist(err) {
				return map[string]any{"configured": false, "runtimeChecked": false}, nil
			}
			if err != nil {
				return nil, core.Unavailable("Configuration cannot be read; check its path, type and private permissions")
			}
			c, err := Parse(data)
			if err != nil {
				return map[string]any{"configured": true, "valid": false, "error": err.Error(), "runtimeChecked": false}, nil
			}
			return map[string]any{"configured": true, "valid": true, "files": store.CheckFiles(c), "runtimeChecked": false}, nil
		}})
}

// RegisterCoordinator exposes revision-checked desired-state saves as jobs.
// Native failures are job failures even when configuration persistence succeeded.
func RegisterCoordinator(r *core.Registry, c Coordinator) error {
	fields := func() map[string]core.Field {
		return map[string]core.Field{"content": core.StringField("Workload YAML", true, MaxConfigBytes), "revision": core.StringField("Current configuration revision, or missing", true, 64)}
	}
	saveFields := fields()
	saveFields["mode"] = core.EnumField("Apply now or publish changes for next start", true, "now", "next_start")
	return r.AddModule(core.Module{ID: "managed_apply", Title: "Managed configuration apply", Available: true},
		core.Tool{Name: "managed_config_plan", Title: "Preview configuration changes", Description: "Validate and preview affected resources without publishing or starting workloads. Backend availability is returned explicitly.", ReadOnly: true, Fields: fields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			cfg, err := Parse([]byte(in.String("content")))
			if err != nil {
				return nil, core.Invalid("%s", err)
			}
			p, err := c.Preview(ctx, cfg, in.String("revision"))
			if err != nil {
				return nil, managedError(err)
			}
			unavailable := []string{}
			for _, change := range p.Changes {
				if c.Adapters[change.Backend] == nil {
					unavailable = append(unavailable, change.Resource)
				}
			}
			return map[string]any{"plan": p, "unavailable": unavailable}, nil
		}},
		core.Tool{Name: "managed_config_save", Title: "Save and apply configuration", Description: "Persist authoritative configuration and apply affected native definitions as a tracked job. Failed apply retains desired configuration and reports failure; no data is deleted implicitly.", Destructive: true, Fields: saveFields, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			cfg, err := Parse([]byte(in.String("content")))
			if err != nil {
				return nil, core.Invalid("%s", err)
			}
			j, err := c.SaveApply(ctx, cfg, in.String("revision"), in.String("mode"))
			if err != nil {
				return nil, managedError(err)
			}
			return j, nil
		}},
		core.Tool{Name: "managed_apply_status", Title: "Inspect apply progress", Description: "Read durable per-resource application status without retrying or starting resources.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			j, err := c.ReadJournal()
			if os.IsNotExist(err) {
				return map[string]any{"stage": "none"}, nil
			}
			if err != nil {
				return nil, core.Unavailable("Apply journal is unreadable or invalid; inspect private state files")
			}
			return j, nil
		}},
		core.Tool{Name: "managed_apply_retry", Title: "Retry configuration apply", Description: "Inspect incomplete application and retry non-destructive publication as a job. Uncertain interrupted removals require explicit inspection and are not replayed.", Destructive: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			j, err := c.Retry(ctx)
			if err != nil {
				return nil, managedError(err)
			}
			return j, nil
		}},
	)
}
func managedError(err error) error {
	var structured *core.Error
	if errors.As(err, &structured) {
		return structured
	}
	if errors.Is(err, ErrConflict) {
		return &core.Error{Code: "conflict", Message: ErrConflict.Error(), Status: 409}
	}
	return core.Unavailable(err.Error())
}

func RegisterLifecycle(r *core.Registry, c Coordinator) error {
	tools := []core.Tool{}
	for _, action := range []string{"start", "stop", "restart", "enable", "disable"} {
		tools = append(tools, core.Tool{Name: "managed_service_" + action, Title: "Managed service " + action, Description: "Manage the authoritative service as a tracked job. Start/stop/restart affect execution; enable/disable affect boot policy only.", Destructive: action != "start", Fields: map[string]core.Field{"service": core.StringField("Managed service ID", true, 63)}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if action == "enable" || action == "disable" {
				j, err := c.SetEnabled(ctx, in.String("service"), action == "enable")
				if err != nil {
					return nil, managedError(err)
				}
				return j, nil
			}
			if err := c.ServiceAction(ctx, in.String("service"), action); err != nil {
				return nil, managedError(err)
			}
			return map[string]string{"service": in.String("service"), "action": action}, nil
		}})
	}
	return r.AddModule(core.Module{ID: "managed_lifecycle", Title: "Managed service lifecycle", Available: true}, tools...)
}
