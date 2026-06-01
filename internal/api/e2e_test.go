package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

type apiTestFixture struct {
	router http.Handler
	root   string
	deps   service.Deps
}

type apiMockLLM struct{}

func (apiMockLLM) Chat(_ context.Context, _ string, messages []llm.Message) (string, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "管理员全权限直连模式") {
		lastUser := ""
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				lastUser = messages[i].Content
				break
			}
		}
		if strings.Contains(lastUser, "shell_result:") {
			return `{"action":"final","reply":"已按 AGENT 处理。","summary":"已按 AGENT 处理。","artifacts":["wiki/knowledge/static-ip.md"],"output_files":["wiki/knowledge/static-ip.md"],"warnings":[]}`, nil
		}
		if strings.Contains(lastUser, "人工审查通过") {
			targetPath := apiTestLineValue(lastUser, "target_path: ")
			sourcePath := apiTestLineValue(lastUser, "source_archive_path: ")
			answer := apiTestSectionValue(lastUser, "confirmed_answer:")
			if targetPath == "" {
				targetPath = "wiki/knowledge/static-ip.md"
			}
			command := "mkdir -p " + apiShellQuote(filepath.ToSlash(filepath.Dir(targetPath))) + " && cat >> " + apiShellQuote(targetPath) + " <<'EOF'\n\n## Human Reviewed Knowledge\n\nsource_pages:\n- " + sourcePath + "\n\n" + answer + "\nEOF\n"
			raw, err := json.Marshal(map[string]any{"action": "shell", "command": command, "reason": "沉淀人工审查知识"})
			if err != nil {
				return "", err
			}
			return string(raw), nil
		}
		return `{"action":"final","reply":"文档已按 AGENT 处理。","summary":"文档已按 AGENT 处理。","artifacts":["wiki/sources/uploaded-document.md"],"output_files":[],"warnings":[]}`, nil
	}
	if apiIsRouterPrompt(messages) {
		return apiRouterJSONForMessages(messages), nil
	}
	return `{
  "answer_mode": "evidence",
  "answer": "静态 IP 适合账号运营、白名单绑定和远程办公。",
  "review_question": "",
  "confidence": 0.9,
  "evidence_confidence": 0.9,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "notes": ""
}`, nil
}

func apiIsRouterPrompt(messages []llm.Message) bool {
	return len(messages) > 0 && strings.Contains(messages[0].Content, "客服经理 Router")
}

func apiRouterJSONForMessages(messages []llm.Message) string {
	userMessage := ""
	if len(messages) > 1 {
		content := messages[len(messages)-1].Content
		if idx := strings.Index(content, "user_message:"); idx >= 0 {
			rest := strings.TrimSpace(content[idx+len("user_message:"):])
			if next := strings.Index(rest, "\n\n"); next >= 0 {
				rest = rest[:next]
			}
			userMessage = strings.TrimSpace(rest)
		}
	}
	specialist := "product"
	intent := "product_inquiry"
	primaryProduct := "static_ip"
	riskFlags := []string{}
	missingInfo := []string{}
	queries := []string{"四叶天 静态 IP 适用场景"}
	handoff := "用户询问产品适用场景。"
	lower := strings.ToLower(userMessage)
	switch {
	case strings.Contains(userMessage, "白名单") || strings.Contains(strings.ToUpper(userMessage), "API"):
		specialist = "technical"
		intent = "technical_setup"
		queries = []string{"四叶天 API 白名单 配置"}
		riskFlags = []string{"technical"}
		handoff = "用户询问技术配置。"
	case strings.Contains(userMessage, "优惠") || strings.Contains(userMessage, "折扣") || strings.Contains(userMessage, "多少钱") || strings.Contains(userMessage, "价格") || strings.Contains(userMessage, "怎么卖"):
		specialist = "pricing"
		intent = "price_inquiry"
		queries = []string{"四叶天 静态 IP 价格 优惠"}
		riskFlags = []string{"pricing"}
		handoff = "用户询问价格或优惠。"
	case strings.Contains(userMessage, "购买") || strings.Contains(userMessage, "怎么买"):
		specialist = "purchase"
		intent = "purchase_inquiry"
		queries = []string{"四叶天 购买 开通 流程"}
		handoff = "用户询问购买开通。"
	case strings.Contains(lower, "google") || strings.Contains(lower, "chatgpt") || strings.Contains(userMessage, "风控") || strings.Contains(userMessage, "封号"):
		specialist = "safety"
		intent = "safety_boundary"
		primaryProduct = "overseas_ip"
		queries = []string{"四叶天 海外 IP 访问边界"}
		riskFlags = []string{"platform_risk", "overseas_access"}
		handoff = "用户询问平台访问或风控边界。"
	}
	if strings.Contains(userMessage, "数据中心") {
		primaryProduct = "datacenter_ip"
	}
	if strings.TrimSpace(userMessage) == "" {
		primaryProduct = "unknown"
		missingInfo = []string{"primary_product"}
	}
	products := []string{}
	if primaryProduct != "unknown" {
		products = []string{primaryProduct}
	}
	raw, err := json.Marshal(map[string]any{
		"contract_version":   "customer_router.v1",
		"specialist":         specialist,
		"routing_confidence": 0.9,
		"routing_reason":     "测试路由原因。",
		"intent":             intent,
		"rewritten_question": firstNonEmptyForAPITest(userMessage, "客户咨询四叶天产品。"),
		"history_summary":    "",
		"slots": map[string]any{
			"primary_product": primaryProduct,
			"products":        products,
			"static_type":     "",
			"ip_type":         "",
			"bandwidth":       "",
			"quantity":        "",
			"scenario":        "",
			"platform":        "",
			"device":          "",
			"error_code":      "",
		},
		"ambiguity": map[string]any{
			"is_ambiguous":     false,
			"ambiguous_fields": []string{},
			"reason":           "",
		},
		"missing_info":      missingInfo,
		"risk_flags":        riskFlags,
		"needs_retrieval":   true,
		"retrieval_queries": queries,
		"handoff_notes":     handoff,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func firstNonEmptyForAPITest(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func apiTestLineValue(text string, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func apiTestSectionValue(text string, heading string) string {
	idx := strings.Index(text, heading)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(text[idx+len(heading):])
	if next := strings.Index(rest, "\n\n"); next >= 0 {
		rest = rest[:next]
	}
	return strings.TrimSpace(rest)
}

func apiShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (m apiMockLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := m.Chat(ctx, model, messages)
	if err != nil {
		return "", err
	}
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
}

type apiStreamingMockLLM struct {
	apiMockLLM
}

func (m apiStreamingMockLLM) Chat(ctx context.Context, model string, messages []llm.Message) (string, error) {
	return m.streamText(ctx, model, messages, nil)
}

func (m apiStreamingMockLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	return m.streamText(ctx, model, messages, func(delta llm.StreamDelta) {
		if onDelta != nil && delta.Content != "" {
			onDelta(delta.Content)
		}
	})
}

func (m apiStreamingMockLLM) StreamChatEvents(ctx context.Context, model string, messages []llm.Message, onDelta func(llm.StreamDelta)) (string, error) {
	return m.streamText(ctx, model, messages, onDelta)
}

func (m apiStreamingMockLLM) streamText(ctx context.Context, model string, messages []llm.Message, onDelta func(llm.StreamDelta)) (string, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "管理员全权限直连模式") {
		return m.apiMockLLM.Chat(ctx, model, messages)
	}
	if apiIsRouterPrompt(messages) {
		text := apiRouterJSONForMessages(messages)
		if onDelta != nil {
			onDelta(llm.StreamDelta{Content: text})
		}
		return text, nil
	}
	chunks := []string{
		`{"answer_mode":"evidence",`,
		`"answer":"静态 IP 适合稳定账号和`,
		`白名单绑定。",`,
		`"review_question":"",`,
		`"confidence":0.9,`,
		`"evidence_confidence":0.9,`,
		`"review_required":false,`,
		`"review_reason":"",`,
		`"suggested_target_path":"",`,
		`"sources":[{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],`,
		`"notes":""}`,
	}
	if onDelta != nil {
		onDelta(llm.StreamDelta{ReasoningContent: "先确认是否有正式知识证据。"})
		for _, chunk := range chunks {
			onDelta(llm.StreamDelta{Content: chunk})
		}
	}
	return strings.Join(chunks, ""), nil
}

type apiCustomerChatTextLLM struct {
	text string
}

func (m apiCustomerChatTextLLM) Chat(ctx context.Context, model string, messages []llm.Message) (string, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "管理员全权限直连模式") {
		return apiMockLLM{}.Chat(ctx, model, messages)
	}
	if apiIsRouterPrompt(messages) {
		return apiRouterJSONForMessages(messages), nil
	}
	return m.text, nil
}

func (m apiCustomerChatTextLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := m.Chat(ctx, model, messages)
	if err != nil {
		return "", err
	}
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
}

type apiCanceledLLM struct{}

func (apiCanceledLLM) Chat(context.Context, string, []llm.Message) (string, error) {
	return "", context.Canceled
}

func (apiCanceledLLM) StreamChat(context.Context, string, []llm.Message, func(string)) (string, error) {
	return "", context.Canceled
}

type apiBlockingLLM struct{}

func (apiBlockingLLM) Chat(ctx context.Context, _ string, _ []llm.Message) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (apiBlockingLLM) StreamChat(ctx context.Context, _ string, _ []llm.Message, _ func(string)) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

type apiNoActiveLLM struct{}

func (apiNoActiveLLM) Chat(context.Context, string, []llm.Message) (string, error) {
	return "", errors.New("当前未启用 LLM 模型，请先在管理员端模型模块配置并启用模型")
}

func (apiNoActiveLLM) StreamChat(context.Context, string, []llm.Message, func(string)) (string, error) {
	return "", errors.New("当前未启用 LLM 模型，请先在管理员端模型模块配置并启用模型")
}

type apiProviderUnavailableLLM struct{}

func (apiProviderUnavailableLLM) Chat(context.Context, string, []llm.Message) (string, error) {
	return "", errors.New("llm api error: Access denied, please make sure your account is in good standing. overdue-payment")
}

func (apiProviderUnavailableLLM) StreamChat(context.Context, string, []llm.Message, func(string)) (string, error) {
	return "", errors.New("llm api error: Access denied, please make sure your account is in good standing. overdue-payment")
}

type apiQMDTool struct{}

func (apiQMDTool) Name() string {
	return "exec.qmd"
}

func (apiQMDTool) RiskLevel() runtime.RiskLevel {
	return runtime.RiskMedium
}

func (apiQMDTool) Validate(map[string]any) error {
	return nil
}

func (apiQMDTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	subcommand, _ := args["subcommand"].(string)
	return runtime.ToolResult{
		Success:   true,
		RiskLevel: runtime.RiskMedium,
		Data:      map[string]any{"subcommand": subcommand, "stdout": "[]", "stderr": "", "exit_code": 0},
	}, nil
}

func TestAdminUploadAcceptsDocumentAndRejectsStructuredFiles(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})

	rec := uploadFile(t, fixture.router, "/api/v1/admin/upload", "product-knowledge.md", []byte("# 产品知识\n\n静态 IP 适合稳定场景。"))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload markdown failed: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"stored_path":"raw/articles/`) || !strings.Contains(body, `"source_format":"document"`) {
		t.Fatalf("expected document upload details, got %s", body)
	}
	for _, forbidden := range []string{"segments_total", "segment_results", "category_results"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("upload response must not contain %q: %s", forbidden, body)
		}
	}
	if matches, err := filepath.Glob(filepath.Join(fixture.root, "raw", "articles", "*.md")); err != nil || len(matches) == 0 {
		t.Fatalf("expected uploaded markdown in raw/articles, matches=%#v err=%v", matches, err)
	}

	for _, file := range []struct {
		name    string
		content []byte
	}{
		{name: "structured.json", content: []byte(`{"hello":"world"}`)},
		{name: "table.xlsx", content: []byte("xlsx")},
		{name: "rows.csv", content: []byte("a,b\n1,2\n")},
	} {
		rec := uploadFile(t, fixture.router, "/api/v1/admin/upload", file.name, file.content)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected %s to be rejected, got %d %s", file.name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "只支持文档文件") || !strings.Contains(rec.Body.String(), "不支持 Excel、CSV、TSV、JSON、图片") {
			t.Fatalf("unexpected reject body for %s: %s", file.name, rec.Body.String())
		}
	}
}

func TestAdminUploadStreamEmitsDocumentOnlyEvents(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	rec := uploadFile(t, fixture.router, "/api/v1/admin/upload/stream", "guide.txt", []byte("静态 IP 使用说明"))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream upload failed: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: meta") || !strings.Contains(body, `"source_format":"document"`) || !strings.Contains(body, "文档已按 AGENT 处理") {
		t.Fatalf("expected document stream events, got %s", body)
	}
	for _, forbidden := range []string{"event: ingest_plan", "event: segment_start", "event: segment_result", "category_results"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("stream response must not contain %q: %s", forbidden, body)
		}
	}
}

func TestAdminWikiFileEditSaveConflictAndReplace(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/wiki/file?path=wiki/knowledge/static-ip.md", nil)
	getRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("wiki file get failed: %d %s", getRec.Code, getRec.Body.String())
	}
	var fileResp struct {
		Path     string `json:"path"`
		Preview  string `json:"preview"`
		Editable bool   `json:"editable"`
		TextKind string `json:"text_kind"`
		Encoding string `json:"encoding"`
		SHA256   string `json:"sha256"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &fileResp); err != nil {
		t.Fatalf("decode file response: %v", err)
	}
	if fileResp.Path != "wiki/knowledge/static-ip.md" || fileResp.Preview != "markdown" || !fileResp.Editable || fileResp.TextKind != "markdown" || fileResp.Encoding != "utf-8" || fileResp.SHA256 == "" {
		t.Fatalf("unexpected file metadata: %s", getRec.Body.String())
	}
	if !strings.Contains(fileResp.Content, "# 静态 IP") {
		t.Fatalf("expected markdown content, got %s", fileResp.Content)
	}

	saveBody, _ := json.Marshal(map[string]any{
		"path":            fileResp.Path,
		"content":         fileResp.Content + "\n## 编辑保存\n\n已更新。\n",
		"expected_sha256": fileResp.SHA256,
	})
	saveReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/wiki/file", bytes.NewReader(saveBody))
	saveReq.Header.Set("Content-Type", "application/json")
	saveRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusOK {
		t.Fatalf("wiki save failed: %d %s", saveRec.Code, saveRec.Body.String())
	}
	if !strings.Contains(saveRec.Body.String(), "编辑保存") {
		t.Fatalf("saved response should include updated content: %s", saveRec.Body.String())
	}
	staleReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/wiki/file", bytes.NewReader(saveBody))
	staleReq.Header.Set("Content-Type", "application/json")
	staleRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(staleRec, staleReq)
	if staleRec.Code != http.StatusConflict || !strings.Contains(staleRec.Body.String(), "FILE_CONFLICT") {
		t.Fatalf("expected conflict for stale sha, got %d %s", staleRec.Code, staleRec.Body.String())
	}

	mustWrite(t, filepath.Join(fixture.root, "wiki", "knowledge", "image.png"), "\x89PNG\r\n")
	binaryReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/wiki/file?path=wiki/knowledge/image.png", nil)
	binaryRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(binaryRec, binaryReq)
	if binaryRec.Code != http.StatusOK {
		t.Fatalf("binary get failed: %d %s", binaryRec.Code, binaryRec.Body.String())
	}
	if !strings.Contains(binaryRec.Body.String(), `"editable":false`) || strings.Contains(binaryRec.Body.String(), `"content"`) {
		t.Fatalf("binary file must be non-editable without content: %s", binaryRec.Body.String())
	}
	replaceRec := replaceWikiFile(t, fixture.router, "wiki/knowledge/image.png", "image.png", []byte("new-binary"))
	if replaceRec.Code != http.StatusOK {
		t.Fatalf("replace file failed: %d %s", replaceRec.Code, replaceRec.Body.String())
	}
	replaced, err := os.ReadFile(filepath.Join(fixture.root, "wiki", "knowledge", "image.png"))
	if err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if string(replaced) != "new-binary" {
		t.Fatalf("expected replacement content, got %q", string(replaced))
	}
}

func TestAdminWikiFileRejectsInvalidPathsAndOversizedText(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	invalidReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/wiki/file?path=../secret.md", nil)
	invalidRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid path rejection, got %d %s", invalidRec.Code, invalidRec.Body.String())
	}

	mustWrite(t, filepath.Join(fixture.root, "wiki", "knowledge", "big.md"), strings.Repeat("x", 501*1024))
	bigReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/wiki/file?path=wiki/knowledge/big.md", nil)
	bigRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(bigRec, bigReq)
	if bigRec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(bigRec.Body.String(), "FILE_TOO_LARGE") {
		t.Fatalf("expected oversized text rejection, got %d %s", bigRec.Code, bigRec.Body.String())
	}

	saveBody, _ := json.Marshal(map[string]any{
		"path":            "wiki/knowledge/image.png",
		"content":         "text",
		"expected_sha256": "",
	})
	saveReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/wiki/file", bytes.NewReader(saveBody))
	saveReq.Header.Set("Content-Type", "application/json")
	saveRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusNotFound {
		t.Fatalf("missing file should return not found before edit-type check, got %d %s", saveRec.Code, saveRec.Body.String())
	}
	mustWrite(t, filepath.Join(fixture.root, "wiki", "knowledge", "image.png"), "png")
	saveReq = httptest.NewRequest(http.MethodPut, "/api/v1/admin/wiki/file", bytes.NewReader(saveBody))
	saveReq.Header.Set("Content-Type", "application/json")
	saveRec = httptest.NewRecorder()
	fixture.router.ServeHTTP(saveRec, saveReq)
	if saveRec.Code != http.StatusUnsupportedMediaType || !strings.Contains(saveRec.Body.String(), "UNSUPPORTED_EDIT_TYPE") {
		t.Fatalf("expected unsupported edit type, got %d %s", saveRec.Code, saveRec.Body.String())
	}
}

func TestAdminSyncPushReturnsGitErrorDetails(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	runGit(t, fixture.root, "init", "-b", "main")
	runGit(t, fixture.root, "config", "user.email", "test@example.com")
	runGit(t, fixture.root, "config", "user.name", "WikiOS Test")
	runGit(t, fixture.root, "add", "AGENT.md")
	runGit(t, fixture.root, "commit", "-m", "init")

	body, _ := json.Marshal(map[string]any{"remote": "missing", "branch": "main"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/sync/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected git push failure, got %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Error struct {
			Code     string `json:"code"`
			Message  string `json:"message"`
			Stderr   string `json:"stderr"`
			ExitCode int    `json:"exit_code"`
		} `json:"error"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode push error: %v", err)
	}
	if payload.Error.Code != "GIT_PUSH_FAILED" || payload.Error.ExitCode == 0 || payload.ExitCode == 0 {
		t.Fatalf("expected structured git error with exit code, got %+v body=%s", payload, rec.Body.String())
	}
	if payload.Error.Stderr == "" || payload.Stderr == "" || !strings.Contains(payload.Error.Message, "missing") {
		t.Fatalf("expected git stderr to be visible, got %+v body=%s", payload, rec.Body.String())
	}
}

func TestCustomerChatSupportsPlainAndStreamAndOmitsDetails(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	plainBody, _ := json.Marshal(map[string]any{
		"message": "静态 IP 适合什么？",
	})
	plainReq := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(plainBody))
	plainReq.Header.Set("Content-Type", "application/json")
	plainRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(plainRec, plainReq)
	if plainRec.Code != http.StatusOK {
		t.Fatalf("plain customer chat failed: %d %s", plainRec.Code, plainRec.Body.String())
	}
	if plainRec.Header().Get("X-Trace-ID") == "" {
		t.Fatalf("expected X-Trace-ID response header")
	}
	if strings.Contains(plainRec.Body.String(), "event:") {
		t.Fatalf("customer chat must default to non-stream JSON, got %s", plainRec.Body.String())
	}
	if !strings.Contains(plainRec.Body.String(), `"answer":"静态 IP 适合稳定账号和白名单绑定。"`) {
		t.Fatalf("unexpected plain customer chat: %s", plainRec.Body.String())
	}
	if strings.Contains(plainRec.Body.String(), `"details"`) || strings.Contains(plainRec.Body.String(), "process_summary") || strings.Contains(plainRec.Body.String(), "steps") {
		t.Fatalf("external customer chat must not expose admin details, got %s", plainRec.Body.String())
	}

	streamBody, _ := json.Marshal(map[string]any{
		"message": "静态 IP 适合什么？",
		"stream":  true,
	})
	streamReq := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(streamBody))
	streamReq.Header.Set("Content-Type", "application/json")
	streamRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(streamRec, streamReq)
	if streamRec.Code != http.StatusOK {
		t.Fatalf("stream customer chat failed: %d %s", streamRec.Code, streamRec.Body.String())
	}
	stream := streamRec.Body.String()
	for _, want := range []string{"event: delta", "event: result", "event: done"} {
		if !strings.Contains(stream, want) {
			t.Fatalf("expected customer stream to contain %q, got %s", want, stream)
		}
	}
	for _, forbidden := range []string{"event: prompt", "event: llm_delta", "event: llm_reasoning_delta", "event: step_start", "event: step_finish", `"details"`, "process_summary"} {
		if strings.Contains(stream, forbidden) {
			t.Fatalf("customer stream must not expose %q, got %s", forbidden, stream)
		}
	}

	for _, path := range []string{
		"/api/v1/" + "pub" + "lic/answer",
		"/api/v1/" + "pub" + "lic/answer/stream",
		"/api/v1/admin/" + "pub" + "lic-answer/audit",
		"/api/v1/admin/" + "pub" + "lic-answer/audit/stream",
		"/api/v1/admin/chat",
		"/api/v1/admin/chat/stream",
	} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(plainBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		fixture.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected removed route %s to return 404, got %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestInternalCustomerChatWritesTraceDetailsWithoutResponseDetails(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"message":    "静态 IP 适合什么？",
		"session_id": "test-customer-chat",
		"entrypoint": "internal",
		"simulation": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("internal customer chat failed: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode internal customer chat: %v", err)
	}
	if payload.Answer != "静态 IP 适合稳定账号和白名单绑定。" {
		t.Fatalf("unexpected internal answer: %s", payload.Answer)
	}
	if strings.Contains(rec.Body.String(), `"details"`) || strings.Contains(rec.Body.String(), "process_summary") {
		t.Fatalf("customer chat response must not expose audit details, got %s", rec.Body.String())
	}
	trace := readCustomerChatTraceByHeader(t, fixture.router, rec)
	if apiTestStringValue(apiTestMapValue(trace["runtime"]), "entrypoint") != "internal" {
		t.Fatalf("expected internal entrypoint in trace, got %#v", trace["runtime"])
	}
	if apiTestStringValue(apiTestMapValue(trace["request"]), "message") != "静态 IP 适合什么？" {
		t.Fatalf("expected request.message in trace, got %#v", trace["request"])
	}
	for _, key := range []string{"router", "retrieval", "specialist", "final"} {
		if _, ok := trace[key]; !ok {
			t.Fatalf("expected trace.%s, got %#v", key, trace)
		}
	}
	if apiTestStringValue(apiTestMapValue(trace["final"]), "answer") != "静态 IP 适合稳定账号和白名单绑定。" {
		t.Fatalf("expected final answer in trace, got %#v", trace["final"])
	}

	streamBody, _ := json.Marshal(map[string]any{
		"message":    "静态 IP 适合什么？",
		"session_id": "test-customer-chat-stream",
		"entrypoint": "internal",
		"simulation": true,
		"stream":     true,
	})
	streamReq := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(streamBody))
	streamReq.Header.Set("Content-Type", "application/json")
	streamRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(streamRec, streamReq)
	if streamRec.Code != http.StatusOK {
		t.Fatalf("internal customer chat stream failed: %d %s", streamRec.Code, streamRec.Body.String())
	}
	stream := streamRec.Body.String()
	if !strings.Contains(stream, "event: result") || !strings.Contains(stream, "event: delta") || strings.Contains(stream, `"details"`) || strings.Contains(stream, "process_summary") {
		t.Fatalf("expected internal customer stream to expose only customer-visible events, got %s", stream)
	}
	for _, forbidden := range []string{"event: prompt", "event: llm_delta", "event: llm_reasoning_delta", "event: step_start", "event: step_finish"} {
		if strings.Contains(stream, forbidden) {
			t.Fatalf("customer stream must not expose %q, got %s", forbidden, stream)
		}
	}
	if deltaAt, resultAt := strings.Index(stream, "event: delta"), strings.Index(stream, "event: result"); deltaAt < 0 || resultAt < 0 || deltaAt > resultAt {
		t.Fatalf("expected streamed answer delta before result, got %s", stream)
	}
	_ = readCustomerChatTraceByHeader(t, fixture.router, streamRec)
}

func TestInternalCustomerChatMatchesExternalCustomerChat(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"message": "静态 IP 适合什么？",
		"history": []map[string]any{
			{"role": "user", "content": "我想了解静态 IP"},
			{"role": "assistant", "content": "静态 IP 适合固定出口。"},
		},
	})

	externalReq := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	externalReq.Header.Set("Content-Type", "application/json")
	externalRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(externalRec, externalReq)
	if externalRec.Code != http.StatusOK {
		t.Fatalf("customer chat failed: %d %s", externalRec.Code, externalRec.Body.String())
	}

	internalBody, _ := json.Marshal(map[string]any{
		"message":    "静态 IP 适合什么？",
		"entrypoint": "internal",
		"simulation": true,
		"history": []map[string]any{
			{"role": "user", "content": "我想了解静态 IP"},
			{"role": "assistant", "content": "静态 IP 适合固定出口。"},
		},
	})
	auditReq := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(internalBody))
	auditReq.Header.Set("Content-Type", "application/json")
	auditRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("admin customer chat audit failed: %d %s", auditRec.Code, auditRec.Body.String())
	}

	var externalPayload, auditPayload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(externalRec.Body.Bytes(), &externalPayload); err != nil {
		t.Fatalf("decode customer chat: %v", err)
	}
	if err := json.Unmarshal(auditRec.Body.Bytes(), &auditPayload); err != nil {
		t.Fatalf("decode admin audit answer: %v", err)
	}
	if externalPayload.Answer != auditPayload.Answer {
		t.Fatalf("expected internal answer to match external answer, external=%q internal=%q", externalPayload.Answer, auditPayload.Answer)
	}
	if strings.Contains(externalRec.Body.String(), `"details"`) || strings.Contains(auditRec.Body.String(), `"details"`) {
		t.Fatalf("customer chat responses must not include details, external=%s internal=%s", externalRec.Body.String(), auditRec.Body.String())
	}
	externalTrace := readCustomerChatTraceByHeader(t, fixture.router, externalRec)
	internalTrace := readCustomerChatTraceByHeader(t, fixture.router, auditRec)
	if apiTestStringValue(apiTestMapValue(externalTrace["runtime"]), "entrypoint") != "external" {
		t.Fatalf("expected external trace entrypoint, got %#v", externalTrace["runtime"])
	}
	if apiTestStringValue(apiTestMapValue(internalTrace["runtime"]), "entrypoint") != "internal" {
		t.Fatalf("expected internal trace entrypoint, got %#v", internalTrace["runtime"])
	}
}

func TestCustomerChatNoActiveModelFailsWithoutFallbackOrConfigurationLeak(t *testing.T) {
	fixture := newAPITestFixture(t, apiNoActiveLLM{})
	body, _ := json.Marshal(map[string]any{
		"message": "静态 IP 适合什么？",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected customer model failure status, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"answer"`) || apiContainsModelInternalLeak(rec.Body.String()) {
		t.Fatalf("expected customer failure without fallback answer or internal config details, got %s", rec.Body.String())
	}
	entry := readLatestCustomerChatLogEntry(t, fixture.deps.WorkspaceDir)
	errInfo := apiTestMapValue(entry["error"])
	if apiTestStringValue(errInfo, "stage") != "router_call" || apiTestStringValue(errInfo, "message") == "" {
		t.Fatalf("expected failed customer chat JSONL with router_call error, got %#v", entry)
	}
	if apiTestStringValue(apiTestMapValue(entry["final"]), "answer") != "" {
		t.Fatalf("failed customer chat JSONL must not invent final answer, got %#v", entry["final"])
	}
}

func TestCustomerChatSpecialistParseFailureWritesAuditError(t *testing.T) {
	fixture := newAPITestFixture(t, apiCustomerChatTextLLM{text: `{
  "answer_mode": "clarification",
  "answer": "",
  "review_question": "",
  "confidence": 0.6,
  "evidence_confidence": 0.6,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [],
  "notes": ""
}`})
	body, _ := json.Marshal(map[string]any{
		"message": "API 白名单怎么设置？",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected specialist parse failure status, got %d %s", rec.Code, rec.Body.String())
	}
	entry := readLatestCustomerChatLogEntry(t, fixture.deps.WorkspaceDir)
	errInfo := apiTestMapValue(entry["error"])
	if apiTestStringValue(errInfo, "stage") != "specialist_parse" || !strings.Contains(apiTestStringValue(errInfo, "message"), "empty answer") {
		t.Fatalf("expected specialist_parse error in JSONL, got %#v", entry)
	}
	if !strings.Contains(apiTestStringValue(errInfo, "raw_output"), `"answer": ""`) {
		t.Fatalf("expected specialist raw output in parse error JSONL, got %#v", errInfo)
	}
}

func TestCustomerChatProviderUnavailableFailsWithoutFallbackOrAccountLeak(t *testing.T) {
	fixture := newAPITestFixture(t, apiProviderUnavailableLLM{})
	body, _ := json.Marshal(map[string]any{
		"message": "你现在不能回复了吗?",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected customer provider failure status, got %d %s", rec.Code, rec.Body.String())
	}
	responseBody := rec.Body.String()
	if strings.Contains(responseBody, `"answer"`) || apiContainsModelInternalLeak(responseBody) {
		t.Fatalf("expected provider error hidden from customer failure response, got %s", responseBody)
	}
}

func apiContainsModelInternalLeak(text string) bool {
	for _, phrase := range []string{
		"Access denied",
		"overdue",
		"管理员端",
		"账号余额",
		"API Key",
		"供应商",
		"base_url",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func TestCustomerContextEstimateIsAvailableWithoutAdminAuth(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"message": "这个怎么买？",
		"history": []map[string]any{
			{"role": "user", "content": "我想了解静态IP"},
			{"role": "assistant", "content": "静态IP适合固定出口场景。"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/context/estimate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("customer context estimate failed: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Mode         string               `json:"mode"`
		ContextUsage service.ContextUsage `json:"context_usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode context estimate: %v", err)
	}
	if payload.Mode != "customer" || payload.ContextUsage.UsedTokens <= 0 || payload.ContextUsage.MaxTokens <= 0 {
		t.Fatalf("unexpected customer context estimate: %+v", payload)
	}
}

func TestCustomerChatWritesQuestionAnswerJSONAndThinkingLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	activeModel := &store.LLMModel{
		ID:              "audit-active-model",
		DisplayName:     "Audit Active Model",
		Provider:        "test",
		BaseURL:         "https://llm.example.test",
		ModelName:       "audit-model-name",
		APIKey:          "test-key",
		IsActive:        true,
		TimeoutSec:      5,
		AdminTimeoutSec: 5,
	}
	if err := fixture.deps.Store.CreateLLMModel(context.Background(), activeModel); err != nil {
		t.Fatalf("create active model: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"message":    "静态 IP 适合什么？",
		"session_id": "s-log",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("customer chat failed: %d %s", rec.Code, rec.Body.String())
	}

	entry := readLatestCustomerChatLogEntry(t, fixture.deps.WorkspaceDir)
	for _, key := range []string{"question", "answer", "answer_mode", "process_summary", "message", "logged_at", "received_at", "answered_at", "message_id", "answer_message_id", "message_created_at", "user_id"} {
		if _, exists := entry[key]; exists {
			t.Fatalf("customer chat JSONL must not contain compatibility top-level field %q: %#v", key, entry)
		}
	}
	if entry["schema_version"] != "customer_chat_audit.v1" || entry["record_type"] != "customer_chat_trace" {
		t.Fatalf("expected customer chat audit schema fields, got %#v", entry)
	}
	if apiTestStringValue(apiTestMapValue(entry["request"]), "message") != "静态 IP 适合什么？" {
		t.Fatalf("expected request.message, got %#v", entry["request"])
	}
	runtimeInfo := apiTestMapValue(entry["runtime"])
	if apiTestStringValue(runtimeInfo, "customer_chat_mode") != "routed" ||
		apiTestStringValue(runtimeInfo, "router_model_id") != activeModel.ID ||
		apiTestStringValue(runtimeInfo, "specialist_model_id") != activeModel.ID ||
		apiTestStringValue(runtimeInfo, "router_contract_version") != "customer_router.v1" {
		t.Fatalf("expected runtime model and contract snapshot, got %#v", runtimeInfo)
	}
	if apiTestStringValue(runtimeInfo, "router_model_id") == "active" ||
		apiTestStringValue(runtimeInfo, "specialist_model_id") == "active" {
		t.Fatalf("runtime model ids must snapshot concrete model ids, got %#v", runtimeInfo)
	}
	timeInfo := apiTestMapValue(entry["time"])
	for _, key := range []string{"logged_at", "received_at", "answered_at", "total_duration_ms"} {
		if _, ok := timeInfo[key]; !ok {
			t.Fatalf("expected time.%s in JSONL, got %#v", key, timeInfo)
		}
	}
	if apiTestStringValue(apiTestMapValue(entry["final"]), "answer") != "静态 IP 适合稳定账号和白名单绑定。" {
		t.Fatalf("expected final.answer, got %#v", entry["final"])
	}
	retrieval := apiTestMapValue(entry["retrieval"])
	if apiTestInt64Value(retrieval, "source_count") != int64(len(apiTestSliceValue(retrieval["sources"]))) {
		t.Fatalf("expected retrieval.source_count to count retrieval sources, got %#v", retrieval)
	}
	router := apiTestMapValue(entry["router"])
	if _, ok := router["duration_ms"]; !ok {
		t.Fatalf("expected router.duration_ms, got %#v", router)
	}
	routerModel := apiTestMapValue(router["model"])
	if routerModel["thinking_enabled"] != false {
		t.Fatalf("expected router thinking disabled in model snapshot, got %#v", routerModel)
	}
	if apiTestStringValue(routerModel, "id") != activeModel.ID || apiTestStringValue(routerModel, "name") != activeModel.ModelName {
		t.Fatalf("expected concrete router model snapshot, got %#v", routerModel)
	}
	specialist := apiTestMapValue(entry["specialist"])
	if _, ok := specialist["duration_ms"]; !ok {
		t.Fatalf("expected specialist.duration_ms, got %#v", specialist)
	}
	specialistModel := apiTestMapValue(specialist["model"])
	if specialistModel["thinking_enabled"] != true {
		t.Fatalf("expected specialist thinking enabled in model snapshot, got %#v", specialistModel)
	}
	if apiTestStringValue(specialistModel, "id") != activeModel.ID || apiTestStringValue(specialistModel, "name") != activeModel.ModelName {
		t.Fatalf("expected concrete specialist model snapshot, got %#v", specialistModel)
	}
	input := apiTestMapValue(specialist["input"])
	if apiTestStringValue(input, "user_message") != "静态 IP 适合什么？" ||
		apiTestStringValue(input, "router_output_ref") != "router.output" ||
		apiTestStringValue(input, "candidate_page_paths_ref") != "retrieval.candidate_page_paths" {
		t.Fatalf("expected specialist input refs, got %#v", input)
	}
	finalInfo := apiTestMapValue(entry["final"])
	specialistOutput := apiTestMapValue(specialist["output"])
	if apiTestInt64Value(finalInfo, "source_count") != int64(len(apiTestSliceValue(specialistOutput["sources"]))) {
		t.Fatalf("expected final.source_count to count specialist output sources, final=%#v specialist_output=%#v", finalInfo, specialistOutput)
	}
	thinking := apiTestMapValue(specialist["thinking"])
	if thinking["enabled"] != true || thinking["saved"] != true || !strings.Contains(apiTestStringValue(thinking, "content"), "先确认是否有正式知识证据。") {
		t.Fatalf("expected full specialist thinking in JSONL when enabled, got %#v", thinking)
	}
	routerThinking := apiTestMapValue(router["thinking"])
	if routerThinking["enabled"] != false || routerThinking["content"] != nil {
		t.Fatalf("expected router thinking disabled content to be nil, got %#v", routerThinking)
	}
	review := apiTestMapValue(entry["review"])
	if apiTestStringValue(review, "status") != "unreviewed" ||
		apiTestStringValue(review, "error_type") != "" ||
		apiTestStringValue(review, "correct_answer") != "" ||
		apiTestStringValue(review, "note") != "" ||
		apiTestStringValue(review, "reviewed_by") != "" ||
		apiTestStringValue(review, "reviewed_at") != "" {
		t.Fatalf("expected full review placeholder, got %#v", review)
	}
	if value, exists := review["is_good_answer"]; !exists || value != nil {
		t.Fatalf("expected review.is_good_answer=null, got exists=%t value=%#v", exists, value)
	}
}

func TestCustomerChatIgnoresPersistLogFalseAndWritesConversationLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"message":     "静态 IP 适合什么？",
		"session_id":  "external-s-user-1",
		"persist_log": false,
		"simulation":  true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("customer chat failed: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"details"`) || !strings.Contains(rec.Body.String(), `"answer":"静态 IP 适合稳定账号和白名单绑定。"`) {
		t.Fatalf("unexpected customer chat: %s", rec.Body.String())
	}
	entry := readLatestCustomerChatLogEntry(t, fixture.deps.WorkspaceDir)
	if entry["session_id"] != "external-s-user-1" || apiTestStringValue(apiTestMapValue(entry["request"]), "message") != "静态 IP 适合什么？" {
		t.Fatalf("expected external customer chat to write a real conversation log, got %#v", entry)
	}
}

func TestInternalCustomerChatStreamWritesConversationLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"message":    "静态 IP 适合什么？",
		"session_id": "audit-s-user-1",
		"entrypoint": "internal",
		"simulation": true,
		"stream":     true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin audit stream customer chat failed: %d %s", rec.Code, rec.Body.String())
	}
	if stream := rec.Body.String(); !strings.Contains(stream, "event: result") || !strings.Contains(stream, "静态 IP 适合稳定账号和白名单绑定。") {
		t.Fatalf("unexpected internal customer chat stream: %s", stream)
	}
	entry := readLatestCustomerChatLogEntry(t, fixture.deps.WorkspaceDir)
	if apiTestStringValue(apiTestMapValue(entry["runtime"]), "entrypoint") != "internal" {
		t.Fatalf("expected internal entrypoint log, got %#v", entry["runtime"])
	}
}

func TestCustomerChatLogCanBeDisabledAndRedactsSecrets(t *testing.T) {
	disabledFixture := newAPITestFixture(t, apiMockLLM{})
	disabled := false
	disabledFixture.deps.Config.CustomerChat.AnswerLog.Enabled = &disabled
	body, _ := json.Marshal(map[string]any{"message": "静态 IP 适合什么？"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	disabledFixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled log customer chat failed: %d %s", rec.Code, rec.Body.String())
	}
	assertNoCustomerChatLogs(t, disabledFixture.deps.WorkspaceDir)

	redactFixture := newAPITestFixture(t, apiMockLLM{})
	secretQuestion := "我的手机号13800138000，token=sk-abcdefgh123456，静态 IP 适合什么？"
	secretBody, _ := json.Marshal(map[string]any{"message": secretQuestion})
	secretReq := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(secretBody))
	secretReq.Header.Set("Content-Type", "application/json")
	secretRec := httptest.NewRecorder()
	redactFixture.router.ServeHTTP(secretRec, secretReq)
	if secretRec.Code != http.StatusOK {
		t.Fatalf("redacted log customer chat failed: %d %s", secretRec.Code, secretRec.Body.String())
	}
	entry := readLatestCustomerChatLogEntry(t, redactFixture.deps.WorkspaceDir)
	encoded, _ := json.Marshal(entry)
	if strings.Contains(string(encoded), "13800138000") || strings.Contains(string(encoded), "sk-abcdefgh123456") {
		t.Fatalf("expected customer chat log to redact secrets, got %s", string(encoded))
	}
}

func TestAdminCustomerConversationsAggregatesCustomerChatLogs(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	postCustomerChat(t, fixture.router, map[string]any{
		"message":           "静态 IP 适合什么？",
		"session_id":        "s-user-1",
		"user_id":           "u-1",
		"message_id":        "duplicate-question-id",
		"answer_message_id": "duplicate-answer-id",
	})
	postCustomerChat(t, fixture.router, map[string]any{
		"message":           "继续说说白名单绑定",
		"session_id":        "s-user-1",
		"user_id":           "u-1",
		"message_id":        "duplicate-question-id",
		"answer_message_id": "duplicate-answer-id",
	})
	postCustomerChat(t, fixture.router, map[string]any{
		"message": "没有 session 的问题",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-conversations?page_size=10", nil)
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list customer conversations failed: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Conversations []struct {
			ID           string `json:"id"`
			SessionID    string `json:"session_id"`
			UserID       string `json:"user_id"`
			TurnCount    int    `json:"turn_count"`
			MessageCount int    `json:"message_count"`
		} `json:"conversations"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode customer conversations: %v", err)
	}
	if list.Total != 2 {
		t.Fatalf("expected two conversation groups, got %+v", list)
	}
	var sessionGroupID string
	for _, item := range list.Conversations {
		if item.SessionID == "s-user-1" {
			sessionGroupID = item.ID
			if item.TurnCount != 2 || item.MessageCount != 4 || item.UserID != "" {
				t.Fatalf("unexpected grouped session summary: %+v", item)
			}
		}
	}
	if sessionGroupID == "" {
		t.Fatalf("expected session group in list: %+v", list.Conversations)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-conversations/"+sessionGroupID, nil)
	detailRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail customer conversation failed: %d %s", detailRec.Code, detailRec.Body.String())
	}
	if strings.Contains(detailRec.Body.String(), "model_json_raw") || strings.Contains(detailRec.Body.String(), "先确认是否有正式知识证据") {
		t.Fatalf("conversation detail should not expose raw model log data: %s", detailRec.Body.String())
	}
	var detail struct {
		Messages []struct {
			ID      string         `json:"id"`
			Role    string         `json:"role"`
			Content string         `json:"content"`
			Details map[string]any `json:"details"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode customer conversation detail: %v", err)
	}
	if len(detail.Messages) != 4 || detail.Messages[0].Role != "user" || detail.Messages[1].Role != "assistant" {
		t.Fatalf("unexpected conversation messages: %+v", detail.Messages)
	}
	if detail.Messages[0].Details != nil {
		t.Fatalf("user conversation message must not include debug details, got %+v", detail.Messages[0].Details)
	}
	assistantDetails := detail.Messages[1].Details
	if assistantDetails["process_summary"] == nil || assistantDetails["answer_mode"] == nil {
		t.Fatalf("expected assistant customer conversation summary details, got %+v", assistantDetails)
	}
	if assistantDetails["steps"] != nil {
		t.Fatalf("conversation detail should not expose execution steps; use trace detail endpoint instead, got %+v", assistantDetails)
	}
	seenMessageIDs := map[string]bool{}
	for _, message := range detail.Messages {
		if message.ID == "" {
			t.Fatalf("expected stable customer conversation message id, got %+v", detail.Messages)
		}
		if seenMessageIDs[message.ID] {
			t.Fatalf("expected unique customer conversation message ids, got duplicate %q in %+v", message.ID, detail.Messages)
		}
		seenMessageIDs[message.ID] = true
	}
}

func TestAdminCustomerConversationsSearchAndEmptyDisabledLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	postCustomerChat(t, fixture.router, map[string]any{
		"message":    "静态 IP 适合什么？",
		"session_id": "s-search-1",
	})
	postCustomerChat(t, fixture.router, map[string]any{
		"message":    "动态 IP 怎么购买？",
		"session_id": "s-search-2",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-conversations?q=动态", nil)
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search customer conversations failed: %d %s", rec.Code, rec.Body.String())
	}
	var searched struct {
		Conversations []struct {
			SessionID string `json:"session_id"`
		} `json:"conversations"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &searched); err != nil {
		t.Fatalf("decode searched customer conversations: %v", err)
	}
	if searched.Total != 1 || searched.Conversations[0].SessionID != "s-search-2" {
		t.Fatalf("unexpected search result: %+v", searched)
	}

	emptyFixture := newAPITestFixture(t, apiMockLLM{})
	disabled := false
	emptyFixture.deps.Config.CustomerChat.AnswerLog.Enabled = &disabled
	emptyReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-conversations", nil)
	emptyRec := httptest.NewRecorder()
	emptyFixture.router.ServeHTTP(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusOK {
		t.Fatalf("empty customer conversations failed: %d %s", emptyRec.Code, emptyRec.Body.String())
	}
	var empty struct {
		Total int `json:"total"`
		Log   struct {
			Enabled bool `json:"enabled"`
		} `json:"log"`
	}
	if err := json.Unmarshal(emptyRec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty customer conversations: %v", err)
	}
	if empty.Total != 0 || empty.Log.Enabled {
		t.Fatalf("expected empty disabled log response, got %+v", empty)
	}
}

func TestAdminCustomerConversationsFiltersAndMetadata(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	postCustomerChat(t, fixture.router, map[string]any{
		"message":    "静态 IP 价格怎么卖？",
		"session_id": "s-filter-external",
		"entrypoint": "external",
		"simulation": false,
	})
	postCustomerChat(t, fixture.router, map[string]any{
		"message":    "白名单怎么配置？",
		"session_id": "s-filter-internal-test",
		"entrypoint": "internal",
		"simulation": true,
	})
	postCustomerChat(t, fixture.router, map[string]any{
		"message":    "怎么购买静态 IP？",
		"session_id": "s-filter-internal-formal",
		"entrypoint": "internal",
		"simulation": false,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-conversations?page_size=10", nil)
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list customer conversations failed: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Conversations []struct {
			ID                  string   `json:"id"`
			SessionID           string   `json:"session_id"`
			Entrypoints         []string `json:"entrypoints"`
			LastEntrypoint      string   `json:"last_entrypoint"`
			LastSimulation      bool     `json:"last_simulation"`
			LastSpecialist      string   `json:"last_specialist"`
			LastTotalDurationMS int64    `json:"last_total_duration_ms"`
			AverageDurationMS   *int64   `json:"average_duration_ms"`
			LastSourceCount     int64    `json:"last_source_count"`
			ErrorCount          int      `json:"error_count"`
			ReviewRequiredCount int      `json:"review_required_count"`
		} `json:"conversations"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode customer conversations: %v", err)
	}
	if list.Total != 3 {
		t.Fatalf("expected three conversations, got %+v", list)
	}
	var internalTestID string
	for _, item := range list.Conversations {
		if len(item.Entrypoints) != 1 || item.Entrypoints[0] != item.LastEntrypoint {
			t.Fatalf("expected entrypoint summary for item, got %+v", item)
		}
		if item.ErrorCount != 0 || item.ReviewRequiredCount != 0 {
			t.Fatalf("expected no errors or review-required records, got %+v", item)
		}
		if item.AverageDurationMS == nil {
			t.Fatalf("expected average_duration_ms in summary, got %+v", item)
		}
		if item.SessionID == "s-filter-internal-test" {
			internalTestID = item.ID
			if item.LastEntrypoint != "internal" || !item.LastSimulation || item.LastSpecialist != "technical" {
				t.Fatalf("unexpected internal simulation metadata: %+v", item)
			}
			if item.LastSourceCount != 1 {
				t.Fatalf("expected last_source_count from final.source_count, got %+v", item)
			}
		}
	}
	if internalTestID == "" {
		t.Fatalf("expected internal simulation conversation in list: %+v", list.Conversations)
	}

	for _, tc := range []struct {
		path      string
		wantTotal int
		wantID    string
	}{
		{path: "/api/v1/admin/customer-conversations?entrypoint=internal&page_size=10", wantTotal: 2},
		{path: "/api/v1/admin/customer-conversations?entrypoint=external&page_size=10", wantTotal: 1, wantID: "s-filter-external"},
		{path: "/api/v1/admin/customer-conversations?simulation=true&page_size=10", wantTotal: 1, wantID: "s-filter-internal-test"},
		{path: "/api/v1/admin/customer-conversations?simulation=false&page_size=10", wantTotal: 2},
	} {
		filterRec := httptest.NewRecorder()
		fixture.router.ServeHTTP(filterRec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if filterRec.Code != http.StatusOK {
			t.Fatalf("filter %s failed: %d %s", tc.path, filterRec.Code, filterRec.Body.String())
		}
		var filtered struct {
			Conversations []struct {
				SessionID string `json:"session_id"`
			} `json:"conversations"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(filterRec.Body.Bytes(), &filtered); err != nil {
			t.Fatalf("decode filtered conversations: %v", err)
		}
		if filtered.Total != tc.wantTotal {
			t.Fatalf("filter %s expected total %d, got %+v", tc.path, tc.wantTotal, filtered)
		}
		if tc.wantID != "" && (len(filtered.Conversations) != 1 || filtered.Conversations[0].SessionID != tc.wantID) {
			t.Fatalf("filter %s expected session %s, got %+v", tc.path, tc.wantID, filtered)
		}
	}

	detailRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(detailRec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-conversations/"+internalTestID, nil))
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail customer conversation failed: %d %s", detailRec.Code, detailRec.Body.String())
	}
	var detail struct {
		Messages []struct {
			Role           string `json:"role"`
			Entrypoint     string `json:"entrypoint"`
			Simulation     bool   `json:"simulation"`
			Specialist     string `json:"specialist"`
			DurationMS     int64  `json:"duration_ms"`
			SourceCount    int64  `json:"source_count"`
			ReviewRequired bool   `json:"review_required"`
			ErrorStage     string `json:"error_stage"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode customer conversation detail: %v", err)
	}
	if len(detail.Messages) != 2 {
		t.Fatalf("expected one turn in detail, got %+v", detail.Messages)
	}
	assistant := detail.Messages[1]
	if assistant.Role != "assistant" || assistant.Entrypoint != "internal" || !assistant.Simulation || assistant.Specialist != "technical" || assistant.SourceCount != 1 || assistant.ReviewRequired || assistant.ErrorStage != "" || assistant.DurationMS < 0 {
		t.Fatalf("unexpected assistant metadata: %+v", assistant)
	}
}

func TestAdminDeleteCustomerConversationRemovesOnlyTargetJSONLLines(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	firstTargetRec := postCustomerChat(t, fixture.router, map[string]any{
		"message":    "静态 IP 价格怎么卖？",
		"session_id": "s-delete-target",
	})
	secondTargetRec := postCustomerChat(t, fixture.router, map[string]any{
		"message":    "继续说说价格",
		"session_id": "s-delete-target",
	})
	otherRec := postCustomerChat(t, fixture.router, map[string]any{
		"message":    "白名单怎么配置？",
		"session_id": "s-delete-other",
	})
	firstTargetTraceID := strings.TrimSpace(firstTargetRec.Header().Get("X-Trace-ID"))
	secondTargetTraceID := strings.TrimSpace(secondTargetRec.Header().Get("X-Trace-ID"))
	otherTraceID := strings.TrimSpace(otherRec.Header().Get("X-Trace-ID"))
	if firstTargetTraceID == "" || secondTargetTraceID == "" || otherTraceID == "" {
		t.Fatalf("expected trace ids, target1=%q target2=%q other=%q", firstTargetTraceID, secondTargetTraceID, otherTraceID)
	}

	deleteRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(deleteRec, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/customer-conversations/s-delete-target", nil))
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete customer conversation failed: %d %s", deleteRec.Code, deleteRec.Body.String())
	}
	var deleted struct {
		OK             bool   `json:"ok"`
		ID             string `json:"id"`
		DeletedRecords int    `json:"deleted_records"`
		TouchedFiles   int    `json:"touched_files"`
		DeletedFiles   int    `json:"deleted_files"`
	}
	if err := json.Unmarshal(deleteRec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if !deleted.OK || deleted.ID != "s-delete-target" || deleted.DeletedRecords != 2 || deleted.TouchedFiles != 1 || deleted.DeletedFiles != 0 {
		t.Fatalf("unexpected delete response: %+v", deleted)
	}

	matches, err := filepath.Glob(filepath.Join(fixture.deps.WorkspaceDir, "customer_chat_logs", "*.jsonl"))
	if err != nil {
		t.Fatalf("glob customer chat logs: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one remaining jsonl file, got %#v", matches)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read remaining jsonl: %v", err)
	}
	if strings.Contains(string(raw), "s-delete-target") || !strings.Contains(string(raw), "s-delete-other") {
		t.Fatalf("expected only other session to remain, got %s", string(raw))
	}

	for _, traceID := range []string{firstTargetTraceID, secondTargetTraceID} {
		traceRec := httptest.NewRecorder()
		fixture.router.ServeHTTP(traceRec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-chat/traces/"+traceID, nil))
		if traceRec.Code != http.StatusNotFound {
			t.Fatalf("expected deleted trace %s to return 404, got %d %s", traceID, traceRec.Code, traceRec.Body.String())
		}
	}
	otherTraceRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(otherTraceRec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-chat/traces/"+otherTraceID, nil))
	if otherTraceRec.Code != http.StatusOK {
		t.Fatalf("expected other trace to remain, got %d %s", otherTraceRec.Code, otherTraceRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-conversations?page_size=10", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list after delete failed: %d %s", listRec.Code, listRec.Body.String())
	}
	var list struct {
		Conversations []struct {
			SessionID string `json:"session_id"`
		} `json:"conversations"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	if list.Total != 1 || list.Conversations[0].SessionID != "s-delete-other" {
		t.Fatalf("expected only other session after delete, got %+v", list)
	}

	missingRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(missingRec, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/customer-conversations/not-found", nil))
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected missing delete to return 404, got %d %s", missingRec.Code, missingRec.Body.String())
	}
}

func TestAdminDeleteCustomerConversationRemovesEmptyJSONLFile(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	postCustomerChat(t, fixture.router, map[string]any{
		"message":    "静态 IP 价格怎么卖？",
		"session_id": "s-delete-only",
	})

	deleteRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(deleteRec, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/customer-conversations/s-delete-only", nil))
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete only customer conversation failed: %d %s", deleteRec.Code, deleteRec.Body.String())
	}
	var deleted struct {
		DeletedRecords int `json:"deleted_records"`
		TouchedFiles   int `json:"touched_files"`
		DeletedFiles   int `json:"deleted_files"`
	}
	if err := json.Unmarshal(deleteRec.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleted.DeletedRecords != 1 || deleted.TouchedFiles != 1 || deleted.DeletedFiles != 1 {
		t.Fatalf("expected empty file removal, got %+v", deleted)
	}
	matches, err := filepath.Glob(filepath.Join(fixture.deps.WorkspaceDir, "customer_chat_logs", "*.jsonl"))
	if err != nil {
		t.Fatalf("glob customer chat logs: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected jsonl file to be removed, got %#v", matches)
	}
}

func TestCustomerChatContextCanceledDoesNotEmitLLMFailure(t *testing.T) {
	fixture := newAPITestFixture(t, apiCanceledLLM{})
	body, _ := json.Marshal(map[string]any{
		"message": "静态IP 和 动态IP 价格有什么区别",
		"stream":  true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)

	if body := rec.Body.String(); strings.Contains(body, "event: error") || strings.Contains(body, "context canceled") || strings.Contains(body, "fallback") {
		t.Fatalf("context cancellation should not be emitted as customer chat failure, got %s", body)
	}
}

func TestCustomerChatResponseTimeoutReturnsSafeFallback(t *testing.T) {
	fixture := newAPITestFixture(t, apiBlockingLLM{})
	fixture.deps.Config.CustomerChat.ResponseTimeoutSec = 1
	body, _ := json.Marshal(map[string]any{
		"message": "API 白名单怎么配置？",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected timeout fallback to return 200, got %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if strings.TrimSpace(payload.Answer) == "" || !strings.Contains(payload.Answer, "暂") {
		t.Fatalf("expected safe temporary-unavailable fallback, got %s", rec.Body.String())
	}
}

func TestCustomerChatOrdinaryPriceQuestionKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiCustomerChatTextLLM{text: `{
  "answer_mode": "evidence",
  "answer": "5M 静态 IP 多买多优惠，10 个可以按 90元/个申请。",
  "review_question": "",
  "confidence": 0.9,
  "evidence_confidence": 0.9,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "notes": ""
}`})
	body, _ := json.Marshal(map[string]any{"message": "5M静态IP多少钱？"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("plain price customer chat failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, want := range []string{"多买多优惠", "90元/个"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("expected model discount wording %q to remain, got %s", want, payload.Answer)
		}
	}
}

func TestCustomerChatOrdinaryStaticPriceKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiCustomerChatTextLLM{text: `{
  "answer_mode": "evidence",
  "answer": "我们静态 IP 分为共享型和独享型两类，按月计费：共享型起步价约 25 至 70 元/个/月，按需选择数据中心或住宅 IP，数量越多越划算（买 5 个起可享折扣）。独享型带宽资源更稳，起步价约 300 至 800 元/个/月。您这边主要是做账号运营还是数据采集呢？",
  "review_question": "",
  "confidence": 0.9,
  "evidence_confidence": 0.9,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/si-ye-tian-static-ip-pricing.md","confidence":"high"}],
  "notes": ""
}`})
	body, _ := json.Marshal(map[string]any{"message": "静态IP 怎么卖的?"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ordinary static price answer failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, want := range []string{"25", "70", "300", "800"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("expected base price %q to remain, got %s", want, payload.Answer)
		}
	}
	for _, want := range []string{"数量越多", "买 5 个", "折扣"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("expected model discount wording %q to remain, got %s", want, payload.Answer)
		}
	}
}

func TestCustomerChatMissingStaticSubtypeKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiCustomerChatTextLLM{text: `{
  "answer_mode": "mixed",
  "answer": "共享型静态IP购买300个以上已经是4折最低价了，10000个也是按这个折扣算。5M折后10元/个/月，10000个每月10万。您打算选哪种带宽？",
  "review_question": "",
  "confidence": 0.7,
  "evidence_confidence": 0.9,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "notes": ""
}`})
	body, _ := json.Marshal(map[string]any{
		"message": "如果买10000个ip嗯",
		"history": []map[string]any{
			{"role": "user", "content": "静态ip"},
			{"role": "user", "content": "1000ge ip neng给优惠么"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("discount customer chat failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, want := range []string{"4折", "折后", "10元/个", "每月10万"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("expected model discount wording %q to remain, got %s", want, payload.Answer)
		}
	}
}

func TestCustomerChatBandwidthQuestionKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiCustomerChatTextLLM{text: `{
  "answer_mode": "evidence",
  "answer": "三种带宽的核心区别在于服务器带宽规格。200个数量选共享型很划算，参考价如下：5M折后约15元/个/月，共约3000元/月；10M折后约18元/个/月，共约3600元/月；20M折后约52.5元/个/月，共约10500元/月。您偏向共享型还是独享型？",
  "review_question": "",
  "confidence": 0.75,
  "evidence_confidence": 0.85,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "notes": ""
}`})
	body, _ := json.Marshal(map[string]any{
		"message": "大概得需要200个吧, 带宽这三种有什么区别?",
		"history": []map[string]any{
			{"role": "user", "content": "静态IP 怎么卖的?"},
			{"role": "assistant", "content": "静态 IP 分共享型和独享型两种。"},
			{"role": "user", "content": "我主要是做游戏代练的, 对IP稳定性比较看重"},
			{"role": "assistant", "content": "游戏代练确实对稳定性有要求。独享型静态IP带宽独享，比共享型更推荐。"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bandwidth customer chat failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, want := range []string{"折后", "15元/个", "18元/个", "52.5元/个", "3000元/月", "3600元/月", "10500元/月"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("expected model discount wording %q to remain, got %s", want, payload.Answer)
		}
	}
	for _, want := range []string{"5M", "10M", "20M"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("expected bandwidth-focused answer to contain %q, got %s", want, payload.Answer)
		}
	}
}

func TestCustomerChatPurchaseKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiCustomerChatTextLLM{text: `{
  "answer_mode": "evidence",
  "answer": "数据中心共享型静态IP如果买 100 个，5M 折后约 17.5元/个/月，可以申请优惠。",
  "review_question": "",
  "confidence": 0.9,
  "evidence_confidence": 0.85,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "notes": ""
}`})
	body, _ := json.Marshal(map[string]any{
		"message": "我想购买数据中心IP",
		"history": []map[string]any{
			{"role": "user", "content": "静态IP怎么卖的?"},
			{"role": "assistant", "content": "静态 IP 分共享型和独享型两种。"},
			{"role": "user", "content": "都有什么"},
			{"role": "assistant", "content": "静态 IP 主要有共享型和独享型两种。"},
			{"role": "user", "content": "我想要共享的"},
			{"role": "assistant", "content": "好的，共享型静态IP有数据中心IP和住宅IP两种。"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purchase customer chat failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, want := range []string{"申请优惠", "折后", "17.5元/个"} {
		if !strings.Contains(payload.Answer, want) {
			t.Fatalf("expected model wording %q to remain, got %s", want, payload.Answer)
		}
	}
	if strings.Contains(rec.Body.String(), `"details"`) {
		t.Fatalf("external customer chat must not expose sanitizer diagnostics in details, got %s", rec.Body.String())
	}

	entry := readLatestCustomerChatLogEntry(t, fixture.deps.WorkspaceDir)
	encodedLog, _ := json.Marshal(entry)
	for _, want := range []string{"17.5元/个", "折后"} {
		if !strings.Contains(string(encodedLog), want) {
			t.Fatalf("expected customer log to persist final model answer %q, got %s", want, string(encodedLog))
		}
	}
	if strings.Contains(string(encodedLog), "sanitizers") {
		t.Fatalf("expected service-layer sanitizer diagnostics to be absent, got %s", string(encodedLog))
	}
}

func TestAdminCustomerChatAuditStreamResultEmitsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiCustomerChatTextLLM{text: discountInquiryLLMText()})
	body, _ := json.Marshal(map[string]any{
		"message":    "我想买10个5M共享型静态IP，可以申请优惠吗？",
		"entrypoint": "internal",
		"simulation": true,
		"stream":     true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit stream discount customer chat failed: %d %s", rec.Code, rec.Body.String())
	}
	stream := rec.Body.String()
	if !strings.Contains(stream, `event: result`) {
		t.Fatalf("expected stream result event, got %s", stream)
	}
	if !strings.Contains(stream, "可以帮您按 5M 共享型静态 IP 10 个的方案申请") {
		t.Fatalf("expected stream result to include model answer, got %s", stream)
	}
}

func discountInquiryLLMText() string {
	return `{
  "answer_mode": "evidence",
  "answer": "可以帮您按 5M 共享型静态 IP 10 个的方案申请 90元/个，最终以人工确认为准。",
  "review_question": "",
  "confidence": 0.9,
  "evidence_confidence": 0.9,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "notes": ""
}`
}

func TestReviewAPIUsesSuggestedTargetPathAndApprovesKnowledgePage(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	reviewSvc := service.NewReviewQueueService(fixture.deps)
	item, err := reviewSvc.CreatePending(context.Background(), service.ReviewCreateRequest{
		Question:            "静态 IP 适合什么场景？",
		DraftAnswer:         "适合账号运营、白名单绑定和远程办公。",
		SuggestedTargetPath: "wiki/knowledge/static-ip.md",
		MatchedPages:        []string{"wiki/knowledge/static-ip.md"},
	})
	if err != nil {
		t.Fatalf("CreatePending: %v", err)
	}

	nextReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/reviews/next", nil)
	nextRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(nextRec, nextReq)
	if nextRec.Code != http.StatusOK {
		t.Fatalf("review next failed: %d %s", nextRec.Code, nextRec.Body.String())
	}
	nextBody := nextRec.Body.String()
	if !strings.Contains(nextBody, `"suggested_target_path":"wiki/knowledge/static-ip.md"`) {
		t.Fatalf("expected suggested_target_path in response, got %s", nextBody)
	}

	approveBody, _ := json.Marshal(map[string]any{"target_path": "wiki/knowledge/static-ip.md"})
	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reviews/"+item.ID+"/approve", bytes.NewReader(approveBody))
	approveReq.Header.Set("Content-Type", "application/json")
	approveRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(approveRec, approveReq)
	if approveRec.Code != http.StatusOK {
		t.Fatalf("review approve failed: %d %s", approveRec.Code, approveRec.Body.String())
	}
	targetRaw, err := os.ReadFile(filepath.Join(fixture.root, "wiki", "knowledge", "static-ip.md"))
	if err != nil {
		t.Fatalf("read approved target: %v", err)
	}
	if target := string(targetRaw); !strings.Contains(target, "## Human Reviewed Knowledge") || !strings.Contains(target, "适合账号运营、白名单绑定和远程办公。") {
		t.Fatalf("expected approved knowledge content, got %s", target)
	}
}

func TestAdminLLMModelCRUDDoesNotExposeAPIKey(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})

	createBody, _ := json.Marshal(map[string]any{
		"display_name":      "Test Model",
		"provider":          "test",
		"base_url":          "http://llm.example/v1",
		"model_name":        "test-model",
		"api_key":           "secret-api-key",
		"timeout_sec":       11,
		"admin_timeout_sec": 22,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/models", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create model failed: %d %s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), "secret-api-key") {
		t.Fatalf("create response leaked api key: %s", createRec.Body.String())
	}
	var createResp struct {
		Model struct {
			ID         string `json:"id"`
			APIKeyMask string `json:"api_key_mask"`
		} `json:"model"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.Model.ID == "" || createResp.Model.APIKeyMask == "" {
		t.Fatalf("expected model id and api key mask: %s", createRec.Body.String())
	}

	updateBody, _ := json.Marshal(map[string]any{
		"display_name":      "Updated Model",
		"provider":          "test",
		"base_url":          "http://llm.example/v1",
		"model_name":        "test-model-updated",
		"api_key":           "",
		"timeout_sec":       33,
		"admin_timeout_sec": 44,
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/models/"+createResp.Model.ID, bytes.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update model failed: %d %s", updateRec.Code, updateRec.Body.String())
	}
	storedModel, err := fixture.deps.Store.GetLLMModel(context.Background(), createResp.Model.ID)
	if err != nil {
		t.Fatalf("get stored model: %v", err)
	}
	if storedModel.APIKey != "secret-api-key" || storedModel.ModelName != "test-model-updated" {
		t.Fatalf("unexpected stored model after update: %+v", storedModel)
	}

	activateReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/models/"+createResp.Model.ID+"/activate", nil)
	activateRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(activateRec, activateReq)
	if activateRec.Code != http.StatusOK {
		t.Fatalf("activate model failed: %d %s", activateRec.Code, activateRec.Body.String())
	}
	activeModel, err := fixture.deps.Store.GetActiveLLMModel(context.Background())
	if err != nil {
		t.Fatalf("get active model: %v", err)
	}
	if activeModel.ID != createResp.Model.ID {
		t.Fatalf("expected active model %s, got %s", createResp.Model.ID, activeModel.ID)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/models", nil)
	listRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list models failed: %d %s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "secret-api-key") || !strings.Contains(listRec.Body.String(), `"is_active":true`) {
		t.Fatalf("unexpected list response: %s", listRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/models/"+createResp.Model.ID, nil)
	deleteRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete model failed: %d %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestAdminLLMModelConnectionTestUsesStoredModel(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Header.Get("Authorization"))
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode model test request: %v", err)
		}
		if payload.Model != "test-chat-model" {
			t.Fatalf("unexpected model name %q", payload.Model)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer server.Close()

	model := &store.LLMModel{
		ID:              "connection-test",
		DisplayName:     "Connection Test",
		Provider:        "test",
		BaseURL:         server.URL,
		ModelName:       "test-chat-model",
		APIKey:          "secret-api-key",
		TimeoutSec:      5,
		AdminTimeoutSec: 5,
	}
	if err := fixture.deps.Store.CreateLLMModel(context.Background(), model); err != nil {
		t.Fatalf("create model: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/models/connection-test/test", nil)
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("test model failed: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK        bool   `json:"ok"`
		Message   string `json:"message"`
		LatencyMS int64  `json:"latency_ms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !payload.OK || !strings.Contains(payload.Message, "连接成功") || payload.LatencyMS < 0 {
		t.Fatalf("unexpected test response: %+v body=%s", payload, rec.Body.String())
	}
	if got := strings.Join(requests, ","); got != "Bearer secret-api-key" {
		t.Fatalf("unexpected auth header: %s", got)
	}
	if strings.Contains(rec.Body.String(), "secret-api-key") {
		t.Fatalf("model test response leaked api key: %s", rec.Body.String())
	}
}

func TestAdminDashboardReturnsSummaryWithoutAuth(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	defer fixture.deps.Store.Close()

	model := &store.LLMModel{
		ID:              "dashboard-model",
		DisplayName:     "Dashboard Model",
		Provider:        "test",
		BaseURL:         "https://example.test",
		ModelName:       "test-model",
		APIKey:          "dashboard-secret",
		IsActive:        true,
		TimeoutSec:      5,
		AdminTimeoutSec: 5,
	}
	if err := fixture.deps.Store.CreateLLMModel(context.Background(), model); err != nil {
		t.Fatalf("create model: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard", nil)
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard failed: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		ActiveModel struct {
			DisplayName string `json:"display_name"`
		} `json:"active_model"`
		ModelsTotal     int `json:"models_total"`
		ReviewPending   int `json:"review_pending"`
		CustomerChatLog struct {
			Enabled       bool `json:"enabled"`
			Redact        bool `json:"redact"`
			RetentionDays int  `json:"retention_days"`
		} `json:"customer_chat_log"`
		QMD struct {
			Index string `json:"index"`
			Root  string `json:"root"`
		} `json:"qmd"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if payload.ActiveModel.DisplayName != "Dashboard Model" || payload.ModelsTotal != 1 {
		t.Fatalf("unexpected dashboard model summary: %+v body=%s", payload, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "dashboard-secret") {
		t.Fatalf("dashboard leaked api key: %s", rec.Body.String())
	}
	if payload.QMD.Index != "test-index" || payload.QMD.Root == "" {
		t.Fatalf("unexpected qmd summary: %+v", payload.QMD)
	}
	if !payload.CustomerChatLog.Enabled || !payload.CustomerChatLog.Redact || payload.CustomerChatLog.RetentionDays != 14 {
		t.Fatalf("unexpected customer chat log summary: %+v", payload.CustomerChatLog)
	}
}

func TestAdminRuntimeSettingsAPI(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime-settings", nil)
	getRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get runtime settings failed: %d %s", getRec.Code, getRec.Body.String())
	}
	var defaults struct {
		Settings    service.RuntimeSettings            `json:"settings"`
		Defaults    service.RuntimeSettings            `json:"defaults"`
		Environment service.RuntimeEnvironmentSettings `json:"environment"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &defaults); err != nil {
		t.Fatalf("decode defaults: %v", err)
	}
	if defaults.Settings.CustomerChat.CandidateTopK == 0 || defaults.Environment.WikiRoot == "" {
		t.Fatalf("unexpected runtime defaults: %+v", defaults)
	}

	next := defaults.Settings
	next.CustomerChat.DirectMin = 0.81
	next.CustomerChat.ReviewMin = 0.35
	next.CustomerChat.CandidateTopK = 9
	next.CustomerChat.MaxEvidenceChars = 3200
	next.CustomerChat.RouterModelID = "router-fast"
	next.CustomerChat.SpecialistModelID = "specialist-main"
	routerThinking := false
	specialistThinking := true
	next.CustomerChat.RouterEnableThinking = &routerThinking
	next.CustomerChat.SpecialistEnableThinking = &specialistThinking
	next.Support.Phone = "400-test"
	next.AnswerLog.Enabled = false
	next.AnswerLog.Redact = false
	next.AnswerLog.RetentionDays = 30
	next.Knowledge.MaxTextFileKB = 800
	next.Sync.Remote = "origin"
	next.Sync.Branch = "main"
	body, _ := json.Marshal(next)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/runtime-settings", bytes.NewReader(body))
	putReq.Header.Set("Content-Type", "application/json")
	putRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put runtime settings failed: %d %s", putRec.Code, putRec.Body.String())
	}
	var updated struct {
		Settings  service.RuntimeSettings `json:"settings"`
		UpdatedAt string                  `json:"updated_at"`
	}
	if err := json.Unmarshal(putRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated runtime settings: %v", err)
	}
	if updated.Settings.CustomerChat.CandidateTopK != 9 ||
		updated.Settings.CustomerChat.RouterModelID != "router-fast" ||
		updated.Settings.CustomerChat.SpecialistModelID != "specialist-main" ||
		updated.Settings.CustomerChat.RouterEnableThinking == nil ||
		*updated.Settings.CustomerChat.RouterEnableThinking ||
		updated.Settings.CustomerChat.SpecialistEnableThinking == nil ||
		!*updated.Settings.CustomerChat.SpecialistEnableThinking ||
		updated.Settings.AnswerLog.Enabled ||
		updated.Settings.Knowledge.MaxTextFileKB != 800 ||
		updated.UpdatedAt == "" {
		t.Fatalf("unexpected updated runtime settings: %+v", updated)
	}

	invalid := next
	invalid.CustomerChat.CandidateTopK = 30
	invalid.AnswerLog.RetentionDays = 0
	invalidBody, _ := json.Marshal(invalid)
	invalidReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/runtime-settings", bytes.NewReader(invalidBody))
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid runtime settings to return 400, got %d %s", invalidRec.Code, invalidRec.Body.String())
	}
	if !strings.Contains(invalidRec.Body.String(), "customer_query.candidate_top_k") ||
		!strings.Contains(invalidRec.Body.String(), "answer_log.retention_days") {
		t.Fatalf("expected runtime field validation errors, got %s", invalidRec.Body.String())
	}
}

func newAPITestFixture(t *testing.T, client llm.Client) apiTestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := createAPITestWiki(t)
	workspace := filepath.Join(root, ".workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	intentPath := filepath.Join(root, "wiki", "intents", "customer-intents.yaml")
	mustWrite(t, intentPath, `version: 1
fallbacks:
  generic: 您好，这个问题我这边暂时还不能准确确认，您可以补充一下具体场景。
  operation: 您好，这项操作我这边暂时没有准确资料，建议您先参考设备说明或联系对应支持人员处理。
  device_operation: 您好，这项操作我这边暂时没有准确资料，建议您先参考设备说明或联系对应支持人员处理。
  model_unavailable:
    - 当前回复服务短暂不可用，您可以稍后再问一次。
    - 这边暂时没能生成准确回复，您可以稍后重试一次。
rules: []
`)
	enabled := true
	cfg := &config.Config{
		Server:          config.ServerConfig{Mode: "debug"},
		MountedWiki:     config.MountedWikiConfig{Root: root, QMDIndex: "test-index"},
		Retrieval:       config.RetrievalConfig{TopK: 3},
		Workspace:       config.WorkspaceConfig{BaseDir: workspace, DefaultTimeoutSec: 5},
		Sandbox:         config.SandboxConfig{QMDTimeoutSec: 1, PythonTimeoutSec: 1},
		Sync:            config.SyncConfig{Remote: "origin", Branch: "main"},
		LLM:             config.LLMConfig{},
		Storage:         config.StorageConfig{SQLitePath: filepath.Join(workspace, "service.db")},
		Upload:          config.UploadConfig{MaxTextFileKB: 500},
		CustomerIntents: config.CustomerIntentsConfig{Enabled: &enabled, Path: intentPath},
	}
	dataStore, err := store.Open(cfg.Storage.SQLitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{Config: cfg, Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root)})
	registry.Register(apiQMDTool{})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	customerIntents := service.NewCustomerIntentManager(cfg.CustomerIntents)
	deps := service.Deps{
		Config:          cfg,
		Runtime:         rt,
		LLM:             client,
		Retriever:       retrieval.NewQMDRetriever(rt),
		Store:           dataStore,
		CustomerIntents: customerIntents,
		PromptDir:       "../../internal/llm/prompts",
		WorkspaceDir:    cfg.Workspace.BaseDir,
	}
	handlers := api.NewHandlers(
		service.NewCustomerChatService(deps),
		service.NewReviewQueueService(deps),
		service.NewDirectAdminService(deps),
		service.NewUploadService(deps),
		service.NewSyncService(deps),
		dataStore,
		cfg,
		customerIntents,
		service.NewContextCounter(cfg.Context),
	)
	return apiTestFixture{router: app.NewRouter(cfg, handlers, dataStore), root: root, deps: deps}
}

func readLatestCustomerChatLogEntry(t *testing.T, workspaceDir string) map[string]any {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(workspaceDir, "customer_chat_logs", "*.jsonl"))
	if err != nil {
		t.Fatalf("glob customer chat logs: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected customer chat log file under %s", workspaceDir)
	}
	raw, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		t.Fatalf("read customer chat log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		t.Fatalf("expected non-empty customer chat log, got %q", string(raw))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("decode customer chat log entry: %v\n%s", err, lines[len(lines)-1])
	}
	return entry
}

func assertNoCustomerChatLogs(t *testing.T, workspaceDir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(workspaceDir, "customer_chat_logs", "*.jsonl"))
	if err != nil {
		t.Fatalf("glob customer chat logs: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no customer chat logs, matches=%#v", matches)
	}
}

func postCustomerChat(t *testing.T, router http.Handler, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/customer/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("customer chat failed: %d %s", rec.Code, rec.Body.String())
	}
	return rec
}

func readCustomerChatTraceByHeader(t *testing.T, router http.Handler, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	traceID := strings.TrimSpace(rec.Header().Get("X-Trace-ID"))
	if traceID == "" {
		t.Fatalf("expected X-Trace-ID response header")
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/customer-chat/traces/"+traceID, nil)
	traceRec := httptest.NewRecorder()
	router.ServeHTTP(traceRec, req)
	if traceRec.Code != http.StatusOK {
		t.Fatalf("read customer chat trace failed: %d %s", traceRec.Code, traceRec.Body.String())
	}
	var entry map[string]any
	if err := json.Unmarshal(traceRec.Body.Bytes(), &entry); err != nil {
		t.Fatalf("decode customer chat trace: %v", err)
	}
	return entry
}

func apiTestMapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func apiTestStringValue(record map[string]any, key string) string {
	if record == nil {
		return ""
	}
	value := record[key]
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func apiTestInt64Value(record map[string]any, key string) int64 {
	if record == nil {
		return 0
	}
	switch typed := record[key].(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		value, _ := typed.Int64()
		return value
	default:
		return 0
	}
}

func apiTestSliceValue(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}

func uploadFile(t *testing.T, router http.Handler, path string, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func replaceWikiFile(t *testing.T, router http.Handler, path string, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("path", path); err != nil {
		t.Fatalf("write path field: %v", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create replacement file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write replacement file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/wiki/file/replace", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

func createAPITestWiki(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENT.md"), `# AGENT

## 定位

测试知识库治理规则。

## INGEST

raw/ 只读，正式知识写入 wiki/sources 和正式知识目录。

## QUERY

优先查询 knowledge、policies、procedures、comparisons、synthesis，再补充 concepts、entities、intents。

## LINT / REPAIR / REFLECT / MERGE

报告写入根目录 outputs/。
`)
	for _, dir := range []string{
		"raw/articles",
		"outputs",
		"wiki/sources",
		"wiki/knowledge",
		"wiki/policies",
		"wiki/procedures",
		"wiki/comparisons",
		"wiki/concepts",
		"wiki/entities",
		"wiki/synthesis",
		"wiki/intents",
		"wiki/templates",
		"wiki/unconfirmed",
		"wiki/forbidden",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	mustWrite(t, filepath.Join(root, "wiki", "index.md"), "# index\n")
	mustWrite(t, filepath.Join(root, "wiki", "log.md"), "# log\n")
	mustWrite(t, filepath.Join(root, "wiki", "knowledge", "static-ip.md"), `---
title: 静态 IP
type: product_knowledge
source_pages:
  - wiki/sources/customer-doc.md
---

# 静态 IP

## Summary

静态 IP 适合账号运营、白名单绑定和远程办公。
`)
	mustWrite(t, filepath.Join(root, "wiki", "sources", "customer-doc.md"), `---
title: Customer Document
type: source
raw_file: raw/articles/customer.txt
---

## Summary

静态 IP 适合长期稳定网络环境。
`)
	mustWrite(t, filepath.Join(root, "raw", "articles", "customer.txt"), "静态 IP 适合账号运营。")
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
