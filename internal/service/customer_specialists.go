package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/wikiadapter"
)

type CustomerSpecialistProfile struct {
	Name             string   `json:"name"`
	PromptFile       string   `json:"prompt_file,omitempty"`
	AllowedPrefixes  []string `json:"allowed_prefixes"`
	CandidateTopK    int      `json:"candidate_top_k,omitempty"`
	MaxEvidenceChars int      `json:"max_evidence_chars,omitempty"`
}

type customerSpecialistEvidenceResult struct {
	Profile        CustomerSpecialistProfile
	Queries        []string
	Candidates     []retrieval.RetrievedPage
	Sources        []SourceRef
	ContentBlocks  []string
	EvidenceBodies map[string]string
	EvidenceTrace  []map[string]any
	CacheTrace     customerSpecialistCacheTrace
	Error          string
}

type customerSpecialistCacheTrace struct {
	QMDHits                    int
	QMDMisses                  int
	ReadPageHits               int
	ReadPageMisses             int
	ExecutedRetrievalQueries   []string
	AttemptedRetrievalQueries  []string
	SkippedRetrievalQueries    []string
	SkippedRetrievalQueryCount int
	RetrievalResults           []map[string]any
	ScopeFilteredPages         []map[string]any
	WikilinkExpandedPages      []map[string]any
	RetrievalTimings           []map[string]any
	ReadPageTimings            []map[string]any
}

func (trace customerSpecialistCacheTrace) summary() map[string]any {
	return map[string]any{
		"qmd_cache_hits":                  trace.QMDHits,
		"qmd_cache_misses":                trace.QMDMisses,
		"read_page_cache_hits":            trace.ReadPageHits,
		"read_page_cache_misses":          trace.ReadPageMisses,
		"executed_retrieval_query_count":  len(trace.ExecutedRetrievalQueries),
		"executed_retrieval_queries":      append([]string(nil), trace.ExecutedRetrievalQueries...),
		"attempted_retrieval_query_count": len(trace.AttemptedRetrievalQueries),
		"attempted_retrieval_queries":     append([]string(nil), trace.AttemptedRetrievalQueries...),
		"skipped_retrieval_query_count":   trace.SkippedRetrievalQueryCount,
		"skipped_retrieval_queries":       append([]string(nil), trace.SkippedRetrievalQueries...),
		"retrieval_results":               append([]map[string]any(nil), trace.RetrievalResults...),
		"scope_filtered_pages":            append([]map[string]any(nil), trace.ScopeFilteredPages...),
		"wikilink_expanded_pages":         append([]map[string]any(nil), trace.WikilinkExpandedPages...),
		"retrieval_timings":               append([]map[string]any(nil), trace.RetrievalTimings...),
		"read_page_timings":               append([]map[string]any(nil), trace.ReadPageTimings...),
	}
}

var customerSpecialistProfiles = map[string]CustomerSpecialistProfile{
	"reception": {
		Name:             "reception",
		PromptFile:       "customer_specialist_reception.md",
		AllowedPrefixes:  []string{"wiki/policies/", "wiki/synthesis/", "wiki/intents/"},
		CandidateTopK:    3,
		MaxEvidenceChars: 1200,
	},
	"product": {
		Name:             "product",
		PromptFile:       "customer_specialist_product.md",
		AllowedPrefixes:  []string{"wiki/knowledge/", "wiki/comparisons/", "wiki/concepts/", "wiki/entities/", "wiki/intents/"},
		CandidateTopK:    5,
		MaxEvidenceChars: 1800,
	},
	"pricing": {
		Name:             "pricing",
		PromptFile:       "customer_specialist_pricing.md",
		AllowedPrefixes:  []string{"wiki/knowledge/", "wiki/comparisons/", "wiki/synthesis/", "wiki/concepts/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"purchase": {
		Name:             "purchase",
		PromptFile:       "customer_specialist_purchase.md",
		AllowedPrefixes:  []string{"wiki/procedures/", "wiki/synthesis/", "wiki/knowledge/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"technical": {
		Name:             "technical",
		PromptFile:       "customer_specialist_technical.md",
		AllowedPrefixes:  []string{"wiki/procedures/", "wiki/knowledge/", "wiki/concepts/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"troubleshooting": {
		Name:             "troubleshooting",
		PromptFile:       "customer_specialist_troubleshooting.md",
		AllowedPrefixes:  []string{"wiki/procedures/", "wiki/policies/", "wiki/knowledge/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"billing_after_sales": {
		Name:             "billing_after_sales",
		PromptFile:       "customer_specialist_billing_after_sales.md",
		AllowedPrefixes:  []string{"wiki/procedures/", "wiki/policies/", "wiki/synthesis/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"safety": {
		Name:             "safety",
		PromptFile:       "customer_specialist_safety.md",
		AllowedPrefixes:  []string{"wiki/policies/", "wiki/comparisons/", "wiki/knowledge/", "wiki/procedures/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
}

func customerSpecialistProfile(name string) CustomerSpecialistProfile {
	normalized := normalizeCustomerSpecialist(name)
	if profile, ok := customerSpecialistProfiles[normalized]; ok {
		return profile
	}
	return customerSpecialistProfiles["product"]
}

func (profile CustomerSpecialistProfile) AllowsPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !isCustomerReadableEvidence(path) {
		return false
	}
	for _, prefix := range profile.AllowedPrefixes {
		if strings.HasPrefix(path, filepath.ToSlash(strings.TrimSpace(prefix))) {
			return true
		}
	}
	return false
}

func (profile CustomerSpecialistProfile) summary() map[string]any {
	return map[string]any{
		"name":               profile.Name,
		"prompt_file":        profile.PromptFile,
		"allowed_prefixes":   append([]string(nil), profile.AllowedPrefixes...),
		"candidate_top_k":    profile.CandidateTopK,
		"max_evidence_chars": profile.MaxEvidenceChars,
	}
}

func customerSpecialistRetrievalQueries(routerOutput *CustomerRouterOutput) []string {
	if routerOutput == nil {
		return nil
	}
	queries := make([]string, 0, 3)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range queries {
			if existing == value {
				return
			}
		}
		queries = append(queries, value)
	}
	for _, query := range routerOutput.RetrievalQueries {
		add(query)
		if len(queries) >= 3 {
			break
		}
	}
	if len(queries) == 0 {
		add(routerOutput.RewrittenQuestion)
	}
	return queries
}

func (s *CustomerChatService) retrieveCustomerSpecialistEvidence(ctx context.Context, traceID string, routerOutput *CustomerRouterOutput, settings RuntimeSettings) customerSpecialistEvidenceResult {
	profile := customerSpecialistProfile("")
	if routerOutput != nil {
		profile = customerSpecialistProfile(routerOutput.Specialist)
	}
	queries := customerSpecialistRetrievalQueries(routerOutput)
	result := customerSpecialistEvidenceResult{
		Profile: profile,
		Queries: queries,
	}
	if routerOutput == nil {
		result.Error = "missing router output"
		return result
	}
	if !routerOutput.NeedsRetrieval {
		return result
	}
	if s.deps.Config == nil || s.deps.Runtime == nil || s.deps.Retriever == nil {
		result.Error = "specialist evidence retrieval skipped because runtime or retriever is unavailable"
		return result
	}
	if len(queries) == 0 {
		result.Error = "missing specialist retrieval query"
		return result
	}

	env := s.env("customer", traceID, "", "")
	topK := customerSpecialistTopK(profile, settings)
	maxChars := customerSpecialistMaxEvidenceChars(profile, settings)
	contentBlocks := []string{}
	sources := []SourceRef{}
	evidenceBodies := map[string]string{}
	seenPaths := map[string]bool{}
	candidates := []retrieval.RetrievedPage{}
	evidenceTrace := []map[string]any{}
	errors := []string{}
	cacheTrace := customerSpecialistCacheTrace{}
	type pendingEvidenceExpansion struct {
		sourcePath string
		content    string
		query      string
	}
	pendingExpansions := []pendingEvidenceExpansion{}

	readPath := func(path string, query string) (string, bool) {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if !profile.AllowsPath(path) {
			cacheTrace.recordScopeFilteredPage(query, path, profile.Name)
			return "", false
		}
		beforeSources := len(sources)
		content, ok := s.readCustomerSpecialistEvidencePage(ctx, env, traceID, profile.Name, path, query, routerOutput, maxChars, seenPaths, &contentBlocks, &sources, &cacheTrace)
		if ok && len(sources) > beforeSources {
			evidenceBodies[path] = content
			evidenceTrace = append(evidenceTrace, customerEvidenceTraceItem(sources[len(sources)-1], content))
			return content, true
		}
		return content, ok
	}
	expandLinkedEvidence := func(sourcePath string, content string, query string) {
		for _, linkedPath := range resolveCustomerEvidenceWikilinks(env, profile, content) {
			if len(sources) >= topK {
				return
			}
			cacheTrace.recordWikilinkExpandedPage(query, sourcePath, linkedPath)
			readPath(linkedPath, query)
		}
	}

	for index, query := range queries {
		if len(sources) >= topK {
			cacheTrace.SkippedRetrievalQueryCount++
			cacheTrace.SkippedRetrievalQueries = append(cacheTrace.SkippedRetrievalQueries, query)
			continue
		}
		cacheTrace.AttemptedRetrievalQueries = append(cacheTrace.AttemptedRetrievalQueries, query)
		pages, err := s.retrieveCustomerSpecialistPages(ctx, env, traceID, profile.Name, index+1, query, topK, &cacheTrace)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", truncateForPrompt(query, 80), err.Error()))
			continue
		}
		cacheTrace.recordRetrievalResults(index+1, query, pages)
		candidates = append(candidates, pages...)
		for _, page := range filterCustomerEvidencePagesForRouter(prioritizeCustomerRetrievedPagesForRouter(pages, routerOutput), routerOutput) {
			if len(sources) >= topK {
				break
			}
			content, ok := readPath(page.Path, query)
			if ok {
				pendingExpansions = append(pendingExpansions, pendingEvidenceExpansion{
					sourcePath: page.Path,
					content:    content,
					query:      query,
				})
			}
		}
	}
	for _, expansion := range pendingExpansions {
		if len(sources) >= topK {
			break
		}
		expandLinkedEvidence(expansion.sourcePath, expansion.content, expansion.query)
	}

	result.Candidates = filterSpecialistCandidatesForRouter(candidates, profile, routerOutput)
	result.Sources = sources
	result.ContentBlocks = contentBlocks
	result.EvidenceBodies = evidenceBodies
	result.EvidenceTrace = evidenceTrace
	result.CacheTrace = cacheTrace
	if len(errors) > 0 {
		result.Error = strings.Join(errors, "; ")
	}
	return result
}

func (s *CustomerChatService) retrieveCustomerSpecialistPages(
	ctx context.Context,
	env *runtime.ExecEnv,
	traceID string,
	specialist string,
	queryIndex int,
	query string,
	topK int,
	trace *customerSpecialistCacheTrace,
) ([]retrieval.RetrievedPage, error) {
	start := time.Now()
	candidateLimit := customerSpecialistRetrievalCandidateLimit(topK)
	key := customerSpecialistRetrievalCacheKey(env, query, candidateLimit)
	if pages, ok := s.cache.getRetrieval(key); ok && len(pages) > 0 {
		if trace != nil {
			trace.QMDHits++
			trace.recordRetrievalTiming(queryIndex, query, "hit", time.Since(start), len(pages), "")
		}
		logCustomerSpecialistRetrievalTiming(traceID, specialist, queryIndex, query, "hit", time.Since(start), len(pages), "")
		return pages, nil
	}
	if trace != nil {
		trace.QMDMisses++
		trace.ExecutedRetrievalQueries = append(trace.ExecutedRetrievalQueries, query)
	}
	pages, err := s.deps.Retriever.Retrieve(ctx, env, query, candidateLimit)
	if err != nil {
		errorMessage := customerSafeErrorTextForLog(err)
		if trace != nil {
			trace.recordRetrievalTiming(queryIndex, query, "miss", time.Since(start), 0, errorMessage)
		}
		logCustomerSpecialistRetrievalTiming(traceID, specialist, queryIndex, query, "miss", time.Since(start), 0, errorMessage)
		return nil, err
	}
	if len(pages) > 0 {
		s.cache.setRetrieval(key, pages)
	}
	if trace != nil {
		trace.recordRetrievalTiming(queryIndex, query, "miss", time.Since(start), len(pages), "")
	}
	logCustomerSpecialistRetrievalTiming(traceID, specialist, queryIndex, query, "miss", time.Since(start), len(pages), "")
	return cloneRetrievedPages(pages), nil
}

func (s *CustomerChatService) readCustomerSpecialistEvidencePage(
	ctx context.Context,
	env *runtime.ExecEnv,
	traceID string,
	specialist string,
	path string,
	question string,
	routerOutput *CustomerRouterOutput,
	maxChars int,
	seenPaths map[string]bool,
	contentBlocks *[]string,
	sources *[]SourceRef,
	trace *customerSpecialistCacheTrace,
) (string, bool) {
	start := time.Now()
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || seenPaths[path] {
		return "", false
	}
	key := customerSpecialistPageCacheKey(env, path)
	if content, ok := s.cache.getPage(key); ok && strings.TrimSpace(content) != "" {
		if trace != nil {
			trace.ReadPageHits++
			trace.recordReadPageTiming(path, "hit", time.Since(start), len([]rune(content)), true, "")
		}
		logCustomerSpecialistReadPageTiming(traceID, specialist, path, "hit", time.Since(start), len([]rune(content)), true, "")
		return appendCustomerEvidencePageForRouter(path, question, routerOutput, maxChars, seenPaths, contentBlocks, sources, content)
	}
	if trace != nil {
		trace.ReadPageMisses++
	}
	result, err := s.deps.Runtime.Execute(ctx, env, runtimeCall("wiki.read_page", map[string]any{"path": path}))
	if err != nil || !result.Success {
		errorMessage := ""
		if err != nil {
			errorMessage = customerSafeErrorTextForLog(err)
		} else {
			errorMessage = "wiki.read_page returned unsuccessful result"
		}
		if trace != nil {
			trace.recordReadPageTiming(path, "miss", time.Since(start), 0, false, errorMessage)
		}
		logCustomerSpecialistReadPageTiming(traceID, specialist, path, "miss", time.Since(start), 0, false, errorMessage)
		return "", false
	}
	content, _ := result.Data["content"].(string)
	if strings.TrimSpace(content) == "" {
		if trace != nil {
			trace.recordReadPageTiming(path, "miss", time.Since(start), 0, false, "empty content")
		}
		logCustomerSpecialistReadPageTiming(traceID, specialist, path, "miss", time.Since(start), 0, false, "empty content")
		return "", false
	}
	s.cache.setPage(key, content)
	if trace != nil {
		trace.recordReadPageTiming(path, "miss", time.Since(start), len([]rune(content)), true, "")
	}
	logCustomerSpecialistReadPageTiming(traceID, specialist, path, "miss", time.Since(start), len([]rune(content)), true, "")
	return appendCustomerEvidencePageForRouter(path, question, routerOutput, maxChars, seenPaths, contentBlocks, sources, content)
}

func resolveCustomerEvidenceWikilinks(env *runtime.ExecEnv, profile CustomerSpecialistProfile, content string) []string {
	links := extractCustomerWikilinkTargets(content)
	if len(links) == 0 {
		return nil
	}
	out := make([]string, 0, len(links))
	seen := map[string]bool{}
	for _, link := range links {
		path := resolveCustomerWikilinkPath(env, profile, link)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func extractCustomerWikilinkTargets(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	for {
		start := strings.Index(content, "[[")
		if start < 0 {
			break
		}
		content = content[start+2:]
		end := strings.Index(content, "]]")
		if end < 0 {
			break
		}
		target := strings.TrimSpace(content[:end])
		content = content[end+2:]
		if pipe := strings.Index(target, "|"); pipe >= 0 {
			target = strings.TrimSpace(target[:pipe])
		}
		if hash := strings.Index(target, "#"); hash >= 0 {
			target = strings.TrimSpace(target[:hash])
		}
		target = filepath.ToSlash(strings.TrimSpace(target))
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

func resolveCustomerWikilinkPath(env *runtime.ExecEnv, profile CustomerSpecialistProfile, target string) string {
	target = filepath.ToSlash(strings.TrimSpace(target))
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "wiki/") {
		if !strings.HasSuffix(target, ".md") {
			target += ".md"
		}
		if profile.AllowsPath(target) && customerEvidencePathExists(env, target) {
			return target
		}
		return ""
	}
	slug := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	if slug == "" || strings.Contains(slug, "/") || strings.Contains(slug, "\\") {
		return ""
	}
	for _, prefix := range profile.AllowedPrefixes {
		candidate := filepath.ToSlash(filepath.Join(filepath.FromSlash(strings.TrimSpace(prefix)), slug+".md"))
		if profile.AllowsPath(candidate) && customerEvidencePathExists(env, candidate) {
			return candidate
		}
	}
	return ""
}

func customerEvidencePathExists(env *runtime.ExecEnv, path string) bool {
	if env == nil || strings.TrimSpace(env.WikiRoot) == "" {
		return false
	}
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !isCustomerReadableEvidence(path) {
		return false
	}
	abs := filepath.Join(strings.TrimSpace(env.WikiRoot), filepath.FromSlash(path))
	info, err := os.Stat(abs)
	return err == nil && !info.IsDir()
}

func (trace *customerSpecialistCacheTrace) recordRetrievalTiming(queryIndex int, query string, cache string, duration time.Duration, resultCount int, errorMessage string) {
	if trace == nil {
		return
	}
	item := map[string]any{
		"query_index":  queryIndex,
		"query":        truncateForPrompt(query, 160),
		"cache":        cache,
		"duration_ms":  duration.Milliseconds(),
		"result_count": resultCount,
	}
	if errorMessage != "" {
		item["error"] = errorMessage
	}
	trace.RetrievalTimings = append(trace.RetrievalTimings, item)
}

func (trace *customerSpecialistCacheTrace) recordRetrievalResults(queryIndex int, query string, pages []retrieval.RetrievedPage) {
	if trace == nil {
		return
	}
	trace.RetrievalResults = append(trace.RetrievalResults, map[string]any{
		"query_index": queryIndex,
		"query":       truncateForPrompt(query, 160),
		"candidates":  customerRetrievedPageSummaries(pages, 12),
	})
}

func (trace *customerSpecialistCacheTrace) recordScopeFilteredPage(query string, path string, specialist string) {
	if trace == nil {
		return
	}
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return
	}
	trace.ScopeFilteredPages = append(trace.ScopeFilteredPages, map[string]any{
		"query":      truncateForPrompt(query, 160),
		"path":       path,
		"specialist": strings.TrimSpace(specialist),
		"reason":     "outside_specialist_scope_or_customer_evidence",
	})
}

func (trace *customerSpecialistCacheTrace) recordWikilinkExpandedPage(query string, sourcePath string, linkedPath string) {
	if trace == nil {
		return
	}
	linkedPath = filepath.ToSlash(strings.TrimSpace(linkedPath))
	if linkedPath == "" {
		return
	}
	trace.WikilinkExpandedPages = append(trace.WikilinkExpandedPages, map[string]any{
		"query":       truncateForPrompt(query, 160),
		"source_path": filepath.ToSlash(strings.TrimSpace(sourcePath)),
		"linked_path": linkedPath,
	})
}

func (trace *customerSpecialistCacheTrace) recordReadPageTiming(path string, cache string, duration time.Duration, bodyChars int, success bool, errorMessage string) {
	if trace == nil {
		return
	}
	item := map[string]any{
		"path":        filepath.ToSlash(strings.TrimSpace(path)),
		"cache":       cache,
		"duration_ms": duration.Milliseconds(),
		"body_chars":  bodyChars,
		"success":     success,
	}
	if errorMessage != "" {
		item["error"] = errorMessage
	}
	trace.ReadPageTimings = append(trace.ReadPageTimings, item)
}

func logCustomerSpecialistRetrievalTiming(traceID string, specialist string, queryIndex int, query string, cache string, duration time.Duration, resultCount int, errorMessage string) {
	if errorMessage != "" {
		log.Printf(
			"customer routed qmd retrieval trace=%s specialist=%s query_index=%d cache=%s duration_ms=%d results=%d query=%q error=%s",
			traceID,
			specialist,
			queryIndex,
			cache,
			duration.Milliseconds(),
			resultCount,
			truncateForPrompt(query, 120),
			errorMessage,
		)
		return
	}
	log.Printf(
		"customer routed qmd retrieval trace=%s specialist=%s query_index=%d cache=%s duration_ms=%d results=%d query=%q",
		traceID,
		specialist,
		queryIndex,
		cache,
		duration.Milliseconds(),
		resultCount,
		truncateForPrompt(query, 120),
	)
}

func logCustomerSpecialistReadPageTiming(traceID string, specialist string, path string, cache string, duration time.Duration, bodyChars int, success bool, errorMessage string) {
	if errorMessage != "" {
		log.Printf(
			"customer routed read page trace=%s specialist=%s path=%s cache=%s duration_ms=%d body_chars=%d success=%t error=%s",
			traceID,
			specialist,
			filepath.ToSlash(strings.TrimSpace(path)),
			cache,
			duration.Milliseconds(),
			bodyChars,
			success,
			errorMessage,
		)
		return
	}
	log.Printf(
		"customer routed read page trace=%s specialist=%s path=%s cache=%s duration_ms=%d body_chars=%d success=%t",
		traceID,
		specialist,
		filepath.ToSlash(strings.TrimSpace(path)),
		cache,
		duration.Milliseconds(),
		bodyChars,
		success,
	)
}

func customerSafeErrorTextForLog(value any) string {
	safe := customerSafeErrorForLog(value)
	code, _ := safe["code"].(string)
	if code == "" {
		code = "customer_chat_generation_failed"
	}
	return fmt.Sprintf("code=%s chars=%v", code, safe["chars"])
}

func customerSpecialistRetrievalCacheKey(env *runtime.ExecEnv, query string, topK int) string {
	if env == nil {
		return strings.Join([]string{"", "", strings.TrimSpace(query), strconv.Itoa(topK)}, "\x00")
	}
	return strings.Join([]string{
		strings.TrimSpace(env.QMDIndex),
		strings.TrimSpace(env.WikiRoot),
		strings.TrimSpace(query),
		strconv.Itoa(topK),
	}, "\x00")
}

func customerSpecialistPageCacheKey(env *runtime.ExecEnv, path string) string {
	wikiRoot := ""
	if env != nil {
		wikiRoot = strings.TrimSpace(env.WikiRoot)
	}
	return strings.Join([]string{wikiRoot, filepath.ToSlash(strings.TrimSpace(path))}, "\x00")
}

func customerSpecialistTopK(profile CustomerSpecialistProfile, settings RuntimeSettings) int {
	if profile.CandidateTopK > 0 {
		return profile.CandidateTopK
	}
	if settings.CustomerChat.CandidateTopK > 0 {
		return settings.CustomerChat.CandidateTopK
	}
	return 4
}

func customerSpecialistRetrievalCandidateLimit(topK int) int {
	if topK <= 0 {
		return topK
	}
	limit := topK * 5
	if limit < 20 {
		return 20
	}
	if limit > 40 {
		return 40
	}
	return limit
}

func customerSpecialistMaxEvidenceChars(profile CustomerSpecialistProfile, settings RuntimeSettings) int {
	if profile.MaxEvidenceChars > 0 {
		return profile.MaxEvidenceChars
	}
	if settings.CustomerChat.MaxEvidenceChars > 0 {
		return settings.CustomerChat.MaxEvidenceChars
	}
	return 1800
}

func filterSpecialistCandidates(candidates []retrieval.RetrievedPage, profile CustomerSpecialistProfile) []retrieval.RetrievedPage {
	return filterSpecialistCandidatesForRouter(candidates, profile, nil)
}

func filterSpecialistCandidatesForRouter(candidates []retrieval.RetrievedPage, profile CustomerSpecialistProfile, routerOutput *CustomerRouterOutput) []retrieval.RetrievedPage {
	out := make([]retrieval.RetrievedPage, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		path := filepath.ToSlash(strings.TrimSpace(candidate.Path))
		if path == "" || seen[path] || !profile.AllowsPath(path) {
			continue
		}
		seen[path] = true
		out = append(out, retrieval.RetrievedPage{Path: path, Score: candidate.Score})
	}
	sortCustomerRetrievedPagesByProductFit(out, routerOutput, false)
	return out
}

func filterCustomerEvidencePagesForRouter(pages []retrieval.RetrievedPage, routerOutput *CustomerRouterOutput) []retrieval.RetrievedPage {
	target := customerEvidenceSinglePrimaryProduct(routerOutput)
	if target == "" {
		return pages
	}
	out := make([]retrieval.RetrievedPage, 0, len(pages))
	for _, page := range pages {
		products := customerEvidenceProductsInPath(page.Path)
		if len(products) > 0 && !products[target] {
			continue
		}
		out = append(out, page)
	}
	return out
}

func appendCustomerEvidencePageForRouter(
	path string,
	question string,
	routerOutput *CustomerRouterOutput,
	maxChars int,
	seenPaths map[string]bool,
	contentBlocks *[]string,
	sources *[]SourceRef,
	content string,
) (string, bool) {
	if customerScenarioIsOverseasIPSwitchUnsupported(routerOutput, customerScenarioGuardText(CustomerChatRequest{Question: question}, routerOutput)) &&
		customerEvidencePathIsConflictingOverseasSwitch(path) {
		return "", false
	}
	if !customerScenarioIsOverseasIPSwitchUnsupported(routerOutput, customerScenarioGuardText(CustomerChatRequest{Question: question}, routerOutput)) {
		return appendCustomerEvidencePage(path, question, maxChars, seenPaths, contentBlocks, sources, content)
	}
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
	preview := customerEvidencePreviewWithoutOverseasSwitchContamination(buildCustomerEvidencePreview(body, path, question, maxChars))
	if strings.TrimSpace(preview) == "" {
		return "", false
	}
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

func customerEvidencePathIsConflictingOverseasSwitch(path string) bool {
	products := customerEvidenceProductsInPath(path)
	return products["static_ip"] || products["dynamic_ip"] || products["residential_ip"] || products["datacenter_ip"]
}

func customerEvidencePreviewWithoutOverseasSwitchContamination(preview string) string {
	lines := strings.Split(strings.TrimSpace(preview), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if customerEvidenceLineIsConflictingOverseasSwitch(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func customerEvidenceLineIsConflictingOverseasSwitch(line string) bool {
	text := normalizeCustomerReviewText(line)
	if text == "" || strings.Contains(text, "不支持切换ip") || strings.Contains(text, "不支持切换 ip") {
		return false
	}
	hasSwitchAction := containsAny(text, "切换ip", "切换 ip", "换ip", "换 ip", "更换ip", "更换 ip", "手动切换", "重新分配", "重新提取", "断开重连", "切换按钮")
	if !hasSwitchAction {
		return false
	}
	return containsAny(text,
		"静态ip", "静态 ip", "住宅ip", "住宅 ip", "动态ip", "动态 ip", "数据中心ip", "数据中心 ip",
		"member/staticip", "member/jingtai", "member/house", "staticip.html", "jingtai.html", "house.html",
		"每月 5 次", "每月5次", "每天 3 次", "每天3次",
	)
}
