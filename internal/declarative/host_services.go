package declarative

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"server-man/internal/core"
)

func managedUnitOwner(cfg Config, unit string) string {
	for id, s := range cfg.Services {
		if s.Kind != "container" {
			continue
		}
		if s.Backend == "quadlet" && s.Engine == "podman" && slices.Contains(quadletUnits(cfg, id), unit) {
			return id
		}
		if s.Backend == "compose" && composeUnit(id) == unit {
			return id
		}
	}
	return ""
}

// RouteManagedUnits keeps the host service screen direct for ordinary units,
// while application-owned user units retain authoritative lifecycle semantics.
// Assembly installs this before the privileged routing wrapper.
func RouteManagedUnits(r *core.Registry, c Coordinator, defaultScope string) {
	if defaultScope == "" {
		defaultScope = "system"
	}
	r.Transform(func(tool core.Tool) func(context.Context, core.Input, io.Writer) (any, error) {
		if !strings.HasPrefix(tool.Name, "services_") || tool.Name == "services_list" || tool.Name == "services_logs" {
			return tool.Run
		}
		original := tool.Run
		return func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			scope := in.String("scope")
			if scope == "" {
				scope = defaultScope
			}
			if scope != "user" {
				return original(ctx, in, out)
			}
			snapshot, err := c.Store.Read()
			if err != nil {
				if !tool.ReadOnly && strings.HasPrefix(in.String("unit"), "sm-") {
					return nil, core.Unavailable("Repair the managed configuration before controlling reserved application units")
				}
				return original(ctx, in, out)
			}
			owner := managedUnitOwner(snapshot.Config, in.String("unit"))
			if owner == "" {
				// The sm- namespace is reserved for server-man-generated user
				// services, even when the definition is missing or stale. Never
				// let direct systemctl writes bypass managed ownership checks.
				if !tool.ReadOnly && strings.HasPrefix(in.String("unit"), "sm-") {
					return nil, core.Unavailable("Unknown or stale managed unit; repair the application definition instead of controlling a reserved unit directly")
				}
				return original(ctx, in, out)
			}
			if tool.ReadOnly {
				result, err := original(ctx, in, out)
				if err != nil {
					return nil, err
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					return nil, err
				}
				fields := map[string]any{}
				if err = json.Unmarshal(encoded, &fields); err != nil {
					return nil, err
				}
				fields["managedApplication"] = owner
				return fields, nil
			}
			action := strings.TrimPrefix(tool.Name, "services_")
			if action == "enable" || action == "disable" {
				j, err := c.SetEnabled(ctx, owner, action == "enable")
				if err != nil {
					return nil, managedError(err)
				}
				return j, nil
			}
			if action == "reload" {
				return nil, core.Invalid("Apply the application definition instead of directly reloading its generated unit")
			}
			if err := c.ServiceAction(ctx, owner, action); err != nil {
				return nil, managedError(err)
			}
			return map[string]string{"service": owner, "action": action}, nil
		}
	})
}
