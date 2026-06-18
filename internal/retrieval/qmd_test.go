package retrieval

import (
	"context"
	"testing"

	"wikios/internal/runtime"
)

func TestParseQMDQueryNormalizesQMDPaths(t *testing.T) {
	results := parseQMDQuery(`[{"path":"qmd://wiki/sources/customer-source.md","score":0.98},"qmd://wiki/concepts/bandwidth.md"]`)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %+v", results)
	}
	if results[0].Path != "wiki/sources/customer-source.md" {
		t.Fatalf("expected normalized source path, got %s", results[0].Path)
	}
	if results[1].Path != "wiki/concepts/bandwidth.md" {
		t.Fatalf("expected normalized concept path, got %s", results[1].Path)
	}
}

func TestNormalizeRetrievedPathKeepsWikiRelativePath(t *testing.T) {
	if got := normalizeRetrievedPath("wiki/sources/customer-qa.md"); got != "wiki/sources/customer-qa.md" {
		t.Fatalf("expected unchanged wiki path, got %s", got)
	}
}

type testTool struct {
	name string
	fn   func(context.Context, *runtime.ExecEnv, map[string]any) (runtime.ToolResult, error)
}

func (t testTool) Name() string { return t.name }
func (t testTool) RiskLevel() runtime.RiskLevel {
	return runtime.RiskLow
}
func (t testTool) Validate(map[string]any) error { return nil }
func (t testTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	return t.fn(ctx, env, args)
}

func TestWikiRetrieverSkipsQMDTool(t *testing.T) {
	registry := runtime.NewRegistry(
		testTool{name: "exec.qmd", fn: func(context.Context, *runtime.ExecEnv, map[string]any) (runtime.ToolResult, error) {
			t.Fatalf("wiki retriever must not call exec.qmd")
			return runtime.ToolResult{}, nil
		}},
		testTool{name: "wiki.search_pages", fn: func(context.Context, *runtime.ExecEnv, map[string]any) (runtime.ToolResult, error) {
			return runtime.ToolResult{
				Success:   true,
				RiskLevel: runtime.RiskLow,
				Data: map[string]any{"matches": []map[string]any{
					{"path": "wiki/knowledge/pricing.md", "score": 12},
				}},
			}, nil
		}},
	)
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	pages, err := NewWikiRetriever(rt).Retrieve(context.Background(), &runtime.ExecEnv{Mode: "customer"}, "动态 IP 价格", 5)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(pages) != 1 || pages[0].Path != "wiki/knowledge/pricing.md" {
		t.Fatalf("unexpected pages: %+v", pages)
	}
}
