package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Proposal struct {
	ID              string         `json:"id"`
	ExecutionID     string         `json:"execution_id"`
	Title           string         `json:"title"`
	RiskLevel       string         `json:"risk_level"`
	TargetFiles     []string       `json:"target_files"`
	Summary         string         `json:"summary"`
	PlannedPatchOps map[string]any `json:"planned_patch_ops,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type LLMModel struct {
	ID              string    `json:"id"`
	DisplayName     string    `json:"display_name"`
	Provider        string    `json:"provider"`
	BaseURL         string    `json:"base_url"`
	ModelName       string    `json:"model_name"`
	APIKey          string    `json:"-"`
	IsActive        bool      `json:"is_active"`
	TimeoutSec      int       `json:"timeout_sec"`
	AdminTimeoutSec int       `json:"admin_timeout_sec"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AdminSetting struct {
	Key       string
	ValueJSON string
	UpdatedAt time.Time
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS repair_proposals (
  id TEXT PRIMARY KEY,
  execution_id TEXT NOT NULL,
  title TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  target_files_json TEXT NOT NULL,
  summary TEXT NOT NULL,
  planned_patch_ops_json TEXT,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS llm_models (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  provider TEXT NOT NULL,
  base_url TEXT NOT NULL,
  model_name TEXT NOT NULL,
  api_key TEXT NOT NULL,
  is_active INTEGER NOT NULL DEFAULT 0,
  timeout_sec INTEGER NOT NULL DEFAULT 90,
  admin_timeout_sec INTEGER NOT NULL DEFAULT 300,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_settings (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
DELETE FROM llm_models WHERE id IN ('llm_default_admin', 'llm_default_public');
UPDATE llm_models
SET is_active = 0
WHERE is_active = 1
  AND id NOT IN (
    SELECT id
    FROM llm_models
    WHERE is_active = 1
    ORDER BY updated_at DESC, created_at DESC
    LIMIT 1
  );
CREATE UNIQUE INDEX IF NOT EXISTS idx_llm_models_one_active
ON llm_models(is_active)
WHERE is_active = 1;`
	_, err := s.db.Exec(schema)
	return err
}

func (s *Store) GetAdminSetting(ctx context.Context, key string) (*AdminSetting, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT key, value_json, updated_at
FROM admin_settings
WHERE key = ?
`, strings.TrimSpace(key))
	var setting AdminSetting
	var updatedAt string
	if err := row.Scan(&setting.Key, &setting.ValueJSON, &updatedAt); err != nil {
		return nil, err
	}
	setting.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &setting, nil
}

func (s *Store) SetAdminSetting(ctx context.Context, key string, value any) (*AdminSetting, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("setting key is required")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO admin_settings (key, value_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
  value_json=excluded.value_json,
  updated_at=excluded.updated_at
`, key, string(encoded), now)
	if err != nil {
		return nil, err
	}
	return s.GetAdminSetting(ctx, key)
}

func (s *Store) SaveProposal(ctx context.Context, proposal *Proposal) error {
	targetsJSON, err := json.Marshal(proposal.TargetFiles)
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
INSERT INTO repair_proposals (id, execution_id, title, risk_level, target_files_json, summary, planned_patch_ops_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  execution_id=excluded.execution_id,
  title=excluded.title,
  risk_level=excluded.risk_level,
  target_files_json=excluded.target_files_json,
  summary=excluded.summary,
  planned_patch_ops_json=excluded.planned_patch_ops_json
`, proposal.ID, proposal.ExecutionID, proposal.Title, proposal.RiskLevel, string(targetsJSON), proposal.Summary, nullableJSON(opsJSON), proposal.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetProposal(ctx context.Context, id string) (*Proposal, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, execution_id, title, risk_level, target_files_json, summary, planned_patch_ops_json, created_at
FROM repair_proposals WHERE id = ?
`, id)
	var proposal Proposal
	var targetsJSON string
	var opsJSON sql.NullString
	var createdAt string
	if err := row.Scan(&proposal.ID, &proposal.ExecutionID, &proposal.Title, &proposal.RiskLevel, &targetsJSON, &proposal.Summary, &opsJSON, &createdAt); err != nil {
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

func (s *Store) ListLLMModels(ctx context.Context) ([]LLMModel, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, display_name, provider, base_url, model_name, api_key, is_active, timeout_sec, admin_timeout_sec, created_at, updated_at
FROM llm_models
ORDER BY is_active DESC, updated_at DESC, created_at DESC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	models := []LLMModel{}
	for rows.Next() {
		model, err := scanLLMModel(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, *model)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return models, nil
}

func (s *Store) GetLLMModel(ctx context.Context, id string) (*LLMModel, error) {
	return scanLLMModel(s.db.QueryRowContext(ctx, `
SELECT id, display_name, provider, base_url, model_name, api_key, is_active, timeout_sec, admin_timeout_sec, created_at, updated_at
FROM llm_models
WHERE id = ?
`, id))
}

func (s *Store) GetActiveLLMModel(ctx context.Context) (*LLMModel, error) {
	return scanLLMModel(s.db.QueryRowContext(ctx, `
SELECT id, display_name, provider, base_url, model_name, api_key, is_active, timeout_sec, admin_timeout_sec, created_at, updated_at
FROM llm_models
WHERE is_active = 1
ORDER BY updated_at DESC
LIMIT 1
`))
}

func (s *Store) CreateLLMModel(ctx context.Context, model *LLMModel) error {
	normalizeLLMModel(model)
	shouldActivate := model.IsActive
	_, err := s.db.ExecContext(ctx, `
INSERT INTO llm_models (id, display_name, provider, base_url, model_name, api_key, is_active, timeout_sec, admin_timeout_sec, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, model.ID, model.DisplayName, model.Provider, model.BaseURL, model.ModelName, model.APIKey, 0, model.TimeoutSec, model.AdminTimeoutSec, model.CreatedAt.Format(time.RFC3339Nano), model.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if shouldActivate {
		return s.ActivateLLMModel(ctx, model.ID)
	}
	return nil
}

func (s *Store) UpdateLLMModel(ctx context.Context, model *LLMModel) error {
	normalizeLLMModel(model)
	shouldActivate := model.IsActive
	_, err := s.db.ExecContext(ctx, `
UPDATE llm_models
SET display_name = ?, provider = ?, base_url = ?, model_name = ?, api_key = ?, is_active = ?, timeout_sec = ?, admin_timeout_sec = ?, updated_at = ?
WHERE id = ?
`, model.DisplayName, model.Provider, model.BaseURL, model.ModelName, model.APIKey, 0, model.TimeoutSec, model.AdminTimeoutSec, model.UpdatedAt.Format(time.RFC3339Nano), model.ID)
	if err == nil && shouldActivate {
		return s.ActivateLLMModel(ctx, model.ID)
	}
	return err
}

func (s *Store) DeleteLLMModel(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM llm_models WHERE id = ?`, id)
	return err
}

func (s *Store) ActivateLLMModel(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `UPDATE llm_models SET is_active = 0, updated_at = ? WHERE is_active = 1`, now); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE llm_models SET is_active = 1, updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func nullableJSON(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	return string(data)
}

type sqlScanner interface {
	Scan(dest ...any) error
}

func scanLLMModel(scanner sqlScanner) (*LLMModel, error) {
	var model LLMModel
	var isActive int
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&model.ID,
		&model.DisplayName,
		&model.Provider,
		&model.BaseURL,
		&model.ModelName,
		&model.APIKey,
		&isActive,
		&model.TimeoutSec,
		&model.AdminTimeoutSec,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, err
	}
	model.IsActive = isActive == 1
	model.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	model.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &model, nil
}

func normalizeLLMModel(model *LLMModel) {
	now := time.Now()
	model.DisplayName = strings.TrimSpace(model.DisplayName)
	model.Provider = firstNonEmpty(strings.TrimSpace(model.Provider), "openai-compatible")
	model.BaseURL = normalizeLLMBaseURL(model.BaseURL)
	model.ModelName = strings.TrimSpace(model.ModelName)
	model.APIKey = strings.TrimSpace(model.APIKey)
	if model.DisplayName == "" {
		model.DisplayName = defaultLLMDisplayName(model.Provider, model.ModelName)
	}
	if model.TimeoutSec <= 0 {
		model.TimeoutSec = 90
	}
	if model.AdminTimeoutSec <= 0 {
		model.AdminTimeoutSec = 300
	}
	if model.CreatedAt.IsZero() {
		model.CreatedAt = now
	}
	model.UpdatedAt = now
}

func normalizeLLMBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func defaultLLMDisplayName(provider string, modelName string) string {
	if strings.TrimSpace(modelName) == "" {
		return firstNonEmpty(provider, "OpenAI Compatible")
	}
	if strings.TrimSpace(provider) == "" || provider == "openai-compatible" {
		return modelName
	}
	return provider + " / " + modelName
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
