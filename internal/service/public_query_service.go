package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"wikios/internal/llm"
	"wikios/internal/runtime"
)

type PublicAnswerRequest struct {
	Question  string         `json:"question"`
	UserID    string         `json:"user_id"`
	SessionID string         `json:"session_id"`
	Context   map[string]any `json:"context"`
}

type SourceRef struct {
	Path       string `json:"path"`
	Title      string `json:"title"`
	Confidence string `json:"confidence"`
}

type PublicAnswerResponse struct {
	Answer         string      `json:"answer"`
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
	userPrompt := fmt.Sprintf("用户问题：%s\n\n候选页面：\n%s", req.Question, strings.Join(contentBlocks, "\n\n"))
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
	return &PublicAnswerResponse{
		Answer:         answerMarkdown,
		AnswerType:     answerType,
		AnswerMarkdown: answerMarkdown,
		Sources:        mergedSources,
		Confidence:     confidence,
		Notes:          strings.TrimSpace(parsed.Notes),
		TraceID:        traceID,
	}, nil
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
