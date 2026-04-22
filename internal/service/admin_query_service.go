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
	prompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	llmText, err := s.deps.LLM.Chat(ctx, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: fmt.Sprintf("问题：%s\n\n页面：\n%s", req.Question, strings.Join(pageContents, "\n\n"))},
	})
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
	artifacts := []report.Artifact{}
	if req.WriteOutput {
		outputPath := "wiki/outputs/" + nowDate() + "-" + slugFromText(req.Question) + ".md"
		outputDoc := buildOutputDocument(req.Question, renderQueryOutputMarkdown(parsed), len(parsed.SourcePaths))
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
		artifacts = append(artifacts, report.Artifact{Kind: "synthesis", Label: "query output", Path: outputPath})
	}
	rep := reportResult(taskModel.ID, "query", "admin query completed", outputFiles, taskModel.Steps)
	rep.Inputs = []report.Field{
		{Label: "question", Value: req.Question},
		{Label: "write_output", Value: fmt.Sprintf("%t", req.WriteOutput)},
		{Label: "matched_page_count", Value: fmt.Sprintf("%d", len(matched))},
	}
	rep.Outputs = []report.Field{
		{Label: "matched_pages", Value: joinOrNone(parsed.MatchedPages)},
		{Label: "source_paths", Value: joinOrNone(parsed.SourcePaths)},
		{Label: "answer_preview", Value: summarizeContent(parsed.Answer)},
		{Label: "contradictions", Value: joinOrNone(parsed.Contradictions)},
		{Label: "limitations", Value: joinOrNone(parsed.Limitations)},
	}
	rep.Artifacts = artifacts
	if len(parsed.MatchedPages) == 0 {
		rep.Findings = append(rep.Findings, report.Finding{Level: "high", Title: "未命中页面", Detail: "没有读取到可用于回答的问题相关页面"})
		rep.NextActions = append(rep.NextActions, "缩小问题范围，或先补充 source 页面后再执行 query")
	} else if len(parsed.MatchedPages) < 3 {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "证据较薄", Detail: "命中页面较少，回答稳定性可能不足"})
		rep.NextActions = append(rep.NextActions, "补充更多来源页，提升 query 命中覆盖面")
	} else {
		rep.Findings = append(rep.Findings, report.Finding{Level: "low", Title: "命中正常", Detail: "已读取多页内容并生成回答"})
	}
	for _, contradiction := range parsed.Contradictions {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "来源分歧", Detail: contradiction})
	}
	for _, limitation := range parsed.Limitations {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "结果限制", Detail: limitation})
	}
	reportMarkdown := report.Markdown(rep)
	reportPath := "wiki/outputs/query-report-" + nowDate() + "-" + slugFromText(req.Question) + ".md"
	reportDoc := buildReportDocument("Query Report", "query", taskModel.ID, reportMarkdown)
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.write_output", map[string]any{"path": reportPath, "content": reportDoc}, "write query report"); err != nil {
		return nil, err
	}
	outputFiles = append(outputFiles, reportPath)
	result["report"] = reportMarkdown
	result["report_file"] = reportPath
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
