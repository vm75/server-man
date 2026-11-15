package declarative

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
)

// Exporters may report only real, bounded, private files in the owned staging
// root. A filename/checksum assertion is not proof that an export succeeded.
func verifyBackupExport(ctx context.Context, directory string, export BackupExport) error {
	if !portablePath(export.BundlePath) || !strings.HasPrefix(export.BundlePath, "native/") || export.SizeBytes < 0 || !revisionPattern.MatchString(export.SHA256) {
		return errors.New("native exporter returned an invalid artifact record")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	entry, err := root.Lstat(export.BundlePath)
	if err != nil || !entry.Mode().IsRegular() {
		return errors.New("native export artifact must be a real regular file")
	}
	f, err := root.OpenFile(export.BundlePath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("required native export artifact is missing or unsafe")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(entry, info) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != export.SizeBytes {
		return errors.New("native export artifact size, type or private permissions did not verify")
	}
	hash, err := hashBackupFile(ctx, f)
	if err != nil || hash != export.SHA256 {
		return errors.New("native export artifact checksum did not verify")
	}
	return nil
}
