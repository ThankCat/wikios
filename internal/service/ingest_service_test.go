package service_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/llm"
	"wikios/internal/service"
)

type timeoutLLM struct{}

func (timeoutLLM) Chat(_ context.Context, _ string, _ []llm.Message) (string, error) {
	return "", fmt.Errorf("context deadline exceeded (Client.Timeout or context cancellation while reading body)")
}

func (timeoutLLM) StreamChat(_ context.Context, _ string, _ []llm.Message, _ func(string)) (string, error) {
	return "", fmt.Errorf("context deadline exceeded (Client.Timeout or context cancellation while reading body)")
}

func TestStructuredFAQIngestFallsBackWhenLLMTimesOut(t *testing.T) {
	deps := testServiceDeps(t, timeoutLLM{})
	mustWriteService(t, filepath.Join(deps.Config.MountedWiki.Root, "raw/articles/faq.json"), `{
  "types": [{"id": "type-1", "category": "账号与登录"}],
  "faq": [
    {"id": "faq-1", "question": "你们的IP能访问微信不", "answer": "<p>不可以用于微信登录业务。</p>", "type_id": "type-1"},
    {"id": "faq-2", "question": "登录不了怎么办", "answer": "<p>请先自助检测代理，再联系客服。</p>", "type_id": "type-1"}
  ]
}`)
	mustWriteService(t, filepath.Join(deps.Config.MountedWiki.Root, "wiki/templates/source-template.md"), "## Summary\n\n## Key Points\n\n## FAQ Entries\n\n## Concepts Extracted\n\n## Entities Extracted\n\n## Contradictions\n\n## My Notes\n")

	svc := service.NewIngestService(deps)
	result, err := svc.Run(context.Background(), service.NewExecution("ingest"), "trace-test", service.IngestRequest{
		InputType: "file",
		Path:      "raw/articles/faq.json",
	})
	if err != nil {
		t.Fatalf("structured faq ingest should fall back instead of failing: %v", err)
	}
	sourcePages, _ := result["source_pages"].([]string)
	if len(sourcePages) == 0 {
		t.Fatalf("expected source pages, got %#v", result)
	}
	warnings, _ := result["warnings"].([]string)
	joinedWarnings := strings.Join(warnings, "\n")
	if !strings.Contains(joinedWarnings, "已回退到本地规则分析") {
		t.Fatalf("expected fallback warning, got %#v", warnings)
	}
	content, err := os.ReadFile(filepath.Join(deps.Config.MountedWiki.Root, filepath.FromSlash(sourcePages[0])))
	if err != nil {
		t.Fatalf("read generated source page: %v", err)
	}
	if !strings.Contains(string(content), "## FAQ Entries") {
		t.Fatalf("expected faq entries section, got:\n%s", string(content))
	}
}
