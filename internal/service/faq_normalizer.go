package service

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"path/filepath"
	"sort"
	"strings"
)

const (
	faqSourceFamily = "faq-dataset"
	faqSegmentSize  = 80
)

type canonicalFAQDataset struct {
	Format        string
	Family        string
	TitleBase     string
	SlugBase      string
	RawPath       string
	Entries       []canonicalFAQEntry
	CategoryOrder []string
	Notes         []string
}

type canonicalFAQEntry struct {
	ID               string
	Category         string
	Question         string
	SimilarQuestions []string
	Answer           string
	ConditionNotes   []string
}

type canonicalFAQSegment struct {
	Dataset *canonicalFAQDataset
	Index   int
	Total   int
	Tag     string
	Entries []canonicalFAQEntry
}

func detectCanonicalFAQDataset(path string, titleHint string, content string) (*canonicalFAQDataset, error) {
	if dataset, ok, err := parseFAQJSONDataset(path, titleHint, content); ok || err != nil {
		return dataset, err
	}
	if dataset, ok := parseFAQMarkdownDataset(path, titleHint, content); ok {
		return dataset, nil
	}
	return nil, nil
}

func parseFAQJSONDataset(path string, titleHint string, content string) (*canonicalFAQDataset, bool, error) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasSuffix(strings.ToLower(path), ".json") && !looksLikeJSONObject(trimmed) {
		return nil, false, nil
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		if strings.HasSuffix(strings.ToLower(path), ".json") {
			return nil, true, ValidationError{Message: fmt.Sprintf("FAQ JSON 解析失败：%s", err.Error())}
		}
		return nil, false, nil
	}
	rawFAQ, ok := root["faq"].([]any)
	if !ok || len(rawFAQ) == 0 {
		if strings.HasSuffix(strings.ToLower(path), ".json") {
			return nil, true, ValidationError{Message: "当前仅支持 FAQ JSON 导入，文件中未识别到顶层 faq 数组。"}
		}
		return nil, false, nil
	}

	typeMap := map[string]string{}
	if rawTypes, ok := root["types"].([]any); ok {
		for _, item := range rawTypes {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			typeID := strings.TrimSpace(stringifyAny(record["id"]))
			category := strings.TrimSpace(stringifyAny(record["category"]))
			if typeID == "" || category == "" {
				continue
			}
			typeMap[typeID] = category
		}
	}

	simMap := map[string][]string{}
	if rawSims, ok := root["sims"].([]any); ok {
		for _, item := range rawSims {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			parentID := strings.TrimSpace(stringifyAny(record["parent_id"]))
			question := strings.TrimSpace(stringifyAny(record["question"]))
			if parentID == "" || question == "" {
				continue
			}
			simMap[parentID] = append(simMap[parentID], question)
		}
	}

	entries := make([]canonicalFAQEntry, 0, len(rawFAQ))
	categoryOrder := []string{}
	seenCategories := map[string]bool{}
	hasConditions := false
	for _, item := range rawFAQ {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		question := strings.TrimSpace(stringifyAny(record["question"]))
		if question == "" {
			continue
		}
		category := strings.TrimSpace(typeMap[strings.TrimSpace(stringifyAny(record["type_id"]))])
		if category == "" {
			category = firstNonEmpty(strings.TrimSpace(stringifyAny(record["category"])), "未分类")
		}
		if !seenCategories[category] {
			seenCategories[category] = true
			categoryOrder = append(categoryOrder, category)
		}
		answer := normalizeFAQAnswerText(stringifyAny(record["answer"]))
		conditionNotes := renderConditionTemplateNotes(record["condition_template"])
		if len(conditionNotes) > 0 {
			hasConditions = true
		}
		entry := canonicalFAQEntry{
			ID:               strings.TrimSpace(stringifyAny(record["id"])),
			Category:         category,
			Question:         question,
			SimilarQuestions: dedupeStrings(append(splitFAQVariants(stringifyAny(record["similar_questions"])), simMap[strings.TrimSpace(stringifyAny(record["id"]))]...)),
			Answer:           answer,
			ConditionNotes:   conditionNotes,
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, true, ValidationError{Message: "FAQ JSON 中没有可摄入的有效 question/answer 数据。"}
	}

	notes := []string{
		fmt.Sprintf("检测到 FAQ JSON 数据集：共 %d 条标准问答，相似问法已按 parent_id 并入主问法。", len(entries)),
	}
	if hasConditions {
		notes = append(notes, "部分 FAQ 带有 condition_template 条件逻辑；本期仅保留为元数据说明，不直接进入回答决策。")
	}
	if wsSummary := summarizeWSInfo(root["ws_info"]); wsSummary != "" {
		notes = append(notes, "检测到 ws_info 元数据："+wsSummary)
	}
	titleBase := firstNonEmpty(strings.TrimSpace(titleHint), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "FAQ 数据")
	return &canonicalFAQDataset{
		Format:        "faq-json",
		Family:        faqSourceFamily,
		TitleBase:     titleBase,
		SlugBase:      stableFAQSlugBase(titleBase, path),
		RawPath:       path,
		Entries:       entries,
		CategoryOrder: categoryOrder,
		Notes:         dedupeStrings(notes),
	}, true, nil
}

func parseFAQMarkdownDataset(path string, titleHint string, content string) (*canonicalFAQDataset, bool) {
	lines := strings.Split(content, "\n")
	tableLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			tableLines = append(tableLines, strings.TrimSpace(line))
		}
	}
	if len(tableLines) < 3 {
		return nil, false
	}
	headers := splitMarkdownTableRow(tableLines[0])
	if len(headers) < 2 {
		return nil, false
	}
	indexMap := faqHeaderIndexMap(headers)
	if indexMap["question"] < 0 || indexMap["answer"] < 0 {
		return nil, false
	}
	entries := []canonicalFAQEntry{}
	categoryOrder := []string{}
	seenCategories := map[string]bool{}
	hasConditions := false
	for _, line := range tableLines[1:] {
		cells := splitMarkdownTableRow(line)
		if len(cells) == 0 || looksLikeMarkdownSeparator(cells[0]) {
			continue
		}
		question := cellAt(cells, indexMap["question"])
		answer := normalizeFAQAnswerText(cellAt(cells, indexMap["answer"]))
		if strings.TrimSpace(question) == "" {
			continue
		}
		category := firstNonEmpty(strings.TrimSpace(cellAt(cells, indexMap["category"])), "未分类")
		if !seenCategories[category] {
			seenCategories[category] = true
			categoryOrder = append(categoryOrder, category)
		}
		conditionNotes := splitFAQVariants(cellAt(cells, indexMap["conditions"]))
		if len(conditionNotes) > 0 {
			hasConditions = true
		}
		entries = append(entries, canonicalFAQEntry{
			Category:         category,
			Question:         strings.TrimSpace(question),
			SimilarQuestions: splitFAQVariants(cellAt(cells, indexMap["similar"])),
			Answer:           answer,
			ConditionNotes:   conditionNotes,
		})
	}
	if len(entries) == 0 {
		return nil, false
	}
	notes := []string{fmt.Sprintf("检测到 FAQ Markdown 表格：共 %d 条标准问答，已转为统一 FAQ 结构处理。", len(entries))}
	if hasConditions {
		notes = append(notes, "部分 FAQ 带有条件列；本期仅保留为元数据说明，不直接进入回答决策。")
	}
	titleBase := firstNonEmpty(strings.TrimSpace(titleHint), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "FAQ 数据")
	return &canonicalFAQDataset{
		Format:        "faq-markdown-table",
		Family:        faqSourceFamily,
		TitleBase:     titleBase,
		SlugBase:      stableFAQSlugBase(titleBase, path),
		RawPath:       path,
		Entries:       entries,
		CategoryOrder: categoryOrder,
		Notes:         dedupeStrings(notes),
	}, true
}

func faqHeaderIndexMap(headers []string) map[string]int {
	indexMap := map[string]int{
		"category":   -1,
		"question":   -1,
		"similar":    -1,
		"answer":     -1,
		"conditions": -1,
	}
	for i, header := range headers {
		normalized := strings.ToLower(strings.TrimSpace(header))
		switch normalized {
		case "技能分类", "分类", "category", "type", "问题分类":
			indexMap["category"] = i
		case "标准问题", "标准问法", "问题", "question":
			indexMap["question"] = i
		case "相似问法", "相似问题", "同义问法", "similar", "similar_questions":
			indexMap["similar"] = i
		case "回复内容", "回复", "答案", "answer", "answer_text":
			indexMap["answer"] = i
		case "命中条件", "条件", "conditions", "condition_template":
			indexMap["conditions"] = i
		}
	}
	return indexMap
}

func cellAt(cells []string, index int) string {
	if index < 0 || index >= len(cells) {
		return ""
	}
	return strings.TrimSpace(cells[index])
}

func looksLikeJSONObject(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

func normalizeFAQAnswerText(raw string) string {
	text := html.UnescapeString(strings.TrimSpace(raw))
	replacements := []struct {
		old string
		new string
	}{
		{"<br />", "\n"},
		{"<br/>", "\n"},
		{"<br>", "\n"},
		{"</p>", "\n\n"},
		{"</div>", "\n"},
		{"</li>", "\n"},
	}
	for _, item := range replacements {
		text = strings.ReplaceAll(text, item.old, item.new)
		text = strings.ReplaceAll(text, strings.ToUpper(item.old), item.new)
	}
	for _, tag := range []string{"<p>", "<div>", "<ul>", "</ul>", "<ol>", "</ol>", "<li>", "<strong>", "</strong>", "<em>", "</em>", "<span>", "</span>"} {
		text = strings.ReplaceAll(text, tag, "")
		text = strings.ReplaceAll(text, strings.ToUpper(tag), "")
	}
	for {
		start := strings.Index(text, "<")
		end := strings.Index(text, ">")
		if start == -1 || end == -1 || end < start {
			break
		}
		text = text[:start] + text[end+1:]
	}
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if blank {
				continue
			}
			blank = true
			cleaned = append(cleaned, "")
			continue
		}
		blank = false
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func splitFAQVariants(raw string) []string {
	text := strings.TrimSpace(raw)
	if text == "" || text == "[]" {
		return nil
	}
	replacer := strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n", "；", "\n", ";", "\n")
	text = replacer.Replace(html.UnescapeString(text))
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return dedupeStrings(out)
}

func renderConditionTemplateNotes(raw any) []string {
	if raw == nil {
		return nil
	}
	values := dedupeStrings(extractConditionValues(raw))
	if len(values) > 0 {
		out := make([]string, 0, len(values))
		for _, item := range values {
			out = append(out, "命中条件："+item)
		}
		return out
	}
	if text := strings.TrimSpace(compactJSON(raw)); text != "" && text != "{}" && text != "[]" && text != "null" {
		return []string{"原始条件：" + text}
	}
	return nil
}

func extractConditionValues(raw any) []string {
	switch typed := raw.(type) {
	case map[string]any:
		out := []string{}
		for key, value := range typed {
			if strings.EqualFold(key, "value") {
				if text := strings.TrimSpace(stringifyAny(value)); text != "" && text != "null" {
					out = append(out, text)
				}
			}
			out = append(out, extractConditionValues(value)...)
		}
		return out
	case []any:
		out := []string{}
		for _, item := range typed {
			out = append(out, extractConditionValues(item)...)
		}
		return out
	default:
		return nil
	}
}

func compactJSON(raw any) string {
	data, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	return string(data)
}

func summarizeWSInfo(raw any) string {
	record, ok := raw.(map[string]any)
	if !ok || len(record) == 0 {
		return ""
	}
	parts := []string{}
	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		switch typed := record[key].(type) {
		case []any:
			if len(typed) > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d 项", key, len(typed)))
			}
		case map[string]any:
			if len(typed) > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d 项", key, len(typed)))
			}
		}
	}
	return strings.Join(parts, "，")
}

func stableFAQSlugBase(titleBase string, path string) string {
	base := slugFromText(titleBase)
	if base == "" || base == "output" {
		base = slugFromText(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if base == "" || base == "output" {
		sum := sha1.Sum([]byte(path))
		base = "faq-" + hex.EncodeToString(sum[:4])
	}
	if !strings.HasPrefix(base, "faq-") {
		base = "faq-" + base
	}
	return base
}

func (d *canonicalFAQDataset) segments(maxEntries int) []canonicalFAQSegment {
	if d == nil || len(d.Entries) == 0 {
		return nil
	}
	if maxEntries <= 0 {
		maxEntries = faqSegmentSize
	}
	out := []canonicalFAQSegment{}
	if len(d.CategoryOrder) <= 1 {
		for start := 0; start < len(d.Entries); start += maxEntries {
			end := start + maxEntries
			if end > len(d.Entries) {
				end = len(d.Entries)
			}
			tag := firstNonEmpty(d.entriesCategory(d.Entries[start:end]), "未分类")
			out = append(out, canonicalFAQSegment{Dataset: d, Tag: tag, Entries: d.Entries[start:end]})
		}
	} else {
		for _, category := range d.CategoryOrder {
			group := []canonicalFAQEntry{}
			for _, entry := range d.Entries {
				if entry.Category == category {
					group = append(group, entry)
				}
			}
			for start := 0; start < len(group); start += maxEntries {
				end := start + maxEntries
				if end > len(group) {
					end = len(group)
				}
				out = append(out, canonicalFAQSegment{Dataset: d, Tag: category, Entries: group[start:end]})
			}
		}
	}
	for i := range out {
		out[i].Index = i + 1
		out[i].Total = len(out)
	}
	return out
}

func (d *canonicalFAQDataset) entriesCategory(entries []canonicalFAQEntry) string {
	seen := map[string]bool{}
	categories := []string{}
	for _, entry := range entries {
		if entry.Category == "" || seen[entry.Category] {
			continue
		}
		seen[entry.Category] = true
		categories = append(categories, entry.Category)
	}
	if len(categories) == 0 {
		return ""
	}
	if len(categories) == 1 {
		return categories[0]
	}
	return strings.Join(categories, " / ")
}

func (s canonicalFAQSegment) title() string {
	base := firstNonEmpty(strings.TrimSpace(s.Dataset.TitleBase), "FAQ 数据")
	if strings.TrimSpace(s.Tag) != "" {
		return fmt.Sprintf("%s FAQ 分段（%s %d/%d）", base, s.Tag, s.Index, s.Total)
	}
	return fmt.Sprintf("%s FAQ 分段（%d/%d）", base, s.Index, s.Total)
}

func (s canonicalFAQSegment) slug() string {
	return fmt.Sprintf("%s-segment-%02d", s.Dataset.SlugBase, s.Index)
}

func (s canonicalFAQSegment) summary() string {
	return fmt.Sprintf(
		"本页是 %s 的 FAQ 数据分段，第 %d/%d 段，共 %d 条标准问答。当前分段主题为 %s；相似问法已并入主问法，HTML 回复已转为纯文本，条件逻辑仅保留为元数据说明。",
		firstNonEmpty(strings.TrimSpace(s.Dataset.TitleBase), "FAQ 数据"),
		s.Index,
		s.Total,
		len(s.Entries),
		firstNonEmpty(strings.TrimSpace(s.Tag), "未分类"),
	)
}

func (s canonicalFAQSegment) keyPoints() []string {
	withSimilar := 0
	withConditions := 0
	for _, entry := range s.Entries {
		if len(entry.SimilarQuestions) > 0 {
			withSimilar++
		}
		if len(entry.ConditionNotes) > 0 {
			withConditions++
		}
	}
	points := []string{
		fmt.Sprintf("当前分段包含 %d 条标准 FAQ，分类标签为 %s。", len(s.Entries), firstNonEmpty(strings.TrimSpace(s.Tag), "未分类")),
		fmt.Sprintf("其中 %d 条 FAQ 带有相似问法，已并入对应主问法结构。", withSimilar),
		"原始 answer 中的 HTML 标签已转为纯文本，便于检索、摘要和客服回答复用。",
	}
	if withConditions > 0 {
		points = append(points, fmt.Sprintf("其中 %d 条 FAQ 带有条件逻辑；本期仅保留为元数据，不直接参与回答决策。", withConditions))
	}
	return points
}

func (s canonicalFAQSegment) notes() []string {
	notes := []string{
		fmt.Sprintf("结构化 FAQ 数据格式：%s。", s.Dataset.Format),
		"本页为自动分段结果；标题和 slug 使用稳定 segment 语义，不从单条问答中抽取主题词覆盖整页语义。",
	}
	notes = append(notes, s.Dataset.Notes...)
	return dedupeStrings(notes)
}

func (s canonicalFAQSegment) renderContent() string {
	parts := []string{
		"# " + s.title(),
		"",
		"## FAQ Entries",
		"",
		strings.TrimSpace(renderFAQEntriesSection(s.Entries)),
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func renderFAQEntriesSection(entries []canonicalFAQEntry) string {
	if len(entries) == 0 {
		return "暂无 FAQ 条目。"
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		block := []string{"### " + entry.Question, ""}
		if strings.TrimSpace(entry.Category) != "" {
			block = append(block, "分类："+entry.Category, "")
		}
		if len(entry.SimilarQuestions) > 0 {
			block = append(block, "相似问法：")
			for _, item := range entry.SimilarQuestions {
				block = append(block, "- "+item)
			}
			block = append(block, "")
		}
		block = append(block, "回复：")
		block = append(block, firstNonEmpty(entry.Answer, "暂无标准回复。"), "")
		if len(entry.ConditionNotes) > 0 {
			block = append(block, "条件元数据：")
			for _, item := range entry.ConditionNotes {
				block = append(block, "- "+item)
			}
			block = append(block, "")
		}
		parts = append(parts, strings.TrimRight(strings.Join(block, "\n"), "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func buildFAQEvidencePreview(body string, question string) string {
	summary := extractMarkdownSection(body, "## Summary")
	keyPoints := extractMarkdownSection(body, "## Key Points")
	faqEntries := extractMarkdownSection(body, "## FAQ Entries")
	entryBlocks := splitFAQEntryBlocks(faqEntries)
	if len(entryBlocks) == 0 {
		return strings.TrimSpace(body)
	}
	selected := selectRelevantFAQEntryBlocks(entryBlocks, question, 3)
	parts := []string{}
	if strings.TrimSpace(summary) != "" {
		parts = append(parts, "## Summary\n\n"+strings.TrimSpace(summary))
	}
	if strings.TrimSpace(keyPoints) != "" {
		parts = append(parts, "## Key Points\n\n"+strings.TrimSpace(keyPoints))
	}
	parts = append(parts, "## FAQ Entries\n\n"+strings.Join(selected, "\n\n"))
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func extractMarkdownSection(body string, heading string) string {
	start := strings.Index(body, heading)
	if start == -1 {
		return ""
	}
	rest := body[start+len(heading):]
	end := strings.Index(rest, "\n## ")
	if end == -1 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

func splitFAQEntryBlocks(section string) []string {
	trimmed := strings.TrimSpace(section)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "\n### ")
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i == 0 && strings.HasPrefix(part, "### ") {
			out = append(out, part)
			continue
		}
		if !strings.HasPrefix(part, "### ") {
			part = "### " + part
		}
		out = append(out, part)
	}
	return out
}

func selectRelevantFAQEntryBlocks(blocks []string, question string, limit int) []string {
	if len(blocks) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(blocks) {
		limit = len(blocks)
	}
	type scoredBlock struct {
		Text  string
		Score int
	}
	scored := make([]scoredBlock, 0, len(blocks))
	for _, block := range blocks {
		scored = append(scored, scoredBlock{Text: block, Score: faqBlockScore(block, question)})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	out := make([]string, 0, limit)
	for _, item := range scored {
		if len(out) == limit {
			break
		}
		if item.Score == 0 && len(out) > 0 {
			break
		}
		out = append(out, item.Text)
	}
	if len(out) == 0 {
		for i := 0; i < limit && i < len(blocks); i++ {
			out = append(out, blocks[i])
		}
	}
	return out
}

func faqBlockScore(block string, question string) int {
	if strings.TrimSpace(block) == "" || strings.TrimSpace(question) == "" {
		return 0
	}
	normalizedBlock := normalizeFAQSearchText(block)
	normalizedQuestion := normalizeFAQSearchText(question)
	score := 0
	if strings.Contains(normalizedBlock, normalizedQuestion) {
		score += 100
	}
	for _, term := range faqSearchTerms(normalizedQuestion) {
		if term == "" {
			continue
		}
		score += strings.Count(normalizedBlock, term) * 8
	}
	return score
}

func normalizeFAQSearchText(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 0x4e00 && r <= 0x9fff:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func faqSearchTerms(text string) []string {
	if len([]rune(text)) <= 1 {
		return []string{text}
	}
	terms := []string{text}
	runes := []rune(text)
	for size := 2; size <= 4; size++ {
		if len(runes) < size {
			break
		}
		for i := 0; i+size <= len(runes); i++ {
			terms = append(terms, string(runes[i:i+size]))
		}
	}
	return dedupeStrings(terms)
}

func stringifyAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprintf("%v", typed)
	}
}
