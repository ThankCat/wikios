package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wikios/internal/runtime"
)

type hashSHA256Tool struct{ baseTool }
type execQMDTool struct{ baseTool }
type execPythonTool struct{ baseTool }
type lintRunTool struct{ baseTool }

func NewHashSHA256Tool(deps Dependencies) runtime.Tool {
	return &hashSHA256Tool{baseTool{name: "hash.sha256", risk: runtime.RiskLow, deps: deps}}
}
func NewExecQMDTool(deps Dependencies) runtime.Tool {
	return &execQMDTool{baseTool{name: "exec.qmd", risk: runtime.RiskMedium, deps: deps}}
}
func NewExecPythonTool(deps Dependencies) runtime.Tool {
	return &execPythonTool{baseTool{name: "exec.python", risk: runtime.RiskMedium, deps: deps}}
}
func NewLintRunTool(deps Dependencies) runtime.Tool {
	return &lintRunTool{baseTool{name: "lint.run", risk: runtime.RiskLow, deps: deps}}
}

func (t *hashSHA256Tool) Validate(args map[string]any) error {
	_, err := requireString(args, "path")
	return err
}
func (t *hashSHA256Tool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	path, _ := requireString(args, "path")
	abs, rel, err := t.deps.Resolver.ResolveReadPath(path)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel, "sha256": computeSHA256(content)}), nil
}

func (t *execQMDTool) Validate(args map[string]any) error {
	sub, err := requireString(args, "subcommand")
	if err != nil {
		return err
	}
	switch sub {
	case "query", "status", "update", "collection_add", "multi_get":
		return nil
	default:
		return fmt.Errorf("unsupported qmd subcommand %q", sub)
	}
}
func (t *execQMDTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	subcommand, _ := requireString(args, "subcommand")
	timeout := time.Duration(t.deps.Config.Sandbox.QMDTimeoutSec) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmdArgs := []string{"--index", env.QMDIndex}
	switch subcommand {
	case "query":
		question, err := requireString(args, "question")
		if err != nil {
			return failure(t.risk, "INVALID_ARGS", err), nil
		}
		cmdArgs = append(cmdArgs, "query", question, "--json")
	case "status":
		cmdArgs = append(cmdArgs, "status")
	case "update":
		cmdArgs = append(cmdArgs, "update")
	case "collection_add":
		path := optionalString(args, "path")
		name := optionalString(args, "name")
		if path == "" {
			path = "wiki/"
		}
		if name == "" {
			name = "wiki"
		}
		cmdArgs = append(cmdArgs, "collection", "add", path, "--name", name)
	case "multi_get":
		pattern, err := requireString(args, "pattern")
		if err != nil {
			return failure(t.risk, "INVALID_ARGS", err), nil
		}
		limit := optionalString(args, "limit")
		if limit == "" {
			limit = "20"
		}
		cmdArgs = append(cmdArgs, "multi-get", pattern, "-l", limit)
	}
	stdout, stderr, exitCode, err := runCommand(runCtx, env.WikiRoot, "qmd", cmdArgs, nil)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	return success(t.risk, map[string]any{
		"subcommand": subcommand,
		"stdout":     stdout,
		"stderr":     stderr,
		"exit_code":  exitCode,
	}), nil
}

func (t *execPythonTool) Validate(args map[string]any) error {
	_, err := requireString(args, "script")
	return err
}
func (t *execPythonTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	script, _ := requireString(args, "script")
	jobID := optionalString(args, "job_id")
	if jobID == "" {
		jobID = env.JobID
	}
	if jobID == "" {
		return failure(t.risk, "INVALID_ARGS", fmt.Errorf("job_id is required")), nil
	}
	target, err := resolveWorkspacePath(env.WorkspaceDir, jobID, "python/"+time.Now().Format("20060102150405")+".py")
	if err != nil {
		return failure(t.risk, "INVALID_PATH", err), nil
	}
	if err := writeFile(target, script); err != nil {
		return failure(t.risk, "WRITE_FAILED", err), nil
	}
	timeout := time.Duration(t.deps.Config.Sandbox.PythonTimeoutSec) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	envs := []string{
		"PATH=" + os.Getenv("PATH"),
		"PYTHONNOUSERSITE=1",
		"HTTP_PROXY=",
		"HTTPS_PROXY=",
		"http_proxy=",
		"https_proxy=",
		"ALL_PROXY=",
		"all_proxy=",
	}
	cwd := filepath.Dir(target)
	stdout, stderr, exitCode, err := runCommand(runCtx, cwd, "python3", []string{filepath.Base(target)}, envs)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	return success(t.risk, map[string]any{
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": exitCode,
		"path":      target,
	}), nil
}

func (t *lintRunTool) Validate(args map[string]any) error { return nil }
func (t *lintRunTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	_ = args
	timeout := time.Duration(t.deps.Config.Workspace.DefaultTimeoutSec) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, exitCode, err := runCommand(runCtx, env.WikiRoot, "python3", []string{"scripts/lint.py"}, nil)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	reportPath := ""
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "Wrote lint report to ") {
			reportPath = strings.TrimSpace(strings.TrimPrefix(line, "Wrote lint report to "))
			break
		}
	}
	return success(t.risk, map[string]any{
		"stdout":      stdout,
		"stderr":      stderr,
		"exit_code":   exitCode,
		"report_path": reportPath,
	}), nil
}
