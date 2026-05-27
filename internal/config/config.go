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
	Server        ServerConfig        `yaml:"server"`
	MountedWiki   MountedWikiConfig   `yaml:"mounted_wiki"`
	LLM           LLMConfig           `yaml:"llm"`
	Retrieval     RetrievalConfig     `yaml:"retrieval"`
	Workspace     WorkspaceConfig     `yaml:"workspace"`
	Sandbox       SandboxConfig       `yaml:"sandbox"`
	Storage       StorageConfig       `yaml:"storage"`
	Sync          SyncConfig          `yaml:"sync"`
	Web           WebConfig           `yaml:"web"`
	Upload        UploadConfig        `yaml:"upload"`
	PublicIntents PublicIntentsConfig `yaml:"public_intents"`
	PublicQuery   PublicQueryConfig   `yaml:"public_query"`
	Support       SupportConfig       `yaml:"support"`
	Context       ContextConfig       `yaml:"context"`
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
	TimeoutSec      int `yaml:"timeout_sec"`
	AdminTimeoutSec int `yaml:"admin_timeout_sec"`
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

type StorageConfig struct {
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

type UploadConfig struct {
	MaxTextFileKB int `yaml:"max_text_file_kb"`
}

type PublicIntentsConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Path    string `yaml:"path"`
}

type PublicQueryConfig struct {
	Confidence         PublicQueryConfidenceConfig `yaml:"confidence"`
	CandidateTopK      int                         `yaml:"candidate_top_k"`
	MaxEvidenceChars   int                         `yaml:"max_evidence_chars"`
	ResponseTimeoutSec int                         `yaml:"response_timeout_sec"`
	AnswerLog          PublicAnswerLogConfig       `yaml:"answer_log"`
}

type PublicAnswerLogConfig struct {
	Enabled       *bool `yaml:"enabled"`
	Redact        *bool `yaml:"redact"`
	RetentionDays int   `yaml:"retention_days"`
}

type PublicQueryConfidenceConfig struct {
	DirectMin float64 `yaml:"direct_min"`
	ReviewMin float64 `yaml:"review_min"`
}

type SupportConfig struct {
	Phone string `yaml:"phone"`
	WeCom string `yaml:"wecom"`
}

type ContextConfig struct {
	MaxTokens     int    `yaml:"max_tokens"`
	ReserveTokens int    `yaml:"reserve_tokens"`
	Counter       string `yaml:"counter"`
	Tokenizer     string `yaml:"tokenizer"`
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
	if c.Storage.SQLitePath == "" {
		c.Storage.SQLitePath = filepath.Join(c.Workspace.BaseDir, "service.db")
	}
	if c.Sandbox.PythonTimeoutSec <= 0 {
		c.Sandbox.PythonTimeoutSec = 20
	}
	if c.Sandbox.QMDTimeoutSec <= 0 {
		c.Sandbox.QMDTimeoutSec = 30
	}
	if c.LLM.TimeoutSec <= 0 {
		c.LLM.TimeoutSec = envInt("WIKIOS_LLM_TIMEOUT_SEC", 300)
	}
	if c.LLM.AdminTimeoutSec <= 0 {
		c.LLM.AdminTimeoutSec = 300
	}
	c.Workspace.BaseDir = filepath.Clean(c.Workspace.BaseDir)
	c.Storage.SQLitePath = filepath.Clean(c.Storage.SQLitePath)
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
	if strings.TrimSpace(c.MountedWiki.QMDIndex) == "" {
		c.MountedWiki.QMDIndex = firstEnv("WIKIOS_QMD_INDEX", "knowledge-base")
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
	if c.Upload.MaxTextFileKB <= 0 {
		c.Upload.MaxTextFileKB = 500
	}
	if c.PublicIntents.Enabled == nil {
		enabled := true
		c.PublicIntents.Enabled = &enabled
	}
	if strings.TrimSpace(c.PublicIntents.Path) == "" {
		c.PublicIntents.Path = filepath.Join("configs", "public_intents.yaml")
	}
	if c.PublicQuery.Confidence.DirectMin <= 0 {
		c.PublicQuery.Confidence.DirectMin = 0.70
	}
	if c.PublicQuery.Confidence.ReviewMin <= 0 {
		c.PublicQuery.Confidence.ReviewMin = 0.25
	}
	if c.PublicQuery.Confidence.DirectMin > 1 {
		c.PublicQuery.Confidence.DirectMin = 1
	}
	if c.PublicQuery.Confidence.ReviewMin > 1 {
		c.PublicQuery.Confidence.ReviewMin = 1
	}
	if c.PublicQuery.Confidence.ReviewMin > c.PublicQuery.Confidence.DirectMin {
		c.PublicQuery.Confidence.ReviewMin = c.PublicQuery.Confidence.DirectMin
	}
	if c.PublicQuery.CandidateTopK <= 0 {
		c.PublicQuery.CandidateTopK = 6
	}
	if c.PublicQuery.MaxEvidenceChars <= 0 {
		c.PublicQuery.MaxEvidenceChars = 2400
	}
	if c.PublicQuery.ResponseTimeoutSec <= 0 {
		c.PublicQuery.ResponseTimeoutSec = envInt("WIKIOS_PUBLIC_RESPONSE_TIMEOUT_SEC", 300)
	}
	if c.PublicQuery.AnswerLog.Enabled == nil {
		enabled := parseEnvBool(os.Getenv("WIKIOS_PUBLIC_ANSWER_LOG_ENABLED"), true)
		c.PublicQuery.AnswerLog.Enabled = &enabled
	}
	if c.PublicQuery.AnswerLog.Redact == nil {
		redact := parseEnvBool(os.Getenv("WIKIOS_PUBLIC_ANSWER_LOG_REDACT"), true)
		c.PublicQuery.AnswerLog.Redact = &redact
	}
	if c.PublicQuery.AnswerLog.RetentionDays <= 0 {
		c.PublicQuery.AnswerLog.RetentionDays = envInt("WIKIOS_PUBLIC_ANSWER_LOG_RETENTION_DAYS", 14)
	}
	if strings.TrimSpace(c.Support.Phone) == "" {
		c.Support.Phone = firstEnv("WIKIOS_SUPPORT_PHONE", "400-1080-106")
	}
	if strings.TrimSpace(c.Support.WeCom) == "" {
		c.Support.WeCom = firstEnv("WIKIOS_SUPPORT_WECOM", "企业微信")
	}
	if c.Context.MaxTokens <= 0 {
		c.Context.MaxTokens = envInt("WIKIOS_CONTEXT_MAX_TOKENS", 1000000)
	}
	if c.Context.ReserveTokens <= 0 {
		c.Context.ReserveTokens = envInt("WIKIOS_CONTEXT_RESERVE_TOKENS", 8192)
	}
	if c.Context.ReserveTokens < 0 {
		c.Context.ReserveTokens = 0
	}
	if c.Context.ReserveTokens >= c.Context.MaxTokens {
		c.Context.ReserveTokens = c.Context.MaxTokens / 10
	}
	if strings.TrimSpace(c.Context.Counter) == "" {
		c.Context.Counter = firstEnv("WIKIOS_CONTEXT_COUNTER", "tokenizer")
	}
	if strings.TrimSpace(c.Context.Tokenizer) == "" {
		c.Context.Tokenizer = firstEnv("WIKIOS_CONTEXT_TOKENIZER", "cl100k_base")
	}
	c.Web.DistDir = filepath.Clean(c.Web.DistDir)
	c.PublicIntents.Path = filepath.Clean(c.PublicIntents.Path)
	return nil
}

func firstEnv(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseEnvBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}
