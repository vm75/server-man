package updates

import (
	"context"
	"io"
	"serverman/internal/core"
)

// dependencyPackages resolves only native distribution packages. It never adds
// repositories, downloads installer scripts, or substitutes another engine.
func (b *Backend) dependencyPackages(target string) (string, error) {
	switch target {
	case "podman":
		if b.manager.ID == "apt" {
			return "podman podman-compose uidmap slirp4netns", nil
		}
		return "podman podman-compose", nil
	case "docker":
		switch b.manager.ID {
		case "apt":
			if b.distributionID == "ubuntu" {
				return "docker.io docker-compose-v2", nil
			}
			return "docker.io docker-compose", nil
		case "dnf":
			return "moby-engine docker-compose", nil
		case "pacman", "zypper":
			return "docker docker-compose", nil
		}
	case "samba":
		return "samba", nil
	case "nfs":
		if b.manager.ID == "apt" || b.manager.ID == "zypper" {
			return "nfs-kernel-server", nil
		}
		return "nfs-utils", nil
	case "virtualization":
		switch b.manager.ID {
		case "apt":
			return "qemu-system-x86 qemu-utils libvirt-daemon-system libvirt-daemon-driver-qemu libvirt-clients", nil
		case "dnf":
			return "qemu-kvm qemu-img libvirt libvirt-client", nil
		case "pacman":
			return "qemu-base libvirt", nil
		case "zypper":
			return "qemu-x86 qemu-tools libvirt libvirt-client", nil
		}
	case "lxc":
		return "lxc", nil
	}
	return "", core.Invalid("unsupported dependency target or package manager")
}
func (b *Backend) InstallDependencies(ctx context.Context, target string, out io.Writer) (any, error) {
	packages, err := b.dependencyPackages(target)
	if err != nil {
		return nil, err
	}
	return b.InstallPackages(ctx, packages, out)
}
