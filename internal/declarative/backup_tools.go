package declarative

import (
	"context"
	"io"
	"time"

	"server-man/internal/core"
)

func RegisterBackup(r *core.Registry, c Coordinator) error {
	return r.AddModule(core.Module{ID: "managed_backup", Title: "Portable backup planning", Available: true},
		core.Tool{Name: "managed_backup_release", Title: "Leave backup workloads as they are", Description: "Acknowledge the reviewed interrupted capture and release its management blocker without starting/stopping workloads, publishing/deleting capture data or replaying capture. Requires the current configuration revision and recovery token; retained journal states do not claim successful resumption.", Destructive: true, Fields: map[string]core.Field{
			"revision":    core.StringField("Current configuration revision from recovery status", true, 64),
			"reviewToken": core.StringField("Current recovery review token", true, 64),
		}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			status, err := c.ReleaseBackup(in.String("revision"), in.String("reviewToken"))
			return status, managedError(err)
		}},
		core.Tool{Name: "managed_backup_recovery_status", Title: "Review backup recovery", Description: "Read the private durable record of workload stop/resume progress and staged/synced/uncertain capture publication. Does not activate workloads.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if !r.AllowWrites {
				return nil, &core.Error{Code: "writes_disabled", Status: 403, Message: "Private backup recovery is disabled with management writes."}
			}
			status, err := c.ReadBackupRecovery()
			return status, managedError(err)
		}},
		core.Tool{Name: "managed_backup_recover", Title: "Resume interrupted backup workloads", Description: "Explicitly recover only previously running workloads whose stop was attempted during the reviewed capture. Rechecks configuration revision, definitions and runtime state; never replays capture or restores data.", Destructive: true, Fields: map[string]core.Field{
			"revision":    core.StringField("Pinned configuration revision", true, 64),
			"reviewToken": core.StringField("Token from current recovery status", true, 64),
		}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			status, err := c.RecoverBackup(ctx, in.String("revision"), in.String("reviewToken"))
			return status, managedError(err)
		}},
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
		core.Tool{Name: "managed_backup_prepare", Title: "Capture backup inputs", Description: "Capture supported stopped-workload inputs into a private checksummed bundle for a reviewed revision. This initial capture is not a prepared/restorable backup: complete filesystem metadata, advanced VM state and restore validation remain unfinished; initial stopped Incus exports and basic libvirt XML/standalone-disk collection are supported. Unresolved prerequisites block capture. Recovery of previously running workloads is attempted and failures are reported.", Destructive: true, JobTimeout: 24 * time.Hour, Fields: map[string]core.Field{
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
