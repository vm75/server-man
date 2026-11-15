package declarative

import (
	"bytes"
	"errors"
	"os"
	"runtime"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

const maxBackupXattrBytes = 64 << 10

// The frozen syscall package does not wrap the FD/no-follow xattr variants.
// Keep these Linux read-only bindings here; no attributes are applied to host
// files. FD variants pin regular files/directories, L variants pin the final
// symlink through its held parent descriptor.
func backupXattrCall(fd *os.File, path, name string, buffer []byte, list bool) (int, error) {
	var data unsafe.Pointer
	if len(buffer) > 0 {
		data = unsafe.Pointer(&buffer[0])
	}
	var result uintptr
	var errno syscall.Errno
	if fd != nil {
		if list {
			result, _, errno = syscall.Syscall(syscall.SYS_FLISTXATTR, fd.Fd(), uintptr(data), uintptr(len(buffer)))
		} else {
			attribute, err := syscall.BytePtrFromString(name)
			if err != nil {
				return 0, err
			}
			result, _, errno = syscall.Syscall6(syscall.SYS_FGETXATTR, fd.Fd(), uintptr(unsafe.Pointer(attribute)), uintptr(data), uintptr(len(buffer)), 0, 0)
			runtime.KeepAlive(attribute)
		}
	} else {
		location, err := syscall.BytePtrFromString(path)
		if err != nil {
			return 0, err
		}
		if list {
			result, _, errno = syscall.Syscall(syscall.SYS_LLISTXATTR, uintptr(unsafe.Pointer(location)), uintptr(data), uintptr(len(buffer)))
		} else {
			attribute, err := syscall.BytePtrFromString(name)
			if err != nil {
				return 0, err
			}
			result, _, errno = syscall.Syscall6(syscall.SYS_LGETXATTR, uintptr(unsafe.Pointer(location)), uintptr(unsafe.Pointer(attribute)), uintptr(data), uintptr(len(buffer)), 0, 0)
			runtime.KeepAlive(attribute)
		}
		runtime.KeepAlive(location)
	}
	runtime.KeepAlive(buffer)
	runtime.KeepAlive(fd)
	if errno != 0 {
		return 0, errno
	}
	return int(result), nil
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
