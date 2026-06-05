package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"wikios/internal/llm"
)

var customerRoutedV1Specialists = map[string]bool{
	"pricing":             true,
	"product":             true,
	"safety":              true,
	"purchase":            true,
	"technical":           true,
	"troubleshooting":     true,
	"reception":           true,
	"billing_after_sales": true,
}

func (s *CustomerChatService) answerRouted(ctx context.Context, traceID string, req CustomerChatRequest, stream *customerChatStream, runtimeSettings RuntimeSettings) (*CustomerChatResponse, error) {
	receivedAt := firstNonEmpty(strings.TrimSpace(req.ReceivedAt), time.Now().Format(time.RFC3339Nano))
	req.ReceivedAt = receivedAt
	preflight, handled, err := s.customerRoutedPreflight(ctx, traceID, req, receivedAt, stream)
	if err != nil {
		s.maybeWriteCustomerChatErrorLog(traceID, req, "final_response", err, nil)
		return nil, err
	}
	if handled != nil {
		return handled, nil
	}

	routerStart := time.Now()
	routerOutput, routerRaw, routerTrace, routerErr := s.routeCustomerQuestion(ctx, req, receivedAt, runtimeSettings.CustomerChat)
	routerDurationMs := time.Since(routerStart).Milliseconds()
	if routerErr != nil {
		if customerChatContextDone(ctx, routerErr) {
			log.Printf("customer routed router canceled trace=%s question=%q err=%v", traceID, truncateForPrompt(req.Question, 80), routerErr)
			return nil, routerErr
		}
		s.maybeWriteCustomerChatErrorLog(traceID, req, customerRouterAuditErrorStage(routerRaw, routerErr), routerErr, s.customerRouterAuditDetails(ctx, traceID, req, receivedAt, runtimeSettings, routerOutput, routerRaw, routerTrace, routerDurationMs))
		log.Printf("customer routed router failed trace=%s question=%q err=%v", traceID, truncateForPrompt(req.Question, 80), routerErr)
		return nil, routerErr
	}
	log.Printf(
		"customer routed router trace=%s contract_version=%s specialist=%s routing_confidence=%.2f routing_reason=%q ambiguous=%t model_id=%s thinking=%s needs_retrieval=%t retrieval_queries=%d duration_ms=%d",
		traceID,
		routerOutput.ContractVersion,
		routerOutput.Specialist,
		routerOutput.RoutingConfidence,
		customerRouterSafeLogText(routerOutput.RoutingReason),
		routerOutput.Ambiguity.IsAmbiguous,
		customerConfiguredModelIDForLog(runtimeSettings.CustomerChat.RouterModelID),
		customerThinkingForLog(runtimeSettings.CustomerChat.RouterEnableThinking),
		routerOutput.NeedsRetrieval,
		len(routerOutput.RetrievalQueries),
		routerDurationMs,
	)
	if !customerRoutedV1Specialists[routerOutput.Specialist] {
		err := fmt.Errorf("unsupported routed specialist: %s", routerOutput.Specialist)
		s.maybeWriteCustomerChatErrorLog(traceID, req, "router_parse", err, s.customerRouterAuditDetails(ctx, traceID, req, receivedAt, runtimeSettings, routerOutput, routerRaw, routerTrace, routerDurationMs))
		return nil, err
	}

	resp, err := s.answerWithSpecialist(ctx, traceID, req, receivedAt, stream, runtimeSettings, routerOutput, routerRaw, routerTrace, routerDurationMs)
	if err != nil {
		if customerChatContextDone(ctx, err) {
			log.Printf("customer routed specialist canceled trace=%s specialist=%s question=%q err=%v", traceID, routerOutput.Specialist, truncateForPrompt(req.Question, 80), err)
			return nil, err
		}
		log.Printf("customer routed specialist failed trace=%s specialist=%s question=%q err=%v", traceID, routerOutput.Specialist, truncateForPrompt(req.Question, 80), err)
		return nil, err
	}
	if preflight != nil {
		resp.Details["preflight"] = preflight
	}
	return resp, nil
}

type customerRoutedPreflightResult struct {
	Execution *Execution
	Queue     *ReviewQueueService
}

func (s *CustomerChatService) customerRoutedPreflight(ctx context.Context, traceID string, req CustomerChatRequest, receivedAt string, stream *customerChatStream) (*customerRoutedPreflightResult, *CustomerChatResponse, error) {
	execution := NewExecution("customer-routed-preflight")
	intakeStart := customerTraceStepStart(ctx, "接收 routed customer 问答请求", "customer.routed.intake", map[string]any{
		"question":      truncateForPrompt(req.Question, 600),
		"history_turns": len(req.History),
		"session_id":    strings.TrimSpace(req.SessionID),
		"simulation":    req.Simulation,
	})
	customerTraceStepFinish(ctx, execution, "接收 routed customer 问答请求", "customer.routed.intake", intakeStart, nil, map[string]any{
		"decision": "continue",
	}, nil)
	return &customerRoutedPreflightResult{Execution: execution}, nil, nil
}

func (s *CustomerChatService) customerRouterAuditDetails(ctx context.Context, traceID string, req CustomerChatRequest, receivedAt string, runtimeSettings RuntimeSettings, routerOutput *CustomerRouterOutput, routerRaw string, routerTrace LLMTrace, routerDurationMs int64) map[string]any {
	routerModelID := firstNonEmpty(s.customerAuditModelID(ctx, runtimeSettings.CustomerChat.RouterModelID), customerConfiguredModelIDForLog(runtimeSettings.CustomerChat.RouterModelID))
	specialistModelID := firstNonEmpty(s.customerAuditModelID(ctx, runtimeSettings.CustomerChat.SpecialistModelID), customerConfiguredModelIDForLog(runtimeSettings.CustomerChat.SpecialistModelID))
	return map[string]any{
		"trace_id":                    strings.TrimSpace(traceID),
		"received_at":                 receivedAt,
		"simulation":                  req.Simulation,
		"persist_log":                 shouldPersistCustomerChatLog(req),
		"history_turns":               len(req.History),
		"question_chars":              len([]rune(strings.TrimSpace(req.Question))),
		"router":                      customerRouterTraceMap(customerRouterTraceSummary(routerOutput, routerRaw, len([]rune(customerRouterUserPrompt(req, receivedAt))), nil)),
		"router_duration_ms":          routerDurationMs,
		"router_model_id":             routerModelID,
		"router_model_name":           s.customerAuditModelName(ctx, runtimeSettings.CustomerChat.RouterModelID),
		"router_thinking_enabled":     customerAuditBoolPtrValue(runtimeSettings.CustomerChat.RouterEnableThinking, false),
		"router_thinking":             customerAuditThinking(runtimeSettings.CustomerChat.RouterEnableThinking, routerTrace.Reasoning),
		"router_raw":                  routerRaw,
		"specialist_model_id":         specialistModelID,
		"specialist_model_name":       s.customerAuditModelName(ctx, runtimeSettings.CustomerChat.SpecialistModelID),
		"specialist_thinking_enabled": customerAuditBoolPtrValue(runtimeSettings.CustomerChat.SpecialistEnableThinking, true),
	}
}

func (s *CustomerChatService) answerWithSpecialist(ctx context.Context, traceID string, req CustomerChatRequest, receivedAt string, stream *customerChatStream, runtimeSettings RuntimeSettings, routerOutput *CustomerRouterOutput, routerRaw string, routerTrace LLMTrace, routerDurationMs int64) (*CustomerChatResponse, error) {
	if routerOutput == nil {
		return nil, errors.New("missing router output")
	}
	req.ReceivedAt = receivedAt
	execution := NewExecution("customer-routed-answer")
	routerAuditModelID := firstNonEmpty(s.customerAuditModelID(ctx, runtimeSettings.CustomerChat.RouterModelID), customerConfiguredModelIDForLog(runtimeSettings.CustomerChat.RouterModelID))
	specialistAuditModelID := firstNonEmpty(s.customerAuditModelID(ctx, runtimeSettings.CustomerChat.SpecialistModelID), customerConfiguredModelIDForLog(runtimeSettings.CustomerChat.SpecialistModelID))
	debugTrace := map[string]any{
		"trace_id":                    strings.TrimSpace(traceID),
		"received_at":                 receivedAt,
		"simulation":                  req.Simulation,
		"persist_log":                 shouldPersistCustomerChatLog(req),
		"history_turns":               len(req.History),
		"question_chars":              len([]rune(strings.TrimSpace(req.Question))),
		"router":                      customerRouterTraceMap(customerRouterTraceSummary(routerOutput, routerRaw, len([]rune(customerRouterUserPrompt(req, receivedAt))), nil)),
		"router_duration_ms":          routerDurationMs,
		"router_model_id":             routerAuditModelID,
		"router_model_name":           s.customerAuditModelName(ctx, runtimeSettings.CustomerChat.RouterModelID),
		"router_thinking_enabled":     customerAuditBoolPtrValue(runtimeSettings.CustomerChat.RouterEnableThinking, false),
		"router_thinking":             customerAuditThinking(runtimeSettings.CustomerChat.RouterEnableThinking, routerTrace.Reasoning),
		"router_raw":                  routerRaw,
		"specialist_model_id":         specialistAuditModelID,
		"specialist_model_name":       s.customerAuditModelName(ctx, runtimeSettings.CustomerChat.SpecialistModelID),
		"specialist_thinking_enabled": customerAuditBoolPtrValue(runtimeSettings.CustomerChat.SpecialistEnableThinking, true),
	}

	evidenceStart := customerTraceStepStart(ctx, "按 Specialist 检索证据", "customer.specialist.retrieve", map[string]any{
		"specialist": routerOutput.Specialist,
		"queries":    customerSpecialistRetrievalQueries(routerOutput),
	})
	evidence := s.retrieveCustomerSpecialistEvidence(ctx, traceID, routerOutput, runtimeSettings)
	retrievalDurationMs := time.Since(evidenceStart).Milliseconds()
	cacheSummary := evidence.CacheTrace.summary()
	cacheSummary["duration_ms"] = retrievalDurationMs
	if evidence.Error != "" {
		err := fmt.Errorf("specialist evidence retrieval: %s", evidence.Error)
		debugTrace["specialist"] = evidence.Profile.summary()
		debugTrace["retrieval_question"] = strings.Join(evidence.Queries, "\n")
		debugTrace["retrieval_cache"] = cacheSummary
		customerTraceStepFinish(ctx, execution, "按 Specialist 检索证据", "customer.specialist.retrieve", evidenceStart, nil, map[string]any{
			"specialist": routerOutput.Specialist,
			"error":      evidence.Error,
			"cache":      cacheSummary,
		}, err)
		s.maybeWriteCustomerChatErrorLog(traceID, req, "retrieval", err, debugTrace)
		return nil, err
	}
	customerTraceStepFinish(ctx, execution, "按 Specialist 检索证据", "customer.specialist.retrieve", evidenceStart, nil, map[string]any{
		"specialist":          evidence.Profile.Name,
		"source_count":        len(evidence.Sources),
		"content_block_count": len(evidence.ContentBlocks),
		"queries":             evidence.Queries,
		"cache":               cacheSummary,
		"candidates":          customerRetrievedPageSummaries(evidence.Candidates, 12),
		"sources":             customerSourceSummaries(evidence.Sources),
	}, nil)
	log.Printf(
		"customer routed retrieval cache trace=%s specialist=%s duration_ms=%d qmd_hit=%d qmd_miss=%d page_hit=%d page_miss=%d executed_queries=%d attempted_queries=%d skipped_queries=%d sources=%d",
		traceID,
		evidence.Profile.Name,
		retrievalDurationMs,
		evidence.CacheTrace.QMDHits,
		evidence.CacheTrace.QMDMisses,
		evidence.CacheTrace.ReadPageHits,
		evidence.CacheTrace.ReadPageMisses,
		len(evidence.CacheTrace.ExecutedRetrievalQueries),
		len(evidence.CacheTrace.AttemptedRetrievalQueries),
		evidence.CacheTrace.SkippedRetrievalQueryCount,
		len(evidence.Sources),
	)
	debugTrace["specialist"] = evidence.Profile.summary()
	debugTrace["retrieval_question"] = strings.Join(evidence.Queries, "\n")
	debugTrace["candidate_top_k"] = customerSpecialistTopK(evidence.Profile, runtimeSettings)
	debugTrace["max_evidence_chars"] = customerSpecialistMaxEvidenceChars(evidence.Profile, runtimeSettings)
	debugTrace["retrieved_candidates"] = customerRetrievedPageSummaries(evidence.Candidates, 12)
	debugTrace["evidence"] = evidence.EvidenceTrace
	debugTrace["sources"] = customerSourceSummaries(evidence.Sources)
	debugTrace["retrieved_paths"] = customerSpecialistRetrievedPaths(evidence)
	debugTrace["retrieval_cache"] = cacheSummary
	systemPrompt, err := s.loadCustomerSpecialistSystemPrompt(evidence.Profile)
	if err != nil {
		s.maybeWriteCustomerChatErrorLog(traceID, req, "specialist_call", err, debugTrace)
		return nil, err
	}
	boundaryPrompt, err := s.loadCustomerSpecialistBoundary()
	if err != nil {
		s.maybeWriteCustomerChatErrorLog(traceID, req, "specialist_call", err, debugTrace)
		return nil, err
	}
	userPrompt := s.customerSpecialistDecisionPrompt(req, receivedAt, routerOutput, evidence, runtimeSettings.Support, boundaryPrompt)
	debugTrace["specialist_input"] = customerSpecialistAuditLLMInput(req.Question, systemPrompt, userPrompt)
	debugTrace["prompt"] = map[string]any{
		"system_chars":   len([]rune(systemPrompt)),
		"user_chars":     len([]rune(userPrompt)),
		"message_count":  2,
		"system_preview": truncateForPrompt(systemPrompt, 1200),
		"user_preview":   truncateForPrompt(userPrompt, 1600),
	}

	hooks := &llmDeltaHooks{}
	if stream != nil && stream.debug {
		hooks.Content = stream.feedLLMContent
	}
	specialistModelID := runtimeSettings.CustomerChat.SpecialistModelID
	specialistThinking := runtimeSettings.CustomerChat.SpecialistEnableThinking
	specialistMessages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	callSpecialist := func(enableThinking *bool, stepName string) (string, LLMTrace, int64, error) {
		start := time.Now()
		text, trace, err := s.executeLLMTraceWithOptionsAndResponseFormat(ctx, execution, llmModelIDToken(specialistModelID), specialistMessages, stepName, hooks, enableThinking, customerSpecialistResponseFormat())
		return text, trace, time.Since(start).Milliseconds(), err
	}
	llmText, trace, specialistDurationMs, err := callSpecialist(specialistThinking, "llm customer specialist "+evidence.Profile.Name)
	debugTrace["specialist_duration_ms"] = specialistDurationMs
	debugTrace["specialist_thinking"] = customerAuditThinking(specialistThinking, trace.Reasoning)
	if err != nil {
		if customerChatRequestCanceled(ctx, err) {
			return nil, err
		}
		s.maybeWriteCustomerChatErrorLog(traceID, req, "specialist_call", err, debugTrace)
		return nil, err
	}
	log.Printf(
		"customer routed specialist llm trace=%s specialist=%s model_id=%s thinking=%s duration_ms=%d prompt_chars=%d",
		traceID,
		evidence.Profile.Name,
		customerConfiguredModelIDForLog(specialistModelID),
		customerThinkingForLog(specialistThinking),
		specialistDurationMs,
		len([]rune(systemPrompt))+len([]rune(userPrompt)),
	)

	parseSpecialist := func(raw string) (customerChatLLMOutput, string, error) {
		parsed, err := s.parseCustomerRoutedSpecialistOutput(raw)
		if err != nil {
			return parsed, "", err
		}
		parsed, answerRecoveredFrom := recoverCustomerSpecialistMisplacedAnswer(parsed)
		parsed.Sources = filterCustomerChatSources(parsed.Sources, evidence.Sources)
		answer := strings.TrimSpace(parsed.AnswerText)
		if answer == "" {
			return parsed, answerRecoveredFrom, fmt.Errorf(
				"specialist returned empty answer (answer_mode=%s review_required=%v)",
				normalizedAnswerMode(parsed.AnswerMode),
				parsed.ReviewRequired,
			)
		}
		parsed.AnswerText = answer
		return parsed, answerRecoveredFrom, nil
	}
	parsed, answerRecoveredFrom, parseErr := parseSpecialist(llmText)
	if shouldRetryCustomerSpecialistParseWithoutThinking(specialistThinking, parseErr) {
		debugTrace["specialist_first_attempt"] = map[string]any{
			"raw_output":         strings.TrimSpace(llmText),
			"error":              parseErr.Error(),
			"thinking_enabled":   customerAuditBoolPtrValue(specialistThinking, true),
			"thinking_chars":     len([]rune(strings.TrimSpace(trace.Reasoning))),
			"duration_ms":        specialistDurationMs,
			"response_raw_chars": len([]rune(llmText)),
		}
		noThinking := false
		retryText, retryTrace, retryDurationMs, retryErr := callSpecialist(&noThinking, "llm customer specialist "+evidence.Profile.Name+" retry without thinking")
		debugTrace["specialist_retry"] = map[string]any{
			"trigger":          "parse_error",
			"thinking_enabled": false,
			"duration_ms":      retryDurationMs,
		}
		if retryErr != nil {
			if customerChatRequestCanceled(ctx, retryErr) {
				return nil, retryErr
			}
			s.maybeWriteCustomerChatErrorLog(traceID, req, "specialist_call", retryErr, debugTrace)
			return nil, retryErr
		}
		llmText = retryText
		trace = retryTrace
		specialistDurationMs += retryDurationMs
		debugTrace["specialist_duration_ms"] = specialistDurationMs
		debugTrace["specialist_thinking_enabled"] = false
		debugTrace["specialist_thinking"] = customerAuditThinking(&noThinking, trace.Reasoning)
		parsed, answerRecoveredFrom, parseErr = parseSpecialist(llmText)
	}

	parseStart := customerTraceStepStart(ctx, "解析 Specialist JSON 输出", "customer.specialist.parse", map[string]any{
		"raw_chars": len([]rune(llmText)),
	})
	if parseErr != nil {
		customerTraceStepFinish(ctx, execution, "解析 Specialist JSON 输出", "customer.specialist.parse", parseStart, nil, nil, parseErr)
		debugTrace["model_json_raw"] = llmText
		if strings.TrimSpace(parsed.AnswerMode) != "" || strings.TrimSpace(parsed.AnswerText) != "" {
			debugTrace["model_json_parsed"] = parsed
		}
		s.maybeWriteCustomerChatErrorLog(traceID, req, "specialist_parse", parseErr, debugTrace)
		return nil, parseErr
	}
	if answerRecoveredFrom != "" {
		debugTrace["answer_recovered_from"] = answerRecoveredFrom
	}
	modelParsedForLog := parsed
	debugTrace["model_json_raw"] = llmText
	debugTrace["model_json_parsed"] = parsed
	customerTraceStepFinish(ctx, execution, "解析 Specialist JSON 输出", "customer.specialist.parse", parseStart, nil, map[string]any{
		"answer_mode":         normalizedAnswerMode(parsed.AnswerMode),
		"confidence":          clampConfidence(parsed.Confidence),
		"evidence_confidence": clampConfidence(parsed.EvidenceConfidence),
		"review_required":     parsed.ReviewRequired,
		"source_count":        len(parsed.Sources),
	}, nil)

	answer := strings.TrimSpace(parsed.AnswerText)
	if answer == "" {
		err := fmt.Errorf(
			"specialist returned empty answer (answer_mode=%s review_required=%v)",
			normalizedAnswerMode(parsed.AnswerMode),
			parsed.ReviewRequired,
		)
		s.maybeWriteCustomerChatErrorLog(traceID, req, "specialist_parse", err, debugTrace)
		return nil, err
	}
	parsed.AnswerText = answer
	retrievedPaths := customerSpecialistRetrievedPaths(evidence)
	reviewQueue := NewReviewQueueService(s.deps)
	reviewWillCreate := !req.Simulation && s.shouldCreateCustomerReview(req, parsed, runtimeSettings.CustomerChat)
	debugTrace["review_decision"] = map[string]any{
		"create_review": reviewWillCreate,
		"review_reason": firstNonEmpty(parsed.ReviewReason, parsed.Notes),
		"answer_mode":   normalizedAnswerMode(parsed.AnswerMode),
	}
	if reviewWillCreate {
		_, _ = reviewQueue.CreatePending(ctx, ReviewCreateRequest{
			Question:            firstNonEmpty(parsed.ReviewQuestion, routerOutput.RewrittenQuestion, req.Question),
			OriginalQuestion:    req.Question,
			DraftAnswer:         answer,
			SuggestedTargetPath: parsed.SuggestedTargetPath,
			Confidence:          clampConfidence(parsed.Confidence),
			BoundaryReason:      firstNonEmpty(parsed.ReviewReason, parsed.Notes, "低可信 customer chat 回答，等待人工审查。"),
			MatchedPages:        retrievedPaths,
			SessionID:           req.SessionID,
			QuestionMessageID:   req.QuestionMessageID,
			AnswerMessageID:     req.AnswerMessageID,
			QuestionCreatedAt:   firstNonEmpty(req.QuestionCreatedAt, receivedAt),
			AnswerCreatedAt:     time.Now().Format(time.RFC3339Nano),
			AnswerMode:          normalizedAnswerMode(parsed.AnswerMode),
			EvidenceConfidence:  clampConfidence(parsed.EvidenceConfidence),
			RetrievedPages:      retrievedPaths,
			ConversationExcerpt: customerConversationExcerpt(req),
		})
	}

	answeredAt := time.Now().Format(time.RFC3339Nano)
	execution.Status = ExecutionSuccess
	execution.EndedAt = time.Now()
	if stream != nil {
		stream.emitRemainingAnswer(answer)
	}
	resp := &CustomerChatResponse{
		Answer:     answer,
		ReceivedAt: receivedAt,
		AnsweredAt: answeredAt,
		Details:    s.customerTraceDetails(req, parsed, trace, execution, evidence.Sources, retrievedPaths, debugTrace),
	}
	resp.Details["specialist"] = evidence.Profile.Name
	s.maybeWriteCustomerChatLog(traceID, req, resp, map[string]any{
		"decision":          "routed_specialist_answer",
		"specialist":        evidence.Profile.Name,
		"router_thinking":   routerTrace.Reasoning,
		"thinking":          trace.Reasoning,
		"model_json_raw":    llmText,
		"model_json_parsed": modelParsedForLog,
		"final_json":        parsed,
	})
	return resp, nil
}

func (s *CustomerChatService) customerSpecialistDecisionPrompt(req CustomerChatRequest, receivedAt string, routerOutput *CustomerRouterOutput, evidence customerSpecialistEvidenceResult, support RuntimeSupportSettings, boundaryPrompt string) string {
	candidateText := strings.TrimSpace(strings.Join(evidence.ContentBlocks, "\n\n"))
	if candidateText == "" {
		candidateText = "[]"
	}
	return strings.Join([]string{
		"current_time:",
		receivedAt,
		"",
		"current_customer_time:",
		formatCustomerBeijingTime(receivedAt),
		"",
		"user_message:",
		strings.TrimSpace(req.Question),
		"",
		"router_output:",
		formatCustomerRouterOutputForSpecialist(routerOutput),
		"",
		"current_customer_contacts:",
		s.supportContactPrompt(support),
		"",
		"hard_boundary:",
		strings.TrimSpace(boundaryPrompt),
		"",
		"candidate_page_paths:",
		formatSourceRefList(evidence.Sources),
		"",
		"candidate_pages:",
		candidateText,
	}, "\n")
}

func formatCustomerRouterOutputForSpecialist(output *CustomerRouterOutput) string {
	if output == nil {
		return "{}"
	}
	lines := []string{
		"contract_version: " + output.ContractVersion,
		"specialist: " + output.Specialist,
		"routing_confidence: " + fmt.Sprintf("%.2f", output.RoutingConfidence),
		"routing_reason: " + output.RoutingReason,
		"intent: " + output.Intent,
		"rewritten_question: " + output.RewrittenQuestion,
		"history_summary: " + output.HistorySummary,
		"slots:",
		"  primary_product: " + output.Slots.PrimaryProduct,
		"  products: " + strings.Join(output.Slots.Products, ", "),
		"  static_type: " + output.Slots.StaticType,
		"  ip_type: " + output.Slots.IPType,
		"  bandwidth: " + output.Slots.Bandwidth,
		"  quantity: " + output.Slots.Quantity,
		"  scenario: " + output.Slots.Scenario,
		"  platform: " + output.Slots.Platform,
		"  device: " + output.Slots.Device,
		"  error_code: " + output.Slots.ErrorCode,
		"ambiguity:",
		"  is_ambiguous: " + fmt.Sprintf("%t", output.Ambiguity.IsAmbiguous),
		"  ambiguous_fields: " + strings.Join(output.Ambiguity.AmbiguousFields, ", "),
		"  reason: " + output.Ambiguity.Reason,
		"missing_info: " + strings.Join(output.MissingInfo, ", "),
		"risk_flags: " + strings.Join(output.RiskFlags, ", "),
		"needs_retrieval: " + fmt.Sprintf("%t", output.NeedsRetrieval),
		"retrieval_queries: " + strings.Join(output.RetrievalQueries, " | "),
		"handoff_notes: " + output.HandoffNotes,
	}
	return strings.Join(lines, "\n")
}

func (s *CustomerChatService) parseCustomerRoutedSpecialistOutput(llmText string) (customerChatLLMOutput, error) {
	fields := map[string]any{}
	if err := llm.DecodeJSONObject(llmText, &fields); err != nil {
		return customerChatLLMOutput{}, fmt.Errorf("decode routed specialist output: %w", err)
	}
	if missing := missingCustomerSpecialistRequiredFields(fields); len(missing) > 0 {
		return customerChatLLMOutput{}, fmt.Errorf("invalid routed specialist output: missing required fields: %s", strings.Join(missing, ", "))
	}
	var parsed customerChatLLMOutput
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		return customerChatLLMOutput{}, fmt.Errorf("decode routed specialist output: %w", err)
	}
	return normalizeCustomerChatOutput(parsed), nil
}

func missingCustomerSpecialistRequiredFields(fields map[string]any) []string {
	required := []string{
		"answer_mode",
		"answer",
		"review_question",
		"confidence",
		"evidence_confidence",
		"review_required",
		"review_reason",
		"suggested_target_path",
		"sources",
		"notes",
	}
	missing := make([]string, 0)
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

func shouldRetryCustomerSpecialistParseWithoutThinking(enableThinking *bool, err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "missing required fields") {
		return true
	}
	if !customerAuditBoolPtrValue(enableThinking, true) {
		return false
	}
	return strings.Contains(text, "empty answer")
}

func customerSpecialistRetrievedPaths(evidence customerSpecialistEvidenceResult) []string {
	paths := make([]string, 0, len(evidence.Sources)+len(evidence.Candidates))
	seen := map[string]bool{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, source := range evidence.Sources {
		add(source.Path)
	}
	for _, candidate := range evidence.Candidates {
		add(candidate.Path)
	}
	return paths
}

func customerConfiguredModelIDForLog(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "active"
	}
	return id
}

func customerThinkingForLog(value *bool) string {
	if value == nil {
		return "default"
	}
	if *value {
		return "true"
	}
	return "false"
}

func customerRouterSafeLogText(value string) string {
	return redactCustomerChatLogString(truncateForPrompt(value, 160))
}
