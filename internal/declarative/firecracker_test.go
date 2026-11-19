package declarative

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const firecrackerExample = `version: 1
storage:
  kernel:
    kind: file
    path: assets/vmlinux
  root:
    kind: file
    path: data/root.ext4
    format: raw
  extra:
    kind: file
    path: data/extra.img
    format: raw
bindings:
  lan:
    kind: bridge
    name: br-example
services:
  web:
    kind: vm
    backend: firecracker
    enabled: false
    vm:
      cpus: 2
      memory_mib: 512
      network: lan
      disks: [root, extra]
      mac: "02:00:00:00:12:34"
      uuid: "11111111-2222-4222-8222-333333333333"
      firecracker:
        kernel: kernel
`

func TestFirecrackerModelAndRenderer(t *testing.T) {
	c, err := Parse([]byte(firecrackerExample))
	if err != nil { t.Fatal(err) }
	out, err := RenderFirecracker(c, "service/web")
	if err != nil { t.Fatal(err) }
	var native FirecrackerConfig
	if err = json.Unmarshal(out, &native); err != nil { t.Fatal(err) }
	if native.BootSource.KernelImagePath != "/kernel" || native.BootSource.BootArgs != defaultFirecrackerBootArgs ||
		len(native.Drives) != 2 || native.Drives[0].PathOnHost != "/disks/root" || !native.Drives[0].IsRootDevice {
		t.Fatalf("invalid native configuration: %+v", native)
	}
	if native.NetworkInterfaces[0].HostDevName != firecrackerTAPName("11111111-2222-4222-8222-333333333333") || len(native.NetworkInterfaces[0].HostDevName) > 15 {
		t.Fatal("invalid TAP name")
	}
	again, err := RenderFirecracker(c, "service/web")
	if err != nil || string(again) != string(out) { t.Fatal("non-deterministic JSON", err) }
	encoded, err := Encode(c)
	if err != nil { t.Fatal(err) }
	if _, err = Parse(encoded); err != nil { t.Fatal("round-trip", err) }
	if !strings.Contains(strings.Join(serviceDataReferences(c.Services["web"]), ","), "storage/kernel") {
		t.Fatal("kernel missing from storage references")
	}
}

func TestFirecrackerRejectsInvalidProfiles(t *testing.T) {
	tests := []struct{name, old, replacement string}{
		{"qcow2", "format: raw", "format: qcow2"},
		{"missing kernel", "kernel: kernel", "kernel: missing"},
		{"no disks", "disks: [root, extra]", "disks: []"},
		{"duplicate disks", "disks: [root, extra]", "disks: [root, root]"},
		{"network kind", "kind: bridge", "kind: network"},
		{"firmware", "memory_mib: 512", "memory_mib: 512\n      firmware: uefi"},
		{"incus image", "memory_mib: 512", "memory_mib: 512\n      image: ubuntu"},
		{"memory", "memory_mib: 512", "memory_mib: 64"},
		{"duplicate kernel disk", "disks: [root, extra]", "disks: [kernel, extra]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(strings.Replace(firecrackerExample, tc.old, tc.replacement, 1))); err == nil {
				t.Fatal("unsupported profile accepted")
			}
		})
	}
}

func TestFirecrackerUnixAPI(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "firecracker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	var received []string
	var receivedMu sync.Mutex
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		receivedMu.Lock()
		received = append(received, req.Method+" "+req.URL.Path)
		receivedMu.Unlock()
		if req.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":"vm-test","state":"Running"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	defer server.Close()
	go func(){ _ = server.Serve(listener) }()
	api, err := NewFirecrackerAPI(socket)
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cfg := FirecrackerConfig{
		BootSource: firecrackerBootSource{KernelImagePath: "/kernel", BootArgs: "root=/dev/vda"},
		MachineConfig: firecrackerMachine{VCPUCount: 1, MemSizeMiB: 128},
		Drives: []firecrackerDrive{{DriveID:"rootfs", PathOnHost:"/disks/root", IsRootDevice:true}},
		NetworkInterfaces: []firecrackerNIC{{IfaceID:"eth0", HostDevName:"smfc0123456789", GuestMAC:"02:00:00:00:00:01"}},
	}
	if err := api.Configure(ctx,cfg); err != nil { t.Fatal(err) }
	if err := api.Start(ctx); err != nil { t.Fatal(err) }
	info, err := api.Info(ctx)
	if err != nil || info.State != "Running" { t.Fatal(info,err) }
	if err := api.RequestShutdown(ctx); err != nil { t.Fatal(err) }
	want := []string{"PUT /machine-config","PUT /boot-source","PUT /drives/rootfs","PUT /network-interfaces/eth0","PUT /actions","GET /","PUT /actions"}
	receivedMu.Lock()
	defer receivedMu.Unlock()
	if strings.Join(received,",") != strings.Join(want,",") {
		t.Fatalf("request ordering: %#v", received)
	}
}

func TestFirecrackerAPIBoundsAndPrivateDiagnostics(t *testing.T) {
	for _, socket := range []string{"relative.sock", "/tmp/../tmp/vm.sock", strings.Repeat("/x", 60)} {
		if _, err := NewFirecrackerAPI(socket); err == nil { t.Fatal("accepted unsafe socket path") }
	}
	socket := filepath.Join(t.TempDir(), "fc.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"fault_message":"private value /secret/disk"}`))
	})}
	defer server.Close()
	go func(){ _ = server.Serve(listener) }()
	api, err := NewFirecrackerAPI(socket)
	if err != nil { t.Fatal(err) }
	err = api.Start(context.Background())
	if err == nil || strings.Contains(err.Error(),"private") { t.Fatal("native error leaked",err) }
}

func TestFirecrackerUnavailableDoesNotPersistDesiredState(t *testing.T) {
	store := Store{Path: filepath.Join(t.TempDir(), "server-man.yaml")}
	c, err := Parse([]byte(firecrackerExample))
	if err != nil { t.Fatal(err) }
	coordinator := Coordinator{Store: store, Adapters: map[string]Adapter{}}
	if _, err := coordinator.Preview(context.Background(), c, "missing"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatal("unsupported backend should be blocked before save", err)
	}
	if _, err := coordinator.SaveApply(context.Background(), c, "missing", "now"); err == nil {
		t.Fatal("unsupported backend was saved")
	}
	if _, _, err := store.ReadRaw(); !os.IsNotExist(err) {
		t.Fatal("unsupported desired configuration was persisted", err)
	}
}
