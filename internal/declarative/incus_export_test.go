package declarative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"server-man/internal/core"
)

type incusExportRecorder struct {
	extraInstances map[string]incusInstanceInfo
	*incusBackupRecorder
	payload     []byte
	lastPayload []byte
	streams     []core.Command
	help        []core.Command
	fail        bool
	legacy      bool
	after       func()
}

func (r *incusExportRecorder) Capture(ctx context.Context, c core.Command) (string, error) {
	if slices.Contains(c.Args, "--help") {
		r.help = append(r.help, c)
		if r.legacy {
			return "legacy export --compression", nil
		}
		return "export --compression --root-only", nil
	}

	if len(r.extraInstances) > 0 {
		method, path := incusRequest(c)
		u, _ := url.Parse(path)
		var metadata any
		if method == "GET" && u.Path == "/1.0/instances" {
			paths := []string{incusPath("guest") + "?project=managed"}
			for id := range r.extraInstances {
				paths = append(paths, incusPath(id)+"?project=managed")
			}
			metadata = paths
		} else if method == "GET" {
			for id, actual := range r.extraInstances {
				if u.Path == incusPath(id) {
					metadata = actual
				}
			}
		}
		if metadata != nil {
			r.commands = append(r.commands, c)
			raw, _ := json.Marshal(map[string]any{"type": "sync", "error_code": 0, "metadata": metadata})
			return string(raw), nil
		}
	}
	return r.incusBackupRecorder.Capture(ctx, c)
}
func (r *incusExportRecorder) Stream(ctx context.Context, c core.Command, stdout, stderr io.Writer) error {
	r.streams = append(r.streams, c)
	stderr.Write([]byte("PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL"))
	r.lastPayload = r.payload
	if len(r.payload) > 0 {
		at := slices.Index(c.Args, "export")
		object := NativeBackupObject{Backend: "incus", Project: c.Args[2]}
		kind, id, uuid := "incus_custom_volume", "", ""
		if slices.Contains(c.Args, "volume") {
			object.Pool, object.Name, object.ContentType = strings.TrimPrefix(c.Args[at+1], "local:"), c.Args[at+2], r.volumeContentType
			if object.ContentType == "" {
				object.ContentType = "filesystem"
			}
		} else {
			kind = "incus_instance_export"
			object.Name = strings.TrimPrefix(c.Args[at+1], "local:")
			actual := r.actual
			for _, candidate := range r.extraInstances {
				if candidate.Name == object.Name {
					actual = candidate
				}
			}
			object.Pool, object.ContentType = actual.Devices["root"]["pool"], "block"
			id, uuid = actual.Config["user.server-man.id"], actual.Config["user.server-man.identity"]
		}
		var err error
		r.lastPayload, err = fixtureIncusArchive(object, kind, id, uuid, r.payload)
		if err != nil {
			return err
		}
	}
	for offset := 0; offset < len(r.lastPayload); offset += 65536 {
		end := min(offset+65536, len(r.lastPayload))
		if _, err := stdout.Write(r.lastPayload[offset:end]); err != nil {
			return err
		}
		if r.fail {
			return errors.New("PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL")
		}
	}
	if r.after != nil {
		r.after()
	}
	return nil
}
func incusExportFixture(t *testing.T) (*Incus, Config, *incusExportRecorder, BackupInput, BackupInput) {
	t.Helper()
	i, c, r := nativeIncusBackupFixture(t)
	export := &incusExportRecorder{incusBackupRecorder: r, payload: bytes.Repeat([]byte{0, 1, 2, 255}, 1<<20)}
	i.Runner = export
	discovered, err := i.BackupDependencies(context.Background(), c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	instance := BackupInput{ID: "native/guest", Kind: "incus_instance_export", Strategy: "native_export", Required: true, Resources: []string{"service/guest"}, Native: discovered.Object}
	var volume BackupInput
	for _, input := range discovered.Inputs {
		if input.Kind == "incus_custom_volume" {
			volume = input
		}
	}
	if volume.Native == nil {
		t.Fatal("missing volume fixture")
	}
	return i, c, export, instance, volume
}
func privateExportDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIncusExportsPortableInstanceAndCompleteVolumeToPrivateStreams(t *testing.T) {
	i, c, r, instance, volume := incusExportFixture(t)
	directory := privateExportDirectory(t)
	for _, input := range []BackupInput{instance, volume} {
		exp, err := i.BackupExport(context.Background(), c, input, directory)
		if err != nil {
			t.Fatal(err)
		}
		if exp.Native == nil || *exp.Native != *input.Native || exp.Kind != input.Kind || exp.SizeBytes != int64(len(r.lastPayload)) || exp.SHA256 != digest(r.lastPayload) {
			t.Fatal("incorrect export record", exp)
		}
		if err := verifyBackupExport(context.Background(), directory, exp); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(directory, exp.BundlePath))
		if err != nil || !bytes.Equal(got, r.lastPayload) {
			t.Fatal("binary archive changed", err)
		}
		raw, _ := json.Marshal(exp)
		if bytes.Contains(raw, []byte("PRIVATE_NATIVE")) {
			t.Fatal("archive or diagnostics leaked into result")
		}
		if _, err := i.BackupExport(context.Background(), c, input, directory); err == nil {
			t.Fatal("existing artifact overwritten")
		}
	}
	if len(r.streams) != 2 {
		t.Fatal("unexpected stream count", len(r.streams))
	}
	for index, cmd := range r.streams {
		expectedProject := []string{instance.Native.Project, volume.Native.Project}[index]
		if cmd.Timeout <= 0 || !slices.Equal(cmd.Args[:4], []string{"--force-local", "--project", expectedProject, "--quiet"}) {
			t.Fatal("incorrect explicit scope", cmd.Args)
		}
		if !slices.Contains(cmd.Args, "-") || !slices.Contains(cmd.Args, "none") || slices.Contains(cmd.Args, "--optimized-storage") || slices.Contains(cmd.Args, "--instance-only") || slices.Contains(cmd.Args, "--volume-only") {
			t.Fatal("nonportable or incomplete export flags", cmd.Args)
		}
	}
	if !slices.Contains(r.streams[0].Args, "--root-only") || !slices.Contains(r.streams[0].Args, "local:sm-guest") {
		t.Fatal(r.streams[0].Args)
	}
	if !slices.Contains(r.streams[1].Args, "local:default") {
		t.Fatal(r.streams[1].Args)
	}
}

func TestIncusExportFailuresNeverProduceACompletedArtifact(t *testing.T) {
	for _, scenario := range []string{"running", "unmanaged writer", "clustered", "ownership", "missing flags", "stream failure", "empty stream", "restarted during export"} {
		t.Run(scenario, func(t *testing.T) {
			i, c, r, instance, volume := incusExportFixture(t)
			input := instance
			switch scenario {
			case "running":
				r.actual.StatusCode = 103
			case "unmanaged writer":
				r.volumeUsers = append(r.volumeUsers, "/1.0/instances/foreign?project=managed")
				input = volume
			case "clustered":
				r.clustered = true
			case "ownership":
				r.actual.Config["user.server-man.id"] = "foreign"
			case "missing flags":
				r.legacy = true
			case "stream failure":
				r.fail = true
			case "empty stream":
				r.payload = nil
			case "restarted during export":
				r.after = func() { r.actual.StatusCode = 103 }
			}
			directory := privateExportDirectory(t)
			_, err := i.BackupExport(context.Background(), c, input, directory)
			if err == nil {
				t.Fatal("unsafe export accepted")
			}
			if strings.Contains(err.Error(), "PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL") {
				t.Fatal("private diagnostics leaked")
			}
			files, _ := filepath.Glob(filepath.Join(directory, "native", "incus", "*"))
			if len(files) != 0 {
				t.Fatal("failed artifact remains", files)
			}
			if scenario != "stream failure" && scenario != "empty stream" && scenario != "restarted during export" && len(r.streams) != 0 {
				t.Fatal("export started before prerequisites were established")
			}
		})
	}
}

func TestBackupStreamBudgetAndCancellationBoundWrites(t *testing.T) {
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	writer := &backupStreamWriter{ctx: ctx, file: &output, hash: sha256.New(), remaining: 4}
	if n, err := writer.Write([]byte("four")); n != 4 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := writer.Write([]byte("overflow")); n != 0 || err == nil {
		t.Fatal("capacity budget bypassed", n, err)
	}
	cancel()
	if n, err := writer.Write([]byte("x")); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", n, err)
	}
	if output.String() != "four" || writer.size != 4 {
		t.Fatal("failed write changed stream")
	}
}

func TestIncusCaptureCoordinatorVerifiesNativeCoverage(t *testing.T) {
	i, c, r, _, _ := incusExportFixture(t)
	snapshot, err := i.Store.Save(c, "missing")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{Store: i.Store, Adapters: map[string]Adapter{"incus": i}}
	destination := filepath.Join(t.TempDir(), "bundle")
	summary, err := coordinator.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: destination}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Stage != "captured" || len(r.streams) != 2 {
		t.Fatal(summary, len(r.streams))
	}
	raw, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.NativeExports) != 2 {
		t.Fatal("required export omitted", manifest.NativeExports)
	}
	for _, exp := range manifest.NativeExports {
		if !slices.Equal(exp.Resources, []string{"service/guest"}) {
			t.Fatal("consumer coverage omitted", exp)
		}
	}
}

func sharedIncusExportFixture(t *testing.T) (*Incus, Config, *incusExportRecorder) {
	t.Helper()
	i, c, r, _, _ := incusExportFixture(t)
	service := c.Services["guest"]
	vm := *service.VM
	vm.UUID = ""
	vm.MAC = ""
	service.VM = &vm
	c.Services["other"] = service
	var err error
	c, err = PrepareIdentities(c, Config{})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := RenderIncus(c, "service/other")
	if err != nil {
		t.Fatal(err)
	}
	desired.Devices["data"] = map[string]string{"type": "disk", "path": "/data", "pool": "default", "source": "data"}
	r.extraInstances = map[string]incusInstanceInfo{"other": {incusDefinition: desired, Name: incusInstance("other"), Type: "virtual-machine", StatusCode: 102}}
	r.volumeUsers = []string{incusPath("guest") + "?project=managed", incusPath("other") + "?project=managed"}
	return i, c, r
}
func TestIncusCaptureExportsSharedVolumeOnceWithEveryConsumer(t *testing.T) {
	i, c, r := sharedIncusExportFixture(t)
	snapshot, err := i.Store.Save(c, "missing")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{Store: i.Store, Adapters: map[string]Adapter{"incus": i}}
	dest := filepath.Join(t.TempDir(), "bundle")
	if _, err := coordinator.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: dest}, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	volumes := 0
	for _, export := range manifest.NativeExports {
		if export.Kind == "incus_custom_volume" {
			volumes++
			if !slices.Equal(export.Resources, []string{"service/guest", "service/other"}) {
				t.Fatal("shared consumers omitted", export)
			}
		}
	}
	if volumes != 1 || len(r.streams) != 3 {
		t.Fatal("shared volume duplicated or omitted", volumes, len(r.streams))
	}
}
func TestIncusSharedVolumeExportRequiresEveryConsumerStopped(t *testing.T) {
	i, c, r := sharedIncusExportFixture(t)
	discovered, err := i.BackupDependencies(context.Background(), c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	var volume BackupInput
	for _, input := range discovered.Inputs {
		if input.Kind == "incus_custom_volume" {
			volume = input
		}
	}
	other := r.extraInstances["other"]
	other.StatusCode = 103
	r.extraInstances["other"] = other
	if _, err := i.BackupExport(context.Background(), c, volume, privateExportDirectory(t)); err == nil {
		t.Fatal("exported volume with a running shared consumer")
	}
	if len(r.streams) != 0 {
		t.Fatal("streamed before quiescing every writer")
	}
}

type mismatchedIncusExporter struct{ *Incus }

func (i mismatchedIncusExporter) BackupExport(ctx context.Context, c Config, input BackupInput, directory string) (BackupExport, error) {
	export, err := i.Incus.BackupExport(ctx, c, input, directory)
	export.Kind = "different_object_kind"
	return export, err
}
func TestCoordinatorRejectsMismatchedNativeExportCoverage(t *testing.T) {
	i, c, _, _, _ := incusExportFixture(t)
	snapshot, err := i.Store.Save(c, "missing")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{Store: i.Store, Adapters: map[string]Adapter{"incus": mismatchedIncusExporter{i}}}
	dest := filepath.Join(t.TempDir(), "bundle")
	if _, err := coordinator.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: dest}, io.Discard); err == nil {
		t.Fatal("wrong native object coverage accepted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("incomplete bundle published", err)
	}
}

func TestIncusExportCapabilityFailurePrecedesQuiescence(t *testing.T) {
	i, c, r, _, _ := incusExportFixture(t)
	r.legacy = true
	r.actual.StatusCode = 103
	snapshot, err := i.Store.Save(c, "missing")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{Store: i.Store, Adapters: map[string]Adapter{"incus": i}}
	if _, err := coordinator.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: filepath.Join(t.TempDir(), "bundle"), Quiesce: true}, io.Discard); err == nil || !strings.Contains(err.Error(), "--root-only") {
		t.Fatal("export capability was not checked before quiescence", err)
	}
	if len(r.streams) != 0 || len(r.payloads) != 0 {
		t.Fatal("changed native state before export preflight passed")
	}
	if status, err := coordinator.ReadBackupRecovery(); err != nil || status.Journal != nil {
		t.Fatal("quiescence started before capability validation", status, err)
	}
}

func TestIncusExportPreservesBlockAndISOContentTypes(t *testing.T) {
	for _, contentType := range []string{"block", "iso"} {
		t.Run(contentType, func(t *testing.T) {
			i, c, r, _, _ := incusExportFixture(t)
			r.volumeContentType = contentType
			deps, err := i.BackupDependencies(context.Background(), c, "service/guest")
			if err != nil {
				t.Fatal(err)
			}
			var input BackupInput
			for _, dependency := range deps.Inputs {
				if dependency.Kind == "incus_custom_volume" {
					input = dependency
				}
			}
			exp, err := i.BackupExport(context.Background(), c, input, privateExportDirectory(t))
			if err != nil {
				t.Fatal(err)
			}
			extension := ".tar"
			if contentType == "iso" {
				extension = ".iso"
			}
			if exp.Native.ContentType != contentType || !strings.HasSuffix(exp.BundlePath, extension) {
				t.Fatal("native content identity lost", exp)
			}
		})
	}
}

func TestIncusSameNamedVolumesRemainDistinctAcrossStorageProjects(t *testing.T) {
	i, c, r, _, defaultVolume := incusExportFixture(t)
	directory := privateExportDirectory(t)
	first, err := i.BackupExport(context.Background(), c, defaultVolume, directory)
	if err != nil {
		t.Fatal(err)
	}
	r.projectVolumes = true
	discovered, err := i.BackupDependencies(context.Background(), c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	var managedVolume BackupInput
	for _, input := range discovered.Inputs {
		if input.Kind == "incus_custom_volume" {
			managedVolume = input
		}
	}
	second, err := i.BackupExport(context.Background(), c, managedVolume, directory)
	if err != nil {
		t.Fatal(err)
	}
	if first.BundlePath == second.BundlePath || first.Native.Project != "default" || second.Native.Project != "managed" {
		t.Fatal("cross-project volume collision", first, second)
	}
	if r.streams[0].Args[2] != "default" || r.streams[1].Args[2] != "managed" {
		t.Fatal("ambient volume project used")
	}
}

func TestIncusExportRejectsUnsafeStagingBeforeStreaming(t *testing.T) {
	for _, scenario := range []string{"public directory", "symlink root", "symlink parent", "no streaming executor"} {
		t.Run(scenario, func(t *testing.T) {
			i, c, r, input, _ := incusExportFixture(t)
			directory := privateExportDirectory(t)
			switch scenario {
			case "public directory":
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink root":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(directory, alias); err != nil {
					t.Fatal(err)
				}
				directory = alias
			case "symlink parent":
				if err := os.Symlink(privateExportDirectory(t), filepath.Join(directory, "native")); err != nil {
					t.Fatal(err)
				}
			case "no streaming executor":
				i.Runner = r.incusBackupRecorder
			}
			if _, err := i.BackupExport(context.Background(), c, input, directory); err == nil {
				t.Fatal("unsafe staging accepted")
			}
			if len(r.streams) != 0 {
				t.Fatal("streamed into unsafe staging")
			}
		})
	}
}
