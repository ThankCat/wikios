package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wikios/internal/llm"
	"wikios/internal/report"
	"wikios/internal/runtime"
)

type ReflectRequest struct {
	Topic          string `json:"topic"`
	WriteReport    bool   `json:"write_report"`
	AutoFixLowRisk bool   `json:"auto_fix_low_risk"`
}

type ReflectService struct {
	baseService
}

type reflectLLMOutput struct {
	Patterns       []string               `json:"patterns"`
	Gaps           []string               `json:"gaps"`
	Contradictions []string               `json:"contradictions"`
	LowRiskFixes   []string               `json:"low_risk_fixes"`
	Proposals      []reflectProposalItem  `json:"proposals"`
	Corrections    []correctionSuggestion `json:"corrections"`
	OutputFiles    []string               `json:"output_files"`
}

type reflectProposalItem struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	RiskLevel   string   `json:"risk_level"`
	TargetFiles []string `json:"target_files"`
	Summary     string   `json:"summary"`
}

func NewReflectService(deps Deps) *ReflectService {
	return &ReflectService{baseService: newBaseService(deps)}
}

func (s *ReflectService) Run(ctx context.Context, execution *Execution, traceID string, req ReflectRequest) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	counterEvidence := s.collectCounterEvidence(ctx, execution, env, req.Topic)
	stageOneBlocks := s.collectStageOneBlocks(ctx, execution, env)
	deepPages, deepBlocks := s.collectDeepReadBlocks(ctx, execution, env, req.Topic)
	prompt, err := s.loadPromptWithWikiSections("admin_reflect_system.md", "REFLECT 相关规则", "## REFLECT 操作规范")
	if err != nil {
		return nil, err
	}
	prompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	llmText, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: fmt.Sprintf("主题：%s\n\nStage 0 反证：\n%s\n\nStage 1 模式扫描：\n%s\n\nStage 2 深读页面：\n%s", req.Topic, counterEvidence, stageOneBlocks, strings.Join(deepBlocks, "\n\n"))},
	}, "llm reflect")
	if err != nil {
		return nil, err
	}
	parsed := reflectLLMOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		parsed.Patterns = []string{llmText}
	}
	detectedCorrections, err := s.detectBackedCorrections(ctx, execution, env, req.Topic)
	if err != nil {
		return nil, err
	}
	fixes, correctionWarnings, err := s.buildCorrectionFixes(ctx, execution, env, detectedCorrections.Corrections)
	if err != nil {
		return nil, err
	}
	appliedFixes := []string{}
	if req.AutoFixLowRisk && len(fixes) > 0 {
		appliedFixes, err = s.applyCorrectionFixes(ctx, execution, env, fixes)
		if err != nil {
			return nil, err
		}
	}
	outputFiles := []string{}
	artifacts := []report.Artifact{}
	if req.WriteReport {
		path, err := report.BuildPath(report.KindReflect, slugFromText(req.Topic), time.Now())
		if err != nil {
			return nil, err
		}
		doc := buildOutputDocument("Gap Report", renderReflectOutputMarkdown(parsed), len(deepPages))
		if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": path, "content": doc}, "write reflect output"); err != nil {
			return nil, err
		}
		outputFiles = append(outputFiles, path)
		artifacts = append(artifacts, report.Artifact{Kind: "reflect_report", Label: "reflect report", Path: path})
	}
	rep := reportResult(execution.ID, "reflect", "reflect completed", outputFiles, execution.Steps)
	rep.Inputs = []report.Field{
		{Label: "topic", Value: req.Topic},
		{Label: "write_report", Value: fmt.Sprintf("%t", req.WriteReport)},
		{Label: "auto_fix_low_risk", Value: fmt.Sprintf("%t", req.AutoFixLowRisk)},
		{Label: "counter_evidence", Value: summarizeContent(counterEvidence)},
		{Label: "deep_page_count", Value: fmt.Sprintf("%d", len(deepPages))},
	}
	rep.Outputs = []report.Field{
		{Label: "deep_pages", Value: joinOrNone(deepPages)},
		{Label: "patterns", Value: joinOrNone(parsed.Patterns)},
		{Label: "gaps", Value: joinOrNone(parsed.Gaps)},
		{Label: "contradictions", Value: joinOrNone(parsed.Contradictions)},
		{Label: "low_risk_fixes", Value: joinOrNone(parsed.LowRiskFixes)},
		{Label: "detected_corrections", Value: fmt.Sprintf("%d", len(fixes))},
		{Label: "applied_corrections", Value: joinOrNone(appliedFixes)},
	}
	rep.Artifacts = artifacts
	if len(deepPages) == 0 {
		rep.Findings = append(rep.Findings, report.Finding{Level: "high", Title: "无样本页面", Detail: "reflect 没有读取到任何 wiki 页面"})
		rep.NextActions = append(rep.NextActions, "先补齐知识库内容，再执行 reflect")
	} else {
		rep.Findings = append(rep.Findings, report.Finding{Level: "low", Title: "完成样本扫描", Detail: fmt.Sprintf("已深读 %d 个页面样本", len(deepPages))})
		rep.NextActions = append(rep.NextActions, "检查 gap report，筛选低风险项进入 repair")
	}
	if strings.TrimSpace(counterEvidence) == "" || strings.Contains(counterEvidence, "未找到明显反证") {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "回音室风险", Detail: "当前未找到足够反驳证据，分析可能存在确认偏差"})
	}
	for _, gap := range parsed.Gaps {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "知识缺口", Detail: gap})
	}
	for _, contradiction := range parsed.Contradictions {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "潜在矛盾", Detail: contradiction})
	}
	for _, correction := range fixes {
		rep.Findings = append(rep.Findings, report.Finding{
			Level:  "low",
			Title:  "检测到可验证纠错",
			Detail: fmt.Sprintf("%s: %s -> %s", correction.Path, correction.Wrong, correction.Correct),
		})
	}
	for _, warning := range append(detectedCorrections.Warnings, correctionWarnings...) {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "纠错提示", Detail: warning})
	}
	if len(fixes) > 0 && !req.AutoFixLowRisk {
		rep.NextActions = append(rep.NextActions, "已检测到 raw 支持的低风险字面纠错；可用 repair 自动模式直接应用")
	}
	if len(appliedFixes) > 0 {
		rep.NextActions = append(rep.NextActions, "已自动应用低风险纠错，请重新执行 query 验证答案是否恢复")
	}
	for _, item := range parsed.Proposals {
		rep.Proposals = append(rep.Proposals, report.Proposal{
			ID:          item.ID,
			Title:       item.Title,
			RiskLevel:   item.RiskLevel,
			TargetFiles: item.TargetFiles,
			Summary:     item.Summary,
		})
	}
	if !req.WriteReport {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "未写出 gap report", Detail: "当前请求未要求将分析结果落盘"})
		rep.NextActions = append(rep.NextActions, "如需留档，请使用 write_report=true 重新执行 reflect")
	}
	reportMarkdown := report.Markdown(rep)
	reportPath, err := report.BuildPath(report.KindReflect, slugFromText(req.Topic), time.Now())
	if err != nil {
		return nil, err
	}
	reportDoc := buildReportDocument("反思与修复报告", "reflect", execution.ID, reportMarkdown)
	if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": reportPath, "content": reportDoc}, "write reflect report"); err != nil {
		return nil, err
	}
	outputFiles = append(outputFiles, reportPath)
	return map[string]any{
		"summary":              firstNonEmpty(detectedCorrections.Summary, "reflect completed"),
		"patterns":             parsed.Patterns,
		"gaps":                 parsed.Gaps,
		"contradictions":       parsed.Contradictions,
		"low_risk_fixes":       parsed.LowRiskFixes,
		"detected_corrections": fixes,
		"applied_fixes":        appliedFixes,
		"proposals":            parsed.Proposals,
		"output_files":         outputFiles,
		"report":               reportMarkdown,
		"report_file":          reportPath,
	}, nil
}

func (s *ReflectService) collectCounterEvidence(ctx context.Context, execution *Execution, env *runtime.ExecEnv, topic string) string {
	query := topic + " 反对 争议 风险 反例"
	pages, err := s.deps.Retriever.Retrieve(ctx, env, query, 3)
	if err != nil || len(pages) == 0 {
		return "未找到明显反证。⚠ 回音室风险"
	}
	blocks := make([]string, 0, len(pages))
	for _, page := range pages {
		readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": page.Path}, "counter read "+page.Path)
		if err != nil {
			continue
		}
		content, _ := readResult.Data["content"].(string)
		blocks = append(blocks, "## "+page.Path+"\n\n"+summarizeContent(content))
	}
	if len(blocks) == 0 {
		return "未找到明显反证。⚠ 回音室风险"
	}
	return strings.Join(blocks, "\n\n")
}

func (s *ReflectService) collectStageOneBlocks(ctx context.Context, execution *Execution, env *runtime.ExecEnv) string {
	requests := []struct {
		pattern string
		limit   string
	}{
		{pattern: "wiki/concepts/*.md", limit: "40"},
		{pattern: "wiki/entities/*.md", limit: "40"},
		{pattern: "wiki/synthesis/*.md", limit: "60"},
	}
	blocks := []string{}
	for _, req := range requests {
		result, err := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{
			"subcommand": "multi_get",
			"pattern":    req.pattern,
			"limit":      req.limit,
		}, "qmd multi_get "+req.pattern)
		if err != nil {
			continue
		}
		stdout, _ := result.Data["stdout"].(string)
		if strings.TrimSpace(stdout) == "" {
			continue
		}
		blocks = append(blocks, "### "+req.pattern+"\n"+summarizeContent(stdout))
	}
	if len(blocks) == 0 {
		return "qmd multi-get 未返回内容，已退化为常规页面读取。"
	}
	return strings.Join(blocks, "\n\n")
}

func (s *ReflectService) collectDeepReadBlocks(ctx context.Context, execution *Execution, env *runtime.ExecEnv, topic string) ([]string, []string) {
	pages, err := s.deps.Retriever.Retrieve(ctx, env, topic, s.deps.Config.Retrieval.TopK)
	if err != nil {
		return nil, nil
	}
	paths := make([]string, 0, len(pages))
	blocks := make([]string, 0, len(pages))
	for _, page := range pages {
		readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": page.Path}, "deep read "+page.Path)
		if err != nil {
			continue
		}
		content, _ := readResult.Data["content"].(string)
		paths = append(paths, page.Path)
		blocks = append(blocks, "## "+page.Path+"\n\n"+summarizeContent(content))
	}
	return paths, blocks
}

func renderReflectOutputMarkdown(parsed reflectLLMOutput) string {
	sections := []string{}
	if len(parsed.Patterns) > 0 {
		sections = append(sections, "## Patterns\n\n"+strings.Join(bulletize(parsed.Patterns), "\n"))
	}
	if len(parsed.Gaps) > 0 {
		sections = append(sections, "## Gaps\n\n"+strings.Join(bulletize(parsed.Gaps), "\n"))
	}
	if len(parsed.Contradictions) > 0 {
		sections = append(sections, "## Contradictions\n\n"+strings.Join(bulletize(parsed.Contradictions), "\n"))
	}
	if len(parsed.LowRiskFixes) > 0 {
		sections = append(sections, "## Low Risk Fixes\n\n"+strings.Join(bulletize(parsed.LowRiskFixes), "\n"))
	}
	if len(parsed.Proposals) > 0 {
		lines := make([]string, 0, len(parsed.Proposals))
		for _, proposal := range parsed.Proposals {
			lines = append(lines, "- "+proposal.Title+" ("+proposal.RiskLevel+"): "+proposal.Summary)
		}
		sections = append(sections, "## Proposals\n\n"+strings.Join(lines, "\n"))
	}
	if len(sections) == 0 {
		return "## Patterns\n\n- 暂无"
	}
	return strings.Join(sections, "\n\n")
}
