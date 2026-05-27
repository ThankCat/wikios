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
)

type PublicSpecialistProfile struct {
	Name             string   `json:"name"`
	PromptFile       string   `json:"prompt_file,omitempty"`
	AllowedPrefixes  []string `json:"allowed_prefixes"`
	CandidateTopK    int      `json:"candidate_top_k,omitempty"`
	MaxEvidenceChars int      `json:"max_evidence_chars,omitempty"`
}

type publicSpecialistEvidenceResult struct {
	Profile        PublicSpecialistProfile
	Queries        []string
	Candidates     []retrieval.RetrievedPage
	Sources        []SourceRef
	ContentBlocks  []string
	EvidenceBodies map[string]string
	EvidenceTrace  []map[string]any
	CacheTrace     publicSpecialistCacheTrace
	Error          string
}

type publicSpecialistCacheTrace struct {
	QMDHits                    int
	QMDMisses                  int
	ReadPageHits               int
	ReadPageMisses             int
	ExecutedRetrievalQueries   []string
	AttemptedRetrievalQueries  []string
	SkippedRetrievalQueryCount int
	RetrievalTimings           []map[string]any
	ReadPageTimings            []map[string]any
}

func (trace publicSpecialistCacheTrace) summary() map[string]any {
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
		"retrieval_timings":               append([]map[string]any(nil), trace.RetrievalTimings...),
		"read_page_timings":               append([]map[string]any(nil), trace.ReadPageTimings...),
	}
}

var publicSpecialistProfiles = map[string]PublicSpecialistProfile{
	"reception": {
		Name:             "reception",
		PromptFile:       "public_specialist_reception.md",
		AllowedPrefixes:  []string{"wiki/policies/", "wiki/synthesis/", "wiki/intents/"},
		CandidateTopK:    3,
		MaxEvidenceChars: 1200,
	},
	"product": {
		Name:             "product",
		PromptFile:       "public_specialist_product.md",
		AllowedPrefixes:  []string{"wiki/knowledge/", "wiki/comparisons/", "wiki/concepts/", "wiki/entities/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"pricing": {
		Name:             "pricing",
		PromptFile:       "public_specialist_pricing.md",
		AllowedPrefixes:  []string{"wiki/knowledge/", "wiki/comparisons/", "wiki/synthesis/", "wiki/concepts/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"purchase": {
		Name:             "purchase",
		PromptFile:       "public_specialist_purchase.md",
		AllowedPrefixes:  []string{"wiki/procedures/", "wiki/synthesis/", "wiki/knowledge/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"technical": {
		Name:             "technical",
		PromptFile:       "public_specialist_technical.md",
		AllowedPrefixes:  []string{"wiki/procedures/", "wiki/knowledge/", "wiki/concepts/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"troubleshooting": {
		Name:             "troubleshooting",
		PromptFile:       "public_specialist_troubleshooting.md",
		AllowedPrefixes:  []string{"wiki/procedures/", "wiki/policies/", "wiki/knowledge/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"billing_after_sales": {
		Name:             "billing_after_sales",
		PromptFile:       "public_specialist_billing_after_sales.md",
		AllowedPrefixes:  []string{"wiki/procedures/", "wiki/policies/", "wiki/synthesis/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
	"safety": {
		Name:             "safety",
		PromptFile:       "public_specialist_safety.md",
		AllowedPrefixes:  []string{"wiki/policies/", "wiki/comparisons/", "wiki/knowledge/", "wiki/procedures/", "wiki/intents/"},
		CandidateTopK:    4,
		MaxEvidenceChars: 1800,
	},
}

func publicSpecialistProfile(name string) PublicSpecialistProfile {
	normalized := normalizePublicSpecialist(name)
	if profile, ok := publicSpecialistProfiles[normalized]; ok {
		return profile
	}
	return publicSpecialistProfiles["product"]
}

func (profile PublicSpecialistProfile) AllowsPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !isPublicReadableEvidence(path) {
		return false
	}
	for _, prefix := range profile.AllowedPrefixes {
		if strings.HasPrefix(path, filepath.ToSlash(strings.TrimSpace(prefix))) {
			return true
		}
	}
	return false
}

func (profile PublicSpecialistProfile) summary() map[string]any {
	return map[string]any{
		"name":               profile.Name,
		"prompt_file":        profile.PromptFile,
		"allowed_prefixes":   append([]string(nil), profile.AllowedPrefixes...),
		"candidate_top_k":    profile.CandidateTopK,
		"max_evidence_chars": profile.MaxEvidenceChars,
	}
}

func publicSpecialistRetrievalQueries(routerOutput *PublicRouterOutput) []string {
	if routerOutput == nil {
		return nil
	}
	queries := make([]string, 0, 2)
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
		if len(queries) >= 2 {
			break
		}
	}
	if len(queries) == 0 {
		add(routerOutput.RewrittenQuestion)
	}
	return queries
}

func (s *PublicQueryService) retrievePublicSpecialistEvidence(ctx context.Context, traceID string, routerOutput *PublicRouterOutput, settings RuntimeSettings) publicSpecialistEvidenceResult {
	profile := publicSpecialistProfile("")
	if routerOutput != nil {
		profile = publicSpecialistProfile(routerOutput.Specialist)
	}
	queries := publicSpecialistRetrievalQueries(routerOutput)
	result := publicSpecialistEvidenceResult{
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

	env := s.env("public", traceID, "", "")
	topK := publicSpecialistTopK(profile, settings)
	maxChars := publicSpecialistMaxEvidenceChars(profile, settings)
	contentBlocks := []string{}
	sources := []SourceRef{}
	evidenceBodies := map[string]string{}
	seenPaths := map[string]bool{}
	candidates := []retrieval.RetrievedPage{}
	evidenceTrace := []map[string]any{}
	errors := []string{}
	cacheTrace := publicSpecialistCacheTrace{}

	readPath := func(path string, query string) (string, bool) {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if !profile.AllowsPath(path) {
			return "", false
		}
		beforeSources := len(sources)
		content, ok := s.readPublicSpecialistEvidencePage(ctx, env, traceID, profile.Name, path, query, maxChars, seenPaths, &contentBlocks, &sources, &cacheTrace)
		if ok && len(sources) > beforeSources {
			evidenceBodies[path] = content
			evidenceTrace = append(evidenceTrace, publicEvidenceTraceItem(sources[len(sources)-1], content))
			return content, true
		}
		return content, ok
	}
	expandLinkedEvidence := func(content string, query string) {
		for _, linkedPath := range resolvePublicEvidenceWikilinks(env, profile, content) {
			if len(sources) >= topK {
				return
			}
			readPath(linkedPath, query)
		}
	}

	for index, query := range queries {
		if len(sources) >= topK {
			cacheTrace.SkippedRetrievalQueryCount++
			continue
		}
		cacheTrace.AttemptedRetrievalQueries = append(cacheTrace.AttemptedRetrievalQueries, query)
		pages, err := s.retrievePublicSpecialistPages(ctx, env, traceID, profile.Name, index+1, query, topK, &cacheTrace)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %s", truncateForPrompt(query, 80), err.Error()))
			continue
		}
		candidates = append(candidates, pages...)
		for _, page := range prioritizePublicRetrievedPages(pages) {
			if len(sources) >= topK {
				break
			}
			content, ok := readPath(page.Path, query)
			if ok {
				expandLinkedEvidence(content, query)
			}
		}
	}

	result.Candidates = filterSpecialistCandidates(candidates, profile)
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

func (s *PublicQueryService) retrievePublicSpecialistPages(
	ctx context.Context,
	env *runtime.ExecEnv,
	traceID string,
	specialist string,
	queryIndex int,
	query string,
	topK int,
	trace *publicSpecialistCacheTrace,
) ([]retrieval.RetrievedPage, error) {
	start := time.Now()
	key := publicSpecialistRetrievalCacheKey(env, query, topK)
	if pages, ok := s.cache.getRetrieval(key); ok && len(pages) > 0 {
		if trace != nil {
			trace.QMDHits++
			trace.recordRetrievalTiming(queryIndex, query, "hit", time.Since(start), len(pages), "")
		}
		logPublicSpecialistRetrievalTiming(traceID, specialist, queryIndex, query, "hit", time.Since(start), len(pages), "")
		return pages, nil
	}
	if trace != nil {
		trace.QMDMisses++
		trace.ExecutedRetrievalQueries = append(trace.ExecutedRetrievalQueries, query)
	}
	pages, err := s.deps.Retriever.Retrieve(ctx, env, query, topK)
	if err != nil {
		errorMessage := publicSafeErrorTextForLog(err)
		if trace != nil {
			trace.recordRetrievalTiming(queryIndex, query, "miss", time.Since(start), 0, errorMessage)
		}
		logPublicSpecialistRetrievalTiming(traceID, specialist, queryIndex, query, "miss", time.Since(start), 0, errorMessage)
		return nil, err
	}
	if len(pages) > 0 {
		s.cache.setRetrieval(key, pages)
	}
	if trace != nil {
		trace.recordRetrievalTiming(queryIndex, query, "miss", time.Since(start), len(pages), "")
	}
	logPublicSpecialistRetrievalTiming(traceID, specialist, queryIndex, query, "miss", time.Since(start), len(pages), "")
	return cloneRetrievedPages(pages), nil
}

func (s *PublicQueryService) readPublicSpecialistEvidencePage(
	ctx context.Context,
	env *runtime.ExecEnv,
	traceID string,
	specialist string,
	path string,
	question string,
	maxChars int,
	seenPaths map[string]bool,
	contentBlocks *[]string,
	sources *[]SourceRef,
	trace *publicSpecialistCacheTrace,
) (string, bool) {
	start := time.Now()
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || seenPaths[path] {
		return "", false
	}
	key := publicSpecialistPageCacheKey(env, path)
	if content, ok := s.cache.getPage(key); ok && strings.TrimSpace(content) != "" {
		if trace != nil {
			trace.ReadPageHits++
			trace.recordReadPageTiming(path, "hit", time.Since(start), len([]rune(content)), true, "")
		}
		logPublicSpecialistReadPageTiming(traceID, specialist, path, "hit", time.Since(start), len([]rune(content)), true, "")
		return appendPublicEvidencePage(path, question, maxChars, seenPaths, contentBlocks, sources, content)
	}
	if trace != nil {
		trace.ReadPageMisses++
	}
	result, err := s.deps.Runtime.Execute(ctx, env, runtimeCall("wiki.read_page", map[string]any{"path": path}))
	if err != nil || !result.Success {
		errorMessage := ""
		if err != nil {
			errorMessage = publicSafeErrorTextForLog(err)
		} else {
			errorMessage = "wiki.read_page returned unsuccessful result"
		}
		if trace != nil {
			trace.recordReadPageTiming(path, "miss", time.Since(start), 0, false, errorMessage)
		}
		logPublicSpecialistReadPageTiming(traceID, specialist, path, "miss", time.Since(start), 0, false, errorMessage)
		return "", false
	}
	content, _ := result.Data["content"].(string)
	if strings.TrimSpace(content) == "" {
		if trace != nil {
			trace.recordReadPageTiming(path, "miss", time.Since(start), 0, false, "empty content")
		}
		logPublicSpecialistReadPageTiming(traceID, specialist, path, "miss", time.Since(start), 0, false, "empty content")
		return "", false
	}
	s.cache.setPage(key, content)
	if trace != nil {
		trace.recordReadPageTiming(path, "miss", time.Since(start), len([]rune(content)), true, "")
	}
	logPublicSpecialistReadPageTiming(traceID, specialist, path, "miss", time.Since(start), len([]rune(content)), true, "")
	return appendPublicEvidencePage(path, question, maxChars, seenPaths, contentBlocks, sources, content)
}

func resolvePublicEvidenceWikilinks(env *runtime.ExecEnv, profile PublicSpecialistProfile, content string) []string {
	links := extractPublicWikilinkTargets(content)
	if len(links) == 0 {
		return nil
	}
	out := make([]string, 0, len(links))
	seen := map[string]bool{}
	for _, link := range links {
		path := resolvePublicWikilinkPath(env, profile, link)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func extractPublicWikilinkTargets(content string) []string {
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

func resolvePublicWikilinkPath(env *runtime.ExecEnv, profile PublicSpecialistProfile, target string) string {
	target = filepath.ToSlash(strings.TrimSpace(target))
	if target == "" {
		return ""
	}
	if strings.HasPrefix(target, "wiki/") {
		if !strings.HasSuffix(target, ".md") {
			target += ".md"
		}
		if profile.AllowsPath(target) && publicEvidencePathExists(env, target) {
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
		if profile.AllowsPath(candidate) && publicEvidencePathExists(env, candidate) {
			return candidate
		}
	}
	return ""
}

func publicEvidencePathExists(env *runtime.ExecEnv, path string) bool {
	if env == nil || strings.TrimSpace(env.WikiRoot) == "" {
		return false
	}
	path = filepath.ToSlash(strings.TrimSpace(path))
	if !isPublicReadableEvidence(path) {
		return false
	}
	abs := filepath.Join(strings.TrimSpace(env.WikiRoot), filepath.FromSlash(path))
	info, err := os.Stat(abs)
	return err == nil && !info.IsDir()
}

func (trace *publicSpecialistCacheTrace) recordRetrievalTiming(queryIndex int, query string, cache string, duration time.Duration, resultCount int, errorMessage string) {
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

func (trace *publicSpecialistCacheTrace) recordReadPageTiming(path string, cache string, duration time.Duration, bodyChars int, success bool, errorMessage string) {
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

func logPublicSpecialistRetrievalTiming(traceID string, specialist string, queryIndex int, query string, cache string, duration time.Duration, resultCount int, errorMessage string) {
	if errorMessage != "" {
		log.Printf(
			"public routed qmd retrieval trace=%s specialist=%s query_index=%d cache=%s duration_ms=%d results=%d query=%q error=%s",
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
		"public routed qmd retrieval trace=%s specialist=%s query_index=%d cache=%s duration_ms=%d results=%d query=%q",
		traceID,
		specialist,
		queryIndex,
		cache,
		duration.Milliseconds(),
		resultCount,
		truncateForPrompt(query, 120),
	)
}

func logPublicSpecialistReadPageTiming(traceID string, specialist string, path string, cache string, duration time.Duration, bodyChars int, success bool, errorMessage string) {
	if errorMessage != "" {
		log.Printf(
			"public routed read page trace=%s specialist=%s path=%s cache=%s duration_ms=%d body_chars=%d success=%t error=%s",
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
		"public routed read page trace=%s specialist=%s path=%s cache=%s duration_ms=%d body_chars=%d success=%t",
		traceID,
		specialist,
		filepath.ToSlash(strings.TrimSpace(path)),
		cache,
		duration.Milliseconds(),
		bodyChars,
		success,
	)
}

func publicSafeErrorTextForLog(value any) string {
	safe := publicSafeErrorForLog(value)
	code, _ := safe["code"].(string)
	if code == "" {
		code = "public_answer_generation_failed"
	}
	return fmt.Sprintf("code=%s chars=%v", code, safe["chars"])
}

func publicSpecialistRetrievalCacheKey(env *runtime.ExecEnv, query string, topK int) string {
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

func publicSpecialistPageCacheKey(env *runtime.ExecEnv, path string) string {
	wikiRoot := ""
	if env != nil {
		wikiRoot = strings.TrimSpace(env.WikiRoot)
	}
	return strings.Join([]string{wikiRoot, filepath.ToSlash(strings.TrimSpace(path))}, "\x00")
}

func publicSpecialistTopK(profile PublicSpecialistProfile, settings RuntimeSettings) int {
	if profile.CandidateTopK > 0 {
		return profile.CandidateTopK
	}
	if settings.PublicQuery.CandidateTopK > 0 {
		return settings.PublicQuery.CandidateTopK
	}
	return 4
}

func publicSpecialistMaxEvidenceChars(profile PublicSpecialistProfile, settings RuntimeSettings) int {
	if profile.MaxEvidenceChars > 0 {
		return profile.MaxEvidenceChars
	}
	if settings.PublicQuery.MaxEvidenceChars > 0 {
		return settings.PublicQuery.MaxEvidenceChars
	}
	return 1800
}

func filterSpecialistCandidates(candidates []retrieval.RetrievedPage, profile PublicSpecialistProfile) []retrieval.RetrievedPage {
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
	return out
}
