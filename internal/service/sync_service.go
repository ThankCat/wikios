package service

import (
	"context"

	"wikios/internal/task"
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

func (s *SyncService) Run(ctx context.Context, taskModel *task.Task, traceID string, req SyncRequest) (map[string]any, error) {
	env := s.env("admin", traceID, taskModel.ID, taskModel.ID)
	status, err := s.executeTool(ctx, taskModel, env, "git.status", nil, "git status")
	if err != nil {
		return nil, err
	}
	commit, err := s.executeTool(ctx, taskModel, env, "git.commit", map[string]any{
		"message": req.Message,
	}, "git commit")
	if err != nil {
		return nil, err
	}
	push, err := s.executeTool(ctx, taskModel, env, "git.push", nil, "git push")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status": status.Data["stdout"],
		"commit": commit.Data,
		"push":   push.Data,
	}, nil
}
