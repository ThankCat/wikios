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
	Keywords         []string
	Tags             []string
	QuickReplies     []string
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
	return detectCanonicalFAQDatasetWithProfile(path, titleHint, content, nil)
}

func detectCanonicalFAQDatasetWithProfile(path string, titleHint string, content string, profile *knowledgeProfile) (*canonicalFAQDataset, error) {
	if dataset, ok, err := parseFAQJSONDataset(path, titleHint, content); ok || err != nil {
		return dataset, err
	}
	if dataset, ok := parseFAQMarkdownDatasetWithProfile(path, titleHint, content, profile); ok {
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

	format := strings.TrimSpace(stringifyAny(root["source_format"]))
	if format == "" {
		format = strings.TrimSpace(stringifyAny(root["format"]))
	}
	switch format {
	case "faq-xlsx", "faq-json", "faq-markdown-table":
	default:
		format = "faq-json"
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
			SimilarQuestions: dedupeStrings(append(splitFAQAny(record["similar_questions"]), simMap[strings.TrimSpace(stringifyAny(record["id"]))]...)),
			Keywords:         splitFAQAny(firstNonNil(record["keywords"], record["keyword"])),
			Tags:             splitFAQAny(firstNonNil(record["tags"], record["tag"])),
			QuickReplies:     splitFAQAny(firstNonNil(record["quick_replies"], record["quick_reply"], record["shortcuts"])),
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
		Format:        format,
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
	return parseFAQMarkdownDatasetWithProfile(path, titleHint, content, nil)
}

func parseFAQMarkdownDatasetWithProfile(path string, titleHint string, content string, profile *knowledgeProfile) (*canonicalFAQDataset, bool) {
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
	indexMap := faqHeaderIndexMapWithProfile(headers, profile, "faq_markdown_table")
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
			Keywords:         splitFAQVariants(cellAt(cells, indexMap["keywords"])),
			Tags:             splitFAQVariants(cellAt(cells, indexMap["tags"])),
			QuickReplies:     splitFAQVariants(cellAt(cells, indexMap["quick_replies"])),
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
	return faqHeaderIndexMapWithProfile(headers, nil, "")
}

func faqHeaderIndexMapWithProfile(headers []string, profile *knowledgeProfile, adapterName string) map[string]int {
	indexMap := map[string]int{
		"category":      -1,
		"question":      -1,
		"similar":       -1,
		"answer":        -1,
		"conditions":    -1,
		"keywords":      -1,
		"tags":          -1,
		"quick_replies": -1,
	}
	aliases := faqHeaderAliases(profile, adapterName)
	for i, header := range headers {
		normalized := strings.ToLower(strings.TrimSpace(header))
		for field, names := range aliases {
			for _, name := range names {
				if normalized == strings.ToLower(strings.TrimSpace(name)) {
					indexMap[field] = i
				}
			}
		}
	}
	return indexMap
}

func faqHeaderAliases(profile *knowledgeProfile, adapterName string) map[string][]string {
	aliases := map[string][]string{
		"category":      {"技能分类", "分类", "category", "type", "问题分类"},
		"question":      {"标准问题", "标准问法", "问题", "question"},
		"similar":       {"相似问法", "相似问题", "同义问法", "similar", "similar_questions"},
		"answer":        {"回复内容", "回复", "答案", "answer", "answer_text"},
		"conditions":    {"命中条件", "条件", "conditions", "condition_template"},
		"keywords":      {"关键词", "关键字", "keywords", "keyword"},
		"tags":          {"标签", "标记", "tags", "tag", "labels"},
		"quick_replies": {"快捷短语", "快捷回复", "快捷指令", "quick_replies", "quick_reply", "shortcuts"},
	}
	if profile == nil {
		return aliases
	}
	adapter := profile.InputAdapters.FAQMarkdownTable
	if adapterName == "faq_xlsx" {
		adapter = profile.InputAdapters.FAQXLSX
	}
	mergeAliasField := func(profileField string, internalField string) {
		if values := adapter.RequiredFields[profileField]; len(values) > 0 {
			aliases[internalField] = dedupeStrings(append(aliases[internalField], values...))
		}
		if values := adapter.OptionalFields[profileField]; len(values) > 0 {
			aliases[internalField] = dedupeStrings(append(aliases[internalField], values...))
		}
	}
	mergeAliasField("original_category", "category")
	mergeAliasField("question", "question")
	mergeAliasField("similar_questions", "similar")
	mergeAliasField("answer", "answer")
	mergeAliasField("condition_notes", "conditions")
	mergeAliasField("keywords", "keywords")
	mergeAliasField("tags", "tags")
	mergeAliasField("quick_replies", "quick_replies")
	return aliases
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

func splitFAQAny(raw any) []string {
	switch typed := raw.(type) {
	case nil:
		return nil
	case []string:
		return dedupeStrings(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(stringifyAny(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return dedupeStrings(out)
	default:
		return splitFAQVariants(stringifyAny(typed))
	}
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if strings.TrimSpace(stringifyAny(value)) != "" {
			return value
		}
	}
	return nil
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

func renderFAQDatasetAsJSON(dataset *canonicalFAQDataset) string {
	if dataset == nil {
		return ""
	}
	categoryIDs := map[string]string{}
	types := make([]map[string]any, 0, len(dataset.CategoryOrder))
	for _, category := range dataset.CategoryOrder {
		category = firstNonEmpty(strings.TrimSpace(category), "未分类")
		if _, ok := categoryIDs[category]; ok {
			continue
		}
		typeID := fmt.Sprintf("type-%02d", len(categoryIDs)+1)
		categoryIDs[category] = typeID
		types = append(types, map[string]any{"id": typeID, "category": category})
	}
	faq := make([]map[string]any, 0, len(dataset.Entries))
	for index, entry := range dataset.Entries {
		category := firstNonEmpty(strings.TrimSpace(entry.Category), "未分类")
		typeID := categoryIDs[category]
		if typeID == "" {
			typeID = fmt.Sprintf("type-%02d", len(categoryIDs)+1)
			categoryIDs[category] = typeID
			types = append(types, map[string]any{"id": typeID, "category": category})
		}
		faq = append(faq, map[string]any{
			"id":                firstNonEmpty(entry.ID, fmt.Sprintf("faq-%04d", index+1)),
			"type_id":           typeID,
			"category":          category,
			"question":          entry.Question,
			"similar_questions": entry.SimilarQuestions,
			"keywords":          entry.Keywords,
			"tags":              entry.Tags,
			"quick_replies":     entry.QuickReplies,
			"answer":            entry.Answer,
			"condition_notes":   entry.ConditionNotes,
		})
	}
	payload := map[string]any{
		"source_format":  dataset.Format,
		"source_family":  dataset.Family,
		"title":          dataset.TitleBase,
		"slug_base":      dataset.SlugBase,
		"raw_path":       dataset.RawPath,
		"types":          types,
		"faq":            faq,
		"operator_notes": dataset.Notes,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(raw) + "\n"
}

func renderFAQSegmentAsJSON(segment canonicalFAQSegment) string {
	entries := make([]map[string]any, 0, len(segment.Entries))
	for index, entry := range segment.Entries {
		entries = append(entries, map[string]any{
			"id":                firstNonEmpty(entry.ID, fmt.Sprintf("faq-%04d", index+1)),
			"category":          firstNonEmpty(strings.TrimSpace(entry.Category), "未分类"),
			"question":          entry.Question,
			"similar_questions": entry.SimilarQuestions,
			"keywords":          entry.Keywords,
			"answer":            entry.Answer,
			"condition_notes":   entry.ConditionNotes,
		})
	}
	payload := map[string]any{
		"source_format":    segment.Dataset.Format,
		"source_title":     segment.title(),
		"source_slug":      segment.slug(),
		"segment_index":    segment.Index,
		"segment_total":    segment.Total,
		"segment_category": firstNonEmpty(strings.TrimSpace(segment.Tag), "未分类"),
		"faq_entry_count":  len(segment.Entries),
		"faq":              entries,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(raw)
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
	withKeywords := 0
	for _, entry := range s.Entries {
		if len(entry.SimilarQuestions) > 0 {
			withSimilar++
		}
		if len(entry.ConditionNotes) > 0 {
			withConditions++
		}
		if len(entry.Keywords) > 0 {
			withKeywords++
		}
	}
	points := []string{
		fmt.Sprintf("当前分段包含 %d 条标准 FAQ，分类标签为 %s。", len(s.Entries), firstNonEmpty(strings.TrimSpace(s.Tag), "未分类")),
		fmt.Sprintf("其中 %d 条 FAQ 带有相似问法，已并入对应主问法结构。", withSimilar),
		"原始 answer 中的 HTML 标签已转为纯文本，便于检索、摘要和客服回答复用。",
	}
	if withKeywords > 0 {
		points = append(points, fmt.Sprintf("其中 %d 条 FAQ 带有关键词，已作为检索辅助信息保留。", withKeywords))
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
	return renderFAQEntriesSectionWithProfile(entries, nil, nil, nil, "")
}

func renderFAQEntriesSectionWithProfile(entries []canonicalFAQEntry, profile *knowledgeProfile, relatedConcepts []string, relatedEntities []string, sourceArchivePath string) string {
	if len(entries) == 0 {
		return "暂无 FAQ 条目。"
	}
	fields := profile.faqEntryFields()
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		heading := strings.TrimSpace(entry.Question)
		if strings.TrimSpace(entry.ID) != "" && containsString(fields, "id") {
			heading = strings.TrimSpace(entry.ID) + " · " + heading
		}
		block := []string{"### " + heading, ""}
		for _, field := range fields {
			switch field {
			case "id":
				if strings.TrimSpace(entry.ID) != "" {
					block = append(block, "- ID："+entry.ID)
				}
			case "question":
				block = append(block, "- 标准问法："+entry.Question)
			case "original_category":
				if strings.TrimSpace(entry.Category) != "" {
					block = append(block, "- 原始分类："+entry.Category)
				}
			case "similar_questions":
				appendFAQListField(&block, "相似问法", entry.SimilarQuestions)
			case "keywords":
				appendFAQListField(&block, "关键词", entry.Keywords)
			case "tags":
				appendFAQListField(&block, "标签", entry.Tags)
			case "quick_replies":
				appendFAQListField(&block, "快捷短语", entry.QuickReplies)
			case "answer":
				block = append(block, "", "#### 回复", "", firstNonEmpty(entry.Answer, "暂无标准回复。"))
			case "condition_notes":
				appendFAQListField(&block, "条件元数据", entry.ConditionNotes)
			case "related_concepts":
				appendFAQLinkField(&block, "相关概念", relatedConcepts)
			case "related_entities":
				appendFAQLinkField(&block, "相关实体", relatedEntities)
			case "source_archive":
				if strings.TrimSpace(sourceArchivePath) != "" {
					block = append(block, "- 来源归档："+sourceArchivePath)
				}
			}
		}
		parts = append(parts, strings.TrimRight(strings.Join(block, "\n"), "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func appendFAQListField(block *[]string, label string, items []string) {
	items = trimStringSlice(items, 0)
	if len(items) == 0 {
		return
	}
	*block = append(*block, "- "+label+"：")
	for _, item := range items {
		*block = append(*block, "  - "+item)
	}
}

func appendFAQLinkField(block *[]string, label string, slugs []string) {
	slugs = dedupeStrings(slugs)
	if len(slugs) == 0 {
		return
	}
	links := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		if strings.TrimSpace(slug) != "" {
			links = append(links, "[["+strings.TrimSpace(slug)+"]]")
		}
	}
	if len(links) > 0 {
		*block = append(*block, "- "+label+"："+strings.Join(links, "、"))
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
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
	case []string:
		return strings.Join(typed, "\n")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(stringifyAny(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", typed)
	}
}
