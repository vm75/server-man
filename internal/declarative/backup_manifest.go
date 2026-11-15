package declarative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// readBackupManifest reads only the private metadata leaf through a held bundle
// root. It returns no payloads and makes no assertion that they match the index.
func readBackupManifest(ctx context.Context, root *os.Root) (BackupManifest, string, error) {
	var manifest BackupManifest
	invalid := errors.New("private backup manifest is missing, unsafe or invalid")
	if err := ctx.Err(); err != nil {
		return manifest, "", err
	}
	before, err := root.Lstat("manifest.json")
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() <= 0 || before.Size() > backupMetadataReserve {
		return manifest, "", invalid
	}
	f, err := root.OpenFile("manifest.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return manifest, "", invalid
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !stableBackupNode(before, opened) {
		return manifest, "", invalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, backupMetadataReserve+1))
	after, statErr := f.Stat()
	current, pathErr := root.Lstat("manifest.json")
	if err != nil || statErr != nil || pathErr != nil || len(raw) > backupMetadataReserve || int64(len(raw)) != before.Size() || !stableBackupNode(before, after) || !stableBackupNode(before, current) {
		return manifest, "", invalid
	}
	if err := ctx.Err(); err != nil {
		return manifest, "", err
	}
	// encoding/json otherwise accepts duplicate keys and replaces invalid UTF-8.
	// Reject both before decoding typed metadata, with bounded nesting.
	if !utf8.Valid(raw) || uniqueBackupJSON(ctx, json.NewDecoder(bytes.NewReader(raw)), 0) != nil {
		return manifest, "", invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil {
		return BackupManifest{}, "", invalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return BackupManifest{}, "", invalid
	}
	if err := validateBackupManifest(ctx, manifest); err != nil {
		return BackupManifest{}, "", err
	}
	return manifest, digest(raw), nil
}

func uniqueBackupJSON(ctx context.Context, decoder *json.Decoder, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 16 {
		return errors.New("manifest JSON nesting limit exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("invalid manifest JSON container")
	}
	keys := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return errors.New("invalid or duplicate manifest JSON key")
			}
			keys[name] = true
		}
		if err := uniqueBackupJSON(ctx, decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

// validateBackupManifest validates metadata only. It does not establish payload
// integrity, capture consistency, native importability or restore readiness.
// Keep errors independent of private paths, attributes and configuration data.
func validateBackupManifest(ctx context.Context, m BackupManifest) error {
	invalid := errors.New("invalid private backup manifest")
	if m.Version != 1 || m.Stage != "captured" || !revisionPattern.MatchString(m.Revision) || m.CreatedAt.IsZero() || len(m.Files) == 0 || len(m.Files) > maxBackupEntries || len(m.NativeExports) > 2048 {
		return invalid
	}
	entries := make(map[string]BackupManifestFile, len(m.Files))
	paths := make(map[string]string, len(m.Files)+len(m.NativeExports))
	for _, f := range m.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !backupRelativePath(f.BundlePath) || f.BundlePath == "manifest.json" || strings.HasPrefix(f.BundlePath, "manifest.json/") || f.BundlePath == "native" || strings.HasPrefix(f.BundlePath, "native/") || paths[f.BundlePath] != "" || f.SizeBytes < 0 || f.AllocatedBytes < 0 || f.LinkCount == 0 {
			return invalid
		}
		mode, err := strconv.ParseUint(f.Mode, 8, 12)
		if err != nil || len(f.Mode) != 4 || mode > 07777 {
			return invalid
		}
		if _, err = time.Parse(time.RFC3339Nano, f.MTime); err != nil {
			return invalid
		}
		if _, err = time.Parse(time.RFC3339Nano, f.ATime); err != nil {
			return invalid
		}
		if f.XattrCoverage != "visible" && f.XattrCoverage != "unsupported_source_fs" || len(f.Xattrs) > 256 || f.XattrCoverage == "unsupported_source_fs" && len(f.Xattrs) != 0 {
			return invalid
		}
		names := 0
		for name, value := range f.Xattrs {
			names += len(name) + 1
			if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.ContainsRune(name, 0) || len(value) > maxBackupXattrBytes || names > maxBackupXattrBytes {
				return invalid
			}
		}
		switch f.Type {
		case "file", "hardlink":
			if !revisionPattern.MatchString(f.SHA256) || f.LinkTarget != "" || f.CapturedLinks == 0 || f.CapturedLinks > f.LinkCount || f.Type == "file" && f.HardlinkTo != "" || f.Type == "hardlink" && !backupRelativePath(f.HardlinkTo) {
				return invalid
			}
		case "directory", "symlink":
			if f.SizeBytes != 0 || f.SHA256 != "" || f.HardlinkTo != "" || f.CapturedLinks != 0 {
				return invalid
			}
			if f.Type == "directory" && f.LinkTarget != "" {
				return invalid
			}
			if f.Type == "symlink" && (f.LinkTarget == "" || filepath.IsAbs(f.LinkTarget) || !utf8.ValidString(f.LinkTarget) || strings.ContainsRune(f.LinkTarget, 0) || !backupRelativePath(filepath.Join(filepath.Dir(f.BundlePath), f.LinkTarget))) {
				return invalid
			}
		default:
			return invalid
		}
		entries[f.BundlePath] = f
		paths[f.BundlePath] = f.Type
	}
	config, ok := entries["server-man.yaml"]
	if !ok || config.Type != "file" || config.SHA256 != m.Revision {
		return invalid
	}
	for _, n := range m.NativeExports {
		if !backupRelativePath(n.BundlePath) || !strings.HasPrefix(n.BundlePath, "native/") || paths[n.BundlePath] != "" || n.SizeBytes < 0 || !revisionPattern.MatchString(n.SHA256) || n.Native == nil || n.Native.Name == "" || n.Native.Backend != n.Backend || n.Kind == "" || len(n.Resources) == 0 || len(n.Resources) > 2048 {
			return invalid
		}
		paths[n.BundlePath] = "file"
	}
	// Missing ancestors may be implicit private bundle directories. An explicitly
	// recorded ancestor must be a directory, never a file or a link.
	for path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		depth := 0
		for parent := filepath.Dir(path); parent != "."; parent = filepath.Dir(parent) {
			depth++
			if depth > maxBackupDepth+4 || paths[parent] != "" && paths[parent] != "directory" {
				return invalid
			}
		}
	}
	counts := map[string]uint64{}
	for _, f := range m.Files {
		if f.Type == "file" {
			counts[f.BundlePath]++
		} else if f.Type == "hardlink" {
			target, found := entries[f.HardlinkTo]
			if !found || target.Type != "file" {
				return invalid
			}
			// All names of one captured inode must describe the same metadata.
			// Reading through an account without O_NOATIME permission can update
			// access time between names. It is an observation, not inode drift.
			target.BundlePath, target.Type, target.HardlinkTo, target.ATime = f.BundlePath, f.Type, f.HardlinkTo, f.ATime
			if !reflect.DeepEqual(target, f) {
				return invalid
			}
			counts[f.HardlinkTo]++
		}
	}
	for _, f := range m.Files {
		if f.Type == "file" && counts[f.BundlePath] != f.CapturedLinks {
			return invalid
		}
	}
	return ctx.Err()
}
