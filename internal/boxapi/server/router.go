package server

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/boxapi/handler"
	"github.com/novitalabs/NovitaBox/internal/boxapi/response"
)

func (s *Server) router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(s.accessLog())

	h := handler.New(s.cfg, s.logger.Component("handler"), s.store, s.sandbox, s.artifact)

	registerCommonRoutes(r, h)
	registerCompatibleRoutes(r, h)
	registerV1Routes(r, h)
	registerV2Routes(r, h)
	registerV3Routes(r, h)

	r.NoRoute(func(c *gin.Context) {
		response.Error(c, response.ErrNotFound("route not found"))
	})

	return r
}

func registerCommonRoutes(r *gin.Engine, h *handler.Handler) {
	r.GET("/health", h.Healthz)
	r.GET("/healthz", h.Healthz)
}

func registerCompatibleRoutes(r *gin.Engine, h *handler.Handler) {
	templateRoutes := r.Group("/templates")
	{
		templateRoutes.GET("", h.ListTemplates)
		templateRoutes.GET("/:template_id", h.GetTemplate)
		templateRoutes.DELETE("/:template_id", h.DeleteTemplate)
		templateRoutes.GET("/:template_id/builds/:build_id/status", h.GetTemplateBuildStatus)
	}

	sandboxRoutes := r.Group("/sandboxes")
	{
		sandboxRoutes.POST("", h.CreateSandbox)
		sandboxRoutes.GET("", h.ListSandboxesV2)
		sandboxRoutes.GET("/:sandbox_id", h.GetSandbox)
		sandboxRoutes.DELETE("/:sandbox_id", h.KillSandbox)
		sandboxRoutes.POST("/:sandbox_id/pause", h.PauseSandbox)
		sandboxRoutes.POST("/:sandbox_id/connect", h.ConnectSandbox)
		sandboxRoutes.POST("/:sandbox_id/timeout", h.SetSandboxTimeout)
	}
}

func registerV1Routes(r *gin.Engine, h *handler.Handler) {
	v1 := r.Group("/v1")
	{
		registerV1SandboxRoutes(v1, h)
		registerV1TemplateRoutes(v1, h)
		registerV1ImageRoutes(v1, h)
		registerV1RuntimeRoutes(v1, h)
	}
}

func registerV1SandboxRoutes(v1 *gin.RouterGroup, h *handler.Handler) {
	sandboxes := v1.Group("/sandboxes")
	{
		sandboxes.POST("", h.CreateSandbox)
		sandboxes.GET("", h.ListSandboxes)
		sandboxes.GET("/:sandbox_id", h.GetSandbox)
		sandboxes.DELETE("/:sandbox_id", h.KillSandbox)
		sandboxes.POST("/:sandbox_id/pause", h.PauseSandbox)
		sandboxes.POST("/:sandbox_id/resume", h.ResumeSandbox)

		sandboxes.POST("/:sandbox_id/poweroff", h.PoweroffSandbox)
		sandboxes.POST("/:sandbox_id/poweron", h.PoweronSandbox)
		sandboxes.POST("/:sandbox_id/reboot", h.RebootSandbox)
		sandboxes.GET("/:sandbox_id/balloon", h.GetSandboxBalloon)
		sandboxes.PATCH("/:sandbox_id/balloon", h.UpdateSandboxBalloon)
		sandboxes.GET("/:sandbox_id/balloon/statistics", h.GetSandboxBalloonStats)
		sandboxes.PATCH("/:sandbox_id/balloon/statistics", h.UpdateSandboxBalloonStats)
		sandboxes.GET("/:sandbox_id/balloon/hinting", h.GetSandboxBalloonHinting)
		sandboxes.POST("/:sandbox_id/balloon/hinting/start", h.StartSandboxBalloonHinting)
		sandboxes.POST("/:sandbox_id/balloon/hinting/stop", h.StopSandboxBalloonHinting)
	}
}

func registerV1TemplateRoutes(v1 *gin.RouterGroup, h *handler.Handler) {
	templates := v1.Group("/templates")
	{
		templates.POST("/convert", h.ConvertTemplate)
	}
}

func registerV1ImageRoutes(v1 *gin.RouterGroup, h *handler.Handler) {
	images := v1.Group("/images")
	{
		images.POST("", h.CreateImage)
		images.GET("", h.ListImages)
		images.GET("/:image_id", h.GetImage)
		images.DELETE("/:image_id", h.DeleteImage)
	}
}

func registerV1RuntimeRoutes(v1 *gin.RouterGroup, h *handler.Handler) {
	runtimes := v1.Group("/runtimes")
	{
		runtimes.GET("", h.ListRuntimes)
		runtimes.GET("/:runtime_type", h.GetRuntime)
		runtimes.GET("/:runtime_type/capabilities", h.GetRuntimeCapabilities)
	}
}

func registerV2Routes(r *gin.Engine, h *handler.Handler) {
	v2 := r.Group("/v2")
	{
		v2.GET("/sandboxes", h.ListSandboxesV2)
		v2.POST("/templates/:template_id/builds/:build_id", h.StartTemplateBuildV2)
		v2.GET("/templates/:template_id/builds/:build_id/status", h.GetTemplateBuildStatus)
	}
}

func registerV3Routes(r *gin.Engine, h *handler.Handler) {
	v3 := r.Group("/v3")
	{
		v3.POST("/templates", h.CreateTemplateV3)
	}
}

func (s *Server) accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		startedAt := time.Now()
		c.Next()

		s.logger.Info("http request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency", time.Since(startedAt).String(),
		)
	}
}
