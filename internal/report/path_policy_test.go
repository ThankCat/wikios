package report

import (
	"testing"
	"time"
)

func TestBuildPath(t *testing.T) {
	day := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		kind Kind
		slug string
		want string
	}{
		{KindIngest, "faq-source", "wiki/outputs/ingest/2026-04-25-faq-source-ingest-report.md"},
		{KindLint, "", "wiki/outputs/lint/2026-04-25-health-check-report.md"},
		{KindReflect, "proxy-ip", "wiki/outputs/reflect/2026-04-25-proxy-ip-reflect-report.md"},
		{KindMerge, "static-ip", "wiki/outputs/merge/2026-04-25-static-ip-merge-report.md"},
		{KindRepair, "sha-fix", "wiki/outputs/repair/2026-04-25-sha-fix-repair-report.md"},
		{KindSync, "", "wiki/outputs/sync/2026-04-25-sync-report.md"},
	}
	for _, tc := range cases {
		got, err := BuildPath(tc.kind, tc.slug, day)
		if err != nil {
			t.Fatalf("BuildPath(%s): %v", tc.kind, err)
		}
		if got != tc.want {
			t.Fatalf("BuildPath(%s)=%q, want %q", tc.kind, got, tc.want)
		}
		if err := ValidatePath(got); err != nil {
			t.Fatalf("ValidatePath(%q): %v", got, err)
		}
	}
}

func TestValidatePathRejectsInvalidReportPaths(t *testing.T) {
	for _, path := range []string{
		"wiki/outputs/output.md",
		"wiki/outputs/lint-2026-04-25.md",
		"wiki/outputs/lint/2026-04-25-lint-report.md",
		"wiki/outputs/repair/foo.md",
		"wiki/outputs/ingest/2026-04-25-faq.md",
	} {
		if err := ValidatePath(path); err == nil {
			t.Fatalf("expected %q to be rejected", path)
		}
	}
}

func TestIsOutputPath(t *testing.T) {
	if !IsOutputPath("./wiki/outputs/ingest/2026-04-25-faq-ingest-report.md") {
		t.Fatalf("expected output path")
	}
	if IsOutputPath("wiki/sources/faq-source.md") {
		t.Fatalf("expected non-output wiki path")
	}
}
