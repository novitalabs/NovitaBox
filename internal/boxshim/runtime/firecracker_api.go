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
	firecrackerPathMachineConfig       = "/machine-config"
	firecrackerPathBootSource          = "/boot-source"
	firecrackerPathActions             = "/actions"
	firecrackerPathVM                  = "/vm"
	firecrackerPathBalloon             = "/balloon"
	firecrackerPathBalloonStats        = "/balloon/statistics"
	firecrackerPathBalloonHinting      = "/balloon/hinting/status"
	firecrackerPathBalloonHintingStart = "/balloon/hinting/start"
	firecrackerPathBalloonHintingStop  = "/balloon/hinting/stop"
	firecrackerPathMetrics             = "/metrics"
	firecrackerPathSnapshotLoad        = "/snapshot/load"
	firecrackerPathSnapshotCreate      = "/snapshot/create"
)

func firecrackerDrivePath(driveID string) string {
	return "/drives/" + driveID
}

func firecrackerNetworkInterfacePath(ifaceID string) string {
	return "/network-interfaces/" + ifaceID
}

type firecrackerClient struct {
	socketPath string
	baseURL    string
	httpClient *http.Client
}

func newFirecrackerClient(socketPath string) *firecrackerClient {
	return &firecrackerClient{
		socketPath: socketPath,
		baseURL:    "http://unix",
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

func (c *firecrackerClient) PutMetrics(ctx context.Context, path string) error {
	return c.put(ctx, firecrackerPathMetrics, firecrackerMetricsConfig{MetricsPath: path})
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

func (c *firecrackerClient) PutBalloon(ctx context.Context, req firecrackerBalloonConfig) error {
	return c.put(ctx, firecrackerPathBalloon, req)
}

func (c *firecrackerClient) UpdateBalloon(ctx context.Context, amountMiB uint32) error {
	return c.patch(ctx, firecrackerPathBalloon, firecrackerBalloonUpdate{AmountMiB: amountMiB})
}

func (c *firecrackerClient) GetBalloon(ctx context.Context) (*firecrackerBalloonConfig, error) {
	var out firecrackerBalloonConfig
	if err := c.get(ctx, firecrackerPathBalloon, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *firecrackerClient) GetBalloonStats(ctx context.Context) (*firecrackerBalloonStats, error) {
	var out firecrackerBalloonStats
	if err := c.get(ctx, firecrackerPathBalloonStats, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *firecrackerClient) UpdateBalloonStats(ctx context.Context, intervalSeconds uint32) error {
	return c.patch(ctx, firecrackerPathBalloonStats, firecrackerBalloonStatsUpdate{StatsPollingIntervalS: intervalSeconds})
}

func (c *firecrackerClient) StartBalloonHinting(ctx context.Context, acknowledgeOnStop bool) error {
	return c.patch(ctx, firecrackerPathBalloonHintingStart, firecrackerBalloonHintingConfig{AcknowledgeOnStop: acknowledgeOnStop})
}

func (c *firecrackerClient) StopBalloonHinting(ctx context.Context) error {
	return c.patch(ctx, firecrackerPathBalloonHintingStop, nil)
}

func (c *firecrackerClient) GetBalloonHinting(ctx context.Context) (*firecrackerBalloonHintingStatus, error) {
	var out firecrackerBalloonHintingStatus
	if err := c.get(ctx, firecrackerPathBalloonHinting, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *firecrackerClient) put(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPut, path, body, nil)
}

func (c *firecrackerClient) patch(ctx context.Context, path string, body any) error {
	return c.do(ctx, http.MethodPatch, path, body, nil)
}

func (c *firecrackerClient) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *firecrackerClient) do(ctx context.Context, method string, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal firecracker request: %w", err)
		}
		r = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
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
		if out == nil || resp.StatusCode == http.StatusNoContent {
			return nil
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode firecracker response: %w", err)
		}
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

type firecrackerMetricsConfig struct {
	MetricsPath string `json:"metrics_path"`
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

type firecrackerBalloonConfig struct {
	AmountMiB             uint32 `json:"amount_mib"`
	DeflateOnOOM          bool   `json:"deflate_on_oom"`
	StatsPollingIntervalS uint32 `json:"stats_polling_interval_s"`
	FreePageHinting       bool   `json:"free_page_hinting"`
	FreePageReporting     bool   `json:"free_page_reporting"`
}

type firecrackerBalloonUpdate struct {
	AmountMiB uint32 `json:"amount_mib"`
}

type firecrackerBalloonStatsUpdate struct {
	StatsPollingIntervalS uint32 `json:"stats_polling_interval_s"`
}

type firecrackerBalloonHintingConfig struct {
	AcknowledgeOnStop bool `json:"acknowledge_on_stop"`
}

type firecrackerBalloonHintingStatus struct {
	HostCmd  uint32  `json:"host_cmd"`
	GuestCmd *uint32 `json:"guest_cmd"`
}

type firecrackerBalloonStats struct {
	TargetMiB          uint32 `json:"target_mib"`
	ActualMiB          uint32 `json:"actual_mib"`
	SwapIn             uint64 `json:"swap_in"`
	SwapOut            uint64 `json:"swap_out"`
	MajorFaults        uint64 `json:"major_faults"`
	MinorFaults        uint64 `json:"minor_faults"`
	FreeMemory         uint64 `json:"free_memory"`
	TotalMemory        uint64 `json:"total_memory"`
	AvailableMemory    uint64 `json:"available_memory"`
	DiskCaches         uint64 `json:"disk_caches"`
	HugetlbAllocations uint64 `json:"hugetlb_allocations"`
	HugetlbFailures    uint64 `json:"hugetlb_failures"`
	SharedMemory       uint64 `json:"shared_memory"`
	UnevictableMemory  uint64 `json:"unevictable_memory"`
	OOMKill            uint64 `json:"oom_kill"`
	AllocStall         uint64 `json:"alloc_stall"`
	AsyncScan          uint64 `json:"async_scan"`
	DirectScan         uint64 `json:"direct_scan"`
	AsyncReclaim       uint64 `json:"async_reclaim"`
	DirectReclaim      uint64 `json:"direct_reclaim"`
}
