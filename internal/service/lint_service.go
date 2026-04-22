package service

import (
	"context"
	"wikios/internal/task"
)

type LintRequest struct {
	WriteReport    bool `json:"write_report"`
	AutoFixLowRisk bool `json:"auto_fix_low_risk"`
}

type LintService struct {
	baseService
}

func NewLintService(deps Deps) *LintService {
	return &LintService{baseService: newBaseService(deps)}
}

func (s *LintService) Run(ctx context.Context, taskModel *task.Task, traceID string, req LintRequest) (map[string]any, error) {
	env := s.env("admin", traceID, taskModel.ID, taskModel.ID)
	lintResult, err := s.executeTool(ctx, taskModel, env, "lint.run", nil, "run lint")
	if err != nil {
		return nil, err
	}
	statusResult, err := s.executeTool(ctx, taskModel, env, "exec.qmd", map[string]any{
		"subcommand": "status",
	}, "qmd status")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"summary":     "lint completed",
		"report_file": lintResult.Data["report_path"],
		"qmd_status":  statusResult.Data["stdout"],
	}, nil
}
