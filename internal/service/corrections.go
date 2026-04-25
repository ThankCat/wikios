package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"wikios/internal/llm"
	"wikios/internal/runtime"
	"wikios/internal/wikiadapter"
)

var plainWikiLinkPattern = regexp.MustCompile(`\[\[([a-z0-9]+(?:-[a-z0-9]+)*)\]\]`)

type correctionDetectionOutput struct {
	Summary     string                 `json:"summary"`
	Corrections []correctionSuggestion `json:"corrections"`
	Warnings    []string               `json:"warnings"`
}

type correctionSuggestion struct {
	Path        string   `json:"path"`
	Section     string   `json:"section"`
	Wrong       string   `json:"wrong"`
	Correct     string   `json:"correct"`
	Reason      string   `json:"reason"`
	RiskLevel   string   `json:"risk_level"`
	ReplaceMode string   `json:"replace_mode"`
	ScopePaths  []string `json:"scope_paths"`
}

type generatedCorrectionFix struct {
	Path    string `json:"path"`
	Ops     []any  `json:"ops"`
	Wrong   string `json:"wrong"`
	Correct string `json:"correct"`
	Reason  string `json:"reason"`
}

type correctionSourceCandidate struct {
	SourcePath  string
	SourceSlug  string
	RawPath     string
	RawContent  string
	TargetPages []correctionTargetPage
}

type correctionTargetPage struct {
	Path    string
	Content string
}

type bodySection struct {
	Heading string
	Content string
}

func (s *baseService) detectBackedCorrections(ctx context.Context, execution *Execution, env *runtime.ExecEnv, topic string) (correctionDetectionOutput, error) {
	candidates, err := s.collectCorrectionCandidates(ctx, execution, env, topic)
	if err != nil {
		return correctionDetectionOutput{}, err
	}
	if len(candidates) == 0 {
		return correctionDetectionOutput{}, nil
	}
	prompt, err := s.loadPromptWithWikiSections("admin_repair_system.md", "REPAIR 相关规则", "## REPAIR 操作规范")
	if err != nil {
		return correctionDetectionOutput{}, err
	}
	prompt += "\n\n你现在只负责把 server 提供的候选材料整理为 REPAIR JSON 输出。具体修复边界与可执行范围以 AGENT.md 为准。你必须只返回 JSON，不要输出代码块。"
	userPrompt := buildCorrectionPrompt(topic, candidates)
	llmText, err := s.executeLLM(ctx, execution, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: userPrompt},
	}, "llm detect corrections")
	if err != nil {
		return correctionDetectionOutput{}, err
	}
	parsed := correctionDetectionOutput{}
	if err := llm.DecodeJSONObject(llmText, &parsed); err != nil {
		return correctionDetectionOutput{
			Warnings: []string{"纠错分析输出未通过 JSON 解析，本轮未生成自动修复"},
		}, nil
	}
	parsed.Corrections = normalizeCorrectionSuggestions(parsed.Corrections)
	parsed.Warnings = dedupeStrings(parsed.Warnings)
	return parsed, nil
}

func (s *baseService) collectCorrectionCandidates(ctx context.Context, execution *Execution, env *runtime.ExecEnv, topic string) ([]correctionSourceCandidate, error) {
	pages, err := s.deps.Retriever.Retrieve(ctx, env, topic, s.deps.Config.Retrieval.TopK)
	if err != nil {
		return nil, nil
	}
	sourceSlugs := []string{}
	pageCache := map[string]string{}
	for _, page := range pages {
		readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": page.Path}, "verify read "+page.Path)
		if err != nil {
			continue
		}
		content, _ := readResult.Data["content"].(string)
		pageCache[page.Path] = content
		if strings.HasPrefix(page.Path, "wiki/sources/") {
			sourceSlugs = append(sourceSlugs, strings.TrimSuffix(filepath.Base(page.Path), ".md"))
		}
		sourceSlugs = append(sourceSlugs, extractSourceLinks(content)...)
	}
	sourceSlugs = dedupeStrings(sourceSlugs)
	if len(sourceSlugs) == 0 {
		sourceSlugs = s.collectSourceSlugsWithRawFile()
	}
	out := make([]correctionSourceCandidate, 0, len(sourceSlugs))
	for _, slug := range sourceSlugs {
		sourcePath := "wiki/sources/" + slug + ".md"
		sourceContent, ok := pageCache[sourcePath]
		if !ok {
			readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": sourcePath}, "verify read "+sourcePath)
			if err != nil {
				continue
			}
			sourceContent, _ = readResult.Data["content"].(string)
		}
		doc, err := wikiadapter.ParseDocument(sourceContent)
		if err != nil {
			continue
		}
		rawPath, _ := doc.Frontmatter["raw_file"].(string)
		rawPath = strings.TrimSpace(rawPath)
		if rawPath == "" {
			continue
		}
		rawResult, err := s.executeTool(ctx, execution, env, "fs.read_file", map[string]any{"path": rawPath}, "verify raw "+rawPath)
		if err != nil {
			continue
		}
		rawContent, _ := rawResult.Data["content"].(string)
		targetPages, err := s.collectPagesReferencingSource(ctx, execution, env, slug)
		if err != nil {
			return nil, err
		}
		if len(targetPages) == 0 {
			targetPages = []correctionTargetPage{{Path: sourcePath, Content: sourceContent}}
		}
		out = append(out, correctionSourceCandidate{
			SourcePath:  sourcePath,
			SourceSlug:  slug,
			RawPath:     rawPath,
			RawContent:  rawContent,
			TargetPages: targetPages,
		})
	}
	return out, nil
}

func (s *baseService) collectSourceSlugsWithRawFile() []string {
	root := filepath.Join(s.deps.Config.MountedWiki.Root, "wiki", "sources")
	slugs := []string{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		doc, err := wikiadapter.ParseDocument(string(content))
		if err != nil {
			return nil
		}
		rawPath, _ := doc.Frontmatter["raw_file"].(string)
		if strings.TrimSpace(rawPath) == "" {
			return nil
		}
		slugs = append(slugs, strings.TrimSuffix(info.Name(), ".md"))
		return nil
	})
	return dedupeStrings(slugs)
}

func (s *baseService) collectPagesReferencingSource(ctx context.Context, execution *Execution, env *runtime.ExecEnv, sourceSlug string) ([]correctionTargetPage, error) {
	root := filepath.Join(s.deps.Config.MountedWiki.Root, "wiki")
	targets := []correctionTargetPage{}
	seen := map[string]bool{}
	sourcePath := "wiki/sources/" + sourceSlug + ".md"
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		rel, err := filepath.Rel(s.deps.Config.MountedWiki.Root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !isCorrectionTargetPath(rel) || seen[rel] {
			return nil
		}
		readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": rel}, "verify scan "+rel)
		if err != nil {
			return nil
		}
		content, _ := readResult.Data["content"].(string)
		if rel == sourcePath || strings.Contains(content, "[[sources/"+sourceSlug+"]]") || strings.Contains(content, "[["+sourceSlug+"]]") {
			seen[rel] = true
			targets = append(targets, correctionTargetPage{Path: rel, Content: content})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return targets, nil
}

func (s *baseService) buildCorrectionFixes(ctx context.Context, execution *Execution, env *runtime.ExecEnv, suggestions []correctionSuggestion) ([]generatedCorrectionFix, []string, error) {
	fixes := []generatedCorrectionFix{}
	warnings := []string{}
	seen := map[string]bool{}
	for _, suggestion := range suggestions {
		if strings.ToLower(strings.TrimSpace(suggestion.RiskLevel)) == "high" {
			warnings = append(warnings, fmt.Sprintf("跳过高风险纠错 %s: %s -> %s", suggestion.Path, suggestion.Wrong, suggestion.Correct))
			continue
		}
		targetPaths, err := s.resolveCorrectionTargets(suggestion)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("无法解析纠错范围 %s: %s", suggestion.Path, err.Error()))
			continue
		}
		matchedCount := 0
		for _, targetPath := range targetPaths {
			key := targetPath + "\x00" + suggestion.Wrong + "\x00" + suggestion.Correct + "\x00" + suggestion.ReplaceMode
			if seen[key] {
				continue
			}
			seen[key] = true
			readResult, err := s.executeTool(ctx, execution, env, "wiki.read_page", map[string]any{"path": targetPath}, "prepare correction "+targetPath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("无法读取待修复页面 %s", targetPath))
				continue
			}
			content, _ := readResult.Data["content"].(string)
			ops, err := planCorrectionOps(content, suggestion)
			if err != nil || len(ops) == 0 {
				if suggestion.ReplaceMode != "global" {
					warnings = append(warnings, fmt.Sprintf("无法生成修复 patch %s: %s", targetPath, firstNonEmpty(errString(err), suggestion.Reason)))
				}
				continue
			}
			matchedCount++
			fixes = append(fixes, generatedCorrectionFix{
				Path:    targetPath,
				Ops:     ops,
				Wrong:   suggestion.Wrong,
				Correct: suggestion.Correct,
				Reason:  suggestion.Reason,
			})
		}
		if suggestion.ReplaceMode == "global" && matchedCount == 0 {
			warnings = append(warnings, fmt.Sprintf("全局纠错未命中任何文件: %s -> %s | scope=%s", suggestion.Wrong, suggestion.Correct, joinOrNone(suggestion.ScopePaths)))
		}
	}
	return fixes, dedupeStrings(warnings), nil
}

func (s *baseService) applyCorrectionFixes(ctx context.Context, execution *Execution, env *runtime.ExecEnv, fixes []generatedCorrectionFix) ([]string, error) {
	applied := []string{}
	for _, fix := range fixes {
		if _, err := s.executeTool(ctx, execution, env, "repair.apply_low_risk", map[string]any{
			"path": fix.Path,
			"ops":  fix.Ops,
		}, "apply correction "+fix.Path); err != nil {
			return applied, err
		}
		applied = append(applied, fix.Path)
	}
	return dedupeStrings(applied), nil
}

func buildCorrectionPrompt(topic string, candidates []correctionSourceCandidate) string {
	blocks := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		targets := make([]string, 0, len(candidate.TargetPages))
		for _, page := range candidate.TargetPages {
			targets = append(targets, fmt.Sprintf("### %s\n%s", page.Path, truncateForPrompt(page.Content, 2200)))
		}
		blocks = append(blocks, fmt.Sprintf(
			"## Source %s\nraw_file: %s\n\n### RAW\n%s\n\n### Derived Wiki Pages\n%s",
			candidate.SourcePath,
			candidate.RawPath,
			truncateForPrompt(candidate.RawContent, 2600),
			strings.Join(targets, "\n\n"),
		))
	}
	return fmt.Sprintf(
		"topic: %s\n\nmounted wiki 的 AGENT.md 是 REPAIR 规则的唯一来源。请按 AGENT.md 判断候选材料，并只输出 server 可解析 JSON：{\"summary\":\"\",\"corrections\":[{\"path\":\"\",\"section\":\"\",\"wrong\":\"\",\"correct\":\"\",\"reason\":\"\",\"risk_level\":\"\",\"replace_mode\":\"\",\"scope_paths\":[]}],\"warnings\":[]}\n\n%s",
		topic,
		strings.Join(blocks, "\n\n"),
	)
}

func normalizeCorrectionScopes(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimSpace(path))
		path = strings.TrimSuffix(path, "/")
		if path == "" || !strings.HasPrefix(path, "wiki/") {
			continue
		}
		out = append(out, path)
	}
	return dedupeStrings(out)
}

func (s *baseService) resolveCorrectionTargets(suggestion correctionSuggestion) ([]string, error) {
	if suggestion.ReplaceMode != "global" || len(suggestion.ScopePaths) == 0 {
		if suggestion.Path == "" {
			return nil, fmt.Errorf("path is required for targeted correction")
		}
		return []string{suggestion.Path}, nil
	}
	paths := []string{}
	for _, scope := range suggestion.ScopePaths {
		abs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(scope))
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			paths = append(paths, scope)
			continue
		}
		err = filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return nil
			}
			rel, err := filepath.Rel(s.deps.Config.MountedWiki.Root, path)
			if err != nil {
				return err
			}
			paths = append(paths, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(paths) == 0 && suggestion.Path != "" {
		paths = append(paths, suggestion.Path)
	}
	return dedupeStrings(paths), nil
}

func normalizeCorrectionSuggestions(items []correctionSuggestion) []correctionSuggestion {
	out := make([]correctionSuggestion, 0, len(items))
	for _, item := range items {
		item.Path = strings.TrimSpace(item.Path)
		item.Section = normalizeCorrectionSection(item.Section)
		item.Wrong = strings.TrimSpace(item.Wrong)
		item.Correct = strings.TrimSpace(item.Correct)
		item.Reason = strings.TrimSpace(item.Reason)
		item.RiskLevel = strings.ToLower(strings.TrimSpace(item.RiskLevel))
		item.ReplaceMode = strings.ToLower(strings.TrimSpace(item.ReplaceMode))
		item.ScopePaths = normalizeCorrectionScopes(item.ScopePaths)
		if item.Path == "" || item.Wrong == "" || item.Correct == "" || item.Wrong == item.Correct {
			continue
		}
		if item.RiskLevel == "" {
			item.RiskLevel = "low"
		}
		if item.ReplaceMode == "" {
			item.ReplaceMode = "targeted"
		}
		out = append(out, item)
	}
	return out
}

func normalizeCorrectionSection(section string) string {
	section = strings.TrimSpace(section)
	if section == "" {
		return ""
	}
	if strings.EqualFold(section, "frontmatter") {
		return "frontmatter"
	}
	if strings.HasPrefix(section, "## ") {
		return section
	}
	return "## " + strings.TrimPrefix(section, "#")
}

func planCorrectionOps(content string, suggestion correctionSuggestion) ([]any, error) {
	doc, err := wikiadapter.ParseDocument(content)
	if err != nil {
		return nil, err
	}
	pairs := replacementPairs(suggestion.Wrong, suggestion.Correct)
	applyGlobally := suggestion.ReplaceMode == "global"
	ops := []any{}
	if applyGlobally || suggestion.Section == "frontmatter" || suggestion.Section == "" {
		fields, changed := replaceFrontmatterTerms(doc.Frontmatter, pairs)
		if changed {
			ops = append(ops, map[string]any{"type": "update_frontmatter", "fields": fields})
		}
	}
	sections := parseBodySections(doc.Body)
	replacedAny := false
	for _, section := range sections {
		if !applyGlobally && suggestion.Section != "" && suggestion.Section != "frontmatter" && section.Heading != suggestion.Section {
			continue
		}
		newContent, changed := applyReplacementPairs(section.Content, pairs)
		if !changed {
			continue
		}
		ops = append(ops, map[string]any{
			"type":    "replace_section",
			"section": section.Heading,
			"content": strings.TrimSpace(newContent),
		})
		replacedAny = true
	}
	if !applyGlobally && suggestion.Section != "" && suggestion.Section != "frontmatter" && !replacedAny {
		return nil, fmt.Errorf("section %s does not contain %q", suggestion.Section, suggestion.Wrong)
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no exact occurrences of %q found in %s", suggestion.Wrong, suggestion.Path)
	}
	return ops, nil
}

func replaceFrontmatterTerms(frontmatter map[string]any, pairs [][2]string) (map[string]any, bool) {
	fields := map[string]any{}
	changed := false
	if title, ok := frontmatter["title"].(string); ok {
		if replaced, localChanged := applyReplacementPairs(title, pairs); localChanged {
			fields["title"] = replaced
			changed = true
		}
	}
	switch aliases := frontmatter["aliases"].(type) {
	case []any:
		next := make([]any, 0, len(aliases))
		localChange := false
		for _, alias := range aliases {
			text := fmt.Sprintf("%v", alias)
			replaced, changedOne := applyReplacementPairs(text, pairs)
			if changedOne {
				localChange = true
			}
			next = append(next, replaced)
		}
		if localChange {
			fields["aliases"] = next
			changed = true
		}
	case []string:
		next := make([]any, 0, len(aliases))
		localChange := false
		for _, alias := range aliases {
			replaced, changedOne := applyReplacementPairs(alias, pairs)
			if changedOne {
				localChange = true
			}
			next = append(next, replaced)
		}
		if localChange {
			fields["aliases"] = next
			changed = true
		}
	}
	return fields, changed
}

func applyReplacementPairs(text string, pairs [][2]string) (string, bool) {
	replaced := text
	changed := false
	for _, pair := range pairs {
		wrong := strings.TrimSpace(pair[0])
		correct := strings.TrimSpace(pair[1])
		if wrong == "" || correct == "" || wrong == correct {
			continue
		}
		next := strings.ReplaceAll(replaced, wrong, correct)
		if next != replaced {
			replaced = next
			changed = true
		}
	}
	return replaced, changed
}

func replacementPairs(wrong string, correct string) [][2]string {
	pairs := [][2]string{}
	appendPair := func(a string, b string) {
		a = strings.TrimSpace(a)
		b = strings.TrimSpace(b)
		if a == "" || b == "" || a == b {
			return
		}
		for _, pair := range pairs {
			if pair[0] == a && pair[1] == b {
				return
			}
		}
		pairs = append(pairs, [2]string{a, b})
	}
	appendPair(wrong, correct)
	appendPair(frontmatterValueToken(wrong), frontmatterValueToken(correct))
	appendPair(coreReplacementToken(wrong), coreReplacementToken(correct))
	appendPair(coreReplacementToken(frontmatterValueToken(wrong)), coreReplacementToken(frontmatterValueToken(correct)))
	return pairs
}

func coreReplacementToken(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range text {
		if r == '(' || r == '（' || unicode.IsSpace(r) || strings.ContainsRune("，。,:：;；/[]【】", r) {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Han, r) || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		break
	}
	return strings.TrimSpace(b.String())
}

func frontmatterValueToken(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, sep := range []string{":", "："} {
		if idx := strings.Index(text, sep); idx >= 0 {
			return strings.TrimSpace(text[idx+len(sep):])
		}
	}
	return ""
}

func parseBodySections(body string) []bodySection {
	lines := strings.Split(body, "\n")
	sections := []bodySection{}
	currentHeading := ""
	currentContent := []string{}
	flush := func() {
		if currentHeading == "" {
			return
		}
		sections = append(sections, bodySection{
			Heading: currentHeading,
			Content: strings.Trim(strings.Join(currentContent, "\n"), "\n"),
		})
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			currentHeading = strings.TrimSpace(line)
			currentContent = currentContent[:0]
			continue
		}
		if currentHeading == "" {
			continue
		}
		currentContent = append(currentContent, line)
	}
	flush()
	return sections
}

func extractSourceLinks(content string) []string {
	matches := []string{}
	rest := content
	for {
		idx := strings.Index(rest, "[[sources/")
		if idx == -1 {
			break
		}
		rest = rest[idx+len("[[sources/"):]
		end := strings.Index(rest, "]]")
		if end == -1 {
			break
		}
		slug := strings.TrimSpace(rest[:end])
		if slug != "" {
			matches = append(matches, slug)
		}
		rest = rest[end+2:]
	}
	for _, match := range plainWikiLinkPattern.FindAllStringSubmatch(content, -1) {
		if len(match) != 2 {
			continue
		}
		slug := strings.TrimSpace(match[1])
		if wikiadapter.IsValidSlug(slug) {
			matches = append(matches, slug)
		}
	}
	return dedupeStrings(matches)
}

func isCorrectionTargetPath(path string) bool {
	switch {
	case strings.HasPrefix(path, "wiki/sources/"):
		return true
	case strings.HasPrefix(path, "wiki/concepts/"):
		return true
	case strings.HasPrefix(path, "wiki/entities/"):
		return true
	case strings.HasPrefix(path, "wiki/synthesis/"):
		return true
	default:
		return false
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
