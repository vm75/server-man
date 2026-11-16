package updates

import (
	"context"
	"io"
	"server-man/internal/core"
	"strings"
)

// dependencyPackages resolves only native distribution packages. It never adds
// repositories, downloads installer scripts, or substitutes another engine.
func (b *Backend) dependencyPackages(target string) (string, error) {
	packages, err := DependencyPackages(b.manager.ID, b.distributionID, target)
	return strings.Join(packages, " "), err
}

// DependencyPackages is shared by interactive management and host installation.
// It returns only native repository package names.
func DependencyPackages(manager, distribution, target string) ([]string, error) {
	packages, err := dependencyPackageNames(manager, distribution, target)
	return strings.Fields(packages), err
}

func dependencyPackageNames(manager, distribution, target string) (string, error) {
	switch target {
	case "podman":
		if manager == "apt" {
			return "podman podman-compose uidmap slirp4netns passt netavark aardvark-dns", nil
		}
		return "podman podman-compose passt netavark aardvark-dns", nil
	case "docker":
		switch manager {
		case "apt":
			if distribution == "ubuntu" {
				return "docker.io docker-compose-v2", nil
			}
			// Debian splits the client from the daemon and only recommends it.
			// Our no-recommends installation must request both explicitly.
			return "docker.io docker-cli docker-compose", nil
		case "dnf":
			return "moby-engine docker-compose", nil
		case "pacman", "zypper":
			return "docker docker-compose", nil
		}
	case "incus":
		if manager == "apt" {
			return "incus qemu-utils ovmf qemu-system-modules-spice dnsmasq-base", nil
		}
		if manager == "pacman" {
			return "incus cdrtools", nil
		}
	case "samba":
		return "samba", nil
	case "nfs":
		if manager == "apt" || manager == "zypper" {
			return "nfs-kernel-server", nil
		}
		return "nfs-utils", nil
	case "virtualization":
		switch manager {
		case "apt":
			return "qemu-system-x86 qemu-utils libvirt-daemon-system libvirt-daemon-driver-qemu libvirt-clients", nil
		case "dnf":
			return "qemu-kvm qemu-img libvirt libvirt-client", nil
		case "pacman":
			return "qemu-base libvirt", nil
		case "zypper":
			return "qemu-x86 qemu-tools libvirt libvirt-client", nil
		}
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
