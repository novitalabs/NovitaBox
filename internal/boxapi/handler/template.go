package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/boxapi/response"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type createTemplateV3Request struct {
	Alias      *string           `json:"alias,omitempty"`
	CPUCount   *int32            `json:"cpuCount,omitempty"`
	MemoryMB   *int32            `json:"memoryMB,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Name       *string           `json:"name,omitempty"`
	Tags       *[]string         `json:"tags,omitempty"`
	TeamID     *string           `json:"teamID,omitempty"`
	TemplateID *string           `json:"templateID,omitempty"`
}

type startTemplateBuildV2Request struct {
	Force             *bool           `json:"force,omitempty"`
	FromImage         *string         `json:"fromImage,omitempty"`
	FromImageRegistry map[string]any  `json:"fromImageRegistry,omitempty"`
	FromTemplate      *string         `json:"fromTemplate,omitempty"`
	ReadyCmd          *string         `json:"readyCmd,omitempty"`
	StartCmd          *string         `json:"startCmd,omitempty"`
	Steps             *[]templateStep `json:"steps,omitempty"`
}

type templateStep struct {
	Args      *[]string         `json:"args,omitempty"`
	FilesHash *string           `json:"filesHash,omitempty"`
	Type      string            `json:"type"`
	EnvVars   map[string]string `json:"envVars,omitempty"`
}

type templateV3Response struct {
	Aliases    []string          `json:"aliases"`
	BuildID    string            `json:"buildID"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Names      []string          `json:"names"`
	Public     bool              `json:"public"`
	Tags       []string          `json:"tags"`
	TemplateID string            `json:"templateID"`
}

type compatibleTemplateResponse struct {
	Aliases       []string          `json:"aliases"`
	BuildCount    int32             `json:"buildCount"`
	BuildID       string            `json:"buildID"`
	BuildStatus   string            `json:"buildStatus"`
	CPUCount      int32             `json:"cpuCount"`
	CreatedAt     time.Time         `json:"createdAt"`
	CreatedBy     *teamUserResponse `json:"createdBy"`
	DiskSizeMB    int64             `json:"diskSizeMB"`
	EnvdVersion   string            `json:"envdVersion"`
	LastSpawnedAt *time.Time        `json:"lastSpawnedAt"`
	MemoryMB      int32             `json:"memoryMB"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Names         []string          `json:"names"`
	Public        bool              `json:"public"`
	SpawnCount    int64             `json:"spawnCount"`
	TemplateID    string            `json:"templateID"`
	UpdatedAt     time.Time         `json:"updatedAt"`
}

type compatibleTemplateWithBuildsResponse struct {
	Aliases       []string                          `json:"aliases"`
	Builds        []compatibleTemplateBuildResponse `json:"builds"`
	CreatedAt     time.Time                         `json:"createdAt"`
	LastSpawnedAt *time.Time                        `json:"lastSpawnedAt"`
	Metadata      map[string]string                 `json:"metadata,omitempty"`
	Names         []string                          `json:"names"`
	Public        bool                              `json:"public"`
	SpawnCount    int64                             `json:"spawnCount"`
	TemplateID    string                            `json:"templateID"`
	UpdatedAt     time.Time                         `json:"updatedAt"`
}

type compatibleTemplateBuildResponse struct {
	BuildID     string     `json:"buildID"`
	CPUCount    int32      `json:"cpuCount"`
	CreatedAt   time.Time  `json:"createdAt"`
	DiskSizeMB  *int64     `json:"diskSizeMB,omitempty"`
	EnvdVersion *string    `json:"envdVersion,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	MemoryMB    int32      `json:"memoryMB"`
	Status      string     `json:"status"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type templateBuildInfoResponse struct {
	BuildID    string          `json:"buildID"`
	LogEntries []buildLogEntry `json:"logEntries"`
	Logs       []string        `json:"logs"`
	Status     string          `json:"status"`
	TemplateID string          `json:"templateID"`
	Reason     *buildReason    `json:"reason,omitempty"`
}

type buildLogEntry struct {
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Step      string    `json:"step,omitempty"`
}

type buildReason struct {
	Message    string          `json:"message"`
	LogEntries []buildLogEntry `json:"logEntries,omitempty"`
	Step       string          `json:"step,omitempty"`
}

type teamUserResponse struct {
	Email string `json:"email,omitempty"`
	ID    string `json:"id,omitempty"`
}

const (
	defaultTemplateCPUCount   = int32(1)
	defaultTemplateMemoryMB   = int32(512)
	defaultSandboxEnvdVersion = "0.1.0"
)

type convertTemplateRequest struct {
	TemplateID string `json:"templateID"`
	ImageID    string `json:"imageID"`
}

type convertTemplateResponse struct {
	ImageID       string `json:"imageID"`
	TemplateID    string `json:"templateID"`
	RootfsPath    string `json:"rootfsPath"`
	CreatedAtUnix int64  `json:"createdAtUnix"`
}

func (h *Handler) CreateTemplateV3(c *gin.Context) {
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	var req createTemplateV3Request
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.ErrBadRequest("invalid template request body"))
		return
	}

	input := ""
	if req.Name != nil {
		input = *req.Name
	} else if req.Alias != nil {
		input = *req.Alias
	}
	if input == "" {
		response.Error(c, response.ErrBadRequest("name is required"))
		return
	}

	name, tags := splitTemplateName(input)
	tags = append(tags, derefStringSlice(req.Tags)...)

	templateID := strings.TrimSpace(derefString(req.TemplateID))
	if templateID == "" {
		var err error
		templateID, err = newTemplateID()
		if err != nil {
			response.Error(c, response.ErrInternal("generate template id failed"))
			return
		}
	}
	if err := validateTemplateID(templateID); err != nil {
		response.Error(c, response.ErrBadRequest(err.Error()))
		return
	}

	record := newTemplateRecord(h.cfg.RootDir, templateID)
	record.Aliases = []string{name}
	record.Names = []string{name}
	record.Metadata = req.Metadata
	record.CPUCount = defaultTemplateCPUCount
	if req.CPUCount != nil && *req.CPUCount > 0 {
		record.CPUCount = *req.CPUCount
	}
	record.MemoryMB = defaultTemplateMemoryMB
	if req.MemoryMB != nil && *req.MemoryMB > 0 {
		record.MemoryMB = *req.MemoryMB
	}

	created, err := h.getOrCreateTemplate(c, record)
	if err != nil {
		response.Error(c, response.ErrInternal("create template failed"))
		return
	}

	buildID, err := newBuildID()
	if err != nil {
		response.Error(c, response.ErrInternal("generate build id failed"))
		return
	}
	if err := h.store.CreateTemplateBuild(c.Request.Context(), store.TemplateBuildRecord{
		ID:         buildID,
		TemplateID: created.ID,
		Status:     store.TemplateBuildStatusWaiting,
	}); err != nil {
		response.Error(c, response.ErrInternal("create template build failed"))
		return
	}

	response.JSON(c, http.StatusAccepted, templateV3Response{
		Aliases:    []string{name},
		BuildID:    buildID,
		Metadata:   req.Metadata,
		Names:      []string{name},
		Public:     false,
		Tags:       deduplicateStrings(tags),
		TemplateID: created.ID,
	})
}

func (h *Handler) StartTemplateBuildV2(c *gin.Context) {
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	templateID := c.Param("template_id")
	buildID := c.Param("build_id")
	if templateID == "" || buildID == "" {
		response.Error(c, response.ErrBadRequest("templateID and buildID are required"))
		return
	}

	var req startTemplateBuildV2Request
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.ErrBadRequest("invalid template build request body"))
		return
	}

	if _, err := h.store.GetTemplateBuild(c.Request.Context(), templateID, buildID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			response.Error(c, response.ErrNotFound("template build not found"))
			return
		}
		h.logger.Error("load template build failed", "template_id", templateID, "build_id", buildID, "error", err)
		response.Error(c, response.ErrInternal("load template build failed"))
		return
	}

	if err := h.store.UpdateTemplateBuildStatus(
		c.Request.Context(),
		templateID,
		buildID,
		store.TemplateBuildStatusWaiting,
		store.TemplateBuildStatusBuilding,
	); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			response.Error(c, response.ErrBadRequest("build is not in waiting state"))
			return
		}
		h.logger.Error("start template build failed", "template_id", templateID, "build_id", buildID, "error", err)
		response.Error(c, response.ErrInternal("start template build failed"))
		return
	}

	if h.artifactClient != nil {
		if _, err := h.artifactClient.CreateTemplate(c.Request.Context(), templateBuildRequestToProto(templateID, req)); err != nil {
			_ = h.store.UpdateTemplateBuildStatus(
				c.Request.Context(),
				templateID,
				buildID,
				store.TemplateBuildStatusBuilding,
				store.TemplateBuildStatusError,
			)
			h.logger.Error("build template artifact failed",
				"template_id", templateID,
				"build_id", buildID,
				"from_image", derefString(req.FromImage),
				"from_template", derefString(req.FromTemplate),
				"error", err,
			)
			response.Error(c, response.ErrInternal("build template artifact failed: "+err.Error()))
			return
		}
	}

	if err := h.store.UpdateTemplateBuildStatus(
		c.Request.Context(),
		templateID,
		buildID,
		store.TemplateBuildStatusBuilding,
		store.TemplateBuildStatusReady,
	); err != nil {
		h.logger.Error("finish template build failed", "template_id", templateID, "build_id", buildID, "error", err)
		response.Error(c, response.ErrInternal("finish template build failed"))
		return
	}

	c.Status(http.StatusAccepted)
}

func (h *Handler) GetTemplateBuildStatus(c *gin.Context) {
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	templateID := c.Param("template_id")
	buildID := c.Param("build_id")
	if templateID == "" || buildID == "" {
		response.Error(c, response.ErrBadRequest("templateID and buildID are required"))
		return
	}

	record, err := h.store.GetTemplateBuild(c.Request.Context(), templateID, buildID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			response.Error(c, response.ErrNotFound("template build not found"))
			return
		}
		h.logger.Error("load template build status failed", "template_id", templateID, "build_id", buildID, "error", err)
		response.Error(c, response.ErrInternal("load template build status failed"))
		return
	}

	response.JSON(c, http.StatusOK, templateBuildInfoFromRecord(*record))
}

func templateBuildRequestToProto(templateID string, req startTemplateBuildV2Request) *novitaboxv1.CreateTemplateRequest {
	out := &novitaboxv1.CreateTemplateRequest{
		TemplateId: templateID,
	}
	if req.FromImage != nil {
		out.DockerImage = *req.FromImage
	}
	if req.FromTemplate != nil {
		out.FromTemplate = *req.FromTemplate
	}
	if req.StartCmd != nil {
		out.StartCmd = *req.StartCmd
	}
	if req.ReadyCmd != nil {
		out.ReadyCmd = *req.ReadyCmd
	}
	if req.Steps != nil {
		out.Steps = make([]*novitaboxv1.TemplateBuildStep, 0, len(*req.Steps))
		for _, step := range *req.Steps {
			out.Steps = append(out.Steps, templateStepToProto(step))
		}
	}

	return out
}

func templateStepToProto(step templateStep) *novitaboxv1.TemplateBuildStep {
	out := &novitaboxv1.TemplateBuildStep{
		Type:    step.Type,
		EnvVars: step.EnvVars,
	}
	if step.Args != nil {
		out.Args = *step.Args
	}
	if step.FilesHash != nil {
		out.FilesHash = *step.FilesHash
	}

	return out
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func (h *Handler) getOrCreateTemplate(c *gin.Context, record store.TemplateRecord) (*store.TemplateRecord, error) {
	created, err := h.store.GetTemplate(c.Request.Context(), record.ID)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	if err := os.MkdirAll(layout.New(h.cfg.RootDir).TemplateDir(record.ID), 0o755); err != nil {
		return nil, err
	}

	if err := h.store.CreateTemplate(c.Request.Context(), record); err != nil {
		return nil, err
	}

	return h.store.GetTemplate(c.Request.Context(), record.ID)
}

func (h *Handler) ListTemplates(c *gin.Context) {
	if h.store == nil && h.artifactClient != nil {
		resp, err := h.artifactClient.ListTemplates(c.Request.Context(), &novitaboxv1.ListTemplatesRequest{})
		if err != nil {
			h.logger.Error("list templates via boxlet failed", "error", err)
			response.Error(c, response.ErrInternal("list templates failed"))
			return
		}
		response.JSON(c, http.StatusOK, templateProtoListToCompatibleResponse(resp.GetTemplates()))
		return
	}

	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}
	records, err := h.store.ListTemplates(c.Request.Context())
	if err != nil {
		h.logger.Error("list templates failed", "error", err)
		response.Error(c, response.ErrInternal("list templates failed"))
		return
	}

	templates := make([]compatibleTemplateResponse, 0, len(records))
	for _, record := range records {
		templates = append(templates, h.templateRecordToCompatibleResponse(c.Request.Context(), record))
	}
	response.JSON(c, http.StatusOK, templates)
}

func (h *Handler) GetTemplate(c *gin.Context) {
	templateID := c.Param("template_id")
	if err := validateTemplateID(templateID); err != nil {
		response.Error(c, response.ErrBadRequest(err.Error()))
		return
	}

	if h.store == nil && h.artifactClient != nil {
		info, err := h.artifactClient.GetTemplate(c.Request.Context(), &novitaboxv1.GetTemplateRequest{TemplateId: templateID})
		if err != nil {
			handleTemplateReadError(c, h, err, "get template failed")
			return
		}
		response.JSON(c, http.StatusOK, templateProtoToCompatibleDetailResponse(info))
		return
	}

	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}
	record, err := h.store.GetTemplate(c.Request.Context(), templateID)
	if err != nil {
		handleTemplateReadError(c, h, err, "get template failed")
		return
	}
	response.JSON(c, http.StatusOK, h.templateRecordToCompatibleDetailResponse(c.Request.Context(), *record))
}

func (h *Handler) DeleteTemplate(c *gin.Context) {
	templateID := c.Param("template_id")
	if err := validateTemplateID(templateID); err != nil {
		response.Error(c, response.ErrBadRequest(err.Error()))
		return
	}

	if h.artifactClient != nil {
		if _, err := h.artifactClient.DeleteTemplate(c.Request.Context(), &novitaboxv1.DeleteTemplateRequest{TemplateId: templateID}); err != nil {
			handleTemplateReadError(c, h, err, "delete template failed")
			return
		}
		c.Status(http.StatusNoContent)
		return
	}

	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}
	record, err := h.store.GetTemplate(c.Request.Context(), templateID)
	if err != nil {
		handleTemplateReadError(c, h, err, "delete template failed")
		return
	}
	if err := h.store.DeleteTemplate(c.Request.Context(), templateID); err != nil {
		handleTemplateReadError(c, h, err, "delete template failed")
		return
	}
	if record.RootfsPath != "" {
		if err := os.RemoveAll(filepath.Dir(record.RootfsPath)); err != nil {
			h.logger.Error("remove template artifact directory failed", "template_id", templateID, "path", filepath.Dir(record.RootfsPath), "error", err)
			response.Error(c, response.ErrInternal("delete template artifact failed"))
			return
		}
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) ConvertTemplate(c *gin.Context) {
	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}

	var req convertTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.ErrBadRequest("invalid template convert request body"))
		return
	}
	if err := validateTemplateID(req.TemplateID); err != nil {
		response.Error(c, response.ErrBadRequest(err.Error()))
		return
	}

	record, err := h.store.GetTemplate(c.Request.Context(), req.TemplateID)
	if err != nil {
		handleTemplateReadError(c, h, err, "get template failed")
		return
	}

	imageID := strings.TrimSpace(req.ImageID)
	if imageID == "" {
		imageID = "img-" + req.TemplateID
	}
	if strings.Contains(imageID, "/") || strings.Contains(imageID, "\\") || strings.Contains(imageID, "..") {
		response.Error(c, response.ErrBadRequest("imageID contains invalid path characters"))
		return
	}

	image := store.ImageRecord{
		ID:         imageID,
		RootfsPath: record.RootfsPath,
	}
	if err := h.store.CreateImage(c.Request.Context(), image); err != nil {
		h.logger.Error("create image from template failed", "template_id", req.TemplateID, "image_id", imageID, "error", err)
		response.Error(c, response.ErrInternal("create image from template failed"))
		return
	}
	created, err := h.store.GetImage(c.Request.Context(), imageID)
	if err != nil {
		h.logger.Error("get converted image failed", "image_id", imageID, "error", err)
		response.Error(c, response.ErrInternal("get converted image failed"))
		return
	}

	response.JSON(c, http.StatusCreated, convertTemplateResponse{
		ImageID:       created.ID,
		TemplateID:    req.TemplateID,
		RootfsPath:    created.RootfsPath,
		CreatedAtUnix: created.CreatedAt.Unix(),
	})
}

func (h *Handler) templateRecordToCompatibleResponse(ctx context.Context, record store.TemplateRecord) compatibleTemplateResponse {
	builds := h.templateBuilds(ctx, record.ID)
	latest := latestTemplateBuild(builds)
	diskSizeMB := templateDiskSizeMB(record)

	return compatibleTemplateResponse{
		Aliases:       record.Aliases,
		BuildCount:    int32(len(builds)),
		BuildID:       latestBuildID(latest),
		BuildStatus:   latestBuildStatus(latest, record),
		CPUCount:      templateCPUCount(record),
		CreatedAt:     record.CreatedAt,
		CreatedBy:     nil,
		DiskSizeMB:    diskSizeMB,
		EnvdVersion:   "",
		LastSpawnedAt: nil,
		MemoryMB:      templateMemoryMB(record),
		Metadata:      emptyMapAsNil(record.Metadata),
		Names:         record.Names,
		Public:        record.Public,
		SpawnCount:    0,
		TemplateID:    record.ID,
		UpdatedAt:     record.UpdatedAt,
	}
}

func (h *Handler) templateRecordToCompatibleDetailResponse(ctx context.Context, record store.TemplateRecord) compatibleTemplateWithBuildsResponse {
	builds := h.templateBuilds(ctx, record.ID)

	return compatibleTemplateWithBuildsResponse{
		Aliases:       record.Aliases,
		Builds:        templateBuildRecordsToCompatibleResponse(builds, templateDiskSizeMB(record), templateCPUCount(record), templateMemoryMB(record)),
		CreatedAt:     record.CreatedAt,
		LastSpawnedAt: nil,
		Metadata:      emptyMapAsNil(record.Metadata),
		Names:         record.Names,
		Public:        record.Public,
		SpawnCount:    0,
		TemplateID:    record.ID,
		UpdatedAt:     record.UpdatedAt,
	}
}

func (h *Handler) templateBuilds(ctx context.Context, templateID string) []store.TemplateBuildRecord {
	if h.store == nil {
		return nil
	}
	builds, err := h.store.ListTemplateBuilds(ctx, templateID)
	if err != nil {
		h.logger.Warn("list template builds failed", "template_id", templateID, "error", err)
		return nil
	}
	return builds
}

func templateProtoToCompatibleResponse(info *novitaboxv1.TemplateInfo) compatibleTemplateResponse {
	record := store.TemplateRecord{
		ID:           info.GetTemplateId(),
		RootfsPath:   info.GetRootfsPath(),
		MemfilePath:  info.GetMemfilePath(),
		SnapfilePath: info.GetSnapfilePath(),
		CreatedAt:    time.Unix(info.GetCreatedAtUnix(), 0).UTC(),
		UpdatedAt:    time.Unix(info.GetCreatedAtUnix(), 0).UTC(),
	}
	return compatibleTemplateResponse{
		Aliases:       []string{},
		BuildCount:    0,
		BuildID:       "",
		BuildStatus:   latestBuildStatus(nil, record),
		CPUCount:      defaultTemplateCPUCount,
		CreatedAt:     record.CreatedAt,
		CreatedBy:     nil,
		DiskSizeMB:    templateDiskSizeMB(record),
		EnvdVersion:   "",
		LastSpawnedAt: nil,
		MemoryMB:      defaultTemplateMemoryMB,
		Names:         []string{},
		Public:        false,
		SpawnCount:    0,
		TemplateID:    info.GetTemplateId(),
		UpdatedAt:     record.UpdatedAt,
	}
}

func templateProtoToCompatibleDetailResponse(info *novitaboxv1.TemplateInfo) compatibleTemplateWithBuildsResponse {
	record := store.TemplateRecord{
		ID:           info.GetTemplateId(),
		RootfsPath:   info.GetRootfsPath(),
		MemfilePath:  info.GetMemfilePath(),
		SnapfilePath: info.GetSnapfilePath(),
		CreatedAt:    time.Unix(info.GetCreatedAtUnix(), 0).UTC(),
		UpdatedAt:    time.Unix(info.GetCreatedAtUnix(), 0).UTC(),
	}
	return compatibleTemplateWithBuildsResponse{
		Aliases:       []string{},
		Builds:        []compatibleTemplateBuildResponse{},
		CreatedAt:     record.CreatedAt,
		LastSpawnedAt: nil,
		Names:         []string{},
		Public:        false,
		SpawnCount:    0,
		TemplateID:    info.GetTemplateId(),
		UpdatedAt:     record.UpdatedAt,
	}
}

func templateProtoListToCompatibleResponse(infos []*novitaboxv1.TemplateInfo) []compatibleTemplateResponse {
	templates := make([]compatibleTemplateResponse, 0, len(infos))
	for _, info := range infos {
		templates = append(templates, templateProtoToCompatibleResponse(info))
	}
	return templates
}

func templateBuildInfoFromRecord(record store.TemplateBuildRecord) templateBuildInfoResponse {
	out := templateBuildInfoResponse{
		BuildID:    record.ID,
		LogEntries: []buildLogEntry{},
		Logs:       []string{},
		Status:     string(record.Status),
		TemplateID: record.TemplateID,
	}
	if record.Status == store.TemplateBuildStatusError {
		out.Reason = &buildReason{Message: "template build failed"}
	}
	return out
}

func templateBuildRecordsToCompatibleResponse(records []store.TemplateBuildRecord, diskSizeMB int64, cpuCount int32, memoryMB int32) []compatibleTemplateBuildResponse {
	builds := make([]compatibleTemplateBuildResponse, 0, len(records))
	for _, record := range records {
		builds = append(builds, templateBuildRecordToCompatibleResponse(record, diskSizeMB, cpuCount, memoryMB))
	}
	return builds
}

func templateBuildRecordToCompatibleResponse(record store.TemplateBuildRecord, diskSizeMB int64, cpuCount int32, memoryMB int32) compatibleTemplateBuildResponse {
	var finishedAt *time.Time
	if record.Status == store.TemplateBuildStatusReady || record.Status == store.TemplateBuildStatusError {
		finishedAt = &record.UpdatedAt
	}
	return compatibleTemplateBuildResponse{
		BuildID:    record.ID,
		CPUCount:   cpuCount,
		CreatedAt:  record.CreatedAt,
		DiskSizeMB: &diskSizeMB,
		MemoryMB:   memoryMB,
		Status:     string(record.Status),
		FinishedAt: finishedAt,
		UpdatedAt:  record.UpdatedAt,
	}
}

func latestTemplateBuild(builds []store.TemplateBuildRecord) *store.TemplateBuildRecord {
	if len(builds) == 0 {
		return nil
	}
	return &builds[0]
}

func latestBuildID(build *store.TemplateBuildRecord) string {
	if build == nil {
		return ""
	}
	return build.ID
}

func latestBuildStatus(build *store.TemplateBuildRecord, record store.TemplateRecord) string {
	if build != nil {
		return string(build.Status)
	}
	if fileExists(record.RootfsPath) && fileExists(record.MemfilePath) && fileExists(record.SnapfilePath) {
		return string(store.TemplateBuildStatusReady)
	}
	return string(store.TemplateBuildStatusWaiting)
}

func templateDiskSizeMB(record store.TemplateRecord) int64 {
	const mib = 1024 * 1024
	total := fileSize(record.RootfsPath) + fileSize(record.MemfilePath) + fileSize(record.SnapfilePath)
	if total == 0 {
		return 0
	}
	return (total + mib - 1) / mib
}

func templateCPUCount(record store.TemplateRecord) int32 {
	if record.CPUCount > 0 {
		return record.CPUCount
	}
	return defaultTemplateCPUCount
}

func templateMemoryMB(record store.TemplateRecord) int32 {
	if record.MemoryMB > 0 {
		return record.MemoryMB
	}
	return defaultTemplateMemoryMB
}

func emptyMapAsNil(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func fileSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}
	return info.Size()
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func handleTemplateReadError(c *gin.Context, h *Handler, err error, message string) {
	if errors.Is(err, store.ErrNotFound) {
		response.Error(c, response.ErrNotFound("template not found"))
		return
	}
	if status.Code(err) == codes.NotFound {
		response.Error(c, response.ErrNotFound("template not found"))
		return
	}
	h.logger.Error(message, "error", err)
	response.Error(c, response.ErrInternal(message))
}

func newTemplateID() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz1234567890"
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return "tpl-" + string(b[:]), nil
}

func validateTemplateID(templateID string) error {
	if templateID == "" {
		return errors.New("templateID is required")
	}
	if strings.Contains(templateID, "/") || strings.Contains(templateID, "\\") || strings.Contains(templateID, "..") {
		return errors.New("templateID contains invalid path characters")
	}
	return nil
}

func newBuildID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16]), nil
}

func splitTemplateName(input string) (string, []string) {
	name := input
	tags := []string{}
	for i, ch := range input {
		if ch == ':' {
			name = input[:i]
			if i+1 < len(input) {
				tags = append(tags, input[i+1:])
			}
			break
		}
	}

	return name, tags
}

func derefStringSlice(values *[]string) []string {
	if values == nil {
		return nil
	}

	return *values
}

func deduplicateStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}

func newTemplateRecord(rootDir string, templateID string) store.TemplateRecord {
	dir := layout.New(rootDir).TemplateDir(templateID)

	return store.TemplateRecord{
		ID:           templateID,
		RootfsPath:   filepath.Join(dir, "rootfs.ext4"),
		MemfilePath:  filepath.Join(dir, "memfile"),
		SnapfilePath: filepath.Join(dir, "snapfile"),
	}
}
