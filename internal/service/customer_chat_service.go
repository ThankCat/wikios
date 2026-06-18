package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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
	ClientChannel     string         `json:"client_channel,omitempty"`
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
	Answer         string              `json:"answer"`
	AnswerMode     string              `json:"answer_mode,omitempty"`
	ReviewRequired bool                `json:"review_required"`
	SourceCount    int                 `json:"source_count"`
	UserIntent     *CustomerUserIntent `json:"user_intent"`
	ReceivedAt     string              `json:"received_at,omitempty"`
	AnsweredAt     string              `json:"answered_at,omitempty"`
	Details        map[string]any      `json:"details,omitempty"`
}

type CustomerChatService struct {
	baseService
	cache       *customerChatCache
	concurrency chan struct{}
}

type customerChatLLMOutput struct {
	AnswerMode          string                      `json:"answer_mode"`
	AnswerType          string                      `json:"answer_type"`
	AnswerText          string                      `json:"answer"`
	CanAnswer           *bool                       `json:"can_answer"`
	ReviewQuestion      string                      `json:"review_question"`
	ConfidenceBreakdown customerConfidenceBreakdown `json:"confidence_breakdown"`
	Confidence          float64                     `json:"confidence"`
	EvidenceConfidence  float64                     `json:"evidence_confidence"`
	ReviewRequired      bool                        `json:"review_required"`
	ReviewReason        string                      `json:"review_reason"`
	BoundaryReason      string                      `json:"boundary_reason"`
	SuggestedTargetPath string                      `json:"suggested_target_path"`
	Sources             []customerChatSource        `json:"sources"`
	Notes               string                      `json:"notes"`
}

type customerConfidenceBreakdown struct {
	EvidenceCoverage  float64 `json:"evidence_coverage"`
	SourceDirectness  float64 `json:"source_directness"`
	AnswerSpecificity float64 `json:"answer_specificity"`
	MissingInfoImpact float64 `json:"missing_info_impact"`
	RiskSensitivity   float64 `json:"risk_sensitivity"`
}

type customerChatSource struct {
	Path       string `json:"path"`
	Confidence string `json:"confidence"`
}

func NewCustomerChatService(deps Deps) *CustomerChatService {
	var concurrency chan struct{}
	if deps.Config != nil && deps.Config.CustomerChat.MaxConcurrent > 0 {
		concurrency = make(chan struct{}, deps.Config.CustomerChat.MaxConcurrent)
	}
	return &CustomerChatService{
		baseService: newBaseService(deps),
		cache:       defaultCustomerChatCache,
		concurrency: concurrency,
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
	release, err := s.acquireCustomerChatSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	runtimeSettings := LoadRuntimeSettingsOrDefault(ctx, s.deps.Store, s.deps.Config)
	return s.answerRouted(ctx, traceID, req, stream, runtimeSettings)
}

func (s *CustomerChatService) acquireCustomerChatSlot(ctx context.Context) (func(), error) {
	if s == nil || s.concurrency == nil {
		return func() {}, nil
	}
	select {
	case s.concurrency <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-s.concurrency
			})
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
		"process_summary":        customerReasoningSummary(req, parsed, sources, retrievedPaths),
		"steps":                  customerExecutionStepsForDebug(execution.Steps, req.Simulation),
		"execution":              customerExecutionSummary(execution),
		"answer_mode":            normalizedAnswerMode(parsed.AnswerMode),
		"source_count":           len(parsed.Sources),
		"final_sources":          parsed.Sources,
		"retrieved_count":        len(retrievedPaths),
		"retrieved_source_count": len(sources),
		"sources":                customerSourceSummaries(sources),
		"retrieved_paths":        retrievedPaths,
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
		"client_channel", "app_policy", "app_guard", "internal_boundary_guard", "scenario_answer_guard", "unsafe_answer_guard", "human_contact_guard",
		"retrieval_question", "candidate_top_k", "max_evidence_chars", "retrieved_candidates",
		"fallback_candidates", "evidence", "sources", "retrieved_paths", "final_sources", "model_json_parsed",
		"review_decision", "retrieval_cache", "decision", "clarification", "hard_stop",
		"quality_signals", "retrieval_diagnostics", "source_count", "retrieved_source_count", "retrieved_count":
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

func customerAuditThinking(enabled *bool, content string, persist bool) map[string]any {
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
		result["chars"] = len([]rune(content))
		if persist {
			result["saved"] = true
			result["content"] = content
		} else {
			result["omitted"] = true
			result["unavailable_reason"] = "thinking_content_not_persisted"
		}
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

func normalizeCustomerClientChannel(channel string) string {
	switch strings.ToLower(strings.TrimSpace(channel)) {
	case "mobile_app":
		return "mobile_app"
	default:
		return "web"
	}
}

func customerRequestClientChannel(req CustomerChatRequest) string {
	return normalizeCustomerClientChannel(req.ClientChannel)
}

func customerRequestIsMobileApp(req CustomerChatRequest, settings RuntimeCustomerQuerySettings) bool {
	return settings.AppChannelEnabled && customerRequestClientChannel(req) == "mobile_app"
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
	clientChannel := customerRequestClientChannel(req)
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
	specialistOutput := map[string]any(nil)
	if parsed, ok := extra["final_json"].(customerChatLLMOutput); ok {
		specialistOutput = customerSafeLLMOutputForLog(parsed)
	}
	if len(specialistOutput) == 0 {
		specialistOutput = auditMapValue(details["model_json_parsed"])
	}
	answerMode := firstNonEmpty(auditStringMapValue(specialistOutput, "answer_mode"), auditStringMapValue(details, "answer_mode"))
	routerThinking := auditMapValue(details["router_thinking"])
	if len(routerThinking) == 0 {
		routerThinking = customerAuditThinking(nil, auditStringMapValue(details, "router_thinking"), false)
	}
	specialistThinking := auditMapValue(details["specialist_thinking"])
	if len(specialistThinking) == 0 {
		specialistThinking = customerAuditThinking(nil, auditStringMapValue(details, "thinking"), false)
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
	specialistName := auditSpecialistName(details)
	retrieval := map[string]any{
		"requested_by":         "router",
		"executed_by":          "service",
		"target_specialist":    specialistName,
		"scope":                specialistName,
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
	observability := map[string]any{
		"decision":                auditMapValue(details["decision"]),
		"clarification":           auditMapValue(details["clarification"]),
		"hard_stop":               auditMapValue(details["hard_stop"]),
		"quality_signals":         auditListValue(details["quality_signals"]),
		"retrieval_diagnostics":   auditMapValue(details["retrieval_diagnostics"]),
		"app_policy":              auditMapValue(details["app_policy"]),
		"app_guard":               auditMapValue(details["app_guard"]),
		"internal_boundary_guard": auditMapValue(details["internal_boundary_guard"]),
		"scenario_answer_guard":   auditMapValue(details["scenario_answer_guard"]),
		"unsafe_answer_guard":     auditMapValue(details["unsafe_answer_guard"]),
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
			ClientChannel:         clientChannel,
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
		Retrieval:     retrieval,
		Observability: observability,
		Specialist: customerChatAuditSpecialist{
			Name: specialistName,
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
			UserIntent:     resp.UserIntent,
		},
		Error:          auditErr,
		ReviewDecision: auditMapValue(details["review_decision"]),
		Review:         customerChatAuditReviewPlaceholder(),
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

func auditSpecialistName(details map[string]any) string {
	if details == nil {
		return ""
	}
	if profile := auditMapValue(details["specialist"]); len(profile) > 0 {
		if name := auditStringMapValue(profile, "name"); name != "" {
			return name
		}
	}
	if name := auditStringMapValue(details, "specialist"); name != "" && !strings.HasPrefix(name, "map[") {
		return name
	}
	routerOutput := auditMapValue(auditMapValue(details["router"])["output"])
	return auditStringMapValue(routerOutput, "specialist")
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

func auditListValue(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func customerSpecialistAuditLLMInput(userMessage string, systemPrompt string, userPrompt string, conversationContext string) map[string]any {
	return map[string]any{
		"user_message":             strings.TrimSpace(userMessage),
		"conversation_context":     strings.TrimSpace(conversationContext),
		"router_output_ref":        "router.output",
		"candidate_page_paths_ref": "retrieval.candidate_page_paths",
		"message_count":            2,
		"prompt_chars": map[string]any{
			"system": len([]rune(systemPrompt)),
			"user":   len([]rune(userPrompt)),
		},
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
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
		"confidence_breakdown":  parsed.ConfidenceBreakdown,
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

// recoverCustomerSpecialistMisplacedAnswer fixes a common model mistake: customer-visible
// text only in review_question while answer is empty. Does not rewrite wording.
func recoverCustomerSpecialistMisplacedAnswer(parsed customerChatLLMOutput) (customerChatLLMOutput, string) {
	if strings.TrimSpace(parsed.AnswerText) != "" {
		return parsed, ""
	}
	reviewQuestion := strings.TrimSpace(parsed.ReviewQuestion)
	if reviewQuestion == "" {
		return parsed, ""
	}
	mode := normalizedAnswerMode(parsed.AnswerMode)
	switch {
	case mode == "clarification":
		parsed.AnswerText = reviewQuestion
		return parsed, "review_question"
	case !parsed.ReviewRequired && mode != "refusal":
		parsed.AnswerText = reviewQuestion
		return parsed, "review_question"
	default:
		return parsed, ""
	}
}

func normalizeCustomerChatOutput(parsed customerChatLLMOutput) customerChatLLMOutput {
	if parsed.CanAnswer != nil && !*parsed.CanAnswer && strings.TrimSpace(parsed.AnswerMode) == "" {
		parsed.AnswerMode = "refusal"
	}
	if strings.TrimSpace(parsed.AnswerMode) == "" && len(parsed.Sources) > 0 {
		// Provider dropped answer_mode but cited evidence sources; treat it as an
		// evidence answer instead of falling back to the generic self_answer
		// default, so review routing and audit stay accurate.
		parsed.AnswerMode = "evidence"
	}
	parsed.AnswerMode = normalizedAnswerMode(parsed.AnswerMode)
	parsed.AnswerText = strings.TrimSpace(parsed.AnswerText)
	parsed.ReviewQuestion = strings.TrimSpace(parsed.ReviewQuestion)
	parsed.ConfidenceBreakdown = normalizeCustomerConfidenceBreakdown(parsed.ConfidenceBreakdown)
	parsed.Confidence = customerConfidenceBreakdownAverage(parsed.ConfidenceBreakdown)
	parsed.EvidenceConfidence = customerEvidenceConfidenceFromBreakdown(parsed.ConfidenceBreakdown)
	parsed.ReviewReason = strings.TrimSpace(parsed.ReviewReason)
	if parsed.ReviewReason == "" {
		parsed.ReviewReason = strings.TrimSpace(parsed.BoundaryReason)
	}
	parsed.SuggestedTargetPath = strings.TrimSpace(parsed.SuggestedTargetPath)
	parsed.Notes = strings.TrimSpace(parsed.Notes)
	return parsed
}

func normalizeCustomerConfidenceBreakdown(value customerConfidenceBreakdown) customerConfidenceBreakdown {
	return customerConfidenceBreakdown{
		EvidenceCoverage:  clampConfidence(value.EvidenceCoverage),
		SourceDirectness:  clampConfidence(value.SourceDirectness),
		AnswerSpecificity: clampConfidence(value.AnswerSpecificity),
		MissingInfoImpact: clampConfidence(value.MissingInfoImpact),
		RiskSensitivity:   clampConfidence(value.RiskSensitivity),
	}
}

func customerConfidenceBreakdownAverage(value customerConfidenceBreakdown) float64 {
	sum := value.EvidenceCoverage +
		value.SourceDirectness +
		value.AnswerSpecificity +
		value.MissingInfoImpact +
		value.RiskSensitivity
	return roundConfidence(sum / 5)
}

func customerEvidenceConfidenceFromBreakdown(value customerConfidenceBreakdown) float64 {
	return roundConfidence((value.EvidenceCoverage + value.SourceDirectness) / 2)
}

func roundConfidence(value float64) float64 {
	return math.Round(clampConfidence(value)*100) / 100
}

func sanitizeCustomerVisibleAnswer(answer string, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) (string, bool) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "", false
	}
	if customerAnswerIsAllowedInternalBoundaryRefusal(answer, parsed, routerOutput) {
		return answer, false
	}
	parts := splitCustomerAnswerSentences(answer)
	if len(parts) == 0 {
		return answer, false
	}
	kept := make([]string, 0, len(parts))
	changed := false
	for _, part := range parts {
		if customerVisibleAnswerLeaksInternalContext(part) {
			changed = true
			continue
		}
		kept = append(kept, part)
	}
	if !changed {
		return answer, false
	}
	sanitized := strings.TrimSpace(strings.Join(kept, ""))
	if sanitized == "" {
		return answer, false
	}
	return sanitized, true
}

func customerAnswerIsAllowedInternalBoundaryRefusal(answer string, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || !customerRouterIsInternalBoundary(routerOutput) {
		return false
	}
	if normalizedAnswerMode(parsed.AnswerMode) != "refusal" {
		return false
	}
	return customerAnswerLooksLikeInternalBoundary(answer)
}

type customerHumanContactGuardResult struct {
	Triggered bool
	Answer    string
	Reason    string
	Allowed   bool
	Removed   []string
}

func customerHumanContactGuard(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerHumanContactGuardResult {
	answer := strings.TrimSpace(parsed.AnswerText)
	if answer == "" {
		return customerHumanContactGuardResult{Reason: "empty_answer"}
	}
	if customerHumanContactAllowed(req, routerOutput) {
		return customerHumanContactGuardResult{Reason: "allowed_refund_or_explicit_contact", Allowed: true}
	}
	if !customerAnswerHasHumanContactGuidance(answer) {
		return customerHumanContactGuardResult{Reason: "no_human_contact_guidance"}
	}
	sanitized, removed := sanitizeCustomerHumanContactGuidance(answer)
	if strings.TrimSpace(sanitized) == strings.TrimSpace(answer) {
		return customerHumanContactGuardResult{Reason: "no_rewrite_available"}
	}
	return customerHumanContactGuardResult{
		Triggered: true,
		Answer:    sanitized,
		Reason:    "removed_unprompted_human_contact_guidance",
		Removed:   removed,
	}
}

func customerHumanContactAllowed(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	decisionText := customerScenarioGuardText(req, routerOutput)
	if customerScenarioIsRefund(routerOutput, decisionText) {
		return true
	}
	current := normalizeCustomerReviewText(req.Question)
	if customerRouterLooksWeComContactQuestion(current) || customerRouterLooksHumanContactQuestion(current) ||
		containsAny(current, "客服电话", "电话多少", "联系方式", "怎么联系", "联系你们", "找你们") {
		return true
	}
	if routerOutput == nil {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if containsAny(intent, "contact", "human", "wecom", "wechat", "customer_service") &&
		containsAny(current, "客服", "电话", "联系方式", "微信", "企微", "联系", "找谁", "找人", "人工") {
		return true
	}
	return false
}

func customerAnswerHasHumanContactGuidance(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text,
		"联系人工",
		"人工客服",
		"联系客服",
		"联系在线客服",
		"企业微信",
		"企微",
		"微信客服",
		"客服电话",
		"400-1080-106",
		"联系微信",
		"添加客服",
		"扫码添加客服",
		"人工核查",
		"人工处理",
		"人工确认",
		"人工核实",
		"人工排查",
		"人工协助",
		"专人协助",
		"由人工",
		"找人工",
		"找客服",
	)
}

func sanitizeCustomerHumanContactGuidance(answer string) (string, []string) {
	parts := splitCustomerAnswerSentences(answer)
	if len(parts) == 0 {
		parts = []string{strings.TrimSpace(answer)}
	}
	kept := make([]string, 0, len(parts))
	removed := []string{}
	for _, part := range parts {
		replaced, changed := rewriteCustomerHumanContactSentence(part)
		if strings.TrimSpace(replaced) == "" {
			removed = append(removed, truncateForPrompt(part, 120))
			continue
		}
		if changed {
			removed = append(removed, truncateForPrompt(part, 120))
		}
		kept = append(kept, replaced)
	}
	sanitized := strings.TrimSpace(strings.Join(kept, ""))
	if sanitized == "" {
		sanitized = "这个问题需要按当前页面和订单状态确认。您可以先刷新页面、重新登录，并在个人中心或对应产品后台查看当前状态。"
	}
	return sanitized, removed
}

func rewriteCustomerHumanContactSentence(sentence string) (string, bool) {
	original := strings.TrimSpace(sentence)
	if original == "" {
		return "", false
	}
	text := normalizeCustomerReviewText(original)
	if containsAny(text,
		"联系人工",
		"人工客服",
		"联系客服",
		"联系在线客服",
		"企业微信",
		"企微",
		"微信客服",
		"客服电话",
		"400-1080-106",
		"联系微信",
		"添加客服",
		"扫码添加客服",
		"人工协助",
		"专人协助",
		"找人工",
		"找客服",
	) {
		return "", true
	}
	replacer := strings.NewReplacer(
		"需要准备订单信息由人工核实", "需要按支付记录和订单状态核实",
		"由人工核实", "按订单状态核实",
		"人工核实", "按订单状态核实",
		"人工核查", "按页面状态核查",
		"人工处理", "按页面状态处理",
		"人工确认", "按当前规则确认",
		"人工排查", "按当前配置继续排查",
		"由人工确认", "按订单状态确认",
		"需要人工按当前规则确认", "需要按当前规则确认",
		"需要人工按订单状态确认", "需要按订单状态确认",
		"需要人工确认", "需要按当前资源和订单状态确认",
		"人工按订单状态确认", "按订单状态确认",
		"人工按当前规则确认", "按当前规则确认",
	)
	rewritten := strings.TrimSpace(replacer.Replace(original))
	return rewritten, rewritten != original
}

func (result customerHumanContactGuardResult) Audit() map[string]any {
	return map[string]any{
		"triggered":     result.Triggered,
		"reason":        result.Reason,
		"allowed":       result.Allowed,
		"removed_count": len(result.Removed),
		"removed":       result.Removed,
		"action":        "audit_only",
	}
}

type customerAppGuardResult struct {
	Triggered bool
	Answer    string
	Hits      []string
	Reason    string
}

func customerMobileAppChannelPolicyPrompt() string {
	return strings.Join([]string{
		"- 当前请求来自手机 App 渠道，客户只能看到 App 已报备、App 内可操作的内容。",
		"- 只回答手机 App 内安装、登录、购买、连接、VPN 权限、IP 未变化、归属地延迟、配置、设置和使用排障。",
		"- App 渠道可见产品只有共享静态 IP 和住宅 IP；客户说“静态 IP”时默认按共享静态 IP 回答。",
		"- 不输出动态 IP、海外 IP、独享静态 IP 的教程、价格、购买入口、销售引导或配置步骤。",
		"- 不输出电脑端配置、API、白名单、SOCKS5、代理地址端口、代码、第三方代理软件教程。",
		"- 如果客户问到 App 渠道外的产品或技术，必须明确说“手机 App 不支持/当前 App 端不支持”，不要顾左右而言他；随后给 App 内可用替代路径。",
		"- 不支持说明要短而直接，例如“手机 App 不支持 API 提取”，然后说明 App 内可选择共享静态 IP 或住宅 IP、连接、确认系统 VPN 权限、重新打开目标 App 检查出口 IP/归属地。",
	}, "\n")
}

func customerAppPolicyAudit(enabled bool) map[string]any {
	return map[string]any{
		"enabled":          enabled,
		"client_channel":   "mobile_app",
		"allowed_products": []string{"shared_static_ip", "residential_ip"},
		"scope":            "mobile_app_operable_answer",
	}
}

func customerAppGuardAnswer(req CustomerChatRequest, answer string) customerAppGuardResult {
	answer = strings.TrimSpace(answer)
	hits := customerAppGuardForbiddenHits(answer)
	if len(hits) == 0 {
		return customerAppGuardResult{Triggered: false, Answer: answer, Hits: nil, Reason: "pass"}
	}
	return customerAppGuardResult{
		Triggered: true,
		Answer:    customerMobileAppFallbackAnswer(req),
		Hits:      hits,
		Reason:    "forbidden_app_channel_content",
	}
}

func (r customerAppGuardResult) Audit() map[string]any {
	return map[string]any{
		"triggered": r.Triggered,
		"reason":    r.Reason,
		"hits":      r.Hits,
		"action":    "audit_only",
	}
}

func customerAppGuardForbiddenHits(answer string) []string {
	text := normalizeCustomerReviewText(answer)
	if text == "" {
		return nil
	}
	hits := []string{}
	check := func(name string, markers ...string) {
		if containsAny(text, markers...) {
			hits = appendUniqueString(hits, name)
		}
	}
	check("dynamic_ip", "动态ip", "动态代理", "动态套餐")
	check("overseas_ip", "海外ip", "海外代理", "跨境", "google", "chatgpt")
	check("dedicated_static_ip", "独享静态", "独享ip", "独享 ip", "独享带宽")
	check("desktop_config", "windows", "macos", "电脑端", "pc端", "浏览器代理", "系统代理")
	check("api", "api", "接口提取", "提取链接", "提取ip", "提取 ip")
	check("whitelist", "白名单", "加白", "授权ip", "授权 ip")
	check("socks5", "socks5", "sock5", "代理端口", "代理地址", "端口号")
	check("code_or_tools", "python", "curl", "代码", "postern", "sstap", "clash", "第三方代理")
	return hits
}

func customerMobileAppFallbackAnswer(req CustomerChatRequest) string {
	text := normalizeCustomerReviewText(req.Question)
	switch {
	case containsAny(text, "api", "接口", "提取"):
		return "手机 App 不支持 API 提取。App 内可以直接选择共享静态 IP 或住宅 IP 后连接，并确认系统 VPN 权限已开启。"
	case containsAny(text, "socks5", "sock5", "端口", "代理地址", "白名单", "加白", "授权ip", "授权 ip"):
		return "手机 App 不支持 SOCKS5、代理地址端口或白名单这类电脑端/API 配置。App 内可以直接选择共享静态 IP 或住宅 IP 后连接，并确认系统 VPN 权限已开启。"
	case containsAny(text, "动态"):
		return "手机 App 当前不支持动态 IP。App 内可以使用共享静态 IP 或住宅 IP，选择对应 IP 后连接即可。"
	case containsAny(text, "海外", "google", "chatgpt"):
		return "手机 App 当前不支持海外 IP。App 内可以使用共享静态 IP 或住宅 IP，选择对应 IP 后连接即可。"
	case containsAny(text, "独享"):
		return "手机 App 当前不支持独享静态 IP。App 内的静态 IP 按共享静态 IP 处理，也可以选择住宅 IP 后连接。"
	case containsAny(text, "电脑", "windows", "mac", "pc"):
		return "手机 App 不支持电脑端配置方式。请在手机 App 内选择共享静态 IP 或住宅 IP 后连接，并确认系统 VPN 权限已开启。"
	}
	if containsAny(text, "没变", "不变", "不显示", "不准", "不准确", "归属地", "定位", "城市") {
		return "手机 App 内可以使用共享静态 IP 或住宅 IP。请先在 App 内选择对应 IP 并连接，确认系统 VPN 权限已开启；如果目标 App 里归属地暂时没变化，先重启目标 App 或等待平台 IP 库刷新后再查看。"
	}
	if containsAny(text, "购买", "怎么买", "开通", "价格", "多少钱", "套餐") {
		return "手机 App 内当前可选择共享静态 IP 或住宅 IP。请在 App 的产品/套餐页面选择需要的类型后按页面提示开通，静态 IP 在 App 场景默认按共享静态 IP 处理。"
	}
	if containsAny(text, "住宅") {
		return "手机 App 内可以使用住宅 IP。请在 App 内选择住宅 IP 后连接，并确认系统 VPN 权限已开启，再打开目标 App 查看当前出口 IP 或归属地。"
	}
	return "手机 App 内可以使用共享静态 IP 或住宅 IP。请在 App 内选择对应 IP 后连接，并确认系统 VPN 权限已开启，再打开目标 App 查看当前出口 IP 或归属地。"
}

type customerInternalBoundaryGuardResult struct {
	Triggered bool
	Answer    string
	Reason    string
}

func customerInternalBoundaryGuard(parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerInternalBoundaryGuardResult {
	if routerOutput == nil {
		return customerInternalBoundaryGuardResult{Reason: "no_router_output"}
	}
	if !customerRouterIsInternalBoundary(routerOutput) {
		return customerInternalBoundaryGuardResult{Reason: "not_internal_boundary"}
	}
	return customerInternalBoundaryGuardResult{Reason: "internal_boundary_observed"}
}

func customerRouterIsInternalBoundary(routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil {
		return false
	}
	if customerRouterListContains(routerOutput.RiskFlags, "internal") {
		return true
	}
	return routerOutput.RiskBoundary == "internal_security_boundary" && customerRouterLooksInternalIntent(routerOutput.Intent)
}

func (result customerInternalBoundaryGuardResult) Audit() map[string]any {
	return map[string]any{
		"triggered": result.Triggered,
		"reason":    result.Reason,
		"action":    "audit_only",
	}
}

func customerAnswerLooksLikeInternalBoundary(answer string) bool {
	text := strings.ToLower(strings.TrimSpace(answer))
	if text == "" {
		return false
	}
	hasInternal := containsAny(text, "内部", "prompt", "提示词", "后台规则", "路由规则", "内部配置", "系统提示")
	hasRefusal := containsAny(text, "不能", "无法", "不便", "不提供", "不能对外提供", "不可公开")
	return hasInternal && hasRefusal
}

type customerScenarioAnswerGuardResult struct {
	Triggered             bool
	Answer                string
	AnswerMode            string
	ReviewRequired        bool
	ReviewReason          string
	Reason                string
	Blocked               bool
	BlockReason           string
	AllowedProducts       []string
	OutputProducts        []string
	FallbackSources       []customerChatSource
	MinConfidence         float64
	MinEvidenceConfidence float64
}

type customerScenarioGuardInput struct {
	Request     CustomerChatRequest
	Router      *CustomerRouterOutput
	CurrentText string
	IntentText  string
	ContextText string
	ProductLock string
}

func newCustomerScenarioGuardInput(req CustomerChatRequest, routerOutput *CustomerRouterOutput) customerScenarioGuardInput {
	input := customerScenarioGuardInput{
		Request:     req,
		Router:      routerOutput,
		CurrentText: normalizeCustomerReviewText(req.Question),
		ContextText: customerScenarioGuardText(req, routerOutput),
	}
	if routerOutput != nil {
		input.IntentText = strings.ToLower(strings.TrimSpace(strings.Join([]string{
			routerOutput.Specialist,
			routerOutput.Intent,
			routerOutput.UserGoal,
			routerOutput.RewrittenQuestion,
			routerOutput.QuestionStage,
			routerOutput.AnswerStrategy,
			routerOutput.Slots.PrimaryProduct,
			strings.Join(routerOutput.Slots.Products, " "),
			routerOutput.Slots.StaticType,
			routerOutput.Slots.IPType,
			routerOutput.Slots.Device,
			routerOutput.Slots.Scenario,
		}, " ")))
		input.ProductLock = customerScenarioPrimaryProductLock(routerOutput)
	}
	return input
}

func customerScenarioPrimaryProductLock(routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil {
		return ""
	}
	product := strings.TrimSpace(routerOutput.Slots.PrimaryProduct)
	if product != "" && product != "unknown" {
		return product
	}
	for _, item := range routerOutput.Slots.Products {
		item = strings.TrimSpace(item)
		if item != "" && item != "unknown" {
			return item
		}
	}
	return ""
}

func customerScenarioAnswerGuard(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerScenarioAnswerGuardResult {
	if routerOutput == nil {
		return customerScenarioAnswerGuardResult{Reason: "no_router_output"}
	}
	answer := strings.TrimSpace(parsed.AnswerText)
	decisionText := customerScenarioGuardText(req, routerOutput)
	if result, ok := customerScenarioHardGuardResult(req, parsed, routerOutput, decisionText, answer); ok {
		return result
	}
	return customerScenarioAnswerGuardResult{Reason: "pass_model_answer"}
}

func customerScenarioAnswerTemplateGuard(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerScenarioAnswerGuardResult {
	if routerOutput == nil {
		return customerScenarioAnswerGuardResult{Reason: "no_router_output"}
	}
	answer := strings.TrimSpace(parsed.AnswerText)
	decisionText := customerScenarioGuardText(req, routerOutput)
	switch {
	case customerScenarioIsProxyIPConcept(req, routerOutput, decisionText) && !customerAnswerHasProxyIPConceptTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "代理 IP 可以理解为请求访问目标网站时对外呈现的出口 IP。配置代理后，目标网站看到的是代理出口 IP，而不是您本地网络的原始出口。",
			AnswerMode:            "evidence",
			Reason:                "proxy_ip_concept_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/knowledge/si-ye-tian-proxy-ip-products.md", Confidence: "high"}},
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsLiveStreamingSelection(req, routerOutput, decisionText) && (!customerAnswerHasLiveStreamingSelectionTerms(answer) || customerAnswerHasLiveStreamingSelectionForbiddenTerms(answer)):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "直播场景一般优先看独享静态 IP，带宽可先按 10M 起评估，并在正式使用前测试实际线路表现。",
			AnswerMode:            "evidence",
			Reason:                "live_streaming_selection_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/comparisons/shared-vs-dedicated-static-ip.md", Confidence: "high"}, {Path: "wiki/comparisons/si-ye-tian-platform-scenario-selection.md", Confidence: "high"}},
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPlatformDisplayIssue(routerOutput, decisionText) && (normalizedAnswerMode(parsed.AnswerMode) != "evidence" || !customerAnswerHasPlatformDisplayBoundary(answer) || len(parsed.Sources) == 0):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "先确认代理已连接并用浏览器打开 https://www.ip138.com/ 或 https://www.ipip.net/ 确认设备出口 IP 是否已变；如果设备出口已变，抖音显示可能受平台 IP 库、缓存或定位权限影响，平台显示可能会有延迟。",
			AnswerMode:            "evidence",
			Reason:                "platform_display_boundary",
			FallbackSources:       []customerChatSource{{Path: "wiki/procedures/si-ye-tian-connection-troubleshooting.md", Confidence: "high"}},
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsAmbiguousDynamicStaticPrice(routerOutput, decisionText) && !customerAnswerHasDynamicStaticPriceClarification(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "您问的是动态 IP 还是静态 IP 的价格？",
			AnswerMode:     "clarification",
			Reason:         "ambiguous_dynamic_static_price_clarification",
			MinConfidence:  0.8,
			ReviewRequired: false,
		}
	case customerScenarioIsStaticIPBandwidthPrice(routerOutput, decisionText) && !customerAnswerHasStaticIPBandwidthPriceTerms(answer, customerScenarioStaticBandwidth(routerOutput, decisionText)):
		bandwidth := customerScenarioStaticBandwidth(routerOutput, decisionText)
		price := customerStaticIPBandwidthPrice(bandwidth)
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                fmt.Sprintf("静态 IP %s 数据中心共享型原价为 %s 元/个/月，独享型原价为 %s 元/个/月。请告诉我您需要购买的数量，以便核算对应折扣价。", bandwidth, price.Shared, price.Dedicated),
			AnswerMode:            "evidence",
			Reason:                "static_ip_bandwidth_price_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/knowledge/si-ye-tian-static-ip-pricing.md", Confidence: "high"}},
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsGenericStaticIPPrice(req, routerOutput, decisionText) && !customerAnswerHasGenericStaticIPPriceTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "静态 IP 按个/月计费，分共享型和独享型：共享型 5M 25 元/个/月起，独享型 5M 300 元/个/月起。您要共享型还是独享型、需要多少个？",
			AnswerMode:            "evidence",
			Reason:                "generic_static_ip_price_baseline",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsDedicatedPrice(routerOutput, decisionText) && !customerAnswerHasAllDedicatedPrices(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "数据中心独享型静态 IP：5M 300 元/个/月，10M 500 元/个/月，20M 800 元/个/月；独享型不参与数量折扣。",
			AnswerMode:            "evidence",
			Reason:                "dedicated_price_complete_table",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsSharedDatacenterQuantity50Price(routerOutput, decisionText) && !customerAnswerHasSharedDatacenterQuantity50Terms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "共享型 50 个按 21-50 个档位 8 折。数据中心 5M 是 25 * 50 * 0.8 = 1000 元/月，折后 20 元/个/月；10M 是 24 元/个/月，20M 是 56 元/个/月。",
			AnswerMode:            "evidence",
			Reason:                "shared_datacenter_quantity_50_discount_terms",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsStaticIPQuantity100Discount(routerOutput, decisionText) && !customerAnswerHasStaticIPQuantity100DiscountTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "共享型静态 IP 100 个属于 51-100 个档位，可享 7折；独享型不参与数量折扣。具体下单金额还要按您选择的带宽和页面实时价格确认。",
			AnswerMode:            "evidence",
			Reason:                "static_ip_quantity_100_discount_terms",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsOverseasIPPrice(routerOutput, decisionText) && !customerAnswerHasOverseasIPPriceBoundary(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "海外 IP 价格需要按国家地区、购买时长和数量在页面确认，不能直接给固定价格。您可以先说明需要的国家地区和购买时长，我再帮您判断该看哪个入口。",
			AnswerMode:            "mixed",
			Reason:                "overseas_ip_price_boundary",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsDatacenterResidentialPriceCompare(routerOutput, decisionText) && !customerAnswerHasDatacenterResidentialPriceCompareTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "同带宽下数据中心 IP 通常更便宜：5M 数据中心 25 元/个/月、住宅 30 元/个/月；10M 数据中心 30 元/个/月、住宅 50 元/个/月；20M 数据中心 70 元/个/月、住宅 70 元/个/月。",
			AnswerMode:            "evidence",
			Reason:                "datacenter_residential_price_compare_terms",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsNewUserSelection(routerOutput, decisionText) && len(customerUnsafeVisibleAnswerHits(answer)) > 0:
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "如果需要频繁换出口，先看动态 IP；需要固定地区或长期账号环境，先看静态 IP；海外平台场景再看海外 IP，并先确认使用环境。",
			AnswerMode:            "evidence",
			Reason:                "new_user_selection_neutralized",
			FallbackSources:       customerNewUserSelectionFallbackSources(),
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsNewUserSelection(routerOutput, decisionText) && (!customerAnswerHasNewUserSelectionBoundary(answer) || len(parsed.Sources) == 0):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "如果需要频繁换出口，先看动态 IP；需要固定地区或长期账号环境，先看静态 IP；海外平台场景再看海外 IP，并先确认使用环境。",
			AnswerMode:            "evidence",
			Reason:                "new_user_selection_complete_boundary",
			FallbackSources:       customerNewUserSelectionFallbackSources(),
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsLongTermAccountSelection(routerOutput, decisionText) && !customerAnswerHasLongTermAccountSelectionTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "长期账号环境通常优先看静态 IP，因为出口在购买期内相对固定；但平台风控还受账号行为、平台规则和网络环境影响，不能承诺账号结果。",
			AnswerMode:            "mixed",
			Reason:                "long_term_account_selection_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPlatformLocationSelection(routerOutput, decisionText) && (!customerAnswerHasPlatformLocationSelectionTerms(answer) || customerAnswerHasPlatformLocationSelectionForbiddenTerms(answer)):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "改抖音 IP 归属地这类场景，更建议先看静态 IP；要相对稳定城市出口可看数据中心静态 IP，想更贴近家庭宽带场景可看住宅 IP。平台显示可能会有延迟，也会受平台 IP 库影响。",
			AnswerMode:            "evidence",
			Reason:                "platform_location_selection_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/comparisons/si-ye-tian-platform-scenario-selection.md", Confidence: "high"}, {Path: "wiki/comparisons/dynamic-vs-static-ip.md", Confidence: "high"}},
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsDynamicStaticCompare(routerOutput, decisionText) && !customerAnswerHasDynamicStaticCompareTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "动态 IP 会按连接或时效变化，适合频繁换出口的短时任务；静态 IP 在购买期内相对固定，适合固定地区、长期账号环境或白名单绑定。",
			AnswerMode:            "evidence",
			Reason:                "dynamic_static_compare_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsDatacenterResidentialCompare(routerOutput, decisionText) && !customerAnswerHasDatacenterResidentialBoundary(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "数据中心 IP 来自机房，通常更经济、适合一般固定出口；住宅 IP 来自家庭宽带，更接近真实用户环境，但可能有同城轮换和轻微波动。平台结果还会受平台规则、账号行为和网络环境影响。",
			AnswerMode:            "evidence",
			Reason:                "datacenter_residential_boundary",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsSharedDedicatedSelection(req, routerOutput, decisionText) && !customerAnswerHasSharedDedicatedSelectionTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "关注成本和批量购买时看共享型；更在意带宽独享、直播或游戏体验时可看独享型。实际体验还会受节点、本地网络和平台影响。",
			AnswerMode:            "evidence",
			Reason:                "shared_dedicated_selection_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsDataCollectionSelection(routerOutput, decisionText) && !customerAnswerHasDataCollectionSelectionTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "数据采集这类短时、频繁换出口的任务通常先看动态 IP；如果业务需要固定出口或白名单绑定，再看静态 IP。",
			AnswerMode:            "evidence",
			Reason:                "data_collection_selection_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsLongTermResidentialFixed(routerOutput, decisionText) && !customerAnswerHasLongTermResidentialBoundary(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "住宅静态 IP 更接近家庭宽带场景，但不承诺完全固定，可能在同一城市范围内轮换；如果需要更固定的出口，可优先看数据中心静态 IP。",
			AnswerMode:            "evidence",
			Reason:                "long_term_residential_fixed_boundary",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsProxyExitIPCapability(req, routerOutput) && !customerAnswerHasProxyExitIPCapabilityTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "可以。配置代理后，您电脑访问目标网站时对外呈现的是代理出口 IP，目标网站看到的是代理出口，而不是您本地网络的原始出口。它不是修改电脑本机的内网 IP，而是让走代理的访问流量从代理 IP 出口出去。",
			AnswerMode:            "evidence",
			Reason:                "proxy_exit_ip_capability_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/knowledge/si-ye-tian-proxy-ip-products.md", Confidence: "high"}},
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsGenericIPChangeClarification(routerOutput, decisionText) && !customerAnswerHasGenericIPChangeClarification(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "请问您指的是哪类产品要改 IP：动态 IP、静态 IP 还是住宅 IP？不同产品的切换方式不同，我确认产品后再按对应方式说明。",
			AnswerMode:     "clarification",
			Reason:         "generic_ip_change_product_clarification",
			MinConfidence:  0.8,
			ReviewRequired: false,
		}
	case customerScenarioIsVagueOperationQuestion(routerOutput, decisionText) && !customerAnswerHasVagueOperationClarification(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "请问您指的是哪个产品或哪一步操作？比如购买、下载、连接、配置或售后处理。",
			AnswerMode:     "clarification",
			Reason:         "vague_operation_clarification",
			MinConfidence:  0.8,
			ReviewRequired: false,
		}
	case customerScenarioIsReceptionUnknownQuestion(routerOutput, decisionText) && !customerAnswerHasReceptionDirections(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "您可以先说想解决什么问题，比如换 IP、购买、配置还是售后。",
			AnswerMode:     "self_answer",
			Reason:         "reception_unknown_question_directions",
			MinConfidence:  0.8,
			ReviewRequired: false,
		}
	case customerScenarioIsResidentialPurchase(req, routerOutput, decisionText) && !customerAnswerHasResidentialPurchaseEntry(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "住宅 IP 可到 https://www.siyetian.com/product/box.html 查看购买入口，实际可售城市和规格以当前页面为准。",
			AnswerMode:            "evidence",
			Reason:                "residential_purchase_entry",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.85,
		}
	case customerScenarioIsResidentialPurchaseSpecs(routerOutput, decisionText) && !customerAnswerHasResidentialPurchaseSpecs(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "住宅 IP 的规格可以在购买页面查看，页面会展示当前可选城市、带宽或套餐选项；实际可售规格以当前页面为准。",
			AnswerMode:            "evidence",
			Reason:                "residential_purchase_specs",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.85,
		}
	case customerScenarioIsGenericPurchaseQuestion(routerOutput, decisionText) && !customerAnswerHasGenericPurchaseClarification(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "请问您想购买哪类产品：动态 IP、静态 IP、住宅 IP 还是海外 IP？确认产品后我再给您对应入口。",
			AnswerMode:     "clarification",
			Reason:         "generic_purchase_product_clarification",
			MinConfidence:  0.8,
			ReviewRequired: false,
		}
	case customerScenarioIsMobileAppDownload(req, routerOutput, decisionText) && (!customerAnswerHasMobileAppDownloadTerms(answer) || len(parsed.Sources) == 0):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "手机端请先到 App Store 搜索四叶天下载；安卓用户可到官网 https://www.siyetian.com/download.html 查看安卓下载入口。安装后登录账号，再按页面选择产品和连接方式。",
			AnswerMode:            "mixed",
			Reason:                "mobile_app_download_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/procedures/si-ye-tian-download-installation.md", Confidence: "high"}},
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsTrialClaim(routerOutput, decisionText) && !customerAnswerHasTrialClaimEntry(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "可以先到 https://www.siyetian.com/test/index.html 查看测试入口；注册并完成认证后，看页面是否有可领取的测试权益。",
			AnswerMode:            "evidence",
			Reason:                "trial_claim_entry",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.85,
		}
	case customerScenarioIsTrialPackageMissing(routerOutput, decisionText) && !customerAnswerHasTrialPackageMissingSteps(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "先刷新页面或重新登录，确认是否已完成注册和实名认证；如果仍未显示，以测试入口和个人中心当前显示的权益状态为准。",
			AnswerMode:            "evidence",
			Reason:                "trial_package_missing_steps",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPurchasedResourceMissing(routerOutput, decisionText) && !customerAnswerHasPurchasedResourceMissingSteps(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "先刷新页面或重新登录，再到个人中心或对应产品后台查看资源；如果仍未显示，以订单状态和对应产品后台当前开通状态为准。",
			AnswerMode:            "mixed",
			Reason:                "purchased_resource_missing_steps",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPurchasedResourceView(routerOutput, decisionText) && (!customerAnswerHasPurchasedResourceViewTerms(answer) || customerAnswerHasPurchasedResourceViewForbiddenTerms(answer)):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "购买后可在个人中心或对应产品后台查看资源；如果没有显示，先刷新页面或重新登录，再以订单状态和对应产品后台当前开通状态为准。",
			AnswerMode:            "mixed",
			Reason:                "purchased_resource_view_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsBasicConnectionTroubleshooting(routerOutput, decisionText) && !customerAnswerHasBasicConnectionTroubleshootingTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "先核对代理地址、端口、账号密码和协议类型；如果使用白名单，确认当前出口公网 IP 已加入白名单。仍连不上时，请记录具体错误码再继续排查。",
			AnswerMode:            "evidence",
			Reason:                "basic_connection_troubleshooting_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsHTTP503Troubleshooting(routerOutput, decisionText) && !customerAnswerHasHTTP503TroubleshootingTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "代理提示 503 时，先核对协议、IP、端口和认证信息是否一致，再换一个目标网站测试；如果仍是 503，记录报错截图或时间点，并按当前资源状态继续排查。",
			AnswerMode:            "mixed",
			Reason:                "http_503_troubleshooting_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsOverseasConnectionTimeout(routerOutput, decisionText) && !customerAnswerHasOverseasConnectionTimeoutTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "海外 IP 连接超时先确认海外网络环境是否可用，再核对代理地址、端口、账号密码和产品有效期；国内本地环境不能简单理解为购买后即可直连。",
			AnswerMode:            "evidence",
			Reason:                "overseas_connection_timeout_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsStaticIPTroubleshootingFollowup(routerOutput, decisionText) && !customerAnswerHasStaticIPTroubleshootingTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "静态 IP 不能用时，先核对代理协议、地址和端口，再确认认证信息或白名单是否正确；连接后可用 https://www.ip138.com/ 查看当前出口 IP 是否已变化。",
			AnswerMode:            "evidence",
			Reason:                "static_ip_troubleshooting_followup_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsIPNotChanged(routerOutput, decisionText) && !customerAnswerHasIPNotChangedSteps(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "先确认代理已连接并且当前请求确实走代理，再用浏览器打开 https://www.ip138.com/ 或 https://www.ipip.net/ 查出口 IP。动态 IP 可以重新提取或断开重连后再测。",
			AnswerMode:            "evidence",
			Reason:                "ip_not_changed_steps",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsOverseasIPSwitchUnsupported(routerOutput, decisionText) && !customerAnswerHasOverseasIPSwitchUnsupportedTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "海外 IP 不支持切换 IP。如果当前海外 IP 无法满足使用需求，请以产品页面实际规则为准，或按页面重新购买其他海外 IP 资源。",
			AnswerMode:            "evidence",
			Reason:                "overseas_ip_switch_unsupported",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.85,
		}
	case customerScenarioIsAPIExtraction(req, routerOutput) && !customerAnswerHasAPIExtractionTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "API 入口是 https://www.siyetian.com/apis.html ；使用前先开通账号密码认证，并把服务器出口公网 IP 加到白名单，再按接口返回的代理地址、端口和认证信息接入。",
			AnswerMode:            "evidence",
			Reason:                "api_extraction_terms",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.85,
		}
	case customerScenarioIsPythonProxyIntegration(routerOutput, decisionText) && (!customerAnswerHasPythonProxyIntegrationTerms(answer) || customerAnswerHasPythonProxyIntegrationForbiddenTerms(answer)):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "Python 接入时按所用代理协议填写主机、端口、账号和密码；具体第三方工具/代码接入说明可看 https://www.siyetian.com/help/28/55.html。这里不补未给出的固定参数示例。",
			AnswerMode:            "evidence",
			Reason:                "python_proxy_integration_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/procedures/si-ye-tian-third-party-tool-configuration.md", Confidence: "high"}},
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsFingerprintBrowserConfig(routerOutput, decisionText) && !customerAnswerHasFingerprintBrowserConfigTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "指纹浏览器里一般按字段填写代理协议、主机、端口和认证信息；主机填获取到的代理地址，认证信息按页面给出的账号密码或提取结果填写。",
			AnswerMode:            "mixed",
			Reason:                "fingerprint_browser_config_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsAccountPasswordAuth(req, routerOutput) && !customerAnswerHasAccountPasswordAuthTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "账号密码认证可在 API 页面 https://www.siyetian.com/apis.html 查看或开启。配置代理时填写代理地址、端口、账号和密码即可，不要把密码发给客服。",
			AnswerMode:            "mixed",
			Reason:                "account_password_auth_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsWhitelistSetup(req, routerOutput) && !customerAnswerHasWhitelistSetupTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "白名单在 https://www.siyetian.com/member/whitelist.html 设置，把当前服务器或电脑的出口公网 IP 填进去并保存即可。",
			AnswerMode:            "evidence",
			Reason:                "whitelist_setup_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/procedures/si-ye-tian-api-whitelist-setup.md", Confidence: "high"}},
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsStaticIPSpecifyAddress(req, routerOutput) && !customerAnswerHasStaticIPSpecifyAddressTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "静态 IP 支持按后台可用资源手动切换，但不能承诺指定到某一个具体 IP；切换结果以后台当前可用资源为准。",
			AnswerMode:            "evidence",
			Reason:                "static_ip_specify_address_boundary",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsStaticIPRegionSwitch(req, routerOutput) && !customerAnswerHasStaticIPRegionSwitchTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "静态 IP 可按后台可用资源切换地区或线路；如果页面当前没有目标地区，就以后台可选项为准。",
			AnswerMode:            "evidence",
			Reason:                "static_ip_region_switch_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsStaticIPSwitch(req, routerOutput) && (!customerAnswerHasStaticIPSwitchTerms(answer) || customerAnswerHasStaticIPSwitchForbiddenTerms(answer)):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "静态 IP 支持手动切换，每月 5 次。可登录后台进入 https://www.siyetian.com/member/staticip.html 按页面操作；切换结果以后台可用资源为准。",
			AnswerMode:            "evidence",
			Reason:                "static_ip_switch_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsSubnetGatewayConfig(routerOutput, decisionText) && !customerAnswerHasSubnetGatewayConfigTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "子网掩码和网关属于设备网络配置项；使用四叶天代理时通常先按代理协议填写代理地址和端口，再根据工具提示补充账号密码等认证信息。",
			AnswerMode:            "mixed",
			Reason:                "subnet_gateway_config_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsAppDynamicMobile(req, routerOutput) && !customerAnswerHasAppDynamicMobileTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "动态 IP 手机端可以按当前 App 或教程支持的方式使用；先下载四叶天 App，再按页面选择套餐、认证方式和代理配置。具体字段以当前页面为准。",
			AnswerMode:            "mixed",
			Reason:                "app_dynamic_mobile_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsBalanceWithdraw(routerOutput, decisionText) && !customerAnswerHasBalanceWithdrawTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "账户余额主要用于平台内购买产品和服务，一般不支持直接提现；特殊情况以当前页面和规则说明为准。",
			AnswerMode:            "evidence",
			Reason:                "balance_withdraw_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsRechargeNotReceived(routerOutput, decisionText) && !customerAnswerHasRechargeNotReceivedTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "先确认支付是否成功，并在个人中心查看余额或订单状态；也可以刷新页面或重新登录后再看。支付多笔、充值未到账或苹果订单未到账，需要按支付记录和订单状态核实。",
			AnswerMode:            "evidence",
			Reason:                "recharge_not_received_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/procedures/si-ye-tian-payment-invoice-recharge.md", Confidence: "high"}},
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsAccountVerification(routerOutput, decisionText) && (!customerAnswerHasAccountVerificationTerms(answer) || len(parsed.Sources) == 0):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "需要先完成实名认证。个人认证可登录后看 https://www.siyetian.com/authent/index.html，企业认证可看 https://www.siyetian.com/authent_company/index.html。",
			AnswerMode:            "evidence",
			Reason:                "account_verification_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/procedures/si-ye-tian-account-verification.md", Confidence: "high"}},
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsExpiredStaticRenewal(routerOutput, decisionText) && !customerAnswerHasExpiredStaticRenewalTermsForRequest(req, decisionText, answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                customerExpiredStaticRenewalAnswer(req, decisionText),
			AnswerMode:            "evidence",
			Reason:                "expired_static_renewal_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/procedures/si-ye-tian-renewal-upgrade-procedure.md", Confidence: "high"}, {Path: "wiki/procedures/si-ye-tian-static-ip-usage.md", Confidence: "high"}},
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPackageChange(routerOutput, decisionText) && customerAnswerIsClarificationOnly(parsed, answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "买错套餐需要按订单状态确认是否能调整方案或多退少补。",
			AnswerMode:            "evidence",
			Reason:                "package_change_boundary_answer",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPackageChange(routerOutput, decisionText) && !customerAnswerHasPackageChangeTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "买错套餐需要按订单状态确认是否能调整方案或多退少补。",
			AnswerMode:            "evidence",
			Reason:                "package_change_boundary_answer",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPackageChange(routerOutput, decisionText) && customerAnswerHasForbiddenPackageChangePhrase(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "买错套餐需要按订单状态确认是否能调整方案或多退少补。",
			AnswerMode:            "evidence",
			Reason:                "package_change_remove_forbidden_phrase",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPackageChange(routerOutput, decisionText) && customerAnswerHasHumanContactGuidance(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "买错套餐需要按订单状态确认是否能调整方案或多退少补；是否能保留原 IP 也以当前后台资源状态和订单状态为准。",
			AnswerMode:            "evidence",
			Reason:                "package_change_remove_human_contact_guidance",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPaymentMethod(routerOutput, decisionText) && !customerAnswerHasPaymentMethodTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "官网或 App 下单可选择微信支付或支付宝；企业对公打款可登录后查看 https://www.siyetian.com/member/recharge.html，实际账户信息以页面为准。",
			AnswerMode:            "evidence",
			Reason:                "payment_method_terms",
			FallbackSources:       []customerChatSource{{Path: "wiki/procedures/si-ye-tian-payment-invoice-recharge.md", Confidence: "high"}},
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}
	case customerScenarioIsBandwidthUpgrade(routerOutput, decisionText) && !customerAnswerHasBandwidthUpgradeTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "5M、10M、20M 之间能否升级取决于资源和产品类型，可能需要补差价、重新开通或更换 IP，具体以当前资源和订单状态为准。",
			AnswerMode:            "evidence",
			Reason:                "bandwidth_upgrade_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsRefund(routerOutput, decisionText) && (customerAnswerAsksForOrderInfo(answer) || customerAnswerHasForbiddenRefundPhrase(answer)):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "退款条件、金额和时效需要人工按订单状态确认；您可以联系人工客服处理。",
			AnswerMode:            "evidence",
			Reason:                "refund_remove_order_request",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsOverseasAccessGoogle(routerOutput, decisionText) && !customerAnswerHasOverseasAccessGoogleTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "不能承诺海外 IP 一定能打开 Google。是否可用还要结合海外网络环境、目标站点策略和实际测试；国内本地环境不能简单理解为购买后即可直连。",
			AnswerMode:            "mixed",
			Reason:                "overseas_access_google_boundary",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsChatGPTDomesticOverseasIP(routerOutput, decisionText) && !customerAnswerHasChatGPTDomesticOverseasIPTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "国内本地环境不能承诺购买海外 IP 后即可访问 ChatGPT；是否可用还受海外网络环境、平台策略和代理检测影响。",
			AnswerMode:            "mixed",
			Reason:                "chatgpt_domestic_overseas_ip_boundary",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsNormalUseAfterPlatformRisk(routerOutput, decisionText) && !customerAnswerHasNormalUseBoundary(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "正常用途可以围绕合法合规的业务访问、固定出口、白名单绑定、数据采集或连接排障来咨询；涉及平台风控、账号结果或规避检测的内容不能承诺。",
			AnswerMode:            "mixed",
			Reason:                "normal_use_after_platform_risk",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsPlatformRiskGuarantee(routerOutput, decisionText) && !customerAnswerHasPlatformRiskGuaranteeTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "不能承诺一定不被平台风控或保证账号结果；平台判断会受自身规则、账号行为和网络环境影响。",
			AnswerMode:     "mixed",
			Reason:         "platform_risk_guarantee_boundary",
			MinConfidence:  0.85,
			ReviewRequired: false,
		}
	case customerScenarioIsBulkRegisterAntiBan(routerOutput, decisionText) && !customerAnswerHasBulkRegisterAntiBanTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "不能提供批量注册、规避平台检测或防封的方法。",
			AnswerMode:     "refusal",
			Reason:         "bulk_register_antiban_refusal",
			MinConfidence:  0.9,
			ReviewRequired: false,
		}
	case customerScenarioIsXiaohongshuRiskAvoidance(routerOutput, decisionText) && !customerAnswerHasXiaohongshuRiskAvoidanceTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "不能承诺避免小红书风控或保证账号结果；平台判断会受自身规则、账号行为和网络环境影响。",
			AnswerMode:     "mixed",
			Reason:         "xiaohongshu_risk_avoidance_boundary",
			MinConfidence:  0.85,
			ReviewRequired: false,
		}
	case customerScenarioIsPlatformAntiBan(routerOutput, decisionText) && !customerAnswerHasPlatformAntiBanTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "不能提供规避平台风控、防封或绕检测的方法。",
			AnswerMode:     "refusal",
			Reason:         "platform_antiban_refusal",
			MinConfidence:  0.9,
			ReviewRequired: false,
		}
	case customerScenarioIsVotingBoost(routerOutput, decisionText) && !customerAnswerHasVotingBoostTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "不能提供投票、刷量等违反平台规则的使用方法。",
			AnswerMode:     "refusal",
			Reason:         "voting_boost_refusal",
			MinConfidence:  0.9,
			ReviewRequired: false,
		}
	case customerScenarioIsClashShadowrocketVPN(routerOutput, decisionText) && (!customerAnswerHasClashShadowrocketVPNTerms(answer) || customerAnswerHasClashShadowrocketVPNForbiddenTerms(answer)):
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "不能提供 Clash、小火箭、VPN 等违规跨境联网工具的配置或教程。",
			AnswerMode:     "refusal",
			Reason:         "clash_shadowrocket_vpn_refusal",
			MinConfidence:  0.9,
			ReviewRequired: false,
		}
	case customerScenarioIsLegalProxySafety(routerOutput, decisionText) && !customerAnswerHasLegalProxySafetyTerms(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "代理 IP 是工具，合法合规使用是前提；具体风险还会受法律法规、目标平台规则和使用方式影响，不能简单承诺零风险。",
			AnswerMode:            "evidence",
			Reason:                "legal_proxy_safety_terms",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	case customerScenarioIsTunnelIPSec(routerOutput, decisionText) && !customerAnswerHasTunnelIPSecBoundary(answer):
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "不能把动态 IP、静态 IP 或 API 提取说成隧道代理；当前可确认的代理协议主要是 HTTP 和 SOCKS5，IPSec 不作为支持能力承诺。",
			AnswerMode:            "evidence",
			Reason:                "tunnel_ipsec_boundary_answer",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.8,
		}
	default:
		return customerScenarioAnswerGuardResult{Reason: "pass"}
	}
}

func customerScenarioGuardUsesEvidence(result customerScenarioAnswerGuardResult) bool {
	return normalizedAnswerMode(result.AnswerMode) == "evidence" || normalizedAnswerMode(result.AnswerMode) == "mixed"
}

func customerScenarioHardGuardResult(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, decisionText string, answer string) (customerScenarioAnswerGuardResult, bool) {
	if strings.TrimSpace(answer) == "" {
		return customerScenarioAnswerGuardResult{Reason: "empty_answer"}, true
	}
	if customerScenarioIsOverseasIPSwitchUnsupported(routerOutput, decisionText) &&
		!customerAnswerHasOverseasIPSwitchUnsupportedTerms(answer) {
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "海外 IP 不支持切换 IP。如果当前海外 IP 无法满足使用需求，请以产品页面实际规则为准，或按页面重新购买其他海外 IP 资源。",
			AnswerMode:            "evidence",
			Reason:                "overseas_ip_switch_unsupported",
			FallbackSources:       []customerChatSource{{Path: "wiki/knowledge/si-ye-tian-overseas-ip.md", Confidence: "high"}},
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.85,
		}, true
	}
	if customerScenarioIsDedicatedPrice(routerOutput, decisionText) &&
		customerScenarioDedicatedPriceRequiresCompleteTable(routerOutput, decisionText) &&
		!customerAnswerHasAllDedicatedPrices(answer) {
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "数据中心独享型静态 IP：5M 300 元/个/月，10M 500 元/个/月，20M 800 元/个/月；独享型不参与数量折扣。",
			AnswerMode:            "evidence",
			Reason:                "dedicated_price_complete_table",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}, true
	}
	if customerScenarioIsSharedDatacenterQuantity50Price(routerOutput, decisionText) &&
		customerScenarioSharedDatacenterQuantity50RequiresDiscountBasis(routerOutput, decisionText) &&
		!customerAnswerHasSharedDatacenterQuantity50Terms(answer) {
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "共享型 50 个按 21-50 个档位 8 折。数据中心 5M 是 25 * 50 * 0.8 = 1000 元/月，折后 20 元/个/月；10M 是 24 元/个/月，20M 是 56 元/个/月。",
			AnswerMode:            "evidence",
			Reason:                "shared_datacenter_quantity_50_discount_terms",
			MinConfidence:         0.85,
			MinEvidenceConfidence: 0.9,
		}, true
	}
	if customerScenarioIsRefund(routerOutput, decisionText) &&
		(customerAnswerAsksForOrderInfo(answer) || customerAnswerHasForbiddenRefundPhrase(answer)) {
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "退款需要按订单状态和售后规则确认，不能直接承诺一定退款。您可以说明遇到的问题和订单状态，我先帮您判断应按哪类售后流程处理。",
			AnswerMode:            "mixed",
			Reason:                "refund_boundary",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.7,
		}, true
	}
	if customerScenarioIsPackageChange(routerOutput, decisionText) &&
		(customerAnswerHasForbiddenPackageChangePhrase(answer) || customerAnswerIsClarificationOnly(parsed, answer)) {
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "套餐变更、升级或保留原 IP 需要以后台当前订单和可操作项为准，不能直接承诺一定可改或一定保留原 IP。您可以先说明当前产品、想调整的套餐和是否已到期，我再按规则说明可确认的部分。",
			AnswerMode:            "mixed",
			Reason:                "package_change_boundary",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.7,
		}, true
	}
	if customerScenarioIsPlatformAntiBan(routerOutput, decisionText) ||
		customerScenarioIsBulkRegisterAntiBan(routerOutput, decisionText) ||
		customerScenarioIsVotingBoost(routerOutput, decisionText) ||
		customerScenarioIsClashShadowrocketVPN(routerOutput, decisionText) {
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "这类用途涉及规避平台规则或违规联网配置，不能提供操作方案。可以帮您说明代理 IP 的合规使用方式、产品差异或常规连接配置。",
			AnswerMode:     "refusal",
			Reason:         "safety_boundary",
			MinConfidence:  0.9,
			ReviewRequired: false,
		}, true
	}
	if customerScenarioIsOverseasAccessGoogle(routerOutput, decisionText) &&
		!customerAnswerHasOverseasAccessGoogleTerms(answer) {
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "海外 IP 只能提供海外代理出口能力，不能承诺一定可以访问 Google；实际访问还受本地网络、目标网站策略和使用环境影响。",
			AnswerMode:            "mixed",
			Reason:                "overseas_google_boundary",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.7,
		}, true
	}
	if customerScenarioIsChatGPTDomesticOverseasIP(routerOutput, decisionText) &&
		!customerAnswerHasChatGPTDomesticOverseasIPTerms(answer) {
		return customerScenarioAnswerGuardResult{
			Triggered:             true,
			Answer:                "国内网络环境下不能保证通过海外 IP 正常访问 ChatGPT；海外 IP 只提供代理出口能力，实际访问还受本地网络、目标服务策略和账号环境影响。",
			AnswerMode:            "mixed",
			Reason:                "chatgpt_domestic_overseas_ip_boundary",
			MinConfidence:         0.8,
			MinEvidenceConfidence: 0.7,
		}, true
	}
	return customerScenarioAnswerGuardResult{}, false
}

func customerScenarioShouldPassThroughModelAnswer(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, decisionText string, answer string) bool {
	if strings.TrimSpace(answer) == "" || routerOutput == nil {
		return false
	}
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if mode == "clarification" || mode == "refusal" {
		return false
	}
	if parsed.Confidence < 0.78 || parsed.EvidenceConfidence < 0.70 || len(parsed.Sources) == 0 {
		return false
	}
	if customerScenarioAnswerNeedsHardGuard(req, parsed, routerOutput, decisionText, answer) {
		return false
	}
	input := newCustomerScenarioGuardInput(req, routerOutput)
	if input.ProductLock != "" {
		outputProducts := customerScenarioAnswerOutputProducts(answer)
		allowed := customerScenarioAllowedProductsForResult(input, customerScenarioAnswerGuardResult{Answer: answer})
		if len(outputProducts) > 0 && !customerScenarioProductsAllowed(outputProducts, allowed) {
			return false
		}
	}
	return true
}

func customerScenarioAnswerNeedsHardGuard(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, decisionText string, answer string) bool {
	if customerAnswerLooksLikeInternalBoundary(answer) ||
		len(customerUnsafeVisibleAnswerHits(answer)) > 0 ||
		customerAnswerHasHumanContactGuidance(answer) {
		return true
	}
	if customerScenarioIsRefund(routerOutput, decisionText) &&
		(customerAnswerAsksForOrderInfo(answer) || customerAnswerHasForbiddenRefundPhrase(answer)) {
		return true
	}
	if customerScenarioIsPackageChange(routerOutput, decisionText) &&
		(customerAnswerHasForbiddenPackageChangePhrase(answer) || customerAnswerHasHumanContactGuidance(answer) || customerAnswerIsClarificationOnly(parsed, answer)) {
		return true
	}
	if customerScenarioIsOverseasIPSwitchUnsupported(routerOutput, decisionText) &&
		!customerAnswerHasOverseasIPSwitchUnsupportedTerms(answer) {
		return true
	}
	if customerScenarioIsDedicatedPrice(routerOutput, decisionText) &&
		customerScenarioDedicatedPriceRequiresCompleteTable(routerOutput, decisionText) &&
		!customerAnswerHasAllDedicatedPrices(answer) {
		return true
	}
	if customerScenarioIsSharedDatacenterQuantity50Price(routerOutput, decisionText) &&
		customerScenarioSharedDatacenterQuantity50RequiresDiscountBasis(routerOutput, decisionText) &&
		!customerAnswerHasSharedDatacenterQuantity50Terms(answer) {
		return true
	}
	if customerScenarioIsPlatformRiskGuarantee(routerOutput, decisionText) &&
		!customerAnswerHasPlatformRiskGuaranteeTerms(answer) {
		return true
	}
	if customerScenarioIsPlatformAntiBan(routerOutput, decisionText) ||
		customerScenarioIsBulkRegisterAntiBan(routerOutput, decisionText) ||
		customerScenarioIsVotingBoost(routerOutput, decisionText) ||
		customerScenarioIsClashShadowrocketVPN(routerOutput, decisionText) {
		return true
	}
	if customerScenarioIsPlatformLocationSelection(routerOutput, decisionText) &&
		customerAnswerHasPlatformLocationSelectionForbiddenTerms(answer) {
		return true
	}
	if customerScenarioIsLiveStreamingSelection(req, routerOutput, decisionText) &&
		customerAnswerHasLiveStreamingSelectionForbiddenTerms(answer) {
		return true
	}
	if customerScenarioIsPythonProxyIntegration(routerOutput, decisionText) &&
		customerAnswerHasPythonProxyIntegrationForbiddenTerms(answer) {
		return true
	}
	if customerScenarioIsStaticIPSwitch(req, routerOutput) &&
		customerAnswerHasStaticIPSwitchForbiddenTerms(answer) {
		return true
	}
	if customerScenarioIsOverseasAccessGoogle(routerOutput, decisionText) &&
		!customerAnswerHasOverseasAccessGoogleTerms(answer) {
		return true
	}
	if customerScenarioIsChatGPTDomesticOverseasIP(routerOutput, decisionText) &&
		!customerAnswerHasChatGPTDomesticOverseasIPTerms(answer) {
		return true
	}
	return false
}

func customerScenarioDedicatedPriceRequiresCompleteTable(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil {
		return false
	}
	compact := normalizeCustomerScenarioCompactText(strings.Join([]string{
		text,
		routerOutput.Intent,
		routerOutput.RewrittenQuestion,
		routerOutput.RoutingReason,
		routerOutput.HandoffNotes,
		strings.Join(routerOutput.RetrievalQueries, " "),
	}, " "))
	return containsAny(compact, "完整三档", "三档", "5m10m20m", "5m、10m、20m", "5m/10m/20m") ||
		(routerOutput.Slots.StaticType == "dedicated" && customerScenarioStaticBandwidth(routerOutput, text) == "")
}

func customerScenarioSharedDatacenterQuantity50RequiresDiscountBasis(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil {
		return false
	}
	compact := normalizeCustomerScenarioCompactText(strings.Join([]string{
		text,
		routerOutput.Intent,
		routerOutput.RewrittenQuestion,
		routerOutput.RoutingReason,
		routerOutput.HandoffNotes,
		strings.Join(routerOutput.RetrievalQueries, " "),
	}, " "))
	return containsAny(compact, "50个", "50个数量", "50个的价格", "50个价格", "8折", "折扣", "折后")
}

func customerScenarioGuardProductLocked(req CustomerChatRequest, routerOutput *CustomerRouterOutput, result customerScenarioAnswerGuardResult) customerScenarioAnswerGuardResult {
	if !result.Triggered || result.Answer == "" || result.Blocked {
		return result
	}
	input := newCustomerScenarioGuardInput(req, routerOutput)
	if input.ProductLock == "" {
		return result
	}
	outputProducts := customerScenarioAnswerOutputProducts(result.Answer)
	result.OutputProducts = outputProducts
	allowed := customerScenarioAllowedProductsForResult(input, result)
	result.AllowedProducts = allowed
	if len(outputProducts) == 0 || customerScenarioProductsAllowed(outputProducts, allowed) {
		return result
	}
	result.Triggered = false
	result.Blocked = true
	result.BlockReason = "blocked_by_product_lock"
	result.Reason = firstNonEmpty(result.Reason, "blocked_by_product_lock")
	result.Answer = ""
	result.AnswerMode = ""
	result.ReviewRequired = false
	result.ReviewReason = ""
	result.FallbackSources = nil
	result.MinConfidence = 0
	result.MinEvidenceConfidence = 0
	return result
}

func customerScenarioAllowedProductsForResult(input customerScenarioGuardInput, result customerScenarioAnswerGuardResult) []string {
	allowed := map[string]bool{}
	add := func(product string) {
		product = strings.TrimSpace(product)
		if product != "" && product != "unknown" {
			allowed[product] = true
		}
	}
	add(input.ProductLock)
	if input.Router != nil {
		add(input.Router.Slots.PrimaryProduct)
		for _, product := range input.Router.Slots.Products {
			add(product)
		}
		if input.Router.Slots.IPType == "residential" {
			add("residential_ip")
		}
		if input.Router.Slots.IPType == "overseas" {
			add("overseas_ip")
		}
		if input.Router.Slots.StaticType == "dedicated" {
			add("dedicated_static_ip")
		}
		if input.Router.Slots.StaticType == "shared" {
			add("static_ip")
			add("shared_static_ip")
		}
	}
	for _, product := range result.AllowedProducts {
		add(product)
	}
	current := input.CurrentText
	intent := input.IntentText
	if customerRouterTextHasDynamicCue(current) || containsAny(intent, "dynamic_ip", "dynamic") {
		add("dynamic_ip")
	}
	if customerRouterTextHasOverseasCue(current) || containsAny(intent, "overseas_ip", "overseas") {
		add("overseas_ip")
	}
	if customerRouterTextHasResidentialCue(current) || containsAny(intent, "residential_ip", "residential") {
		add("residential_ip")
	}
	if containsAny(current, "独享") || containsAny(intent, "dedicated") {
		add("dedicated_static_ip")
		add("static_ip")
	}
	if containsAny(current, "共享") || containsAny(intent, "shared") {
		add("shared_static_ip")
		add("static_ip")
	}
	if customerScenarioResultAllowsProductExpansion(input, result) {
		for _, product := range customerScenarioAnswerOutputProducts(result.Answer) {
			add(product)
		}
	}
	out := make([]string, 0, len(allowed))
	for product := range allowed {
		out = append(out, product)
	}
	sort.Strings(out)
	return out
}

func customerScenarioResultAllowsProductExpansion(input customerScenarioGuardInput, result customerScenarioAnswerGuardResult) bool {
	if input.Router == nil {
		return false
	}
	if input.Router.Specialist != "product" && input.Router.Specialist != "pricing" {
		return false
	}
	if !customerScenarioGuardReasonCanCompareProducts(result.Reason) {
		return false
	}
	return customerScenarioGuardInputLooksLikeProductComparison(input)
}

func customerScenarioGuardReasonCanCompareProducts(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "ambiguous_dynamic_static_price_clarification",
		"datacenter_residential_price_compare_terms",
		"new_user_selection_neutralized",
		"new_user_selection_complete_boundary",
		"platform_location_selection_terms",
		"dynamic_static_compare_terms",
		"datacenter_residential_boundary",
		"shared_dedicated_selection_terms",
		"data_collection_selection_terms",
		"generic_purchase_product_clarification":
		return true
	default:
		return false
	}
}

func customerScenarioGuardInputLooksLikeProductComparison(input customerScenarioGuardInput) bool {
	if input.Router != nil && input.Router.QuestionStage == "product_selection" {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		input.CurrentText,
		input.IntentText,
	}, " ")))
	return containsAny(text,
		"product_selection",
		"selection",
		"compare",
		"vs",
		"对比",
		"区别",
		"差异",
		"怎么选",
		"选型",
		"选哪个",
		"应该买",
		"买哪个",
		"用哪",
		"哪种",
		"推荐",
	)
}

func customerScenarioAnswerOutputProducts(answer string) []string {
	text := normalizeCustomerReviewText(answer)
	products := map[string]bool{}
	if customerRouterTextHasDynamicCue(text) {
		products["dynamic_ip"] = true
	}
	if customerRouterTextHasOverseasCue(text) {
		products["overseas_ip"] = true
	}
	if customerRouterTextHasResidentialCue(text) {
		products["residential_ip"] = true
	}
	if customerRouterTextHasStaticCue(text) {
		products["static_ip"] = true
	}
	if containsAny(text, "独享静态", "独享ip", "独享 ip", "独享型") {
		products["dedicated_static_ip"] = true
		products["static_ip"] = true
	}
	if containsAny(text, "共享静态", "共享ip", "共享 ip", "共享型") {
		products["shared_static_ip"] = true
		products["static_ip"] = true
	}
	out := make([]string, 0, len(products))
	for product := range products {
		out = append(out, product)
	}
	sort.Strings(out)
	return out
}

func customerScenarioProductsAllowed(outputProducts []string, allowedProducts []string) bool {
	allowed := map[string]bool{}
	for _, product := range allowedProducts {
		allowed[strings.TrimSpace(product)] = true
	}
	for _, product := range outputProducts {
		product = strings.TrimSpace(product)
		if product == "" {
			continue
		}
		if allowed[product] {
			continue
		}
		if product == "shared_static_ip" && allowed["static_ip"] {
			continue
		}
		if product == "dedicated_static_ip" && allowed["static_ip"] {
			continue
		}
		return false
	}
	return true
}

func customerFallbackChatSourcesFromEvidence(sources []SourceRef) []customerChatSource {
	out := make([]customerChatSource, 0, len(sources))
	seen := map[string]bool{}
	for _, source := range sources {
		path := filepath.ToSlash(strings.TrimSpace(source.Path))
		if path == "" || seen[path] {
			continue
		}
		confidence := strings.ToLower(strings.TrimSpace(source.Confidence))
		switch confidence {
		case "low", "medium", "high":
		default:
			confidence = customerSourceConfidence(path)
		}
		out = append(out, customerChatSource{Path: path, Confidence: confidence})
		seen[path] = true
		if len(out) >= 2 {
			break
		}
	}
	return out
}

func (result customerScenarioAnswerGuardResult) Audit() map[string]any {
	out := map[string]any{
		"triggered": result.Triggered,
		"reason":    result.Reason,
		"action":    "review_only",
	}
	if result.Blocked {
		out["blocked"] = true
		out["block_reason"] = result.BlockReason
	}
	if len(result.AllowedProducts) > 0 {
		out["allowed_products"] = result.AllowedProducts
	}
	if len(result.OutputProducts) > 0 {
		out["output_products"] = result.OutputProducts
	}
	return out
}

func customerScenarioGuardText(req CustomerChatRequest, routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil {
		return strings.ToLower(strings.TrimSpace(req.Question))
	}
	parts := []string{
		req.Question,
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.RoutingReason,
		routerOutput.HandoffNotes,
		routerOutput.Slots.PrimaryProduct,
		strings.Join(routerOutput.Slots.Products, " "),
		routerOutput.Slots.StaticType,
		routerOutput.Slots.IPType,
		routerOutput.Slots.Bandwidth,
		routerOutput.Slots.Quantity,
		routerOutput.Slots.Scenario,
	}
	return strings.ToLower(strings.TrimSpace(strings.Join(parts, " ")))
}

func customerScenarioIsGenericStaticIPPrice(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if routerOutput.Ambiguity.IsAmbiguous || routerOutput.AnswerStrategy == "ask_clarification" {
		return false
	}
	question := strings.TrimSpace(req.Question)
	compactQuestion := strings.ToLower(strings.ReplaceAll(question, " ", ""))
	if compactQuestion != "静态ip怎么卖？" && compactQuestion != "静态ip怎么卖?" {
		return false
	}
	if !customerRouterTextHasStaticCue(text) || !customerRouterLooksPriceQuestion(text) {
		return false
	}
	if strings.TrimSpace(parsedStaticType(routerOutput)) != "" || strings.TrimSpace(routerOutput.Slots.Bandwidth) != "" || strings.TrimSpace(routerOutput.Slots.Quantity) != "" {
		return false
	}
	if containsAny(text, "5m", "10m", "20m", "50个", "100个", "批量", "优惠", "折扣", "海外") {
		return false
	}
	return true
}

func parsedStaticType(routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil {
		return ""
	}
	return strings.TrimSpace(routerOutput.Slots.StaticType)
}

func customerRouterTextHasOverseasCue(text string) bool {
	return containsAny(text, "海外ip", "海外 ip", "海外代理", "海外节点", "海外")
}

func customerAnswerHasGenericStaticIPPriceTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	if containsAny(text, "10m 30", "20m 70", "批量折扣", "最终成交价", "官网原价") {
		return false
	}
	return strings.Contains(text, "共享型") &&
		strings.Contains(text, "独享型") &&
		strings.Contains(text, "25") &&
		strings.Contains(text, "300") &&
		strings.Contains(text, "元/个/月")
}

type customerStaticBandwidthPrice struct {
	Shared    string
	Dedicated string
}

func customerScenarioIsStaticIPBandwidthPrice(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if strings.TrimSpace(routerOutput.Slots.Quantity) != "" || containsAny(text, "50个", "50 个", "100个", "100 个", "批量", "优惠", "折扣") {
		return false
	}
	return customerRouterTextHasStaticCue(text) &&
		customerRouterLooksPriceQuestion(text) &&
		customerScenarioStaticBandwidth(routerOutput, text) != ""
}

func customerScenarioStaticBandwidth(routerOutput *CustomerRouterOutput, text string) string {
	if routerOutput != nil {
		switch strings.ToLower(strings.TrimSpace(routerOutput.Slots.Bandwidth)) {
		case "5m":
			return "5M"
		case "10m":
			return "10M"
		case "20m":
			return "20M"
		}
	}
	compact := normalizeCustomerScenarioCompactText(text)
	switch {
	case strings.Contains(compact, "5m"):
		return "5M"
	case strings.Contains(compact, "10m"):
		return "10M"
	case strings.Contains(compact, "20m"):
		return "20M"
	default:
		return ""
	}
}

func customerStaticIPBandwidthPrice(bandwidth string) customerStaticBandwidthPrice {
	switch strings.ToLower(strings.TrimSpace(bandwidth)) {
	case "10m":
		return customerStaticBandwidthPrice{Shared: "30", Dedicated: "500"}
	case "20m":
		return customerStaticBandwidthPrice{Shared: "70", Dedicated: "800"}
	default:
		return customerStaticBandwidthPrice{Shared: "25", Dedicated: "300"}
	}
}

func customerAnswerHasStaticIPBandwidthPriceTerms(answer string, bandwidth string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	normalizedBandwidth := strings.ToLower(strings.TrimSpace(bandwidth))
	if normalizedBandwidth != "" &&
		strings.Contains(compact, normalizedBandwidth) &&
		strings.Contains(compact, "元/个") &&
		containsAny(text, "多买多优惠", "折扣", "申请", "优惠") {
		return true
	}
	price := customerStaticIPBandwidthPrice(bandwidth)
	return strings.Contains(text, strings.ToLower(strings.TrimSpace(bandwidth))) &&
		strings.Contains(text, "共享型") &&
		strings.Contains(text, "独享型") &&
		strings.Contains(text, price.Shared) &&
		strings.Contains(text, price.Dedicated) &&
		strings.Contains(text, "元/个/月")
}

func customerScenarioIsStaticIPQuantity100Discount(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if strings.Contains(normalizeCustomerReviewText(routerOutput.RewrittenQuestion), "独享") ||
		strings.Contains(normalizeCustomerReviewText(routerOutput.Intent), "dedicated") {
		return false
	}
	return customerRouterTextHasStaticCue(text) && containsAny(text, "100个", "100 个") && containsAny(text, "便宜", "优惠", "折扣")
}

func customerAnswerHasStaticIPQuantity100DiscountTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "51-100") &&
		strings.Contains(text, "7折") &&
		strings.Contains(text, "独享型") &&
		strings.Contains(text, "不参与")
}

func customerScenarioIsOverseasIPPrice(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	return customerRouterTextHasOverseasCue(text) && customerRouterLooksPriceQuestion(text)
}

func customerAnswerHasOverseasIPPriceBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "国家地区") &&
		strings.Contains(text, "购买时长") &&
		strings.Contains(text, "不能直接给固定价格")
}

func customerScenarioIsDatacenterResidentialPriceCompare(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	return containsAny(text, "数据中心", "机房") &&
		customerRouterTextHasResidentialCue(text) &&
		containsAny(text, "便宜", "价格", "多少钱", "对比")
}

func customerAnswerHasDatacenterResidentialPriceCompareTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "5m") &&
		strings.Contains(text, "25") &&
		strings.Contains(text, "30") &&
		strings.Contains(text, "10m") &&
		strings.Contains(text, "50") &&
		strings.Contains(text, "20m") &&
		strings.Contains(text, "70")
}

func customerScenarioIsDedicatedPrice(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if routerOutput.Slots.StaticType == "dedicated" {
		return true
	}
	intentText := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.RewrittenQuestion,
		routerOutput.RoutingReason,
	}, " ")))
	return customerRouterLooksDedicatedPrice(intentText) && !customerRouterLooksStaticBandwidthPrice(text)
}

func customerAnswerHasAllDedicatedPrices(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "300") && strings.Contains(text, "500") && strings.Contains(text, "800")
}

func customerScenarioIsSharedDatacenterQuantity50Price(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if routerOutput.Slots.StaticType == "dedicated" || (strings.Contains(text, "独享") && !strings.Contains(text, "共享")) {
		return false
	}
	if routerOutput.Slots.IPType != "" && routerOutput.Slots.IPType != "datacenter" {
		return false
	}
	if qty, ok := parseCustomerQuantity(routerOutput.Slots.Quantity); !ok || qty != 50 {
		if !containsAny(text, "50个", "50 个") {
			return false
		}
	}
	return (strings.Contains(text, "共享") || strings.Contains(text, "8折") || strings.Contains(text, "0.8") || routerOutput.Slots.StaticType == "shared") &&
		(containsAny(text, "数据中心", "datacenter") || routerOutput.Slots.IPType == "datacenter" || !customerRouterTextHasResidentialCue(text)) &&
		(customerRouterListContains(routerOutput.RiskFlags, "pricing") || customerRouterListContains(routerOutput.RiskFlags, "discount") || strings.Contains(text, "价格") || strings.Contains(text, "折扣"))
}

func customerAnswerHasSharedDatacenterQuantity50Terms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "50") &&
		(strings.Contains(text, "8折") || strings.Contains(text, "0.8")) &&
		strings.Contains(text, "5m") &&
		strings.Contains(text, "25") &&
		strings.Contains(text, "0.8") &&
		strings.Contains(text, "1000") &&
		strings.Contains(text, "20") &&
		strings.Contains(text, "10m") &&
		strings.Contains(text, "24") &&
		strings.Contains(text, "20m") &&
		strings.Contains(text, "56")
}

func customerScenarioIsAmbiguousDynamicStaticPrice(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if routerOutput.AnswerStrategy != "ask_clarification" || !routerOutput.Ambiguity.IsAmbiguous {
		return false
	}
	return customerRouterTextHasDynamicCue(text) &&
		customerRouterTextHasStaticCue(text) &&
		customerRouterLooksPriceQuestion(text)
}

func customerAnswerHasDynamicStaticPriceClarification(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") && strings.Contains(text, "静态ip")
}

func customerScenarioIsDynamicStaticCompare(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	if routerOutput.Slots.IPType == "residential" || strings.Contains(routerOutput.Intent, "residential") {
		return false
	}
	intent := normalizeCustomerReviewText(routerOutput.Intent)
	rewrite := normalizeCustomerReviewText(routerOutput.RewrittenQuestion)
	return containsAny(intent, "dynamic_static_compare", "dynamic_vs_static") ||
		containsAny(rewrite, "动态和静态", "动态ip和静态ip", "动态 ip和静态 ip")
}

func customerAnswerHasDynamicStaticCompareTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "静态ip") &&
		strings.Contains(text, "相对固定") &&
		containsAny(text, "频繁换", "频繁切换")
}

func customerScenarioIsProxyIPConcept(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	questionText := normalizeCustomerScenarioCompactText(req.Question)
	if containsAny(questionText, "代理ip是啥", "代理ip是什么", "什么是代理ip") {
		return true
	}
	return routerOutput.Intent == "proxy_ip_definition_inquiry" ||
		containsAny(normalizeCustomerScenarioCompactText(text), "代理ip是啥", "代理ip是什么", "什么是代理ip")
}

func customerAnswerHasProxyIPConceptTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return strings.Contains(compact, "出口ip") && strings.Contains(text, "目标网站")
}

func customerScenarioIsDatacenterResidentialCompare(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	intentText := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " ")))
	return customerRouterTextHasResidentialCue(intentText) &&
		containsAny(intentText, "数据中心", "机房", "datacenter") &&
		containsAny(intentText, "区别", "对比", "差异", "compare")
}

func customerAnswerHasDatacenterResidentialBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "数据中心") &&
		strings.Contains(text, "家庭宽带") &&
		strings.Contains(text, "同城轮换") &&
		containsAny(text, "平台结果", "平台规则", "平台风控", "账号行为")
}

func customerScenarioIsLiveStreamingSelection(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	questionText := normalizeCustomerScenarioCompactText(req.Question)
	if (strings.Contains(questionText, "直播") || strings.Contains(questionText, "开播")) && strings.Contains(questionText, "ip") {
		return true
	}
	return routerOutput.Intent == "live_streaming_ip_selection" ||
		(containsAny(text, "直播", "开播") && containsAny(text, "用哪", "选", "ip"))
}

func customerAnswerHasLiveStreamingSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "独享静态ip") &&
		strings.Contains(text, "10m") &&
		strings.Contains(text, "测试")
}

func customerAnswerHasLiveStreamingSelectionForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "保证直播画质", "稳定不卡顿", "一定不断线", "不能承诺")
}

func customerScenarioIsPlatformLocationSelection(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	if customerScenarioIsLongTermAccountSelection(routerOutput, text) {
		return false
	}
	intentText := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.Platform,
	}, " ")))
	if !customerRouterMentionsDomesticPlatform(intentText) || customerPlatformLocationLooksTroubleshooting("", CustomerRouterOutput{
		Intent:            intentText,
		RewrittenQuestion: intentText,
		RoutingReason:     intentText,
		HandoffNotes:      intentText,
	}) {
		return false
	}
	return customerRouterMentionsIPLocationChange(intentText) &&
		containsAny(intentText, "怎么选", "应该买", "买哪个", "选型", "用哪", "哪种", "推荐", "selection")
}

func customerAnswerHasPlatformLocationSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "静态ip") &&
		strings.Contains(text, "数据中心静态ip") &&
		strings.Contains(text, "住宅ip") &&
		containsAny(text, "平台ip库", "ip库") &&
		containsAny(text, "延迟", "影响")
}

func customerAnswerHasPlatformLocationSelectionForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "不能保证", "不能承诺")
}

func customerScenarioIsLongTermAccountSelection(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.Scenario,
	}, " "))
	return strings.Contains(intentText, "长期账号")
}

func customerAnswerHasLongTermAccountSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "静态ip") &&
		strings.Contains(text, "相对固定") &&
		containsAny(text, "不能承诺", "不能保证")
}

func customerScenarioIsSharedDedicatedSelection(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	if !customerRouterLooksSharedDedicatedCompare(current) {
		return false
	}
	return customerRouterLooksSharedDedicatedCompare(text) && containsAny(text, "怎么选", "选哪个", "选择", "适合", "区别")
}

func customerAnswerHasSharedDedicatedSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "共享型") &&
		strings.Contains(text, "独享型") &&
		containsAny(text, "带宽独享", "完全独享", "独立带宽")
}

func normalizeCustomerScenarioCompactText(text string) string {
	return strings.ReplaceAll(normalizeCustomerReviewText(text), " ", "")
}

func customerScenarioIsNewUserSelection(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" || routerOutput.QuestionStage != "product_selection" {
		return false
	}
	return strings.Contains(text, "新手") || strings.Contains(text, "第一次") || strings.Contains(text, "首次")
}

func customerNewUserSelectionFallbackSources() []customerChatSource {
	return []customerChatSource{
		{Path: "wiki/knowledge/si-ye-tian-proxy-ip-products.md", Confidence: "high"},
		{Path: "wiki/comparisons/dynamic-vs-static-ip.md", Confidence: "high"},
		{Path: "wiki/knowledge/si-ye-tian-overseas-ip.md", Confidence: "medium"},
	}
}

func customerAnswerHasNewUserSelectionBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "静态ip") &&
		strings.Contains(text, "海外ip") &&
		strings.Contains(text, "使用环境")
}

func customerScenarioIsDataCollectionSelection(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.Scenario,
	}, " "))
	return strings.Contains(intentText, "数据采集") &&
		(customerRouterTextHasDynamicCue(intentText) || customerRouterTextHasStaticCue(intentText) || containsAny(intentText, "选型", "用哪个", "一般用"))
}

func customerAnswerHasDataCollectionSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "频繁换出口") &&
		strings.Contains(text, "静态ip")
}

func customerScenarioIsLongTermResidentialFixed(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	return customerRouterTextHasResidentialCue(text) && containsAny(text, "长效", "固定", "是不是固定", "是否固定", "完全固定")
}

func customerAnswerHasLongTermResidentialBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "住宅静态ip") &&
		strings.Contains(text, "不承诺完全固定") &&
		strings.Contains(text, "同一城市范围") &&
		!strings.Contains(text, "绝对固定")
}

func customerScenarioIsGenericIPChangeClarification(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if routerOutput.AnswerStrategy == "ask_clarification" &&
		containsAny(intent, "ip_change_capability", "general_ip_switch", "ip_switch_product_clarification") {
		return true
	}
	if !containsAny(intent, "ip_change", "ip_switch", "proxy_ip_switch", "switch_capability", "change_capability") {
		return false
	}
	if routerOutput.Slots.PrimaryProduct != "" && routerOutput.Slots.PrimaryProduct != "unknown" {
		return false
	}
	return containsAny(text, "哪类产品", "未说明产品类型") &&
		containsAny(text, "改 ip", "改ip", "换 ip", "换ip", "切换 ip", "切换ip", "更换出口 ip", "更换出口ip")
}

func customerAnswerHasGenericIPChangeClarification(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return strings.Contains(text, "哪类产品") &&
		strings.Contains(compact, "动态ip") &&
		strings.Contains(compact, "静态ip")
}

func customerScenarioIsProxyExitIPCapability(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	current := normalizeCustomerReviewText(req.Question)
	if containsAny(current, "不是要切换", "不是切换", "不是要换", "不是换") && containsAny(current, "出口ip", "出口 ip", "电脑") {
		return true
	}
	if containsAny(intentText, "proxy_exit_ip_capability", "出口ip", "出口 ip") &&
		containsAny(intentText, "可以", "能", "能否", "能不能", "支持", "改变", "修改", "改") {
		return true
	}
	return false
}

func customerAnswerHasProxyExitIPCapabilityTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return containsAny(text, "可以", "能") &&
		strings.Contains(compact, "代理出口ip") &&
		containsAny(compact, "目标网站看到", "对外呈现") &&
		containsAny(compact, "本地网络", "本地公网", "本机", "内网ip")
}

func customerScenarioIsVagueOperationQuestion(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "reception" {
		return false
	}
	return containsAny(text, "这个怎么弄", "这个怎么操作", "这个怎么办")
}

func customerAnswerHasVagueOperationClarification(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "哪个产品") && strings.Contains(text, "操作") &&
		!containsAny(text, "动态ip", "静态ip", "价格", "api")
}

func customerScenarioIsReceptionUnknownQuestion(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "reception" {
		return false
	}
	return containsAny(text, "不知道问啥", "不知道该问啥", "不知道咨询什么", "随便看看", "不会描述")
}

func customerAnswerHasReceptionDirections(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return strings.Contains(compact, "换ip") && strings.Contains(compact, "购买") && strings.Contains(compact, "配置") && strings.Contains(compact, "售后")
}

func customerScenarioIsResidentialPurchase(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	if containsAny(current, "数据中心", "机房") {
		return false
	}
	if containsAny(text, "优惠", "折扣", "报价", "多少钱", "价格") {
		return false
	}
	if routerOutput.Slots.IPType == "residential" && containsAny(current, "买", "购买", "入口", "下单", "开通", "这个怎么买") {
		return true
	}
	return customerRouterTextHasResidentialCue(current) && containsAny(current, "购买", "怎么买", "入口", "下单", "开通")
}

func customerAnswerHasResidentialPurchaseEntry(answer string) bool {
	text := strings.ToLower(strings.TrimSpace(answer))
	return strings.Contains(text, "https://www.siyetian.com/product/box.html") &&
		strings.Contains(text, "当前页面") &&
		strings.Contains(text, "购买")
}

func customerScenarioIsGenericPurchaseQuestion(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	if containsAny(intentText, "purchased_resource", "购买后", "买完", "买了", "查看", "在哪里看", "在哪看", "资源", "套餐") {
		return false
	}
	return containsAny(intentText, "generic_purchase", "purchase_inquiry", "怎么购买", "怎么买", "如何购买", "购买入口", "下单入口") &&
		!containsAny(intentText, "动态", "静态", "住宅", "海外", "测试", "下载", "手机", "app")
}

func customerAnswerHasGenericPurchaseClarification(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "静态ip") &&
		strings.Contains(text, "住宅ip") &&
		strings.Contains(text, "海外ip") &&
		!strings.Contains(text, "https://www.siyetian.com/product.html")
}

func customerScenarioIsMobileAppDownload(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	return containsAny(current, "手机软件", "手机app", "手机 app", "app") && containsAny(current, "下载", "怎么用", "先去哪")
}

func customerAnswerHasMobileAppDownloadTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "app store") &&
		strings.Contains(text, "安卓") &&
		strings.Contains(text, "登录")
}

func customerScenarioIsTrialClaim(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	return customerRouterLooksTrialClaim(text)
}

func customerAnswerHasTrialClaimEntry(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "https://www.siyetian.com/test/index.html") &&
		strings.Contains(text, "注册") &&
		strings.Contains(text, "认证")
}

func customerScenarioIsPurchasedResourceView(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	if customerScenarioIsResidentialPurchaseSpecs(routerOutput, text) {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	if containsAny(intentText, "没有显示", "没显示", "未显示", "套餐没有", "没有套餐", "没套餐", "没有权益", "没权益") {
		return false
	}
	return customerRouterLooksPurchasedResourceView(intentText)
}

func customerAnswerHasPurchasedResourceViewTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "个人中心") &&
		strings.Contains(text, "对应产品后台") &&
		containsAny(text, "没有显示", "未显示") &&
		containsAny(text, "订单状态", "开通状态", "重新登录", "刷新")
}

func customerAnswerHasPurchasedResourceViewForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text,
		"排查流程",
		"付款后没ip",
		"刷新或重新登录后仍未显示",
		"刷新个人中心",
		"准备订单信息",
		"联系人工核查",
		"联系人工客服核查",
		"直接访问以下链接",
		"https://www.siyetian.com/member/",
	)
}

func customerScenarioIsResidentialPurchaseSpecs(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	if routerOutput.Slots.IPType != "residential" && !strings.Contains(routerOutput.Intent, "residential") {
		return false
	}
	if !strings.Contains(routerOutput.Intent, "spec") && !strings.Contains(routerOutput.Intent, "规格") {
		return false
	}
	return containsAny(text, "规格", "带宽", "城市", "套餐选项", "可选项") &&
		containsAny(text, "页面", "哪里看", "在哪看", "查看", "展示", "购买")
}

func customerAnswerHasResidentialPurchaseSpecs(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "规格") &&
		strings.Contains(text, "页面") &&
		containsAny(text, "可选城市", "带宽", "套餐选项", "当前可选") &&
		containsAny(text, "以当前页面为准", "以页面为准")
}

func customerScenarioIsPurchasedResourceMissing(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil {
		return false
	}
	if routerOutput.Specialist != "troubleshooting" && routerOutput.Specialist != "purchase" {
		return false
	}
	if customerScenarioIsTrialPackageMissing(routerOutput, text) {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return containsAny(intentText, "没有显示", "没显示", "未显示", "看不到", "没有ip", "没有 ip", "没ip", "没 ip", "套餐没有", "没有套餐", "没套餐")
}

func customerAnswerHasPurchasedResourceMissingSteps(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "刷新") &&
		strings.Contains(text, "重新登录") &&
		containsAny(text, "个人中心", "产品后台", "对应产品后台") &&
		containsAny(text, "订单状态", "开通状态")
}

func customerScenarioIsTrialPackageMissing(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return customerRouterLooksTrialPackageMissing(text)
}

func customerAnswerHasTrialPackageMissingSteps(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "刷新") && strings.Contains(text, "重新登录") && strings.Contains(text, "实名认证") &&
		containsAny(text, "测试入口", "个人中心", "权益状态")
}

func customerScenarioIsBasicConnectionTroubleshooting(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return containsAny(text, "代理连不上", "连不上怎么办") && !containsAny(text, "海外", "503", "407")
}

func customerAnswerHasBasicConnectionTroubleshootingTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "代理地址") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "账号密码") &&
		strings.Contains(text, "白名单") &&
		strings.Contains(text, "错误码") &&
		!containsAny(text, "防火墙安全软件", "防火墙", "安全软件")
}

func customerScenarioIsHTTP503Troubleshooting(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return strings.Contains(text, "503")
}

func customerAnswerHasHTTP503TroubleshootingTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "协议") &&
		strings.Contains(text, "ip") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "认证") &&
		strings.Contains(text, "目标网站")
}

func customerScenarioIsOverseasConnectionTimeout(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return customerRouterTextHasOverseasCue(text) && containsAny(text, "连接超时", "超时")
}

func customerAnswerHasOverseasConnectionTimeoutTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "海外网络环境") &&
		strings.Contains(text, "代理地址") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "有效期")
}

func customerScenarioIsStaticIPTroubleshootingFollowup(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.PrimaryProduct,
		strings.Join(routerOutput.Slots.Products, " "),
	}, " "))
	if containsAny(intentText, "代理连不上", "连不上怎么办") {
		return false
	}
	return customerRouterTextHasStaticCue(intentText) && containsAny(intentText, "不能用", "连不上")
}

func customerAnswerHasStaticIPTroubleshootingTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "静态ip") &&
		strings.Contains(text, "协议") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "https://www.ip138.com/")
}

func customerScenarioIsIPNotChanged(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	if routerOutput.Slots.PrimaryProduct != "" &&
		routerOutput.Slots.PrimaryProduct != "unknown" &&
		routerOutput.Slots.PrimaryProduct != "dynamic_ip" {
		return false
	}
	return containsAny(text, "ip没变", "ip 没变", "ip不变", "ip 不变", "出口没变", "出口不变")
}

func customerAnswerHasIPNotChangedSteps(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "https://www.ip138.com/") &&
		strings.Contains(text, "https://www.ipip.net/") &&
		strings.Contains(text, "重新提取") &&
		strings.Contains(text, "浏览器")
}

func customerScenarioIsOverseasIPSwitchUnsupported(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.PrimaryProduct,
		strings.Join(routerOutput.Slots.Products, " "),
		routerOutput.Slots.IPType,
	}, " "))
	if !customerRouterTextHasOverseasCue(intentText) &&
		routerOutput.Slots.PrimaryProduct != "overseas_ip" &&
		!customerRouterListContains(routerOutput.Slots.Products, "overseas_ip") &&
		routerOutput.Slots.IPType != "overseas" {
		return false
	}
	return containsAny(intentText, "切换ip", "切换 ip", "换ip", "换 ip", "更换ip", "更换 ip", "切换出口", "更换出口", "switch")
}

func customerAnswerHasOverseasIPSwitchUnsupportedTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "海外ip") &&
		strings.Contains(text, "不支持切换ip") &&
		!containsAny(text, "手动切换", "重新分配", "切换按钮", "重新提取", "断开重连")
}

func customerScenarioIsPlatformDisplayIssue(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return customerRouterMentionsDomesticPlatform(text) && customerPlatformLocationLooksTroubleshooting("", CustomerRouterOutput{
		Intent:            text,
		RewrittenQuestion: text,
	})
}

func customerAnswerHasPlatformDisplayBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return strings.Contains(compact, "设备出口ip") && strings.Contains(compact, "平台ip库") &&
		strings.Contains(compact, "缓存") && containsAny(compact, "显示可能会有延迟", "显示可能有延迟", "可能有延迟", "受平台ip库影响")
}

func customerScenarioIsWhitelistSetup(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	if customerScenarioIsAPIExtraction(req, routerOutput) {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	return intent == "whitelist_configuration_inquiry" ||
		containsAny(intent, "whitelist") ||
		containsAny(current, "白名单怎么设置", "白名单设置", "设置白名单")
}

func customerAnswerHasWhitelistSetupTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	if strings.Contains(text, "https://www.siyetian.com/member/whitelist.html") &&
		strings.Contains(text, "出口公网ip") {
		return true
	}
	return strings.Contains(compact, "白名单") &&
		containsAny(compact, "当前出口ip", "出口公网ip", "当前服务器", "当前电脑") &&
		containsAny(text, "添加", "填进去", "填入") &&
		strings.Contains(text, "保存") &&
		containsAny(text, "重新连接", "重新测试", "连接代理测试")
}

func customerScenarioIsAPIExtraction(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if strings.Contains(intent, "whitelist") {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	return customerRouterLooksAPIExtraction(current) || containsAny(intent, "api_extraction", "api_inquiry")
}

func customerAnswerHasAPIExtractionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "https://www.siyetian.com/apis.html") &&
		strings.Contains(text, "账号密码认证") &&
		strings.Contains(text, "白名单")
}

func customerScenarioIsAccountPasswordAuth(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if strings.Contains(intent, "whitelist") || customerScenarioIsAPIExtraction(req, routerOutput) {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	return containsAny(intent, "account_password", "password_auth") ||
		containsAny(current, "账号密码认证", "开启账号密码", "账号密码怎么", "账号密码如何")
}

func customerAnswerHasAccountPasswordAuthTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "api页面") &&
		strings.Contains(text, "代理地址") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "账号") &&
		strings.Contains(text, "密码")
}

func customerScenarioIsPythonProxyIntegration(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	return strings.Contains(text, "python") && containsAny(text, "接入", "配置", "怎么用")
}

func customerAnswerHasPythonProxyIntegrationTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "协议") &&
		strings.Contains(text, "主机") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "账号") &&
		strings.Contains(text, "密码") &&
		strings.Contains(text, "https://www.siyetian.com/help/28/55.html")
}

func customerAnswerHasPythonProxyIntegrationForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "token示例", "默认端口")
}

func customerScenarioIsFingerprintBrowserConfig(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	return containsAny(text, "指纹浏览器", "浏览器") && containsAny(text, "代理怎么填", "怎么填", "配置")
}

func customerAnswerHasFingerprintBrowserConfigTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "代理协议") &&
		strings.Contains(text, "主机") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "认证信息")
}

func customerScenarioHasStaticIPProduct(routerOutput *CustomerRouterOutput) bool {
	return routerOutput != nil &&
		(routerOutput.Slots.PrimaryProduct == "static_ip" || customerRouterListContains(routerOutput.Slots.Products, "static_ip"))
}

func customerScenarioExplicitStaticSwitchText(text string) bool {
	return containsAny(text, "切换ip", "切换 ip", "换ip", "换 ip", "手动切换", "更换ip", "更换 ip")
}

func customerScenarioExplicitStaticRegionSwitchText(text string) bool {
	return containsAny(text, "换地区", "换城市", "切换地区", "切换城市", "更换地区", "更换城市")
}

func customerScenarioIsStaticIPSwitch(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if !customerScenarioHasStaticIPProduct(routerOutput) {
		return false
	}
	if customerScenarioIsStaticIPSpecifyAddress(req, routerOutput) || customerScenarioIsStaticIPRegionSwitch(req, routerOutput) {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if containsAny(intent, "static_ip_switch", "static_ip_change", "ip_switch") && !containsAny(intent, "usage", "configuration", "troubleshooting") {
		return true
	}
	return customerScenarioExplicitStaticSwitchText(normalizeCustomerReviewText(req.Question))
}

func customerAnswerHasStaticIPSwitchTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "手动切换") &&
		strings.Contains(text, "每月5次") &&
		strings.Contains(text, "https://www.siyetian.com/member/staticip.html")
}

func customerAnswerHasStaticIPSwitchForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "指定某个具体ip", "指定某个具体 ip")
}

func customerScenarioIsStaticIPSpecifyAddress(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if !customerScenarioHasStaticIPProduct(routerOutput) {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if strings.Contains(intent, "region") {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	return containsAny(intent, "specify_ip", "specified_ip") ||
		containsAny(current, "指定某一个ip", "指定某一个 ip", "指定某个ip", "指定某个 ip", "指定ip", "指定 ip")
}

func customerAnswerHasStaticIPSpecifyAddressTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "指定到某一个具体ip", "指定某一个具体ip", "指定具体ip", "指定到某个具体ip") &&
		containsAny(text, "不能承诺", "不能保证", "以后台当前可用资源为准", "以后台可用资源为准")
}

func customerScenarioIsStaticIPRegionSwitch(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if !customerScenarioHasStaticIPProduct(routerOutput) {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if strings.Contains(intent, "specify_ip") && !strings.Contains(intent, "region") {
		return false
	}
	if containsAny(intent, "region_switch", "region_change", "city_switch", "city_change") {
		return true
	}
	return customerScenarioExplicitStaticRegionSwitchText(normalizeCustomerReviewText(req.Question))
}

func customerAnswerHasStaticIPRegionSwitchTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "地区") &&
		containsAny(text, "后台可用资源", "后台可选项", "页面当前")
}

func customerScenarioIsSubnetGatewayConfig(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if containsAny(text, "什么是", "是什么", "是啥") {
		return false
	}
	return containsAny(text, "子网掩码", "网关") && containsAny(text, "怎么填", "如何填", "配置", "设置", "填什么")
}

func customerAnswerHasSubnetGatewayConfigTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "网络配置") &&
		strings.Contains(text, "代理协议") &&
		strings.Contains(text, "地址") &&
		strings.Contains(text, "端口")
}

func customerScenarioIsAppDynamicMobile(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if customerRequestClientChannel(req) == "mobile_app" {
		return false
	}
	if routerOutput.Slots.PrimaryProduct == "static_ip" || customerRouterListContains(routerOutput.Slots.Products, "static_ip") {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	current := normalizeCustomerReviewText(req.Question)
	return (containsAny(intent, "dynamic") || customerRouterTextHasDynamicCue(current)) &&
		containsAny(current, "手机", "app", "移动端")
}

func customerAnswerHasAppDynamicMobileTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "手机端") &&
		strings.Contains(text, "app") &&
		strings.Contains(text, "当前页面")
}

func customerScenarioIsBalanceWithdraw(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return strings.Contains(intentText, "余额") && containsAny(intentText, "提现", "提出来", "退回", "withdraw")
}

func customerAnswerHasBalanceWithdrawTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "平台内购买") &&
		strings.Contains(text, "一般不支持直接提现") &&
		containsAny(text, "页面", "规则说明", "当前规则")
}

func customerScenarioIsRechargeNotReceived(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return containsAny(routerOutput.Intent, "balance_not_received", "recharge_not_received", "payment_not_received") ||
		(containsAny(intentText, "充值", "余额", "支付") && containsAny(intentText, "没到账", "未到账", "没有到账", "未增加"))
}

func customerAnswerHasRechargeNotReceivedTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "支付是否成功") &&
		strings.Contains(text, "个人中心") &&
		strings.Contains(text, "刷新") &&
		containsAny(text, "支付记录", "订单状态")
}

func customerScenarioIsPaymentMethod(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	compact := normalizeCustomerScenarioCompactText(text)
	if containsAny(intent, "package_change", "plan_change", "refund", "invoice", "renewal", "upgrade", "balance_not_received", "recharge_not_received", "payment_not_received", "account_verification") {
		return false
	}
	if containsAny(intent, "payment_method", "payment_options", "corporate_payment") {
		return true
	}
	if containsAny(compact, "加微信", "有没有微信", "有微信吗", "微信客服", "企业微信", "企微", "微信联系", "微信沟通", "联系方式", "联系电话", "客服微信") {
		return false
	}
	currentText := normalizeCustomerScenarioCompactText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return (containsAny(currentText, "支付方式", "怎么支付", "如何支付", "微信支付", "微信买", "微信付款", "微信付", "支付宝", "对公", "打款", "付款方式", "付款") &&
		containsAny(compact, "支持", "可以", "能", "支付", "付款", "对公", "打款"))
}

func customerAnswerHasPaymentMethodTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "微信支付") &&
		strings.Contains(text, "支付宝") &&
		strings.Contains(text, "https://www.siyetian.com/member/recharge.html")
}

func customerScenarioIsAccountVerification(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return containsAny(intentText, "实名", "实名认证", "不实名", "verification")
}

func customerAnswerHasAccountVerificationTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "实名认证") &&
		strings.Contains(text, "https://www.siyetian.com/authent/index.html") &&
		strings.Contains(text, "企业认证")
}

func customerScenarioIsExpiredStaticRenewal(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.PrimaryProduct,
		strings.Join(routerOutput.Slots.Products, " "),
	}, " "))
	return customerRouterTextHasStaticCue(intentText) && containsAny(intentText, "到期", "过期", "expired") && containsAny(intentText, "续费", "续回", "renewal")
}

func customerAnswerHasExpiredStaticRenewalTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "到期前") &&
		strings.Contains(text, "可能被释放") &&
		(strings.Contains(text, "后台资源状态") || strings.Contains(text, "续费入口") || strings.Contains(text, "重新购买"))
}

func customerAnswerHasExpiredStaticRenewalTermsForRequest(req CustomerChatRequest, decisionText string, answer string) bool {
	text := normalizeCustomerReviewText(answer)
	if customerExpiredStaticRenewalNeedsActionPath(req, decisionText) {
		current := normalizeCustomerReviewText(req.Question)
		if containsAny(current, "还能续", "还可以续", "能续吗", "续回") {
			return containsAny(text, "能不能续", "主要看", "是否还显示续费入口") &&
				containsAny(text, "续费入口", "member/staticip", "member/jingtai") &&
				strings.Contains(text, "重新购买")
		}
		return containsAny(text, "续费入口", "member/staticip", "member/jingtai", "重新购买") &&
			!strings.Contains(text, "到期前")
	}
	return customerAnswerHasExpiredStaticRenewalTerms(answer)
}

func customerExpiredStaticRenewalAnswer(req CustomerChatRequest, text string) string {
	if customerExpiredStaticRenewalNeedsActionPath(req, text) {
		current := normalizeCustomerReviewText(req.Question)
		if containsAny(current, "还能续", "还可以续", "能续吗", "续回") {
			return "能不能续主要看个人中心是否还显示续费入口。您可以到 https://www.siyetian.com/member/staticip.html 或 https://www.siyetian.com/member/jingtai.html 查看；有入口就按页面续费，没有入口或原 IP 已释放时，通常需要重新分配或重新购买。"
		}
		return "已经过期的静态 IP 可以先到个人中心对应产品页查看是否还有续费入口： https://www.siyetian.com/member/staticip.html 或 https://www.siyetian.com/member/jingtai.html 。如果页面还能续，就按页面续费；如果原 IP 已释放或页面不再显示续费入口，可能需要重新分配或重新购买。"
	}
	return "到期前续费更有利于保留原 IP；如果已经过期，IP 可能被释放或需要重新分配。能否保留要以当前后台资源状态为准。"
}

func customerExpiredStaticRenewalNeedsActionPath(req CustomerChatRequest, text string) bool {
	current := normalizeCustomerReviewText(req.Question)
	return containsAny(current, "已经过期", "过期了", "还能续", "还可以续", "能续吗", "续回") ||
		(containsAny(text, "已经过期", "过期了") && containsAny(text, "还能续", "还可以续", "能续吗", "续回"))
}

func customerScenarioIsPackageChange(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	if containsAny(routerOutput.Intent, "package_change", "package_change_policy", "change_request", "plan_change", "套餐") {
		return true
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return strings.Contains(intentText, "买错套餐") || strings.Contains(intentText, "换套餐") || strings.Contains(intentText, "调整方案") || strings.Contains(intentText, "多退少补")
}

func customerAnswerHasPackageChangeTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "订单状态") &&
		containsAny(text, "调整方案", "多退少补", "换套餐")
}

func customerAnswerIsClarificationOnly(parsed customerChatLLMOutput, answer string) bool {
	if normalizedAnswerMode(parsed.AnswerMode) == "clarification" {
		return true
	}
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "请问") && strings.Contains(text, "哪") && !containsAny(text, "订单状态", "当前资源")
}

func customerAnswerHasForbiddenPackageChangePhrase(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "一定能换", "一定能退差价", "不能直接承诺")
}

func customerScenarioIsBandwidthUpgrade(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return containsAny(intentText, "升级带宽", "带宽升级", "升带宽", "bandwidth_upgrade") ||
		(strings.Contains(intentText, "5m") && strings.Contains(intentText, "10m") && containsAny(intentText, "换", "升级"))
}

func customerAnswerHasBandwidthUpgradeTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "产品类型") &&
		strings.Contains(text, "补差价") &&
		containsAny(text, "当前资源", "订单状态")
}

func customerScenarioIsRefund(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	return customerRouterListContains(routerOutput.RiskFlags, "refund") || routerOutput.UserIntentSignals.RefundStrong || strings.Contains(text, "退款") || strings.Contains(text, "退费")
}

func customerAnswerAsksForOrderInfo(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "订单号", "订单信息", "购买的产品信息", "支付截图", "提供具体", "为您核实", "人工为您核实", "核实订单", "订单情况")
}

func customerAnswerHasForbiddenRefundPhrase(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "不能直接承诺可以退款", "不能承诺可以退款", "不能直接承诺")
}

func customerScenarioIsLegalProxySafety(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return containsAny(text, "合法吗", "合不合法", "代理ip合法", "代理 ip合法", "代理ip 合法", "是否违法", "违法吗")
}

func customerAnswerHasLegalProxySafetyTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "工具") &&
		strings.Contains(text, "合法合规") &&
		strings.Contains(text, "不能简单承诺零风险")
}

func customerScenarioIsOverseasAccessGoogle(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return customerRouterTextHasOverseasCue(text) && strings.Contains(text, "google")
}

func customerAnswerHasOverseasAccessGoogleTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能承诺") &&
		strings.Contains(text, "海外网络环境") &&
		strings.Contains(text, "目标站点策略")
}

func customerScenarioIsChatGPTDomesticOverseasIP(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "chatgpt") && customerRouterTextHasOverseasCue(text) && containsAny(text, "国内", "本地")
}

func customerAnswerHasChatGPTDomesticOverseasIPTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能承诺") &&
		strings.Contains(text, "chatgpt") &&
		strings.Contains(text, "平台策略") &&
		strings.Contains(text, "代理检测")
}

func customerScenarioIsPlatformAntiBan(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	if customerScenarioIsPlatformRiskGuarantee(routerOutput, text) {
		return false
	}
	return containsAny(text, "防封", "绕检测", "规避风控", "过风控") && containsAny(text, "抖音", "平台", "账号", "ip")
}

func customerScenarioIsPlatformRiskGuarantee(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	if containsAny(text, "正常用途", "合法用途", "合规用途") {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if containsAny(text, "绕过平台检测", "绕检测", "规避风控", "规避平台", "绕风控", "过风控") {
		return false
	}
	return containsAny(intent, "platform_risk_guarantee") ||
		(containsAny(text, "保证", "能不能", "能否", "不会被", "不被") && containsAny(text, "风控", "平台", "账号结果"))
}

func customerScenarioIsNormalUseAfterPlatformRisk(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return containsAny(text, "正常用途", "合法用途", "合规用途")
}

func customerAnswerHasNormalUseBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "合法合规") &&
		containsAny(text, "固定出口", "白名单", "数据采集", "连接排障") &&
		containsAny(text, "不能承诺", "不承诺")
}

func customerAnswerHasPlatformRiskGuaranteeTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能承诺") &&
		strings.Contains(text, "风控") &&
		containsAny(text, "账号结果", "账号行为", "平台规则")
}

func customerAnswerHasPlatformAntiBanTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能提供") &&
		strings.Contains(text, "风控") &&
		strings.Contains(text, "防封")
}

func customerScenarioIsVotingBoost(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "投票") || strings.Contains(text, "刷量")
}

func customerAnswerHasVotingBoostTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能提供") &&
		strings.Contains(text, "投票") &&
		strings.Contains(text, "刷量")
}

func customerScenarioIsBulkRegisterAntiBan(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "批量注册") && containsAny(text, "防封", "风控", "检测")
}

func customerAnswerHasBulkRegisterAntiBanTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能提供") &&
		strings.Contains(text, "批量注册") &&
		strings.Contains(text, "防封")
}

func customerScenarioIsXiaohongshuRiskAvoidance(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "小红书") && containsAny(text, "避免被风控", "风控", "账号结果")
}

func customerAnswerHasXiaohongshuRiskAvoidanceTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能承诺") &&
		strings.Contains(text, "风控") &&
		strings.Contains(text, "账号结果")
}

func customerScenarioIsClashShadowrocketVPN(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return containsAny(text, "clash", "小火箭", "vpn", "机场节点")
}

func customerAnswerHasClashShadowrocketVPNTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能提供") &&
		strings.Contains(text, "clash") &&
		strings.Contains(text, "小火箭") &&
		strings.Contains(text, "vpn")
}

func customerAnswerHasClashShadowrocketVPNForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "订阅链接", "节点", "替代工具", "配置步骤")
}

func customerScenarioIsTunnelIPSec(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "隧道") || strings.Contains(text, "ipsec")
}

func customerAnswerHasTunnelIPSecBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") && strings.Contains(text, "静态ip") &&
		strings.Contains(text, "api") && strings.Contains(text, "隧道") &&
		strings.Contains(text, "http") && strings.Contains(text, "socks5") &&
		strings.Contains(text, "ipsec") && containsAny(text, "不作为", "不能", "不支持")
}

func splitCustomerAnswerSentences(answer string) []string {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil
	}
	parts := []string{}
	start := 0
	runes := []rune(answer)
	for i, r := range runes {
		switch r {
		case '。', '！', '？', '!', '?', '\n':
			segment := strings.TrimSpace(string(runes[start : i+1]))
			if segment != "" {
				parts = append(parts, segment)
			}
			start = i + 1
		}
	}
	if start < len(runes) {
		segment := strings.TrimSpace(string(runes[start:]))
		if segment != "" {
			parts = append(parts, segment)
		}
	}
	return parts
}

func customerVisibleAnswerLeaksInternalContext(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	for _, marker := range []string{
		"技术专家",
		"产品专家",
		"价格专家",
		"售后专家",
		"安全专家",
		"前台接待",
		"转接专家",
		"转给专家",
		"安排专家",
		"分派给专家",
		"router",
		"specialist",
		"candidate_pages",
		"candidate page",
		"candidate_page_paths",
		"review_question",
		"answer_mode",
		"evidence_confidence",
		"review_required",
		"系统提示词",
		"内部提示词",
		"prompt",
		"json 字段",
		"json字段",
	} {
		if strings.Contains(normalized, strings.ToLower(marker)) {
			return true
		}
	}
	for _, phrase := range []string{
		"知识库没有",
		"知识库暂无",
		"知识库显示",
		"知识库提示",
		"资料提示",
		"资料显示",
		"候选资料",
		"候选知识",
		"候选页面",
		"候选页",
		"检索结果",
		"路由判断",
		"分诊结果",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
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

func (s *CustomerChatService) shouldCreateCustomerReview(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, retrievedPaths []string, settings RuntimeCustomerQuerySettings) (bool, string) {
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if strings.TrimSpace(parsed.AnswerText) == "" {
		return false, "empty_answer"
	}
	if isObviouslyNonReviewableCustomerQuestion(req.Question) {
		return false, "non_reviewable_question"
	}
	if customerReviewRequiredByServiceGuard(parsed) {
		return true, "unsafe_answer_guard"
	}
	if mode == "refusal" && customerReviewLooksLikeBoundaryRefusal(req, parsed) {
		return false, "boundary_refusal"
	}
	directMin, reviewMin := customerConfidenceThresholds(
		settings.DirectMin,
		settings.ReviewMin,
	)
	confidence := clampConfidence(parsed.Confidence)
	evidenceConfidence := clampConfidence(parsed.EvidenceConfidence)
	hasReviewSignal := customerHasExplicitReviewSignal(parsed)
	if mode == "refusal" {
		if customerReviewLooksLikeKnowledgeGap(parsed) && (hasReviewSignal || confidence < directMin || evidenceConfidence < directMin) {
			return true, "knowledge_gap_refusal"
		}
		return false, "refusal"
	}
	if hasReviewSignal {
		if confidence < directMin || evidenceConfidence < directMin || len(parsed.Sources) == 0 || len(retrievedPaths) == 0 {
			return true, "model_review_signal"
		}
		return false, "model_review_signal_high_confidence"
	}
	if customerReviewLooksLikeKnowledgeGap(parsed) {
		return true, "knowledge_gap_signal"
	}
	if mode == "clarification" {
		return false, "clarification"
	}
	if customerRouterOutputRequiresFinalEvidence(routerOutput) && len(parsed.Sources) == 0 && mode != "refusal" {
		return true, "high_risk_without_final_sources"
	}
	if mode == "self_answer" {
		if customerQuestionLooksTechnicalReviewCandidate(req.Question, routerOutput) {
			if customerAnswerLooksServiceProcedure(parsed.AnswerText) {
				if !customerHasProcedureEvidence(parsed.Sources, retrievedPaths) {
					return true, "technical_procedure_without_evidence"
				}
				if evidenceConfidence < directMin {
					return true, "technical_procedure_weak_evidence"
				}
			}
			if len(parsed.Sources) > 0 || evidenceConfidence > 0 {
				return false, "technical_self_answer_trace_only"
			}
			return false, "technical_self_answer"
		}
		if len(parsed.Sources) > 0 || evidenceConfidence > 0 {
			return false, "self_answer_with_evidence_trace_only"
		}
		return false, "self_answer"
	}
	if confidence >= directMin {
		if evidenceConfidence < directMin && (mode == "mixed" || len(parsed.Sources) == 0) {
			return true, "weak_evidence"
		}
		return false, "high_confidence"
	}
	if confidence >= reviewMin {
		return true, "low_confidence"
	}
	if evidenceConfidence < directMin && len(parsed.Sources) == 0 {
		return true, "weak_evidence_no_sources"
	}
	return false, "below_review_threshold"
}

func customerReviewRequiredByServiceGuard(parsed customerChatLLMOutput) bool {
	text := strings.TrimSpace(parsed.ReviewReason + " " + parsed.Notes)
	return strings.Contains(text, "客户可见答案包含高风险用途话术")
}

func customerRouterOutputRequiresFinalEvidence(routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || !routerOutput.NeedsRetrieval {
		return false
	}
	if routerOutput.Specialist == "pricing" || routerOutput.Specialist == "billing_after_sales" {
		return true
	}
	if routerOutput.QuestionStage == "pricing" || routerOutput.QuestionStage == "after_sales" {
		return true
	}
	if routerOutput.RiskBoundary == "pricing_review" || routerOutput.RiskBoundary == "after_sales_review" {
		return true
	}
	for _, flag := range routerOutput.RiskFlags {
		switch strings.TrimSpace(flag) {
		case "pricing", "discount", "refund", "billing", "after_sales":
			return true
		}
	}
	return false
}

type customerUnsafeAnswerGuardResult struct {
	Triggered bool
	Answer    string
	Hits      []string
	Reason    string
}

func customerUnsafeAnswerGuard(parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerUnsafeAnswerGuardResult {
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if mode == "refusal" {
		return customerUnsafeAnswerGuardResult{Reason: "refusal"}
	}
	answer := strings.TrimSpace(parsed.AnswerText)
	hits := customerUnsafeVisibleAnswerHits(answer)
	if len(hits) == 0 {
		return customerUnsafeAnswerGuardResult{Reason: "no_hits"}
	}
	if routerOutput != nil && routerOutput.Specialist == "safety" {
		return customerUnsafeAnswerGuardResult{Reason: "safety_specialist", Hits: hits}
	}
	return customerUnsafeAnswerGuardResult{
		Triggered: true,
		Answer:    "这类用途涉及平台规则或风控规避，不能提供相关操作建议。可以正常介绍四叶天产品的合规用途、价格、购买或配置方式。",
		Hits:      hits,
		Reason:    "unsafe_customer_visible_terms",
	}
}

func (result customerUnsafeAnswerGuardResult) Audit() map[string]any {
	return map[string]any{
		"triggered": result.Triggered,
		"reason":    result.Reason,
		"hits":      result.Hits,
		"action":    "review_only",
	}
}

func customerUnsafeVisibleAnswerHits(answer string) []string {
	text := strings.ToLower(strings.TrimSpace(answer))
	if text == "" {
		return nil
	}
	hits := []string{}
	for _, marker := range []string{
		"批量注册",
		"防封",
		"反爬",
		"爬虫",
		"过风控",
		"绕风控",
		"规避风控",
		"绕检测",
		"防检测",
		"降低被封",
		"避免封号",
		"养号",
		"刷量",
	} {
		if strings.Contains(text, strings.ToLower(marker)) {
			hits = appendUniqueString(hits, marker)
		}
	}
	return hits
}

func customerAnswerLooksServiceProcedure(answer string) bool {
	normalized := normalizeCustomerReviewText(answer)
	if normalized == "" {
		return false
	}
	hasServiceObject := containsAny(
		normalized,
		"四叶天",
		"管理后台",
		"后台",
		"产品页面",
		"客户端",
		"当前页面",
		"工具字段",
		"api",
		"白名单",
		"代理地址",
		"端口",
		"认证信息",
		"账号密码",
		"socks5",
		"http",
		"https",
	)
	hasAction := containsAny(
		normalized,
		"打开",
		"选择",
		"填写",
		"保存",
		"设置",
		"配置",
		"接入",
		"添加",
		"获取",
		"开通",
	)
	if hasServiceObject && hasAction {
		return true
	}
	return strings.Contains(answer, "\n1.") && hasServiceObject
}

func customerHasProcedureEvidence(sources []customerChatSource, retrievedPaths []string) bool {
	for _, source := range sources {
		if customerPathLooksProcedureEvidence(source.Path) {
			return true
		}
	}
	for _, path := range retrievedPaths {
		if customerPathLooksProcedureEvidence(path) {
			return true
		}
	}
	return false
}

func customerPathLooksProcedureEvidence(path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	return containsAny(path, "configuration", "setup", "usage", "whitelist", "troubleshooting", "connection", "installation", "api")
}

func customerHasExplicitReviewSignal(parsed customerChatLLMOutput) bool {
	if parsed.ReviewRequired {
		return true
	}
	if strings.TrimSpace(parsed.ReviewReason) != "" || strings.TrimSpace(parsed.SuggestedTargetPath) != "" {
		return true
	}
	if normalizedAnswerMode(parsed.AnswerMode) != "clarification" && strings.TrimSpace(parsed.ReviewQuestion) != "" {
		return true
	}
	return false
}

func customerReviewLooksLikeKnowledgeGap(parsed customerChatLLMOutput) bool {
	text := normalizeCustomerReviewText(strings.Join([]string{
		parsed.AnswerText,
		parsed.ReviewReason,
		parsed.ReviewQuestion,
		parsed.SuggestedTargetPath,
		parsed.Notes,
	}, " "))
	return containsAny(
		text,
		"知识缺口",
		"缺少知识",
		"无知识",
		"无证据",
		"证据不足",
		"候选页",
		"候选知识",
		"没有找到",
		"未找到",
		"未收录",
		"暂未收录",
		"暂无资料",
		"无法确认",
		"不确定",
		"低置信",
		"需要补充",
		"不在候选",
	)
}

func customerReviewLooksLikeBoundaryRefusal(req CustomerChatRequest, parsed customerChatLLMOutput) bool {
	text := normalizeCustomerReviewText(strings.Join([]string{
		req.Question,
		parsed.AnswerText,
		parsed.ReviewReason,
		parsed.Notes,
	}, " "))
	return containsAny(
		text,
		"违法",
		"违规",
		"合规",
		"不能提供",
		"无法提供",
		"绕过",
		"风控",
		"封号",
		"批量注册",
		"内部",
		"系统提示",
		"prompt",
		"越狱",
		"vpn",
		"翻墙",
		"clash",
		"小火箭",
	)
}

func customerQuestionLooksTechnicalReviewCandidate(question string, routerOutput *CustomerRouterOutput) bool {
	routerText := ""
	if routerOutput != nil {
		if strings.EqualFold(strings.TrimSpace(routerOutput.Specialist), "technical") ||
			strings.EqualFold(strings.TrimSpace(routerOutput.Specialist), "troubleshooting") {
			return true
		}
		for _, flag := range routerOutput.RiskFlags {
			if strings.EqualFold(strings.TrimSpace(flag), "technical") ||
				strings.EqualFold(strings.TrimSpace(flag), "troubleshooting") {
				return true
			}
		}
		routerText = strings.Join([]string{
			routerOutput.Intent,
			routerOutput.RewrittenQuestion,
			routerOutput.HandoffNotes,
		}, " ")
	}
	text := normalizeCustomerReviewText(question + " " + routerText)
	return containsAny(
		text,
		"子网",
		"掩码",
		"网关",
		"dns",
		"端口",
		"协议",
		"代理地址",
		"白名单",
		"认证",
		"账号密码",
		"socks5",
		"http代理",
		"https代理",
		"路由",
		"分流",
		"出口ip",
		"连接失败",
		"连不上",
		"配置",
	)
}

func isObviouslyNonReviewableCustomerQuestion(question string) bool {
	normalized := normalizeCustomerReviewText(question)
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

func normalizeCustomerReviewText(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, " \t\r\n？?。.!！~～")
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized
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
	wecom := normalizedSupportWeCom(settings.WeCom)
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

func normalizedSupportWeCom(value string) string {
	wecom := strings.TrimSpace(value)
	switch strings.ToLower(wecom) {
	case "":
		return ""
	case "企业微信", "企业微信客服", "微信客服", "企微", "wecom":
		return "官网右侧企业微信二维码"
	default:
		return wecom
	}
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
	return prioritizeCustomerRetrievedPagesForRouter(pages, nil)
}

func prioritizeCustomerRetrievedPagesForRouter(pages []retrieval.RetrievedPage, routerOutput *CustomerRouterOutput) []retrieval.RetrievedPage {
	out := append([]retrieval.RetrievedPage(nil), pages...)
	sortCustomerRetrievedPagesByProductFit(out, routerOutput, true)
	return out
}

func sortCustomerRetrievedPagesByProductFit(pages []retrieval.RetrievedPage, routerOutput *CustomerRouterOutput, useDirectoryRank bool) {
	for i := 0; i < len(pages)-1; i++ {
		for j := i + 1; j < len(pages); j++ {
			leftDirectoryRank := 0
			rightDirectoryRank := 0
			if useDirectoryRank {
				leftDirectoryRank = customerEvidenceDirectoryRank(pages[i].Path)
				rightDirectoryRank = customerEvidenceDirectoryRank(pages[j].Path)
			}
			leftProductPenalty := customerEvidenceProductMismatchPenalty(pages[i].Path, routerOutput)
			rightProductPenalty := customerEvidenceProductMismatchPenalty(pages[j].Path, routerOutput)
			if rightDirectoryRank < leftDirectoryRank ||
				(rightDirectoryRank == leftDirectoryRank && rightProductPenalty < leftProductPenalty) ||
				(useDirectoryRank && rightDirectoryRank == leftDirectoryRank && rightProductPenalty == leftProductPenalty && pages[j].Score > pages[i].Score) {
				pages[i], pages[j] = pages[j], pages[i]
			}
		}
	}
}

func customerEvidenceDirectoryRank(path string) int {
	path = filepath.ToSlash(strings.TrimSpace(path))
	switch {
	case strings.HasPrefix(path, "wiki/knowledge/"):
		return 0
	// Procedures share the top tier with knowledge: how-to pages are direct
	// answer evidence, so relevance score (not directory) must decide between a
	// canonical fact page and the matching step-by-step page. Otherwise a
	// loosely-relevant knowledge page out-ranks the procedure that actually
	// answers a "how do I ..." question and crowds it out of topK.
	case strings.HasPrefix(path, "wiki/procedures/"):
		return 0
	case strings.HasPrefix(path, "wiki/policies/"):
		return 1
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

func customerEvidenceProductMismatchPenalty(path string, routerOutput *CustomerRouterOutput) int {
	target := customerEvidenceSinglePrimaryProduct(routerOutput)
	if target == "" {
		return 0
	}
	products := customerEvidenceProductsInPath(path)
	if len(products) == 0 || products[target] {
		return 0
	}
	return 1
}

func customerEvidenceSinglePrimaryProduct(routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil || routerOutput.Ambiguity.IsAmbiguous {
		return ""
	}
	for _, field := range routerOutput.Ambiguity.AmbiguousFields {
		if strings.TrimSpace(field) == "primary_product" || strings.TrimSpace(field) == "products" {
			return ""
		}
	}
	primary := strings.TrimSpace(routerOutput.Slots.PrimaryProduct)
	if primary == "" || primary == "unknown" {
		return ""
	}
	for _, product := range routerOutput.Slots.Products {
		product = strings.TrimSpace(product)
		if product != "" && product != "unknown" && product != primary {
			return ""
		}
	}
	return primary
}

func customerEvidenceProductsInPath(path string) map[string]bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	products := map[string]bool{}
	if strings.Contains(path, "static-ip") || strings.Contains(path, "static_ip") || strings.Contains(path, "shared-static") || strings.Contains(path, "dedicated-static") {
		products["static_ip"] = true
	}
	if strings.Contains(path, "dynamic-ip") || strings.Contains(path, "dynamic_ip") {
		products["dynamic_ip"] = true
	}
	if strings.Contains(path, "overseas-ip") || strings.Contains(path, "overseas_ip") || strings.Contains(path, "product/os") {
		products["overseas_ip"] = true
	}
	return products
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
	parts := make([]string, 0, 2)
	if customerQuestionLooksForOperationURL(question) {
		if preview := customerEvidenceURLWindows(body, terms, 2); strings.TrimSpace(preview) != "" {
			parts = append(parts, preview)
		}
	}
	if preview := relevantTextWindows(body, terms, 2); strings.TrimSpace(preview) != "" {
		parts = append(parts, preview)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n---\n\n")
	}
	return truncateForPrompt(body, maxChars)
}

func relevantTextWindows(body string, terms []string, limit int) string {
	bodyRunes := []rune(body)
	lowerRunes := []rune(strings.ToLower(body))
	type hit struct {
		index int
		score int
	}
	hits := make([]hit, 0)
	for _, term := range terms {
		termRunes := []rune(strings.ToLower(strings.TrimSpace(term)))
		if len(termRunes) == 0 {
			continue
		}
		for _, index := range runeSearchIndices(lowerRunes, termRunes, 20) {
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
	windows := make([]string, 0, len(hits))
	selected := make([]customerEvidenceWindow, 0, len(hits))
	for _, item := range hits {
		start := item.index - 600
		if start < 0 {
			start = 0
		}
		end := item.index + 900
		if end > len(bodyRunes) {
			end = len(bodyRunes)
		}
		if customerEvidenceWindowOverlaps(selected, start, end) {
			continue
		}
		selected = append(selected, customerEvidenceWindow{start: start, end: end})
		windows = append(windows, strings.TrimSpace(string(bodyRunes[start:end])))
		if limit > 0 && len(windows) >= limit {
			break
		}
	}
	return strings.Join(windows, "\n\n---\n\n")
}

type customerEvidenceWindow struct {
	start int
	end   int
}

func customerEvidenceWindowOverlaps(windows []customerEvidenceWindow, start int, end int) bool {
	for _, window := range windows {
		if start < window.end && end > window.start {
			return true
		}
	}
	return false
}

func runeSearchIndices(haystack []rune, needle []rune, limit int) []int {
	if len(haystack) == 0 || len(needle) == 0 || len(needle) > len(haystack) {
		return nil
	}
	out := make([]int, 0)
	for i := 0; i <= len(haystack)-len(needle); i++ {
		matched := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, i)
			if limit > 0 && len(out) >= limit {
				return out
			}
		}
	}
	return out
}

func customerQuestionLooksForOperationURL(question string) bool {
	question = strings.ToLower(strings.TrimSpace(question))
	if question == "" {
		return false
	}
	for _, marker := range []string{
		"入口", "链接", "网址", "哪里", "哪儿", "地址", "页面",
		"购买", "怎么买", "下单", "开通", "试用", "测试", "领取", "下载",
		"切换", "换ip", "换 ip", "更换", "设置", "配置", "白名单", "api", "socks",
	} {
		if strings.Contains(question, marker) {
			return true
		}
	}
	return false
}

func customerEvidenceURLWindows(body string, terms []string, limit int) string {
	lines := strings.Split(body, "\n")
	type candidate struct {
		start int
		end   int
		score int
	}
	candidates := make([]candidate, 0)
	for index, line := range lines {
		if !strings.Contains(line, "http://") && !strings.Contains(line, "https://") {
			continue
		}
		start := index - 1
		if start < 0 {
			start = 0
		}
		end := index + 2
		if end > len(lines) {
			end = len(lines)
		}
		context := strings.ToLower(strings.Join(lines[start:end], "\n"))
		score := 1
		for _, term := range terms {
			term = strings.ToLower(strings.TrimSpace(term))
			if term == "" {
				continue
			}
			if strings.Contains(context, term) {
				score += len([]rune(term))
			}
		}
		candidates = append(candidates, candidate{start: start, end: end, score: score})
	}
	if len(candidates) == 0 {
		return ""
	}
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score ||
				(candidates[j].score == candidates[i].score && candidates[j].start < candidates[i].start) {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	windows := make([]string, 0, len(candidates))
	selected := make([]customerEvidenceWindow, 0, len(candidates))
	for _, item := range candidates {
		if customerEvidenceWindowOverlaps(selected, item.start, item.end) {
			continue
		}
		selected = append(selected, customerEvidenceWindow{start: item.start, end: item.end})
		windows = append(windows, strings.TrimSpace(strings.Join(lines[item.start:item.end], "\n")))
		if limit > 0 && len(windows) >= limit {
			break
		}
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
