package declarative

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type BackupRecovery struct {
	Artifact    *BackupArtifact `json:"artifact,omitempty"`
	Version     int             `json:"version"`
	Revision    string          `json:"revision"`
	Destination string          `json:"destination"`
	Stage       string          `json:"stage"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	// Only services observed running before capture appear here.
	Services map[string]string `json:"services"`
}
type BackupRecoveryStatus struct {
	Journal         *BackupRecovery `json:"journal,omitempty"`
	ReviewToken     string          `json:"reviewToken,omitempty"`
	CurrentRevision string          `json:"currentRevision,omitempty"`
}

func (c Coordinator) ReadBackupRecovery() (BackupRecoveryStatus, error) {
	var j BackupRecovery
	err := readPrivateJournal(c.Store.Path+".backup.json", &j)
	if os.IsNotExist(err) {
		return BackupRecoveryStatus{}, nil
	}
	if err != nil {
		return BackupRecoveryStatus{}, err
	}
	if err = validateBackupRecovery(j); err != nil {
		return BackupRecoveryStatus{}, err
	}
	raw, _ := json.Marshal(j)
	_, current, err := c.Store.ReadRaw()
	if os.IsNotExist(err) {
		current = "missing"
	} else if err != nil {
		return BackupRecoveryStatus{}, err
	}
	return BackupRecoveryStatus{Journal: &j, ReviewToken: digest(raw), CurrentRevision: current}, nil
}
func validateBackupRecovery(j BackupRecovery) error {
	if j.Version != 1 || !revisionPattern.MatchString(j.Revision) || j.UpdatedAt.IsZero() || !filepath.IsAbs(j.Destination) || filepath.Clean(j.Destination) != j.Destination || len(j.Destination) > 4096 || len(j.Services) > 128 {
		return errors.New("invalid backup recovery journal")
	}
	if j.Stage != "capturing" && j.Stage != "recovering" && j.Stage != "complete" && j.Stage != "released" {
		return errors.New("invalid backup recovery stage")
	}

	if a := j.Artifact; a != nil {
		switch a.Stage {
		case "building", "synced", "publishing", "published", "discarded", "uncertain", "released":
		default:
			return errors.New("invalid backup artifact stage")
		}
		name := filepath.Base(a.StagingPath)
		if !filepath.IsAbs(a.StagingPath) || filepath.Clean(a.StagingPath) != a.StagingPath || filepath.Dir(a.StagingPath) != filepath.Dir(j.Destination) || !strings.HasPrefix(name, ".staging-backup-") || len(strings.TrimPrefix(name, ".staging-backup-")) != 32 || a.Inode == 0 || a.ParentInode == 0 {
			return errors.New("invalid backup artifact identity")
		}
		if _, err := hex.DecodeString(strings.TrimPrefix(name, ".staging-backup-")); err != nil {
			return errors.New("invalid backup artifact location")
		}
		if a.ManifestChecksum != "" && !revisionPattern.MatchString(a.ManifestChecksum) {
			return errors.New("invalid backup manifest checksum")
		}
		if (a.Stage == "synced" || a.Stage == "publishing" || a.Stage == "published") && a.ManifestChecksum == "" {
			return errors.New("backup publication has no manifest checksum")
		}
	}
	for id, state := range j.Services {
		if !validID(id) {
			return errors.New("invalid backup recovery service")
		}
		switch state {
		case "untouched", "stop_attempted", "stop_uncertain", "stopped", "resumed", "failed":
		default:
			return errors.New("invalid backup recovery state")
		}
		if j.Stage == "complete" && state != "untouched" && state != "resumed" {
			return errors.New("incomplete backup recovery marked complete")
		}
	}
	return nil
}
func (c Coordinator) writeBackupRecovery(j *BackupRecovery) error {
	j.UpdatedAt = time.Now().UTC()
	if err := validateBackupRecovery(*j); err != nil {
		return err
	}
	return writePrivateJournal(c.Store.Path+".backup.json", j)
}
func (c Coordinator) checkBackupIdle() error {
	status, err := c.ReadBackupRecovery()
	if err != nil {
		return errors.New("backup recovery journal is invalid or inaccessible; inspect it before managing workloads")
	}
	if backupRecoveryPending(status.Journal) {
		return errors.New("backup recovery is pending; review recovery actions in Configuration")
	}
	return nil
}

func backupRecoveryPending(j *BackupRecovery) bool {
	return j != nil && (j.Stage != "complete" && j.Stage != "released" || backupArtifactPending(j.Artifact))
}

// ReleaseBackup acknowledges current runtime state without starting or stopping
// anything. It permits repair after external config drift or a deliberately
// stopped, unattempted dependency, without authorizing unsafe recovery replay.
func (c Coordinator) ReleaseBackup(revision, token string) (BackupRecoveryStatus, error) {
	status, err := c.ReadBackupRecovery()
	if err != nil {
		return status, err
	}
	if status.Journal == nil || token != status.ReviewToken || revision != status.CurrentRevision {
		return status, ErrConflict
	}
	if !backupRecoveryPending(status.Journal) {
		return status, nil
	}
	status.Journal.Stage = "released"
	if backupArtifactPending(status.Journal.Artifact) {
		status.Journal.Artifact.Stage = "released"
	}
	if err := c.writeBackupRecovery(status.Journal); err != nil {
		return status, err
	}
	return c.ReadBackupRecovery()
}

// RecoverBackup is explicit and revision/token pinned. Startup never resumes guests.
func (c Coordinator) RecoverBackup(ctx context.Context, revision, token string) (BackupRecoveryStatus, error) {
	status, err := c.ReadBackupRecovery()
	if err != nil {
		return status, err
	}
	if status.Journal == nil || token != status.ReviewToken || revision != status.Journal.Revision {
		return status, ErrConflict
	}
	snapshot, err := c.Store.Read()
	if err != nil || snapshot.Revision != revision {
		return status, ErrConflict
	}
	for id := range status.Journal.Services {
		if _, exists := snapshot.Config.Services[id]; !exists {
			return status, errors.New("backup recovery references an unknown service")
		}
	}
	if err := c.checkApplyIdle(); err != nil {
		return status, err
	}
	if !backupRecoveryPending(status.Journal) {
		return status, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, _, err = c.resumeBackupWorkloads(ctx, snapshot.Config, status.Journal)
	latest, readErr := c.ReadBackupRecovery()
	return latest, errors.Join(err, readErr)
}

// Shared by normal cleanup and explicit interrupted-operation recovery. Persist
// intent before actions and progress after verification, making retries inspect
// already active services rather than replaying starts blindly.
func (c Coordinator) resumeBackupWorkloads(ctx context.Context, cfg Config, j *BackupRecovery) (resumed, failed []string, resultErr error) {
	j.Stage = "recovering"
	if err := c.writeBackupRecovery(j); err != nil {
		return nil, nil, err
	}
	files := c.Store.CheckFiles(cfg)
	for _, key := range orderedResources(cfg) {
		if !strings.HasPrefix(key, "service/") {
			continue
		}
		id := strings.TrimPrefix(key, "service/")
		state := j.Services[id]
		if state == "" || state == "untouched" || state == "resumed" {
			continue
		}
		if _, rev, err := c.Store.ReadRaw(); err != nil || rev != j.Revision {
			return resumed, failed, ErrConflict
		}
		adapter := c.Adapters[backend(cfg, key)]
		life, ok := adapter.(LifecycleAdapter)
		success := false
		stopSettled := state != "stop_attempted" && state != "stop_uncertain"
		if ok && ctx.Err() == nil {
			observed, err := adapter.Inspect(ctx, cfg, key)
			if err == nil && observed.Registered && !observed.Active {
				stopSettled = true
				if state == "stop_attempted" || state == "stop_uncertain" {
					j.Services[id] = "stopped"
					if err := c.writeBackupRecovery(j); err != nil {
						return resumed, failed, err
					}
				}
			}
			// A timed-out shutdown can still complete after an active observation.
			// Do not claim recovery until that stop has been observed settled.
			if err == nil && observed.Active && stopSettled {
				success = true
			} else if err == nil && !observed.Active && observed.Matches && observed.Registered {
				ready := true
				for _, dependency := range cfg.Services[id].DependsOn {
					depKey := "service/" + dependency
					depAdapter := c.Adapters[backend(cfg, depKey)]
					if depAdapter == nil {
						ready = false
						break
					}
					dep, e := depAdapter.Inspect(ctx, cfg, depKey)
					if e != nil || !dep.Active || j.Services[dependency] == "failed" || j.Services[dependency] == "stop_uncertain" {
						ready = false
						break
					}
				}
				for _, issue := range files.Issues {
					if issue.Resource == key && issue.Severity != "warning" {
						ready = false
					}
				}
				if ready && life.Action(ctx, cfg, key, "start") == nil {
					active, e := adapter.Inspect(ctx, cfg, key)
					success = e == nil && active.Active
				}
			}
		}
		if success {
			j.Services[id] = "resumed"
			resumed = append(resumed, id)
		} else {
			j.Services[id] = "failed"
			if !stopSettled {
				j.Services[id] = "stop_uncertain"
			}
			failed = append(failed, id)
		}
		if err := c.writeBackupRecovery(j); err != nil {
			return resumed, failed, err
		}
	}
	if len(failed) > 0 {
		return resumed, failed, fmt.Errorf("previously running workloads need recovery: %s", strings.Join(failed, ", "))
	}
	j.Stage = "complete"
	if err := c.writeBackupRecovery(j); err != nil {
		return resumed, failed, err
	}
	return resumed, failed, nil
}
