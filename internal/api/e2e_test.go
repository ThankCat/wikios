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
	return `{
  "answer_type": "text",
  "answer_markdown": "静态 IP 适合账号运营、白名单绑定和远程办公。",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "confidence": 0.9,
  "notes": ""
}`, nil
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
	chunks := []string{
		`{"answer_mode":"evidence",`,
		`"answer_markdown":"静态 IP 适合稳定账号和`,
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

type apiPublicAnswerTextLLM struct {
	text string
}

func (m apiPublicAnswerTextLLM) Chat(ctx context.Context, model string, messages []llm.Message) (string, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "管理员全权限直连模式") {
		return apiMockLLM{}.Chat(ctx, model, messages)
	}
	return m.text, nil
}

func (m apiPublicAnswerTextLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := m.Chat(ctx, model, messages)
	if err != nil {
		return "", err
	}
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
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
	cookie := loginCookie(t, fixture.router)

	rec := uploadFile(t, fixture.router, cookie, "/api/v1/admin/upload", "product-knowledge.md", []byte("# 产品知识\n\n静态 IP 适合稳定场景。"))
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
		rec := uploadFile(t, fixture.router, cookie, "/api/v1/admin/upload", file.name, file.content)
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
	cookie := loginCookie(t, fixture.router)
	rec := uploadFile(t, fixture.router, cookie, "/api/v1/admin/upload/stream", "guide.txt", []byte("静态 IP 使用说明"))
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

func TestPublicAnswerStreamFlagDefaultsToJSONAndStreamsRealDeltas(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	plainBody, _ := json.Marshal(map[string]any{
		"question": "静态 IP 适合什么？",
	})
	plainReq := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(plainBody))
	plainReq.Header.Set("Content-Type", "application/json")
	plainRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(plainRec, plainReq)
	if plainRec.Code != http.StatusOK {
		t.Fatalf("plain public answer failed: %d %s", plainRec.Code, plainRec.Body.String())
	}
	if strings.Contains(plainRec.Body.String(), "event:") {
		t.Fatalf("public answer must default to non-stream JSON, got %s", plainRec.Body.String())
	}
	if !strings.Contains(plainRec.Body.String(), `"answer":"静态 IP 适合稳定账号和白名单绑定。"`) {
		t.Fatalf("unexpected plain public answer: %s", plainRec.Body.String())
	}

	streamBody, _ := json.Marshal(map[string]any{
		"question": "静态 IP 适合什么？",
		"stream":   true,
	})
	streamReq := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(streamBody))
	streamReq.Header.Set("Content-Type", "application/json")
	streamRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(streamRec, streamReq)
	if streamRec.Code != http.StatusOK {
		t.Fatalf("stream public answer failed: %d %s", streamRec.Code, streamRec.Body.String())
	}
	if contentType := streamRec.Result().Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", contentType)
	}
	stream := streamRec.Body.String()
	for _, want := range []string{"event: meta", "event: delta", "event: result", "event: done", "静态 IP 适合稳定账号和白名单绑定。"} {
		if !strings.Contains(stream, want) {
			t.Fatalf("expected public stream to contain %q, got %s", want, stream)
		}
	}
	if deltaAt, resultAt := strings.Index(stream, "event: delta"), strings.Index(stream, "event: result"); deltaAt < 0 || resultAt < 0 || deltaAt > resultAt {
		t.Fatalf("expected answer delta before result, got %s", stream)
	}
	for _, forbidden := range []string{"event: prompt", "event: llm_delta", "answer_markdown"} {
		if strings.Contains(stream, forbidden) {
			t.Fatalf("public stream must not expose raw internals %q: %s", forbidden, stream)
		}
	}
}

func TestPublicAnswerOrdinaryPriceQuestionDoesNotExposeBulkDiscountIntent(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: `{
  "answer_mode": "evidence",
  "answer_markdown": "5M 静态 IP 多买多优惠，10 个可以按 90元/个申请。",
  "review_question": "",
  "confidence": 0.9,
  "evidence_confidence": 0.9,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "user_intent": {
    "type": "price_adjustment",
    "price_info": {
      "expected_price": "90元/个",
      "product_type": "static",
      "product_bandwidth": 5,
      "intended_purchase_quantity": 10,
      "box_usage_time": 0,
      "box_usage_quantity_min": 0,
      "box_usage_quantity_max": 0
    }
  },
  "notes": ""
}`})
	body, _ := json.Marshal(map[string]any{"question": "5M静态IP多少钱？"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("plain price public answer failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Answer     string                    `json:"answer"`
		UserIntent *service.PublicUserIntent `json:"user_intent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.UserIntent != nil {
		t.Fatalf("ordinary price question must not return user_intent, got %#v", payload.UserIntent)
	}
	for _, forbidden := range []string{"多买多优惠", "阶梯优惠", "批量优惠", "90元/个"} {
		if strings.Contains(payload.Answer, forbidden) {
			t.Fatalf("ordinary price answer exposed %q: %s", forbidden, payload.Answer)
		}
	}
}

func TestPublicAnswerStrongDiscountRequestReturnsPriceAdjustmentIntent(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: priceAdjustmentLLMText()})
	body, _ := json.Marshal(map[string]any{"question": "我想买10个5M静态IP，可以申请优惠吗？"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("discount public answer failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		UserIntent *service.PublicUserIntent `json:"user_intent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.UserIntent == nil || payload.UserIntent.Type != "price_adjustment" || payload.UserIntent.PriceInfo == nil {
		t.Fatalf("expected price_adjustment intent, got %#v", payload.UserIntent)
	}
	price := payload.UserIntent.PriceInfo
	if price.ExpectedPrice != "90元/个" || price.ProductType != "static" || price.ProductBandwidth != 5 || price.IntendedPurchaseQuantity != 10 {
		t.Fatalf("unexpected price_info: %#v", price)
	}
}

func TestPublicAnswerStreamResultIncludesUserIntent(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: priceAdjustmentLLMText()})
	body, _ := json.Marshal(map[string]any{
		"question": "我想买10个5M静态IP，可以申请优惠吗？",
		"stream":   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream discount public answer failed: %d %s", rec.Code, rec.Body.String())
	}
	stream := rec.Body.String()
	for _, want := range []string{`event: result`, `"user_intent":{"type":"price_adjustment"`, `"expected_price":"90元/个"`} {
		if !strings.Contains(stream, want) {
			t.Fatalf("expected stream to contain %q, got %s", want, stream)
		}
	}
}

func TestPublicAnswerSwitchIPIntent(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: `{
  "answer_mode": "self_answer",
  "answer_markdown": "可以的，您可以在产品支持的范围内发起切换 IP。",
  "review_question": "",
  "confidence": 0.8,
  "evidence_confidence": 0,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [],
  "user_intent": {"type": "switch_ip"},
  "notes": ""
}`})
	body, _ := json.Marshal(map[string]any{"question": "我要切换IP"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch ip public answer failed: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		UserIntent *service.PublicUserIntent `json:"user_intent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.UserIntent == nil || payload.UserIntent.Type != "switch_ip" || payload.UserIntent.PriceInfo != nil {
		t.Fatalf("expected switch_ip intent without price_info, got %#v", payload.UserIntent)
	}
}

func priceAdjustmentLLMText() string {
	return `{
  "answer_mode": "evidence",
  "answer_markdown": "可以帮您按 5M 静态 IP 10 个的方案申请 90元/个，最终以人工确认为准。",
  "review_question": "",
  "confidence": 0.9,
  "evidence_confidence": 0.9,
  "review_required": false,
  "review_reason": "",
  "suggested_target_path": "",
  "sources": [{"path":"wiki/knowledge/static-ip.md","confidence":"high"}],
  "user_intent": {
    "type": "price_adjustment",
    "price_info": {
      "expected_price": "90元/个",
      "product_type": "static",
      "product_bandwidth": 5,
      "intended_purchase_quantity": 10,
      "box_usage_time": 0,
      "box_usage_quantity_min": 0,
      "box_usage_quantity_max": 0
    }
  },
  "notes": ""
}`
}

func TestReviewAPIUsesSuggestedTargetPathAndApprovesKnowledgePage(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	cookie := loginCookie(t, fixture.router)
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
	nextReq.AddCookie(cookie)
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
	approveReq.AddCookie(cookie)
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

func newAPITestFixture(t *testing.T, client llm.Client) apiTestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := createAPITestWiki(t)
	workspace := filepath.Join(root, ".workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	intentPath := filepath.Join(root, "wiki", "intents", "public-intents.yaml")
	mustWrite(t, intentPath, `version: 1
fallbacks:
  generic: 您好，这个问题我这边暂时还不能准确确认，您可以补充一下具体场景。
  operation: 您好，这项操作我这边暂时没有准确资料，建议您先参考设备说明或联系对应支持人员处理。
  device_operation: 您好，这项操作我这边暂时没有准确资料，建议您先参考设备说明或联系对应支持人员处理。
rules: []
`)
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
		Upload:        config.UploadConfig{MaxTextFileKB: 500},
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
	registry.Register(apiQMDTool{})
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
		service.NewReviewQueueService(deps),
		service.NewDirectAdminService(deps),
		service.NewUploadService(deps),
		service.NewSyncService(deps),
		dataStore,
		cfg,
		cfg.Auth,
		publicIntents,
		service.NewContextCounter(cfg.Context),
	)
	return apiTestFixture{router: app.NewRouter(cfg, handlers, dataStore), root: root, deps: deps}
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
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set cookie")
	}
	return cookies[0]
}

func uploadFile(t *testing.T, router http.Handler, cookie *http.Cookie, path string, filename string, content []byte) *httptest.ResponseRecorder {
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
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
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
