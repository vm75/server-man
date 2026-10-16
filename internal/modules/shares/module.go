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
	base := func() map[string]core.Field {
		return map[string]core.Field{"name": core.StringField("Managed share name, portable identifier up to 64 characters", true, 64), "revision": core.StringField("Current managed-fragment revision from shares_list; refresh after every change", true, 64)}
	}
	tools := []core.Tool{{Name: "shares_list", Title: "File shares", Description: "Read managed and existing Samba/NFS share metadata, fragment revisions, configured state, Samba connection counts and registered NFS export state. Never returns full daemon configuration.", ReadOnly: true, Fields: map[string]core.Field{"protocol": core.EnumField("Optional protocol filter", false, "samba", "nfs")}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
		return b.List(ctx, in.String("protocol"))
	}}}
	for _, p := range []string{"samba", "nfs"} {
		fields := base()
		fields["path"] = core.StringField("Existing absolute directory; resolved symlinks are stored as the canonical path", true, 4096)
		fields["readOnly"] = core.Field{Type: "boolean", Required: true}
		if p == "samba" {
			fields["guest"] = core.Field{Type: "boolean", Required: true}
			fields["browseable"] = core.Field{Type: "boolean", Required: true}
			fields["users"] = core.StringField("Comma/space separated local users or @groups, required unless guest is true", false, 4096)
		} else {
			fields["clients"] = core.StringField("Comma/space separated IP addresses, CIDR networks or explicit *; limit 16", true, 2048)
			fields["rootSquash"] = core.Field{Type: "boolean", Required: true}
			fields["security"] = core.EnumField("NFS security flavor; Kerberos requires existing host setup", true, "sys", "krb5", "krb5i", "krb5p")
		}
		tools = append(tools, core.Tool{Name: p + "_share_save", Title: "Save " + p + " share", Description: "Create or replace an application-owned share and apply it as one job. Requires the current fragment revision; validates before publication and attempts file/runtime rollback on apply failure. May change network access or interrupt existing clients. Does not alter shared directory data, users, ACLs or unmanaged configuration.", Destructive: true, Fields: fields, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			d := Definition{Name: in.String("name"), Path: in.String("path"), ReadOnly: in.Bool("readOnly")}
			if p == "samba" {
				d.Guest = in.Bool("guest")
				d.Browseable = in.Bool("browseable")
				d.Users = tokens(in.String("users"))
			} else {
				d.Clients = tokens(in.String("clients"))
				d.RootSquash = in.Bool("rootSquash")
				d.Security = in.String("security")
			}
			return b.Change(ctx, p, in.String("revision"), in.String("name"), &d, out)
		}})
		tools = append(tools, core.Tool{Name: p + "_share_remove", Title: "Remove " + p + " share", Description: "Remove only an application-owned share and apply the change, preserving directory data. Requires the current fragment revision; may interrupt clients. Attempts file/runtime rollback on failure.", Destructive: true, Fields: base(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			return b.Change(ctx, p, in.String("revision"), in.String("name"), nil, out)
		}})
	}
	return r.AddModule(core.Module{ID: "shares", Title: "File share management", Available: true}, tools...)
}
