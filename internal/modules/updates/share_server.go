package updates

import (
	"context"
	"io"
	"serverman/internal/core"
)

// InstallShareServer uses native distribution package names and returns the
// corresponding fixed system service. Package installation remains explicit.
func (b *Backend) InstallShareServer(ctx context.Context, protocol string, out io.Writer) (string, error) {
	var packages, unit string
	switch protocol {
	case "samba":
		packages = "samba"
		unit = "smb.service"
		if b.manager.ID == "apt" {
			unit = "smbd.service"
		}
	case "nfs":
		packages = "nfs-utils"
		unit = "nfs-server.service"
		if b.manager.ID == "apt" || b.manager.ID == "zypper" {
			packages = "nfs-kernel-server"
		}
	default:
		return "", core.Invalid("unsupported share protocol")
	}
	_, err := b.InstallPackages(ctx, packages, out)
	return unit, err
}
