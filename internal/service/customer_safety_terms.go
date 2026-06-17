package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"wikios/internal/config"
)

type CustomerSafetyTermManager struct {
	cfg config.CustomerSafetyTerms
}

type CustomerSafetyTermsConfig struct {
	Version    int                          `yaml:"version" json:"version"`
	Categories []CustomerSafetyTermCategory `yaml:"categories" json:"categories"`
}

type CustomerSafetyTermCategory struct {
	ID            string   `yaml:"id" json:"id"`
	Name          string   `yaml:"name" json:"name"`
	Signals       []string `yaml:"signals" json:"signals"`
	RouteTo       string   `yaml:"route_to" json:"route_to"`
	ResponseGoal  string   `yaml:"response_goal" json:"response_goal"`
	LegacyRefusal string   `yaml:"refusal,omitempty" json:"-"`
}

type CustomerSafetyTermsStatus struct {
	Path          string `json:"path"`
	LoadedAt      string `json:"loaded_at,omitempty"`
	Error         string `json:"error,omitempty"`
	CategoryCount int    `json:"category_count"`
}

func NewCustomerSafetyTermManager(cfg config.CustomerSafetyTerms) *CustomerSafetyTermManager {
	return &CustomerSafetyTermManager{cfg: cfg}
}

func (m *CustomerSafetyTermManager) PromptBlock() string {
	if m == nil || !customerSafetyTermsEnabled(m.cfg) {
		return ""
	}
	raw, err := os.ReadFile(m.path())
	if err != nil {
		return ""
	}
	terms, err := parseCustomerSafetyTerms(raw)
	if err != nil {
		return ""
	}
	return formatCustomerSafetyTermsPromptBlock(terms)
}

func (m *CustomerSafetyTermManager) Config() (CustomerSafetyTermsConfig, CustomerSafetyTermsStatus, error) {
	status := CustomerSafetyTermsStatus{Path: m.path()}
	if m == nil || !customerSafetyTermsEnabled(m.cfg) {
		status.Error = "customer safety terms are disabled"
		return CustomerSafetyTermsConfig{Version: 1}, status, errors.New(status.Error)
	}
	raw, err := os.ReadFile(m.path())
	if err != nil {
		status.Error = err.Error()
		return CustomerSafetyTermsConfig{Version: 1}, status, err
	}
	parsed, err := parseCustomerSafetyTerms(raw)
	if err != nil {
		status.Error = err.Error()
		return CustomerSafetyTermsConfig{Version: 1}, status, err
	}
	status.LoadedAt = time.Now().Format(time.RFC3339)
	status.CategoryCount = len(parsed.Categories)
	return parsed, status, nil
}

func (m *CustomerSafetyTermManager) Source() (string, error) {
	if m == nil || !customerSafetyTermsEnabled(m.cfg) {
		return "", fmt.Errorf("customer safety terms are disabled")
	}
	raw, err := os.ReadFile(m.path())
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (m *CustomerSafetyTermManager) SourceOrDefault() string {
	source, err := m.Source()
	if err == nil {
		return source
	}
	raw, _ := yaml.Marshal(defaultCustomerSafetyTermsConfig())
	return string(raw)
}

func (m *CustomerSafetyTermManager) SaveConfig(next CustomerSafetyTermsConfig) (CustomerSafetyTermsStatus, error) {
	status := CustomerSafetyTermsStatus{Path: m.path()}
	if m == nil || !customerSafetyTermsEnabled(m.cfg) {
		status.Error = "customer safety terms are disabled"
		return status, errors.New(status.Error)
	}
	normalized, err := normalizeCustomerSafetyTerms(next, true)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	raw, err := yaml.Marshal(normalized)
	if err != nil {
		status.Error = err.Error()
		return status, err
	}
	path := m.path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		status.Error = err.Error()
		return status, err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		status.Error = err.Error()
		return status, err
	}
	status.LoadedAt = time.Now().Format(time.RFC3339)
	status.CategoryCount = len(normalized.Categories)
	return status, nil
}

func (m *CustomerSafetyTermManager) path() string {
	if m == nil {
		return filepath.Join("configs", "customer_safety_terms.yaml")
	}
	path := filepath.Clean(strings.TrimSpace(m.cfg.Path))
	if path == "" || path == "." {
		path = filepath.Join("configs", "customer_safety_terms.yaml")
	}
	return path
}

func customerSafetyTermsEnabled(cfg config.CustomerSafetyTerms) bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

func parseCustomerSafetyTerms(raw []byte) (CustomerSafetyTermsConfig, error) {
	var parsed CustomerSafetyTermsConfig
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		return CustomerSafetyTermsConfig{}, err
	}
	return normalizeCustomerSafetyTerms(parsed, false)
}

func normalizeCustomerSafetyTerms(parsed CustomerSafetyTermsConfig, strict bool) (CustomerSafetyTermsConfig, error) {
	if parsed.Version == 0 {
		parsed.Version = 1
	}
	if parsed.Version != 1 {
		return CustomerSafetyTermsConfig{}, fmt.Errorf("version must be 1")
	}
	categories := make([]CustomerSafetyTermCategory, 0, len(parsed.Categories))
	seen := map[string]bool{}
	for i, category := range parsed.Categories {
		category.ID = normalizeSafetyTermAtom(category.ID)
		category.Name = strings.TrimSpace(category.Name)
		category.RouteTo = normalizeCustomerSpecialist(category.RouteTo)
		category.ResponseGoal = strings.TrimSpace(firstNonEmpty(category.ResponseGoal, category.LegacyRefusal))
		category.LegacyRefusal = ""
		if category.ID == "" {
			if strict {
				return CustomerSafetyTermsConfig{}, fmt.Errorf("categories[%d].id is required", i)
			}
			continue
		}
		if category.Name == "" {
			if strict {
				return CustomerSafetyTermsConfig{}, fmt.Errorf("categories[%d].name is required", i)
			}
			continue
		}
		if seen[category.ID] {
			if strict {
				return CustomerSafetyTermsConfig{}, fmt.Errorf("duplicate category id %q", category.ID)
			}
			continue
		}
		category.Signals = cleanSafetyTermSignals(category.Signals)
		if len(category.Signals) == 0 {
			if strict {
				return CustomerSafetyTermsConfig{}, fmt.Errorf("categories[%d].signals is required", i)
			}
			continue
		}
		if category.ResponseGoal == "" {
			if strict {
				return CustomerSafetyTermsConfig{}, fmt.Errorf("categories[%d].response_goal is required", i)
			}
			continue
		}
		if category.RouteTo != "safety" {
			category.RouteTo = "safety"
		}
		seen[category.ID] = true
		categories = append(categories, category)
		if len(categories) >= 20 {
			break
		}
	}
	if strict && len(categories) == 0 {
		return CustomerSafetyTermsConfig{}, fmt.Errorf("at least one category is required")
	}
	parsed.Categories = categories
	return parsed, nil
}

func cleanSafetyTermSignals(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) >= 20 {
			break
		}
	}
	return out
}

func normalizeSafetyTermAtom(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatCustomerSafetyTermsPromptBlock(terms CustomerSafetyTermsConfig) string {
	if len(terms.Categories) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 服务端注入：安全风险信号表\n\n")
	b.WriteString("这些信号只用于识别风险意图和选择 safety 回复目标；不是命中即拒答。必须结合上下文判断客户真实诉求，普通价格、产品、技术或售后问题不要因为出现某个词而误拒。`response_goal` 是语义目标，不是固定话术；不要逐字照抄，要用自然短句表达同一含义。不要向客户提到词表、配置或服务端注入。\n\n")
	b.WriteString("分类：\n")
	for _, category := range terms.Categories {
		b.WriteString("- ")
		b.WriteString(category.Name)
		b.WriteString(" (`")
		b.WriteString(category.ID)
		b.WriteString("`)\n")
		b.WriteString("  - signals: ")
		b.WriteString(strings.Join(category.Signals, "、"))
		b.WriteString("\n")
		b.WriteString("  - route_to: `")
		b.WriteString(category.RouteTo)
		b.WriteString("`\n")
		if category.ResponseGoal != "" {
			b.WriteString("  - response_goal: ")
			b.WriteString(category.ResponseGoal)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func defaultCustomerSafetyTermsConfig() CustomerSafetyTermsConfig {
	return CustomerSafetyTermsConfig{
		Version: 1,
		Categories: []CustomerSafetyTermCategory{
			{
				ID:           "platform_evasion",
				Name:         "绕平台风控",
				Signals:      []string{"绕检测", "过风控", "防检测", "防封", "规避封禁", "避免封号"},
				RouteTo:      "safety",
				ResponseGoal: "表达不能承诺规避平台风控、避免封号或保证账号结果。",
			},
			{
				ID:           "account_abuse",
				Name:         "账号违规",
				Signals:      []string{"批量注册", "刷号", "养号", "批量养号", "多账号规避"},
				RouteTo:      "safety",
				ResponseGoal: "表达不能提供绕过平台检测、批量注册或账号滥用方法。",
			},
			{
				ID:           "illegal_cross_border_access",
				Name:         "违规跨境联网",
				Signals:      []string{"翻墙", "科学上网", "梯子", "机场", "机场节点", "VPN", "Clash", "Clash Verge", "小火箭", "Shadowrocket", "V2Ray", "Xray", "Trojan", "Shadowsocks", "SSR", "Sing-box", "Hysteria", "Quantumult", "圈X", "Surge"},
				RouteTo:      "safety",
				ResponseGoal: "表达不能提供翻墙、机场节点、Clash、小火箭、VPN 等违规跨境联网工具的配置、节点、教程或使用方法。",
			},
		},
	}
}
