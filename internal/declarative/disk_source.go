package declarative

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"server-man/internal/core"
)

const maxSourceBytes = int64(4096) << 30

type imageInfo struct {
	Format      string            `json:"format"`
	VirtualSize int64             `json:"virtual-size"`
	Backing     string            `json:"backing-filename"`
	Snapshots   []json.RawMessage `json:"snapshots"`
}

func descriptorPath(f *os.File) string { return fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), f.Fd()) }

// Check fixed header fields before a native tool can follow backing/data files.
// This is a rejection gate; native qemu-img remains the image format validator.
func checkStandaloneImage(f *os.File, format string) error {
	if format == "raw" {
		return nil
	}
	var header [104]byte
	if _, err := f.ReadAt(header[:], 0); err != nil {
		return errors.New("source qcow2 header is invalid")
	}
	version := binary.BigEndian.Uint32(header[4:8])
	if binary.BigEndian.Uint32(header[:4]) != 0x514649fb || (version != 2 && version != 3) || binary.BigEndian.Uint64(header[8:16]) != 0 || binary.BigEndian.Uint32(header[16:20]) != 0 || binary.BigEndian.Uint32(header[32:36]) != 0 {
		return errors.New("source qcow2 must be standalone and unencrypted; backing files are not accepted")
	}
	if version == 3 && binary.BigEndian.Uint64(header[72:80]) != 0 {
		return errors.New("source qcow2 has unsupported incompatible features, dirty state or external data")
	}
	return nil
}
func (p Provisioner) inspectImage(ctx context.Context, f *os.File, format string) (imageInfo, error) {
	return inspectStandaloneImage(ctx, p.Runner, p.QemuImg, f, format, maxSourceBytes)
}
func inspectStandaloneImage(ctx context.Context, runner core.Executor, program string, f *os.File, format string, maxVirtualSize int64) (imageInfo, error) {
	raw, err := runner.Capture(ctx, core.Command{Program: program, Args: []string{"info", "--output=json", "-f", format, descriptorPath(f)}, Timeout: 10 * time.Second})
	var image imageInfo
	if err != nil || json.Unmarshal([]byte(raw), &image) != nil || image.Format != format || image.VirtualSize <= 0 || image.VirtualSize > maxVirtualSize || image.Backing != "" {
		return imageInfo{}, errors.New("disk image cannot be verified as a supported standalone disk")
	}
	return image, nil
}
func (p Provisioner) openSource(ctx context.Context, source DiskSource) (*os.File, imageInfo, error) {
	root, err := os.OpenRoot(filepath.Dir(p.Coordinator.Store.Path))
	if err != nil {
		return nil, imageInfo{}, errors.New("image asset directory is unavailable")
	}
	defer root.Close()
	parent := filepath.Dir(source.Path)
	at := ""
	for _, part := range strings.Split(parent, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		info, e := root.Lstat(at)
		if e != nil || !info.IsDir() {
			return nil, imageInfo{}, errors.New("image assets require real parent directories without symlinks")
		}
	}
	f, err := root.OpenFile(source.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, imageInfo{}, errors.New("image asset is missing, inaccessible or unsafe")
	}
	success := false
	defer func() {
		if !success {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSourceBytes || info.Mode().Perm()&0022 != 0 {
		return nil, imageInfo{}, errors.New("image asset must be a bounded regular file that denies group/other writes")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 || (int(stat.Uid) != os.Geteuid() && stat.Uid != 0) {
		return nil, imageInfo{}, errors.New("image asset must be an independent file owned by the application account or root")
	}
	if err = checkStandaloneImage(f, source.Format); err != nil {
		return nil, imageInfo{}, err
	}
	image, err := p.inspectImage(ctx, f, source.Format)
	if err != nil {
		return nil, imageInfo{}, err
	}
	success = true
	return f, image, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func verifyImageChecksum(ctx context.Context, f *os.File, expected string) error {
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	hash := sha256.New()
	if count, err := io.Copy(hash, contextReader{ctx, io.LimitReader(f, maxSourceBytes+1)}); err != nil || count > maxSourceBytes {
		return errors.New("source image checksum could not complete")
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return errors.New("source image checksum does not match configuration")
	}
	return nil
}
func (p Provisioner) cloneSource(ctx context.Context, destination *os.File, plan ProvisionPlan) error {
	source, image, err := p.openSource(ctx, *plan.Source)
	if err != nil {
		return err
	}
	defer source.Close()
	if image.VirtualSize > int64(plan.SizeGiB)<<30 {
		return errors.New("destination is smaller than image")
	}
	if err = verifyImageChecksum(ctx, source, plan.Source.SHA256); err != nil {
		return err
	}
	if err = p.Runner.Run(ctx, core.Command{Program: p.QemuImg, Args: []string{"convert", "-n", "-f", plan.Source.Format, "-O", plan.Format, descriptorPath(source), descriptorPath(destination)}, Timeout: 20 * time.Minute}, io.Discard); err != nil {
		return errors.New("source image conversion failed")
	}
	if err = verifyImageChecksum(ctx, source, plan.Source.SHA256); err != nil {
		return err
	}
	actual, err := p.inspectImage(ctx, destination, plan.Format)
	if err != nil || actual.VirtualSize != int64(plan.SizeGiB)<<30 {
		return errors.New("provisioned image size or format did not verify")
	}
	return nil
}
