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
	Answer     string `json:"answer"`
	ReceivedAt string `json:"received_at,omitempty"`
	AnsweredAt string `json:"answered_at,omitempty"`
}

type PublicQueryService struct {
	baseService
}

const publicHistoryLimit = 8

type publicAnswerLLMOutput struct {
	AnswerMode         string               `json:"answer_mode"`
	AnswerType         string               `json:"answer_type"`
	AnswerMarkdown     string               `json:"answer_markdown"`
	CanAnswer          *bool                `json:"can_answer"`
	ReviewQuestion     string               `json:"review_question"`
	Confidence         float64              `json:"confidence"`
	EvidenceConfidence float64              `json:"evidence_confidence"`
	ReviewRequired     bool                 `json:"review_required"`
	ReviewReason       string               `json:"review_reason"`
	BoundaryReason     string               `json:"boundary_reason"`
	SuggestedFAQPath   string               `json:"suggested_faq_path"`
	Sources            []publicAnswerSource `json:"sources"`
	Notes              string               `json:"notes"`
}

type publicAnswerSource struct {
	Path       string `json:"path"`
	Confidence string `json:"confidence"`
}

func NewPublicQueryService(deps Deps) *PublicQueryService {
	return &PublicQueryService{baseService: newBaseService(deps)}
}

func (s *PublicQueryService) Answer(ctx context.Context, traceID string, req PublicAnswerRequest) (*PublicAnswerResponse, error) {
	receivedAt := firstNonEmpty(strings.TrimSpace(req.ReceivedAt), time.Now().Format(time.RFC3339Nano))
	if reply, ok := hardPublicSafetyReply(req.Question); ok {
		return publicAnswerResponse(reply, receivedAt), nil
	}
	if intent, ok := s.matchPublicIntent(req.Question); ok && shouldUsePublicIntentBypass(req.Question, intent) && strings.TrimSpace(intent.Response) != "" {
		return publicAnswerResponse(intent.Response, receivedAt), nil
	}
	reviewQueue := NewReviewQueueService(s.deps)
	if _, forbidden, err := reviewQueue.MatchForbidden(ctx, req.Question); err != nil {
		return nil, err
	} else if forbidden {
		return publicAnswerResponse(forbiddenPublicReply(), receivedAt), nil
	}

	env := s.env("public", traceID, "", "")
	candidateTopK := s.deps.Config.Retrieval.TopK
	if candidateTopK < 3 {
		candidateTopK = 3
	}
	if candidateTopK > 6 {
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
		for _, page := range candidates {
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

	systemPrompt, err := s.loadPrompt("public_answer_system.md")
	if err != nil {
		return nil, err
	}
	systemPrompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	userPrompt := s.publicDecisionPrompt(req, receivedAt, sources, contentBlocks)
	llmText, err := s.executeLLM(ctx, nil, s.deps.Config.LLM.ModelPublic, []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, "llm public answer")
	if err != nil {
		log.Printf("public answer llm failed trace=%s question=%q err=%v", traceID, truncateForPrompt(req.Question, 80), err)
		return publicAnswerResponse(s.publicFallback(req.Question), receivedAt), nil
	}
	parsed := s.parsePublicAnswerOutput(ctx, llmText)
	parsed.Sources = filterPublicAnswerSources(parsed.Sources, sources)
	answerMarkdown := strings.TrimSpace(parsed.AnswerMarkdown)
	if answerMarkdown == "" {
		answerMarkdown = s.publicFallback(req.Question)
	}
	if sanitized, ok := sanitizePublicAnswer(answerMarkdown, req.Question); ok {
		answerMarkdown = sanitized
	} else if normalized, ok := normalizeBrandedPublicAnswer(answerMarkdown, true); ok {
		answerMarkdown = normalized
	}
	answeredAt := time.Now().Format(time.RFC3339Nano)
	parsed.AnswerMarkdown = answerMarkdown

	if s.shouldCreatePublicReview(req, parsed) {
		_, _ = reviewQueue.CreatePending(ctx, ReviewCreateRequest{
			Question:            firstNonEmpty(parsed.ReviewQuestion, req.Question),
			OriginalQuestion:    req.Question,
			DraftAnswer:         answerMarkdown,
			SuggestedFAQPath:    parsed.SuggestedFAQPath,
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
	}, nil
}

func publicAnswerResponse(answer string, receivedAt string) *PublicAnswerResponse {
	return &PublicAnswerResponse{
		Answer:     answer,
		ReceivedAt: receivedAt,
		AnsweredAt: time.Now().Format(time.RFC3339Nano),
	}
}

func (s *PublicQueryService) parsePublicAnswerOutput(ctx context.Context, llmText string) publicAnswerLLMOutput {
	parsed := publicAnswerLLMOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err == nil {
		return normalizePublicAnswerOutput(parsed)
	}
	systemPrompt := "你只负责把输入改写成一个合法 JSON 对象，不改变语义，不补充事实。必须输出字段 answer_mode、answer_markdown、review_question、confidence、evidence_confidence、review_required、review_reason、suggested_faq_path、sources、notes；缺失字段用空字符串、false、0 或空数组补齐。"
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
	parsed.SuggestedFAQPath = strings.TrimSpace(parsed.SuggestedFAQPath)
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
	preview := buildPublicEvidencePreview(body, path, question)
	seenPaths[path] = true
	source := SourceRef{
		Path:       path,
		Title:      displayTitle,
		Confidence: publicSourceConfidence(path),
	}
	*contentBlocks = append(*contentBlocks, formatCandidatePageBlock(source, truncateForPrompt(preview, 2000)))
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

func buildPublicEvidencePreview(body string, path string, question string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	terms := publicEvidenceTerms(question)
	if len(terms) == 0 {
		return truncateForPrompt(body, 2000)
	}
	if strings.HasPrefix(filepath.ToSlash(path), "wiki/faq/") {
		if preview := relevantFAQSections(body, terms, 3); strings.TrimSpace(preview) != "" {
			return preview
		}
	}
	if preview := relevantTextWindows(body, terms, 2); strings.TrimSpace(preview) != "" {
		return preview
	}
	return truncateForPrompt(body, 2000)
}

func relevantFAQSections(body string, terms []string, limit int) string {
	sections := splitMarkdownSections(body, "### ")
	scored := make([]scoredText, 0, len(sections))
	for _, section := range sections {
		score := publicEvidenceScore(section, terms)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredText{text: strings.TrimSpace(section), score: score})
	}
	if len(scored) == 0 {
		return ""
	}
	sortScoredText(scored)
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	parts := make([]string, 0, len(scored))
	for _, item := range scored {
		parts = append(parts, truncateForPrompt(item.text, 1400))
	}
	return strings.Join(parts, "\n\n---\n\n")
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

func sanitizePublicAnswer(answer string, question string) (string, bool) {
	lower := strings.ToLower(answer)
	if containsAny(lower,
		"wiki/index.md",
		"wiki/outputs",
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

func normalizeBrandedPublicAnswer(answer string, hasEvidence bool) (string, bool) {
	if !hasEvidence {
		return "", false
	}
	normalized := strings.TrimSpace(answer)
	replacements := []struct {
		old string
		new string
	}{
		{"根据我们的服务说明，", ""},
		{"根据我们的服务说明", ""},
		{"根据我们的资料，", ""},
		{"根据我们的资料", ""},
		{"根据现有资料，", ""},
		{"根据现有资料", ""},
		{"根据现有信息，", ""},
		{"根据现有信息", ""},
		{"根据当前信息，", ""},
		{"根据当前信息", ""},
		{"根据我们客服知识库的信息，", ""},
		{"根据客服知识库的信息，", ""},
		{"根据我们客服知识库", ""},
		{"根据客服知识库", ""},
		{"当前知识库中", "目前我们这边"},
		{"当前知识库里", "目前我们这边"},
		{"目前知识库中", "目前我们这边"},
		{"目前知识库里", "目前我们这边"},
		{"知识库中", "我们这边"},
		{"知识库里", "我们这边"},
		{"知识库", "资料"},
		{"资料库中", "资料中"},
		{"资料库里", "资料里"},
		{"资料库", "资料"},
		{"检索结果", "当前信息"},
		{"常见FAQ", "常见问题"},
		{"常见 faq", "常见问题"},
		{"常见 FAQ", "常见问题"},
		{"抱歉，这个问题目前不在我们常见问题的范围内，暂时没办法给您准确确认。", "这个问题需要结合具体使用场景再确认。"},
		{"这个问题目前不在我们常见问题的范围内，暂时没办法给您准确确认。", "这个问题需要结合具体使用场景再确认。"},
		{"暂时没办法给您准确确认", "需要结合具体使用场景再确认"},
		{"管理员待确认", "需要进一步确认"},
		{"未确认", "需要进一步确认"},
		{"审查", "确认"},
		{"尚未收录", "暂时还没有整理出"},
		{"联系管理员", "联系人工客服"},
		{"管理员", "人工客服"},
		{"当前可公开联系方式：\n", ""},
		{"当前可公开联系方式:\n", ""},
		{"当前可公开联系方式：", ""},
		{"当前可公开联系方式:", ""},
		{"通过服务商提供的管理后台/控制面板", "您可以通过我们的管理后台"},
		{"通过服务商提供的管理后台", "您可以通过我们的管理后台"},
		{"服务商通常会提供", "我们通常会提供"},
		{"服务提供商通常会提供", "我们通常会提供"},
	}
	changed := false
	for _, item := range replacements {
		if strings.Contains(normalized, item.old) {
			normalized = strings.ReplaceAll(normalized, item.old, item.new)
			changed = true
		}
	}
	normalized = strings.TrimSpace(normalized)
	if changed && normalized != "" {
		return normalized, true
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
	if !strings.HasPrefix(path, "wiki/") {
		return false
	}
	if strings.HasPrefix(path, "wiki/unconfirmed/") ||
		strings.HasPrefix(path, "wiki/forbidden/") ||
		strings.HasPrefix(path, "wiki/templates/") ||
		strings.HasPrefix(path, "wiki/outputs/") {
		return false
	}
	return strings.HasSuffix(path, ".md")
}

func publicSourceConfidence(path string) string {
	switch {
	case strings.HasPrefix(path, "wiki/faq/"):
		return "high"
	case strings.HasPrefix(path, "wiki/concepts/"), strings.HasPrefix(path, "wiki/entities/"):
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
		switch {
		case strings.HasPrefix(target, "faq/"):
			paths = append(paths, "wiki/"+strings.TrimSuffix(target, ".md")+".md")
		case !strings.Contains(target, "/") && wikiadapter.IsValidSlug(strings.TrimSuffix(target, ".md")):
			paths = append(paths, "wiki/faq/"+strings.TrimSuffix(target, ".md")+".md")
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
