package config_test

import (
	"os"
	"path/filepath"
	"strings"
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
	if cfg.Auth.SessionCookieSameSite == "" {
		t.Fatalf("expected auth cookie same_site default")
	}
}

func TestLoadRejectsDefaultAdminPasswordInReleaseMode(t *testing.T) {
	wikiRoot := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	raw := `server:
  mode: release
mounted_wiki:
  root: ` + wikiRoot + `
storage:
  sqlite_path: service.db
auth:
  default_admin_username: admin
  default_admin_password: admin123
`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := config.Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "secure non-default") {
		t.Fatalf("expected release default password error, got %v", err)
	}
}
