package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"wikios/internal/llm"
	"wikios/internal/report"
	"wikios/internal/runtime"
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

func NewIngestService(deps Deps) *IngestService {
	return &IngestService{baseService: newBaseService(deps)}
}

func (s *IngestService) Run(ctx context.Context, taskModel *task.Task, traceID string, req IngestRequest) (map[string]any, error) {
	env := s.env("admin", traceID, taskModel.ID, taskModel.ID)
	normalizedPath, err := s.normalizeMountedInputPath(req.Path)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(normalizedPath, "raw/") {
		return nil, fmt.Errorf("ingest path must be under raw/, got %s", normalizedPath)
	}
	if _, err := s.executeTool(ctx, taskModel, env, "workspace.create_job_dir", map[string]any{"job_id": taskModel.ID}, "create job dir"); err != nil {
		return nil, err
	}
	readResult, err := s.executeTool(ctx, taskModel, env, "fs.read_file", map[string]any{"path": normalizedPath}, "read raw")
	if err != nil {
		return nil, err
	}
	hashResult, err := s.executeTool(ctx, taskModel, env, "hash.sha256", map[string]any{"path": normalizedPath}, "hash raw")
	if err != nil {
		return nil, err
	}
	content, _ := readResult.Data["content"].(string)
	title := extractTitle(content, filepath.Base(normalizedPath))
	analysis, err := s.analyzeIngestContent(ctx, normalizedPath, req.Interactive, content, fmt.Sprintf("%v", hashResult.Data["sha256"]))
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
		"raw_sha256":        hashResult.Data["sha256"],
		"last_verified":     nowDate(),
		"possibly_outdated": analysis.PossiblyOutdated,
	}
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.create_from_template", map[string]any{
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
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.patch_page", map[string]any{
		"path": target,
		"ops":  toAnySlice(ops),
	}, "patch source page"); err != nil {
		return nil, err
	}

	updatedPages := []string{"wiki/index.md", "wiki/log.md"}
	createdPages := []string{target}
	conceptChanges, entityChanges, pageArtifacts, err := s.upsertKnowledgePages(ctx, taskModel, env, slug, analysis)
	if err != nil {
		return nil, err
	}
	createdPages = append(createdPages, conceptChanges.Created...)
	createdPages = append(createdPages, entityChanges.Created...)
	updatedPages = append(updatedPages, conceptChanges.Updated...)
	updatedPages = append(updatedPages, entityChanges.Updated...)

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
	reportPath := "wiki/outputs/ingest-report-" + nowDate() + "-" + slug + ".md"
	reportDoc := buildReportDocument("Ingest Report", "ingest", taskModel.ID, reportMarkdown)
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.write_output", map[string]any{"path": reportPath, "content": reportDoc}, "write ingest report"); err != nil {
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

func (s *IngestService) analyzeIngestContent(ctx context.Context, normalizedPath string, interactive bool, content string, sha256 string) (ingestLLMOutput, error) {
	prompt, err := s.loadPromptWithWikiAgent("admin_ingest_system.md")
	if err != nil {
		return ingestLLMOutput{}, err
	}
	prompt += "\n\n你必须只返回一个 JSON 对象，不要输出代码块。"
	userPrompt := fmt.Sprintf("input_type=file\ninteractive=%t\nraw_path=%s\nraw_sha256=%s\n\nraw_content:\n%s", interactive, normalizedPath, sha256, truncateForPrompt(content, 6000))
	llmText, err := s.executeLLM(ctx, nil, s.deps.Config.LLM.ModelAdmin, []llm.Message{
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

func (s *IngestService) upsertKnowledgePages(ctx context.Context, taskModel *task.Task, env *runtime.ExecEnv, sourceSlug string, analysis ingestLLMOutput) (pageChangeSet, pageChangeSet, []report.Artifact, error) {
	concepts, conceptArtifacts, err := s.upsertConceptPages(ctx, taskModel, env, analysis.Concepts, sourceSlug)
	if err != nil {
		return pageChangeSet{}, pageChangeSet{}, nil, err
	}
	entities, entityArtifacts, err := s.upsertEntityPages(ctx, taskModel, env, analysis.Entities, sourceSlug)
	if err != nil {
		return pageChangeSet{}, pageChangeSet{}, nil, err
	}
	return concepts, entities, append(conceptArtifacts, entityArtifacts...), nil
}

func (s *IngestService) upsertConceptPages(ctx context.Context, taskModel *task.Task, env *runtime.ExecEnv, items []ingestConceptItem, sourceSlug string) (pageChangeSet, []report.Artifact, error) {
	changes := pageChangeSet{}
	artifacts := []report.Artifact{}
	for _, item := range items {
		if item.Slug == "" {
			continue
		}
		path, created, err := s.resolveOrCreateConceptPage(ctx, taskModel, env, item, sourceSlug)
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

func (s *IngestService) upsertEntityPages(ctx context.Context, taskModel *task.Task, env *runtime.ExecEnv, items []ingestEntityItem, sourceSlug string) (pageChangeSet, []report.Artifact, error) {
	changes := pageChangeSet{}
	artifacts := []report.Artifact{}
	for _, item := range items {
		if item.Slug == "" {
			continue
		}
		path, created, err := s.resolveOrCreateEntityPage(ctx, taskModel, env, item, sourceSlug)
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

func (s *IngestService) resolveOrCreateConceptPage(ctx context.Context, taskModel *task.Task, env *runtime.ExecEnv, item ingestConceptItem, sourceSlug string) (string, bool, error) {
	slugResult, err := s.executeTool(ctx, taskModel, env, "wiki.find_by_slug", map[string]any{"slug": item.Slug}, "find concept by slug "+item.Slug)
	if err != nil {
		return "", false, err
	}
	if path, _ := slugResult.Data["path"].(string); strings.HasPrefix(path, "wiki/concepts/") {
		return s.patchConceptPage(ctx, taskModel, env, path, item, sourceSlug, false)
	}
	for _, alias := range conceptAliasCandidates(item) {
		aliasResult, err := s.executeTool(ctx, taskModel, env, "wiki.find_by_alias", map[string]any{"alias": alias}, "find concept by alias "+alias)
		if err != nil {
			return "", false, err
		}
		for _, path := range stringifyAnySlice(aliasResult.Data["matches"]) {
			if strings.HasPrefix(path, "wiki/concepts/") {
				return s.patchConceptPage(ctx, taskModel, env, path, item, sourceSlug, false)
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
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.create_from_template", map[string]any{
		"template_path": "wiki/templates/concept-template.md",
		"target_path":   path,
		"frontmatter":   frontmatter,
	}, "create concept page "+path); err != nil {
		return "", false, err
	}
	return s.patchConceptPage(ctx, taskModel, env, path, item, sourceSlug, true)
}

func (s *IngestService) patchConceptPage(ctx context.Context, taskModel *task.Task, env *runtime.ExecEnv, path string, item ingestConceptItem, sourceSlug string, created bool) (string, bool, error) {
	sourceLink := fmt.Sprintf("- [[sources/%s]]", sourceSlug)
	readResult, err := s.executeTool(ctx, taskModel, env, "wiki.read_page", map[string]any{"path": path}, "read concept page "+path)
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
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.patch_page", map[string]any{
		"path": path,
		"ops":  toAnySlice(ops),
	}, "patch concept page "+path); err != nil {
		return "", false, err
	}
	return path, created, nil
}

func (s *IngestService) resolveOrCreateEntityPage(ctx context.Context, taskModel *task.Task, env *runtime.ExecEnv, item ingestEntityItem, sourceSlug string) (string, bool, error) {
	slugResult, err := s.executeTool(ctx, taskModel, env, "wiki.find_by_slug", map[string]any{"slug": item.Slug}, "find entity by slug "+item.Slug)
	if err != nil {
		return "", false, err
	}
	if path, _ := slugResult.Data["path"].(string); strings.HasPrefix(path, "wiki/entities/") {
		return s.patchEntityPage(ctx, taskModel, env, path, item, sourceSlug, false)
	}
	for _, alias := range entityAliasCandidates(item) {
		aliasResult, err := s.executeTool(ctx, taskModel, env, "wiki.find_by_alias", map[string]any{"alias": alias}, "find entity by alias "+alias)
		if err != nil {
			return "", false, err
		}
		for _, path := range stringifyAnySlice(aliasResult.Data["matches"]) {
			if strings.HasPrefix(path, "wiki/entities/") {
				return s.patchEntityPage(ctx, taskModel, env, path, item, sourceSlug, false)
			}
		}
	}
	path := "wiki/entities/" + item.Slug + ".md"
	frontmatter := map[string]any{
		"title":       item.Title,
		"aliases":     toAnyStrings(entityAliasCandidates(item)),
		"entity_type": firstNonEmpty(item.EntityType, "other"),
	}
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.create_from_template", map[string]any{
		"template_path": "wiki/templates/entity-template.md",
		"target_path":   path,
		"frontmatter":   frontmatter,
	}, "create entity page "+path); err != nil {
		return "", false, err
	}
	return s.patchEntityPage(ctx, taskModel, env, path, item, sourceSlug, true)
}

func (s *IngestService) patchEntityPage(ctx context.Context, taskModel *task.Task, env *runtime.ExecEnv, path string, item ingestEntityItem, sourceSlug string, created bool) (string, bool, error) {
	sourceLink := fmt.Sprintf("- [[sources/%s]]", sourceSlug)
	readResult, err := s.executeTool(ctx, taskModel, env, "wiki.read_page", map[string]any{"path": path}, "read entity page "+path)
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
	if _, err := s.executeTool(ctx, taskModel, env, "wiki.patch_page", map[string]any{
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
