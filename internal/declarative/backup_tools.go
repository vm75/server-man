package declarative

import (
	"context"
	"io"
	"time"

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
		core.Tool{Name: "managed_backup_prepare", Title: "Capture backup inputs", Description: "Capture supported stopped-workload inputs into a private checksummed bundle for a reviewed revision. This initial capture is not a prepared/restorable backup: complete filesystem metadata, native exports and restore validation remain unfinished. Unresolved prerequisites block capture. Recovery of previously running workloads is attempted and failures are reported.", Destructive: true, JobTimeout: 24 * time.Hour, Fields: map[string]core.Field{
			"revision":        core.StringField("Reviewed configuration revision", true, 64),
			"destination":     core.StringField("Absolute path to backup destination directory", true, 4096),
			"quiesce":         {Type: "boolean", Description: "Gracefully stop running workloads during backup and resume afterward"},
			"deadlineSeconds": {Type: "integer", Minimum: backupDeadlineMinimum(), Maximum: backupDeadlineMaximum(), Description: "Backup timeout in seconds (30-86400, default 900)"},
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
				Revision:        in.String("revision"),
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

func backupDeadlineMinimum() *float64 { value := 30.0; return &value }
func backupDeadlineMaximum() *float64 { value := 86400.0; return &value }
