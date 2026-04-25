package service

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type knowledgeProfile struct {
	Version           int                        `yaml:"version" json:"version,omitempty"`
	Name              string                     `yaml:"name" json:"name,omitempty"`
	DisplayName       string                     `yaml:"display_name" json:"display_name,omitempty"`
	PublicPersona     string                     `yaml:"public_persona" json:"public_persona,omitempty"`
	InputAdapters     knowledgeInputAdapters     `yaml:"input_adapters" json:"input_adapters,omitempty"`
	FAQTaxonomy       faqTaxonomyProfile         `yaml:"faq_taxonomy" json:"faq_taxonomy,omitempty"`
	KnowledgeSeeds    knowledgeProfileSeedGroups `yaml:"knowledge_seeds" json:"knowledge_seeds,omitempty"`
	WikiWriteContract wikiWriteContractProfile   `yaml:"wiki_write_contract" json:"wiki_write_contract,omitempty"`
	QualityGates      qualityGatesProfile        `yaml:"quality_gates" json:"quality_gates,omitempty"`
}

type knowledgeInputAdapters struct {
	FAQXLSX          faqInputAdapterProfile `yaml:"faq_xlsx" json:"faq_xlsx,omitempty"`
	FAQMarkdownTable faqInputAdapterProfile `yaml:"faq_markdown_table" json:"faq_markdown_table,omitempty"`
}

type faqInputAdapterProfile struct {
	RequiredFields map[string][]string `yaml:"required_fields" json:"required_fields,omitempty"`
	OptionalFields map[string][]string `yaml:"optional_fields" json:"optional_fields,omitempty"`
}

type faqTaxonomyProfile struct {
	MaxEntriesPerCategory int                           `yaml:"max_entries_per_category" json:"max_entries_per_category,omitempty"`
	ReviewSlug            string                        `yaml:"review_slug" json:"review_slug,omitempty"`
	ReviewTitle           string                        `yaml:"review_title" json:"review_title,omitempty"`
	GenericCategoryNames  []string                      `yaml:"generic_category_names" json:"generic_category_names,omitempty"`
	CategoryHints         []knowledgeProfileFAQCategory `yaml:"category_hints" json:"category_hints,omitempty"`
}

type knowledgeProfileFAQCategory struct {
	Title       string   `yaml:"title" json:"title"`
	Slug        string   `yaml:"slug" json:"slug"`
	Aliases     []string `yaml:"aliases" json:"aliases,omitempty"`
	Keywords    []string `yaml:"keywords" json:"keywords,omitempty"`
	Description string   `yaml:"description" json:"description,omitempty"`
}

type knowledgeProfileSeedGroups struct {
	Concepts []knowledgeProfileConceptSeed `yaml:"concepts" json:"concepts,omitempty"`
	Entities []knowledgeProfileEntitySeed  `yaml:"entities" json:"entities,omitempty"`
}

type knowledgeProfileConceptSeed struct {
	Title          string   `yaml:"title" json:"title"`
	Slug           string   `yaml:"slug" json:"slug"`
	EnglishName    string   `yaml:"english_name" json:"english_name,omitempty"`
	Aliases        []string `yaml:"aliases" json:"aliases,omitempty"`
	Definition     string   `yaml:"definition" json:"definition,omitempty"`
	KeyPoints      []string `yaml:"key_points" json:"key_points,omitempty"`
	Contradictions []string `yaml:"contradictions" json:"contradictions,omitempty"`
}

type knowledgeProfileEntitySeed struct {
	Title            string   `yaml:"title" json:"title"`
	Slug             string   `yaml:"slug" json:"slug"`
	EntityType       string   `yaml:"entity_type" json:"entity_type,omitempty"`
	Aliases          []string `yaml:"aliases" json:"aliases,omitempty"`
	Description      string   `yaml:"description" json:"description,omitempty"`
	KeyContributions []string `yaml:"key_contributions" json:"key_contributions,omitempty"`
}

type wikiWriteContractProfile struct {
	FAQEntryFields              []string `yaml:"faq_entry_fields" json:"faq_entry_fields,omitempty"`
	PreserveOperationalMetadata bool     `yaml:"preserve_operational_metadata" json:"preserve_operational_metadata,omitempty"`
	ForbidCustomerToneInWiki    bool     `yaml:"forbid_customer_tone_in_wiki" json:"forbid_customer_tone_in_wiki,omitempty"`
}

type qualityGatesProfile struct {
	MaxUngroupedEntries       int  `yaml:"max_ungrouped_entries" json:"max_ungrouped_entries,omitempty"`
	BlockLargeGenericCategory bool `yaml:"block_large_generic_category" json:"block_large_generic_category,omitempty"`
	RequireSourceArchive      bool `yaml:"require_source_archive" json:"require_source_archive,omitempty"`
	RequireFAQIndex           bool `yaml:"require_faq_index" json:"require_faq_index,omitempty"`
}

func (s *baseService) loadKnowledgeProfile() (*knowledgeProfile, []string) {
	if s == nil || s.deps.Config == nil {
		return nil, nil
	}
	path := strings.TrimSpace(s.deps.Config.KnowledgeProfile.Path)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("知识 profile 读取失败：%s", err.Error())}
	}
	var profile knowledgeProfile
	if err := yaml.Unmarshal(raw, &profile); err != nil {
		return nil, []string{fmt.Sprintf("知识 profile 解析失败：%s", err.Error())}
	}
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = strings.TrimSpace(s.deps.Config.KnowledgeProfile.Name)
	}
	profile.normalize()
	return &profile, profile.validate()
}

func (p *knowledgeProfile) normalize() {
	if p == nil {
		return
	}
	if p.Version == 0 {
		p.Version = 1
	}
	if p.FAQTaxonomy.MaxEntriesPerCategory <= 0 {
		p.FAQTaxonomy.MaxEntriesPerCategory = 60
	}
	if strings.TrimSpace(p.FAQTaxonomy.ReviewSlug) == "" {
		p.FAQTaxonomy.ReviewSlug = "faq-needs-human-taxonomy-review"
	}
	if strings.TrimSpace(p.FAQTaxonomy.ReviewTitle) == "" {
		p.FAQTaxonomy.ReviewTitle = "待人工复核 FAQ"
	}
	p.FAQTaxonomy.GenericCategoryNames = dedupeStrings(append(p.FAQTaxonomy.GenericCategoryNames, defaultGenericFAQCategoryNames()...))
	if len(p.WikiWriteContract.FAQEntryFields) == 0 {
		p.WikiWriteContract.FAQEntryFields = defaultFAQEntryContractFields()
	}
	if p.QualityGates.MaxUngroupedEntries <= 0 {
		p.QualityGates.MaxUngroupedEntries = 10
	}
	p.InputAdapters.FAQXLSX.normalize()
	p.InputAdapters.FAQMarkdownTable.normalize()
}

func (p *knowledgeProfile) validate() []string {
	if p == nil {
		return nil
	}
	warnings := []string{}
	if p.Version != 1 {
		warnings = append(warnings, fmt.Sprintf("知识 profile version=%d 当前按 v1 兼容处理。", p.Version))
	}
	seenSlugs := map[string]bool{}
	for _, hint := range p.FAQTaxonomy.CategoryHints {
		slug := strings.TrimSpace(hint.Slug)
		if slug == "" {
			warnings = append(warnings, "知识 profile 中存在缺少 slug 的 FAQ 分类。")
			continue
		}
		if !isUsableFAQSlug(slug) {
			warnings = append(warnings, fmt.Sprintf("知识 profile FAQ 分类 slug 不可用：%s", slug))
		}
		if seenSlugs[slug] {
			warnings = append(warnings, fmt.Sprintf("知识 profile FAQ 分类 slug 重复：%s", slug))
		}
		seenSlugs[slug] = true
	}
	for _, field := range p.WikiWriteContract.FAQEntryFields {
		if !validFAQEntryContractFields()[field] {
			warnings = append(warnings, fmt.Sprintf("知识 profile FAQ 写入字段不支持：%s", field))
		}
	}
	return dedupeStrings(warnings)
}

func (p *faqInputAdapterProfile) normalize() {
	if p.RequiredFields == nil {
		p.RequiredFields = map[string][]string{}
	}
	if p.OptionalFields == nil {
		p.OptionalFields = map[string][]string{}
	}
	mergeFieldAliases(p.RequiredFields, "question", []string{"标准问题", "标准问法", "问题", "question"})
	mergeFieldAliases(p.RequiredFields, "answer", []string{"回复内容", "回复", "答案", "answer", "answer_text"})
	mergeFieldAliases(p.OptionalFields, "original_category", []string{"技能分类", "分类", "category", "type", "问题分类"})
	mergeFieldAliases(p.OptionalFields, "similar_questions", []string{"相似问法", "相似问题", "同义问法", "similar", "similar_questions"})
	mergeFieldAliases(p.OptionalFields, "keywords", []string{"关键词", "关键字", "keywords", "keyword"})
	mergeFieldAliases(p.OptionalFields, "tags", []string{"标签", "标记", "tags", "tag", "labels"})
	mergeFieldAliases(p.OptionalFields, "quick_replies", []string{"快捷短语", "快捷回复", "快捷指令", "quick_replies", "quick_reply", "shortcuts"})
	mergeFieldAliases(p.OptionalFields, "condition_notes", []string{"命中条件", "条件", "conditions", "condition_template"})
}

func mergeFieldAliases(target map[string][]string, field string, defaults []string) {
	target[field] = dedupeStrings(append(target[field], defaults...))
}

func defaultFAQEntryContractFields() []string {
	return []string{"id", "question", "original_category", "similar_questions", "keywords", "answer", "condition_notes", "related_concepts", "related_entities"}
}

func validFAQEntryContractFields() map[string]bool {
	return map[string]bool{
		"id":                true,
		"question":          true,
		"original_category": true,
		"similar_questions": true,
		"keywords":          true,
		"tags":              true,
		"quick_replies":     true,
		"answer":            true,
		"condition_notes":   true,
		"related_concepts":  true,
		"related_entities":  true,
		"source_archive":    true,
	}
}

func (p *knowledgeProfile) promptJSON() string {
	if p == nil {
		return "{}"
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func (p *knowledgeProfile) profileName() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(p.Name, p.DisplayName))
}

func profileNameForReport(p *knowledgeProfile) string {
	if p == nil {
		return ""
	}
	return p.profileName()
}

func (p *knowledgeProfile) maxFAQEntriesPerCategory() int {
	if p == nil || p.FAQTaxonomy.MaxEntriesPerCategory <= 0 {
		return 80
	}
	return p.FAQTaxonomy.MaxEntriesPerCategory
}

func (p *knowledgeProfile) faqEntryFields() []string {
	if p == nil {
		return defaultFAQEntryContractFields()
	}
	fields := []string{}
	valid := validFAQEntryContractFields()
	for _, field := range p.WikiWriteContract.FAQEntryFields {
		field = strings.TrimSpace(field)
		if field != "" && valid[field] {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return defaultFAQEntryContractFields()
	}
	return fields
}

func (p *knowledgeProfile) reviewCategory() knowledgeProfileFAQCategory {
	title := "待人工复核 FAQ"
	slug := "faq-needs-human-taxonomy-review"
	if p != nil {
		title = firstNonEmpty(p.FAQTaxonomy.ReviewTitle, title)
		slug = firstNonEmpty(p.FAQTaxonomy.ReviewSlug, slug)
	}
	return knowledgeProfileFAQCategory{Title: title, Slug: slug}
}

func (p *knowledgeProfile) categoryHintBySlug(slug string) (knowledgeProfileFAQCategory, bool) {
	slug = strings.TrimSpace(slug)
	if p == nil || slug == "" {
		return knowledgeProfileFAQCategory{}, false
	}
	for _, hint := range p.FAQTaxonomy.CategoryHints {
		if strings.TrimSpace(hint.Slug) == slug || faqPageSlug(strings.TrimSpace(hint.Slug)) == slug {
			return hint, true
		}
	}
	return knowledgeProfileFAQCategory{}, false
}

func (p *knowledgeProfile) categoryHintByName(text string) (knowledgeProfileFAQCategory, bool) {
	key := normalizeFAQCategoryKey(text)
	if p == nil || key == "" {
		return knowledgeProfileFAQCategory{}, false
	}
	for _, hint := range p.FAQTaxonomy.CategoryHints {
		if normalizeFAQCategoryKey(hint.Title) == key {
			return hint, true
		}
		for _, alias := range hint.Aliases {
			if normalizeFAQCategoryKey(alias) == key {
				return hint, true
			}
		}
	}
	return knowledgeProfileFAQCategory{}, false
}

func (p *knowledgeProfile) matchCategory(entry canonicalFAQEntry) (knowledgeProfileFAQCategory, bool) {
	if p == nil {
		return knowledgeProfileFAQCategory{}, false
	}
	entryText := strings.ToLower(strings.Join([]string{
		entry.Category,
		entry.Question,
		strings.Join(entry.SimilarQuestions, " "),
		strings.Join(entry.Keywords, " "),
		strings.Join(entry.Tags, " "),
		strings.Join(entry.QuickReplies, " "),
		entry.Answer,
	}, "\n"))
	bestScore := 0
	var best knowledgeProfileFAQCategory
	for _, hint := range p.FAQTaxonomy.CategoryHints {
		score := 0
		if normalizeFAQCategoryKey(entry.Category) != "" {
			if normalizeFAQCategoryKey(entry.Category) == normalizeFAQCategoryKey(hint.Title) {
				score += 8
			}
			for _, alias := range hint.Aliases {
				if normalizeFAQCategoryKey(entry.Category) == normalizeFAQCategoryKey(alias) {
					score += 8
				}
			}
		}
		for _, alias := range hint.Aliases {
			if termMatchesText(entryText, alias) {
				score += 4
			}
		}
		for _, keyword := range hint.Keywords {
			if termMatchesText(entryText, keyword) {
				score += 3
			}
		}
		if score > bestScore {
			bestScore = score
			best = hint
		}
	}
	return best, bestScore > 0
}

func (p *knowledgeProfile) isGenericCategory(text string) bool {
	return isGenericFAQCategoryName(text, p.genericCategoryNames())
}

func (p *knowledgeProfile) genericCategoryNames() []string {
	if p == nil {
		return defaultGenericFAQCategoryNames()
	}
	return dedupeStrings(append(p.FAQTaxonomy.GenericCategoryNames, defaultGenericFAQCategoryNames()...))
}

func defaultGenericFAQCategoryNames() []string {
	return []string{"faq", "FAQ", "常见问题", "通用问题", "其它", "其他", "未分类", "默认分类", "general"}
}

func isGenericFAQCategoryName(text string, names []string) bool {
	key := normalizeFAQCategoryKey(text)
	if key == "" {
		return true
	}
	for _, name := range names {
		if key == normalizeFAQCategoryKey(name) {
			return true
		}
	}
	return false
}

func termMatchesText(text string, term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return false
	}
	return strings.Contains(text, term)
}

func (p *knowledgeProfile) inferKnowledgeCandidates(category string, entries []canonicalFAQEntry) ([]ingestConceptItem, []ingestEntityItem) {
	if p == nil {
		return nil, nil
	}
	textParts := []string{category}
	for _, entry := range entries {
		textParts = append(textParts,
			entry.Category,
			entry.Question,
			strings.Join(entry.SimilarQuestions, " "),
			strings.Join(entry.Keywords, " "),
			strings.Join(entry.Tags, " "),
			strings.Join(entry.QuickReplies, " "),
			entry.Answer,
		)
	}
	text := strings.ToLower(strings.Join(textParts, "\n"))
	concepts := []ingestConceptItem{}
	for _, seed := range p.KnowledgeSeeds.Concepts {
		if faqSeedMatches(text, seed.Title, seed.EnglishName, seed.Aliases) {
			concepts = append(concepts, ingestConceptItem{
				Title:          seed.Title,
				Slug:           seed.Slug,
				EnglishName:    seed.EnglishName,
				Aliases:        seed.Aliases,
				Definition:     seed.Definition,
				KeyPoints:      seed.KeyPoints,
				Contradictions: seed.Contradictions,
			})
		}
	}
	entities := []ingestEntityItem{}
	for _, seed := range p.KnowledgeSeeds.Entities {
		if faqSeedMatches(text, seed.Title, "", seed.Aliases) {
			entities = append(entities, ingestEntityItem{
				Title:            seed.Title,
				Slug:             seed.Slug,
				EntityType:       seed.EntityType,
				Aliases:          seed.Aliases,
				Description:      seed.Description,
				KeyContributions: seed.KeyContributions,
			})
		}
	}
	return filterFAQConceptItems(concepts), filterFAQEntityItems(entities)
}

func faqSeedMatches(text string, title string, englishName string, aliases []string) bool {
	for _, term := range append([]string{title, englishName}, aliases...) {
		if termMatchesText(text, term) {
			return true
		}
	}
	return false
}
