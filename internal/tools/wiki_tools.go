package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"wikios/internal/runtime"
	"wikios/internal/wikiadapter"
)

type wikiReadPageTool struct{ baseTool }
type wikiSearchPagesTool struct{ baseTool }
type wikiFindBySlugTool struct{ baseTool }
type wikiFindByAliasTool struct{ baseTool }
type wikiCreateFromTemplateTool struct{ baseTool }
type wikiPatchPageTool struct{ baseTool }
type wikiAppendLogTool struct{ baseTool }
type wikiWriteOutputTool struct{ baseTool }
type wikiUpdateIndexEntryTool struct{ baseTool }
type wikiUpdateQuestionsTool struct{ baseTool }

func NewWikiReadPageTool(deps Dependencies) runtime.Tool {
	return &wikiReadPageTool{baseTool{name: "wiki.read_page", risk: runtime.RiskLow, deps: deps}}
}
func NewWikiSearchPagesTool(deps Dependencies) runtime.Tool {
	return &wikiSearchPagesTool{baseTool{name: "wiki.search_pages", risk: runtime.RiskLow, deps: deps}}
}
func NewWikiFindBySlugTool(deps Dependencies) runtime.Tool {
	return &wikiFindBySlugTool{baseTool{name: "wiki.find_by_slug", risk: runtime.RiskLow, deps: deps}}
}
func NewWikiFindByAliasTool(deps Dependencies) runtime.Tool {
	return &wikiFindByAliasTool{baseTool{name: "wiki.find_by_alias", risk: runtime.RiskLow, deps: deps}}
}
func NewWikiCreateFromTemplateTool(deps Dependencies) runtime.Tool {
	return &wikiCreateFromTemplateTool{baseTool{name: "wiki.create_from_template", risk: runtime.RiskMedium, deps: deps}}
}
func NewWikiPatchPageTool(deps Dependencies) runtime.Tool {
	return &wikiPatchPageTool{baseTool{name: "wiki.patch_page", risk: runtime.RiskMedium, deps: deps}}
}
func NewWikiAppendLogTool(deps Dependencies) runtime.Tool {
	return &wikiAppendLogTool{baseTool{name: "wiki.append_log", risk: runtime.RiskLow, deps: deps}}
}
func NewWikiWriteOutputTool(deps Dependencies) runtime.Tool {
	return &wikiWriteOutputTool{baseTool{name: "wiki.write_output", risk: runtime.RiskLow, deps: deps}}
}
func NewWikiUpdateIndexEntryTool(deps Dependencies) runtime.Tool {
	return &wikiUpdateIndexEntryTool{baseTool{name: "wiki.update_index_entry", risk: runtime.RiskLow, deps: deps}}
}
func NewWikiUpdateQuestionsTool(deps Dependencies) runtime.Tool {
	return &wikiUpdateQuestionsTool{baseTool{name: "wiki.update_questions", risk: runtime.RiskLow, deps: deps}}
}

func (t *wikiReadPageTool) Validate(args map[string]any) error {
	_, err := requireString(args, "path")
	return err
}
func (t *wikiReadPageTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	path, _ := requireString(args, "path")
	abs, rel, err := t.deps.Resolver.ResolveReadPath(path)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel, "content": string(content)}), nil
}

func (t *wikiSearchPagesTool) Validate(args map[string]any) error {
	_, err := requireString(args, "query")
	return err
}
func (t *wikiSearchPagesTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	query, _ := requireString(args, "query")
	query = strings.ToLower(strings.TrimSpace(query))
	terms := searchTerms(query)
	var matches []map[string]any
	err := filepath.Walk(filepath.Join(t.deps.Resolver.WikiRoot(), "wiki"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		haystack := normalizeSearchText(string(content))
		rel, _ := filepath.Rel(t.deps.Resolver.WikiRoot(), path)
		normalizedRel := normalizeSearchText(filepath.ToSlash(rel))
		score := searchScore(haystack, normalizedRel, terms)
		if score == 0 {
			return nil
		}
		matches = append(matches, map[string]any{
			"path":  filepath.ToSlash(rel),
			"score": score,
		})
		return nil
	})
	if err != nil {
		return failure(t.risk, "SEARCH_FAILED", err), nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i]["score"].(int) > matches[j]["score"].(int)
	})
	if len(matches) > 10 {
		matches = matches[:10]
	}
	return success(t.risk, map[string]any{"matches": matches}), nil
}

func (t *wikiFindBySlugTool) Validate(args map[string]any) error {
	slug, err := requireString(args, "slug")
	if err != nil {
		return err
	}
	if !wikiadapter.IsValidSlug(slug) {
		return fmt.Errorf("invalid slug")
	}
	return nil
}
func (t *wikiFindBySlugTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	slug, _ := requireString(args, "slug")
	paths := []string{
		"wiki/concepts/" + slug + ".md",
		"wiki/entities/" + slug + ".md",
		"wiki/sources/" + slug + ".md",
		"wiki/synthesis/" + slug + ".md",
	}
	for _, path := range paths {
		if _, _, err := t.deps.Resolver.ResolveReadPath(path); err == nil {
			return success(t.risk, map[string]any{"path": path, "slug": slug}), nil
		}
	}
	return success(t.risk, map[string]any{"slug": slug, "path": ""}), nil
}

func (t *wikiFindByAliasTool) Validate(args map[string]any) error {
	_, err := requireString(args, "alias")
	return err
}
func (t *wikiFindByAliasTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	alias, _ := requireString(args, "alias")
	normalized := strings.ToLower(strings.TrimSpace(alias))
	var matches []string
	for _, dir := range []string{"wiki/concepts", "wiki/entities"} {
		root := filepath.Join(t.deps.Resolver.WikiRoot(), dir)
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
				return err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			doc, err := wikiadapter.ParseDocument(string(content))
			if err != nil {
				return nil
			}
			raw, ok := doc.Frontmatter["aliases"]
			if !ok {
				return nil
			}
			for _, item := range stringifySlice(raw) {
				if strings.ToLower(strings.TrimSpace(item)) == normalized {
					rel, _ := filepath.Rel(t.deps.Resolver.WikiRoot(), path)
					matches = append(matches, filepath.ToSlash(rel))
					break
				}
			}
			return nil
		})
	}
	slices.Sort(matches)
	return success(t.risk, map[string]any{"alias": alias, "matches": matches}), nil
}

func searchScore(haystack string, rel string, terms []string) int {
	score := 0
	for index, term := range terms {
		if term == "" {
			continue
		}
		weight := 6
		if index > 0 {
			weight = 3
		}
		score += strings.Count(haystack, term) * weight
		score += strings.Count(rel, term) * (weight + 2)
	}
	switch {
	case strings.Contains(rel, "wiki/sources/"):
		score += 5
	case strings.Contains(rel, "wiki/concepts/"):
		score += 4
	case strings.Contains(rel, "wiki/entities/"):
		score += 3
	case strings.Contains(rel, "wiki/synthesis/"):
		score += 2
	case strings.Contains(rel, "wiki/index.md"):
		score -= 2
	}
	if score < 0 {
		return 0
	}
	return score
}

func searchTerms(query string) []string {
	normalized := normalizeSearchText(query)
	if normalized == "" {
		return nil
	}
	terms := []string{normalized}
	segments := splitSearchSegments(normalized)
	for _, segment := range segments {
		if len([]rune(segment)) <= 1 {
			continue
		}
		terms = append(terms, segment)
		if isHanString(segment) {
			terms = append(terms, hanNGrams(segment, 2)...)
		}
	}
	return dedupeSearchTerms(terms)
}

func normalizeSearchText(text string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.Is(unicode.Han, r):
			b.WriteRune(r)
			lastSpace = false
		default:
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func splitSearchSegments(text string) []string {
	parts := strings.Fields(text)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		var current strings.Builder
		lastKind := 0
		flush := func() {
			if current.Len() == 0 {
				return
			}
			out = append(out, current.String())
			current.Reset()
		}
		for _, r := range part {
			kind := searchRuneKind(r)
			if lastKind != 0 && kind != lastKind {
				flush()
			}
			current.WriteRune(r)
			lastKind = kind
		}
		flush()
	}
	return out
}

func searchRuneKind(r rune) int {
	switch {
	case unicode.Is(unicode.Han, r):
		return 1
	case unicode.IsLetter(r), unicode.IsDigit(r):
		return 2
	default:
		return 0
	}
}

func isHanString(text string) bool {
	for _, r := range text {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return text != ""
}

func hanNGrams(text string, size int) []string {
	runes := []rune(text)
	if len(runes) <= size {
		return nil
	}
	out := make([]string, 0, len(runes)-size+1)
	for i := 0; i <= len(runes)-size; i++ {
		out = append(out, string(runes[i:i+size]))
	}
	return out
}

func dedupeSearchTerms(terms []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" || seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

func (t *wikiCreateFromTemplateTool) Validate(args map[string]any) error {
	if _, err := requireString(args, "template_path"); err != nil {
		return err
	}
	_, err := requireString(args, "target_path")
	return err
}
func (t *wikiCreateFromTemplateTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	templatePath, _ := requireString(args, "template_path")
	targetPath, _ := requireString(args, "target_path")
	absTemplate, _, err := t.deps.Resolver.ResolveReadPath(templatePath)
	if err != nil {
		return failure(t.risk, "READ_TEMPLATE_FAILED", err), nil
	}
	absTarget, relTarget, err := t.deps.Resolver.EnsureWritableWikiPath(targetPath)
	if err != nil {
		return failure(t.risk, "WRITE_DENIED", err), nil
	}
	content, err := os.ReadFile(absTemplate)
	if err != nil {
		return failure(t.risk, "READ_TEMPLATE_FAILED", err), nil
	}
	doc, err := wikiadapter.ParseDocument(string(content))
	if err != nil {
		return failure(t.risk, "INVALID_TEMPLATE", err), nil
	}
	if fields, ok := args["frontmatter"].(map[string]any); ok {
		for key, value := range fields {
			doc.Frontmatter[key] = value
		}
	}
	if wikiadapter.NeedsGraphExcluded(relTarget) {
		doc.Frontmatter["graph-excluded"] = true
	}
	if err := writeFile(absTarget, wikiadapter.RenderDocument(doc)); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": relTarget}), nil
}

func (t *wikiPatchPageTool) Validate(args map[string]any) error {
	_, err := requireString(args, "path")
	if err != nil {
		return err
	}
	raw, ok := args["ops"]
	if !ok {
		return fmt.Errorf("ops is required")
	}
	if _, ok := raw.([]any); !ok {
		if _, ok := raw.([]wikiadapter.PatchOp); !ok {
			return fmt.Errorf("ops must be a list")
		}
	}
	return nil
}
func (t *wikiPatchPageTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	path, _ := requireString(args, "path")
	abs, rel, err := t.deps.Resolver.EnsureWritableWikiPath(path)
	if err != nil {
		return failure(t.risk, "WRITE_DENIED", err), nil
	}
	if rel == "wiki/log.md" {
		return failure(t.risk, "WRITE_DENIED", fmt.Errorf("wiki/log.md only allows append")), nil
	}
	rawContent, err := os.ReadFile(abs)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	ops, err := decodePatchOps(args["ops"])
	if err != nil {
		return failure(t.risk, "INVALID_ARGS", err), nil
	}
	patched, err := wikiadapter.ApplyPatch(string(rawContent), ops)
	if err != nil {
		return failure(t.risk, "PATCH_FAILED", err), nil
	}
	doc, err := wikiadapter.ParseDocument(patched)
	if err != nil {
		return failure(t.risk, "PATCH_FAILED", err), nil
	}
	if wikiadapter.NeedsGraphExcluded(rel) {
		doc.Frontmatter["graph-excluded"] = true
		patched = wikiadapter.RenderDocument(doc)
	}
	if err := writeFile(abs, patched); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel}), nil
}

func (t *wikiAppendLogTool) Validate(args map[string]any) error {
	_, err := requireString(args, "line")
	return err
}
func (t *wikiAppendLogTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	line, _ := requireString(args, "line")
	abs, rel, err := t.deps.Resolver.EnsureWritableWikiPath("wiki/log.md")
	if err != nil {
		return failure(t.risk, "WRITE_DENIED", err), nil
	}
	f, err := os.OpenFile(abs, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	defer f.Close()
	if !strings.HasPrefix(line, "- ") {
		line = "- " + line
	}
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	if _, err := f.WriteString(line); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel}), nil
}

func (t *wikiWriteOutputTool) Validate(args map[string]any) error {
	_, err := requireString(args, "path")
	if err != nil {
		return err
	}
	_, err = requireString(args, "content")
	return err
}
func (t *wikiWriteOutputTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	path, _ := requireString(args, "path")
	content, _ := requireString(args, "content")
	abs, rel, err := t.deps.Resolver.EnsureWritableWikiPath(path)
	if err != nil {
		return failure(t.risk, "WRITE_DENIED", err), nil
	}
	doc, err := wikiadapter.ParseDocument(content)
	if err != nil {
		return failure(t.risk, "INVALID_CONTENT", err), nil
	}
	if wikiadapter.NeedsGraphExcluded(rel) {
		doc.Frontmatter["graph-excluded"] = true
		content = wikiadapter.RenderDocument(doc)
	}
	if err := writeFile(abs, content); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel}), nil
}

func (t *wikiUpdateIndexEntryTool) Validate(args map[string]any) error {
	_, err := requireString(args, "section")
	if err != nil {
		return err
	}
	_, err = requireString(args, "entry")
	return err
}
func (t *wikiUpdateIndexEntryTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	section, _ := requireString(args, "section")
	entry, _ := requireString(args, "entry")
	abs, rel, err := t.deps.Resolver.EnsureWritableWikiPath("wiki/index.md")
	if err != nil {
		return failure(t.risk, "WRITE_DENIED", err), nil
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	patched, err := wikiadapter.ApplyPatch(string(content), []wikiadapter.PatchOp{{
		Type:    "append_section",
		Section: section,
		Content: entry,
	}})
	if err != nil {
		return failure(t.risk, "PATCH_FAILED", err), nil
	}
	if err := writeFile(abs, patched); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel}), nil
}

func (t *wikiUpdateQuestionsTool) Validate(args map[string]any) error {
	_, err := requireString(args, "entry")
	return err
}
func (t *wikiUpdateQuestionsTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	entry, _ := requireString(args, "entry")
	section := optionalString(args, "section")
	if section == "" {
		section = "## Open Questions"
	}
	abs, rel, err := t.deps.Resolver.EnsureWritableWikiPath("wiki/QUESTIONS.md")
	if err != nil {
		return failure(t.risk, "WRITE_DENIED", err), nil
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	patched, err := wikiadapter.ApplyPatch(string(content), []wikiadapter.PatchOp{{
		Type:    "append_section",
		Section: section,
		Content: entry,
	}})
	if err != nil {
		return failure(t.risk, "PATCH_FAILED", err), nil
	}
	if err := writeFile(abs, patched); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel}), nil
}

func decodePatchOps(raw any) ([]wikiadapter.PatchOp, error) {
	switch typed := raw.(type) {
	case []wikiadapter.PatchOp:
		return typed, nil
	case []any:
		ops := make([]wikiadapter.PatchOp, 0, len(typed))
		for _, item := range typed {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("invalid patch op")
			}
			op := wikiadapter.PatchOp{
				Type:    optionalFromMap(m, "type"),
				Section: optionalFromMap(m, "section"),
				Target:  optionalFromMap(m, "target"),
				Content: optionalFromMap(m, "content"),
			}
			if fields, ok := m["fields"].(map[string]any); ok {
				op.Fields = fields
			}
			ops = append(ops, op)
		}
		return ops, nil
	default:
		return nil, fmt.Errorf("ops must be a list")
	}
}

func optionalFromMap(m map[string]any, key string) string {
	if value, ok := m[key].(string); ok {
		return value
	}
	return ""
}

func stringifySlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out
	default:
		return nil
	}
}

func buildOutputDocument(title string, body string, sourceCount int) string {
	return fmt.Sprintf(`---
type: synthesis
title: %q
date: %s
tags: []
source_count: %d
confidence: low
graph-excluded: true
---

%s
`, title, nowDate(), sourceCount, body)
}

func newProposalID() string {
	return "proposal_" + uuid.NewString()
}
