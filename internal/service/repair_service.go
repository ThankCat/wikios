package service

import (
	"context"
	"fmt"
	"time"

	"wikios/internal/task"
)

type ApplyLowRiskRequest struct {
	Path string `json:"path"`
	Ops  []any  `json:"ops"`
}

type ApplyProposalRequest struct {
	ProposalID string `json:"proposal_id"`
}

type RepairService struct {
	baseService
}

func NewRepairService(deps Deps) *RepairService {
	return &RepairService{baseService: newBaseService(deps)}
}

func (s *RepairService) ApplyLowRisk(ctx context.Context, taskModel *task.Task, traceID string, req ApplyLowRiskRequest) (map[string]any, error) {
	env := s.env("admin", traceID, taskModel.ID, taskModel.ID)
	if _, err := s.executeTool(ctx, taskModel, env, "repair.apply_low_risk", map[string]any{
		"path": req.Path,
		"ops":  req.Ops,
	}, "apply low risk"); err != nil {
		return nil, err
	}
	return map[string]any{"applied_fixes": []string{req.Path}}, nil
}

func (s *RepairService) CreateProposal(ctx context.Context, taskModel *task.Task, traceID string, title string, summary string, targetFiles []string, plannedOps map[string]any) (map[string]any, error) {
	env := s.env("admin", traceID, taskModel.ID, taskModel.ID)
	result, err := s.executeTool(ctx, taskModel, env, "repair.create_high_risk_proposal", map[string]any{
		"title":             title,
		"summary":           summary,
		"target_files":      targetFiles,
		"planned_patch_ops": plannedOps,
	}, "create proposal")
	if err != nil {
		return nil, err
	}
	proposal := &task.Proposal{
		ID:              fmt.Sprintf("%v", result.Data["proposal_id"]),
		TaskID:          taskModel.ID,
		Title:           title,
		RiskLevel:       "high",
		TargetFiles:     targetFiles,
		Summary:         summary,
		PlannedPatchOps: plannedOps,
		CreatedAt:       time.Now(),
	}
	if err := s.deps.TaskStore.SaveProposal(ctx, proposal); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (s *RepairService) ApplyProposal(ctx context.Context, taskModel *task.Task, traceID string, req ApplyProposalRequest) (map[string]any, error) {
	proposal, err := s.deps.TaskStore.GetProposal(ctx, req.ProposalID)
	if err != nil {
		return nil, err
	}
	ops, _ := proposal.PlannedPatchOps["ops"].([]any)
	return s.ApplyLowRisk(ctx, taskModel, traceID, ApplyLowRiskRequest{
		Path: fmt.Sprintf("%v", proposal.PlannedPatchOps["path"]),
		Ops:  ops,
	})
}
