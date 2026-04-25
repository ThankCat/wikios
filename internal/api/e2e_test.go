package api_test

import (
	"archive/zip"
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
	case strings.Contains(fullPrompt, "模式提示：\ningest") && strings.Contains(fullPrompt, "source_format: faq"):
		return `{"action":"final","reply":"FAQ 数据兼容摄入已交由 LLM 按 AGENT 处理。","summary":"FAQ 数据兼容摄入已交由 LLM 按 AGENT 处理。","artifacts":["raw/articles/faq.json"],"output_files":[],"warnings":[]}`
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

func TestAdminPublicIntentsRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public-intents", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminContextEstimateRequiresAuthAndReturnsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)

	unauth := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/context/estimate", bytes.NewReader([]byte(`{"message":"hello","stream":true}`)))
	unauthReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(unauth, unauthReq)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d %s", unauth.Code, unauth.Body.String())
	}

	cookie := loginCookie(t, router)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/context/estimate", bytes.NewReader([]byte(`{"message":"执行一次健康检查","stream":true,"mode_hint":"lint"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("estimate failed: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"used_tokens"`) || !strings.Contains(rec.Body.String(), `"remaining_tokens"`) {
		t.Fatalf("expected context usage, got %s", rec.Body.String())
	}
}

func TestAdminChatLintUsesDirectAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)
	cookie := loginCookie(t, router)

	body, _ := json.Marshal(map[string]any{
		"message":   "执行一次健康检查",
		"stream":    false,
		"mode_hint": "lint",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("lint chat failed: %d %s", rec.Code, rec.Body.String())
	}
	response := rec.Body.String()
	if strings.Contains(response, "wiki_health") || strings.Contains(response, "qmd_status") {
		t.Fatalf("lint should not use server LintService details, got %s", response)
	}
	if !strings.Contains(response, "健康检查完成") {
		t.Fatalf("expected health summary, got %s", response)
	}
}

func TestAdminWikiFilePreview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)
	cookie := loginCookie(t, router)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/wiki/file?path=wiki/sources/customer-qa.md", nil)
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("wiki file failed: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"preview":"markdown"`) || !strings.Contains(body, "静态IP适合长期稳定网络环境") {
		t.Fatalf("unexpected wiki file response: %s", body)
	}
}

func TestAdminPublicIntentsSaveValidatesAndHotSwaps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := buildRouter(t)
	cookie := loginCookie(t, router)

	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public-intents", nil)
	getReq.AddCookie(cookie)
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), "source") {
		t.Fatalf("unexpected get response: %d %s", getRec.Code, getRec.Body.String())
	}

	badBody, _ := json.Marshal(map[string]any{"source": `version: 1
rules:
  - name: bad
    enabled: true
    priority: 10
    match:
      exact: [你好]
    response: 根据知识库回复
`})
	badRec := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/public-intents", bytes.NewReader(badBody))
	badReq.Header.Set("Content-Type", "application/json")
	badReq.AddCookie(cookie)
	router.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected validation failure, got %d %s", badRec.Code, badRec.Body.String())
	}

	source := `version: 1
fallbacks:
  generic: 您好，这个问题我这边暂时没有准确资料，您可以补充一下具体场景，我再为您确认。
rules:
  - name: greeting
    enabled: true
    priority: 50
    category: smalltalk
    match:
      exact: [你好]
    response: 您好，请问有什么可以帮您？
`
	goodBody, _ := json.Marshal(map[string]any{"source": source})
	goodRec := httptest.NewRecorder()
	goodReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/public-intents", bytes.NewReader(goodBody))
	goodReq.Header.Set("Content-Type", "application/json")
	goodReq.AddCookie(cookie)
	router.ServeHTTP(goodRec, goodReq)
	if goodRec.Code != http.StatusOK {
		t.Fatalf("expected save success, got %d %s", goodRec.Code, goodRec.Body.String())
	}

	answerBody, _ := json.Marshal(map[string]any{"question": "你好"})
	answerRec := httptest.NewRecorder()
	answerReq := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(answerBody))
	answerReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(answerRec, answerReq)
	if answerRec.Code != http.StatusOK || !strings.Contains(answerRec.Body.String(), "请问有什么可以帮您") {
		t.Fatalf("expected public answer to use hot-swapped intent, got %d %s", answerRec.Code, answerRec.Body.String())
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
	if !strings.Contains(rec.Body.String(), "FAQ 数据兼容摄入已交由 LLM") {
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
	if !strings.Contains(rec.Body.String(), "FAQ 数据兼容摄入已交由 LLM") {
		t.Fatalf("unexpected upload reply: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "classification_status") || strings.Contains(rec.Body.String(), "category_results") {
		t.Fatalf("structured upload should not expose server ingest classification details, got %s", rec.Body.String())
	}
}

func TestAdminUploadSupportsFAQXLSXViaDirectLLM(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := createFixtureWiki(t)
	router := buildRouterWithRoot(t, root)
	cookie := loginCookie(t, router)

	raw := buildAPITestFAQXLSX(t, [][]string{
		{"技能分类", "标准问题", "相似问法", "关键词", "回复内容", "标签", "快捷短语"},
		{"下载与安装", "怎么下载", "下载什么软件\n下载地址在哪", "下载\n安装", "您可以通过官网下载页面进行下载。", "下载与安装", "Windows安装"},
		{"产品咨询", "静态IP适合什么场景", "固定IP适合做什么", "静态ip\n固定ip", "静态IP适合长期稳定网络环境。", "产品咨询", "价格怎么咨询"},
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "知识库问答整理.xlsx")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write(raw)
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/upload/stream", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.AddCookie(cookie)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected success, got %d %s", rec.Code, rec.Body.String())
	}
	bodyText := rec.Body.String()
	if !strings.Contains(bodyText, `"source_format":"faq-xlsx"`) || !strings.Contains(bodyText, "event: ingest_plan") || !strings.Contains(bodyText, "FAQ 数据兼容摄入已交由 LLM") {
		t.Fatalf("expected xlsx normalized upload to enter direct LLM ingest, got %s", bodyText)
	}
	if strings.Contains(bodyText, "classification_status") || strings.Contains(bodyText, "category_results") {
		t.Fatalf("xlsx structured ingest must not use server classification details, got %s", bodyText)
	}
	for _, forbidden := range []string{"segments", "docs", "content", "curated", "kb"} {
		if _, err := os.Stat(filepath.Join(root, forbidden)); err == nil {
			t.Fatalf("unexpected wiki structure path created: %s", forbidden)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "raw/articles"))
	if err != nil {
		t.Fatalf("read raw articles: %v", err)
	}
	foundJSON := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			foundJSON = true
			break
		}
	}
	if !foundJSON {
		t.Fatalf("expected uploaded xlsx to be normalized into raw/articles json, got %#v", entries)
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
	if strings.Contains(rec.Body.String(), "category_results") || strings.Contains(rec.Body.String(), "classification_status") {
		t.Fatalf("structured FAQ upload should be delegated to direct LLM, got %s", rec.Body.String())
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
	for _, marker := range []string{"event: meta", "event: ingest_plan", "event: result", "event: done"} {
		if !strings.Contains(rec.Body.String(), marker) {
			t.Fatalf("expected %s in stream body, got %s", marker, rec.Body.String())
		}
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
	intentPath := filepath.Join(workspace, "public_intents.yaml")
	if err := os.WriteFile(intentPath, []byte(`version: 1
fallbacks:
  generic: 您好，这个问题我这边暂时没有准确资料，您可以补充一下具体场景，我再为您确认。
  operation: 您好，这方面我这边暂时没有可直接确认的操作说明，您可以补充一下具体场景，我再为您确认。
  device_operation: 您好，这项操作我这边暂时没有准确资料，建议您先参考设备说明或联系对应支持人员处理。
rules:
  - name: identity
    enabled: true
    priority: 80
    category: service_identity
    match:
      exact: [你是谁]
    response: 您好，我是四叶天代理IP客服，主要为您解答动态IP、静态IP、套餐选择和使用相关问题。
`), 0o644); err != nil {
		t.Fatalf("write public intents: %v", err)
	}
	enabled := true
	cfg := &config.Config{
		Server:      config.ServerConfig{Mode: "debug"},
		MountedWiki: config.MountedWikiConfig{Root: root, QMDIndex: "test-index"},
		Auth: config.AuthConfig{
			DefaultAdminUsername: "admin",
			DefaultAdminPassword: "admin123",
			SessionCookieName:    "wikios_admin_session",
			SessionTTLHours:      24,
		},
		Retrieval:     config.RetrievalConfig{TopK: 3},
		Workspace:     config.WorkspaceConfig{BaseDir: workspace, DefaultTimeoutSec: 5},
		Sandbox:       config.SandboxConfig{QMDTimeoutSec: 1, PythonTimeoutSec: 1},
		Sync:          config.SyncConfig{Remote: "origin", Branch: "main"},
		LLM:           config.LLMConfig{ModelAdmin: "test", ModelPublic: "test"},
		Storage:       config.StorageConfig{SQLitePath: filepath.Join(workspace, "service.db")},
		Upload:        config.UploadConfig{MaxTextFileKB: 500, MaxTableRows: 120},
		PublicIntents: config.PublicIntentsConfig{Enabled: &enabled, Path: intentPath},
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
	publicIntents := service.NewPublicIntentManager(cfg.PublicIntents)
	deps := service.Deps{
		Config:        cfg,
		Runtime:       rt,
		LLM:           client,
		Retriever:     retrieval.NewQMDRetriever(rt),
		Store:         dataStore,
		PublicIntents: publicIntents,
		PromptDir:     "../../internal/llm/prompts",
		WorkspaceDir:  cfg.Workspace.BaseDir,
	}
	handlers := api.NewHandlers(
		service.NewPublicQueryService(deps),
		service.NewDirectAdminService(deps),
		service.NewUploadService(deps),
		service.NewSyncService(deps),
		dataStore,
		cfg,
		cfg.Auth,
		publicIntents,
		service.NewContextCounter(cfg.Context),
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

func buildAPITestFAQXLSX(t *testing.T, rows [][]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zipper := zip.NewWriter(&buf)
	writeZipFile(t, zipper, "[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>
</Types>`)
	writeZipFile(t, zipper, "_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`)
	writeZipFile(t, zipper, "xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets><sheet name="FAQ" sheetId="1" r:id="rId1"/></sheets>
</workbook>`)
	writeZipFile(t, zipper, "xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>
</Relationships>`)
	shared := []string{}
	index := map[string]int{}
	ref := func(value string) int {
		if existing, ok := index[value]; ok {
			return existing
		}
		index[value] = len(shared)
		shared = append(shared, value)
		return len(shared) - 1
	}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for r, row := range rows {
		sheet.WriteString(fmt.Sprintf(`<row r="%d">`, r+1))
		for c, cell := range row {
			sheet.WriteString(fmt.Sprintf(`<c r="%s%d" t="s"><v>%d</v></c>`, excelColumnName(c+1), r+1, ref(cell)))
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	writeZipFile(t, zipper, "xl/worksheets/sheet1.xml", sheet.String())
	var sharedXML strings.Builder
	sharedXML.WriteString(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="%d" uniqueCount="%d">`, len(shared), len(shared)))
	for _, value := range shared {
		sharedXML.WriteString(`<si><t>`)
		sharedXML.WriteString(escapeXMLText(value))
		sharedXML.WriteString(`</t></si>`)
	}
	sharedXML.WriteString(`</sst>`)
	writeZipFile(t, zipper, "xl/sharedStrings.xml", sharedXML.String())
	if err := zipper.Close(); err != nil {
		t.Fatalf("close xlsx zip: %v", err)
	}
	return buf.Bytes()
}

func writeZipFile(t *testing.T, zipper *zip.Writer, name string, content string) {
	t.Helper()
	writer, err := zipper.Create(name)
	if err != nil {
		t.Fatalf("create zip entry %s: %v", name, err)
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatalf("write zip entry %s: %v", name, err)
	}
}

func excelColumnName(index int) string {
	name := ""
	for index > 0 {
		index--
		name = string(rune('A'+index%26)) + name
		index /= 26
	}
	return name
}

func escapeXMLText(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
}
