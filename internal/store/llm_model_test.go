package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLLMModelCRUDAndActivation(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	defer dataStore.Close()

	first := &LLMModel{
		ID:              "first",
		DisplayName:     "First",
		Provider:        "test",
		BaseURL:         "http://first.example/v1",
		ModelName:       "first-model",
		APIKey:          "first-key",
		IsActive:        true,
		TimeoutSec:      12,
		AdminTimeoutSec: 34,
	}
	if err := dataStore.CreateLLMModel(ctx, first); err != nil {
		t.Fatalf("create first: %v", err)
	}
	second := &LLMModel{
		ID:              "second",
		DisplayName:     "Second",
		Provider:        "test",
		BaseURL:         "http://second.example/v1",
		ModelName:       "second-model",
		APIKey:          "second-key",
		IsActive:        true,
		TimeoutSec:      56,
		AdminTimeoutSec: 78,
	}
	if err := dataStore.CreateLLMModel(ctx, second); err != nil {
		t.Fatalf("create second: %v", err)
	}
	active, err := dataStore.GetActiveLLMModel(ctx)
	if err != nil {
		t.Fatalf("active model: %v", err)
	}
	if active.ID != "second" {
		t.Fatalf("expected second active, got %s", active.ID)
	}

	second.DisplayName = "Second Updated"
	second.IsActive = true
	if err := dataStore.UpdateLLMModel(ctx, second); err != nil {
		t.Fatalf("update second: %v", err)
	}
	updated, err := dataStore.GetLLMModel(ctx, "second")
	if err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if updated.DisplayName != "Second Updated" || updated.APIKey != "second-key" {
		t.Fatalf("unexpected updated model: %+v", updated)
	}

	if err := dataStore.DeleteLLMModel(ctx, "second"); err != nil {
		t.Fatalf("delete active: %v", err)
	}
	_, err = dataStore.GetActiveLLMModel(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no active model after deleting active, got %v", err)
	}
	if err := dataStore.ActivateLLMModel(ctx, "first"); err != nil {
		t.Fatalf("reactivate first: %v", err)
	}
	if err := dataStore.DeleteLLMModel(ctx, "first"); err != nil {
		t.Fatalf("delete first: %v", err)
	}
	_, err = dataStore.GetActiveLLMModel(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no active model, got %v", err)
	}
}

func TestOpenRemovesLegacyDefaultLLMModels(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "service.db")
	dataStore, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := dataStore.CreateLLMModel(ctx, &LLMModel{
		ID:              "llm_default_admin",
		DisplayName:     "Legacy Default",
		Provider:        "legacy-provider",
		BaseURL:         "http://legacy.example/v1",
		ModelName:       "legacy-model",
		APIKey:          "legacy-key",
		IsActive:        true,
		TimeoutSec:      90,
		AdminTimeoutSec: 300,
	}); err != nil {
		t.Fatalf("create legacy model: %v", err)
	}
	if err := dataStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	dataStore, err = Open(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer dataStore.Close()
	_, err = dataStore.GetLLMModel(ctx, "llm_default_admin")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected legacy default model removed, got %v", err)
	}
	_, err = dataStore.GetActiveLLMModel(ctx)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no active model after legacy cleanup, got %v", err)
	}
}

func TestLLMModelsDatabaseAllowsOnlyOneActiveModel(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	defer dataStore.Close()
	if err := dataStore.CreateLLMModel(ctx, &LLMModel{
		ID:              "active",
		DisplayName:     "Active",
		Provider:        "test",
		BaseURL:         "http://active.example/v1",
		ModelName:       "active-model",
		APIKey:          "active-key",
		IsActive:        true,
		TimeoutSec:      90,
		AdminTimeoutSec: 300,
	}); err != nil {
		t.Fatalf("create active: %v", err)
	}
	now := time.Now().Format(time.RFC3339Nano)
	_, err := dataStore.db.ExecContext(ctx, `
INSERT INTO llm_models (id, display_name, provider, base_url, model_name, api_key, is_active, timeout_sec, admin_timeout_sec, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 1, 90, 300, ?, ?)
`, "second-active", "Second Active", "test", "http://second.example/v1", "second-model", "second-key", now, now)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unique") {
		t.Fatalf("expected database unique active constraint, got %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dataStore, err := Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return dataStore
}
