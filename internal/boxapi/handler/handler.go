package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/boxapi/response"
	"github.com/novitalabs/NovitaBox/internal/config"
	"github.com/novitalabs/NovitaBox/internal/log"
	novitaboxv1 "github.com/novitalabs/NovitaBox/internal/pb/novitabox/v1"
	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

type Handler struct {
	cfg            config.Config
	logger         *log.Logger
	store          store.Store
	sandboxClient  novitaboxv1.BoxletSandboxServiceClient
	artifactClient novitaboxv1.BoxletArtifactServiceClient
}

func New(cfg config.Config, logger *log.Logger, store store.Store, sandboxClient novitaboxv1.BoxletSandboxServiceClient, artifactClient novitaboxv1.BoxletArtifactServiceClient) *Handler {
	if logger == nil {
		logger = log.NewNop()
	}
	return &Handler{cfg: cfg, logger: logger, store: store, sandboxClient: sandboxClient, artifactClient: artifactClient}
}

func (h *Handler) Healthz(c *gin.Context) {
	response.JSON(c, http.StatusOK, gin.H{
		"status": "ok",
	})
}

func notImplemented(c *gin.Context, operation string) {
	response.Error(c, response.ErrNotImplemented(operation+" is not implemented yet"))
}
