package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/runtime"
	"wikios/internal/tools"
	"wikios/internal/wikiadapter"
)

type sequenceLLM struct {
	responses []string
	index     int
}

func (m *sequenceLLM) Chat(_ context.Context, _ string, _ []llm.Message) (string, error) {
	if m.index >= len(m.responses) {
		return `{"action":"final","reply":"done","summary":"done"}`, nil
	}
	resp := m.responses[m.index]
	m.index++
	return resp, nil
}

func (m *sequenceLLM) StreamChat(_ context.Context, _ string, _ []llm.Message, onDelta func(string)) (string, error) {
	resp, err := m.Chat(context.Background(), "", nil)
	if err == nil && onDelta != nil {
		onDelta(resp)
	}
	return resp, err
}

func TestDirectAdminServiceRunsShell(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{Root: root},
		Workspace:   config.WorkspaceConfig{BaseDir: t.TempDir(), DefaultTimeoutSec: 5},
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(root),
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	svc := NewDirectAdminService(Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          &sequenceLLM{responses: []string{`{"action":"shell","command":"pwd","reason":"确认目录"}`, `{"action":"final","reply":"已确认目录","summary":"完成"}`}},
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	})
	execution := NewExecution("direct")
	result, err := svc.Run(context.Background(), execution, "trace-test", DirectAdminRequest{Message: "确认当前目录"})
	if err != nil {
		t.Fatalf("run direct admin: %v", err)
	}
	if directStringValue(result, "reply") != "已确认目录" {
		t.Fatalf("unexpected reply: %+v", result)
	}
	commands, ok := result["commands"].([]map[string]any)
	if !ok || len(commands) != 1 {
		t.Fatalf("expected one shell command, got %+v", result["commands"])
	}
	if directStringValue(commands[0], "command") != "pwd" {
		t.Fatalf("unexpected command record: %+v", commands[0])
	}
}

func TestDirectShellResultPromptIncludesToolErrors(t *testing.T) {
	prompt := directShellResultPrompt(map[string]any{
		"command":       "pwd",
		"cwd":           "/data/wiki-repo",
		"shell":         "/bin/bash",
		"tool_success":  false,
		"exit_code":     -1,
		"error_code":    "EXEC_FAILED",
		"error_message": "exec: zsh: executable file not found in $PATH",
		"error":         "exec.shell: exec: zsh: executable file not found in $PATH",
	})
	for _, want := range []string{
		"tool_success: false",
		"error_code: EXEC_FAILED",
		"error_message: exec: zsh: executable file not found in $PATH",
		"error: exec.shell: exec: zsh: executable file not found in $PATH",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to include %q, got %q", want, prompt)
		}
	}
}

func TestDirectAdminServiceSalvagesMalformedFinalJSON(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{Root: root},
		Workspace:   config.WorkspaceConfig{BaseDir: t.TempDir(), DefaultTimeoutSec: 5},
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(root),
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	svc := NewDirectAdminService(Deps{
		Config:  cfg,
		Runtime: rt,
		LLM: &sequenceLLM{responses: []string{
			"{\"action\":\"final\",\"reply\":\"清理操作已完成。\n\n已修复 index.md 中的重复链接。\",\"summary\":\"清理完成\"}",
		}},
		PromptDir:    "../../internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	})
	execution := NewExecution("direct")
	result, err := svc.Run(context.Background(), execution, "trace-test", DirectAdminRequest{Message: "清理 index"})
	if err != nil {
		t.Fatalf("run malformed final: %v", err)
	}
	if strings.Contains(directStringValue(result, "reply"), `"action":"final"`) {
		t.Fatalf("expected salvaged reply instead of raw json: %+v", result)
	}
	if !strings.Contains(directStringValue(result, "reply"), "已修复 index.md") {
		t.Fatalf("expected extracted reply body: %+v", result)
	}
}

func TestDirectAdminWikiModeGuideUsesMountedAgentSections(t *testing.T) {
	root := t.TempDir()
	agent := `# AGENT

## 系统概述

system rules

## INGEST 操作规范

ingest rules

## QUERY 操作规范

query rules

## REPAIR 操作规范

repair rules

## SYNC 操作规范

sync rules

## Wikilink 使用规范

wikilink rules

## 报告命名规范

report rules
`
	if err := os.WriteFile(filepath.Join(root, "AGENT.md"), []byte(agent), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	svc := NewDirectAdminService(Deps{
		Config: &config.Config{MountedWiki: config.MountedWikiConfig{Root: root}},
	})

	ingestGuide := svc.directAdminWikiModeGuide("ingest")
	if !strings.Contains(ingestGuide, "ingest rules") || !strings.Contains(ingestGuide, "wikilink rules") {
		t.Fatalf("expected ingest guide to include mounted wiki ingest sections, got %q", ingestGuide)
	}
	if strings.Contains(ingestGuide, "query rules") || strings.Contains(ingestGuide, "repair rules") {
		t.Fatalf("expected ingest guide to exclude unrelated sections, got %q", ingestGuide)
	}

	repairGuide := svc.directAdminWikiModeGuide("repair")
	if !strings.Contains(repairGuide, "repair rules") || !strings.Contains(repairGuide, "report rules") {
		t.Fatalf("expected repair guide to include mounted wiki repair sections, got %q", repairGuide)
	}

	syncGuide := svc.directAdminWikiModeGuide("sync")
	if !strings.Contains(syncGuide, "sync rules") || !strings.Contains(syncGuide, "report rules") {
		t.Fatalf("expected sync guide to include mounted wiki sync sections, got %q", syncGuide)
	}
	if strings.Contains(syncGuide, "repair rules") || strings.Contains(syncGuide, "ingest rules") {
		t.Fatalf("expected sync guide to exclude unrelated sections, got %q", syncGuide)
	}
}

func TestPromptInjectionStatesAgentPrecedence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENT.md"), []byte(`# AGENT

## 系统概述

system rules

## LINT 操作规范

lint rules
`), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	promptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptDir, "test.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	svc := NewDirectAdminService(Deps{
		Config: &config.Config{
			MountedWiki: config.MountedWikiConfig{Root: root, QMDIndex: "test"},
			LLM:         config.LLMConfig{ModelAdmin: "test"},
		},
		PromptDir: promptDir,
	})
	merged, err := svc.loadPromptWithWikiAgent("test.md")
	if err != nil {
		t.Fatalf("load prompt: %v", err)
	}
	if !strings.Contains(merged, "Wiki 治理规则最高优先级") || !strings.Contains(merged, "system rules") || strings.Contains(merged, "lint rules") {
		t.Fatalf("expected scoped AGENT section wording without full AGENT injection, got %q", merged)
	}
}
