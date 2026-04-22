package app

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"wikios/internal/api"
	"wikios/internal/app/middleware"
	"wikios/internal/config"
)

func NewRouter(cfg *config.Config, handlers *api.Handlers) *gin.Engine {
	r := gin.New()
	r.Use(
		middleware.Trace(),
		middleware.Logger(),
		middleware.Recovery(),
	)

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	public := r.Group("/api/v1/public")
	public.POST("/answer", handlers.PublicAnswer)

	admin := r.Group("/api/v1/admin")
	admin.Use(middleware.AdminAuth(cfg.Auth.AdminBearerToken))
	admin.POST("/ingest", handlers.AdminIngest)
	admin.POST("/query", handlers.AdminQueryRun)
	admin.POST("/lint", handlers.AdminLint)
	admin.POST("/reflect", handlers.AdminReflect)
	admin.POST("/repair/apply-low-risk", handlers.AdminRepairApplyLowRisk)
	admin.POST("/repair/apply-proposal", handlers.AdminRepairApplyProposal)
	admin.POST("/sync", handlers.AdminSync)
	admin.GET("/tasks/:id", handlers.AdminTaskStatus)

	return r
}
