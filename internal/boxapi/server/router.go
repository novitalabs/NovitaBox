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

	r.GET("/healthz", h.Healthz)

	v2 := r.Group("/v2")
	{
		v2.POST("/templates/:template_id/builds/:build_id", h.StartTemplateBuildV2)
	}

	v3 := r.Group("/v3")
	{
		v3.POST("/templates", h.CreateTemplateV3)
	}

	v1 := r.Group("/v1")
	{
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
		}

		images := v1.Group("/images")
		{
			images.POST("", h.CreateImage)
			images.GET("", h.ListImages)
			images.GET("/:image_id", h.GetImage)
			images.DELETE("/:image_id", h.DeleteImage)
		}

		runtimes := v1.Group("/runtimes")
		{
			runtimes.GET("", h.ListRuntimes)
			runtimes.GET("/:runtime_type", h.GetRuntime)
			runtimes.GET("/:runtime_type/capabilities", h.GetRuntimeCapabilities)
		}

		templates := v1.Group("/templates")
		{
			templates.GET("", h.ListTemplates)
			templates.POST("/convert", h.ConvertTemplate)
			templates.GET("/:template_id", h.GetTemplate)
			templates.DELETE("/:template_id", h.DeleteTemplate)
		}
	}

	r.NoRoute(func(c *gin.Context) {
		response.Error(c, response.ErrNotFound("route not found"))
	})

	return r
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
