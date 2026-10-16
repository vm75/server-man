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
			module.Reason = name + " executable was not found; install it and restart server-man"
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
		{Name: "compose_project_create", Title: "Create Compose project", Description: "Create a new project with compose.yaml under the selected engine root and an isolated data/<project> directory. Compose is validated against the per-project isolation policy before publication. File contents are private administrative configuration.", Fields: map[string]core.Field{"engine": core.EnumField("Container engine", true, "podman", "docker"), "project": core.StringField("New project directory name", true, 128), "content": core.StringField("Compose YAML content (maximum 256 KiB)", true, 256<<10)}, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			return b.CreateProject(ctx, input.String("engine"), input.String("project"), input.String("content"), out)
		}},
		{Name: "compose_file_read", Title: "Read Compose file", Description: "Read the selected project's raw Compose YAML for the authenticated editor. The result may contain private values; use only on a trusted connection.", ReadOnly: true, Fields: identity(), Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			if !b.allowFileRead {
				return nil, &core.Error{Code: "writes_disabled", Message: "File editing is disabled with management writes.", Status: 403}
			}
			return b.ComposeFile(input.String("engine"), input.String("project"))
		}},
		{Name: "compose_file_save", Title: "Save Compose file", Description: "Replace the selected Compose file after validation and stale-edit checking. Bind mounts must be inside data/<project>; privileged/device access and host namespaces are rejected. File contents are not included in job output.", Destructive: true, Fields: map[string]core.Field{"engine": core.EnumField("Container engine", true, "podman", "docker"), "project": core.StringField("Project directory name", true, 128), "revision": core.StringField("Current file revision from compose_file_read", true, 64), "content": core.StringField("Compose YAML content (maximum 256 KiB)", true, 256<<10)}, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			return b.SaveCompose(ctx, input.String("engine"), input.String("project"), input.String("revision"), input.String("content"), out)
		}},
		{Name: "compose_env_read", Title: "Read project environment file", Description: "Read one project's private .env file for the authenticated editor. Secret values are returned only to the caller; use only on a trusted connection.", ReadOnly: true, Fields: map[string]core.Field{"engine": core.EnumField("Container engine", true, "podman", "docker"), "project": core.StringField("Project directory name", true, 128), "filename": core.StringField("Project environment filename ending in .env", true, 128)}, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			if !b.allowFileRead {
				return nil, &core.Error{Code: "writes_disabled", Message: "File editing is disabled with management writes.", Status: 403}
			}
			return b.EnvFile(input.String("engine"), input.String("project"), input.String("filename"))
		}},
		{Name: "compose_env_save", Title: "Save project environment file", Description: "Create or replace one project .env file with stale-edit checking and Compose validation. Secret contents are excluded from job output.", Destructive: true, Fields: map[string]core.Field{"engine": core.EnumField("Container engine", true, "podman", "docker"), "project": core.StringField("Project directory name", true, 128), "filename": core.StringField("Project environment filename ending in .env", true, 128), "revision": core.StringField("Current revision, or missing for a new file", true, 64), "content": core.StringField("Environment file content (maximum 64 KiB)", true, 64<<10)}, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			return b.SaveEnv(ctx, input.String("engine"), input.String("project"), input.String("filename"), input.String("revision"), input.String("content"))
		}},
		{Name: "compose_data_mkdir", Title: "Create project data directory", Description: "Create a private directory or nested directory under data/<project>, rejecting traversal and symlinks. Bind mounts may use paths within this project data root.", Fields: map[string]core.Field{"engine": core.EnumField("Container engine", true, "podman", "docker"), "project": core.StringField("Project directory name", true, 128), "path": core.StringField("Relative folder path under data/<project>", true, 1024)}, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			return b.CreateDataDirectory(input.String("engine"), input.String("project"), input.String("path"))
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
