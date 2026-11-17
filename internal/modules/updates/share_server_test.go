package updates

import (
	"context"
	"errors"
	"io"
	"reflect"
	"slices"
	"testing"

	"server-man/internal/core"
)

func TestShareServerUsesDependencyPresetAndNativeUnits(t *testing.T) {
	for _, manager := range []string{"apt", "dnf", "pacman", "zypper"} {
		for _, protocol := range []string{"samba", "nfs"} {
			t.Run(manager+"/"+protocol, func(t *testing.T) {
				b, r := fixture(t, manager)
				units, err := b.InstallShareServer(context.Background(), protocol, io.Discard)
				if err != nil {
					t.Fatal(err)
				}
				want := ShareServerServices{Unit: "nfs-server.service"}
				if protocol == "samba" {
					want = ShareServerServices{Unit: "smb.service", DiscoveryUnit: "wsdd.service"}
					if manager == "apt" {
						want = ShareServerServices{Unit: "smbd.service", DiscoveryUnit: "wsdd-server.service"}
					}
				}
				if units != want {
					t.Fatal(units, want)
				}
				commands := r.recorded()
				args := commands[len(commands)-1].Args
				packages, err := DependencyPackages(manager, "", protocol)
				if err != nil || !reflect.DeepEqual(args[slices.Index(args, "--")+1:], packages) {
					t.Fatal(args, packages, err)
				}
				if manager == "apt" && protocol == "samba" && !slices.Contains(args, "wsdd-server") {
					t.Fatal("APT needs the WS-Discovery server package", args)
				}
			})
		}
	}
}

func TestShareServerDoesNotHideDiscoveryPackageFailure(t *testing.T) {
	b, r := fixture(t, "apt")
	want := errors.New("discovery package unavailable")
	installs := 0
	r.run = func(_ context.Context, c core.Command, _ io.Writer) error {
		if slices.Contains(c.Args, "install") {
			installs++
			return want
		}
		return nil
	}
	if _, err := b.InstallShareServer(context.Background(), "samba", io.Discard); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if installs != 1 {
		t.Fatal("unexpected fallback install", installs)
	}
}
