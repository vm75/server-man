package shares

import (
	"context"
	"io"

	"serverman/internal/core"
)

func (b *Backend) Register(r *core.Registry) error {
	for _, p := range []string{"samba", "nfs"} {
		available := b.programs["testparm"] != ""
		reason := "testparm was not found"
		if p == "nfs" {
			available = b.programs["exportfs"] != ""
			reason = "exportfs was not found"
		}
		if available {
			reason = ""
		}
		if err := r.AddModule(core.Module{ID: p, Title: p + " shares", Available: available, Reason: reason}); err != nil {
			return err
		}
	}
	tools := []core.Tool{{Name: "shares_list", Title: "File shares", Description: "Read managed and existing Samba/NFS share metadata, fragment revisions, configured state, Samba connection counts and registered NFS export state. Never returns full daemon configuration.", ReadOnly: true, Fields: map[string]core.Field{"protocol": core.EnumField("Optional protocol filter", false, "samba", "nfs")}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
		return b.List(ctx, in.String("protocol"))
	}}}
	return r.AddModule(core.Module{ID: "shares", Title: "File share management", Available: true}, tools...)
}
