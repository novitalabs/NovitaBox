package handler

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/boxapi/response"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/sandbox"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type createSandboxRequest struct {
	AllowInternetAccess *bool                `json:"allow_internet_access,omitempty"`
	AutoPause           *bool                `json:"autoPause,omitempty"`
	AutoResume          *autoResumeConfig    `json:"autoResume,omitempty"`
	EnvVars             map[string]string    `json:"envVars,omitempty"`
	MCP                 map[string]any       `json:"mcp,omitempty"`
	Metadata            map[string]string    `json:"metadata,omitempty"`
	Network             map[string]any       `json:"network,omitempty"`
	GPU                 *uint32              `json:"gpu,omitempty"`
	Secure              *bool                `json:"secure,omitempty"`
	TemplateID          string               `json:"templateID"`
	Timeout             *int32               `json:"timeout,omitempty"`
	VolumeMounts        *[]map[string]any    `json:"volumeMounts,omitempty"`
	Rootfs              *rootfsSourceRequest `json:"rootfs,omitempty"`

	ImageID     string `json:"image_id,omitempty"`
	SnapshotID  string `json:"snapshot_id,omitempty"`
	RuntimeType string `json:"runtime_type,omitempty"`
}

type rootfsSourceRequest struct {
	Provider string `json:"provider"`
	Image    string `json:"image"`
	PullMode string `json:"pullMode,omitempty"`
}

type autoResumeConfig struct {
	Enabled bool `json:"enabled"`
}

type sandboxResponse struct {
	Alias              *string `json:"alias,omitempty"`
	ClientID           string  `json:"clientID"`
	Domain             *string `json:"domain"`
	EnvdAccessToken    *string `json:"envdAccessToken,omitempty"`
	EnvdVersion        string  `json:"envdVersion"`
	SandboxID          string  `json:"sandboxID"`
	TemplateID         string  `json:"templateID"`
	TrafficAccessToken *string `json:"trafficAccessToken"`
}

type sandboxInfoResponse struct {
	SandboxID     string              `json:"sandboxID"`
	TemplateID    string              `json:"templateID,omitempty"`
	ImageID       string              `json:"imageID,omitempty"`
	SnapshotID    string              `json:"snapshotID,omitempty"`
	State         string              `json:"state"`
	RuntimeType   string              `json:"runtimeType"`
	Rootfs        *rootfsInfoResponse `json:"rootfs,omitempty"`
	CreatedAtUnix int64               `json:"createdAtUnix,omitempty"`
	UpdatedAtUnix int64               `json:"updatedAtUnix,omitempty"`
}

type rootfsInfoResponse struct {
	Provider    string `json:"provider"`
	Image       string `json:"image,omitempty"`
	Digest      string `json:"digest,omitempty"`
	SnapshotKey string `json:"snapshotKey,omitempty"`
}

type sandboxListItemResponse struct {
	Alias        *string           `json:"alias,omitempty"`
	ClientID     string            `json:"clientID"`
	CPUCount     int32             `json:"cpuCount"`
	DiskSizeMB   int64             `json:"diskSizeMB"`
	EndAt        string            `json:"endAt"`
	EnvdVersion  string            `json:"envdVersion"`
	MemoryMB     int32             `json:"memoryMB"`
	Metadata     map[string]string `json:"metadata"`
	SandboxID    string            `json:"sandboxID"`
	StartedAt    string            `json:"startedAt"`
	State        string            `json:"state"`
	TemplateID   string            `json:"templateID"`
	VolumeMounts []any             `json:"volumeMounts"`
}

type snapshotResponse struct {
	SnapshotID    string `json:"snapshotID"`
	SandboxID     string `json:"sandboxID"`
	RootfsPath    string `json:"rootfsPath,omitempty"`
	MemfilePath   string `json:"memfilePath,omitempty"`
	SnapfilePath  string `json:"snapfilePath,omitempty"`
	CreatedAtUnix int64  `json:"createdAtUnix,omitempty"`
}

type updateBalloonRequest struct {
	AmountMiB *uint32 `json:"amountMiB" binding:"required"`
}

type updateBalloonStatsRequest struct {
	StatsPollingIntervalS *uint32 `json:"statsPollingIntervalS" binding:"required"`
}

type startBalloonHintingRequest struct {
	AcknowledgeOnStop *bool `json:"acknowledgeOnStop"`
}

type balloonConfigResponse struct {
	AmountMiB             uint32 `json:"amountMiB"`
	DeflateOnOOM          bool   `json:"deflateOnOOM"`
	StatsPollingIntervalS uint32 `json:"statsPollingIntervalS"`
	FreePageHinting       bool   `json:"freePageHinting"`
	FreePageReporting     bool   `json:"freePageReporting"`
}

type balloonStatsResponse struct {
	TargetMiB          uint32 `json:"targetMiB"`
	ActualMiB          uint32 `json:"actualMiB"`
	SwapIn             uint64 `json:"swapIn"`
	SwapOut            uint64 `json:"swapOut"`
	MajorFaults        uint64 `json:"majorFaults"`
	MinorFaults        uint64 `json:"minorFaults"`
	FreeMemory         uint64 `json:"freeMemory"`
	TotalMemory        uint64 `json:"totalMemory"`
	AvailableMemory    uint64 `json:"availableMemory"`
	DiskCaches         uint64 `json:"diskCaches"`
	HugetlbAllocations uint64 `json:"hugetlbAllocations"`
	HugetlbFailures    uint64 `json:"hugetlbFailures"`
	SharedMemory       uint64 `json:"sharedMemory"`
	UnevictableMemory  uint64 `json:"unevictableMemory"`
	OOMKill            uint64 `json:"oomKill"`
	AllocStall         uint64 `json:"allocStall"`
	AsyncScan          uint64 `json:"asyncScan"`
	DirectScan         uint64 `json:"directScan"`
	AsyncReclaim       uint64 `json:"asyncReclaim"`
	DirectReclaim      uint64 `json:"directReclaim"`
}

type balloonHintingResponse struct {
	State    string  `json:"state"`
	HostCmd  uint32  `json:"hostCmd"`
	GuestCmd *uint32 `json:"guestCmd,omitempty"`
}

func (h *Handler) CreateSandbox(c *gin.Context) {
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	var req createSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.ErrBadRequest("invalid sandbox request body"))
		return
	}
	if req.TemplateID == "" && req.ImageID == "" && req.SnapshotID == "" && req.Rootfs == nil {
		response.Error(c, response.ErrBadRequest("templateID, imageID, snapshotID, or rootfs is required"))
		return
	}

	sandboxID, err := newSandboxID()
	if err != nil {
		response.Error(c, response.ErrInternal("generate sandbox id failed"))
		return
	}

	if existing, err := h.store.GetSandbox(c.Request.Context(), sandboxID); err == nil && existing != nil {
		response.Error(c, response.ErrConflict("sandbox already exists"))
		return
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		response.Error(c, response.ErrInternal("check sandbox failed"))
		return
	}

	runtimeType := req.RuntimeType
	if runtimeType == "" {
		runtimeType = "firecracker"
	}
	if req.Rootfs != nil {
		if !strings.EqualFold(req.Rootfs.Provider, "overlaybd") {
			response.Error(c, response.ErrBadRequest("rootfs.provider must be overlaybd"))
			return
		}
		if strings.TrimSpace(req.Rootfs.Image) == "" {
			response.Error(c, response.ErrBadRequest("rootfs.image is required"))
			return
		}
		if !strings.EqualFold(runtimeType, "gvisor") {
			response.Error(c, response.ErrBadRequest("overlaybd rootfs requires runtime_type gvisor"))
			return
		}
	}

	if h.sandboxClient != nil {
		created, err := h.sandboxClient.CreateSandbox(c.Request.Context(), &novitaboxv1.CreateSandboxRequest{
			SandboxId:    sandboxID,
			TemplateId:   req.TemplateID,
			ImageId:      req.ImageID,
			SnapshotId:   req.SnapshotID,
			RuntimeType:  runtimeTypeToProto(runtimeType),
			RootfsSource: rootfsSourceToProto(req.Rootfs),
			RuntimeSpec: &novitaboxv1.RuntimeSpec{
				SandboxId:   sandboxID,
				RuntimeType: runtimeTypeToProto(runtimeType),
				Machine: func() *novitaboxv1.MachineSpec {
					if req.GPU == nil {
						return nil
					}
					return &novitaboxv1.MachineSpec{Gpu: *req.GPU}
				}(),
			},
		})
		if err != nil {
			h.respondSandboxBoxletError(c, err, "create sandbox through boxlet failed")
			return
		}

		response.JSON(c, http.StatusCreated, sandboxProtoResponse(created))
		return
	}

	record := store.SandboxRecord{
		ID:          sandboxID,
		State:       sandbox.StateRunning,
		RuntimeType: runtimeType,
		TemplateID:  req.TemplateID,
		ImageID:     req.ImageID,
		SnapshotID:  req.SnapshotID,
	}
	if err := h.store.CreateSandbox(c.Request.Context(), record); err != nil {
		response.Error(c, response.ErrInternal("create sandbox failed"))
		return
	}

	created, err := h.store.GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		response.Error(c, response.ErrInternal("load created sandbox failed"))
		return
	}

	response.JSON(c, http.StatusCreated, sandboxRecordResponse(*created))
}

func rootfsSourceToProto(source *rootfsSourceRequest) *novitaboxv1.RootfsSourceSpec {
	if source == nil {
		return nil
	}
	pullMode := strings.TrimSpace(source.PullMode)
	if pullMode == "" {
		pullMode = "lazy"
	}
	return &novitaboxv1.RootfsSourceSpec{
		Provider: strings.ToLower(strings.TrimSpace(source.Provider)),
		Image:    strings.TrimSpace(source.Image),
		PullMode: strings.ToLower(pullMode),
	}
}

func (h *Handler) UpdateSandboxBalloon(c *gin.Context) {
	if !h.ensureBalloonClient(c) {
		return
	}
	var req updateBalloonRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.AmountMiB == nil {
		response.Error(c, response.ErrBadRequest("amountMiB is required"))
		return
	}
	config, err := h.sandboxClient.UpdateSandboxBalloon(c.Request.Context(), &novitaboxv1.UpdateSandboxBalloonRequest{
		SandboxId: c.Param("sandbox_id"),
		AmountMib: *req.AmountMiB,
	})
	if err != nil {
		h.respondSandboxBoxletError(c, err, "update sandbox balloon failed")
		return
	}
	response.JSON(c, http.StatusOK, balloonConfigProtoResponse(config))
}

func (h *Handler) GetSandboxBalloon(c *gin.Context) {
	if !h.ensureBalloonClient(c) {
		return
	}
	config, err := h.sandboxClient.GetSandboxBalloon(c.Request.Context(), &novitaboxv1.GetSandboxBalloonRequest{SandboxId: c.Param("sandbox_id")})
	if err != nil {
		h.respondSandboxBoxletError(c, err, "get sandbox balloon failed")
		return
	}
	response.JSON(c, http.StatusOK, balloonConfigProtoResponse(config))
}

func (h *Handler) GetSandboxBalloonStats(c *gin.Context) {
	if !h.ensureBalloonClient(c) {
		return
	}
	stats, err := h.sandboxClient.GetSandboxBalloonStats(c.Request.Context(), &novitaboxv1.GetSandboxBalloonStatsRequest{SandboxId: c.Param("sandbox_id")})
	if err != nil {
		h.respondSandboxBoxletError(c, err, "get sandbox balloon statistics failed")
		return
	}
	response.JSON(c, http.StatusOK, balloonStatsProtoResponse(stats))
}

func (h *Handler) UpdateSandboxBalloonStats(c *gin.Context) {
	if !h.ensureBalloonClient(c) {
		return
	}
	var req updateBalloonStatsRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.StatsPollingIntervalS == nil {
		response.Error(c, response.ErrBadRequest("statsPollingIntervalS is required"))
		return
	}
	config, err := h.sandboxClient.UpdateSandboxBalloonStats(c.Request.Context(), &novitaboxv1.UpdateSandboxBalloonStatsRequest{
		SandboxId:             c.Param("sandbox_id"),
		StatsPollingIntervalS: *req.StatsPollingIntervalS,
	})
	if err != nil {
		h.respondSandboxBoxletError(c, err, "update sandbox balloon statistics failed")
		return
	}
	response.JSON(c, http.StatusOK, balloonConfigProtoResponse(config))
}

func (h *Handler) StartSandboxBalloonHinting(c *gin.Context) {
	if !h.ensureBalloonClient(c) {
		return
	}
	var req startBalloonHintingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.ErrBadRequest("invalid balloon hinting request body"))
		return
	}
	acknowledgeOnStop := true
	if req.AcknowledgeOnStop != nil {
		acknowledgeOnStop = *req.AcknowledgeOnStop
	}
	status, err := h.sandboxClient.StartSandboxBalloonHinting(c.Request.Context(), &novitaboxv1.StartSandboxBalloonHintingRequest{
		SandboxId:         c.Param("sandbox_id"),
		AcknowledgeOnStop: acknowledgeOnStop,
	})
	if err != nil {
		h.respondSandboxBoxletError(c, err, "start sandbox balloon hinting failed")
		return
	}
	response.JSON(c, http.StatusOK, balloonHintingProtoResponse(status))
}

func (h *Handler) StopSandboxBalloonHinting(c *gin.Context) {
	if !h.ensureBalloonClient(c) {
		return
	}
	status, err := h.sandboxClient.StopSandboxBalloonHinting(c.Request.Context(), &novitaboxv1.StopSandboxBalloonHintingRequest{SandboxId: c.Param("sandbox_id")})
	if err != nil {
		h.respondSandboxBoxletError(c, err, "stop sandbox balloon hinting failed")
		return
	}
	response.JSON(c, http.StatusOK, balloonHintingProtoResponse(status))
}

func (h *Handler) GetSandboxBalloonHinting(c *gin.Context) {
	if !h.ensureBalloonClient(c) {
		return
	}
	status, err := h.sandboxClient.GetSandboxBalloonHinting(c.Request.Context(), &novitaboxv1.GetSandboxBalloonHintingRequest{SandboxId: c.Param("sandbox_id")})
	if err != nil {
		h.respondSandboxBoxletError(c, err, "get sandbox balloon hinting failed")
		return
	}
	response.JSON(c, http.StatusOK, balloonHintingProtoResponse(status))
}

func balloonConfigProtoResponse(config *novitaboxv1.BalloonConfig) balloonConfigResponse {
	return balloonConfigResponse{
		AmountMiB:             config.GetAmountMib(),
		DeflateOnOOM:          config.GetDeflateOnOom(),
		StatsPollingIntervalS: config.GetStatsPollingIntervalS(),
		FreePageHinting:       config.GetFreePageHinting(),
		FreePageReporting:     config.GetFreePageReporting(),
	}
}

func (h *Handler) ensureBalloonClient(c *gin.Context) bool {
	if h.sandboxClient != nil {
		return true
	}
	response.Error(c, response.ErrInternal("boxlet sandbox service is not configured"))
	return false
}

func balloonStatsProtoResponse(stats *novitaboxv1.BalloonStats) balloonStatsResponse {
	return balloonStatsResponse{
		TargetMiB:          stats.GetTargetMib(),
		ActualMiB:          stats.GetActualMib(),
		SwapIn:             stats.GetSwapIn(),
		SwapOut:            stats.GetSwapOut(),
		MajorFaults:        stats.GetMajorFaults(),
		MinorFaults:        stats.GetMinorFaults(),
		FreeMemory:         stats.GetFreeMemory(),
		TotalMemory:        stats.GetTotalMemory(),
		AvailableMemory:    stats.GetAvailableMemory(),
		DiskCaches:         stats.GetDiskCaches(),
		HugetlbAllocations: stats.GetHugetlbAllocations(),
		HugetlbFailures:    stats.GetHugetlbFailures(),
		SharedMemory:       stats.GetSharedMemory(),
		UnevictableMemory:  stats.GetUnevictableMemory(),
		OOMKill:            stats.GetOomKill(),
		AllocStall:         stats.GetAllocStall(),
		AsyncScan:          stats.GetAsyncScan(),
		DirectScan:         stats.GetDirectScan(),
		AsyncReclaim:       stats.GetAsyncReclaim(),
		DirectReclaim:      stats.GetDirectReclaim(),
	}
}

func balloonHintingProtoResponse(status *novitaboxv1.BalloonHintingStatus) balloonHintingResponse {
	return balloonHintingResponse{
		State:    status.GetState(),
		HostCmd:  status.GetHostCmd(),
		GuestCmd: status.GuestCmd,
	}
}

func (h *Handler) ListSandboxes(c *gin.Context) {
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	if h.sandboxClient != nil {
		list, err := h.sandboxClient.ListSandboxes(c.Request.Context(), &novitaboxv1.ListSandboxesRequest{})
		if err != nil {
			response.Error(c, response.ErrInternal("list sandboxes through boxlet failed"))
			return
		}

		out := make([]sandboxInfoResponse, 0, len(list.GetSandboxes()))
		for _, item := range list.GetSandboxes() {
			out = append(out, sandboxProtoInfoResponse(item))
		}
		response.JSON(c, http.StatusOK, out)
		return
	}

	records, err := h.store.ListSandboxes(c.Request.Context())
	if err != nil {
		response.Error(c, response.ErrInternal("list sandboxes failed"))
		return
	}

	out := make([]sandboxInfoResponse, 0, len(records))
	for _, record := range records {
		out = append(out, sandboxRecordInfoResponse(record))
	}
	response.JSON(c, http.StatusOK, out)
}

func (h *Handler) ListSandboxesV2(c *gin.Context) {
	items, err := h.listSandboxItems(c)
	if err != nil {
		response.Error(c, response.ErrInternal(err.Error()))
		return
	}

	items = filterSandboxItemsByState(items, c.QueryArray("state"), c.Query("state"))

	offset, err := parseSandboxListOffset(c.Query("nextToken"))
	if err != nil {
		response.Error(c, response.ErrBadRequest("invalid nextToken"))
		return
	}
	limit, err := parseSandboxListLimit(c.Query("limit"))
	if err != nil {
		response.Error(c, response.ErrBadRequest("invalid limit"))
		return
	}
	items, nextToken := paginateSandboxItems(items, offset, limit)
	if nextToken != "" {
		c.Header("X-Next-Token", nextToken)
	}

	response.JSON(c, http.StatusOK, items)
}

func (h *Handler) GetSandbox(c *gin.Context) {
	sandboxID := c.Param("sandbox_id")
	if sandboxID == "" {
		response.Error(c, response.ErrBadRequest("sandboxID is required"))
		return
	}

	if h.sandboxClient != nil {
		got, err := h.sandboxClient.GetSandbox(c.Request.Context(), &novitaboxv1.GetSandboxRequest{SandboxId: sandboxID})
		if err != nil {
			h.respondSandboxBoxletError(c, err, "get sandbox through boxlet failed")
			return
		}
		response.JSON(c, http.StatusOK, sandboxProtoInfoResponse(got))
		return
	}
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	record, err := h.store.GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			response.Error(c, response.ErrNotFound("sandbox not found"))
			return
		}
		response.Error(c, response.ErrInternal("get sandbox failed"))
		return
	}

	response.JSON(c, http.StatusOK, sandboxRecordInfoResponse(*record))
}

func (h *Handler) KillSandbox(c *gin.Context) {
	sandboxID := c.Param("sandbox_id")
	if sandboxID == "" {
		response.Error(c, response.ErrBadRequest("sandboxID is required"))
		return
	}

	if h.sandboxClient != nil {
		if _, err := h.sandboxClient.KillSandbox(c.Request.Context(), &novitaboxv1.KillSandboxRequest{SandboxId: sandboxID}); err != nil {
			h.respondSandboxBoxletError(c, err, "kill sandbox through boxlet failed")
			return
		}
		c.Status(http.StatusNoContent)
		return
	}
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	if _, err := h.store.GetSandbox(c.Request.Context(), sandboxID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			response.Error(c, response.ErrNotFound("sandbox not found"))
			return
		}
		response.Error(c, response.ErrInternal("get sandbox failed"))
		return
	}
	if err := h.updateLocalSandboxState(c, sandboxID, sandbox.StateKilling, "kill"); err != nil {
		response.Error(c, response.ErrInternal("mark sandbox killing failed"))
		return
	}
	if err := h.updateLocalSandboxState(c, sandboxID, sandbox.StateKilled, "kill"); err != nil {
		response.Error(c, response.ErrInternal("mark sandbox killed failed"))
		return
	}
	if err := h.deleteLocalSandboxSnapshots(c, sandboxID); err != nil {
		response.Error(c, response.ErrInternal("delete sandbox snapshots failed"))
		return
	}
	if err := h.store.DeleteSandbox(c.Request.Context(), sandboxID); err != nil {
		response.Error(c, response.ErrInternal("delete sandbox failed"))
		return
	}
	if err := os.RemoveAll(layout.New(h.cfg.RootDir).SandboxDir(sandboxID)); err != nil {
		response.Error(c, response.ErrInternal("remove sandbox directory failed"))
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) PauseSandbox(c *gin.Context) {
	sandboxID := c.Param("sandbox_id")
	if sandboxID == "" {
		response.Error(c, response.ErrBadRequest("sandboxID is required"))
		return
	}

	if h.sandboxClient != nil {
		snapshot, err := h.sandboxClient.PauseSandbox(c.Request.Context(), &novitaboxv1.PauseSandboxRequest{SandboxId: sandboxID})
		if err != nil {
			h.respondSandboxBoxletError(c, err, "pause sandbox through boxlet failed")
			return
		}
		response.JSON(c, http.StatusOK, snapshotProtoResponse(snapshot))
		return
	}
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	if err := h.updateLocalSandboxState(c, sandboxID, sandbox.StatePausing, "pause"); err != nil {
		h.respondSandboxStoreError(c, err, "mark sandbox pausing failed")
		return
	}
	snapshot := h.localSnapshotRecord(sandboxID)
	if err := h.store.CreateSnapshot(c.Request.Context(), snapshot); err != nil {
		response.Error(c, response.ErrInternal("create sandbox snapshot failed"))
		return
	}
	if err := h.updateLocalSandboxState(c, sandboxID, sandbox.StatePaused, "pause"); err != nil {
		h.respondSandboxStoreError(c, err, "mark sandbox paused failed")
		return
	}
	response.JSON(c, http.StatusOK, snapshotRecordResponse(snapshot))
}

func (h *Handler) ResumeSandbox(c *gin.Context) {
	h.transitionSandbox(c, "resume", sandbox.StateResuming, sandbox.StateRunning, func(sandboxID string) error {
		if h.sandboxClient == nil {
			return nil
		}
		_, err := h.sandboxClient.ResumeSandbox(c.Request.Context(), &novitaboxv1.ResumeSandboxRequest{SandboxId: sandboxID})
		return err
	})
}

func (h *Handler) ConnectSandbox(c *gin.Context) {
	sandboxID := c.Param("sandbox_id")
	if sandboxID == "" {
		response.Error(c, response.ErrBadRequest("sandboxID is required"))
		return
	}

	if h.sandboxClient != nil {
		got, err := h.sandboxClient.GetSandbox(c.Request.Context(), &novitaboxv1.GetSandboxRequest{SandboxId: sandboxID})
		if err != nil {
			h.respondSandboxBoxletError(c, err, "get sandbox through boxlet failed")
			return
		}
		if protoSandboxState(got.GetState()) == string(sandbox.StatePaused) {
			got, err = h.sandboxClient.ResumeSandbox(c.Request.Context(), &novitaboxv1.ResumeSandboxRequest{SandboxId: sandboxID})
			if err != nil {
				h.respondSandboxBoxletError(c, err, "connect sandbox through boxlet failed")
				return
			}
		}
		response.JSON(c, http.StatusOK, sandboxProtoResponse(got))
		return
	}
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	record, err := h.store.GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		h.respondSandboxStoreError(c, err, "get sandbox failed")
		return
	}
	if record.State == sandbox.StatePaused {
		if err := h.updateLocalSandboxState(c, sandboxID, sandbox.StateResuming, "connect"); err != nil {
			h.respondSandboxStoreError(c, err, "mark sandbox resuming failed")
			return
		}
		if err := h.updateLocalSandboxState(c, sandboxID, sandbox.StateRunning, "connect"); err != nil {
			h.respondSandboxStoreError(c, err, "mark sandbox running failed")
			return
		}
		record, err = h.store.GetSandbox(c.Request.Context(), sandboxID)
		if err != nil {
			h.respondSandboxStoreError(c, err, "load sandbox failed")
			return
		}
	}

	response.JSON(c, http.StatusOK, sandboxRecordResponse(*record))
}

func (h *Handler) SetSandboxTimeout(c *gin.Context) {
	sandboxID := c.Param("sandbox_id")
	if sandboxID == "" {
		response.Error(c, response.ErrBadRequest("sandboxID is required"))
		return
	}

	if h.sandboxClient != nil {
		if _, err := h.sandboxClient.GetSandbox(c.Request.Context(), &novitaboxv1.GetSandboxRequest{SandboxId: sandboxID}); err != nil {
			h.respondSandboxBoxletError(c, err, "get sandbox through boxlet failed")
			return
		}
		c.Status(http.StatusNoContent)
		return
	}
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}
	if _, err := h.store.GetSandbox(c.Request.Context(), sandboxID); err != nil {
		h.respondSandboxStoreError(c, err, "get sandbox failed")
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) PoweroffSandbox(c *gin.Context) {
	h.transitionSandbox(c, "poweroff", sandbox.StateStopping, sandbox.StateStopped, func(sandboxID string) error {
		if h.sandboxClient == nil {
			return nil
		}
		_, err := h.sandboxClient.StopSandbox(c.Request.Context(), &novitaboxv1.StopSandboxRequest{SandboxId: sandboxID})
		return err
	})
}

func (h *Handler) PoweronSandbox(c *gin.Context) {
	h.transitionSandbox(c, "poweron", sandbox.StateStarting, sandbox.StateRunning, func(sandboxID string) error {
		if h.sandboxClient == nil {
			return nil
		}
		_, err := h.sandboxClient.StartSandbox(c.Request.Context(), &novitaboxv1.StartSandboxRequest{SandboxId: sandboxID})
		return err
	})
}

func (h *Handler) RebootSandbox(c *gin.Context) {
	h.transitionSandbox(c, "reboot", sandbox.StateRebooting, sandbox.StateRunning, func(sandboxID string) error {
		if h.sandboxClient == nil {
			return nil
		}
		_, err := h.sandboxClient.RebootSandbox(c.Request.Context(), &novitaboxv1.RebootSandboxRequest{SandboxId: sandboxID})
		return err
	})
}

func newSandboxID() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz1234567890"
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}

	return "sbx-" + string(b[:]), nil
}

func sandboxRecordResponse(record store.SandboxRecord) sandboxResponse {
	return sandboxResponse{
		Alias:              nil,
		ClientID:           "",
		Domain:             nil,
		EnvdAccessToken:    nil,
		EnvdVersion:        defaultSandboxEnvdVersion,
		SandboxID:          record.ID,
		TemplateID:         record.TemplateID,
		TrafficAccessToken: nil,
	}
}

func sandboxProtoResponse(record *novitaboxv1.SandboxInfo) sandboxResponse {
	return sandboxResponse{
		Alias:              nil,
		ClientID:           "",
		Domain:             nil,
		EnvdAccessToken:    nil,
		EnvdVersion:        defaultSandboxEnvdVersion,
		SandboxID:          record.GetSandboxId(),
		TemplateID:         record.GetTemplateId(),
		TrafficAccessToken: nil,
	}
}

func runtimeTypeToProto(runtimeType string) novitaboxv1.RuntimeType {
	switch strings.ToLower(runtimeType) {
	case "cloud-hypervisor", "cloud_hypervisor":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR
	case "container", "gvisor":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER
	default:
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_FIRECRACKER
	}
}

func (h *Handler) transitionSandbox(c *gin.Context, action string, transitionState sandbox.State, finalState sandbox.State, call func(string) error) {
	sandboxID := c.Param("sandbox_id")
	if sandboxID == "" {
		response.Error(c, response.ErrBadRequest("sandboxID is required"))
		return
	}

	if h.sandboxClient != nil {
		if err := call(sandboxID); err != nil {
			h.respondSandboxBoxletError(c, err, action+" sandbox through boxlet failed")
			return
		}
		got, err := h.sandboxClient.GetSandbox(c.Request.Context(), &novitaboxv1.GetSandboxRequest{SandboxId: sandboxID})
		if err != nil {
			h.respondSandboxBoxletError(c, err, "get sandbox through boxlet failed")
			return
		}
		response.JSON(c, http.StatusOK, sandboxProtoInfoResponse(got))
		return
	}
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	if err := h.updateLocalSandboxState(c, sandboxID, transitionState, action); err != nil {
		h.respondSandboxStoreError(c, err, fmt.Sprintf("mark sandbox %s failed", transitionState))
		return
	}
	if err := h.updateLocalSandboxState(c, sandboxID, finalState, action); err != nil {
		h.respondSandboxStoreError(c, err, fmt.Sprintf("mark sandbox %s failed", finalState))
		return
	}
	record, err := h.store.GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		h.respondSandboxStoreError(c, err, "load sandbox failed")
		return
	}
	response.JSON(c, http.StatusOK, sandboxRecordInfoResponse(*record))
}

func (h *Handler) respondSandboxBoxletError(c *gin.Context, err error, fallbackMessage string) {
	switch status.Code(err) {
	case codes.NotFound:
		response.Error(c, response.ErrNotFound("sandbox not found"))
	case codes.AlreadyExists:
		response.Error(c, response.ErrConflict("sandbox already exists"))
	case codes.InvalidArgument:
		response.Error(c, response.ErrBadRequest(status.Convert(err).Message()))
	case codes.FailedPrecondition:
		response.Error(c, response.ErrConflict(status.Convert(err).Message()))
	case codes.Unimplemented:
		response.Error(c, response.ErrNotImplemented(status.Convert(err).Message()))
	default:
		message := fallbackMessage
		if status.Convert(err).Message() != "" {
			message += ": " + status.Convert(err).Message()
		} else if err != nil {
			message += ": " + err.Error()
		}
		response.Error(c, response.ErrInternal(message))
	}
}

func (h *Handler) respondSandboxStoreError(c *gin.Context, err error, fallbackMessage string) {
	if errors.Is(err, store.ErrNotFound) {
		response.Error(c, response.ErrNotFound("sandbox not found"))
		return
	}
	response.Error(c, response.ErrInternal(fallbackMessage))
}

func (h *Handler) updateLocalSandboxState(c *gin.Context, sandboxID string, to sandbox.State, action string) error {
	record, err := h.store.GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		return err
	}
	if record.State == to {
		return nil
	}

	return h.store.UpdateSandboxState(c.Request.Context(), sandboxID, record.State, to, action)
}

func (h *Handler) deleteLocalSandboxSnapshots(c *gin.Context, sandboxID string) error {
	snapshots, err := h.store.ListSnapshotsBySandbox(c.Request.Context(), sandboxID)
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		if err := h.store.DeleteSnapshot(c.Request.Context(), snapshot.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}

	return nil
}

func (h *Handler) localSnapshotRecord(sandboxID string) store.SnapshotRecord {
	now := time.Now()
	paths := localSandboxRuntimePaths(h.cfg.RootDir, sandboxID)
	return store.SnapshotRecord{
		ID:           fmt.Sprintf("snap-%s-%d", sandboxID, time.Now().UnixNano()),
		SandboxID:    sandboxID,
		RootfsPath:   paths.RootfsPath,
		MemfilePath:  paths.MemfilePath,
		SnapfilePath: paths.SnapfilePath,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

type localSandboxArtifactPaths struct {
	RootfsPath   string
	MemfilePath  string
	SnapfilePath string
}

func localSandboxRuntimePaths(rootDir string, sandboxID string) localSandboxArtifactPaths {
	sandboxDir := layout.New(rootDir).SandboxDir(sandboxID)
	snapshotDir := filepath.Join(sandboxDir, "snapshot")
	return localSandboxArtifactPaths{
		RootfsPath:   filepath.Join(snapshotDir, "rootfs.ext4"),
		MemfilePath:  filepath.Join(snapshotDir, "memfile"),
		SnapfilePath: filepath.Join(snapshotDir, "snapfile"),
	}
}

func sandboxRecordInfoResponse(record store.SandboxRecord) sandboxInfoResponse {
	return sandboxInfoResponse{
		SandboxID:     record.ID,
		TemplateID:    record.TemplateID,
		ImageID:       record.ImageID,
		SnapshotID:    record.SnapshotID,
		State:         string(record.State),
		RuntimeType:   normalizeRuntimeType(record.RuntimeType),
		Rootfs:        sandboxRecordRootfsInfoResponse(record),
		CreatedAtUnix: record.CreatedAt.Unix(),
		UpdatedAtUnix: record.UpdatedAt.Unix(),
	}
}

func sandboxProtoInfoResponse(info *novitaboxv1.SandboxInfo) sandboxInfoResponse {
	return sandboxInfoResponse{
		SandboxID:     info.GetSandboxId(),
		TemplateID:    info.GetTemplateId(),
		ImageID:       info.GetImageId(),
		SnapshotID:    info.GetSnapshotId(),
		State:         protoSandboxState(info.GetState()),
		RuntimeType:   protoRuntimeType(info.GetRuntimeType()),
		Rootfs:        sandboxProtoRootfsInfoResponse(info.GetRootfs()),
		CreatedAtUnix: info.GetCreatedAtUnix(),
		UpdatedAtUnix: info.GetUpdatedAtUnix(),
	}
}

func sandboxRecordRootfsInfoResponse(record store.SandboxRecord) *rootfsInfoResponse {
	if record.RootfsProvider == "" || (record.RootfsProvider == "directory" && record.RootfsSourceRef == "" && record.RootfsSourceDigest == "" && record.RootfsSnapshotKey == "") {
		return nil
	}
	return &rootfsInfoResponse{
		Provider:    record.RootfsProvider,
		Image:       record.RootfsSourceRef,
		Digest:      record.RootfsSourceDigest,
		SnapshotKey: record.RootfsSnapshotKey,
	}
}

func sandboxProtoRootfsInfoResponse(rootfs *novitaboxv1.RootfsInfo) *rootfsInfoResponse {
	if rootfs == nil || (rootfs.GetProvider() == "directory" && rootfs.GetImage() == "" && rootfs.GetDigest() == "" && rootfs.GetSnapshotKey() == "") {
		return nil
	}
	return &rootfsInfoResponse{
		Provider:    rootfs.GetProvider(),
		Image:       rootfs.GetImage(),
		Digest:      rootfs.GetDigest(),
		SnapshotKey: rootfs.GetSnapshotKey(),
	}
}

func (h *Handler) listSandboxItems(c *gin.Context) ([]sandboxListItemResponse, error) {
	if h.sandboxClient != nil {
		list, err := h.sandboxClient.ListSandboxes(c.Request.Context(), &novitaboxv1.ListSandboxesRequest{})
		if err != nil {
			return nil, fmt.Errorf("list sandboxes through boxlet failed")
		}

		out := make([]sandboxListItemResponse, 0, len(list.GetSandboxes()))
		for _, item := range list.GetSandboxes() {
			out = append(out, sandboxProtoListItemResponse(item))
		}
		return out, nil
	}
	if h.store == nil {
		return nil, fmt.Errorf("storage is not configured")
	}

	records, err := h.store.ListSandboxes(c.Request.Context())
	if err != nil {
		return nil, fmt.Errorf("list sandboxes failed")
	}

	out := make([]sandboxListItemResponse, 0, len(records))
	for _, record := range records {
		out = append(out, sandboxRecordListItemResponse(record))
	}
	return out, nil
}

func sandboxRecordListItemResponse(record store.SandboxRecord) sandboxListItemResponse {
	startedAt := record.CreatedAt
	if startedAt.IsZero() {
		startedAt = time.Unix(0, 0).UTC()
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = startedAt
	}

	return sandboxListItemResponse{
		Alias:        nil,
		ClientID:     "",
		CPUCount:     defaultTemplateCPUCount,
		DiskSizeMB:   0,
		EndAt:        updatedAt.UTC().Format(time.RFC3339),
		EnvdVersion:  defaultSandboxEnvdVersion,
		MemoryMB:     defaultTemplateMemoryMB,
		Metadata:     map[string]string{},
		SandboxID:    record.ID,
		StartedAt:    startedAt.UTC().Format(time.RFC3339),
		State:        string(record.State),
		TemplateID:   record.TemplateID,
		VolumeMounts: []any{},
	}
}

func sandboxProtoListItemResponse(info *novitaboxv1.SandboxInfo) sandboxListItemResponse {
	startedAt := unixSecondsToTime(info.GetCreatedAtUnix())
	updatedAt := unixSecondsToTime(info.GetUpdatedAtUnix())
	if updatedAt.IsZero() {
		updatedAt = startedAt
	}

	return sandboxListItemResponse{
		Alias:        nil,
		ClientID:     "",
		CPUCount:     defaultTemplateCPUCount,
		DiskSizeMB:   0,
		EndAt:        updatedAt.UTC().Format(time.RFC3339),
		EnvdVersion:  defaultSandboxEnvdVersion,
		MemoryMB:     defaultTemplateMemoryMB,
		Metadata:     map[string]string{},
		SandboxID:    info.GetSandboxId(),
		StartedAt:    startedAt.UTC().Format(time.RFC3339),
		State:        protoSandboxState(info.GetState()),
		TemplateID:   info.GetTemplateId(),
		VolumeMounts: []any{},
	}
}

func unixSecondsToTime(ts int64) time.Time {
	if ts <= 0 {
		return time.Unix(0, 0).UTC()
	}
	return time.Unix(ts, 0).UTC()
}

func filterSandboxItemsByState(items []sandboxListItemResponse, queryValues []string, raw string) []sandboxListItemResponse {
	states := map[string]struct{}{}
	for _, value := range queryValues {
		for _, state := range strings.Split(value, ",") {
			state = strings.TrimSpace(state)
			if state != "" {
				states[state] = struct{}{}
			}
		}
	}
	if len(states) == 0 && raw != "" {
		for _, state := range strings.Split(raw, ",") {
			state = strings.TrimSpace(state)
			if state != "" {
				states[state] = struct{}{}
			}
		}
	}
	if len(states) == 0 {
		return items
	}

	out := make([]sandboxListItemResponse, 0, len(items))
	for _, item := range items {
		if _, ok := states[item.State]; ok {
			out = append(out, item)
		}
	}
	return out
}

func parseSandboxListOffset(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(value)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid offset")
	}
	return offset, nil
}

func parseSandboxListLimit(value string) (int, error) {
	if value == "" {
		return 100, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 0 {
		return 0, fmt.Errorf("invalid limit")
	}
	if limit == 0 || limit > 1000 {
		return 1000, nil
	}
	return limit, nil
}

func paginateSandboxItems(items []sandboxListItemResponse, offset int, limit int) ([]sandboxListItemResponse, string) {
	if offset >= len(items) {
		return []sandboxListItemResponse{}, ""
	}
	end := offset + limit
	if end >= len(items) {
		return items[offset:], ""
	}
	return items[offset:end], strconv.Itoa(end)
}

func snapshotRecordResponse(record store.SnapshotRecord) snapshotResponse {
	return snapshotResponse{
		SnapshotID:    record.ID,
		SandboxID:     record.SandboxID,
		RootfsPath:    record.RootfsPath,
		MemfilePath:   record.MemfilePath,
		SnapfilePath:  record.SnapfilePath,
		CreatedAtUnix: record.CreatedAt.Unix(),
	}
}

func snapshotProtoResponse(info *novitaboxv1.SnapshotInfo) snapshotResponse {
	return snapshotResponse{
		SnapshotID:    info.GetSnapshotId(),
		SandboxID:     info.GetSandboxId(),
		RootfsPath:    info.GetRootfsPath(),
		MemfilePath:   info.GetMemfilePath(),
		SnapfilePath:  info.GetSnapfilePath(),
		CreatedAtUnix: info.GetCreatedAtUnix(),
	}
}

func normalizeRuntimeType(runtimeType string) string {
	switch strings.ToLower(runtimeType) {
	case "runtime_type_cloud_hypervisor", "cloud-hypervisor", "cloud_hypervisor":
		return "cloud-hypervisor"
	case "runtime_type_container", "container", "gvisor":
		return "gvisor"
	default:
		return "firecracker"
	}
}

func protoRuntimeType(runtimeType novitaboxv1.RuntimeType) string {
	switch runtimeType {
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR:
		return "cloud-hypervisor"
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER:
		return "gvisor"
	default:
		return "firecracker"
	}
}

func protoSandboxState(state novitaboxv1.SandboxState) string {
	switch state {
	case novitaboxv1.SandboxState_SANDBOX_STATE_CREATING:
		return string(sandbox.StateCreating)
	case novitaboxv1.SandboxState_SANDBOX_STATE_RUNNING:
		return string(sandbox.StateRunning)
	case novitaboxv1.SandboxState_SANDBOX_STATE_PAUSING:
		return string(sandbox.StatePausing)
	case novitaboxv1.SandboxState_SANDBOX_STATE_PAUSED:
		return string(sandbox.StatePaused)
	case novitaboxv1.SandboxState_SANDBOX_STATE_RESUMING:
		return string(sandbox.StateResuming)
	case novitaboxv1.SandboxState_SANDBOX_STATE_STOPPING:
		return string(sandbox.StateStopping)
	case novitaboxv1.SandboxState_SANDBOX_STATE_STOPPED:
		return string(sandbox.StateStopped)
	case novitaboxv1.SandboxState_SANDBOX_STATE_STARTING:
		return string(sandbox.StateStarting)
	case novitaboxv1.SandboxState_SANDBOX_STATE_REBOOTING:
		return string(sandbox.StateRebooting)
	case novitaboxv1.SandboxState_SANDBOX_STATE_KILLING:
		return string(sandbox.StateKilling)
	case novitaboxv1.SandboxState_SANDBOX_STATE_KILLED:
		return string(sandbox.StateKilled)
	case novitaboxv1.SandboxState_SANDBOX_STATE_FAILED:
		return string(sandbox.StateFailed)
	case novitaboxv1.SandboxState_SANDBOX_STATE_UNKNOWN:
		return string(sandbox.StateUnknown)
	default:
		return ""
	}
}
