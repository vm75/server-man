package updates

import (
	"context"
	"io"

	"serverman/internal/core"
)

func (b *Backend) Register(r *core.Registry) error {
	return r.AddModule(core.Module{ID: "updates", Title: "System package updates", Available: true},
		core.Tool{Name: "dependencies_install", Title: "Install module dependencies", Description: "Install native dependencies for an optional module. Does not add repositories or change container ownership, group memberships, or daemon startup settings. Native package scripts can start services; on Arch also upgrades the system. Restart server-man after installation to refresh provider discovery.", Destructive: true, Fields: map[string]core.Field{"target": core.EnumField("Dependency group", true, "podman", "docker", "samba", "nfs", "virtualization", "lxc")}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.InstallDependencies(ctx, in.String("target"), out)
		}},
		core.Tool{Name: "packages_install", Title: "Install repository packages", Description: "Install 1–32 repository package names and their dependencies. Arch also performs a full system upgrade to avoid partial upgrades. Native signatures and locks remain enabled. May restart services; no reboot is requested.", Destructive: true, Fields: map[string]core.Field{"packages": core.StringField("Whitespace-separated repository package names", true, 4096)}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.InstallPackages(ctx, in.String("packages"), out)
		}},
		core.Tool{Name: "updates_status", Title: "Pending system updates", Description: "Read native package-manager capabilities, cached pending updates and reboot status where detectable. Unavailable data is explicit; this does not refresh repositories or install packages.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return b.Status(ctx) }},
		core.Tool{Name: "updates_refresh", Title: "Refresh update metadata", Description: "Refresh package metadata as a serialized job. Arch uses checkupdates with an isolated database; requires pacman-contrib. Does not install packages or reboot.", Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return b.Refresh(ctx, out) }},
		core.Tool{Name: "updates_install", Title: "Install system updates", Description: "Refresh native repositories and apply normal system package updates as one serialized job. May restart services and change dependencies; no distribution migration or reboot is requested. Cancellation interrupts the native process, allowing 30 seconds before forced termination; an interrupted package transaction may need native recovery.", Destructive: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return b.Install(ctx, out) }},
	)
}
