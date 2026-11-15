package services

import (
	"context"
	"io"

	"server-man/internal/core"
)

func (b *Backend) Register(r *core.Registry) error {
	module := core.Module{ID: "systemd", Title: "systemd services (" + b.scope + ")", Available: b.systemctl != ""}
	if !module.Available {
		module.Reason = "systemctl was not found; install systemd tools and restart server-man"
	}
	if err := r.AddModule(module); err != nil {
		return err
	}
	fields := func() map[string]core.Field {
		return map[string]core.Field{"scope": core.EnumField("Manager scope; defaults to systemdScope", false, "system", "user"), "unit": core.StringField("One exact .service unit name in the configured scope", true, 255)}
	}
	logFields := fields()
	min, max := 1.0, 2000.0
	logFields["tail"] = core.Field{Type: "integer", Minimum: &min, Maximum: &max, Description: "Most recent journal entries, default 200"}
	tools := []core.Tool{
		{Name: "services_list", Title: "Service inventory", Description: "List loaded and installed systemd services with state and enablement in the configured system/user scope; reports partial inventory and bus failures explicitly.", ReadOnly: true, Fields: map[string]core.Field{"scope": core.EnumField("Manager scope; defaults to systemdScope", false, "system", "user")}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return b.selected(in).List(ctx) }},
		{Name: "services_details", Title: "Service details", Description: "Read selected service properties without environment, command lines, credentials or unit-file contents.", ReadOnly: true, Fields: fields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.selected(in).Details(ctx, in.String("unit"))
		}},
		{Name: "services_logs", Title: "Service journal", Description: "Read bounded recent journal output for one service in the configured scope. Logs may contain private application data.", ReadOnly: true, Fields: logFields, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.selected(in).Logs(ctx, in.String("unit"), int(in.Number("tail")))
		}},
	}
	for _, action := range []string{"start", "stop", "restart", "reload", "enable", "disable"} {
		description := "Run systemctl " + action + " for one validated service as a tracked job. The configured privilege policy applies."
		if action == "enable" || action == "disable" {
			description += " Changes startup enablement only; does not start or stop the current service."
		}
		tools = append(tools, core.Tool{Name: "services_" + action, Title: "Service " + action, Description: description, Destructive: action != "start" && action != "enable", Fields: fields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.selected(in).Action(ctx, in.String("unit"), action, out)
		}})
	}
	return r.AddModule(core.Module{ID: "services", Title: "Service management", Available: true}, tools...)
}

func (b *Backend) selected(in core.Input) *Backend {
	copy := *b
	if scope := in.String("scope"); scope != "" {
		copy.scope = scope
	}
	return &copy
}
