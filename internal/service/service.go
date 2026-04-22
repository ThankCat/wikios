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
	"wikios/internal/task"
)

type Deps struct {
	Config       *config.Config
	Runtime      *runtime.Runtime
	LLM          llm.Client
	Retriever    *retrieval.QMDRetriever
	TaskStore    *task.Store
	PromptDir    string
	WorkspaceDir string
}

type baseService struct {
	deps Deps
}

func newBaseService(deps Deps) baseService {
	return baseService{deps: deps}
}

func (s *baseService) env(mode string, traceID string, taskID string, jobID string) *runtime.ExecEnv {
	return &runtime.ExecEnv{
		WikiRoot:     s.deps.Config.MountedWiki.Root,
		WorkspaceDir: s.deps.WorkspaceDir,
		JobID:        jobID,
		Mode:         mode,
		TraceID:      traceID,
		TaskID:       taskID,
		QMDIndex:     s.deps.Config.MountedWiki.QMDIndex,
	}
}

func (s *baseService) executeTool(ctx context.Context, taskModel *task.Task, env *runtime.ExecEnv, name string, args map[string]any, stepName string) (runtime.ToolResult, error) {
	start := time.Now()
	emitStreamEvent(ctx, "step_start", map[string]any{
		"name":  stepName,
		"tool":  name,
		"input": args,
	})
	result, err := s.deps.Runtime.Execute(ctx, env, runtime.ToolCall{Name: name, Args: args})
	step := task.Step{
		Name:       stepName,
		Tool:       name,
		Input:      args,
		DurationMs: time.Since(start).Milliseconds(),
		Status:     "SUCCESS",
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
	taskModel.Steps = append(taskModel.Steps, step)
	taskModel.UpdatedAt = time.Now()
	_ = s.deps.TaskStore.SaveTask(ctx, taskModel)
	emitStreamEvent(ctx, "step_finish", step)
	if err != nil {
		return result, err
	}
	if !result.Success {
		return result, fmt.Errorf("%s: %s", name, result.Error.Message)
	}
	return result, nil
}

func (s *baseService) executeLLM(ctx context.Context, taskModel *task.Task, model string, messages []llm.Message, stepName string) (string, error) {
	start := time.Now()
	emitStreamEvent(ctx, "prompt", map[string]any{
		"name":     stepName,
		"model":    model,
		"messages": messages,
	})
	onDelta := func(delta string) {
		emitStreamEvent(ctx, "llm_delta", map[string]any{
			"name":  stepName,
			"delta": delta,
		})
	}
	text, err := s.deps.LLM.StreamChat(ctx, model, messages, onDelta)
	if taskModel == nil {
		if err != nil {
			return "", err
		}
		return text, nil
	}
	step := task.Step{
		Name:       stepName,
		Tool:       "llm.chat",
		DurationMs: time.Since(start).Milliseconds(),
		Status:     "SUCCESS",
		Input: map[string]any{
			"model":          model,
			"message_count":  len(messages),
			"system_preview": summarizeMessage(messages, "system"),
			"user_preview":   summarizeMessage(messages, "user"),
		},
		Output: map[string]any{
			"response_preview": summarizeContent(text),
		},
	}
	if err != nil {
		step.Status = "FAILED"
		step.Output = map[string]any{"error": err.Error()}
	}
	taskModel.Steps = append(taskModel.Steps, step)
	taskModel.UpdatedAt = time.Now()
	_ = s.deps.TaskStore.SaveTask(ctx, taskModel)
	emitStreamEvent(ctx, "llm_done", map[string]any{
		"name": stepName,
		"text": text,
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

func (s *baseService) loadPrompt(name string) (string, error) {
	return llm.LoadPrompt(filepath.Join(s.deps.PromptDir, name))
}

func (s *baseService) loadPromptWithWikiAgent(name string) (string, error) {
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
	agentText := strings.TrimSpace(string(agentRaw))
	if agentText == "" {
		return prompt, nil
	}
	return prompt + "\n\n以下是当前挂载 Wiki 的运行规则（来自 mounted wiki 的 AGENT.md）。当这些规则比通用 prompt 更具体时，优先遵守这些规则：\n\n" + agentText, nil
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
	case strings.Contains(path, "/sources/"):
		return "medium"
	case strings.Contains(path, "/concepts/"), strings.Contains(path, "/entities/"):
		return "low"
	default:
		return "low"
	}
}

func reportResult(taskID string, taskType string, summary string, outputFiles []string, timeline []task.Step) report.Report {
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
		TaskID:      taskID,
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

func summarizeStepOutput(step task.Step) string {
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
