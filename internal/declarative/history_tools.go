package declarative

import (
	"context"
	"io"

	"serverman/internal/core"
)

func RegisterHistory(r *core.Registry, c Coordinator) error {
	private := func() error {
		if !r.AllowWrites {
			return &core.Error{Code: "writes_disabled", Status: 403, Message: "Configuration history is disabled with management writes."}
		}
		return nil
	}
	targetFields := func() map[string]core.Field {
		return map[string]core.Field{"target": core.StringField("History revision to inspect or restore", true, 64)}
	}
	planFields := targetFields()
	planFields["revision"] = core.StringField("Current configuration revision", true, 64)
	revertFields := targetFields()
	revertFields["revision"] = planFields["revision"]
	revertFields["mode"] = core.EnumField("Apply now or publish for next start", true, "now", "next_start")
	return r.AddModule(core.Module{ID: "managed_history", Title: "Private configuration recovery", Available: true},
		core.Tool{Name: "managed_config_history", Title: "Configuration history", Description: "List bounded private configuration revisions. Never reads referenced secret files. Disabled with management writes.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if err := private(); err != nil {
				return nil, err
			}
			entries, err := c.Store.History()
			if err != nil {
				return nil, managedError(err)
			}
			return map[string]any{"entries": entries}, nil
		}},
		core.Tool{Name: "managed_config_history_read", Title: "Inspect configuration revision", Description: "Read a verified private historical configuration, including malformed YAML for recovery. Never reads referenced secret files.", ReadOnly: true, Fields: targetFields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if err := private(); err != nil {
				return nil, err
			}
			raw, err := c.Store.ReadHistory(in.String("target"))
			if err != nil {
				return nil, managedError(err)
			}
			_, parseErr := Parse(raw)
			return map[string]any{"revision": in.String("target"), "content": string(raw), "valid": parseErr == nil}, nil
		}},
		core.Tool{Name: "managed_config_revert_plan", Title: "Review configuration revert", Description: "Validate a historical definition and preview affected resources. Reverting configuration does not restore data or reverse client effects.", ReadOnly: true, Fields: planFields, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if err := private(); err != nil {
				return nil, err
			}
			plan, err := c.PreviewRevert(ctx, in.String("target"), in.String("revision"))
			if err != nil {
				return nil, managedError(err)
			}
			return map[string]any{"plan": plan}, nil
		}},
		core.Tool{Name: "managed_config_revert", Title: "Revert configuration", Description: "Restore a verified historical definition as a new revision and reconcile owned resources. May interrupt running workloads. Preserves data; unresolved prior removals cannot be abandoned.", Destructive: true, Fields: revertFields, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			journal, err := c.Revert(ctx, in.String("target"), in.String("revision"), in.String("mode"))
			if err != nil {
				return nil, managedError(err)
			}
			return journal, nil
		}},
	)
}
