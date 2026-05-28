package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
)

type testRuntimeTool struct {
	name string
	fn   func(context.Context, *runtime.ExecEnv, map[string]any) (runtime.ToolResult, error)
}

func (t testRuntimeTool) Name() string {
	return t.name
}

func (t testRuntimeTool) RiskLevel() runtime.RiskLevel {
	return runtime.RiskLow
}

func (t testRuntimeTool) Validate(map[string]any) error {
	return nil
}

func (t testRuntimeTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	if t.fn != nil {
		return t.fn(ctx, env, args)
	}
	return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{}}, nil
}

func testRuntime(tools ...runtime.Tool) *runtime.Runtime {
	return runtime.NewRuntime(
		runtime.NewRegistry(tools...),
		runtime.NewPolicyEngine(),
		runtime.NewValidator(),
		runtime.NewAuditLogger(),
	)
}

type reviewApprovalLLM struct{}

func (reviewApprovalLLM) Chat(_ context.Context, _ string, messages []llm.Message) (string, error) {
	lastUser := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUser = messages[i].Content
			break
		}
	}
	if strings.Contains(lastUser, "shell_result:") {
		return `{"action":"final","reply":"已沉淀到正式知识页。","summary":"已沉淀到正式知识页。","artifacts":["wiki/knowledge/static-ip.md"],"output_files":["wiki/knowledge/static-ip.md"]}`, nil
	}
	targetPath := testLineValue(lastUser, "target_path: ")
	sourcePath := testLineValue(lastUser, "source_archive_path: ")
	answer := testSectionValue(lastUser, "confirmed_answer:")
	if targetPath == "" {
		targetPath = "wiki/knowledge/static-ip.md"
	}
	command := "mkdir -p " + shellQuote(filepath.ToSlash(filepath.Dir(targetPath))) + " && cat > " + shellQuote(targetPath) + " <<'EOF'\n" +
		"---\n" +
		"title: Static IP\n" +
		"type: product_knowledge\n" +
		"last_verified: 2026-05-07\n" +
		"source_pages:\n" +
		"  - " + sourcePath + "\n" +
		"---\n\n" +
		"# Static IP\n\n" +
		"## Summary\n\n" +
		answer + "\n" +
		"EOF\n"
	raw, err := json.Marshal(map[string]any{"action": "shell", "command": command, "reason": "沉淀人工审查知识"})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (m reviewApprovalLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := m.Chat(ctx, model, messages)
	if err != nil {
		return "", err
	}
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
}

func testLineValue(text string, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func testSectionValue(text string, heading string) string {
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

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func TestCustomerEvidencePriorityUsesFormalKnowledgeDirs(t *testing.T) {
	pages := []retrieval.RetrievedPage{
		{Path: "wiki/sources/raw-note.md", Score: 120},
		{Path: "wiki/intents/customer-router.md", Score: 100},
		{Path: "wiki/entities/product.md", Score: 90},
		{Path: "wiki/policies/refund.md", Score: 1},
		{Path: "wiki/knowledge/network-setup.md", Score: 0.1},
		{Path: "wiki/synthesis/summary.md", Score: 50},
	}
	got := prioritizeCustomerRetrievedPages(pages)
	if got[0].Path != "wiki/knowledge/network-setup.md" {
		t.Fatalf("expected knowledge page first, got %#v", got)
	}
	if got[1].Path != "wiki/policies/refund.md" {
		t.Fatalf("expected policy page second, got %#v", got)
	}
	if got[len(got)-2].Path != "wiki/intents/customer-router.md" || got[len(got)-1].Path != "wiki/sources/raw-note.md" {
		t.Fatalf("expected intents then non-customer source after formal evidence dirs, got %#v", got)
	}
	if isCustomerReadableEvidence("wiki/unconfirmed/draft.md") {
		t.Fatal("unconfirmed path must not be customer-readable evidence")
	}
	if isCustomerReadableEvidence("wiki/sources/raw-note.md") {
		t.Fatal("source archive path must not be customer-readable evidence")
	}
}

func TestReviewApproveWritesKnowledgeTargetAndSourceArchive(t *testing.T) {
	root := t.TempDir()
	deps := Deps{
		Config: &config.Config{
			MountedWiki: config.MountedWikiConfig{
				Root:     root,
				QMDIndex: "test-knowledge",
			},
		},
		LLM: reviewApprovalLLM{},
		Runtime: testRuntime(
			testRuntimeTool{name: "exec.shell", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
				command, _ := args["command"].(string)
				cmd := exec.CommandContext(ctx, "sh", "-c", command)
				cmd.Dir = env.WikiRoot
				output, err := cmd.CombinedOutput()
				if err != nil {
					return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskHigh, Data: map[string]any{"stdout": string(output), "exit_code": 1}, Error: &runtime.ToolError{Code: "EXEC_FAILED", Message: err.Error()}}, nil
				}
				return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskHigh, Data: map[string]any{"stdout": string(output), "exit_code": 0}}, nil
			}},
			testRuntimeTool{name: "exec.qmd"},
			testRuntimeTool{name: "wiki.update_index_entry"},
			testRuntimeTool{name: "wiki.append_log"},
		),
	}
	svc := NewReviewQueueService(deps)
	item, err := svc.CreatePending(context.Background(), ReviewCreateRequest{
		Question:            "怎么配置固定 IP？",
		DraftAnswer:         "在网络设置中按设备实际网段配置固定 IP。",
		SuggestedTargetPath: "wiki/knowledge/static-ip.md",
		MatchedPages:        []string{"wiki/knowledge/network-setup.md"},
		SessionID:           "session-1",
	})
	if err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	approved, err := svc.Approve(context.Background(), item.ID, ReviewApproveRequest{})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.SuggestedTargetPath != "wiki/knowledge/static-ip.md" {
		t.Fatalf("unexpected approved target: %s", approved.SuggestedTargetPath)
	}
	targetRaw, err := os.ReadFile(filepath.Join(root, "wiki", "knowledge", "static-ip.md"))
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	target := string(targetRaw)
	for _, want := range []string{"type: product_knowledge", "source_pages:", "在网络设置中按设备实际网段配置固定 IP。", "wiki/sources/" + item.ID + ".md"} {
		if !strings.Contains(target, want) {
			t.Fatalf("target missing %q:\n%s", want, target)
		}
	}
	sourceRaw, err := os.ReadFile(filepath.Join(root, "wiki", "sources", item.ID+".md"))
	if err != nil {
		t.Fatalf("read source archive: %v", err)
	}
	if source := string(sourceRaw); !strings.Contains(source, "source_kind: human-review") || !strings.Contains(source, "target_path: wiki/knowledge/static-ip.md") {
		t.Fatalf("unexpected source archive:\n%s", source)
	}
	if _, err := os.Stat(filepath.Join(root, "wiki", "unconfirmed", item.ID+".md")); !os.IsNotExist(err) {
		t.Fatalf("pending review should be removed, stat err=%v", err)
	}
}

func TestUploadPrepareAcceptsDocumentsAndRejectsStructuredData(t *testing.T) {
	svc := NewUploadService(Deps{
		Config: &config.Config{
			MountedWiki: config.MountedWikiConfig{Root: t.TempDir()},
			Upload:      config.UploadConfig{MaxTextFileKB: 64},
		},
	})
	prepared, err := svc.prepareUpload(context.Background(), UploadRequest{
		Filename: "产品知识.md",
		Content:  []byte("# 产品知识\n\n仅用于知识沉淀。"),
	})
	if err != nil {
		t.Fatalf("prepare markdown: %v", err)
	}
	if prepared.kind != "document" {
		t.Fatalf("expected document kind, got %s", prepared.kind)
	}
	if !strings.HasPrefix(prepared.storedRel, "raw/articles/") || !strings.HasSuffix(prepared.storedRel, ".md") {
		t.Fatalf("unexpected stored path: %s", prepared.storedRel)
	}
	for _, filename := range []string{"table.xlsx", "data.json", "rows.csv", "image.png", "no-extension"} {
		_, err := svc.prepareUpload(context.Background(), UploadRequest{
			Filename: filename,
			Content:  []byte("x"),
		})
		if err == nil {
			t.Fatalf("expected %s to be rejected", filename)
		}
		if _, ok := err.(ValidationError); !ok {
			t.Fatalf("expected ValidationError for %s, got %T: %v", filename, err, err)
		}
		if !strings.Contains(err.Error(), "只支持文档文件") || !strings.Contains(err.Error(), "不支持 Excel、CSV、TSV、JSON、图片") {
			t.Fatalf("unexpected validation message for %s: %v", filename, err)
		}
	}
}

func TestExtractDocumentTextSupportsDocxAndRTF(t *testing.T) {
	docxText, err := extractDocumentText(context.Background(), buildTestDocx(t, "产品知识", "支持静态 IP。"), ".docx")
	if err != nil {
		t.Fatalf("extract docx: %v", err)
	}
	if !strings.Contains(docxText, "产品知识") || !strings.Contains(docxText, "支持静态 IP。") {
		t.Fatalf("unexpected docx text: %q", docxText)
	}
	rtfText, err := extractDocumentText(context.Background(), []byte(`{\rtf1\ansi 产品知识\par 支持静态 IP。}`), ".rtf")
	if err != nil {
		t.Fatalf("extract rtf: %v", err)
	}
	if !strings.Contains(rtfText, "产品知识") || !strings.Contains(rtfText, "支持静态 IP。") {
		t.Fatalf("unexpected rtf text: %q", rtfText)
	}
}

func buildTestDocx(t *testing.T, lines ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zipper := zip.NewWriter(&buf)
	writer, err := zipper.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create docx xml: %v", err)
	}
	var xml strings.Builder
	xml.WriteString(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, line := range lines {
		xml.WriteString(`<w:p><w:r><w:t>`)
		xml.WriteString(line)
		xml.WriteString(`</w:t></w:r></w:p>`)
	}
	xml.WriteString(`</w:body></w:document>`)
	if _, err := writer.Write([]byte(xml.String())); err != nil {
		t.Fatalf("write docx xml: %v", err)
	}
	if err := zipper.Close(); err != nil {
		t.Fatalf("close docx zip: %v", err)
	}
	return buf.Bytes()
}
