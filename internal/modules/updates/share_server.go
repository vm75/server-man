package updates

import (
	"context"
	"io"
	"server-man/internal/core"
)

type ShareServerServices struct {
	Unit          string
	DiscoveryUnit string
}

// InstallShareServer uses native distribution package names and returns the
// corresponding fixed system services. Package installation remains explicit.
func (b *Backend) InstallShareServer(ctx context.Context, protocol string, out io.Writer) (ShareServerServices, error) {
	var units ShareServerServices
	switch protocol {
	case "samba":
		units = ShareServerServices{Unit: "smb.service", DiscoveryUnit: "wsdd.service"}
		if b.manager.ID == "apt" {
			units = ShareServerServices{Unit: "smbd.service", DiscoveryUnit: "wsdd-server.service"}
		}
	case "nfs":
		units.Unit = "nfs-server.service"
	default:
		return units, core.Invalid("unsupported share protocol")
	}
	_, err := b.InstallDependencies(ctx, protocol, out)
	return units, err
}
