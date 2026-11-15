package declarative

import (
	"context"
	"io"
	"server-man/internal/core"
)

func RegisterBackup(r *core.Registry, c Coordinator) error {
	return r.AddModule(core.Module{ID: "managed_backup", Title: "Portable backup planning", Available: true},
		core.Tool{Name: "managed_backup_inventory", Title: "Review backup inputs", Description: "Inventory private configuration, data, image/private-file references and native/external requirements. Metadata only: never reads referenced private files, exposes native configuration contents, copies data, exports guests or produces a completed backup.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if !r.AllowWrites {
				return nil, &core.Error{Code: "writes_disabled", Status: 403, Message: "Private backup inventory is disabled with management writes."}
			}
			inventory, err := c.BackupInventory(ctx)
			if err != nil {
				return nil, managedError(err)
			}
			return inventory, nil
		}},
		core.Tool{Name: "managed_backup_prepare", Title: "Prepare portable backup", Description: "Prepare a portable backup bundle containing authoritative configuration, declared storage data, referenced secret/asset files and native VM exports as a tracked job. Workloads can be gracefully quiesced during backup and resumed afterward.", Destructive: true, Fields: map[string]core.Field{
			"destination":     core.StringField("Absolute path to backup destination directory", true, 4096),
			"quiesce":         {Type: "boolean", Description: "Gracefully stop running workloads during backup and resume afterward"},
			"deadlineSeconds": {Type: "integer", Description: "Backup timeout in seconds (30-86400, default 900)"},
		}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if !r.AllowWrites {
				return nil, &core.Error{Code: "writes_disabled", Status: 403, Message: "Backup preparation is disabled with management writes."}
			}
			dest := in.String("destination")
			quiesce := true
			if q, ok := in["quiesce"].(bool); ok {
				quiesce = q
			}
			deadline := int(in.Number("deadlineSeconds"))
			summary, err := c.PrepareBackup(ctx, BackupPrepareOptions{
				Destination:     dest,
				Quiesce:         quiesce,
				DeadlineSeconds: deadline,
			}, out)
			if err != nil {
				return nil, managedError(err)
			}
			return summary, nil
		}},
	)
}
