package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
)

func TestFAQClassificationManifestIsCompleteAndNotSegmentJSON(t *testing.T) {
	dataset := &canonicalFAQDataset{
		Format:    "faq-xlsx",
		TitleBase: "知识库问答整理",
		Entries: []canonicalFAQEntry{
			{ID: "faq-1", Category: "下载与安装", Question: "怎么下载", SimilarQuestions: []string{"下载地址在哪"}, Keywords: []string{"下载"}, Tags: []string{"安装"}, QuickReplies: []string{"Windows安装"}, Answer: strings.Repeat("下载说明", 120)},
			{ID: "faq-2", Category: "产品咨询", Question: "静态IP适合什么场景", Keywords: []string{"静态ip"}, Answer: "静态IP适合长期稳定网络环境。"},
		},
	}
	manifest, estimatedTokens, err := renderFAQClassificationManifest(canonicalFAQSegment{Dataset: dataset, Entries: dataset.Entries})
	if err != nil {
		t.Fatalf("render manifest: %v", err)
	}
	for _, id := range []string{"faq-1", "faq-2"} {
		if !strings.Contains(manifest, id) {
			t.Fatalf("expected manifest to include %s, got %s", id, manifest)
		}
	}
	if strings.Contains(manifest, "source_slug") || strings.Contains(manifest, "segment_index") {
		t.Fatalf("classification manifest should not use segment JSON shape: %s", manifest)
	}
	if strings.Contains(manifest, strings.Repeat("下载说明", 80)) {
		t.Fatalf("answer should be summarized in manifest, got %s", manifest)
	}
	if estimatedTokens <= 0 {
		t.Fatalf("expected estimated tokens, got %d", estimatedTokens)
	}
}

func TestStableFAQCategorySlugDoesNotFallBackToFAQOutput(t *testing.T) {
	cases := []string{"产品咨询", "下载与安装", "完全新的中文分类", "常见问题"}
	for _, category := range cases {
		slug := stableFAQCategorySlug(category, category+" FAQ", "")
		if slug == "faq-output" || slug == "faq-page" || slug == "faq-faq" || slug == "" {
			t.Fatalf("unexpected slug for %q: %q", category, slug)
		}
		if !strings.HasPrefix(slug, "faq-") {
			t.Fatalf("expected faq prefix for %q, got %q", category, slug)
		}
	}
	if got := stableFAQCategorySlug("产品咨询", "产品咨询 FAQ", "faq-product-consulting"); got != "faq-product-consulting" {
		t.Fatalf("expected valid LLM slug to win, got %q", got)
	}
}

func TestFallbackFAQClassificationUsesProfileAndRejectsGenericFAQBucket(t *testing.T) {
	dataset := &canonicalFAQDataset{Format: "faq-json", TitleBase: "FAQ", Entries: []canonicalFAQEntry{
		{ID: "faq-1", Category: "常见问题", Question: "怎么登录微信", Keywords: []string{"微信", "登录"}, Answer: "不支持相关场景。"},
		{ID: "faq-2", Category: "常见问题", Question: "怎么收费", Keywords: []string{"价格"}, Answer: "按套餐收费。"},
	}}
	profile := &knowledgeProfile{
		FAQTaxonomy: faqTaxonomyProfile{CategoryHints: []knowledgeProfileFAQCategory{
			{Title: "账号登录", Slug: "faq-account-login", Keywords: []string{"登录", "微信"}},
			{Title: "价格购买", Slug: "faq-pricing-purchase", Keywords: []string{"价格", "收费"}},
		}},
	}
	profile.normalize()
	out := fallbackFAQClassificationBatch(canonicalFAQSegment{Dataset: dataset, Entries: dataset.Entries}, profile, "timeout")
	if len(out.Categories) != 2 {
		t.Fatalf("expected semantic profile fallback categories, got %+v", out.Categories)
	}
	slugs := strings.Join(faqClassificationSlugs(out.Categories), "\n")
	if strings.Contains(slugs, "faq-faq") || strings.Contains(slugs, "faq-general") {
		t.Fatalf("expected no generic faq bucket, got %s", slugs)
	}
	if !strings.Contains(slugs, "faq-account-login") || !strings.Contains(slugs, "faq-pricing-purchase") {
		t.Fatalf("expected profile slugs, got %s", slugs)
	}
}

func TestFAQKnowledgeCandidatesComeFromProfileOnly(t *testing.T) {
	concepts, entities := inferFAQKnowledgeCandidates("产品", []canonicalFAQEntry{{Question: "微信怎么登录", Answer: "测试"}}, nil)
	if len(concepts) != 0 || len(entities) != 0 {
		t.Fatalf("expected no hardcoded candidates without profile, got concepts=%+v entities=%+v", concepts, entities)
	}
	profile := &knowledgeProfile{KnowledgeSeeds: knowledgeProfileSeedGroups{Entities: []knowledgeProfileEntitySeed{
		{Title: "微信", Slug: "wechat", Aliases: []string{"微信"}},
	}}}
	profile.normalize()
	_, entities = inferFAQKnowledgeCandidates("产品", []canonicalFAQEntry{{Question: "微信怎么登录", Answer: "测试"}}, profile)
	if len(entities) != 1 || entities[0].Slug != "wechat" {
		t.Fatalf("expected profile entity candidate, got %+v", entities)
	}
}

func TestFAQEntryRenderingFollowsProfileContract(t *testing.T) {
	profile := &knowledgeProfile{WikiWriteContract: wikiWriteContractProfile{FAQEntryFields: []string{
		"id", "question", "keywords", "answer", "related_concepts", "related_entities",
	}}}
	profile.normalize()
	out := renderFAQEntriesSectionWithProfile([]canonicalFAQEntry{{
		ID:       "faq-0001",
		Category: "原始分类",
		Question: "怎么购买",
		Keywords: []string{"价格", "购买"},
		Answer:   "访问官网购买。",
	}}, profile, []string{"pricing-plan"}, []string{"siyetian"}, "wiki/sources/source.md")
	for _, required := range []string{
		"### faq-0001 · 怎么购买",
		"- ID：faq-0001",
		"- 标准问法：怎么购买",
		"- 关键词：",
		"#### 回复",
		"访问官网购买。",
		"- 相关概念：[[pricing-plan]]",
		"- 相关实体：[[siyetian]]",
	} {
		if !strings.Contains(out, required) {
			t.Fatalf("expected %q in rendered FAQ entry:\n%s", required, out)
		}
	}
	if strings.Contains(out, "原始分类") {
		t.Fatalf("profile contract omitted original_category but output included it:\n%s", out)
	}
}

func TestFAQXLSXParserUsesProfileFieldAliases(t *testing.T) {
	raw := buildTestFAQXLSX(t, [][]string{
		{"业务线", "客户问题", "标准答案", "同问", "检索词"},
		{"购买", "怎么买", "官网购买。", "如何购买", "价格"},
	})
	profile := &knowledgeProfile{InputAdapters: knowledgeInputAdapters{FAQXLSX: faqInputAdapterProfile{
		RequiredFields: map[string][]string{
			"question": {"客户问题"},
			"answer":   {"标准答案"},
		},
		OptionalFields: map[string][]string{
			"original_category": {"业务线"},
			"similar_questions": {"同问"},
			"keywords":          {"检索词"},
		},
	}}}
	profile.normalize()
	_, dataset, err := parseFAQXLSXDatasetWithProfile("faq.xlsx", "FAQ", raw, profile)
	if err != nil {
		t.Fatalf("parse xlsx with profile aliases: %v", err)
	}
	if dataset == nil || len(dataset.Entries) != 1 {
		t.Fatalf("expected one entry, got %+v", dataset)
	}
	entry := dataset.Entries[0]
	if entry.Category != "购买" || entry.Question != "怎么买" || entry.Answer != "官网购买。" || !containsString(entry.Keywords, "价格") {
		t.Fatalf("profile aliases not applied, got %+v", entry)
	}
}

func TestFAQQualityGateBlocksLargeReviewBucket(t *testing.T) {
	profile := &knowledgeProfile{
		FAQTaxonomy:  faqTaxonomyProfile{ReviewSlug: "faq-needs-human-taxonomy-review", ReviewTitle: "待人工复核 FAQ"},
		QualityGates: qualityGatesProfile{BlockLargeGenericCategory: true, MaxUngroupedEntries: 1},
	}
	profile.normalize()
	groups := map[string]*faqCategoryGroup{
		"faq-needs-human-taxonomy-review": {
			Title:   "待人工复核 FAQ",
			Slug:    "faq-needs-human-taxonomy-review",
			Entries: []canonicalFAQEntry{{ID: "faq-1"}, {ID: "faq-2"}},
		},
	}
	_, err := validateFAQQualityGates(groups, []string{"faq-needs-human-taxonomy-review"}, profile)
	if err == nil {
		t.Fatalf("expected quality gate to block large review bucket")
	}
}

func TestPreIntentFAQEntriesAreSeparated(t *testing.T) {
	publicEntries, preIntent := splitPublicFAQEntries([]canonicalFAQEntry{
		{ID: "faq-1", Category: "开场问候", Question: "你好", Answer: "您好，请问有什么可以帮您？"},
		{ID: "faq-2", Category: "产品咨询", Question: "动态IP多久换一次", Answer: "请按套餐说明确认。"},
	})
	if len(publicEntries) != 1 || publicEntries[0].ID != "faq-2" {
		t.Fatalf("unexpected public entries: %+v", publicEntries)
	}
	if len(preIntent) != 1 || preIntent[0].ID != "faq-1" {
		t.Fatalf("unexpected pre-intent entries: %+v", preIntent)
	}
}

func TestPromptLoadsOnlyRequestedWikiAgentSections(t *testing.T) {
	root := t.TempDir()
	promptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(promptDir, "test.md"), []byte("base prompt"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	agent := `# AGENT

## INGEST 操作规范

ingest rules

## LINT 操作规范

lint rules

## REPAIR 操作规范

repair rules
`
	if err := os.WriteFile(filepath.Join(root, "AGENT.md"), []byte(agent), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	svc := baseService{deps: Deps{
		Config:    &config.Config{MountedWiki: config.MountedWikiConfig{Root: root}},
		PromptDir: promptDir,
	}}
	prompt, err := svc.loadPromptWithWikiSections("test.md", "INGEST 相关规则", "## INGEST 操作规范")
	if err != nil {
		t.Fatalf("load prompt: %v", err)
	}
	if !strings.Contains(prompt, "ingest rules") {
		t.Fatalf("expected ingest rules, got %s", prompt)
	}
	if strings.Contains(prompt, "lint rules") || strings.Contains(prompt, "repair rules") {
		t.Fatalf("unexpected unrelated rules in prompt: %s", prompt)
	}
}
