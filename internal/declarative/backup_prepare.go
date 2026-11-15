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
	Resource   string              `json:"resource"`
	Backend    string              `json:"backend"`
	Kind       string              `json:"kind"`
	BundlePath string              `json:"bundlePath"`
	SizeBytes  int64               `json:"sizeBytes"`
	SHA256     string              `json:"sha256"`
	Native     *NativeBackupObject `json:"native,omitempty"`
}

type BackupManifest struct {
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
	BackupExport(ctx context.Context, c Config, key string, bundleDir string) ([]BackupExport, error)
}

func copyFileSparseAndHash(src, dst string, mode os.FileMode) (BackupManifestFile, error) {
	srcF, err := os.Open(src)
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
	dstF, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return BackupManifestFile{}, err
	}
	defer dstF.Close()

	hasher := sha256.New()
	buf := make([]byte, 128*1024)
	var sparseOffset int64

	for {
		n, rErr := srcF.Read(buf)
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
		dstPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
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
		count++
		if count > maxFiles {
			return errors.New("directory file limit exceeded during backup copy")
		}
		_ = os.MkdirAll(filepath.Dir(dstPath), 0700)
		fileEntry, err := copyFileSparseAndHash(path, dstPath, 0)
		if err != nil {
			return err
		}
		fileEntry.BundlePath = filepath.ToSlash(filepath.Join(bundlePrefix, rel))
		files = append(files, fileEntry)
		return nil
	})
	return files, err
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c Coordinator) PrepareBackup(ctx context.Context, opts BackupPrepareOptions, out io.Writer) (BackupSummary, error) {
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

	destExisted := false
	destInfo, err := os.Stat(cleanDest)
	if err == nil {
		destExisted = true
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
	if deadline <= 0 {
		deadline = 900
	}
	if deadline < 30 || deadline > 86400 {
		return BackupSummary{}, core.Invalid("deadlineSeconds must be between 30 and 86400")
	}
	bCtx, cancel := context.WithTimeout(ctx, time.Duration(deadline)*time.Second)
	defer cancel()

	j, err := c.ReadJournal()
	if err == nil && j.Stage != "complete" && j.Stage != "aborted" {
		return BackupSummary{}, errors.New("cannot prepare backup while an incomplete apply is pending")
	}

	snapshot, err := c.Store.Read()
	if err != nil {
		return BackupSummary{}, errors.New("a valid private configuration is required to prepare a backup")
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
		if issue.Severity == "error" {
			return BackupSummary{}, fmt.Errorf("backup blocked by unresolved issue on %s: %s", issue.Resource, issue.Message)
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
	if err := syscall.Statfs(targetFsDir, &fs); err == nil {
		if fs.Flags&1 != 0 {
			return BackupSummary{}, errors.New("destination filesystem is read-only")
		}
		avail := fs.Bavail * uint64(fs.Bsize)
		if avail < requiredBytes+(16<<20) {
			return BackupSummary{}, errors.New("insufficient disk space at destination")
		}
	}

	var stagingDir string
	if destExisted {
		stagingDir, err = os.MkdirTemp(cleanDest, ".staging-backup-*")
	} else {
		stagingDir, err = os.MkdirTemp(filepath.Dir(cleanDest), ".staging-backup-*")
	}
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
		if adapter != nil {
			obs, err := adapter.Inspect(bCtx, snapshot.Config, key)
			if err == nil && obs.Active {
				runningWorkloads = append(runningWorkloads, id)
			}
		}
	}
	slices.Sort(runningWorkloads)

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

	quiesced := map[string]bool{}
	var resumed []string
	var failedResume []string

	resumeWorkloads := func() {
		if len(quiesced) == 0 {
			return
		}
		resumeCtx, resumeCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer resumeCancel()
		for _, res := range ordered {
			if strings.HasPrefix(res, "service/") {
				id := strings.TrimPrefix(res, "service/")
				if quiesced[id] {
					key := "service/" + id
					adapter := c.Adapters[backend(snapshot.Config, key)]
					if life, ok := adapter.(LifecycleAdapter); ok {
						if err := life.Action(resumeCtx, snapshot.Config, key, "start"); err != nil {
							failedResume = append(failedResume, id)
						} else {
							resumed = append(resumed, id)
						}
					}
					delete(quiesced, id)
				}
			}
		}
	}
	defer resumeWorkloads()

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
			if err := life.Action(bCtx, snapshot.Config, key, "stop"); err != nil {
				return BackupSummary{}, fmt.Errorf("failed to quiesce service %s: %w", id, err)
			}
			quiesced[id] = true
		}
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
	if err := os.WriteFile(configDst, rawBytes, 0600); err != nil {
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
			} else if input.Kind == "file" || input.Kind == "private_file" {
				mode := os.FileMode(0)
				if input.Kind == "private_file" {
					mode = 0600
				}
				_ = os.MkdirAll(filepath.Dir(targetDst), 0700)
				fileEntry, err := copyFileSparseAndHash(input.Path, targetDst, mode)
				if err != nil {
					return BackupSummary{}, fmt.Errorf("copy file %s: %w", input.ID, err)
				}
				fileEntry.BundlePath = filepath.ToSlash(input.BundlePath)
				manifestFiles = append(manifestFiles, fileEntry)
			}
		} else if input.Strategy == "native_file" && input.Path != "" {
			targetDst := filepath.Join(stagingDir, input.BundlePath)
			_ = os.MkdirAll(filepath.Dir(targetDst), 0700)
			fileEntry, err := copyFileSparseAndHash(input.Path, targetDst, 0600)
			if err != nil {
				return BackupSummary{}, fmt.Errorf("copy native state file %s: %w", input.ID, err)
			}
			fileEntry.BundlePath = filepath.ToSlash(input.BundlePath)
			manifestFiles = append(manifestFiles, fileEntry)
		}
	}

	for id := range snapshot.Config.Services {
		key := "service/" + id
		adapter := c.Adapters[backend(snapshot.Config, key)]
		if exporter, ok := adapter.(BackupExporter); ok {
			exports, err := exporter.BackupExport(bCtx, snapshot.Config, key, stagingDir)
			if err != nil {
				return BackupSummary{}, fmt.Errorf("native export for %s: %w", id, err)
			}
			for _, exp := range exports {
				manifestNativeExports = append(manifestNativeExports, BackupManifestNativeExport{
					Resource:   key,
					Backend:    snapshot.Config.Services[id].Backend,
					Kind:       exp.Kind,
					BundlePath: exp.BundlePath,
					SizeBytes:  exp.SizeBytes,
					SHA256:     exp.SHA256,
					Native:     exp.Native,
				})
			}
		}
	}

	resumeWorkloads()

	manifest := BackupManifest{
		Version:                  1,
		Stage:                    "prepared",
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
	if err := os.WriteFile(manifestPath, manifestBytes, 0600); err != nil {
		return BackupSummary{}, fmt.Errorf("write manifest: %w", err)
	}
	manifestChecksum := digest(manifestBytes)

	if err := bCtx.Err(); err != nil {
		return BackupSummary{}, err
	}

	if destExisted {
		entries, err := os.ReadDir(stagingDir)
		if err != nil {
			return BackupSummary{}, err
		}
		for _, entry := range entries {
			src := filepath.Join(stagingDir, entry.Name())
			dst := filepath.Join(cleanDest, entry.Name())
			if err := os.Rename(src, dst); err != nil {
				return BackupSummary{}, fmt.Errorf("publish bundle entry %s: %w", entry.Name(), err)
			}
		}
		_ = os.Remove(stagingDir)
	} else {
		if err := os.Rename(stagingDir, cleanDest); err != nil {
			return BackupSummary{}, fmt.Errorf("publish bundle to destination: %w", err)
		}
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
		Stage:            "prepared",
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
