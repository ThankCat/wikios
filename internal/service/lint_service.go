package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
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

func (s *LintService) Run(ctx context.Context, execution *Execution, traceID string, req LintRequest) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	lintResult, err := s.executeTool(ctx, execution, env, "lint.run", nil, "run lint")
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"summary":     summarizeLintResult(lintResult.Data),
		"report_file": lintResult.Data["report_path"],
	}
	statusResult, statusErr := s.executeTool(ctx, execution, env, "exec.qmd", map[string]any{
		"subcommand": "status",
	}, "qmd status")
	if statusResult.Data != nil {
		if stdout := strings.TrimSpace(toolString(statusResult.Data, "stdout")); stdout != "" {
			result["qmd_status"] = stdout
		}
		if stderr := strings.TrimSpace(toolString(statusResult.Data, "stderr")); stderr != "" {
			result["qmd_stderr"] = stderr
		}
		result["qmd_exit_code"] = statusResult.Data["exit_code"]
	}
	if statusErr != nil {
		result["qmd_error"] = statusErr.Error()
		result["summary"] = fmt.Sprintf("%s；QMD 状态检查失败", result["summary"])
	}
	return result, nil
}

var lintSummaryPattern = regexp.MustCompile(`Checked\s+(\d+)\s+markdown files;\s+found\s+(\d+)\s+issues`)

func summarizeLintResult(data map[string]any) string {
	stdout := toolString(data, "stdout")
	if match := lintSummaryPattern.FindStringSubmatch(stdout); len(match) == 3 {
		return fmt.Sprintf("健康检查完成：检查了 %s 个 Markdown 文件，发现 %s 个问题", match[1], match[2])
	}
	reportPath := strings.TrimSpace(toolString(data, "report_path"))
	if reportPath != "" {
		return fmt.Sprintf("健康检查完成，报告已写入 %s", reportPath)
	}
	return "健康检查完成"
}

func toolString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}
