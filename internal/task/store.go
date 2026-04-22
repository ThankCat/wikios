package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  status TEXT NOT NULL,
  steps_json TEXT NOT NULL,
  result_json TEXT,
  error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS proposals (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  title TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  target_files_json TEXT NOT NULL,
  summary TEXT NOT NULL,
  planned_patch_ops_json TEXT,
  created_at TEXT NOT NULL
);`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) SaveTask(ctx context.Context, task *Task) error {
	stepsJSON, err := json.Marshal(task.Steps)
	if err != nil {
		return err
	}
	var resultJSON []byte
	if task.Result != nil {
		resultJSON, err = json.Marshal(task.Result)
		if err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO tasks (id, type, status, steps_json, result_json, error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  type=excluded.type,
  status=excluded.status,
  steps_json=excluded.steps_json,
  result_json=excluded.result_json,
  error=excluded.error,
  updated_at=excluded.updated_at
`, task.ID, task.Type, task.Status, string(stepsJSON), bytesOrNil(resultJSON), task.Error, task.CreatedAt.Format(time.RFC3339Nano), task.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, type, status, steps_json, result_json, error, created_at, updated_at FROM tasks WHERE id = ?`, id)
	var task Task
	var stepsJSON string
	var resultJSON sql.NullString
	var createdAt string
	var updatedAt string
	if err := row.Scan(&task.ID, &task.Type, &task.Status, &stepsJSON, &resultJSON, &task.Error, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(stepsJSON), &task.Steps); err != nil {
		return nil, err
	}
	if resultJSON.Valid && resultJSON.String != "" {
		if err := json.Unmarshal([]byte(resultJSON.String), &task.Result); err != nil {
			return nil, err
		}
	}
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	task.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &task, nil
}

func (s *Store) SaveProposal(ctx context.Context, proposal *Proposal) error {
	targetJSON, err := json.Marshal(proposal.TargetFiles)
	if err != nil {
		return err
	}
	var opsJSON []byte
	if proposal.PlannedPatchOps != nil {
		opsJSON, err = json.Marshal(proposal.PlannedPatchOps)
		if err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO proposals (id, task_id, title, risk_level, target_files_json, summary, planned_patch_ops_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  task_id=excluded.task_id,
  title=excluded.title,
  risk_level=excluded.risk_level,
  target_files_json=excluded.target_files_json,
  summary=excluded.summary,
  planned_patch_ops_json=excluded.planned_patch_ops_json
`, proposal.ID, proposal.TaskID, proposal.Title, proposal.RiskLevel, string(targetJSON), proposal.Summary, bytesOrNil(opsJSON), proposal.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetProposal(ctx context.Context, id string) (*Proposal, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, task_id, title, risk_level, target_files_json, summary, planned_patch_ops_json, created_at FROM proposals WHERE id = ?`, id)
	var proposal Proposal
	var targetsJSON string
	var opsJSON sql.NullString
	var createdAt string
	if err := row.Scan(&proposal.ID, &proposal.TaskID, &proposal.Title, &proposal.RiskLevel, &targetsJSON, &proposal.Summary, &opsJSON, &createdAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(targetsJSON), &proposal.TargetFiles); err != nil {
		return nil, err
	}
	if opsJSON.Valid && opsJSON.String != "" {
		if err := json.Unmarshal([]byte(opsJSON.String), &proposal.PlannedPatchOps); err != nil {
			return nil, err
		}
	}
	proposal.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &proposal, nil
}

func bytesOrNil(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	return string(data)
}
