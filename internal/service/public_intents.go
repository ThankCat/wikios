package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wikios/internal/config"

	"gopkg.in/yaml.v3"
)

type PublicIntentManager struct {
	path    string
	enabled bool
	mu      sync.RWMutex
	status  PublicIntentStatus
	current atomic.Value
}

type PublicIntentStatus struct {
	Path      string    `json:"path"`
	LoadedAt  time.Time `json:"loaded_at"`
	Error     string    `json:"error,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
	RuleCount int       `json:"rule_count"`
}

type PublicIntentConfig struct {
	Version   int                   `yaml:"version" json:"version"`
	Fallbacks PublicIntentFallbacks `yaml:"fallbacks" json:"fallbacks"`
	Rules     []PublicIntentRule    `yaml:"rules" json:"rules"`
}

type PublicIntentFallbacks struct {
	Generic         string `yaml:"generic" json:"generic"`
	Operation       string `yaml:"operation" json:"operation"`
	DeviceOperation string `yaml:"device_operation" json:"device_operation"`
}

type PublicIntentRule struct {
	Name     string            `yaml:"name" json:"name"`
	Enabled  *bool             `yaml:"enabled" json:"enabled"`
	Priority int               `yaml:"priority" json:"priority"`
	Category string            `yaml:"category" json:"category"`
	Match    PublicIntentMatch `yaml:"match" json:"match"`
	Response string            `yaml:"response" json:"response"`
}

type PublicIntentMatch struct {
	Exact    []string `yaml:"exact" json:"exact"`
	Contains []string `yaml:"contains" json:"contains"`
}

type PublicIntentResult struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Response string `json:"response"`
}

type publicIntentSnapshot struct {
	config PublicIntentConfig
	rules  []PublicIntentRule
}

var publicIntentNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func NewPublicIntentManager(cfg config.PublicIntentsConfig) *PublicIntentManager {
	enabled := cfg.Enabled == nil || *cfg.Enabled
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		path = filepath.Join("configs", "public_intents.yaml")
	}
	m := &PublicIntentManager{path: filepath.Clean(path), enabled: enabled}
	m.current.Store(&publicIntentSnapshot{config: defaultPublicIntentConfig()})
	m.status = PublicIntentStatus{Path: m.path}
	if enabled {
		_ = m.Reload()
	}
	return m
}

func (m *PublicIntentManager) Match(question string) (PublicIntentResult, bool) {
	if m == nil || !m.enabled {
		return PublicIntentResult{}, false
	}
	snapshot := m.snapshot()
	normalized := normalizePublicIntentText(question)
	if normalized == "" {
		return PublicIntentResult{}, false
	}
	for _, rule := range snapshot.rules {
		if !ruleEnabled(rule) {
			continue
		}
		if publicIntentRuleMatches(rule, normalized) {
			return PublicIntentResult{
				Name:     strings.TrimSpace(rule.Name),
				Category: strings.TrimSpace(rule.Category),
				Response: strings.TrimSpace(rule.Response),
			}, true
		}
	}
	return PublicIntentResult{}, false
}

func (m *PublicIntentManager) Fallback(question string) string {
	if m == nil || !m.enabled {
		return genericPublicFallback(question)
	}
	fallbacks := m.snapshot().config.Fallbacks
	lower := strings.ToLower(strings.TrimSpace(question))
	switch {
	case containsAny(lower, "关机", "重启", "开机", "启动"):
		if strings.TrimSpace(fallbacks.DeviceOperation) != "" {
			return strings.TrimSpace(fallbacks.DeviceOperation)
		}
	case containsAny(lower, "安装", "下载", "设置", "配置", "登录"):
		if strings.TrimSpace(fallbacks.Operation) != "" {
			return strings.TrimSpace(fallbacks.Operation)
		}
	}
	if strings.TrimSpace(fallbacks.Generic) != "" {
		return strings.TrimSpace(fallbacks.Generic)
	}
	return genericPublicFallback(question)
}

func (m *PublicIntentManager) Reload() error {
	source, err := m.Source()
	if err != nil {
		if os.IsNotExist(err) {
			source = defaultPublicIntentSource()
		} else {
			m.setError(err)
			return err
		}
	}
	config, warnings, err := ParsePublicIntentConfig(source)
	if err != nil {
		m.setError(err)
		return err
	}
	m.store(config, warnings, "")
	return nil
}

func (m *PublicIntentManager) Source() (string, error) {
	if m == nil {
		return "", fmt.Errorf("public intent manager is not configured")
	}
	raw, err := os.ReadFile(m.path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (m *PublicIntentManager) SourceOrDefault() string {
	source, err := m.Source()
	if err == nil {
		return source
	}
	return defaultPublicIntentSource()
}

func (m *PublicIntentManager) Save(source string) (PublicIntentStatus, error) {
	if m == nil || !m.enabled {
		return PublicIntentStatus{}, fmt.Errorf("public intents are disabled")
	}
	config, warnings, err := ParsePublicIntentConfig(source)
	if err != nil {
		return m.Status(), err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return m.Status(), err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(source), 0o644); err != nil {
		return m.Status(), err
	}
	if err := os.Rename(tmp, m.path); err != nil {
		_ = os.Remove(tmp)
		return m.Status(), err
	}
	m.store(config, warnings, "")
	return m.Status(), nil
}

func (m *PublicIntentManager) Status() PublicIntentStatus {
	if m == nil {
		return PublicIntentStatus{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *PublicIntentManager) snapshot() *publicIntentSnapshot {
	value := m.current.Load()
	if snapshot, ok := value.(*publicIntentSnapshot); ok && snapshot != nil {
		return snapshot
	}
	return &publicIntentSnapshot{config: defaultPublicIntentConfig()}
}

func (m *PublicIntentManager) store(config PublicIntentConfig, warnings []string, errorText string) {
	rules := append([]PublicIntentRule{}, config.Rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})
	m.current.Store(&publicIntentSnapshot{config: config, rules: rules})
	m.mu.Lock()
	m.status = PublicIntentStatus{
		Path:      m.path,
		LoadedAt:  time.Now(),
		Error:     errorText,
		Warnings:  append([]string{}, warnings...),
		RuleCount: len(rules),
	}
	m.mu.Unlock()
}

func (m *PublicIntentManager) setError(err error) {
	m.mu.Lock()
	m.status.Path = m.path
	m.status.Error = err.Error()
	m.mu.Unlock()
}

func ParsePublicIntentConfig(source string) (PublicIntentConfig, []string, error) {
	var parsed PublicIntentConfig
	if err := yaml.Unmarshal([]byte(source), &parsed); err != nil {
		return PublicIntentConfig{}, nil, err
	}
	return validatePublicIntentConfig(parsed)
}

func validatePublicIntentConfig(parsed PublicIntentConfig) (PublicIntentConfig, []string, error) {
	if parsed.Version != 1 {
		return PublicIntentConfig{}, nil, fmt.Errorf("version must be 1")
	}
	if strings.TrimSpace(parsed.Fallbacks.Generic) == "" {
		parsed.Fallbacks.Generic = defaultPublicIntentConfig().Fallbacks.Generic
	}
	if banned := firstBannedPublicIntentResponseTerm(parsed.Fallbacks.Generic); banned != "" {
		return PublicIntentConfig{}, nil, fmt.Errorf("fallbacks.generic contains internal wording %q", banned)
	}
	if banned := firstBannedPublicIntentResponseTerm(parsed.Fallbacks.Operation); banned != "" {
		return PublicIntentConfig{}, nil, fmt.Errorf("fallbacks.operation contains internal wording %q", banned)
	}
	if banned := firstBannedPublicIntentResponseTerm(parsed.Fallbacks.DeviceOperation); banned != "" {
		return PublicIntentConfig{}, nil, fmt.Errorf("fallbacks.device_operation contains internal wording %q", banned)
	}
	names := map[string]bool{}
	warnings := []string{}
	maxNonSafetyPriority := -1
	minSafetyPriority := 1001
	for i := range parsed.Rules {
		rule := &parsed.Rules[i]
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Category = strings.TrimSpace(rule.Category)
		rule.Response = strings.TrimSpace(rule.Response)
		if rule.Name == "" {
			return PublicIntentConfig{}, nil, fmt.Errorf("rules[%d].name is required", i)
		}
		if !publicIntentNamePattern.MatchString(rule.Name) {
			return PublicIntentConfig{}, nil, fmt.Errorf("rules[%d].name must use letters, digits, '_' or '-'", i)
		}
		if names[rule.Name] {
			return PublicIntentConfig{}, nil, fmt.Errorf("duplicate rule name %q", rule.Name)
		}
		names[rule.Name] = true
		if rule.Priority < 0 || rule.Priority > 1000 {
			return PublicIntentConfig{}, nil, fmt.Errorf("rules[%d].priority must be between 0 and 1000", i)
		}
		var err error
		rule.Match.Exact, err = cleanPublicIntentPatterns(rule.Match.Exact, fmt.Sprintf("rules[%d].match.exact", i))
		if err != nil {
			return PublicIntentConfig{}, nil, err
		}
		rule.Match.Contains, err = cleanPublicIntentPatterns(rule.Match.Contains, fmt.Sprintf("rules[%d].match.contains", i))
		if err != nil {
			return PublicIntentConfig{}, nil, err
		}
		if !ruleEnabled(*rule) {
			continue
		}
		if len(rule.Match.Exact) == 0 && len(rule.Match.Contains) == 0 {
			return PublicIntentConfig{}, nil, fmt.Errorf("rules[%d] must define match.exact or match.contains", i)
		}
		if rule.Response == "" {
			return PublicIntentConfig{}, nil, fmt.Errorf("rules[%d].response is required", i)
		}
		if banned := firstBannedPublicIntentResponseTerm(rule.Response); banned != "" {
			return PublicIntentConfig{}, nil, fmt.Errorf("rules[%d].response contains internal wording %q", i, banned)
		}
		if strings.EqualFold(rule.Category, "safety") {
			if rule.Priority < minSafetyPriority {
				minSafetyPriority = rule.Priority
			}
		} else if rule.Priority > maxNonSafetyPriority {
			maxNonSafetyPriority = rule.Priority
		}
	}
	if minSafetyPriority <= maxNonSafetyPriority {
		warnings = append(warnings, "safety 类规则 priority 建议高于身份、寒暄和转人工规则")
	}
	return parsed, warnings, nil
}

func cleanPublicIntentPatterns(values []string, field string) ([]string, error) {
	out := make([]string, 0, len(values))
	for i, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, fmt.Errorf("%s[%d] must not be empty", field, i)
		}
		out = append(out, trimmed)
	}
	return out, nil
}

func firstBannedPublicIntentResponseTerm(response string) string {
	lower := strings.ToLower(response)
	for _, term := range []string{
		"知识库",
		"资料库",
		"检索结果",
		"wiki/",
		"wiki ",
		"slug",
		"source 页",
		"source页",
		"source page",
		"sources/",
		"索引页",
		"系统提示词",
		"根据知识库",
		"知识库中尚未收录",
		"客服知识库",
	} {
		if strings.Contains(lower, strings.ToLower(term)) {
			return term
		}
	}
	return ""
}

func publicIntentRuleMatches(rule PublicIntentRule, normalizedQuestion string) bool {
	for _, item := range rule.Match.Exact {
		if normalizePublicIntentText(item) == normalizedQuestion {
			return true
		}
	}
	for _, item := range rule.Match.Contains {
		pattern := normalizePublicIntentText(item)
		if pattern != "" && strings.Contains(normalizedQuestion, pattern) {
			return true
		}
	}
	return false
}

func ruleEnabled(rule PublicIntentRule) bool {
	return rule.Enabled == nil || *rule.Enabled
}

func normalizePublicIntentText(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, " \t\r\n？?。.!！~～")
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized
}

func defaultPublicIntentConfig() PublicIntentConfig {
	return PublicIntentConfig{
		Version: 1,
		Fallbacks: PublicIntentFallbacks{
			Generic:         "您好，这个问题我这边暂时还不能准确确认，您可以补充一下具体场景，我再为您确认。",
			Operation:       "您好，这方面我这边暂时没有可直接确认的操作说明，您可以补充一下具体场景，我再为您确认。",
			DeviceOperation: "您好，这项操作我这边暂时还不能准确确认，建议您先参考设备说明或联系对应支持人员处理。",
		},
	}
}

func defaultPublicIntentSource() string {
	return strings.TrimSpace(`version: 1

fallbacks:
  generic: 您好，这个问题我这边暂时还不能准确确认，您可以补充一下具体场景，我再为您确认。
  operation: 您好，这方面我这边暂时没有可直接确认的操作说明，您可以补充一下具体场景，我再为您确认。
  device_operation: 您好，这项操作我这边暂时还不能准确确认，建议您先参考设备说明或联系对应支持人员处理。

rules: []
`) + "\n"
}
