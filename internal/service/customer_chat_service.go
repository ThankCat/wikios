package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"wikios/internal/config"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/wikiadapter"
)

type CustomerChatRequest struct {
	Question          string         `json:"question"`
	Stream            bool           `json:"stream,omitempty"`
	PersistLog        *bool          `json:"persist_log,omitempty"`
	Simulation        bool           `json:"simulation,omitempty"`
	Entrypoint        string         `json:"entrypoint,omitempty"`
	UserID            string         `json:"user_id"`
	SessionID         string         `json:"session_id"`
	QuestionMessageID string         `json:"question_message_id"`
	AnswerMessageID   string         `json:"answer_message_id"`
	QuestionCreatedAt string         `json:"question_created_at"`
	ReceivedAt        string         `json:"received_at"`
	Context           map[string]any `json:"context"`
	History           []ChatMessage  `json:"history"`
}

type ChatMessage struct {
	ID        string `json:"id,omitempty"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at,omitempty"`
}

type SourceRef struct {
	Path       string `json:"path"`
	Title      string `json:"title"`
	Confidence string `json:"confidence"`
}

type CustomerChatResponse struct {
	Answer     string         `json:"answer"`
	ReceivedAt string         `json:"received_at,omitempty"`
	AnsweredAt string         `json:"answered_at,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

type CustomerChatService struct {
	baseService
	cache *customerChatCache
}

type customerChatLLMOutput struct {
	AnswerMode          string               `json:"answer_mode"`
	AnswerType          string               `json:"answer_type"`
	AnswerText          string               `json:"answer"`
	CanAnswer           *bool                `json:"can_answer"`
	ReviewQuestion      string               `json:"review_question"`
	Confidence          float64              `json:"confidence"`
	EvidenceConfidence  float64              `json:"evidence_confidence"`
	ReviewRequired      bool                 `json:"review_required"`
	ReviewReason        string               `json:"review_reason"`
	BoundaryReason      string               `json:"boundary_reason"`
	SuggestedTargetPath string               `json:"suggested_target_path"`
	Sources             []customerChatSource `json:"sources"`
	Notes               string               `json:"notes"`
}

type customerChatSource struct {
	Path       string `json:"path"`
	Confidence string `json:"confidence"`
}

func NewCustomerChatService(deps Deps) *CustomerChatService {
	return &CustomerChatService{
		baseService: newBaseService(deps),
		cache:       defaultCustomerChatCache,
	}
}

func (s *CustomerChatService) Answer(ctx context.Context, traceID string, req CustomerChatRequest) (*CustomerChatResponse, error) {
	return s.answer(ctx, traceID, req, nil)
}

func (s *CustomerChatService) AnswerStream(ctx context.Context, traceID string, req CustomerChatRequest, emitter StreamEmitter) (*CustomerChatResponse, error) {
	return s.answerStream(ctx, traceID, req, emitter, false)
}

func (s *CustomerChatService) AnswerDebugStream(ctx context.Context, traceID string, req CustomerChatRequest, emitter StreamEmitter) (*CustomerChatResponse, error) {
	return s.answerStream(ctx, traceID, req, emitter, true)
}

func (s *CustomerChatService) answerStream(ctx context.Context, traceID string, req CustomerChatRequest, emitter StreamEmitter, debug bool) (*CustomerChatResponse, error) {
	req.Stream = true
	stream := newCustomerChatStream(emitter, debug)
	return s.answer(WithStreamEmitter(ctx, stream), traceID, req, stream)
}

func (s *CustomerChatService) answer(ctx context.Context, traceID string, req CustomerChatRequest, stream *customerChatStream) (*CustomerChatResponse, error) {
	runtimeSettings := LoadRuntimeSettingsOrDefault(ctx, s.deps.Store, s.deps.Config)
	return s.answerRouted(ctx, traceID, req, stream, runtimeSettings)
}

func customerChatRequestCanceled(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
}

func customerChatContextDone(ctx context.Context, err error) bool {
	if ctx == nil {
		return false
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return true
	}
	return errors.Is(err, context.Canceled)
}

func (s *CustomerChatService) customerTraceDetails(req CustomerChatRequest, parsed customerChatLLMOutput, trace LLMTrace, execution *Execution, sources []SourceRef, retrievedPaths []string, debugTrace map[string]any) map[string]any {
	details := map[string]any{
		"process_summary": customerReasoningSummary(req, parsed, sources, retrievedPaths),
		"steps":           customerExecutionStepsForDebug(execution.Steps, req.Simulation),
		"execution":       customerExecutionSummary(execution),
		"answer_mode":     normalizedAnswerMode(parsed.AnswerMode),
		"source_count":    len(sources),
		"retrieved_count": len(retrievedPaths),
		"sources":         customerSourceSummaries(sources),
		"retrieved_paths": retrievedPaths,
	}
	for key, value := range debugTrace {
		if value != nil {
			details[key] = value
		}
	}
	if strings.TrimSpace(trace.Reasoning) != "" {
		details["reasoning"] = trace.Reasoning
		details["reasoning_chars"] = len([]rune(trace.Reasoning))
	}
	return details
}

func customerTraceKeyAllowedInPersistentDetails(key string) bool {
	switch key {
	case "trace_id", "received_at", "simulation", "persist_log", "history_turns", "question_chars",
		"retrieval_question", "candidate_top_k", "max_evidence_chars", "retrieved_candidates",
		"fallback_candidates", "evidence", "sources", "retrieved_paths", "model_json_parsed",
		"review_decision", "retrieval_cache":
		return true
	default:
		return false
	}
}

func customerTraceStepStart(ctx context.Context, name string, tool string, input map[string]any) time.Time {
	start := time.Now()
	emitStreamEvent(ctx, "step_start", map[string]any{
		"name":       name,
		"tool":       tool,
		"input":      customerTraceMap(input),
		"started_at": start.Format(time.RFC3339Nano),
	})
	return start
}

func customerTraceStepFinish(ctx context.Context, execution *Execution, name string, tool string, start time.Time, input map[string]any, output map[string]any, err error) {
	if start.IsZero() {
		start = time.Now()
	}
	end := time.Now()
	status := "SUCCESS"
	resolvedOutput := customerTraceMap(output)
	if err != nil {
		status = "FAILED"
		if resolvedOutput == nil {
			resolvedOutput = map[string]any{}
		}
		resolvedOutput["error"] = truncateForPrompt(err.Error(), 1200)
	}
	step := Step{
		Name:       name,
		Tool:       tool,
		Status:     status,
		Input:      customerTraceMap(input),
		Output:     resolvedOutput,
		DurationMs: end.Sub(start).Milliseconds(),
		StartedAt:  start,
		EndedAt:    end,
	}
	if execution != nil {
		execution.Steps = append(execution.Steps, step)
	}
	emitStreamEvent(ctx, "step_finish", step)
}

func customerTraceMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = customerTracePayload(value)
	}
	return out
}

func customerTracePayload(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return truncateForPrompt(typed, 1600)
	case []string:
		limit := len(typed)
		if limit > 24 {
			limit = 24
		}
		out := make([]string, 0, limit)
		for _, item := range typed[:limit] {
			out = append(out, truncateForPrompt(item, 500))
		}
		return out
	case []map[string]any:
		limit := len(typed)
		if limit > 16 {
			limit = 16
		}
		out := make([]any, 0, limit)
		for _, item := range typed[:limit] {
			out = append(out, customerTraceMap(item))
		}
		return out
	case []any:
		limit := len(typed)
		if limit > 16 {
			limit = 16
		}
		out := make([]any, 0, limit)
		for _, item := range typed[:limit] {
			out = append(out, customerTracePayload(item))
		}
		return out
	case map[string]any:
		return customerTraceMap(typed)
	default:
		return value
	}
}

func customerAuditThinking(enabled *bool, content string) map[string]any {
	content = strings.TrimSpace(content)
	enabledValue := false
	if enabled != nil {
		enabledValue = *enabled
	}
	result := map[string]any{
		"enabled": enabledValue,
		"saved":   false,
		"content": nil,
		"chars":   0,
	}
	if enabledValue && content != "" {
		result["saved"] = true
		result["content"] = content
		result["chars"] = len([]rune(content))
	} else if enabledValue {
		result["unavailable_reason"] = "model_did_not_return_reasoning"
	}
	return result
}

func customerConversationContextForAudit(history []ChatMessage) []map[string]any {
	items := make([]map[string]any, 0, len(history)/2)
	var pendingQuestion string
	for _, item := range history {
		role := strings.ToLower(strings.TrimSpace(item.Role))
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		switch role {
		case "user":
			pendingQuestion = content
		case "assistant":
			if pendingQuestion != "" {
				items = append(items, map[string]any{"question": pendingQuestion, "answer": content})
				pendingQuestion = ""
			}
		}
	}
	return items
}

func (s *CustomerChatService) maybeWriteCustomerChatLog(traceID string, req CustomerChatRequest, resp *CustomerChatResponse, extra map[string]any) {
	if !shouldPersistCustomerChatLog(req) {
		return
	}
	s.writeCustomerChatAuditLog(traceID, req, resp, extra, nil)
}

func (s *CustomerChatService) maybeWriteCustomerChatErrorLog(traceID string, req CustomerChatRequest, stage string, err error, extra map[string]any) {
	if !shouldPersistCustomerChatLog(req) {
		return
	}
	details := map[string]any{}
	for key, value := range extra {
		if strings.TrimSpace(key) != "" {
			details[key] = value
		}
	}
	rawOutput := ""
	switch normalizeCustomerChatAuditErrorStage(stage) {
	case "router_parse":
		rawOutput = auditStringMapValue(details, "router_raw")
	case "specialist_parse":
		rawOutput = auditStringMapValue(details, "model_json_raw")
	}
	answeredAt := time.Now().UTC().Format(time.RFC3339Nano)
	resp := &CustomerChatResponse{
		ReceivedAt: firstNonEmpty(strings.TrimSpace(req.ReceivedAt), answeredAt),
		AnsweredAt: answeredAt,
		Details:    details,
	}
	s.writeCustomerChatAuditLog(traceID, req, resp, details, newCustomerChatAuditError(stage, err, rawOutput))
}

func shouldPersistCustomerChatLog(req CustomerChatRequest) bool {
	return req.PersistLog == nil || *req.PersistLog
}

func (s *CustomerChatService) writeCustomerChatAuditLog(traceID string, req CustomerChatRequest, resp *CustomerChatResponse, extra map[string]any, auditErr *customerChatAuditError) {
	if s == nil {
		return
	}
	enabled, redact, retentionDays := s.customerChatLogSettings()
	if !enabled {
		return
	}
	if resp == nil {
		resp = &CustomerChatResponse{}
	}
	workspaceDir := strings.TrimSpace(s.deps.WorkspaceDir)
	if workspaceDir == "" && s.deps.Config != nil {
		workspaceDir = strings.TrimSpace(s.deps.Config.Workspace.BaseDir)
	}
	if workspaceDir == "" {
		workspaceDir = ".workspace"
	}
	loggedAt := time.Now().UTC()
	entrypoint := strings.TrimSpace(strings.ToLower(req.Entrypoint))
	if entrypoint == "" {
		entrypoint = "external"
	}
	details := map[string]any{}
	if resp.Details != nil {
		for key, value := range resp.Details {
			details[key] = value
		}
	}
	for key, value := range extra {
		if strings.TrimSpace(key) != "" {
			if _, exists := details[key]; !exists {
				details[key] = value
			}
		}
	}
	specialistOutput := auditMapValue(details["model_json_parsed"])
	if len(specialistOutput) == 0 {
		if parsed, ok := extra["final_json"].(customerChatLLMOutput); ok {
			specialistOutput = customerSafeLLMOutputForLog(parsed)
		}
	}
	answerMode := firstNonEmpty(auditStringMapValue(specialistOutput, "answer_mode"), auditStringMapValue(details, "answer_mode"))
	routerThinking := auditMapValue(details["router_thinking"])
	if len(routerThinking) == 0 {
		routerThinking = customerAuditThinking(nil, auditStringMapValue(details, "router_thinking"))
	}
	specialistThinking := auditMapValue(details["specialist_thinking"])
	if len(specialistThinking) == 0 {
		specialistThinking = customerAuditThinking(nil, auditStringMapValue(details, "thinking"))
	}
	retrievalCache := auditMapValue(details["retrieval_cache"])
	routerModelID := firstNonEmpty(auditStringMapValue(details, "router_model_id"), customerConfiguredModelIDForLog(""))
	specialistModelID := firstNonEmpty(auditStringMapValue(details, "specialist_model_id"), customerConfiguredModelIDForLog(""))
	routerModelName := auditStringMapValue(details, "router_model_name")
	if routerModelName == "" {
		routerModelName = s.customerAuditModelName(context.Background(), strings.TrimSpace(auditStringMapValue(details, "router_model_id")))
	}
	specialistModelName := auditStringMapValue(details, "specialist_model_name")
	if specialistModelName == "" {
		specialistModelName = s.customerAuditModelName(context.Background(), strings.TrimSpace(auditStringMapValue(details, "specialist_model_id")))
	}
	routerThinkingEnabled := resultBoolValue(details, "router_thinking_enabled")
	specialistThinkingEnabled := resultBoolValue(details, "specialist_thinking_enabled")
	retrieval := map[string]any{
		"requested_by":         "router",
		"executed_by":          "service",
		"target_specialist":    auditStringMapValue(details, "specialist"),
		"scope":                auditStringMapValue(details, "specialist"),
		"duration_ms":          resultInt64Value(retrievalCache, "duration_ms"),
		"source_count":         auditListLen(details["sources"]),
		"attempted_queries":    retrievalCache["attempted_retrieval_queries"],
		"executed_queries":     retrievalCache["executed_retrieval_queries"],
		"skipped_query_count":  retrievalCache["skipped_retrieval_query_count"],
		"qmd_cache_hits":       retrievalCache["qmd_cache_hits"],
		"qmd_cache_misses":     retrievalCache["qmd_cache_misses"],
		"page_cache_hits":      retrievalCache["read_page_cache_hits"],
		"page_cache_misses":    retrievalCache["read_page_cache_misses"],
		"query_timings":        retrievalCache["retrieval_timings"],
		"page_timings":         retrievalCache["read_page_timings"],
		"candidates":           details["retrieved_candidates"],
		"sources":              details["sources"],
		"candidate_page_paths": details["retrieved_paths"],
		"evidence_preview":     details["evidence"],
	}
	receivedAt := firstNonEmpty(strings.TrimSpace(req.ReceivedAt), strings.TrimSpace(resp.ReceivedAt))
	answeredAt := strings.TrimSpace(resp.AnsweredAt)
	entryRecord := customerChatAuditRecord{
		SchemaVersion: customerChatAuditSchemaVersion,
		RecordType:    customerChatAuditRecordType,
		TraceID:       strings.TrimSpace(traceID),
		SessionID:     strings.TrimSpace(req.SessionID),
		Time: customerChatAuditTime{
			LoggedAt:        loggedAt.Format(time.RFC3339Nano),
			ReceivedAt:      receivedAt,
			AnsweredAt:      answeredAt,
			TotalDurationMS: customerTotalDurationMS(receivedAt, answeredAt),
		},
		Runtime: customerChatAuditRuntime{
			Environment:           customerRuntimeEnvironment(s.deps.Config),
			Entrypoint:            entrypoint,
			Simulation:            req.Simulation,
			GitCommit:             customerAuditGitCommit(),
			CustomerChatMode:      customerChatModeRouted,
			RouterModelID:         routerModelID,
			SpecialistModelID:     specialistModelID,
			RouterContractVersion: customerRouterContractVersion,
		},
		Request: customerChatAuditRequest{
			Message:             strings.TrimSpace(req.Question),
			HistoryTurns:        len(req.History),
			HistorySummary:      auditStringMapValue(auditMapValue(auditMapValue(details["router"])["output"]), "history_summary"),
			ConversationContext: customerConversationContextForAudit(req.History),
		},
		Router: customerChatAuditRouter{
			Model: customerChatAuditModel{
				ID:              routerModelID,
				Name:            routerModelName,
				ThinkingEnabled: routerThinkingEnabled,
			},
			DurationMS: resultInt64Value(details, "router_duration_ms"),
			Thinking:   routerThinking,
			RawOutput:  auditStringMapValue(details, "router_raw"),
			Output:     auditMapValue(auditMapValue(details["router"])["output"]),
		},
		Retrieval: retrieval,
		Specialist: customerChatAuditSpecialist{
			Name: auditStringMapValue(details, "specialist"),
			Model: customerChatAuditModel{
				ID:              specialistModelID,
				Name:            specialistModelName,
				ThinkingEnabled: specialistThinkingEnabled,
			},
			DurationMS: resultInt64Value(details, "specialist_duration_ms"),
			Thinking:   specialistThinking,
			Input:      customerSpecialistAuditInput(req, details),
			RawOutput:  auditStringMapValue(details, "model_json_raw"),
			Output:     specialistOutput,
		},
		Final: customerChatAuditFinal{
			Answer:         resp.Answer,
			AnswerMode:     answerMode,
			SourceCount:    auditListLen(specialistOutput["sources"]),
			ReviewRequired: resultBoolValue(specialistOutput, "review_required"),
		},
		Error:  auditErr,
		Review: customerChatAuditReviewPlaceholder(),
	}
	entry := customerChatAuditRecordToMap(entryRecord)
	if redact {
		entry = redactCustomerChatLogEntry(entry)
	}
	path := filepath.Join(workspaceDir, "customer_chat_logs", loggedAt.Format("2006-01-02")+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("write customer chat log mkdir failed trace=%s err=%v", traceID, err)
		return
	}
	s.pruneCustomerChatLogs(filepath.Dir(path), loggedAt, retentionDays)
	line, err := json.Marshal(entry)
	if err != nil {
		log.Printf("write customer chat log marshal failed trace=%s err=%v", traceID, err)
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("write customer chat log open failed trace=%s err=%v", traceID, err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		log.Printf("write customer chat log failed trace=%s err=%v", traceID, err)
	}
}

func auditStringMapValue(record map[string]any, key string) string {
	if record == nil {
		return ""
	}
	value, ok := record[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func auditMapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func customerSpecialistAuditInput(req CustomerChatRequest, details map[string]any) map[string]any {
	if input := auditMapValue(details["specialist_input"]); len(input) > 0 {
		return input
	}
	return map[string]any{
		"user_message":                 strings.TrimSpace(req.Question),
		"router_output_ref":            "router.output",
		"candidate_page_paths_ref":     "retrieval.candidate_page_paths",
		"candidate_page_paths_preview": details["retrieved_paths"],
	}
}

func auditListLen(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case []any:
		return int64(len(typed))
	case []map[string]any:
		return int64(len(typed))
	case []SourceRef:
		return int64(len(typed))
	case []customerChatSource:
		return int64(len(typed))
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return 0
		}
		var items []any
		if err := json.Unmarshal(raw, &items); err != nil {
			return 0
		}
		return int64(len(items))
	}
}

func resultInt64Value(result map[string]any, key string) int64 {
	if result == nil {
		return 0
	}
	switch value := result[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return 0
	}
}

func customerRuntimeEnvironment(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.Server.Mode) == "" {
		return "local"
	}
	return strings.TrimSpace(cfg.Server.Mode)
}

func customerTotalDurationMS(receivedAt string, answeredAt string) int64 {
	start, err1 := time.Parse(time.RFC3339Nano, strings.TrimSpace(receivedAt))
	end, err2 := time.Parse(time.RFC3339Nano, strings.TrimSpace(answeredAt))
	if err1 != nil || err2 != nil || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func customerSafeErrorForLog(value any) map[string]any {
	raw := strings.TrimSpace(fmt.Sprint(value))
	code := "customer_chat_generation_failed"
	if isCustomerHiddenLLMError(errors.New(raw)) {
		code = "model_service_unavailable"
	}
	return map[string]any{
		"code":  code,
		"chars": len([]rune(raw)),
	}
}

func customerSafeThinkingForLog(resp *CustomerChatResponse, raw any) string {
	if resp != nil && resp.Details != nil {
		if value, ok := resp.Details["process_summary"]; ok {
			if summary := strings.TrimSpace(fmt.Sprint(value)); summary != "" {
				return summary
			}
		}
	}
	if strings.TrimSpace(fmt.Sprint(raw)) == "" {
		return ""
	}
	return "已生成模型思考内容；customer 日志仅保留安全审计摘要，原始推导不写入。"
}

func customerRawModelOutputLogSummary(value any) map[string]any {
	text := strings.TrimSpace(fmt.Sprint(value))
	return map[string]any{
		"omitted": true,
		"reason":  "customer_raw_model_output_not_persisted",
		"chars":   len([]rune(text)),
	}
}

func customerSafeModelJSONForLog(value any, resp *CustomerChatResponse) any {
	if parsed, ok := value.(customerChatLLMOutput); ok {
		return customerSafeLLMOutputForLog(parsed)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return value
	}
	return decoded
}

func customerSafeLLMOutputForLog(parsed customerChatLLMOutput) map[string]any {
	return map[string]any{
		"answer_mode":           normalizedAnswerMode(parsed.AnswerMode),
		"answer_type":           strings.TrimSpace(parsed.AnswerType),
		"answer":                customerSafeAnswerForLog(parsed.AnswerText),
		"can_answer":            parsed.CanAnswer,
		"review_question":       strings.TrimSpace(parsed.ReviewQuestion),
		"confidence":            clampConfidence(parsed.Confidence),
		"evidence_confidence":   clampConfidence(parsed.EvidenceConfidence),
		"review_required":       parsed.ReviewRequired,
		"review_reason":         strings.TrimSpace(parsed.ReviewReason),
		"boundary_reason":       strings.TrimSpace(parsed.BoundaryReason),
		"suggested_target_path": strings.TrimSpace(parsed.SuggestedTargetPath),
		"sources":               parsed.Sources,
	}
}

func customerSafeAnswerForLog(answer string) any {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return ""
	}
	return answer
}

func (s *CustomerChatService) customerChatLogSettings() (bool, bool, int) {
	if s == nil {
		defaults := DefaultRuntimeSettings(nil).AnswerLog
		return defaults.Enabled, defaults.Redact, defaults.RetentionDays
	}
	settings := LoadRuntimeSettingsOrDefault(context.Background(), s.deps.Store, s.deps.Config).AnswerLog
	return settings.Enabled, settings.Redact, settings.RetentionDays
}

func redactCustomerChatLogEntry(entry map[string]any) map[string]any {
	raw, err := json.Marshal(entry)
	if err != nil {
		return entry
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return entry
	}
	redacted, ok := redactCustomerChatLogValue(value).(map[string]any)
	if !ok {
		return entry
	}
	return redacted
}

func redactCustomerChatLogValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if customerLogSensitiveKey(key) {
				if strings.TrimSpace(fmt.Sprint(item)) == "" {
					out[key] = item
				} else {
					out[key] = "[redacted]"
				}
				continue
			}
			out[key] = redactCustomerChatLogValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactCustomerChatLogValue(item))
		}
		return out
	case string:
		return redactCustomerChatLogString(typed)
	default:
		return value
	}
}

func customerLogSensitiveKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "api_key") ||
		strings.Contains(key, "authorization") ||
		strings.Contains(key, "password") ||
		strings.Contains(key, "secret") ||
		key == "token" ||
		strings.HasSuffix(key, "_token")
}

func redactCustomerChatLogString(value string) string {
	out := value
	for _, pattern := range customerLogSecretPatterns {
		out = pattern.ReplaceAllString(out, "[redacted]")
	}
	return out
}

func (s *CustomerChatService) pruneCustomerChatLogs(dir string, now time.Time, retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		day, err := time.Parse("2006-01-02", strings.TrimSuffix(name, ".jsonl"))
		if err != nil || !day.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			log.Printf("prune customer chat log failed path=%s err=%v", filepath.Join(dir, name), err)
		}
	}
}

func customerReasoningSummary(req CustomerChatRequest, parsed customerChatLLMOutput, sources []SourceRef, retrievedPaths []string) string {
	lines := []string{
		"1. 先做安全边界和禁答检查，确认这个问题能否用正式知识库回答。",
	}
	if len(sources) > 0 {
		lines = append(lines, fmt.Sprintf("2. 检索并读取 %d 个 customer-safe 候选知识页，优先使用正式知识、政策、流程、对比和综合页面。", len(sources)))
	} else {
		lines = append(lines, "2. 未检索到足够的正式候选页面，因此按低置信策略组织回答或进入人工审查。")
	}
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if mode == "" {
		mode = "unknown"
	}
	lines = append(lines, fmt.Sprintf("3. 根据证据可信度选择回答模式：%s。", mode))
	if parsed.ReviewRequired {
		lines = append(lines, "4. 当前回答已标记为需要人工审查，后续会沉淀到正式知识页或意图页。")
	} else if len(retrievedPaths) > 0 {
		lines = append(lines, "4. 最终回答只保留用户可见内容，不暴露内部路径、索引页或系统提示。")
	}
	if strings.TrimSpace(req.Question) != "" {
		lines = append(lines, "5. 服务层只解析结构化输出并记录审计信息，不改写客户可见答案。")
	}
	return strings.Join(lines, "\n")
}

func customerRetrievedPageSummaries(pages []retrieval.RetrievedPage, limit int) []map[string]any {
	if limit <= 0 || limit > len(pages) {
		limit = len(pages)
	}
	out := make([]map[string]any, 0, limit)
	for _, page := range pages[:limit] {
		out = append(out, map[string]any{
			"path":  strings.TrimSpace(page.Path),
			"score": page.Score,
		})
	}
	return out
}

func customerSourceSummaries(sources []SourceRef) []map[string]any {
	out := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		out = append(out, map[string]any{
			"path":       strings.TrimSpace(source.Path),
			"title":      strings.TrimSpace(source.Title),
			"confidence": strings.TrimSpace(source.Confidence),
		})
	}
	return out
}

func customerEvidenceTraceItem(source SourceRef, body string) map[string]any {
	body = strings.TrimSpace(body)
	return map[string]any{
		"path":       strings.TrimSpace(source.Path),
		"title":      strings.TrimSpace(source.Title),
		"confidence": strings.TrimSpace(source.Confidence),
		"body_chars": len([]rune(body)),
		"preview":    truncateForPrompt(body, 800),
	}
}

func customerExecutionSteps(steps []Step) []map[string]any {
	return customerExecutionStepsForDebug(steps, false)
}

func customerExecutionStepsForDebug(steps []Step, debug bool) []map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		item := map[string]any{
			"name":        step.Name,
			"tool":        step.Tool,
			"status":      step.Status,
			"duration_ms": step.DurationMs,
			"started_at":  step.StartedAt,
			"ended_at":    step.EndedAt,
		}
		if debug && len(step.Input) > 0 {
			item["input"] = customerTraceMap(step.Input)
		}
		if debug && len(step.Output) > 0 {
			item["output"] = customerTraceMap(step.Output)
		}
		if !debug {
			if safeInput := customerSafeStepInput(step); len(safeInput) > 0 {
				item["input"] = safeInput
			}
			if safeOutput := customerSafeStepOutput(step); len(safeOutput) > 0 {
				item["output"] = safeOutput
			}
		}
		out = append(out, item)
	}
	return out
}

func customerSafeStepInput(step Step) map[string]any {
	if len(step.Input) == 0 {
		return nil
	}
	if step.Tool == "llm.chat" {
		out := map[string]any{}
		for _, key := range []string{"model", "message_count", "prompt_chars", "prompt_estimated_tokens", "timeout_sec", "enable_thinking", "response_format"} {
			if value, ok := step.Input[key]; ok {
				out[key] = value
			}
		}
		return out
	}
	return customerTraceMap(step.Input)
}

func customerSafeStepOutput(step Step) map[string]any {
	if len(step.Output) == 0 {
		return nil
	}
	out := map[string]any{}
	if errText := resultStringValue(step.Output, "error"); errText != "" {
		out["error"] = customerSafeErrorForLog(errText)
	}
	if step.Tool == "llm.chat" {
		if value, ok := step.Output["response_preview"]; ok {
			out["response_chars"] = len([]rune(strings.TrimSpace(fmt.Sprint(value))))
		}
		if value, ok := step.Output["reasoning_chars"]; ok {
			out["reasoning_chars"] = value
		}
		return out
	}
	for key, value := range step.Output {
		if key == "error" {
			continue
		}
		out[key] = customerTracePayload(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func customerExecutionSummary(execution *Execution) map[string]any {
	if execution == nil {
		return nil
	}
	return map[string]any{
		"id":         execution.ID,
		"kind":       execution.Kind,
		"status":     execution.Status,
		"step_count": len(execution.Steps),
		"started_at": execution.StartedAt,
		"ended_at":   execution.EndedAt,
	}
}

func normalizeCustomerChatOutput(parsed customerChatLLMOutput) customerChatLLMOutput {
	if parsed.CanAnswer != nil && !*parsed.CanAnswer && strings.TrimSpace(parsed.AnswerMode) == "" {
		parsed.AnswerMode = "refusal"
	}
	parsed.AnswerMode = normalizedAnswerMode(parsed.AnswerMode)
	parsed.AnswerText = strings.TrimSpace(parsed.AnswerText)
	parsed.ReviewQuestion = strings.TrimSpace(parsed.ReviewQuestion)
	parsed.Confidence = clampConfidence(parsed.Confidence)
	parsed.EvidenceConfidence = clampConfidence(parsed.EvidenceConfidence)
	parsed.ReviewReason = strings.TrimSpace(parsed.ReviewReason)
	if parsed.ReviewReason == "" {
		parsed.ReviewReason = strings.TrimSpace(parsed.BoundaryReason)
	}
	parsed.SuggestedTargetPath = strings.TrimSpace(parsed.SuggestedTargetPath)
	parsed.Notes = strings.TrimSpace(parsed.Notes)
	return parsed
}

func filterCustomerChatSources(items []customerChatSource, candidates []SourceRef) []customerChatSource {
	if len(items) == 0 || len(candidates) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, candidate := range candidates {
		path := filepath.ToSlash(strings.TrimSpace(candidate.Path))
		if path != "" {
			allowed[path] = true
		}
	}
	out := make([]customerChatSource, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		path := filepath.ToSlash(strings.TrimSpace(item.Path))
		if path == "" || !allowed[path] || seen[path] {
			continue
		}
		confidence := strings.ToLower(strings.TrimSpace(item.Confidence))
		switch confidence {
		case "low", "medium", "high":
		default:
			confidence = customerSourceConfidence(path)
		}
		out = append(out, customerChatSource{Path: path, Confidence: confidence})
		seen[path] = true
	}
	return out
}

func normalizedAnswerMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "evidence", "mixed", "self_answer", "clarification", "refusal":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "self_answer"
	}
}

func clampConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (s *CustomerChatService) shouldCreateCustomerReview(req CustomerChatRequest, parsed customerChatLLMOutput, settings RuntimeCustomerQuerySettings) bool {
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if mode == "refusal" || strings.TrimSpace(parsed.AnswerText) == "" {
		return false
	}
	if !parsed.ReviewRequired {
		return false
	}
	if isObviouslyNonReviewableCustomerQuestion(req.Question) {
		return false
	}
	directMin, reviewMin := customerConfidenceThresholds(
		settings.DirectMin,
		settings.ReviewMin,
	)
	confidence := clampConfidence(parsed.Confidence)
	if confidence >= directMin {
		return false
	}
	return confidence >= reviewMin || strings.TrimSpace(parsed.ReviewReason) != "" || strings.TrimSpace(parsed.ReviewQuestion) != ""
}

func isObviouslyNonReviewableCustomerQuestion(question string) bool {
	normalized := normalizeCustomerIntentText(question)
	if normalized == "" {
		return true
	}
	switch normalized {
	case "你好", "您好", "hello", "hi", "nihao", "在吗", "在嘛", "在不", "谢谢", "谢谢你", "好的", "ok", "拜拜", "再见",
		"我是你爸爸吗", "我是你爸爸", "你是我爸爸吗", "你是我爸爸":
		return true
	}
	hasLetter := false
	hasTechnicalSeparator := false
	for _, r := range normalized {
		switch {
		case r >= '\u4e00' && r <= '\u9fff', r >= 'a' && r <= 'z':
			hasLetter = true
		case r == '.' || r == ':' || r == '/':
			hasTechnicalSeparator = true
		}
	}
	if hasLetter {
		return false
	}
	if hasTechnicalSeparator {
		return false
	}
	for _, r := range normalized {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return true
}

func customerConfidenceThresholds(directMin float64, reviewMin float64) (float64, float64) {
	if directMin <= 0 {
		directMin = 0.70
	}
	if reviewMin <= 0 {
		reviewMin = 0.25
	}
	directMin = clampConfidence(directMin)
	reviewMin = clampConfidence(reviewMin)
	if reviewMin > directMin {
		reviewMin = directMin
	}
	return directMin, reviewMin
}

func formatCustomerBeijingTime(value string) string {
	receivedAt := strings.TrimSpace(value)
	parsed, err := time.Parse(time.RFC3339Nano, receivedAt)
	if err != nil {
		return receivedAt
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return parsed.In(location).Format("2006-01-02 15:04:05 Asia/Shanghai")
}

func (s *CustomerChatService) supportContactPrompt(settings RuntimeSupportSettings) string {
	phone := strings.TrimSpace(settings.Phone)
	if phone == "" {
		phone = "400-1080-106"
	}
	wecom := strings.TrimSpace(settings.WeCom)
	if wecom == "" {
		wecom = "企业微信"
	}
	lines := make([]string, 0, 2)
	if phone != "" {
		lines = append(lines, "- 客服电话："+phone)
	}
	if wecom != "" {
		lines = append(lines, "- 企业微信："+wecom)
	}
	if len(lines) == 0 {
		return "- 暂无"
	}
	return strings.Join(lines, "\n")
}

func formatCandidatePageBlock(source SourceRef, content string) string {
	lines := []string{
		"- path: " + emptyAsDash(source.Path),
		"  title: " + emptyAsDash(source.Title),
		"  confidence: " + emptyAsDash(source.Confidence),
		"  content: |",
	}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		lines = append(lines, "    "+line)
	}
	if len(lines) == 4 {
		lines = append(lines, "    暂无内容")
	}
	return strings.Join(lines, "\n")
}

func formatSourceRefList(sources []SourceRef) string {
	if len(sources) == 0 {
		return "[]"
	}
	lines := make([]string, 0, len(sources))
	for _, source := range sources {
		path := strings.TrimSpace(source.Path)
		if path == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s | title=%s | confidence=%s", path, emptyAsDash(source.Title), emptyAsDash(source.Confidence)))
	}
	if len(lines) == 0 {
		return "[]"
	}
	return strings.Join(lines, "\n")
}

func formatCustomerHardBoundary() string {
	return strings.Join([]string{
		"- 服务端不生成、改写或替换本轮客户可见答案。",
		"- 你必须根据角色提示词、router_output 和 candidate_pages 自行判断普通问题、边界问题和拒答场景。",
		"- 不要向客户暴露 hard_boundary、candidate_pages、review 或其它内部字段。",
	}, "\n")
}

func appendCustomerEvidencePage(
	path string,
	question string,
	maxChars int,
	seenPaths map[string]bool,
	contentBlocks *[]string,
	sources *[]SourceRef,
	content string,
) (string, bool) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || seenPaths[path] || strings.TrimSpace(content) == "" {
		return "", false
	}
	displayTitle := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	body := strings.TrimSpace(content)
	if doc, err := wikiadapter.ParseDocument(content); err == nil {
		if title, _ := doc.Frontmatter["title"].(string); strings.TrimSpace(title) != "" {
			displayTitle = strings.TrimSpace(title)
		}
		if strings.TrimSpace(doc.Body) != "" {
			body = strings.TrimSpace(doc.Body)
		}
	}
	if maxChars <= 0 {
		maxChars = 2400
	}
	preview := buildCustomerEvidencePreview(body, path, question, maxChars)
	seenPaths[path] = true
	source := SourceRef{
		Path:       path,
		Title:      displayTitle,
		Confidence: customerSourceConfidence(path),
	}
	*contentBlocks = append(*contentBlocks, formatCandidatePageBlock(source, truncateForPrompt(preview, maxChars)))
	*sources = append(*sources, source)
	return body, true
}

func prioritizeCustomerRetrievedPages(pages []retrieval.RetrievedPage) []retrieval.RetrievedPage {
	out := append([]retrieval.RetrievedPage(nil), pages...)
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			leftRank := customerEvidenceDirectoryRank(out[i].Path)
			rightRank := customerEvidenceDirectoryRank(out[j].Path)
			if rightRank < leftRank || (rightRank == leftRank && out[j].Score > out[i].Score) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func customerEvidenceDirectoryRank(path string) int {
	path = filepath.ToSlash(strings.TrimSpace(path))
	switch {
	case strings.HasPrefix(path, "wiki/knowledge/"):
		return 0
	case strings.HasPrefix(path, "wiki/policies/"):
		return 1
	case strings.HasPrefix(path, "wiki/procedures/"):
		return 2
	case strings.HasPrefix(path, "wiki/comparisons/"):
		return 3
	case strings.HasPrefix(path, "wiki/synthesis/"):
		return 4
	case strings.HasPrefix(path, "wiki/concepts/"):
		return 5
	case strings.HasPrefix(path, "wiki/entities/"):
		return 6
	case strings.HasPrefix(path, "wiki/intents/"):
		return 7
	default:
		return 99
	}
}

func buildCustomerEvidencePreview(body string, path string, question string, maxChars int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if maxChars <= 0 {
		maxChars = 2400
	}
	terms := customerEvidenceTerms(question)
	if len(terms) == 0 {
		return truncateForPrompt(body, maxChars)
	}
	if preview := relevantTextWindows(body, terms, 2); strings.TrimSpace(preview) != "" {
		return preview
	}
	return truncateForPrompt(body, maxChars)
}

func relevantTextWindows(body string, terms []string, limit int) string {
	lower := strings.ToLower(body)
	type hit struct {
		index int
		score int
	}
	hits := make([]hit, 0)
	for _, term := range terms {
		index := strings.Index(lower, term)
		if index >= 0 {
			hits = append(hits, hit{index: index, score: len([]rune(term))})
		}
	}
	if len(hits) == 0 {
		return ""
	}
	for i := 0; i < len(hits)-1; i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].score > hits[i].score {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	windows := make([]string, 0, len(hits))
	for _, item := range hits {
		start := item.index - 600
		if start < 0 {
			start = 0
		}
		end := item.index + 900
		if end > len(body) {
			end = len(body)
		}
		windows = append(windows, strings.TrimSpace(body[start:end]))
	}
	return strings.Join(windows, "\n\n---\n\n")
}

func customerEvidenceTerms(question string) []string {
	normalized := strings.ToLower(strings.TrimSpace(question))
	if normalized == "" {
		return nil
	}
	seen := map[string]bool{}
	terms := make([]string, 0)
	add := func(term string) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" || seen[term] {
			return
		}
		if len([]rune(term)) < 2 {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, chunk := range splitSearchChunks(normalized) {
		add(chunk)
		runes := []rune(chunk)
		for size := 4; size >= 2; size-- {
			if len(runes) < size {
				continue
			}
			for i := 0; i <= len(runes)-size; i++ {
				add(string(runes[i : i+size]))
			}
		}
	}
	return terms
}

func splitSearchChunks(text string) []string {
	chunks := make([]string, 0)
	var current []rune
	lastKind := 0
	flush := func() {
		if len(current) > 0 {
			chunks = append(chunks, string(current))
			current = nil
		}
		lastKind = 0
	}
	for _, r := range text {
		kind := customerSearchRuneKind(r)
		if kind == 0 {
			flush()
			continue
		}
		if lastKind != 0 && kind != lastKind {
			flush()
		}
		current = append(current, r)
		lastKind = kind
	}
	flush()
	return chunks
}

func customerSearchRuneKind(r rune) int {
	switch {
	case r >= '\u4e00' && r <= '\u9fff':
		return 1
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return 2
	default:
		return 0
	}
}

func customerConversationExcerpt(req CustomerChatRequest) []string {
	lines := make([]string, 0, len(req.History)+1)
	for _, item := range req.History {
		content := strings.TrimSpace(item.Content)
		role := strings.TrimSpace(item.Role)
		if content == "" || role == "" {
			continue
		}
		prefix := role
		if item.CreatedAt != "" {
			prefix = item.CreatedAt + " " + role
		}
		lines = append(lines, prefix+": "+truncateForPrompt(content, 240))
	}
	if strings.TrimSpace(req.Question) != "" {
		prefix := "user"
		if req.QuestionCreatedAt != "" {
			prefix = req.QuestionCreatedAt + " user"
		}
		lines = append(lines, prefix+": "+truncateForPrompt(req.Question, 240))
	}
	return lines
}

var customerLogSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)sk-[a-z0-9_\-]{8,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|password|secret|token)\s*[:=]\s*["']?[^"'\s,;]+`),
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b1[3-9]\d{9}\b`),
}

func containsAny(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func isCustomerReadableEvidence(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !strings.HasPrefix(path, "wiki/") || !strings.HasSuffix(path, ".md") {
		return false
	}
	if strings.HasPrefix(path, "wiki/unconfirmed/") ||
		strings.HasPrefix(path, "wiki/forbidden/") ||
		strings.HasPrefix(path, "wiki/sources/") ||
		strings.HasPrefix(path, "wiki/templates/") {
		return false
	}
	return customerEvidenceDirectoryRank(path) < 99
}

func customerSourceConfidence(path string) string {
	path = filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(path, "wiki/knowledge/"),
		strings.HasPrefix(path, "wiki/policies/"),
		strings.HasPrefix(path, "wiki/procedures/"),
		strings.HasPrefix(path, "wiki/comparisons/"),
		strings.HasPrefix(path, "wiki/synthesis/"):
		return "high"
	case strings.HasPrefix(path, "wiki/concepts/"),
		strings.HasPrefix(path, "wiki/entities/"):
		return "medium"
	default:
		return "low"
	}
}

func runtimeCall(name string, args map[string]any) runtime.ToolCall {
	return runtime.ToolCall{Name: name, Args: args}
}
