package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/store"
)

type Deps struct {
	Config          *config.Config
	Runtime         *runtime.Runtime
	LLM             llm.Client
	Retriever       *retrieval.QMDRetriever
	Store           *store.Store
	CustomerIntents *CustomerIntentManager
	PromptDir       string
	WorkspaceDir    string
}

const currentLLMModel = "database-active-model"

type baseService struct {
	deps Deps
}

type activeLLMModelNamer interface {
	ActiveModelName(ctx context.Context) (string, error)
}

type llmRequestTimeoutResolver interface {
	RequestTimeout(ctx context.Context, admin bool) time.Duration
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
	text, _, err := s.executeLLMTrace(ctx, execution, model, messages, stepName)
	return text, err
}

type LLMTrace struct {
	Reasoning string `json:"reasoning,omitempty"`
	ModelID   string `json:"model_id,omitempty"`
	ModelName string `json:"model_name,omitempty"`
}

func (s *baseService) executeLLMTrace(ctx context.Context, execution *Execution, model string, messages []llm.Message, stepName string) (string, LLMTrace, error) {
	return s.executeLLMTraceWithHooks(ctx, execution, model, messages, stepName, nil)
}

type llmDeltaHooks struct {
	Content   func(string)
	Reasoning func(string)
}

func (s *baseService) executeLLMTraceWithHooks(ctx context.Context, execution *Execution, model string, messages []llm.Message, stepName string, hooks *llmDeltaHooks) (string, LLMTrace, error) {
	return s.executeLLMTraceWithOptions(ctx, execution, model, messages, stepName, hooks, nil)
}

func (s *baseService) executeLLMTraceWithOptions(ctx context.Context, execution *Execution, model string, messages []llm.Message, stepName string, hooks *llmDeltaHooks, enableThinking *bool) (string, LLMTrace, error) {
	return s.executeLLMTraceWithOptionsAndResponseFormat(ctx, execution, model, messages, stepName, hooks, enableThinking, nil)
}

func (s *baseService) executeLLMTraceWithOptionsAndResponseFormat(ctx context.Context, execution *Execution, model string, messages []llm.Message, stepName string, hooks *llmDeltaHooks, enableThinking *bool, responseFormat *llm.ResponseFormat) (string, LLMTrace, error) {
	start := time.Now()
	promptChars, promptTokens := estimatePromptSize(messages)
	timeout := s.llmRequestTimeout(execution)
	timeoutSec := int(timeout / time.Second)
	displayModel := model
	if namer, ok := s.deps.LLM.(activeLLMModelNamer); ok {
		if activeModelName, err := namer.ActiveModelName(ctx); err == nil && strings.TrimSpace(activeModelName) != "" {
			displayModel = activeModelName
		}
	}
	emitStreamEvent(ctx, "prompt", map[string]any{
		"name":                    stepName,
		"model":                   displayModel,
		"messages":                messages,
		"created_at":              start.Format(time.RFC3339Nano),
		"prompt_chars":            promptChars,
		"prompt_estimated_tokens": promptTokens,
		"timeout_sec":             timeoutSec,
		"enable_thinking":         enableThinking,
	})
	ctx = llm.WithRequestTimeout(ctx, timeout)
	ctx = llm.WithEnableThinking(ctx, enableThinking)
	ctx = llm.WithResponseFormat(ctx, responseFormat)
	ctx, usedModel := contextWithUsedLLMModelRecorder(ctx)
	var reasoning strings.Builder
	onDelta := func(delta llm.StreamDelta) {
		if delta.ReasoningContent != "" {
			reasoning.WriteString(delta.ReasoningContent)
			emitStreamEvent(ctx, "llm_reasoning_delta", map[string]any{
				"name":       stepName,
				"delta":      delta.ReasoningContent,
				"created_at": time.Now().Format(time.RFC3339Nano),
			})
			if hooks != nil && hooks.Reasoning != nil {
				hooks.Reasoning(delta.ReasoningContent)
			}
		}
		if delta.Content != "" {
			emitStreamEvent(ctx, "llm_delta", map[string]any{
				"name":       stepName,
				"delta":      delta.Content,
				"created_at": time.Now().Format(time.RFC3339Nano),
			})
			if hooks != nil && hooks.Content != nil {
				hooks.Content(delta.Content)
			}
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
	usedModelSnapshot := usedModel()
	trace := LLMTrace{
		Reasoning: reasoningText,
		ModelID:   usedModelSnapshot.ID,
		ModelName: usedModelSnapshot.Name,
	}
	if execution != nil {
		step := Step{
			Name:       stepName,
			Tool:       "llm.chat",
			DurationMs: end.Sub(start).Milliseconds(),
			Status:     "SUCCESS",
			StartedAt:  start,
			EndedAt:    end,
			Input: map[string]any{
				"model":                   displayModel,
				"model_id":                trace.ModelID,
				"message_count":           len(messages),
				"prompt_chars":            promptChars,
				"prompt_estimated_tokens": promptTokens,
				"timeout_sec":             timeoutSec,
				"enable_thinking":         enableThinking,
				"response_format":         customerLLMResponseFormatForTrace(responseFormat),
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
	}
	emitStreamEvent(ctx, "llm_done", map[string]any{
		"name":            stepName,
		"model_id":        trace.ModelID,
		"model_name":      trace.ModelName,
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
		return "", trace, err
	}
	return text, trace, nil
}

func customerLLMResponseFormatForTrace(format *llm.ResponseFormat) any {
	if format == nil {
		return nil
	}
	out := map[string]any{"type": strings.TrimSpace(format.Type)}
	if format.JSONSchema != nil {
		out["schema_name"] = strings.TrimSpace(format.JSONSchema.Name)
		out["strict"] = format.JSONSchema.Strict
	}
	return out
}

func (s *baseService) llmRequestTimeout(execution *Execution) time.Duration {
	if s == nil || s.deps.Config == nil {
		return 90 * time.Second
	}
	admin := isAdminLLMExecution(execution)
	if resolver, ok := s.deps.LLM.(llmRequestTimeoutResolver); ok {
		return s.adjustLLMRequestTimeout(resolver.RequestTimeout(context.Background(), admin), admin)
	}
	timeoutSec := s.deps.Config.LLM.TimeoutSec
	if admin {
		timeoutSec = s.deps.Config.LLM.AdminTimeoutSec
	}
	if timeoutSec <= 0 {
		if admin {
			timeoutSec = 300
		} else {
			timeoutSec = 90
		}
	}
	return s.adjustLLMRequestTimeout(time.Duration(timeoutSec)*time.Second, admin)
}

func (s *baseService) adjustLLMRequestTimeout(timeout time.Duration, admin bool) time.Duration {
	if admin || s == nil || s.deps.Config == nil || s.deps.Config.CustomerChat.ResponseTimeoutSec <= 0 {
		return timeout
	}
	customerTimeout := time.Duration(s.deps.Config.CustomerChat.ResponseTimeoutSec) * time.Second
	if customerTimeout > timeout {
		return customerTimeout
	}
	return timeout
}

func isAdminLLMExecution(execution *Execution) bool {
	if execution == nil {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(execution.Kind))
	return !strings.HasPrefix(kind, "customer-")
}

func (s *baseService) loadPrompt(name string) (string, error) {
	return llm.LoadPrompt(filepath.Join(s.deps.PromptDir, name))
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

func nowDate() string {
	return time.Now().Format("2006-01-02")
}

func stableShortHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:12]
}

func dedupeStrings(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func trimStringSlice(items []string, limit int) []string {
	items = dedupeStrings(items)
	if limit > 0 && len(items) > limit {
		return append([]string{}, items[:limit]...)
	}
	return items
}

func summarizeContent(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) > 400 {
		content = string(runes[:400]) + "..."
	}
	return content
}

func humanizeSlug(slug string) string {
	parts := strings.Split(strings.TrimSpace(slug), "-")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if len(part) <= 2 {
			out = append(out, strings.ToUpper(part))
			continue
		}
		runes := []rune(part)
		out = append(out, strings.ToUpper(string(runes[:1]))+string(runes[1:]))
	}
	return strings.Join(out, " ")
}

func slugFromText(text string) string {
	text = strings.ToLower(text)
	text = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, text)
	text = strings.Trim(text, "-")
	for strings.Contains(text, "--") {
		text = strings.ReplaceAll(text, "--", "-")
	}
	if text == "" {
		return "output"
	}
	return text
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
