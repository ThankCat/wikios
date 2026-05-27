package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	return `{
  "answer_type": "text",
  "answer": "静态 IP 适合账号运营、白名单绑定和远程办公。",
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

func TestPublicAnswerRejectsStreamingAndOmitsDetails(t *testing.T) {
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
	if strings.Contains(plainRec.Body.String(), `"details"`) || strings.Contains(plainRec.Body.String(), "process_summary") || strings.Contains(plainRec.Body.String(), "steps") {
		t.Fatalf("external public answer must not expose admin details, got %s", plainRec.Body.String())
	}

	streamBody, _ := json.Marshal(map[string]any{
		"question": "静态 IP 适合什么？",
		"stream":   true,
	})
	streamReq := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(streamBody))
	streamReq.Header.Set("Content-Type", "application/json")
	streamRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(streamRec, streamReq)
	if streamRec.Code != http.StatusBadRequest {
		t.Fatalf("expected external stream request to be rejected, got %d %s", streamRec.Code, streamRec.Body.String())
	}
	if !strings.Contains(streamRec.Body.String(), "STREAM_NOT_SUPPORTED") {
		t.Fatalf("expected STREAM_NOT_SUPPORTED error, got %s", streamRec.Body.String())
	}

	streamRouteReq := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer/stream", bytes.NewReader(streamBody))
	streamRouteReq.Header.Set("Content-Type", "application/json")
	streamRouteRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(streamRouteRec, streamRouteReq)
	if streamRouteRec.Code != http.StatusNotFound {
		t.Fatalf("expected removed external stream route to return 404, got %d %s", streamRouteRec.Code, streamRouteRec.Body.String())
	}
}

func TestAdminPublicAnswerAuditReturnsDebugDetailsWithoutWritingConversationLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"question":   "静态 IP 适合什么？",
		"session_id": "test-public-answer",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/public-answer/audit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin public answer audit failed: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Answer  string         `json:"answer"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode admin public answer audit: %v", err)
	}
	if payload.Answer != "静态 IP 适合稳定账号和白名单绑定。" {
		t.Fatalf("unexpected audit answer: %s", payload.Answer)
	}
	if payload.Details["process_summary"] == nil || payload.Details["steps"] == nil {
		t.Fatalf("expected admin public answer audit details, got %+v", payload.Details)
	}
	steps, _ := payload.Details["steps"].([]any)
	if len(steps) < 3 {
		t.Fatalf("expected rich admin public answer audit execution steps, got %+v", payload.Details["steps"])
	}
	for _, key := range []string{"prompt", "model_json_raw", "model_json_parsed", "review_decision"} {
		if payload.Details[key] == nil {
			t.Fatalf("expected admin public answer audit details.%s, got %+v", key, payload.Details)
		}
	}
	assertNoPublicAnswerLogs(t, fixture.deps.WorkspaceDir)

	streamBody, _ := json.Marshal(map[string]any{
		"question":   "静态 IP 适合什么？",
		"session_id": "test-public-answer",
	})
	streamReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/public-answer/audit/stream", bytes.NewReader(streamBody))
	streamReq.Header.Set("Content-Type", "application/json")
	streamRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(streamRec, streamReq)
	if streamRec.Code != http.StatusOK {
		t.Fatalf("admin public answer audit stream failed: %d %s", streamRec.Code, streamRec.Body.String())
	}
	stream := streamRec.Body.String()
	if !strings.Contains(stream, "event: result") || !strings.Contains(stream, `"details"`) || !strings.Contains(stream, "process_summary") || !strings.Contains(stream, "public.specialist.parse") {
		t.Fatalf("expected admin public answer audit stream to include debug details, got %s", stream)
	}
	for _, want := range []string{"event: prompt", "event: llm_delta", "event: llm_reasoning_delta", "event: llm_done", "event: delta"} {
		if !strings.Contains(stream, want) {
			t.Fatalf("expected admin public answer audit stream to include %q, got %s", want, stream)
		}
	}
	if deltaAt, resultAt := strings.Index(stream, "event: delta"), strings.Index(stream, "event: result"); deltaAt < 0 || resultAt < 0 || deltaAt > resultAt {
		t.Fatalf("expected streamed answer delta before result, got %s", stream)
	}
	assertNoPublicAnswerLogs(t, fixture.deps.WorkspaceDir)
}

func TestAdminPublicAnswerAuditMatchesExternalPublicAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"question": "静态 IP 适合什么？",
		"history": []map[string]any{
			{"role": "user", "content": "我想了解静态 IP"},
			{"role": "assistant", "content": "静态 IP 适合固定出口。"},
		},
	})

	publicReq := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	publicReq.Header.Set("Content-Type", "application/json")
	publicRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusOK {
		t.Fatalf("public answer failed: %d %s", publicRec.Code, publicRec.Body.String())
	}

	auditReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/public-answer/audit", bytes.NewReader(body))
	auditReq.Header.Set("Content-Type", "application/json")
	auditRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("admin public answer audit failed: %d %s", auditRec.Code, auditRec.Body.String())
	}

	var publicPayload, auditPayload struct {
		Answer  string         `json:"answer"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(publicRec.Body.Bytes(), &publicPayload); err != nil {
		t.Fatalf("decode public answer: %v", err)
	}
	if err := json.Unmarshal(auditRec.Body.Bytes(), &auditPayload); err != nil {
		t.Fatalf("decode admin audit answer: %v", err)
	}
	if publicPayload.Answer != auditPayload.Answer {
		t.Fatalf("expected audit answer to match external answer, public=%q audit=%q", publicPayload.Answer, auditPayload.Answer)
	}
	if publicPayload.Details != nil || auditPayload.Details == nil {
		t.Fatalf("expected only audit response to include details, public=%+v audit=%+v", publicPayload.Details, auditPayload.Details)
	}
}

func TestPublicAnswerNoActiveModelFailsWithoutFallbackOrConfigurationLeak(t *testing.T) {
	fixture := newAPITestFixture(t, apiNoActiveLLM{})
	body, _ := json.Marshal(map[string]any{
		"question": "静态 IP 适合什么？",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected public model failure status, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"answer"`) || apiContainsModelInternalLeak(rec.Body.String()) {
		t.Fatalf("expected public failure without fallback answer or internal config details, got %s", rec.Body.String())
	}
}

func TestPublicAnswerProviderUnavailableFailsWithoutFallbackOrAccountLeak(t *testing.T) {
	fixture := newAPITestFixture(t, apiProviderUnavailableLLM{})
	body, _ := json.Marshal(map[string]any{
		"question": "你现在不能回复了吗?",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected public provider failure status, got %d %s", rec.Code, rec.Body.String())
	}
	responseBody := rec.Body.String()
	if strings.Contains(responseBody, `"answer"`) || apiContainsModelInternalLeak(responseBody) {
		t.Fatalf("expected provider error hidden from public failure response, got %s", responseBody)
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

func TestPublicContextEstimateIsAvailableWithoutAdminAuth(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"question": "这个怎么买？",
		"history": []map[string]any{
			{"role": "user", "content": "我想了解静态IP"},
			{"role": "assistant", "content": "静态IP适合固定出口场景。"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/context/estimate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public context estimate failed: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Mode         string               `json:"mode"`
		ContextUsage service.ContextUsage `json:"context_usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode context estimate: %v", err)
	}
	if payload.Mode != "public" || payload.ContextUsage.UsedTokens <= 0 || payload.ContextUsage.MaxTokens <= 0 {
		t.Fatalf("unexpected public context estimate: %+v", payload)
	}
}

func TestPublicAnswerWritesQuestionAnswerJSONAndThinkingLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"question":   "静态 IP 适合什么？",
		"session_id": "s-log",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public answer failed: %d %s", rec.Code, rec.Body.String())
	}

	entry := readLatestPublicAnswerLogEntry(t, fixture.deps.WorkspaceDir)
	if entry["question"] != "静态 IP 适合什么？" {
		t.Fatalf("expected logged question, got %#v", entry["question"])
	}
	if entry["answer"] != "静态 IP 适合稳定账号和白名单绑定。" {
		t.Fatalf("expected logged answer, got %#v", entry["answer"])
	}
	if thinking, _ := entry["thinking"].(string); !strings.Contains(thinking, "先做安全边界和禁答检查") {
		t.Fatalf("expected safe audit thinking summary in log, got %#v", entry["thinking"])
	} else if strings.Contains(thinking, "先确认是否有正式知识证据。") {
		t.Fatalf("expected raw model reasoning omitted from public log, got %#v", entry["thinking"])
	}
	jsonData, ok := entry["json_data"].(map[string]any)
	if !ok {
		t.Fatalf("expected json_data object, got %#v", entry["json_data"])
	}
	for _, key := range []string{"response", "details", "model_json_raw", "model_json_parsed", "final_json"} {
		if _, ok := jsonData[key]; !ok {
			t.Fatalf("expected json_data.%s in log, got %#v", key, jsonData)
		}
	}
	rawSummary, ok := jsonData["model_json_raw"].(map[string]any)
	if !ok || rawSummary["omitted"] != true {
		t.Fatalf("expected raw model output to be omitted in public log, got %#v", jsonData["model_json_raw"])
	}
	encodedLog, _ := json.Marshal(entry)
	if strings.Contains(string(encodedLog), "先确认是否有正式知识证据。") {
		t.Fatalf("expected public log not to persist raw model reasoning, got %s", string(encodedLog))
	}
}

func TestPublicAnswerIgnoresPersistLogFalseAndWritesConversationLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"question":    "静态 IP 适合什么？",
		"session_id":  "external-s-user-1",
		"persist_log": false,
		"simulation":  true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public answer failed: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"details"`) || !strings.Contains(rec.Body.String(), `"answer":"静态 IP 适合稳定账号和白名单绑定。"`) {
		t.Fatalf("unexpected public answer: %s", rec.Body.String())
	}
	entry := readLatestPublicAnswerLogEntry(t, fixture.deps.WorkspaceDir)
	if entry["session_id"] != "external-s-user-1" || entry["question"] != "静态 IP 适合什么？" {
		t.Fatalf("expected external public answer to write a real conversation log, got %#v", entry)
	}
}

func TestAdminPublicAnswerAuditStreamDoesNotWriteConversationLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiStreamingMockLLM{})
	body, _ := json.Marshal(map[string]any{
		"question":   "静态 IP 适合什么？",
		"session_id": "audit-s-user-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/public-answer/audit/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin audit stream public answer failed: %d %s", rec.Code, rec.Body.String())
	}
	if stream := rec.Body.String(); !strings.Contains(stream, "event: result") || !strings.Contains(stream, "静态 IP 适合稳定账号和白名单绑定。") {
		t.Fatalf("unexpected admin audit public stream: %s", stream)
	}
	assertNoPublicAnswerLogs(t, fixture.deps.WorkspaceDir)
}

func TestPublicAnswerLogCanBeDisabledAndRedactsSecrets(t *testing.T) {
	disabledFixture := newAPITestFixture(t, apiMockLLM{})
	disabled := false
	disabledFixture.deps.Config.PublicQuery.AnswerLog.Enabled = &disabled
	body, _ := json.Marshal(map[string]any{"question": "静态 IP 适合什么？"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	disabledFixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled log public answer failed: %d %s", rec.Code, rec.Body.String())
	}
	assertNoPublicAnswerLogs(t, disabledFixture.deps.WorkspaceDir)

	redactFixture := newAPITestFixture(t, apiMockLLM{})
	secretQuestion := "我的手机号13800138000，token=sk-abcdefgh123456，静态 IP 适合什么？"
	secretBody, _ := json.Marshal(map[string]any{"question": secretQuestion})
	secretReq := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(secretBody))
	secretReq.Header.Set("Content-Type", "application/json")
	secretRec := httptest.NewRecorder()
	redactFixture.router.ServeHTTP(secretRec, secretReq)
	if secretRec.Code != http.StatusOK {
		t.Fatalf("redacted log public answer failed: %d %s", secretRec.Code, secretRec.Body.String())
	}
	entry := readLatestPublicAnswerLogEntry(t, redactFixture.deps.WorkspaceDir)
	encoded, _ := json.Marshal(entry)
	if strings.Contains(string(encoded), "13800138000") || strings.Contains(string(encoded), "sk-abcdefgh123456") {
		t.Fatalf("expected public answer log to redact secrets, got %s", string(encoded))
	}
}

func TestAdminPublicConversationsAggregatesPublicAnswerLogs(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	postPublicAnswer(t, fixture.router, map[string]any{
		"question":            "静态 IP 适合什么？",
		"session_id":          "s-user-1",
		"user_id":             "u-1",
		"question_message_id": "duplicate-question-id",
		"answer_message_id":   "duplicate-answer-id",
	})
	postPublicAnswer(t, fixture.router, map[string]any{
		"question":            "继续说说白名单绑定",
		"session_id":          "s-user-1",
		"user_id":             "u-1",
		"question_message_id": "duplicate-question-id",
		"answer_message_id":   "duplicate-answer-id",
	})
	postPublicAnswer(t, fixture.router, map[string]any{
		"question": "没有 session 的问题",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public-conversations?page_size=10", nil)
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list public conversations failed: %d %s", rec.Code, rec.Body.String())
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
		t.Fatalf("decode public conversations: %v", err)
	}
	if list.Total != 2 {
		t.Fatalf("expected two conversation groups, got %+v", list)
	}
	var sessionGroupID string
	for _, item := range list.Conversations {
		if item.SessionID == "s-user-1" {
			sessionGroupID = item.ID
			if item.TurnCount != 2 || item.MessageCount != 4 || item.UserID != "u-1" {
				t.Fatalf("unexpected grouped session summary: %+v", item)
			}
		}
	}
	if sessionGroupID == "" {
		t.Fatalf("expected session group in list: %+v", list.Conversations)
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public-conversations/"+sessionGroupID, nil)
	detailRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail public conversation failed: %d %s", detailRec.Code, detailRec.Body.String())
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
		t.Fatalf("decode public conversation detail: %v", err)
	}
	if len(detail.Messages) != 4 || detail.Messages[0].Role != "user" || detail.Messages[1].Role != "assistant" {
		t.Fatalf("unexpected conversation messages: %+v", detail.Messages)
	}
	if detail.Messages[0].Details != nil {
		t.Fatalf("user conversation message must not include debug details, got %+v", detail.Messages[0].Details)
	}
	assistantDetails := detail.Messages[1].Details
	if assistantDetails["process_summary"] == nil || assistantDetails["steps"] == nil {
		t.Fatalf("expected assistant public conversation details, got %+v", assistantDetails)
	}
	seenMessageIDs := map[string]bool{}
	for _, message := range detail.Messages {
		if message.ID == "" {
			t.Fatalf("expected stable public conversation message id, got %+v", detail.Messages)
		}
		if seenMessageIDs[message.ID] {
			t.Fatalf("expected unique public conversation message ids, got duplicate %q in %+v", message.ID, detail.Messages)
		}
		seenMessageIDs[message.ID] = true
	}
}

func TestAdminPublicConversationsSearchAndEmptyDisabledLog(t *testing.T) {
	fixture := newAPITestFixture(t, apiMockLLM{})
	postPublicAnswer(t, fixture.router, map[string]any{
		"question":   "静态 IP 适合什么？",
		"session_id": "s-search-1",
	})
	postPublicAnswer(t, fixture.router, map[string]any{
		"question":   "动态 IP 怎么购买？",
		"session_id": "s-search-2",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public-conversations?q=动态", nil)
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search public conversations failed: %d %s", rec.Code, rec.Body.String())
	}
	var searched struct {
		Conversations []struct {
			SessionID string `json:"session_id"`
		} `json:"conversations"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &searched); err != nil {
		t.Fatalf("decode searched public conversations: %v", err)
	}
	if searched.Total != 1 || searched.Conversations[0].SessionID != "s-search-2" {
		t.Fatalf("unexpected search result: %+v", searched)
	}

	emptyFixture := newAPITestFixture(t, apiMockLLM{})
	disabled := false
	emptyFixture.deps.Config.PublicQuery.AnswerLog.Enabled = &disabled
	emptyReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/public-conversations", nil)
	emptyRec := httptest.NewRecorder()
	emptyFixture.router.ServeHTTP(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusOK {
		t.Fatalf("empty public conversations failed: %d %s", emptyRec.Code, emptyRec.Body.String())
	}
	var empty struct {
		Total int `json:"total"`
		Log   struct {
			Enabled bool `json:"enabled"`
		} `json:"log"`
	}
	if err := json.Unmarshal(emptyRec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty public conversations: %v", err)
	}
	if empty.Total != 0 || empty.Log.Enabled {
		t.Fatalf("expected empty disabled log response, got %+v", empty)
	}
}

func TestPublicAnswerContextCanceledDoesNotEmitLLMFailure(t *testing.T) {
	fixture := newAPITestFixture(t, apiCanceledLLM{})
	body, _ := json.Marshal(map[string]any{
		"question": "静态IP 和 动态IP 价格有什么区别",
		"stream":   true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)

	if body := rec.Body.String(); strings.Contains(body, "event: error") || strings.Contains(body, "context canceled") || strings.Contains(body, "fallback") {
		t.Fatalf("context cancellation should not be emitted as public answer failure, got %s", body)
	}
}

func TestPublicAnswerResponseTimeoutReturnsSafeFallback(t *testing.T) {
	fixture := newAPITestFixture(t, apiBlockingLLM{})
	fixture.deps.Config.PublicQuery.ResponseTimeoutSec = 1
	body, _ := json.Marshal(map[string]any{
		"question": "API 白名单怎么配置？",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
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

func TestPublicAnswerOrdinaryPriceQuestionKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: `{
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
	body, _ := json.Marshal(map[string]any{"question": "5M静态IP多少钱？"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("plain price public answer failed: %d %s", rec.Code, rec.Body.String())
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

func TestPublicAnswerOrdinaryStaticPriceKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: `{
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
	body, _ := json.Marshal(map[string]any{"question": "静态IP 怎么卖的?"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
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

func TestPublicAnswerMissingStaticSubtypeKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: `{
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
		"question": "如果买10000个ip嗯",
		"history": []map[string]any{
			{"role": "user", "content": "静态ip"},
			{"role": "user", "content": "1000ge ip neng给优惠么"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("discount public answer failed: %d %s", rec.Code, rec.Body.String())
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

func TestPublicAnswerBandwidthQuestionKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: `{
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
		"question": "大概得需要200个吧, 带宽这三种有什么区别?",
		"history": []map[string]any{
			{"role": "user", "content": "静态IP 怎么卖的?"},
			{"role": "assistant", "content": "静态 IP 分共享型和独享型两种。"},
			{"role": "user", "content": "我主要是做游戏代练的, 对IP稳定性比较看重"},
			{"role": "assistant", "content": "游戏代练确实对稳定性有要求。独享型静态IP带宽独享，比共享型更推荐。"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bandwidth public answer failed: %d %s", rec.Code, rec.Body.String())
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

func TestPublicAnswerPurchaseKeepsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: `{
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
		"question": "我想购买数据中心IP",
		"history": []map[string]any{
			{"role": "user", "content": "静态IP怎么卖的?"},
			{"role": "assistant", "content": "静态 IP 分共享型和独享型两种。"},
			{"role": "user", "content": "都有什么"},
			{"role": "assistant", "content": "静态 IP 主要有共享型和独享型两种。"},
			{"role": "user", "content": "我想要共享的"},
			{"role": "assistant", "content": "好的，共享型静态IP有数据中心IP和住宅IP两种。"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("purchase public answer failed: %d %s", rec.Code, rec.Body.String())
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
		t.Fatalf("external public answer must not expose sanitizer diagnostics in details, got %s", rec.Body.String())
	}

	entry := readLatestPublicAnswerLogEntry(t, fixture.deps.WorkspaceDir)
	encodedLog, _ := json.Marshal(entry)
	for _, want := range []string{"17.5元/个", "折后"} {
		if !strings.Contains(string(encodedLog), want) {
			t.Fatalf("expected public log to persist final model answer %q, got %s", want, string(encodedLog))
		}
	}
	jsonData, ok := entry["json_data"].(map[string]any)
	if !ok {
		t.Fatalf("expected json_data object, got %#v", entry["json_data"])
	}
	details, ok := jsonData["details"].(map[string]any)
	if !ok {
		t.Fatalf("expected json_data.details object, got %#v", jsonData["details"])
	}
	if sanitizers, ok := details["sanitizers"].([]any); ok && len(sanitizers) > 0 {
		t.Fatalf("expected service-layer sanitizer diagnostics to be absent, got %#v", sanitizers)
	}
}

func TestAdminPublicAnswerAuditStreamResultEmitsModelAnswer(t *testing.T) {
	fixture := newAPITestFixture(t, apiPublicAnswerTextLLM{text: discountInquiryLLMText()})
	body, _ := json.Marshal(map[string]any{
		"question": "我想买10个5M共享型静态IP，可以申请优惠吗？",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/public-answer/audit/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fixture.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit stream discount public answer failed: %d %s", rec.Code, rec.Body.String())
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
		PublicAnswerLog struct {
			Enabled       bool `json:"enabled"`
			Redact        bool `json:"redact"`
			RetentionDays int  `json:"retention_days"`
		} `json:"public_answer_log"`
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
	if !payload.PublicAnswerLog.Enabled || !payload.PublicAnswerLog.Redact || payload.PublicAnswerLog.RetentionDays != 14 {
		t.Fatalf("unexpected public log summary: %+v", payload.PublicAnswerLog)
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
	if defaults.Settings.PublicQuery.CandidateTopK == 0 || defaults.Environment.WikiRoot == "" {
		t.Fatalf("unexpected runtime defaults: %+v", defaults)
	}

	next := defaults.Settings
	next.PublicQuery.DirectMin = 0.81
	next.PublicQuery.ReviewMin = 0.35
	next.PublicQuery.CandidateTopK = 9
	next.PublicQuery.MaxEvidenceChars = 3200
	next.PublicQuery.RouterModelID = "router-fast"
	next.PublicQuery.SpecialistModelID = "specialist-main"
	routerThinking := false
	specialistThinking := true
	next.PublicQuery.RouterEnableThinking = &routerThinking
	next.PublicQuery.SpecialistEnableThinking = &specialistThinking
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
	if updated.Settings.PublicQuery.CandidateTopK != 9 ||
		updated.Settings.PublicQuery.RouterModelID != "router-fast" ||
		updated.Settings.PublicQuery.SpecialistModelID != "specialist-main" ||
		updated.Settings.PublicQuery.RouterEnableThinking == nil ||
		*updated.Settings.PublicQuery.RouterEnableThinking ||
		updated.Settings.PublicQuery.SpecialistEnableThinking == nil ||
		!*updated.Settings.PublicQuery.SpecialistEnableThinking ||
		updated.Settings.AnswerLog.Enabled ||
		updated.Settings.Knowledge.MaxTextFileKB != 800 ||
		updated.UpdatedAt == "" {
		t.Fatalf("unexpected updated runtime settings: %+v", updated)
	}

	invalid := next
	invalid.PublicQuery.CandidateTopK = 30
	invalid.AnswerLog.RetentionDays = 0
	invalidBody, _ := json.Marshal(invalid)
	invalidReq := httptest.NewRequest(http.MethodPut, "/api/v1/admin/runtime-settings", bytes.NewReader(invalidBody))
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidRec := httptest.NewRecorder()
	fixture.router.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid runtime settings to return 400, got %d %s", invalidRec.Code, invalidRec.Body.String())
	}
	if !strings.Contains(invalidRec.Body.String(), "public_query.candidate_top_k") ||
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
	intentPath := filepath.Join(root, "wiki", "intents", "public-intents.yaml")
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
		Server:        config.ServerConfig{Mode: "debug"},
		MountedWiki:   config.MountedWikiConfig{Root: root, QMDIndex: "test-index"},
		Retrieval:     config.RetrievalConfig{TopK: 3},
		Workspace:     config.WorkspaceConfig{BaseDir: workspace, DefaultTimeoutSec: 5},
		Sandbox:       config.SandboxConfig{QMDTimeoutSec: 1, PythonTimeoutSec: 1},
		Sync:          config.SyncConfig{Remote: "origin", Branch: "main"},
		LLM:           config.LLMConfig{},
		Storage:       config.StorageConfig{SQLitePath: filepath.Join(workspace, "service.db")},
		Upload:        config.UploadConfig{MaxTextFileKB: 500},
		PublicIntents: config.PublicIntentsConfig{Enabled: &enabled, Path: intentPath},
	}
	dataStore, err := store.Open(cfg.Storage.SQLitePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
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
		publicIntents,
		service.NewContextCounter(cfg.Context),
	)
	return apiTestFixture{router: app.NewRouter(cfg, handlers, dataStore), root: root, deps: deps}
}

func readLatestPublicAnswerLogEntry(t *testing.T, workspaceDir string) map[string]any {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(workspaceDir, "public_answer_logs", "*.jsonl"))
	if err != nil {
		t.Fatalf("glob public answer logs: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected public answer log file under %s", workspaceDir)
	}
	raw, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		t.Fatalf("read public answer log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) == "" {
		t.Fatalf("expected non-empty public answer log, got %q", string(raw))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("decode public answer log entry: %v\n%s", err, lines[len(lines)-1])
	}
	return entry
}

func assertNoPublicAnswerLogs(t *testing.T, workspaceDir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(workspaceDir, "public_answer_logs", "*.jsonl"))
	if err != nil {
		t.Fatalf("glob public answer logs: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no public answer logs, matches=%#v", matches)
	}
}

func postPublicAnswer(t *testing.T, router http.Handler, payload map[string]any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/answer", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("public answer failed: %d %s", rec.Code, rec.Body.String())
	}
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
