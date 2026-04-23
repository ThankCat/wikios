package retrieval

import "testing"

func TestParseQMDQueryNormalizesQMDPaths(t *testing.T) {
	results := parseQMDQuery(`[{"path":"qmd://wiki/sources/faq-faq-segment-05.md","score":0.98},"qmd://wiki/concepts/bandwidth.md"]`)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %+v", results)
	}
	if results[0].Path != "wiki/sources/faq-faq-segment-05.md" {
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
