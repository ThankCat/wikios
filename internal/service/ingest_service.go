package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
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

const faqClassificationMaxEstimatedTokens = 180000

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

type faqCategoryRunResult struct {
	Index             int
	Title             string
	Slug              string
	Category          string
	EntryCount        int
	FAQPage           string
	CreatedPages      []string
	UpdatedPages      []string
	ConceptsAffected  []string
	EntitiesAffected  []string
	LowRiskFixes      []string
	HighRiskProposals []string
	Warnings          []string
}

type faqClassificationBatchOutput struct {
	Categories []faqClassificationItem `json:"categories"`
	Warnings   []string                `json:"warnings"`
	Status     string                  `json:"-"`
	Fallbacks  int                     `json:"-"`
}

type faqClassificationItem struct {
	Title            string              `json:"title"`
	Slug             string              `json:"slug"`
	Category         string              `json:"category"`
	Summary          string              `json:"summary"`
	KeyPoints        []string            `json:"key_points"`
	EntryIDs         []string            `json:"entry_ids"`
	ConceptsAffected []string            `json:"concepts_affected"`
	EntitiesAffected []string            `json:"entities_affected"`
	Concepts         []ingestConceptItem `json:"concepts"`
	Entities         []ingestEntityItem  `json:"entities"`
	Warnings         []string            `json:"warnings"`
}

type faqCategoryGroup struct {
	Title            string
	Slug             string
	Category         string
	Summary          string
	KeyPoints        []string
	Entries          []canonicalFAQEntry
	ConceptsAffected []string
	EntitiesAffected []string
	Concepts         []ingestConceptItem
	Entities         []ingestEntityItem
	Warnings         []string
}

type faqClassificationManifest struct {
	SourceFormat string                      `json:"source_format"`
	SourceTitle  string                      `json:"source_title"`
	EntryCount   int                         `json:"entry_count"`
	FAQ          []faqClassificationEntryRef `json:"faq"`
}

type faqClassificationEntryRef struct {
	ID               string   `json:"id"`
	OriginalCategory string   `json:"original_category"`
	Question         string   `json:"question"`
	SimilarQuestions []string `json:"similar_questions,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	QuickReplies     []string `json:"quick_replies,omitempty"`
	AnswerSummary    string   `json:"answer_summary"`
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
	knowledgeProfile, profileWarnings := s.loadKnowledgeProfile()
	content := strings.TrimSpace(req.ContentOverride)
	shaValue := strings.TrimSpace(req.SHA256Override)
	if content == "" && strings.HasSuffix(strings.ToLower(normalizedPath), ".xlsx") {
		rawPath := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(normalizedPath))
		raw, err := os.ReadFile(rawPath)
		if err != nil {
			return nil, err
		}
		titleHint := firstNonEmpty(strings.TrimSpace(req.TitleOverride), strings.TrimSuffix(filepath.Base(normalizedPath), filepath.Ext(normalizedPath)))
		normalizedContent, faqDataset, err := parseFAQXLSXDatasetWithProfile(normalizedPath, titleHint, raw, knowledgeProfile)
		if err != nil {
			return nil, err
		}
		content = strings.TrimSpace(normalizedContent)
		if shaValue == "" {
			sum := sha256.Sum256(raw)
			shaValue = hex.EncodeToString(sum[:])
		}
		if faqDataset != nil {
			faqDataset.Notes = append(faqDataset.Notes, profileWarnings...)
			return s.runStructuredFAQIngest(ctx, execution, env, normalizedPath, shaValue, faqDataset)
		}
	}
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
	faqDataset, err := detectCanonicalFAQDatasetWithProfile(normalizedPath, title, content, knowledgeProfile)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(normalizedPath), ".json") && faqDataset == nil {
		return nil, ValidationError{Message: "当前仅支持 FAQ JSON 导入，文件中未识别到顶层 faq 数组。"}
	}
	if faqDataset != nil {
		faqDataset.Notes = append(faqDataset.Notes, profileWarnings...)
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
		"entry":   fmt.Sprintf("- %s | [[%s]]", nowDate(), slug),
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
	ensureFAQEntryIDs(dataset)
	publicEntries, preIntentCandidates := splitPublicFAQEntries(dataset.Entries)
	if len(dataset.Entries) == 0 {
		return nil, ValidationError{Message: "FAQ 数据中没有可用于摄入的有效条目。"}
	}
	if len(publicEntries) == 0 {
		return nil, ValidationError{Message: "FAQ 数据中只有前置话术候选，没有可写入公开 FAQ 主分类的业务条目。"}
	}

	createdPages := []string{}
	updatedPages := []string{"wiki/index.md", "wiki/log.md"}
	allConcepts := []string{}
	allEntities := []string{}
	allLowRiskFixes := []string{}
	allHighRiskProposals := []string{}
	allWarnings := []string{}
	completedCategories := []map[string]any{}
	failedBatches := []map[string]any{}
	classificationStatus := "trusted"
	fallbackCount := 0
	knowledgeProfile, profileWarnings := s.loadKnowledgeProfile()
	allWarnings = append(allWarnings, profileWarnings...)
	artifacts := []report.Artifact{
		{Kind: "system_page", Label: "index", Path: "wiki/index.md"},
		{Kind: "system_page", Label: "log", Path: "wiki/log.md"},
	}

	categoryGroups := map[string]*faqCategoryGroup{}
	categoryOrder := []string{}
	globalBatch := canonicalFAQSegment{
		Dataset: dataset,
		Index:   1,
		Total:   1,
		Tag:     "全局 FAQ 分类",
		Entries: publicEntries,
	}
	emitStreamEvent(ctx, "segment_start", map[string]any{
		"index":       1,
		"total":       1,
		"title":       "FAQ 全局分类规划",
		"slug":        dataset.SlugBase,
		"category":    "全局分类",
		"entry_count": len(publicEntries),
	})
	classification := s.classifyStructuredFAQBatch(ctx, execution, normalizedPath, shaValue, globalBatch, knowledgeProfile)
	if classification.Status != "" {
		classificationStatus = classification.Status
	}
	fallbackCount += classification.Fallbacks
	if len(classification.Warnings) > 0 {
		allWarnings = append(allWarnings, classification.Warnings...)
	}
	if len(preIntentCandidates) > 0 {
		allWarnings = append(allWarnings, fmt.Sprintf("已识别 %d 条前置话术候选，未写入公开 FAQ 主分类。", len(preIntentCandidates)))
	}
	if len(classification.Categories) == 0 {
		failure := map[string]any{
			"index":       1,
			"total":       1,
			"title":       "FAQ 全局分类规划",
			"slug":        dataset.SlugBase,
			"category":    "全局分类",
			"entry_count": len(publicEntries),
			"error":       "FAQ 全局分类没有可用分类结果",
		}
		failedBatches = append(failedBatches, failure)
		allWarnings = append(allWarnings, "FAQ 全局分类失败：没有可用分类结果")
		emitStreamEvent(ctx, "segment_error", failure)
	} else {
		mergeFAQClassifications(categoryGroups, &categoryOrder, globalBatch, classification.Categories)
		emitStreamEvent(ctx, "segment_result", map[string]any{
			"index":                 1,
			"total":                 1,
			"title":                 "FAQ 全局分类规划",
			"entry_count":           len(publicEntries),
			"category_count":        len(classification.Categories),
			"category_slugs":        faqClassificationSlugs(classification.Categories),
			"classification_status": classificationStatus,
			"fallback_count":        fallbackCount,
			"pre_intent_candidates": len(preIntentCandidates),
			"warnings":              classification.Warnings,
		})
	}

	if len(categoryOrder) == 0 {
		return nil, ValidationError{Message: "FAQ 数据未生成任何可写入的业务分类。"}
	}
	qualityWarnings, qualityErr := validateFAQQualityGates(categoryGroups, categoryOrder, knowledgeProfile)
	allWarnings = append(allWarnings, qualityWarnings...)
	if qualityErr != nil {
		return nil, qualityErr
	}

	sourceSlug := faqDatasetSourceSlug(dataset)
	sourcePath := "wiki/sources/" + sourceSlug + ".md"
	sourceDoc := buildFAQSourceArchiveDocument(dataset, normalizedPath, shaValue, sourceSlug, nil)
	if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": sourcePath, "content": sourceDoc}, "write faq source archive"); err != nil {
		return nil, err
	}
	createdPages = append(createdPages, sourcePath)
	artifacts = append(artifacts, report.Artifact{Kind: "source_page", Label: "FAQ source archive", Path: sourcePath})

	processedConcepts := map[string]bool{}
	processedEntities := map[string]bool{}
	for index, slug := range categoryOrder {
		group := categoryGroups[slug]
		if group == nil || len(group.Entries) == 0 {
			continue
		}
		categoryResult, pageArtifacts, err := s.ingestStructuredFAQCategory(ctx, execution, env, normalizedPath, shaValue, dataset, sourcePath, index+1, len(categoryOrder), group, processedConcepts, processedEntities, knowledgeProfile)
		if err != nil {
			failure := map[string]any{
				"index":       index + 1,
				"total":       len(categoryOrder),
				"title":       group.Title,
				"slug":        group.Slug,
				"category":    group.Category,
				"entry_count": len(group.Entries),
				"error":       err.Error(),
			}
			failedBatches = append(failedBatches, failure)
			allWarnings = append(allWarnings, fmt.Sprintf("FAQ 分类 %s 写入失败：%s", group.Slug, err.Error()))
			continue
		}
		completedCategories = append(completedCategories, map[string]any{
			"index":               categoryResult.Index,
			"total":               len(categoryOrder),
			"title":               categoryResult.Title,
			"slug":                categoryResult.Slug,
			"category":            categoryResult.Category,
			"entry_count":         categoryResult.EntryCount,
			"faq_page":            categoryResult.FAQPage,
			"source_archive":      sourcePath,
			"created_pages":       categoryResult.CreatedPages,
			"updated_pages":       categoryResult.UpdatedPages,
			"concepts_affected":   categoryResult.ConceptsAffected,
			"entities_affected":   categoryResult.EntitiesAffected,
			"warnings":            categoryResult.Warnings,
			"high_risk_proposals": categoryResult.HighRiskProposals,
		})
		createdPages = append(createdPages, categoryResult.CreatedPages...)
		updatedPages = append(updatedPages, categoryResult.UpdatedPages...)
		artifacts = append(artifacts, pageArtifacts...)
		allConcepts = append(allConcepts, categoryResult.ConceptsAffected...)
		allEntities = append(allEntities, categoryResult.EntitiesAffected...)
		allLowRiskFixes = append(allLowRiskFixes, categoryResult.LowRiskFixes...)
		allHighRiskProposals = append(allHighRiskProposals, categoryResult.HighRiskProposals...)
		allWarnings = append(allWarnings, categoryResult.Warnings...)
	}

	faqIndexPath := "wiki/faq/index.md"
	if len(completedCategories) > 0 {
		indexDoc := buildFAQIndexDocument(dataset, completedCategories)
		if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": faqIndexPath, "content": indexDoc}, "write faq index"); err != nil {
			return nil, err
		}
		createdPages = append(createdPages, faqIndexPath)
		artifacts = append(artifacts, report.Artifact{Kind: "faq_index", Label: "FAQ index", Path: faqIndexPath})
		sourceDoc = buildFAQSourceArchiveDocument(dataset, normalizedPath, shaValue, sourceSlug, faqPagePathsFromArtifacts(artifacts))
		if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": sourcePath, "content": sourceDoc}, "update faq source archive"); err != nil {
			return nil, err
		}
	}

	qmdUpdated := false
	if len(completedCategories) > 0 {
		if _, err := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{"subcommand": "update"}, "qmd update"); err == nil {
			qmdUpdated = true
		}
	}

	partialSuccess := len(failedBatches) > 0 && len(completedCategories) > 0
	summary := fmt.Sprintf(
		"已完成 FAQ 分类摄入：共处理 %d 条标准问答，生成 %d 个 FAQ 分类页，成功 %d 个，失败 %d 个。",
		len(dataset.Entries),
		len(categoryOrder),
		len(completedCategories),
		len(failedBatches),
	)
	if partialSuccess {
		summary = fmt.Sprintf(
			"FAQ 数据已部分摄入：共处理 %d 条标准问答，生成 %d 个 FAQ 分类页，成功 %d 个，失败 %d 个。",
			len(dataset.Entries),
			len(categoryOrder),
			len(completedCategories),
			len(failedBatches),
		)
	}
	if len(completedCategories) == 0 && len(failedBatches) > 0 {
		summary = fmt.Sprintf(
			"FAQ 数据摄入未完成：共处理 %d 条标准问答，计划生成 %d 个 FAQ 分类页，但全部失败。",
			len(dataset.Entries),
			len(categoryOrder),
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
		{Label: "source_archive", Value: sourcePath},
		{Label: "category_total", Value: fmt.Sprintf("%d", len(categoryOrder))},
		{Label: "categories_completed", Value: fmt.Sprintf("%d", len(completedCategories))},
		{Label: "batches_failed", Value: fmt.Sprintf("%d", len(failedBatches))},
		{Label: "classification_status", Value: classificationStatus},
		{Label: "knowledge_profile", Value: firstNonEmpty(profileNameForReport(knowledgeProfile), "none")},
		{Label: "fallback_count", Value: fmt.Sprintf("%d", fallbackCount)},
		{Label: "pre_intent_candidates", Value: fmt.Sprintf("%d", len(preIntentCandidates))},
		{Label: "faq_pages", Value: joinOrNone(faqPagePathsFromArtifacts(artifacts))},
		{Label: "faq_entry_count", Value: fmt.Sprintf("%d", len(dataset.Entries))},
		{Label: "concepts_affected", Value: joinOrNone(dedupeStrings(allConcepts))},
		{Label: "entities_affected", Value: joinOrNone(dedupeStrings(allEntities))},
		{Label: "qmd_updated", Value: fmt.Sprintf("%t", qmdUpdated)},
	}
	rep.Artifacts = dedupeArtifacts(artifacts)
	rep.Findings = []report.Finding{
		{Level: "low", Title: "FAQ 分类摄入执行完成", Detail: fmt.Sprintf("已基于 %s 生成 %d 个 FAQ 分类页，并保留 source 归档页", dataset.Format, len(categoryOrder))},
	}
	for _, item := range failedBatches {
		record := item
		rep.Findings = append(rep.Findings, report.Finding{
			Level:  "high",
			Title:  fmt.Sprintf("批次/分类失败：第 %v 项", record["index"]),
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
	if len(failedBatches) > 0 {
		rep.NextActions = append(rep.NextActions, "检查 failed_batches 中的失败批次或分类，必要时重试对应 FAQ 数据摄入")
	}
	if !qmdUpdated && len(completedCategories) > 0 {
		rep.Findings = append(rep.Findings, report.Finding{Level: "medium", Title: "QMD 未更新", Detail: "FAQ 页面已落盘，但未成功刷新 qmd 索引"})
		rep.NextActions = append(rep.NextActions, "手动执行 qmd update，确认 FAQ 页面已进入索引")
	} else if qmdUpdated {
		rep.NextActions = append(rep.NextActions, "可继续执行 admin/query 或公共问答，验证 FAQ-first 检索与回答表现")
	}
	reportMarkdown := report.Markdown(rep)
	reportPath := "wiki/outputs/ingest/" + nowDate() + "-" + dataset.SlugBase + "-ingest-report.md"
	reportDoc := buildReportDocument("FAQ 数据摄入报告", "ingest", execution.ID, reportMarkdown)
	if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": reportPath, "content": reportDoc}, "write ingest report"); err != nil {
		return nil, err
	}

	return map[string]any{
		"summary":               summary,
		"source_title":          dataset.TitleBase,
		"source_slug_base":      dataset.SlugBase,
		"source_archive":        sourcePath,
		"faq_index":             faqIndexPath,
		"faq_pages":             dedupeStrings(faqPagePathsFromArtifacts(artifacts)),
		"faq_page_count":        len(faqPagePathsFromArtifacts(artifacts)),
		"source_pages":          []string{sourcePath},
		"created_pages":         dedupeStrings(createdPages),
		"updated_pages":         dedupeStrings(updatedPages),
		"concepts_affected":     dedupeStrings(allConcepts),
		"entities_affected":     dedupeStrings(allEntities),
		"low_risk_fixes":        dedupeStrings(allLowRiskFixes),
		"high_risk_proposals":   dedupeStrings(allHighRiskProposals),
		"warnings":              dedupeStrings(append(allWarnings, dataset.Notes...)),
		"qmd_updated":           qmdUpdated,
		"classification_status": classificationStatus,
		"knowledge_profile":     profileNameForReport(knowledgeProfile),
		"fallback_count":        fallbackCount,
		"pre_intent_candidates": len(preIntentCandidates),
		"max_category_entries":  maxFAQCategoryEntries(categoryGroups),
		"report":                reportMarkdown,
		"report_file":           reportPath,
		"output_files":          []string{reportPath},
		"segments_total":        1,
		"segments_completed":    1 - len(failedBatches),
		"segments_failed":       len(failedBatches),
		"categories_total":      len(categoryOrder),
		"category_results":      completedCategories,
		"failed_batches":        failedBatches,
		"segment_results":       completedCategories,
		"failed_segments":       failedBatches,
		"segments":              completedCategories,
		"partial_success":       partialSuccess,
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
	pageSlug := faqPageSlug(firstNonEmpty(strings.TrimSpace(analysis.SourceSlug), segment.slug()))
	analysis.SourceSlug = pageSlug
	if strings.TrimSpace(analysis.SourceTitle) == "" {
		analysis.SourceTitle = segment.title()
	}
	target := "wiki/faq/" + pageSlug + ".md"
	frontmatter := map[string]any{
		"title":             segment.title(),
		"type":              "faq",
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
		"category":          firstNonEmpty(strings.TrimSpace(segment.Tag), "未分类"),
		"related_concepts":  toAnyStrings(conceptTitles(analysis)),
		"related_entities":  toAnyStrings(entityTitles(analysis)),
	}
	pageDoc := buildFAQPageDocument(frontmatter, segment, analysis, nil)
	if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": target, "content": pageDoc}, "write faq page "+target); err != nil {
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
	artifacts := []report.Artifact{{Kind: "faq_page", Label: "FAQ page", Path: target}}

	conceptChanges, entityChanges, pageArtifacts, err := s.upsertKnowledgePages(ctx, execution, env, pageSlug, analysis)
	if err != nil {
		return faqSegmentRunResult{}, nil, err
	}
	result.CreatedPages = append(result.CreatedPages, conceptChanges.Created...)
	result.CreatedPages = append(result.CreatedPages, entityChanges.Created...)
	result.UpdatedPages = append(result.UpdatedPages, conceptChanges.Updated...)
	result.UpdatedPages = append(result.UpdatedPages, entityChanges.Updated...)
	artifacts = append(artifacts, pageArtifacts...)

	_, _ = s.executeTool(ctx, execution, env, "wiki.update_index_entry", map[string]any{
		"section": "## FAQ",
		"entry":   fmt.Sprintf("- %s | [[%s]]", nowDate(), pageSlug),
	}, "update index")
	_, _ = s.executeTool(ctx, execution, env, "wiki.append_log", map[string]any{
		"line": fmt.Sprintf("%s | ingest | %s", nowDate(), segment.title()),
	}, "append log")
	return result, artifacts, nil
}

func (s *IngestService) ingestStructuredFAQCategory(
	ctx context.Context,
	execution *Execution,
	env *runtime.ExecEnv,
	normalizedPath string,
	shaValue string,
	dataset *canonicalFAQDataset,
	sourceArchivePath string,
	index int,
	total int,
	group *faqCategoryGroup,
	processedConcepts map[string]bool,
	processedEntities map[string]bool,
	profile *knowledgeProfile,
) (faqCategoryRunResult, []report.Artifact, error) {
	pageSlug := stableFAQCategorySlugWithProfile(profile, group.Category, group.Title, group.Slug)
	group.Slug = pageSlug
	enrichFAQCategoryKnowledgeCandidates(group, profile)
	category := firstNonEmpty(strings.TrimSpace(group.Category), strings.TrimSpace(group.Title), "未分类")
	title := firstNonEmpty(strings.TrimSpace(group.Title), category+" FAQ")
	analysis := ingestLLMOutput{
		Summary:           firstNonEmpty(strings.TrimSpace(group.Summary), fmt.Sprintf("本页汇总“%s”相关的客服 FAQ，共 %d 条标准问答。", category, len(group.Entries))),
		SourceTitle:       title,
		SourceSlug:        pageSlug,
		KeyPoints:         dedupeStrings(group.KeyPoints),
		ConceptsAffected:  dedupeStrings(group.ConceptsAffected),
		EntitiesAffected:  dedupeStrings(group.EntitiesAffected),
		Concepts:          filterFAQConceptItems(normalizedConcepts(ingestLLMOutput{Concepts: group.Concepts, ConceptsAffected: group.ConceptsAffected, Summary: group.Summary, KeyPoints: group.KeyPoints})),
		Entities:          filterFAQEntityItems(normalizedEntities(ingestLLMOutput{Entities: group.Entities, EntitiesAffected: group.EntitiesAffected, Summary: group.Summary, KeyPoints: group.KeyPoints})),
		Warnings:          dedupeStrings(group.Warnings),
		PossiblyOutdated:  false,
		Contradictions:    nil,
		LowRiskFixes:      nil,
		HighRiskProposals: nil,
	}
	if len(analysis.KeyPoints) == 0 {
		analysis.KeyPoints = []string{
			fmt.Sprintf("当前分类包含 %d 条标准 FAQ。", len(group.Entries)),
			"相似问法、关键词和运营标签已保留，用于提升查询匹配稳定性。",
		}
	}
	if len(analysis.ConceptsAffected) == 0 {
		analysis.ConceptsAffected = conceptTitles(analysis)
	}
	if len(analysis.EntitiesAffected) == 0 {
		analysis.EntitiesAffected = entityTitles(analysis)
	}
	target := "wiki/faq/" + pageSlug + ".md"
	frontmatter := map[string]any{
		"title":            title,
		"type":             "faq",
		"date":             nowDate(),
		"processed":        true,
		"raw_file":         normalizedPath,
		"raw_sha256":       shaValue,
		"raw_source_page":  sourceArchivePath,
		"last_verified":    nowDate(),
		"source_format":    dataset.Format,
		"source_family":    dataset.Family,
		"category":         category,
		"category_index":   index,
		"category_total":   total,
		"faq_entry_count":  len(group.Entries),
		"related_concepts": toAnyStrings(conceptSlugs(analysis)),
		"related_entities": toAnyStrings(entitySlugs(analysis)),
	}
	pageDoc := buildFAQCategoryPageDocument(frontmatter, group, analysis, sourceArchivePath, profile)
	if _, err := s.executeTool(ctx, execution, env, "wiki.write_output", map[string]any{"path": target, "content": pageDoc}, "write faq category page "+target); err != nil {
		return faqCategoryRunResult{}, nil, err
	}
	result := faqCategoryRunResult{
		Index:             index,
		Title:             title,
		Slug:              pageSlug,
		Category:          category,
		EntryCount:        len(group.Entries),
		FAQPage:           target,
		CreatedPages:      []string{target},
		UpdatedPages:      []string{"wiki/index.md", "wiki/log.md"},
		ConceptsAffected:  conceptTitles(analysis),
		EntitiesAffected:  entityTitles(analysis),
		LowRiskFixes:      dedupeStrings(analysis.LowRiskFixes),
		HighRiskProposals: dedupeStrings(analysis.HighRiskProposals),
		Warnings:          dedupeStrings(analysis.Warnings),
	}
	artifacts := []report.Artifact{{Kind: "faq_page", Label: "FAQ category page", Path: target}}
	upsertAnalysis := analysis
	upsertAnalysis.Concepts = filterAlreadyProcessedConcepts(analysis.Concepts, processedConcepts)
	upsertAnalysis.Entities = filterAlreadyProcessedEntities(analysis.Entities, processedEntities)
	upsertAnalysis.ConceptsAffected = conceptItemTitles(upsertAnalysis.Concepts)
	upsertAnalysis.EntitiesAffected = entityItemTitles(upsertAnalysis.Entities)
	conceptChanges, entityChanges, pageArtifacts, err := s.upsertKnowledgePages(ctx, execution, env, pageSlug, upsertAnalysis)
	if err != nil {
		return faqCategoryRunResult{}, nil, err
	}
	result.CreatedPages = append(result.CreatedPages, conceptChanges.Created...)
	result.CreatedPages = append(result.CreatedPages, entityChanges.Created...)
	result.UpdatedPages = append(result.UpdatedPages, conceptChanges.Updated...)
	result.UpdatedPages = append(result.UpdatedPages, entityChanges.Updated...)
	artifacts = append(artifacts, pageArtifacts...)
	_, _ = s.executeTool(ctx, execution, env, "wiki.update_index_entry", map[string]any{
		"section": "## FAQ",
		"entry":   fmt.Sprintf("- %s | [[%s]] | %s | %d 条", nowDate(), pageSlug, category, len(group.Entries)),
	}, "update index")
	_, _ = s.executeTool(ctx, execution, env, "wiki.append_log", map[string]any{
		"line": fmt.Sprintf("%s | ingest | FAQ 分类页 %s", nowDate(), title),
	}, "append log")
	return result, artifacts, nil
}

func (s *IngestService) analyzeIngestContent(ctx context.Context, execution *Execution, normalizedPath string, interactive bool, content string, sha256 string) (ingestLLMOutput, error) {
	prompt, err := s.loadPromptWithWikiSections("admin_ingest_system.md", "INGEST 相关规则",
		"## 系统概述",
		"## INGEST 操作规范",
		"## Wikilink 使用规范",
		"## Wiki 语言规范",
		"## Confidence 更新规则",
		"## Source Integrity Rules",
	)
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
	prompt, err := s.loadPromptWithWikiSections("admin_ingest_faq_system.md", "FAQ 摄入相关规则",
		"## 系统概述",
		"## INGEST 操作规范",
		"## Wikilink 使用规范",
		"## Wiki 语言规范",
	)
	if err != nil {
		fallback.Warnings = dedupeStrings(append(fallback.Warnings, "FAQ 分段轻量分析 prompt 加载失败，已回退到本地规则分析。"))
		return fallback
	}
	userPrompt := fmt.Sprintf(
		"raw_path=%s\nraw_sha256=%s\nsource_format=%s\nsource_title=%s\nsource_slug=%s\nsegment_index=%d\nsegment_total=%d\nfaq_entry_count=%d\nsegment_category=%s\n\nfaq_segment_json:\n%s",
		normalizedPath,
		sha256,
		segment.Dataset.Format,
		segment.title(),
		segment.slug(),
		segment.Index,
		segment.Total,
		len(segment.Entries),
		firstNonEmpty(strings.TrimSpace(segment.Tag), "未分类"),
		renderFAQSegmentAsJSON(segment),
	)
	llmText, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: userPrompt},
	}, "llm ingest faq analyze")
	if err != nil {
		return fallbackStructuredFAQAnalysis(segment, err.Error())
	}
	raw := map[string]any{}
	if err := llm.DecodeJSONObject(llmText, &raw); err != nil {
		return fallbackStructuredFAQAnalysis(segment, "FAQ 分段 LLM 输出未通过 JSON 解析，已回退到本地规则分析。")
	}
	parsed := ingestLLMOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		return fallbackStructuredFAQAnalysis(segment, "FAQ 分段 LLM 输出未通过 JSON 解析，已回退到本地规则分析。")
	}
	if !looksLikeIngestFAQAnalysis(raw, parsed) {
		return fallbackStructuredFAQAnalysis(segment, "FAQ 分段 LLM 输出不是 ingest 分析结构，已回退到本地规则分析。")
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

func (s *IngestService) classifyStructuredFAQBatch(
	ctx context.Context,
	execution *Execution,
	normalizedPath string,
	sha256 string,
	batch canonicalFAQSegment,
	profile *knowledgeProfile,
) faqClassificationBatchOutput {
	fallback := fallbackFAQClassificationBatch(batch, profile, "")
	prompt, err := s.loadPromptWithWikiSections("admin_ingest_faq_system.md", "FAQ 摄入相关规则",
		"## 系统概述",
		"## INGEST 操作规范",
		"## Wikilink 使用规范",
		"## Wiki 语言规范",
	)
	if err != nil {
		fallback.Warnings = dedupeStrings(append(fallback.Warnings, "FAQ 分类 prompt 加载失败，已回退到 server profile 分类。"))
		return fallback
	}
	manifestJSON, estimatedTokens, err := renderFAQClassificationManifest(batch)
	if err != nil {
		return fallbackFAQClassificationBatch(batch, profile, "FAQ 分类 manifest 生成失败："+err.Error())
	}
	if estimatedTokens > faqClassificationMaxEstimatedTokens {
		return fallbackFAQClassificationBatch(batch, profile, fmt.Sprintf("FAQ 分类 manifest 预计 %d tokens，超过单次分类上限 %d，已使用 server profile 完整分组。", estimatedTokens, faqClassificationMaxEstimatedTokens))
	}
	userPrompt := fmt.Sprintf(
		"raw_path=%s\nraw_sha256=%s\nsource_format=%s\nsource_title=%s\nclassification_scope=global\nfaq_entry_count=%d\nmanifest_estimated_tokens=%d\nknowledge_profile_name=%s\n\nknowledge_profile_json:\n%s\n\nfaq_classification_manifest:\n%s",
		normalizedPath,
		sha256,
		batch.Dataset.Format,
		batch.Dataset.TitleBase,
		len(batch.Entries),
		estimatedTokens,
		profileNameForReport(profile),
		profile.promptJSON(),
		manifestJSON,
	)
	llmText, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: userPrompt},
	}, "llm faq classify")
	if err != nil {
		return fallbackFAQClassificationBatch(batch, profile, err.Error())
	}
	raw := map[string]any{}
	_ = llm.DecodeJSONObject(llmText, &raw)
	parsed := faqClassificationBatchOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil || len(parsed.Categories) == 0 {
		legacy := ingestLLMOutput{}
		if _, isPublicAnswer := raw["answer_markdown"]; !isPublicAnswer {
			if _, isPublicAnswer = raw["answer_type"]; !isPublicAnswer {
				if legacyErr := llm.DecodeJSONObject(llmText, &legacy); legacyErr == nil && looksLikeIngestFAQAnalysis(raw, legacy) {
					item := faqClassificationItem{
						Title:            firstNonEmpty(legacy.SourceTitle, batch.title()),
						Slug:             firstNonEmpty(legacy.SourceSlug, batch.slug()),
						Category:         firstNonEmpty(batch.Tag, legacy.SourceTitle, "未分类"),
						Summary:          legacy.Summary,
						KeyPoints:        legacy.KeyPoints,
						EntryIDs:         faqEntryIDs(batch.Entries),
						ConceptsAffected: legacy.ConceptsAffected,
						EntitiesAffected: legacy.EntitiesAffected,
						Concepts:         legacy.Concepts,
						Entities:         legacy.Entities,
						Warnings:         legacy.Warnings,
					}
					return faqClassificationBatchOutput{Categories: []faqClassificationItem{normalizeFAQClassificationItem(batch, item, profile)}, Warnings: []string{"FAQ 分类输出使用旧分析结构，已按当前批次归入单个业务分类。"}}
				}
			}
		}
		return fallbackFAQClassificationBatch(batch, profile, "FAQ 分类 LLM 输出未通过 JSON 解析，已回退到 server profile 分类。")
	}
	out := faqClassificationBatchOutput{Warnings: dedupeStrings(parsed.Warnings), Status: "trusted"}
	for _, item := range parsed.Categories {
		normalized := normalizeFAQClassificationItem(batch, item, profile)
		if len(normalized.EntryIDs) == 0 {
			continue
		}
		out.Categories = append(out.Categories, normalized)
	}
	if len(out.Categories) == 0 {
		return fallbackFAQClassificationBatch(batch, profile, "FAQ 分类 LLM 输出没有有效 entry_ids，已回退到 server profile 分类。")
	}
	return out
}

func normalizeFAQClassificationItem(batch canonicalFAQSegment, item faqClassificationItem, profile *knowledgeProfile) faqClassificationItem {
	category := firstNonEmpty(strings.TrimSpace(item.Category), strings.TrimSpace(item.Title), strings.TrimSpace(batch.Tag), "未分类")
	title := firstNonEmpty(strings.TrimSpace(item.Title), category+" FAQ")
	slug := stableFAQCategorySlugWithProfile(profile, category, title, strings.TrimSpace(item.Slug))
	allowed := map[string]bool{}
	for _, id := range faqEntryIDs(batch.Entries) {
		allowed[id] = true
	}
	entryIDs := []string{}
	for _, id := range item.EntryIDs {
		id = strings.TrimSpace(id)
		if id != "" && allowed[id] {
			entryIDs = append(entryIDs, id)
		}
	}
	if isGenericFAQCategoryName(category, profile.genericCategoryNames()) || isGenericFAQCategoryName(title, profile.genericCategoryNames()) {
		review := profile.reviewCategory()
		category = review.Title
		title = review.Title
		slug = faqPageSlug(review.Slug)
		item.Warnings = append(item.Warnings, "LLM 输出了过泛 FAQ 分类，已转入待人工复核分类。")
	}
	if len(entryIDs) > profile.maxFAQEntriesPerCategory() {
		item.Warnings = append(item.Warnings, fmt.Sprintf("当前 FAQ 分类包含 %d 条，超过 profile 建议上限 %d，建议调整 server profile 分类规则后重新摄入。", len(entryIDs), profile.maxFAQEntriesPerCategory()))
	}
	item.Category = category
	item.Title = title
	item.Slug = slug
	item.EntryIDs = dedupeStrings(entryIDs)
	item.KeyPoints = dedupeStrings(item.KeyPoints)
	item.ConceptsAffected = dedupeStrings(item.ConceptsAffected)
	item.EntitiesAffected = dedupeStrings(item.EntitiesAffected)
	item.Warnings = dedupeStrings(item.Warnings)
	item.Concepts = filterFAQConceptItems(normalizedConcepts(ingestLLMOutput{Concepts: item.Concepts, ConceptsAffected: item.ConceptsAffected, Summary: item.Summary, KeyPoints: item.KeyPoints}))
	item.Entities = filterFAQEntityItems(normalizedEntities(ingestLLMOutput{Entities: item.Entities, EntitiesAffected: item.EntitiesAffected, Summary: item.Summary, KeyPoints: item.KeyPoints}))
	item.ConceptsAffected = conceptItemTitles(item.Concepts)
	item.EntitiesAffected = entityItemTitles(item.Entities)
	return item
}

func fallbackFAQClassificationBatch(batch canonicalFAQSegment, profile *knowledgeProfile, reason string) faqClassificationBatchOutput {
	groups := map[string]*faqClassificationItem{}
	order := []string{}
	for _, entry := range batch.Entries {
		hint, matched := profile.matchCategory(entry)
		if !matched {
			if rawHint, ok := profile.categoryHintByName(entry.Category); ok {
				hint = rawHint
				matched = true
			}
		}
		if !matched {
			review := profile.reviewCategory()
			hint = review
		}
		slug := stableFAQCategorySlugWithProfile(profile, hint.Title, hint.Title, hint.Slug)
		group := groups[slug]
		if group == nil {
			category := firstNonEmpty(strings.TrimSpace(hint.Title), strings.TrimSpace(entry.Category), "待人工复核 FAQ")
			group = &faqClassificationItem{
				Title:    category,
				Slug:     slug,
				Category: category,
				Summary:  fmt.Sprintf("本分类由 server profile fallback 归组，共包含相关 FAQ。"),
				KeyPoints: []string{
					"LLM 分类不可用时，server 使用当前知识 profile 进行确定性归组。",
					"若本页仍然过大或过泛，应调整 server profile 后重新摄入。",
				},
			}
			if !matched {
				group.Warnings = append(group.Warnings, "存在无法通过 profile 明确分类的 FAQ，已转入待人工复核分类。")
			}
			groups[slug] = group
			order = append(order, slug)
		}
		group.EntryIDs = append(group.EntryIDs, entry.ID)
	}
	warnings := []string{}
	if strings.TrimSpace(reason) != "" {
		warnings = append(warnings, "FAQ 分类已回退到 server profile；原因："+strings.TrimSpace(reason))
		warnings = append(warnings, "FAQ 分类 LLM 不可用，未使用原始粗分类直接生成最终分类。")
	}
	out := faqClassificationBatchOutput{Warnings: warnings, Status: "fallback", Fallbacks: 1}
	for _, slug := range order {
		item := *groups[slug]
		item.EntryIDs = dedupeStrings(item.EntryIDs)
		item.Warnings = dedupeStrings(append(item.Warnings, warnings...))
		out.Categories = append(out.Categories, normalizeFAQClassificationItem(batch, item, profile))
	}
	return out
}

func mergeFAQClassifications(groups map[string]*faqCategoryGroup, order *[]string, batch canonicalFAQSegment, items []faqClassificationItem) {
	entryByID := map[string]canonicalFAQEntry{}
	for _, entry := range batch.Entries {
		entryByID[entry.ID] = entry
	}
	for _, item := range items {
		slug := stableFAQCategorySlugWithProfile(nil, item.Category, item.Title, item.Slug)
		group := groups[slug]
		if group == nil {
			group = &faqCategoryGroup{
				Title:    item.Title,
				Slug:     slug,
				Category: item.Category,
				Summary:  item.Summary,
			}
			groups[slug] = group
			*order = append(*order, slug)
		}
		group.Title = firstNonEmpty(group.Title, item.Title)
		group.Category = firstNonEmpty(group.Category, item.Category)
		group.Summary = firstNonEmpty(group.Summary, item.Summary)
		group.KeyPoints = mergeStringLists(group.KeyPoints, item.KeyPoints)
		group.ConceptsAffected = mergeStringLists(group.ConceptsAffected, item.ConceptsAffected)
		group.EntitiesAffected = mergeStringLists(group.EntitiesAffected, item.EntitiesAffected)
		group.Concepts = mergeConceptItems(group.Concepts, item.Concepts)
		group.Entities = mergeEntityItems(group.Entities, item.Entities)
		group.Warnings = mergeStringLists(group.Warnings, item.Warnings)
		for _, id := range item.EntryIDs {
			if entry, ok := entryByID[id]; ok && !faqGroupHasEntry(group, entry.ID) {
				group.Entries = append(group.Entries, entry)
			}
		}
	}
}

func validateFAQQualityGates(groups map[string]*faqCategoryGroup, order []string, profile *knowledgeProfile) ([]string, error) {
	if profile == nil {
		return nil, nil
	}
	warnings := []string{}
	reviewSlug := faqPageSlug(profile.reviewCategory().Slug)
	for _, slug := range order {
		group := groups[slug]
		if group == nil {
			continue
		}
		entryCount := len(group.Entries)
		if entryCount == 0 {
			continue
		}
		if entryCount > profile.maxFAQEntriesPerCategory() {
			warnings = append(warnings, fmt.Sprintf("FAQ 分类 %s 包含 %d 条，超过 profile 建议上限 %d。", slug, entryCount, profile.maxFAQEntriesPerCategory()))
		}
		isReview := slug == reviewSlug
		isGeneric := profile.isGenericCategory(group.Category) || profile.isGenericCategory(group.Title)
		if profile.QualityGates.BlockLargeGenericCategory && (isReview || isGeneric) && entryCount > profile.QualityGates.MaxUngroupedEntries {
			return warnings, ValidationError{Message: fmt.Sprintf("FAQ 分类质量闸门阻止写入：%s 包含 %d 条未明确分类条目，超过上限 %d。请先调整 server knowledge profile 分类规则后重新摄入。", firstNonEmpty(group.Title, slug), entryCount, profile.QualityGates.MaxUngroupedEntries)}
		}
	}
	return dedupeStrings(warnings), nil
}

func mergeConceptItems(existing []ingestConceptItem, incoming []ingestConceptItem) []ingestConceptItem {
	bySlug := map[string]int{}
	out := append([]ingestConceptItem{}, existing...)
	for i, item := range out {
		bySlug[item.Slug] = i
	}
	for _, item := range incoming {
		if item.Slug == "" {
			continue
		}
		if index, ok := bySlug[item.Slug]; ok {
			out[index].Aliases = mergeStringLists(out[index].Aliases, item.Aliases)
			out[index].KeyPoints = mergeStringLists(out[index].KeyPoints, item.KeyPoints)
			out[index].Contradictions = mergeStringLists(out[index].Contradictions, item.Contradictions)
			out[index].Definition = firstNonEmpty(out[index].Definition, item.Definition)
			continue
		}
		bySlug[item.Slug] = len(out)
		out = append(out, item)
	}
	return out
}

func mergeEntityItems(existing []ingestEntityItem, incoming []ingestEntityItem) []ingestEntityItem {
	bySlug := map[string]int{}
	out := append([]ingestEntityItem{}, existing...)
	for i, item := range out {
		bySlug[item.Slug] = i
	}
	for _, item := range incoming {
		if item.Slug == "" {
			continue
		}
		if index, ok := bySlug[item.Slug]; ok {
			out[index].Aliases = mergeStringLists(out[index].Aliases, item.Aliases)
			out[index].KeyContributions = mergeStringLists(out[index].KeyContributions, item.KeyContributions)
			out[index].Description = firstNonEmpty(out[index].Description, item.Description)
			continue
		}
		bySlug[item.Slug] = len(out)
		out = append(out, item)
	}
	return out
}

func looksLikeIngestFAQAnalysis(raw map[string]any, parsed ingestLLMOutput) bool {
	for _, key := range []string{
		"summary",
		"source_title",
		"source_slug",
		"key_points",
		"concepts_affected",
		"entities_affected",
		"concepts",
		"entities",
		"contradictions",
		"low_risk_fixes",
		"high_risk_proposals",
		"warnings",
		"possibly_outdated",
	} {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return strings.TrimSpace(parsed.Summary) != "" ||
		strings.TrimSpace(parsed.SourceTitle) != "" ||
		strings.TrimSpace(parsed.SourceSlug) != "" ||
		len(parsed.KeyPoints) > 0 ||
		len(parsed.ConceptsAffected) > 0 ||
		len(parsed.EntitiesAffected) > 0 ||
		len(parsed.Concepts) > 0 ||
		len(parsed.Entities) > 0 ||
		len(parsed.Contradictions) > 0 ||
		len(parsed.LowRiskFixes) > 0 ||
		len(parsed.HighRiskProposals) > 0 ||
		len(parsed.Warnings) > 0
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
	sourceLink := fmt.Sprintf("- [[%s]]", sourceSlug)
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
		ops = append(ops, map[string]any{"type": "append_section", "section": "## Evolution Log", "content": fmt.Sprintf("- %s（%d sources）：由 [[%s]] 新增或强化该概念", nowDate(), maxInt(sourceCount, 1), sourceSlug)})
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
	sourceLink := fmt.Sprintf("- [[%s]]", sourceSlug)
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
			items = append(items, fmt.Sprintf("- %s（[[%s]]）", label, item.Slug))
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
			items = append(items, fmt.Sprintf("- %s（[[%s]]）", label, item.Slug))
		}
	}
	if len(items) == 0 {
		return bulletListOrPlaceholder(analysis.EntitiesAffected, "待补充实体提取")
	}
	return strings.Join(items, "\n")
}

func buildFAQPageDocument(frontmatter map[string]any, segment canonicalFAQSegment, analysis ingestLLMOutput, profile *knowledgeProfile) string {
	body := strings.Join([]string{
		"# " + firstNonEmpty(strings.TrimSpace(analysis.SourceTitle), segment.title()),
		"",
		"## Summary",
		"",
		firstNonEmpty(strings.TrimSpace(analysis.Summary), segment.summary()),
		"",
		"## Key Points",
		"",
		bulletListOrPlaceholder(segment.keyPoints(), "待补充 FAQ 关键要点"),
		"",
		"## FAQ Entries",
		"",
		renderFAQEntriesSectionWithProfile(segment.Entries, profile, conceptSlugs(analysis), entitySlugs(analysis), ""),
		"",
		"## Concepts Extracted",
		"",
		renderConceptExtractedSection(analysis),
		"",
		"## Entities Extracted",
		"",
		renderEntityExtractedSection(analysis),
		"",
		"## Contradictions",
		"",
		bulletListOrPlaceholder(analysis.Contradictions, "暂无明确矛盾"),
		"",
		"## Notes",
		"",
		bulletListOrPlaceholder(mergeStringLists(segment.notes(), analysis.Warnings), "FAQ 数据已自动规范化并按业务分类摄入"),
	}, "\n")
	return wikiadapter.RenderDocument(&wikiadapter.Document{Frontmatter: frontmatter, Body: body})
}

func buildFAQCategoryPageDocument(frontmatter map[string]any, group *faqCategoryGroup, analysis ingestLLMOutput, sourceArchivePath string, profile *knowledgeProfile) string {
	relatedConcepts := wikilinkListOrPlaceholder(conceptSlugs(analysis), "暂无关联概念")
	relatedEntities := wikilinkListOrPlaceholder(entitySlugs(analysis), "暂无关联实体")
	body := strings.Join([]string{
		"# " + firstNonEmpty(strings.TrimSpace(analysis.SourceTitle), group.Title),
		"",
		"## Summary",
		"",
		firstNonEmpty(strings.TrimSpace(analysis.Summary), fmt.Sprintf("本页汇总“%s”相关客服 FAQ。", firstNonEmpty(group.Category, group.Title))),
		"",
		"## Key Points",
		"",
		bulletListOrPlaceholder(analysis.KeyPoints, "待补充 FAQ 关键要点"),
		"",
		"## FAQ Entries",
		"",
		renderFAQEntriesSectionWithProfile(group.Entries, profile, conceptSlugs(analysis), entitySlugs(analysis), sourceArchivePath),
		"",
		"## Related Concepts",
		"",
		relatedConcepts,
		"",
		"## Related Entities",
		"",
		relatedEntities,
		"",
		"## Source Archive",
		"",
		fmt.Sprintf("- %s", sourceArchivePath),
		"",
		"## Notes",
		"",
		bulletListOrPlaceholder(group.Warnings, "本页为客服 FAQ 分类页；source 归档页只用于审计，不作为公开问答主证据。"),
	}, "\n")
	return wikiadapter.RenderDocument(&wikiadapter.Document{Frontmatter: frontmatter, Body: body})
}

func buildFAQSourceArchiveDocument(dataset *canonicalFAQDataset, normalizedPath string, shaValue string, sourceSlug string, faqPages []string) string {
	frontmatter := map[string]any{
		"title":           dataset.TitleBase + " FAQ 数据集归档",
		"type":            "faq-source-archive",
		"date":            nowDate(),
		"processed":       true,
		"raw_file":        normalizedPath,
		"raw_sha256":      shaValue,
		"last_verified":   nowDate(),
		"source_format":   dataset.Format,
		"source_family":   dataset.Family,
		"faq_entry_count": len(dataset.Entries),
	}
	lines := []string{
		"# " + dataset.TitleBase + " FAQ 数据集归档",
		"",
		"## Summary",
		"",
		fmt.Sprintf("本页归档 FAQ 数据集“%s”的来源信息，共 %d 条标准问答。客服问答检索以 `wiki/faq/` 分类页为主，本页只用于来源审计。", dataset.TitleBase, len(dataset.Entries)),
		"",
		"## Key Points",
		"",
		fmt.Sprintf("- 原始文件：%s", normalizedPath),
		fmt.Sprintf("- 原始 SHA-256：%s", shaValue),
		fmt.Sprintf("- 规范化格式：%s", dataset.Format),
		fmt.Sprintf("- 标准 FAQ 条目：%d 条", len(dataset.Entries)),
		"",
		"## FAQ Pages",
		"",
	}
	if len(faqPages) == 0 {
		lines = append(lines, "- 待生成 FAQ 分类页")
	} else {
		for _, path := range dedupeStrings(faqPages) {
			slug := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			lines = append(lines, fmt.Sprintf("- [[%s]]", slug))
		}
	}
	lines = append(lines, "", "## Notes", "")
	if len(dataset.Notes) == 0 {
		lines = append(lines, "- 本页为 FAQ 数据集归档，不作为终端客户回答主证据。")
	} else {
		for _, note := range dataset.Notes {
			lines = append(lines, "- "+note)
		}
		lines = append(lines, "- 本页为 FAQ 数据集归档，不作为终端客户回答主证据。")
	}
	_ = sourceSlug
	return wikiadapter.RenderDocument(&wikiadapter.Document{Frontmatter: frontmatter, Body: strings.Join(lines, "\n")})
}

func buildFAQIndexDocument(dataset *canonicalFAQDataset, segments []map[string]any) string {
	frontmatter := map[string]any{
		"title":           "FAQ 索引",
		"type":            "faq-index",
		"source_format":   dataset.Format,
		"source_family":   dataset.Family,
		"raw_file":        dataset.RawPath,
		"faq_entry_count": len(dataset.Entries),
		"faq_page_count":  len(segments),
		"last_verified":   nowDate(),
	}
	lines := []string{
		"# FAQ 索引",
		"",
		"## Summary",
		"",
		fmt.Sprintf("本索引汇总 %s 摄入生成的客服 FAQ 分类页，共 %d 条标准问答，%d 个业务分类页。", firstNonEmpty(dataset.TitleBase, "FAQ 数据集"), len(dataset.Entries), len(segments)),
		"",
		"## FAQ Pages",
		"",
	}
	for _, item := range segments {
		path := strings.TrimSpace(fmt.Sprintf("%v", firstNonNil(item["faq_page"], item["source_page"])))
		slug := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		title := strings.TrimSpace(fmt.Sprintf("%v", item["title"]))
		category := strings.TrimSpace(fmt.Sprintf("%v", item["category"]))
		count := strings.TrimSpace(fmt.Sprintf("%v", item["entry_count"]))
		if slug == "" || slug == "." {
			continue
		}
		lines = append(lines, fmt.Sprintf("- [[%s]]：%s（分类：%s，问答：%s 条）", slug, firstNonEmpty(title, slug), firstNonEmpty(category, "未分类"), firstNonEmpty(count, "0")))
	}
	lines = append(lines, "", "## Notes", "", "- `wiki/faq/` 是客服 FAQ 的主回答层；公开问答优先依据具体 FAQ 条目回答。", "- `wiki/sources/` 中的 FAQ 数据集页只用于归档和审计，不作为客服回答主证据。")
	return wikiadapter.RenderDocument(&wikiadapter.Document{Frontmatter: frontmatter, Body: strings.Join(lines, "\n")})
}

func faqPageSlug(slug string) string {
	slug = strings.TrimSpace(slug)
	if slug == "" || !wikiadapter.IsValidSlug(slug) {
		slug = "faq-page"
	}
	if strings.HasPrefix(slug, "faq-") {
		return slug
	}
	return "faq-" + slug
}

func faqDatasetSourceSlug(dataset *canonicalFAQDataset) string {
	slug := strings.TrimSpace(dataset.SlugBase)
	if slug == "" || !wikiadapter.IsValidSlug(slug) {
		slug = slugFromText(firstNonEmpty(dataset.TitleBase, "faq-dataset"))
	}
	if slug == "" || !wikiadapter.IsValidSlug(slug) {
		slug = "faq-dataset"
	}
	if strings.HasPrefix(slug, "faq-source-") {
		return slug
	}
	return "faq-source-" + strings.TrimPrefix(slug, "faq-")
}

func ensureFAQEntryIDs(dataset *canonicalFAQDataset) {
	if dataset == nil {
		return
	}
	seen := map[string]bool{}
	for i := range dataset.Entries {
		id := strings.TrimSpace(dataset.Entries[i].ID)
		if id == "" || seen[id] {
			id = fmt.Sprintf("faq-%04d", i+1)
		}
		seen[id] = true
		dataset.Entries[i].ID = id
	}
}

func faqEntryIDs(entries []canonicalFAQEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) != "" {
			out = append(out, strings.TrimSpace(entry.ID))
		}
	}
	return dedupeStrings(out)
}

func faqGroupHasEntry(group *faqCategoryGroup, id string) bool {
	if group == nil || strings.TrimSpace(id) == "" {
		return false
	}
	for _, entry := range group.Entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func faqClassificationSlugs(items []faqClassificationItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Slug)
	}
	return dedupeStrings(out)
}

func conceptSlugs(analysis ingestLLMOutput) []string {
	out := make([]string, 0, len(analysis.Concepts))
	for _, item := range analysis.Concepts {
		if item.Slug != "" {
			out = append(out, item.Slug)
		}
	}
	return dedupeStrings(out)
}

func entitySlugs(analysis ingestLLMOutput) []string {
	out := make([]string, 0, len(analysis.Entities))
	for _, item := range analysis.Entities {
		if item.Slug != "" {
			out = append(out, item.Slug)
		}
	}
	return dedupeStrings(out)
}

func wikilinkListOrPlaceholder(slugs []string, placeholder string) string {
	slugs = dedupeStrings(slugs)
	if len(slugs) == 0 {
		return "- " + placeholder
	}
	lines := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		lines = append(lines, fmt.Sprintf("- [[%s]]", slug))
	}
	return strings.Join(lines, "\n")
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
		warnings = append(warnings, "FAQ 分段的 LLM 分析失败，已回退到本地规则分析；FAQ 页仍会写入，但概念和实体提取可能不完整。")
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

func enrichFAQCategoryKnowledgeCandidates(group *faqCategoryGroup, profile *knowledgeProfile) {
	if group == nil {
		return
	}
	concepts, entities := inferFAQKnowledgeCandidates(group.Category, group.Entries, profile)
	group.Concepts = mergeConceptItems(group.Concepts, concepts)
	group.Entities = mergeEntityItems(group.Entities, entities)
	group.ConceptsAffected = mergeStringLists(group.ConceptsAffected, conceptItemTitles(concepts))
	group.EntitiesAffected = mergeStringLists(group.EntitiesAffected, entityItemTitles(entities))
}

func inferFAQKnowledgeCandidates(category string, entries []canonicalFAQEntry, profile *knowledgeProfile) ([]ingestConceptItem, []ingestEntityItem) {
	return profile.inferKnowledgeCandidates(category, entries)
}

func renderFAQClassificationManifest(batch canonicalFAQSegment) (string, int, error) {
	manifest := faqClassificationManifest{
		SourceFormat: batch.Dataset.Format,
		SourceTitle:  batch.Dataset.TitleBase,
		EntryCount:   len(batch.Entries),
		FAQ:          make([]faqClassificationEntryRef, 0, len(batch.Entries)),
	}
	for _, entry := range batch.Entries {
		manifest.FAQ = append(manifest.FAQ, faqClassificationEntryRef{
			ID:               strings.TrimSpace(entry.ID),
			OriginalCategory: strings.TrimSpace(entry.Category),
			Question:         strings.TrimSpace(entry.Question),
			SimilarQuestions: trimStringSlice(entry.SimilarQuestions, 6),
			Keywords:         trimStringSlice(entry.Keywords, 8),
			Tags:             trimStringSlice(entry.Tags, 6),
			QuickReplies:     trimStringSlice(entry.QuickReplies, 4),
			AnswerSummary:    truncateForPrompt(entry.Answer, 260),
		})
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", 0, err
	}
	text := string(raw)
	return text, estimateTextTokens(text), nil
}

func estimateTextTokens(text string) int {
	runes := len([]rune(text))
	if runes == 0 {
		return 0
	}
	tokens := runes / 3
	if tokens == 0 {
		return 1
	}
	return tokens
}

func trimStringSlice(items []string, limit int) []string {
	items = dedupeStrings(items)
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	return items
}

func splitPublicFAQEntries(entries []canonicalFAQEntry) ([]canonicalFAQEntry, []canonicalFAQEntry) {
	publicEntries := make([]canonicalFAQEntry, 0, len(entries))
	preIntent := []canonicalFAQEntry{}
	for _, entry := range entries {
		if isPreIntentFAQEntry(entry) {
			preIntent = append(preIntent, entry)
			continue
		}
		publicEntries = append(publicEntries, entry)
	}
	return publicEntries, preIntent
}

func isPreIntentFAQEntry(entry canonicalFAQEntry) bool {
	text := strings.ToLower(strings.Join([]string{
		entry.Category,
		entry.Question,
		strings.Join(entry.SimilarQuestions, " "),
		strings.Join(entry.Keywords, " "),
		strings.Join(entry.Tags, " "),
		strings.Join(entry.QuickReplies, " "),
		entry.Answer,
	}, " "))
	for _, pattern := range []string{
		"你好", "您好", "在吗", "hello", "hi", "谢谢", "感谢", "没问题了", "不用了", "再见",
		"人工客服", "转人工", "联系人工", "客服热线", "投诉",
		"删除知识库", "删除资料库", "清空知识库", "修改系统", "管理页面", "系统提示词", "prompt", "slug",
	} {
		if strings.Contains(text, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func stableFAQCategorySlug(category string, title string, explicit string) string {
	return stableFAQCategorySlugWithProfile(nil, category, title, explicit)
}

func stableFAQCategorySlugWithProfile(profile *knowledgeProfile, category string, title string, explicit string) string {
	if slug := strings.TrimSpace(explicit); wikiadapter.IsValidSlug(slug) && isUsableFAQSlug(slug) {
		return faqPageSlug(slug)
	}
	if hint, ok := profile.categoryHintByName(firstNonEmpty(category, title)); ok {
		if slug := strings.TrimSpace(hint.Slug); wikiadapter.IsValidSlug(slug) {
			return faqPageSlug(slug)
		}
	}
	key := normalizeFAQCategoryKey(firstNonEmpty(category, title))
	if mapped := faqCategorySlugMap()[key]; mapped != "" {
		return faqPageSlug(mapped)
	}
	if isGenericFAQCategoryName(firstNonEmpty(category, title), profile.genericCategoryNames()) {
		return faqPageSlug(profile.reviewCategory().Slug)
	}
	for _, candidate := range []string{title, category} {
		slug := slugFromText(candidate)
		if wikiadapter.IsValidSlug(slug) && isUsableFAQSlug(slug) {
			return faqPageSlug(slug)
		}
	}
	return "faq-category-" + stableShortHash(firstNonEmpty(category, title, "general"))
}

func isUsableFAQSlug(slug string) bool {
	switch strings.TrimSpace(slug) {
	case "", "output", "page", "faq", "general", "faq-output", "faq-page", "faq-faq", "faq-general":
		return false
	default:
		return true
	}
}

func normalizeFAQCategoryKey(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "faq", "", "FAQ", "", "问题", "")
	return replacer.Replace(text)
}

func faqCategorySlugMap() map[string]string {
	return map[string]string{
		"账号与登录": "account-login",
		"账号登录":  "account-login",
		"登录注册":  "account-login",
		"下载与安装": "download-installation",
		"下载安装":  "download-installation",
		"产品咨询":  "product-consulting",
		"产品":    "product-consulting",
		"售后":    "after-sales",
		"售后服务":  "after-sales",
		"使用配置":  "setup-usage",
		"配置使用":  "setup-usage",
		"技术支持":  "technical-support",
		"故障排查":  "troubleshooting",
		"价格套餐":  "pricing-plans",
		"套餐价格":  "pricing-plans",
		"购买支付":  "purchase-payment",
		"充值续费":  "billing-renewal",
		"退款":    "refund",
		"发票":    "invoice",
	}
}

func stableShortHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])[:10]
}

func filterFAQConceptItems(items []ingestConceptItem) []ingestConceptItem {
	out := make([]ingestConceptItem, 0, len(items))
	for _, item := range items {
		if isNoisyFAQKnowledgeTerm(firstNonEmpty(item.Title, item.Slug, item.EnglishName)) || isNoisyFAQKnowledgeTerm(item.Slug) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterFAQEntityItems(items []ingestEntityItem) []ingestEntityItem {
	out := make([]ingestEntityItem, 0, len(items))
	for _, item := range items {
		if isNoisyFAQKnowledgeTerm(firstNonEmpty(item.Title, item.Slug)) || isNoisyFAQKnowledgeTerm(item.Slug) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func isNoisyFAQKnowledgeTerm(term string) bool {
	normalized := strings.ToLower(strings.TrimSpace(term))
	compact := strings.ReplaceAll(strings.ReplaceAll(normalized, "-", ""), " ", "")
	if compact == "" {
		return true
	}
	if len([]rune(compact)) <= 1 {
		return true
	}
	for _, item := range []string{
		"你好", "您好", "在吗", "hello", "hi", "谢谢", "感谢", "再见", "人工客服", "转人工",
		"faq", "问题", "回复", "答案", "标签", "关键词", "快捷短语", "未分类", "通用问题",
		"error", "错误码", "output", "page",
	} {
		if compact == strings.ToLower(strings.ReplaceAll(item, " ", "")) {
			return true
		}
	}
	return false
}

func filterAlreadyProcessedConcepts(items []ingestConceptItem, seen map[string]bool) []ingestConceptItem {
	out := []ingestConceptItem{}
	for _, item := range items {
		if item.Slug == "" || seen[item.Slug] {
			continue
		}
		seen[item.Slug] = true
		out = append(out, item)
	}
	return out
}

func filterAlreadyProcessedEntities(items []ingestEntityItem, seen map[string]bool) []ingestEntityItem {
	out := []ingestEntityItem{}
	for _, item := range items {
		if item.Slug == "" || seen[item.Slug] {
			continue
		}
		seen[item.Slug] = true
		out = append(out, item)
	}
	return out
}

func conceptItemTitles(items []ingestConceptItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, firstNonEmpty(item.Title, item.Slug))
	}
	return dedupeStrings(out)
}

func entityItemTitles(items []ingestEntityItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, firstNonEmpty(item.Title, item.Slug))
	}
	return dedupeStrings(out)
}

func maxFAQCategoryEntries(groups map[string]*faqCategoryGroup) int {
	maxCount := 0
	for _, group := range groups {
		if group != nil && len(group.Entries) > maxCount {
			maxCount = len(group.Entries)
		}
	}
	return maxCount
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
	return fmt.Sprintf("客户服务知识库分段（%s）", label)
}

func deriveKnowledgeBaseSourceSlug(normalizedPath string, content string) string {
	label := slugFromText(strings.TrimSuffix(filepath.Base(normalizedPath), filepath.Ext(normalizedPath)))
	if label == "" {
		label = "segment"
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
	return fmt.Sprintf(
		"本次 ingest 处理的是%s的一个表格分段（%s），属于多主题客服问答集合，而不是单一主题 FAQ。该分段覆盖%s等分类。\n\n同一分段中既有带“海外IP”限定的问答，也有不带限定词的通用 IP / 登录问答；整理时不应把单条问答里的限定词上升为整页标题或整页结论。",
		"客户服务知识库",
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

func faqPagePathsFromArtifacts(items []report.Artifact) []string {
	out := []string{}
	for _, item := range items {
		if item.Kind != "faq_page" {
			continue
		}
		out = append(out, item.Path)
	}
	return dedupeStrings(out)
}
