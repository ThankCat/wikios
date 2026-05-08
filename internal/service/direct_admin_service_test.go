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

func TestDirectAdminWikiModeGuideUsesMountedAgentFullText(t *testing.T) {
	root := t.TempDir()
	agent := `# AGENT

## 定位

system rules

## INGEST

ingest rules

## QUERY

query rules

## LINT / REPAIR / REFLECT / MERGE

repair rules

## SYNC

sync rules

## Wikilink

wikilink rules

## Outputs

report rules
`
	if err := os.WriteFile(filepath.Join(root, "AGENT.md"), []byte(agent), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	svc := NewDirectAdminService(Deps{
		Config: &config.Config{MountedWiki: config.MountedWikiConfig{Root: root}},
	})

	ingestGuide := svc.directAdminWikiModeGuide("ingest")
	for _, want := range []string{"mounted wiki AGENT.md 全文", "当前 mode_hint=ingest", "system rules", "ingest rules", "query rules", "repair rules", "sync rules", "wikilink rules", "report rules"} {
		if !strings.Contains(ingestGuide, want) {
			t.Fatalf("expected full AGENT guide to include %q, got %q", want, ingestGuide)
		}
	}

	repairGuide := svc.directAdminWikiModeGuide("repair")
	if !strings.Contains(repairGuide, "当前 mode_hint=repair") || !strings.Contains(repairGuide, "ingest rules") || !strings.Contains(repairGuide, "sync rules") {
		t.Fatalf("expected repair guide to keep full AGENT text, got %q", repairGuide)
	}

	syncGuide := svc.directAdminWikiModeGuide("sync")
	if !strings.Contains(syncGuide, "当前 mode_hint=sync") || !strings.Contains(syncGuide, "repair rules") || !strings.Contains(syncGuide, "ingest rules") {
		t.Fatalf("expected sync guide to keep full AGENT text, got %q", syncGuide)
	}
}
