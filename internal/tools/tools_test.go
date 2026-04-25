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
	root := createFixtureWiki(t)
	cfg := testConfig(root, t.TempDir())
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{
		WikiRoot:     root,
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

func TestExecShellPrefersConfiguredQMDNodePath(t *testing.T) {
	root := createFixtureWiki(t)
	preferredNodeDir := t.TempDir()
	t.Setenv("WIKIOS_QMD_NODE_BIN", preferredNodeDir)
	cfg := testConfig(root, t.TempDir())
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{
		WikiRoot:     root,
		WorkspaceDir: cfg.Workspace.BaseDir,
		Mode:         "admin_direct",
		QMDIndex:     cfg.MountedWiki.QMDIndex,
	}
	result, err := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "exec.shell",
		Args: map[string]any{"command": "printf '%s' \"$PATH\""},
	})
	if err != nil || !result.Success {
		t.Fatalf("exec.shell failed: %+v %v", result, err)
	}
	stdout, _ := result.Data["stdout"].(string)
	firstPath := strings.Split(stdout, ":")[0]
	if firstPath != preferredNodeDir {
		t.Fatalf("expected preferred qmd node path first, got %q in %q", firstPath, stdout)
	}
}

func TestWikiWriteOutputAllowsLLMGovernedOutputPaths(t *testing.T) {
	root := createFixtureWiki(t)
	cfg := testConfig(root, t.TempDir())
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{
		WikiRoot:     root,
		WorkspaceDir: cfg.Workspace.BaseDir,
		Mode:         "admin",
		QMDIndex:     cfg.MountedWiki.QMDIndex,
	}
	invalid, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "wiki.write_output",
		Args: map[string]any{"path": "wiki/outputs/output.md", "content": "---\ngraph-excluded: true\n---\n\nbad"},
	})
	if !invalid.Success {
		t.Fatalf("expected LLM-governed output path to be allowed by tool layer: %+v", invalid)
	}
	valid, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "wiki.write_output",
		Args: map[string]any{"path": "wiki/outputs/repair/2026-04-25-sha-fix-repair-report.md", "content": "---\ngraph-excluded: true\n---\n\nok"},
	})
	if !valid.Success {
		t.Fatalf("expected valid report path: %+v", valid)
	}
	nonReport, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "wiki.write_output",
		Args: map[string]any{"path": "wiki/sources/faq-source.md", "content": "---\ntype: source\ntitle: FAQ Source\n---\n\nok"},
	})
	if !nonReport.Success {
		t.Fatalf("expected non-report wiki write to stay allowed: %+v", nonReport)
	}
}

func TestExecShellAllowsLLMGovernedReportPaths(t *testing.T) {
	root := createFixtureWiki(t)
	cfg := testConfig(root, t.TempDir())
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{
		WikiRoot:     root,
		WorkspaceDir: cfg.Workspace.BaseDir,
		Mode:         "admin_direct",
		QMDIndex:     cfg.MountedWiki.QMDIndex,
	}
	result, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "exec.shell",
		Args: map[string]any{"command": "printf ok > wiki/outputs/output.md"},
	})
	if !result.Success {
		t.Fatalf("expected shell report path to be governed by AGENT, not server tool layer: %+v", result)
	}
	valid, _ := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "exec.shell",
		Args: map[string]any{"command": "mkdir -p wiki/outputs/lint && printf ok > wiki/outputs/lint/2026-04-25-health-check-report.md"},
	})
	if !valid.Success {
		t.Fatalf("expected valid shell report path: %+v", valid)
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

func TestWikiSearchPagesMatchesChineseQuestionFallback(t *testing.T) {
	root := createFixtureWiki(t)
	mustWrite(t, filepath.Join(root, "wiki", "sources", "network.md"), "---\ntype: source\ntitle: 网络知识\ndate: 2026-04-22\n---\n\n## Summary\n\n静态IP适用于账号长期运营、白名单绑定、远程办公等需要稳定网络环境的场景。\n")
	mustWrite(t, filepath.Join(root, "wiki", "concepts", "static-ip.md"), "---\ntype: concept\ntitle: static-ip\ndate: 2026-04-22\naliases:\n  - 静态IP\n  - static-ip\n---\n\n## Definition\n\n静态IP用于需要固定出口地址的网络场景。\n\n## Sources\n\n- [[sources/network]]\n")
	cfg := testConfig(root, t.TempDir())
	rt := newRuntime(cfg)
	env := &runtime.ExecEnv{
		WikiRoot:     root,
		WorkspaceDir: cfg.Workspace.BaseDir,
		Mode:         "admin",
		QMDIndex:     cfg.MountedWiki.QMDIndex,
	}
	result, err := rt.Execute(context.Background(), env, runtime.ToolCall{
		Name: "wiki.search_pages",
		Args: map[string]any{"query": "静态IP适用什么场景？"},
	})
	if err != nil || !result.Success {
		t.Fatalf("wiki.search_pages failed: %+v %v", result, err)
	}
	rawMatches, ok := result.Data["matches"].([]map[string]any)
	if !ok || len(rawMatches) == 0 {
		t.Fatalf("expected matches, got %+v", result.Data["matches"])
	}
	firstPath, _ := rawMatches[0]["path"].(string)
	if firstPath != "wiki/sources/network.md" && firstPath != "wiki/concepts/static-ip.md" {
		t.Fatalf("expected source or concept page to rank first, got %s", firstPath)
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
	mustWrite(t, filepath.Join(root, "scripts", "lint.py"), "#!/usr/bin/env python3\nfrom pathlib import Path\np=Path('wiki/outputs/lint/2026-04-22-health-check-report.md')\np.parent.mkdir(parents=True, exist_ok=True)\np.write_text('ok', encoding='utf-8')\nprint('Wrote lint report to wiki/outputs/lint/2026-04-22-health-check-report.md')\n")
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
