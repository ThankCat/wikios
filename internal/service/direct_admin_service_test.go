package service

import (
	"context"
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
