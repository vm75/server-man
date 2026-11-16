// Package installation owns the explicit, local host bootstrap workflow.
package installation

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"server-man/internal/config"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"server-man/internal/modules/updates"
)

var safeName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
var safePath = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

type plan struct {
	Account, Home, UID, GID, Bundle, Manager string
	Backends, Packages, Groups               []string
	SkipPackages                             bool
}

func selected(value string) ([]string, error) {
	if value == "all" {
		return []string{"podman", "docker", "virtualization", "incus", "samba", "nfs"}, nil
	}
	var result []string
	for _, item := range strings.Split(value, ",") {
		switch item {
		case "podman", "docker", "virtualization", "incus", "samba", "nfs":
		default:
			return nil, fmt.Errorf("unknown backend %q", item)
		}
		if slices.Contains(result, item) {
			return nil, fmt.Errorf("duplicate backend %q", item)
		}
		result = append(result, item)
	}
	return result, nil
}

func packagePlan(distribution string, backends []string) (string, []string, error) {
	manager := ""
	switch distribution {
	case "ubuntu", "debian":
		manager = "apt"
	case "arch":
		manager = "pacman"
	default:
		return "", nil, fmt.Errorf("unsupported distribution %q; supported: Ubuntu, Debian, Arch Linux", distribution)
	}
	packages := []string{"sudo"}
	for _, target := range backends {
		names, err := updates.DependencyPackages(manager, distribution, target)
		if err != nil {
			return "", nil, err
		}
		for _, name := range names {
			if !slices.Contains(packages, name) {
				packages = append(packages, name)
			}
		}
	}
	return manager, packages, nil
}

func distributionID(data string) string {
	for _, line := range strings.Split(data, "\n") {
		if strings.HasPrefix(line, "ID=") {
			return strings.Trim(strings.TrimPrefix(line, "ID="), `"'`)
		}
	}
	return ""
}

// hostUnits is shared by the reviewed plan and execution. Docker's socket is
// enabled for the early sockets target before boot-only user launchers run.
func hostUnits(backends []string) []string {
	var units []string
	for _, backend := range backends {
		switch backend {
		case "docker":
			units = append(units, "docker.socket", "docker.service")
		case "virtualization":
			units = append(units, "libvirtd.socket")
		case "incus":
			units = append(units, "incus.socket")
		}
	}
	return units
}

func Run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 2 && args[0] == "--initialize-config" {
		return initializeConfig(args[1])
	}
	fs := flag.NewFlagSet("server-man-install", flag.ContinueOnError)
	fs.SetOutput(out)
	account := fs.String("user", "", "existing workload account (required)")
	bundle := fs.String("bundle", ".", "built source tree or extracted release directory")
	backends := fs.String("backends", "all", "comma-separated podman,docker,virtualization,incus,samba,nfs; or all")
	yes := fs.Bool("yes", false, "approve the printed package, privilege and service changes")
	dry := fs.Bool("dry-run", false, "print the installation plan without changes")
	skip := fs.Bool("skip-packages", false, "use installed dependencies without refreshing or installing packages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if !safeName.MatchString(*account) || *account == "root" {
		return errors.New("--user must name an existing non-root workload account")
	}
	u, err := user.Lookup(*account)
	if err != nil {
		return err
	}
	if u.Uid == "0" || !safePath.MatchString(u.HomeDir) || filepath.Clean(u.HomeDir) != u.HomeDir {
		return errors.New("unsupported workload account home or UID")
	}
	if uid, err := strconv.Atoi(u.Uid); err != nil || uid <= 0 {
		return errors.New("workload account must have a positive numeric UID")
	}
	if gid, err := strconv.Atoi(u.Gid); err != nil || gid < 0 {
		return errors.New("workload account must have a nonnegative numeric GID")
	}
	targets, err := selected(*backends)
	if err != nil {
		return err
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return err
	}
	manager, packages, err := packagePlan(distributionID(string(osRelease)), targets)
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(*bundle)
	if err != nil {
		return err
	}
	p := plan{Account: *account, Home: u.HomeDir, UID: u.Uid, GID: u.Gid, Bundle: absolute, Manager: manager, Packages: packages, Backends: targets, SkipPackages: *skip}
	for _, target := range targets {
		switch target {
		case "docker":
			p.Groups = append(p.Groups, "docker")
		case "incus":
			p.Groups = append(p.Groups, "incus-admin")
		case "virtualization":
			p.Groups = append(p.Groups, "libvirt")
		}
	}
	for _, binary := range []string{"server-man", "server-man-admin", "server-man-install"} {
		info, err := os.Stat(filepath.Join(absolute, "bin", binary))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return fmt.Errorf("missing executable %s", binary)
		}
	}
	fmt.Fprintf(out, "Account: %s (%s)\nBackends: %s\nPackage manager: %s\nPackages: %s\nSkip package installation: %t\nGroups: %s\n", p.Account, p.UID, strings.Join(targets, ", "), manager, strings.Join(packages, " "), *skip, strings.Join(p.Groups, ", "))
	fmt.Fprintln(out, "Package policy: unpinned latest candidates from the configured native distribution repositories; no upstream repository additions or exact version locks. --skip-packages preserves installed versions.")
	fmt.Fprintln(out, "Host units to enable/start:", strings.Join(hostUnits(targets), ", "))
	fmt.Fprintln(out, "Install: /usr/local/bin/server-man, /usr/local/libexec/server-man-admin, /usr/local/sbin/server-man-install")
	fmt.Fprintf(out, "Privilege rule: %s ALL=(root) NOPASSWD: NOSETENV: /usr/local/libexec/server-man-admin \"\"\n", p.Account)
	fmt.Fprintln(out, "Enable lingering and server-man.service as the workload account, with loopback access and management writes. Docker/Incus groups and helper operations grant broad host control. Package installation can start daemons; Arch uses a full system upgrade. Existing configuration is preserved. Incus pools/networks and shares are not initialized.")
	if *dry {
		return nil
	}
	if os.Geteuid() != 0 {
		return errors.New("run installation with sudo (or use --dry-run)")
	}
	if !*yes {
		fmt.Fprint(out, "Apply this plan? [y/N] ")
		answer, err := bufio.NewReader(in).ReadString('\n')
		if err != nil {
			return err
		}
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			return errors.New("installation cancelled")
		}
	}
	lock, err := os.OpenFile("/run/lock/server-man-install.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.New("another installation is running")
	}
	return p.apply(ctx, out)
}

func run(ctx context.Context, out io.Writer, program string, args ...string) error {
	// A fixed trusted search path prevents selecting executables from the bundle.
	var executable string
	for _, dir := range []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"} {
		path := filepath.Join(dir, program)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			executable = path
			break
		}
	}
	if executable == "" {
		return fmt.Errorf("required command unavailable: %s", program)
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l"}
	command.Stdout = out
	command.Stderr = out
	command.Stdin = nil
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGINT) }
	command.WaitDelay = 30 * time.Second
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", program, err)
	}
	return nil
}

func atomicFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".server-man-install-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

func rootDirectory(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return err
	}
	for current := path; current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("installation parent must be root-owned, non-symlink and not group/world writable: %s", current)
		}
	}
	return nil
}

func (p plan) unit() string {
	return fmt.Sprintf(`[Unit]
Description=server-man Linux server manager
Requires=user@%s.service
After=user@%s.service network.target

[Service]
Type=simple
User=%s
Group=%s
Environment=HOME=%s XDG_RUNTIME_DIR=/run/user/%s DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/%s/bus
WorkingDirectory=%s
ExecStart=/usr/local/bin/server-man -config %s/.config/server-man/server-man.yaml
Restart=on-failure
RestartSec=3s
TimeoutStopSec=60s
KillMode=mixed

[Install]
WantedBy=multi-user.target
`, p.UID, p.UID, p.Account, p.GID, p.Home, p.UID, p.UID, p.Home, p.Home)
}

func (p plan) apply(ctx context.Context, out io.Writer) error {
	if !p.SkipPackages {
		switch p.Manager {
		case "apt":
			if err := run(ctx, out, "apt-get", "update"); err != nil {
				return err
			}
			args := append([]string{"--assume-yes", "--no-remove", "--no-install-recommends", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "install", "--"}, p.Packages...)
			if err := run(ctx, out, "apt-get", args...); err != nil {
				return err
			}
		case "pacman":
			args := append([]string{"--sync", "--refresh", "--sysupgrade", "--needed", "--noconfirm", "--"}, p.Packages...)
			if err := run(ctx, out, "pacman", args...); err != nil {
				return err
			}
		}
	}
	for _, group := range p.Groups {
		if _, err := user.LookupGroup(group); err != nil {
			return fmt.Errorf("required group %s missing; install its backend first", group)
		}
	}
	// Check helper policy before changing persistent files or service ownership.
	for _, directory := range []string{"/usr/local/bin", "/usr/local/libexec", "/usr/local/sbin", "/etc/sudoers.d", "/etc/systemd/system"} {
		if err := rootDirectory(directory); err != nil {
			return err
		}
	}
	rule := []byte(fmt.Sprintf("%s ALL=(root) NOPASSWD: NOSETENV: /usr/local/libexec/server-man-admin \"\"\n", p.Account))
	f, err := os.CreateTemp("/etc/sudoers.d", ".server-man-check-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(rule); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = run(ctx, out, "visudo", "-cf", f.Name()); err != nil {
		return err
	}
	for _, binary := range []struct{ source, target string }{{"server-man", "/usr/local/bin/server-man"}, {"server-man-admin", "/usr/local/libexec/server-man-admin"}, {"server-man-install", "/usr/local/sbin/server-man-install"}} {
		data, err := os.ReadFile(filepath.Join(p.Bundle, "bin", binary.source))
		if err != nil {
			return err
		}
		if err = atomicFile(binary.target, data, 0755); err != nil {
			return err
		}
	}
	if err = atomicFile("/etc/sudoers.d/server-man", rule, 0440); err != nil {
		return err
	}
	if len(p.Groups) > 0 {
		if err = run(ctx, out, "usermod", "-aG", strings.Join(p.Groups, ","), p.Account); err != nil {
			return err
		}
	}
	// Run mkdir as the workload user: never follow user-controlled paths as root.
	configDir := filepath.Join(p.Home, ".config/server-man")
	if err = run(ctx, out, "runuser", "-u", p.Account, "--", "mkdir", "-p", "-m", "0700", configDir); err != nil {
		return err
	}
	// Delegate exclusive configuration creation to an unprivileged child process.
	if err = run(ctx, out, "runuser", "-u", p.Account, "--", "/usr/local/sbin/server-man-install", "--initialize-config", p.Home); err != nil {
		return err
	}
	if err = run(ctx, out, "loginctl", "enable-linger", p.Account); err != nil {
		return err
	}
	if err = run(ctx, out, "systemctl", "start", "user@"+p.UID+".service"); err != nil {
		return err
	}
	// Replace only server-man's user service, preserving the desktop session.
	userSystemctl := []string{"-u", p.Account, "--", "env", "XDG_RUNTIME_DIR=/run/user/" + p.UID, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/" + p.UID + "/bus", "systemctl", "--user"}
	var loaded bytes.Buffer
	if err = run(ctx, &loaded, "runuser", append(slices.Clone(userSystemctl), "show", "--property=LoadState", "--value", "server-man.service")...); err != nil {
		return err
	}
	if strings.TrimSpace(loaded.String()) != "not-found" {
		for _, action := range []string{"stop", "disable"} {
			if err = run(ctx, out, "runuser", append(slices.Clone(userSystemctl), action, "server-man.service")...); err != nil {
				return err
			}
		}
	}
	for _, unit := range hostUnits(p.Backends) {
		if err = run(ctx, out, "systemctl", "enable", "--now", unit); err != nil {
			return err
		}
	}

	if err = atomicFile("/etc/systemd/system/server-man.service", []byte(p.unit()), 0644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "server-man.service"}, {"restart", "server-man.service"}, {"is-active", "server-man.service"}} {
		if err = run(ctx, out, "systemctl", args...); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "Installed: http://127.0.0.1:8080. Token: "+configDir+"/token. Check provider readiness before defining workloads. Incus needs explicit storage/network initialization; no workloads or shares were created.")
	return nil
}

// initializeConfig runs only after runuser has dropped all root privileges.
func initializeConfig(home string) error {
	if os.Geteuid() == 0 {
		return errors.New("configuration initialization must run as the workload account")
	}
	u, err := user.Current()
	if err != nil {
		return err
	}
	if u.HomeDir != home {
		return errors.New("configuration home does not match workload account")
	}
	return writeInitialConfiguration(filepath.Join(home, ".config/server-man/server-man.yaml"), home)
}

func writeInitialConfiguration(path, home string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		_, err = config.Load(path)
		return err
	}
	if err != nil {
		return err
	}
	defer f.Close()
	data, _ := yaml.Marshal(map[string]any{"version": 1, "server": map[string]any{"listen": "127.0.0.1:8080", "allow_writes": true, "privileged_operations": true}})
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
