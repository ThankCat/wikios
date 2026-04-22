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
	"wikios/internal/store"
)

func TestRouterServesMissingWebBuildPage(t *testing.T) {
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{Name: "fixture-wiki"},
		Auth:        config.AuthConfig{SessionCookieName: "wikios_admin_session"},
		Web:         config.WebConfig{DistDir: filepath.Join(t.TempDir(), "dist")},
	}
	router := app.NewRouter(cfg, &api.Handlers{}, dataStore)

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

func TestRouterServesStaticFilesAndAPINotFound(t *testing.T) {
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
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{Name: "fixture-wiki"},
		Auth:        config.AuthConfig{SessionCookieName: "wikios_admin_session"},
		Web:         config.WebConfig{DistDir: distDir},
	}
	router := app.NewRouter(cfg, &api.Handlers{}, dataStore)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log('ok');") {
		t.Fatalf("expected static asset, got %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/chat", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "workbench") {
		t.Fatalf("expected spa fallback, got %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown api route, got %d", rec.Code)
	}
}
