package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"wikios/internal/config"
	"wikios/internal/runtime"
	"wikios/internal/wikiadapter"
)

type Dependencies struct {
	Config   *config.Config
	Resolver *wikiadapter.PathResolver
}

func RegisterAll(registry *runtime.Registry, deps Dependencies) {
	tools := []runtime.Tool{
		NewFSReadFileTool(deps),
		NewFSListDirTool(deps),
		NewFSFileStatTool(deps),
		NewFSGlobTool(deps),
		NewWikiReadPageTool(deps),
		NewWikiSearchPagesTool(deps),
		NewWikiFindBySlugTool(deps),
		NewWikiFindByAliasTool(deps),
		NewHashSHA256Tool(deps),
		NewWorkspaceCreateJobDirTool(deps),
		NewWorkspaceWriteTempFileTool(deps),
		NewWorkspaceReadTempFileTool(deps),
		NewWorkspaceCommitTempToWikiTool(deps),
		NewWorkspaceDiscardTool(deps),
		NewWikiCreateFromTemplateTool(deps),
		NewWikiPatchPageTool(deps),
		NewWikiAppendLogTool(deps),
		NewWikiWriteOutputTool(deps),
		NewWikiUpdateIndexEntryTool(deps),
		NewWikiUpdateQuestionsTool(deps),
		NewExecQMDTool(deps),
		NewExecPythonTool(deps),
		NewExecShellTool(deps),
		NewLintRunTool(deps),
		NewRepairApplyLowRiskTool(deps),
		NewRepairCreateHighRiskProposalTool(deps),
		NewGitStatusTool(deps),
		NewGitCommitTool(deps),
		NewGitPushTool(deps),
	}
	for _, tool := range tools {
		registry.Register(tool)
	}
}

type baseTool struct {
	name string
	risk runtime.RiskLevel
	deps Dependencies
}

func (t *baseTool) Name() string                 { return t.name }
func (t *baseTool) RiskLevel() runtime.RiskLevel { return t.risk }

func requireString(args map[string]any, key string) (string, error) {
	raw, ok := args[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	value, ok := raw.(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return value, nil
}

func optionalString(args map[string]any, key string) string {
	if raw, ok := args[key]; ok {
		if value, ok := raw.(string); ok {
			return value
		}
	}
	return ""
}

func optionalBool(args map[string]any, key string) bool {
	if raw, ok := args[key]; ok {
		if value, ok := raw.(bool); ok {
			return value
		}
	}
	return false
}

func optionalStringSlice(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch typed := raw.(type) {
	case []string:
		return typed, nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string list", key)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be a string list", key)
	}
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func writeFile(path string, content string) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func runCommand(ctx context.Context, cwd string, name string, args []string, env []string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd
	if env != nil {
		cmd.Env = env
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0, nil
	}
	if ctx.Err() != nil {
		return stdout.String(), stderr.String(), -1, ctx.Err()
	}
	exitCode := -1
	if ee, ok := err.(*exec.ExitError); ok {
		exitCode = ee.ExitCode()
		return stdout.String(), stderr.String(), exitCode, nil
	}
	return stdout.String(), stderr.String(), exitCode, err
}

func nowDate() string {
	return time.Now().Format("2006-01-02")
}

func computeSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
