package service

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/wikiadapter"
)

type PublicAnswerRequest struct {
	Question          string         `json:"question"`
	Stream            bool           `json:"stream,omitempty"`
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

type PublicAnswerResponse struct {
	Answer     string            `json:"answer"`
	ReceivedAt string            `json:"received_at,omitempty"`
	AnsweredAt string            `json:"answered_at,omitempty"`
	UserIntent *PublicUserIntent `json:"user_intent"`
	Details    map[string]any    `json:"details,omitempty"`
}

type PublicUserIntent struct {
	Type      string           `json:"type"`
	PriceInfo *PublicPriceInfo `json:"price_info,omitempty"`
}

type PublicPriceInfo struct {
	ExpectedPrice            string `json:"expected_price"`
	ProductType              string `json:"product_type"`
	ProductBandwidth         int    `json:"product_bandwidth"`
	IntendedPurchaseQuantity int    `json:"intended_purchase_quantity"`
	BoxUsageTime             int    `json:"box_usage_time"`
	BoxUsageQuantityMin      int    `json:"box_usage_quantity_min"`
	BoxUsageQuantityMax      int    `json:"box_usage_quantity_max"`
}

type PublicQueryService struct {
	baseService
}

const publicHistoryLimit = 8

type publicAnswerLLMOutput struct {
	AnswerMode          string               `json:"answer_mode"`
	AnswerType          string               `json:"answer_type"`
	AnswerMarkdown      string               `json:"answer_markdown"`
	CanAnswer           *bool                `json:"can_answer"`
	ReviewQuestion      string               `json:"review_question"`
	Confidence          float64              `json:"confidence"`
	EvidenceConfidence  float64              `json:"evidence_confidence"`
	ReviewRequired      bool                 `json:"review_required"`
	ReviewReason        string               `json:"review_reason"`
	BoundaryReason      string               `json:"boundary_reason"`
	SuggestedTargetPath string               `json:"suggested_target_path"`
	Sources             []publicAnswerSource `json:"sources"`
	UserIntent          *PublicUserIntent    `json:"user_intent"`
	Notes               string               `json:"notes"`
}

type publicAnswerSource struct {
	Path       string `json:"path"`
	Confidence string `json:"confidence"`
}

func NewPublicQueryService(deps Deps) *PublicQueryService {
	return &PublicQueryService{baseService: newBaseService(deps)}
}

func (s *PublicQueryService) Answer(ctx context.Context, traceID string, req PublicAnswerRequest) (*PublicAnswerResponse, error) {
	return s.answer(ctx, traceID, req, nil)
}

func (s *PublicQueryService) AnswerStream(ctx context.Context, traceID string, req PublicAnswerRequest, emitter StreamEmitter) (*PublicAnswerResponse, error) {
	req.Stream = true
	return s.answer(ctx, traceID, req, newPublicAnswerStream(emitter))
}

func (s *PublicQueryService) answer(ctx context.Context, traceID string, req PublicAnswerRequest, stream *publicAnswerStream) (*PublicAnswerResponse, error) {
	receivedAt := firstNonEmpty(strings.TrimSpace(req.ReceivedAt), time.Now().Format(time.RFC3339Nano))
	if stream != nil {
		stream.emitReasoning("先做安全边界检查，确认问题能否直接面向客户回答。")
	}
	if reply, ok := hardPublicSafetyReply(req.Question); ok {
		resp := publicAnswerResponse(reply, receivedAt)
		resp.Details = publicStaticTraceDetails("refusal", "问题命中硬安全边界，直接返回可对外展示的拒答内容。")
		if stream != nil {
			stream.emitAnswerDelta(resp.Answer)
		}
		return resp, nil
	}
	if intent, ok := s.matchPublicIntent(req.Question); ok && shouldUsePublicIntentBypass(req.Question, intent) && strings.TrimSpace(intent.Response) != "" {
		resp := publicAnswerResponse(intent.Response, receivedAt)
		resp.Details = publicStaticTraceDetails("intent", "问题命中公开意图规则，直接使用已配置的客户可见话术。")
		if stream != nil {
			stream.emitStep("命中公开意图规则", map[string]any{"answer_mode": "intent"})
			stream.emitAnswerDelta(resp.Answer)
		}
		return resp, nil
	}
	reviewQueue := NewReviewQueueService(s.deps)
	if _, forbidden, err := reviewQueue.MatchForbidden(ctx, req.Question); err != nil {
		return nil, err
	} else if forbidden {
		resp := publicAnswerResponse(forbiddenPublicReply(), receivedAt)
		resp.Details = publicStaticTraceDetails("refusal", "问题命中禁答知识，直接返回可对外展示的拒答内容。")
		if stream != nil {
			stream.emitStep("命中禁答知识", map[string]any{"answer_mode": "refusal"})
			stream.emitAnswerDelta(resp.Answer)
		}
		return resp, nil
	}

	env := s.env("public", traceID, "", "")
	candidateTopK := s.deps.Config.PublicQuery.CandidateTopK
	if candidateTopK <= 0 {
		candidateTopK = 6
	}
	retrievalQuestion := buildPublicRetrievalQuestion(req.Question, req.History)
	pages, err := s.deps.Retriever.Retrieve(ctx, env, retrievalQuestion, candidateTopK)
	if err != nil {
		return nil, err
	}
	contentBlocks := make([]string, 0, len(pages))
	sources := make([]SourceRef, 0, len(pages))
	seenPaths := map[string]bool{}
	relatedEvidencePaths := make([]string, 0, len(pages))
	processPages := func(candidates []retrieval.RetrievedPage) {
		for _, page := range prioritizePublicRetrievedPages(candidates) {
			if !isPublicReadableEvidence(page.Path) {
				continue
			}
			content, ok := s.readPublicEvidencePage(ctx, env, page.Path, retrievalQuestion, seenPaths, &contentBlocks, &sources)
			if !ok {
				continue
			}
			relatedEvidencePaths = append(relatedEvidencePaths, linkedPublicEvidencePathsFromContent(content)...)
		}
	}
	processPages(pages)
	fallbackPages := s.searchPublicEvidencePages(ctx, env, retrievalQuestion, candidateTopK)
	if len(fallbackPages) > 0 {
		pages = append(pages, fallbackPages...)
		processPages(fallbackPages)
	}
	for _, evidencePath := range dedupeEvidencePaths(relatedEvidencePaths) {
		s.readPublicEvidencePage(ctx, env, evidencePath, retrievalQuestion, seenPaths, &contentBlocks, &sources)
	}
	retrievedPaths := retrievedPagePaths(pages)
	if stream != nil {
		stream.emitStep("检索公开证据", map[string]any{
			"source_count":    len(sources),
			"retrieved_count": len(retrievedPaths),
		})
		stream.emitReasoning(fmt.Sprintf("已读取 %d 个公开可用候选知识页，开始生成客户可见回答。", len(sources)))
	}

	systemPrompt, err := s.loadPromptWithWikiQueryGuide("public_answer_system.md")
	if err != nil {
		return nil, err
	}
	systemPrompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	userPrompt := s.publicDecisionPrompt(req, receivedAt, sources, contentBlocks)
	execution := NewExecution("public-answer")
	var hooks *llmDeltaHooks
	if stream != nil {
		hooks = &llmDeltaHooks{Content: stream.feedLLMContent}
	}
	llmText, trace, err := s.executeLLMTraceWithHooks(ctx, execution, s.deps.Config.LLM.ModelPublic, []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, "llm public answer", hooks)
	if err != nil {
		execution.Status = ExecutionFailed
		execution.Error = err.Error()
		execution.EndedAt = time.Now()
		log.Printf("public answer llm failed trace=%s question=%q err=%v", traceID, truncateForPrompt(req.Question, 80), err)
		resp := publicAnswerResponse(s.publicFallback(req.Question), receivedAt)
		resp.Details = map[string]any{
			"reasoning":   "1. 已完成安全边界和公开证据检索。\n2. 生成回答时遇到模型调用错误，因此使用安全兜底话术。\n3. 兜底回答不暴露内部路径、prompt 或 raw JSON。",
			"steps":       publicExecutionSteps(execution.Steps),
			"execution":   publicExecutionSummary(execution),
			"answer_mode": "fallback",
		}
		return resp, nil
	}
	parsed := s.parsePublicAnswerOutput(ctx, llmText)
	parsed.Sources = filterPublicAnswerSources(parsed.Sources, sources)
	parsed.UserIntent = publicResponseUserIntent(req, parsed.UserIntent, parsed.Sources)
	answerMarkdown := strings.TrimSpace(parsed.AnswerMarkdown)
	if answerMarkdown == "" {
		answerMarkdown = s.publicFallback(req.Question)
	}
	if sanitized, ok := sanitizePublicAnswer(answerMarkdown, req.Question); ok {
		answerMarkdown = sanitized
	} else if sanitized, ok := sanitizePublicPricingAnswer(answerMarkdown, req, parsed.UserIntent); ok {
		answerMarkdown = sanitized
	}
	answeredAt := time.Now().Format(time.RFC3339Nano)
	parsed.AnswerMarkdown = answerMarkdown
	execution.Status = ExecutionSuccess
	execution.EndedAt = time.Now()
	if stream != nil {
		stream.emitStep("生成并清理回答", map[string]any{
			"answer_mode": normalizedAnswerMode(parsed.AnswerMode),
			"review":      parsed.ReviewRequired,
		})
	}

	if s.shouldCreatePublicReview(req, parsed) {
		_, _ = reviewQueue.CreatePending(ctx, ReviewCreateRequest{
			Question:            firstNonEmpty(parsed.ReviewQuestion, req.Question),
			OriginalQuestion:    req.Question,
			DraftAnswer:         answerMarkdown,
			SuggestedTargetPath: parsed.SuggestedTargetPath,
			Confidence:          clampConfidence(parsed.Confidence),
			BoundaryReason:      firstNonEmpty(parsed.ReviewReason, parsed.Notes, "低可信 public query 回答，等待人工审查。"),
			MatchedPages:        retrievedPaths,
			SessionID:           req.SessionID,
			QuestionMessageID:   req.QuestionMessageID,
			AnswerMessageID:     req.AnswerMessageID,
			QuestionCreatedAt:   firstNonEmpty(req.QuestionCreatedAt, receivedAt),
			AnswerCreatedAt:     answeredAt,
			AnswerMode:          normalizedAnswerMode(parsed.AnswerMode),
			EvidenceConfidence:  clampConfidence(parsed.EvidenceConfidence),
			RetrievedPages:      retrievedPaths,
			ConversationExcerpt: publicConversationExcerpt(req),
		})
	}
	return &PublicAnswerResponse{
		Answer:     answerMarkdown,
		ReceivedAt: receivedAt,
		AnsweredAt: answeredAt,
		UserIntent: parsed.UserIntent,
		Details:    s.publicTraceDetails(req, parsed, trace, execution, sources, retrievedPaths),
	}, nil
}

func publicAnswerResponse(answer string, receivedAt string) *PublicAnswerResponse {
	return &PublicAnswerResponse{
		Answer:     answer,
		ReceivedAt: receivedAt,
		AnsweredAt: time.Now().Format(time.RFC3339Nano),
	}
}

func publicStaticTraceDetails(answerMode string, reasoning string) map[string]any {
	now := time.Now()
	return map[string]any{
		"reasoning":   reasoning,
		"answer_mode": answerMode,
		"steps": []map[string]any{
			{
				"name":       "公开问答边界检查",
				"tool":       "public.answer",
				"status":     "SUCCESS",
				"started_at": now,
				"ended_at":   now,
			},
		},
	}
}

func (s *PublicQueryService) publicTraceDetails(req PublicAnswerRequest, parsed publicAnswerLLMOutput, trace LLMTrace, execution *Execution, sources []SourceRef, retrievedPaths []string) map[string]any {
	reasoning := publicReasoningSummary(req, parsed, sources, retrievedPaths)
	if reasoning == "" {
		reasoning = summarizeContent(trace.Reasoning)
	}
	return map[string]any{
		"reasoning":       reasoning,
		"steps":           publicExecutionSteps(execution.Steps),
		"execution":       publicExecutionSummary(execution),
		"answer_mode":     normalizedAnswerMode(parsed.AnswerMode),
		"source_count":    len(sources),
		"retrieved_count": len(retrievedPaths),
	}
}

func publicReasoningSummary(req PublicAnswerRequest, parsed publicAnswerLLMOutput, sources []SourceRef, retrievedPaths []string) string {
	lines := []string{
		"1. 先做安全边界和禁答检查，确认这个问题能否用正式知识库回答。",
	}
	if len(sources) > 0 {
		lines = append(lines, fmt.Sprintf("2. 检索并读取 %d 个 public-safe 候选知识页，优先使用正式知识、政策、流程、对比和综合页面。", len(sources)))
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
		lines = append(lines, "5. 输出前再次做品牌表达和安全措辞清理。")
	}
	return strings.Join(lines, "\n")
}

func publicExecutionSteps(steps []Step) []map[string]any {
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
		if step.Status == "FAILED" {
			if errText := resultStringValue(step.Output, "error"); errText != "" {
				item["output"] = map[string]any{"error": errText}
			}
		}
		out = append(out, item)
	}
	return out
}

func publicExecutionSummary(execution *Execution) map[string]any {
	if execution == nil {
		return nil
	}
	return map[string]any{
		"id":         execution.ID,
		"kind":       execution.Kind,
		"status":     execution.Status,
		"started_at": execution.StartedAt,
		"ended_at":   execution.EndedAt,
	}
}

func (s *PublicQueryService) parsePublicAnswerOutput(ctx context.Context, llmText string) publicAnswerLLMOutput {
	parsed := publicAnswerLLMOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err == nil {
		return normalizePublicAnswerOutput(parsed)
	}
	systemPrompt := "你只负责把输入改写成一个合法 JSON 对象，不改变语义，不补充事实。必须输出字段 answer_mode、answer_markdown、review_question、confidence、evidence_confidence、review_required、review_reason、suggested_target_path、sources、user_intent、notes；缺失字段用空字符串、false、0、null 或空数组补齐。"
	userPrompt := "原始输出：\n" + truncateForPrompt(llmText, 4000)
	repaired, err := s.executeLLM(ctx, nil, s.deps.Config.LLM.ModelPublic, []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, "llm public answer json repair")
	if err == nil {
		parsed = publicAnswerLLMOutput{}
		if decodeErr := llm.DecodeJSONObject(repaired, &parsed); decodeErr == nil {
			return normalizePublicAnswerOutput(parsed)
		}
	}
	return normalizePublicAnswerOutput(publicAnswerLLMOutput{
		AnswerMode:     "self_answer",
		AnswerMarkdown: strings.TrimSpace(llmText),
		Confidence:     s.deps.Config.PublicQuery.Confidence.ReviewMin,
		ReviewRequired: true,
		ReviewReason:   "LLM 未输出标准 JSON，按低可信回答进入审查。",
	})
}

func normalizePublicAnswerOutput(parsed publicAnswerLLMOutput) publicAnswerLLMOutput {
	if parsed.CanAnswer != nil && !*parsed.CanAnswer && strings.TrimSpace(parsed.AnswerMode) == "" {
		parsed.AnswerMode = "refusal"
	}
	parsed.AnswerMode = normalizedAnswerMode(parsed.AnswerMode)
	parsed.AnswerMarkdown = strings.TrimSpace(parsed.AnswerMarkdown)
	parsed.ReviewQuestion = strings.TrimSpace(parsed.ReviewQuestion)
	parsed.Confidence = clampConfidence(parsed.Confidence)
	parsed.EvidenceConfidence = clampConfidence(parsed.EvidenceConfidence)
	parsed.ReviewReason = strings.TrimSpace(parsed.ReviewReason)
	if parsed.ReviewReason == "" {
		parsed.ReviewReason = strings.TrimSpace(parsed.BoundaryReason)
	}
	parsed.SuggestedTargetPath = strings.TrimSpace(parsed.SuggestedTargetPath)
	parsed.UserIntent = normalizePublicUserIntent(parsed.UserIntent)
	parsed.Notes = strings.TrimSpace(parsed.Notes)
	return parsed
}

func filterPublicAnswerSources(items []publicAnswerSource, candidates []SourceRef) []publicAnswerSource {
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
	out := make([]publicAnswerSource, 0, len(items))
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
			confidence = publicSourceConfidence(path)
		}
		out = append(out, publicAnswerSource{Path: path, Confidence: confidence})
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

func publicResponseUserIntent(req PublicAnswerRequest, intent *PublicUserIntent, sources []publicAnswerSource) *PublicUserIntent {
	intent = normalizePublicUserIntent(intent)
	if intent == nil {
		return nil
	}
	switch intent.Type {
	case "price_adjustment":
		if intent.PriceInfo == nil || !hasPublicPriceIntentEvidence(sources) || !isStrongPublicPriceAdjustmentRequest(req) {
			return nil
		}
		return intent
	case "switch_ip":
		if !isPublicSwitchIPRequest(req) {
			return nil
		}
		return intent
	default:
		return nil
	}
}

func hasPublicPriceIntentEvidence(sources []publicAnswerSource) bool {
	for _, source := range sources {
		if strings.EqualFold(strings.TrimSpace(source.Confidence), "high") {
			return true
		}
	}
	return false
}

func normalizePublicUserIntent(intent *PublicUserIntent) *PublicUserIntent {
	if intent == nil {
		return nil
	}
	intentType := normalizedPublicUserIntentType(intent.Type)
	switch intentType {
	case "price_adjustment":
		priceInfo := normalizePublicPriceInfo(intent.PriceInfo)
		if priceInfo == nil {
			return nil
		}
		return &PublicUserIntent{Type: intentType, PriceInfo: priceInfo}
	case "switch_ip":
		return &PublicUserIntent{Type: intentType}
	default:
		return nil
	}
}

func normalizedPublicUserIntentType(value string) string {
	normalized := normalizePublicIntentText(value)
	switch normalized {
	case "price_adjustment", "price adjustment", "申请修改价格", "申请改价", "申请优惠", "修改价格", "改价", "优惠申请":
		return "price_adjustment"
	case "switch_ip", "switch ip", "切换ip", "切换 ip", "换ip", "换 ip", "更换ip", "更换 ip":
		return "switch_ip"
	default:
		return ""
	}
}

func normalizePublicPriceInfo(info *PublicPriceInfo) *PublicPriceInfo {
	if info == nil {
		return nil
	}
	out := *info
	out.ExpectedPrice = strings.TrimSpace(out.ExpectedPrice)
	out.ProductType = normalizedPublicProductType(out.ProductType)
	out.ProductBandwidth = nonNegativeInt(out.ProductBandwidth)
	out.IntendedPurchaseQuantity = nonNegativeInt(out.IntendedPurchaseQuantity)
	out.BoxUsageTime = nonNegativeInt(out.BoxUsageTime)
	out.BoxUsageQuantityMin = nonNegativeInt(out.BoxUsageQuantityMin)
	out.BoxUsageQuantityMax = nonNegativeInt(out.BoxUsageQuantityMax)
	if out.ExpectedPrice == "" || out.ProductType == "" {
		return nil
	}
	switch out.ProductType {
	case "static", "box":
		if !isAllowedPublicBandwidth(out.ProductBandwidth) || out.IntendedPurchaseQuantity <= 0 {
			return nil
		}
		out.BoxUsageTime = 0
		out.BoxUsageQuantityMin = 0
		out.BoxUsageQuantityMax = 0
		return &out
	case "dynamic":
		out.ProductBandwidth = 0
		out.IntendedPurchaseQuantity = 0
		hasUsageTime := isAllowedPublicDynamicUsageTime(out.BoxUsageTime)
		hasUsageQuantity := out.BoxUsageQuantityMin > 0 && out.BoxUsageQuantityMax >= out.BoxUsageQuantityMin
		if !hasUsageTime {
			out.BoxUsageTime = 0
		}
		if !hasUsageQuantity {
			out.BoxUsageQuantityMin = 0
			out.BoxUsageQuantityMax = 0
		}
		if !hasUsageTime && !hasUsageQuantity {
			return nil
		}
		return &out
	default:
		return nil
	}
}

func normalizedPublicProductType(value string) string {
	normalized := normalizePublicIntentText(value)
	switch normalized {
	case "static", "static ip", "静态", "静态ip", "静态 ip":
		return "static"
	case "dynamic", "dynamic ip", "动态", "动态ip", "动态 ip":
		return "dynamic"
	case "box", "住宅", "住宅ip", "住宅 ip", "residential", "residential ip":
		return "box"
	default:
		return ""
	}
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func isAllowedPublicBandwidth(value int) bool {
	return value == 5 || value == 10 || value == 20
}

func isAllowedPublicDynamicUsageTime(value int) bool {
	switch value {
	case 7, 30, 90, 180, 360:
		return true
	default:
		return false
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

func (s *PublicQueryService) shouldCreatePublicReview(req PublicAnswerRequest, parsed publicAnswerLLMOutput) bool {
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if mode == "refusal" || strings.TrimSpace(parsed.AnswerMarkdown) == "" {
		return false
	}
	if !parsed.ReviewRequired {
		return false
	}
	if isObviouslyNonReviewablePublicQuestion(req.Question) {
		return false
	}
	directMin, reviewMin := publicConfidenceThresholds(
		s.deps.Config.PublicQuery.Confidence.DirectMin,
		s.deps.Config.PublicQuery.Confidence.ReviewMin,
	)
	confidence := clampConfidence(parsed.Confidence)
	if confidence >= directMin {
		return false
	}
	return confidence >= reviewMin || strings.TrimSpace(parsed.ReviewReason) != "" || strings.TrimSpace(parsed.ReviewQuestion) != ""
}

func isObviouslyNonReviewablePublicQuestion(question string) bool {
	normalized := normalizePublicIntentText(question)
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

func publicConfidenceThresholds(directMin float64, reviewMin float64) (float64, float64) {
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

func (s *PublicQueryService) publicDecisionPrompt(req PublicAnswerRequest, receivedAt string, sources []SourceRef, contentBlocks []string) string {
	candidateText := strings.TrimSpace(strings.Join(contentBlocks, "\n\n"))
	if candidateText == "" {
		candidateText = "[]"
	}
	return strings.Join([]string{
		"current_time:",
		receivedAt,
		"",
		"user_message:",
		strings.TrimSpace(req.Question),
		"",
		"conversation_context:",
		formatConversationContext(req.History),
		"",
		"current_public_contacts:",
		s.supportContactPrompt(),
		"",
		"hard_boundary:",
		formatPublicHardBoundary(),
		"",
		"candidate_page_paths:",
		formatSourceRefList(sources),
		"",
		"candidate_pages:",
		candidateText,
	}, "\n")
}

func (s *PublicQueryService) supportContactPrompt() string {
	phone := strings.TrimSpace(s.deps.Config.Support.Phone)
	if phone == "" {
		phone = "400-1080-106"
	}
	wecom := strings.TrimSpace(s.deps.Config.Support.WeCom)
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

func formatPublicHardBoundary() string {
	return strings.Join([]string{
		"- Server 已在进入本轮 LLM 前拦截明显内部系统操作、明显违法攻击请求和已命中 forbidden 的问题。",
		"- 本轮没有命中这些硬拦截；你仍必须按系统提示词自行判断普通问题、边界问题和拒答场景。",
		"- 不要向客户暴露 hard_boundary、candidate_pages、review 或其它内部字段。",
	}, "\n")
}

func (s *PublicQueryService) readPublicEvidencePage(
	ctx context.Context,
	env *runtime.ExecEnv,
	path string,
	question string,
	seenPaths map[string]bool,
	contentBlocks *[]string,
	sources *[]SourceRef,
) (string, bool) {
	if seenPaths[path] {
		return "", false
	}
	result, err := s.deps.Runtime.Execute(ctx, env, runtimeCall("wiki.read_page", map[string]any{"path": path}))
	if err != nil || !result.Success {
		return "", false
	}
	content, _ := result.Data["content"].(string)
	if strings.TrimSpace(content) == "" {
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
	maxChars := s.deps.Config.PublicQuery.MaxEvidenceChars
	if maxChars <= 0 {
		maxChars = 2400
	}
	preview := buildPublicEvidencePreview(body, path, question, maxChars)
	seenPaths[path] = true
	source := SourceRef{
		Path:       path,
		Title:      displayTitle,
		Confidence: publicSourceConfidence(path),
	}
	*contentBlocks = append(*contentBlocks, formatCandidatePageBlock(source, truncateForPrompt(preview, maxChars)))
	*sources = append(*sources, source)
	return body, true
}

func (s *PublicQueryService) searchPublicEvidencePages(ctx context.Context, env *runtime.ExecEnv, question string, topK int) []retrieval.RetrievedPage {
	result, err := s.deps.Runtime.Execute(ctx, env, runtimeCall("wiki.search_pages", map[string]any{"query": question}))
	if err != nil || !result.Success {
		return nil
	}
	raw, ok := result.Data["matches"].([]map[string]any)
	if !ok {
		return nil
	}
	out := make([]retrieval.RetrievedPage, 0, len(raw))
	for _, item := range raw {
		path, _ := item["path"].(string)
		if !isPublicReadableEvidence(path) {
			continue
		}
		score := 0
		if rawScore, ok := item["score"].(int); ok {
			score = rawScore
		}
		out = append(out, retrieval.RetrievedPage{Path: path, Score: float64(score)})
		if topK > 0 && len(out) >= topK {
			break
		}
	}
	return out
}

func prioritizePublicRetrievedPages(pages []retrieval.RetrievedPage) []retrieval.RetrievedPage {
	out := append([]retrieval.RetrievedPage(nil), pages...)
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			leftRank := publicEvidenceDirectoryRank(out[i].Path)
			rightRank := publicEvidenceDirectoryRank(out[j].Path)
			if rightRank < leftRank || (rightRank == leftRank && out[j].Score > out[i].Score) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func publicEvidenceDirectoryRank(path string) int {
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
	case strings.HasPrefix(path, "wiki/sources/"):
		return 8
	default:
		return 99
	}
}

func buildPublicEvidencePreview(body string, path string, question string, maxChars int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if maxChars <= 0 {
		maxChars = 2400
	}
	terms := publicEvidenceTerms(question)
	if len(terms) == 0 {
		return truncateForPrompt(body, maxChars)
	}
	if preview := relevantTextWindows(body, terms, 2); strings.TrimSpace(preview) != "" {
		return preview
	}
	return truncateForPrompt(body, maxChars)
}

func splitMarkdownSections(body string, headingPrefix string) []string {
	lines := strings.Split(body, "\n")
	sections := make([]string, 0)
	current := make([]string, 0)
	for _, line := range lines {
		if strings.HasPrefix(line, headingPrefix) {
			if len(current) > 0 {
				sections = append(sections, strings.Join(current, "\n"))
			}
			current = []string{line}
			continue
		}
		if len(current) > 0 {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		sections = append(sections, strings.Join(current, "\n"))
	}
	return sections
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

type scoredText struct {
	text  string
	score int
}

func sortScoredText(items []scoredText) {
	for i := 0; i < len(items)-1; i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].score > items[i].score {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func publicEvidenceScore(text string, terms []string) int {
	haystack := strings.ToLower(text)
	score := 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		count := strings.Count(haystack, term)
		if count == 0 {
			continue
		}
		score += count * len([]rune(term))
	}
	return score
}

func publicEvidenceTerms(question string) []string {
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
		kind := publicSearchRuneKind(r)
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

func publicSearchRuneKind(r rune) int {
	switch {
	case r >= '\u4e00' && r <= '\u9fff':
		return 1
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return 2
	default:
		return 0
	}
}

func formatConversationHistory(history []ChatMessage) string {
	if len(history) == 0 {
		return ""
	}
	lines := make([]string, 0, len(history))
	start := 0
	if len(history) > publicHistoryLimit {
		start = len(history) - publicHistoryLimit
	}
	for _, item := range history[start:] {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if role == "" || content == "" {
			continue
		}
		timeText := strings.TrimSpace(item.CreatedAt)
		if timeText != "" {
			lines = append(lines, fmt.Sprintf("- [%s] %s: %s", timeText, role, content))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s", role, content))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "最近对话上下文（按时间顺序）：\n" + strings.Join(lines, "\n") + "\n\n"
}

func formatConversationContext(history []ChatMessage) string {
	if len(history) == 0 {
		return "[]"
	}
	lines := make([]string, 0, len(history))
	start := 0
	if len(history) > publicHistoryLimit {
		start = len(history) - publicHistoryLimit
	}
	for _, item := range history[start:] {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if role == "" || content == "" {
			continue
		}
		timeText := strings.TrimSpace(item.CreatedAt)
		block := []string{}
		if timeText != "" {
			block = append(block, "- created_at: "+timeText)
		} else {
			block = append(block, "-")
		}
		block = append(block, "  role: "+role, "  content: |")
		for _, line := range strings.Split(truncateForPrompt(content, 600), "\n") {
			block = append(block, "    "+line)
		}
		lines = append(lines, strings.Join(block, "\n"))
	}
	if len(lines) == 0 {
		return "[]"
	}
	return strings.Join(lines, "\n")
}

func buildPublicRetrievalQuestion(question string, history []ChatMessage) string {
	question = strings.TrimSpace(question)
	if len(history) == 0 {
		return question
	}
	lines := make([]string, 0, len(history)+1)
	start := 0
	if len(history) > publicHistoryLimit {
		start = len(history) - publicHistoryLimit
	}
	for _, item := range history[start:] {
		role := publicRetrievalRoleLabel(item.Role)
		content := strings.TrimSpace(item.Content)
		if role == "" || content == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s：%s", role, truncateForPrompt(content, 180)))
	}
	if question != "" {
		lines = append(lines, "当前问题："+question)
	}
	return strings.Join(lines, "\n")
}

func publicRetrievalRoleLabel(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return "用户"
	case "assistant":
		return "客服"
	default:
		return ""
	}
}

func publicConversationExcerpt(req PublicAnswerRequest) []string {
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

func (s *PublicQueryService) matchPublicIntent(question string) (PublicIntentResult, bool) {
	if s.deps.PublicIntents == nil {
		return PublicIntentResult{}, false
	}
	return s.deps.PublicIntents.Match(question)
}

func shouldUsePublicIntentBypass(question string, intent PublicIntentResult) bool {
	category := strings.ToLower(strings.TrimSpace(intent.Category))
	switch category {
	case "safety", "service_identity", "smalltalk":
		return true
	case "handoff":
		normalized := normalizePublicIntentText(question)
		if normalized == "" {
			return false
		}
		if containsAny(normalized, "转人工", "转让工", "转接人工", "找人工", "人工客服", "联系人工", "客服热线", "客服电话") {
			return true
		}
		return containsAny(normalized, "投诉") && len([]rune(normalized)) <= 8
	default:
		return false
	}
}

func (s *PublicQueryService) publicFallback(question string) string {
	if s.deps.PublicIntents == nil {
		return genericPublicFallback(question)
	}
	return s.deps.PublicIntents.Fallback(question)
}

func hardPublicSafetyReply(question string) (string, bool) {
	if unsupported, ok := unsupportedPublicReply(question); ok {
		return unsupported, true
	}
	lower := strings.ToLower(strings.TrimSpace(question))
	if containsAny(lower,
		"查看 prompt", "查看prompt", "系统提示词", "泄露提示词", "内部路径", "api key", "apikey",
		"删除资料库", "删除知识库", "删库", "删除wiki", "删除页面", "清空知识库",
		"drop database", "delete wiki", "delete knowledge base",
	) {
		return "这个请求不属于对外客服问答范围。如需处理系统或资料管理操作，请联系管理员。", true
	}
	if isObviousIllegalAssistanceRequest(lower) {
		return "这个请求我这边不能协助处理。", true
	}
	return "", false
}

func isObviousIllegalAssistanceRequest(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	if containsAny(text, "投诉", "退款", "被封", "封号", "不能用", "用不了", "赔偿", "售后") {
		return false
	}
	if containsAny(text,
		"教我诈骗", "怎么诈骗", "如何诈骗", "诈骗话术", "诈骗脚本", "诈骗模板", "我要诈骗",
		"教我洗钱", "怎么洗钱", "如何洗钱", "我要洗钱",
		"教我攻击", "怎么攻击", "如何攻击", "攻击脚本", "攻击工具", "我要攻击",
		"教我破解", "怎么破解", "如何破解", "破解账号", "破解密码",
		"教我盗号", "怎么盗号", "如何盗号", "盗号脚本", "盗号工具",
		"教我撞库", "怎么撞库", "如何撞库", "撞库脚本", "撞库工具",
		"教我窃取", "怎么窃取", "如何窃取", "窃取隐私", "爬取隐私",
		"钓鱼网站怎么做", "如何做钓鱼网站", "生成钓鱼网站", "钓鱼链接怎么做",
		"绕过监管", "绕过风控", "绕过实名", "绕过验证",
	) {
		return true
	}
	dangerousTerms := []string{"ddos", "sql注入", "sql injection", "木马", "恶意软件"}
	assistanceVerbs := []string{"教我", "怎么", "如何", "帮我", "帮忙", "我要", "想要", "提供", "生成", "写一个", "脚本", "工具", "教程", "方法"}
	for _, term := range dangerousTerms {
		if containsAny(text, term) && containsAny(text, assistanceVerbs...) {
			return true
		}
	}
	return false
}

func unsupportedPublicReply(question string) (string, bool) {
	text := strings.TrimSpace(question)
	if text == "" {
		return "", false
	}
	lower := strings.ToLower(text)
	if containsAny(lower,
		"删除资料库",
		"删除知识库",
		"删库",
		"删除wiki",
		"删除页面",
		"清空知识库",
		"drop database",
		"delete wiki",
		"delete knowledge base",
	) {
		return "这个请求不属于对外客服问答范围。如需处理系统或资料管理操作，请联系管理员。", true
	}
	return "", false
}

func forbiddenPublicReply() string {
	return "这个问题我这边不能继续回复，建议您联系人工客服进一步确认。"
}

func sanitizePublicPricingAnswer(answer string, req PublicAnswerRequest, intent *PublicUserIntent) (string, bool) {
	if intent != nil && intent.Type == "price_adjustment" {
		return "", false
	}
	if !isPlainPublicPriceQuestion(req) || !containsPublicDiscountDisclosure(answer) {
		return "", false
	}
	return "具体套餐价格以当前页面展示为准。您可以告诉我想购买的产品类型、带宽和数量，我再帮您确认适合的套餐。", true
}

func isPlainPublicPriceQuestion(req PublicAnswerRequest) bool {
	question := normalizePublicIntentText(req.Question)
	if question == "" {
		return false
	}
	return hasPublicPriceQuestionTerm(question) && !isStrongPublicPriceAdjustmentRequest(req)
}

func isStrongPublicPriceAdjustmentRequest(req PublicAnswerRequest) bool {
	text := publicUserIntentText(req)
	if text == "" {
		return false
	}
	return hasPublicDiscountRequestTerm(text) && hasPublicPurchaseIntentTerm(text)
}

func isPublicSwitchIPRequest(req PublicAnswerRequest) bool {
	text := publicUserIntentText(req)
	return text != "" && containsAny(text,
		"切换ip", "切换 ip", "换ip", "换 ip", "更换ip", "更换 ip",
		"换一个ip", "换一个 ip", "换个ip", "换个 ip", "换一下ip", "换一下 ip",
		"更换一下ip", "更换一下 ip",
	)
}

func publicUserIntentText(req PublicAnswerRequest) string {
	parts := make([]string, 0, len(req.History)+1)
	for _, item := range req.History {
		if strings.ToLower(strings.TrimSpace(item.Role)) != "user" {
			continue
		}
		if content := strings.TrimSpace(item.Content); content != "" {
			parts = append(parts, content)
		}
	}
	if question := strings.TrimSpace(req.Question); question != "" {
		parts = append(parts, question)
	}
	return normalizePublicIntentText(strings.Join(parts, " "))
}

func hasPublicPriceQuestionTerm(text string) bool {
	return containsAny(text, "多少钱", "价格", "价钱", "费用", "收费", "报价", "怎么卖", "套餐价格", "价格表")
}

func hasPublicDiscountRequestTerm(text string) bool {
	return containsAny(text,
		"优惠", "优惠价", "申请优惠", "申请价格", "申请改价", "改价", "折扣", "打折", "便宜点", "便宜些",
		"能不能便宜", "可以便宜", "能少", "少一点", "专属价", "批量价",
	)
}

func hasPublicPurchaseIntentTerm(text string) bool {
	if containsAny(text, "我要买", "我想买", "想买", "准备买", "打算买", "购买", "下单", "开通", "订购", "要买") {
		return true
	}
	if !hasASCIIDigit(text) || !containsAny(text, "ip", "静态", "动态", "住宅", "套餐", "5m", "10m", "20m") {
		return false
	}
	return containsAny(text, "要", "拿", "来", "开", "买")
}

func hasASCIIDigit(text string) bool {
	for _, r := range text {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func containsPublicDiscountDisclosure(answer string) bool {
	text := normalizePublicIntentText(answer)
	return containsAny(text,
		"多买多优惠", "多买优惠", "阶梯优惠", "阶梯价格", "阶梯价", "批量优惠", "批量价",
		"大量购买优惠", "买得越多", "买越多", "优惠价格方案", "优惠方案", "优惠价", "折扣价", "折扣方案",
	)
}

func sanitizePublicAnswer(answer string, question string) (string, bool) {
	lower := strings.ToLower(answer)
	if containsAny(lower,
		"wiki/index.md",
		"outputs/",
		"wiki/unconfirmed",
		"wiki/forbidden",
		"slug",
		"资料库中仅包含",
		"系统索引页",
		"历史检查报告",
		"请问您希望删除整个资料库",
		"如果是特定页面",
	) {
		return "当前无法直接处理这类系统操作。如需处理资料或系统配置，请联系管理员。", true
	}
	if internalPathPattern.MatchString(answer) {
		return "当前无法直接处理这类系统操作。如需处理资料或系统配置，请联系管理员。", true
	}
	if containsAny(strings.ToLower(question), "删除资料库", "删除知识库", "删库") {
		return "这个请求不属于对外客服问答范围。如需处理系统或资料管理操作，请联系管理员。", true
	}
	return "", false
}

var internalPathPattern = regexp.MustCompile(`wiki/[a-z0-9/_\-.]+\.md`)

func containsAny(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func genericPublicFallback(question string) string {
	lower := strings.ToLower(strings.TrimSpace(question))
	switch {
	case containsAny(lower, "关机", "重启", "开机", "启动"):
		return "您好，这项操作我这边暂时还不能准确确认，建议您先参考设备说明或联系对应支持人员处理。"
	case containsAny(lower, "安装", "下载", "设置", "配置", "登录"):
		return "您好，这方面我这边暂时没有可直接确认的操作说明，您可以补充一下具体场景，我再为您确认。"
	default:
		return "您好，这个问题我这边暂时还不能准确确认，您可以补充一下具体场景，我再为您确认。"
	}
}

func isPublicReadableEvidence(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !strings.HasPrefix(path, "wiki/") || !strings.HasSuffix(path, ".md") {
		return false
	}
	if strings.HasPrefix(path, "wiki/unconfirmed/") ||
		strings.HasPrefix(path, "wiki/forbidden/") ||
		strings.HasPrefix(path, "wiki/templates/") {
		return false
	}
	return publicEvidenceDirectoryRank(path) < 99
}

func publicSourceConfidence(path string) string {
	path = filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(path, "wiki/knowledge/"),
		strings.HasPrefix(path, "wiki/policies/"),
		strings.HasPrefix(path, "wiki/procedures/"),
		strings.HasPrefix(path, "wiki/comparisons/"),
		strings.HasPrefix(path, "wiki/synthesis/"):
		return "high"
	case strings.HasPrefix(path, "wiki/concepts/"),
		strings.HasPrefix(path, "wiki/entities/"),
		strings.HasPrefix(path, "wiki/sources/"):
		return "medium"
	default:
		return "low"
	}
}

var wikilinkPattern = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

func linkedPublicEvidencePathsFromContent(content string) []string {
	matches := wikilinkPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		target := strings.TrimSpace(match[1])
		target = strings.TrimPrefix(target, "wiki/")
		target = strings.TrimPrefix(target, "./")
		target = strings.TrimSuffix(target, ".md")
		if strings.Contains(target, "/") {
			candidate := "wiki/" + target + ".md"
			if isPublicReadableEvidence(candidate) {
				paths = append(paths, candidate)
			}
			continue
		}
		if !wikiadapter.IsValidSlug(target) {
			continue
		}
		for _, dir := range []string{"knowledge", "policies", "procedures", "comparisons", "synthesis", "concepts", "entities", "intents", "sources"} {
			paths = append(paths, "wiki/"+dir+"/"+target+".md")
		}
	}
	return paths
}

func dedupeEvidencePaths(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		if item == "" || seen[item] || !isPublicReadableEvidence(item) {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func retrievedPagePaths(pages []retrieval.RetrievedPage) []string {
	out := make([]string, 0, len(pages))
	seen := map[string]bool{}
	for _, page := range pages {
		path := strings.TrimSpace(page.Path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func formatMatchedPageList(paths []string) string {
	if len(paths) == 0 {
		return "- 暂无"
	}
	lines := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			lines = append(lines, "- "+strings.TrimSpace(path))
		}
	}
	if len(lines) == 0 {
		return "- 暂无"
	}
	return strings.Join(lines, "\n")
}

func runtimeCall(name string, args map[string]any) runtime.ToolCall {
	return runtime.ToolCall{Name: name, Args: args}
}
