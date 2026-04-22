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

type correctionLLM struct{}

func (correctionLLM) Chat(_ context.Context, _ string, messages []llm.Message) (string, error) {
	if len(messages) > 0 && strings.Contains(messages[0].Content, "原始来源核验模式") {
		return `{
  "summary": "检测到 raw 可验证的品牌名纠错",
  "corrections": [
    {
      "path": "wiki/entities/siyetian.md",
      "section": "frontmatter",
      "wrong": "title: 思叶天",
      "correct": "title: 四叶天",
      "reason": "raw 原文只出现四叶天",
      "risk_level": "low",
      "replace_mode": "global",
      "scope_paths": ["wiki/entities", "wiki/sources"]
    }
  ],
  "warnings": []
}`, nil
	}
	return `{"patterns":["品牌名称不一致"],"gaps":[],"contradictions":[],"low_risk_fixes":[],"proposals":[],"output_files":[]}`, nil
}

func (c correctionLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := c.Chat(ctx, model, messages)
	if err != nil {
		return "", err
	}
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
}

func TestAutoDetectAppliesBackedCorrection(t *testing.T) {
	deps := testServiceDeps(t, correctionLLM{})
	svc := service.NewRepairService(deps)

	result, err := svc.AutoDetect(context.Background(), service.NewExecution("repair"), "trace-test", service.AutoRepairRequest{
		Topic: "四叶天 名称写错",
		Apply: true,
	})
	if err != nil {
		t.Fatalf("auto detect: %v", err)
	}
	updated, err := os.ReadFile(filepath.Join(deps.Config.MountedWiki.Root, "wiki/entities/siyetian.md"))
	if err != nil {
		t.Fatalf("read entity: %v", err)
	}
	if !strings.Contains(string(updated), "title: 四叶天") {
		t.Fatalf("expected corrected title, got:\n%s", updated)
	}
	applied, _ := result["applied_fixes"].([]string)
	if len(applied) == 0 {
		t.Fatalf("expected applied fixes, got %#v", result)
	}
}

func TestReflectCanAutoFixLowRisk(t *testing.T) {
	deps := testServiceDeps(t, correctionLLM{})
	svc := service.NewReflectService(deps)

	result, err := svc.Run(context.Background(), service.NewExecution("reflect"), "trace-test", service.ReflectRequest{
		Topic:          "四叶天 名称写错",
		WriteReport:    false,
		AutoFixLowRisk: true,
	})
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	applied, _ := result["applied_fixes"].([]string)
	if len(applied) == 0 {
		t.Fatalf("expected reflect to apply corrections, got %#v", result)
	}
}

func testServiceDeps(t *testing.T, client llm.Client) service.Deps {
	t.Helper()
	root := t.TempDir()
	mustWriteService(t, filepath.Join(root, "AGENT.md"), "# test agent\n")
	mustWriteService(t, filepath.Join(root, "raw/articles/customer.md"), "# 四叶天客服知识库\n四叶天提供静态IP服务。\n")
	mustWriteService(t, filepath.Join(root, "wiki/sources/siyetian-customer-faq.md"), `---
title: 思叶天客服知识库
raw_file: raw/articles/customer.md
---

## Summary

思叶天提供静态IP服务。
`)
	mustWriteService(t, filepath.Join(root, "wiki/entities/siyetian.md"), `---
title: 思叶天
aliases:
  - 思叶天
type: entity
---

## Description

思叶天是一家服务商。
`)
	mustWriteService(t, filepath.Join(root, "wiki/index.md"), "# index\n- [[sources/siyetian-customer-faq]]\n")
	mustWriteService(t, filepath.Join(root, "wiki/log.md"), "# log\n")
	mustWriteService(t, filepath.Join(root, "wiki/templates/source-template.md"), "## Summary\n\n")
	mustWriteService(t, filepath.Join(root, "wiki/templates/concept-template.md"), "## Definition\n\n## Key Points\n\n## Contradictions\n\n## Sources\n\n## Evolution Log\n")
	mustWriteService(t, filepath.Join(root, "wiki/templates/entity-template.md"), "## Description\n\n## Key Contributions\n\n## Sources\n")
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{
			Root:     root,
			QMDIndex: "knowledge-base",
		},
		Retrieval: config.RetrievalConfig{TopK: 5},
		Workspace: config.WorkspaceConfig{BaseDir: filepath.Join(root, ".workspace"), DefaultTimeoutSec: 5},
		Sandbox:   config.SandboxConfig{QMDTimeoutSec: 5, PythonTimeoutSec: 5},
		LLM:       config.LLMConfig{ModelAdmin: "test", ModelPublic: "test"},
		Sync:      config.SyncConfig{Remote: "origin", Branch: "main"},
	}
	if err := os.MkdirAll(cfg.Workspace.BaseDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	dataStore, err := store.Open(filepath.Join(root, ".workspace", "service.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root),
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	return service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          client,
		Retriever:    retrieval.NewQMDRetriever(rt),
		Store:        dataStore,
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	}
}

func mustWriteService(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
