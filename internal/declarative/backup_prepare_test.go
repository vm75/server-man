package declarative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"server-man/internal/core"
)

type backupTestAdapter struct {
	actionHook      func(string, string)
	mu              sync.Mutex
	actions         []string
	activeStates    map[string]bool
	failAction      string
	failInspect     bool
	exportItems     []BackupExport
	failAfterChange bool
	silentStart     bool
}

func (a *backupTestAdapter) Validate(_ context.Context, _ Config, _ string) error {
	return nil
}

func (a *backupTestAdapter) Inspect(_ context.Context, _ Config, key string) (Observation, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failInspect {
		return Observation{}, errors.New("inspect failed")
	}
	isActive := a.activeStates[key]
	return Observation{Registered: true, Active: isActive, Matches: true}, nil
}

func (a *backupTestAdapter) Apply(_ context.Context, _ Config, _ Config, _ string, _ string) error {
	return nil
}

func (a *backupTestAdapter) Action(_ context.Context, _ Config, key, action string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, action+":"+key)
	if a.actionHook != nil {
		a.actionHook(key, action)
	}
	if a.failAction == action+":"+key && !a.failAfterChange {
		return errors.New("action failed: " + action + " on " + key)
	}
	if action == "stop" {
		a.activeStates[key] = false
	} else if action == "start" && !a.silentStart {
		a.activeStates[key] = true
	}
	if a.failAction == action+":"+key {
		return errors.New("action failed after state change")
	}
	return nil
}

func (a *backupTestAdapter) BackupExport(_ context.Context, _ Config, key string, bundleDir string) ([]BackupExport, error) {
	return a.exportItems, nil
}

func setupBackupFixture(t *testing.T) (Coordinator, *backupTestAdapter, string) {
	t.Helper()
	root := t.TempDir()
	storePath := filepath.Join(root, "server-man.yaml")

	dataDir := filepath.Join(root, "data", "documents")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	file1 := filepath.Join(dataDir, "notes.txt")
	if err := os.WriteFile(file1, []byte("hello notes"), 0644); err != nil {
		t.Fatal(err)
	}

	secretDir := filepath.Join(root, "secrets")
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(secretDir, "token.env")
	if err := os.WriteFile(secretFile, []byte("SECRET=12345"), 0600); err != nil {
		t.Fatal(err)
	}

	rawYAML := `version: 1
storage:
  documents:
    path: data/documents
secrets:
  token: secrets/token.env
services:
  db:
    kind: container
    backend: quadlet
    engine: podman
    enabled: true
    containers:
      main:
        image: docker.io/library/postgres:16
        mounts:
          - storage: documents
            target: /var/lib/postgresql/data
  app:
    kind: container
    backend: quadlet
    engine: podman
    enabled: true
    depends_on: [db]
    containers:
      main:
        image: docker.io/library/node:20
        env_files: [token]
`
	if err := os.WriteFile(storePath, []byte(rawYAML), 0600); err != nil {
		t.Fatal(err)
	}

	adapter := &backupTestAdapter{
		activeStates: map[string]bool{
			"service/db":  true,
			"service/app": true,
		},
	}
	store := Store{Path: storePath}
	if _, err := store.Read(); err != nil {
		t.Fatalf("setup fixture store.Read: %v", err)
	}
	coordinator := Coordinator{
		Store: store,
		Adapters: map[string]Adapter{
			"podman/quadlet": adapter,
		},
	}
	return coordinator, adapter, root
}

func TestPrepareBackupSuccess(t *testing.T) {
	coord, adapter, root := setupBackupFixture(t)
	backupDest := filepath.Join(t.TempDir(), "my-backup-bundle")

	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord),
		Destination:     backupDest,
		Quiesce:         true,
		DeadlineSeconds: 120,
	}, io.Discard)
	if err != nil {
		t.Fatalf("PrepareBackup failed: %v", err)
	}

	if summary.Stage != "captured" {
		t.Fatalf("expected stage prepared, got %q", summary.Stage)
	}
	if summary.Destination != backupDest {
		t.Fatalf("expected destination %q, got %q", backupDest, summary.Destination)
	}
	if !summary.Quiesced {
		t.Fatal("expected quiesced true")
	}

	adapter.mu.Lock()
	actions := slices.Clone(adapter.actions)
	adapter.mu.Unlock()

	// Stopping should be reverse dependency order: app first, then db!
	// Resuming should be forward dependency order: db first, then app!
	expectedActions := []string{
		"stop:service/app",
		"stop:service/db",
		"start:service/db",
		"start:service/app",
	}
	if !reflect.DeepEqual(actions, expectedActions) {
		t.Fatalf("unexpected actions: got %v, want %v", actions, expectedActions)
	}

	manifestPath := filepath.Join(backupDest, "manifest.json")
	mBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	calculatedDigest := digest(mBytes)
	if summary.ManifestChecksum != calculatedDigest {
		t.Fatalf("manifest checksum mismatch: %s != %s", summary.ManifestChecksum, calculatedDigest)
	}

	var manifest BackupManifest
	if err := json.Unmarshal(mBytes, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if manifest.Stage != "captured" {
		t.Fatalf("manifest stage: %q", manifest.Stage)
	}
	if len(manifest.RunningWorkloadsAtBackup) != 2 {
		t.Fatalf("expected 2 running workloads at backup, got %v", manifest.RunningWorkloadsAtBackup)
	}
	if !slices.Contains(manifest.ResumedWorkloads, "app") || !slices.Contains(manifest.ResumedWorkloads, "db") {
		t.Fatalf("resumed workloads missing: %v", manifest.ResumedWorkloads)
	}

	// Verify server-man.yaml
	configPath := filepath.Join(backupDest, "server-man.yaml")
	cBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read bundle config: %v", err)
	}
	sourceConfig, err := os.ReadFile(filepath.Join(root, "server-man.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cBytes, sourceConfig) {
		t.Fatal("bundle server-man.yaml does not match source")
	}

	// Verify documents/notes.txt
	notesPath := filepath.Join(backupDest, "data", "documents", "notes.txt")
	nBytes, err := os.ReadFile(notesPath)
	if err != nil {
		t.Fatalf("read bundle notes: %v", err)
	}
	if string(nBytes) != "hello notes" {
		t.Fatalf("unexpected notes content: %q", string(nBytes))
	}

	// Verify secret permissions
	secretBundlePath := filepath.Join(backupDest, "secrets", "token.env")
	sInfo, err := os.Stat(secretBundlePath)
	if err != nil {
		t.Fatalf("stat secret file: %v", err)
	}
	if sInfo.Mode().Perm() != 0600 {
		t.Fatalf("expected 0600 permissions on secret file, got %04o", sInfo.Mode().Perm())
	}
}

func TestPrepareBackupStoppedWorkloadRemainsStopped(t *testing.T) {
	coord, adapter, _ := setupBackupFixture(t)
	// Make app stopped before backup
	adapter.mu.Lock()
	adapter.activeStates["service/app"] = false
	adapter.mu.Unlock()

	backupDest := filepath.Join(t.TempDir(), "bundle-partial")
	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord),
		Destination:     backupDest,
		Quiesce:         true,
		DeadlineSeconds: 120,
	}, io.Discard)
	if err != nil {
		t.Fatalf("PrepareBackup failed: %v", err)
	}

	adapter.mu.Lock()
	actions := slices.Clone(adapter.actions)
	adapter.mu.Unlock()

	// Only db was running, so only db should be stopped and resumed!
	expectedActions := []string{
		"stop:service/db",
		"start:service/db",
	}
	if !reflect.DeepEqual(actions, expectedActions) {
		t.Fatalf("unexpected actions: got %v, want %v", actions, expectedActions)
	}

	if slices.Contains(summary.ResumedServices, "app") {
		t.Fatal("app was stopped before backup and should not have been resumed")
	}
	if !slices.Contains(summary.ResumedServices, "db") {
		t.Fatal("db should have been resumed")
	}
}

func TestPrepareBackupResumeFailurePreservesBundleAndAlerts(t *testing.T) {
	coord, adapter, _ := setupBackupFixture(t)
	// Cause db restart to fail
	adapter.mu.Lock()
	adapter.failAction = "start:service/db"
	adapter.mu.Unlock()

	backupDest := filepath.Join(t.TempDir(), "bundle-resume-fail")
	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord),
		Destination:     backupDest,
		Quiesce:         true,
		DeadlineSeconds: 120,
	}, io.Discard)
	if err == nil {
		t.Fatal("resume failure was reported as success")
	}

	if !slices.Contains(summary.FailedResume, "db") {
		t.Fatalf("expected db in FailedResume, got %v", summary.FailedResume)
	}
	if slices.Contains(summary.ResumedServices, "app") || !slices.Contains(summary.FailedResume, "app") {
		t.Fatalf("dependant resumed after dependency failure: %+v", summary)
	}

	// Verify bundle still published
	if _, err := os.Stat(filepath.Join(backupDest, "manifest.json")); err != nil {
		t.Fatalf("manifest should exist: %v", err)
	}
}

func TestPrepareBackupRejectsInvalidDestinations(t *testing.T) {
	coord, _, root := setupBackupFixture(t)

	// Non-absolute
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: "rel/path"}, io.Discard); err == nil {
		t.Fatal("expected error for non-absolute path")
	}

	// Inside config directory
	insidePath := filepath.Join(root, "my-backup")
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: insidePath}, io.Discard); err == nil {
		t.Fatal("expected error for destination inside config root")
	}

	// System directory
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: "/proc/backup"}, io.Discard); err == nil {
		t.Fatal("expected error for system directory")
	}

	// Non-empty existing directory
	existingDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(existingDir, "dummy.txt"), []byte("data"), 0644)
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: existingDir}, io.Discard); err == nil {
		t.Fatal("expected error for non-empty existing directory")
	}
}

func TestPrepareBackupRejectsUnresolvedInventoryErrors(t *testing.T) {
	coord, _, root := setupBackupFixture(t)
	// Delete the required secret file
	_ = os.Remove(filepath.Join(root, "secrets", "token.env"))

	dest := filepath.Join(t.TempDir(), "should-fail")
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: dest}, io.Discard); err == nil {
		t.Fatal("expected error due to missing required secret file")
	}

	// Ensure no leftover bundle
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("destination should not exist after failure")
	}
}

func TestPrepareBackupSparseFileSupport(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "sparse.bin")
	dst := filepath.Join(tmp, "sparse_copy.bin")

	// Create 1 MB file with 512 KB of zeroes in the middle
	data := make([]byte, 1024*1024)
	copy(data[:1024], []byte("header"))
	copy(data[len(data)-1024:], []byte("footer"))

	if err := os.WriteFile(src, data, 0644); err != nil {
		t.Fatal(err)
	}

	manifestFile, err := copyFileSparseAndHash(src, dst, 0644)
	if err != nil {
		t.Fatalf("copyFileSparseAndHash failed: %v", err)
	}

	h := sha256.New()
	h.Write(data)
	expectedHash := hex.EncodeToString(h.Sum(nil))
	if manifestFile.SHA256 != expectedHash {
		t.Fatalf("hash mismatch: %s != %s", manifestFile.SHA256, expectedHash)
	}
	if manifestFile.SizeBytes != int64(len(data)) {
		t.Fatalf("size mismatch: %d != %d", manifestFile.SizeBytes, len(data))
	}

	copiedData, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copiedData, data) {
		t.Fatal("copied data does not match original sparse data")
	}
}

func TestPrepareBackupIntoExistingEmptyDirectory(t *testing.T) {
	coord, _, _ := setupBackupFixture(t)
	coord.Adapters["podman/quadlet"].(*backupTestAdapter).activeStates = map[string]bool{}
	backupDest := filepath.Join(t.TempDir(), "existing-empty-bundle")
	if err := os.MkdirAll(backupDest, 0700); err != nil {
		t.Fatal(err)
	}

	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord),
		Destination:     backupDest,
		Quiesce:         false,
		DeadlineSeconds: 120,
	}, io.Discard)
	if err != nil {
		t.Fatalf("PrepareBackup failed: %v", err)
	}

	if summary.Destination != backupDest {
		t.Fatalf("expected destination %q, got %q", backupDest, summary.Destination)
	}
	if _, err := os.Stat(filepath.Join(backupDest, "manifest.json")); err != nil {
		t.Fatalf("manifest.json missing: %v", err)
	}
}

func TestPrepareBackupCancellationCleansStagingAndResumes(t *testing.T) {
	coord, adapter, _ := setupBackupFixture(t)
	backupDest := filepath.Join(t.TempDir(), "cancelled-bundle")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so it aborts during file preparation
	cancel()

	_, err := coord.PrepareBackup(ctx, BackupPrepareOptions{Revision: backupRevision(t, coord),
		Destination:     backupDest,
		Quiesce:         true,
		DeadlineSeconds: 120,
	}, io.Discard)
	if err == nil {
		t.Fatal("expected context error")
	}

	// Verify no published bundle exists
	if _, err := os.Stat(backupDest); !os.IsNotExist(err) {
		t.Fatal("bundle should not be published on cancellation")
	}

	// Verify workloads were still resumed!
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	for _, k := range []string{"service/db", "service/app"} {
		if !adapter.activeStates[k] {
			t.Fatalf("expected %s to be active after cleanup", k)
		}
	}
}

func TestPrepareBackupRejectsUnverifiedNativeExportRecords(t *testing.T) {
	coord, adapter, _ := setupBackupFixture(t)
	adapter.activeStates = map[string]bool{}
	adapter.exportItems = []BackupExport{{BundlePath: "native/missing.tar", SizeBytes: 123, SHA256: strings.Repeat("a", 64), Kind: "fake_export"}}
	destination := filepath.Join(t.TempDir(), "bundle")
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: destination, Quiesce: true}, io.Discard); err == nil {
		t.Fatal("nonexistent native export was accepted")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("incomplete bundle was published", err)
	}
}

func TestPrepareBackupRegistryPolicy(t *testing.T) {
	coord, _, _ := setupBackupFixture(t)
	jobs, err := core.NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := core.NewRegistry(false, jobs)
	if err := RegisterBackup(r, coord); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "backup-bundle")

	// Writes disabled
	rawNoWrites, _ := json.Marshal(map[string]any{"revision": backupRevision(t, coord), "destination": dest, "confirm": true})
	_, err = r.Invoke(context.Background(), "managed_backup_prepare", rawNoWrites, "test")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected writes disabled error, got %v", err)
	}

	// Writes enabled, but no confirm
	r = core.NewRegistry(true, jobs)
	if err := RegisterBackup(r, coord); err != nil {
		t.Fatal(err)
	}
	rawNoConfirm, _ := json.Marshal(map[string]any{"revision": backupRevision(t, coord), "destination": dest})
	_, err = r.Invoke(context.Background(), "managed_backup_prepare", rawNoConfirm, "test")
	if err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("expected confirm error, got %v", err)
	}
}

func backupRevision(t *testing.T, c Coordinator) string {
	t.Helper()
	snapshot, err := c.Store.Read()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Revision
}

func TestBackupCaptureGuardsRunBeforeQuiescence(t *testing.T) {
	for _, scenario := range []string{"missing revision", "stale revision", "unknown runtime", "live copy", "corrupt journal", "destination symlink", "parent alias into source"} {
		t.Run(scenario, func(t *testing.T) {
			coord, adapter, root := setupBackupFixture(t)
			dest := filepath.Join(t.TempDir(), "bundle")
			options := BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: dest, Quiesce: true}
			switch scenario {
			case "missing revision":
				options.Revision = ""
			case "stale revision":
				options.Revision = strings.Repeat("0", 64)
			case "unknown runtime":
				adapter.failInspect = true
			case "live copy":
				options.Quiesce = false
			case "corrupt journal":
				if err := os.WriteFile(coord.Store.Path+".apply.json", []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "destination symlink":
				empty := t.TempDir()
				if err := os.Symlink(empty, dest); err != nil {
					t.Fatal(err)
				}
			case "parent alias into source":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(root, alias); err != nil {
					t.Fatal(err)
				}
				options.Destination = filepath.Join(alias, "bundle")
			}
			if _, err := coord.PrepareBackup(context.Background(), options, io.Discard); err == nil {
				t.Fatal("unsafe capture was accepted")
			}
			if len(adapter.actions) != 0 {
				t.Fatalf("preflight changed workloads: %v", adapter.actions)
			}
			if _, err := os.Stat(filepath.Join(options.Destination, "manifest.json")); !os.IsNotExist(err) {
				t.Fatalf("manifest published: %v", err)
			}
		})
	}
}

func TestBackupExportVerificationChecksRealPrivateArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "native"), 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("opaque native archive")
	path := filepath.Join(root, "native", "guest.tar")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	good := BackupExport{BundlePath: "native/guest.tar", SizeBytes: int64(len(data)), SHA256: digest(data)}
	if err := verifyBackupExport(context.Background(), root, good); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"checksum", "size", "escape", "metadata path", "public", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			exp := good
			switch scenario {
			case "checksum":
				exp.SHA256 = strings.Repeat("0", 64)
			case "size":
				exp.SizeBytes++
			case "escape":
				exp.BundlePath = "../guest.tar"
			case "metadata path":
				exp.BundlePath = "manifest.json"
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(path, 0600)
			case "symlink":
				if err := os.Symlink("guest.tar", filepath.Join(root, "native", "link")); err != nil {
					t.Fatal(err)
				}
				exp.BundlePath = "native/link"
			}
			if err := verifyBackupExport(context.Background(), root, exp); err == nil {
				t.Fatal("unverified export accepted")
			}
		})
	}
}

func TestBackupFileCopyHonorsCancellationAndNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	source, dest := filepath.Join(root, "source"), filepath.Join(root, "copy")
	if err := os.WriteFile(source, []byte("private data"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := copyBackupFile(ctx, source, dest, 0600); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if err := os.WriteFile(dest, []byte("existing data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := copyBackupFile(context.Background(), source, dest, 0600); err == nil {
		t.Fatal("existing file overwritten")
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "existing data" {
		t.Fatalf("existing file changed: %s %v", got, err)
	}
}

func TestBackupRecoversStopThatChangedStateBeforeFailure(t *testing.T) {
	coord, adapter, _ := setupBackupFixture(t)
	adapter.failAction = "stop:service/app"
	adapter.failAfterChange = true
	destination := filepath.Join(t.TempDir(), "bundle")
	_, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: destination, Quiesce: true}, io.Discard)
	if err == nil {
		t.Fatal("failed stop reported success")
	}
	if !adapter.activeStates["service/app"] || !adapter.activeStates["service/db"] {
		t.Fatalf("original running state not recovered: %+v", adapter.activeStates)
	}
	if !slices.Equal(adapter.actions, []string{"stop:service/app", "start:service/app"}) {
		t.Fatalf("unexpected recovery: %v", adapter.actions)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("capture published despite stop failure", err)
	}
}

func TestBackupResumeRequiresVerifiedActiveState(t *testing.T) {
	coord, adapter, _ := setupBackupFixture(t)
	adapter.silentStart = true
	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: filepath.Join(t.TempDir(), "bundle"), Quiesce: true}, io.Discard)
	if err == nil {
		t.Fatal("unverified resume reported success")
	}
	if !slices.Equal(summary.FailedResume, []string{"db", "app"}) || len(summary.ResumedServices) != 0 {
		t.Fatalf("wrong recovery result: %+v", summary)
	}
	if slices.Contains(adapter.actions, "start:service/app") {
		t.Fatal("dependant started despite dependency being inactive")
	}
}

func TestBackupTreeLimitsDirectoriesAndRejectsAbsoluteLinks(t *testing.T) {
	source := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(source, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := copyDirectory(context.Background(), source, filepath.Join(t.TempDir(), "limited"), "data", 1); err == nil {
		t.Fatal("directory entries bypassed copy limit")
	}
	if err := os.Symlink(filepath.Join(source, "a"), filepath.Join(source, "absolute")); err != nil {
		t.Fatal(err)
	}
	if _, err := copyDirectory(context.Background(), source, filepath.Join(t.TempDir(), "links"), "data", 10); err == nil {
		t.Fatal("absolute link retained source-host binding")
	}
}

func TestBackupCaptureIncludesAndVerifiesInitialImageAssets(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "mismatch"}[mismatch], func(t *testing.T) {
			coord, adapter, root := setupBackupFixture(t)
			adapter.activeStates = map[string]bool{}
			if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
				t.Fatal(err)
			}
			image := []byte("private initial raw image")
			if err := os.WriteFile(filepath.Join(root, "assets", "seed.raw"), image, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "data", "vm.raw"), make([]byte, 4096), 0600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := coord.Store.Read()
			if err != nil {
				t.Fatal(err)
			}
			checksum := digest(image)
			if mismatch {
				checksum = strings.Repeat("0", 64)
			}
			snapshot.Config.Storage["disk"] = Storage{Kind: "file", Path: "data/vm.raw", Format: "raw", Source: &DiskSource{Path: "assets/seed.raw", Format: "raw", SHA256: checksum}}
			if _, err := coord.Store.Save(snapshot.Config, snapshot.Revision); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "bundle")
			_, err = coord.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, coord), Destination: dest}, io.Discard)
			if mismatch {
				if err == nil {
					t.Fatal("mismatched asset checksum accepted")
				}
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatal("mismatched bundle published", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dest, "assets", "seed.raw"))
			if err != nil || !bytes.Equal(got, image) {
				t.Fatalf("asset omitted/changed: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest BackupManifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(manifest.Files, func(file BackupManifestFile) bool {
				return file.BundlePath == "assets/seed.raw" && file.SHA256 == digest(image)
			}) {
				t.Fatal("asset missing from manifest")
			}
		})
	}
}
