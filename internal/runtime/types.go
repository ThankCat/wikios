package runtime

import "context"

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type ToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type ToolResult struct {
	Success   bool           `json:"success"`
	RiskLevel RiskLevel      `json:"risk_level"`
	Data      map[string]any `json:"data,omitempty"`
	Error     *ToolError     `json:"error,omitempty"`
}

type ToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Tool interface {
	Name() string
	RiskLevel() RiskLevel
	Validate(args map[string]any) error
	Execute(ctx context.Context, env *ExecEnv, args map[string]any) (ToolResult, error)
}

type ExecEnv struct {
	WikiRoot     string
	WorkspaceDir string
	JobID        string
	Mode         string
	TraceID      string
	TaskID       string
	QMDIndex     string
}
