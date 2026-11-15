package declarative

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func publicationFixture(t *testing.T, existing bool) (*backupPublication, string) {
	t.Helper()
	target := filepath.Join(t.TempDir(), "bundle")
	var expected os.FileInfo
	if existing {
		if err := os.Mkdir(target, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		expected, err = os.Lstat(target)
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := newBackupPublication(target, expected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	if err := os.Mkdir(filepath.Join(p.path(), "data"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.path(), "data", "file"), []byte("owned private data"), 0600); err != nil {
		t.Fatal(err)
	}
	return p, target
}
func TestPublicationSyncAndRenamePreservePrivateCapture(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "reviewed empty"}[existing], func(t *testing.T) {
			p, target := publicationFixture(t, existing)
			if err := os.Symlink("file", filepath.Join(p.path(), "data", "relative")); err != nil {
				t.Fatal(err)
			}
			if err := p.sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := p.rename(); err != nil {
				t.Fatal(err)
			}
			if err := p.parentFile.Sync(); err != nil {
				t.Fatal(err)
			}
			if err := p.discard(); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(target, "data", "file"))
			if err != nil || string(got) != "owned private data" {
				t.Fatal("published capture removed/changed", err)
			}
			if _, err := p.parent.Lstat(p.stage); !os.IsNotExist(err) {
				t.Fatal("staging name remains", err)
			}
		})
	}
}
func TestPublicationRefusesDestinationConflictsWithoutDeletingData(t *testing.T) {
	for _, scenario := range []string{"new empty destination", "new populated destination", "changed empty destination", "populated reviewed destination"} {
		t.Run(scenario, func(t *testing.T) {
			existing := strings.Contains(scenario, "reviewed") || scenario == "changed empty destination"
			p, target := publicationFixture(t, existing)
			if err := p.sync(context.Background()); err != nil {
				t.Fatal(err)
			}
			if scenario == "changed empty destination" {
				if err := os.Rename(target, target+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if !existing {
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains(scenario, "populated") {
				if err := os.WriteFile(filepath.Join(target, "unmanaged"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.rename(); err == nil {
				t.Fatal("unreviewed destination replaced")
			}
			if strings.Contains(scenario, "populated") {
				got, err := os.ReadFile(filepath.Join(target, "unmanaged"))
				if err != nil || string(got) != "preserve" {
					t.Fatal("destination data changed", err)
				}
			}
			if err := p.discard(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatal("destination removed during cleanup", err)
			}
		})
	}
}
func TestPublicationUsesHeldParentAndRefusesRetargetedNamespace(t *testing.T) {
	p, target := publicationFixture(t, false)
	parent := filepath.Dir(target)
	old := parent + ".held"
	if err := os.Rename(parent, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(parent); os.Rename(old, parent) })
	outside := t.TempDir()
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	if err := p.rename(); err == nil {
		t.Fatal("publication followed retargeted parent")
	}
	if err := p.discard(); err != nil {
		t.Fatal("owned staging could not be cleaned through held parent", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "bundle")); !os.IsNotExist(err) {
		t.Fatal("wrote through replacement namespace", err)
	}
}
func TestPublicationRefusesChangedStagingCleanup(t *testing.T) {
	p, _ := publicationFixture(t, false)
	if err := p.parent.Rename(p.stage, p.stage+".original"); err != nil {
		t.Fatal(err)
	}
	if err := p.parent.Mkdir(p.stage, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := p.parent.OpenFile(filepath.Join(p.stage, "unmanaged"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.Write([]byte("keep"))
	file.Close()
	if err := p.rename(); err == nil {
		t.Fatal("changed staging published")
	}
	if err := p.discard(); err == nil {
		t.Fatal("changed staging recursively deleted")
	}
	got, err := p.parent.ReadFile(filepath.Join(p.stage, "unmanaged"))
	if err != nil || string(got) != "keep" {
		t.Fatal("replacement staging data removed", err)
	}
}
func TestPublicationSyncRejectsSpecialFilesAndCancellation(t *testing.T) {
	p, _ := publicationFixture(t, false)
	if err := syscall.Mkfifo(filepath.Join(p.path(), "unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.sync(context.Background()); err == nil {
		t.Fatal("unexpected special file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("sync cancellation ignored", err)
	}
	if err := p.discard(); err != nil {
		t.Fatal(err)
	}
}
func TestRenamedCaptureSurvivesUncertainParentSync(t *testing.T) {
	p, target := publicationFixture(t, false)
	if err := p.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.rename(); err != nil {
		t.Fatal(err)
	}
	if err := p.parentFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.parentFile.Sync(); err == nil {
		t.Fatal("fault did not fail parent sync")
	}
	if err := p.discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "data/file")); err != nil {
		t.Fatal("uncertain published capture deleted", err)
	}
}
func TestCaptureRecordsPublishedArtifactForStoppedWorkloads(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	a.activeStates = map[string]bool{}
	destination := filepath.Join(t.TempDir(), "bundle")
	summary, err := c.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: backupRevision(t, c), Destination: destination}, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := c.ReadBackupRecovery()
	if err != nil {
		t.Fatal(err)
	}
	if status.Journal == nil || status.Journal.Artifact == nil || status.Journal.Artifact.Stage != "published" || status.Journal.Artifact.ManifestChecksum != summary.ManifestChecksum {
		t.Fatal("publication record missing", status)
	}
	if backupRecoveryPending(status.Journal) {
		t.Fatal("completed publication blocks future work")
	}
}
func TestInterruptedPublicationStaysPendingAfterWorkloadRecoveryAndCanBeReleased(t *testing.T) {
	c, a, _ := setupBackupFixture(t)
	status := pendingBackup(t, c, map[string]string{})
	p, _ := publicationFixture(t, false)
	artifact := p.artifact()
	artifact.Stage = "publishing"
	artifact.ManifestChecksum = strings.Repeat("a", 64)
	// The journal destination is pinned to the same parent as its staging record.
	status.Journal.Destination = filepath.Join(p.parentPath, p.destination)
	status.Journal.Artifact = artifact
	if err := c.writeBackupRecovery(status.Journal); err != nil {
		t.Fatal(err)
	}
	current, err := c.ReadBackupRecovery()
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := c.RecoverBackup(context.Background(), current.Journal.Revision, current.ReviewToken)
	if err != nil {
		t.Fatal(err)
	}
	if !backupRecoveryPending(recovered.Journal) {
		t.Fatal("workload-only recovery cleared uncertain publication")
	}
	released, err := c.ReleaseBackup(recovered.CurrentRevision, recovered.ReviewToken)
	if err != nil || backupRecoveryPending(released.Journal) || released.Journal.Artifact.Stage != "released" {
		t.Fatal(released, err)
	}
	if len(a.actions) != 0 {
		t.Fatal("publication review changed workloads")
	}
	if _, err := p.parent.Lstat(p.stage); err != nil {
		t.Fatal("release removed staged data", err)
	}
}

func TestPublicationJournalRejectsUnsafeLocationsAndInvalidProgress(t *testing.T) {
	for _, scenario := range []string{"outside parent", "invalid nonce", "missing identity", "missing checksum", "unknown stage"} {
		t.Run(scenario, func(t *testing.T) {
			c, _, _ := setupBackupFixture(t)
			p, _ := publicationFixture(t, false)
			j := BackupRecovery{Version: 1, Revision: backupRevision(t, c), Destination: filepath.Join(p.parentPath, p.destination), Stage: "complete", Services: map[string]string{}, Artifact: p.artifact()}
			switch scenario {
			case "outside parent":
				j.Artifact.StagingPath = filepath.Join(t.TempDir(), p.stage)
			case "invalid nonce":
				j.Artifact.StagingPath = filepath.Join(p.parentPath, ".staging-backup-"+strings.Repeat("z", 32))
			case "missing identity":
				j.Artifact.Inode = 0
			case "missing checksum":
				j.Artifact.Stage = "published"
			case "unknown stage":
				j.Artifact.Stage = "success"
			}
			if err := c.writeBackupRecovery(&j); err == nil {
				t.Fatal("invalid publication record accepted")
			}
		})
	}
}
