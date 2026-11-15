package declarative

import (
	"context"
	"io"
	"server-man/internal/core"
)

func RegisterBackup(r *core.Registry, c Coordinator) error {
	return r.AddModule(core.Module{ID: "managed_backup", Title: "Portable backup planning", Available: true}, core.Tool{Name: "managed_backup_inventory", Title: "Review backup inputs", Description: "Inventory private configuration, data, image/private-file references and native/external requirements. Metadata only: never reads secret contents, copies data, exports guests or produces a completed backup.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
		if !r.AllowWrites {
			return nil, &core.Error{Code: "writes_disabled", Status: 403, Message: "Private backup inventory is disabled with management writes."}
		}
		inventory, err := c.BackupInventory(ctx)
		if err != nil {
			return nil, managedError(err)
		}
		return inventory, nil
	}})
}
