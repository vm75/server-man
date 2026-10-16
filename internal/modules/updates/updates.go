// Package updates exposes fixed native package-manager update operations.
package updates

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"serverman/internal/config"
	"serverman/internal/core"
)

type Manager struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
	CanRefresh    bool   `json:"canRefresh"`
	RefreshReason string `json:"refreshReason,omitempty"`
}
type Package struct {
	Name         string `json:"name"`
	Architecture string `json:"architecture,omitempty"`
	Installed    string `json:"installed,omitempty"`
	Candidate    string `json:"candidate,omitempty"`
	Kind         string `json:"kind"`
}
type Reboot struct {
	Required *bool  `json:"required"`
	Reason   string `json:"reason"`
}
type Snapshot struct {
	Manager      Manager   `json:"manager"`
	Distribution string    `json:"distribution"`
	Packages     []Package `json:"packages"`
	Valid        bool      `json:"valid"`
	Notice       string    `json:"notice"`
	CacheSource  string    `json:"cacheSource"`
	Reboot       Reboot    `json:"reboot"`
	ReadAt       time.Time `json:"readAt"`
}
type Backend struct {
	manager                                                   Manager
	program, checkupdates, stateDir, distribution, rebootFile string
	runner                                                    core.Executor
	mu                                                        sync.Mutex
	rebootHint                                                bool
}

func New(cfg config.Config, runner core.Executor) *Backend {
	programs := map[string]string{}
	for _, name := range []string{"apt-get", "dnf5", "dnf", "pacman", "checkupdates", "zypper"} {
		if path, err := exec.LookPath(name); err == nil {
			programs[name] = path
		}
	}
	var data []byte
	if file, err := os.Open("/etc/os-release"); err == nil {
		data, _ = io.ReadAll(io.LimitReader(file, (64<<10)+1))
		_ = file.Close()
	}
	release := parseRelease(string(data))
	_, ostreeErr := os.Stat("/run/ostree-booted")
	b := detect(cfg.PackageManager, programs, release, ostreeErr == nil)
	b.stateDir = cfg.StateDir
	b.runner = runner
	b.rebootFile = "/run/reboot-required"
	return b
}

func parseRelease(text string) map[string]string {
	result := map[string]string{}
	if len(text) > 64<<10 {
		return result
	}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && slices.Contains([]string{"ID", "ID_LIKE", "PRETTY_NAME", "VARIANT_ID"}, key) {
			result[key] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return result
}
func detect(requested string, programs, release map[string]string, ostree bool) *Backend {
	b := &Backend{distribution: release["PRETTY_NAME"], checkupdates: programs["checkupdates"]}
	native := func(id string) string {
		switch id {
		case "debian", "ubuntu", "linuxmint", "pop":
			return "apt"
		case "fedora", "rhel", "centos", "rocky", "almalinux", "ol":
			return "dnf"
		case "arch", "manjaro", "endeavouros":
			return "pacman"
		case "suse", "opensuse", "opensuse-leap", "opensuse-tumbleweed", "sles", "sled":
			return "zypper"
		}
		return ""
	}
	if requested == "" {
		requested = "auto"
	}
	if requested == "auto" {
		if ostree || slices.Contains([]string{"silverblue", "kinoite", "sericea", "onyx"}, release["VARIANT_ID"]) {
			b.manager = Manager{ID: "unsupported", Title: "Image-based system", Reason: "Image-based OS updates are not supported; use the distribution's native update workflow"}
			return b
		}
		requested = native(release["ID"])
		if requested == "" {
			for _, id := range strings.Fields(release["ID_LIKE"]) {
				if requested = native(id); requested != "" {
					break
				}
			}
		}
		if requested == "" {
			found := []string{}
			for _, id := range []string{"apt", "dnf", "pacman", "zypper"} {
				name := id
				if id == "apt" {
					name = "apt-get"
				}
				if programs[name] != "" || (id == "dnf" && programs["dnf5"] != "") {
					found = append(found, id)
				}
			}
			if len(found) == 1 {
				requested = found[0]
			} else {
				b.manager = Manager{ID: "unsupported", Title: "No selected package manager", Reason: "No unambiguous supported native manager detected; configure packageManager explicitly when appropriate"}
				return b
			}
		}
	}
	b.manager = Manager{ID: requested, Title: map[string]string{"apt": "APT", "dnf": "DNF", "pacman": "Pacman", "zypper": "Zypper"}[requested]}
	program := requested
	if program == "apt" {
		program = "apt-get"
	}
	if program == "dnf" && programs["dnf"] == "" && programs["dnf5"] != "" {
		program = "dnf5"
		b.manager.Title = "DNF 5"
	}
	b.program = programs[program]
	b.manager.Available = b.program != ""
	b.manager.CanRefresh = b.manager.Available
	if !b.manager.Available {
		b.manager.Reason = program + " executable was not found; install the native tools and restart Serverman"
	}
	if requested == "pacman" && b.checkupdates == "" {
		b.manager.CanRefresh = false
		b.manager.RefreshReason = "Install pacman-contrib/checkupdates for isolated metadata refresh. Install updates still uses a full pacman -Syu."
	}
	return b
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var status interface{ ExitCode() int }
	if errors.As(err, &status) {
		return status.ExitCode()
	}
	return -1
}
func (b *Backend) cacheDir() string { return filepath.Join(b.stateDir, "updates-pacman") }
func (b *Backend) reboot() Reboot {
	if _, err := os.Stat(b.rebootFile); err == nil {
		yes := true
		return Reboot{Required: &yes, Reason: "The host's reboot-required marker is present"}
	} else if !os.IsNotExist(err) {
		return Reboot{Reason: "Reboot marker could not be inspected"}
	}
	b.mu.Lock()
	hint := b.rebootHint
	b.mu.Unlock()
	if hint {
		yes := true
		return Reboot{Required: &yes, Reason: "Zypper reported that a reboot is required during this server process"}
	}
	if b.manager.ID == "apt" {
		no := false
		return Reboot{Required: &no, Reason: "No reboot requirement is declared by the Debian/Ubuntu marker"}
	}
	return Reboot{Reason: "No reliable reboot requirement is available from this backend"}
}

func (b *Backend) Status(ctx context.Context) (Snapshot, error) {
	s := Snapshot{Manager: b.manager, Distribution: b.distribution, Packages: []Package{}, Reboot: b.reboot(), ReadAt: time.Now().UTC(), CacheSource: "Native cached repository metadata", Notice: "Package listings use cached metadata. Refresh before reviewing updates; installation resolves the current repositories and may change the plan."}
	if !b.manager.Available {
		s.Notice = b.manager.Reason
		return s, nil
	}
	c := core.Command{Program: b.program, Timeout: 12 * time.Second, Env: []string{"NO_COLOR=1"}}
	switch b.manager.ID {
	case "apt":
		c.Args = []string{"--simulate", "--no-remove", "--with-new-pkgs", "-o", "Debug::NoLocking=1", "-o", "APT::Get::Show-User-Simulation-Note=false", "-o", "Dir::Cache::pkgcache=", "-o", "Dir::Cache::srcpkgcache=", "upgrade"}
		s.Notice += " APT holds/phased or dependency-blocked packages may remain deferred."
	case "dnf":
		c.Args = []string{"--cacheonly", "--quiet", "repoquery", "--upgrades", "--latest-limit=1", "--queryformat", "%{name}\t%{arch}\t%{evr}\n"}
	case "pacman":
		c.Args = []string{"--query", "--upgrades", "--color", "never"}
		if stat, err := os.Stat(filepath.Join(b.cacheDir(), "sync")); err == nil && stat.IsDir() {
			c.Args = append(c.Args, "--dbpath", b.cacheDir())
			s.CacheSource = "Serverman's isolated checkupdates database"
		}
	case "zypper":
		c.Args = []string{"--non-interactive", "--no-refresh", "--xmlout", "list-updates", "--type", "package"}
	}
	text, err := b.runner.Capture(ctx, c)
	code := exitCode(err)
	if err != nil {
		accepted := b.manager.ID == "zypper" && (code == 100 || code == 101)
		if b.manager.ID == "pacman" && code == 1 && strings.TrimSpace(text) == "" {
			var captured *core.CaptureError
			accepted = errors.As(err, &captured) && captured.Stderr == ""
		}
		if !accepted {
			s.Notice = "Pending updates are unavailable; check package-manager permissions, cached metadata and native diagnostics"
			return s, nil
		}
	}
	var packages []Package
	switch b.manager.ID {
	case "apt":
		packages, err = parseAPT(text)
	case "dnf":
		packages, err = parseDNF(text)
	case "pacman":
		packages, err = parsePacman(text)
	case "zypper":
		packages, err = parseZypper(text)
	}
	if err != nil {
		s.Notice = "Package manager returned unsupported or oversized update data"
		return s, nil
	}
	s.Packages = packages
	s.Valid = true
	return s, nil
}

func (b *Backend) refreshCommand() (core.Command, error) {
	c := core.Command{Program: b.program, Timeout: 20 * time.Minute, CancelGrace: 30 * time.Second, Env: []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l", "NO_COLOR=1"}}
	switch b.manager.ID {
	case "apt":
		c.Args = []string{"-o", "APT::Update::Error-Mode=any", "update"}
	case "dnf":
		c.Args = []string{"--assumeyes", "--refresh", "makecache"}
	case "zypper":
		c.Args = []string{"--non-interactive", "refresh"}
	case "pacman":
		if b.checkupdates == "" {
			return c, core.Unavailable(b.manager.RefreshReason)
		}
		if !filepath.IsAbs(b.stateDir) {
			return c, core.Unavailable("A private absolute state directory is required for checkupdates")
		}
		c.Program = b.checkupdates
		c.Env = append(c.Env, "CHECKUPDATES_DB="+b.cacheDir())
	default:
		return c, core.Unavailable("Unsupported package manager")
	}
	return c, nil
}
func (b *Backend) Refresh(ctx context.Context, out io.Writer) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !b.manager.Available {
		return nil, core.Unavailable(b.manager.Reason)
	}
	c, err := b.refreshCommand()
	if err != nil {
		return nil, err
	}
	err = b.runner.Run(ctx, c, out)
	if err != nil && !(b.manager.ID == "pacman" && exitCode(err) == 2) {
		return nil, fmt.Errorf("Metadata refresh failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return map[string]string{"manager": b.manager.ID, "action": "metadata refreshed"}, nil
}
func (b *Backend) Install(ctx context.Context, out io.Writer) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !b.manager.Available {
		return nil, core.Unavailable(b.manager.Reason)
	}
	if b.manager.ID != "pacman" {
		if _, err := b.Refresh(ctx, out); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c := core.Command{Program: b.program, Timeout: 20 * time.Minute, CancelGrace: 30 * time.Second, Env: []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l", "NO_COLOR=1"}}
	switch b.manager.ID {
	case "apt":
		c.Args = []string{"--assume-yes", "--no-remove", "--with-new-pkgs", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade"}
	case "dnf":
		c.Args = []string{"--assumeyes", "upgrade"}
	case "pacman":
		c.Args = []string{"--sync", "--refresh", "--sysupgrade", "--noconfirm"}
	case "zypper":
		c.Args = []string{"--non-interactive", "--no-refresh", "update", "--type", "package"}
	}
	err := b.runner.Run(ctx, c, out)
	code := exitCode(err)
	recheck := false
	if b.manager.ID == "zypper" && (code == 102 || code == 103) {
		if code == 102 {
			b.mu.Lock()
			b.rebootHint = true
			b.mu.Unlock()
		}
		recheck = code == 103
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("Package installation failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"manager": b.manager.ID, "action": "updates installed", "reboot": b.reboot(), "recheckRequired": recheck}, nil
}
