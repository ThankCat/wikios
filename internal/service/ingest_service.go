package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"wikios/internal/report"
	"wikios/internal/task"
	"wikios/internal/wikiadapter"
)

type IngestRequest struct {
	InputType   string `json:"input_type"`
	Path        string `json:"path"`
	Interactive bool   `json:"interactive"`
}

type IngestService struct {
	baseService
}

func NewIngestService(deps Deps) *IngestService {
	return &IngestService{baseService: newBaseService(deps)}
}

func (s *IngestService) Run(ctx context.Context, taskModel *task.Task, traceID string, req IngestRequest) (map[string]any, error) {
	env := s.env("admin", traceID, taskModel.ID, taskModel.ID)
	if !strings.HasPrefix(filepath.ToSlash(req.Path), "raw/") {
		return nil, fmt.Errorf("ingest path must be under raw/")
	}
	if _, err := s.executeTool(ctx, taskModel, env, "workspace.create_job_dir", map[string]any{"job_id": taskModel.ID}, "create job dir"); err != nil {
		return nil, err
	}
	readResult, err := s.executeTool(ctx, taskModel, env, "fs.read_file", map[string]any{"path": req.Path}, "read raw")
	if err != nil {
		return nil, err
	}
	hashResult, err := s.executeTool(ctx, taskModel, env, "hash.sha256", map[string]any{"path": req.Path}, "hash raw")
	if err != nil {
		return nil, err
	}
	content, _ := readResult.Data["content"].(string)
	title := extractTitle(content, filepath.Base(req.Path))
	slug := slugFromText(title)
	if !wikiadapter.IsValidSlug(slug) {
		return nil, fmt.Errorf("derived slug is invalid")
	}
	target := "wiki/sources/" + slug + ".md"
	frontmatter := map[string]any{
		"title":         title,
		"date":          nowDate(),
		"processed":     true,
		"raw_file":      req.Path,
		"raw_sha256":    hashResult.Data["sha256"],
		"last_verified": nowDate(),
	}
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.create_from_template", map[string]any{
		"template_path": "wiki/templates/source-template.md",
		"target_path":   target,
		"frontmatter":   frontmatter,
	}, "create source page"); err != nil {
		return nil, err
	}
	ops := []map[string]any{
		{"type": "replace_section", "section": "## Summary", "content": summarizeContent(content)},
		{"type": "replace_section", "section": "## Key Points", "content": keyPoints(content)},
	}
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.patch_page", map[string]any{
		"path": target,
		"ops":  toAnySlice(ops),
	}, "patch source page"); err != nil {
		return nil, err
	}
	_, _ = s.executeTool(ctx, taskModel, env, "wiki.update_index_entry", map[string]any{
		"section": "## Sources",
		"entry":   fmt.Sprintf("- %s | [[sources/%s]]", nowDate(), slug),
	}, "update index")
	_, _ = s.executeTool(ctx, taskModel, env, "wiki.append_log", map[string]any{
		"line": fmt.Sprintf("%s | ingest | %s", nowDate(), title),
	}, "append log")
	qmdUpdated := false
	if _, err := s.executeTool(ctx, taskModel, env, "exec.qmd", map[string]any{"subcommand": "update"}, "qmd update"); err == nil {
		qmdUpdated = true
	}
	rep := reportResult(taskModel.ID, "ingest", "ingest completed", nil, taskModel.Steps)
	rep.Inputs = []report.Field{
		{Label: "input_type", Value: req.InputType},
		{Label: "path", Value: req.Path},
		{Label: "interactive", Value: fmt.Sprintf("%t", req.Interactive)},
	}
	rep.Outputs = []report.Field{
		{Label: "source_title", Value: title},
		{Label: "source_slug", Value: slug},
		{Label: "source_page", Value: target},
		{Label: "qmd_updated", Value: fmt.Sprintf("%t", qmdUpdated)},
	}
	rep.Artifacts = []report.Artifact{
		{Kind: "source_page", Label: "source page", Path: target},
		{Kind: "system_page", Label: "index", Path: "wiki/index.md"},
		{Kind: "system_page", Label: "log", Path: "wiki/log.md"},
	}
	rep.Findings = []report.Finding{
		{Level: "low", Title: "来源页已创建", Detail: fmt.Sprintf("已基于模板生成 %s", target)},
	}
	if req.Interactive {
		rep.NextActions = append(rep.NextActions, "当前为交互模式请求，但 V1 仍按自动化 ingest 流程执行；如需逐步确认，需要后续补充多阶段交互")
	}
	if !qmdUpdated {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "QMD 未更新", Detail: "source 页面已落盘，但未成功刷新 qmd 索引"})
		rep.NextActions = append(rep.NextActions, "手动执行 qmd update，确认新 source 页已被索引")
	} else {
		rep.NextActions = append(rep.NextActions, "可继续执行 admin/query 或 reflect 验证新来源是否已进入检索结果")
	}
	reportMarkdown := report.Markdown(rep)
	reportPath := "wiki/outputs/ingest-report-" + nowDate() + "-" + slug + ".md"
	reportDoc := buildReportDocument("Ingest Report", "ingest", taskModel.ID, reportMarkdown)
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.write_output", map[string]any{"path": reportPath, "content": reportDoc}, "write ingest report"); err != nil {
		return nil, err
	}
	return map[string]any{
		"summary":       "ingest completed",
		"created_pages": []string{target},
		"updated_pages": []string{"wiki/index.md", "wiki/log.md"},
		"qmd_updated":   qmdUpdated,
		"report":        reportMarkdown,
		"report_file":   reportPath,
		"output_files":  []string{reportPath},
	}, nil
}

func extractTitle(content string, fallback string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return strings.TrimSuffix(fallback, filepath.Ext(fallback))
}

func summarizeContent(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) > 400 {
		content = string(runes[:400]) + "..."
	}
	return content
}

func keyPoints(content string) string {
	lines := strings.Split(content, "\n")
	points := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) < 12 || strings.HasPrefix(line, "#") {
			continue
		}
		points = append(points, "- "+line)
		if len(points) == 5 {
			break
		}
	}
	if len(points) == 0 {
		points = append(points, "- 待补充关键要点")
	}
	return strings.Join(points, "\n")
}

func toAnySlice(items []map[string]any) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}
