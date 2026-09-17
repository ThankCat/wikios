package service

import (
	"path/filepath"
	"regexp"
	"strings"

	"wikios/internal/retrieval"
	"wikios/internal/wikiadapter"
)

func appendCustomerEvidencePage(
	path string,
	question string,
	maxChars int,
	seenPaths map[string]bool,
	contentBlocks *[]string,
	sources *[]SourceRef,
	content string,
) (string, bool) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || seenPaths[path] || strings.TrimSpace(content) == "" {
		return "", false
	}
	displayTitle := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	body := strings.TrimSpace(content)
	if doc, err := wikiadapter.ParseDocument(content); err == nil {
		if title, _ := doc.Frontmatter["title"].(string); strings.TrimSpace(title) != "" {
			displayTitle = strings.TrimSpace(title)
		}
		if strings.TrimSpace(doc.Body) != "" {
			body = strings.TrimSpace(doc.Body)
		}
	}
	if maxChars <= 0 {
		maxChars = 2400
	}
	preview := buildCustomerEvidencePreview(body, path, question, maxChars)
	seenPaths[path] = true
	source := SourceRef{
		Path:       path,
		Title:      displayTitle,
		Confidence: customerSourceConfidence(path),
	}
	*contentBlocks = append(*contentBlocks, formatCandidatePageBlock(source, truncateForPrompt(preview, maxChars)))
	*sources = append(*sources, source)
	return body, true
}

func prioritizeCustomerRetrievedPages(pages []retrieval.RetrievedPage) []retrieval.RetrievedPage {
	return prioritizeCustomerRetrievedPagesForRouter(pages, nil)
}

func prioritizeCustomerRetrievedPagesForRouter(pages []retrieval.RetrievedPage, routerOutput *CustomerRouterOutput) []retrieval.RetrievedPage {
	out := append([]retrieval.RetrievedPage(nil), pages...)
	sortCustomerRetrievedPagesByProductFit(out, routerOutput, true)
	return out
}

func sortCustomerRetrievedPagesByProductFit(pages []retrieval.RetrievedPage, routerOutput *CustomerRouterOutput, useDirectoryRank bool) {
	for i := 0; i < len(pages)-1; i++ {
		for j := i + 1; j < len(pages); j++ {
			leftDirectoryRank := 0
			rightDirectoryRank := 0
			if useDirectoryRank {
				leftDirectoryRank = customerEvidenceDirectoryRank(pages[i].Path)
				rightDirectoryRank = customerEvidenceDirectoryRank(pages[j].Path)
			}
			leftProductPenalty := customerEvidenceProductMismatchPenalty(pages[i].Path, routerOutput)
			rightProductPenalty := customerEvidenceProductMismatchPenalty(pages[j].Path, routerOutput)
			if rightDirectoryRank < leftDirectoryRank ||
				(rightDirectoryRank == leftDirectoryRank && rightProductPenalty < leftProductPenalty) ||
				(useDirectoryRank && rightDirectoryRank == leftDirectoryRank && rightProductPenalty == leftProductPenalty && pages[j].Score > pages[i].Score) {
				pages[i], pages[j] = pages[j], pages[i]
			}
		}
	}
}

func customerEvidenceDirectoryRank(path string) int {
	path = filepath.ToSlash(strings.TrimSpace(path))
	switch {
	case strings.HasPrefix(path, "wiki/knowledge/"):
		return 0
	// Procedures share the top tier with knowledge: how-to pages are direct
	// answer evidence, so relevance score (not directory) must decide between a
	// canonical fact page and the matching step-by-step page. Otherwise a
	// loosely-relevant knowledge page out-ranks the procedure that actually
	// answers a "how do I ..." question and crowds it out of topK.
	case strings.HasPrefix(path, "wiki/procedures/"):
		return 0
	case strings.HasPrefix(path, "wiki/policies/"):
		return 1
	case strings.HasPrefix(path, "wiki/comparisons/"):
		return 3
	case strings.HasPrefix(path, "wiki/synthesis/"):
		return 4
	case strings.HasPrefix(path, "wiki/concepts/"):
		return 5
	case strings.HasPrefix(path, "wiki/entities/"):
		return 6
	case strings.HasPrefix(path, "wiki/intents/"):
		return 7
	default:
		return 99
	}
}

func customerEvidenceProductMismatchPenalty(path string, routerOutput *CustomerRouterOutput) int {
	target := customerEvidenceSinglePrimaryProduct(routerOutput)
	if target == "" {
		return 0
	}
	products := customerEvidenceProductsInPath(path)
	if len(products) == 0 || products[target] {
		return 0
	}
	return 1
}

func customerEvidenceSinglePrimaryProduct(routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil || routerOutput.Ambiguity.IsAmbiguous {
		return ""
	}
	for _, field := range routerOutput.Ambiguity.AmbiguousFields {
		if strings.TrimSpace(field) == "primary_product" || strings.TrimSpace(field) == "products" {
			return ""
		}
	}
	primary := strings.TrimSpace(routerOutput.Slots.PrimaryProduct)
	if primary == "" || primary == "unknown" {
		return ""
	}
	for _, product := range routerOutput.Slots.Products {
		product = strings.TrimSpace(product)
		if product != "" && product != "unknown" && product != primary {
			return ""
		}
	}
	return primary
}

func customerEvidenceProductsInPath(path string) map[string]bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	products := map[string]bool{}
	if strings.Contains(path, "static-ip") || strings.Contains(path, "static_ip") || strings.Contains(path, "shared-static") || strings.Contains(path, "dedicated-static") {
		products["static_ip"] = true
	}
	if strings.Contains(path, "dynamic-ip") || strings.Contains(path, "dynamic_ip") {
		products["dynamic_ip"] = true
	}
	if strings.Contains(path, "residential-ip") || strings.Contains(path, "residential_ip") || strings.Contains(path, "house") {
		products["residential_ip"] = true
	}
	if strings.Contains(path, "datacenter-ip") || strings.Contains(path, "datacenter_ip") || strings.Contains(path, "data-center-ip") || strings.Contains(path, "data_center_ip") {
		products["datacenter_ip"] = true
	}
	if strings.Contains(path, "overseas-ip") || strings.Contains(path, "overseas_ip") || strings.Contains(path, "product/os") {
		products["overseas_ip"] = true
	}
	return products
}

func buildCustomerEvidencePreview(body string, path string, question string, maxChars int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if maxChars <= 0 {
		maxChars = 2400
	}
	terms := customerEvidenceTerms(question)
	if len(terms) == 0 {
		return truncateForPrompt(body, maxChars)
	}
	parts := make([]string, 0, 2)
	if customerQuestionLooksForOperationURL(question) {
		if preview := customerEvidenceURLWindows(body, terms, 2); strings.TrimSpace(preview) != "" {
			parts = append(parts, preview)
		}
	}
	if preview := relevantTextWindows(body, terms, 2); strings.TrimSpace(preview) != "" {
		parts = append(parts, preview)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n---\n\n")
	}
	return truncateForPrompt(body, maxChars)
}

func relevantTextWindows(body string, terms []string, limit int) string {
	bodyRunes := []rune(body)
	lowerRunes := []rune(strings.ToLower(body))
	type hit struct {
		index int
		score int
	}
	hits := make([]hit, 0)
	for _, term := range terms {
		termRunes := []rune(strings.ToLower(strings.TrimSpace(term)))
		if len(termRunes) == 0 {
			continue
		}
		for _, index := range runeSearchIndices(lowerRunes, termRunes, 20) {
			hits = append(hits, hit{index: index, score: len([]rune(term))})
		}
	}
	if len(hits) == 0 {
		return ""
	}
	for i := 0; i < len(hits)-1; i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].score > hits[i].score {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	windows := make([]string, 0, len(hits))
	selected := make([]customerEvidenceWindow, 0, len(hits))
	for _, item := range hits {
		start := item.index - 600
		if start < 0 {
			start = 0
		}
		end := item.index + 900
		if end > len(bodyRunes) {
			end = len(bodyRunes)
		}
		if customerEvidenceWindowOverlaps(selected, start, end) {
			continue
		}
		selected = append(selected, customerEvidenceWindow{start: start, end: end})
		windows = append(windows, strings.TrimSpace(string(bodyRunes[start:end])))
		if limit > 0 && len(windows) >= limit {
			break
		}
	}
	return strings.Join(windows, "\n\n---\n\n")
}

type customerEvidenceWindow struct {
	start int
	end   int
}

func customerEvidenceWindowOverlaps(windows []customerEvidenceWindow, start int, end int) bool {
	for _, window := range windows {
		if start < window.end && end > window.start {
			return true
		}
	}
	return false
}

func runeSearchIndices(haystack []rune, needle []rune, limit int) []int {
	if len(haystack) == 0 || len(needle) == 0 || len(needle) > len(haystack) {
		return nil
	}
	out := make([]int, 0)
	for i := 0; i <= len(haystack)-len(needle); i++ {
		matched := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				matched = false
				break
			}
		}
		if matched {
			out = append(out, i)
			if limit > 0 && len(out) >= limit {
				return out
			}
		}
	}
	return out
}

func customerQuestionLooksForOperationURL(question string) bool {
	question = strings.ToLower(strings.TrimSpace(question))
	if question == "" {
		return false
	}
	for _, marker := range []string{
		"入口", "链接", "网址", "哪里", "哪儿", "地址", "页面",
		"购买", "怎么买", "下单", "开通", "试用", "测试", "领取", "下载",
		"切换", "换ip", "换 ip", "更换", "设置", "配置", "白名单", "api", "socks",
	} {
		if strings.Contains(question, marker) {
			return true
		}
	}
	return false
}

func customerEvidenceURLWindows(body string, terms []string, limit int) string {
	lines := strings.Split(body, "\n")
	type candidate struct {
		start int
		end   int
		score int
	}
	candidates := make([]candidate, 0)
	for index, line := range lines {
		if !strings.Contains(line, "http://") && !strings.Contains(line, "https://") {
			continue
		}
		start := index - 1
		if start < 0 {
			start = 0
		}
		end := index + 2
		if end > len(lines) {
			end = len(lines)
		}
		context := strings.ToLower(strings.Join(lines[start:end], "\n"))
		score := 1
		for _, term := range terms {
			term = strings.ToLower(strings.TrimSpace(term))
			if term == "" {
				continue
			}
			if strings.Contains(context, term) {
				score += len([]rune(term))
			}
		}
		candidates = append(candidates, candidate{start: start, end: end, score: score})
	}
	if len(candidates) == 0 {
		return ""
	}
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score ||
				(candidates[j].score == candidates[i].score && candidates[j].start < candidates[i].start) {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	windows := make([]string, 0, len(candidates))
	selected := make([]customerEvidenceWindow, 0, len(candidates))
	for _, item := range candidates {
		if customerEvidenceWindowOverlaps(selected, item.start, item.end) {
			continue
		}
		selected = append(selected, customerEvidenceWindow{start: item.start, end: item.end})
		windows = append(windows, strings.TrimSpace(strings.Join(lines[item.start:item.end], "\n")))
		if limit > 0 && len(windows) >= limit {
			break
		}
	}
	return strings.Join(windows, "\n\n---\n\n")
}

func customerEvidenceTerms(question string) []string {
	normalized := strings.ToLower(strings.TrimSpace(question))
	if normalized == "" {
		return nil
	}
	seen := map[string]bool{}
	terms := make([]string, 0)
	add := func(term string) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" || seen[term] {
			return
		}
		if len([]rune(term)) < 2 {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, chunk := range splitSearchChunks(normalized) {
		add(chunk)
		runes := []rune(chunk)
		for size := 4; size >= 2; size-- {
			if len(runes) < size {
				continue
			}
			for i := 0; i <= len(runes)-size; i++ {
				add(string(runes[i : i+size]))
			}
		}
	}
	return terms
}

func splitSearchChunks(text string) []string {
	chunks := make([]string, 0)
	var current []rune
	lastKind := 0
	flush := func() {
		if len(current) > 0 {
			chunks = append(chunks, string(current))
			current = nil
		}
		lastKind = 0
	}
	for _, r := range text {
		kind := customerSearchRuneKind(r)
		if kind == 0 {
			flush()
			continue
		}
		if lastKind != 0 && kind != lastKind {
			flush()
		}
		current = append(current, r)
		lastKind = kind
	}
	flush()
	return chunks
}

func customerSearchRuneKind(r rune) int {
	switch {
	case r >= '\u4e00' && r <= '\u9fff':
		return 1
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return 2
	default:
		return 0
	}
}

func customerConversationExcerpt(req CustomerChatRequest) []string {
	lines := make([]string, 0, len(req.History)+1)
	for _, item := range req.History {
		content := strings.TrimSpace(item.Content)
		role := strings.TrimSpace(item.Role)
		if content == "" || role == "" {
			continue
		}
		prefix := role
		if item.CreatedAt != "" {
			prefix = item.CreatedAt + " " + role
		}
		lines = append(lines, prefix+": "+truncateForPrompt(content, 240))
	}
	if strings.TrimSpace(req.Question) != "" {
		prefix := "user"
		if req.QuestionCreatedAt != "" {
			prefix = req.QuestionCreatedAt + " user"
		}
		lines = append(lines, prefix+": "+truncateForPrompt(req.Question, 240))
	}
	return lines
}

var customerLogSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)sk-[a-z0-9_\-]{8,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|password|secret|token)\s*[:=]\s*["']?[^"'\s,;]+`),
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b1[3-9]\d{9}\b`),
}

func containsAny(text string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

func isCustomerReadableEvidence(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !strings.HasPrefix(path, "wiki/") || !strings.HasSuffix(path, ".md") {
		return false
	}
	if strings.HasPrefix(path, "wiki/unconfirmed/") ||
		strings.HasPrefix(path, "wiki/forbidden/") ||
		strings.HasPrefix(path, "wiki/sources/") ||
		strings.HasPrefix(path, "wiki/templates/") {
		return false
	}
	return customerEvidenceDirectoryRank(path) < 99
}

func customerSourceConfidence(path string) string {
	path = filepath.ToSlash(path)
	switch {
	case strings.HasPrefix(path, "wiki/knowledge/"),
		strings.HasPrefix(path, "wiki/policies/"),
		strings.HasPrefix(path, "wiki/procedures/"),
		strings.HasPrefix(path, "wiki/comparisons/"),
		strings.HasPrefix(path, "wiki/synthesis/"):
		return "high"
	case strings.HasPrefix(path, "wiki/concepts/"),
		strings.HasPrefix(path, "wiki/entities/"):
		return "medium"
	default:
		return "low"
	}
}

