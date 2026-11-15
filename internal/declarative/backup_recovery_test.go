package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"server-man/internal/core"
)

func pendingBackup(t *testing.T, c Coordinator, states map[string]string) BackupRecoveryStatus {
	t.Helper()
	j := BackupRecovery{Version: 1, Revision: backupRevision(t, c), Destination: filepath.Join(t.TempDir(), "bundle"), Stage: "capturing", Services: states}
	if err := c.writeBackupRecovery(&j); err != nil {
		t.Fatal(err)
	}
	status, err := c.ReadBackupRecovery()
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestBackupPersistsStopIntentBeforeChangingRuntime(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	a.actionHook = func(key, action string) {
		status, err := c.ReadBackupRecovery()
		if err != nil || status.Journal == nil {
			t.Fatal("missing durable record", err)
		}
		state := status.Journal.Services[strings.TrimPrefix(key, "service/")]
		if action == "stop" && state != "stop_attempted" {
			t.Fatal("stop preceded durable intent", state)
		}
		if action == "start" && state != "stopped" {
			t.Fatal("resume lacks verified stop record", state)
		}
	}
	if _, err := c.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, c), Destination: filepath.Join(t.TempDir(), "bundle"), Quiesce: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	status, err := c.ReadBackupRecovery()
	if err != nil || status.Journal.Stage != "complete" {
		t.Fatal("successful recovery not durable", status, err)
	}
	if status.Journal.Services["db"] != "resumed" || status.Journal.Services["app"] != "resumed" {
		t.Fatal(status)
	}
}

func TestBackupRecoverySurvivesRestartAndDoesNotReplayActiveStarts(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	status := pendingBackup(t, c, map[string]string{"db": "stopped", "app": "stop_attempted"})
	a.activeStates["service/app"] = false // db already resumed before a simulated crash.
	restarted := Coordinator{Store: Store{Path: c.Store.Path}, Adapters: c.Adapters}
	got, err := restarted.RecoverBackup(context.Background(), status.Journal.Revision, status.ReviewToken)
	if err != nil || got.Journal.Stage != "complete" {
		t.Fatal(got, err)
	}
	if !slices.Equal(a.actions, []string{"start:service/app"}) {
		t.Fatal("replayed an already active start", a.actions)
	}
	if _, err := restarted.RecoverBackup(context.Background(), got.Journal.Revision, got.ReviewToken); err != nil {
		t.Fatal(err)
	}
	if len(a.actions) != 1 {
		t.Fatal("completed recovery repeated actions")
	}
}

func TestBackupRecoveryLeavesUnattemptedWorkloadsUntouched(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	status := pendingBackup(t, c, map[string]string{"db": "untouched", "app": "stop_attempted"})
	a.activeStates["service/db"] = false
	a.activeStates["service/app"] = false
	_, err := c.RecoverBackup(context.Background(), status.Journal.Revision, status.ReviewToken)
	if err == nil {
		t.Fatal("dependant resumed with inactive unattempted dependency")
	}
	if len(a.actions) != 0 {
		t.Fatal("started an unattempted dependency", a.actions)
	}
	latest, e := c.ReadBackupRecovery()
	if e != nil || latest.Journal.Services["db"] != "untouched" || latest.Journal.Services["app"] != "failed" {
		t.Fatal(latest, e)
	}
}

func TestBackupRecoveryRetainsFailuresForReviewedRetry(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	status := pendingBackup(t, c, map[string]string{"db": "stopped", "app": "stopped"})
	a.activeStates = map[string]bool{}
	a.failAction = "start:service/db"
	latest, err := c.RecoverBackup(context.Background(), status.Journal.Revision, status.ReviewToken)
	if err == nil || latest.Journal.Stage == "complete" {
		t.Fatal("failed recovery marked complete", latest, err)
	}
	if slices.Contains(a.actions, "start:service/app") {
		t.Fatal("started dependant after dependency failure")
	}
	if _, err := c.RecoverBackup(context.Background(), status.Journal.Revision, status.ReviewToken); !errors.Is(err, ErrConflict) {
		t.Fatal("stale review accepted", err)
	}
	a.failAction = ""
	got, err := c.RecoverBackup(context.Background(), latest.Journal.Revision, latest.ReviewToken)
	if err != nil || got.Journal.Stage != "complete" {
		t.Fatal(got, err)
	}
	if !a.activeStates["service/db"] || !a.activeStates["service/app"] {
		t.Fatal("running state not recovered")
	}
}

func TestPendingBackupBlocksManagedMutationsAndRaisesGlobalAlert(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	pendingBackup(t, c, map[string]string{"db": "stopped"})
	snapshot, err := c.Store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ServiceAction(context.Background(), "app", "start"); err == nil {
		t.Fatal("lifecycle ignored pending backup")
	}
	if _, err := c.SaveApply(context.Background(), snapshot.Config, snapshot.Revision, "next_start"); err == nil {
		t.Fatal("save ignored pending backup")
	}
	if _, err := c.Retry(context.Background()); err == nil {
		t.Fatal("retry ignored pending backup")
	}
	if _, err := (Provisioner{Coordinator: c}).Create(context.Background(), "documents", snapshot.Revision, "", 0); err == nil {
		t.Fatal("provisioning ignored pending backup")
	}
	if _, err := c.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: filepath.Join(t.TempDir(), "next")}, io.Discard); err == nil {
		t.Fatal("new backup ignored pending recovery")
	}
	health := c.ConfigurationReadiness(context.Background())
	if health.Health == nil || !slices.ContainsFunc(health.Health.Issues, func(i Issue) bool { return i.Code == "backup_recovery" }) {
		t.Fatal("missing global recovery blocker", health)
	}
	if len(a.actions) != 0 {
		t.Fatal("inspection activated workloads", a.actions)
	}
}

func TestBackupRecoveryRejectsConfigDriftBeforeAnyAction(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	status := pendingBackup(t, c, map[string]string{"db": "stopped"})
	raw, err := os.ReadFile(c.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Store.Path, append(raw, []byte("\n# changed outside server-man\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecoverBackup(context.Background(), status.Journal.Revision, status.ReviewToken); !errors.Is(err, ErrConflict) {
		t.Fatal("configuration drift accepted", err)
	}
	if len(a.actions) != 0 {
		t.Fatal("drift activated workloads")
	}
}

func TestBackupRecoveryRejectsUnsafeOrMalformedJournal(t *testing.T) {
	for _, scenario := range []string{"public", "symlink", "malformed", "unknown field", "invalid state", "false completion", "unknown service"} {
		t.Run(scenario, func(t *testing.T) {
			c, a, _ := setupBackupFixture(t)
			status := pendingBackup(t, c, map[string]string{"db": "stopped"})
			path := c.Store.Path + ".backup.json"
			switch scenario {
			case "public":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".real", path); err != nil {
					t.Fatal(err)
				}
			default:
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "malformed":
					raw = []byte("{")
				case "unknown field":
					raw = append([]byte(`{"unexpected":true,`), raw[1:]...)
				case "invalid state":
					raw = []byte(strings.ReplaceAll(string(raw), "stopped", "running"))
				case "false completion":
					raw = []byte(strings.ReplaceAll(string(raw), "capturing", "complete"))
				case "unknown service":
					raw = []byte(strings.ReplaceAll(string(raw), `"db":`, `"nonexistent":`))
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			// An unknown but syntactically valid service is rejected against the pinned config.
			if scenario == "unknown service" {
				var err error
				status, err = c.ReadBackupRecovery()
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.RecoverBackup(context.Background(), status.Journal.Revision, status.ReviewToken); err == nil {
				t.Fatal("unsafe recovery accepted")
			}
			if len(a.actions) != 0 {
				t.Fatal("unsafe journal activated workloads")
			}
		})
	}
}

func TestBackupRecoveryToolsApplyPrivatePolicyAndConfirmation(t *testing.T) {
	c, _, _ := setupBackupFixture(t)
	status := pendingBackup(t, c, map[string]string{"db": "stopped"})
	jobs, err := core.NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	r := core.NewRegistry(false, jobs)
	if err := RegisterBackup(r, c); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"revision": status.Journal.Revision, "reviewToken": status.ReviewToken, "confirm": true})
	for name, raw := range map[string]json.RawMessage{"managed_backup_recovery_status": json.RawMessage(`{}`), "managed_backup_recover": args, "managed_backup_release": args, "managed_backup_publish": args} {
		if _, err := r.Invoke(context.Background(), name, raw, "test"); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatal(name, err)
		}
	}
	r = core.NewRegistry(true, jobs)
	if err := RegisterBackup(r, c); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Invoke(context.Background(), "managed_backup_recovery_status", json.RawMessage(`{}`), "test"); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]any{"revision": status.Journal.Revision, "reviewToken": status.ReviewToken})
	for _, name := range []string{"managed_backup_recover", "managed_backup_publish"} {
		if _, err := r.Invoke(context.Background(), name, args, "test"); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Fatal(err)
		}
	}
}

func TestReviewedBackupReleaseUnblocksRepairWithoutRuntimeActions(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	original := pendingBackup(t, c, map[string]string{"db": "untouched", "app": "failed"})
	raw, err := os.ReadFile(c.Store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Store.Path, append(raw, []byte("\n# external edit\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	status, err := c.ReadBackupRecovery()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReleaseBackup(original.CurrentRevision, original.ReviewToken); !errors.Is(err, ErrConflict) {
		t.Fatal("release accepted stale configuration review", err)
	}
	released, err := c.ReleaseBackup(status.CurrentRevision, status.ReviewToken)
	if err != nil || released.Journal.Stage != "released" {
		t.Fatal(released, err)
	}
	if released.Journal.Services["app"] != "failed" {
		t.Fatal("release falsely claimed resumption", released)
	}
	if err := c.checkBackupIdle(); err != nil {
		t.Fatal("reviewed release did not unblock repair", err)
	}
	if len(a.actions) != 0 {
		t.Fatal("release changed runtime", a.actions)
	}
}

func TestMissingConfigStillShowsPendingRecoveryAndCanBeReleased(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	pendingBackup(t, c, map[string]string{"db": "stop_attempted"})
	if err := os.Remove(c.Store.Path); err != nil {
		t.Fatal(err)
	}
	health := c.ConfigurationReadiness(context.Background())
	if !health.Configured || health.Error == "" {
		t.Fatal("missing config hid interrupted backup", health)
	}
	status, err := c.ReadBackupRecovery()
	if err != nil || status.CurrentRevision != "missing" {
		t.Fatal(status, err)
	}
	if _, err := c.ReleaseBackup(status.CurrentRevision, status.ReviewToken); err != nil {
		t.Fatal(err)
	}
	if len(a.actions) != 0 {
		t.Fatal("release activated workloads")
	}
}

func TestBackupRecoveryRefusesMissingDataAndCancellation(t *testing.T) {
	for _, scenario := range []string{"missing data", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			c, a, root := setupBackupFixture(t)
			status := pendingBackup(t, c, map[string]string{"db": "stopped", "app": "stopped"})
			a.activeStates = map[string]bool{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "missing data" {
				if err := os.RemoveAll(filepath.Join(root, "data", "documents")); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			got, err := c.RecoverBackup(ctx, status.Journal.Revision, status.ReviewToken)
			if err == nil || got.Journal.Stage == "complete" {
				t.Fatal("unready recovery completed", got, err)
			}
			if len(a.actions) != 0 {
				t.Fatal("unready recovery started workloads", a.actions)
			}
		})
	}
}
