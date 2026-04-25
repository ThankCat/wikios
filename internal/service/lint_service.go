package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"wikios/internal/wikiadapter"
)

type LintRequest struct {
	WriteReport    bool `json:"write_report"`
	AutoFixLowRisk bool `json:"auto_fix_low_risk"`
}

type LintService struct {
	baseService
}

func NewLintService(deps Deps) *LintService {
	return &LintService{baseService: newBaseService(deps)}
}

func (s *LintService) Run(ctx context.Context, execution *Execution, traceID string, req LintRequest) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	lintResult, err := s.executeTool(ctx, execution, env, "lint.run", nil, "run lint")
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"summary":     summarizeLintResult(lintResult.Data),
		"report_file": lintResult.Data["report_path"],
	}
	health := auditWikiHealth(s.deps.Config.MountedWiki.Root)
	result["wiki_health"] = health
	result["reply"] = formatWikiHealthReply(health, result["report_file"])
	statusResult, statusErr := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{
		"subcommand": "status",
	}, "qmd status")
	if statusResult.Data != nil {
		if stdout := strings.TrimSpace(toolString(statusResult.Data, "stdout")); stdout != "" {
			result["qmd_status"] = stdout
		}
		if stderr := strings.TrimSpace(toolString(statusResult.Data, "stderr")); stderr != "" {
			result["qmd_stderr"] = stderr
		}
		result["qmd_exit_code"] = statusResult.Data["exit_code"]
	}
	if statusErr != nil {
		result["qmd_error"] = statusErr.Error()
		result["summary"] = fmt.Sprintf("%s；QMD 状态检查失败", result["summary"])
	} else {
		updateResult, updateErr := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{
			"subcommand": "update",
		}, "qmd update")
		if updateResult.Data != nil {
			if stdout := strings.TrimSpace(toolString(updateResult.Data, "stdout")); stdout != "" {
				result["qmd_update"] = stdout
			}
			if stderr := strings.TrimSpace(toolString(updateResult.Data, "stderr")); stderr != "" {
				result["qmd_update_stderr"] = stderr
			}
			result["qmd_update_exit_code"] = updateResult.Data["exit_code"]
		}
		if updateErr != nil {
			result["qmd_update_error"] = updateErr.Error()
			result["summary"] = fmt.Sprintf("%s；QMD 更新失败", result["summary"])
		} else {
			result["qmd_updated"] = true
		}
	}
	if reply := strings.TrimSpace(fmt.Sprintf("%v", result["reply"])); reply != "" {
		result["summary"] = firstNonEmpty(health.Summary, fmt.Sprintf("%v", result["summary"]))
	}
	return result, nil
}

type wikiHealthAudit struct {
	Summary                 string              `json:"summary"`
	FAQSegmentFiles         int                 `json:"faq_segment_files"`
	FAQPageFiles            int                 `json:"faq_page_files"`
	FAQEntryCount           int                 `json:"faq_entry_count"`
	FAQIndexExists          bool                `json:"faq_index_exists"`
	ActiveFAQPrefix         string              `json:"active_faq_prefix"`
	LegacyFAQSourceFiles    []string            `json:"legacy_faq_source_files"`
	MissingFAQBacklinks     []string            `json:"missing_faq_backlinks"`
	MissingSourceReferences []string            `json:"missing_source_references"`
	RepeatedQuestions       map[string][]string `json:"repeated_questions"`
	TempFiles               []string            `json:"temp_files"`
	LowRiskIssues           []string            `json:"low_risk_issues"`
	Notes                   []string            `json:"notes"`
}

var faqSegmentFilePattern = regexp.MustCompile(`^(faq-.+)-segment-\d+\.md$`)
var faqQuestionHeadingPattern = regexp.MustCompile(`(?m)^###\s+(.+?)\s*$`)
var sourceWikiLinkPattern = regexp.MustCompile(`\[\[sources/([a-z0-9]+(?:-[a-z0-9]+)*)`)

func auditWikiHealth(root string) wikiHealthAudit {
	wikiRoot := filepath.Join(root, "wiki")
	sourcesDir := filepath.Join(wikiRoot, "sources")
	faqDir := filepath.Join(wikiRoot, "faq")
	faqFiles, faqIndexExists := collectFAQPageFiles(faqDir)
	legacySources := collectLegacyFAQSourceFiles(sourcesDir)
	sourceSet := map[string]bool{}
	for _, rel := range legacySources {
		sourceSet[strings.TrimSuffix(filepath.Base(rel), ".md")] = true
	}
	entryCount := 0
	repeated := map[string][]string{}
	questionLocations := map[string][]string{}
	for _, rel := range faqFiles {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for _, match := range faqQuestionHeadingPattern.FindAllStringSubmatch(string(body), -1) {
			question := strings.TrimSpace(match[1])
			if question == "" {
				continue
			}
			entryCount++
			questionLocations[question] = append(questionLocations[question], rel)
		}
	}
	for question, locations := range questionLocations {
		if len(locations) > 1 {
			repeated[question] = locations
		}
	}
	missingRefs := collectMissingSourceRefs(root, wikiRoot, sourceSet)
	missingFAQBacklinks := collectMissingFAQBacklinks(root, faqFiles)
	tempFiles := collectTempFiles(wikiRoot)
	issues := []string{}
	if !faqIndexExists && len(faqFiles) > 0 {
		issues = append(issues, "wiki/faq/index.md 缺失")
	}
	if len(legacySources) > 0 {
		issues = append(issues, fmt.Sprintf("存在 %d 个旧版 FAQ source 文件", len(legacySources)))
	}
	if len(missingRefs) > 0 {
		issues = append(issues, fmt.Sprintf("存在 %d 条指向缺失 source 的引用", len(missingRefs)))
	}
	if len(missingFAQBacklinks) > 0 {
		issues = append(issues, fmt.Sprintf("存在 %d 条 FAQ 与概念/实体的反向链接缺失", len(missingFAQBacklinks)))
	}
	if len(tempFiles) > 0 {
		issues = append(issues, fmt.Sprintf("存在 %d 个临时/备份文件", len(tempFiles)))
	}
	summary := fmt.Sprintf("健康检查完成：FAQ 页面 %d 个，FAQ 条目 %d 条", len(faqFiles), entryCount)
	if len(issues) > 0 {
		summary += fmt.Sprintf("；发现 %d 类低风险问题", len(issues))
	} else {
		summary += "；未发现结构性问题"
	}
	notes := []string{}
	if len(repeated) > 0 {
		notes = append(notes, "重复问题标题按源数据提示处理，不等同于结构损坏；如需合并问候类话术，应回到原始 FAQ 数据治理。")
	}
	notes = append(notes, "wiki/config.yaml、wiki/schema.yaml、wiki/rules.json 不是当前直连模式的必需文件，不作为健康问题。")
	return wikiHealthAudit{
		Summary:                 summary,
		FAQSegmentFiles:         len(faqFiles),
		FAQPageFiles:            len(faqFiles),
		FAQEntryCount:           entryCount,
		FAQIndexExists:          faqIndexExists,
		ActiveFAQPrefix:         "",
		LegacyFAQSourceFiles:    legacySources,
		MissingFAQBacklinks:     missingFAQBacklinks,
		MissingSourceReferences: missingRefs,
		RepeatedQuestions:       repeated,
		TempFiles:               tempFiles,
		LowRiskIssues:           issues,
		Notes:                   notes,
	}
}

func collectFAQPageFiles(faqDir string) ([]string, bool) {
	entries, err := os.ReadDir(faqDir)
	if err != nil {
		return nil, false
	}
	files := []string{}
	indexExists := false
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("wiki/faq", entry.Name()))
		if entry.Name() == "index.md" {
			indexExists = true
			continue
		}
		files = append(files, rel)
	}
	sort.Strings(files)
	return files, indexExists
}

func collectLegacyFAQSourceFiles(sourcesDir string) []string {
	entries, err := os.ReadDir(sourcesDir)
	if err != nil {
		return nil
	}
	files := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		match := faqSegmentFilePattern.FindStringSubmatch(name)
		if len(match) != 2 {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("wiki/sources", name))
		files = append(files, rel)
	}
	sort.Strings(files)
	return files
}

func collectMissingSourceRefs(root string, wikiRoot string, sourceSet map[string]bool) []string {
	missing := []string{}
	_ = filepath.WalkDir(wikiRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		seen := map[string]bool{}
		for _, match := range sourceWikiLinkPattern.FindAllStringSubmatch(string(body), -1) {
			if len(match) != 2 || sourceSet[match[1]] || seen[match[1]] {
				continue
			}
			seen[match[1]] = true
			missing = append(missing, rel+" -> sources/"+match[1])
		}
		return nil
	})
	sort.Strings(missing)
	return missing
}

func collectMissingFAQBacklinks(root string, faqFiles []string) []string {
	missing := []string{}
	for _, faqRel := range faqFiles {
		absFAQ := filepath.Join(root, filepath.FromSlash(faqRel))
		body, err := os.ReadFile(absFAQ)
		if err != nil {
			continue
		}
		faqSlug := strings.TrimSuffix(filepath.Base(faqRel), filepath.Ext(faqRel))
		doc, err := parseFrontmatterForLint(string(body))
		if err != nil {
			continue
		}
		for _, slug := range append(stringSliceFromLintFrontmatter(doc["related_concepts"]), stringSliceFromLintFrontmatter(doc["related_entities"])...) {
			if slug == "" {
				continue
			}
			targetRel := ""
			for _, candidate := range []string{
				filepath.ToSlash(filepath.Join("wiki/concepts", slug+".md")),
				filepath.ToSlash(filepath.Join("wiki/entities", slug+".md")),
			} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); err == nil {
					targetRel = candidate
					break
				}
			}
			if targetRel == "" {
				missing = append(missing, fmt.Sprintf("%s -> [[%s]] target missing", faqRel, slug))
				continue
			}
			targetBody, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(targetRel)))
			if err != nil {
				continue
			}
			if !strings.Contains(string(targetBody), "[["+faqSlug+"]]") {
				missing = append(missing, fmt.Sprintf("%s -> %s missing [[%s]]", faqRel, targetRel, faqSlug))
			}
		}
	}
	sort.Strings(missing)
	return missing
}

func parseFrontmatterForLint(content string) (map[string]any, error) {
	doc, err := wikiadapter.ParseDocument(content)
	if err != nil {
		return nil, err
	}
	return doc.Frontmatter, nil
}

func stringSliceFromLintFrontmatter(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprintf("%v", item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return typed
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	default:
		return nil
	}
}

func collectTempFiles(wikiRoot string) []string {
	temp := []string{}
	_ = filepath.WalkDir(wikiRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".bak") || strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, "~") {
			rel, _ := filepath.Rel(filepath.Dir(wikiRoot), path)
			temp = append(temp, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(temp)
	return temp
}

func formatWikiHealthReply(health wikiHealthAudit, reportFile any) string {
	lines := []string{"✅ " + health.Summary, ""}
	lines = append(lines, "检查结果：")
	lines = append(lines, fmt.Sprintf("- FAQ 页面：%d 个", health.FAQPageFiles))
	lines = append(lines, fmt.Sprintf("- FAQ 索引：%s", boolStatus(health.FAQIndexExists)))
	lines = append(lines, fmt.Sprintf("- FAQ 条目：%d 条", health.FAQEntryCount))
	if len(health.LegacyFAQSourceFiles) > 0 {
		lines = append(lines, fmt.Sprintf("- 旧版 FAQ source：%d 个，仅提示，不作为活跃 FAQ", len(health.LegacyFAQSourceFiles)))
	}
	if len(health.MissingSourceReferences) > 0 {
		lines = append(lines, fmt.Sprintf("- 缺失 source 引用：%d 条，可执行修复自动删除这些失效链接", len(health.MissingSourceReferences)))
	}
	if len(health.MissingFAQBacklinks) > 0 {
		lines = append(lines, fmt.Sprintf("- FAQ 双向链接缺失：%d 条，需按 AGENT 的 repair 规则修复", len(health.MissingFAQBacklinks)))
	}
	if len(health.RepeatedQuestions) > 0 {
		lines = append(lines, fmt.Sprintf("- 重复问题标题：%d 类，按源数据提示记录，不作为结构损坏", len(health.RepeatedQuestions)))
	}
	if len(health.TempFiles) == 0 {
		lines = append(lines, "- 临时/备份文件：无")
	}
	if report := strings.TrimSpace(fmt.Sprintf("%v", reportFile)); report != "" && report != "<nil>" {
		lines = append(lines, "- lint 报告："+report)
	}
	lines = append(lines, "- QMD：server 按 AGENT.md 的 LINT 目标执行 status/update，避免由 LLM 猜测 qmd 子命令")
	if len(health.Notes) > 0 {
		lines = append(lines, "", "说明：")
		for _, note := range health.Notes {
			lines = append(lines, "- "+note)
		}
	}
	return strings.Join(lines, "\n")
}

func boolStatus(ok bool) string {
	if ok {
		return "存在"
	}
	return "缺失"
}

var lintSummaryPattern = regexp.MustCompile(`Checked\s+(\d+)\s+markdown files;\s+found\s+(\d+)\s+issues`)

func summarizeLintResult(data map[string]any) string {
	stdout := toolString(data, "stdout")
	if match := lintSummaryPattern.FindStringSubmatch(stdout); len(match) == 3 {
		return fmt.Sprintf("健康检查完成：检查了 %s 个 Markdown 文件，发现 %s 个问题", match[1], match[2])
	}
	reportPath := strings.TrimSpace(toolString(data, "report_path"))
	if reportPath != "" {
		return fmt.Sprintf("健康检查完成，报告已写入 %s", reportPath)
	}
	return "健康检查完成"
}

func toolString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}
