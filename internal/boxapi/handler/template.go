package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/boxapi/response"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

type createTemplateV3Request struct {
	Alias    *string           `json:"alias,omitempty"`
	CPUCount *int32            `json:"cpuCount,omitempty"`
	MemoryMB *int32            `json:"memoryMB,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Name     *string           `json:"name,omitempty"`
	Tags     *[]string         `json:"tags,omitempty"`
	TeamID   *string           `json:"teamID,omitempty"`
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

	templateID, err := templateIDFromName(name)
	if err != nil {
		response.Error(c, response.ErrInternal("generate template id failed"))
		return
	}

	created, err := h.getOrCreateTemplate(c, templateID)
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

func (h *Handler) getOrCreateTemplate(c *gin.Context, templateID string) (*store.TemplateRecord, error) {
	created, err := h.store.GetTemplate(c.Request.Context(), templateID)
	if err == nil {
		return created, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	paths := templatePaths(h.cfg.RootDir, templateID)
	if err := os.MkdirAll(layout.New(h.cfg.RootDir).TemplateDir(templateID), 0o755); err != nil {
		return nil, err
	}

	record := store.TemplateRecord{
		ID:           templateID,
		RootfsPath:   paths.RootfsPath,
		MemfilePath:  paths.MemfilePath,
		SnapfilePath: paths.SnapfilePath,
	}
	if err := h.store.CreateTemplate(c.Request.Context(), record); err != nil {
		return nil, err
	}

	return h.store.GetTemplate(c.Request.Context(), templateID)
}

func (h *Handler) ListTemplates(c *gin.Context) {
	notImplemented(c, "list templates")
}

func (h *Handler) GetTemplate(c *gin.Context) {
	notImplemented(c, "get template")
}

func (h *Handler) DeleteTemplate(c *gin.Context) {
	notImplemented(c, "delete template")
}

func (h *Handler) ConvertTemplate(c *gin.Context) {
	notImplemented(c, "convert template")
}

func templateIDFromName(name string) (string, error) {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return "tpl-" + replacer.Replace(name), nil
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

func templatePaths(rootDir string, templateID string) store.TemplateRecord {
	dir := layout.New(rootDir).TemplateDir(templateID)

	return store.TemplateRecord{
		RootfsPath:   filepath.Join(dir, "rootfs.ext4"),
		MemfilePath:  filepath.Join(dir, "memfile"),
		SnapfilePath: filepath.Join(dir, "snapfile"),
	}
}
