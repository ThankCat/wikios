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
	intentPath := filepath.Join(t.TempDir(), "public_intents.yaml")
	if err := os.WriteFile(intentPath, []byte(defaultPublicIntentTestYAML()), 0o644); err != nil {
		t.Fatalf("write public intents: %v", err)
	}
	enabled := true
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{
			Root:     root,
			QMDIndex: "missing-index-for-test",
		},
		Retrieval:     config.RetrievalConfig{TopK: 3},
		Workspace:     config.WorkspaceConfig{BaseDir: t.TempDir()},
		Sandbox:       config.SandboxConfig{QMDTimeoutSec: 1},
		LLM:           config.LLMConfig{ModelPublic: "test"},
		PublicIntents: config.PublicIntentsConfig{Enabled: &enabled, Path: intentPath},
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
	publicIntents := service.NewPublicIntentManager(cfg.PublicIntents)
	svc := service.NewPublicQueryService(service.Deps{
		Config:        cfg,
		Runtime:       rt,
		LLM:           mock,
		Retriever:     retrieval.NewQMDRetriever(rt),
		Store:         dataStore,
		PublicIntents: publicIntents,
		PromptDir:     "../../internal/llm/prompts",
		WorkspaceDir:  cfg.Workspace.BaseDir,
	})
	return svc, mock
}

func defaultPublicIntentTestYAML() string {
	return `version: 1
fallbacks:
  generic: 您好，这个问题我这边暂时还不能准确确认，您可以补充一下具体场景，我再为您确认。
  operation: 您好，这方面我这边暂时没有可直接确认的操作说明，您可以补充一下具体场景，我再为您确认。
  device_operation: 您好，这项操作我这边暂时还不能准确确认，建议您先参考设备说明或联系对应支持人员处理。
rules:
  - name: admin_unsupported
    enabled: true
    priority: 100
    category: safety
    match:
      contains: [删除知识库, 删除资料库, 删库]
    response: 这个请求不属于对外客服问答范围。如需处理系统或资料管理操作，请联系管理员。
  - name: identity
    enabled: true
    priority: 80
    category: service_identity
    match:
      exact: [你是谁, 你能做什么]
      contains: [你是谁, 你能做什么]
    response: 您好，我是四叶天代理IP客服，主要为您解答动态IP、静态IP、套餐选择和使用相关问题。
`
}

func createPublicFixtureWiki(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWritePublicFixture(t, filepath.Join(root, "AGENT.md"), `# AGENT

## INGEST 操作规范

- 先规范化再摄入。

## QUERY 操作规范

- Step Q1：执行 qmd query。
- Step Q2：优先读取 wiki/faq 页面。

## LINT 操作规范

- 执行健康检查。
`)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/index.md"), "# index\n")
	mustWritePublicFixture(t, filepath.Join(root, "wiki/sources/rules.md"), `---
title: Knowledge Base Rules
---

## Summary

知识库系统规则用于约束摄入、命名和来源维护。
`)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/faq/customer-qa.md"), `---
title: Customer QA
type: faq
source_family: faq-dataset
---

## Summary

静态IP适合长期稳定网络环境。

## FAQ Entries

### 静态IP的使用场景是什么？

回复：
账号运营、白名单绑定和远程办公。
`)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/faq/wechat-login.md"), `---
title: 微信登录限制
type: faq
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

- [[customer-qa]]
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
  "sources": [{"path":"wiki/faq/customer-qa.md","confidence":"high"}],
  "confidence": 0.82,
  "notes": "基于命中来源生成"
}`)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "知识库系统规则是什么？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if resp.Answer == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestPublicAnswerInjectsOnlyMountedWikiQueryGuide(t *testing.T) {
	svc, mock := newPublicQueryTestService(t, `{
	  "answer_type": "text",
	  "answer_markdown": "知识库规则摘要",
  "sources": [{"path":"wiki/faq/customer-qa.md","confidence":"high"}],
  "confidence": 0.82,
  "notes": "基于命中来源生成"
}`)
	_, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "静态IP的使用场景是什么？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(mock.lastMessages) == 0 {
		t.Fatalf("expected llm messages to be captured")
	}
	if !strings.Contains(mock.lastMessages[0].Content, "## QUERY 操作规范") {
		t.Fatalf("expected mounted QUERY guide to be injected into system prompt")
	}
	for _, leakedSection := range []string{"## INGEST 操作规范", "## LINT 操作规范"} {
		if strings.Contains(mock.lastMessages[0].Content, leakedSection) {
			t.Fatalf("expected public prompt to omit %s, got %s", leakedSection, mock.lastMessages[0].Content)
		}
	}
}

func TestPublicAnswerIncludesConversationHistory(t *testing.T) {
	svc, mock := newPublicQueryTestService(t, `{
  "answer_type": "text",
  "answer_markdown": "知识库规则摘要",
  "sources": [{"path":"wiki/faq/customer-qa.md","confidence":"high"}],
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

func TestPublicAnswerHandlesIdentityQuestionWithoutKnowledgeGap(t *testing.T) {
	svc, _ := newPublicQueryTestService(t, "")
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "你是谁"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "四叶天代理IP客服") {
		t.Fatalf("expected branded customer-service identity, got %+v", resp)
	}
	if strings.Contains(resp.Answer, "资料") || strings.Contains(resp.Answer, "知识库") || strings.Contains(resp.Answer, "准确资料") {
		t.Fatalf("expected no internal knowledge-gap wording, got %+v", resp)
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

func TestPublicAnswerSanitizesKnowledgeBaseGapWording(t *testing.T) {
	svc, _ := newPublicQueryTestService(t, `{
  "answer_type": "text",
  "answer_markdown": "您是想了解如何用我们的代理 IP 配合 AI 工具使用吧？目前知识库里没有专门针对“如何使用AI”的详细说明。不过如果您是用在外网AI工具的代理切换、数据采集等场景，可以告诉我您具体用的哪个 AI 工具、做什么操作，我先帮您匹配对应的使用方式。",
  "sources": [{"path":"wiki/faq/customer-qa.md","confidence":"high"}],
  "confidence": 0.7,
  "notes": ""
}`)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "如何使用AI"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	for _, leaked := range []string{"知识库", "资料库", "检索结果", "没有专门针对"} {
		if strings.Contains(resp.Answer, leaked) {
			t.Fatalf("expected internal wording %q to be sanitized, got %+v", leaked, resp)
		}
	}
	if !strings.Contains(resp.Answer, "暂时还不能准确确认") {
		t.Fatalf("expected configured safe fallback, got %+v", resp)
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
  "sources": [{"path":"wiki/faq/siyetian-proxy-ip-faq.md","confidence":"high"}],
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
  "sources": [{"path":"wiki/faq/customer-qa.md","confidence":"high"}],
  "confidence": 0.9,
  "notes": "基于静态IP概念页和其关联来源整理。"
}`)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "静态IP的使用场景是什么？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "账号运营") {
		t.Fatalf("expected FAQ-backed static IP answer, got %+v", resp)
	}
}

func TestPublicAnswerStripsRoboticServicePreamble(t *testing.T) {
	svc, _ := newPublicQueryTestService(t, `{
  "answer_type": "text",
  "answer_markdown": "您好，根据我们的服务说明，不可以用于微信登录业务。",
  "sources": [{"path":"wiki/faq/wechat-login.md","confidence":"high"}],
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

func TestPublicPromptDoesNotCarryFixedPreIntentFallback(t *testing.T) {
	raw, err := os.ReadFile("../../internal/llm/prompts/public_answer_system.md")
	if err != nil {
		t.Fatalf("read public prompt: %v", err)
	}
	text := string(raw)
	for _, banned := range []string{
		"您好，这个问题我这边暂时没有准确资料",
		"删除知识库、修改系统、管理页面",
		"面向终端客户回答时，必须额外遵守以下风格规则",
	} {
		if strings.Contains(text, banned) {
			t.Fatalf("public prompt still contains migrated pre-intent wording %q", banned)
		}
	}
	if !strings.Contains(text, "wiki/faq/xxx.md") {
		t.Fatalf("expected FAQ evidence output rule to remain")
	}
}
