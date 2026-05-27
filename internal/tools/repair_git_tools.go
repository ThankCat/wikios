package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	wikigit "wikios/internal/git"
	"wikios/internal/runtime"
)

type gitStatusTool struct{ baseTool }
type gitCommitTool struct{ baseTool }
type gitPushTool struct{ baseTool }

func NewGitStatusTool(deps Dependencies) runtime.Tool {
	return &gitStatusTool{baseTool{name: "git.status", risk: runtime.RiskMedium, deps: deps}}
}
func NewGitCommitTool(deps Dependencies) runtime.Tool {
	return &gitCommitTool{baseTool{name: "git.commit", risk: runtime.RiskHigh, deps: deps}}
}
func NewGitPushTool(deps Dependencies) runtime.Tool {
	return &gitPushTool{baseTool{name: "git.push", risk: runtime.RiskHigh, deps: deps}}
}

func (t *gitStatusTool) Validate(args map[string]any) error { return nil }
func (t *gitStatusTool) Execute(ctx context.Context, env *runtime.ExecEnv, _ map[string]any) (runtime.ToolResult, error) {
	result, err := t.gitRunner(env, "", "").Run(ctx, "status", "--short", "--branch")
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	return success(t.risk, gitResultMap(result)), nil
}

func (t *gitCommitTool) Validate(args map[string]any) error {
	if _, err := requireString(args, "message"); err != nil {
		return err
	}
	paths, err := optionalStringSlice(args, "paths")
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("paths is required")
	}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || path == ".." || strings.HasPrefix(path, "../") || strings.Contains("/"+filepath.ToSlash(path)+"/", "/.git/") {
			return fmt.Errorf("invalid git path %q", path)
		}
	}
	return nil
}
func (t *gitCommitTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	message, _ := requireString(args, "message")
	addPaths, err := optionalStringSlice(args, "paths")
	if err != nil {
		return failure(t.risk, "INVALID_ARGS", err), nil
	}
	if len(addPaths) == 0 {
		return failure(t.risk, "INVALID_ARGS", fmt.Errorf("paths is required")), nil
	}
	runner := t.gitRunner(env, "", "")
	addArgs := append([]string{"add", "--"}, addPaths...)
	addResult, err := runner.Run(ctx, addArgs...)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	if addResult.ExitCode != 0 {
		data := gitResultMap(addResult)
		data["committed"] = false
		return success(t.risk, data), nil
	}
	result, err := runner.Run(ctx, "commit", "-m", message)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	data := gitResultMap(result)
	if result.ExitCode != 0 && strings.Contains(result.Stdout+result.Stderr, "nothing to commit") {
		data["committed"] = false
		return success(t.risk, data), nil
	}
	data["committed"] = result.ExitCode == 0
	return success(t.risk, data), nil
}

func (t *gitPushTool) Validate(args map[string]any) error { return nil }
func (t *gitPushTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	remote := optionalString(args, "remote")
	if remote == "" {
		remote = t.deps.Config.Sync.Remote
	}
	branch := optionalString(args, "branch")
	if branch == "" {
		branch = t.deps.Config.Sync.Branch
	}
	result, err := t.gitRunner(env, remote, branch).Run(ctx, "push", remote, branch)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	return success(t.risk, gitResultMap(result)), nil
}

func (t *baseTool) gitRunner(env *runtime.ExecEnv, remote string, branch string) *wikigit.Runner {
	if remote == "" && t.deps.Config != nil {
		remote = t.deps.Config.Sync.Remote
	}
	if branch == "" && t.deps.Config != nil {
		branch = t.deps.Config.Sync.Branch
	}
	repoDir := ""
	if env != nil {
		repoDir = env.WikiRoot
	}
	return wikigit.NewRunner(wikigit.ConfigFromEnv(repoDir, remote, branch))
}

func gitResultMap(result wikigit.Result) map[string]any {
	return map[string]any{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	}
}
