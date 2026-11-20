package declarative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// FirecrackerAPI is a bounded Unix-domain socket client, not a VM supervisor.
// A future privileged runner must validate socket directory ownership before
// using the client; socket presence alone never verifies microVM identity.
type FirecrackerAPI struct {
	client *http.Client
}

func NewFirecrackerAPI(socket string) (*FirecrackerAPI, error) {
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 100 || strings.ContainsAny(socket, "\x00\r\n") {
		return nil, errors.New("invalid Firecracker Unix socket path")
	}
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{
		DisableKeepAlives: true,
		MaxConnsPerHost: 1,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	return &FirecrackerAPI{client: &http.Client{Timeout: 5 * time.Second, Transport: transport}}, nil
}

func (a *FirecrackerAPI) call(ctx context.Context, method, endpoint string, value, destination any) error {
	if a == nil || a.client == nil {
		return errors.New("Firecracker API is unavailable")
	}
	var body io.Reader
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil || len(data) > 16*1024 {
			return errors.New("invalid Firecracker request")
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://firecracker"+endpoint, body)
	if err != nil {
		return errors.New("invalid Firecracker request")
	}
	if value != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return errors.New("Firecracker API socket is unavailable")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024+1))
	if err != nil || len(data) > 16*1024 {
		return errors.New("Firecracker API response is too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Native errors can mention storage paths; never echo the raw body.
		return errors.New("Firecracker rejected the management request")
	}
	if destination != nil && json.Unmarshal(data, destination) != nil {
		return errors.New("Firecracker returned invalid management metadata")
	}
	return nil
}

// Configure applies the supported typed resources to a *stopped*, already
// securely launched VMM. A native supervisor must verify that precondition.
func (a *FirecrackerAPI) Configure(ctx context.Context, cfg FirecrackerConfig) error {
	if len(cfg.Drives) == 0 || len(cfg.Drives) > 8 || len(cfg.NetworkInterfaces) != 1 ||
		cfg.MachineConfig.VCPUCount < 1 || cfg.MachineConfig.VCPUCount > 32 ||
		cfg.MachineConfig.MemSizeMiB < 128 || cfg.MachineConfig.MemSizeMiB > 262144 ||
		cfg.BootSource.KernelImagePath != "/kernel" || len(cfg.BootSource.BootArgs) > 2048 {
		return errors.New("unsupported Firecracker native configuration")
	}
	if err := a.call(ctx, http.MethodPut, "/machine-config", cfg.MachineConfig, nil); err != nil { return err }
	if err := a.call(ctx, http.MethodPut, "/boot-source", cfg.BootSource, nil); err != nil { return err }
	for n, disk := range cfg.Drives {
		expected := "rootfs"
		path := "/disks/root"
		if n != 0 {
			expected = fmt.Sprintf("disk%d", n)
			path = fmt.Sprintf("/disks/disk%d", n)
		}
		if disk.DriveID != expected || disk.PathOnHost != path || disk.IsRootDevice != (n == 0) {
			return errors.New("invalid Firecracker drive configuration")
		}
		if err := a.call(ctx, http.MethodPut, "/drives/"+disk.DriveID, disk, nil); err != nil { return err }
	}
	for _, nic := range cfg.NetworkInterfaces {
		if nic.IfaceID != "eth0" || len(nic.HostDevName) > 15 || !strings.HasPrefix(nic.HostDevName, "smfc") || nic.GuestMAC == "" {
			return errors.New("unsupported Firecracker network interface")
		}
		if err := a.call(ctx, http.MethodPut, "/network-interfaces/eth0", nic, nil); err != nil { return err }
	}
	return nil
}

func (a *FirecrackerAPI) Start(ctx context.Context) error {
	return a.call(ctx, http.MethodPut, "/actions", struct {
		Action string `json:"action_type"`
	}{Action: "InstanceStart"}, nil)
}
func (a *FirecrackerAPI) RequestShutdown(ctx context.Context) error {
	return a.call(ctx, http.MethodPut, "/actions", struct {
		Action string `json:"action_type"`
	}{Action: "SendCtrlAltDel"}, nil)
}

type FirecrackerInstanceInfo struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// Info must be combined with independently verified native ownership before
// being used in public workload status or lifecycle decisions.
func (a *FirecrackerAPI) Info(ctx context.Context) (FirecrackerInstanceInfo, error) {
	var result FirecrackerInstanceInfo
	err := a.call(ctx, http.MethodGet, "/", nil, &result)
	return result, err
}
