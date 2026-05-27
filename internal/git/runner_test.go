package git

import (
	"strings"
	"testing"
)

func TestRedactRemoteURLPreservesWhitespaceAndRedactsSecrets(t *testing.T) {
	input := "## main...origin/main\x00 M wiki/index.md\x00?? outputs/lint-2026-05-21.md https://x-access-token:secret-token@github.com/acme/wiki.git\n"
	got := RedactRemoteURL(input)
	if !strings.Contains(got, "\x00 M wiki/index.md\x00") {
		t.Fatalf("expected whitespace to be preserved, got %q", got)
	}
	if strings.Contains(got, "secret-token") || !strings.Contains(got, "redacted") {
		t.Fatalf("expected credentials to be redacted, got %q", got)
	}
}
