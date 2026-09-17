package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wikios/internal/config"
	"wikios/internal/retrieval"
)

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
		"answer_sanitized":        auditMapValue(details["answer_sanitized"]),
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

