package updates

import (
	"context"
	"io"

	"serverman/internal/core"
)

func (b *Backend) Register(r *core.Registry) error {
	return r.AddModule(core.Module{ID: "updates", Title: "System package updates", Available: true},
		core.Tool{Name: "updates_status", Title: "Pending system updates", Description: "Read native package-manager capabilities, cached pending updates and reboot status where detectable. Unavailable data is explicit; this does not refresh repositories or install packages.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return b.Status(ctx) }},
		core.Tool{Name: "updates_refresh", Title: "Refresh update metadata", Description: "Refresh package metadata as a serialized job. Arch uses checkupdates with an isolated database; requires pacman-contrib. Does not install packages or reboot.", Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return b.Refresh(ctx, out) }},
		core.Tool{Name: "updates_install", Title: "Install system updates", Description: "Refresh native repositories and apply normal system package updates as one serialized job. May restart services and change dependencies; no distribution migration or reboot is requested. Cancellation interrupts the native process, allowing 30 seconds before forced termination; an interrupted package transaction may need native recovery.", Destructive: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) { return b.Install(ctx, out) }},
	)
}
