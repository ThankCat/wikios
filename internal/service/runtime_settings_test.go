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
	if snapshot.Settings.CustomerChat.CandidateTopK != 8 ||
		snapshot.Settings.CustomerChat.MaxEvidenceChars != 3600 ||
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
	settings.CustomerChat.DirectMin = 0.82
	settings.CustomerChat.ReviewMin = 0.31
	settings.CustomerChat.RouterModelID = "router-fast"
	settings.CustomerChat.SpecialistModelID = "specialist-main"
	routerThinking := true
	specialistThinking := false
	settings.CustomerChat.RouterEnableThinking = &routerThinking
	settings.CustomerChat.SpecialistEnableThinking = &specialistThinking
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
	if reloaded.Settings.CustomerChat.DirectMin != 0.82 ||
		reloaded.Settings.CustomerChat.RouterModelID != "router-fast" ||
		reloaded.Settings.CustomerChat.SpecialistModelID != "specialist-main" ||
		reloaded.Settings.CustomerChat.RouterEnableThinking == nil ||
		!*reloaded.Settings.CustomerChat.RouterEnableThinking ||
		reloaded.Settings.CustomerChat.SpecialistEnableThinking == nil ||
		*reloaded.Settings.CustomerChat.SpecialistEnableThinking ||
		reloaded.Settings.AnswerLog.Enabled ||
		reloaded.Settings.Knowledge.MaxTextFileKB != 900 ||
		reloaded.Settings.Sync.Branch != "release" {
		t.Fatalf("expected persisted runtime settings, got %+v", reloaded.Settings)
	}
}

func TestLoadRuntimeSettingsMigratesLegacyCustomerQueryKey(t *testing.T) {
	cfg := testRuntimeSettingsConfig(t)
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dataStore.Close()

	legacyKey := "publ" + "ic_query"
	raw := map[string]any{
		legacyKey: map[string]any{
			"direct_min":         0.81,
			"review_min":         0.32,
			"candidate_top_k":    5,
			"max_evidence_chars": 1800,
			"router_model_id":    "router-legacy",
		},
	}
	if _, err := dataStore.SetAdminSetting(context.Background(), RuntimeSettingsKey, raw); err != nil {
		t.Fatalf("seed legacy settings: %v", err)
	}

	snapshot, err := LoadRuntimeSettings(context.Background(), dataStore, cfg)
	if err != nil {
		t.Fatalf("LoadRuntimeSettings: %v", err)
	}
	if snapshot.Settings.CustomerChat.DirectMin != 0.81 ||
		snapshot.Settings.CustomerChat.ReviewMin != 0.32 ||
		snapshot.Settings.CustomerChat.CandidateTopK != 5 ||
		snapshot.Settings.CustomerChat.MaxEvidenceChars != 1800 ||
		snapshot.Settings.CustomerChat.RouterModelID != "router-legacy" {
		t.Fatalf("expected legacy customer query settings to load, got %+v", snapshot.Settings.CustomerChat)
	}
}

func TestValidateRuntimeSettingsRejectsInvalidValues(t *testing.T) {
	settings := DefaultRuntimeSettings(nil)
	settings.CustomerChat.ReviewMin = 0.8
	settings.CustomerChat.DirectMin = 0.7
	settings.CustomerChat.CandidateTopK = 30
	settings.AnswerLog.RetentionDays = 0
	settings.Sync.Remote = ""
	fields := ValidateRuntimeSettings(settings)
	for _, key := range []string{
		"customer_query.review_min",
		"customer_query.candidate_top_k",
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
		Workspace:       config.WorkspaceConfig{BaseDir: t.TempDir(), DefaultTimeoutSec: 5},
		Storage:         config.StorageConfig{SQLitePath: filepath.Join(t.TempDir(), "service.db")},
		Web:             config.WebConfig{Enabled: &enabled, DistDir: "web/dist"},
		CustomerIntents: config.CustomerIntentsConfig{Path: "configs/customer_intents.yaml"},
		CustomerChat: config.CustomerQueryConfig{
			Confidence:       config.CustomerQueryConfidenceConfig{DirectMin: 0.75, ReviewMin: 0.3},
			CandidateTopK:    8,
			MaxEvidenceChars: 3600,
			AnswerLog:        config.CustomerChatLogConfig{Enabled: &enabled, Redact: &redact, RetentionDays: 21},
		},
		Support: config.SupportConfig{Phone: "12345", WeCom: "Test WeCom"},
		Upload:  config.UploadConfig{MaxTextFileKB: 700},
		Sync:    config.SyncConfig{Remote: "upstream", Branch: "develop"},
	}
}
