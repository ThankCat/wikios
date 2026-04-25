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

type faqClassifyLLM struct{}

func (faqClassifyLLM) Chat(_ context.Context, _ string, _ []llm.Message) (string, error) {
	return `{
  "categories": [{
    "title": "账号登录 FAQ",
    "slug": "faq-account-login",
    "category": "账号与登录",
    "summary": "账号登录相关客服问答。",
    "key_points": ["说明微信登录限制"],
    "entry_ids": ["faq-1"],
    "concepts_affected": ["微信登录限制"],
    "entities_affected": ["微信"],
    "concepts": [{
      "title": "微信登录限制",
      "slug": "wechat-login-limit",
      "english_name": "WeChat Login Limit",
      "aliases": ["微信登录限制", "WeChat Login Limit"],
      "definition": "微信登录限制说明代理 IP 不可用于微信登录业务。",
      "key_points": ["不可用于微信登录业务"],
      "contradictions": []
    }],
    "entities": [{
      "title": "微信",
      "slug": "wechat",
      "entity_type": "product",
      "aliases": ["微信", "WeChat"],
      "description": "微信是 FAQ 中被明确提到的第三方产品。",
      "key_contributions": ["登录限制场景"]
    }],
    "warnings": []
  }],
  "warnings": []
	}`, nil
}

func (faqClassifyLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := faqClassifyLLM{}.Chat(ctx, model, messages)
	if onDelta != nil && text != "" {
		onDelta(text)
	}
	return text, err
}

func TestStructuredFAQIngestFallsBackWhenLLMTimesOut(t *testing.T) {
	deps := testServiceDeps(t, timeoutLLM{})
	profilePath := filepath.Join(deps.Config.MountedWiki.Root, "profile.yaml")
	mustWriteService(t, profilePath, `
name: test
faq_taxonomy:
  category_hints:
    - title: 账号登录
      slug: faq-account-login
      aliases: [账号与登录]
      keywords: [登录, 微信]
knowledge_seeds:
  concepts:
    - title: 微信登录限制
      slug: wechat-login-limit
      aliases: [微信, 登录]
      definition: 微信登录限制说明相关业务边界。
  entities:
    - title: 微信
      slug: wechat
      entity_type: product
      aliases: [微信]
      description: 微信是第三方产品。
`)
	deps.Config.KnowledgeProfile.Path = profilePath
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
	faqPages, _ := result["faq_pages"].([]string)
	if len(faqPages) == 0 {
		t.Fatalf("expected FAQ pages, got %#v", result)
	}
	warnings, _ := result["warnings"].([]string)
	joinedWarnings := strings.Join(warnings, "\n")
	if !strings.Contains(joinedWarnings, "已回退到 server profile") {
		t.Fatalf("expected fallback warning, got %#v", warnings)
	}
	content, err := os.ReadFile(filepath.Join(deps.Config.MountedWiki.Root, filepath.FromSlash(faqPages[0])))
	if err != nil {
		t.Fatalf("read generated FAQ page: %v", err)
	}
	if !strings.Contains(string(content), "## FAQ Entries") {
		t.Fatalf("expected faq entries section, got:\n%s", string(content))
	}
	conceptContent, err := os.ReadFile(filepath.Join(deps.Config.MountedWiki.Root, "wiki/concepts/wechat-login-limit.md"))
	if err != nil {
		t.Fatalf("expected profile concept page: %v", err)
	}
	if !strings.Contains(string(conceptContent), "[[faq-account-login]]") {
		t.Fatalf("expected fallback concept backlink to FAQ page, got:\n%s", string(conceptContent))
	}
	entityContent, err := os.ReadFile(filepath.Join(deps.Config.MountedWiki.Root, "wiki/entities/wechat.md"))
	if err != nil {
		t.Fatalf("expected fallback entity page: %v", err)
	}
	if !strings.Contains(string(entityContent), "[[faq-account-login]]") {
		t.Fatalf("expected fallback entity backlink to FAQ page, got:\n%s", string(entityContent))
	}
}

func TestStructuredFAQIngestWritesSourceArchiveCategoryAndBacklinks(t *testing.T) {
	deps := testServiceDeps(t, faqClassifyLLM{})
	mustWriteService(t, filepath.Join(deps.Config.MountedWiki.Root, "raw/articles/faq.json"), `{
  "types": [{"id": "type-1", "category": "账号与登录"}],
  "faq": [
    {"id": "faq-1", "question": "你们的IP能访问微信不", "answer": "<p>不可以用于微信登录业务。</p>", "type_id": "type-1"}
  ]
}`)

	svc := service.NewIngestService(deps)
	result, err := svc.Run(context.Background(), service.NewExecution("ingest"), "trace-test", service.IngestRequest{
		InputType: "file",
		Path:      "raw/articles/faq.json",
	})
	if err != nil {
		t.Fatalf("structured faq ingest: %v", err)
	}
	sourcePages, _ := result["source_pages"].([]string)
	if len(sourcePages) != 1 || !strings.HasPrefix(sourcePages[0], "wiki/sources/") {
		t.Fatalf("expected one source archive page, got %#v", result["source_pages"])
	}
	faqPages, _ := result["faq_pages"].([]string)
	if len(faqPages) != 1 || faqPages[0] != "wiki/faq/faq-account-login.md" {
		t.Fatalf("expected classified FAQ page, got %#v", faqPages)
	}
	faqContent, err := os.ReadFile(filepath.Join(deps.Config.MountedWiki.Root, "wiki/faq/faq-account-login.md"))
	if err != nil {
		t.Fatalf("read faq page: %v", err)
	}
	if !strings.Contains(string(faqContent), "[[wechat-login-limit]]") || !strings.Contains(string(faqContent), "[[wechat]]") {
		t.Fatalf("expected FAQ page to link concepts/entities, got:\n%s", string(faqContent))
	}
	conceptContent, err := os.ReadFile(filepath.Join(deps.Config.MountedWiki.Root, "wiki/concepts/wechat-login-limit.md"))
	if err != nil {
		t.Fatalf("read concept page: %v", err)
	}
	if !strings.Contains(string(conceptContent), "[[faq-account-login]]") {
		t.Fatalf("expected concept backlink to FAQ page, got:\n%s", string(conceptContent))
	}
	entityContent, err := os.ReadFile(filepath.Join(deps.Config.MountedWiki.Root, "wiki/entities/wechat.md"))
	if err != nil {
		t.Fatalf("read entity page: %v", err)
	}
	if !strings.Contains(string(entityContent), "[[faq-account-login]]") {
		t.Fatalf("expected entity backlink to FAQ page, got:\n%s", string(entityContent))
	}
}
