package service

import (
	"context"
	"fmt"
	"time"
	"wikios/internal/store"
)

type ApplyLowRiskRequest struct {
	Path string `json:"path"`
	Ops  []any  `json:"ops"`
}

type ApplyProposalRequest struct {
	ProposalID string `json:"proposal_id"`
}

type AutoRepairRequest struct {
	Topic string `json:"topic"`
	Apply bool   `json:"apply"`
}

type RepairService struct {
	baseService
}

func NewRepairService(deps Deps) *RepairService {
	return &RepairService{baseService: newBaseService(deps)}
}

func (s *RepairService) ApplyLowRisk(ctx context.Context, execution *Execution, traceID string, req ApplyLowRiskRequest) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	if _, err := s.executeTool(ctx, execution, env, "repair.apply_low_risk", map[string]any{
		"path": req.Path,
		"ops":  req.Ops,
	}, "apply low risk"); err != nil {
		return nil, err
	}
	return map[string]any{"applied_fixes": []string{req.Path}}, nil
}

func (s *RepairService) CreateProposal(ctx context.Context, execution *Execution, traceID string, title string, summary string, targetFiles []string, plannedOps map[string]any) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	result, err := s.executeTool(ctx, execution, env, "repair.create_high_risk_proposal", map[string]any{
		"title":             title,
		"summary":           summary,
		"target_files":      targetFiles,
		"planned_patch_ops": plannedOps,
	}, "create proposal")
	if err != nil {
		return nil, err
	}
	proposal := &store.Proposal{
		ID:              fmt.Sprintf("%v", result.Data["proposal_id"]),
		ExecutionID:     execution.ID,
		Title:           title,
		RiskLevel:       "high",
		TargetFiles:     targetFiles,
		Summary:         summary,
		PlannedPatchOps: plannedOps,
		CreatedAt:       time.Now(),
	}
	if err := s.deps.Store.SaveProposal(ctx, proposal); err != nil {
		return nil, err
	}
	return result.Data, nil
}

func (s *RepairService) ApplyProposal(ctx context.Context, execution *Execution, traceID string, req ApplyProposalRequest) (map[string]any, error) {
	proposal, err := s.deps.Store.GetProposal(ctx, req.ProposalID)
	if err != nil {
		return nil, err
	}
	ops, _ := proposal.PlannedPatchOps["ops"].([]any)
	return s.ApplyLowRisk(ctx, execution, traceID, ApplyLowRiskRequest{
		Path: fmt.Sprintf("%v", proposal.PlannedPatchOps["path"]),
		Ops:  ops,
	})
}

func (s *RepairService) AutoDetect(ctx context.Context, execution *Execution, traceID string, req AutoRepairRequest) (map[string]any, error) {
	env := s.env("admin", traceID, execution.ID, execution.ID)
	detected, err := s.detectBackedCorrections(ctx, execution, env, req.Topic)
	if err != nil {
		return nil, err
	}
	fixes, warnings, err := s.buildCorrectionFixes(ctx, execution, env, detected.Corrections)
	if err != nil {
		return nil, err
	}
	applied := []string{}
	if req.Apply && len(fixes) > 0 {
		applied, err = s.applyCorrectionFixes(ctx, execution, env, fixes)
		if err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"summary":              firstNonEmpty(detected.Summary, "repair analysis completed"),
		"detected_corrections": fixes,
		"applied_fixes":        applied,
		"warnings":             dedupeStrings(append(detected.Warnings, warnings...)),
	}, nil
}
