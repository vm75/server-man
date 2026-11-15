package declarative

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-man/internal/core"
)

func libvirtFixture(t *testing.T) (*Libvirt, Config, *libvirtRecorder) {
	t.Helper()
	c, err := Parse([]byte(vmExample))
	if err != nil {
		t.Fatal(err)
	}
	c, err = PrepareIdentities(c, Config{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	disk := filepath.Join(root, "root.qcow2")
	if err = os.WriteFile(disk, []byte("fixture; not a disk image"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Storage = map[string]Storage{"root": {Path: "root.qcow2", Kind: "file", Format: "qcow2"}}
	s := c.Services["guest"]
	s.VM.Disks = []string{"root"}
	s.VM.Image = ""
	c.Services["guest"] = s
	r := &libvirtRecorder{state: "shut off"}
	l := &Libvirt{Store: Store{Path: filepath.Join(root, "config.yaml")}, URI: "qemu:///session", Virsh: "virsh", Validator: "virt-xml-validate", Directory: filepath.Join(root, "generated"), Runner: r}
	return l, c, r
}

type libvirtRecorder struct {
	commands        []core.Command
	xml, activeXML  string
	exists, enabled bool
	state           string
	failShutdown    bool
}

func (r *libvirtRecorder) Run(_ context.Context, c core.Command, _ io.Writer) error {
	r.commands = append(r.commands, c)
	args := c.Args
	if len(args) < 3 {
		return nil
	}
	switch args[2] {
	case "define":
		data, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		r.xml = string(data)
		r.exists = true
	case "autostart":
		r.enabled = !strings.Contains(strings.Join(args, " "), "--disable")
	case "start":
		r.state = "running"
		r.activeXML = r.xml
	case "shutdown":
		if r.failShutdown {
			return errors.New("fixture denied")
		}
		r.state = "shut off"
	case "undefine":
		r.exists = false
	}
	return nil
}
func (r *libvirtRecorder) Capture(_ context.Context, c core.Command) (string, error) {
	r.commands = append(r.commands, c)
	if c.Program == "virt-xml-validate" {
		return "", nil
	}
	if len(c.Args) < 3 {
		return "", nil
	}
	args := c.Args
	var d domainXML
	if r.xml != "" {
		_ = xml.Unmarshal([]byte(r.xml), &d)
	}
	switch args[2] {
	case "list":
		if !r.exists {
			return "", nil
		}
		if args[len(args)-1] == "--uuid" {
			return d.UUID + "\n", nil
		}
		return d.Name + "\n", nil
	case "dumpxml":
		if strings.Contains(strings.Join(args, " "), "--inactive") {
			return r.xml, nil
		}
		if r.activeXML != "" {
			return r.activeXML, nil
		}
		return r.xml, nil
	case "domstate":
		return r.state + "\n", nil
	case "dominfo":
		if r.enabled {
			return "Autostart: enable\n", nil
		}
		return "Autostart: disable\n", nil
	}
	return "", nil
}
func TestLibvirtRenderStableIdentityAndEscapedPaths(t *testing.T) {
	l, c, _ := libvirtFixture(t)
	st := c.Storage["root"]
	st.Path = "disk & data.qcow2"
	c.Storage["root"] = st
	bytes, err := RenderLibvirt(l.Store, c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	var d domainXML
	if err = xml.Unmarshal(bytes, &d); err != nil {
		t.Fatal(err)
	}
	if d.UUID != c.Services["guest"].VM.UUID || d.Metadata.Managed.XMLName.Space != libvirtMetadataNS || d.Devices.Disks[0].Source.File != l.Store.Resolve(st.Path) {
		t.Fatalf("invalid domain: %+v", d)
	}
	if !strings.Contains(string(bytes), "&amp;") {
		t.Fatal("XML path not escaped")
	}
}
func TestLibvirtDefineAutostartAndDeferredHardware(t *testing.T) {
	l, c, r := libvirtFixture(t)
	if err := l.Validate(context.Background(), c, "service/guest"); err != nil {
		t.Fatal(err)
	}
	if err := l.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	obs, err := l.Inspect(context.Background(), c, "service/guest")
	if err != nil || !obs.Matches || obs.Active {
		t.Fatal(obs, err)
	}
	for _, cmd := range r.commands {
		if len(cmd.Args) > 2 && cmd.Args[2] == "start" {
			t.Fatal("definition started VM")
		}
	}
	if err = l.Action(context.Background(), c, "service/guest", "start"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode(c)
	next, _ := Parse(raw)
	next.Services["guest"].VM.MemoryMiB = 2048
	if err = l.Apply(context.Background(), c, next, "service/guest", "now"); err == nil {
		t.Fatal("running hardware change silently applied")
	}
	if err = l.Apply(context.Background(), c, next, "service/guest", "next_start"); err != nil {
		t.Fatal(err)
	}
	obs, err = l.Inspect(context.Background(), next, "service/guest")
	if err != nil || !obs.Matches || !obs.Active || obs.ActiveMatches {
		t.Fatal("deferred update not distinguished", obs, err)
	}
	if err = l.Action(context.Background(), next, "service/guest", "restart"); err != nil {
		t.Fatal(err)
	}
	obs, err = l.Inspect(context.Background(), next, "service/guest")
	if err != nil || !obs.ActiveMatches {
		t.Fatal("restart did not load new configuration", obs, err)
	}
}
func TestLibvirtRemovalRetainsGuestDataAndRefusesForeignDomain(t *testing.T) {
	l, c, r := libvirtFixture(t)
	if err := l.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	original := r.xml
	r.xml = strings.Replace(r.xml, libvirtMetadataNS, "https://foreign.invalid/", 1)
	if err := l.Apply(context.Background(), c, Config{Version: 1}, "service/guest", "now"); err == nil {
		t.Fatal("foreign domain removed")
	}
	r.xml = original
	if err := l.Apply(context.Background(), c, Config{Version: 1}, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.Store.Resolve(c.Storage["root"].Path)); err != nil {
		t.Fatal("disk removed", err)
	}
	for _, cmd := range r.commands {
		for _, arg := range cmd.Args {
			if arg == "--remove-all-storage" || arg == "--nvram" || arg == "destroy" {
				t.Fatal("destructive data operation", cmd.Args)
			}
		}
	}
	if removed, err := l.VerifyRemoved(context.Background(), c, "service/guest"); err != nil || !removed {
		t.Fatal(removed, err)
	}
}
func TestLibvirtBootToggleDoesNotRestartGuest(t *testing.T) {
	l, c, r := libvirtFixture(t)
	if err := l.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	if err := l.Action(context.Background(), c, "service/guest", "start"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode(c)
	next, _ := Parse(raw)
	s := next.Services["guest"]
	s.Enabled = true
	next.Services["guest"] = s
	r.commands = nil
	if err := l.Apply(context.Background(), c, next, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.commands {
		if len(cmd.Args) > 2 {
			switch cmd.Args[2] {
			case "shutdown", "start", "reboot", "destroy":
				t.Fatal("boot toggle interrupted guest")
			}
		}
	}
}

func TestInstalledLibvirtSchemaValidatesTemporaryDomain(t *testing.T) {
	l, c, _ := libvirtFixture(t)
	native := NewLibvirt(l.Store, "qemu:///session", core.Runner{})
	if native.Validator == "" || native.Virsh == "" {
		t.Skip("native libvirt validation tools are not installed")
	}
	if err := native.Validate(context.Background(), c, "service/guest"); err != nil {
		t.Fatal(err)
	}
	// Only virt-xml-validate runs; no libvirt connection or guest mutation occurs.
}

func TestLibvirtShutdownFailureNeverForcesOrStarts(t *testing.T) {
	l, c, r := libvirtFixture(t)
	if err := l.Apply(context.Background(), Config{Version: 1}, c, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	r.state = "running"
	r.activeXML = r.xml
	r.failShutdown = true
	r.commands = nil
	if err := l.Action(context.Background(), c, "service/guest", "restart"); err == nil {
		t.Fatal("shutdown failure ignored")
	}
	for _, cmd := range r.commands {
		if len(cmd.Args) > 2 && (cmd.Args[2] == "start" || cmd.Args[2] == "destroy" || cmd.Args[2] == "reboot") {
			t.Fatal("guest force-stopped/restarted despite shutdown failure", cmd.Args)
		}
	}
}
func TestLibvirtRejectsUnsupportedProvisioningBeforeNativeMutation(t *testing.T) {
	l, c, r := libvirtFixture(t)
	c.Services["guest"].VM.Disks = nil
	c.Services["guest"].VM.Image = "assets/new.qcow2"
	if err := l.Validate(context.Background(), c, "service/guest"); err == nil {
		t.Fatal("image-only VM silently accepted")
	}
	if len(r.commands) != 0 {
		t.Fatal("unsupported source reached native commands")
	}
}
