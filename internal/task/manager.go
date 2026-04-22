package task

import (
	"context"
	"fmt"
	"log"
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
	task, err := m.Create(ctx, taskType)
	if err != nil {
		return nil, err
	}
	go m.run(task, runner)
	return task, nil
}

func (m *Manager) Create(ctx context.Context, taskType string) (*Task, error) {
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
	return task, nil
}

func (m *Manager) MarkRunning(ctx context.Context, task *Task) error {
	task.Status = StatusRunning
	task.UpdatedAt = time.Now()
	return m.store.SaveTask(ctx, task)
}

func (m *Manager) Complete(ctx context.Context, task *Task, result map[string]any, runErr error) error {
	task.UpdatedAt = time.Now()
	if runErr != nil {
		task.Status = StatusFailed
		task.Error = runErr.Error()
		log.Printf("task failed id=%s type=%s error=%s", task.ID, task.Type, task.Error)
	} else {
		task.Status = StatusSuccess
		task.Result = result
		task.Error = ""
		log.Printf("task completed id=%s type=%s", task.ID, task.Type)
	}
	return m.store.SaveTask(ctx, task)
}

func (m *Manager) Save(ctx context.Context, task *Task) error {
	task.UpdatedAt = time.Now()
	return m.store.SaveTask(ctx, task)
}

func (m *Manager) run(task *Task, runner Runner) {
	ctx := context.Background()
	_ = m.MarkRunning(ctx, task)

	result, err := runner(ctx, task)
	_ = m.Complete(ctx, task, result, err)
}

func (m *Manager) Get(ctx context.Context, id string) (*Task, error) {
	task, err := m.store.GetTask(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get task %s: %w", id, err)
	}
	return task, nil
}
