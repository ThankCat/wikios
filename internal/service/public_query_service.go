package service

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"wikios/internal/llm"
	"wikios/internal/runtime"
	"wikios/internal/wikiadapter"
)

type PublicAnswerRequest struct {
	Question  string         `json:"question"`
	UserID    string         `json:"user_id"`
	SessionID string         `json:"session_id"`
	Context   map[string]any `json:"context"`
	History   []ChatMessage  `json:"history"`
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type SourceRef struct {
	Path       string `json:"path"`
	Title      string `json:"title"`
	Confidence string `json:"confidence"`
}

type PublicAnswerResponse struct {
	Answer string `json:"answer"`
}

type PublicQueryService struct {
	baseService
}

const publicHistoryLimit = 8

type publicAnswerLLMOutput struct {
	AnswerType     string `json:"answer_type"`
	AnswerMarkdown string `json:"answer_markdown"`
	Sources        []struct {
		Path       string `json:"path"`
		Confidence string `json:"confidence"`
	} `json:"sources"`
	Confidence float64 `json:"confidence"`
	Notes      string  `json:"notes"`
}

func NewPublicQueryService(deps Deps) *PublicQueryService {
	return &PublicQueryService{baseService: newBaseService(deps)}
}

func (s *PublicQueryService) Answer(ctx context.Context, traceID string, req PublicAnswerRequest) (*PublicAnswerResponse, error) {
	if intent, ok := s.matchPublicIntent(req.Question); ok {
		return &PublicAnswerResponse{
			Answer: intent.Response,
		}, nil
	}
	if unsupported, ok := unsupportedPublicReply(req.Question); ok {
		return &PublicAnswerResponse{
			Answer: unsupported,
		}, nil
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
	contentBlocks := make([]string, 0, len(pages))
	sources := make([]SourceRef, 0, len(pages))
	seenPaths := map[string]bool{}
	relatedEvidencePaths := make([]string, 0, len(pages))
	for _, page := range pages {
		if !isPublicReadableEvidence(page.Path) {
			continue
		}
		content, ok := s.readPublicEvidencePage(ctx, env, page.Path, retrievalQuestion, seenPaths, &contentBlocks, &sources)
		if !ok {
			continue
		}
		relatedEvidencePaths = append(relatedEvidencePaths, linkedPublicEvidencePathsFromContent(content)...)
	}
	for _, evidencePath := range dedupeEvidencePaths(relatedEvidencePaths) {
		s.readPublicEvidencePage(ctx, env, evidencePath, retrievalQuestion, seenPaths, &contentBlocks, &sources)
	}
	if len(contentBlocks) == 0 || !hasPublicEvidence(sources) {
		fallback := s.publicFallback(req.Question)
		return &PublicAnswerResponse{
			Answer: fallback,
		}, nil
	}
	systemPrompt, err := s.loadPromptWithWikiQueryGuide("public_answer_system.md")
	if err != nil {
		return nil, err
	}
	systemPrompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	userPrompt := fmt.Sprintf(
		"%s用户问题：%s\n\n候选页面：\n%s",
		formatConversationHistory(req.History),
		req.Question,
		strings.Join(contentBlocks, "\n\n"),
	)
	llmText, err := s.executeLLM(ctx, nil, s.deps.Config.LLM.ModelPublic, []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, "llm public answer")
	if err != nil {
		return nil, err
	}
	parsed := publicAnswerLLMOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		parsed.AnswerType = "text"
		parsed.AnswerMarkdown = llmText
	}
	mergedSources := mergePromptSources(parsed.Sources, dedupeSources(sources))
	answerMarkdown := strings.TrimSpace(parsed.AnswerMarkdown)
	if answerMarkdown == "" {
		answerMarkdown = llmText
	}
	if normalized, changed := normalizeBrandedPublicAnswer(answerMarkdown, hasPublicEvidence(mergedSources)); changed {
		answerMarkdown = normalized
	}
	if leaked, ok := sanitizePublicAnswer(answerMarkdown, req.Question, s.publicFallback(req.Question)); ok {
		answerMarkdown = leaked
		mergedSources = nil
	}
	if !hasPublicEvidence(mergedSources) {
		answerMarkdown = s.publicFallback(req.Question)
		mergedSources = nil
	}
	return &PublicAnswerResponse{
		Answer: answerMarkdown,
	}, nil
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
	preview := body
	if doc, err := wikiadapter.ParseDocument(content); err == nil {
		if title, _ := doc.Frontmatter["title"].(string); strings.TrimSpace(title) != "" {
			displayTitle = strings.TrimSpace(title)
		}
		if strings.TrimSpace(doc.Body) != "" {
			body = strings.TrimSpace(doc.Body)
		}
		if isStructuredFAQEvidence(doc.Frontmatter, path) {
			preview = buildFAQEvidencePreview(body, question)
		}
	}
	if strings.TrimSpace(preview) == "" {
		preview = body
	}
	seenPaths[path] = true
	*contentBlocks = append(*contentBlocks, fmt.Sprintf("## %s\n\n%s", displayTitle, truncateForPrompt(preview, 2200)))
	*sources = append(*sources, SourceRef{
		Path:       path,
		Title:      displayTitle,
		Confidence: sourceConfidence(path),
	})
	return body, true
}

func isStructuredFAQEvidence(frontmatter map[string]any, path string) bool {
	if strings.HasPrefix(path, "wiki/faq/") {
		return true
	}
	if frontmatter == nil {
		return false
	}
	if value, _ := frontmatter["source_family"].(string); strings.TrimSpace(value) == faqSourceFamily {
		return true
	}
	if value, _ := frontmatter["source_format"].(string); strings.HasPrefix(strings.TrimSpace(value), "faq-") {
		return true
	}
	return false
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
		lines = append(lines, fmt.Sprintf("- %s: %s", role, content))
	}
	if len(lines) == 0 {
		return ""
	}
	return "最近对话上下文：\n" + strings.Join(lines, "\n") + "\n\n"
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
	if len(lines) == 0 {
		return question
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

func sanitizePublicAnswer(answer string, question string, fallback string) (string, bool) {
	lower := strings.ToLower(answer)
	if containsAny(lower,
		"wiki/index.md",
		"wiki/outputs",
		"slug",
		"资料库中仅包含",
		"系统索引页",
		"历史检查报告",
		"请问您希望删除整个资料库",
		"如果是特定页面",
	) {
		return "这个请求不属于对外客服问答范围。如需处理系统或资料管理操作，请联系管理员。", true
	}
	if internalPathPattern.MatchString(answer) {
		return "当前无法直接处理这类系统操作。如需处理资料或系统配置，请联系管理员。", true
	}
	if containsAny(lower,
		"目前知识库",
		"当前知识库",
		"知识库里",
		"知识库中",
		"知识库没有",
		"知识库暂无",
		"资料库",
		"当前资料",
		"现有资料",
		"检索结果",
		"没有专门针对",
		"当前知识库中尚未收录",
		"由于当前知识库中尚未收录",
		"知识库当前",
		"我暂时无法提供准确的信息",
		"暂时无法提供准确的信息",
		"建议您联系管理员",
		"建议联系管理员",
		"请问您是想了解",
		"请问您是想",
		"当前没有对应信息",
		"知识库中没有足够依据",
		"客服知识库",
		"服务商",
		"服务提供商",
		"用户指南",
		"通常有以下几种方式",
		"一般有以下几种方式",
		"根据客服知识库",
	) {
		if strings.TrimSpace(fallback) != "" {
			return strings.TrimSpace(fallback), true
		}
		return genericPublicFallback(question), true
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
		{"通常有以下几种方式：", ""},
		{"一般有以下几种方式：", ""},
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
	switch {
	case strings.HasPrefix(path, "wiki/faq/"):
		return true
	case strings.HasPrefix(path, "wiki/concepts/"):
		return true
	case strings.HasPrefix(path, "wiki/entities/"):
		return true
	default:
		return false
	}
}

func hasPublicEvidence(sources []SourceRef) bool {
	for _, source := range sources {
		if strings.HasPrefix(source.Path, "wiki/faq/") {
			return true
		}
	}
	return false
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
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func dedupeSources(in []SourceRef) []SourceRef {
	seen := map[string]bool{}
	out := make([]SourceRef, 0, len(in))
	for _, item := range in {
		if seen[item.Path] {
			continue
		}
		seen[item.Path] = true
		out = append(out, item)
	}
	return out
}

func confidenceFromSources(sources []SourceRef) float64 {
	if len(sources) >= 5 {
		return 0.92
	}
	if len(sources) >= 3 {
		return 0.85
	}
	if len(sources) >= 1 {
		return 0.72
	}
	return 0.3
}

func mergePromptSources(promptSources []struct {
	Path       string `json:"path"`
	Confidence string `json:"confidence"`
}, retrieved []SourceRef) []SourceRef {
	index := map[string]SourceRef{}
	for _, item := range retrieved {
		index[item.Path] = item
	}
	for _, item := range promptSources {
		if strings.TrimSpace(item.Path) == "" {
			continue
		}
		existing := index[item.Path]
		if existing.Path == "" {
			existing = SourceRef{
				Path:       item.Path,
				Title:      strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path)),
				Confidence: item.Confidence,
			}
		}
		if item.Confidence != "" {
			existing.Confidence = item.Confidence
		}
		index[item.Path] = existing
	}
	out := make([]SourceRef, 0, len(index))
	for _, item := range index {
		out = append(out, item)
	}
	return dedupeSources(out)
}

func runtimeCall(name string, args map[string]any) runtime.ToolCall {
	return runtime.ToolCall{Name: name, Args: args}
}
