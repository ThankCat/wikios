package service

import (
	"context"
	"path/filepath"
	"testing"

	"wikios/internal/config"
	"wikios/internal/store"
)

func TestLoadRuntimeSettingsUsesConfigDefaults(t *testing.T) {
	cfg := testRuntimeSettingsConfig(t)
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dataStore.Close()

	snapshot, err := LoadRuntimeSettings(context.Background(), dataStore, cfg)
	if err != nil {
		t.Fatalf("LoadRuntimeSettings: %v", err)
	}
	if snapshot.Settings.PublicQuery.CandidateTopK != 8 ||
		snapshot.Settings.PublicQuery.MaxEvidenceChars != 3600 ||
		snapshot.Settings.Support.Phone != "12345" ||
		snapshot.Settings.Sync.Remote != "upstream" {
		t.Fatalf("unexpected config defaults: %+v", snapshot.Settings)
	}
	if snapshot.Environment.WikiRoot != cfg.MountedWiki.Root || snapshot.Environment.SQLitePath != cfg.Storage.SQLitePath {
		t.Fatalf("unexpected environment: %+v", snapshot.Environment)
	}
}

func TestSaveRuntimeSettingsPersistsAcrossStoreRestart(t *testing.T) {
	cfg := testRuntimeSettingsConfig(t)
	path := filepath.Join(t.TempDir(), "settings.db")
	dataStore, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	settings := DefaultRuntimeSettings(cfg)
	settings.PublicQuery.DirectMin = 0.82
	settings.PublicQuery.ReviewMin = 0.31
	settings.PublicQuery.RouterModelID = "router-fast"
	settings.PublicQuery.SpecialistModelID = "specialist-main"
	routerThinking := true
	specialistThinking := false
	settings.PublicQuery.RouterEnableThinking = &routerThinking
	settings.PublicQuery.SpecialistEnableThinking = &specialistThinking
	settings.AnswerLog.Enabled = false
	settings.AnswerLog.Redact = false
	settings.AnswerLog.RetentionDays = 30
	settings.Knowledge.MaxTextFileKB = 900
	settings.Sync.Remote = "origin"
	settings.Sync.Branch = "release"

	snapshot, fields, err := SaveRuntimeSettings(context.Background(), dataStore, cfg, settings)
	if err != nil {
		t.Fatalf("SaveRuntimeSettings: %v", err)
	}
	if len(fields) > 0 {
		t.Fatalf("unexpected validation fields: %+v", fields)
	}
	if snapshot.UpdatedAt == "" || snapshot.Settings.Sync.Branch != "release" {
		t.Fatalf("unexpected saved snapshot: %+v", snapshot)
	}
	if err := dataStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	reloaded, err := LoadRuntimeSettings(context.Background(), reopened, cfg)
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	if reloaded.Settings.PublicQuery.DirectMin != 0.82 ||
		reloaded.Settings.PublicQuery.RouterModelID != "router-fast" ||
		reloaded.Settings.PublicQuery.SpecialistModelID != "specialist-main" ||
		reloaded.Settings.PublicQuery.RouterEnableThinking == nil ||
		!*reloaded.Settings.PublicQuery.RouterEnableThinking ||
		reloaded.Settings.PublicQuery.SpecialistEnableThinking == nil ||
		*reloaded.Settings.PublicQuery.SpecialistEnableThinking ||
		reloaded.Settings.AnswerLog.Enabled ||
		reloaded.Settings.Knowledge.MaxTextFileKB != 900 ||
		reloaded.Settings.Sync.Branch != "release" {
		t.Fatalf("expected persisted runtime settings, got %+v", reloaded.Settings)
	}
}

func TestValidateRuntimeSettingsRejectsInvalidValues(t *testing.T) {
	settings := DefaultRuntimeSettings(nil)
	settings.PublicQuery.ReviewMin = 0.8
	settings.PublicQuery.DirectMin = 0.7
	settings.PublicQuery.CandidateTopK = 30
	settings.AnswerLog.RetentionDays = 0
	settings.Sync.Remote = ""
	fields := ValidateRuntimeSettings(settings)
	for _, key := range []string{
		"public_query.review_min",
		"public_query.candidate_top_k",
		"answer_log.retention_days",
		"sync.remote",
	} {
		if fields[key] == "" {
			t.Fatalf("expected validation error for %s, got %+v", key, fields)
		}
	}
}

func testRuntimeSettingsConfig(t *testing.T) *config.Config {
	t.Helper()
	enabled := true
	redact := true
	return &config.Config{
		Server: config.ServerConfig{Port: 8081, Mode: "debug"},
		MountedWiki: config.MountedWikiConfig{
			Root:     t.TempDir(),
			Name:     "Test Wiki",
			QMDIndex: "test-index",
		},
		Workspace:     config.WorkspaceConfig{BaseDir: t.TempDir(), DefaultTimeoutSec: 5},
		Storage:       config.StorageConfig{SQLitePath: filepath.Join(t.TempDir(), "service.db")},
		Web:           config.WebConfig{Enabled: &enabled, DistDir: "web/dist"},
		PublicIntents: config.PublicIntentsConfig{Path: "configs/public_intents.yaml"},
		PublicQuery: config.PublicQueryConfig{
			Confidence:       config.PublicQueryConfidenceConfig{DirectMin: 0.75, ReviewMin: 0.3},
			CandidateTopK:    8,
			MaxEvidenceChars: 3600,
			AnswerLog:        config.PublicAnswerLogConfig{Enabled: &enabled, Redact: &redact, RetentionDays: 21},
		},
		Support: config.SupportConfig{Phone: "12345", WeCom: "Test WeCom"},
		Upload:  config.UploadConfig{MaxTextFileKB: 700},
		Sync:    config.SyncConfig{Remote: "upstream", Branch: "develop"},
	}
}
