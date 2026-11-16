package declarative

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestLibvirtLiveNetworkBridgeIsResolvedState(t *testing.T) {
	l, cfg, r := libvirtFixture(t)
	if err := l.Apply(context.Background(), Config{Version: 1}, cfg, "service/guest", "now"); err != nil {
		t.Fatal(err)
	}
	var live domainXML
	if err := xml.Unmarshal([]byte(r.xml), &live); err != nil {
		t.Fatal(err)
	}
	live.Devices.Interface.Source.Bridge = "virbr0"
	setLive := func() {
		data, err := xml.Marshal(live)
		if err != nil {
			t.Fatal(err)
		}
		r.activeXML, r.state = string(data), "running"
	}
	setLive()
	obs, err := l.Inspect(context.Background(), cfg, "service/guest")
	if err != nil || !obs.Active || !obs.Matches || !obs.ActiveMatches {
		t.Fatal("native resolved bridge must not require restart", obs, err)
	}
	live.Devices.Interface.Source.Network = "different-network"
	setLive()
	obs, err = l.Inspect(context.Background(), cfg, "service/guest")
	if err != nil || obs.ActiveMatches {
		t.Fatal("different network hidden", obs, err)
	}
	var want domainXML
	want.Devices.Interface.Type = "bridge"
	want.Devices.Interface.Source.Bridge = "br0"
	actual := want
	actual.Devices.Interface.Source.Bridge = "br1"
	if domainMatches(want, actual) {
		t.Fatal("explicit bridge drift hidden")
	}
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

type libvirtCapabilityRecorder struct {
	libvirtRecorder
	output                        string
	uriFailure, capabilityFailure error
}

func (r *libvirtCapabilityRecorder) Capture(ctx context.Context, c core.Command) (string, error) {
	if len(c.Args) > 2 && (c.Args[2] == "uri" || c.Args[2] == "domcapabilities") {
		r.commands = append(r.commands, c)
		if c.Args[2] == "uri" {
			return "qemu:///system\n", r.uriFailure
		}
		return r.output, r.capabilityFailure
	}
	return r.libvirtRecorder.Capture(ctx, c)
}

func TestLibvirtReadinessUsesConfiguredDaemonCapabilities(t *testing.T) {
	valid := `<domainCapabilities><path>/usr/bin/qemu-system-x86_64</path><domain>kvm</domain><machine>pc-q35-10.1</machine><arch>x86_64</arch><cpu><mode name="host-model" supported="yes"/></cpu></domainCapabilities>`
	for _, uri := range []string{"qemu:///system", "qemu:///session"} {
		recorder := &libvirtCapabilityRecorder{output: valid}
		backend := &Libvirt{URI: uri, Virsh: "/usr/bin/virsh", Validator: "/usr/bin/virt-xml-validate", Runner: recorder}
		if issues := backend.Prerequisites(context.Background()); len(issues) != 0 {
			t.Fatal(uri, issues)
		}
		if len(recorder.commands) != 2 {
			t.Fatal(recorder.commands)
		}
		probe := recorder.commands[1]
		if !slices.Equal(probe.Args, []string{"--connect", uri, "domcapabilities", "--virttype", "kvm", "--arch", "x86_64", "--machine", "q35"}) || probe.Timeout <= 0 || probe.Timeout > 5*time.Second || !slices.Contains(probe.Env, "LC_ALL=C") {
			t.Fatal(probe)
		}
	}
}

func TestLibvirtCapabilityFailuresAreBoundedRedactedAndBlockGuests(t *testing.T) {
	for _, tc := range []struct {
		name, output, code            string
		uriFailure, capabilityFailure error
	}{
		{name: "connection", uriFailure: errors.New("private-native-output"), code: "libvirt_unavailable"},
		{name: "KVM unsupported", capabilityFailure: errors.New("private-native-output"), code: "kvm_unavailable"},
		{name: "malformed", output: "private-native-output", code: "kvm_unknown"},
		{name: "wrong domain", output: `<domainCapabilities><domain>qemu</domain><arch>x86_64</arch></domainCapabilities>`, code: "kvm_unknown"},
		{name: "wrong machine", output: `<domainCapabilities><domain>kvm</domain><arch>x86_64</arch><machine>pc</machine></domainCapabilities>`, code: "kvm_unknown"},
		{name: "missing machine", output: `<domainCapabilities><domain>kvm</domain><arch>x86_64</arch></domainCapabilities>`, code: "kvm_unknown"},
		{name: "duplicate machine", output: `<domainCapabilities><domain>kvm</domain><arch>x86_64</arch><machine>q35</machine><machine>pc</machine></domainCapabilities>`, code: "kvm_unknown"},
		{name: "wrong architecture", output: `<domainCapabilities><domain>kvm</domain><arch>aarch64</arch></domainCapabilities>`, code: "kvm_unknown"},
		{name: "duplicate domain", output: `<domainCapabilities><domain>qemu</domain><domain>kvm</domain><arch>x86_64</arch></domainCapabilities>`, code: "kvm_unknown"},
		{name: "duplicate architecture", output: `<domainCapabilities><domain>kvm</domain><arch>x86_64</arch><arch>x86_64</arch></domainCapabilities>`, code: "kvm_unknown"},
		{name: "trailing document", output: `<domainCapabilities><domain>kvm</domain><arch>x86_64</arch></domainCapabilities><other/>`, code: "kvm_unknown"},
		{name: "trailing text", output: `<domainCapabilities><domain>kvm</domain><arch>x86_64</arch></domainCapabilities>unexpected`, code: "kvm_unknown"},
		{name: "oversized", output: strings.Repeat("x", (1<<20)+1), code: "kvm_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &libvirtCapabilityRecorder{output: tc.output, uriFailure: tc.uriFailure, capabilityFailure: tc.capabilityFailure}
			backend := &Libvirt{URI: "qemu:///system", Virsh: "/usr/bin/virsh", Validator: "/usr/bin/virt-xml-validate", Runner: recorder}
			issues := backend.Prerequisites(context.Background())
			if len(issues) != 1 || issues[0].Code != tc.code || issues[0].Severity == "warning" {
				t.Fatal(issues)
			}
			if strings.Contains(issues[0].Message+issues[0].Remedy, "private-native-output") {
				t.Fatal("native diagnostic exposed")
			}
			if tc.uriFailure != nil && len(recorder.commands) != 1 {
				t.Fatal("probed capabilities after connection failure")
			}
			fixtureBackend, cfg, _ := libvirtFixture(t)
			c := Coordinator{Store: fixtureBackend.Store, Adapters: map[string]Adapter{"libvirt": &probeAdapter{issues: issues}}}
			if health := c.Store.CheckFiles(cfg); len(health.Issues) != 0 {
				t.Fatal("fixture prerequisites were not healthy", health.Issues)
			}
			if err := c.readinessError(context.Background(), cfg, "guest", false); err == nil {
				t.Fatal("unverified KVM allowed current startup")
			}
		})
	}
}
