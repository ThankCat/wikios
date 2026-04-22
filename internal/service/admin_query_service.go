package service

import (
	"context"
	"fmt"
	"strings"

	"wikios/internal/llm"
	"wikios/internal/report"
	"wikios/internal/task"
)

type AdminQueryRequest struct {
	Question    string `json:"question"`
	WriteOutput bool   `json:"write_output"`
}

type AdminQueryService struct {
	baseService
}

func NewAdminQueryService(deps Deps) *AdminQueryService {
	return &AdminQueryService{baseService: newBaseService(deps)}
}

func (s *AdminQueryService) Run(ctx context.Context, taskModel *task.Task, traceID string, req AdminQueryRequest) (map[string]any, error) {
	env := s.env("admin", traceID, taskModel.ID, taskModel.ID)
	pages, err := s.deps.Retriever.Retrieve(ctx, env, req.Question, s.deps.Config.Retrieval.TopK)
	if err != nil {
		return nil, err
	}
	pageContents := make([]string, 0, len(pages))
	matched := make([]string, 0, len(pages))
	for _, page := range pages {
		readResult, err := s.executeTool(ctx, taskModel, env, "wiki.read_page", map[string]any{"path": page.Path}, "read "+page.Path)
		if err != nil {
			continue
		}
		content, _ := readResult.Data["content"].(string)
		pageContents = append(pageContents, "## "+page.Path+"\n\n"+content)
		matched = append(matched, page.Path)
	}
	prompt, err := s.loadPrompt("admin_query_system.md")
	if err != nil {
		return nil, err
	}
	answer, err := s.deps.LLM.Chat(ctx, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: fmt.Sprintf("问题：%s\n\n页面：\n%s", req.Question, strings.Join(pageContents, "\n\n"))},
	})
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"answer":        answer,
		"matched_pages": matched,
		"source_paths":  matched,
	}
	var outputFiles []string
	if req.WriteOutput {
		outputPath := "wiki/outputs/" + nowDate() + "-" + slugFromText(req.Question) + ".md"
		outputDoc := buildOutputDocument(req.Question, answer+"\n\n## Sources\n\n"+strings.Join(bulletize(matched), "\n"), len(matched))
		if _, err := s.executeTool(ctx, taskModel, env, "wiki.write_output", map[string]any{"path": outputPath, "content": outputDoc}, "write output"); err != nil {
			return nil, err
		}
		_, _ = s.executeTool(ctx, taskModel, env, "wiki.update_index_entry", map[string]any{
			"section": "## Recent Synthesis",
			"entry":   "- [[" + strings.TrimSuffix(outputPath, ".md") + "]]",
		}, "update index")
		_, _ = s.executeTool(ctx, taskModel, env, "wiki.append_log", map[string]any{
			"line": fmt.Sprintf("%s | query | %s", nowDate(), outputPath),
		}, "append log")
		result["output_file"] = outputPath
		outputFiles = append(outputFiles, outputPath)
	}
	rep := reportResult(taskModel.ID, "query", "admin query completed", outputFiles, taskModel.Steps)
	result["report"] = report.Markdown(rep)
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
