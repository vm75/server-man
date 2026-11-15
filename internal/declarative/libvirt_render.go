package declarative

import (
	"encoding/xml"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var machineTypePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

const libvirtMetadataNS = "https://server-man.local/managed/v1"

type domainMarker struct {
	XMLName     xml.Name `xml:"https://server-man.local/managed/v1 managed"`
	ID          string   `xml:"id,attr"`
	Fingerprint string   `xml:"fingerprint,attr"`
	Runtime     string   `xml:"runtime,attr"`
}
type domainXML struct {
	XMLName  xml.Name `xml:"domain"`
	Type     string   `xml:"type,attr"`
	Name     string   `xml:"name"`
	UUID     string   `xml:"uuid"`
	Metadata struct {
		Managed domainMarker `xml:"managed"`
	} `xml:"metadata"`
	Memory struct {
		Unit  string `xml:"unit,attr"`
		Value int    `xml:",chardata"`
	} `xml:"memory"`
	VCPU int `xml:"vcpu"`
	OS   struct {
		Type struct {
			Arch    string `xml:"arch,attr"`
			Machine string `xml:"machine,attr"`
			Value   string `xml:",chardata"`
		} `xml:"type"`
		Boot struct {
			Dev string `xml:"dev,attr"`
		} `xml:"boot"`
	} `xml:"os"`
	Features struct {
		ACPI *struct{} `xml:"acpi"`
		APIC *struct{} `xml:"apic"`
	} `xml:"features"`
	CPU struct {
		Mode string `xml:"mode,attr"`
	} `xml:"cpu"`
	OnPoweroff string `xml:"on_poweroff"`
	OnReboot   string `xml:"on_reboot"`
	OnCrash    string `xml:"on_crash"`
	Devices    struct {
		Disks     []domainDisk    `xml:"disk"`
		Interface domainInterface `xml:"interface"`
		Console   struct {
			Type   string `xml:"type,attr"`
			Target struct {
				Type string `xml:"type,attr"`
				Port int    `xml:"port,attr"`
			} `xml:"target"`
		} `xml:"console"`
	} `xml:"devices"`
}
type domainDisk struct {
	Type   string `xml:"type,attr"`
	Device string `xml:"device,attr"`
	Driver struct {
		Name string `xml:"name,attr"`
		Type string `xml:"type,attr"`
	} `xml:"driver"`
	Source struct {
		File string `xml:"file,attr"`
	} `xml:"source"`
	Target struct {
		Dev string `xml:"dev,attr"`
		Bus string `xml:"bus,attr"`
	} `xml:"target"`
}
type domainInterface struct {
	Type string `xml:"type,attr"`
	MAC  struct {
		Address string `xml:"address,attr"`
	} `xml:"mac"`
	Source struct {
		Network string `xml:"network,attr,omitempty"`
		Bridge  string `xml:"bridge,attr,omitempty"`
	} `xml:"source"`
	Model struct {
		Type string `xml:"type,attr"`
	} `xml:"model"`
}

func vmDomainName(id string) string { return "sm-" + id }
func RenderLibvirt(store Store, c Config, key string) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	kind, id, ok := strings.Cut(key, "/")
	s, exists := c.Services[id]
	if !ok || kind != "service" || !exists || s.Kind != "vm" || s.Backend != "libvirt" {
		return nil, errors.New("resource is not a libvirt VM")
	}
	v := s.VM
	if v.Image != "" {
		return nil, errors.New("libvirt initial images must use disk storage source references")
	}
	if v.UUID == "" || v.MAC == "" {
		return nil, errors.New("persistent VM identity is required")
	}
	if v.RootSizeGiB != 0 {
		return nil, errors.New("libvirt root size requires an explicit disk provisioning workflow")
	}
	if v.Pool != "" || v.Project != "" {
		return nil, errors.New("libvirt existing-disk definitions do not use Incus pool/project bindings")
	}
	if len(v.Disks) == 0 {
		return nil, errors.New("provision VM image into declared disk storage before defining the guest")
	}
	if len(v.Disks) > 26 {
		return nil, errors.New("too many VM disks")
	}
	if v.Firmware != "" && v.Firmware != "bios" {
		return nil, errors.New("this initial libvirt adapter requires BIOS; UEFI support is pending")
	}
	arch := v.Architecture
	if arch == "" {
		arch = "x86_64"
	}
	if arch != "x86_64" {
		return nil, errors.New("unsupported libvirt guest architecture")
	}
	machine := v.Machine
	if machine == "" {
		machine = "q35"
	}
	if !machineTypePattern.MatchString(machine) {
		return nil, errors.New("invalid libvirt machine type")
	}
	if len(s.DependsOn) > 0 {
		return nil, errors.New("libvirt boot dependency integration is not implemented")
	}
	d := domainXML{Type: "kvm", Name: vmDomainName(id), UUID: v.UUID, VCPU: v.CPUs, OnPoweroff: "destroy", OnReboot: "restart", OnCrash: "destroy"}
	d.Metadata.Managed = domainMarker{ID: id, Fingerprint: resourceFingerprint(c, key), Runtime: runtimeFingerprint(c, key)}
	d.Memory.Unit = "MiB"
	d.Memory.Value = v.MemoryMiB
	d.OS.Type.Arch = arch
	d.OS.Type.Machine = machine
	d.OS.Type.Value = "hvm"
	d.OS.Boot.Dev = "hd"
	d.Features.ACPI = &struct{}{}
	d.Features.APIC = &struct{}{}
	d.CPU.Mode = "host-model"
	for i, ref := range v.Disks {
		st := c.Storage[ref]
		if st.Format != "qcow2" && st.Format != "raw" {
			return nil, errors.New("VM disks require an explicit qcow2 or raw format")
		}
		path := store.Resolve(st.Path)
		if !filepath.IsAbs(path) {
			return nil, errors.New("manifest path must be absolute")
		}
		disk := domainDisk{Type: "file", Device: "disk"}
		disk.Driver.Name = "qemu"
		disk.Driver.Type = st.Format
		disk.Source.File = path
		disk.Target.Dev = fmt.Sprintf("vd%c", 'a'+i)
		disk.Target.Bus = "virtio"
		d.Devices.Disks = append(d.Devices.Disks, disk)
	}
	binding := c.Bindings[v.Network]
	d.Devices.Interface.Type = binding.Kind
	d.Devices.Interface.MAC.Address = v.MAC
	d.Devices.Interface.Model.Type = "virtio"
	if binding.Kind == "network" {
		d.Devices.Interface.Source.Network = binding.Name
	} else {
		d.Devices.Interface.Source.Bridge = binding.Name
	}
	d.Devices.Console.Type = "pty"
	d.Devices.Console.Target.Type = "serial"
	bytes, err := xml.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte("<!-- Generated by server-man; edit the authoritative workload configuration. -->\n"), append(bytes, '\n')...), nil
}
