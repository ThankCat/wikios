package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath = "configs/config.local.yaml"
)

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	MountedWiki MountedWikiConfig `yaml:"mounted_wiki"`
	LLM         LLMConfig         `yaml:"llm"`
	Auth        AuthConfig        `yaml:"auth"`
	Retrieval   RetrievalConfig   `yaml:"retrieval"`
	Workspace   WorkspaceConfig   `yaml:"workspace"`
	Sandbox     SandboxConfig     `yaml:"sandbox"`
	TaskStore   TaskStoreConfig   `yaml:"task_store"`
	Sync        SyncConfig        `yaml:"sync"`
	Web         WebConfig         `yaml:"web"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"`
}

type MountedWikiConfig struct {
	Root     string `yaml:"root"`
	Name     string `yaml:"name"`
	QMDIndex string `yaml:"qmd_index"`
}

type LLMConfig struct {
	Provider    string `yaml:"provider"`
	ModelPublic string `yaml:"model_public"`
	ModelAdmin  string `yaml:"model_admin"`
	APIKey      string `yaml:"api_key"`
	BaseURL     string `yaml:"base_url"`
	TimeoutSec  int    `yaml:"timeout_sec"`
}

type AuthConfig struct {
	AdminBearerToken string `yaml:"admin_bearer_token"`
}

type RetrievalConfig struct {
	TopK int `yaml:"top_k"`
}

type WorkspaceConfig struct {
	BaseDir           string `yaml:"base_dir"`
	MaxFileSizeMB     int    `yaml:"max_file_size_mb"`
	DefaultTimeoutSec int    `yaml:"default_timeout_sec"`
}

type SandboxConfig struct {
	PythonAllowNetwork bool `yaml:"python_allow_network"`
	PythonTimeoutSec   int  `yaml:"python_timeout_sec"`
	QMDTimeoutSec      int  `yaml:"qmd_timeout_sec"`
}

type TaskStoreConfig struct {
	SQLitePath string `yaml:"sqlite_path"`
}

type SyncConfig struct {
	Provider string `yaml:"provider"`
	Enabled  bool   `yaml:"enabled"`
	Remote   string `yaml:"remote"`
	Branch   string `yaml:"branch"`
}

type WebConfig struct {
	Enabled *bool  `yaml:"enabled"`
	DistDir string `yaml:"dist_dir"`
}

func Load(path string) (*Config, error) {
	if path == "" {
		path = defaultConfigPath
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(raw))
	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.normalizeAndValidate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) normalizeAndValidate() error {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.Mode == "" {
		c.Server.Mode = "debug"
	}
	if c.Retrieval.TopK <= 0 {
		c.Retrieval.TopK = 5
	}
	if c.Workspace.BaseDir == "" {
		c.Workspace.BaseDir = ".workspace"
	}
	if c.Workspace.DefaultTimeoutSec <= 0 {
		c.Workspace.DefaultTimeoutSec = 20
	}
	if c.TaskStore.SQLitePath == "" {
		c.TaskStore.SQLitePath = filepath.Join(c.Workspace.BaseDir, "service.db")
	}
	if c.Sandbox.PythonTimeoutSec <= 0 {
		c.Sandbox.PythonTimeoutSec = 20
	}
	if c.Sandbox.QMDTimeoutSec <= 0 {
		c.Sandbox.QMDTimeoutSec = 30
	}
	if c.LLM.TimeoutSec <= 0 {
		c.LLM.TimeoutSec = 90
	}
	c.Workspace.BaseDir = filepath.Clean(c.Workspace.BaseDir)
	c.TaskStore.SQLitePath = filepath.Clean(c.TaskStore.SQLitePath)
	c.MountedWiki.Root = filepath.Clean(c.MountedWiki.Root)
	if c.MountedWiki.Root == "." || c.MountedWiki.Root == "" {
		return errors.New("mounted_wiki.root is required")
	}
	info, err := os.Stat(c.MountedWiki.Root)
	if err != nil {
		return fmt.Errorf("stat mounted_wiki.root: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("mounted_wiki.root is not a directory: %s", c.MountedWiki.Root)
	}
	if c.MountedWiki.QMDIndex == "" {
		return errors.New("mounted_wiki.qmd_index is required")
	}
	if c.LLM.BaseURL == "" {
		return errors.New("llm.base_url is required")
	}
	if c.LLM.ModelPublic == "" || c.LLM.ModelAdmin == "" {
		return errors.New("llm models are required")
	}
	if strings.TrimSpace(c.Sync.Remote) == "" {
		c.Sync.Remote = "origin"
	}
	if strings.TrimSpace(c.Sync.Branch) == "" {
		c.Sync.Branch = "main"
	}
	if c.Web.DistDir == "" {
		c.Web.DistDir = "web/dist"
	}
	if c.Web.Enabled == nil {
		enabled := true
		c.Web.Enabled = &enabled
	}
	c.Web.DistDir = filepath.Clean(c.Web.DistDir)
	return nil
}
