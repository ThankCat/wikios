package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/api"
	"wikios/internal/app"
	"wikios/internal/config"
)

func TestRouterServesMissingWebBuildPage(t *testing.T) {
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{Name: "fixture-wiki"},
		Auth:        config.AuthConfig{AdminBearerToken: "secret"},
		Web:         config.WebConfig{DistDir: filepath.Join(t.TempDir(), "dist")},
	}
	router := app.NewRouter(cfg, &api.Handlers{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Frontend build not found") {
		t.Fatalf("expected missing build page, got %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/app-config.json", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var payload struct {
		MountedWikiName string `json:"mountedWikiName"`
		WebEnabled      bool   `json:"webEnabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode app config: %v", err)
	}
	if payload.MountedWikiName != "fixture-wiki" || !payload.WebEnabled {
		t.Fatalf("unexpected app config payload: %+v", payload)
	}
}

func TestRouterServesStaticFilesAndSPAFallback(t *testing.T) {
	distDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte("<html><body>workbench</body></html>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(distDir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "assets", "app.js"), []byte("console.log('ok');"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{Name: "fixture-wiki"},
		Auth:        config.AuthConfig{AdminBearerToken: "secret"},
		Web:         config.WebConfig{DistDir: distDir},
	}
	router := app.NewRouter(cfg, &api.Handlers{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log('ok');") {
		t.Fatalf("expected static asset, got %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/login", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "workbench") {
		t.Fatalf("expected spa fallback, got %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/../secrets.txt", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid path, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown api route, got %d", rec.Code)
	}
}
