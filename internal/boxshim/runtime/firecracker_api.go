package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

const (
	firecrackerPathMachineConfig  = "/machine-config"
	firecrackerPathBootSource     = "/boot-source"
	firecrackerPathActions        = "/actions"
	firecrackerPathVM             = "/vm"
	firecrackerPathSnapshotLoad   = "/snapshot/load"
	firecrackerPathSnapshotCreate = "/snapshot/create"
)

func firecrackerDrivePath(driveID string) string {
	return "/drives/" + driveID
}

func firecrackerNetworkInterfacePath(ifaceID string) string {
	return "/network-interfaces/" + ifaceID
}

type firecrackerClient struct {
	socketPath string
	httpClient *http.Client
}

func newFirecrackerClient(socketPath string) *firecrackerClient {
	return &firecrackerClient{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (c *firecrackerClient) PutMachineConfig(ctx context.Context, req firecrackerMachineConfig) error {
	return c.put(ctx, firecrackerPathMachineConfig, req)
}

func (c *firecrackerClient) PutBootSource(ctx context.Context, req firecrackerBootSource) error {
	return c.put(ctx, firecrackerPathBootSource, req)
}

func (c *firecrackerClient) PutDrive(ctx context.Context, req firecrackerDrive) error {
	return c.put(ctx, firecrackerDrivePath(req.DriveID), req)
}

func (c *firecrackerClient) PutNetworkInterface(ctx context.Context, req firecrackerNetworkInterface) error {
	return c.put(ctx, firecrackerNetworkInterfacePath(req.IfaceID), req)
}

func (c *firecrackerClient) StartInstance(ctx context.Context) error {
	return c.put(ctx, firecrackerPathActions, firecrackerActionRequest{ActionType: "InstanceStart"})
}

func (c *firecrackerClient) SendCtrlAltDel(ctx context.Context) error {
	return c.put(ctx, firecrackerPathActions, firecrackerActionRequest{ActionType: "SendCtrlAltDel"})
}

func (c *firecrackerClient) PauseVM(ctx context.Context) error {
	return c.patch(ctx, firecrackerPathVM, firecrackerVMStateRequest{State: "Paused"})
}

func (c *firecrackerClient) CreateSnapshot(ctx context.Context, req firecrackerSnapshotCreateRequest) error {
	return c.put(ctx, firecrackerPathSnapshotCreate, req)
}

func (c *firecrackerClient) LoadSnapshot(ctx context.Context, req firecrackerSnapshotLoadRequest) error {
	return c.put(ctx, firecrackerPathSnapshotLoad, req)
}

func (c *firecrackerClient) put(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPut, path, body)
}

func (c *firecrackerClient) patch(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPatch, path, body)
}

func (c *firecrackerClient) do(ctx context.Context, method string, path string, body any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal firecracker request: %w", err)
		}
		r = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://unix"+path, r)
	if err != nil {
		return fmt.Errorf("create firecracker request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("firecracker api %s %s failed: status=%d body=%s", method, path, resp.StatusCode, string(data))
}

type firecrackerMachineConfig struct {
	VCPUCount  int64 `json:"vcpu_count"`
	MemSizeMib int64 `json:"mem_size_mib"`
}

type firecrackerBootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args,omitempty"`
}

type firecrackerDrive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type firecrackerNetworkInterface struct {
	IfaceID     string `json:"iface_id"`
	HostDevName string `json:"host_dev_name"`
	GuestMAC    string `json:"guest_mac,omitempty"`
}

type firecrackerActionRequest struct {
	ActionType string `json:"action_type"`
}

type firecrackerVMStateRequest struct {
	State string `json:"state"`
}

type firecrackerSnapshotCreateRequest struct {
	SnapshotType string `json:"snapshot_type"`
	SnapshotPath string `json:"snapshot_path"`
	MemFilePath  string `json:"mem_file_path"`
}

type firecrackerMemBackend struct {
	BackendPath string `json:"backend_path"`
	BackendType string `json:"backend_type"`
}

type firecrackerSnapshotLoadRequest struct {
	SnapshotPath        string                `json:"snapshot_path"`
	MemBackend          firecrackerMemBackend `json:"mem_backend"`
	ResumeVM            bool                  `json:"resume_vm"`
	EnableDiffSnapshots bool                  `json:"enable_diff_snapshots"`
}
