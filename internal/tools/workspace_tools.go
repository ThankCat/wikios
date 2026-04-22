package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wikios/internal/runtime"
)

type workspaceCreateJobDirTool struct{ baseTool }
type workspaceWriteTempFileTool struct{ baseTool }
type workspaceReadTempFileTool struct{ baseTool }
type workspaceCommitTempToWikiTool struct{ baseTool }
type workspaceDiscardTool struct{ baseTool }

func NewWorkspaceCreateJobDirTool(deps Dependencies) runtime.Tool {
	return &workspaceCreateJobDirTool{baseTool{name: "workspace.create_job_dir", risk: runtime.RiskLow, deps: deps}}
}
func NewWorkspaceWriteTempFileTool(deps Dependencies) runtime.Tool {
	return &workspaceWriteTempFileTool{baseTool{name: "workspace.write_temp_file", risk: runtime.RiskLow, deps: deps}}
}
func NewWorkspaceReadTempFileTool(deps Dependencies) runtime.Tool {
	return &workspaceReadTempFileTool{baseTool{name: "workspace.read_temp_file", risk: runtime.RiskLow, deps: deps}}
}
func NewWorkspaceCommitTempToWikiTool(deps Dependencies) runtime.Tool {
	return &workspaceCommitTempToWikiTool{baseTool{name: "workspace.commit_temp_to_wiki", risk: runtime.RiskMedium, deps: deps}}
}
func NewWorkspaceDiscardTool(deps Dependencies) runtime.Tool {
	return &workspaceDiscardTool{baseTool{name: "workspace.discard", risk: runtime.RiskLow, deps: deps}}
}

func (t *workspaceCreateJobDirTool) Validate(args map[string]any) error {
	_, err := requireString(args, "job_id")
	return err
}
func (t *workspaceCreateJobDirTool) Execute(_ context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	jobID, _ := requireString(args, "job_id")
	dir := filepath.Join(env.WorkspaceDir, "jobs", jobID)
	if err := ensureDir(dir); err != nil {
		return failure(t.risk, "CREATE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": dir}), nil
}

func (t *workspaceWriteTempFileTool) Validate(args map[string]any) error {
	if _, err := requireString(args, "job_id"); err != nil {
		return err
	}
	if _, err := requireString(args, "path"); err != nil {
		return err
	}
	_, err := requireString(args, "content")
	return err
}
func (t *workspaceWriteTempFileTool) Execute(_ context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	jobID, _ := requireString(args, "job_id")
	rel, _ := requireString(args, "path")
	content, _ := requireString(args, "content")
	target, err := resolveWorkspacePath(env.WorkspaceDir, jobID, rel)
	if err != nil {
		return failure(t.risk, "INVALID_PATH", err), nil
	}
	if err := writeFile(target, content); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": target}), nil
}

func (t *workspaceReadTempFileTool) Validate(args map[string]any) error {
	if _, err := requireString(args, "job_id"); err != nil {
		return err
	}
	_, err := requireString(args, "path")
	return err
}
func (t *workspaceReadTempFileTool) Execute(_ context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	jobID, _ := requireString(args, "job_id")
	rel, _ := requireString(args, "path")
	target, err := resolveWorkspacePath(env.WorkspaceDir, jobID, rel)
	if err != nil {
		return failure(t.risk, "INVALID_PATH", err), nil
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"content": string(content), "path": target}), nil
}

func (t *workspaceCommitTempToWikiTool) Validate(args map[string]any) error {
	if _, err := requireString(args, "job_id"); err != nil {
		return err
	}
	if _, err := requireString(args, "temp_path"); err != nil {
		return err
	}
	_, err := requireString(args, "target_path")
	return err
}
func (t *workspaceCommitTempToWikiTool) Execute(_ context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	jobID, _ := requireString(args, "job_id")
	tempPath, _ := requireString(args, "temp_path")
	targetPath, _ := requireString(args, "target_path")
	source, err := resolveWorkspacePath(env.WorkspaceDir, jobID, tempPath)
	if err != nil {
		return failure(t.risk, "INVALID_PATH", err), nil
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	targetAbs, rel, err := t.deps.Resolver.EnsureWritableWikiPath(targetPath)
	if err != nil {
		return failure(t.risk, "WRITE_DENIED", err), nil
	}
	if err := writeFile(targetAbs, string(content)); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel}), nil
}

func (t *workspaceDiscardTool) Validate(args map[string]any) error {
	_, err := requireString(args, "job_id")
	return err
}
func (t *workspaceDiscardTool) Execute(_ context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	jobID, _ := requireString(args, "job_id")
	dir := filepath.Join(env.WorkspaceDir, "jobs", jobID)
	if err := os.RemoveAll(dir); err != nil {
		return failure(t.risk, "DISCARD_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": dir}), nil
}

func resolveWorkspacePath(base string, jobID string, rel string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", fmt.Errorf("invalid workspace path")
	}
	root := filepath.Join(base, "jobs", jobID)
	target := filepath.Join(root, filepath.FromSlash(clean))
	relPath, err := filepath.Rel(root, target)
	if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace path escapes job dir")
	}
	return target, nil
}
