package task

import "time"

type Status string

const (
	StatusPending Status = "PENDING"
	StatusRunning Status = "RUNNING"
	StatusSuccess Status = "SUCCESS"
	StatusFailed  Status = "FAILED"
)

type Task struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Status    Status         `json:"status"`
	Steps     []Step         `json:"steps"`
	Result    map[string]any `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Step struct {
	Name       string         `json:"name"`
	Tool       string         `json:"tool,omitempty"`
	Status     string         `json:"status"`
	Input      map[string]any `json:"input,omitempty"`
	Output     map[string]any `json:"output,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
}

type Proposal struct {
	ID              string         `json:"id"`
	TaskID          string         `json:"task_id"`
	Title           string         `json:"title"`
	RiskLevel       string         `json:"risk_level"`
	TargetFiles     []string       `json:"target_files"`
	Summary         string         `json:"summary"`
	PlannedPatchOps map[string]any `json:"planned_patch_ops,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}
