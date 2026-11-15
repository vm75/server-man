package declarative

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

// Both source capture and offline validation use the same modeled guest-state
// checks. Private XML is evidence, never an unreviewed native restore command.
func validateLibvirtBackupXML(ctx context.Context, raw []byte, cfg Config, id, sourceRoot string) error {
	invalid := errors.New("libvirt backup XML identity, hardware or guest-state coverage did not verify")
	if err := guardLibvirtBackupXML(ctx, raw); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return invalid
	}
	wantRaw, err := RenderLibvirt(Store{Path: filepath.Join(sourceRoot, "server-man.yaml")}, cfg, "service/"+id)
	if err != nil {
		return invalid
	}
	var want, actual domainXML
	var state libvirtBackupXML
	if xml.Unmarshal(wantRaw, &want) != nil || xml.Unmarshal(raw, &actual) != nil || xml.Unmarshal(raw, &state) != nil {
		return invalid
	}
	marker := actual.Metadata.Managed
	if !domainMatches(want, actual) || marker.XMLName.Space != libvirtMetadataNS || marker.ID != id || marker.Fingerprint != want.Metadata.Managed.Fingerprint || marker.Runtime != want.Metadata.Managed.Runtime {
		return invalid
	}
	if libvirtBackupUnsupportedBoot(state) || state.OS.Loader != nil || state.OS.NVRAM != nil || len(state.Devices.TPMs)+len(state.Devices.Filesystems)+len(state.Devices.HostDevices)+len(state.Devices.Memory)+len(state.Devices.Shmem)+len(state.Devices.Crypto) != 0 {
		return invalid
	}
	for _, disk := range state.Devices.Disks {
		if disk.Encryption != nil || disk.Source.DataStore != nil {
			return invalid
		}
		if b := disk.Backing; b != nil && (b.Type != "" || b.Source.File != "" || b.Source.Dev != "" || b.Source.Protocol != "" || b.Source.DataStore != nil || b.Backing != nil) {
			return invalid
		}
	}
	return ctx.Err()
}

func libvirtBackupUnsupportedBoot(actual libvirtBackupXML) bool {
	return actual.Commandline != nil || actual.LaunchSecurity != nil || actual.Sysinfo != nil || actual.OS.Kernel != "" || actual.OS.Initrd != "" || actual.OS.DTB != "" || actual.OS.Varstore != nil
}

type backupXMLFrame struct {
	name     xml.Name
	path     string
	children map[xml.Name]bool
}

// encoding/xml accepts duplicate attributes and repeated modeled scalar nodes.
// Reject ambiguity before decoding, as well as DTDs, multiple roots, namespace
// overrides outside metadata and excessive private XML work/depth.
func guardLibvirtBackupXML(ctx context.Context, raw []byte) error {
	invalid := errors.New("invalid or ambiguous private libvirt XML")
	if len(raw) == 0 || len(raw) > MaxConfigBytes {
		return invalid
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	stack := []backupXMLFrame{}
	roots, declarations := 0, 0
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if count >= 65536 {
			return invalid
		}
		token, err := decoder.Token()
		if err == io.EOF {
			if roots != 1 || len(stack) != 0 {
				return invalid
			}
			return nil
		}
		if err != nil {
			return invalid
		}
		switch token := token.(type) {
		case xml.StartElement:
			if len(stack) >= 64 {
				return invalid
			}
			path := token.Name.Local
			insideMetadata := len(stack) >= 2 && stack[1].path == "domain/metadata"
			if len(stack) == 0 {
				roots++
				if roots != 1 || token.Name != (xml.Name{Local: "domain"}) {
					return invalid
				}
			} else {
				parent := &stack[len(stack)-1]
				path = parent.path + "/" + token.Name.Local
				if !insideMetadata && token.Name.Space != "" {
					return invalid
				}
				if parent.children[token.Name] && backupXMLSingleton(parent.path, token.Name.Local) {
					return invalid
				}
				parent.children[token.Name] = true
				if parent.path == "domain/devices/disk/source" && token.Name.Local != "seclabel" {
					return invalid
				}
			}
			attributes := map[xml.Name]bool{}
			for _, attribute := range token.Attr {
				if (!insideMetadata || token.Name.Space == libvirtMetadataNS) && attribute.Name.Space != "" && attribute.Name.Space != "xmlns" {
					return invalid
				}
				if attributes[attribute.Name] {
					return invalid
				}
				attributes[attribute.Name] = true
				if path == "domain/devices/disk/source" && attribute.Name.Space == "" && attribute.Name.Local != "file" && attribute.Name.Local != "index" && attribute.Name.Local != "startupPolicy" {
					return invalid
				}
				if (path == "domain/devices/serial" || path == "domain/devices/console") && attribute.Name.Local == "type" && attribute.Value != "pty" && attribute.Value != "null" {
					return invalid
				}
			}
			stack = append(stack, backupXMLFrame{token.Name, path, map[xml.Name]bool{}})
		case xml.EndElement:
			if len(stack) == 0 {
				return invalid
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 && len(bytes.TrimSpace(token)) != 0 {
				return invalid
			}
		case xml.Directive:
			return invalid
		case xml.ProcInst:
			declarations++
			if token.Target != "xml" || roots != 0 || declarations > 1 {
				return invalid
			}
		}
	}
}

func backupXMLSingleton(parent, child string) bool {
	switch parent {
	case "domain":
		return child != "seclabel"
	case "domain/metadata", "domain/os", "domain/devices/disk", "domain/devices/interface", "domain/memory", "domain/vcpu":
		return true
	case "domain/devices":
		return child == "interface" || child == "console" || child == "emulator"
	}
	return strings.HasPrefix(parent, "domain/devices/disk/driver") || strings.HasPrefix(parent, "domain/devices/disk/target") || strings.HasPrefix(parent, "domain/devices/interface/mac") || strings.HasPrefix(parent, "domain/devices/interface/source") || strings.HasPrefix(parent, "domain/devices/interface/model")
}
