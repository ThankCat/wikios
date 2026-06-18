package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"wikios/internal/config"
)

func TestLoadLocalConfig(t *testing.T) {
	cfg, err := config.Load("../../configs/config.local.yaml")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.MountedWiki.Root == "" {
		t.Fatalf("expected mounted wiki root")
	}
	if cfg.MountedWiki.QMDIndex == "" {
		t.Fatalf("expected qmd index")
	}
	if cfg.SafetyTerms.Path == "" {
		t.Fatalf("expected customer safety terms path")
	}
}

func TestLoadCustomerMaxConcurrentFromEnv(t *testing.T) {
	wikiRoot := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte("mounted_wiki:\n  root: " + wikiRoot + "\ncustomer_query:\n  max_concurrent: 0\n")
	if err := os.WriteFile(configPath, raw, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("WIKIOS_CUSTOMER_MAX_CONCURRENT", "2")

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.CustomerChat.MaxConcurrent != 2 {
		t.Fatalf("expected max_concurrent from env, got %d", cfg.CustomerChat.MaxConcurrent)
	}
}
