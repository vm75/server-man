package declarative

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type libvirtBackupSource struct {
	File      string              `xml:"file,attr"`
	Dev       string              `xml:"dev,attr"`
	Protocol  string              `xml:"protocol,attr"`
	DataStore *libvirtBackupStore `xml:"dataStore"`
}
type libvirtBackupStore struct {
	Type    string              `xml:"type,attr"`
	Source  libvirtBackupSource `xml:"source"`
	Backing *libvirtBackupStore `xml:"backingStore"`
}
type libvirtBackupXML struct {
	Commandline    *struct{} `xml:"commandline"`
	LaunchSecurity *struct{} `xml:"launchSecurity"`
	Sysinfo        *struct{} `xml:"sysinfo"`
	OS             struct {
		Kernel   string    `xml:"kernel"`
		Initrd   string    `xml:"initrd"`
		DTB      string    `xml:"dtb"`
		Varstore *struct{} `xml:"varstore"`
		Loader   *struct {
			Path string `xml:",chardata"`
		} `xml:"loader"`
		NVRAM *struct {
			Type   string              `xml:"type,attr"`
			Path   string              `xml:",chardata"`
			Source libvirtBackupSource `xml:"source"`
		} `xml:"nvram"`
	} `xml:"os"`
	Devices struct {
		Disks []struct {
			Encryption *struct{}           `xml:"encryption"`
			Type       string              `xml:"type,attr"`
			Source     libvirtBackupSource `xml:"source"`
			Backing    *libvirtBackupStore `xml:"backingStore"`
		} `xml:"disk"`
		TPMs []struct {
			Backend struct {
				Type   string `xml:"type,attr"`
				Source struct {
					Type string `xml:"type,attr"`
					Path string `xml:"path,attr"`
				} `xml:"source"`
				Encryption *struct{} `xml:"encryption"`
			} `xml:"backend"`
		} `xml:"tpm"`
		Filesystems []struct{} `xml:"filesystem"`
		HostDevices []struct{} `xml:"hostdev"`
		Memory      []struct{} `xml:"memory"`
		Shmem       []struct{} `xml:"shmem"`
		Crypto      []struct{} `xml:"crypto"`
	} `xml:"devices"`
}

func (l *Libvirt) BackupDependencies(ctx context.Context, c Config, key string) (NativeBackupDependencies, error) {
	result := NativeBackupDependencies{}
	_, id, _ := strings.Cut(key, "/")
	service := c.Services[id]
	if service.VM == nil || service.Backend != "libvirt" || (l.URI != "qemu:///system" && l.URI != "qemu:///session") || l.Virsh == "" || l.Runner == nil {
		return result, errors.New("libvirt backup discovery is unavailable")
	}
	result.Object = &NativeBackupObject{Backend: "libvirt", Name: vmDomainName(id)}
	exists, err := l.exists(ctx, id, service.VM.UUID)
	if err != nil {
		return result, err
	}
	if !exists {
		result.State = "not_created"
		result.issue(key, "backup_guest_absent", "The configured libvirt guest is not registered.", "Review whether this is an unprovisioned definition or missing guest state before backup.")
		return result, nil
	}
	state, err := l.state(ctx, service.VM.UUID)
	if err != nil {
		return result, err
	}
	switch state {
	case "shut off":
		result.State = "stopped"
	case "running":
		result.State = "running"
	default:
		result.State = "unknown"
		result.issue(key, "backup_native_state", "The native libvirt guest is paused, transitional or in an unknown state.", "Inspect and stabilize the guest before capture.")
	}
	_, raw, err := l.readDomainXML(ctx, c, key, true)
	if err != nil {
		return result, err
	}
	definitions := [][]byte{raw}
	if state != "shut off" {
		_, live, e := l.readDomainXML(ctx, c, key, false)
		if e != nil {
			return result, e
		}
		if string(live) != string(raw) {
			definitions = append(definitions, live)
			result.require(key, "live_guest_state", "Live/persistent domain XML differ. Reinspect after graceful shutdown and preserve required active guest state before deciding snapshot contents.")
		}
	}

	paths := map[string]bool{}
	addPath := func(path, kind string) {
		if !nativeBackupPath(path) {
			result.issue(key, "backup_native_path", "A native guest state path cannot be safely inventoried.", "Inspect the owned domain's native storage and declare a portable backup strategy.")
			return
		}
		if len(paths) >= 128 {
			result.issue(key, "backup_dependency_limit", "Native guest state path count exceeds the inventory bound.", "Review an explicit native export strategy before completing backup.")
			return
		}
		if paths[path] {
			return
		}
		paths[path] = true
		result.Inputs = append(result.Inputs, BackupInput{ID: fmt.Sprintf("native/%s/state/%s", id, digest([]byte(path))[:16]), Kind: kind, Strategy: "native_file", Path: path, Resources: []string{key}, Required: true, Sensitive: true, Inspection: "native_path_discovered"})
	}
	declared := map[string]bool{}
	for _, disk := range service.VM.Disks {
		declared[filepath.Clean(l.Store.Resolve(c.Storage[disk].Path))] = true
	}
	var visitStore func(*libvirtBackupStore, int)
	visitSource := func(source libvirtBackupSource, kind string) {
		if source.File != "" {
			addPath(source.File, kind)
		} else if source.Dev != "" || source.Protocol != "" {
			result.issue(key, "backup_native_storage", "A native backing/data source is not a regular file.", "Provide an explicit export strategy for block/network storage before completing a backup.")
		}
	}
	visitStore = func(store *libvirtBackupStore, depth int) {
		if store == nil {
			return
		}
		if depth > 16 || len(paths) >= 128 {
			result.issue(key, "backup_dependency_limit", "Native disk dependency depth/count exceeds the inventory bound.", "Inspect and flatten/export the storage chain explicitly.")
			return
		}
		visitSource(store.Source, "libvirt_disk_dependency")
		visitStore(store.Source.DataStore, depth+1)
		visitStore(store.Backing, depth+1)
	}
	for _, rawDefinition := range definitions {
		var actual libvirtBackupXML
		if xml.Unmarshal(rawDefinition, &actual) != nil {
			return result, errors.New("unsupported libvirt backup XML")
		}
		if libvirtBackupUnsupportedBoot(actual) {
			result.issue(key, "backup_native_boot_state", "Native boot/security settings reference state outside the supported managed profile.", "Preserve and explicitly account for native boot/security dependencies before capture.")
		}
		if len(actual.Devices.Disks) > 64 || len(actual.Devices.TPMs) > 8 {
			return result, errors.New("libvirt backup device limit exceeded")
		}
		for _, disk := range actual.Devices.Disks {
			if disk.Source.File != "" {
				if !declared[filepath.Clean(disk.Source.File)] {
					addPath(disk.Source.File, "libvirt_undeclared_disk")
					result.issue(key, "backup_undeclared_disk", "The native domain references disk data outside its declared storage.", "Preserve this data and reconcile explicit storage references before backup/restore.")
				}
			} else {
				result.issue(key, "backup_native_storage", "A native disk uses storage outside the supported file model.", "Review the native block/network/volume source and its portable export requirements.")
			}
			if disk.Encryption != nil {
				result.require(key, "disk_encryption_credentials", "Encrypted native disk keys remain an explicit protected requirement; never expose secret values in inventory.")
				result.issue(key, "backup_disk_credentials", "Native disk encryption requires protected credentials outside the supported capture contract.", "Preserve encryption credentials through an explicitly reviewed native backup; never assume encrypted disk bytes alone are restorable.")
			}
			visitStore(disk.Source.DataStore, 0)
			visitStore(disk.Backing, 0)
		}
		if actual.OS.Loader != nil {
			result.require(key, "guest_firmware", "Review native firmware code/version and architecture on the target; firmware binaries remain host prerequisites.")
			if service.VM.Firmware != "uefi" {
				result.issue(key, "backup_native_firmware", "The native guest firmware is outside the current declared hardware profile.", "Preserve guest state and reconcile supported firmware settings before restore activation.")
			}
		}
		if nvram := actual.OS.NVRAM; nvram != nil {
			visitStore(nvram.Source.DataStore, 0)
			path := strings.TrimSpace(nvram.Path)
			if nvram.Source.File != "" {
				path = nvram.Source.File
			}
			if nvram.Type != "" && nvram.Type != "file" {
				result.issue(key, "backup_nvram_storage", "Guest NVRAM uses non-file native storage.", "Collect its native persistent state explicitly before declaring the backup complete.")
			} else {
				addPath(path, "libvirt_nvram")
			}
		}
		for _, tpm := range actual.Devices.TPMs {
			backend := tpm.Backend
			if backend.Type != "emulator" || (backend.Source.Type != "file" && backend.Source.Type != "dir") || backend.Source.Path == "" {
				result.issue(key, "backup_tpm_state", "The TPM's persistent state location cannot be established from the owned domain XML.", "Use explicit native TPM state capture; do not guess libvirt/swtpm internal paths.")
				continue
			}
			addPath(backend.Source.Path, "libvirt_tpm_state")
			if backend.Encryption != nil {
				result.require(key, "tpm_credentials", "TPM state encryption credentials remain an explicit protected native requirement; do not expose them through inventory.")
			}
		}
		if len(actual.Devices.Filesystems)+len(actual.Devices.HostDevices)+len(actual.Devices.Memory)+len(actual.Devices.Shmem)+len(actual.Devices.Crypto) > 0 {
			result.issue(key, "backup_native_devices", "Native shared/passthrough/persistent-memory devices require an explicit backup policy.", "Inventory these resources before completing a guest backup; they are not covered by ordinary disk references.")
		}
	}
	result.require(key, "native_state_capture", "Reinspect stopped guest XML/state and disk dependencies, capture private NVRAM/TPM metadata and validate native snapshots/managed-save state before a completed bundle.")
	return result, nil
}
