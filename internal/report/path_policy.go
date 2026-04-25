package report

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Kind string

const (
	KindIngest  Kind = "ingest"
	KindLint    Kind = "lint"
	KindReflect Kind = "reflect"
	KindMerge   Kind = "merge"
	KindRepair  Kind = "repair"
	KindSync    Kind = "sync"
)

var (
	reportSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	reportPathPattern = map[Kind]*regexp.Regexp{
		KindIngest:  regexp.MustCompile(`^wiki/outputs/ingest/\d{4}-\d{2}-\d{2}-[a-z0-9]+(?:-[a-z0-9]+)*-ingest-report\.md$`),
		KindLint:    regexp.MustCompile(`^wiki/outputs/lint/\d{4}-\d{2}-\d{2}-health-check-report\.md$`),
		KindReflect: regexp.MustCompile(`^wiki/outputs/reflect/\d{4}-\d{2}-\d{2}-[a-z0-9]+(?:-[a-z0-9]+)*-reflect-report\.md$`),
		KindMerge:   regexp.MustCompile(`^wiki/outputs/merge/\d{4}-\d{2}-\d{2}-[a-z0-9]+(?:-[a-z0-9]+)*-merge-report\.md$`),
		KindRepair:  regexp.MustCompile(`^wiki/outputs/repair/\d{4}-\d{2}-\d{2}-[a-z0-9]+(?:-[a-z0-9]+)*-repair-report\.md$`),
		KindSync:    regexp.MustCompile(`^wiki/outputs/sync/\d{4}-\d{2}-\d{2}-sync-report\.md$`),
	}
)

func BuildPath(kind Kind, slug string, date time.Time) (string, error) {
	day := date.Format("2006-01-02")
	switch kind {
	case KindLint:
		return fmt.Sprintf("wiki/outputs/lint/%s-health-check-report.md", day), nil
	case KindSync:
		return fmt.Sprintf("wiki/outputs/sync/%s-sync-report.md", day), nil
	case KindIngest, KindReflect, KindMerge, KindRepair:
		cleanSlug := NormalizeSlug(slug)
		if cleanSlug == "" {
			return "", fmt.Errorf("%s report slug is required", kind)
		}
		return fmt.Sprintf("wiki/outputs/%s/%s-%s-%s-report.md", kind, day, cleanSlug, kind), nil
	default:
		return "", fmt.Errorf("unsupported report kind %q", kind)
	}
}

func ValidatePath(path string) error {
	clean := cleanReportPath(path)
	if !strings.HasPrefix(clean, "wiki/outputs/") {
		return fmt.Errorf("report must be written under wiki/outputs/")
	}
	for kind, pattern := range reportPathPattern {
		if pattern.MatchString(clean) {
			return nil
		}
		if strings.HasPrefix(clean, "wiki/outputs/"+string(kind)+"/") {
			return fmt.Errorf("invalid %s report path %q; expected %s", kind, clean, ExpectedPattern(kind))
		}
	}
	return fmt.Errorf("invalid report path %q; expected wiki/outputs/<ingest|lint|reflect|merge|repair|sync>/...", clean)
}

func IsOutputPath(path string) bool {
	return strings.HasPrefix(cleanReportPath(path), "wiki/outputs/")
}

func ExpectedPattern(kind Kind) string {
	switch kind {
	case KindIngest:
		return "wiki/outputs/ingest/YYYY-MM-DD-<source-slug>-ingest-report.md"
	case KindLint:
		return "wiki/outputs/lint/YYYY-MM-DD-health-check-report.md"
	case KindReflect:
		return "wiki/outputs/reflect/YYYY-MM-DD-<topic>-reflect-report.md"
	case KindMerge:
		return "wiki/outputs/merge/YYYY-MM-DD-<primary-slug>-merge-report.md"
	case KindRepair:
		return "wiki/outputs/repair/YYYY-MM-DD-<topic>-repair-report.md"
	case KindSync:
		return "wiki/outputs/sync/YYYY-MM-DD-sync-report.md"
	default:
		return "wiki/outputs/<kind>/YYYY-MM-DD-<slug>-<kind>-report.md"
	}
}

func NormalizeSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if reportSlugPattern.MatchString(out) {
		return out
	}
	return ""
}

func cleanReportPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	return strings.TrimPrefix(path, "./")
}
