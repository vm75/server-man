package declarative

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func reconcileFixture(t *testing.T, staged bool) (Coordinator, *backupTestAdapter, BackupRecoveryStatus) {
	t.Helper()
	c, adapter, _ := setupBackupFixture(t)
	adapter.activeStates = map[string]bool{}
	destination := filepath.Join(t.TempDir(), "bundle")
	if _, err := c.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, c), Destination: destination}, io.Discard); err != nil {
		t.Fatal(err)
	}
	status, err := c.ReadBackupRecovery()
	if err != nil {
		t.Fatal(err)
	}
	if staged {
		if err := os.Rename(destination, status.Journal.Artifact.StagingPath); err != nil {
			t.Fatal(err)
		}
	}
	status.Journal.Artifact.Stage = "synced"
	if err := c.writeBackupRecovery(status.Journal); err != nil {
		t.Fatal(err)
	}
	status, err = c.ReadBackupRecovery()
	if err != nil {
		t.Fatal(err)
	}
	return c, adapter, status
}

func TestReconcileBackupPublicationCompletesRecordedArtifactOnly(t *testing.T) {
	for _, staged := range []bool{true, false} {
		t.Run(map[bool]string{true: "staged", false: "renamed"}[staged], func(t *testing.T) {
			c, adapter, before := reconcileFixture(t, staged)
			before.Journal.Stage = "recovering"
			before.Journal.Services = map[string]string{"db": "failed"}
			if err := c.writeBackupRecovery(before.Journal); err != nil {
				t.Fatal(err)
			}
			before, _ = c.ReadBackupRecovery()
			after, err := c.ReconcileBackupPublication(context.Background(), before.CurrentRevision, before.ReviewToken, 30)
			if err != nil {
				t.Fatal(err)
			}
			if after.Journal.Artifact.Stage != "published" || after.Journal.Stage != "recovering" || !reflect.DeepEqual(before.Journal.Services, after.Journal.Services) || len(adapter.actions) != 0 || !backupRecoveryPending(after.Journal) {
				t.Fatal("publication changed or released workload recovery", after)
			}
			if _, err := os.Stat(before.Journal.Artifact.StagingPath); !os.IsNotExist(err) {
				t.Fatal("staging name remains", err)
			}
			if _, err := os.Stat(filepath.Join(before.Journal.Destination, "manifest.json")); err != nil {
				t.Fatal(err)
			}
			if _, err := c.ReconcileBackupPublication(context.Background(), after.CurrentRevision, after.ReviewToken, 30); err != nil {
				t.Fatal("completed retry", err)
			}
			if _, err := c.ReconcileBackupPublication(context.Background(), before.CurrentRevision, before.ReviewToken, 30); err != ErrConflict {
				t.Fatal("stale review accepted", err)
			}
		})
	}
}

func TestReconcileBackupPublicationRefusesUnsafeArtifacts(t *testing.T) {
	for _, scenario := range []string{"empty destination", "populated destination", "missing staging", "changed inode", "staging symlink", "changed parent", "payload", "checksum", "revision", "no checksum", "cancelled", "timeout", "external config", "external journal"} {
		t.Run(scenario, func(t *testing.T) {
			c, adapter, status := reconcileFixture(t, true)
			a := status.Journal.Artifact
			ctx := context.Background()
			deadline := 30
			switch scenario {
			case "empty destination", "populated destination":
				if err := os.Mkdir(status.Journal.Destination, 0700); err != nil {
					t.Fatal(err)
				}
				if scenario == "populated destination" {
					mustBackupWrite(t, filepath.Join(status.Journal.Destination, "unrelated"), "keep")
				}
			case "missing staging":
				if err := os.Rename(a.StagingPath, a.StagingPath+"-away"); err != nil {
					t.Fatal(err)
				}
			case "changed inode", "staging symlink":
				if err := os.Rename(a.StagingPath, a.StagingPath+"-away"); err != nil {
					t.Fatal(err)
				}
				if scenario == "staging symlink" {
					if err := os.Symlink(filepath.Base(a.StagingPath)+"-away", a.StagingPath); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(a.StagingPath, 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "changed parent":
				parent := filepath.Dir(a.StagingPath)
				if err := os.Rename(parent, parent+"-away"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(parent + "-away") })
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
			case "payload":
				mustBackupWrite(t, filepath.Join(a.StagingPath, "data/documents/notes.txt"), "tampered")
			case "checksum":
				a.ManifestChecksum = strings.Repeat("b", 64)
			case "revision":
				status.Journal.Revision = strings.Repeat("b", 64)
			case "no checksum":
				a.Stage = "uncertain"
				a.ManifestChecksum = ""
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "timeout":
				deadline = 1
			case "external config":
				ctx = &backupHashContext{Context: ctx, onRead: func() {
					raw, err := os.ReadFile(c.Store.Path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(c.Store.Path, append(raw, '\n'), 0600); err != nil {
						t.Fatal(err)
					}
				}}
			case "external journal":
				ctx = &backupHashContext{Context: ctx, onRead: func() {
					status.Journal.Services = map[string]string{"db": "untouched"}
					if err := c.writeBackupRecovery(status.Journal); err != nil {
						t.Fatal(err)
					}
				}}
			}
			if scenario == "checksum" || scenario == "revision" || scenario == "no checksum" {
				if err := c.writeBackupRecovery(status.Journal); err != nil {
					t.Fatal(err)
				}
				status, _ = c.ReadBackupRecovery()
			}
			var err error
			if scenario == "external config" || scenario == "external journal" {
				_, err = c.reconcileBackupPublication(ctx, status.CurrentRevision, status.ReviewToken)
			} else {
				_, err = c.ReconcileBackupPublication(ctx, status.CurrentRevision, status.ReviewToken, deadline)
			}
			if err == nil {
				t.Fatal("unsafe publication accepted")
			}
			latest, err := c.ReadBackupRecovery()
			if err != nil {
				t.Fatal(err)
			}
			if latest.Journal.Artifact.Stage == "published" || len(adapter.actions) != 0 {
				t.Fatal("failed publication changed state", latest)
			}
			if scenario == "populated destination" {
				if raw, err := os.ReadFile(filepath.Join(status.Journal.Destination, "unrelated")); err != nil || string(raw) != "keep" {
					t.Fatal("conflicting data changed", err)
				}
			}
		})
	}
}

type reconcileCheckContext struct {
	context.Context
	check func()
}

func (c reconcileCheckContext) Err() error { c.check(); return c.Context.Err() }

func TestReconcileBackupPublicationRetainsIntentAcrossRenameBoundaries(t *testing.T) {
	for _, boundary := range []string{"before rename", "after rename"} {
		t.Run(boundary, func(t *testing.T) {
			c, _, status := reconcileFixture(t, true)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			injected := false
			ctx := reconcileCheckContext{Context: base, check: func() {
				if injected {
					return
				}
				latest, err := c.ReadBackupRecovery()
				if err != nil {
					t.Fatal(err)
				}
				if latest.Journal.Artifact.Stage != "publishing" {
					return
				}
				if boundary == "before rename" {
					injected = true
					if err := os.Mkdir(status.Journal.Destination, 0700); err != nil {
						t.Fatal(err)
					}
					mustBackupWrite(t, filepath.Join(status.Journal.Destination, "unrelated"), "keep")
				} else if _, err := os.Stat(filepath.Join(status.Journal.Destination, "manifest.json")); err == nil {
					injected = true
					cancel()
				}
			}}
			if _, err := c.reconcileBackupPublication(ctx, status.CurrentRevision, status.ReviewToken); err == nil || !injected {
				t.Fatal("fault did not interrupt publication", err)
			}
			latest, err := c.ReadBackupRecovery()
			if err != nil {
				t.Fatal(err)
			}
			if latest.Journal.Artifact.Stage != "publishing" {
				t.Fatal("interrupted intent lost", latest)
			}
			if boundary == "before rename" {
				if raw, err := os.ReadFile(filepath.Join(status.Journal.Destination, "unrelated")); err != nil || string(raw) != "keep" {
					t.Fatal("conflicting data changed", err)
				}
				if err := os.RemoveAll(status.Journal.Destination); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.ReconcileBackupPublication(context.Background(), latest.CurrentRevision, latest.ReviewToken, 30); err != nil {
				t.Fatal("reviewed retry failed", err)
			}
		})
	}
}
