package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/boxapi/response"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/sandbox"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

type createSandboxRequest struct {
	AllowInternetAccess *bool             `json:"allow_internet_access,omitempty"`
	AutoPause           *bool             `json:"autoPause,omitempty"`
	AutoResume          *autoResumeConfig `json:"autoResume,omitempty"`
	EnvVars             map[string]string `json:"envVars,omitempty"`
	MCP                 map[string]any    `json:"mcp,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
	Network             map[string]any    `json:"network,omitempty"`
	Secure              *bool             `json:"secure,omitempty"`
	TemplateID          string            `json:"templateID"`
	Timeout             *int32            `json:"timeout,omitempty"`
	VolumeMounts        *[]map[string]any `json:"volumeMounts,omitempty"`

	SandboxID   string `json:"sandbox_id,omitempty"`
	ImageID     string `json:"image_id,omitempty"`
	SnapshotID  string `json:"snapshot_id,omitempty"`
	RuntimeType string `json:"runtime_type,omitempty"`
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
	SandboxID     string `json:"sandboxID"`
	TemplateID    string `json:"templateID,omitempty"`
	ImageID       string `json:"imageID,omitempty"`
	SnapshotID    string `json:"snapshotID,omitempty"`
	State         string `json:"state"`
	RuntimeType   string `json:"runtimeType"`
	CreatedAtUnix int64  `json:"createdAtUnix,omitempty"`
	UpdatedAtUnix int64  `json:"updatedAtUnix,omitempty"`
}

type listSandboxesResponse struct {
	Sandboxes []sandboxInfoResponse `json:"sandboxes"`
}

type snapshotResponse struct {
	SnapshotID    string `json:"snapshotID"`
	SandboxID     string `json:"sandboxID"`
	RootfsPath    string `json:"rootfsPath,omitempty"`
	MemfilePath   string `json:"memfilePath,omitempty"`
	SnapfilePath  string `json:"snapfilePath,omitempty"`
	CreatedAtUnix int64  `json:"createdAtUnix,omitempty"`
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
	if req.TemplateID == "" && req.ImageID == "" && req.SnapshotID == "" {
		response.Error(c, response.ErrBadRequest("templateID is required"))
		return
	}

	sandboxID := req.SandboxID
	if sandboxID == "" {
		var err error
		sandboxID, err = newSandboxID()
		if err != nil {
			response.Error(c, response.ErrInternal("generate sandbox id failed"))
			return
		}
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

	if h.sandboxClient != nil {
		created, err := h.sandboxClient.CreateSandbox(c.Request.Context(), &novitaboxv1.CreateSandboxRequest{
			SandboxId:   sandboxID,
			TemplateId:  req.TemplateID,
			ImageId:     req.ImageID,
			SnapshotId:  req.SnapshotID,
			RuntimeType: runtimeTypeToProto(runtimeType),
			RuntimeSpec: &novitaboxv1.RuntimeSpec{
				SandboxId:   sandboxID,
				RuntimeType: runtimeTypeToProto(runtimeType),
			},
		})
		if err != nil {
			response.Error(c, response.ErrInternal("create sandbox through boxlet failed"))
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

		out := listSandboxesResponse{Sandboxes: make([]sandboxInfoResponse, 0, len(list.GetSandboxes()))}
		for _, item := range list.GetSandboxes() {
			out.Sandboxes = append(out.Sandboxes, sandboxProtoInfoResponse(item))
		}
		response.JSON(c, http.StatusOK, out)
		return
	}

	records, err := h.store.ListSandboxes(c.Request.Context())
	if err != nil {
		response.Error(c, response.ErrInternal("list sandboxes failed"))
		return
	}

	out := listSandboxesResponse{Sandboxes: make([]sandboxInfoResponse, 0, len(records))}
	for _, record := range records {
		out.Sandboxes = append(out.Sandboxes, sandboxRecordInfoResponse(record))
	}
	response.JSON(c, http.StatusOK, out)
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
			response.Error(c, response.ErrInternal("get sandbox through boxlet failed"))
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
			response.Error(c, response.ErrInternal("kill sandbox through boxlet failed"))
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
			response.Error(c, response.ErrInternal("pause sandbox through boxlet failed"))
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
		response.Error(c, response.ErrInternal("mark sandbox pausing failed"))
		return
	}
	snapshot := h.localSnapshotRecord(sandboxID)
	if err := h.store.CreateSnapshot(c.Request.Context(), snapshot); err != nil {
		response.Error(c, response.ErrInternal("create sandbox snapshot failed"))
		return
	}
	if err := h.updateLocalSandboxState(c, sandboxID, sandbox.StatePaused, "pause"); err != nil {
		response.Error(c, response.ErrInternal("mark sandbox paused failed"))
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
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	return "sbx-" + hex.EncodeToString(b[:]), nil
}

func sandboxRecordResponse(record store.SandboxRecord) sandboxResponse {
	return sandboxResponse{
		Alias:              nil,
		ClientID:           "",
		Domain:             nil,
		EnvdAccessToken:    nil,
		EnvdVersion:        "",
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
		EnvdVersion:        "",
		SandboxID:          record.GetSandboxId(),
		TemplateID:         record.GetTemplateId(),
		TrafficAccessToken: nil,
	}
}

func runtimeTypeToProto(runtimeType string) novitaboxv1.RuntimeType {
	switch strings.ToLower(runtimeType) {
	case "cloud-hypervisor", "cloud_hypervisor":
		return novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR
	case "container":
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
			response.Error(c, response.ErrInternal(action+" sandbox through boxlet failed"))
			return
		}
		got, err := h.sandboxClient.GetSandbox(c.Request.Context(), &novitaboxv1.GetSandboxRequest{SandboxId: sandboxID})
		if err != nil {
			response.Error(c, response.ErrInternal("get sandbox through boxlet failed"))
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
		response.Error(c, response.ErrInternal(fmt.Sprintf("mark sandbox %s failed", transitionState)))
		return
	}
	if err := h.updateLocalSandboxState(c, sandboxID, finalState, action); err != nil {
		response.Error(c, response.ErrInternal(fmt.Sprintf("mark sandbox %s failed", finalState)))
		return
	}
	record, err := h.store.GetSandbox(c.Request.Context(), sandboxID)
	if err != nil {
		response.Error(c, response.ErrInternal("load sandbox failed"))
		return
	}
	response.JSON(c, http.StatusOK, sandboxRecordInfoResponse(*record))
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
	sandboxDir := layout.New(h.cfg.RootDir).SandboxDir(sandboxID)
	return store.SnapshotRecord{
		ID:           fmt.Sprintf("snap-%s-%d", sandboxID, time.Now().UnixNano()),
		SandboxID:    sandboxID,
		RootfsPath:   sandboxDir + "/rootfs.ext4",
		MemfilePath:  sandboxDir + "/snapshot/memfile",
		SnapfilePath: sandboxDir + "/snapshot/snapfile",
		CreatedAt:    now,
		UpdatedAt:    now,
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
		CreatedAtUnix: info.GetCreatedAtUnix(),
		UpdatedAtUnix: info.GetUpdatedAtUnix(),
	}
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
	case "runtime_type_container", "container":
		return "container"
	default:
		return "firecracker"
	}
}

func protoRuntimeType(runtimeType novitaboxv1.RuntimeType) string {
	switch runtimeType {
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CLOUD_HYPERVISOR:
		return "cloud-hypervisor"
	case novitaboxv1.RuntimeType_RUNTIME_TYPE_CONTAINER:
		return "container"
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
