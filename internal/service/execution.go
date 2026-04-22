package service

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ExecutionStatus string

const (
	ExecutionSuccess ExecutionStatus = "SUCCESS"
	ExecutionFailed  ExecutionStatus = "FAILED"
	ExecutionRunning ExecutionStatus = "RUNNING"
)

type Step struct {
	Name       string         `json:"name"`
	Tool       string         `json:"tool,omitempty"`
	Status     string         `json:"status"`
	Input      map[string]any `json:"input,omitempty"`
	Output     map[string]any `json:"output,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
}

type Execution struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Status    ExecutionStatus `json:"status"`
	Steps     []Step          `json:"steps"`
	Error     string          `json:"error,omitempty"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   time.Time       `json:"ended_at,omitempty"`
}

func NewExecution(kind string) *Execution {
	now := time.Now()
	return &Execution{
		ID:        fmt.Sprintf("exec_%s", uuid.NewString()),
		Kind:      kind,
		Status:    ExecutionRunning,
		Steps:     []Step{},
		StartedAt: now,
	}
}
