package service

import (
	"context"
)

type SyncRequest struct {
	Message string `json:"message"`
}

type SyncService struct {
	baseService
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
