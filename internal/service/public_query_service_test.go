package service_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/service"
	"wikios/internal/store"
	"wikios/internal/tools"
	"wikios/internal/wikiadapter"
)

type mockLLM struct {
	answer       string
	lastMessages []llm.Message
}

func (m *mockLLM) Chat(_ context.Context, _ string, messages []llm.Message) (string, error) {
	m.lastMessages = messages
	return m.answer, nil
}

func (m *mockLLM) StreamChat(_ context.Context, _ string, messages []llm.Message, onDelta func(string)) (string, error) {
	m.lastMessages = messages
	if onDelta != nil && m.answer != "" {
		onDelta(m.answer)
	}
	return m.answer, nil
}

func newPublicQueryTestService(t *testing.T, answer string) (*service.PublicQueryService, *mockLLM) {
	t.Helper()
	root := createPublicFixtureWiki(t)
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{
			Root:     root,
			QMDIndex: "missing-index-for-test",
		},
		Retrieval: config.RetrievalConfig{TopK: 3},
		Workspace: config.WorkspaceConfig{BaseDir: t.TempDir()},
		Sandbox:   config.SandboxConfig{QMDTimeoutSec: 1},
		LLM:       config.LLMConfig{ModelPublic: "test"},
	}
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root),
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	mock := &mockLLM{answer: answer}
	svc := service.NewPublicQueryService(service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          mock,
		Retriever:    retrieval.NewQMDRetriever(rt),
		Store:        dataStore,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	})
	return svc, mock
}

func createPublicFixtureWiki(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWritePublicFixture(t, filepath.Join(root, "AGENT.md"), "# AGENT\n\n## INGEST 操作规范\n\n- 先规范化再摄入。\n")
	mustWritePublicFixture(t, filepath.Join(root, "wiki/index.md"), "# index\n")
	mustWritePublicFixture(t, filepath.Join(root, "wiki/sources/rules.md"), `---
title: Knowledge Base Rules
---

## Summary

知识库系统规则用于约束摄入、命名和来源维护。
`)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/sources/customer-qa.md"), `---
title: Customer QA
source_family: faq-dataset
---

## Summary

静态IP适合长期稳定网络环境。

## FAQ Entries

### 静态IP的使用场景是什么？

回复：
账号运营、白名单绑定和远程办公。
`)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/sources/wechat-login.md"), `---
title: 微信登录限制
source_family: faq-dataset
---

## Summary

本页说明微信登录相关限制。

## FAQ Entries

### 你们的IP能访问微信不

回复：
不可以用于微信登录业务。
`)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/concepts/static-ip.md"), `---
title: 静态IP
---

## Definition

静态IP是固定不变的 IP 地址。

## Key Points

- 适合长期稳定网络环境。

## Sources

- [[sources/customer-qa]]
`)
	return root
}

func mustWritePublicFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestPublicAnswerUsesKnowledgeBase(t *testing.T) {
	svc, _ := newPublicQueryTestService(t, `{
  "answer_type": "text",
  "answer_markdown": "知识库规则摘要",
  "sources": [{"path":"wiki/sources/rules.md","confidence":"medium"}],
  "confidence": 0.82,
  "notes": "基于命中来源生成"
}`)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "知识库系统规则是什么？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if resp.Answer == "" || resp.Details == nil || resp.Details.AnswerType != "text" || len(resp.Details.Sources) == 0 || resp.Details.Notes == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestPublicAnswerInjectsMountedWikiAgent(t *testing.T) {
	svc, mock := newPublicQueryTestService(t, `{
  "answer_type": "text",
  "answer_markdown": "知识库规则摘要",
  "sources": [{"path":"wiki/sources/rules.md","confidence":"medium"}],
  "confidence": 0.82,
  "notes": "基于命中来源生成"
}`)
	_, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "知识库系统规则是什么？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(mock.lastMessages) == 0 {
		t.Fatalf("expected llm messages to be captured")
	}
	if !strings.Contains(mock.lastMessages[0].Content, "## INGEST 操作规范") {
		t.Fatalf("expected mounted AGENT.md content to be injected into system prompt")
	}
}

func TestPublicAnswerIncludesConversationHistory(t *testing.T) {
	svc, mock := newPublicQueryTestService(t, `{
  "answer_type": "text",
  "answer_markdown": "知识库规则摘要",
  "sources": [{"path":"wiki/sources/rules.md","confidence":"medium"}],
  "confidence": 0.82,
  "notes": "基于命中来源生成"
}`)
	_, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{
		Question: "那它适合什么场景？",
		History: []service.ChatMessage{
			{Role: "user", Content: "静态IP是什么？"},
			{Role: "assistant", Content: "静态IP是固定不变的 IP 地址。"},
		},
	})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(mock.lastMessages) < 2 || !strings.Contains(mock.lastMessages[1].Content, "静态IP是什么？") {
		t.Fatalf("expected conversation history in user prompt, got %+v", mock.lastMessages)
	}
}

func TestPublicAnswerBlocksAdminLikeDeleteRequest(t *testing.T) {
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{
			Root:     "/Users/chenhao/Project/knowledge-base",
			QMDIndex: "zy-knowledge-base",
		},
		Retrieval: config.RetrievalConfig{TopK: 3},
		Workspace: config.WorkspaceConfig{BaseDir: t.TempDir()},
		Sandbox:   config.SandboxConfig{QMDTimeoutSec: 30},
		LLM:       config.LLMConfig{ModelPublic: "test"},
	}
	dataStore, err := store.Open(t.TempDir() + "/service.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root),
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	mock := &mockLLM{answer: `{
  "answer_type": "text",
  "answer_markdown": "当前资料库中仅包含一个来源页面（siyetian-proxy-ip-faq）和一个系统索引页（index.md），以及一个历史检查报告。请问您希望删除整个资料库（包含所有文件），还是仅删除特定页面？如果是特定页面，请提供页面名称或 slug。",
  "sources": [{"path":"wiki/index.md","confidence":"high"}],
  "confidence": 1,
  "notes": "用户请求删除资料库，但知识库当前内容较少，需要进一步明确操作范围。"
}`}
	svc := service.NewPublicQueryService(service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          mock,
		Retriever:    retrieval.NewQMDRetriever(rt),
		Store:        dataStore,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	})
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "请帮我删除资料库"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if strings.Contains(resp.Answer, "wiki/index.md") || strings.Contains(resp.Answer, "slug") {
		t.Fatalf("expected sanitized public response, got %+v", resp)
	}
	if !strings.Contains(resp.Answer, "联系管理员") {
		t.Fatalf("expected admin handoff response, got %+v", resp)
	}
}

func TestPublicAnswerSanitizesKnowledgeGapTalk(t *testing.T) {
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{
			Root:     "/Users/chenhao/Project/knowledge-base",
			QMDIndex: "zy-knowledge-base",
		},
		Retrieval: config.RetrievalConfig{TopK: 3},
		Workspace: config.WorkspaceConfig{BaseDir: t.TempDir()},
		Sandbox:   config.SandboxConfig{QMDTimeoutSec: 30},
		LLM:       config.LLMConfig{ModelPublic: "test"},
	}
	dataStore, err := store.Open(t.TempDir() + "/service.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root),
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	mock := &mockLLM{answer: `{
  "answer_type": "text",
  "answer_markdown": "请问您是想了解关于关机的具体操作指南、常见问题，还是其他相关事项？由于当前知识库中尚未收录关于“关机”的相关内容，我暂时无法提供准确的信息。建议您联系管理员或参考设备自带的用户手册。",
  "sources": [{"path":"wiki/index.md","confidence":"high"}],
  "confidence": 1,
  "notes": "知识库暂无相关内容"
}`}
	svc := service.NewPublicQueryService(service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          mock,
		Retriever:    retrieval.NewQMDRetriever(rt),
		Store:        dataStore,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	})
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "关机"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if strings.Contains(resp.Answer, "知识库") || strings.Contains(resp.Answer, "管理员") || strings.Contains(resp.Answer, "请问您是想") {
		t.Fatalf("expected customer-safe fallback, got %+v", resp)
	}
	if !strings.Contains(resp.Answer, "说明书") && !strings.Contains(resp.Answer, "支持人员") {
		t.Fatalf("expected helpful fallback, got %+v", resp)
	}
}

func TestPublicAnswerRejectsNeutralVendorTone(t *testing.T) {
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{
			Root:     "/Users/chenhao/Project/knowledge-base",
			QMDIndex: "zy-knowledge-base",
		},
		Retrieval: config.RetrievalConfig{TopK: 3},
		Workspace: config.WorkspaceConfig{BaseDir: t.TempDir()},
		Sandbox:   config.SandboxConfig{QMDTimeoutSec: 30},
		LLM:       config.LLMConfig{ModelPublic: "test"},
	}
	dataStore, err := store.Open(t.TempDir() + "/service.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root),
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	mock := &mockLLM{answer: `{
  "answer_type": "text",
  "answer_markdown": "您好，关于查看海外IP的使用情况和连通状态，根据我们客服知识库的信息，通常有以下几种方式：通过服务商提供的管理后台查看，或者使用 ping、traceroute 等工具检测。",
  "sources": [{"path":"wiki/sources/siyetian-proxy-ip-faq.md","confidence":"medium"}],
  "confidence": 0.7,
  "notes": ""
}`}
	svc := service.NewPublicQueryService(service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          mock,
		Retriever:    retrieval.NewQMDRetriever(rt),
		Store:        dataStore,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	})
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "怎么查看海外IP的使用情况和连通状态"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if strings.Contains(resp.Answer, "服务商") || strings.Contains(resp.Answer, "客服知识库") || strings.Contains(resp.Answer, "通常有以下几种方式") {
		t.Fatalf("expected branded fallback, got %+v", resp)
	}
	if !strings.Contains(resp.Answer, "您好") {
		t.Fatalf("expected customer service tone, got %+v", resp)
	}
}

func TestPublicAnswerUsesSourceBackedStaticIPAnswer(t *testing.T) {
	svc, _ := newPublicQueryTestService(t, `{
  "answer_type": "list",
  "answer_markdown": "您好，静态IP适合长期固定网络环境的场景，比如账号运营、白名单绑定和远程办公。",
  "sources": [{"path":"wiki/concepts/static-ip.md","confidence":"medium"}],
  "confidence": 0.9,
  "notes": "基于静态IP概念页和其关联来源整理。"
}`)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "静态IP的使用场景是什么？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "账号运营") {
		t.Fatalf("expected source-backed static IP answer, got %+v", resp)
	}
	if resp.Details == nil || len(resp.Details.Sources) == 0 {
		t.Fatalf("expected merged sources, got %+v", resp)
	}
	foundSource := false
	for _, source := range resp.Details.Sources {
		if strings.HasPrefix(source.Path, "wiki/sources/") {
			foundSource = true
			break
		}
	}
	if !foundSource {
		t.Fatalf("expected related source page to be backfilled, got %+v", resp.Details.Sources)
	}
}

func TestPublicAnswerStripsRoboticServicePreamble(t *testing.T) {
	svc, _ := newPublicQueryTestService(t, `{
  "answer_type": "text",
  "answer_markdown": "您好，根据我们的服务说明，不可以用于微信登录业务。",
  "sources": [{"path":"wiki/sources/wechat-login.md","confidence":"high"}],
  "confidence": 0.92,
  "notes": ""
}`)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "你们的IP能访问微信不"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if strings.Contains(resp.Answer, "根据我们的服务说明") {
		t.Fatalf("expected robotic preamble to be stripped, got %+v", resp)
	}
	if !strings.Contains(resp.Answer, "不可以用于微信登录业务") {
		t.Fatalf("expected core answer to remain, got %+v", resp)
	}
}
