package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if len(messages) > 0 && strings.Contains(messages[0].Content, "管理员全权限直连模式") {
		return mockDirectAdminResponse(messages), nil
	}
	if len(messages) > 0 && strings.Contains(messages[0].Content, "后台深度查询助手") {
		return `{
  "answer": "静态IP适合需要长期稳定网络环境的场景，例如账号长期运营、白名单绑定和远程办公。",
  "matched_pages": ["wiki/sources/customer-qa.md"],
  "source_paths": ["wiki/sources/customer-qa.md"],
  "contradictions": [],
  "limitations": []
}`, nil
	}
	if len(messages) > 0 && strings.Contains(messages[0].Content, "结构化 FAQ 摄入分析器") {
		return `{
  "summary": "这是 FAQ 数据分段",
  "source_title": "FAQ 分段",
  "source_slug": "faq-segment",
  "key_points": ["这是 FAQ 数据分段"],
  "concepts_affected": [],
  "entities_affected": [],
  "concepts": [],
  "entities": [],
  "contradictions": [],
  "low_risk_fixes": [],
  "high_risk_proposals": [],
  "warnings": [],
  "possibly_outdated": false
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

type partialFailFAQStreamLLM struct{}

func (partialFailFAQStreamLLM) Chat(_ context.Context, _ string, messages []llm.Message) (string, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "管理员全权限直连模式") {
		userPrompt := ""
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				userPrompt = messages[i].Content
				break
			}
		}
		if strings.Contains(userPrompt, "segment_index: 2") && !strings.Contains(userPrompt, "shell_result:") {
			return "", fmt.Errorf("context deadline exceeded (Client.Timeout or context cancellation while reading body)")
		}
		return mockDirectAdminResponse(messages), nil
	}
	if len(messages) > 1 && strings.Contains(messages[0].Content, "结构化 FAQ 摄入分析器") {
		if strings.Contains(messages[1].Content, "segment_index=2") {
			return "", fmt.Errorf("context deadline exceeded (Client.Timeout or context cancellation while reading body)")
		}
		return `{
  "summary": "这是 FAQ 数据分段",
  "source_title": "FAQ 分段",
  "source_slug": "faq-segment",
  "key_points": ["这是 FAQ 数据分段"],
  "concepts_affected": [],
  "entities_affected": [],
  "concepts": [],
  "entities": [],
  "contradictions": [],
  "low_risk_fixes": [],
  "high_risk_proposals": [],
  "warnings": [],
  "possibly_outdated": false
}`, nil
	}
	return mockLLM{}.Chat(context.Background(), "", messages)
}

func (m partialFailFAQStreamLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := m.Chat(ctx, model, messages)
	if err != nil {
		return "", err
	}
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
}

func mockDirectAdminResponse(messages []llm.Message) string {
	userPrompt := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			userPrompt = messages[i].Content
			break
		}
	}
	fullPrompt := ""
	if len(messages) > 1 {
		fullPrompt = messages[1].Content
	}
	if strings.Contains(userPrompt, "shell_result:") {
		switch {
		case strings.Contains(fullPrompt, "模式提示：\ningest"):
			return `{"action":"final","reply":"FAQ 数据兼容摄入已完成。","summary":"FAQ 数据兼容摄入已完成。","artifacts":["wiki/sources/faq-generated.md"],"output_files":["wiki/sources/faq-generated.md"],"warnings":[]}`
		default:
			return `{"action":"final","reply":"管理员直连执行完成。","summary":"管理员直连执行完成"}`
		}
	}
	switch {
	case strings.Contains(fullPrompt, "模式提示：\nquery"):
		return `{"action":"final","reply":"静态IP适合需要长期稳定网络环境的场景，例如账号长期运营、白名单绑定和远程办公。","summary":"管理员查询完成","answer":"静态IP适合需要长期稳定网络环境的场景，例如账号长期运营、白名单绑定和远程办公。","artifacts":["wiki/sources/customer-qa.md"],"output_files":[],"warnings":[]}`
	case strings.Contains(fullPrompt, "模式提示：\ningest") && strings.Contains(fullPrompt, "segment_title:"):
		return `{"action":"shell","command":"mkdir -p wiki/sources && printf '%s' '## Summary\n\nmock\n\n## Key Points\n\n- mock\n\n## FAQ Entries\n\n### 测试问题\n\n分类：常见问题\n\n回复：\n测试回复\n' > wiki/sources/faq-generated.md","reason":"写入 FAQ source 页"}`
	case strings.Contains(fullPrompt, "模式提示：\ningest"):
		return `{"action":"final","reply":"已完成来源摄入","summary":"已完成来源摄入","artifacts":["wiki/sources/customer-qa.md"],"output_files":["wiki/sources/customer-qa.md"],"warnings":[]}`
	case strings.Contains(fullPrompt, "模式提示：\nlint"):
		return `{"action":"final","reply":"健康检查完成","summary":"健康检查完成","warnings":[]}`
	case strings.Contains(fullPrompt, "模式提示：\nreflect"):
		return `{"action":"final","reply":"反思分析完成","summary":"反思分析完成","warnings":[]}`
	case strings.Contains(fullPrompt, "模式提示：\nrepair"):
		return `{"action":"final","reply":"修复完成","summary":"修复完成","warnings":[]}`
	case strings.Contains(fullPrompt, "模式提示：\nsync"):
		return `{"action":"final","reply":"同步完成","summary":"同步完成","warnings":[]}`
	default:
		return `{"action":"final","reply":"管理员直连执行完成。","summary":"管理员直连执行完成","warnings":[]}`
	}
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

func TestAdminUploadAutoSegmentsLargeFAQTable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)
	cookie := loginCookie(t, router)

	var builder strings.Builder
	builder.WriteString("| 技能分类 | 标准问题 | 回复内容 |\n")
	builder.WriteString("| --- | --- | --- |\n")
	for i := 0; i < 140; i++ {
		builder.WriteString(fmt.Sprintf("| 产品咨询 | 问题 %d？ | 回复内容 %d |\n", i, i))
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "faq.md")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte(builder.String()))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected success, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "成功 2 段") {
		t.Fatalf("unexpected upload reply: %s", rec.Body.String())
	}
}

func TestAdminUploadSupportsFAQJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)
	cookie := loginCookie(t, router)

	raw := `{
  "types": [{"id": "type-1", "category": "账号与登录"}],
  "faq": [{
    "id": "faq-1",
    "question": "你们的IP能访问微信不",
    "answer": "<p>不可以用于微信登录业务。</p>",
    "type_id": "type-1",
    "condition_template": []
  }],
  "sims": [{
    "parent_id": "faq-1",
    "question": "你们的IP能访问微信不吗"
  }],
  "ws_info": {"wordslots": []}
}`

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "FAQ数据.json")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte(raw))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected success, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "FAQ 数据兼容摄入已完成") {
		t.Fatalf("unexpected upload reply: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"tool\":\"llm.chat\"") {
		t.Fatalf("expected upload details to include llm execution steps, got %s", rec.Body.String())
	}
}

func TestAdminUploadSupportsFAQJSONWithLegacySourceTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := createFixtureWiki(t)
	mustWrite(t, filepath.Join(root, "wiki/templates/source-template.md"), "## Summary\n\n## Key Points\n\n## Concepts Extracted\n\n## Entities Extracted\n\n## Contradictions\n\n## My Notes\n")
	router := buildRouterWithRoot(t, root)
	cookie := loginCookie(t, router)

	raw := `{
  "types": [{"id": "type-1", "category": "账号与登录"}],
  "faq": [{
    "id": "faq-1",
    "question": "你们的IP能访问微信不",
    "answer": "<p>不可以用于微信登录业务。</p>",
    "type_id": "type-1",
    "condition_template": []
  }]
}`

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "FAQ数据.json")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte(raw))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected success with legacy template, got %d %s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/sources"))
	if err != nil {
		t.Fatalf("read sources dir: %v", err)
	}
	generated := ""
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "faq-") && strings.HasSuffix(name, ".md") {
			generated = name
			break
		}
	}
	if generated == "" {
		t.Fatalf("expected generated FAQ source page under wiki/sources")
	}
	content, err := os.ReadFile(filepath.Join(root, "wiki/sources", generated))
	if err != nil {
		t.Fatalf("read generated source page: %v", err)
	}
	if !strings.Contains(string(content), "## FAQ Entries") {
		t.Fatalf("expected generated page to inject FAQ Entries section, got %s", string(content))
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

func TestAdminUploadStreamEmitsSegmentEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)
	cookie := loginCookie(t, router)

	var builder strings.Builder
	builder.WriteString("| 技能分类 | 标准问题 | 回复内容 |\n")
	builder.WriteString("| --- | --- | --- |\n")
	for i := 0; i < 140; i++ {
		builder.WriteString(fmt.Sprintf("| 产品咨询 | 问题 %d？ | 回复内容 %d |\n", i, i))
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "faq.md")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte(builder.String()))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/upload/stream", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream upload failed: %d %s", rec.Code, rec.Body.String())
	}
	for _, marker := range []string{"event: meta", "event: ingest_plan", "event: segment_start", "event: segment_result", "event: result", "event: done"} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Fatalf("expected %s in stream body, got %s", marker, rec.Body.String())
		}
	}
}

func TestAdminUploadStreamFallsBackWhenFAQSegmentLLMTimesOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := createFixtureWiki(t)
	router := buildRouterWithRootAndClient(t, root, partialFailFAQStreamLLM{})
	cookie := loginCookie(t, router)

	var builder strings.Builder
	builder.WriteString("| 技能分类 | 标准问题 | 回复内容 |\n")
	builder.WriteString("| --- | --- | --- |\n")
	for i := 0; i < 140; i++ {
		builder.WriteString(fmt.Sprintf("| 产品咨询 | 问题 %d？ | 回复内容 %d |\n", i, i))
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "faq.md")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte(builder.String()))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/upload/stream", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream upload failed: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "event: segment_error") {
		t.Fatalf("expected segment_error event, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"partial_success\":true") {
		t.Fatalf("expected partial_success in stream body, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"status\":\"PARTIAL_SUCCESS\"") {
		t.Fatalf("expected partial execution status, got %s", rec.Body.String())
	}
}

func buildRouter(t *testing.T) http.Handler {
	t.Helper()
	return buildRouterWithRootAndClient(t, createFixtureWiki(t), mockLLM{})
}

func buildRouterWithRoot(t *testing.T, root string) http.Handler {
	t.Helper()
	return buildRouterWithRootAndClient(t, root, mockLLM{})
}

func buildRouterWithRootAndClient(t *testing.T, root string, client llm.Client) http.Handler {
	t.Helper()
	workspace := t.TempDir()
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
		Upload:    config.UploadConfig{MaxTextFileKB: 500, MaxTableRows: 120},
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
		LLM:          client,
		Retriever:    retrieval.NewQMDRetriever(rt),
		Store:        dataStore,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	}
	handlers := api.NewHandlers(
		service.NewPublicQueryService(deps),
		service.NewDirectAdminService(deps),
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
	mustWrite(t, filepath.Join(root, "wiki/templates/source-template.md"), "## Summary\n\n## Key Points\n\n## FAQ Entries\n\n## Concepts Extracted\n\n## Entities Extracted\n\n## Contradictions\n\n## My Notes\n")
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
