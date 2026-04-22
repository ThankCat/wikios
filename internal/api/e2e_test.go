package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wikios/internal/api"
	"wikios/internal/app"
	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/service"
	"wikios/internal/store"
	"wikios/internal/tools"
	"wikios/internal/wikiadapter"
)

type mockLLM struct{}

func (mockLLM) Chat(_ context.Context, _ string, messages []llm.Message) (string, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "后台深度查询助手") {
		return `{
  "answer": "静态IP适合需要长期稳定网络环境的场景，例如账号长期运营、白名单绑定和远程办公。",
  "matched_pages": ["wiki/sources/customer-qa.md"],
  "source_paths": ["wiki/sources/customer-qa.md"],
  "contradictions": [],
  "limitations": []
}`, nil
	}
	if len(messages) > 0 && strings.Contains(messages[0].Content, "摄入助手") {
		return `{
  "summary": "已完成来源摄入",
  "source_title": "Customer QA",
  "source_slug": "customer-qa",
  "key_points": ["静态IP适合稳定场景"],
  "concepts_affected": ["静态IP"],
  "entities_affected": ["四叶天"],
  "concepts": [],
  "entities": [],
  "contradictions": [],
  "low_risk_fixes": [],
  "high_risk_proposals": [],
  "warnings": [],
  "possibly_outdated": false
}`, nil
	}
	return `{
  "answer_type": "text",
  "answer_markdown": "静态IP适合账号运营、白名单绑定和远程办公。",
  "sources": [{"path":"wiki/sources/customer-qa.md","confidence":"medium"}],
  "confidence": 0.9,
  "notes": ""
}`, nil
}

func (m mockLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := m.Chat(ctx, model, messages)
	if err != nil {
		return "", err
	}
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
}

func TestAdminLoginAndChat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)

	loginBody, _ := json.Marshal(map[string]any{
		"username": "admin",
		"password": "admin123",
	})
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", loginRec.Code, loginRec.Body.String())
	}
	cookie := loginRec.Result().Cookies()[0]

	chatBody, _ := json.Marshal(map[string]any{
		"message":   "静态IP适用什么场景？",
		"stream":    false,
		"mode_hint": "query",
	})
	chatRec := httptest.NewRecorder()
	chatReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatReq.AddCookie(cookie)
	router.ServeHTTP(chatRec, chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("chat failed: %d %s", chatRec.Code, chatRec.Body.String())
	}
	if !strings.Contains(chatRec.Body.String(), "长期稳定网络环境") {
		t.Fatalf("unexpected chat response: %s", chatRec.Body.String())
	}
}

func TestAdminUploadStoresAndAutoIngestsText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)
	cookie := loginCookie(t, router)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "customer.txt")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte("# Customer QA\n静态IP适合账号运营。"))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload failed: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已完成来源摄入") {
		t.Fatalf("unexpected upload response: %s", rec.Body.String())
	}
}

func TestPublicAnswerStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)

	body, _ := json.Marshal(map[string]any{
		"question": "静态IP适用什么场景？",
		"history": []map[string]any{
			{"role": "user", "content": "静态IP是什么？"},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream failed: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "event: delta") || !strings.Contains(rec.Body.String(), "event: result") {
		t.Fatalf("expected stream events, got %s", rec.Body.String())
	}
}

func buildRouter(t *testing.T) http.Handler {
	t.Helper()
	workspace := t.TempDir()
	root := createFixtureWiki(t)
	cfg := &config.Config{
		Server:      config.ServerConfig{Mode: "debug"},
		MountedWiki: config.MountedWikiConfig{Root: root, QMDIndex: "test-index"},
		Auth: config.AuthConfig{
			DefaultAdminUsername: "admin",
			DefaultAdminPassword: "admin123",
			SessionCookieName:    "wikios_admin_session",
			SessionTTLHours:      24,
		},
		Retrieval: config.RetrievalConfig{TopK: 3},
		Workspace: config.WorkspaceConfig{BaseDir: workspace, DefaultTimeoutSec: 5},
		Sandbox:   config.SandboxConfig{QMDTimeoutSec: 1, PythonTimeoutSec: 1},
		Sync:      config.SyncConfig{Remote: "origin", Branch: "main"},
		LLM:       config.LLMConfig{ModelAdmin: "test", ModelPublic: "test"},
		Storage:   config.StorageConfig{SQLitePath: filepath.Join(workspace, "service.db")},
	}
	dataStore, err := store.Open(cfg.Storage.SQLitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := dataStore.EnsureDefaultAdmin(context.Background(), "admin", "admin123"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{Config: cfg, Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root)})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	deps := service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          mockLLM{},
		Retriever:    retrieval.NewQMDRetriever(rt),
		Store:        dataStore,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	}
	handlers := api.NewHandlers(
		service.NewPublicQueryService(deps),
		service.NewAdminQueryService(deps),
		service.NewIngestService(deps),
		service.NewLintService(deps),
		service.NewReflectService(deps),
		service.NewRepairService(deps),
		service.NewSyncService(deps),
		service.NewUploadService(deps),
		dataStore,
		cfg.Auth,
	)
	return app.NewRouter(cfg, handlers, dataStore)
}

func loginCookie(t *testing.T, router http.Handler) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	return rec.Result().Cookies()[0]
}

func createFixtureWiki(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENT.md"), "# AGENT\n")
	mustWrite(t, filepath.Join(root, "wiki/index.md"), "# index\n")
	mustWrite(t, filepath.Join(root, "wiki/log.md"), "# log\n")
	mustWrite(t, filepath.Join(root, "wiki/templates/source-template.md"), "## Summary\n\n## Key Points\n\n## Concepts Extracted\n\n## Entities Extracted\n\n## Contradictions\n\n## My Notes\n")
	mustWrite(t, filepath.Join(root, "wiki/templates/concept-template.md"), "## Definition\n\n## Key Points\n\n## Contradictions\n\n## Sources\n\n## Evolution Log\n")
	mustWrite(t, filepath.Join(root, "wiki/templates/entity-template.md"), "## Description\n\n## Key Contributions\n\n## Sources\n")
	mustWrite(t, filepath.Join(root, "wiki/sources/customer-qa.md"), `---
title: Customer QA
raw_file: raw/articles/customer.txt
---

## Summary

静态IP适合长期稳定网络环境。
`)
	mustWrite(t, filepath.Join(root, "raw/articles/customer.txt"), "# Customer QA\n静态IP适合账号运营。")
	return root
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
