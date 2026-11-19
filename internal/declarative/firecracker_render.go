package declarative

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// FirecrackerConfig is the strictly typed supported subset of Firecracker's
// --config-file JSON. Paths refer to files inside an independently prepared jail;
// this renderer does not stage files, configure TAPs, or launch a microVM.
type FirecrackerConfig struct {
	BootSource        firecrackerBootSource `json:"boot-source"`
	MachineConfig     firecrackerMachine    `json:"machine-config"`
	Drives            []firecrackerDrive    `json:"drives"`
	NetworkInterfaces []firecrackerNIC      `json:"network-interfaces"`
}
type firecrackerBootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args"`
}
type firecrackerMachine struct {
	VCPUCount  int  `json:"vcpu_count"`
	MemSizeMiB int  `json:"mem_size_mib"`
	SMT        bool `json:"smt"`
}
type firecrackerDrive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}
type firecrackerNIC struct {
	IfaceID     string `json:"iface_id"`
	GuestMAC    string `json:"guest_mac"`
	HostDevName string `json:"host_dev_name"`
}

const defaultFirecrackerBootArgs = "console=ttyS0 reboot=k panic=1 root=/dev/vda rw"

// A bridge binding is an existing host network interface, not a free-form
// command fragment. Linux IFNAMSIZ leaves 15 bytes for the interface name.
var firecrackerBridgePattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$")

func validateFirecrackerVM(c Config, id string, v VM) error {
	if v.Firecracker == nil || !validID(v.Firecracker.Kernel) {
		return errors.New("Firecracker requires a kernel storage ID")
	}
	if v.Image != "" || v.Pool != "" || v.Project != "" || v.Firmware != "" || v.Machine != "" || v.RootSizeGiB != 0 {
		return errors.New("Firecracker does not support image aliases, pools, projects, firmware, machine or implicit root sizing")
	}
	if v.Architecture != "" {
		return errors.New("Firecracker uses the host architecture; architecture overrides are unsupported")
	}
	if len(v.Disks) == 0 || len(v.Disks) > 8 || v.CPUs < 1 || v.CPUs > 32 || v.MemoryMiB < 128 || v.MemoryMiB > 262144 {
		return errors.New("Firecracker requires one to eight raw disks, 1-32 CPUs and 128-262144 MiB memory")
	}
	if b, ok := c.Bindings[v.Network]; !ok || b.Kind != "bridge" || !firecrackerBridgePattern.MatchString(b.Name) {
		return errors.New("Firecracker requires a valid existing Linux bridge binding")
	}
	kernel, ok := c.Storage[v.Firecracker.Kernel]
	if !ok || kernel.Kind != "file" || kernel.Format != "" {
		return errors.New("Firecracker kernel must reference file storage without a disk format")
	}
	seen := map[string]bool{v.Firecracker.Kernel: true}
	for _, id := range v.Disks {
		st, ok := c.Storage[id]
		if !ok || st.Kind != "file" || st.Format != "raw" || seen[id] {
			return errors.New("Firecracker disks must be unique raw file storage references separate from the kernel")
		}
		seen[id] = true
	}
	for otherID, service := range c.Services {
		if otherID == id || service.VM == nil {
			continue
		}
		// The same raw writable backing file may not be attached to separate
		// managed VMs, including other VM backends.
		for _, id := range service.VM.Disks {
			if seen[id] && id != v.Firecracker.Kernel {
				return errors.New("Firecracker writable disk must not be shared with another managed VM")
			}
		}
	}
	if len(v.Firecracker.BootArgs) > 2048 || strings.ContainsAny(v.Firecracker.BootArgs, "\x00\r\n") {
		return errors.New("invalid Firecracker kernel boot arguments")
	}
	return nil
}

// TAP names stay below IFNAMSIZ and depend only on the stable resource ID.
// Native preparation must reject an existing foreign device with this name.
func firecrackerTAPName(id string) string {
	return "smfc" + digest([]byte(id))[:10]
}

// RenderFirecracker produces deterministic, schema-bounded JSON for a jailed
// Firecracker VMM. It must not be passed to an unjailed production process.
func RenderFirecracker(c Config, key string) ([]byte, error) {
	kind, id, ok := strings.Cut(key, "/")
	s, exists := c.Services[id]
	if !ok || kind != "service" || !exists || s.Kind != "vm" || s.Backend != "firecracker" || s.VM == nil {
		return nil, errors.New("resource is not a Firecracker VM")
	}
	v := s.VM
	if err := validateFirecrackerVM(c, id, *v); err != nil {
		return nil, err
	}
	if v.MAC == "" {
		return nil, errors.New("persist the VM identity before rendering native configuration")
	}
	args := v.Firecracker.BootArgs
	if args == "" {
		args = defaultFirecrackerBootArgs
	}
	cfg := FirecrackerConfig{
		BootSource: firecrackerBootSource{KernelImagePath: "/kernel", BootArgs: args},
		MachineConfig: firecrackerMachine{VCPUCount: v.CPUs, MemSizeMiB: v.MemoryMiB, SMT: false},
		Drives: make([]firecrackerDrive, 0, len(v.Disks)),
		NetworkInterfaces: []firecrackerNIC{{
			IfaceID: "eth0", GuestMAC: v.MAC, HostDevName: firecrackerTAPName(id),
		}},
	}
	for n := range v.Disks {
		name, path := "rootfs", "/disks/root"
		if n != 0 {
			name, path = fmt.Sprintf("disk%d", n), fmt.Sprintf("/disks/disk%d", n)
		}
		cfg.Drives = append(cfg.Drives, firecrackerDrive{
			DriveID: name, PathOnHost: path, IsRootDevice: n == 0, IsReadOnly: false,
		})
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, errors.New("Firecracker native serialization failed")
	}
	return append(out, '\n'), nil
}
