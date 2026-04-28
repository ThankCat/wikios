package service

import (
	"context"
	"fmt"
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
	AnswerMode         string  `json:"answer_mode"`
	AnswerType         string  `json:"answer_type"`
	AnswerMarkdown     string  `json:"answer_markdown"`
	CanAnswer          *bool   `json:"can_answer"`
	ReviewQuestion     string  `json:"review_question"`
	Confidence         float64 `json:"confidence"`
	EvidenceConfidence float64 `json:"evidence_confidence"`
	ReviewRequired     bool    `json:"review_required"`
	ReviewReason       string  `json:"review_reason"`
	BoundaryReason     string  `json:"boundary_reason"`
	SuggestedFAQPath   string  `json:"suggested_faq_path"`
	Sources            []struct {
		Path       string `json:"path"`
		Confidence string `json:"confidence"`
	} `json:"sources"`
	Notes string `json:"notes"`
}

func NewPublicQueryService(deps Deps) *PublicQueryService {
	return &PublicQueryService{baseService: newBaseService(deps)}
}

func (s *PublicQueryService) Answer(ctx context.Context, traceID string, req PublicAnswerRequest) (*PublicAnswerResponse, error) {
	receivedAt := firstNonEmpty(strings.TrimSpace(req.ReceivedAt), time.Now().Format(time.RFC3339Nano))
	if reply, ok := hardPublicSafetyReply(req.Question); ok {
		return publicAnswerResponse(reply, receivedAt), nil
	}
	reviewQueue := NewReviewQueueService(s.deps)
	if _, forbidden, err := reviewQueue.MatchForbidden(ctx, req.Question); err != nil {
		return nil, err
	} else if forbidden {
		return publicAnswerResponse(forbiddenPublicReply(), receivedAt), nil
	}

	env := s.env("public", traceID, "", "")
	candidateTopK := s.deps.Config.Retrieval.TopK * 4
	if candidateTopK < 8 {
		candidateTopK = 8
	}
	if candidateTopK > 12 {
		candidateTopK = 12
	}
	retrievalQuestion := buildPublicRetrievalQuestion(req.Question, req.History)
	pages, err := s.deps.Retriever.Retrieve(ctx, env, retrievalQuestion, candidateTopK)
	if err != nil {
		return nil, err
	}
	retrievedPaths := retrievedPagePaths(pages)
	contentBlocks := make([]string, 0, len(pages))
	sources := make([]SourceRef, 0, len(pages))
	seenPaths := map[string]bool{}
	relatedEvidencePaths := make([]string, 0, len(pages))
	for _, page := range pages {
		if !isPublicReadableEvidence(page.Path) {
			continue
		}
		content, ok := s.readPublicEvidencePage(ctx, env, page.Path, seenPaths, &contentBlocks, &sources)
		if !ok {
			continue
		}
		relatedEvidencePaths = append(relatedEvidencePaths, linkedPublicEvidencePathsFromContent(content)...)
	}
	for _, evidencePath := range dedupeEvidencePaths(relatedEvidencePaths) {
		s.readPublicEvidencePage(ctx, env, evidencePath, seenPaths, &contentBlocks, &sources)
	}

	systemPrompt, err := s.loadPrompt("public_answer_system.md")
	if err != nil {
		return nil, err
	}
	systemPrompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	userPrompt := s.publicDecisionPrompt(req, receivedAt, retrievedPaths, contentBlocks)
	llmText, err := s.executeLLM(ctx, nil, s.deps.Config.LLM.ModelPublic, []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, "llm public answer")
	if err != nil {
		return nil, err
	}
	parsed := s.parsePublicAnswerOutput(ctx, llmText)
	answerMarkdown := strings.TrimSpace(parsed.AnswerMarkdown)
	if answerMarkdown == "" {
		answerMarkdown = s.publicFallback(req.Question)
	}
	fallback := s.publicFallback(req.Question)
	if sanitized, ok := sanitizePublicAnswer(answerMarkdown, req.Question, fallback); ok {
		answerMarkdown = sanitized
	} else if normalized, ok := normalizeBrandedPublicAnswer(answerMarkdown, true); ok {
		answerMarkdown = normalized
	}
	answeredAt := time.Now().Format(time.RFC3339Nano)
	parsed.AnswerMarkdown = answerMarkdown

	if s.shouldCreatePublicReview(parsed) {
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
	systemPrompt := "你只负责把输入改写成一个 JSON 对象，不改变语义，不补充事实。必须输出字段 answer_mode、answer_markdown、confidence、evidence_confidence、review_required、review_reason、suggested_faq_path。"
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

func (s *PublicQueryService) shouldCreatePublicReview(parsed publicAnswerLLMOutput) bool {
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if mode == "refusal" || strings.TrimSpace(parsed.AnswerMarkdown) == "" {
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
	if confidence >= reviewMin {
		return true
	}
	return parsed.ReviewRequired
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

func (s *PublicQueryService) publicDecisionPrompt(req PublicAnswerRequest, receivedAt string, retrievedPaths []string, contentBlocks []string) string {
	candidateText := strings.TrimSpace(strings.Join(contentBlocks, "\n\n"))
	if candidateText == "" {
		candidateText = "- 暂无候选页面"
	}
	return fmt.Sprintf(
		"当前时间：%s\n用户问题：%s\n\n%s当前可公开联系方式：\n%s\n\n检索到的候选路径：\n%s\n\n候选页面内容：\n%s",
		receivedAt,
		strings.TrimSpace(req.Question),
		formatConversationHistory(req.History),
		s.supportContactPrompt(),
		formatMatchedPageList(retrievedPaths),
		candidateText,
	)
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

func (s *PublicQueryService) readPublicEvidencePage(
	ctx context.Context,
	env *runtime.ExecEnv,
	path string,
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
	seenPaths[path] = true
	*contentBlocks = append(*contentBlocks, fmt.Sprintf("## %s\npath: %s\n\n%s", displayTitle, path, truncateForPrompt(body, 2200)))
	*sources = append(*sources, SourceRef{
		Path:       path,
		Title:      displayTitle,
		Confidence: publicSourceConfidence(path),
	})
	return body, true
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
	if containsAny(lower,
		"诈骗", "洗钱", "攻击", "盗号", "木马", "恶意软件", "ddos", "sql注入", "sql injection",
		"破解", "撞库", "爬取隐私", "窃取", "钓鱼网站", "绕过监管",
	) {
		return "这个请求我这边不能协助处理。", true
	}
	return "", false
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

func sanitizePublicAnswer(answer string, question string, fallback string) (string, bool) {
	lower := strings.ToLower(answer)
	if containsAny(lower,
		"wiki/index.md",
		"wiki/outputs",
		"wiki/unconfirmed",
		"wiki/forbidden",
		"slug",
		"知识库",
		"资料库",
		"检索结果",
		"常见faq",
		"常见 faq",
		"未确认",
		"审查",
		"管理员待确认",
		"尚未收录",
		"没有专门针对",
		"资料库中仅包含",
		"系统索引页",
		"历史检查报告",
		"请问您希望删除整个资料库",
		"如果是特定页面",
	) {
		if containsAny(lower, "wiki/", "slug", "资料库中仅包含", "系统索引页", "历史检查报告", "请问您希望删除整个资料库", "如果是特定页面") {
			return "当前无法直接处理这类系统操作。如需处理资料或系统配置，请联系管理员。", true
		}
		return firstNonEmpty(strings.TrimSpace(fallback), genericPublicFallback(question)), true
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
