package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Runner func(ctx context.Context, task *Task) (map[string]any, error)

type Manager struct {
	store *Store
	mu    sync.Mutex
}

func NewManager(store *Store) *Manager {
	return &Manager{store: store}
}

func (m *Manager) Submit(ctx context.Context, taskType string, runner Runner) (*Task, error) {
	now := time.Now()
	task := &Task{
		ID:        "task_" + uuid.NewString(),
		Type:      taskType,
		Status:    StatusPending,
		Steps:     []Step{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := m.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}
	go m.run(task, runner)
	return task, nil
}

func (m *Manager) run(task *Task, runner Runner) {
	ctx := context.Background()
	task.Status = StatusRunning
	task.UpdatedAt = time.Now()
	_ = m.store.SaveTask(ctx, task)

	result, err := runner(ctx, task)
	task.UpdatedAt = time.Now()
	if err != nil {
		task.Status = StatusFailed
		task.Error = err.Error()
	} else {
		task.Status = StatusSuccess
		task.Result = result
	}
	_ = m.store.SaveTask(ctx, task)
}

func (m *Manager) Get(ctx context.Context, id string) (*Task, error) {
	task, err := m.store.GetTask(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", id, err)
	}
	return task, nil
}
