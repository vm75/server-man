package declarative

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// ReconcileBackupPublication verifies and durably completes only the recorded
// artifact. It never resumes workloads, deletes data or replaces a destination.
// Callers use the shared serialized management job boundary.
func (c Coordinator) ReconcileBackupPublication(ctx context.Context, revision, token string, deadlineSeconds int) (BackupRecoveryStatus, error) {
	deadline, err := backupTimeout(deadlineSeconds)
	if err != nil {
		return BackupRecoveryStatus{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	return c.reconcileBackupPublication(ctx, revision, token)
}

func (c Coordinator) reconcileBackupPublication(ctx context.Context, revision, token string) (BackupRecoveryStatus, error) {
	status, err := c.ReadBackupRecovery()
	if err != nil {
		return BackupRecoveryStatus{}, err
	}
	if status.Journal == nil || status.ReviewToken != token || status.CurrentRevision != revision {
		return BackupRecoveryStatus{}, ErrConflict
	}
	j := status.Journal
	a := j.Artifact
	if !backupArtifactPending(a) {
		return status, nil
	}
	if a.ManifestChecksum == "" {
		return BackupRecoveryStatus{}, errors.New("capture has no finalized manifest checksum; inspect the incomplete data before releasing recovery")
	}
	p, alreadyRenamed, err := openBackupReconciliation(*j)
	if err != nil {
		return BackupRecoveryStatus{}, err
	}
	defer p.close()
	root, err := p.parent.OpenRoot(p.stage)
	if err != nil {
		return BackupRecoveryStatus{}, errors.New("recorded capture cannot be opened for verification")
	}
	m, verifyErr := verifyBackupBundle(ctx, root, a.ManifestChecksum)
	root.Close()
	if verifyErr != nil {
		return BackupRecoveryStatus{}, verifyErr
	}
	if m.Revision != j.Revision {
		return BackupRecoveryStatus{}, errors.New("recorded capture configuration revision does not match its manifest")
	}
	if err := p.sync(ctx); err != nil {
		return BackupRecoveryStatus{}, err
	}
	if err := c.checkBackupReview(revision, token); err != nil {
		return BackupRecoveryStatus{}, err
	}
	// Persist intent even when rename already happened. Any subsequent failure
	// keeps publication pending for inspection rather than claiming success.
	a.Stage = "publishing"
	if err := c.writeBackupRecovery(j); err != nil {
		return BackupRecoveryStatus{}, err
	}
	raw, _ := json.Marshal(j)
	token = digest(raw)
	if err := ctx.Err(); err != nil {
		return BackupRecoveryStatus{}, err
	}
	if !alreadyRenamed {
		if err := p.rename(); err != nil {
			return BackupRecoveryStatus{}, err
		}
	}
	if err := p.parentFile.Sync(); err != nil {
		return BackupRecoveryStatus{}, err
	}
	if err := p.checkParent(); err != nil {
		return BackupRecoveryStatus{}, err
	}
	final, err := p.parent.Lstat(p.destination)
	if err != nil || !final.IsDir() || !os.SameFile(final, p.stageInfo) {
		return BackupRecoveryStatus{}, errors.New("capture destination identity changed during reconciliation")
	}
	if err := c.checkBackupReview(revision, token); err != nil {
		return BackupRecoveryStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return BackupRecoveryStatus{}, err
	}
	a.Stage = "published"
	if err := c.writeBackupRecovery(j); err != nil {
		return BackupRecoveryStatus{}, err
	}
	return c.ReadBackupRecovery()
}

func (c Coordinator) checkBackupReview(revision, token string) error {
	status, err := c.ReadBackupRecovery()
	if err != nil {
		return err
	}
	if status.ReviewToken != token || status.CurrentRevision != revision {
		return ErrConflict
	}
	return nil
}

func backupRecordedIdentity(info os.FileInfo, device, inode uint64) bool {
	if info == nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Dev) == device && stat.Ino == inode
}

func openBackupReconciliation(j BackupRecovery) (*backupPublication, bool, error) {
	a := j.Artifact
	parentPath := filepath.Dir(j.Destination)
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return nil, false, errors.New("recorded capture parent is unavailable")
	}
	dir, err := parent.Open(".")
	if err != nil {
		parent.Close()
		return nil, false, err
	}
	p := &backupPublication{parent: parent, parentFile: dir, parentPath: parentPath, stage: filepath.Base(a.StagingPath), destination: filepath.Base(j.Destination)}
	success := false
	defer func() {
		if !success {
			p.close()
		}
	}()
	p.parentInfo, err = dir.Stat()
	// The parent can be a normal non-private destination directory; its recorded
	// identity pins the namespace. The capture itself must remain private.
	if err != nil {
		return nil, false, err
	}
	stat, ok := p.parentInfo.Sys().(*syscall.Stat_t)
	if !ok || uint64(stat.Dev) != a.ParentDevice || stat.Ino != a.ParentInode {
		return nil, false, errors.New("recorded capture parent identity changed")
	}
	if err := p.checkParent(); err != nil {
		return nil, false, err
	}
	staged, stageErr := parent.Lstat(p.stage)
	final, finalErr := parent.Lstat(p.destination)
	alreadyRenamed := false
	switch {
	case stageErr == nil && backupRecordedIdentity(staged, a.Device, a.Inode) && os.IsNotExist(finalErr):
		p.stageInfo = staged
	case os.IsNotExist(stageErr) && finalErr == nil && backupRecordedIdentity(final, a.Device, a.Inode):
		p.stage, p.stageInfo, p.published = p.destination, final, true
		alreadyRenamed = true
	default:
		return nil, false, errors.New("capture location is missing, changed or conflicts with the destination; reconciliation never replaces or deletes data")
	}
	success = true
	return p, alreadyRenamed, nil
}
