package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"wikios/internal/llm"
)

type SyncRequest struct {
	Message string `json:"message"`
}

type SyncService struct {
	baseService
}

type SyncChangedFile struct {
	Path     string `json:"path"`
	Status   string `json:"status"`
	Deleted  bool   `json:"deleted"`
	Preview  string `json:"preview"`
	OldPath  string `json:"old_path,omitempty"`
	Index    string `json:"index,omitempty"`
	Worktree string `json:"worktree,omitempty"`
}

type SyncCommitMessageRequest struct {
	Files      []SyncChangedFile `json:"files"`
	DiffStat   string            `json:"diff_stat"`
	NameStatus string            `json:"name_status"`
}

type syncCommitMessageOutput struct {
	Message string `json:"message"`
}

func NewSyncService(deps Deps) *SyncService {
	return &SyncService{baseService: newBaseService(deps)}
}

func (s *SyncService) Run(ctx context.Context, execution *Execution, traceID string, req SyncRequest) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	status, err := s.executeTool(ctx, execution, env, "git.status", nil, "git status")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":  status.Data["stdout"],
		"summary": "同步已改为 server API 流程：先查看变更、选择文件、提交，再确认推送。",
		"message": req.Message,
	}, nil
}

func (s *SyncService) GenerateCommitMessage(ctx context.Context, req SyncCommitMessageRequest) (string, string, error) {
	rule := syncCommitMessageRule()
	fallback := fallbackSyncCommitMessage(req)
	if s == nil || s.deps.LLM == nil {
		return fallback, rule, nil
	}
	prompt, err := s.loadPrompt("admin_sync_commit_message.md")
	if err != nil {
		return fallback, rule, nil
	}
	payload, _ := json.MarshalIndent(req, "", "  ")
	timeout := s.llmRequestTimeout(NewExecution("sync"))
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	text, err := s.deps.LLM.Chat(llm.WithRequestTimeout(ctx, timeout), s.deps.Config.LLM.ModelAdmin, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: string(payload)},
	})
	if err != nil {
		return fallback, rule, nil
	}
	parsed := syncCommitMessageOutput{}
	if err := llm.DecodeJSONObject(text, &parsed); err != nil {
		return fallback, rule, nil
	}
	message := sanitizeSyncCommitMessage(parsed.Message)
	if message == "" {
		message = fallback
	}
	return message, rule, nil
}

func syncCommitMessageRule() string {
	return "提交信息规则：用中文，一行，12-50 字；动词开头；说明本次 Wiki 资料变更；不写句号；不提 LLM、AI、自动生成；不要包含换行、引号或 Markdown。"
}

func fallbackSyncCommitMessage(req SyncCommitMessageRequest) string {
	count := len(req.Files)
	if count == 0 {
		return "更新 Wiki 内容"
	}
	kinds := map[string]int{}
	for _, file := range req.Files {
		switch {
		case strings.HasPrefix(file.Path, "wiki/faq/"):
			kinds["FAQ"]++
		case strings.HasPrefix(file.Path, "wiki/concepts/"):
			kinds["概念"]++
		case strings.HasPrefix(file.Path, "wiki/entities/"):
			kinds["实体"]++
		case strings.HasPrefix(file.Path, "wiki/outputs/"):
			kinds["报告"]++
		case strings.HasPrefix(file.Path, "raw/"):
			kinds["原始资料"]++
		default:
			kinds["Wiki"]++
		}
	}
	priority := []string{"FAQ", "概念", "实体", "原始资料", "报告", "Wiki"}
	parts := []string{}
	for _, key := range priority {
		if kinds[key] > 0 {
			parts = append(parts, key)
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("更新 Wiki 内容（%d 个文件）", count)
	}
	return fmt.Sprintf("更新 %s 内容（%d 个文件）", strings.Join(parts, "、"), count)
}

func sanitizeSyncCommitMessage(message string) string {
	message = strings.TrimSpace(message)
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	message = strings.Trim(message, "`\"' ")
	for strings.Contains(message, "  ") {
		message = strings.ReplaceAll(message, "  ", " ")
	}
	if message == "" {
		return ""
	}
	runes := []rune(message)
	if len(runes) > 50 {
		message = string(runes[:50])
	}
	return strings.TrimSpace(message)
}
