package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"wikios/internal/api"
	"wikios/internal/app"
	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/service"
	"wikios/internal/task"
	"wikios/internal/tools"
	"wikios/internal/wikiadapter"
)

type mockLLM struct{}

func (mockLLM) Chat(_ context.Context, _ string, _ []llm.Message) (string, error) {
	return "mock answer", nil
}

func TestAdminQueryTaskFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	workspace := t.TempDir()
	root := createFixtureWiki(t)
	cfg := &config.Config{
		Server:      config.ServerConfig{Mode: "debug"},
		MountedWiki: config.MountedWikiConfig{Root: root, QMDIndex: "test-index"},
		Auth:        config.AuthConfig{AdminBearerToken: "secret"},
		Retrieval:   config.RetrievalConfig{TopK: 3},
		Workspace:   config.WorkspaceConfig{BaseDir: workspace},
		Sandbox:     config.SandboxConfig{QMDTimeoutSec: 1},
		Sync:        config.SyncConfig{Remote: "origin", Branch: "main"},
		LLM:         config.LLMConfig{ModelAdmin: "test", ModelPublic: "test"},
		TaskStore:   config.TaskStoreConfig{SQLitePath: filepath.Join(workspace, "service.db")},
	}
	store, err := task.OpenStore(cfg.TaskStore.SQLitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{Config: cfg, Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root)})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	deps := service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          mockLLM{},
		Retriever:    retrieval.NewQMDRetriever(rt),
		TaskStore:    store,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: workspace,
	}
	handlers := api.NewHandlers(
		service.NewPublicQueryService(deps),
		service.NewAdminQueryService(deps),
		service.NewIngestService(deps),
		service.NewLintService(deps),
		service.NewReflectService(deps),
		service.NewRepairService(deps),
		service.NewSyncService(deps),
		task.NewManager(store),
	)
	router := app.NewRouter(cfg, handlers)

	reqBody, _ := json.Marshal(service.AdminQueryRequest{Question: "知识库系统规则", WriteOutput: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/query", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var accepted map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted: %v", err)
	}
	taskID := accepted["task_id"].(string)

	var statusBody []byte
	for range 40 {
		time.Sleep(100 * time.Millisecond)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tasks/"+taskID, nil)
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for task status, got %d", rec.Code)
		}
		statusBody = rec.Body.Bytes()
		if bytes.Contains(statusBody, []byte(`"status":"SUCCESS"`)) {
			break
		}
	}
	if !bytes.Contains(statusBody, []byte(`"status":"SUCCESS"`)) {
		t.Fatalf("task did not complete: %s", string(statusBody))
	}
	if !bytes.Contains(statusBody, []byte(`"output_file"`)) {
		t.Fatalf("expected output_file in task result: %s", string(statusBody))
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/query", bytes.NewReader(reqBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}
	health := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthRec := httptest.NewRecorder()
	router.ServeHTTP(healthRec, health)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected healthz to work")
	}
	if _, err := os.Stat(filepath.Join(root, "wiki/outputs")); err != nil {
		t.Fatalf("fixture outputs directory missing: %v", err)
	}
}

func createFixtureWiki(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "wiki", "outputs"))
	mustWrite(t, filepath.Join(root, "wiki", "index.md"), "---\ntype: system-index\ngraph-excluded: true\n---\n\n# System Index\n\n## Sources\n\n规则说明\n")
	mustWrite(t, filepath.Join(root, "wiki", "log.md"), "---\ntype: system-log\ngraph-excluded: true\n---\n\n# System Log\n")
	mustWrite(t, filepath.Join(root, "wiki", "overview.md"), "---\ntype: system-overview\ngraph-excluded: true\n---\n\n# System Overview\n")
	mustWrite(t, filepath.Join(root, "wiki", "QUESTIONS.md"), "---\ntype: system-questions\ngraph-excluded: true\n---\n\n# Questions\n\n## Open Questions\n")
	mustMkdirAll(t, filepath.Join(root, "wiki", "sources"))
	mustWrite(t, filepath.Join(root, "wiki", "sources", "rules.md"), "---\ntype: source\ntitle: 规则\nprocessed: true\n---\n\n## Summary\n\n知识库系统规则说明。\n\n## Key Points\n\n- 规则一\n")
	mustMkdirAll(t, filepath.Join(root, "wiki", "templates"))
	mustWrite(t, filepath.Join(root, "wiki", "templates", "source-template.md"), "---\ntype: source\ntitle: \"\"\ndate: 2026-04-22\nprocessed: false\n---\n\n## Summary\n\n## Key Points\n\n## Concepts Extracted\n\n## Entities Extracted\n\n## Contradictions\n\n## My Notes\n")
	mustMkdirAll(t, filepath.Join(root, "scripts"))
	mustWrite(t, filepath.Join(root, "scripts", "lint.py"), "#!/usr/bin/env python3\nprint('Wrote lint report to wiki/outputs/lint-2026-04-22.md')\n")
	run(t, root, "git", "init", "-b", "main")
	run(t, root, "git", "config", "user.email", "test@example.com")
	run(t, root, "git", "config", "user.name", "Test")
	run(t, root, "git", "add", ".")
	run(t, root, "git", "commit", "-m", "init")
	run(t, root, "git", "remote", "add", "origin", "https://example.com/repo.git")
	return root
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, string(out))
	}
}
