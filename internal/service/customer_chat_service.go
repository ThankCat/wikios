package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"wikios/internal/runtime"
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
		"client_channel", "app_policy", "app_guard", "internal_boundary_guard", "scenario_answer_guard", "unsafe_answer_guard", "human_contact_guard", "answer_sanitized",
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

const customerPricingKnowledgePagePath = "wiki/knowledge/si-ye-tian-static-ip-pricing.md"

var errCustomerKnowledgeVersionMismatch = errors.New("knowledge version mismatch: pricing page is incompatible with current customer chat rules")

func customerPricingKnowledgeCompatible(content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	if customerKnowledgePageHasDeprecatedPricing(content) {
		return false
	}
	return strings.Contains(content, "自建共享") && strings.Contains(content, "住宅独享")
}

func (s *CustomerChatService) customerKnowledgeVersionError() error {
	if s == nil || s.deps.Config == nil {
		return nil
	}
	root := strings.TrimSpace(s.deps.Config.MountedWiki.Root)
	if root == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(customerPricingKnowledgePagePath)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if customerPricingKnowledgeCompatible(string(raw)) {
		return nil
	}
	return errCustomerKnowledgeVersionMismatch
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

func runtimeCall(name string, args map[string]any) runtime.ToolCall {
	return runtime.ToolCall{Name: name, Args: args}
}
