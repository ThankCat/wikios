package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/report"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/store"
)

type Deps struct {
	Config        *config.Config
	Runtime       *runtime.Runtime
	LLM           llm.Client
	Retriever     *retrieval.QMDRetriever
	Store         *store.Store
	PublicIntents *PublicIntentManager
	PromptDir     string
	WorkspaceDir  string
}

type baseService struct {
	deps Deps
}

func newBaseService(deps Deps) baseService {
	return baseService{deps: deps}
}

func (s *baseService) env(mode string, traceID string, executionID string, jobID string) *runtime.ExecEnv {
	return &runtime.ExecEnv{
		WikiRoot:     s.deps.Config.MountedWiki.Root,
		WorkspaceDir: s.deps.WorkspaceDir,
		JobID:        jobID,
		Mode:         mode,
		TraceID:      traceID,
		TaskID:       executionID,
		QMDIndex:     s.deps.Config.MountedWiki.QMDIndex,
	}
}

func (s *baseService) executeTool(ctx context.Context, execution *Execution, env *runtime.ExecEnv, name string, args map[string]any, stepName string) (runtime.ToolResult, error) {
	start := time.Now()
	emitStreamEvent(ctx, "step_start", map[string]any{
		"name":       stepName,
		"tool":       name,
		"input":      args,
		"started_at": start.Format(time.RFC3339Nano),
	})
	result, err := s.deps.Runtime.Execute(ctx, env, runtime.ToolCall{Name: name, Args: args})
	end := time.Now()
	step := Step{
		Name:       stepName,
		Tool:       name,
		Input:      args,
		DurationMs: end.Sub(start).Milliseconds(),
		Status:     "SUCCESS",
		StartedAt:  start,
		EndedAt:    end,
	}
	if result.Data != nil {
		step.Output = result.Data
	}
	if err != nil || !result.Success {
		step.Status = "FAILED"
		if step.Output == nil {
			step.Output = map[string]any{}
		}
		if result.Error != nil {
			step.Output["error"] = result.Error.Message
		}
		if err != nil {
			step.Output["error"] = err.Error()
		}
	}
	if execution != nil {
		execution.Steps = append(execution.Steps, step)
	}
	emitStreamEvent(ctx, "step_finish", step)
	if err != nil {
		return result, err
	}
	if !result.Success {
		return result, fmt.Errorf("%s: %s", name, result.Error.Message)
	}
	return result, nil
}

func (s *baseService) executeLLM(ctx context.Context, execution *Execution, model string, messages []llm.Message, stepName string) (string, error) {
	start := time.Now()
	promptChars, promptTokens := estimatePromptSize(messages)
	timeout := s.llmRequestTimeout(execution)
	timeoutSec := int(timeout / time.Second)
	emitStreamEvent(ctx, "prompt", map[string]any{
		"name":                    stepName,
		"model":                   model,
		"messages":                messages,
		"created_at":              start.Format(time.RFC3339Nano),
		"prompt_chars":            promptChars,
		"prompt_estimated_tokens": promptTokens,
		"timeout_sec":             timeoutSec,
	})
	ctx = llm.WithRequestTimeout(ctx, timeout)
	var reasoning strings.Builder
	onDelta := func(delta llm.StreamDelta) {
		if delta.ReasoningContent != "" {
			reasoning.WriteString(delta.ReasoningContent)
			emitStreamEvent(ctx, "llm_reasoning_delta", map[string]any{
				"name":       stepName,
				"delta":      delta.ReasoningContent,
				"created_at": time.Now().Format(time.RFC3339Nano),
			})
		}
		if delta.Content != "" {
			emitStreamEvent(ctx, "llm_delta", map[string]any{
				"name":       stepName,
				"delta":      delta.Content,
				"created_at": time.Now().Format(time.RFC3339Nano),
			})
		}
	}
	var text string
	var err error
	if streamClient, ok := s.deps.LLM.(llm.EventStreamClient); ok {
		text, err = streamClient.StreamChatEvents(ctx, model, messages, onDelta)
	} else {
		text, err = s.deps.LLM.StreamChat(ctx, model, messages, func(delta string) {
			onDelta(llm.StreamDelta{Content: delta})
		})
	}
	end := time.Now()
	reasoningText := strings.TrimSpace(reasoning.String())
	if execution == nil {
		if err != nil {
			return "", err
		}
		return text, nil
	}
	step := Step{
		Name:       stepName,
		Tool:       "llm.chat",
		DurationMs: end.Sub(start).Milliseconds(),
		Status:     "SUCCESS",
		StartedAt:  start,
		EndedAt:    end,
		Input: map[string]any{
			"model":                   model,
			"message_count":           len(messages),
			"prompt_chars":            promptChars,
			"prompt_estimated_tokens": promptTokens,
			"timeout_sec":             timeoutSec,
			"system_preview":          summarizeMessage(messages, "system"),
			"user_preview":            summarizeMessage(messages, "user"),
		},
		Output: map[string]any{
			"response_preview": summarizeContent(text),
		},
	}
	if reasoningText != "" {
		step.Output["reasoning_preview"] = summarizeContent(reasoningText)
		step.Output["reasoning_chars"] = len([]rune(reasoningText))
	}
	if err != nil {
		step.Status = "FAILED"
		step.Output = map[string]any{"error": err.Error()}
	}
	execution.Steps = append(execution.Steps, step)
	emitStreamEvent(ctx, "llm_done", map[string]any{
		"name":            stepName,
		"text":            text,
		"reasoning":       reasoningText,
		"reasoning_chars": len([]rune(reasoningText)),
		"timeout_sec":     timeoutSec,
		"ended_at":        end.Format(time.RFC3339Nano),
		"started_at":      start.Format(time.RFC3339Nano),
		"error": func() string {
			if err != nil {
				return err.Error()
			}
			return ""
		}(),
	})
	if err != nil {
		return "", err
	}
	return text, nil
}

func (s *baseService) llmRequestTimeout(execution *Execution) time.Duration {
	if s == nil || s.deps.Config == nil {
		return 90 * time.Second
	}
	timeoutSec := s.deps.Config.LLM.TimeoutSec
	if execution != nil {
		timeoutSec = s.deps.Config.LLM.AdminTimeoutSec
	}
	if timeoutSec <= 0 {
		if execution != nil {
			timeoutSec = 300
		} else {
			timeoutSec = 90
		}
	}
	return time.Duration(timeoutSec) * time.Second
}

func (s *baseService) loadPrompt(name string) (string, error) {
	return llm.LoadPrompt(filepath.Join(s.deps.PromptDir, name))
}

func (s *baseService) loadPromptWithWikiAgent(name string) (string, error) {
	prompt, err := s.loadPrompt(name)
	if err != nil {
		return "", err
	}
	guide := s.loadWikiAgentSections("通用 Wiki 治理规则",
		"## 系统概述",
		"## Server / Wiki 职责边界",
		"## Wikilink 使用规范",
		"## Wiki 语言规范",
	)
	if strings.TrimSpace(guide) == "" {
		return prompt, nil
	}
	return prompt + "\n\n" + guide, nil
}

func (s *baseService) loadPromptWithWikiQueryGuide(name string) (string, error) {
	prompt, err := s.loadPrompt(name)
	if err != nil {
		return "", err
	}
	agentPath := filepath.Join(s.deps.Config.MountedWiki.Root, "AGENT.md")
	agentRaw, err := os.ReadFile(agentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return prompt, nil
		}
		return "", fmt.Errorf("read mounted wiki AGENT.md: %w", err)
	}
	queryGuide := extractPublicMarkdownSection(string(agentRaw), "## QUERY 操作规范")
	if strings.TrimSpace(queryGuide) == "" {
		return prompt, nil
	}
	return prompt + "\n\n以下是当前挂载 Wiki 的最高优先级 QUERY 规范（来自 mounted wiki 的 AGENT.md），仅用于证据查询和答案合成。若 public answer prompt 与该 QUERY 规范存在差异，一律以 AGENT.md 的 QUERY 规范为准；public answer prompt 只补充客户可见表达和安全边界：\n\n" + strings.TrimSpace(queryGuide), nil
}

func (s *baseService) loadPromptWithWikiSections(name string, title string, headings ...string) (string, error) {
	prompt, err := s.loadPrompt(name)
	if err != nil {
		return "", err
	}
	guide := s.loadWikiAgentSections(title, headings...)
	if strings.TrimSpace(guide) == "" {
		return prompt, nil
	}
	return prompt + "\n\n" + guide, nil
}

func (s *baseService) loadWikiAgentSections(title string, headings ...string) string {
	agentPath := filepath.Join(s.deps.Config.MountedWiki.Root, "AGENT.md")
	agentRaw, err := os.ReadFile(agentPath)
	if err != nil {
		return ""
	}
	sections := make([]string, 0, len(headings))
	for _, heading := range headings {
		section := extractPublicMarkdownSection(string(agentRaw), heading)
		if strings.TrimSpace(section) != "" {
			sections = append(sections, strings.TrimSpace(section))
		}
	}
	if len(sections) == 0 {
		return ""
	}
	return "【" + strings.TrimSpace(title) + "（来自 mounted wiki 的 AGENT.md，Wiki 治理规则最高优先级）】\n" + strings.Join(sections, "\n\n")
}

func estimatePromptSize(messages []llm.Message) (int, int) {
	chars := 0
	for _, message := range messages {
		chars += len([]rune(message.Role)) + len([]rune(message.Content))
	}
	tokens := chars / 4
	if chars > 0 && tokens == 0 {
		tokens = 1
	}
	return chars, tokens
}

func extractPublicMarkdownSection(markdown string, heading string) string {
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == heading {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func (s *baseService) normalizeMountedInputPath(input string) (string, error) {
	path := strings.TrimSpace(input)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	cleanRoot := filepath.Clean(s.deps.Config.MountedWiki.Root)
	cleanPath := filepath.Clean(path)
	if filepath.IsAbs(cleanPath) {
		rel, err := filepath.Rel(cleanRoot, cleanPath)
		if err != nil {
			return "", fmt.Errorf("normalize mounted path: %w", err)
		}
		rel = filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return "", fmt.Errorf("path must stay within mounted wiki root")
		}
		return rel, nil
	}
	rel := filepath.ToSlash(cleanPath)
	rel = strings.TrimPrefix(rel, "./")
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("invalid path")
	}
	return rel, nil
}

func sourceConfidence(path string) string {
	switch {
	case strings.Contains(path, "/faq/"):
		return "high"
	case strings.Contains(path, "/sources/"):
		return "medium"
	case strings.Contains(path, "/concepts/"), strings.Contains(path, "/entities/"):
		return "low"
	default:
		return "low"
	}
}

func reportResult(executionID string, taskType string, summary string, outputFiles []string, timeline []Step) report.Report {
	events := make([]report.Event, 0, len(timeline))
	for _, step := range timeline {
		events = append(events, report.Event{
			Step:       step.Name,
			Tool:       step.Tool,
			Status:     step.Status,
			DurationMs: step.DurationMs,
			Message:    summarizeStepOutput(step),
		})
	}
	return report.Report{
		TaskID:      executionID,
		TaskType:    taskType,
		Title:       strings.Title(taskType) + " Report",
		Summary:     summary,
		Timeline:    events,
		OutputFiles: outputFiles,
	}
}

func nowDate() string {
	return time.Now().Format("2006-01-02")
}

func buildOutputDocument(title string, body string, sourceCount int) string {
	return fmt.Sprintf(`---
type: synthesis
title: %q
date: %s
tags: []
source_count: %d
confidence: low
graph-excluded: true
---

%s
`, title, nowDate(), sourceCount, body)
}

func buildReportDocument(title string, taskType string, taskID string, body string) string {
	return fmt.Sprintf(`---
type: system-report
title: %q
date: %s
graph-excluded: true
task_type: %s
task_id: %s
---

%s
`, title, nowDate(), taskType, taskID, body)
}

func summarizeStepOutput(step Step) string {
	if step.Output == nil {
		return ""
	}
	for _, key := range []string{"path", "report_path", "error", "stdout"} {
		if value, ok := step.Output[key]; ok {
			text := fmt.Sprintf("%v", value)
			if key == "stdout" && len(text) > 80 {
				text = text[:80] + "..."
			}
			if strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "暂无"
	}
	return strings.Join(items, ", ")
}

func truncateForPrompt(text string, maxRunes int) string {
	trimmed := strings.TrimSpace(text)
	if maxRunes <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return string(runes[:maxRunes]) + "\n\n[truncated]"
}

func summarizeMessage(messages []llm.Message, role string) string {
	for _, message := range messages {
		if message.Role == role {
			return summarizeContent(message.Content)
		}
	}
	return ""
}

func bulletListOrPlaceholder(items []string, placeholder string) string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		filtered = append(filtered, "- "+item)
	}
	if len(filtered) == 0 {
		return "- " + placeholder
	}
	return strings.Join(filtered, "\n")
}
