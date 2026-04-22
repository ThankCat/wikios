package service

import (
	"context"
	"fmt"
	"strings"

	"wikios/internal/llm"
	"wikios/internal/report"
	"wikios/internal/task"
)

type ReflectRequest struct {
	Topic          string `json:"topic"`
	WriteReport    bool   `json:"write_report"`
	AutoFixLowRisk bool   `json:"auto_fix_low_risk"`
}

type ReflectService struct {
	baseService
}

func NewReflectService(deps Deps) *ReflectService {
	return &ReflectService{baseService: newBaseService(deps)}
}

func (s *ReflectService) Run(ctx context.Context, taskModel *task.Task, traceID string, req ReflectRequest) (map[string]any, error) {
	env := s.env("admin", traceID, taskModel.ID, taskModel.ID)
	globResult, err := s.executeTool(ctx, taskModel, env, "fs.glob", map[string]any{"pattern": "wiki/**/*.md"}, "scan wiki")
	if err != nil {
		return nil, err
	}
	matchList, _ := globResult.Data["matches"].([]string)
	if len(matchList) > 12 {
		matchList = matchList[:12]
	}
	blocks := make([]string, 0, len(matchList))
	for _, path := range matchList {
		readResult, err := s.executeTool(ctx, taskModel, env, "wiki.read_page", map[string]any{"path": path}, "read "+path)
		if err != nil {
			continue
		}
		content, _ := readResult.Data["content"].(string)
		if len(content) > 600 {
			content = content[:600] + "..."
		}
		blocks = append(blocks, "## "+path+"\n\n"+content)
	}
	prompt, err := s.loadPrompt("admin_reflect_system.md")
	if err != nil {
		return nil, err
	}
	answer, err := s.deps.LLM.Chat(ctx, s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: fmt.Sprintf("主题：%s\n\n样本页面：\n%s", req.Topic, strings.Join(blocks, "\n\n"))},
	})
	if err != nil {
		return nil, err
	}
	outputFiles := []string{}
	if req.WriteReport {
		path := "wiki/outputs/gap-report-" + nowDate() + ".md"
		doc := buildOutputDocument("Gap Report", answer, len(matchList))
		if _, err := s.executeTool(ctx, taskModel, env, "wiki.write_output", map[string]any{"path": path, "content": doc}, "write gap report"); err != nil {
			return nil, err
		}
		outputFiles = append(outputFiles, path)
	}
	rep := reportResult(taskModel.ID, "reflect", "reflect completed", outputFiles, taskModel.Steps)
	return map[string]any{
		"patterns":       []string{answer},
		"gaps":           []string{},
		"contradictions": []string{},
		"output_files":   outputFiles,
		"report":         report.Markdown(rep),
	}, nil
}
