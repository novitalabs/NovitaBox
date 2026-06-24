package handler

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/boxapi/response"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/layout"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type createImageRequest struct {
	ImageID     string            `json:"imageID"`
	TemplateID  string            `json:"templateID,omitempty"`
	SandboxID   string            `json:"sandboxID,omitempty"`
	DockerImage string            `json:"dockerImage,omitempty"`
	RootfsPath  string            `json:"rootfsPath,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type imageResponse struct {
	ImageID       string            `json:"imageID"`
	RootfsPath    string            `json:"rootfsPath"`
	CreatedAtUnix int64             `json:"createdAtUnix"`
	Labels        map[string]string `json:"labels,omitempty"`
}

type listImagesResponse struct {
	Images []imageResponse `json:"images"`
}

func (h *Handler) CreateImage(c *gin.Context) {
	var req createImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, response.ErrBadRequest("invalid image request body"))
		return
	}
	if err := validateImageID(req.ImageID); err != nil {
		response.Error(c, response.ErrBadRequest(err.Error()))
		return
	}
	if req.TemplateID == "" && req.SandboxID == "" && req.DockerImage == "" && req.RootfsPath == "" {
		response.Error(c, response.ErrBadRequest("one of templateID, sandboxID, dockerImage, or rootfsPath is required"))
		return
	}

	if h.artifactClient != nil {
		info, err := h.artifactClient.CreateImage(c.Request.Context(), &novitaboxv1.CreateImageRequest{
			ImageId:     req.ImageID,
			TemplateId:  req.TemplateID,
			SandboxId:   req.SandboxID,
			DockerImage: imageDockerSource(req),
			Labels:      req.Labels,
		})
		if err != nil {
			handleImageReadError(c, h, err, "create image failed")
			return
		}
		response.JSON(c, http.StatusCreated, imageProtoToResponse(info))
		return
	}

	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}
	record, err := h.createLocalImage(c, req)
	if err != nil {
		handleImageReadError(c, h, err, "create image failed")
		return
	}
	response.JSON(c, http.StatusCreated, imageRecordToResponse(*record))
}

func (h *Handler) ListImages(c *gin.Context) {
	if h.artifactClient != nil {
		resp, err := h.artifactClient.ListImages(c.Request.Context(), &novitaboxv1.ListImagesRequest{})
		if err != nil {
			h.logger.Error("list images via boxlet failed", "error", err)
			response.Error(c, response.ErrInternal("list images failed"))
			return
		}
		response.JSON(c, http.StatusOK, listImagesResponse{Images: imageProtoListToResponse(resp.GetImages())})
		return
	}

	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}
	records, err := h.store.ListImages(c.Request.Context())
	if err != nil {
		h.logger.Error("list images failed", "error", err)
		response.Error(c, response.ErrInternal("list images failed"))
		return
	}

	images := make([]imageResponse, 0, len(records))
	for _, record := range records {
		images = append(images, imageRecordToResponse(record))
	}
	response.JSON(c, http.StatusOK, listImagesResponse{Images: images})
}

func (h *Handler) GetImage(c *gin.Context) {
	imageID := c.Param("image_id")
	if err := validateImageID(imageID); err != nil {
		response.Error(c, response.ErrBadRequest(err.Error()))
		return
	}

	if h.artifactClient != nil {
		info, err := h.artifactClient.GetImage(c.Request.Context(), &novitaboxv1.GetImageRequest{ImageId: imageID})
		if err != nil {
			handleImageReadError(c, h, err, "get image failed")
			return
		}
		response.JSON(c, http.StatusOK, imageProtoToResponse(info))
		return
	}

	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}
	record, err := h.store.GetImage(c.Request.Context(), imageID)
	if err != nil {
		handleImageReadError(c, h, err, "get image failed")
		return
	}
	response.JSON(c, http.StatusOK, imageRecordToResponse(*record))
}

func (h *Handler) DeleteImage(c *gin.Context) {
	imageID := c.Param("image_id")
	if err := validateImageID(imageID); err != nil {
		response.Error(c, response.ErrBadRequest(err.Error()))
		return
	}

	if h.artifactClient != nil {
		if _, err := h.artifactClient.DeleteImage(c.Request.Context(), &novitaboxv1.DeleteImageRequest{ImageId: imageID}); err != nil {
			handleImageReadError(c, h, err, "delete image failed")
			return
		}
		c.Status(http.StatusNoContent)
		return
	}

	if h.store == nil {
		response.Error(c, response.ErrInternal("storage is not configured"))
		return
	}
	record, err := h.store.GetImage(c.Request.Context(), imageID)
	if err != nil {
		handleImageReadError(c, h, err, "delete image failed")
		return
	}
	if err := h.store.DeleteImage(c.Request.Context(), imageID); err != nil {
		handleImageReadError(c, h, err, "delete image failed")
		return
	}
	if shouldRemoveImageDir(h.cfg.RootDir, imageID, record.RootfsPath) {
		if err := os.RemoveAll(layout.New(h.cfg.RootDir).ImageDir(imageID)); err != nil {
			h.logger.Error("remove image artifact directory failed", "image_id", imageID, "error", err)
			response.Error(c, response.ErrInternal("delete image artifact failed"))
			return
		}
	}

	c.Status(http.StatusNoContent)
}

func (h *Handler) createLocalImage(c *gin.Context, req createImageRequest) (*store.ImageRecord, error) {
	imageDir := layout.New(h.cfg.RootDir).ImageDir(req.ImageID)
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return nil, fmt.Errorf("create image directory: %w", err)
	}
	rootfsPath := filepath.Join(imageDir, "rootfs.ext4")

	source, err := h.localImageRootfsSource(c, req)
	if err != nil {
		return nil, err
	}
	if source != "" {
		if err := copyLocalFile(source, rootfsPath); err != nil {
			return nil, err
		}
	}

	record := store.ImageRecord{
		ID:         req.ImageID,
		RootfsPath: rootfsPath,
	}
	if err := h.store.CreateImage(c.Request.Context(), record); err != nil {
		return nil, err
	}
	return h.store.GetImage(c.Request.Context(), req.ImageID)
}

func (h *Handler) localImageRootfsSource(c *gin.Context, req createImageRequest) (string, error) {
	switch {
	case req.RootfsPath != "":
		return req.RootfsPath, nil
	case req.TemplateID != "":
		record, err := h.store.GetTemplate(c.Request.Context(), req.TemplateID)
		if err != nil {
			return "", err
		}
		return record.RootfsPath, nil
	case req.SandboxID != "":
		if _, err := h.store.GetSandbox(c.Request.Context(), req.SandboxID); err != nil {
			return "", err
		}
		return localSandboxRuntimePaths(h.cfg.RootDir, req.SandboxID).RootfsPath, nil
	case req.DockerImage != "":
		if isLocalRootfsFile(req.DockerImage) {
			return req.DockerImage, nil
		}
		return "", errors.New("docker image materialization requires boxlet")
	default:
		return "", nil
	}
}

func imageDockerSource(req createImageRequest) string {
	if req.DockerImage != "" {
		return req.DockerImage
	}
	return req.RootfsPath
}

func imageRecordToResponse(record store.ImageRecord) imageResponse {
	return imageResponse{
		ImageID:       record.ID,
		RootfsPath:    record.RootfsPath,
		CreatedAtUnix: record.CreatedAt.Unix(),
	}
}

func imageProtoToResponse(info *novitaboxv1.ImageInfo) imageResponse {
	return imageResponse{
		ImageID:       info.GetImageId(),
		RootfsPath:    info.GetRootfsPath(),
		CreatedAtUnix: info.GetCreatedAtUnix(),
		Labels:        info.GetLabels(),
	}
}

func imageProtoListToResponse(infos []*novitaboxv1.ImageInfo) []imageResponse {
	images := make([]imageResponse, 0, len(infos))
	for _, info := range infos {
		images = append(images, imageProtoToResponse(info))
	}
	return images
}

func isImageAlreadyExistsError(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func handleImageReadError(c *gin.Context, h *Handler, err error, message string) {
	if errors.Is(err, store.ErrNotFound) || status.Code(err) == codes.NotFound {
		response.Error(c, response.ErrNotFound("image not found"))
		return
	}
	if status.Code(err) == codes.AlreadyExists || isImageAlreadyExistsError(err) {
		response.Error(c, response.ErrConflict("image already exists"))
		return
	}
	if status.Code(err) == codes.InvalidArgument {
		response.Error(c, response.ErrBadRequest(status.Convert(err).Message()))
		return
	}
	h.logger.Error(message, "error", err)
	response.Error(c, response.ErrInternal(message))
}

func validateImageID(imageID string) error {
	if imageID == "" {
		return errors.New("imageID is required")
	}
	if strings.Contains(imageID, "/") || strings.Contains(imageID, "\\") || strings.Contains(imageID, "..") {
		return errors.New("imageID contains invalid path characters")
	}
	return nil
}

func shouldRemoveImageDir(rootDir string, imageID string, rootfsPath string) bool {
	imageDir := layout.New(rootDir).ImageDir(imageID)
	return rootfsPath == "" || filepath.Dir(rootfsPath) == imageDir
}

func isLocalRootfsFile(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") {
		info, err := os.Stat(value)
		return err == nil && !info.IsDir()
	}
	return false
}

func copyLocalFile(src string, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source %q: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create destination dir: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write destination %q: %w", dst, err)
	}
	return nil
}
