package service_test

import (
	"context"
	"strings"
	"testing"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/service"
	"wikios/internal/task"
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

func TestPublicAnswerUsesKnowledgeBase(t *testing.T) {
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
	store, err := task.OpenStore(t.TempDir() + "/service.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root),
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	svc := service.NewPublicQueryService(service.Deps{
		Config:  cfg,
		Runtime: rt,
		LLM: &mockLLM{answer: `{
  "answer_type": "text",
  "answer_markdown": "知识库规则摘要",
  "sources": [{"path":"wiki/sources/rules.md","confidence":"medium"}],
  "confidence": 0.82,
  "notes": "基于命中来源生成"
}`},
		Retriever:    retrieval.NewQMDRetriever(rt),
		TaskStore:    store,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	})
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "知识库系统规则是什么？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if resp.AnswerMarkdown == "" || resp.AnswerType != "text" || len(resp.Sources) == 0 || resp.Notes == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestPublicAnswerInjectsMountedWikiAgent(t *testing.T) {
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
	store, err := task.OpenStore(t.TempDir() + "/service.db")
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
  "answer_markdown": "知识库规则摘要",
  "sources": [{"path":"wiki/sources/rules.md","confidence":"medium"}],
  "confidence": 0.82,
  "notes": "基于命中来源生成"
}`}
	svc := service.NewPublicQueryService(service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          mock,
		Retriever:    retrieval.NewQMDRetriever(rt),
		TaskStore:    store,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	})
	_, err = svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "知识库系统规则是什么？"})
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
