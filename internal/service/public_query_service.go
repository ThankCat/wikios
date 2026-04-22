package service

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"wikios/internal/llm"
	"wikios/internal/runtime"
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
	Answer  string               `json:"answer"`
	Details *PublicAnswerDetails `json:"details,omitempty"`
}

type PublicAnswerDetails struct {
	AnswerType     string      `json:"answer_type"`
	AnswerMarkdown string      `json:"answer_markdown"`
	Sources        []SourceRef `json:"sources"`
	Confidence     float64     `json:"confidence"`
	Notes          string      `json:"notes,omitempty"`
	TraceID        string      `json:"trace_id"`
}

type PublicQueryService struct {
	baseService
}

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
	if unsupported, ok := unsupportedPublicReply(req.Question); ok {
		return &PublicAnswerResponse{
			Answer: unsupported,
			Details: &PublicAnswerDetails{
				AnswerType:     "text",
				AnswerMarkdown: unsupported,
				Sources:        nil,
				Confidence:     1,
				Notes:          "用户请求超出客服问答范围，已返回前台安全拒答。",
				TraceID:        traceID,
			},
		}, nil
	}
	env := s.env("public", traceID, "", "")
	pages, err := s.deps.Retriever.Retrieve(ctx, env, req.Question, s.deps.Config.Retrieval.TopK)
	if err != nil {
		return nil, err
	}
	contentBlocks := make([]string, 0, len(pages))
	sources := make([]SourceRef, 0, len(pages))
	for _, page := range pages {
		result, err := s.deps.Runtime.Execute(ctx, env, runtimeCall("wiki.read_page", map[string]any{"path": page.Path}))
		if err != nil || !result.Success {
			continue
		}
		content, _ := result.Data["content"].(string)
		contentBlocks = append(contentBlocks, fmt.Sprintf("## %s\n\n%s", page.Path, truncateForPrompt(content, 1800)))
		sources = append(sources, SourceRef{
			Path:       page.Path,
			Title:      strings.TrimSuffix(filepath.Base(page.Path), filepath.Ext(page.Path)),
			Confidence: sourceConfidence(page.Path),
		})
	}
	if len(contentBlocks) == 0 {
		return nil, fmt.Errorf("no readable pages found")
	}
	systemPrompt, err := s.loadPromptWithWikiAgent("public_answer_system.md")
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
	confidence := parsed.Confidence
	if confidence <= 0 || confidence > 1 {
		confidence = confidenceFromSources(mergedSources)
	}
	answerMarkdown := strings.TrimSpace(parsed.AnswerMarkdown)
	if answerMarkdown == "" {
		answerMarkdown = llmText
	}
	answerType := parsed.AnswerType
	if answerType == "" {
		answerType = "text"
	}
	if leaked, ok := sanitizePublicAnswer(answerMarkdown, req.Question); ok {
		answerMarkdown = leaked
		mergedSources = nil
		confidence = 1
	}
	return &PublicAnswerResponse{
		Answer: answerMarkdown,
		Details: &PublicAnswerDetails{
			AnswerType:     answerType,
			AnswerMarkdown: answerMarkdown,
			Sources:        mergedSources,
			Confidence:     confidence,
			Notes:          strings.TrimSpace(parsed.Notes),
			TraceID:        traceID,
		},
	}, nil
}

func formatConversationHistory(history []ChatMessage) string {
	if len(history) == 0 {
		return ""
	}
	lines := make([]string, 0, len(history))
	start := 0
	if len(history) > 6 {
		start = len(history) - 6
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

func sanitizePublicAnswer(answer string, question string) (string, bool) {
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
	) {
		return genericPublicFallback(question), true
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
		return "暂时没有这项操作说明，建议参考设备说明书或联系对应支持人员。"
	case containsAny(lower, "安装", "下载", "设置", "配置", "登录"):
		return "暂时没有这方面的操作说明，建议查看官方说明或联系对应支持人员。"
	default:
		return "暂时没有这方面的准确信息，建议稍后再试或联系对应支持人员。"
	}
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
