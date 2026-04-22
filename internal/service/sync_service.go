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
	commit, err := s.executeTool(ctx, execution, env, "git.commit", map[string]any{
		"message": req.Message,
	}, "git commit")
	if err != nil {
		return nil, err
	}
	push, err := s.executeTool(ctx, execution, env, "git.push", nil, "git push")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status": status.Data["stdout"],
		"commit": commit.Data,
		"push":   push.Data,
	}, nil
}
