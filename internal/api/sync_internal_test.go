package api

import (
	"strings"
	"testing"

	wikigit "wikios/internal/git"
)

func TestValidateSyncPathsRequiresExplicitStatusPaths(t *testing.T) {
	files := []syncStatusFile{
		{Path: "wiki/sources/a.md"},
		{Path: ".obsidian/workspace.json"},
	}
	paths, err := validateSyncPaths([]string{"wiki/sources/a.md"}, files)
	if err != nil {
		t.Fatalf("validate paths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "wiki/sources/a.md" {
		t.Fatalf("unexpected paths: %#v", paths)
	}
	if _, err := validateSyncPaths(nil, files); err == nil {
		t.Fatal("expected empty paths to fail")
	}
	if _, err := validateSyncPaths([]string{"wiki/sources/missing.md"}, files); err == nil {
		t.Fatal("expected unknown path to fail")
	}
	if _, err := validateSyncPaths([]string{"../outside.md"}, files); err == nil {
		t.Fatal("expected path traversal to fail")
	}
}

func TestValidateSyncPathsSplitsPersistedJoinedPath(t *testing.T) {
	files := []syncStatusFile{
		{Path: "outputs/lint-2026-05-21.md"},
		{Path: "wiki/intents/overseas-ip-domestic-use.md"},
		{Path: "wiki/sources/user-correction-overseas-ip-domestic-direct-source.md"},
	}
	paths, err := validateSyncPaths([]string{"outputs/lint-2026-05-21.md wiki/intents/overseas-ip-domestic-use.md wiki/sources/user-correction-overseas-ip-domestic-direct-source.md"}, files)
	if err != nil {
		t.Fatalf("validate joined paths: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected joined path to split into 3 paths, got %#v", paths)
	}
	for i, file := range files {
		if paths[i] != file.Path {
			t.Fatalf("path %d mismatch: got %q want %q", i, paths[i], file.Path)
		}
	}
}

func TestParseStatusLineKeepsObsidianUnchecked(t *testing.T) {
	file, ok := parseStatusLine("?? .obsidian/workspace.json")
	if !ok {
		t.Fatal("expected status line")
	}
	if file.DefaultOn {
		t.Fatal("obsidian files should not be selected by default")
	}
	if file.Preview != "download" {
		t.Fatalf("expected download preview, got %s", file.Preview)
	}
}

func TestParseStatusLineKeepsUntrackedFiles(t *testing.T) {
	file, ok := parseStatusLine("?? outputs/lint-2026-05-20.md")
	if !ok {
		t.Fatal("expected untracked file status line")
	}
	if file.Path != "outputs/lint-2026-05-20.md" || file.Status != "?" || file.Index != "?" || file.Worktree != "?" {
		t.Fatalf("unexpected parsed file: %+v", file)
	}
	if !file.DefaultOn {
		t.Fatal("untracked non-obsidian files should be selected by default")
	}
}

func TestParseStatusOutputZKeepsSeparateUntrackedFiles(t *testing.T) {
	status := syncStatusResponse{}
	files := parseStatusOutput("## main...origin/main\x00?? outputs/lint-2026-05-21.md\x00?? wiki/intents/overseas-ip-domestic-use.md\x00?? wiki/sources/user-correction-overseas-ip-domestic-direct-source.md\x00", &status)
	if status.Branch != "main" {
		t.Fatalf("expected branch main, got %q", status.Branch)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %#v", files)
	}
	want := []string{
		"outputs/lint-2026-05-21.md",
		"wiki/intents/overseas-ip-domestic-use.md",
		"wiki/sources/user-correction-overseas-ip-domestic-direct-source.md",
	}
	for i, path := range want {
		if files[i].Path != path || files[i].Status != "?" {
			t.Fatalf("file %d mismatch: %+v", i, files[i])
		}
	}
}

func TestMergeUntrackedFilesAddsMissingEntries(t *testing.T) {
	files := []syncStatusFile{{Path: "wiki/a.md", Status: "M"}}
	untracked := []syncStatusFile{
		{Path: "outputs/lint-2026-05-20.md", Status: "?"},
		{Path: "wiki/a.md", Status: "?"},
	}
	merged := mergeUntrackedFiles(files, untracked)
	if len(merged) != 2 {
		t.Fatalf("expected one missing untracked file to be merged, got %#v", merged)
	}
	if merged[1].Path != "outputs/lint-2026-05-20.md" {
		t.Fatalf("unexpected merged file: %#v", merged[1])
	}
}

func TestRedactRemoteURLHidesEmbeddedCredentials(t *testing.T) {
	got := wikigit.RedactRemoteURL("https://x-access-token:secret-token@github.com/acme/wiki.git")
	if strings.Contains(got, "secret-token") || !strings.Contains(got, "redacted") {
		t.Fatalf("expected redacted url, got %q", got)
	}
}

func TestNonInteractiveSSHCommandKeepsConfiguredIdentity(t *testing.T) {
	got := wikigit.NonInteractiveSSHCommand("ssh -i /Users/chenhao/.ssh/llm_wiki_github -F /tmp/ssh_config")
	if !strings.Contains(got, "-i /Users/chenhao/.ssh/llm_wiki_github") || !strings.Contains(got, "BatchMode=yes") || !strings.Contains(got, "NumberOfPasswordPrompts=0") {
		t.Fatalf("unexpected ssh command: %q", got)
	}
}
