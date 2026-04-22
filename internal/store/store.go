package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type AdminUser struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AdminSession struct {
	Token     string
	UserID    string
	ExpiresAt time.Time
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
CREATE TABLE IF NOT EXISTS admin_users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  is_active INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_sessions (
  token TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS repair_proposals (
  id TEXT PRIMARY KEY,
  execution_id TEXT NOT NULL,
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

func (s *Store) EnsureDefaultAdmin(ctx context.Context, username string, password string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM admin_users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `
INSERT INTO admin_users (id, username, password_hash, is_active, created_at, updated_at)
VALUES (?, ?, ?, 1, ?, ?)
`, "admin_default", username, string(hashed), now, now)
	return err
}

func (s *Store) AuthenticateAdmin(ctx context.Context, username string, password string) (*AdminUser, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, username, password_hash, is_active, created_at, updated_at
FROM admin_users WHERE username = ?
`, username)
	var user AdminUser
	var passwordHash string
	var isActive int
	var createdAt string
	var updatedAt string
	if err := row.Scan(&user.ID, &user.Username, &passwordHash, &isActive, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if isActive == 0 {
		return nil, fmt.Errorf("admin account is disabled")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid username or password")
	}
	user.IsActive = true
	user.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	user.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &user, nil
}

func (s *Store) CreateSession(ctx context.Context, session AdminSession) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO admin_sessions (token, user_id, expires_at)
VALUES (?, ?, ?)
`, session.Token, session.UserID, session.ExpiresAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetSessionUser(ctx context.Context, token string) (*AdminUser, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT u.id, u.username, u.is_active, u.created_at, u.updated_at, s.expires_at
FROM admin_sessions s
JOIN admin_users u ON u.id = s.user_id
WHERE s.token = ?
`, token)
	var user AdminUser
	var isActive int
	var createdAt string
	var updatedAt string
	var expiresAt string
	if err := row.Scan(&user.ID, &user.Username, &isActive, &createdAt, &updatedAt, &expiresAt); err != nil {
		return nil, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return nil, err
	}
	if time.Now().After(expiry) {
		_ = s.DeleteSession(ctx, token)
		return nil, sql.ErrNoRows
	}
	user.IsActive = isActive == 1
	user.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	user.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &user, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM admin_sessions WHERE token = ?`, token)
	return err
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

func nullableJSON(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	return string(data)
}
