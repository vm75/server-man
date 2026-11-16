package installation

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBackendPlanAndDistributionPackages(t *testing.T) {
	targets, err := selected("all")
	if err != nil {
		t.Fatal(err)
	}
	for _, distro := range []string{"ubuntu", "debian", "arch"} {
		manager, packages, err := packagePlan(distro, targets)
		if err != nil {
			t.Fatal(err)
		}
		if manager == "" || !slices.Contains(packages, "incus") || !slices.Contains(packages, "sudo") {
			t.Fatal(manager, packages)
		}
		seen := map[string]bool{}
		for _, p := range packages {
			if seen[p] || strings.ContainsAny(p, " /;=@<>:") {
				t.Fatal(packages)
			}
			seen[p] = true
		}
		want := "docker-compose"
		if distro == "ubuntu" {
			want = "docker-compose-v2"
		}
		if !slices.Contains(packages, want) {
			t.Fatal(packages)
		}
		if distro == "debian" && !slices.Contains(packages, "docker-cli") {
			t.Fatal("Debian no-recommends plan omits Docker client", packages)
		}
	}
	if _, _, err := packagePlan("unsupported", targets); err == nil {
		t.Fatal("unsupported distribution accepted")
	}
	for _, bad := range []string{"", "docker,docker", "docker;id", "../incus", "all,docker"} {
		if _, err := selected(bad); err == nil {
			t.Fatal(bad)
		}
	}
}

func TestCLIRejectsUnsafeAccountsBeforeMutation(t *testing.T) {
	for _, name := range []string{"root", "--root", "a\nUser=root", "a;id", "../root", ""} {
		if err := Run(context.Background(), []string{"--user", name, "--yes"}, strings.NewReader(""), io.Discard); err == nil {
			t.Fatal(name)
		}
	}
}

func TestAtomicInstallReplacesLeafSymlinkWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	target := filepath.Join(dir, "binary")
	if err := os.WriteFile(outside, []byte("preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	if err := atomicFile(target, []byte("new binary"), 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "preserved" {
		t.Fatal("followed target symlink")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 {
		t.Fatal(info, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatal("temporary files retained", entries)
	}
}

func TestAccountUnitUsesFreshSystemGroupsAndUserBus(t *testing.T) {
	p := plan{Account: "example", Home: "/home/example", UID: "1234", GID: "1234"}
	unit := p.unit()
	for _, part := range []string{"User=example", "Group=1234", "Requires=user@1234.service", "XDG_RUNTIME_DIR=/run/user/1234", "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1234/bus", "-config /home/example/.config/server-man/server-man.yaml"} {
		if !strings.Contains(unit, part) {
			t.Fatal(part, unit)
		}
	}
	if strings.Contains(unit, "NoNewPrivileges") {
		t.Fatal("helper elevation blocked")
	}
}

func TestRootDirectoryRejectsSymlink(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := rootDirectory(link); err == nil {
		t.Fatal("accepted symlink installation parent")
	}
}

func TestInitialConfigurationPrivateAndPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server-man.yaml")
	if err := writeInitialConfiguration(path, "/home/example"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	original := []byte("version: 1\nserver:\n  listen: 127.0.0.1:8181\n  allow_writes: false\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeInitialConfiguration(path, "/home/example"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(original) {
		t.Fatal("existing config overwritten")
	}
	if err := os.WriteFile(path, []byte("version: 1\nserver: {unknown: true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeInitialConfiguration(path, "/home/example"); err == nil {
		t.Fatal("invalid existing config silently accepted")
	}
}

func TestSelectedHostUnitsIncludeDockerSocketWithoutShareExposure(t *testing.T) {
	units := hostUnits([]string{"podman", "docker", "virtualization", "incus", "samba", "nfs"})
	if !slices.Equal(units, []string{"docker.socket", "docker.service", "libvirtd.socket", "incus.socket"}) {
		t.Fatal(units)
	}
	if got := hostUnits([]string{"podman", "samba", "nfs"}); len(got) != 0 {
		t.Fatal("rootless Podman/share selection should not activate system daemons", got)
	}
}
