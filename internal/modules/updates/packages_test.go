package updates

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestInstallPackagesRejectsInputBeforeCommands(t *testing.T) {
	for _, names := range []string{"", "--config=/tmp/x", "/tmp/a.deb", "https://example.com/x", "repo/pkg", "name=version", "good good", "a;touch", strings.Repeat("a ", 33)} {
		b, r := fixture(t, "apt")
		if _, err := b.InstallPackages(context.Background(), names, io.Discard); err == nil {
			t.Errorf("accepted %q", names)
		}
		if len(r.recorded()) != 0 {
			t.Fatalf("invalid names ran commands: %q", names)
		}
	}
}
func TestInstallPackagesNativeArguments(t *testing.T) {
	for _, id := range []string{"apt", "dnf", "pacman", "zypper"} {
		t.Run(id, func(t *testing.T) {
			b, r := fixture(t, id)
			if _, err := b.InstallPackages(context.Background(), "samba nfs-utils", io.Discard); err != nil {
				t.Fatal(err)
			}
			commands := r.recorded()
			last := commands[len(commands)-1]
			if !reflect.DeepEqual(last.Args[len(last.Args)-3:], []string{"--", "samba", "nfs-utils"}) {
				t.Fatal(last.Args)
			}
			if id == "pacman" && !strings.Contains(strings.Join(last.Args, " "), "--sysupgrade") {
				t.Fatal("partial Arch upgrade")
			}
			for _, arg := range last.Args {
				if strings.Contains(arg, "allow-unauthenticated") || strings.Contains(arg, "nogpgcheck") {
					t.Fatal("signature bypass")
				}
			}
		})
	}
}
