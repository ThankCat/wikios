package runtime

import (
	"context"
	"sync"
	"time"
)

type AuditEvent struct {
	Timestamp time.Time
	TraceID   string
	TaskID    string
	Tool      string
	Args      map[string]any
	Result    ToolResult
	Err       string
}

type AuditLogger struct {
	mu     sync.RWMutex
	events []AuditEvent
}

func NewAuditLogger() *AuditLogger {
	return &AuditLogger{}
}

func (a *AuditLogger) Log(_ context.Context, env *ExecEnv, call ToolCall, result ToolResult, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := AuditEvent{
		Timestamp: time.Now(),
		TraceID:   env.TraceID,
		TaskID:    env.TaskID,
		Tool:      call.Name,
		Args:      cloneMap(call.Args),
		Result:    result,
	}
	if err != nil {
		entry.Err = err.Error()
	}
	a.events = append(a.events, entry)
}

func (a *AuditLogger) List() []AuditEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AuditEvent, len(a.events))
	copy(out, a.events)
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
