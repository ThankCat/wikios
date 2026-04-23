package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"wikios/internal/llm"
	"wikios/internal/report"
	"wikios/internal/runtime"
	"wikios/internal/wikiadapter"
)

type IngestRequest struct {
	InputType       string `json:"input_type"`
	Path            string `json:"path"`
	Interactive     bool   `json:"interactive"`
	ContentOverride string `json:"content_override,omitempty"`
	TitleOverride   string `json:"title_override,omitempty"`
	SHA256Override  string `json:"sha256_override,omitempty"`
}

type IngestService struct {
	baseService
}

type ingestConceptItem struct {
	Title          string   `json:"title"`
	Slug           string   `json:"slug"`
	EnglishName    string   `json:"english_name"`
	Aliases        []string `json:"aliases"`
	Definition     string   `json:"definition"`
	KeyPoints      []string `json:"key_points"`
	Contradictions []string `json:"contradictions"`
}

type ingestEntityItem struct {
	Title            string   `json:"title"`
	Slug             string   `json:"slug"`
	EntityType       string   `json:"entity_type"`
	Aliases          []string `json:"aliases"`
	Description      string   `json:"description"`
	KeyContributions []string `json:"key_contributions"`
}

type ingestLLMOutput struct {
	Summary           string              `json:"summary"`
	SourceTitle       string              `json:"source_title"`
	SourceSlug        string              `json:"source_slug"`
	KeyPoints         []string            `json:"key_points"`
	ConceptsAffected  []string            `json:"concepts_affected"`
	EntitiesAffected  []string            `json:"entities_affected"`
	Concepts          []ingestConceptItem `json:"concepts"`
	Entities          []ingestEntityItem  `json:"entities"`
	Contradictions    []string            `json:"contradictions"`
	LowRiskFixes      []string            `json:"low_risk_fixes"`
	HighRiskProposals []string            `json:"high_risk_proposals"`
	Warnings          []string            `json:"warnings"`
	PossiblyOutdated  bool                `json:"possibly_outdated"`
}

type pageChangeSet struct {
	Created []string
	Updated []string
}

const faqAnalyzePromptRunes = 2800

type faqSegmentRunResult struct {
	Index             int
	Title             string
	Slug              string
	Category          string
	EntryCount        int
	SourcePage        string
	CreatedPages      []string
	UpdatedPages      []string
	ConceptsAffected  []string
	EntitiesAffected  []string
	LowRiskFixes      []string
	HighRiskProposals []string
	Warnings          []string
}

func NewIngestService(deps Deps) *IngestService {
	return &IngestService{baseService: newBaseService(deps)}
}

func (s *IngestService) Run(ctx context.Context, execution *Execution, traceID string, req IngestRequest) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	normalizedPath, err := s.normalizeMountedInputPath(req.Path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(normalizedPath, "raw/") {
		return nil, fmt.Errorf("ingest path must be under raw/, got %s", normalizedPath)
	}
	if _, err := s.executeTool(ctx, execution, env, "workspace.create_job_dir", map[string]any{"job_id": execution.ID}, "create job dir"); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(req.ContentOverride)
	shaValue := strings.TrimSpace(req.SHA256Override)
	if content == "" {
		readResult, err := s.executeTool(ctx, execution, env, "fs.read_file", map[string]any{"path": normalizedPath}, "read raw")
		if err != nil {
			return nil, err
		}
		content, _ = readResult.Data["content"].(string)
	}
	if shaValue == "" {
		hashResult, err := s.executeTool(ctx, execution, env, "hash.sha256", map[string]any{"path": normalizedPath}, "hash raw")
		if err != nil {
			return nil, err
		}
		shaValue = fmt.Sprintf("%v", hashResult.Data["sha256"])
	}
	title := firstNonEmpty(strings.TrimSpace(req.TitleOverride), extractTitle(content, filepath.Base(normalizedPath)))
	faqDataset, err := detectCanonicalFAQDataset(normalizedPath, title, content)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(normalizedPath), ".json") && faqDataset == nil {
		return nil, ValidationError{Message: "当前仅支持 FAQ JSON 导入，文件中未识别到顶层 faq 数组。"}
	}
	if faqDataset != nil {
		return s.runStructuredFAQIngest(ctx, execution, env, normalizedPath, shaValue, faqDataset)
	}
	analysis, err := s.analyzeIngestContent(ctx, execution, normalizedPath, req.Interactive, content, shaValue)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(analysis.SourceTitle) != "" {
		title = strings.TrimSpace(analysis.SourceTitle)
	}
	slug := slugFromText(title)
	if strings.TrimSpace(analysis.SourceSlug) != "" && wikiadapter.IsValidSlug(strings.TrimSpace(analysis.SourceSlug)) {
		slug = strings.TrimSpace(analysis.SourceSlug)
	}
	if !wikiadapter.IsValidSlug(slug) {
		return nil, fmt.Errorf("derived slug is invalid")
	}
	target := "wiki/sources/" + slug + ".md"
	frontmatter := map[string]any{
		"title":             title,
		"date":              nowDate(),
		"processed":         true,
		"raw_file":          normalizedPath,
		"raw_sha256":        shaValue,
		"last_verified":     nowDate(),
		"possibly_outdated": analysis.PossiblyOutdated,
	}
	if _, err := s.executeTool(ctx, execution, env, "wiki.create_from_template", map[string]any{
		"template_path": "wiki/templates/source-template.md",
		"target_path":   target,
		"frontmatter":   frontmatter,
	}, "create source page"); err != nil {
		return nil, err
	}
	ops := []map[string]any{
		{"type": "replace_section", "section": "## Summary", "content": renderIngestSummary(content, analysis)},
		{"type": "replace_section", "section": "## Key Points", "content": bulletListOrPlaceholder(analysis.KeyPoints, "待补充关键要点")},
		{"type": "replace_section", "section": "## Concepts Extracted", "content": renderConceptExtractedSection(analysis)},
		{"type": "replace_section", "section": "## Entities Extracted", "content": renderEntityExtractedSection(analysis)},
		{"type": "replace_section", "section": "## Contradictions", "content": bulletListOrPlaceholder(analysis.Contradictions, "暂无明确矛盾")},
		{"type": "replace_section", "section": "## My Notes", "content": bulletListOrPlaceholder(analysis.Warnings, "非交互模式执行；可继续人工校对摘要和提取结果")},
	}
	if _, err := s.executeTool(ctx, execution, env, "wiki.patch_page", map[string]any{
		"path": target,
		"ops":  toAnySlice(ops),
	}, "patch source page"); err != nil {
		return nil, err
	}

	updatedPages := []string{"wiki/index.md", "wiki/log.md"}
	createdPages := []string{target}
	conceptChanges, entityChanges, pageArtifacts, err := s.upsertKnowledgePages(ctx, execution, env, slug, analysis)
	if err != nil {
		return nil, err
	}
	createdPages = append(createdPages, conceptChanges.Created...)
	createdPages = append(createdPages, entityChanges.Created...)
	updatedPages = append(updatedPages, conceptChanges.Updated...)
	updatedPages = append(updatedPages, entityChanges.Updated...)

	_, _ = s.executeTool(ctx, execution, env, "wiki.update_index_entry", map[string]any{
		"section": "## Sources",
		"entry":   fmt.Sprintf("- %s | [[sources/%s]]", nowDate(), slug),
	}, "update index")
	_, _ = s.executeTool(ctx, execution, env, "wiki.append_log", map[string]any{
		"line": fmt.Sprintf("%s | ingest | %s", nowDate(), title),
	}, "append log")
	qmdUpdated := false
	if _, err := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{"subcommand": "update"}, "qmd update"); err == nil {
		qmdUpdated = true
	}

	rep := reportResult(execution.ID, "ingest", "ingest completed", nil, execution.Steps)
	rep.Inputs = []report.Field{
		{Label: "input_type", Value: req.InputType},
		{Label: "path", Value: normalizedPath},
		{Label: "interactive", Value: fmt.Sprintf("%t", req.Interactive)},
	}
	rep.Outputs = []report.Field{
		{Label: "source_title", Value: title},
		{Label: "source_slug", Value: slug},
		{Label: "source_page", Value: target},
		{Label: "concepts_affected", Value: joinOrNone(conceptTitles(analysis))},
		{Label: "entities_affected", Value: joinOrNone(entityTitles(analysis))},
		{Label: "qmd_updated", Value: fmt.Sprintf("%t", qmdUpdated)},
	}
	rep.Artifacts = []report.Artifact{
		{Kind: "source_page", Label: "source page", Path: target},
		{Kind: "system_page", Label: "index", Path: "wiki/index.md"},
		{Kind: "system_page", Label: "log", Path: "wiki/log.md"},
	}
	rep.Artifacts = append(rep.Artifacts, pageArtifacts...)
	rep.Findings = []report.Finding{
		{Level: "low", Title: "来源页已创建", Detail: fmt.Sprintf("已基于模板生成 %s", target)},
	}
	for _, warning := range analysis.Warnings {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "摄入提示", Detail: warning})
	}
	for _, proposal := range analysis.HighRiskProposals {
		rep.Findings = append(rep.Findings, report.Finding{Level: "high", Title: "高风险提案", Detail: proposal})
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
	reportPath := "wiki/outputs/ingest/" + nowDate() + "-" + slug + "-ingest-report.md"
	reportDoc := buildReportDocument("摄入报告", "ingest", execution.ID, reportMarkdown)
	if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": reportPath, "content": reportDoc}, "write ingest report"); err != nil {
		return nil, err
	}
	return map[string]any{
		"summary":             firstNonEmpty(analysis.Summary, "ingest completed"),
		"created_pages":       dedupeStrings(createdPages),
		"updated_pages":       dedupeStrings(updatedPages),
		"concepts_affected":   conceptTitles(analysis),
		"entities_affected":   entityTitles(analysis),
		"low_risk_fixes":      dedupeStrings(analysis.LowRiskFixes),
		"high_risk_proposals": dedupeStrings(analysis.HighRiskProposals),
		"warnings":            dedupeStrings(analysis.Warnings),
		"qmd_updated":         qmdUpdated,
		"report":              reportMarkdown,
		"report_file":         reportPath,
		"output_files":        []string{reportPath},
	}, nil
}

func (s *IngestService) runStructuredFAQIngest(
	ctx context.Context,
	execution *Execution,
	env *runtime.ExecEnv,
	normalizedPath string,
	shaValue string,
	dataset *canonicalFAQDataset,
) (map[string]any, error) {
	segments := dataset.segments(faqSegmentSize)
	if len(segments) == 0 {
		return nil, ValidationError{Message: "FAQ 数据中没有可用于摄入的有效条目。"}
	}

	createdPages := []string{}
	updatedPages := []string{"wiki/index.md", "wiki/log.md"}
	allConcepts := []string{}
	allEntities := []string{}
	allLowRiskFixes := []string{}
	allHighRiskProposals := []string{}
	allWarnings := []string{}
	completedSegments := []map[string]any{}
	failedSegments := []map[string]any{}
	artifacts := []report.Artifact{
		{Kind: "system_page", Label: "index", Path: "wiki/index.md"},
		{Kind: "system_page", Label: "log", Path: "wiki/log.md"},
	}

	for _, segment := range segments {
		emitStreamEvent(ctx, "segment_start", map[string]any{
			"index":       segment.Index,
			"total":       segment.Total,
			"title":       segment.title(),
			"slug":        segment.slug(),
			"category":    firstNonEmpty(strings.TrimSpace(segment.Tag), "未分类"),
			"entry_count": len(segment.Entries),
		})
		segmentResult, pageArtifacts, err := s.ingestStructuredFAQSegment(ctx, execution, env, normalizedPath, shaValue, dataset, segment)
		if err != nil {
			failure := map[string]any{
				"index":       segment.Index,
				"total":       segment.Total,
				"title":       segment.title(),
				"slug":        segment.slug(),
				"category":    firstNonEmpty(strings.TrimSpace(segment.Tag), "未分类"),
				"entry_count": len(segment.Entries),
				"error":       err.Error(),
			}
			failedSegments = append(failedSegments, failure)
			allWarnings = append(allWarnings, fmt.Sprintf("第 %d 段摄入失败：%s", segment.Index, err.Error()))
			emitStreamEvent(ctx, "segment_error", failure)
			continue
		}

		completedSegments = append(completedSegments, map[string]any{
			"index":               segmentResult.Index,
			"total":               segment.Total,
			"title":               segmentResult.Title,
			"slug":                segmentResult.Slug,
			"category":            segmentResult.Category,
			"entry_count":         segmentResult.EntryCount,
			"source_page":         segmentResult.SourcePage,
			"created_pages":       segmentResult.CreatedPages,
			"updated_pages":       segmentResult.UpdatedPages,
			"concepts_affected":   segmentResult.ConceptsAffected,
			"entities_affected":   segmentResult.EntitiesAffected,
			"warnings":            segmentResult.Warnings,
			"high_risk_proposals": segmentResult.HighRiskProposals,
		})
		emitStreamEvent(ctx, "segment_result", completedSegments[len(completedSegments)-1])

		createdPages = append(createdPages, segmentResult.CreatedPages...)
		updatedPages = append(updatedPages, segmentResult.UpdatedPages...)
		artifacts = append(artifacts, pageArtifacts...)
		allConcepts = append(allConcepts, segmentResult.ConceptsAffected...)
		allEntities = append(allEntities, segmentResult.EntitiesAffected...)
		allLowRiskFixes = append(allLowRiskFixes, segmentResult.LowRiskFixes...)
		allHighRiskProposals = append(allHighRiskProposals, segmentResult.HighRiskProposals...)
		allWarnings = append(allWarnings, segmentResult.Warnings...)
	}

	qmdUpdated := false
	if len(completedSegments) > 0 {
		if _, err := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{"subcommand": "update"}, "qmd update"); err == nil {
			qmdUpdated = true
		}
	}

	partialSuccess := len(failedSegments) > 0 && len(completedSegments) > 0
	summary := fmt.Sprintf(
		"已完成 FAQ 数据兼容摄入：共处理 %d 条标准问答，拆分为 %d 个 source segment，成功 %d 段，失败 %d 段。",
		len(dataset.Entries),
		len(segments),
		len(completedSegments),
		len(failedSegments),
	)
	if partialSuccess {
		summary = fmt.Sprintf(
			"FAQ 数据已部分摄入：共处理 %d 条标准问答，拆分为 %d 个 source segment，成功 %d 段，失败 %d 段。",
			len(dataset.Entries),
			len(segments),
			len(completedSegments),
			len(failedSegments),
		)
	}
	if len(completedSegments) == 0 && len(failedSegments) > 0 {
		summary = fmt.Sprintf(
			"FAQ 数据摄入未完成：共处理 %d 条标准问答，拆分为 %d 个 source segment，但全部失败。",
			len(dataset.Entries),
			len(segments),
		)
	}

	rep := reportResult(execution.ID, "ingest", summary, nil, execution.Steps)
	rep.Inputs = []report.Field{
		{Label: "input_type", Value: "file"},
		{Label: "path", Value: normalizedPath},
		{Label: "source_format", Value: dataset.Format},
	}
	rep.Outputs = []report.Field{
		{Label: "source_title", Value: dataset.TitleBase},
		{Label: "source_slug_base", Value: dataset.SlugBase},
		{Label: "segment_total", Value: fmt.Sprintf("%d", len(segments))},
		{Label: "segments_completed", Value: fmt.Sprintf("%d", len(completedSegments))},
		{Label: "segments_failed", Value: fmt.Sprintf("%d", len(failedSegments))},
		{Label: "source_pages", Value: joinOrNone(sourcePagePathsFromArtifacts(artifacts))},
		{Label: "faq_entry_count", Value: fmt.Sprintf("%d", len(dataset.Entries))},
		{Label: "concepts_affected", Value: joinOrNone(dedupeStrings(allConcepts))},
		{Label: "entities_affected", Value: joinOrNone(dedupeStrings(allEntities))},
		{Label: "qmd_updated", Value: fmt.Sprintf("%t", qmdUpdated)},
	}
	rep.Artifacts = dedupeArtifacts(artifacts)
	rep.Findings = []report.Finding{
		{Level: "low", Title: "FAQ 结构化摄入执行完成", Detail: fmt.Sprintf("已基于 %s 拆分 %d 个 FAQ source segment", dataset.Format, len(segments))},
	}
	for _, item := range failedSegments {
		record := item
		rep.Findings = append(rep.Findings, report.Finding{
			Level:  "high",
			Title:  fmt.Sprintf("分段失败：第 %v 段", record["index"]),
			Detail: fmt.Sprintf("%v", record["error"]),
		})
	}
	for _, warning := range dedupeStrings(allWarnings) {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "摄入提示", Detail: warning})
	}
	for _, note := range dataset.Notes {
		rep.Findings = append(rep.Findings, report.Finding{Level: "low", Title: "FAQ 元数据", Detail: note})
	}
	for _, proposal := range dedupeStrings(allHighRiskProposals) {
		rep.Findings = append(rep.Findings, report.Finding{Level: "high", Title: "高风险提案", Detail: proposal})
	}
	if len(failedSegments) > 0 {
		rep.NextActions = append(rep.NextActions, "检查 failed_segments 中的失败段，必要时重试对应 segment 或修复模板/索引问题")
	}
	if !qmdUpdated && len(completedSegments) > 0 {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "QMD 未更新", Detail: "FAQ source 页面已落盘，但未成功刷新 qmd 索引"})
		rep.NextActions = append(rep.NextActions, "手动执行 qmd update，确认 FAQ source segment 已进入索引")
	} else if qmdUpdated {
		rep.NextActions = append(rep.NextActions, "可继续执行 admin/query 或公共问答，验证 FAQ segment 检索与回答表现")
	}
	reportMarkdown := report.Markdown(rep)
	reportPath := "wiki/outputs/ingest/" + nowDate() + "-" + dataset.SlugBase + "-ingest-report.md"
	reportDoc := buildReportDocument("FAQ 数据摄入报告", "ingest", execution.ID, reportMarkdown)
	if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": reportPath, "content": reportDoc}, "write ingest report"); err != nil {
		return nil, err
	}

	return map[string]any{
		"summary":             summary,
		"source_title":        dataset.TitleBase,
		"source_slug_base":    dataset.SlugBase,
		"source_pages":        dedupeStrings(sourcePagePathsFromArtifacts(artifacts)),
		"created_pages":       dedupeStrings(createdPages),
		"updated_pages":       dedupeStrings(updatedPages),
		"concepts_affected":   dedupeStrings(allConcepts),
		"entities_affected":   dedupeStrings(allEntities),
		"low_risk_fixes":      dedupeStrings(allLowRiskFixes),
		"high_risk_proposals": dedupeStrings(allHighRiskProposals),
		"warnings":            dedupeStrings(append(allWarnings, dataset.Notes...)),
		"qmd_updated":         qmdUpdated,
		"report":              reportMarkdown,
		"report_file":         reportPath,
		"output_files":        []string{reportPath},
		"segments_total":      len(segments),
		"segments_completed":  len(completedSegments),
		"segments_failed":     len(failedSegments),
		"segment_results":     completedSegments,
		"failed_segments":     failedSegments,
		"segments":            completedSegments,
		"partial_success":     partialSuccess,
	}, nil
}

func (s *IngestService) ingestStructuredFAQSegment(
	ctx context.Context,
	execution *Execution,
	env *runtime.ExecEnv,
	normalizedPath string,
	shaValue string,
	dataset *canonicalFAQDataset,
	segment canonicalFAQSegment,
) (faqSegmentRunResult, []report.Artifact, error) {
	analysis := s.analyzeStructuredFAQSegment(ctx, execution, normalizedPath, shaValue, segment)
	target := "wiki/sources/" + segment.slug() + ".md"
	frontmatter := map[string]any{
		"title":             segment.title(),
		"date":              nowDate(),
		"processed":         true,
		"raw_file":          normalizedPath,
		"raw_sha256":        shaValue,
		"last_verified":     nowDate(),
		"possibly_outdated": analysis.PossiblyOutdated,
		"source_format":     dataset.Format,
		"source_family":     dataset.Family,
		"segment_index":     segment.Index,
		"segment_total":     segment.Total,
		"faq_entry_count":   len(segment.Entries),
	}
	if _, err := s.executeTool(ctx, execution, env, "wiki.create_from_template", map[string]any{
		"template_path": "wiki/templates/source-template.md",
		"target_path":   target,
		"frontmatter":   frontmatter,
	}, "create source page "+target); err != nil {
		return faqSegmentRunResult{}, nil, err
	}
	readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": target}, "read source page "+target)
	if err != nil {
		return faqSegmentRunResult{}, nil, err
	}
	rawContent, _ := readResult.Data["content"].(string)
	keyPointsContent := bulletListOrPlaceholder(segment.keyPoints(), "待补充 FAQ 关键要点")
	ops := []map[string]any{
		{"type": "replace_section", "section": "## Summary", "content": segment.summary()},
		{"type": "replace_section", "section": "## Concepts Extracted", "content": renderConceptExtractedSection(analysis)},
		{"type": "replace_section", "section": "## Entities Extracted", "content": renderEntityExtractedSection(analysis)},
		{"type": "replace_section", "section": "## Contradictions", "content": bulletListOrPlaceholder(analysis.Contradictions, "暂无明确矛盾")},
		{"type": "replace_section", "section": "## My Notes", "content": bulletListOrPlaceholder(mergeStringLists(segment.notes(), analysis.Warnings), "FAQ 数据已自动规范化并分段摄入")},
	}
	if strings.Contains(rawContent, "## FAQ Entries") {
		ops = append(ops,
			map[string]any{"type": "replace_section", "section": "## Key Points", "content": keyPointsContent},
			map[string]any{"type": "replace_section", "section": "## FAQ Entries", "content": renderFAQEntriesSection(segment.Entries)},
		)
	} else {
		ops = append(ops,
			map[string]any{
				"type":    "replace_section",
				"section": "## Key Points",
				"content": keyPointsContent + "\n\n## FAQ Entries\n\n" + renderFAQEntriesSection(segment.Entries),
			},
		)
	}
	if _, err := s.executeTool(ctx, execution, env, "wiki.patch_page", map[string]any{
		"path": target,
		"ops":  toAnySlice(ops),
	}, "patch source page "+target); err != nil {
		return faqSegmentRunResult{}, nil, err
	}

	result := faqSegmentRunResult{
		Index:             segment.Index,
		Title:             segment.title(),
		Slug:              segment.slug(),
		Category:          firstNonEmpty(strings.TrimSpace(segment.Tag), "未分类"),
		EntryCount:        len(segment.Entries),
		SourcePage:        target,
		CreatedPages:      []string{target},
		UpdatedPages:      []string{"wiki/index.md", "wiki/log.md"},
		ConceptsAffected:  conceptTitles(analysis),
		EntitiesAffected:  entityTitles(analysis),
		LowRiskFixes:      dedupeStrings(analysis.LowRiskFixes),
		HighRiskProposals: dedupeStrings(analysis.HighRiskProposals),
		Warnings:          dedupeStrings(analysis.Warnings),
	}
	artifacts := []report.Artifact{{Kind: "source_page", Label: "source page", Path: target}}

	conceptChanges, entityChanges, pageArtifacts, err := s.upsertKnowledgePages(ctx, execution, env, segment.slug(), analysis)
	if err != nil {
		return faqSegmentRunResult{}, nil, err
	}
	result.CreatedPages = append(result.CreatedPages, conceptChanges.Created...)
	result.CreatedPages = append(result.CreatedPages, entityChanges.Created...)
	result.UpdatedPages = append(result.UpdatedPages, conceptChanges.Updated...)
	result.UpdatedPages = append(result.UpdatedPages, entityChanges.Updated...)
	artifacts = append(artifacts, pageArtifacts...)

	_, _ = s.executeTool(ctx, execution, env, "wiki.update_index_entry", map[string]any{
		"section": "## Sources",
		"entry":   fmt.Sprintf("- %s | [[sources/%s]]", nowDate(), segment.slug()),
	}, "update index")
	_, _ = s.executeTool(ctx, execution, env, "wiki.append_log", map[string]any{
		"line": fmt.Sprintf("%s | ingest | %s", nowDate(), segment.title()),
	}, "append log")
	return result, artifacts, nil
}

func (s *IngestService) analyzeIngestContent(ctx context.Context, execution *Execution, normalizedPath string, interactive bool, content string, sha256 string) (ingestLLMOutput, error) {
	prompt, err := s.loadPromptWithWikiAgent("admin_ingest_system.md")
	if err != nil {
		return ingestLLMOutput{}, err
	}
	prompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	userPrompt := fmt.Sprintf("input_type=file\ninteractive=%t\nraw_path=%s\nraw_sha256=%s\n\nraw_content:\n%s", interactive, normalizedPath, sha256, truncateForPrompt(content, 6000))
	llmText, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: userPrompt},
	}, "llm ingest analyze")
	if err != nil {
		return ingestLLMOutput{}, err
	}
	parsed := ingestLLMOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		parsed.Summary = summarizeContent(content)
		parsed.KeyPoints = strings.Split(keyPoints(content), "\n")
		parsed.Warnings = []string{"LLM 输出未通过 JSON 解析，已退化到本地摘要逻辑"}
	}
	parsed = normalizeAnalyzedIngestContent(normalizedPath, content, parsed)
	parsed.Concepts = normalizedConcepts(parsed)
	parsed.Entities = normalizedEntities(parsed)
	if len(parsed.ConceptsAffected) == 0 {
		parsed.ConceptsAffected = conceptTitles(parsed)
	}
	if len(parsed.EntitiesAffected) == 0 {
		parsed.EntitiesAffected = entityTitles(parsed)
	}
	return parsed, nil
}

func (s *IngestService) analyzeStructuredFAQSegment(
	ctx context.Context,
	execution *Execution,
	normalizedPath string,
	sha256 string,
	segment canonicalFAQSegment,
) ingestLLMOutput {
	fallback := fallbackStructuredFAQAnalysis(segment, "")
	prompt, err := s.loadPrompt("admin_ingest_faq_system.md")
	if err != nil {
		fallback.Warnings = dedupeStrings(append(fallback.Warnings, "FAQ 分段轻量分析 prompt 加载失败，已回退到本地规则分析。"))
		return fallback
	}
	userPrompt := fmt.Sprintf(
		"raw_path=%s\nraw_sha256=%s\nsource_format=%s\nsource_title=%s\nsource_slug=%s\nsegment_index=%d\nsegment_total=%d\nfaq_entry_count=%d\nsegment_category=%s\n\nfaq_segment_preview:\n%s",
		normalizedPath,
		sha256,
		segment.Dataset.Format,
		segment.title(),
		segment.slug(),
		segment.Index,
		segment.Total,
		len(segment.Entries),
		firstNonEmpty(strings.TrimSpace(segment.Tag), "未分类"),
		truncateForPrompt(segment.renderContent(), faqAnalyzePromptRunes),
	)
	llmText, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: userPrompt},
	}, "llm ingest faq analyze")
	if err != nil {
		return fallbackStructuredFAQAnalysis(segment, err.Error())
	}
	parsed := ingestLLMOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		return fallbackStructuredFAQAnalysis(segment, "FAQ 分段 LLM 输出未通过 JSON 解析，已回退到本地规则分析。")
	}
	if strings.TrimSpace(parsed.Summary) == "" {
		parsed.Summary = segment.summary()
	}
	if strings.TrimSpace(parsed.SourceTitle) == "" {
		parsed.SourceTitle = segment.title()
	}
	if strings.TrimSpace(parsed.SourceSlug) == "" || !wikiadapter.IsValidSlug(strings.TrimSpace(parsed.SourceSlug)) {
		parsed.SourceSlug = segment.slug()
	}
	if len(parsed.KeyPoints) == 0 {
		parsed.KeyPoints = segment.keyPoints()
	}
	parsed.Concepts = normalizedConcepts(parsed)
	parsed.Entities = normalizedEntities(parsed)
	if len(parsed.ConceptsAffected) == 0 {
		parsed.ConceptsAffected = conceptTitles(parsed)
	}
	if len(parsed.EntitiesAffected) == 0 {
		parsed.EntitiesAffected = entityTitles(parsed)
	}
	return parsed
}

func (s *IngestService) upsertKnowledgePages(ctx context.Context, execution *Execution, env *runtime.ExecEnv, sourceSlug string, analysis ingestLLMOutput) (pageChangeSet, pageChangeSet, []report.Artifact, error) {
	concepts, conceptArtifacts, err := s.upsertConceptPages(ctx, execution, env, analysis.Concepts, sourceSlug)
	if err != nil {
		return pageChangeSet{}, pageChangeSet{}, nil, err
	}
	entities, entityArtifacts, err := s.upsertEntityPages(ctx, execution, env, analysis.Entities, sourceSlug)
	if err != nil {
		return pageChangeSet{}, pageChangeSet{}, nil, err
	}
	return concepts, entities, append(conceptArtifacts, entityArtifacts...), nil
}

func (s *IngestService) upsertConceptPages(ctx context.Context, execution *Execution, env *runtime.ExecEnv, items []ingestConceptItem, sourceSlug string) (pageChangeSet, []report.Artifact, error) {
	changes := pageChangeSet{}
	artifacts := []report.Artifact{}
	for _, item := range items {
		if item.Slug == "" {
			continue
		}
		path, created, err := s.resolveOrCreateConceptPage(ctx, execution, env, item, sourceSlug)
		if err != nil {
			return pageChangeSet{}, nil, err
		}
		if created {
			changes.Created = append(changes.Created, path)
		} else {
			changes.Updated = append(changes.Updated, path)
		}
		artifacts = append(artifacts, report.Artifact{Kind: "concept", Label: "concept page", Path: path})
	}
	return changes, artifacts, nil
}

func (s *IngestService) upsertEntityPages(ctx context.Context, execution *Execution, env *runtime.ExecEnv, items []ingestEntityItem, sourceSlug string) (pageChangeSet, []report.Artifact, error) {
	changes := pageChangeSet{}
	artifacts := []report.Artifact{}
	for _, item := range items {
		if item.Slug == "" {
			continue
		}
		path, created, err := s.resolveOrCreateEntityPage(ctx, execution, env, item, sourceSlug)
		if err != nil {
			return pageChangeSet{}, nil, err
		}
		if created {
			changes.Created = append(changes.Created, path)
		} else {
			changes.Updated = append(changes.Updated, path)
		}
		artifacts = append(artifacts, report.Artifact{Kind: "entity", Label: "entity page", Path: path})
	}
	return changes, artifacts, nil
}

func (s *IngestService) resolveOrCreateConceptPage(ctx context.Context, execution *Execution, env *runtime.ExecEnv, item ingestConceptItem, sourceSlug string) (string, bool, error) {
	slugResult, err := s.executeTool(ctx, execution, env, "wiki.find_by_slug", map[string]any{"slug": item.Slug}, "find concept by slug "+item.Slug)
	if err != nil {
		return "", false, err
	}
	if path, _ := slugResult.Data["path"].(string); strings.HasPrefix(path, "wiki/concepts/") {
		return s.patchConceptPage(ctx, execution, env, path, item, sourceSlug, false)
	}
	for _, alias := range conceptAliasCandidates(item) {
		aliasResult, err := s.executeTool(ctx, execution, env, "wiki.find_by_alias", map[string]any{"alias": alias}, "find concept by alias "+alias)
		if err != nil {
			return "", false, err
		}
		for _, path := range stringifyAnySlice(aliasResult.Data["matches"]) {
			if strings.HasPrefix(path, "wiki/concepts/") {
				return s.patchConceptPage(ctx, execution, env, path, item, sourceSlug, false)
			}
		}
	}
	path := "wiki/concepts/" + item.Slug + ".md"
	frontmatter := map[string]any{
		"title":         item.Title,
		"aliases":       toAnyStrings(conceptAliasCandidates(item)),
		"updated":       nowDate(),
		"last_reviewed": nowDate(),
		"source_count":  1,
		"confidence":    "low",
	}
	if _, err := s.executeTool(ctx, execution, env, "wiki.create_from_template", map[string]any{
		"template_path": "wiki/templates/concept-template.md",
		"target_path":   path,
		"frontmatter":   frontmatter,
	}, "create concept page "+path); err != nil {
		return "", false, err
	}
	return s.patchConceptPage(ctx, execution, env, path, item, sourceSlug, true)
}

func (s *IngestService) patchConceptPage(ctx context.Context, execution *Execution, env *runtime.ExecEnv, path string, item ingestConceptItem, sourceSlug string, created bool) (string, bool, error) {
	sourceLink := fmt.Sprintf("- [[sources/%s]]", sourceSlug)
	readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": path}, "read concept page "+path)
	if err != nil {
		return "", false, err
	}
	rawContent, _ := readResult.Data["content"].(string)
	doc, err := wikiadapter.ParseDocument(rawContent)
	if err != nil {
		return "", false, err
	}
	sourceCount := sourceCountFromFrontmatter(doc.Frontmatter["source_count"])
	if sourceCount == 0 {
		sourceCount = 1
	}
	if !strings.Contains(rawContent, sourceLink) {
		sourceCount++
	}
	aliases := mergeStringLists(stringifyAnySlice(doc.Frontmatter["aliases"]), conceptAliasCandidates(item))
	ops := []map[string]any{
		{"type": "replace_section", "section": "## Definition", "content": firstNonEmpty(strings.TrimSpace(item.Definition), conceptDefinitionFallback(item))},
		{"type": "replace_section", "section": "## Key Points", "content": bulletListOrPlaceholder(item.KeyPoints, "待补充关键要点")},
		{"type": "replace_section", "section": "## Contradictions", "content": bulletListOrPlaceholder(item.Contradictions, "暂无明确矛盾")},
		{"type": "update_frontmatter", "fields": map[string]any{
			"title":         item.Title,
			"aliases":       toAnyStrings(aliases),
			"updated":       nowDate(),
			"last_reviewed": nowDate(),
			"source_count":  sourceCount,
			"confidence":    confidenceBySourceCount(sourceCount),
		}},
	}
	if !strings.Contains(rawContent, sourceLink) {
		ops = append(ops, map[string]any{"type": "append_section", "section": "## Sources", "content": sourceLink})
	}
	if !strings.Contains(rawContent, sourceSlug) {
		ops = append(ops, map[string]any{"type": "append_section", "section": "## Evolution Log", "content": fmt.Sprintf("- %s（%d sources）：由 [[sources/%s]] 新增或强化该概念", nowDate(), maxInt(sourceCount, 1), sourceSlug)})
	}
	if _, err := s.executeTool(ctx, execution, env, "wiki.patch_page", map[string]any{
		"path": path,
		"ops":  toAnySlice(ops),
	}, "patch concept page "+path); err != nil {
		return "", false, err
	}
	return path, created, nil
}

func (s *IngestService) resolveOrCreateEntityPage(ctx context.Context, execution *Execution, env *runtime.ExecEnv, item ingestEntityItem, sourceSlug string) (string, bool, error) {
	slugResult, err := s.executeTool(ctx, execution, env, "wiki.find_by_slug", map[string]any{"slug": item.Slug}, "find entity by slug "+item.Slug)
	if err != nil {
		return "", false, err
	}
	if path, _ := slugResult.Data["path"].(string); strings.HasPrefix(path, "wiki/entities/") {
		return s.patchEntityPage(ctx, execution, env, path, item, sourceSlug, false)
	}
	for _, alias := range entityAliasCandidates(item) {
		aliasResult, err := s.executeTool(ctx, execution, env, "wiki.find_by_alias", map[string]any{"alias": alias}, "find entity by alias "+alias)
		if err != nil {
			return "", false, err
		}
		for _, path := range stringifyAnySlice(aliasResult.Data["matches"]) {
			if strings.HasPrefix(path, "wiki/entities/") {
				return s.patchEntityPage(ctx, execution, env, path, item, sourceSlug, false)
			}
		}
	}
	path := "wiki/entities/" + item.Slug + ".md"
	frontmatter := map[string]any{
		"title":       item.Title,
		"aliases":     toAnyStrings(entityAliasCandidates(item)),
		"entity_type": firstNonEmpty(item.EntityType, "other"),
	}
	if _, err := s.executeTool(ctx, execution, env, "wiki.create_from_template", map[string]any{
		"template_path": "wiki/templates/entity-template.md",
		"target_path":   path,
		"frontmatter":   frontmatter,
	}, "create entity page "+path); err != nil {
		return "", false, err
	}
	return s.patchEntityPage(ctx, execution, env, path, item, sourceSlug, true)
}

func (s *IngestService) patchEntityPage(ctx context.Context, execution *Execution, env *runtime.ExecEnv, path string, item ingestEntityItem, sourceSlug string, created bool) (string, bool, error) {
	sourceLink := fmt.Sprintf("- [[sources/%s]]", sourceSlug)
	readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": path}, "read entity page "+path)
	if err != nil {
		return "", false, err
	}
	rawContent, _ := readResult.Data["content"].(string)
	doc, err := wikiadapter.ParseDocument(rawContent)
	if err != nil {
		return "", false, err
	}
	aliases := mergeStringLists(stringifyAnySlice(doc.Frontmatter["aliases"]), entityAliasCandidates(item))
	ops := []map[string]any{
		{"type": "replace_section", "section": "## Description", "content": firstNonEmpty(item.Description, item.Title)},
		{"type": "replace_section", "section": "## Key Contributions", "content": bulletListOrPlaceholder(item.KeyContributions, "待补充关键信息")},
		{"type": "update_frontmatter", "fields": map[string]any{
			"title":       item.Title,
			"aliases":     toAnyStrings(aliases),
			"entity_type": firstNonEmpty(item.EntityType, "other"),
		}},
	}
	if !strings.Contains(rawContent, sourceLink) {
		ops = append(ops, map[string]any{"type": "append_section", "section": "## Sources", "content": sourceLink})
	}
	if _, err := s.executeTool(ctx, execution, env, "wiki.patch_page", map[string]any{
		"path": path,
		"ops":  toAnySlice(ops),
	}, "patch entity page "+path); err != nil {
		return "", false, err
	}
	return path, created, nil
}

func renderIngestSummary(content string, analysis ingestLLMOutput) string {
	summary := strings.TrimSpace(analysis.Summary)
	if summary == "" {
		summary = summarizeContent(content)
	}
	if analysis.PossiblyOutdated && !strings.Contains(summary, "过时") {
		summary += "\n\n⚠ 该来源发表时间可能较早，需谨慎校验其时效性。"
	}
	return summary
}

func renderConceptExtractedSection(analysis ingestLLMOutput) string {
	items := make([]string, 0, len(analysis.Concepts))
	for _, item := range analysis.Concepts {
		label := firstNonEmpty(item.Title, item.Slug)
		if item.Slug != "" {
			items = append(items, fmt.Sprintf("- %s（[[concepts/%s]]）", label, item.Slug))
		}
	}
	if len(items) == 0 {
		return bulletListOrPlaceholder(analysis.ConceptsAffected, "待补充概念提取")
	}
	return strings.Join(items, "\n")
}

func renderEntityExtractedSection(analysis ingestLLMOutput) string {
	items := make([]string, 0, len(analysis.Entities))
	for _, item := range analysis.Entities {
		label := firstNonEmpty(item.Title, item.Slug)
		if item.Slug != "" {
			items = append(items, fmt.Sprintf("- %s（[[entities/%s]]）", label, item.Slug))
		}
	}
	if len(items) == 0 {
		return bulletListOrPlaceholder(analysis.EntitiesAffected, "待补充实体提取")
	}
	return strings.Join(items, "\n")
}

func conceptTitles(analysis ingestLLMOutput) []string {
	if len(analysis.Concepts) == 0 {
		return dedupeStrings(analysis.ConceptsAffected)
	}
	out := make([]string, 0, len(analysis.Concepts))
	for _, item := range analysis.Concepts {
		out = append(out, firstNonEmpty(item.Title, item.Slug))
	}
	return dedupeStrings(out)
}

func entityTitles(analysis ingestLLMOutput) []string {
	if len(analysis.Entities) == 0 {
		return dedupeStrings(analysis.EntitiesAffected)
	}
	out := make([]string, 0, len(analysis.Entities))
	for _, item := range analysis.Entities {
		out = append(out, firstNonEmpty(item.Title, item.Slug))
	}
	return dedupeStrings(out)
}

func normalizedConcepts(parsed ingestLLMOutput) []ingestConceptItem {
	if len(parsed.Concepts) == 0 {
		out := make([]ingestConceptItem, 0, len(parsed.ConceptsAffected))
		for _, item := range dedupeStrings(parsed.ConceptsAffected) {
			slug := slugFromText(item)
			if !wikiadapter.IsValidSlug(slug) {
				continue
			}
			keyPoints := dedupeStrings(parsed.KeyPoints)
			if len(keyPoints) > 3 {
				keyPoints = keyPoints[:3]
			}
			out = append(out, ingestConceptItem{
				Title:       item,
				Slug:        slug,
				EnglishName: humanizeSlug(slug),
				Aliases:     []string{item, slug},
				Definition:  fallbackConceptDefinition(item, parsed.Summary),
				KeyPoints:   keyPoints,
			})
		}
		return out
	}
	out := make([]ingestConceptItem, 0, len(parsed.Concepts))
	for _, item := range parsed.Concepts {
		slug := firstNonEmpty(item.Slug, slugFromText(firstNonEmpty(item.Title, item.EnglishName)))
		if !wikiadapter.IsValidSlug(slug) {
			continue
		}
		title := firstNonEmpty(item.Title, item.EnglishName, humanizeSlug(slug))
		out = append(out, ingestConceptItem{
			Title:          title,
			Slug:           slug,
			EnglishName:    firstNonEmpty(item.EnglishName, humanizeSlug(slug)),
			Aliases:        mergeStringLists(item.Aliases, []string{title, slug, item.EnglishName}),
			Definition:     strings.TrimSpace(item.Definition),
			KeyPoints:      dedupeStrings(item.KeyPoints),
			Contradictions: dedupeStrings(item.Contradictions),
		})
	}
	return out
}

func normalizedEntities(parsed ingestLLMOutput) []ingestEntityItem {
	if len(parsed.Entities) == 0 {
		out := make([]ingestEntityItem, 0, len(parsed.EntitiesAffected))
		for _, item := range dedupeStrings(parsed.EntitiesAffected) {
			slug := slugFromText(item)
			if !wikiadapter.IsValidSlug(slug) {
				continue
			}
			keyPoints := dedupeStrings(parsed.KeyPoints)
			if len(keyPoints) > 3 {
				keyPoints = keyPoints[:3]
			}
			out = append(out, ingestEntityItem{
				Title:            item,
				Slug:             slug,
				Aliases:          []string{item, slug},
				Description:      fallbackEntityDescription(item, parsed.Summary),
				KeyContributions: keyPoints,
			})
		}
		return out
	}
	out := make([]ingestEntityItem, 0, len(parsed.Entities))
	for _, item := range parsed.Entities {
		slug := firstNonEmpty(item.Slug, slugFromText(item.Title))
		if !wikiadapter.IsValidSlug(slug) {
			continue
		}
		title := firstNonEmpty(item.Title, humanizeSlug(slug))
		out = append(out, ingestEntityItem{
			Title:            title,
			Slug:             slug,
			EntityType:       strings.TrimSpace(item.EntityType),
			Aliases:          mergeStringLists(item.Aliases, []string{title, slug}),
			Description:      strings.TrimSpace(item.Description),
			KeyContributions: dedupeStrings(item.KeyContributions),
		})
	}
	return out
}

func conceptAliasCandidates(item ingestConceptItem) []string {
	return mergeStringLists(item.Aliases, []string{item.Title, item.EnglishName, item.Slug})
}

func entityAliasCandidates(item ingestEntityItem) []string {
	return mergeStringLists(item.Aliases, []string{item.Title, item.Slug})
}

func conceptDefinitionFallback(item ingestConceptItem) string {
	if item.EnglishName != "" && item.Title != "" && item.EnglishName != item.Title {
		return fmt.Sprintf("%s（%s）", item.Title, item.EnglishName)
	}
	return firstNonEmpty(item.Title, item.EnglishName, item.Slug)
}

func fallbackConceptDefinition(title string, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return title
	}
	if strings.Contains(summary, title) {
		return summary
	}
	return fmt.Sprintf("%s：%s", title, summary)
}

func fallbackEntityDescription(title string, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return title
	}
	if strings.Contains(summary, title) {
		return summary
	}
	return fmt.Sprintf("%s：%s", title, summary)
}

func fallbackStructuredFAQAnalysis(segment canonicalFAQSegment, reason string) ingestLLMOutput {
	warnings := []string{}
	if strings.TrimSpace(reason) != "" {
		warnings = append(warnings, "FAQ 分段的 LLM 分析失败，已回退到本地规则分析；source 页仍会写入，但概念和实体提取可能不完整。")
		warnings = append(warnings, "失败原因："+strings.TrimSpace(reason))
	}
	return ingestLLMOutput{
		Summary:          segment.summary(),
		SourceTitle:      segment.title(),
		SourceSlug:       segment.slug(),
		KeyPoints:        segment.keyPoints(),
		Warnings:         dedupeStrings(warnings),
		PossiblyOutdated: false,
	}
}

func dedupeStrings(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func mergeStringLists(groups ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, group := range groups {
		for _, item := range group {
			item = strings.TrimSpace(item)
			if item == "" || seen[item] {
				continue
			}
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func stringifyAnySlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		text := strings.TrimSpace(fmt.Sprintf("%v", item))
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func toAnyStrings(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sourceCountFromFrontmatter(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func confidenceBySourceCount(sourceCount int) string {
	if sourceCount >= 3 {
		return "medium"
	}
	return "low"
}

func humanizeSlug(slug string) string {
	parts := strings.Split(strings.TrimSpace(slug), "-")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if len(part) <= 2 {
			out = append(out, strings.ToUpper(part))
			continue
		}
		out = append(out, strings.ToUpper(part[:1])+part[1:])
	}
	return strings.Join(out, " ")
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
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

func normalizeAnalyzedIngestContent(normalizedPath string, content string, parsed ingestLLMOutput) ingestLLMOutput {
	if !looksLikeKnowledgeBaseTable(content) {
		return parsed
	}
	parsed.SourceTitle = deriveKnowledgeBaseSourceTitle(normalizedPath, content)
	parsed.SourceSlug = deriveKnowledgeBaseSourceSlug(normalizedPath, content)
	parsed.Summary = buildKnowledgeBaseSegmentSummary(normalizedPath, content)
	parsed.Warnings = dedupeStrings(append(parsed.Warnings, "检测到多主题客服知识库表格，已改用分段标题和摘要，避免单条问答主题误导整页语义。"))
	return parsed
}

func looksLikeKnowledgeBaseTable(content string) bool {
	if !strings.Contains(content, "| 技能分类") || !strings.Contains(content, "| 标准问题") {
		return false
	}
	categories := extractKnowledgeBaseCategories(content)
	return len(categories) >= 2 && countKnowledgeBaseRows(content) >= 6
}

func extractKnowledgeBaseCategories(content string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitMarkdownTableRow(line)
		if len(cells) == 0 {
			continue
		}
		category := strings.TrimSpace(cells[0])
		if category == "" || category == "技能分类" || looksLikeMarkdownSeparator(category) || seen[category] {
			continue
		}
		seen[category] = true
		out = append(out, category)
	}
	return out
}

func countKnowledgeBaseRows(content string) int {
	count := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := splitMarkdownTableRow(line)
		if len(cells) < 2 || cells[0] == "技能分类" || looksLikeMarkdownSeparator(cells[0]) {
			continue
		}
		count++
	}
	return count
}

func splitMarkdownTableRow(line string) []string {
	parts := strings.Split(line, "|")
	if len(parts) < 3 {
		return nil
	}
	out := make([]string, 0, len(parts)-2)
	for _, part := range parts[1 : len(parts)-1] {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func looksLikeMarkdownSeparator(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, r := range text {
		if r != '-' && r != ':' {
			return false
		}
	}
	return true
}

func deriveKnowledgeBaseSourceTitle(normalizedPath string, content string) string {
	label := strings.TrimSuffix(filepath.Base(normalizedPath), filepath.Ext(normalizedPath))
	if strings.Contains(content, "四叶天") {
		return fmt.Sprintf("四叶天代理IP客户服务知识库分段（%s）", label)
	}
	return fmt.Sprintf("客户服务知识库分段（%s）", label)
}

func deriveKnowledgeBaseSourceSlug(normalizedPath string, content string) string {
	label := slugFromText(strings.TrimSuffix(filepath.Base(normalizedPath), filepath.Ext(normalizedPath)))
	if label == "" {
		label = "segment"
	}
	if strings.Contains(content, "四叶天") {
		return "siyetian-customer-service-knowledge-base-" + label
	}
	return "customer-service-knowledge-base-" + label
}

func buildKnowledgeBaseSegmentSummary(normalizedPath string, content string) string {
	label := strings.TrimSuffix(filepath.Base(normalizedPath), filepath.Ext(normalizedPath))
	categories := extractKnowledgeBaseCategories(content)
	if len(categories) > 4 {
		categories = categories[:4]
	}
	categoryText := "多类客服问答"
	if len(categories) > 0 {
		categoryText = strings.Join(categories, "、")
	}
	subject := "客户服务知识库"
	if strings.Contains(content, "四叶天") {
		subject = "“四叶天”代理IP客户服务知识库"
	}
	return fmt.Sprintf(
		"本次 ingest 处理的是%s的一个表格分段（%s），属于多主题客服问答集合，而不是单一主题 FAQ。该分段覆盖%s等分类。\n\n同一分段中既有带“海外IP”限定的问答，也有不带限定词的通用 IP / 登录问答；整理时不应把单条问答里的限定词上升为整页标题或整页结论。",
		subject,
		label,
		categoryText,
	)
}

func dedupeArtifacts(items []report.Artifact) []report.Artifact {
	seen := map[string]bool{}
	out := make([]report.Artifact, 0, len(items))
	for _, item := range items {
		key := item.Kind + "|" + item.Path
		if item.Path == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func sourcePagePathsFromArtifacts(items []report.Artifact) []string {
	out := []string{}
	for _, item := range items {
		if item.Kind != "source_page" {
			continue
		}
		out = append(out, item.Path)
	}
	return dedupeStrings(out)
}
