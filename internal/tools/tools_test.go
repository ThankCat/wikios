package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
	"wikios/internal/runtime"
	"wikios/internal/tools"
	"wikios/internal/wikiadapter"
)

func TestLintRunOnKnowledgeBase(t *testing.T) {
	cfg := testConfig("/Users/chenhao/Project/knowledge-base", t.TempDir())
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{
		WikiRoot:     cfg.MountedWiki.Root,
		WorkspaceDir: cfg.Workspace.BaseDir,
		Mode:         "admin",
		QMDIndex:     cfg.MountedWiki.QMDIndex,
	}
	result, err := rt.Execute(context.Background(), env, runtime.ToolCall{Name: "lint.run"})
	if err != nil || !result.Success {
		t.Fatalf("lint.run failed: %+v %v", result, err)
	}
	if result.Data["report_path"] == "" {
		t.Fatalf("expected report path")
	}
}

func TestPatchAndWorkspaceRestrictions(t *testing.T) {
	root := createFixtureWiki(t)
	cfg := testConfig(root, t.TempDir())
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{
		WikiRoot:     root,
		WorkspaceDir: cfg.Workspace.BaseDir,
		Mode:         "admin",
		QMDIndex:     cfg.MountedWiki.QMDIndex,
		JobID:        "job-1",
	}
	if _, err := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "workspace.create_job_dir",
		Args: map[string]any{"job_id": "job-1"},
	}); err != nil {
		t.Fatalf("create_job_dir: %v", err)
	}
	if _, err := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "workspace.write_temp_file",
		Args: map[string]any{"job_id": "job-1", "path": "draft.md", "content": "hello"},
	}); err != nil {
		t.Fatalf("write_temp_file: %v", err)
	}
	result, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "workspace.commit_temp_to_wiki",
		Args: map[string]any{"job_id": "job-1", "temp_path": "draft.md", "target_path": "raw/nope.md"},
	})
	if result.Success {
		t.Fatalf("expected raw write rejection")
	}
	patchResult, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "wiki.patch_page",
		Args: map[string]any{
			"path": "wiki/concepts/test.md",
			"ops": []any{
				map[string]any{"type": "append_section", "section": "## Evolution Log", "content": "- 2026-04-22 change"},
			},
		},
	})
	if !patchResult.Success {
		t.Fatalf("expected patch success: %+v", patchResult)
	}
	content, err := os.ReadFile(filepath.Join(root, "wiki/concepts/test.md"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if !strings.Contains(string(content), "2026-04-22 change") {
		t.Fatalf("patch not applied")
	}
	badPatch, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "wiki.patch_page",
		Args: map[string]any{
			"path": "wiki/concepts/test.md",
			"ops": []any{
				map[string]any{"type": "replace_section", "section": "## Missing", "content": "x"},
			},
		},
	})
	if badPatch.Success {
		t.Fatalf("expected patch failure")
	}
}

func TestExecRestrictionsAndRepair(t *testing.T) {
	root := createFixtureWiki(t)
	cfg := testConfig(root, t.TempDir())
	cfg.Sandbox.PythonTimeoutSec = 1
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{
		WikiRoot:     root,
		WorkspaceDir: cfg.Workspace.BaseDir,
		Mode:         "admin",
		QMDIndex:     cfg.MountedWiki.QMDIndex,
		JobID:        "job-2",
	}
	invalid, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "exec.qmd",
		Args: map[string]any{"subcommand": "delete"},
	})
	if invalid.Success {
		t.Fatalf("expected invalid qmd subcommand rejection")
	}
	_, _ = rt.Execute(context.Background(), env, runtime.ToolCall{Name: "workspace.create_job_dir", Args: map[string]any{"job_id": "job-2"}})
	pyResult, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "exec.python",
		Args: map[string]any{"job_id": "job-2", "script": "import time\ntime.sleep(2)\nprint('done')"},
	})
	if pyResult.Success {
		t.Fatalf("expected python timeout failure")
	}
	proposal, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "repair.create_high_risk_proposal",
		Args: map[string]any{"title": "merge duplicate", "summary": "needs review"},
	})
	if !proposal.Success || proposal.Data["proposal_id"] == "" {
		t.Fatalf("expected proposal id: %+v", proposal)
	}
}

func TestGitStatusCleanAndDirty(t *testing.T) {
	root := createFixtureWiki(t)
	cfg := testConfig(root, t.TempDir())
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{WikiRoot: root, WorkspaceDir: cfg.Workspace.BaseDir, Mode: "admin", QMDIndex: cfg.MountedWiki.QMDIndex}
	clean, _ := rt.Execute(context.Background(), env, runtime.ToolCall{Name: "git.status"})
	if !clean.Success {
		t.Fatalf("expected clean status")
	}
	if err := os.WriteFile(filepath.Join(root, "wiki/concepts/test.md"), []byte("---\ntype: concept\ntitle: test\n---\n\n## Definition\n\nchanged\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	dirty, _ := rt.Execute(context.Background(), env, runtime.ToolCall{Name: "git.status"})
	if !dirty.Success || !strings.Contains(dirty.Data["stdout"].(string), "wiki/concepts/test.md") {
		t.Fatalf("expected dirty git status: %+v", dirty)
	}
}

func newRuntime(cfg *config.Config) *runtime.Runtime {
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root),
	})
	return runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
}

func testConfig(root string, workspace string) *config.Config {
	return &config.Config{
		MountedWiki: config.MountedWikiConfig{Root: root, QMDIndex: "test-index"},
		Workspace:   config.WorkspaceConfig{BaseDir: workspace, DefaultTimeoutSec: 2},
		Sandbox:     config.SandboxConfig{PythonTimeoutSec: 1, QMDTimeoutSec: 2},
		Sync:        config.SyncConfig{Remote: "origin", Branch: "main"},
	}
}

func createFixtureWiki(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, "wiki", "concepts"))
	mustMkdirAll(t, filepath.Join(root, "wiki", "entities"))
	mustMkdirAll(t, filepath.Join(root, "wiki", "sources"))
	mustMkdirAll(t, filepath.Join(root, "wiki", "synthesis"))
	mustMkdirAll(t, filepath.Join(root, "wiki", "outputs"))
	mustMkdirAll(t, filepath.Join(root, "wiki", "templates"))
	mustMkdirAll(t, filepath.Join(root, "raw"))
	mustWrite(t, filepath.Join(root, "wiki", "log.md"), "---\ntype: system-log\ngraph-excluded: true\n---\n\n# System Log\n")
	mustWrite(t, filepath.Join(root, "wiki", "index.md"), "---\ntype: system-index\ngraph-excluded: true\n---\n\n# System Index\n\n## Sources\n")
	mustWrite(t, filepath.Join(root, "wiki", "QUESTIONS.md"), "---\ntype: system-questions\ngraph-excluded: true\n---\n\n# Questions\n\n## Open Questions\n")
	mustWrite(t, filepath.Join(root, "wiki", "overview.md"), "---\ntype: system-overview\ngraph-excluded: true\n---\n\n# System Overview\n")
	mustWrite(t, filepath.Join(root, "wiki", "templates", "source-template.md"), "---\ntype: source\ntitle: \"\"\ndate: 2026-04-22\nprocessed: false\n---\n\n## Summary\n\n## Key Points\n\n## Concepts Extracted\n\n## Entities Extracted\n\n## Contradictions\n\n## My Notes\n")
	mustWrite(t, filepath.Join(root, "wiki", "concepts", "test.md"), "---\ntype: concept\ntitle: test\ndate: 2026-04-22\naliases:\n  - test\n---\n\n## Definition\n\ntext\n\n## Evolution Log\n")
	mustWrite(t, filepath.Join(root, "scripts", "lint.py"), "#!/usr/bin/env python3\nfrom pathlib import Path\np=Path('wiki/outputs/lint-2026-04-22.md')\np.write_text('ok', encoding='utf-8')\nprint('Wrote lint report to wiki/outputs/lint-2026-04-22.md')\n")
	run(t, root, "git", "init", "-b", "main")
	run(t, root, "git", "config", "user.email", "test@example.com")
	run(t, root, "git", "config", "user.name", "Test")
	run(t, root, "git", "add", ".")
	run(t, root, "git", "commit", "-m", "init")
	run(t, root, "git", "remote", "add", "origin", "https://example.com/repo.git")
	return root
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, string(out))
	}
}
