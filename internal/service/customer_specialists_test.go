package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wikios/internal/config"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
)

func TestCustomerSpecialistProfilePricingScope(t *testing.T) {
	profile := customerSpecialistProfile("pricing")
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

func TestCustomerSpecialistProfileSafetyScopesCustomerEvidenceDirectories(t *testing.T) {
	profile := customerSpecialistProfile("safety")
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

func TestCustomerSpecialistProfileUnknownFallsBackToProduct(t *testing.T) {
	profile := customerSpecialistProfile("not-a-specialist")
	if profile.Name != "product" {
		t.Fatalf("expected unknown specialist to fall back to product, got %q", profile.Name)
	}
}

func TestCustomerSpecialistProductKeepsFiveCandidates(t *testing.T) {
	profile := customerSpecialistProfile("product")
	if profile.CandidateTopK != 5 {
		t.Fatalf("expected product specialist to keep 5 candidates, got %d", profile.CandidateTopK)
	}
}

func TestCustomerSpecialistRetrievalQueriesUsesRouterQueriesThenRewriteFallback(t *testing.T) {
	queries := customerSpecialistRetrievalQueries(&CustomerRouterOutput{
		RewrittenQuestion: "静态 IP 价格",
		RetrievalQueries:  []string{" 静态 IP 价格 ", "静态 IP 价格", "共享静态 IP 报价", "第三条会执行", "第四条不会执行"},
	})
	want := []string{"静态 IP 价格", "共享静态 IP 报价", "第三条会执行"}
	if len(queries) != len(want) {
		t.Fatalf("expected %d queries, got %+v", len(want), queries)
	}
	for index, expected := range want {
		if queries[index] != expected {
			t.Fatalf("query %d: expected %q, got %q", index, expected, queries[index])
		}
	}

	queries = customerSpecialistRetrievalQueries(&CustomerRouterOutput{RewrittenQuestion: "静态 IP 价格"})
	if len(queries) != 1 || queries[0] != "静态 IP 价格" {
		t.Fatalf("expected rewrite fallback query, got %+v", queries)
	}
}

func TestRetrieveCustomerSpecialistEvidenceCachesRetrievalAndPages(t *testing.T) {
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
	svc := newTestCustomerChatService(t, rt)
	routerOutput := &CustomerRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "静态 IP 怎么卖",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"静态 IP 价格"},
	}
	first := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-cache-1", routerOutput, RuntimeSettings{})
	if first.Error != "" || len(first.Sources) != 1 {
		t.Fatalf("expected first retrieval to read evidence, got error=%q sources=%d", first.Error, len(first.Sources))
	}
	second := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-cache-2", routerOutput, RuntimeSettings{})
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

func TestRetrieveCustomerSpecialistEvidenceExpandsAllowedWikilinks(t *testing.T) {
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
	svc := newTestCustomerChatServiceWithRoot(t, rt, root)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-expand-link", &CustomerRouterOutput{
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
	if len(result.CacheTrace.WikilinkExpandedPages) != 1 {
		t.Fatalf("expected wikilink expansion trace, got %+v", result.CacheTrace)
	}
	if got, _ := result.CacheTrace.WikilinkExpandedPages[0]["linked_path"].(string); got != "wiki/procedures/si-ye-tian-api-whitelist-setup.md" {
		t.Fatalf("expected linked procedure trace, got %+v", result.CacheTrace.WikilinkExpandedPages)
	}
}

func TestRetrieveCustomerSpecialistEvidenceKeepsDirectCandidatesBeforeWikilinks(t *testing.T) {
	root := t.TempDir()
	writeTestWikiPage(t, root, "wiki/procedures/si-ye-tian-connection-troubleshooting.md", "---\ntitle: 连接排障\n---\n静态 IP 连接后没网时，不要只看配置；相关资料见 [[si-ye-tian-api-whitelist-setup]]、[[si-ye-tian-download-installation]]、[[si-ye-tian-after-sales-policy]]。")
	writeTestWikiPage(t, root, "wiki/procedures/si-ye-tian-static-ip-usage.md", "---\ntitle: 静态 IP 使用\n---\n静态 IP 支持手动切换、重新分配，或更换地区/线路后测试。")
	writeTestWikiPage(t, root, "wiki/procedures/si-ye-tian-device-network-configuration.md", "---\ntitle: 设备网络\n---\n检查系统代理和全局代理设置。")
	writeTestWikiPage(t, root, "wiki/knowledge/si-ye-tian-static-ip.md", "---\ntitle: 静态 IP\n---\n静态 IP 是相对固定出口，也支持按后台资源手动切换。")
	writeTestWikiPage(t, root, "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "---\ntitle: 白名单\n---\n确认当前出口公网 IP 已加入白名单。")
	writeTestWikiPage(t, root, "wiki/procedures/si-ye-tian-download-installation.md", "---\ntitle: 下载安装\n---\n下载并安装客户端。")
	writeTestWikiPage(t, root, "wiki/policies/si-ye-tian-after-sales-policy.md", "---\ntitle: 售后\n---\n以当前订单状态为准。")

	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			raw, err := json.Marshal([]map[string]any{
				{"path": "wiki/procedures/si-ye-tian-connection-troubleshooting.md", "score": 100},
				{"path": "wiki/procedures/si-ye-tian-static-ip-usage.md", "score": 90},
				{"path": "wiki/procedures/si-ye-tian-device-network-configuration.md", "score": 80},
				{"path": "wiki/knowledge/si-ye-tian-static-ip.md", "score": 70},
			})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			path, _ := args["path"].(string)
			raw, err := os.ReadFile(filepath.Join(env.WikiRoot, filepath.FromSlash(path)))
			if err != nil {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": string(raw)}}, nil
		}},
	)
	svc := newTestCustomerChatServiceWithRoot(t, rt, root)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-direct-before-links", &CustomerRouterOutput{
		Specialist:        "troubleshooting",
		RewrittenQuestion: "客户使用静态 IP，连接后所有网页都打不开，基础排查后还是连不上。",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"四叶天 静态 IP 连接后没网 所有网页打不开 排查"},
		Slots: CustomerRouterSlots{
			PrimaryProduct: "static_ip",
			Products:       []string{"static_ip"},
		},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected retrieval without error, got %q", result.Error)
	}
	if len(result.Sources) != 4 {
		t.Fatalf("expected direct topK sources, got %+v", result.Sources)
	}
	wantSources := []string{
		"wiki/procedures/si-ye-tian-connection-troubleshooting.md",
		"wiki/procedures/si-ye-tian-static-ip-usage.md",
		"wiki/procedures/si-ye-tian-device-network-configuration.md",
		"wiki/knowledge/si-ye-tian-static-ip.md",
	}
	for index, want := range wantSources {
		if result.Sources[index].Path != want {
			t.Fatalf("source %d: expected %s, got %+v", index, want, result.Sources)
		}
	}
	if _, ok := result.EvidenceBodies["wiki/procedures/si-ye-tian-static-ip-usage.md"]; !ok {
		t.Fatalf("expected static IP usage evidence to be visible, got keys %+v", result.EvidenceBodies)
	}
	for _, source := range result.Sources {
		if source.Path == "wiki/procedures/si-ye-tian-api-whitelist-setup.md" {
			t.Fatalf("expected wikilink expansion not to crowd out direct candidates, got %+v", result.Sources)
		}
	}
}

func TestResolveCustomerEvidenceWikilinksKeepsSpecialistScope(t *testing.T) {
	root := t.TempDir()
	writeTestWikiPage(t, root, "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "procedure")
	env := &runtime.ExecEnv{WikiRoot: root}
	links := resolveCustomerEvidenceWikilinks(env, customerSpecialistProfile("pricing"), "见 [[si-ye-tian-api-whitelist-setup]]")
	if len(links) != 0 {
		t.Fatalf("expected pricing scope to block procedure wikilink, got %+v", links)
	}
	links = resolveCustomerEvidenceWikilinks(env, customerSpecialistProfile("technical"), "见 [[si-ye-tian-api-whitelist-setup|白名单配置]]")
	if len(links) != 1 || links[0] != "wiki/procedures/si-ye-tian-api-whitelist-setup.md" {
		t.Fatalf("expected technical scope to resolve procedure wikilink, got %+v", links)
	}
}

func TestRetrieveCustomerSpecialistEvidenceExecutesAtMostThreeRouterQueries(t *testing.T) {
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
	svc := newTestCustomerChatService(t, rt)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-two-queries", &CustomerRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "兜底问题",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"query-1", "query-2", "query-3"},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected empty retrieval without error, got %q", result.Error)
	}
	if qmdCalls != 3 {
		t.Fatalf("expected at most three qmd queries, got %d", qmdCalls)
	}
	want := []string{"query-1", "query-2", "query-3"}
	if len(result.Queries) != len(want) || result.Queries[0] != want[0] || result.Queries[1] != want[1] || result.Queries[2] != want[2] {
		t.Fatalf("expected first three router queries, got %+v", result.Queries)
	}
}

func TestRetrieveCustomerSpecialistEvidenceStopsAfterEnoughEvidence(t *testing.T) {
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
	svc := newTestCustomerChatService(t, rt)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-stop", &CustomerRouterOutput{
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

func TestRetrieveCustomerSpecialistEvidenceDemotesConflictingProductPages(t *testing.T) {
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			raw, err := json.Marshal([]map[string]any{
				{"path": "wiki/procedures/si-ye-tian-dynamic-ip-usage.md", "score": 100},
				{"path": "wiki/procedures/si-ye-tian-static-ip-usage.md", "score": 90},
				{"path": "wiki/knowledge/si-ye-tian-static-ip.md", "score": 80},
				{"path": "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "score": 70},
				{"path": "wiki/procedures/si-ye-tian-renewal-upgrade-procedure.md", "score": 60},
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
				"wiki/procedures/si-ye-tian-dynamic-ip-usage.md":          "---\ntitle: 动态 IP 使用\n---\n动态 IP 可重新提取。",
				"wiki/procedures/si-ye-tian-static-ip-usage.md":           "---\ntitle: 静态 IP 使用\n---\n静态 IP 可在后台手动切换。",
				"wiki/knowledge/si-ye-tian-static-ip.md":                  "---\ntitle: 静态 IP\n---\n静态 IP 是固定出口产品。",
				"wiki/procedures/si-ye-tian-api-whitelist-setup.md":       "---\ntitle: 白名单\n---\n后台可配置白名单。",
				"wiki/procedures/si-ye-tian-renewal-upgrade-procedure.md": "---\ntitle: 续费升级\n---\n后台可续费升级。",
			}
			content := pages[path]
			if content == "" {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow, Error: &runtime.ToolError{Code: "NOT_FOUND", Message: path}}, nil
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": content}}, nil
		}},
	)
	svc := newTestCustomerChatService(t, rt)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-static-product-rank", &CustomerRouterOutput{
		Specialist:        "technical",
		RewrittenQuestion: "客户询问静态 IP 怎么切换。",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"四叶天 静态 IP 切换 IP 方法"},
		Slots: CustomerRouterSlots{
			PrimaryProduct: "static_ip",
			Products:       []string{"static_ip"},
		},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected retrieval without error, got %q", result.Error)
	}
	if len(result.Sources) != 4 {
		t.Fatalf("expected topK evidence pages, got %+v", result.Sources)
	}
	if result.Sources[0].Path != "wiki/procedures/si-ye-tian-static-ip-usage.md" {
		t.Fatalf("expected static IP usage to be the first source, got %+v", result.Sources)
	}
	for _, source := range result.Sources {
		if source.Path == "wiki/procedures/si-ye-tian-dynamic-ip-usage.md" {
			t.Fatalf("expected conflicting dynamic IP page to stay out of topK sources, got %+v", result.Sources)
		}
	}
	candidateIndex := map[string]int{}
	for index, candidate := range result.Candidates {
		candidateIndex[candidate.Path] = index
	}
	dynamicIndex, hasDynamic := candidateIndex["wiki/procedures/si-ye-tian-dynamic-ip-usage.md"]
	renewalIndex, hasRenewal := candidateIndex["wiki/procedures/si-ye-tian-renewal-upgrade-procedure.md"]
	if !hasDynamic || !hasRenewal {
		t.Fatalf("expected both dynamic and renewal candidates to remain visible, got %+v", result.Candidates)
	}
	if dynamicIndex < renewalIndex {
		t.Fatalf("expected conflicting dynamic candidate to be demoted, got %+v", result.Candidates)
	}
}

func TestRetrieveCustomerSpecialistEvidenceFiltersConflictingProductEvidence(t *testing.T) {
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			raw, err := json.Marshal([]map[string]any{
				{"path": "wiki/procedures/si-ye-tian-connection-troubleshooting.md", "score": 327},
				{"path": "wiki/procedures/si-ye-tian-static-ip-usage.md", "score": 311},
				{"path": "wiki/knowledge/si-ye-tian-overseas-ip.md", "score": 251},
				{"path": "wiki/procedures/si-ye-tian-device-network-configuration.md", "score": 231},
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
				"wiki/procedures/si-ye-tian-connection-troubleshooting.md":       "---\ntitle: 连接排障\n---\n海外 IP 场景需确认海外网络环境。",
				"wiki/procedures/si-ye-tian-static-ip-usage.md":                 "---\ntitle: 静态 IP 使用\n---\n静态 IP 可在会员中心手动切换、重新分配，每月 5 次。",
				"wiki/knowledge/si-ye-tian-overseas-ip.md":                      "---\ntitle: 海外 IP\n---\n海外 IP 需要海外网络环境或海外服务器环境。",
				"wiki/procedures/si-ye-tian-device-network-configuration.md":     "---\ntitle: 设备网络\n---\n连接后用 IP 查询站检查出口 IP。",
			}
			content := pages[path]
			if content == "" {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow, Error: &runtime.ToolError{Code: "NOT_FOUND", Message: path}}, nil
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": content}}, nil
		}},
	)
	svc := newTestCustomerChatService(t, rt)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-overseas-product-filter", &CustomerRouterOutput{
		Specialist:        "technical",
		RewrittenQuestion: "客户想了解四叶天海外 IP 如何切换 IP。",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"四叶天 海外 IP 切换 方法 步骤"},
		Slots: CustomerRouterSlots{
			PrimaryProduct: "overseas_ip",
			Products:       []string{"overseas_ip"},
			IPType:         "overseas",
		},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected retrieval without error, got %q", result.Error)
	}
	for _, source := range result.Sources {
		if source.Path == "wiki/procedures/si-ye-tian-static-ip-usage.md" {
			t.Fatalf("expected conflicting static IP page not to be used as overseas evidence, got %+v", result.Sources)
		}
	}
	if _, ok := result.EvidenceBodies["wiki/procedures/si-ye-tian-static-ip-usage.md"]; ok {
		t.Fatalf("expected static IP usage body to stay out of evidence, got keys %+v", result.EvidenceBodies)
	}
	candidatePaths := customerRetrievedPagePaths(result.Candidates, 10)
	if !containsString(candidatePaths, "wiki/procedures/si-ye-tian-static-ip-usage.md") {
		t.Fatalf("expected conflicting static page to remain visible as candidate for diagnostics, got %+v", result.Candidates)
	}
}

func TestRetrieveCustomerSpecialistCacheDoesNotBypassProfileScope(t *testing.T) {
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
	svc := newTestCustomerChatService(t, rt)
	routerOutput := &CustomerRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "静态 IP 价格",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"白名单"},
	}
	first := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-scope-1", routerOutput, RuntimeSettings{})
	second := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-scope-2", routerOutput, RuntimeSettings{})
	if len(first.Sources) != 0 || len(second.Sources) != 0 {
		t.Fatalf("expected pricing scope to reject procedure pages, got first=%+v second=%+v", first.Sources, second.Sources)
	}
	if len(first.CacheTrace.ScopeFilteredPages) == 0 || len(second.CacheTrace.ScopeFilteredPages) == 0 {
		t.Fatalf("expected scoped-out pages to be traced, got first=%+v second=%+v", first.CacheTrace, second.CacheTrace)
	}
	if qmdCalls != 1 {
		t.Fatalf("expected second retrieval to use cached candidates, got %d qmd calls", qmdCalls)
	}
	if readCalls != 0 {
		t.Fatalf("expected scoped-out cached candidate not to read page, got %d read calls", readCalls)
	}
}

func TestRetrieveCustomerSpecialistEvidenceTechnicalWhitelistReadsProcedure(t *testing.T) {
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			raw, err := json.Marshal([]map[string]any{{"path": "wiki/procedures/si-ye-tian-api-whitelist-setup.md", "score": 100}})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			path, _ := args["path"].(string)
			if path != "wiki/procedures/si-ye-tian-api-whitelist-setup.md" {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow, Error: &runtime.ToolError{Code: "UNEXPECTED_PATH", Message: path}}, nil
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": "---\ntitle: API 白名单\n---\n添加当前出口公网 IP 到授权白名单。"}}, nil
		}},
	)
	svc := newTestCustomerChatService(t, rt)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-whitelist-procedure", &CustomerRouterOutput{
		Specialist:        "technical",
		RewrittenQuestion: "客户询问 API 白名单怎么添加。",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"四叶天 API 白名单 添加 出口公网 IP"},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected technical retrieval without error, got %q", result.Error)
	}
	if len(result.Sources) != 1 || result.Sources[0].Path != "wiki/procedures/si-ye-tian-api-whitelist-setup.md" {
		t.Fatalf("expected whitelist procedure evidence, got %+v", result.Sources)
	}
	if len(result.CacheTrace.RetrievalResults) != 1 {
		t.Fatalf("expected retrieval result trace, got %+v", result.CacheTrace)
	}
}

func TestBuildCustomerEvidencePreviewKeepsStaticSwitchEntryURL(t *testing.T) {
	body := `# 四叶天静态 IP 使用与切换流程

## Summary

静态 IP 使用与切换流程用于处理“静态IP怎么用”“静态IP怎么切换”“静态IP怎么换IP/换地区”这类操作问题。正式事实以 [[si-ye-tian-static-ip]] 为准；本页只沉淀购买、安装、连接、切换和验证的流程。

## Preconditions

- 已确认需要相对固定的出口 IP，而不是高频更换；需要频繁更换时应改用动态 IP，不要依赖静态 IP 高频切换。
- 已确认产品类型：数据中心静态 IP（购买期内 IP 固定）或住宅静态 IP（信任度高，但可能在同城范围内跳 IP）。
- 已确认使用端：电脑（Windows/Mac）或手机。
- 切换前先登录会员中心确认当前套餐、地区和剩余切换次数。

## Steps

1. 购买静态 IP：购买入口 ` + "`https://www.siyetian.com/staticip.html`" + `；选择共享型或独享型、带宽和节点，价格与有效期以官网或人工确认为准。
2. 下载并安装软件：下载入口 ` + "`https://www.siyetian.com/download.html`" + `；Windows 客户端可选择静态 IP（数据中心 IP）或住宅 IP、带宽和节点。
3. 登录会员中心查看与管理已购静态 IP，登录后的查看、续费和切换入口包括 ` + "`https://www.siyetian.com/member/jingtai.html`" + `、` + "`https://www.siyetian.com/member/staticip.html`" + ` 和 ` + "`https://www.siyetian.com/member/house.html`" + `；具体可见资源以当前账号后台为准。
4. 在会员中心对应产品页对静态 IP 执行手动切换（更换地区/线路或重新分配）；静态 IP 支持手动切换，主来源记录为每月 5 次切换机会。`

	preview := buildCustomerEvidencePreview(body, "wiki/procedures/si-ye-tian-static-ip-usage.md", "我的静态IP怎么切换成另一个IP地址？", 1800)
	for _, want := range []string{
		"https://www.siyetian.com/member/staticip.html",
		"执行手动切换",
		"每月 5 次",
	} {
		if !strings.Contains(preview, want) {
			t.Fatalf("expected preview to include %q, got:\n%s", want, preview)
		}
	}
	if strings.Contains(preview, "\uFFFD") {
		t.Fatalf("expected preview not to contain replacement chars, got:\n%s", preview)
	}
}

func TestRetrieveCustomerSpecialistEvidenceSafetyReadsPolicyBoundary(t *testing.T) {
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			raw, err := json.Marshal([]map[string]any{
				{"path": "wiki/sources/si-ye-tian-safety-boundaries-source.md", "score": 120},
				{"path": "wiki/policies/si-ye-tian-safety-boundaries.md", "score": 100},
			})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			path, _ := args["path"].(string)
			if path != "wiki/policies/si-ye-tian-safety-boundaries.md" {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow, Error: &runtime.ToolError{Code: "UNEXPECTED_PATH", Message: path}}, nil
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": "---\ntitle: 安全边界\n---\n不承诺绕过平台风控或访问受限服务。"}}, nil
		}},
	)
	svc := newTestCustomerChatService(t, rt)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-safety-boundary", &CustomerRouterOutput{
		Specialist:        "safety",
		RewrittenQuestion: "客户询问海外 IP 是否可以稳定访问 Google 和 ChatGPT。",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"四叶天 海外 IP Google ChatGPT 访问边界"},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected safety retrieval without error, got %q", result.Error)
	}
	if len(result.Sources) != 1 || result.Sources[0].Path != "wiki/policies/si-ye-tian-safety-boundaries.md" {
		t.Fatalf("expected safety policy evidence, got %+v", result.Sources)
	}
	if len(result.CacheTrace.ScopeFilteredPages) == 0 {
		t.Fatalf("expected raw source page to be scope-filtered, got %+v", result.CacheTrace)
	}
	if len(result.CacheTrace.RetrievalResults) != 1 {
		t.Fatalf("expected safety query-to-candidate trace, got %+v", result.CacheTrace.RetrievalResults)
	}
}

func TestRetrieveCustomerSpecialistEvidenceMultiProductPricingReadsMultipleProductEvidence(t *testing.T) {
	qmdCalls := 0
	rt := testRuntime(
		testRuntimeTool{name: "exec.qmd", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			qmdCalls++
			question, _ := args["question"].(string)
			path := "wiki/knowledge/si-ye-tian-static-ip-pricing.md"
			if strings.Contains(question, "动态") {
				path = "wiki/knowledge/si-ye-tian-dynamic-ip-pricing.md"
			}
			raw, err := json.Marshal([]map[string]any{{"path": path, "score": 100}})
			if err != nil {
				return runtime.ToolResult{}, err
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"stdout": string(raw)}}, nil
		}},
		testRuntimeTool{name: "wiki.search_pages"},
		testRuntimeTool{name: "wiki.read_page", fn: func(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
			path, _ := args["path"].(string)
			pages := map[string]string{
				"wiki/knowledge/si-ye-tian-static-ip-pricing.md":  "---\ntitle: 静态 IP 价格\n---\n静态 IP 公开价格证据。",
				"wiki/knowledge/si-ye-tian-dynamic-ip-pricing.md": "---\ntitle: 动态 IP 价格\n---\n动态 IP 公开价格证据。",
			}
			content := pages[path]
			if content == "" {
				return runtime.ToolResult{Success: false, RiskLevel: runtime.RiskLow, Error: &runtime.ToolError{Code: "NOT_FOUND", Message: path}}, nil
			}
			return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskLow, Data: map[string]any{"content": content}}, nil
		}},
	)
	svc := newTestCustomerChatService(t, rt)
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-multi-product-pricing", &CustomerRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "客户想比较动态 IP 和静态 IP 的价格。",
		NeedsRetrieval:    true,
		RetrievalQueries: []string{
			"四叶天 静态 IP 价格",
			"四叶天 动态 IP 价格",
		},
	}, RuntimeSettings{})
	if result.Error != "" {
		t.Fatalf("expected multi-product pricing retrieval without error, got %q", result.Error)
	}
	if qmdCalls != 2 {
		t.Fatalf("expected two product-specific retrieval queries, got %d", qmdCalls)
	}
	paths := map[string]bool{}
	for _, source := range result.Sources {
		paths[source.Path] = true
	}
	for _, path := range []string{
		"wiki/knowledge/si-ye-tian-static-ip-pricing.md",
		"wiki/knowledge/si-ye-tian-dynamic-ip-pricing.md",
	} {
		if !paths[path] {
			t.Fatalf("expected multi-product evidence %s, got %+v", path, result.Sources)
		}
	}
	if len(result.CacheTrace.RetrievalResults) != 2 {
		t.Fatalf("expected query-to-candidate trace for both product queries, got %+v", result.CacheTrace.RetrievalResults)
	}
}

func TestRetrieveCustomerSpecialistCacheExpires(t *testing.T) {
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
	svc := newTestCustomerChatService(t, rt)
	svc.cache = newCustomerChatCache(time.Millisecond)
	routerOutput := &CustomerRouterOutput{
		Specialist:        "pricing",
		RewrittenQuestion: "静态 IP 怎么卖",
		NeedsRetrieval:    true,
		RetrievalQueries:  []string{"静态 IP 价格"},
	}
	_ = svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-expire-1", routerOutput, RuntimeSettings{})
	time.Sleep(2 * time.Millisecond)
	_ = svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-expire-2", routerOutput, RuntimeSettings{})
	if qmdCalls != 2 {
		t.Fatalf("expected qmd cache to expire, got %d qmd calls", qmdCalls)
	}
	if readCalls != 2 {
		t.Fatalf("expected page cache to expire, got %d read calls", readCalls)
	}
}

func TestRetrieveCustomerSpecialistEvidenceScopesRetrievedPages(t *testing.T) {
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
				"wiki/sources/raw-pricing-note.md":                           "---\ntitle: 原始价格笔记\n---\n这不是 customer specialist 证据。",
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
	svc := NewCustomerChatService(Deps{
		Config:    &config.Config{MountedWiki: config.MountedWikiConfig{Root: t.TempDir(), QMDIndex: "test"}},
		Runtime:   rt,
		Retriever: retrieval.NewQMDRetriever(rt),
	})
	result := svc.retrieveCustomerSpecialistEvidence(context.Background(), "trace-specialist", &CustomerRouterOutput{
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
	for _, candidate := range customerRetrievedPageSummaries(result.Candidates, 12) {
		path, _ := candidate["path"].(string)
		if path == "wiki/procedures/si-ye-tian-api-whitelist-setup.md" || path == "wiki/sources/raw-pricing-note.md" {
			t.Fatalf("expected scoped candidates to exclude %s, got %+v", path, result.Candidates)
		}
	}
	for _, source := range result.Sources {
		if !customerSpecialistProfile("pricing").AllowsPath(source.Path) {
			t.Fatalf("expected source %s to stay inside pricing scope, got %+v", source.Path, result.Sources)
		}
	}
	if len(result.Sources) == 0 {
		t.Fatalf("expected at least one scoped source, got %+v", result)
	}
	if len(result.CacheTrace.ScopeFilteredPages) < 2 {
		t.Fatalf("expected scope-filter trace for procedure and source pages, got %+v", result.CacheTrace.ScopeFilteredPages)
	}
	if len(result.CacheTrace.RetrievalResults) != 1 {
		t.Fatalf("expected query-to-candidate trace, got %+v", result.CacheTrace.RetrievalResults)
	}
}

func newTestCustomerChatService(t *testing.T, rt *runtime.Runtime) *CustomerChatService {
	t.Helper()
	return newTestCustomerChatServiceWithRoot(t, rt, t.TempDir())
}

func newTestCustomerChatServiceWithRoot(t *testing.T, rt *runtime.Runtime, root string) *CustomerChatService {
	t.Helper()
	return NewCustomerChatService(Deps{
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
