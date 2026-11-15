package declarative

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"server-man/internal/core"
)

type Libvirt struct {
	Store                            Store
	URI, Virsh, Validator, Directory string
	Runner                           core.Executor
}

func NewLibvirt(store Store, uri string, runner core.Executor) *Libvirt {
	v, _ := exec.LookPath("virsh")
	validator, _ := exec.LookPath("virt-xml-validate")
	return &Libvirt{Store: store, URI: uri, Virsh: v, Validator: validator, Directory: store.Path + ".generated/libvirt", Runner: runner}
}
func (l *Libvirt) command(args ...string) core.Command {
	return core.Command{Program: l.Virsh, Args: append([]string{"--connect", l.URI}, args...), Timeout: 5 * time.Second}
}
func (l *Libvirt) resourceDir(key string) string {
	_, id, _ := strings.Cut(key, "/")
	return filepath.Join(l.Directory, id)
}
func (l *Libvirt) Validate(ctx context.Context, c Config, key string) error {
	if l.URI != "qemu:///system" && l.URI != "qemu:///session" {
		return errors.New("only local libvirt connections are supported")
	}
	if l.Virsh == "" || l.Validator == "" {
		return errors.New("virsh and virt-xml-validate are required")
	}
	data, err := RenderLibvirt(l.Store, c, key)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "server-man-domain-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "domain.xml")
	if err = os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	if _, err = l.Runner.Capture(ctx, core.Command{Program: l.Validator, Args: []string{path, "domain"}, Timeout: 10 * time.Second}); err != nil {
		return errors.New("native libvirt schema validation failed")
	}
	return nil
}
func (l *Libvirt) exists(ctx context.Context, id, uuid string) (bool, error) {
	output, err := l.Runner.Capture(ctx, l.command("list", "--all", "--uuid"))
	if err != nil {
		return false, errors.New("libvirt inventory is unavailable")
	}
	for _, line := range strings.Fields(output) {
		if line == uuid {
			return true, nil
		}
	}
	output, err = l.Runner.Capture(ctx, l.command("list", "--all", "--name"))
	if err != nil {
		return false, errors.New("libvirt name inventory is unavailable")
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == vmDomainName(id) {
			return false, errors.New("an existing libvirt domain conflicts with this managed name")
		}
	}
	return false, nil
}
func (l *Libvirt) readDomain(ctx context.Context, c Config, key string, inactive bool) (domainXML, error) {
	_, id, _ := strings.Cut(key, "/")
	uuid := c.Services[id].VM.UUID
	args := []string{"dumpxml", "--domain", uuid}
	if inactive {
		args = append(args, "--inactive")
	}
	output, err := l.Runner.Capture(ctx, l.command(args...))
	if err != nil {
		return domainXML{}, errors.New("libvirt domain definition cannot be read")
	}
	var domain domainXML
	if xml.Unmarshal([]byte(output), &domain) != nil {
		return domain, errors.New("invalid libvirt domain XML")
	}
	if domain.UUID != uuid || domain.Name != vmDomainName(id) || domain.Metadata.Managed.XMLName.Space != libvirtMetadataNS || domain.Metadata.Managed.ID != id {
		return domain, errors.New("libvirt domain is not owned by this managed definition")
	}
	return domain, nil
}
func domainMatches(want, actual domainXML) bool {
	if actual.Memory.Unit == "KiB" || actual.Memory.Unit == "kib" {
		if actual.Memory.Value%1024 != 0 {
			return false
		}
		actual.Memory.Value /= 1024
		actual.Memory.Unit = "MiB"
	}
	if actual.Memory.Unit == "" {
		if actual.Memory.Value%1024 != 0 {
			return false
		}
		actual.Memory.Value /= 1024
		actual.Memory.Unit = "MiB"
	}
	if want.OS.Type.Machine == "q35" && strings.HasPrefix(actual.OS.Type.Machine, "pc-q35-") {
		actual.OS.Type.Machine = "q35"
	}
	// Compare managed hardware fields, allowing libvirt to retain its generated
	// addresses, controllers, aliases and other native defaults outside this model.
	return want.UUID == actual.UUID && want.Name == actual.Name && want.Type == actual.Type && want.VCPU == actual.VCPU && reflect.DeepEqual(want.Memory, actual.Memory) && reflect.DeepEqual(want.OS, actual.OS) && reflect.DeepEqual(want.Devices.Disks, actual.Devices.Disks) && reflect.DeepEqual(want.Devices.Interface, actual.Devices.Interface)
}
func (l *Libvirt) state(ctx context.Context, uuid string) (string, error) {
	output, err := l.Runner.Capture(ctx, l.command("domstate", "--domain", uuid))
	if err != nil {
		return "", errors.New("libvirt state cannot be read")
	}
	return strings.TrimSpace(output), nil
}
func (l *Libvirt) Inspect(ctx context.Context, c Config, key string) (Observation, error) {
	_, id, _ := strings.Cut(key, "/")
	s, ok := c.Services[id]
	if !ok {
		return Observation{}, errors.New("removed VM verification requires the prior identity")
	}
	exists, err := l.exists(ctx, id, s.VM.UUID)
	if err != nil {
		return Observation{}, err
	}
	if !exists {
		return Observation{ActiveMatches: true}, nil
	}
	persistent, err := l.readDomain(ctx, c, key, true)
	if err != nil {
		return Observation{}, err
	}
	raw, err := RenderLibvirt(l.Store, c, key)
	if err != nil {
		return Observation{}, err
	}
	var want domainXML
	if xml.Unmarshal(raw, &want) != nil {
		return Observation{}, errors.New("invalid generated domain")
	}
	state, err := l.state(ctx, s.VM.UUID)
	if err != nil {
		return Observation{}, err
	}
	obs := Observation{Registered: true, Active: state != "shut off", ActiveMatches: true, Matches: domainMatches(want, persistent) && persistent.Metadata.Managed.Fingerprint == resourceFingerprint(c, key)}
	output, err := l.Runner.Capture(ctx, l.command("dominfo", "--domain", s.VM.UUID))
	if err != nil {
		return obs, errors.New("libvirt boot policy cannot be read")
	}
	autostart := ""
	for _, line := range strings.Split(output, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "Autostart" {
			autostart = strings.TrimSpace(v)
		}
	}
	if (s.Enabled && autostart != "enable") || (!s.Enabled && autostart != "disable") {
		obs.Matches = false
	}
	if obs.Active {
		active, err := l.readDomain(ctx, c, key, false)
		if err != nil {
			return obs, err
		}
		obs.ActiveMatches = domainMatches(want, active) && active.Metadata.Managed.Runtime == runtimeFingerprint(c, key)
	}
	return obs, nil
}
func (l *Libvirt) Apply(ctx context.Context, previous, desired Config, key, mode string) error {
	_, id, _ := strings.Cut(key, "/")
	s, want := desired.Services[id]
	old, had := previous.Services[id]
	identity := s
	if !want {
		identity = old
	}
	if identity.VM == nil {
		return errors.New("VM identity unavailable")
	}
	exists, err := l.exists(ctx, id, identity.VM.UUID)
	if err != nil {
		return err
	}
	state := "shut off"
	if exists {
		config := desired
		if !want {
			config = previous
		}
		if _, err = l.readDomain(ctx, config, key, true); err != nil {
			return err
		}
		state, err = l.state(ctx, identity.VM.UUID)
		if err != nil {
			return err
		}
	}
	before, after := map[string][]byte{}, map[string][]byte{}
	if had {
		data, e := RenderLibvirt(l.Store, previous, key)
		if e != nil {
			return e
		}
		before["domain.xml"] = data
	}
	if want {
		data, e := RenderLibvirt(l.Store, desired, key)
		if e != nil {
			return e
		}
		after["domain.xml"] = data
	}
	// XML uses an XML comment rather than the INI/YAML ownership header.
	if err = checkArtifactOwnership(l.resourceDir(key), before, after); err != nil {
		return err
	}
	if !want {
		if exists && state != "shut off" {
			return errors.New("shut down VM before removing its definition")
		}
		if exists {
			cmd := l.command("undefine", "--domain", identity.VM.UUID, "--keep-nvram", "--keep-tpm")
			if err = l.Runner.Run(ctx, cmd, io.Discard); err != nil {
				return errors.New("domain removal failed; disks were not requested for deletion")
			}
		}
		return publishArtifacts(l.resourceDir(key), before, after)
	}
	if exists && mode == "now" && state != "shut off" {
		obs, e := l.Inspect(ctx, desired, key)
		if e != nil {
			return e
		}
		if !obs.ActiveMatches {
			return errors.New("running VM changes require explicit shutdown/start or Save for next start")
		}
	}
	if err = publishArtifacts(l.resourceDir(key), before, after); err != nil {
		return err
	}
	cmd := l.command("define", filepath.Join(l.resourceDir(key), "domain.xml"), "--validate")
	cmd.Timeout = time.Minute
	if err = l.Runner.Run(ctx, cmd, io.Discard); err != nil {
		return errors.New("persistent libvirt definition failed")
	}
	args := []string{"autostart", "--domain", s.VM.UUID}
	if !s.Enabled {
		args = append(args, "--disable")
	}
	if err = l.Runner.Run(ctx, l.command(args...), io.Discard); err != nil {
		return errors.New("libvirt boot policy update failed")
	}
	return nil
}
func (l *Libvirt) Action(ctx context.Context, c Config, key, action string) error {
	_, id, _ := strings.Cut(key, "/")
	s := c.Services[id]
	if s.VM == nil {
		return errors.New("VM identity unavailable")
	}
	if _, err := l.readDomain(ctx, c, key, true); err != nil {
		return err
	}
	switch action {
	case "start":
		return l.runStart(ctx, s.VM.UUID)
	case "stop", "restart":
		state, err := l.state(ctx, s.VM.UUID)
		if err != nil {
			return err
		}
		if state != "shut off" {
			if err = l.Runner.Run(ctx, l.command("shutdown", "--domain", s.VM.UUID), io.Discard); err != nil {
				return errors.New("guest shutdown request failed")
			}
		}
		deadline := time.NewTimer(90 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			state, err = l.state(ctx, s.VM.UUID)
			if err != nil {
				return err
			}
			if state == "shut off" {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return errors.New("guest did not shut down; force-stop was not requested")
			case <-ticker.C:
			}
		}
		if action == "restart" {
			return l.runStart(ctx, s.VM.UUID)
		}
		return nil
	default:
		return errors.New("unsupported libvirt action")
	}
}
func (l *Libvirt) runStart(ctx context.Context, uuid string) error {
	if err := l.Runner.Run(ctx, l.command("start", "--domain", uuid), io.Discard); err != nil {
		return errors.New("libvirt start failed")
	}
	state, err := l.state(ctx, uuid)
	if err != nil {
		return err
	}
	if state != "running" {
		return errors.New("VM did not reach running state")
	}
	return nil
}
func (l *Libvirt) Prerequisites(ctx context.Context) []Issue {
	issues := []Issue{}
	add := func(field, code, message, remedy string) {
		issues = append(issues, Issue{"error", "backend/libvirt", field, code, message, remedy})
	}
	if l.Virsh == "" {
		add("virsh", "missing_dependency", "virsh is missing.", "Install the libvirt client through Software.")
	} else if _, err := l.Runner.Capture(ctx, l.command("uri")); err != nil {
		add("connection", "libvirt_unavailable", "The configured libvirt connection is unavailable.", "Check the local daemon, connection URI and account permissions.")
	}
	if l.Validator == "" {
		add("validator", "missing_dependency", "virt-xml-validate is missing.", "Install native libvirt XML validation tools.")
	}
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		add("kvm", "kvm_unavailable", "Hardware virtualization is unavailable to the service account.", "Enable virtualization and grant deliberate access to /dev/kvm.")
	} else {
		f.Close()
	}
	return issues
}

func (l *Libvirt) VerifyRemoved(ctx context.Context, previous Config, key string) (bool, error) {
	_, id, _ := strings.Cut(key, "/")
	s, ok := previous.Services[id]
	if !ok || s.VM == nil {
		return false, errors.New("previous VM identity unavailable")
	}
	exists, err := l.exists(ctx, id, s.VM.UUID)
	return !exists, err
}
