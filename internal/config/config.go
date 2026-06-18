package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultConfigPath = "configs/config.local.yaml"
)

type Config struct {
	Server       ServerConfig        `yaml:"server"`
	MountedWiki  MountedWikiConfig   `yaml:"mounted_wiki"`
	LLM          LLMConfig           `yaml:"llm"`
	Retrieval    RetrievalConfig     `yaml:"retrieval"`
	Workspace    WorkspaceConfig     `yaml:"workspace"`
	Sandbox      SandboxConfig       `yaml:"sandbox"`
	Storage      StorageConfig       `yaml:"storage"`
	Sync         SyncConfig          `yaml:"sync"`
	Web          WebConfig           `yaml:"web"`
	Upload       UploadConfig        `yaml:"upload"`
	CustomerChat CustomerQueryConfig `yaml:"customer_query"`
	SafetyTerms  CustomerSafetyTerms `yaml:"customer_safety_terms"`
	Support      SupportConfig       `yaml:"support"`
	Context      ContextConfig       `yaml:"context"`
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
	// Temperature controls LLM sampling randomness. Lower values make answers
	// more deterministic/stable (recommended for grounded customer chat); nil
	// leaves it unset so the provider default applies.
	Temperature *float64 `yaml:"temperature"`
}

type RetrievalConfig struct {
	TopK    int           `yaml:"top_k"`
	Mode    string        `yaml:"mode"`
	QMDHTTP QMDHTTPConfig `yaml:"qmd_http"`
}

// QMDHTTPConfig enables retrieval against a long-running `qmd mcp --http`
// daemon, which keeps the local embedding/rerank models warm.
type QMDHTTPConfig struct {
	Enabled    bool   `yaml:"enabled"`
	URL        string `yaml:"url"`
	TimeoutSec int    `yaml:"timeout_sec"`
	// Rerank toggles the daemon's LLM reranker. Reranking improves ordering but
	// is the dominant cost on a cold query (~8s). Defaults to true. Set false to
	// rely on lexical+vector scores only (much faster, ordering still puts the
	// best in-scope page first in practice).
	Rerank *bool `yaml:"rerank"`
	// RerankCandidates caps how many candidates the reranker scores
	// (daemon's candidateLimit). Lower = faster; only the most promising
	// candidates are reranked. Defaults to 8. Ignored when Rerank is false.
	RerankCandidates int `yaml:"rerank_candidates"`
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
	Enabled    *bool  `yaml:"enabled"`
	DistDir    string `yaml:"dist_dir"`
	APIBaseURL string `yaml:"api_base_url"`
}

type UploadConfig struct {
	MaxTextFileKB int `yaml:"max_text_file_kb"`
}

type CustomerQueryConfig struct {
	Confidence         CustomerQueryConfidenceConfig `yaml:"confidence"`
	CandidateTopK      int                           `yaml:"candidate_top_k"`
	MaxEvidenceChars   int                           `yaml:"max_evidence_chars"`
	ResponseTimeoutSec int                           `yaml:"response_timeout_sec"`
	MaxConcurrent      int                           `yaml:"max_concurrent"`
	AnswerLog          CustomerChatLogConfig         `yaml:"answer_log"`
}

type CustomerSafetyTerms struct {
	Enabled *bool  `yaml:"enabled"`
	Path    string `yaml:"path"`
}

type CustomerChatLogConfig struct {
	Enabled       *bool `yaml:"enabled"`
	Redact        *bool `yaml:"redact"`
	RetentionDays int   `yaml:"retention_days"`
}

type CustomerQueryConfidenceConfig struct {
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
	c.Retrieval.Mode = strings.ToLower(strings.TrimSpace(firstEnv("WIKIOS_RETRIEVAL_MODE", c.Retrieval.Mode)))
	switch c.Retrieval.Mode {
	case "", "qmd":
		c.Retrieval.Mode = "qmd"
	case "wiki", "lite", "fallback":
		c.Retrieval.Mode = "wiki"
	default:
		return fmt.Errorf("retrieval.mode must be qmd or wiki, got %q", c.Retrieval.Mode)
	}
	if strings.TrimSpace(c.Retrieval.QMDHTTP.URL) == "" {
		c.Retrieval.QMDHTTP.URL = "http://localhost:8181/mcp"
	}
	if c.Retrieval.QMDHTTP.TimeoutSec <= 0 {
		c.Retrieval.QMDHTTP.TimeoutSec = 30
	}
	if c.Retrieval.QMDHTTP.Rerank == nil {
		enabled := true
		c.Retrieval.QMDHTTP.Rerank = &enabled
	}
	if c.Retrieval.QMDHTTP.RerankCandidates <= 0 {
		c.Retrieval.QMDHTTP.RerankCandidates = 8
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
	if c.LLM.Temperature == nil {
		if v, ok := envFloat("WIKIOS_LLM_TEMPERATURE"); ok {
			c.LLM.Temperature = &v
		}
	}
	if c.LLM.Temperature != nil {
		if *c.LLM.Temperature < 0 {
			*c.LLM.Temperature = 0
		}
		if *c.LLM.Temperature > 2 {
			*c.LLM.Temperature = 2
		}
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
	c.Web.APIBaseURL = strings.TrimSpace(firstEnv("WIKIOS_WEB_API_BASE_URL", c.Web.APIBaseURL))
	if c.Web.Enabled == nil {
		enabled := true
		c.Web.Enabled = &enabled
	}
	if c.Upload.MaxTextFileKB <= 0 {
		c.Upload.MaxTextFileKB = 500
	}
	if c.CustomerChat.Confidence.DirectMin <= 0 {
		c.CustomerChat.Confidence.DirectMin = 0.70
	}
	if c.CustomerChat.Confidence.ReviewMin <= 0 {
		c.CustomerChat.Confidence.ReviewMin = 0.25
	}
	if c.CustomerChat.Confidence.DirectMin > 1 {
		c.CustomerChat.Confidence.DirectMin = 1
	}
	if c.CustomerChat.Confidence.ReviewMin > 1 {
		c.CustomerChat.Confidence.ReviewMin = 1
	}
	if c.CustomerChat.Confidence.ReviewMin > c.CustomerChat.Confidence.DirectMin {
		c.CustomerChat.Confidence.ReviewMin = c.CustomerChat.Confidence.DirectMin
	}
	if c.CustomerChat.CandidateTopK <= 0 {
		c.CustomerChat.CandidateTopK = 6
	}
	if c.CustomerChat.MaxEvidenceChars <= 0 {
		c.CustomerChat.MaxEvidenceChars = 2400
	}
	if c.CustomerChat.ResponseTimeoutSec <= 0 {
		c.CustomerChat.ResponseTimeoutSec = envInt("WIKIOS_CUSTOMER_RESPONSE_TIMEOUT_SEC", 300)
	}
	if v, ok := envPositiveInt("WIKIOS_CUSTOMER_MAX_CONCURRENT"); ok {
		c.CustomerChat.MaxConcurrent = v
	}
	if c.CustomerChat.MaxConcurrent < 0 {
		c.CustomerChat.MaxConcurrent = 0
	}
	if c.CustomerChat.AnswerLog.Enabled == nil {
		enabled := parseEnvBool(os.Getenv("WIKIOS_CUSTOMER_CHAT_LOG_ENABLED"), true)
		c.CustomerChat.AnswerLog.Enabled = &enabled
	}
	if c.CustomerChat.AnswerLog.Redact == nil {
		redact := parseEnvBool(os.Getenv("WIKIOS_CUSTOMER_CHAT_LOG_REDACT"), true)
		c.CustomerChat.AnswerLog.Redact = &redact
	}
	if c.CustomerChat.AnswerLog.RetentionDays <= 0 {
		c.CustomerChat.AnswerLog.RetentionDays = envInt("WIKIOS_CUSTOMER_CHAT_LOG_RETENTION_DAYS", 14)
	}
	if c.SafetyTerms.Enabled == nil {
		enabled := true
		c.SafetyTerms.Enabled = &enabled
	}
	if strings.TrimSpace(c.SafetyTerms.Path) == "" {
		c.SafetyTerms.Path = filepath.Join("configs", "customer_safety_terms.yaml")
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
	c.SafetyTerms.Path = filepath.Clean(c.SafetyTerms.Path)
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

func envPositiveInt(key string) (int, bool) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

func envFloat(key string) (float64, bool) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
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
