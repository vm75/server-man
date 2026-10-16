// Package machines manages LXD, classic LXC and libvirt through fixed CLIs.
package machines

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"serverman/internal/config"
	"serverman/internal/core"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var instanceName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,62}$`)
var imageName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,255}$`)
var domainUUID = regexp.MustCompile(`^[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12}$`)

type Capability struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Context   string   `json:"context"`
	Actions   []string `json:"actions"`
	Notice    string   `json:"notice,omitempty"`
}

type Machine struct {
	Backend      string   `json:"backend"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	State        string   `json:"state"`
	Manageable   bool     `json:"manageable"`
	Architecture string   `json:"architecture,omitempty"`
	Location     string   `json:"location,omitempty"`
	IPs          []string `json:"ips"`
	CPUs         *int     `json:"cpus,omitempty"`
	MemoryBytes  *uint64  `json:"memoryBytes,omitempty"`
	UUID         string   `json:"uuid,omitempty"`
	Autostart    *bool    `json:"autostart,omitempty"`
	Persistent   *bool    `json:"persistent,omitempty"`
}

type Inventory struct {
	Backends []Capability `json:"backends"`
	Machines []Machine    `json:"machines"`
}

type Backend struct {
	cfg      config.Config
	programs map[string]string
	runner   core.Executor
}

func New(cfg config.Config, runner core.Executor) *Backend {
	if cfg.LXDRemote == "" {
		cfg.LXDRemote = "local"
	}
	if cfg.LXDProject == "" {
		cfg.LXDProject = "default"
	}
	if cfg.LibvirtURI == "" {
		cfg.LibvirtURI = "qemu:///system"
	}
	b := &Backend{cfg: cfg, runner: runner, programs: map[string]string{}}
	for _, name := range []string{"lxc", "lxc-ls", "lxc-info", "lxc-start", "lxc-stop", "virsh"} {
		if path, err := exec.LookPath(name); err == nil {
			b.programs[name] = path
		}
	}
	return b
}

func (b *Backend) capabilities() []Capability {
	lxcContext := b.cfg.LXCPath
	if lxcContext == "" {
		lxcContext = "Account's default LXC path"
	}
	result := []Capability{
		{ID: "lxd", Title: "LXD", Available: b.programs["lxc"] != "", Context: b.cfg.LXDRemote + ": / project " + b.cfg.LXDProject, Actions: []string{}},
		{ID: "lxc", Title: "Classic LXC", Available: b.programs["lxc-ls"] != "" && b.programs["lxc-info"] != "", Context: lxcContext, Actions: []string{}},
		{ID: "kvm", Title: "KVM / libvirt", Available: b.programs["virsh"] != "", Context: b.cfg.LibvirtURI, Actions: []string{}},
	}
	for i := range result {
		c := &result[i]
		if !c.Available {
			c.Reason = map[string]string{"lxd": "LXD client lxc was not found", "lxc": "Classic lxc-ls and lxc-info are required", "kvm": "virsh was not found"}[c.ID] + "; install tools and restart Serverman"
			continue
		}
		switch c.ID {
		case "lxd":
			c.Actions = []string{"launch", "start", "stop", "restart", "delete"}
		case "kvm":
			c.Actions = []string{"start", "shutdown", "reboot", "autostart", "define", "undefine"}
		case "lxc":
			for _, action := range []string{"start", "stop"} {
				if b.programs["lxc-"+action] != "" {
					c.Actions = append(c.Actions, action)
				}
			}
		}
	}
	return result
}

func (b *Backend) command(backend, action string, args ...string) core.Command {
	c := core.Command{Timeout: 4 * time.Second}
	switch backend {
	case "lxd":
		c.Program = b.programs["lxc"]
		c.Args = append([]string{"--project", b.cfg.LXDProject, action}, args...)
	case "kvm":
		c.Program = b.programs["virsh"]
		c.Args = append([]string{"--connect", b.cfg.LibvirtURI, action}, args...)
	case "lxc":
		c.Program = b.programs["lxc-"+action]
		if b.cfg.LXCPath != "" {
			c.Args = []string{"--lxcpath", b.cfg.LXCPath}
		}
		c.Args = append(c.Args, args...)
	}
	return c
}

func (b *Backend) List(ctx context.Context, backend string) (Inventory, error) {
	result := Inventory{Backends: []Capability{}, Machines: []Machine{}}
	if backend != "" && !slices.Contains([]string{"lxd", "lxc", "kvm"}, backend) {
		return result, core.Invalid("unsupported machine backend")
	}
	for _, c := range b.capabilities() {
		if backend != "" && c.ID != backend {
			continue
		}
		if c.Available {
			items, err := b.list(ctx, c.ID)
			if err != nil {
				c.Notice = c.Title + " inventory unavailable; check backend access, permissions and initialization"
			} else {
				result.Machines = append(result.Machines, items...)
			}
		}
		result.Backends = append(result.Backends, c)
	}
	return result, nil
}

func (b *Backend) list(ctx context.Context, backend string) ([]Machine, error) {
	var command core.Command
	switch backend {
	case "lxd":
		command = b.command(backend, "list", b.cfg.LXDRemote+":", "--format", "json")
	case "lxc":
		command = b.command(backend, "ls", "--fancy", "--fancy-format", "NAME,STATE")
	case "kvm":
		command = b.command(backend, "domstats", "--raw", "--state", "--balloon", "--vcpu")
	}
	text, err := b.runner.Capture(ctx, command)
	if err != nil {
		return nil, err
	}
	switch backend {
	case "lxd":
		return parseLXD(text)
	case "lxc":
		return parseLXC(text)
	default:
		return parseKVM(text)
	}
}

func validName(backend, name string) bool {
	if backend == "lxd" {
		return instanceName.MatchString(name) && !strings.HasSuffix(name, "-")
	}
	return identifier.MatchString(name)
}

func (b *Backend) Details(ctx context.Context, backend, name string) (Machine, error) {
	if !slices.Contains([]string{"lxd", "lxc", "kvm"}, backend) || !validName(backend, name) {
		return Machine{}, core.Invalid("unsupported backend or machine identifier")
	}
	for _, c := range b.capabilities() {
		if c.ID == backend && !c.Available {
			return Machine{}, core.Unavailable(c.Reason)
		}
	}
	var c core.Command
	switch backend {
	case "lxd":
		c = b.command(backend, "list", b.cfg.LXDRemote+":", "^"+name+"$", "--format", "json")
	case "lxc":
		c = b.command(backend, "info", "--name", name)
	case "kvm":
		c = b.command(backend, "dominfo", "--domain", name)
	}
	text, err := b.runner.Capture(ctx, c)
	if err != nil {
		return Machine{}, core.Unavailable("Machine details unavailable; check its identifier and backend permissions")
	}
	if backend == "lxd" {
		items, err := parseLXD(text)
		if err != nil {
			return Machine{}, core.Unavailable("LXD returned unsupported instance JSON")
		}
		for _, m := range items {
			if m.Name == name {
				return m, nil
			}
		}
		return Machine{}, &core.Error{Code: "not_found", Message: "Instance not found", Status: 404}
	}
	m, err := parseDetails(backend, text)
	if err != nil {
		return Machine{}, core.Unavailable("Backend returned unsupported machine details")
	}
	if m.Name != name && (backend != "kvm" || m.UUID != name) {
		return Machine{}, core.Invalid("Backend resolved a different machine; use its UUID for libvirt")
	}
	return m, nil
}

func (b *Backend) Action(ctx context.Context, backend, name, action string, out io.Writer) (any, error) {
	if !validName(backend, name) {
		return nil, core.Invalid("unsupported machine identifier")
	}
	var supported bool
	for _, c := range b.capabilities() {
		if c.ID == backend && c.Available {
			supported = slices.Contains(c.Actions, action)
		}
	}
	if !supported || action == "launch" || action == "define" {
		return nil, core.Unavailable("Machine action is unavailable for this backend")
	}
	var c core.Command
	switch backend {
	case "lxd":
		if action == "delete" {
			m, err := b.Details(ctx, backend, name)
			if err != nil {
				return nil, err
			}
			if m.State != "stopped" {
				return nil, core.Invalid("Stop the LXD instance before deleting it")
			}
		}
		c = b.command(backend, action, b.cfg.LXDRemote+":"+name)
		if action == "stop" || action == "restart" {
			c.Args = append(c.Args, "--timeout", "60")
		}
	case "lxc":
		c = b.command(backend, action, "--name", name)
		if action == "start" {
			c.Args = append(c.Args, "--daemon")
		} else {
			c.Args = append(c.Args, "--nokill", "--timeout", "60")
		}
	case "kvm":
		m, err := b.Details(ctx, backend, name)
		if err != nil {
			return nil, err
		}
		if !domainUUID.MatchString(m.UUID) {
			return nil, core.Unavailable("Backend did not return a domain UUID")
		}
		if action == "undefine" {
			if m.State != "shut off" {
				return nil, core.Invalid("Shut down the domain before undefining it")
			}
		}
		c = b.command(backend, action, "--domain", m.UUID)
		if action == "undefine" {
			c.Args = append(c.Args, "--keep-nvram")
		}
	}
	c.Timeout = 20 * time.Minute
	if err := b.runner.Run(ctx, c, out); err != nil {
		return nil, fmt.Errorf("%s %s failed: %w", backend, action, err)
	}
	return map[string]string{"backend": backend, "name": name, "action": action}, nil
}

func (b *Backend) Launch(ctx context.Context, name, image, kind string, out io.Writer) (any, error) {
	if !validName("lxd", name) || !imageName.MatchString(image) || strings.Contains(image, "//") || strings.Contains(image, "..") {
		return nil, core.Invalid("invalid LXD name or image alias/fingerprint")
	}
	if kind != "container" && kind != "virtual-machine" {
		return nil, core.Invalid("unsupported LXD instance type")
	}
	if b.programs["lxc"] == "" {
		return nil, core.Unavailable("LXD client is unavailable")
	}
	c := b.command("lxd", "launch", image, b.cfg.LXDRemote+":"+name)
	if kind == "virtual-machine" {
		c.Args = append(c.Args, "--vm")
	}
	c.Timeout = 20 * time.Minute
	if err := b.runner.Run(ctx, c, out); err != nil {
		return nil, err
	}
	return map[string]string{"name": name, "image": image, "kind": kind}, nil
}

func (b *Backend) Autostart(ctx context.Context, name string, enabled bool, out io.Writer) (any, error) {
	if !validName("kvm", name) {
		return nil, core.Invalid("invalid domain identifier")
	}
	if b.programs["virsh"] == "" {
		return nil, core.Unavailable("virsh is unavailable")
	}
	m, err := b.Details(ctx, "kvm", name)
	if err != nil {
		return nil, err
	}
	if !domainUUID.MatchString(m.UUID) {
		return nil, core.Unavailable("Backend did not return a domain UUID")
	}
	c := b.command("kvm", "autostart", "--domain", m.UUID)
	if !enabled {
		c.Args = append(c.Args, "--disable")
	}
	c.Timeout = time.Minute
	if err := b.runner.Run(ctx, c, out); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "autostart": enabled}, nil
}
