package updates

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestDependencyTargetsUseValidatedNativePackages(t *testing.T) {
	for _, id := range []string{"apt", "dnf", "pacman", "zypper"} {
		for _, target := range []string{"podman", "docker", "samba", "nfs", "virtualization"} {
			b, r := fixture(t, id)
			if _, err := b.InstallDependencies(context.Background(), target, io.Discard); err != nil {
				t.Fatal(id, target, err)
			}
			commands := r.recorded()
			args := commands[len(commands)-1].Args
			marker := -1
			for i, arg := range args {
				if arg == "--" {
					marker = i
				}
			}
			if marker < 0 || marker == len(args)-1 {
				t.Fatal(args)
			}
			for _, name := range args[marker+1:] {
				if !installPackageName.MatchString(name) {
					t.Fatal(name)
				}
			}
		}
	}
	b, r := fixture(t, "apt")
	if _, err := b.InstallDependencies(context.Background(), "../other", io.Discard); err == nil || len(r.recorded()) != 0 {
		t.Fatal("invalid dependency target ran commands")
	}
	b.distributionID = "ubuntu"
	names, err := b.dependencyPackages("docker")
	if err != nil || !strings.Contains(names, "docker-compose-v2") {
		t.Fatal(names, err)
	}
}

func TestPodmanInstallIncludesRootlessNetworkHelper(t *testing.T) {
	for _, host := range []struct{ manager, distribution string }{{"apt", "ubuntu"}, {"apt", "debian"}, {"pacman", "arch"}} {
		packages, err := DependencyPackages(host.manager, host.distribution, "podman")
		if err != nil || !slices.Contains(packages, "passt") || !slices.Contains(packages, "netavark") || !slices.Contains(packages, "aardvark-dns") {
			t.Fatal(host, packages, err)
		}
		if host.manager == "apt" && !slices.Contains(packages, "dbus-user-session") {
			t.Fatal("rootless systemd user bus dependency omitted", host, packages)
		}
	}
}

func TestDebianDockerInstallIncludesClientWithoutRecommendations(t *testing.T) {
	b, r := fixture(t, "apt")
	b.distributionID = "debian"
	if _, err := b.InstallDependencies(context.Background(), "docker", io.Discard); err != nil {
		t.Fatal(err)
	}
	commands := r.recorded()
	args := commands[len(commands)-1].Args
	if !slices.Contains(args, "--no-install-recommends") {
		t.Fatal("dependency installation policy changed", args)
	}
	for _, name := range []string{"docker.io", "docker-cli", "docker-compose"} {
		if !slices.Contains(args, name) {
			t.Fatal("Docker runtime/provider is incomplete", name, args)
		}
	}
}

func TestIncusInstallIncludesVMDependenciesWithoutRecommendations(t *testing.T) {
	for _, distribution := range []string{"debian", "ubuntu"} {
		packages, err := DependencyPackages("apt", distribution, "incus")
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"incus", "qemu-utils", "ovmf", "qemu-system-modules-spice", "dnsmasq-base"} {
			if !slices.Contains(packages, name) {
				t.Fatal(distribution, "missing VM prerequisite", name, packages)
			}
		}
	}
	packages, err := DependencyPackages("pacman", "arch", "incus")
	if err != nil || !slices.Contains(packages, "cdrtools") {
		t.Fatal(packages, err)
	}
}

func TestDependencyPresetsUseUnpinnedNativeRepositoryCandidates(t *testing.T) {
	for _, host := range []struct {
		manager, distribution string
		targets               []string
	}{
		{"apt", "ubuntu", []string{"podman", "docker", "incus", "samba", "nfs", "virtualization"}},
		{"apt", "debian", []string{"podman", "docker", "incus", "samba", "nfs", "virtualization"}},
		{"pacman", "arch", []string{"podman", "docker", "incus", "samba", "nfs", "virtualization"}},
		{"dnf", "fedora", []string{"podman", "docker", "samba", "nfs", "virtualization"}},
		{"zypper", "opensuse-tumbleweed", []string{"podman", "docker", "samba", "nfs", "virtualization"}},
	} {
		for _, target := range host.targets {
			names, err := DependencyPackages(host.manager, host.distribution, target)
			if err != nil || len(names) == 0 {
				t.Fatal(host, target, names, err)
			}
			for _, name := range names {
				if !installPackageName.MatchString(name) {
					t.Fatal("preset pins a version or selects a repository", host, target, name)
				}
			}
		}
	}
}
