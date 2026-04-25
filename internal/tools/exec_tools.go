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
type execShellTool struct{ baseTool }
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
func NewExecShellTool(deps Dependencies) runtime.Tool {
	return &execShellTool{baseTool{name: "exec.shell", risk: runtime.RiskHigh, deps: deps}}
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
	stdout, stderr, exitCode, err := runCommand(runCtx, env.WikiRoot, "qmd", cmdArgs, qmdEnv())
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	data := map[string]any{
		"subcommand": subcommand,
		"stdout":     stdout,
		"stderr":     stderr,
		"exit_code":  exitCode,
	}
	if exitCode != 0 {
		return runtime.ToolResult{
			Success:   false,
			RiskLevel: t.risk,
			Data:      data,
			Error: &runtime.ToolError{
				Code:    "EXEC_FAILED",
				Message: fmt.Sprintf("qmd %s exited with code %d", subcommand, exitCode),
			},
		}, nil
	}
	return success(t.risk, data), nil
}

func qmdEnv() []string {
	return commandEnvWithPreferredPath(qmdPreferredPath())
}

func qmdPreferredPath() string {
	path := os.Getenv("PATH")
	preferred := []string{
		os.Getenv("WIKIOS_QMD_NODE_BIN"),
		os.Getenv("QMD_NODE_BIN"),
		"/opt/homebrew/opt/node@24/bin",
	}
	segments := make([]string, 0, len(preferred)+1)
	for _, item := range preferred {
		if strings.TrimSpace(item) == "" {
			continue
		}
		if stat, err := os.Stat(item); err == nil && stat.IsDir() {
			segments = append(segments, item)
		}
	}
	if strings.TrimSpace(path) != "" {
		segments = append(segments, path)
	}
	return strings.Join(segments, ":")
}

func commandEnvWithPreferredPath(path string) []string {
	if strings.TrimSpace(path) == "" {
		path = os.Getenv("PATH")
	}
	return []string{
		"PATH=" + path,
		"HOME=" + os.Getenv("HOME"),
		"WIKIOS_QMD_NODE_BIN=" + os.Getenv("WIKIOS_QMD_NODE_BIN"),
		"QMD_NODE_BIN=" + os.Getenv("QMD_NODE_BIN"),
	}
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

func (t *execShellTool) Validate(args map[string]any) error {
	_, err := requireString(args, "command")
	return err
}
func (t *execShellTool) Execute(ctx context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	command, _ := requireString(args, "command")
	timeoutSec := t.deps.Config.Workspace.DefaultTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cwd := env.WikiRoot
	if strings.TrimSpace(cwd) == "" {
		cwd = t.deps.Resolver.WikiRoot()
	}
	preferredPath := qmdPreferredPath()
	envs := append(commandEnvWithPreferredPath(preferredPath), "WIKI_ROOT="+cwd)
	shellCommand := fmt.Sprintf("export PATH=%q; export WIKI_ROOT=%q; %s", preferredPath, cwd, command)
	stdout, stderr, exitCode, err := runCommand(runCtx, cwd, "zsh", []string{"-c", shellCommand}, envs)
	if err != nil {
		return failure(t.risk, "EXEC_FAILED", err), nil
	}
	return success(t.risk, map[string]any{
		"command":   command,
		"cwd":       cwd,
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": exitCode,
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
