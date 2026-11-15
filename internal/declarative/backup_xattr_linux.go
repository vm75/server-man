package declarative

import (
	"bytes"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
	"unicode/utf8"
)

const maxBackupXattrBytes = 64 << 10

// Use maintained Linux FD/no-follow bindings; attributes are read only.
func backupXattrCall(fd *os.File, path, name string, buffer []byte, list bool) (int, error) {
	if fd != nil {
		if list {
			return unix.Flistxattr(int(fd.Fd()), buffer)
		}
		return unix.Fgetxattr(int(fd.Fd()), name, buffer)
	}
	if list {
		return unix.Llistxattr(path, buffer)
	}
	return unix.Lgetxattr(path, name, buffer)
}

func readBackupXattrs(fd *os.File, linkPath string) (map[string][]byte, string, error) {

	size, err := backupXattrCall(fd, linkPath, "", nil, true)
	if errors.Is(err, syscall.ENOTSUP) {
		return nil, "unsupported_source_fs", nil
	}
	if err != nil || size > maxBackupXattrBytes {
		return nil, "", errors.New("extended attribute names could not be read within the capture limit")
	}
	if size == 0 {
		return map[string][]byte{}, "visible", nil
	}
	buffer := make([]byte, size)
	n, err := backupXattrCall(fd, linkPath, "", buffer, true)
	if err != nil || n > len(buffer) {
		return nil, "", errors.New("extended attribute names changed during capture")
	}
	names := bytes.Split(buffer[:n], []byte{0})
	if len(names) > 257 {
		return nil, "", errors.New("extended attribute count exceeds capture limit")
	}
	attributes := map[string][]byte{}
	value := make([]byte, maxBackupXattrBytes)
	for _, name := range names {
		if len(name) == 0 {
			continue
		}
		if len(name) > 255 || !utf8.Valid(name) {
			return nil, "", errors.New("invalid extended attribute name")
		}
		count, err := backupXattrCall(fd, linkPath, string(name), value, false)
		if err != nil || count > len(value) {
			return nil, "", errors.New("extended attribute value changed or could not be read")
		}
		attributes[string(name)] = append([]byte(nil), value[:count]...)
	}
	// Linux hides privileged namespaces from some callers. Visible values include
	// readable ACLs/SELinux labels, but are not proof of complete privileged state.
	return attributes, "visible", nil
}
