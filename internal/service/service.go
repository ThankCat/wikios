package service

import (
	"context"
	"fmt"
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
	if err != nil {
		return result, err
	}
	if !result.Success {
		return result, fmt.Errorf("%s: %s", name, result.Error.Message)
	}
	return result, nil
}

func (s *baseService) loadPrompt(name string) (string, error) {
	return llm.LoadPrompt(filepath.Join(s.deps.PromptDir, name))
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
