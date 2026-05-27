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

var publicRoutedV1Specialists = map[string]bool{
	"pricing":             true,
	"product":             true,
	"safety":              true,
	"purchase":            true,
	"technical":           true,
	"troubleshooting":     true,
	"reception":           true,
	"billing_after_sales": true,
}

func (s *PublicQueryService) answerRouted(ctx context.Context, traceID string, req PublicAnswerRequest, stream *publicAnswerStream, runtimeSettings RuntimeSettings) (*PublicAnswerResponse, error) {
	receivedAt := firstNonEmpty(strings.TrimSpace(req.ReceivedAt), time.Now().Format(time.RFC3339Nano))
	preflight, handled, err := s.publicRoutedPreflight(ctx, traceID, req, receivedAt, stream)
	if err != nil {
		return nil, err
	}
	if handled != nil {
		return handled, nil
	}

	routerStart := time.Now()
	routerOutput, routerRaw, routerErr := s.routePublicQuestion(ctx, req, receivedAt, runtimeSettings.PublicQuery)
	routerDurationMs := time.Since(routerStart).Milliseconds()
	if routerErr != nil {
		if publicAnswerContextDone(ctx, routerErr) {
			log.Printf("public routed router canceled trace=%s question=%q err=%v", traceID, truncateForPrompt(req.Question, 80), routerErr)
			return nil, routerErr
		}
		log.Printf("public routed router failed trace=%s question=%q err=%v", traceID, truncateForPrompt(req.Question, 80), routerErr)
		return nil, routerErr
	}
	log.Printf(
		"public routed router trace=%s specialist=%s model_id=%s thinking=%s needs_retrieval=%t retrieval_queries=%d duration_ms=%d",
		traceID,
		routerOutput.Specialist,
		publicConfiguredModelIDForLog(runtimeSettings.PublicQuery.RouterModelID),
		publicThinkingForLog(runtimeSettings.PublicQuery.RouterEnableThinking),
		routerOutput.NeedsRetrieval,
		len(routerOutput.RetrievalQueries),
		routerDurationMs,
	)
	if !publicRoutedV1Specialists[routerOutput.Specialist] {
		return nil, fmt.Errorf("unsupported routed specialist: %s", routerOutput.Specialist)
	}

	resp, err := s.answerWithSpecialist(ctx, traceID, req, receivedAt, stream, runtimeSettings, routerOutput, routerRaw)
	if err != nil {
		if publicAnswerContextDone(ctx, err) {
			log.Printf("public routed specialist canceled trace=%s specialist=%s question=%q err=%v", traceID, routerOutput.Specialist, truncateForPrompt(req.Question, 80), err)
			return nil, err
		}
		log.Printf("public routed specialist failed trace=%s specialist=%s question=%q err=%v", traceID, routerOutput.Specialist, truncateForPrompt(req.Question, 80), err)
		return nil, err
	}
	if preflight != nil {
		resp.Details["preflight"] = preflight
	}
	return resp, nil
}

type publicRoutedPreflightResult struct {
	Execution *Execution
	Queue     *ReviewQueueService
}

func (s *PublicQueryService) publicRoutedPreflight(ctx context.Context, traceID string, req PublicAnswerRequest, receivedAt string, stream *publicAnswerStream) (*publicRoutedPreflightResult, *PublicAnswerResponse, error) {
	execution := NewExecution("public-routed-preflight")
	intakeStart := publicTraceStepStart(ctx, "接收 routed public 问答请求", "public.routed.intake", map[string]any{
		"question":      truncateForPrompt(req.Question, 600),
		"history_turns": len(req.History),
		"session_id":    strings.TrimSpace(req.SessionID),
		"simulation":    req.Simulation,
	})
	publicTraceStepFinish(ctx, execution, "接收 routed public 问答请求", "public.routed.intake", intakeStart, nil, map[string]any{
		"decision": "continue",
	}, nil)
	return &publicRoutedPreflightResult{Execution: execution}, nil, nil
}

func (s *PublicQueryService) answerWithSpecialist(ctx context.Context, traceID string, req PublicAnswerRequest, receivedAt string, stream *publicAnswerStream, runtimeSettings RuntimeSettings, routerOutput *PublicRouterOutput, routerRaw string) (*PublicAnswerResponse, error) {
	if routerOutput == nil {
		return nil, errors.New("missing router output")
	}
	execution := NewExecution("public-routed-answer")
	debugTrace := map[string]any{
		"trace_id":       strings.TrimSpace(traceID),
		"received_at":    receivedAt,
		"simulation":     req.Simulation,
		"persist_log":    shouldPersistPublicAnswerLog(req),
		"history_turns":  len(req.History),
		"question_chars": len([]rune(strings.TrimSpace(req.Question))),
		"router":         publicRouterTraceMap(publicRouterTraceSummary(routerOutput, routerRaw, len([]rune(publicRouterUserPrompt(req, receivedAt))), nil)),
	}

	evidenceStart := publicTraceStepStart(ctx, "按 Specialist 检索证据", "public.specialist.retrieve", map[string]any{
		"specialist": routerOutput.Specialist,
		"queries":    publicSpecialistRetrievalQueries(routerOutput),
	})
	evidence := s.retrievePublicSpecialistEvidence(ctx, traceID, routerOutput, runtimeSettings)
	if evidence.Error != "" {
		publicTraceStepFinish(ctx, execution, "按 Specialist 检索证据", "public.specialist.retrieve", evidenceStart, nil, map[string]any{
			"specialist": routerOutput.Specialist,
			"error":      evidence.Error,
		}, nil)
		return nil, fmt.Errorf("specialist evidence retrieval: %s", evidence.Error)
	}
	publicTraceStepFinish(ctx, execution, "按 Specialist 检索证据", "public.specialist.retrieve", evidenceStart, nil, map[string]any{
		"specialist":          evidence.Profile.Name,
		"source_count":        len(evidence.Sources),
		"content_block_count": len(evidence.ContentBlocks),
		"queries":             evidence.Queries,
		"cache":               evidence.CacheTrace.summary(),
		"candidates":          publicRetrievedPageSummaries(evidence.Candidates, 12),
		"sources":             publicSourceSummaries(evidence.Sources),
	}, nil)
	log.Printf(
		"public routed retrieval cache trace=%s specialist=%s duration_ms=%d qmd_hit=%d qmd_miss=%d page_hit=%d page_miss=%d executed_queries=%d attempted_queries=%d skipped_queries=%d sources=%d",
		traceID,
		evidence.Profile.Name,
		time.Since(evidenceStart).Milliseconds(),
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
	debugTrace["candidate_top_k"] = publicSpecialistTopK(evidence.Profile, runtimeSettings)
	debugTrace["max_evidence_chars"] = publicSpecialistMaxEvidenceChars(evidence.Profile, runtimeSettings)
	debugTrace["retrieved_candidates"] = publicRetrievedPageSummaries(evidence.Candidates, 12)
	debugTrace["evidence"] = evidence.EvidenceTrace
	debugTrace["sources"] = publicSourceSummaries(evidence.Sources)
	debugTrace["retrieval_cache"] = evidence.CacheTrace.summary()

	systemPrompt, err := s.loadPrompt(evidence.Profile.PromptFile)
	if err != nil {
		return nil, err
	}
	systemPrompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	userPrompt := s.publicSpecialistDecisionPrompt(req, receivedAt, routerOutput, evidence, runtimeSettings.Support)
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
	specialistLLMStart := time.Now()
	specialistModelID := runtimeSettings.PublicQuery.SpecialistModelID
	specialistThinking := runtimeSettings.PublicQuery.SpecialistEnableThinking
	llmText, trace, err := s.executeLLMTraceWithOptionsAndResponseFormat(ctx, execution, llmModelIDToken(specialistModelID), []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, "llm public specialist "+evidence.Profile.Name, hooks, specialistThinking, publicSpecialistResponseFormat())
	if err != nil {
		if publicAnswerRequestCanceled(ctx, err) {
			return nil, err
		}
		return nil, err
	}
	log.Printf(
		"public routed specialist llm trace=%s specialist=%s model_id=%s thinking=%s duration_ms=%d prompt_chars=%d",
		traceID,
		evidence.Profile.Name,
		publicConfiguredModelIDForLog(specialistModelID),
		publicThinkingForLog(specialistThinking),
		time.Since(specialistLLMStart).Milliseconds(),
		len([]rune(systemPrompt))+len([]rune(userPrompt)),
	)

	parseStart := publicTraceStepStart(ctx, "解析 Specialist JSON 输出", "public.specialist.parse", map[string]any{
		"raw_chars": len([]rune(llmText)),
	})
	parsed, err := s.parsePublicRoutedSpecialistOutput(llmText)
	if err != nil {
		publicTraceStepFinish(ctx, execution, "解析 Specialist JSON 输出", "public.specialist.parse", parseStart, nil, nil, err)
		return nil, err
	}
	parsed.Sources = filterPublicAnswerSources(parsed.Sources, evidence.Sources)
	modelParsedForLog := parsed
	if req.Simulation {
		debugTrace["model_json_raw"] = truncateForPrompt(llmText, 6000)
		debugTrace["model_json_parsed"] = parsed
	} else {
		debugTrace["model_json_parsed"] = publicSafeLLMOutputForLog(parsed)
	}
	publicTraceStepFinish(ctx, execution, "解析 Specialist JSON 输出", "public.specialist.parse", parseStart, nil, map[string]any{
		"answer_mode":         normalizedAnswerMode(parsed.AnswerMode),
		"confidence":          clampConfidence(parsed.Confidence),
		"evidence_confidence": clampConfidence(parsed.EvidenceConfidence),
		"review_required":     parsed.ReviewRequired,
		"source_count":        len(parsed.Sources),
	}, nil)

	answer := strings.TrimSpace(parsed.AnswerText)
	if answer == "" {
		return nil, errors.New("specialist returned empty answer")
	}
	parsed.AnswerText = answer
	retrievedPaths := publicSpecialistRetrievedPaths(evidence)
	reviewQueue := NewReviewQueueService(s.deps)
	reviewWillCreate := !req.Simulation && s.shouldCreatePublicReview(req, parsed, runtimeSettings.PublicQuery)
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
			BoundaryReason:      firstNonEmpty(parsed.ReviewReason, parsed.Notes, "低可信 routed public query 回答，等待人工审查。"),
			MatchedPages:        retrievedPaths,
			SessionID:           req.SessionID,
			QuestionMessageID:   req.QuestionMessageID,
			AnswerMessageID:     req.AnswerMessageID,
			QuestionCreatedAt:   firstNonEmpty(req.QuestionCreatedAt, receivedAt),
			AnswerCreatedAt:     time.Now().Format(time.RFC3339Nano),
			AnswerMode:          normalizedAnswerMode(parsed.AnswerMode),
			EvidenceConfidence:  clampConfidence(parsed.EvidenceConfidence),
			RetrievedPages:      retrievedPaths,
			ConversationExcerpt: publicConversationExcerpt(req),
		})
	}

	answeredAt := time.Now().Format(time.RFC3339Nano)
	execution.Status = ExecutionSuccess
	execution.EndedAt = time.Now()
	if stream != nil {
		stream.emitRemainingAnswer(answer)
	}
	resp := &PublicAnswerResponse{
		Answer:     answer,
		ReceivedAt: receivedAt,
		AnsweredAt: answeredAt,
		Details:    s.publicTraceDetails(req, parsed, trace, execution, evidence.Sources, retrievedPaths, debugTrace),
	}
	resp.Details["specialist"] = evidence.Profile.Name
	s.maybeWritePublicAnswerLog(traceID, req, resp, map[string]any{
		"decision":          "routed_specialist_answer",
		"specialist":        evidence.Profile.Name,
		"thinking":          trace.Reasoning,
		"model_json_raw":    llmText,
		"model_json_parsed": modelParsedForLog,
		"final_json":        parsed,
	})
	return resp, nil
}

func (s *PublicQueryService) publicSpecialistDecisionPrompt(req PublicAnswerRequest, receivedAt string, routerOutput *PublicRouterOutput, evidence publicSpecialistEvidenceResult, support RuntimeSupportSettings) string {
	candidateText := strings.TrimSpace(strings.Join(evidence.ContentBlocks, "\n\n"))
	if candidateText == "" {
		candidateText = "[]"
	}
	return strings.Join([]string{
		"current_time:",
		receivedAt,
		"",
		"current_public_time:",
		formatPublicBeijingTime(receivedAt),
		"",
		"user_message:",
		strings.TrimSpace(req.Question),
		"",
		"router_output:",
		formatPublicRouterOutputForSpecialist(routerOutput),
		"",
		"current_public_contacts:",
		s.supportContactPrompt(support),
		"",
		"hard_boundary:",
		formatPublicHardBoundary(),
		"",
		"candidate_page_paths:",
		formatSourceRefList(evidence.Sources),
		"",
		"candidate_pages:",
		candidateText,
	}, "\n")
}

func formatPublicRouterOutputForSpecialist(output *PublicRouterOutput) string {
	if output == nil {
		return "{}"
	}
	lines := []string{
		"specialist: " + output.Specialist,
		"intent: " + output.Intent,
		"rewritten_question: " + output.RewrittenQuestion,
		"history_summary: " + output.HistorySummary,
		"slots:",
		"  product: " + output.Slots.Product,
		"  products: " + strings.Join(output.Slots.Products, ", "),
		"  product_resolution:",
		"    primary: " + output.Slots.ProductResolution.Primary,
		"    all: " + strings.Join(output.Slots.ProductResolution.All, ", "),
		"    from_history: " + fmt.Sprintf("%t", output.Slots.ProductResolution.FromHistory),
		"    confidence: " + fmt.Sprintf("%.2f", output.Slots.ProductResolution.Confidence),
		"    ambiguous: " + fmt.Sprintf("%t", output.Slots.ProductResolution.Ambiguous),
		"    reason: " + output.Slots.ProductResolution.Reason,
		"  static_type: " + output.Slots.StaticType,
		"  ip_type: " + output.Slots.IPType,
		"  bandwidth: " + output.Slots.Bandwidth,
		"  quantity: " + output.Slots.Quantity,
		"  scenario: " + output.Slots.Scenario,
		"  platform: " + output.Slots.Platform,
		"  device: " + output.Slots.Device,
		"  error_code: " + output.Slots.ErrorCode,
		"missing_info: " + strings.Join(output.MissingInfo, ", "),
		"risk_flags: " + strings.Join(output.RiskFlags, ", "),
		"answer_policy: " + output.AnswerPolicy,
	}
	return strings.Join(lines, "\n")
}

func (s *PublicQueryService) parsePublicRoutedSpecialistOutput(llmText string) (publicAnswerLLMOutput, error) {
	var parsed publicAnswerLLMOutput
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		return publicAnswerLLMOutput{}, fmt.Errorf("decode routed specialist output: %w", err)
	}
	return normalizePublicAnswerOutput(parsed), nil
}

func publicSpecialistRetrievedPaths(evidence publicSpecialistEvidenceResult) []string {
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

func publicConfiguredModelIDForLog(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "active"
	}
	return id
}

func publicThinkingForLog(value *bool) string {
	if value == nil {
		return "default"
	}
	if *value {
		return "true"
	}
	return "false"
}
