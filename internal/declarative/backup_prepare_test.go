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
	mu           sync.Mutex
	actions      []string
	activeStates map[string]bool
	failAction   string
	failInspect  bool
	exportItems  []BackupExport
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
	if a.failAction == action+":"+key {
		return errors.New("action failed: " + action + " on " + key)
	}
	if action == "stop" {
		a.activeStates[key] = false
	} else if action == "start" {
		a.activeStates[key] = true
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

	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{
		Destination:     backupDest,
		Quiesce:         true,
		DeadlineSeconds: 120,
	}, io.Discard)
	if err != nil {
		t.Fatalf("PrepareBackup failed: %v", err)
	}

	if summary.Stage != "prepared" {
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
	if manifest.Stage != "prepared" {
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
	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{
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
	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{
		Destination:     backupDest,
		Quiesce:         true,
		DeadlineSeconds: 120,
	}, io.Discard)
	if err != nil {
		t.Fatalf("PrepareBackup should still succeed if only resume failed: %v", err)
	}

	if !slices.Contains(summary.FailedResume, "db") {
		t.Fatalf("expected db in FailedResume, got %v", summary.FailedResume)
	}
	if !slices.Contains(summary.ResumedServices, "app") {
		t.Fatalf("expected app in ResumedServices, got %v", summary.ResumedServices)
	}

	// Verify bundle still published
	if _, err := os.Stat(filepath.Join(backupDest, "manifest.json")); err != nil {
		t.Fatalf("manifest should exist: %v", err)
	}
}

func TestPrepareBackupRejectsInvalidDestinations(t *testing.T) {
	coord, _, root := setupBackupFixture(t)

	// Non-absolute
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Destination: "rel/path"}, io.Discard); err == nil {
		t.Fatal("expected error for non-absolute path")
	}

	// Inside config directory
	insidePath := filepath.Join(root, "my-backup")
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Destination: insidePath}, io.Discard); err == nil {
		t.Fatal("expected error for destination inside config root")
	}

	// System directory
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Destination: "/proc/backup"}, io.Discard); err == nil {
		t.Fatal("expected error for system directory")
	}

	// Non-empty existing directory
	existingDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(existingDir, "dummy.txt"), []byte("data"), 0644)
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Destination: existingDir}, io.Discard); err == nil {
		t.Fatal("expected error for non-empty existing directory")
	}
}

func TestPrepareBackupRejectsUnresolvedInventoryErrors(t *testing.T) {
	coord, _, root := setupBackupFixture(t)
	// Delete the required secret file
	_ = os.Remove(filepath.Join(root, "secrets", "token.env"))

	dest := filepath.Join(t.TempDir(), "should-fail")
	if _, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{Destination: dest}, io.Discard); err == nil {
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
	backupDest := filepath.Join(t.TempDir(), "existing-empty-bundle")
	if err := os.MkdirAll(backupDest, 0700); err != nil {
		t.Fatal(err)
	}

	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{
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

	_, err := coord.PrepareBackup(ctx, BackupPrepareOptions{
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

func TestPrepareBackupNativeExportIntegration(t *testing.T) {
	coord, adapter, _ := setupBackupFixture(t)
	adapter.exportItems = []BackupExport{
		{
			BundlePath: "native/custom/export.tar.gz",
			SizeBytes:  123,
			SHA256:     "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
			Kind:       "custom_export",
			Native:     &NativeBackupObject{Backend: "quadlet", Name: "export"},
		},
	}

	backupDest := filepath.Join(t.TempDir(), "bundle-native")
	summary, err := coord.PrepareBackup(context.Background(), BackupPrepareOptions{
		Destination:     backupDest,
		Quiesce:         false,
		DeadlineSeconds: 120,
	}, io.Discard)
	if err != nil {
		t.Fatalf("PrepareBackup failed: %v", err)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(backupDest, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.NativeExports) == 0 {
		t.Fatal("expected native exports in manifest")
	}
	if summary.TotalFiles < 3 {
		t.Fatalf("expected at least 3 files, got %d", summary.TotalFiles)
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
	rawNoWrites, _ := json.Marshal(map[string]any{"destination": dest, "confirm": true})
	_, err = r.Invoke(context.Background(), "managed_backup_prepare", rawNoWrites, "test")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected writes disabled error, got %v", err)
	}

	// Writes enabled, but no confirm
	r = core.NewRegistry(true, jobs)
	if err := RegisterBackup(r, coord); err != nil {
		t.Fatal(err)
	}
	rawNoConfirm, _ := json.Marshal(map[string]any{"destination": dest})
	_, err = r.Invoke(context.Background(), "managed_backup_prepare", rawNoConfirm, "test")
	if err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("expected confirm error, got %v", err)
	}
}


