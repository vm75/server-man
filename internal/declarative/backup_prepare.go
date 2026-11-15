package declarative

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"server-man/internal/buildinfo"
	"server-man/internal/core"
)

type BackupPrepareOptions struct {
	Revision        string `json:"revision"`
	Destination     string `json:"destination"`
	Quiesce         bool   `json:"quiesce"`
	DeadlineSeconds int    `json:"deadlineSeconds,omitempty"`
}

type BackupManifestFile struct {
	BundlePath string `json:"bundlePath"`
	SizeBytes  int64  `json:"sizeBytes"`
	SHA256     string `json:"sha256"`
	Mode       string `json:"mode"`
	MTime      string `json:"mtime"`
}

type BackupManifestNativeExport struct {
	Resources  []string            `json:"resources"`
	Backend    string              `json:"backend"`
	Kind       string              `json:"kind"`
	BundlePath string              `json:"bundlePath"`
	SizeBytes  int64               `json:"sizeBytes"`
	SHA256     string              `json:"sha256"`
	Native     *NativeBackupObject `json:"native,omitempty"`
}

type BackupManifest struct {
	VerificationRequired     []string                     `json:"verificationRequired"`
	Version                  int                          `json:"version"`
	Stage                    string                       `json:"stage"`
	Revision                 string                       `json:"revision"`
	CreatedAt                time.Time                    `json:"createdAt"`
	ServerManVersion         string                       `json:"serverManVersion"`
	Files                    []BackupManifestFile         `json:"files"`
	NativeExports            []BackupManifestNativeExport `json:"nativeExports"`
	RunningWorkloadsAtBackup []string                     `json:"runningWorkloadsAtBackup"`
	ResumedWorkloads         []string                     `json:"resumedWorkloads"`
	FailedResume             []string                     `json:"failedResume,omitempty"`
}

type BackupSummary struct {
	Stage            string    `json:"stage"`
	Destination      string    `json:"destination"`
	Revision         string    `json:"revision"`
	PreparedAt       time.Time `json:"preparedAt"`
	TotalFiles       int       `json:"totalFiles"`
	TotalBytes       int64     `json:"totalBytes"`
	ManifestChecksum string    `json:"manifestChecksum"`
	Quiesced         bool      `json:"quiesced"`
	ResumedServices  []string  `json:"resumedServices,omitempty"`
	FailedResume     []string  `json:"failedResume,omitempty"`
}

type BackupExport struct {
	BundlePath string              `json:"bundlePath"`
	SizeBytes  int64               `json:"sizeBytes"`
	SHA256     string              `json:"sha256"`
	Kind       string              `json:"kind"`
	Native     *NativeBackupObject `json:"native,omitempty"`
}

type BackupExporter interface {
	ValidateBackupExport(context.Context, Config, BackupInput) error
	BackupExport(ctx context.Context, c Config, input BackupInput, bundleDir string) (BackupExport, error)
}

func copyBackupFile(ctx context.Context, src, dst string, mode os.FileMode) (BackupManifestFile, error) {
	srcF, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return BackupManifestFile{}, err
	}
	defer srcF.Close()
	info, err := srcF.Stat()
	if err != nil {
		return BackupManifestFile{}, err
	}
	if !info.Mode().IsRegular() {
		return BackupManifestFile{}, fmt.Errorf("source %s is not a regular file", src)
	}
	if mode == 0 {
		mode = info.Mode().Perm() & 0777
	}
	dstF, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return BackupManifestFile{}, err
	}
	defer dstF.Close()

	hasher := sha256.New()
	buf := make([]byte, 128*1024)
	var sparseOffset int64

	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return BackupManifestFile{}, err
		}
		n, rErr := srcF.Read(buf)
		copied += int64(n)
		if n > 0 {
			chunk := buf[:n]
			hasher.Write(chunk)

			isZero := true
			for _, b := range chunk {
				if b != 0 {
					isZero = false
					break
				}
			}
			if isZero {
				sparseOffset += int64(n)
			} else {
				if sparseOffset > 0 {
					if _, err := dstF.Seek(sparseOffset, io.SeekCurrent); err != nil {
						return BackupManifestFile{}, err
					}
					sparseOffset = 0
				}
				if _, err := dstF.Write(chunk); err != nil {
					return BackupManifestFile{}, err
				}
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			return BackupManifestFile{}, rErr
		}
	}
	latest, e := srcF.Stat()
	if e != nil || copied != info.Size() || latest.Size() != info.Size() || !latest.ModTime().Equal(info.ModTime()) {
		return BackupManifestFile{}, errors.New("source changed while copying backup data")
	}
	if err := dstF.Truncate(info.Size()); err != nil {
		return BackupManifestFile{}, err
	}
	if err := dstF.Sync(); err != nil {
		return BackupManifestFile{}, err
	}
	return BackupManifestFile{
		SizeBytes: info.Size(),
		SHA256:    hex.EncodeToString(hasher.Sum(nil)),
		Mode:      fmt.Sprintf("%04o", mode.Perm()),
		MTime:     info.ModTime().UTC().Format(time.RFC3339Nano),
	}, nil
}

func copyDirectory(ctx context.Context, srcDir, dstDir, bundlePrefix string, maxFiles int) ([]BackupManifestFile, error) {
	if err := os.MkdirAll(dstDir, 0700); err != nil {
		return nil, err
	}
	var files []BackupManifestFile
	count := 0
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		count++
		if count > maxFiles {
			return errors.New("directory entry limit exceeded during backup copy")
		}
		dstPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) {
				return errors.New("absolute symlinks cannot be relocated in a portable capture")
			}
			resolved := target
			if !filepath.IsAbs(target) {
				resolved = filepath.Join(filepath.Dir(path), target)
			}
			rRel, rErr := filepath.Rel(srcDir, resolved)
			if rErr != nil || strings.HasPrefix(rRel, "..") {
				return fmt.Errorf("symlink %s escapes declared directory", path)
			}
			_ = os.MkdirAll(filepath.Dir(dstPath), 0700)
			return os.Symlink(target, dstPath)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("unsupported file type for %s", path)
		}
		_ = os.MkdirAll(filepath.Dir(dstPath), 0700)
		fileEntry, err := copyBackupFile(ctx, path, dstPath, 0)
		if err != nil {
			return err
		}
		fileEntry.BundlePath = filepath.ToSlash(filepath.Join(bundlePrefix, rel))
		files = append(files, fileEntry)
		return nil
	})
	return files, err
}

func hashBackupFile(ctx context.Context, f *os.File) (string, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, contextReader{ctx, f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (c Coordinator) PrepareBackup(ctx context.Context, opts BackupPrepareOptions, out io.Writer) (summary BackupSummary, resultErr error) {
	if !revisionPattern.MatchString(opts.Revision) {
		return BackupSummary{}, core.Invalid("a reviewed configuration revision is required")
	}
	if err := c.checkBackupIdle(); err != nil {
		return BackupSummary{}, err
	}
	if opts.Destination == "" {
		return BackupSummary{}, core.Invalid("missing destination path")
	}
	cleanDest := filepath.Clean(opts.Destination)
	if !filepath.IsAbs(cleanDest) || cleanDest != opts.Destination {
		return BackupSummary{}, core.Invalid("destination must be an absolute cleaned path")
	}
	if cleanDest == "/" || strings.HasPrefix(cleanDest, "/proc") || strings.HasPrefix(cleanDest, "/sys") || strings.HasPrefix(cleanDest, "/dev") {
		return BackupSummary{}, core.Invalid("destination path cannot be a system directory")
	}
	rootPath := filepath.Dir(c.Store.Path)
	rel, err := filepath.Rel(rootPath, cleanDest)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
		return BackupSummary{}, core.Invalid("destination cannot be inside the configuration root directory")
	}

	parentReal, e := filepath.EvalSymlinks(filepath.Dir(cleanDest))
	if e != nil {
		return BackupSummary{}, core.Invalid("destination parent cannot be resolved")
	}
	sourceReal, e := filepath.EvalSymlinks(rootPath)
	if e != nil {
		return BackupSummary{}, core.Invalid("configuration root cannot be resolved")
	}
	relative, e := filepath.Rel(sourceReal, parentReal)
	if e == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, "../")) {
		return BackupSummary{}, core.Invalid("destination resolves inside the configuration root")
	}
	destExisted := false
	destInfo, err := os.Lstat(cleanDest)
	if err == nil {
		destExisted = true
		if !destInfo.IsDir() || destInfo.Mode().Perm()&0077 != 0 {
			return BackupSummary{}, core.Invalid("existing destination must be a private real directory")
		}
		if !destInfo.IsDir() {
			return BackupSummary{}, core.Invalid("destination exists and is not a directory")
		}
		entries, readErr := os.ReadDir(cleanDest)
		if readErr != nil {
			return BackupSummary{}, core.Invalid("destination directory cannot be read")
		}
		if len(entries) > 0 {
			return BackupSummary{}, core.Invalid("destination directory already exists and is not empty")
		}
	} else if !os.IsNotExist(err) {
		return BackupSummary{}, core.Invalid("destination path cannot be checked: %s", err.Error())
	} else {
		parentDir := filepath.Dir(cleanDest)
		parentInfo, pErr := os.Stat(parentDir)
		if pErr != nil || !parentInfo.IsDir() {
			return BackupSummary{}, core.Invalid("destination parent directory does not exist")
		}
	}

	deadline := opts.DeadlineSeconds
	if deadline == 0 {
		deadline = 900
	}
	if deadline < 30 || deadline > 86400 {
		return BackupSummary{}, core.Invalid("deadlineSeconds must be between 30 and 86400")
	}
	bCtx, cancel := context.WithTimeout(ctx, time.Duration(deadline)*time.Second)
	defer cancel()

	if err := c.checkApplyIdle(); err != nil {
		return BackupSummary{}, err
	}

	snapshot, err := c.Store.Read()
	if err != nil {
		return BackupSummary{}, errors.New("a valid private configuration is required to prepare a backup")
	}
	if snapshot.Revision != opts.Revision {
		return BackupSummary{}, ErrConflict
	}
	rawBytes, rev, err := c.Store.ReadRaw()
	if err != nil || rev != snapshot.Revision {
		return BackupSummary{}, ErrConflict
	}

	inventory, err := c.BackupInventory(bCtx)
	if err != nil {
		return BackupSummary{}, fmt.Errorf("backup inventory failed: %w", err)
	}
	for _, issue := range inventory.Issues {
		if issue.Severity != "warning" {
			return BackupSummary{}, fmt.Errorf("backup blocked by unresolved issue on %s: %s", issue.Resource, issue.Message)
		}
	}

	if inventory.Revision != snapshot.Revision {
		return BackupSummary{}, ErrConflict
	}
	for _, share := range snapshot.Config.Shares {
		if share.Enabled {
			return BackupSummary{}, errors.New("published shares require a verified client-quiescence strategy before backup; no data was captured")
		}
	}
	for _, input := range inventory.Inputs {
		if input.Strategy == "external_data" {
			return BackupSummary{}, errors.New("external data inclusion remains unresolved")
		}

		if input.Strategy == "native_export" && input.Required && input.IncludedBy == "" {
			if input.Native == nil {
				return BackupSummary{}, errors.New("native export identity is unresolved")
			}
			adapter, ok := c.Adapters[input.Native.Backend].(BackupExporter)
			if !ok {
				return BackupSummary{}, fmt.Errorf("native export support for %s is unavailable; capture is blocked", input.Native.Backend)
			}
			if err := adapter.ValidateBackupExport(bCtx, snapshot.Config, input); err != nil {
				return BackupSummary{}, err
			}
		}

		if input.Strategy == "native_file" {
			return BackupSummary{}, errors.New("native state paths require a verified capture strategy before backup")
		}
		if input.Strategy == "bundle_copy" && (input.BundlePath == "manifest.json" || input.BundlePath == "native" || strings.HasPrefix(input.BundlePath, "native/")) {
			return BackupSummary{}, errors.New("backup data paths collide with reserved bundle metadata/export paths")
		}
	}
	var requiredBytes uint64
	for _, input := range inventory.Inputs {
		if input.LogicalBytes != nil {
			requiredBytes += uint64(*input.LogicalBytes)
		}
	}
	targetFsDir := cleanDest
	if !destExisted {
		targetFsDir = filepath.Dir(cleanDest)
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(targetFsDir, &fs); err != nil {
		return BackupSummary{}, errors.New("destination filesystem capacity could not be checked")
	} else {
		if fs.Flags&1 != 0 {
			return BackupSummary{}, errors.New("destination filesystem is read-only")
		}
		avail := fs.Bavail * uint64(fs.Bsize)
		if avail < requiredBytes+(16<<20) {
			return BackupSummary{}, errors.New("insufficient disk space at destination")
		}
	}

	var stagingDir string
	stagingDir, err = os.MkdirTemp(filepath.Dir(cleanDest), ".staging-backup-*")
	if err != nil {
		return BackupSummary{}, fmt.Errorf("create staging directory: %w", err)
	}
	_ = os.Chmod(stagingDir, 0700)

	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	var runningWorkloads []string
	for id := range snapshot.Config.Services {
		key := "service/" + id
		adapter := c.Adapters[backend(snapshot.Config, key)]
		if adapter == nil {
			return BackupSummary{}, errors.New("every service needs an available backend to establish a consistent backup boundary")
		}
		obs, inspectErr := adapter.Inspect(bCtx, snapshot.Config, key)
		if inspectErr != nil {
			return BackupSummary{}, errors.New("service runtime state could not be established before backup")
		}
		if obs.Active && (!obs.Matches || !obs.Registered) {
			return BackupSummary{}, errors.New("resolve managed definition drift before quiescing running workloads for capture")
		}
		if obs.Active {
			runningWorkloads = append(runningWorkloads, id)
		}
	}
	slices.Sort(runningWorkloads)
	if !opts.Quiesce && len(runningWorkloads) > 0 {
		return BackupSummary{}, errors.New("running workloads must be quiesced; live copies are not consistent backups")
	}

	ordered := orderedResources(snapshot.Config)
	runningSet := map[string]bool{}
	for _, id := range runningWorkloads {
		runningSet[id] = true
	}
	var toStop []string
	for i := len(ordered) - 1; i >= 0; i-- {
		res := ordered[i]
		if strings.HasPrefix(res, "service/") {
			id := strings.TrimPrefix(res, "service/")
			if runningSet[id] {
				toStop = append(toStop, id)
			}
		}
	}

	var recovery *BackupRecovery
	if len(toStop) > 0 {
		recovery = &BackupRecovery{Version: 1, Revision: snapshot.Revision, Destination: cleanDest, Stage: "capturing", Services: map[string]string{}}
		for _, id := range runningWorkloads {
			recovery.Services[id] = "untouched"
		}
		if err := c.writeBackupRecovery(recovery); err != nil {
			return BackupSummary{}, err
		}
	}
	var resumed, failedResume []string
	var recoveryErr error
	recoveryDone := false
	resumeWorkloads := func() {
		if recovery == nil || recoveryDone {
			return
		}
		recoveryDone = true
		resumeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		resumed, failedResume, recoveryErr = c.resumeBackupWorkloads(resumeCtx, snapshot.Config, recovery)
	}
	defer func() { resumeWorkloads(); resultErr = errors.Join(resultErr, recoveryErr) }()

	if opts.Quiesce {
		for _, id := range toStop {
			if err := bCtx.Err(); err != nil {
				return BackupSummary{}, err
			}
			key := "service/" + id
			adapter := c.Adapters[backend(snapshot.Config, key)]
			life, ok := adapter.(LifecycleAdapter)
			if !ok {
				return BackupSummary{}, fmt.Errorf("service %s does not support lifecycle operations", id)
			}
			// A failed/cancelled stop may already have changed runtime state.
			// Include the attempted workload in recovery before issuing it.
			recovery.Services[id] = "stop_attempted"
			if err := c.writeBackupRecovery(recovery); err != nil {
				return BackupSummary{}, err
			}
			if err := life.Action(bCtx, snapshot.Config, key, "stop"); err != nil {
				return BackupSummary{}, fmt.Errorf("failed to quiesce service %s: %w", id, err)
			}
			observed, e := adapter.Inspect(bCtx, snapshot.Config, key)
			if e != nil || observed.Active {
				return BackupSummary{}, errors.New("workload did not reach a verified stopped state for backup")
			}
			recovery.Services[id] = "stopped"
			if err := c.writeBackupRecovery(recovery); err != nil {
				return BackupSummary{}, err
			}
		}
	}

	if err := c.verifyNativeCaptureBoundary(bCtx, inventory); err != nil {
		return BackupSummary{}, err
	}
	if err := os.MkdirAll(filepath.Join(stagingDir, "data"), 0700); err != nil {
		return BackupSummary{}, err
	}
	if err := os.MkdirAll(filepath.Join(stagingDir, "secrets"), 0700); err != nil {
		return BackupSummary{}, err
	}
	if err := os.MkdirAll(filepath.Join(stagingDir, "assets"), 0700); err != nil {
		return BackupSummary{}, err
	}
	if err := os.MkdirAll(filepath.Join(stagingDir, "native"), 0700); err != nil {
		return BackupSummary{}, err
	}

	var manifestFiles []BackupManifestFile
	var manifestNativeExports []BackupManifestNativeExport

	configDst := filepath.Join(stagingDir, "server-man.yaml")
	if err := writeBackupMetadata(configDst, rawBytes); err != nil {
		return BackupSummary{}, fmt.Errorf("write bundle configuration: %w", err)
	}
	configInfo, err := os.Stat(configDst)
	if err != nil {
		return BackupSummary{}, err
	}
	manifestFiles = append(manifestFiles, BackupManifestFile{
		BundlePath: "server-man.yaml",
		SizeBytes:  configInfo.Size(),
		SHA256:     snapshot.Revision,
		Mode:       "0600",
		MTime:      configInfo.ModTime().UTC().Format(time.RFC3339Nano),
	})

	for _, input := range inventory.Inputs {
		if err := bCtx.Err(); err != nil {
			return BackupSummary{}, err
		}
		if input.IncludedBy != "" {
			continue
		}
		if input.Strategy == "bundle_copy" {
			targetDst := filepath.Join(stagingDir, input.BundlePath)
			if input.Kind == "directory" {
				dirFiles, err := copyDirectory(bCtx, input.Path, targetDst, input.BundlePath, 50000)
				if err != nil {
					return BackupSummary{}, fmt.Errorf("copy directory %s: %w", input.ID, err)
				}
				manifestFiles = append(manifestFiles, dirFiles...)
			} else if input.Kind == "file" || input.Kind == "private_file" || input.Kind == "image_asset" {
				mode := os.FileMode(0)
				if input.Kind == "private_file" {
					mode = 0600
				}
				_ = os.MkdirAll(filepath.Dir(targetDst), 0700)
				fileEntry, err := copyBackupFile(bCtx, input.Path, targetDst, mode)
				if err != nil {
					return BackupSummary{}, fmt.Errorf("copy file %s: %w", input.ID, err)
				}
				if input.Kind == "image_asset" {
					_, storageID, _ := strings.Cut(input.ID, "/")
					source := snapshot.Config.Storage[storageID].Source
					if source == nil || fileEntry.SHA256 != source.SHA256 {
						return BackupSummary{}, errors.New("initial image checksum does not match configuration")
					}
				}
				fileEntry.BundlePath = filepath.ToSlash(input.BundlePath)
				manifestFiles = append(manifestFiles, fileEntry)
			}
		}
	}

	for _, input := range inventory.Inputs {
		if input.Strategy != "native_export" || input.IncludedBy != "" {
			continue
		}
		if input.Native == nil || len(input.Resources) == 0 {
			return BackupSummary{}, errors.New("native backup input identity is unresolved")
		}
		adapter, ok := c.Adapters[input.Native.Backend].(BackupExporter)
		if !ok {
			return BackupSummary{}, errors.New("required native exporter is unavailable")
		}
		expected := *input.Native
		exp, err := adapter.BackupExport(bCtx, snapshot.Config, input, stagingDir)
		if err != nil {
			return BackupSummary{}, fmt.Errorf("native export for %s: %w", input.ID, err)
		}
		if exp.Native == nil || *exp.Native != expected || exp.Kind != input.Kind {
			return BackupSummary{}, errors.New("native exporter returned a different object or kind")
		}
		if err := verifyBackupExport(bCtx, stagingDir, exp); err != nil {
			return BackupSummary{}, err
		}
		manifestNativeExports = append(manifestNativeExports, BackupManifestNativeExport{Resources: slices.Clone(input.Resources), Backend: expected.Backend, Kind: exp.Kind, BundlePath: exp.BundlePath, SizeBytes: exp.SizeBytes, SHA256: exp.SHA256, Native: exp.Native})
	}
	if err := c.verifyNativeCaptureBoundary(bCtx, inventory); err != nil {
		return BackupSummary{}, err
	}

	if _, current, e := c.Store.ReadRaw(); e != nil || current != snapshot.Revision {
		return BackupSummary{}, ErrConflict
	}
	resumeWorkloads()
	if recoveryErr != nil && len(failedResume) == 0 {
		return BackupSummary{}, recoveryErr
	}

	manifest := BackupManifest{
		VerificationRequired:     []string{"Owner/group, ACL, xattr/SELinux, directory and link metadata require complete capture/validation before this bundle can be marked prepared."},
		Version:                  1,
		Stage:                    "captured",
		Revision:                 snapshot.Revision,
		CreatedAt:                time.Now().UTC(),
		ServerManVersion:         buildinfo.Version,
		Files:                    manifestFiles,
		NativeExports:            manifestNativeExports,
		RunningWorkloadsAtBackup: runningWorkloads,
		ResumedWorkloads:         resumed,
		FailedResume:             failedResume,
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BackupSummary{}, fmt.Errorf("serialize manifest: %w", err)
	}
	manifestPath := filepath.Join(stagingDir, "manifest.json")
	if err := writeBackupMetadata(manifestPath, manifestBytes); err != nil {
		return BackupSummary{}, fmt.Errorf("write manifest: %w", err)
	}
	manifestChecksum := digest(manifestBytes)

	if err := bCtx.Err(); err != nil {
		return BackupSummary{}, err
	}

	// Linux rename atomically replaces an empty destination directory; os.Rename
	// rejects an existing directory before reaching that kernel operation.
	if err = syscall.Rename(stagingDir, cleanDest); err != nil {
		return BackupSummary{}, errors.New("bundle publication failed; destination must remain absent or empty")
	}
	directory, e := os.Open(filepath.Dir(cleanDest))
	if e != nil {
		return BackupSummary{}, errors.New("bundle published but parent sync failed; inspect destination before retry")
	}
	e = directory.Sync()
	directory.Close()
	if e != nil {
		return BackupSummary{}, errors.New("bundle published but parent sync failed; inspect destination before retry")
	}

	success = true

	totalFiles := len(manifest.Files) + len(manifest.NativeExports) + 1
	var totalBytes int64 = int64(len(manifestBytes))
	for _, f := range manifest.Files {
		totalBytes += f.SizeBytes
	}
	for _, e := range manifest.NativeExports {
		totalBytes += e.SizeBytes
	}

	return BackupSummary{
		Stage:            "captured",
		Destination:      cleanDest,
		Revision:         snapshot.Revision,
		PreparedAt:       manifest.CreatedAt,
		TotalFiles:       totalFiles,
		TotalBytes:       totalBytes,
		ManifestChecksum: manifestChecksum,
		Quiesced:         opts.Quiesce,
		ResumedServices:  resumed,
		FailedResume:     failedResume,
	}, nil
}

func writeBackupMetadata(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	return errors.Join(writeErr, f.Close())
}
