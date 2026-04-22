package task_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"wikios/internal/task"
)

func TestSQLiteStorePersistsTask(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "service.db")
	store, err := task.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Now().UTC()
	in := &task.Task{
		ID:        "task-1",
		Type:      "query",
		Status:    task.StatusSuccess,
		Steps:     []task.Step{{Name: "step", Status: "SUCCESS"}},
		Result:    map[string]any{"ok": true},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveTask(context.Background(), in); err != nil {
		t.Fatalf("save task: %v", err)
	}
	_ = store.Close()

	store, err = task.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	out, err := store.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if out.Type != "query" || out.Status != task.StatusSuccess {
		t.Fatalf("unexpected task: %+v", out)
	}
}
