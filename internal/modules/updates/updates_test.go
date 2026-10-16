package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"serverman/internal/config"
	"serverman/internal/core"
)

const aptData = "Reading package lists... Done\nThe following packages have been kept back:\n  held-package\nInst demo [1.0] (2.0 Ubuntu:stable [amd64])\nInst dependency (1:3.0-1 Ubuntu:stable [amd64])\nConf demo (2.0 Ubuntu:stable [amd64])\n1 upgraded, 1 newly installed, 0 to remove and 1 not upgraded.\n"
const zypperData = `<?xml version="1.0"?><stream><update-status version="0.6"><update-list><update kind="package" name="demo" edition="2.0-1" edition-old="1.0-1" arch="x86_64"><summary>Update</summary><source url="https://PRIVATE:SECRET@example.invalid/repo" alias="main"/></update></update-list></update-status></stream>`

type statusError int

func (e statusError) Error() string { return fmt.Sprintf("fixture exit %d", e) }
func (e statusError) ExitCode() int { return int(e) }

type recorder struct {
	mu       sync.Mutex
	commands []core.Command
	capture  func(core.Command) (string, error)
	run      func(context.Context, core.Command, io.Writer) error
}

func (r *recorder) Capture(ctx context.Context, c core.Command) (string, error) {
	r.mu.Lock()
	r.commands = append(r.commands, c)
	r.mu.Unlock()
	return r.capture(c)
}
func (r *recorder) Run(ctx context.Context, c core.Command, out io.Writer) error {
	r.mu.Lock()
	r.commands = append(r.commands, c)
	r.mu.Unlock()
	if r.run != nil {
		return r.run(ctx, c, out)
	}
	_, err := io.WriteString(out, "fixture package operation\n")
	return err
}
func (r *recorder) recorded() []core.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.Command{}, r.commands...)
}
func fixture(t *testing.T, id string) (*Backend, *recorder) {
	t.Helper()
	programs := map[string]string{"apt-get": "apt-get", "dnf": "dnf", "dnf5": "dnf5", "pacman": "pacman", "checkupdates": "checkupdates", "zypper": "zypper"}
	b := detect(id, programs, map[string]string{"PRETTY_NAME": "Fixture Linux"}, false)
	b.stateDir = t.TempDir()
	b.rebootFile = filepath.Join(b.stateDir, "reboot-required")
	r := &recorder{capture: func(core.Command) (string, error) {
		switch id {
		case "apt":
			return aptData, nil
		case "dnf":
			return "demo\tx86_64\t2.0-1\n", nil
		case "pacman":
			return "demo 1.0-1 -> 2.0-1\n", nil
		default:
			return zypperData, nil
		}
	}}
	b.runner = r
	return b, r
}

func TestDetectionUsesNativeFamilyAndExplicitCapabilities(t *testing.T) {
	all := map[string]string{"apt-get": "apt-get", "dnf5": "dnf5", "dnf": "dnf", "zypper": "zypper", "pacman": "pacman"}
	for _, tc := range []struct{ id, like, want string }{{"ubuntu", "debian", "apt"}, {"fedora", "", "dnf"}, {"opensuse-tumbleweed", "suse", "zypper"}, {"manjaro", "arch", "pacman"}, {"custom", "rhel fedora", "dnf"}} {
		b := detect("auto", all, map[string]string{"ID": tc.id, "ID_LIKE": tc.like}, false)
		if b.manager.ID != tc.want || !b.manager.Available {
			t.Fatal(tc, b.manager)
		}
	}
	if b := detect("auto", map[string]string{"dnf": "dnf"}, map[string]string{"ID": "ubuntu"}, false); b.manager.ID != "apt" || b.manager.Available {
		t.Fatal("wrong family fallback", b.manager)
	}
	if b := detect("auto", all, map[string]string{"ID": "unknown"}, false); b.manager.Available {
		t.Fatal("ambiguous autodetection", b.manager)
	}
	if b := detect("auto", map[string]string{"apt-get": "apt-get"}, nil, false); b.manager.ID != "apt" || !b.manager.Available {
		t.Fatal(b.manager)
	}
	if b := detect("auto", all, map[string]string{"ID": "fedora"}, true); b.manager.Available {
		t.Fatal("ostree automatically managed")
	}
	if b := detect("pacman", all, nil, false); b.manager.CanRefresh || b.manager.RefreshReason == "" || !b.manager.Available {
		t.Fatal("missing helper capability", b.manager)
	}
	if b := detect("dnf", map[string]string{"dnf": "dnf"}, nil, false); b.program != "dnf" {
		t.Fatal("DNF4 selection", b)
	}
	if b := detect("dnf", map[string]string{"dnf5": "dnf5"}, nil, false); b.program != "dnf5" {
		t.Fatal("DNF5 fallback", b)
	}
}

func TestCachedStatusCommandsAndParsing(t *testing.T) {
	for _, id := range []string{"apt", "dnf", "pacman", "zypper"} {
		t.Run(id, func(t *testing.T) {
			b, r := fixture(t, id)
			s, err := b.Status(context.Background())
			if err != nil || !s.Valid || len(s.Packages) == 0 {
				t.Fatal(s, err)
			}
			c := r.recorded()[0]
			if c.Timeout != 12*time.Second || c.CancelGrace != 0 {
				t.Fatal(c)
			}
			switch id {
			case "apt":
				if !slices.Contains(c.Args, "--simulate") || !slices.Contains(c.Args, "Debug::NoLocking=1") {
					t.Fatal(c)
				}
				if len(s.Packages) != 3 || s.Packages[1].Kind != "install" || s.Packages[2].Kind != "held" {
					t.Fatal(s.Packages)
				}
			case "dnf":
				if c.Program != "dnf" || !slices.Contains(c.Args, "--cacheonly") || !slices.Contains(c.Args, "--queryformat") {
					t.Fatal(c)
				}
			case "pacman":
				if !reflect.DeepEqual(c.Args, []string{"--query", "--upgrades", "--color", "never"}) {
					t.Fatal(c)
				}
			case "zypper":
				if !slices.Contains(c.Args, "--no-refresh") || !slices.Contains(c.Args, "--xmlout") {
					t.Fatal(c)
				}
				data, _ := json.Marshal(s)
				if strings.Contains(string(data), "PRIVATE") || strings.Contains(string(data), "SECRET") {
					t.Fatal("repository credentials leaked")
				}
			}
		})
	}
}

func TestRefreshAndInstallUseFixedCommands(t *testing.T) {
	for _, id := range []string{"apt", "dnf", "pacman", "zypper"} {
		t.Run(id, func(t *testing.T) {
			b, r := fixture(t, id)
			if _, err := b.Install(context.Background(), io.Discard); err != nil {
				t.Fatal(err)
			}
			commands := r.recorded()
			if id == "pacman" {
				if len(commands) != 1 || !reflect.DeepEqual(commands[0].Args, []string{"--sync", "--refresh", "--sysupgrade", "--noconfirm"}) {
					t.Fatal(commands)
				}
			} else if len(commands) != 2 {
				t.Fatal(commands)
			}
			last := commands[len(commands)-1]
			for _, c := range commands {
				if c.CancelGrace != 30*time.Second || c.Timeout != 20*time.Minute {
					t.Fatal(c)
				}
				for _, bad := range []string{"dist-upgrade", "full-upgrade", "distro-sync", "system-upgrade", "dup", "reboot", "--allow-unauthenticated", "--nogpgcheck"} {
					if slices.Contains(c.Args, bad) {
						t.Fatal("unsafe extra action", c)
					}
				}
			}
			switch id {
			case "apt":
				want := []string{"--assume-yes", "--no-remove", "--with-new-pkgs", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade"}
				if !reflect.DeepEqual(last.Args, want) || !slices.Contains(last.Env, "DEBIAN_FRONTEND=noninteractive") {
					t.Fatal(last)
				}
			case "dnf":
				if !reflect.DeepEqual(last.Args, []string{"--assumeyes", "upgrade"}) {
					t.Fatal(last)
				}
			case "zypper":
				if !reflect.DeepEqual(last.Args, []string{"--non-interactive", "--no-refresh", "update", "--type", "package"}) {
					t.Fatal(last)
				}
			}
		})
	}
	b, r := fixture(t, "pacman")
	if _, err := b.Refresh(context.Background(), io.Discard); err != nil {
		t.Fatal(err)
	}
	c := r.recorded()[0]
	if c.Program != "checkupdates" || !slices.Contains(c.Env, "CHECKUPDATES_DB="+b.cacheDir()) || len(c.Args) != 0 {
		t.Fatal(c)
	}
	if err := os.MkdirAll(filepath.Join(b.cacheDir(), "sync"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := b.Status(context.Background())
	if err != nil || !strings.Contains(s.CacheSource, "isolated") {
		t.Fatal(s, err)
	}
	if !slices.Contains(r.recorded()[1].Args, b.cacheDir()) {
		t.Fatal("isolated cache not queried")
	}
}

func TestInformationalExitCodesRemainCommandSpecific(t *testing.T) {
	for _, code := range []int{0, 100, 101, 106} {
		b, r := fixture(t, "zypper")
		r.capture = func(core.Command) (string, error) {
			if code == 0 {
				return zypperData, nil
			}
			return zypperData, statusError(code)
		}
		s, err := b.Status(context.Background())
		if err != nil || s.Valid != (code != 106) {
			t.Fatal(code, s, err)
		}
	}
	for _, code := range []int{0, 102, 103, 104, 106} {
		b, r := fixture(t, "zypper")
		r.run = func(ctx context.Context, c core.Command, out io.Writer) error {
			if slices.Contains(c.Args, "update") && code != 0 {
				return statusError(code)
			}
			return nil
		}
		result, err := b.Install(context.Background(), io.Discard)
		if (err == nil) != (code == 0 || code == 102 || code == 103) {
			t.Fatal(code, result, err)
		}
		if code == 102 && (b.reboot().Required == nil || !*b.reboot().Required) {
			t.Fatal("reboot hint lost")
		}
		if code == 103 && !result.(map[string]any)["recheckRequired"].(bool) {
			t.Fatal("package-manager restart hint lost")
		}
	}
	for _, code := range []int{0, 1, 2} {
		b, r := fixture(t, "pacman")
		r.run = func(context.Context, core.Command, io.Writer) error {
			if code == 0 {
				return nil
			}
			return statusError(code)
		}
		_, err := b.Refresh(context.Background(), io.Discard)
		if (err == nil) != (code == 0 || code == 2) {
			t.Fatal(code, err)
		}
	}
	for _, diagnostic := range []string{"", "error: could not read database"} {
		b, r := fixture(t, "pacman")
		r.capture = func(core.Command) (string, error) {
			return "", &core.CaptureError{Err: statusError(1), Stderr: diagnostic}
		}
		s, err := b.Status(context.Background())
		if err != nil || s.Valid != (diagnostic == "") {
			t.Fatal(diagnostic, s, err)
		}
	}
	b, r := fixture(t, "apt")
	r.capture = func(core.Command) (string, error) { return aptData, statusError(100) }
	s, _ := b.Status(context.Background())
	if s.Valid {
		t.Fatal("APT error 100 treated as updates-available")
	}
}

func TestFailuresDoNotAdvanceToInstallAndCancellationIsPreserved(t *testing.T) {
	b, r := fixture(t, "apt")
	r.run = func(context.Context, core.Command, io.Writer) error { return statusError(100) }
	if _, err := b.Install(context.Background(), io.Discard); err == nil || len(r.recorded()) != 1 {
		t.Fatal("failed refresh advanced to install", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	countBeforeCancel := len(r.recorded())
	if _, err := b.Install(ctx, io.Discard); !errors.Is(err, context.Canceled) || len(r.recorded()) != countBeforeCancel {
		t.Fatal("cancellation ignored", err)
	}
	b.manager.Available = false
	count := len(r.recorded())
	if _, err := b.Refresh(context.Background(), io.Discard); err == nil {
		t.Fatal("unavailable manager executed")
	}
	if len(r.recorded()) != count {
		t.Fatal("missing manager executed")
	}
}

func TestParserFailuresBoundsAndHolds(t *testing.T) {
	for _, suffix := range []string{" []", " [dependency:amd64 ]"} {
		rows, err := parseAPT("Inst demo [1.0] (2.0 Ubuntu:stable [amd64])" + suffix + "\n")
		if err != nil || len(rows) != 1 || rows[0].Architecture != "amd64" || rows[0].Candidate != "2.0" {
			t.Fatal(rows, err)
		}
	}
	for _, parse := range []func(string) ([]Package, error){parseAPT, parseDNF, parsePacman, parseZypper} {
		if _, err := parse("malformed output"); err == nil {
			t.Fatal("invalid data accepted")
		}
	}
	if _, err := parseAPT("Inst malformed\n"); err == nil {
		t.Fatal("invalid APT install row accepted")
	}
	if _, err := parseZypper(`<stream><message type="error">PRIVATE</message></stream>`); err == nil {
		t.Fatal("zypper error ignored")
	}
	if _, err := parseZypper(`<stream/>`); err == nil {
		t.Fatal("missing XML updates interpreted as empty")
	}
	rows, err := parsePacman("linux 1-1 -> 2-1 [ignored]\n")
	if err != nil || rows[0].Kind != "held" {
		t.Fatal(rows, err)
	}
	rows, err = parseAPT("The following upgrades have been deferred due to phasing:\n  demo\n0 upgraded, 0 newly installed, 0 to remove and 1 not upgraded.\n")
	if err != nil || len(rows) != 1 || rows[0].Kind != "held" {
		t.Fatal(rows, err)
	}
	if _, err := parseDNF(strings.Repeat("demo\tx86_64\t2.0\n", 2001)); err == nil {
		t.Fatal("unbounded updates")
	}
}

func TestRebootMarkerAndUnknownState(t *testing.T) {
	b, _ := fixture(t, "apt")
	if r := b.reboot(); r.Required == nil || *r.Required {
		t.Fatal(r)
	}
	if err := os.WriteFile(b.rebootFile, []byte("required"), 0600); err != nil {
		t.Fatal(err)
	}
	if r := b.reboot(); r.Required == nil || !*r.Required {
		t.Fatal(r)
	}
	b, _ = fixture(t, "dnf")
	if r := b.reboot(); r.Required != nil {
		t.Fatal("unknown state fabricated", r)
	}
}

func TestRegistrySharesMutationLockAndRequiresAcknowledgement(t *testing.T) {
	b, r := fixture(t, "apt")
	jobs, err := core.NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	registry := core.NewRegistry(true, jobs)
	if err := b.Register(registry); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Invoke(context.Background(), "updates_install", json.RawMessage(`{}`), "test"); err == nil {
		t.Fatal("installation confirmation bypassed")
	}
	if _, err := registry.Invoke(context.Background(), "updates_refresh", json.RawMessage(`{"command":"upgrade"}`), "test"); err == nil {
		t.Fatal("arbitrary argument accepted")
	}
	started := make(chan struct{})
	r.run = func(ctx context.Context, c core.Command, out io.Writer) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	result, err := registry.Invoke(context.Background(), "updates_refresh", json.RawMessage(`{}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["job"].(core.Job).ID
	<-started
	if _, err := registry.Invoke(context.Background(), "updates_install", json.RawMessage(`{"confirm":true}`), "test"); err == nil {
		t.Fatal("concurrent package job accepted")
	}
	if err := jobs.Cancel(id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, err := jobs.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			if job.State != "cancelled" {
				t.Fatal(job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not cancel")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLiveAPTSimulationDoesNotRefreshOrInstall(t *testing.T) {
	program, err := exec.LookPath("apt-get")
	if err != nil {
		t.Skip("APT not installed")
	}
	b := New(config.Config{PackageManager: "apt", StateDir: t.TempDir()}, core.Runner{})
	b.program = program
	s, err := b.Status(context.Background())
	if err != nil || !s.Valid {
		t.Fatal(s.Notice, err)
	}
	t.Logf("Read-only APT simulation parsed %d candidate/deferred packages; no refresh or installation was executed", len(s.Packages))
}
