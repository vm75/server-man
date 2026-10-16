package machines

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"serverman/internal/config"
	"serverman/internal/core"
)

const uuid = "8b57b5af-dd80-4b96-81d3-0880c0771122"
const lxdJSON = `[{"name":"demo","status":"Stopped","type":"container","architecture":"x86_64","location":"node1","config":{"user.secret":"PRIVATE"},"expanded_config":{"user.user-data":"PRIVATE"},"state":{"memory":{"usage":4096},"network":{"eth0":{"addresses":[{"address":"10.1.2.3","scope":"global"},{"address":"fe80::1","scope":"link"}]}}}}]`
const kvmStats = "Domain: 'demo'\n  state.state=5\n  balloon.current=2048\n  vcpu.current=2\n\nDomain: 'other guest'\n  state.state=1\n"
const kvmInfo = "Id: -\nName: demo\nUUID: " + uuid + "\nState: shut off\nCPU(s): 2\nUsed memory: 2048 KiB\nPersistent: yes\nAutostart: disable\nSecurity label: PRIVATE\n"

type recorder struct {
	mu       sync.Mutex
	commands []core.Command
	capture  func(core.Command) (string, error)
	run      func(core.Command, io.Writer) error
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
		return r.run(c, out)
	}
	_, err := io.WriteString(out, "fixture completed\n")
	return err
}
func (r *recorder) recorded() []core.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.Command{}, r.commands...)
}

func fixture(t *testing.T) (*Backend, *recorder) {
	t.Helper()
	cfg, err := config.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = t.TempDir()
	cfg.VMRoot = filepath.Join(cfg.StateDir, "vm root")
	cfg.LXCPath = "/srv/lxc"
	cfg.LXDRemote = "lab"
	cfg.LXDProject = "workloads"
	cfg.LibvirtURI = "qemu:///session"
	r := &recorder{capture: func(c core.Command) (string, error) {
		switch c.Program {
		case "lxc":
			return lxdJSON, nil
		case "lxc-ls":
			return "NAME STATE\ndemo STOPPED\n", nil
		case "lxc-info":
			return "Name: demo\nState: STOPPED\nIP: 10.2.3.4\nIP: 2001:db8::1\n", nil
		case "virsh":
			if slices.Contains(c.Args, "dominfo") {
				return kvmInfo, nil
			}
			return kvmStats, nil
		}
		return "", errors.New("unexpected fixture command")
	}}
	b := New(cfg, r)
	b.programs = map[string]string{}
	for _, name := range []string{"lxc", "lxc-ls", "lxc-info", "lxc-start", "lxc-stop", "virsh"} {
		b.programs[name] = name
	}
	return b, r
}

func TestInventoryAndDetailsUseIndependentContextsAndWhitelist(t *testing.T) {
	b, r := fixture(t)
	inv, err := b.List(context.Background(), "")
	if err != nil || len(inv.Backends) != 3 || len(inv.Machines) != 4 {
		t.Fatalf("%+v %v", inv, err)
	}
	commands := r.recorded()
	want := [][]string{{"--project", "workloads", "list", "lab:", "--format", "json"}, {"--lxcpath", "/srv/lxc", "--fancy", "--fancy-format", "NAME,STATE"}, {"--connect", "qemu:///session", "domstats", "--raw", "--state", "--balloon", "--vcpu"}}
	for i, c := range commands {
		if !reflect.DeepEqual(c.Args, want[i]) || c.Timeout != 4*time.Second {
			t.Fatalf("command %+v", c)
		}
	}
	for _, backend := range []string{"lxd", "lxc", "kvm"} {
		m, err := b.Details(context.Background(), backend, "demo")
		if err != nil || m.Name != "demo" {
			t.Fatalf("%+v %v", m, err)
		}
		if backend == "kvm" && (m.UUID != uuid || m.Autostart == nil || *m.Autostart || m.MemoryBytes == nil || *m.MemoryBytes != 2048*1024) {
			t.Fatalf("%+v", m)
		}
		if backend == "lxc" && len(m.IPs) != 2 {
			t.Fatalf("%+v", m)
		}
	}
	data, _ := json.Marshal(inv)
	if strings.Contains(string(data), "PRIVATE") || strings.Contains(string(data), "expanded_config") {
		t.Fatal("private guest configuration leaked")
	}
	if !reflect.DeepEqual(inv.Machines[0].IPs, []string{"10.1.2.3"}) {
		t.Fatalf("%+v", inv.Machines[0])
	}
}

func TestLifecycleCommandsAndDestructiveGuards(t *testing.T) {
	for _, tc := range []struct {
		backend, action string
		args            []string
	}{
		{"lxd", "start", []string{"--project", "workloads", "start", "lab:demo"}},
		{"lxd", "stop", []string{"--project", "workloads", "stop", "lab:demo", "--timeout", "60"}},
		{"lxd", "restart", []string{"--project", "workloads", "restart", "lab:demo", "--timeout", "60"}},
		{"lxd", "delete", []string{"--project", "workloads", "delete", "lab:demo"}},
		{"lxc", "start", []string{"--lxcpath", "/srv/lxc", "--name", "demo", "--daemon"}},
		{"lxc", "stop", []string{"--lxcpath", "/srv/lxc", "--name", "demo", "--nokill", "--timeout", "60"}},
		{"kvm", "start", []string{"--connect", "qemu:///session", "start", "--domain", uuid}},
		{"kvm", "shutdown", []string{"--connect", "qemu:///session", "shutdown", "--domain", uuid}},
		{"kvm", "reboot", []string{"--connect", "qemu:///session", "reboot", "--domain", uuid}},
		{"kvm", "undefine", []string{"--connect", "qemu:///session", "undefine", "--domain", uuid, "--keep-nvram"}},
	} {
		t.Run(tc.backend+"/"+tc.action, func(t *testing.T) {
			b, r := fixture(t)
			if _, err := b.Action(context.Background(), tc.backend, "demo", tc.action, io.Discard); err != nil {
				t.Fatal(err)
			}
			commands := r.recorded()
			last := commands[len(commands)-1]
			if !reflect.DeepEqual(last.Args, tc.args) {
				t.Fatalf("%v != %v", last.Args, tc.args)
			}
			for _, c := range commands {
				for _, bad := range []string{"--force", "--remove-all-storage", "--wipe-storage", "--nvram"} {
					if slices.Contains(c.Args, bad) {
						t.Fatalf("unexpected %s", bad)
					}
				}
			}
		})
	}
	for _, backend := range []string{"lxd", "kvm"} {
		b, r := fixture(t)
		r.capture = func(c core.Command) (string, error) {
			if backend == "lxd" {
				return strings.ReplaceAll(lxdJSON, "Stopped", "Running"), nil
			}
			return strings.ReplaceAll(kvmInfo, "shut off", "running"), nil
		}
		action := "delete"
		if backend == "kvm" {
			action = "undefine"
		}
		if _, err := b.Action(context.Background(), backend, "demo", action, io.Discard); err == nil {
			t.Fatal("active machine removed")
		}
		if len(r.recorded()) != 1 {
			t.Fatal("removal command executed")
		}
	}
}

func TestLaunchAndAutostart(t *testing.T) {
	for _, kind := range []string{"container", "virtual-machine"} {
		b, r := fixture(t)
		if _, err := b.Launch(context.Background(), "demo", "ubuntu:24.04", kind, io.Discard); err != nil {
			t.Fatal(err)
		}
		want := []string{"--project", "workloads", "launch", "ubuntu:24.04", "lab:demo"}
		if kind == "virtual-machine" {
			want = append(want, "--vm")
		}
		if !reflect.DeepEqual(r.recorded()[0].Args, want) {
			t.Fatal(r.recorded())
		}
	}
	for _, enabled := range []bool{true, false} {
		b, r := fixture(t)
		if _, err := b.Autostart(context.Background(), "demo", enabled, io.Discard); err != nil {
			t.Fatal(err)
		}
		want := []string{"--connect", "qemu:///session", "autostart", "--domain", uuid}
		if !enabled {
			want = append(want, "--disable")
		}
		commands := r.recorded()
		if !reflect.DeepEqual(commands[len(commands)-1].Args, want) {
			t.Fatal(commands)
		}
	}
}

func TestInvalidNamesAndUnavailableBackendsDoNotExecute(t *testing.T) {
	b, r := fixture(t)
	for _, name := range []string{"../escape", "--force", "remote:demo", "demo;quit", "demo\nquit", ""} {
		for _, backend := range []string{"lxd", "lxc", "kvm"} {
			if _, err := b.Action(context.Background(), backend, name, "start", io.Discard); err == nil {
				t.Fatal("invalid name accepted")
			}
			if _, err := b.Details(context.Background(), backend, name); err == nil {
				t.Fatal("invalid detail name accepted")
			}
		}
	}
	for _, image := range []string{"--ephemeral", "https://example/image", "file:///tmp/image", "../../image", "ubuntu:24.04 --config=x"} {
		if _, err := b.Launch(context.Background(), "demo", image, "container", io.Discard); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	if len(r.recorded()) != 0 {
		t.Fatal("invalid command executed")
	}
	b.programs = map[string]string{"lxc": "lxc"}
	inv, err := b.List(context.Background(), "lxc")
	if err != nil || len(inv.Backends) != 1 || inv.Backends[0].Available || len(inv.Machines) != 0 {
		t.Fatalf("%+v %v", inv, err)
	}
	if _, err := b.Action(context.Background(), "lxc", "demo", "start", io.Discard); err == nil {
		t.Fatal("missing classic tools accepted")
	}
	if len(r.recorded()) != 0 {
		t.Fatal("missing backend executed")
	}
	if _, err := b.List(context.Background(), "docker"); err == nil {
		t.Fatal("unsupported backend accepted")
	}
}

func TestRuntimeAndParserFailuresAreExplicit(t *testing.T) {
	b, r := fixture(t)
	r.capture = func(core.Command) (string, error) { return "", errors.New("PRIVATE runtime failure") }
	inv, err := b.List(context.Background(), "")
	if err != nil || len(inv.Backends) != 3 || len(inv.Machines) != 0 {
		t.Fatal(inv, err)
	}
	for _, c := range inv.Backends {
		if c.Notice == "" || strings.Contains(c.Notice, "PRIVATE") {
			t.Fatal(c)
		}
	}
	if _, err := b.Details(context.Background(), "lxd", "demo"); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal(err)
	}
	for _, parse := range []func(string) ([]Machine, error){parseLXD, parseLXC, parseKVM} {
		if _, err := parse("unsupported output"); err == nil {
			t.Fatal("invalid output accepted")
		}
	}
	if _, err := parseLXD("null"); err == nil {
		t.Fatal("null accepted")
	}
	if _, err := parseLXD(`[{"name":"demo"}]`); err == nil {
		t.Fatal("missing state accepted")
	}
	for _, parse := range []func(string) ([]Machine, error){parseLXC, parseKVM} {
		items, err := parse("")
		if err != nil || len(items) != 0 {
			t.Fatal(items, err)
		}
	}
	items, err := parseKVM(kvmStats)
	if err != nil || items[0].State != "shut off" || items[1].Manageable || *items[0].CPUs != 2 {
		t.Fatal(items, err)
	}
	r.capture = func(core.Command) (string, error) {
		return strings.ReplaceAll(kvmInfo, "Name: demo", "Name: other"), nil
	}
	if _, err := b.Action(context.Background(), "kvm", "42", "start", io.Discard); err == nil {
		t.Fatal("numeric ID resolved a different domain")
	}
}

func TestDefineUsesValidatedSnapshotAndPreservesSource(t *testing.T) {
	b, r := fixture(t)
	if err := os.MkdirAll(b.cfg.VMRoot, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(b.cfg.VMRoot, "existing domain.xml")
	data := []byte(`<domain type="kvm"><name>demo</name><memory unit="MiB">512</memory><vcpu>1</vcpu></domain>`)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	r.run = func(c core.Command, out io.Writer) error {
		if c.Args[2] != "define" || c.Args[4] != "--validate" {
			t.Fatal(c)
		}
		snapshot = c.Args[3]
		if snapshot == file {
			t.Fatal("mutable source passed to libvirt")
		}
		got, err := os.ReadFile(snapshot)
		if err != nil || string(got) != string(data) {
			t.Fatal(string(got), err)
		}
		info, err := os.Stat(snapshot)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(info, err)
		}
		return nil
	}
	if _, err := b.Define(context.Background(), file, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatal("temporary XML retained")
	}
	got, _ := os.ReadFile(file)
	if string(got) != string(data) {
		t.Fatal("source changed")
	}
	if len(r.recorded()) != 1 {
		t.Fatal(r.recorded())
	}
}

func TestDefineRejectsEscapesAndInvalidXML(t *testing.T) {
	b, r := fixture(t)
	if err := os.MkdirAll(b.cfg.VMRoot, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(b.cfg.VMRoot, "domain.xml")
	for _, data := range []string{"", `<domain type="qemu"><name>demo</name></domain>`, `<domain type="kvm"><name>--bad</name></domain>`, `<!DOCTYPE domain><domain type="kvm"><name>demo</name></domain>`, `<domain type="kvm"><name>demo</name></domain><domain/>`, strings.Repeat("x", (1<<20)+1)} {
		if err := os.WriteFile(file, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Define(context.Background(), file, io.Discard); err == nil {
			t.Fatal("invalid XML accepted")
		}
	}
	outside := filepath.Join(t.TempDir(), "external.xml")
	if err := os.WriteFile(outside, []byte(`<domain type="kvm"><name>demo</name></domain>`), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(b.cfg.VMRoot, "link.xml")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"domain.xml", outside, link, b.cfg.VMRoot} {
		if _, err := b.Define(context.Background(), path, io.Discard); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
	if len(r.recorded()) != 0 {
		t.Fatal("invalid XML executed")
	}
	pipe := filepath.Join(b.cfg.VMRoot, "pipe.xml")
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Define(context.Background(), pipe, io.Discard); err == nil {
		t.Fatal("FIFO XML accepted")
	}
}

func TestMissingActionUtilitiesAndExecutionFailures(t *testing.T) {
	b, r := fixture(t)
	delete(b.programs, "lxc-stop")
	capabilities := b.capabilities()
	if !capabilities[1].Available || !reflect.DeepEqual(capabilities[1].Actions, []string{"start"}) {
		t.Fatal(capabilities[1])
	}
	if _, err := b.Action(context.Background(), "lxc", "demo", "stop", io.Discard); err == nil {
		t.Fatal("missing stop executable accepted")
	}
	if len(r.recorded()) != 0 {
		t.Fatal("missing utility executed")
	}
	r.run = func(core.Command, io.Writer) error { return errors.New("fixture failure") }
	for _, backend := range []string{"lxd", "lxc", "kvm"} {
		if _, err := b.Action(context.Background(), backend, "demo", "start", io.Discard); err == nil {
			t.Fatal("command failure hidden")
		}
	}
	items, err := parseLXD(`[{"name":"demo","status":"Stopped","state":{"network":{}}}]`)
	if err != nil || items[0].MemoryBytes != nil {
		t.Fatal("missing memory was fabricated", items, err)
	}
	if _, err := parseLXC(strings.Repeat("demo RUNNING\n", 1025)); err == nil {
		t.Fatal("unbounded inventory")
	}
}

func TestRegistryPolicyAndJob(t *testing.T) {
	b, r := fixture(t)
	jobs, err := core.NewJobs(context.Background(), b.cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	registry := core.NewRegistry(true, jobs)
	if err := b.Register(registry); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Invoke(context.Background(), "lxd_delete", json.RawMessage(`{"name":"demo"}`), "test"); err == nil {
		t.Fatal("confirmation bypassed")
	}
	if len(r.recorded()) != 0 {
		t.Fatal("unconfirmed command executed")
	}
	result, err := registry.Invoke(context.Background(), "lxd_stop", json.RawMessage(`{"name":"demo","confirm":true}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	job := result.(map[string]any)["job"].(core.Job)
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, err = jobs.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if job.State != "succeeded" || !strings.Contains(job.Output, "fixture completed") {
		t.Fatal(job)
	}
	denied := core.NewRegistry(false, jobs)
	if err := b.Register(denied); err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Invoke(context.Background(), "lxd_launch", json.RawMessage(`{"name":"demo","image":"ubuntu:24.04","kind":"container"}`), "test"); err == nil {
		t.Fatal("write policy bypassed")
	}
}
