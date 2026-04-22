package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wikios/internal/runtime"
)

type fsReadFileTool struct{ baseTool }
type fsListDirTool struct{ baseTool }
type fsFileStatTool struct{ baseTool }
type fsGlobTool struct{ baseTool }

func NewFSReadFileTool(deps Dependencies) runtime.Tool {
	return &fsReadFileTool{baseTool{name: "fs.read_file", risk: runtime.RiskLow, deps: deps}}
}
func NewFSListDirTool(deps Dependencies) runtime.Tool {
	return &fsListDirTool{baseTool{name: "fs.list_dir", risk: runtime.RiskLow, deps: deps}}
}
func NewFSFileStatTool(deps Dependencies) runtime.Tool {
	return &fsFileStatTool{baseTool{name: "fs.file_stat", risk: runtime.RiskLow, deps: deps}}
}
func NewFSGlobTool(deps Dependencies) runtime.Tool {
	return &fsGlobTool{baseTool{name: "fs.glob", risk: runtime.RiskLow, deps: deps}}
}

func (t *fsReadFileTool) Validate(args map[string]any) error {
	_, err := requireString(args, "path")
	return err
}
func (t *fsReadFileTool) Execute(_ context.Context, env *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	path, _ := requireString(args, "path")
	abs, rel, err := t.deps.Resolver.ResolveReadPath(path)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return failure(t.risk, "READ_FAILED", err), nil
	}
	return success(t.risk, map[string]any{"path": rel, "content": string(content), "mode": env.Mode}), nil
}

func (t *fsListDirTool) Validate(args map[string]any) error {
	_, err := requireString(args, "path")
	return err
}
func (t *fsListDirTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	path, _ := requireString(args, "path")
	abs, rel, err := t.deps.Resolver.ResolveReadPath(path)
	if err != nil {
		return failure(t.risk, "LIST_FAILED", err), nil
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return failure(t.risk, "LIST_FAILED", err), nil
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		info, _ := entry.Info()
		items = append(items, map[string]any{
			"name":     entry.Name(),
			"is_dir":   entry.IsDir(),
			"size":     info.Size(),
			"mod_time": info.ModTime().Format(timeLayout),
		})
	}
	return success(t.risk, map[string]any{"path": rel, "entries": items}), nil
}

func (t *fsFileStatTool) Validate(args map[string]any) error {
	_, err := requireString(args, "path")
	return err
}
func (t *fsFileStatTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	path, _ := requireString(args, "path")
	abs, rel, err := t.deps.Resolver.ResolveReadPath(path)
	if err != nil {
		return failure(t.risk, "STAT_FAILED", err), nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return failure(t.risk, "STAT_FAILED", err), nil
	}
	return success(t.risk, map[string]any{
		"path":     rel,
		"size":     info.Size(),
		"is_dir":   info.IsDir(),
		"mod_time": info.ModTime().Format(timeLayout),
	}), nil
}

func (t *fsGlobTool) Validate(args map[string]any) error {
	_, err := requireString(args, "pattern")
	return err
}
func (t *fsGlobTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	pattern, _ := requireString(args, "pattern")
	clean := filepath.ToSlash(filepath.Clean(pattern))
	if strings.HasPrefix(clean, "../") {
		return failure(t.risk, "INVALID_PATTERN", fmt.Errorf("pattern escapes wiki root")), nil
	}
	matches, err := filepath.Glob(filepath.Join(t.deps.Resolver.WikiRoot(), filepath.FromSlash(clean)))
	if err != nil {
		return failure(t.risk, "INVALID_PATTERN", err), nil
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		rel, err := filepath.Rel(t.deps.Resolver.WikiRoot(), match)
		if err != nil {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return success(t.risk, map[string]any{"matches": out}), nil
}

func failure(risk runtime.RiskLevel, code string, err error) runtime.ToolResult {
	return runtime.ToolResult{
		Success:   false,
		RiskLevel: risk,
		Error: &runtime.ToolError{
			Code:    code,
			Message: err.Error(),
		},
	}
}

func success(risk runtime.RiskLevel, data map[string]any) runtime.ToolResult {
	return runtime.ToolResult{
		Success:   true,
		RiskLevel: risk,
		Data:      data,
	}
}

const timeLayout = "2006-01-02T15:04:05Z07:00"
