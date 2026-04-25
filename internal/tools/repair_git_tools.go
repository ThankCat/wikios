package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"wikios/internal/runtime"
)

type repairApplyLowRiskTool struct{ baseTool }
type repairCreateHighRiskProposalTool struct{ baseTool }
type gitStatusTool struct{ baseTool }
type gitCommitTool struct{ baseTool }
type gitPushTool struct{ baseTool }

func NewRepairApplyLowRiskTool(deps Dependencies) runtime.Tool {
	return &repairApplyLowRiskTool{baseTool{name: "repair.apply_low_risk", risk: runtime.RiskMedium, deps: deps}}
}
func NewRepairCreateHighRiskProposalTool(deps Dependencies) runtime.Tool {
	return &repairCreateHighRiskProposalTool{baseTool{name: "repair.create_high_risk_proposal", risk: runtime.RiskHigh, deps: deps}}
}
func NewGitStatusTool(deps Dependencies) runtime.Tool {
	return &gitStatusTool{baseTool{name: "git.status", risk: runtime.RiskMedium, deps: deps}}
}
func NewGitCommitTool(deps Dependencies) runtime.Tool {
	return &gitCommitTool{baseTool{name: "git.commit", risk: runtime.RiskHigh, deps: deps}}
}
func NewGitPushTool(deps Dependencies) runtime.Tool {
	return &gitPushTool{baseTool{name: "git.push", risk: runtime.RiskHigh, deps: deps}}
}

func (t *repairApplyLowRiskTool) Validate(args map[string]any) error {
	if _, err := requireString(args, "path"); err != nil {
		return err
	}
	raw := args["ops"]
	if raw == nil {
		return fmt.Errorf("ops is required")
	}
	return nil
}
func (t *repairApplyLowRiskTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	patchTool := NewWikiPatchPageTool(t.deps)
	return patchTool.Execute(ctx, env, args)
}

func (t *repairCreateHighRiskProposalTool) Validate(args map[string]any) error {
	_, err := requireString(args, "title")
	return err
}
func (t *repairCreateHighRiskProposalTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	title, _ := requireString(args, "title")
	summary := optionalString(args, "summary")
	targets, _ := optionalStringSlice(args, "target_files")
	return success(t.risk, map[string]any{
		"proposal_id":  newProposalID(),
		"title":        title,
		"risk_level":   string(runtime.RiskHigh),
		"summary":      summary,
		"target_files": targets,
		"planned_ops":  args["planned_patch_ops"],
	}), nil
}

func (t *gitStatusTool) Validate(args map[string]any) error { return nil }
func (t *gitStatusTool) Execute(ctx context.Context, env *runtime.ExecEnv, _ map[string]any) (runtime.ToolResult, error) {
	stdout, stderr, exitCode, err := runCommand(ctx, env.WikiRoot, "git", []string{"status", "--short", "--branch"}, nil)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"stdout": stdout, "stderr": stderr, "exit_code": exitCode}), nil
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
	addArgs := append([]string{"add"}, addPaths...)
	if _, _, _, err := runCommand(ctx, env.WikiRoot, "git", addArgs, nil); err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	stdout, stderr, exitCode, err := runCommand(ctx, env.WikiRoot, "git", []string{"commit", "-m", message}, nil)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	if exitCode != 0 && strings.Contains(stdout+stderr, "nothing to commit") {
		return success(t.risk, map[string]any{"stdout": stdout, "stderr": stderr, "exit_code": exitCode, "committed": false}), nil
	}
	return success(t.risk, map[string]any{"stdout": stdout, "stderr": stderr, "exit_code": exitCode, "committed": true}), nil
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
	stdout, stderr, exitCode, err := runCommand(ctx, env.WikiRoot, "git", []string{"push", remote, branch}, nil)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"stdout": stdout, "stderr": stderr, "exit_code": exitCode}), nil
}
