package declarative

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
)

// Small private metadata is read through real held parents; payloads use the
// streaming verifier. Never return parser/OS diagnostics containing private data.
func readBackupPrivateFile(ctx context.Context, root *os.Root, path string, limit int, checksum string) ([]byte, error) {
	invalid := errors.New("private backup metadata is missing, unsafe, changed or invalid")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > backupMetadataReserve || checksum != "" && !revisionPattern.MatchString(checksum) {
		return nil, invalid
	}
	parent, name, err := backupRealParent(root, path, false)
	if err != nil {
		return nil, invalid
	}
	defer parent.Close()
	before, err := parent.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() <= 0 || before.Size() > int64(limit) {
		return nil, invalid
	}
	f, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, invalid
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !stableBackupNode(before, opened) {
		return nil, invalid
	}
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, int64(limit)+1))
	after, statErr := f.Stat()
	current, pathErr := parent.Lstat(name)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || statErr != nil || pathErr != nil || len(raw) > limit || int64(len(raw)) != before.Size() || !stableBackupNode(before, after) || !stableBackupNode(before, current) || checksum != "" && digest(raw) != checksum {
		return nil, invalid
	}
	currentParent, _, err := backupRealParent(root, path, false)
	if err != nil {
		return nil, invalid
	}
	defer currentParent.Close()
	held, heldErr := parent.Stat(".")
	resolved, resolvedErr := currentParent.Stat(".")
	if heldErr != nil || resolvedErr != nil || !os.SameFile(held, resolved) {
		return nil, invalid
	}
	return raw, nil
}
