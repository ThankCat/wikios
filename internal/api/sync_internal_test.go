package api

import "testing"

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

func TestParseStatusLineKeepsObsidianUnchecked(t *testing.T) {
	file, ok := parseStatusLine("?? .obsidian/workspace.json")
	if !ok {
		t.Fatal("expected status line")
	}
	if file.DefaultOn {
		t.Fatal("obsidian files should not be selected by default")
	}
	if file.Preview != "json" {
		t.Fatalf("expected json preview, got %s", file.Preview)
	}
}
