package config_test

import (
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
}
