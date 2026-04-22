package service

import (
	"context"
	"fmt"
	"strings"

	"wikios/internal/llm"
)

type AdminQueryRequest struct {
	Question    string `json:"question"`
	WriteOutput bool   `json:"write_output"`
}

type AdminQueryService struct {
	baseService
}

type adminQueryLLMOutput struct {
	Answer         string   `json:"answer"`
	MatchedPages   []string `json:"matched_pages"`
	SourcePaths    []string `json:"source_paths"`
	Contradictions []string `json:"contradictions"`
	Limitations    []string `json:"limitations"`
	OutputFile     string   `json:"output_file"`
}

func NewAdminQueryService(deps Deps) *AdminQueryService {
	return &AdminQueryService{baseService: newBaseService(deps)}
}

func (s *AdminQueryService) Run(ctx context.Context, execution *Execution, traceID string, req AdminQueryRequest) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	pages, err := s.deps.Retriever.Retrieve(ctx, env, req.Question, s.deps.Config.Retrieval.TopK)
	if err != nil {
		return nil, err
	}
	pageContents := make([]string, 0, len(pages))
	matched := make([]string, 0, len(pages))
	for _, page := range pages {
		readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": page.Path}, "read "+page.Path)
		if err != nil {
			continue
		}
		content, _ := readResult.Data["content"].(string)
		pageContents = append(pageContents, "## "+page.Path+"\n\n"+truncateForPrompt(content, 2400))
		matched = append(matched, page.Path)
	}
	prompt, err := s.loadPromptWithWikiAgent("admin_query_system.md")
	if err != nil {
		return nil, err
	}
	prompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	llmText, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: fmt.Sprintf("问题：%s\n\n页面：\n%s", req.Question, strings.Join(pageContents, "\n\n"))},
	}, "llm admin query")
	if err != nil {
		return nil, err
	}
	parsed := adminQueryLLMOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		parsed.Answer = llmText
	}
	if len(parsed.MatchedPages) == 0 {
		parsed.MatchedPages = matched
	}
	if len(parsed.SourcePaths) == 0 {
		parsed.SourcePaths = matched
	}
	result := map[string]any{
		"summary":        "admin query completed",
		"answer":         parsed.Answer,
		"matched_pages":  parsed.MatchedPages,
		"source_paths":   parsed.SourcePaths,
		"contradictions": parsed.Contradictions,
		"limitations":    parsed.Limitations,
	}
	outputFiles := []string{}
	if req.WriteOutput {
		outputPath := "wiki/outputs/" + nowDate() + "-" + slugFromText(req.Question) + ".md"
		outputDoc := buildOutputDocument(req.Question, renderQueryOutputMarkdown(parsed), len(parsed.SourcePaths))
		if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": outputPath, "content": outputDoc}, "write output"); err != nil {
			return nil, err
		}
		_, _ = s.executeTool(ctx, execution, env, "wiki.update_index_entry", map[string]any{
			"section": "## Recent Synthesis",
			"entry":   "- [[" + strings.TrimSuffix(outputPath, ".md") + "]]",
		}, "update index")
		_, _ = s.executeTool(ctx, execution, env, "wiki.append_log", map[string]any{
			"line": fmt.Sprintf("%s | query | %s", nowDate(), outputPath),
		}, "append log")
		result["output_file"] = outputPath
		outputFiles = append(outputFiles, outputPath)
	}
	result["output_files"] = outputFiles
	return result, nil
}

func bulletize(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, "- "+item)
	}
	return out
}

func slugFromText(text string) string {
	text = strings.ToLower(text)
	text = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, text)
	text = strings.Trim(text, "-")
	for strings.Contains(text, "--") {
		text = strings.ReplaceAll(text, "--", "-")
	}
	if text == "" {
		return "output"
	}
	return text
}

func renderQueryOutputMarkdown(parsed adminQueryLLMOutput) string {
	parts := []string{strings.TrimSpace(parsed.Answer)}
	if len(parsed.SourcePaths) > 0 {
		parts = append(parts, "## Sources\n\n"+strings.Join(bulletize(parsed.SourcePaths), "\n"))
	}
	if len(parsed.Contradictions) > 0 {
		parts = append(parts, "## Contradictions\n\n"+strings.Join(bulletize(parsed.Contradictions), "\n"))
	}
	if len(parsed.Limitations) > 0 {
		parts = append(parts, "## Limitations\n\n"+strings.Join(bulletize(parsed.Limitations), "\n"))
	}
	parts = append(parts, "## Confidence Notes\n\n- 基于当前命中页面和来源链路生成，若来源不足或存在冲突，请结合 limitations 与 contradictions 一并判断。")
	return strings.Join(parts, "\n\n")
}
