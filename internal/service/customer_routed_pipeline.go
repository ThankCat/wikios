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
		"router_thinking":             customerAuditThinking(runtimeSettings.CustomerChat.RouterEnableThinking, routerTrace.Reasoning, runtimeSettings.CustomerChat.PersistThinking),
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
		"client_channel":              customerRequestClientChannel(req),
		"simulation":                  req.Simulation,
		"persist_log":                 shouldPersistCustomerChatLog(req),
		"history_turns":               len(req.History),
		"question_chars":              len([]rune(strings.TrimSpace(req.Question))),
		"router":                      customerRouterTraceMap(customerRouterTraceSummary(routerOutput, routerRaw, len([]rune(customerRouterUserPrompt(req, receivedAt))), nil)),
		"decision":                    customerRouterDecisionAudit(routerOutput),
		"clarification":               customerClarificationAudit(routerOutput),
		"hard_stop":                   customerHardStopAudit(routerOutput),
		"router_duration_ms":          routerDurationMs,
		"router_model_id":             routerAuditModelID,
		"router_model_name":           s.customerAuditModelName(ctx, runtimeSettings.CustomerChat.RouterModelID),
		"router_thinking_enabled":     customerAuditBoolPtrValue(runtimeSettings.CustomerChat.RouterEnableThinking, false),
		"router_thinking":             customerAuditThinking(runtimeSettings.CustomerChat.RouterEnableThinking, routerTrace.Reasoning, runtimeSettings.CustomerChat.PersistThinking),
		"router_raw":                  routerRaw,
		"specialist_model_id":         specialistAuditModelID,
		"specialist_model_name":       s.customerAuditModelName(ctx, runtimeSettings.CustomerChat.SpecialistModelID),
		"specialist_thinking_enabled": customerAuditBoolPtrValue(runtimeSettings.CustomerChat.SpecialistEnableThinking, true),
	}
	if customerRequestClientChannel(req) == "mobile_app" {
		debugTrace["app_policy"] = customerAppPolicyAudit(runtimeSettings.CustomerChat.AppChannelEnabled)
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
		debugTrace["specialist"] = evidence.Profile.Name
		debugTrace["specialist_profile"] = evidence.Profile.summary()
		debugTrace["retrieval_question"] = strings.Join(evidence.Queries, "\n")
		debugTrace["retrieval_cache"] = cacheSummary
		debugTrace["retrieval_diagnostics"] = customerRetrievalDiagnostics(routerOutput, evidence, retrievalDurationMs, customerSpecialistTopK(evidence.Profile, runtimeSettings), customerSpecialistMaxEvidenceChars(evidence.Profile, runtimeSettings))
		debugTrace["quality_signals"] = customerQualitySignals(req, routerOutput, evidence, nil, customerSpecialistTopK(evidence.Profile, runtimeSettings))
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
	debugTrace["specialist"] = evidence.Profile.Name
	debugTrace["specialist_profile"] = evidence.Profile.summary()
	debugTrace["retrieval_question"] = strings.Join(evidence.Queries, "\n")
	debugTrace["candidate_top_k"] = customerSpecialistTopK(evidence.Profile, runtimeSettings)
	debugTrace["max_evidence_chars"] = customerSpecialistMaxEvidenceChars(evidence.Profile, runtimeSettings)
	debugTrace["retrieval_diagnostics"] = customerRetrievalDiagnostics(routerOutput, evidence, retrievalDurationMs, customerSpecialistTopK(evidence.Profile, runtimeSettings), customerSpecialistMaxEvidenceChars(evidence.Profile, runtimeSettings))
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
	userPrompt := s.customerSpecialistDecisionPrompt(req, receivedAt, routerOutput, evidence, runtimeSettings.Support, boundaryPrompt, runtimeSettings.CustomerChat.AppChannelEnabled)
	conversationContext := formatCustomerSpecialistConversationContext(req.History)
	debugTrace["specialist_conversation_context"] = conversationContext
	debugTrace["specialist_input"] = customerSpecialistAuditLLMInput(req.Question, systemPrompt, userPrompt, conversationContext)
	debugTrace["prompt"] = map[string]any{
		"system_chars":   len([]rune(systemPrompt)),
		"user_chars":     len([]rune(userPrompt)),
		"message_count":  2,
		"system_preview": truncateForPrompt(systemPrompt, 1200),
		"user_preview":   truncateForPrompt(userPrompt, 1600),
	}

	hooks := &llmDeltaHooks{}
	if stream != nil && stream.debug && !customerRequestIsMobileApp(req, runtimeSettings.CustomerChat) {
		hooks.Content = stream.feedLLMContent
	}
	specialistModelID := runtimeSettings.CustomerChat.SpecialistModelID
	specialistThinking := runtimeSettings.CustomerChat.SpecialistEnableThinking
	ctx = llm.WithTemperature(ctx, runtimeSettings.CustomerChat.SpecialistTemperature)
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
	debugTrace["specialist_thinking"] = customerAuditThinking(specialistThinking, trace.Reasoning, runtimeSettings.CustomerChat.PersistThinking)
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
		debugTrace["specialist_thinking"] = customerAuditThinking(&noThinking, trace.Reasoning, runtimeSettings.CustomerChat.PersistThinking)
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
	debugTrace["quality_signals"] = customerQualitySignals(req, routerOutput, evidence, &parsed, customerSpecialistTopK(evidence.Profile, runtimeSettings))
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
	if sanitizedAnswer, sanitized := sanitizeCustomerVisibleAnswer(parsed.AnswerText, parsed, routerOutput); sanitized {
		debugTrace["answer_sanitized"] = map[string]any{
			"reason":          "internal_context_removed",
			"original_chars":  len([]rune(parsed.AnswerText)),
			"sanitized_chars": len([]rune(sanitizedAnswer)),
		}
		parsed.AnswerText = sanitizedAnswer
		answer = sanitizedAnswer
	}
	appGuardTriggered := false
	if customerRequestIsMobileApp(req, runtimeSettings.CustomerChat) {
		guardResult := customerAppGuardAnswer(req, answer)
		debugTrace["app_guard"] = guardResult.Audit()
	}
	internalGuardResult := customerInternalBoundaryGuard(parsed, routerOutput)
	debugTrace["internal_boundary_guard"] = internalGuardResult.Audit()
	scenarioGuardResult := customerScenarioAnswerGuardResult{Reason: "pass_model_answer"}
	if appGuardTriggered {
		scenarioGuardResult = customerScenarioAnswerGuardResult{Reason: "skipped_after_app_guard"}
	} else {
		scenarioGuardResult = customerScenarioAnswerGuard(req, parsed, routerOutput)
		scenarioGuardResult = customerScenarioGuardProductLocked(req, routerOutput, scenarioGuardResult)
	}
	debugTrace["scenario_answer_guard"] = scenarioGuardResult.Audit()
	if scenarioGuardResult.Triggered {
		parsed.ReviewRequired = true
		parsed.ReviewReason = firstNonEmpty(parsed.ReviewReason, "服务端检测到答案可能触及硬边界，已保留模型原文并进入复核。")
	}
	unsafeGuardResult := customerUnsafeAnswerGuard(parsed, routerOutput)
	debugTrace["unsafe_answer_guard"] = unsafeGuardResult.Audit()
	if unsafeGuardResult.Triggered {
		parsed.ReviewRequired = true
		parsed.ReviewReason = firstNonEmpty(parsed.ReviewReason, "客户可见答案包含高风险用途话术，已保留模型原文并进入复核。")
	}
	humanContactGuardResult := customerHumanContactGuard(req, parsed, routerOutput)
	debugTrace["human_contact_guard"] = humanContactGuardResult.Audit()
	retrievedPaths := customerSpecialistRetrievedPaths(evidence)
	reviewQueue := NewReviewQueueService(s.deps)
	shouldCreateReview, reviewDecisionReason := s.shouldCreateCustomerReview(req, parsed, routerOutput, retrievedPaths, runtimeSettings.CustomerChat)
	if reviewDecisionReason == "high_risk_without_final_sources" {
		parsed.ReviewRequired = true
		parsed.ReviewReason = firstNonEmpty(parsed.ReviewReason, "高风险价格/售后事实缺少最终引用来源，需要人工审查。")
	}
	reviewWillCreate := !req.Simulation && shouldCreateReview
	reviewDecision := map[string]any{
		"create_review":         reviewWillCreate,
		"decision_reason":       reviewDecisionReason,
		"model_review_signal":   customerHasExplicitReviewSignal(parsed),
		"model_review_required": parsed.ReviewRequired,
		"review_reason":         firstNonEmpty(parsed.ReviewReason, parsed.Notes),
		"answer_mode":           normalizedAnswerMode(parsed.AnswerMode),
		"confidence":            clampConfidence(parsed.Confidence),
		"evidence_confidence":   clampConfidence(parsed.EvidenceConfidence),
		"source_count":          len(parsed.Sources),
		"retrieved_page_count":  len(retrievedPaths),
		"simulation":            req.Simulation,
	}
	debugTrace["review_decision"] = reviewDecision
	debugTrace["model_json_parsed"] = parsed
	if reviewWillCreate {
		item, err := reviewQueue.CreatePending(ctx, ReviewCreateRequest{
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
		if err != nil {
			reviewDecision["error"] = err.Error()
			log.Printf("customer review queue create failed trace=%s err=%v", traceID, err)
		} else if item != nil {
			reviewDecision["review_id"] = item.ID
			reviewDecision["review_path"] = item.Path
		}
	}

	answeredAt := time.Now().Format(time.RFC3339Nano)
	execution.Status = ExecutionSuccess
	execution.EndedAt = time.Now()
	if stream != nil {
		stream.emitRemainingAnswer(answer)
	}
	userIntent := resolveCustomerUserIntent(routerOutput)
	resp := &CustomerChatResponse{
		Answer:         answer,
		AnswerMode:     normalizedAnswerMode(parsed.AnswerMode),
		ReviewRequired: parsed.ReviewRequired,
		SourceCount:    len(parsed.Sources),
		UserIntent:     userIntent,
		ReceivedAt:     receivedAt,
		AnsweredAt:     answeredAt,
		Details:        s.customerTraceDetails(req, parsed, trace, execution, evidence.Sources, retrievedPaths, debugTrace),
	}
	resp.Details["specialist"] = evidence.Profile.Name
	resp.Details["user_intent"] = userIntent
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

func (s *CustomerChatService) customerSpecialistDecisionPrompt(req CustomerChatRequest, receivedAt string, routerOutput *CustomerRouterOutput, evidence customerSpecialistEvidenceResult, support RuntimeSupportSettings, boundaryPrompt string, appChannelEnabled ...bool) string {
	candidateText := strings.TrimSpace(strings.Join(evidence.ContentBlocks, "\n\n"))
	if candidateText == "" {
		candidateText = "[]"
	}
	appPolicyEnabled := true
	if len(appChannelEnabled) > 0 {
		appPolicyEnabled = appChannelEnabled[0]
	}
	parts := []string{
		"current_time:",
		receivedAt,
		"",
		"current_customer_time:",
		formatCustomerBeijingTime(receivedAt),
		"",
		"client_channel:",
		customerRequestClientChannel(req),
		"",
	}
	if appPolicyEnabled && customerRequestClientChannel(req) == "mobile_app" {
		parts = append(parts,
			"mobile_app_channel_policy:",
			strings.TrimSpace(customerMobileAppChannelPolicyPrompt()),
			"",
		)
	}
	parts = append(parts,
		"user_message:",
		strings.TrimSpace(req.Question),
		"",
		"conversation_context:",
		formatCustomerSpecialistConversationContext(req.History),
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
	)
	return strings.Join(parts, "\n")
}

func formatCustomerSpecialistConversationContext(history []ChatMessage) string {
	context := strings.TrimSpace(formatRouterConversationContext(history, 10))
	if context == "" {
		return "[]"
	}
	return context
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
		"question_stage: " + output.QuestionStage,
		"user_goal: " + output.UserGoal,
		"has_product: " + fmt.Sprintf("%t", output.HasProduct),
		"needs_product_clarification: " + fmt.Sprintf("%t", output.NeedsProductClarification),
		"clarification_target: " + output.ClarificationTarget,
		"answer_strategy: " + output.AnswerStrategy,
		"risk_boundary: " + output.RiskBoundary,
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
	var parsed customerChatLLMOutput
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		return customerChatLLMOutput{}, fmt.Errorf("decode routed specialist output: %w", err)
	}
	normalized := normalizeCustomerChatOutput(parsed)
	// Only the customer-visible content is essential. Some providers don't fully
	// honor a strict json_schema and drop metadata fields (e.g. answer_mode); in
	// that case we default the metadata in normalizeCustomerChatOutput rather than
	// discarding an otherwise-valid, well-sourced answer. We only reject when there
	// is no usable content at all (neither an answer nor a review_question to
	// recover an answer from).
	if strings.TrimSpace(normalized.AnswerText) == "" && strings.TrimSpace(normalized.ReviewQuestion) == "" {
		return customerChatLLMOutput{}, fmt.Errorf("invalid routed specialist output: empty answer")
	}
	return normalized, nil
}

func shouldRetryCustomerSpecialistParseWithoutThinking(enableThinking *bool, err error) bool {
	if err == nil {
		return false
	}
	if !customerAuditBoolPtrValue(enableThinking, true) {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "empty answer")
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
