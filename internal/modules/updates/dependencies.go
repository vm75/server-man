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
	if err != nil {
		return nil, err
	}
	// Keep installation rolling with the native repository candidate, never
	// a version constraint or an alternate repository selector. This is shared
	// by guided installation and the Software dependency workflow.
	names := strings.Fields(packages)
	for _, name := range names {
		if !installPackageName.MatchString(name) {
			return nil, core.Invalid("invalid unpinned native dependency package")
		}
	}
	return names, nil
}

// UserSessionPackages supplies the native session-bus component split from
// base D-Bus on APT systems. Other managers retain their existing package
// policy; readiness verifies the actual socket independently of package names.
func UserSessionPackages(manager string) []string {
	if manager == "apt" {
		return []string{"dbus-user-session"}
	}
	return nil
}

func dependencyPackageNames(manager, distribution, target string) (string, error) {
	switch target {
	case "podman":
		if manager == "apt" {
			return "podman podman-compose uidmap slirp4netns passt netavark aardvark-dns nftables " + strings.Join(UserSessionPackages(manager), " "), nil
		}
		return "podman podman-compose passt netavark aardvark-dns nftables", nil
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
		if manager == "apt" {
			return "samba wsdd-server", nil
		}
		return "samba wsdd", nil
	case "nfs":
		if manager == "apt" || manager == "zypper" {
			return "nfs-kernel-server", nil
		}
		return "nfs-utils", nil
	case "firecracker":
		// Ubuntu/Debian do not currently ship a consistently available
		// upstream Firecracker+jailer pair. Install host prerequisites, then
		// report both binary paths in readiness until an operator verifies
		// official matching native binaries.
		if manager == "pacman" { return "firecracker iproute2", nil }
		if manager == "apt" { return "iproute2", nil }
		return "", core.Invalid("Firecracker dependency installation is unsupported on this package manager")
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
