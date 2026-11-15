package updates

import (
	"context"
	"io"
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
