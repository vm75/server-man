package declarative

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"server-man/internal/core"
)

type libvirtExportRecorder struct {
	*libvirtRecorder
	managedSave, snapshot, checkpoint, historyFailure, failQemu, physicalWhileRunning bool
	nativeQemu                                                                        bool
	imageBacking                                                                      string
	imageSnapshots                                                                    bool
	privateXML                                                                        string
	privateDumps                                                                      int
	restartAfterDump, changeAfterDump                                                 bool
	qemuCommands                                                                      []core.Command
}

func (r *libvirtExportRecorder) Capture(ctx context.Context, c core.Command) (string, error) {
	if strings.Contains(filepath.Base(c.Program), "qemu-img") {
		if r.failQemu {
			return "", errors.New("PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL")
		}
		if len(c.Args) > 0 && c.Args[0] == "--version" {
			return "qemu-img fixture", nil
		}
		r.qemuCommands = append(r.qemuCommands, c)
		if r.state != "shut off" {
			r.physicalWhileRunning = true
			return "", errors.New("physical image query while running")
		}
		if r.nativeQemu {
			return (core.Runner{}).Capture(ctx, c)
		}
		if len(c.Args) != 5 || c.Args[0] != "info" || c.Args[1] != "--output=json" || c.Args[2] != "-f" || !strings.HasPrefix(c.Args[4], "/proc/") {
			return "", errors.New("disk query did not use held descriptor and explicit format")
		}
		info, err := os.Stat(c.Args[4])
		if err != nil {
			return "", err
		}
		metadata := map[string]any{"format": c.Args[3], "virtual-size": info.Size(), "backing-filename": r.imageBacking}
		if r.imageSnapshots {
			metadata["snapshots"] = []any{map[string]any{"name": "private-snapshot"}}
		}
		raw, _ := json.Marshal(metadata)
		return string(raw), nil
	}
	if len(c.Args) > 2 {
		switch c.Args[2] {
		case "list":
			if slices.Contains(c.Args, "--with-managed-save") {
				r.commands = append(r.commands, c)
				if r.historyFailure {
					return "", errors.New("PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL")
				}
				if r.managedSave {
					var d domainXML
					_ = xml.Unmarshal([]byte(r.xml), &d)
					return d.UUID + "\n", nil
				}
				return "", nil
			}
		case "snapshot-list", "checkpoint-list":
			r.commands = append(r.commands, c)
			if r.historyFailure {
				return "", errors.New("PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL")
			}
			if c.Args[2] == "snapshot-list" && r.snapshot || c.Args[2] == "checkpoint-list" && r.checkpoint {
				return "private-history-name\n", nil
			}
			return "", nil
		case "dumpxml":
			if slices.Contains(c.Args, "--security-info") {
				r.commands = append(r.commands, c)
				r.privateDumps++
				raw := r.privateXML
				if raw == "" {
					raw = r.xml
				}
				if r.changeAfterDump && r.privateDumps > 2 {
					raw = strings.Replace(raw, "</domain>", "<description>changed</description></domain>", 1)
				}
				if r.restartAfterDump {
					r.state = "running"
				}
				return raw, nil
			}
		}
	}
	return r.libvirtRecorder.Capture(ctx, c)
}

func libvirtExportFixture(t *testing.T) (*Libvirt, Config, *libvirtExportRecorder, BackupInput) {
	t.Helper()
	l, c, base := libvirtFixture(t)
	disk := c.Storage["root"]
	disk.Format = "raw"
	c.Storage["root"] = disk
	if err := os.WriteFile(l.Store.Resolve(disk.Path), bytes.Repeat([]byte{0, 1, 2, 255}, 1024), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := RenderLibvirt(l.Store, c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	base.xml = string(raw)
	base.exists = true
	recorder := &libvirtExportRecorder{libvirtRecorder: base, privateXML: strings.Replace(string(raw), "</devices>", "<graphics type='vnc' passwd='PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL'/></devices>", 1)}
	l.Runner = recorder
	l.QemuImg = "qemu-img"
	input := BackupInput{ID: "native/guest", Kind: "libvirt_guest_state", Strategy: "native_export", Native: &NativeBackupObject{Backend: "libvirt", Name: vmDomainName("guest")}, Resources: []string{"service/guest"}, Required: true}
	return l, c, recorder, input
}

func TestLibvirtCapturesPrivateDefinitionAndDeclaredDiskThroughCoordinator(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "stopped", true: "running"}[running], func(t *testing.T) {
			l, c, r, _ := libvirtExportFixture(t)
			if running {
				r.state = "running"
				r.activeXML = r.xml
			}
			snapshot, err := l.Store.Save(c, "missing")
			if err != nil {
				t.Fatal(err)
			}
			coordinator := Coordinator{Store: l.Store, Adapters: map[string]Adapter{"libvirt": l}}
			destination := filepath.Join(t.TempDir(), "bundle")
			summary, err := coordinator.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: destination, Quiesce: true}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Stage != "captured" || r.physicalWhileRunning || len(r.qemuCommands) == 0 {
				t.Fatal("physical capture boundary not established", summary)
			}
			if running && (r.state != "running" || !slices.Equal(summary.ResumedServices, []string{"guest"})) {
				t.Fatal("running state not recovered", summary, r.state)
			}
			if !running && r.state != "shut off" {
				t.Fatal("stopped guest started")
			}
			raw, err := os.ReadFile(filepath.Join(destination, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest BackupManifest
			if err := json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			if len(manifest.NativeExports) != 1 || manifest.NativeExports[0].Kind != "libvirt_guest_state" {
				t.Fatal(manifest.NativeExports)
			}
			export := manifest.NativeExports[0]
			xml, err := os.ReadFile(filepath.Join(destination, export.BundlePath))
			if err != nil || string(xml) != r.privateXML {
				t.Fatal("private definition omitted/changed", err)
			}
			if export.SHA256 != digest(xml) || export.SizeBytes != int64(len(xml)) {
				t.Fatal("definition checksum mismatch")
			}
			source, err := os.ReadFile(l.Store.Resolve(c.Storage["root"].Path))
			if err != nil {
				t.Fatal(err)
			}
			copied, err := os.ReadFile(filepath.Join(destination, c.Storage["root"].Path))
			if err != nil || !bytes.Equal(source, copied) {
				t.Fatal("declared disk omitted/changed", err)
			}
			for _, cmd := range r.commands {
				if cmd.Program == l.Virsh && !slices.Contains(cmd.Env, "LC_ALL=C") {
					t.Fatal("libvirt output locale is ambient", cmd.Args)
				}
			}
			public, _ := json.Marshal(summary)
			if strings.Contains(string(public), "PRIVATE_NATIVE") {
				t.Fatal("private definition leaked into result")
			}
		})
	}
}

func TestLibvirtCaptureRejectsUnresolvedStateAndHistory(t *testing.T) {
	for _, scenario := range []string{"managed save", "snapshots", "checkpoints", "history unavailable", "paused", "remote URI", "missing executor", "missing qemu-img", "failed qemu-img", "missing disk", "disk symlink", "undeclared disk", "disk format drift", "encrypted disk", "TPM state", "NVRAM state", "native kernel", "qemu arguments", "physical backing", "internal snapshots"} {
		t.Run(scenario, func(t *testing.T) {
			l, c, r, input := libvirtExportFixture(t)
			switch scenario {
			case "managed save":
				r.managedSave = true
			case "snapshots":
				r.snapshot = true
			case "checkpoints":
				r.checkpoint = true
			case "history unavailable":
				r.historyFailure = true
			case "paused":
				r.state = "paused"
			case "remote URI":
				l.URI = "qemu+ssh://remote/system"
			case "missing executor":
				l.Runner = nil
			case "missing qemu-img":
				l.QemuImg = ""
				t.Setenv("PATH", t.TempDir())
			case "failed qemu-img":
				r.failQemu = true
			case "missing disk":
				if err := os.Remove(l.Store.Resolve(c.Storage["root"].Path)); err != nil {
					t.Fatal(err)
				}
			case "disk symlink":
				path := l.Store.Resolve(c.Storage["root"].Path)
				if err := os.Rename(path, path+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".real", path); err != nil {
					t.Fatal(err)
				}
			case "undeclared disk":
				r.xml = strings.Replace(r.xml, l.Store.Resolve(c.Storage["root"].Path), "/native/unmanaged.raw", 1)
			case "disk format drift":
				r.xml = strings.Replace(r.xml, `type="raw"`, `type="qcow2"`, 1)
			case "encrypted disk":
				r.xml = strings.Replace(r.xml, "</disk>", "<encryption format='luks'><secret type='passphrase' uuid='PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL'/></encryption></disk>", 1)
			case "TPM state":
				r.xml = strings.Replace(r.xml, "</devices>", "<tpm><backend type='emulator'><source type='file' path='/native/tpm.state'/></backend></tpm></devices>", 1)
			case "NVRAM state":
				r.xml = strings.Replace(r.xml, "</os>", "<nvram>/native/nvram.fd</nvram></os>", 1)
			case "native kernel":
				r.xml = strings.Replace(r.xml, "</os>", "<kernel>/native/kernel</kernel></os>", 1)
			case "qemu arguments":
				r.xml = strings.Replace(r.xml, "</domain>", "<commandline xmlns='http://libvirt.org/schemas/domain/qemu/1.0'><arg value='-drive'/></commandline></domain>", 1)
			case "physical backing":
				r.imageBacking = "/native/undisclosed-base.raw"
			case "internal snapshots":
				r.imageSnapshots = true
			}
			directory := privateExportDirectory(t)
			_, err := l.BackupExport(context.Background(), c, input, directory)
			if err == nil {
				t.Fatal("incomplete state capture accepted")
			}
			if strings.Contains(err.Error(), "PRIVATE_NATIVE_DIAGNOSTIC_SENTINEL") {
				t.Fatal("native diagnostics leaked")
			}
			files, _ := filepath.Glob(filepath.Join(directory, "native", "libvirt", "*"))
			if len(files) != 0 {
				t.Fatal("failed artifact remains", files)
			}
			for _, cmd := range r.commands {
				if len(cmd.Args) > 2 && (cmd.Args[2] == "shutdown" || cmd.Args[2] == "start" || cmd.Args[2] == "managedsave-remove") {
					t.Fatal("direct capture changed native lifecycle/history", cmd.Args)
				}
			}
		})
	}
}

func TestLibvirtCaptureRefusesStateChangesAfterPrivateDump(t *testing.T) {
	for _, restart := range []bool{true, false} {
		l, c, r, input := libvirtExportFixture(t)
		r.restartAfterDump = restart
		r.changeAfterDump = !restart
		directory := privateExportDirectory(t)
		if _, err := l.BackupExport(context.Background(), c, input, directory); err == nil {
			t.Fatal("state/definition change accepted")
		}
		files, _ := filepath.Glob(filepath.Join(directory, "native", "libvirt", "*"))
		if len(files) != 0 {
			t.Fatal("uncertain artifact remains")
		}
	}
}

func TestLibvirtDiskHeaderRejectsUndeclaredBackingBeforeNativeInspect(t *testing.T) {
	l, c, r, input := libvirtExportFixture(t)
	disk := c.Storage["root"]
	disk.Format = "qcow2"
	c.Storage["root"] = disk
	header := make([]byte, 104)
	binary.BigEndian.PutUint32(header, 0x514649fb)
	binary.BigEndian.PutUint32(header[4:], 3)
	binary.BigEndian.PutUint64(header[8:], 104)
	binary.BigEndian.PutUint32(header[16:], 32)
	if err := os.WriteFile(l.Store.Resolve(disk.Path), header, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := RenderLibvirt(l.Store, c, "service/guest")
	if err != nil {
		t.Fatal(err)
	}
	r.xml = string(raw)
	if _, err := l.BackupExport(context.Background(), c, input, privateExportDirectory(t)); err == nil {
		t.Fatal("hidden backing chain accepted")
	}
	if len(r.qemuCommands) != 0 {
		t.Fatal("native inspector could follow unreviewed backing data")
	}
}

func TestNativeQemuInspectionForTemporaryLibvirtCapture(t *testing.T) {
	program, err := exec.LookPath("qemu-img")
	if err != nil {
		t.Skip("qemu-img unavailable: native temporary-disk inspection is unverified")
	}
	for _, format := range []string{"raw", "qcow2"} {
		t.Run(format, func(t *testing.T) {
			l, c, r, input := libvirtExportFixture(t)
			disk := c.Storage["root"]
			disk.Format = format
			c.Storage["root"] = disk
			path := l.Store.Resolve(disk.Path)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := (core.Runner{}).Run(context.Background(), core.Command{Program: program, Args: []string{"create", "-f", format, path, "16M"}}, io.Discard); err != nil {
				t.Fatal(err)
			}
			raw, err := RenderLibvirt(l.Store, c, "service/guest")
			if err != nil {
				t.Fatal(err)
			}
			r.xml = string(raw)
			r.privateXML = ""
			l.QemuImg = program
			r.nativeQemu = true
			directory := privateExportDirectory(t)
			export, err := l.BackupExport(context.Background(), c, input, directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyBackupExport(context.Background(), directory, export); err != nil {
				t.Fatal(err)
			}
			if len(r.qemuCommands) < 2 {
				t.Fatal("native pre/post inspection was omitted")
			}
		})
	}
}

func TestLibvirtUncertainShutdownStaysPendingUntilStopIsObserved(t *testing.T) {
	l, c, r, _ := libvirtExportFixture(t)
	r.state = "running"
	r.activeXML = r.xml
	r.failShutdown = true
	snapshot, err := l.Store.Save(c, "missing")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{Store: l.Store, Adapters: map[string]Adapter{"libvirt": l}}
	if _, err := coordinator.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: filepath.Join(t.TempDir(), "bundle"), Quiesce: true}, io.Discard); err == nil {
		t.Fatal("failed shutdown reported successful capture")
	}
	status, err := coordinator.ReadBackupRecovery()
	if err != nil {
		t.Fatal(err)
	}
	if status.Journal.Stage == "complete" || status.Journal.Services["guest"] != "stop_uncertain" {
		t.Fatal("active observation incorrectly settled possible queued shutdown", status)
	}
	if _, err := coordinator.RecoverBackup(context.Background(), snapshot.Revision, status.ReviewToken); err == nil {
		t.Fatal("uncertain active shutdown was declared recovered")
	}
	// Simulate the earlier asynchronous request finally shutting down the guest.
	r.state = "shut off"
	status, err = coordinator.ReadBackupRecovery()
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := coordinator.RecoverBackup(context.Background(), snapshot.Revision, status.ReviewToken)
	if err != nil || recovered.Journal.Stage != "complete" || r.state != "running" {
		t.Fatal(recovered, err, r.state)
	}
	starts := 0
	for _, cmd := range r.commands {
		if len(cmd.Args) > 2 && cmd.Args[2] == "start" {
			starts++
		}
	}
	if starts != 1 {
		t.Fatal("uncertain shutdown replayed starts", starts)
	}
}

func TestLibvirtMissingCaptureToolBlocksBeforeShutdown(t *testing.T) {
	l, c, r, _ := libvirtExportFixture(t)
	r.state = "running"
	r.activeXML = r.xml
	r.failQemu = true
	snapshot, err := l.Store.Save(c, "missing")
	if err != nil {
		t.Fatal(err)
	}
	coordinator := Coordinator{Store: l.Store, Adapters: map[string]Adapter{"libvirt": l}}
	if _, err := coordinator.PrepareBackup(context.Background(), BackupPrepareOptions{Revision: snapshot.Revision, Destination: filepath.Join(t.TempDir(), "bundle"), Quiesce: true}, io.Discard); err == nil || !strings.Contains(err.Error(), "qemu-img") {
		t.Fatal("missing dependency not identified", err)
	}
	for _, cmd := range r.commands {
		if len(cmd.Args) > 2 && cmd.Args[2] == "shutdown" {
			t.Fatal("guest stopped before dependency check")
		}
	}
	if status, err := coordinator.ReadBackupRecovery(); err != nil || status.Journal != nil {
		t.Fatal("missing prerequisite started quiescence", status, err)
	}
}

func TestLibvirtCaptureRechecksInitiallyMissingImageTool(t *testing.T) {
	l, c, _, input := libvirtExportFixture(t)
	l.QemuImg = ""
	programs := t.TempDir()
	t.Setenv("PATH", programs)
	if err := l.ValidateBackupExport(context.Background(), c, input); err == nil || !strings.Contains(err.Error(), "qemu-img") {
		t.Fatal("missing image tool not reported", err)
	}
	// Simulate explicit package setup. The fixture runner records commands; this
	// placeholder is never executed and no host package is installed.
	if err := os.WriteFile(filepath.Join(programs, "qemu-img"), nil, 0700); err != nil {
		t.Fatal(err)
	}
	if err := l.ValidateBackupExport(context.Background(), c, input); err != nil {
		t.Fatal("new dependency still treated as missing", err)
	}
}
