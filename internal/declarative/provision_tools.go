package declarative

import (
	"context"
	"io"
	"server-man/internal/core"
)

func RegisterProvisioning(r *core.Registry, p Provisioner) error {
	fields := func() map[string]core.Field {
		min, max := 0.0, 4096.0
		return map[string]core.Field{"storage": core.StringField("Existing storage reference ID", true, 63), "revision": core.StringField("Current configuration revision", true, 64), "sizeGiB": {Type: "integer", Minimum: &min, Maximum: &max, Description: "New disk capacity in GiB; omit or use zero for a directory"}}
	}
	createFields := fields()
	createFields["reviewToken"] = core.StringField("Token from the reviewed provisioning plan", true, 64)
	createFields["purpose"] = core.EnumField("Explicitly authorize new data, never replacement of missing restored data", true, "new_data")
	return r.AddModule(core.Module{ID: "managed_storage", Title: "New storage provisioning", Available: true},
		core.Tool{Name: "managed_storage_plan", Title: "Review new storage", Description: "Inspect an absent owned path and stopped consumers without creating data. Existing parents must be owned real directories. Private paths require management writes.", ReadOnly: true, Fields: fields(), Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if !r.AllowWrites {
				return nil, &core.Error{Code: "writes_disabled", Status: 403, Message: "Private storage provisioning plans are disabled with management writes."}
			}
			plan, err := p.Plan(ctx, in.String("storage"), in.String("revision"), int(in.Number("sizeGiB")))
			if err != nil {
				return nil, managedError(err)
			}
			return plan, nil
		}},
		core.Tool{Name: "managed_storage_create", Title: "Provision new storage", Description: "Create a reviewed empty directory or new sparse raw/qcow2 disk, optionally cloned from its configured image as a job. Never overwrites data, restores backups, creates parents, changes ownership or starts workloads.", Destructive: true, Fields: createFields, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			plan, err := p.Create(ctx, in.String("storage"), in.String("revision"), in.String("reviewToken"), int(in.Number("sizeGiB")))
			if err != nil {
				return nil, managedError(err)
			}
			return plan, nil
		}})
}
