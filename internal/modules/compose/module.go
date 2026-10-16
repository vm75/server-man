package compose

import (
	"context"
	"io"

	"serverman/internal/core"
)

func (b *Backend) Register(registry *core.Registry) error {
	for _, name := range []string{"podman", "docker"} {
		module := core.Module{ID: name, Title: name + " engine", Available: b.programs[name] != ""}
		if !module.Available {
			module.Reason = name + " executable was not found; install it and restart Serverman"
		}
		if err := registry.AddModule(module); err != nil {
			return err
		}
	}
	identity := func() map[string]core.Field {
		return map[string]core.Field{"engine": core.EnumField("Container engine", true, "podman", "docker"), "project": core.StringField("Project directory name under the configured engine root", true, 128)}
	}
	fields := identity()
	minimum, maximum := 1.0, 2000.0
	fields["service"] = core.StringField("Optional service name for logs", false, 128)
	fields["tail"] = core.Field{Type: "integer", Minimum: &minimum, Maximum: &maximum, Description: "Lines per container, default 200"}
	tools := []core.Tool{
		{Name: "compose_projects", Title: "Compose projects", Description: "Discover Podman/Docker Compose projects and engine state. Returns selected container metadata only; no Compose or environment file contents.", ReadOnly: true, Fields: map[string]core.Field{"engine": core.EnumField("Optional engine filter", false, "podman", "docker")}, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			return b.Inventory(ctx, input.String("engine"))
		}},
		{Name: "compose_status", Title: "Compose project details", Description: "Read project containers and associated Podman pods through the configured Compose provider, preserving project/environment context.", ReadOnly: true, Fields: identity(), Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			return b.Status(ctx, input.String("engine"), input.String("project"))
		}},
		{Name: "compose_logs", Title: "Compose logs", Description: "Read bounded logs, optionally for one service. Logs may contain private application data; only request relevant output.", ReadOnly: true, Fields: fields, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			return b.Logs(ctx, input.String("engine"), input.String("project"), input.String("service"), int(input.Number("tail")))
		}},
	}
	for _, action := range []string{"up", "start", "stop", "restart", "pull", "down"} {
		destructive := action == "up" || action == "stop" || action == "restart" || action == "down"
		description := "Run Compose " + action + " as a tracked job. Disabled markers block up/start/restart/pull."
		if action == "down" {
			description += " Removes project containers/networks; never requests volume removal."
		}
		if action == "up" {
			description += " Creates/reconciles the project in detached mode and may recreate changed containers."
		}
		tools = append(tools, core.Tool{Name: "compose_" + action, Title: "Compose " + action, Description: description, Destructive: destructive, Fields: identity(), Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			return b.Action(ctx, input.String("engine"), input.String("project"), action, out)
		}})
	}
	return registry.AddModule(core.Module{ID: "compose", Title: "Compose projects", Available: true}, tools...)
}
