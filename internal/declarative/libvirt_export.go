package declarative

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"server-man/internal/core"
)

func (l *Libvirt) exportService(c Config, input BackupInput) (string, error) {
	if (l.URI != "qemu:///system" && l.URI != "qemu:///session") || l.Virsh == "" || l.Runner == nil {
		return "", errors.New("libvirt capture requires an available local libvirt connection")
	}
	if l.backupImageProgram() == "" {
		return "", errors.New("qemu-img is required for libvirt capture; install QEMU image tools before capture")
	}
	if input.Kind != "libvirt_guest_state" || input.Native == nil || input.Native.Backend != "libvirt" || len(input.Resources) != 1 {
		return "", errors.New("libvirt capture requires a resolved guest identity")
	}
	kind, id, ok := strings.Cut(input.Resources[0], "/")
	service, exists := c.Services[id]
	if !ok || kind != "service" || !exists || service.Kind != "vm" || service.Backend != "libvirt" || service.VM == nil || input.Native.Name != vmDomainName(id) || input.Native.Project != "" || input.Native.Pool != "" || input.Native.ContentType != "" {
		return "", errors.New("libvirt capture identity does not match the managed VM")
	}
	return id, nil
}
func (l *Libvirt) ValidateBackupExport(ctx context.Context, c Config, input BackupInput) error {
	id, err := l.exportService(c, input)
	if err != nil {
		return err
	}
	desired, err := RenderLibvirt(l.Store, c, input.Resources[0])
	if err != nil {
		return errors.New("libvirt hardware profile is not supported for capture")
	}
	actual, _, err := l.readDomainXMLFlags(ctx, c, input.Resources[0], true, true)
	if err != nil {
		return err
	}
	var want domainXML
	if xml.Unmarshal(desired, &want) != nil || !reflect.DeepEqual(want.Devices.Disks, actual.Devices.Disks) {
		return errors.New("libvirt disk definitions differ from declared storage; reconcile them before capture")
	}
	ordinary, err := l.readDomain(ctx, c, input.Resources[0], true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(ordinary.Devices.Disks, actual.Devices.Disks) {
		return errors.New("libvirt disk definitions changed between private and ordinary inspection")
	}
	deps, err := l.BackupDependencies(ctx, c, input.Resources[0])
	if err != nil {
		return err
	}
	for _, issue := range deps.Issues {
		if issue.Severity != "warning" {
			return errors.New("libvirt native dependencies are unresolved; review backup inventory")
		}
	}
	if len(deps.Inputs) != 0 {
		return errors.New("libvirt native state files need explicit capture support; NVRAM, TPM and disk-chain data must not be omitted")
	}
	if deps.State != "running" && deps.State != "stopped" {
		return errors.New("libvirt guest state is not stable for capture")
	}
	if err := l.checkBackupHistory(ctx, c.Services[id].VM.UUID); err != nil {
		return err
	}
	if _, err := l.Runner.Capture(ctx, core.Command{Program: l.backupImageProgram(), Args: []string{"--version"}, Timeout: 5 * time.Second}); err != nil {
		return errors.New("qemu-img is unavailable for libvirt disk verification")
	}
	return l.inspectBackupDisks(ctx, c, id, deps.State == "stopped")
}
func (l *Libvirt) checkBackupHistory(ctx context.Context, uuid string) error {
	cmd := l.command("list", "--all", "--uuid", "--with-managed-save")
	saved, err := l.Runner.Capture(ctx, cmd)
	if err != nil {
		return errors.New("libvirt managed-save inventory failed; absence cannot be assumed")
	}
	for _, other := range strings.Fields(saved) {
		if !uuidPattern.MatchString(strings.ToLower(other)) {
			return errors.New("libvirt managed-save inventory is invalid")
		}
		if strings.EqualFold(other, uuid) {
			return errors.New("libvirt managed-save data needs a reviewed state capture strategy; no saved state was removed")
		}
	}
	for _, operation := range []string{"snapshot-list", "checkpoint-list"} {
		cmd := l.command(operation, "--domain", uuid, "--name")
		names, err := l.Runner.Capture(ctx, cmd)
		if err != nil {
			return errors.New("libvirt snapshot/checkpoint inventory failed; absence cannot be assumed")
		}
		if strings.TrimSpace(names) != "" {
			return errors.New("libvirt snapshot/checkpoint data needs explicit capture support; native history was preserved")
		}
	}
	return nil
}

func (l *Libvirt) inspectBackupDisks(ctx context.Context, c Config, id string, physical bool) error {
	root, err := os.OpenRoot(filepath.Dir(l.Store.Path))
	if err != nil {
		return errors.New("libvirt disk root is unavailable")
	}
	defer root.Close()
	for _, diskID := range c.Services[id].VM.Disks {
		storage, ok := c.Storage[diskID]
		if !ok || storage.External || storage.Kind != "file" || !portablePath(storage.Path) || (storage.Format != "raw" && storage.Format != "qcow2") {
			return errors.New("libvirt disk needs declared owned raw/qcow2 file storage")
		}
		at := ""
		for _, part := range strings.Split(storage.Path, string(filepath.Separator)) {
			at = filepath.Join(at, part)
			info, e := root.Lstat(at)
			if e != nil || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("libvirt disk %s is missing, inaccessible or uses a symlink", diskID)
			}
		}
		entry, err := root.Lstat(storage.Path)
		if err != nil {
			return errors.New("libvirt disk metadata is unavailable")
		}
		f, err := root.OpenFile(storage.Path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return fmt.Errorf("libvirt disk %s cannot be read for capture", diskID)
		}
		err = func() error {
			defer f.Close()
			info, e := f.Stat()
			if e != nil || !info.Mode().IsRegular() || info.Size() <= 0 || !os.SameFile(entry, info) {
				return errors.New("libvirt disk must be a stable nonempty regular file")
			}
			if !physical {
				return nil
			}
			if e := checkStandaloneImage(f, storage.Format); e != nil {
				return e
			}
			image, e := inspectStandaloneImage(ctx, l.Runner, l.backupImageProgram(), f, storage.Format, math.MaxInt64)
			if e != nil {
				return e
			}
			if len(image.Snapshots) > 0 {
				return errors.New("internal disk snapshots need explicit native history capture support")
			}
			after, e := f.Stat()
			if e != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
				return errors.New("libvirt disk changed during physical inspection")
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("libvirt disk %s: %w", diskID, err)
		}
	}
	return nil
}

// Recheck an initially missing image tool after explicit host package setup.
// No shared adapter fields are mutated by concurrent reads.
func (l *Libvirt) backupImageProgram() string {
	if l.QemuImg != "" {
		return l.QemuImg
	}
	program, _ := exec.LookPath("qemu-img")
	return program
}
func (l *Libvirt) stoppedBackupXML(ctx context.Context, c Config, input BackupInput) ([]byte, error) {
	if err := l.ValidateBackupExport(ctx, c, input); err != nil {
		return nil, err
	}
	id, _ := l.exportService(c, input)
	state, err := l.state(ctx, c.Services[id].VM.UUID)
	if err != nil || state != "shut off" {
		return nil, errors.New("libvirt guest must be verified shut off for capture")
	}
	_, raw, err := l.readDomainXMLFlags(ctx, c, input.Resources[0], true, true)
	return raw, err
}
func (l *Libvirt) BackupExport(ctx context.Context, c Config, input BackupInput, directory string) (BackupExport, error) {
	raw, err := l.stoppedBackupXML(ctx, c, input)
	if err != nil {
		return BackupExport{}, err
	}
	path := "native/libvirt/" + digest([]byte(input.Native.Name)) + ".xml"
	export, err := writeBackupArtifact(ctx, directory, path, input.Kind, input.Native, func(writer io.Writer) error { _, err := io.Copy(writer, bytes.NewReader(raw)); return err })
	if err != nil {
		return BackupExport{}, err
	}
	latest, err := l.stoppedBackupXML(ctx, c, input)
	if err != nil || !bytes.Equal(latest, raw) {
		if err == nil {
			err = errors.New("libvirt private definition changed during capture")
		}
		return BackupExport{}, errors.Join(err, discardBackupArtifact(directory, path))
	}
	return export, nil
}
