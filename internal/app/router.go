package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	registerWebRoutes(r, cfg)

	return r
}

func registerWebRoutes(r *gin.Engine, cfg *config.Config) {
	webEnabled := cfg.Web.Enabled == nil || *cfg.Web.Enabled
	if !webEnabled {
		return
	}

	distDir := cfg.Web.DistDir
	r.GET("/app-config.json", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{
			"mountedWikiName": cfg.MountedWiki.Name,
			"webEnabled":      webEnabled,
		})
	})

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || path == "/healthz" {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"code":    "NOT_FOUND",
					"message": "route not found",
				},
			})
			return
		}

		requested := filepath.Clean(strings.TrimPrefix(path, "/"))
		if requested == "." {
			requested = "index.html"
		}
		if requested == ".." || strings.HasPrefix(requested, ".."+string(filepath.Separator)) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "INVALID_PATH",
					"message": "invalid static asset path",
				},
			})
			return
		}
		target := filepath.Join(distDir, requested)
		if fileExists(target) {
			c.File(target)
			return
		}

		indexPath := filepath.Join(distDir, "index.html")
		if fileExists(indexPath) {
			c.File(indexPath)
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusServiceUnavailable, missingWebBuildHTML(distDir))
	})
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func missingWebBuildHTML(distDir string) string {
	payload, _ := json.Marshal(map[string]string{
		"message": "web frontend has not been built",
		"distDir": distDir,
		"hint":    "cd web && bun install && bun run build",
	})
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Web Build Missing</title>
    <style>
      body { font-family: ui-sans-serif, system-ui, sans-serif; background: #0f172a; color: #e2e8f0; margin: 0; padding: 40px; }
      .card { max-width: 720px; margin: 40px auto; padding: 24px; border-radius: 16px; background: #111827; border: 1px solid #334155; }
      code { background: #1e293b; padding: 2px 6px; border-radius: 6px; }
      pre { background: #020617; padding: 16px; border-radius: 12px; overflow: auto; }
    </style>
  </head>
  <body>
    <div class="card">
      <h1>Frontend build not found</h1>
      <p>Gin is configured to serve the SPA from <code>%s</code>, but no build output was found.</p>
      <p>Run <code>cd web && bun install && bun run build</code> and refresh this page.</p>
      <pre>%s</pre>
    </div>
  </body>
</html>`, distDir, payload)
}
