package service

import "testing"

func TestAuditSpecialistNameReadsProfileMapName(t *testing.T) {
	details := map[string]any{
		"specialist": map[string]any{
			"name":               "technical",
			"prompt_file":        "customer_specialist_technical.md",
			"allowed_prefixes":   []string{"wiki/procedures/", "wiki/knowledge/"},
			"candidate_top_k":    4,
			"max_evidence_chars": 1800,
		},
	}

	if got := auditSpecialistName(details); got != "technical" {
		t.Fatalf("expected profile map to resolve specialist name, got %q", got)
	}
}

func TestAuditSpecialistNameFallsBackToRouterOutput(t *testing.T) {
	details := map[string]any{
		"specialist": "map[allowed_prefixes:[wiki/procedures/] name:technical]",
		"router": map[string]any{
			"output": map[string]any{
				"specialist": "troubleshooting",
			},
		},
	}

	if got := auditSpecialistName(details); got != "troubleshooting" {
		t.Fatalf("expected router output fallback, got %q", got)
	}
}
