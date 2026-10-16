package compose

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
	"testing"
	"time"

	"serverman/internal/core"
)

type recorder struct {
	mu       sync.Mutex
	commands []core.Command
	capture  func(core.Command) (string, error)
	run      func(core.Command, io.Writer) error
}

func (r *recorder) Capture(ctx context.Context, command core.Command) (string, error) {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if r.capture != nil {
		return r.capture(command)
	}
	return "[]", nil
}
func (r *recorder) Run(ctx context.Context, command core.Command, out io.Writer) error {
	r.mu.Lock()
	r.commands = append(r.commands, command)
	r.mu.Unlock()
	if r.run != nil {
		return r.run(command, out)
	}
	_, _ = io.WriteString(out, "fixture operation")
	return nil
}
func (r *recorder) recorded() []core.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]core.Command{}, r.commands...)
}

func fixture(t *testing.T) (*Backend, *recorder) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vm root")
	for _, engine := range []string{"podman", "docker"} {
		put(t, filepath.Join(root, "config", engine, "demo", "compose.yml"), "services:\n  web:\n    image: example/web\n")
	}
	for _, name := range []string{"data_root.env", "global.env", "demo/90-app.env", "demo/10-base.env", "demo/.hidden.env"} {
		put(t, filepath.Join(root, "data", name), "PRIVATE_VALUE="+t.Name()+"\n")
	}
	runner := &recorder{}
	b := New(root, nil, runner)
	b.programs = map[string]string{"podman": "podman", "docker": "docker"}
	return b, runner
}

func put(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryPreservesLayoutWithoutReadingPrivateValues(t *testing.T) {
	b, runner := fixture(t)
	put(t, filepath.Join(b.vmRoot, "config/podman/demo/compose.yaml"), "services: {}\n")
	put(t, filepath.Join(b.vmRoot, "config/podman/no-compose/readme"), "")
	put(t, filepath.Join(b.vmRoot, "data/demo/.disabled"), "")
	inventory, err := b.Discover("")
	if err != nil || len(inventory.Projects) != 2 || len(runner.recorded()) != 0 {
		t.Fatalf("inventory=%+v error=%v", inventory, err)
	}
	for _, project := range inventory.Projects {
		if !project.Disabled {
			t.Fatal("disabled marker ignored")
		}
		want := []string{filepath.Join(b.vmRoot, "data/data_root.env"), filepath.Join(b.vmRoot, "data/global.env"), filepath.Join(b.vmRoot, "data/demo/10-base.env"), filepath.Join(b.vmRoot, "data/demo/90-app.env")}
		if !reflect.DeepEqual(project.EnvFiles, want) {
			t.Fatalf("environment order=%v", project.EnvFiles)
		}
		if project.Engine == "podman" && filepath.Base(project.File) != "compose.yaml" {
			t.Fatal("Compose filename precedence changed")
		}
	}
	data, _ := json.Marshal(inventory)
	if strings.Contains(string(data), "PRIVATE_VALUE") || strings.Contains(string(data), "example/web") {
		t.Fatal("discovery exposed configuration values")
	}
}

func TestCommandsPreserveProjectAndEnvironmentContext(t *testing.T) {
	b, runner := fixture(t)
	for _, engine := range []string{"podman", "docker"} {
		for _, action := range []string{"up", "start", "stop", "restart", "pull", "down"} {
			if _, err := b.Action(context.Background(), engine, "demo", action, io.Discard); err != nil {
				t.Fatal(err)
			}
			commands := runner.recorded()
			command := commands[len(commands)-1]
			project, _ := b.resolve(engine, "demo")
			want := []string{"compose", "--project-directory", project.Directory, "-f", project.File}
			for _, file := range project.EnvFiles {
				want = append(want, "--env-file", file)
			}
			want = append(want, action)
			if action == "up" {
				want = append(want, "-d")
			}
			if command.Program != engine || command.Dir != project.Directory || !reflect.DeepEqual(command.Args, want) {
				t.Fatalf("%s %s: %+v", engine, action, command)
			}
			if slices.Contains(command.Args, "-v") || slices.Contains(command.Args, "--volumes") || slices.Contains(command.Args, "--remove-orphans") {
				t.Fatal("unexpected destructive Compose option")
			}
		}
	}
}

func TestInventoryReadsEachRuntimeOnceAndMatchesDirectoryLabels(t *testing.T) {
	b, runner := fixture(t)
	runner.capture = func(command core.Command) (string, error) {
		if !reflect.DeepEqual(command.Args, []string{"ps", "--all", "--format", "json"}) {
			t.Fatalf("unexpected inventory command: %+v", command)
		}
		row := map[string]any{"Id": "container-" + command.Program, "Names": "web", "Image": "example/web", "State": "running", "Command": "private-command", "Labels": map[string]string{"com.docker.compose.project": "custom-name", "com.docker.compose.project.working_dir": filepath.Join(b.roots[command.Program], "demo"), "com.docker.compose.service": "web"}}
		data, _ := json.Marshal(row)
		return string(data), nil
	}
	result, err := b.Inventory(context.Background(), "")
	if err != nil || len(result.Projects) != 2 || len(runner.recorded()) != 2 {
		t.Fatalf("inventory=%+v error=%v", result, err)
	}
	for _, project := range result.Projects {
		if project.State != "running" || project.Containers != 1 || project.Running != 1 {
			t.Fatal("runtime mapping failed", project)
		}
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "private-command") {
		t.Fatal("native inventory exposed a command")
	}
	b.programs["podman"] = ""
	result, err = b.Inventory(context.Background(), "podman")
	if err != nil || result.Engines[0].Available || result.Engines[0].Reason == "" || len(result.Projects) != 1 {
		t.Fatal("unavailable engine hid filesystem projects")
	}
	missing := New(t.TempDir(), nil, runner)
	result, err = missing.Discover("")
	if err != nil || len(result.Projects) != 0 || result.Engines[0].Notice == "" {
		t.Fatal("missing roots were not reported")
	}
}

func TestDisabledProjectsAndPathEscapesNeverLaunchCommands(t *testing.T) {
	b, runner := fixture(t)
	put(t, filepath.Join(b.vmRoot, "data/demo/.disabled"), "")
	for _, action := range []string{"up", "start", "restart", "pull"} {
		if _, err := b.Action(context.Background(), "podman", "demo", action, io.Discard); err == nil {
			t.Fatalf("disabled %s accepted", action)
		}
	}
	for _, name := range []string{"../demo", "--help", "a/b", ".", ""} {
		if _, err := b.Action(context.Background(), "podman", name, "up", io.Discard); err == nil {
			t.Fatalf("invalid project %q accepted", name)
		}
	}
	if _, err := b.Logs(context.Background(), "podman", "demo", "--follow", 10); err == nil {
		t.Fatal("service argument injection accepted")
	}
	outside := t.TempDir()
	put(t, filepath.Join(outside, "compose.yml"), "services: {}")
	if err := os.Symlink(outside, filepath.Join(b.roots["podman"], "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.resolve("podman", "escape"); err == nil {
		t.Fatal("project symlink escape accepted")
	}
	if err := os.Symlink(filepath.Join(outside, "compose.yml"), filepath.Join(b.roots["docker"], "demo", "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.resolve("docker", "demo"); err == nil {
		t.Fatal("Compose file symlink escape accepted")
	}
	put(t, filepath.Join(outside, "outside.env"), "PRIVATE_VALUE="+t.Name())
	if err := os.Symlink(filepath.Join(outside, "outside.env"), filepath.Join(b.vmRoot, "data/demo/outside.env")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.resolve("podman", "demo"); err == nil {
		t.Fatal("environment symlink escape accepted")
	}
	if len(runner.recorded()) != 0 {
		t.Fatal("invalid request launched a command")
	}
	if err := os.Remove(filepath.Join(b.vmRoot, "data/demo/outside.env")); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"stop", "down"} {
		if _, err := b.Action(context.Background(), "podman", "demo", action, io.Discard); err != nil {
			t.Fatal("disabled project could not be stopped", err)
		}
	}
}

func TestDockerAndPodmanJSONWhitelistingAndProjectMapping(t *testing.T) {
	docker := `{"ID":"c1","Names":"web","Image":"example/web","State":"running","Status":"Up 2 minutes","Labels":"com.docker.compose.project=custom,com.docker.compose.project.working_dir=/srv/config/docker/demo,com.docker.compose.service=web,private=hidden","Command":"private-command"}`
	containers, err := parseContainers(docker + "\n")
	if err != nil || len(containers) != 1 || !matches(containers[0], Project{Name: "demo", Directory: "/srv/config/docker/demo"}) {
		t.Fatalf("Docker JSON: %+v %v", containers, err)
	}
	data, _ := json.Marshal(containers)
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "hidden") {
		t.Fatal("container inventory exposed unselected fields")
	}
	podman := `[{"Id":"c2","Names":["backend"],"Image":"example/backend","State":"running","Pod":"pod1","Labels":{"io.podman.compose.project":"demo","com.docker.compose.service":"backend"},"Ports":[{"host_ip":"::","host_port":8080,"container_port":80,"protocol":"tcp"}]}]`
	containers, err = parseContainers(podman)
	if err != nil || containers[0].PodID != "pod1" || containers[0].Name != "backend" || containers[0].Ports != "[::]:8080 → 80/tcp" {
		t.Fatalf("Podman JSON=%+v %v", containers, err)
	}
	pods, err := parsePods(`[{"Id":"pod1","Name":"demo-pod","Status":"Running","Containers":[{"Id":"c2"}]},{"Id":"other","Name":"unrelated"}]`, containers)
	if err != nil || len(pods) != 1 || pods[0].Containers != 1 {
		t.Fatalf("pods=%+v %v", pods, err)
	}
	if _, err := parseContainers("not JSON"); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestStatusProviderFallbackAndBoundedLogs(t *testing.T) {
	b, runner := fixture(t)
	runner.capture = func(command core.Command) (string, error) {
		if command.Args[0] == "pod" {
			return `[{"Id":"pod1","Name":"demo-pod","Status":"Running","NumContainers":1}]`, nil
		}
		if slices.Contains(command.Args, "--all") {
			return "", errors.New("unrecognized arguments: --all")
		}
		return `[{"Id":"c1","Names":["demo-web"],"State":"running","Pod":"pod1"}]`, nil
	}
	detail, err := b.Status(context.Background(), "podman", "demo")
	if err != nil || detail.Project.State != "running" || len(detail.Pods) != 1 || len(runner.recorded()) != 3 {
		t.Fatalf("details=%+v error=%v", detail, err)
	}
	runner.run = func(command core.Command, out io.Writer) error {
		_, _ = io.WriteString(out, strings.Repeat("x", 140<<10)+"last log")
		if command.Timeout > 15*time.Second || slices.Contains(command.Args, "--follow") {
			t.Error("unbounded log command")
		}
		return nil
	}
	logs, err := b.Logs(context.Background(), "podman", "demo", "web", 20)
	if err != nil || !logs.Truncated || len(logs.Text) != 128<<10 || !strings.HasSuffix(logs.Text, "last log") {
		t.Fatal("log limits failed")
	}
	runner.capture = func(core.Command) (string, error) { return "", errors.New("PRIVATE_VALUE=" + t.Name()) }
	if _, err := b.Status(context.Background(), "docker", "demo"); err == nil || strings.Contains(err.Error(), "PRIVATE_VALUE") {
		t.Fatal("status errors exposed provider values")
	}
}

func TestRegistryUsesAcknowledgementAndTrackedJobs(t *testing.T) {
	b, runner := fixture(t)
	jobs, err := core.NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	registry := core.NewRegistry(true, jobs)
	if err := b.Register(registry); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Invoke(context.Background(), "compose_down", json.RawMessage(`{"engine":"docker","project":"demo"}`), "test"); err == nil {
		t.Fatal("down accepted without acknowledgement")
	}
	if len(runner.recorded()) != 0 {
		t.Fatal("unacknowledged removal launched command")
	}
	result, err := registry.Invoke(context.Background(), "compose_up", json.RawMessage(`{"engine":"docker","project":"demo","confirm":true}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["job"].(core.Job).ID
	deadline := time.After(3 * time.Second)
	for {
		job, err := jobs.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			if job.State != "succeeded" {
				t.Fatal(job.Error)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("job did not finish")
		case <-time.After(time.Millisecond):
		}
	}
	if len(runner.recorded()) != 1 {
		t.Fatal("tracked action did not execute once")
	}
}

func TestExistingVMLayoutDiscoveryReadOnly(t *testing.T) {
	root, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	root = filepath.Join(root, "vms")
	if _, err := os.Stat(filepath.Join(root, "config/podman")); err != nil {
		t.Skip("existing ~/vms layout not present")
	}
	runner := &recorder{}
	b := New(root, nil, runner)
	result, err := b.Discover("podman")
	if err != nil || len(result.Projects) == 0 {
		t.Fatalf("live discovery failed: %v", err)
	}
	if len(runner.recorded()) != 0 {
		t.Fatal("filesystem discovery ran an engine command")
	}
	t.Logf("Discovered %d existing Podman projects using file metadata only", len(result.Projects))
}
