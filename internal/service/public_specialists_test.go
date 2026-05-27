package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wikios/internal/config"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
)

func TestPublicSpecialistProfilePricingScope(t *testing.T) {
	profile := publicSpecialistProfile("pricing")
	if profile.Name != "pricing" {
		t.Fatalf("expected pricing profile, got %q", profile.Name)
	}
	for _, path := range []string{
		"wiki/knowledge/si-ye-tian-static-ip-pricing.md",
		"wiki/comparisons/shared-vs-dedicated-static-ip.md",
		"wiki/synthesis/si-ye-tian-purchase-guidance-rules.md",
		"wiki/intents/pricing-router.md",
	} {
		if !profile.AllowsPath(path) {
			t.Fatalf("expected pricing profile to allow %s", path)
		}
	}
	for _, path := range []string{
		"wiki/procedures/si-ye-tian-api-whitelist-setup.md",
		"wiki/policies/si-ye-tian-after-sales-policy.md",
		"wiki/sources/raw-pricing-note.md",
		"wiki/unconfirmed/pending-pricing.md",
		"wiki/forbidden/pricing-secret.md",
		"wiki/knowledge/pricing.txt",
	} {
		if profile.AllowsPath(path) {
			t.Fatalf("expected pricing profile to block %s", path)
		}
	}
}

func TestPublicSpecialistProfileSafetyScopesPublicEvidenceDirectories(t *testing.T) {
	profile := publicSpecialistProfile("safety")
	if profile.Name != "safety" {
		t.Fatalf("expected safety profile, got %q", profile.Name)
	}
	if !profile.AllowsPath("wiki/policies/si-ye-tian-safety-boundaries.md") {
		t.Fatal("expected safety profile to allow safety boundaries policy")
	}
	if profile.AllowsPath("wiki/sources/si-ye-tian-safety-boundaries-source.md") {
		t.Fatal("expected safety profile to block raw source pages")
	}
}

func TestPublicSpecialistProfileUnknownFallsBackToProduct(t *testing.T) {
	profile := publicSpecialistProfile("not-a-specialist")
	if profile.Name != "product" {
		t.Fatalf("expected unknown specialist to fall back to product, got %q", profile.Name)
	}
}

func TestPublicSpecialistRetrievalQueriesUsesRouterQueriesThenRewriteFallback(t *testing.T) {
	queries := publicSpecialistRetrievalQueries(&PublicRouterOutput{
		RewrittenQuestion: "静态 IP 价格",
		RetrievalQueries:  []string{" 静态 IP 价格 ", "静态 IP 价格", "共享静态 IP 报价", "第三条不会执行"},
	})
	want := []string{"静态 IP 价格", "共享静态 IP 报价"}
	if len(queries) != len(want) {
		t.Fatalf("expected %d queries, got %+v", len(want), queries)
	}
	for index, expected := range want {
		if queries[index] != expected {
			t.Fatalf("query %d: expected %q, got %q", index, expected, queries[index])
		}
	}

	queries = publicSpecialistRetrievalQueries(&PublicRouterOutput{RewrittenQuestion: "静态 IP 价格"})
	if len(queries) != 1 || queries[0] != "静态 IP 价格" {
		t.Fatalf("expected rewrite fallback query, got %+v", queries)
	}
}

func TestRetrievePublicSpecialistEvidenceCachesRetrievalAndPages(t *testing.T) {
	qmdCalls := 0
	readCalls := 0
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			qmdCalls++
			raw, err := json.Marshal([]map[string]any{{"path": "wiki/knowledge/static-ip-pricing.md", "score": 100}})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			readCalls++
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": "---\ntitle: 静态价格\n---\n静态 IP 按个/月计费。"}}, nil
		}},
	)
	svc := newTestPublicQueryService(t, rt)
	routerOutput := &PublicRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "静态 IP 怎么卖",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"静态 IP 价格"},
	}
	first := svc.retrievePublicSpecialistEvidence(context.Background(), "trace-cache-1", routerOutput, RuntimeSettings{})
	if first.Error != "" || len(first.Sources) != 1 {
		t.Fatalf("expected first retrieval to read evidence, got error=%q sources=%d", first.Error, len(first.Sources))
	}
	second := svc.retrievePublicSpecialistEvidence(context.Background(), "trace-cache-2", routerOutput, RuntimeSettings{})
	if second.Error != "" || len(second.Sources) != 1 {
		t.Fatalf("expected second retrieval to read cached evidence, got error=%q sources=%d", second.Error, len(second.Sources))
	}
	if qmdCalls != 1 {
		t.Fatalf("expected second identical query to hit qmd cache, got %d qmd calls", qmdCalls)
	}
	if readCalls != 1 {
		t.Fatalf("expected second identical page read to hit page cache, got %d read calls", readCalls)
	}
	if second.CacheTrace.QMDHits != 1 || second.CacheTrace.ReadPageHits != 1 {
		t.Fatalf("expected cache hits in second trace, got %+v", second.CacheTrace)
	}
}

func TestRetrievePublicSpecialistEvidenceExpandsAllowedWikilinks(t *testing.T) {
	root := t.TempDir()
	writeTestWikiPage(t, root, "wiki/knowledge/si-ye-tian-dynamic-ip.md", "---\ntitle: 四叶天动态 IP\n---\n使用 API 前先配置白名单，具体配置见 [[si-ye-tian-api-whitelist-setup]]。")
	writeTestWikiPage(t, root, "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "---\ntitle: 四叶天白名单、API 与认证配置\n---\n登录后进入白名单管理页，添加当前网络的公网出口 IP；自动白名单接口在提取 API 页的其他接口位置查看。")
	readCalls := 0
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			raw, err := json.Marshal([]map[string]any{{"path": "wiki/knowledge/si-ye-tian-dynamic-ip.md", "score": 100}})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			readCalls++
			path, _ := args["path"].(string)
			raw, err := os.ReadFile(filepath.Join(env.WikiRoot, filepath.FromSlash(path)))
			if err != nil {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": string(raw)}}, nil
		}},
	)
	svc := newTestPublicQueryServiceWithRoot(t, rt, root)
	result := svc.retrievePublicSpecialistEvidence(context.Background(), "trace-expand-link", &PublicRouterOutput{
		Specialist:        "technical",
		RewrittenQuestion: "客户询问如何通过 API 接口添加白名单。",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"四叶天 API 白名单"},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected retrieval without error, got %q", result.Error)
	}
	if len(result.Sources) != 2 {
		t.Fatalf("expected direct page plus linked procedure, got %+v", result.Sources)
	}
	if result.Sources[1].Path != "wiki/procedures/si-ye-tian-api-whitelist-setup.md" {
		t.Fatalf("expected linked procedure as second source, got %+v", result.Sources)
	}
	if _, ok := result.EvidenceBodies["wiki/procedures/si-ye-tian-api-whitelist-setup.md"]; !ok {
		t.Fatalf("expected linked procedure body in evidence, got keys %+v", result.EvidenceBodies)
	}
	if readCalls != 2 {
		t.Fatalf("expected reading original and linked page, got %d", readCalls)
	}
}

func TestResolvePublicEvidenceWikilinksKeepsSpecialistScope(t *testing.T) {
	root := t.TempDir()
	writeTestWikiPage(t, root, "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "procedure")
	env := &runtime.ExecEnv{WikiRoot: root}
	links := resolvePublicEvidenceWikilinks(env, publicSpecialistProfile("pricing"), "见 [[si-ye-tian-api-whitelist-setup]]")
	if len(links) != 0 {
		t.Fatalf("expected pricing scope to block procedure wikilink, got %+v", links)
	}
	links = resolvePublicEvidenceWikilinks(env, publicSpecialistProfile("technical"), "见 [[si-ye-tian-api-whitelist-setup|白名单配置]]")
	if len(links) != 1 || links[0] != "wiki/procedures/si-ye-tian-api-whitelist-setup.md" {
		t.Fatalf("expected technical scope to resolve procedure wikilink, got %+v", links)
	}
}

func TestRetrievePublicSpecialistEvidenceExecutesAtMostTwoRouterQueries(t *testing.T) {
	qmdCalls := 0
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			qmdCalls++
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": "[]"}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"matches": []map[string]any{}}}, nil
		}},
		testRuntimeTool{name: "wiki.read_page"},
	)
	svc := newTestPublicQueryService(t, rt)
	result := svc.retrievePublicSpecialistEvidence(context.Background(), "trace-two-queries", &PublicRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "兜底问题",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"query-1", "query-2", "query-3"},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected empty retrieval without error, got %q", result.Error)
	}
	if qmdCalls != 2 {
		t.Fatalf("expected at most two qmd queries, got %d", qmdCalls)
	}
	want := []string{"query-1", "query-2"}
	if len(result.Queries) != len(want) || result.Queries[0] != want[0] || result.Queries[1] != want[1] {
		t.Fatalf("expected first two router queries, got %+v", result.Queries)
	}
}

func TestRetrievePublicSpecialistEvidenceStopsAfterEnoughEvidence(t *testing.T) {
	qmdCalls := 0
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			qmdCalls++
			pages := make([]map[string]any, 0, 4)
			for index := 1; index <= 4; index++ {
				pages = append(pages, map[string]any{"path": fmt.Sprintf("wiki/knowledge/evidence-%d.md", index), "score": 100 - index})
			}
			raw, err := json.Marshal(pages)
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			path, _ := args["path"].(string)
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": fmt.Sprintf("---\ntitle: %s\n---\n证据正文。", path)}}, nil
		}},
	)
	svc := newTestPublicQueryService(t, rt)
	result := svc.retrievePublicSpecialistEvidence(context.Background(), "trace-stop", &PublicRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "静态 IP 价格",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"query-1", "query-2"},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected retrieval without error, got %q", result.Error)
	}
	if len(result.Sources) != 4 {
		t.Fatalf("expected topK evidence pages, got %d", len(result.Sources))
	}
	if qmdCalls != 1 {
		t.Fatalf("expected retrieval to stop before second query, got %d qmd calls", qmdCalls)
	}
	if result.CacheTrace.SkippedRetrievalQueryCount != 1 {
		t.Fatalf("expected one skipped query in trace, got %+v", result.CacheTrace)
	}
}

func TestRetrievePublicSpecialistCacheDoesNotBypassProfileScope(t *testing.T) {
	qmdCalls := 0
	readCalls := 0
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			qmdCalls++
			raw, err := json.Marshal([]map[string]any{{"path": "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "score": 100}})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			readCalls++
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": "不应该读取。"}}, nil
		}},
	)
	svc := newTestPublicQueryService(t, rt)
	routerOutput := &PublicRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "静态 IP 价格",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"白名单"},
	}
	first := svc.retrievePublicSpecialistEvidence(context.Background(), "trace-scope-1", routerOutput, RuntimeSettings{})
	second := svc.retrievePublicSpecialistEvidence(context.Background(), "trace-scope-2", routerOutput, RuntimeSettings{})
	if len(first.Sources) != 0 || len(second.Sources) != 0 {
		t.Fatalf("expected pricing scope to reject procedure pages, got first=%+v second=%+v", first.Sources, second.Sources)
	}
	if qmdCalls != 1 {
		t.Fatalf("expected second retrieval to use cached candidates, got %d qmd calls", qmdCalls)
	}
	if readCalls != 0 {
		t.Fatalf("expected scoped-out cached candidate not to read page, got %d read calls", readCalls)
	}
}

func TestRetrievePublicSpecialistCacheExpires(t *testing.T) {
	qmdCalls := 0
	readCalls := 0
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			qmdCalls++
			raw, err := json.Marshal([]map[string]any{{"path": "wiki/knowledge/static-ip-pricing.md", "score": 100}})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			readCalls++
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": "---\ntitle: 静态价格\n---\n静态 IP 按个/月计费。"}}, nil
		}},
	)
	svc := newTestPublicQueryService(t, rt)
	svc.cache = newPublicAnswerCache(time.Millisecond)
	routerOutput := &PublicRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "静态 IP 怎么卖",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"静态 IP 价格"},
	}
	_ = svc.retrievePublicSpecialistEvidence(context.Background(), "trace-expire-1", routerOutput, RuntimeSettings{})
	time.Sleep(2 * time.Millisecond)
	_ = svc.retrievePublicSpecialistEvidence(context.Background(), "trace-expire-2", routerOutput, RuntimeSettings{})
	if qmdCalls != 2 {
		t.Fatalf("expected qmd cache to expire, got %d qmd calls", qmdCalls)
	}
	if readCalls != 2 {
		t.Fatalf("expected page cache to expire, got %d read calls", readCalls)
	}
}

func TestRetrievePublicSpecialistEvidenceScopesRetrievedPages(t *testing.T) {
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			raw, err := json.Marshal([]map[string]any{
				{"path": "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "score": 100},
				{"path": "wiki/knowledge/si-ye-tian-static-ip-pricing.md", "score": 90},
				{"path": "wiki/sources/raw-pricing-note.md", "score": 80},
				{"path": "wiki/synthesis/si-ye-tian-purchase-guidance-rules.md", "score": 70},
			})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			path, _ := args["path"].(string)
			pages := map[string]string{
				"wiki/knowledge/si-ye-tian-static-ip-pricing.md":             "---\ntitle: 静态 IP 价格\n---\n共享型 25 元起，独享型 300 元起。",
				"wiki/knowledge/si-ye-tian-proxy-ip-pricing.md":              "---\ntitle: 代理 IP 价格\n---\n动态代理按套餐计费。",
				"wiki/synthesis/si-ye-tian-purchase-guidance-rules.md":       "---\ntitle: 购买建议\n---\n普通问价只回答公开基础价。",
				"wiki/procedures/si-ye-tian-api-whitelist-setup.md":          "---\ntitle: API 白名单\n---\n这不是价格证据。",
				"wiki/sources/raw-pricing-note.md":                           "---\ntitle: 原始价格笔记\n---\n这不是 public specialist 证据。",
				"wiki/policies/si-ye-tian-safety-boundaries.md":              "---\ntitle: 安全边界\n---\n安全边界证据。",
				"wiki/comparisons/si-ye-tian-platform-scenario-selection.md": "---\ntitle: 平台场景\n---\n平台场景证据。",
			}
			content := pages[path]
			if content == "" {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow, Error: &runtime.ToolError{Code: "NOT_FOUND", Message: "not found"}}, nil
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": content}}, nil
		}},
	)
	svc := NewPublicQueryService(Deps{
		Config:    &config.Config{MountedWiki: config.MountedWikiConfig{Root: t.TempDir(), QMDIndex: "test"}},
		Runtime:   rt,
		Retriever: retrieval.NewQMDRetriever(rt),
	})
	result := svc.retrievePublicSpecialistEvidence(context.Background(), "trace-specialist", &PublicRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "静态 IP 价格",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"静态 IP 价格"},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected scoped retrieval without error, got %q", result.Error)
	}
	if result.Profile.Name != "pricing" {
		t.Fatalf("expected pricing specialist, got %q", result.Profile.Name)
	}
	for _, candidate := range publicRetrievedPageSummaries(result.Candidates, 12) {
		path, _ := candidate["path"].(string)
		if path == "wiki/procedures/si-ye-tian-api-whitelist-setup.md" || path == "wiki/sources/raw-pricing-note.md" {
			t.Fatalf("expected scoped candidates to exclude %s, got %+v", path, result.Candidates)
		}
	}
	for _, source := range result.Sources {
		if !publicSpecialistProfile("pricing").AllowsPath(source.Path) {
			t.Fatalf("expected source %s to stay inside pricing scope, got %+v", source.Path, result.Sources)
		}
	}
	if len(result.Sources) == 0 {
		t.Fatalf("expected at least one scoped source, got %+v", result)
	}
}

func newTestPublicQueryService(t *testing.T, rt *runtime.Runtime) *PublicQueryService {
	t.Helper()
	return newTestPublicQueryServiceWithRoot(t, rt, t.TempDir())
}

func newTestPublicQueryServiceWithRoot(t *testing.T, rt *runtime.Runtime, root string) *PublicQueryService {
	t.Helper()
	return NewPublicQueryService(Deps{
		Config:    &config.Config{MountedWiki: config.MountedWikiConfig{Root: root, QMDIndex: "test"}},
		Runtime:   rt,
		Retriever: retrieval.NewQMDRetriever(rt),
	})
}

func writeTestWikiPage(t *testing.T, root string, path string, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir test wiki page: %v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write test wiki page: %v", err)
	}
}
