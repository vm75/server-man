package shares

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"server-man/internal/core"
	"server-man/internal/modules/services"
	"strings"
	"testing"
)

func fixtureServices(t *testing.T, r core.Executor) *services.Backend {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte("fixture, never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return services.New("system", r)
}

func TestDiscoverySetupReportsEnableAndStartFailures(t *testing.T) {
	for _, fail := range []string{"", "enable", "start"} {
		t.Run(fail, func(t *testing.T) {
			_, r, _ := fixture(t)
			service := fixtureServices(t, r)
			r.beforeRun = func(_ context.Context, c core.Command) error {
				if c.Args[3] == fail {
					return errors.New("fixture service failure")
				}
				return nil
			}
			err := startDiscovery(context.Background(), service, "wsdd-server.service", io.Discard)
			if (err != nil) != (fail != "") {
				t.Fatal(fail, err)
			}
			if err != nil && !strings.Contains(err.Error(), "WS-Discovery setup (wsdd-server.service)") {
				t.Fatal(err)
			}
			calls := r.recorded()
			want := 2
			if fail == "enable" {
				want = 1
			}
			if len(calls) != want || calls[0].Args[3] != "enable" || want == 2 && calls[1].Args[3] != "start" {
				t.Fatal(calls)
			}
		})
	}
}

func TestDiscoveryAvailabilityRequiresServerUnit(t *testing.T) {
	for _, installed := range []string{"", "wsdd-server.service", "wsdd.service"} {
		t.Run(installed, func(t *testing.T) {
			b, r, _ := fixture(t)
			b.programs["wsdd"] = "wsdd" // A client-only installation is insufficient.
			service := fixtureServices(t, r)
			r.captureOverride = func(c core.Command) (string, error) {
				unit := c.Args[len(c.Args)-1]
				if unit == installed {
					return "Id=" + unit + "\nLoadState=loaded\nActiveState=inactive\n", nil
				}
				return "LoadState=not-found\n", nil
			}
			if got := discoveryAvailable(context.Background(), service); got != (installed != "") {
				t.Fatal(installed, got)
			}
		})
	}
}

func TestPreparePreservesConfigurationAndIsIdempotent(t *testing.T) {
	b, _, _ := fixture(t)
	original := []byte("# administrator settings\n[global]\n workgroup = OFFICE\n[existing]\n path = /srv/existing\n")
	if err := os.WriteFile(b.sambaConfig, original, 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(b.sambaConfig, 0640); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := b.prepare(context.Background(), "samba"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(b.sambaConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), string(original)) || strings.Count(string(got), "include = "+b.sambaManaged) != 1 {
		t.Fatal(string(got))
	}
	info, _ := os.Stat(b.sambaConfig)
	if info.Mode().Perm() != 0640 {
		t.Fatal(info.Mode())
	}
}
func TestPrepareValidationFailurePreservesMain(t *testing.T) {
	b, r, _ := fixture(t)
	original := []byte("[global]\n workgroup = OFFICE\n")
	if err := os.WriteFile(b.sambaConfig, original, 0644); err != nil {
		t.Fatal(err)
	}
	r.captureOverride = func(core.Command) (string, error) { return "", errors.New("invalid configuration") }
	if err := b.prepare(context.Background(), "samba"); err == nil {
		t.Fatal("expected validation failure")
	}
	got, _ := os.ReadFile(b.sambaConfig)
	if string(got) != string(original) {
		t.Fatal("main file changed")
	}
}
func TestPrepareNFSPreservesNativeExports(t *testing.T) {
	b, _, _ := fixture(t)
	if err := os.WriteFile(b.nfsExports, []byte("/srv/existing 192.0.2.1(ro)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := b.prepare(context.Background(), "nfs"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(b.nfsExports)
	if string(got) != "/srv/existing 192.0.2.1(ro)\n" {
		t.Fatal("native exports changed")
	}
}

func TestPrepareRefusesToActivateUnownedFragment(t *testing.T) {
	b, _, _ := fixture(t)
	original := []byte("[global]\n workgroup = OFFICE\n")
	if err := os.WriteFile(b.sambaConfig, original, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.sambaManaged, []byte("[unmanaged]\n path = /private\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := b.prepare(context.Background(), "samba"); err == nil {
		t.Fatal("activated an unowned fragment")
	}
	got, _ := os.ReadFile(b.sambaConfig)
	if string(got) != string(original) {
		t.Fatal("main config changed")
	}
}
