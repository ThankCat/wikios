package service

import (
	"fmt"
	"hash/fnv"
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

type CustomerIntentManager struct {
	path    string
	enabled bool
	mu      sync.RWMutex
	status  CustomerIntentStatus
	current atomic.Value
}

type CustomerIntentStatus struct {
	Path      string    `json:"path"`
	LoadedAt  time.Time `json:"loaded_at"`
	Error     string    `json:"error,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
	RuleCount int       `json:"rule_count"`
}

type CustomerIntentConfig struct {
	Version   int                     `yaml:"version" json:"version"`
	Fallbacks CustomerIntentFallbacks `yaml:"fallbacks" json:"fallbacks"`
	Rules     []CustomerIntentRule    `yaml:"rules" json:"rules"`
}

type CustomerIntentFallbacks struct {
	Generic          string   `yaml:"generic" json:"generic"`
	Operation        string   `yaml:"operation" json:"operation"`
	DeviceOperation  string   `yaml:"device_operation" json:"device_operation"`
	ModelUnavailable []string `yaml:"model_unavailable" json:"model_unavailable"`
}

type CustomerIntentRule struct {
	Name     string              `yaml:"name" json:"name"`
	Enabled  *bool               `yaml:"enabled" json:"enabled"`
	Priority int                 `yaml:"priority" json:"priority"`
	Category string              `yaml:"category" json:"category"`
	Match    CustomerIntentMatch `yaml:"match" json:"match"`
	Response string              `yaml:"response" json:"response"`
}

type CustomerIntentMatch struct {
	Exact    []string `yaml:"exact" json:"exact"`
	Contains []string `yaml:"contains" json:"contains"`
}

type CustomerIntentResult struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Response string `json:"response"`
}

type customerIntentSnapshot struct {
	config CustomerIntentConfig
	rules  []CustomerIntentRule
}

var customerIntentNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func NewCustomerIntentManager(cfg config.CustomerIntentsConfig) *CustomerIntentManager {
	enabled := cfg.Enabled == nil || *cfg.Enabled
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		path = filepath.Join("configs", "customer_intents.yaml")
	}
	m := &CustomerIntentManager{path: filepath.Clean(path), enabled: enabled}
	m.current.Store(&customerIntentSnapshot{config: defaultCustomerIntentConfig()})
	m.status = CustomerIntentStatus{Path: m.path}
	if enabled {
		_ = m.Reload()
	}
	return m
}

func (m *CustomerIntentManager) Match(question string) (CustomerIntentResult, bool) {
	if m == nil || !m.enabled {
		return CustomerIntentResult{}, false
	}
	snapshot := m.snapshot()
	normalized := normalizeCustomerIntentText(question)
	if normalized == "" {
		return CustomerIntentResult{}, false
	}
	for _, rule := range snapshot.rules {
		if !ruleEnabled(rule) {
			continue
		}
		if customerIntentRuleMatches(rule, normalized) {
			return CustomerIntentResult{
				Name:     strings.TrimSpace(rule.Name),
				Category: strings.TrimSpace(rule.Category),
				Response: strings.TrimSpace(rule.Response),
			}, true
		}
	}
	return CustomerIntentResult{}, false
}

func (m *CustomerIntentManager) Fallback(question string) string {
	if m == nil || !m.enabled {
		return genericCustomerFallback(question)
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
	return genericCustomerFallback(question)
}

func (m *CustomerIntentManager) ModelUnavailableFallback(seed string) string {
	fallbacks := defaultCustomerIntentConfig().Fallbacks.ModelUnavailable
	if m != nil && m.enabled {
		if configured := m.snapshot().config.Fallbacks.ModelUnavailable; len(configured) > 0 {
			fallbacks = configured
		}
	}
	return pickCustomerFallback(fallbacks, seed)
}

func (m *CustomerIntentManager) Reload() error {
	source, err := m.Source()
	if err != nil {
		if os.IsNotExist(err) {
			source = defaultCustomerIntentSource()
		} else {
			m.setError(err)
			return err
		}
	}
	config, warnings, err := ParseCustomerIntentConfig(source)
	if err != nil {
		m.setError(err)
		return err
	}
	m.store(config, warnings, "")
	return nil
}

func (m *CustomerIntentManager) Source() (string, error) {
	if m == nil {
		return "", fmt.Errorf("customer intent manager is not configured")
	}
	raw, err := os.ReadFile(m.path)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (m *CustomerIntentManager) SourceOrDefault() string {
	source, err := m.Source()
	if err == nil {
		return source
	}
	return defaultCustomerIntentSource()
}

func (m *CustomerIntentManager) Save(source string) (CustomerIntentStatus, error) {
	if m == nil || !m.enabled {
		return CustomerIntentStatus{}, fmt.Errorf("customer intents are disabled")
	}
	config, warnings, err := ParseCustomerIntentConfig(source)
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

func (m *CustomerIntentManager) Status() CustomerIntentStatus {
	if m == nil {
		return CustomerIntentStatus{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *CustomerIntentManager) snapshot() *customerIntentSnapshot {
	value := m.current.Load()
	if snapshot, ok := value.(*customerIntentSnapshot); ok && snapshot != nil {
		return snapshot
	}
	return &customerIntentSnapshot{config: defaultCustomerIntentConfig()}
}

func (m *CustomerIntentManager) store(config CustomerIntentConfig, warnings []string, errorText string) {
	rules := append([]CustomerIntentRule{}, config.Rules...)
	sort.SliceStable(rules, func(i, j int) bool {
		return rules[i].Priority > rules[j].Priority
	})
	m.current.Store(&customerIntentSnapshot{config: config, rules: rules})
	m.mu.Lock()
	m.status = CustomerIntentStatus{
		Path:      m.path,
		LoadedAt:  time.Now(),
		Error:     errorText,
		Warnings:  append([]string{}, warnings...),
		RuleCount: len(rules),
	}
	m.mu.Unlock()
}

func (m *CustomerIntentManager) setError(err error) {
	m.mu.Lock()
	m.status.Path = m.path
	m.status.Error = err.Error()
	m.mu.Unlock()
}

func ParseCustomerIntentConfig(source string) (CustomerIntentConfig, []string, error) {
	var parsed CustomerIntentConfig
	if err := yaml.Unmarshal([]byte(source), &parsed); err != nil {
		return CustomerIntentConfig{}, nil, err
	}
	return validateCustomerIntentConfig(parsed)
}

func validateCustomerIntentConfig(parsed CustomerIntentConfig) (CustomerIntentConfig, []string, error) {
	if parsed.Version != 1 {
		return CustomerIntentConfig{}, nil, fmt.Errorf("version must be 1")
	}
	if strings.TrimSpace(parsed.Fallbacks.Generic) == "" {
		parsed.Fallbacks.Generic = defaultCustomerIntentConfig().Fallbacks.Generic
	}
	if banned := firstBannedCustomerIntentResponseTerm(parsed.Fallbacks.Generic); banned != "" {
		return CustomerIntentConfig{}, nil, fmt.Errorf("fallbacks.generic contains internal wording %q", banned)
	}
	if banned := firstBannedCustomerIntentResponseTerm(parsed.Fallbacks.Operation); banned != "" {
		return CustomerIntentConfig{}, nil, fmt.Errorf("fallbacks.operation contains internal wording %q", banned)
	}
	if banned := firstBannedCustomerIntentResponseTerm(parsed.Fallbacks.DeviceOperation); banned != "" {
		return CustomerIntentConfig{}, nil, fmt.Errorf("fallbacks.device_operation contains internal wording %q", banned)
	}
	parsed.Fallbacks.ModelUnavailable = cleanCustomerFallbackPool(parsed.Fallbacks.ModelUnavailable, defaultCustomerIntentConfig().Fallbacks.ModelUnavailable)
	for i, response := range parsed.Fallbacks.ModelUnavailable {
		if banned := firstBannedCustomerIntentResponseTerm(response); banned != "" {
			return CustomerIntentConfig{}, nil, fmt.Errorf("fallbacks.model_unavailable[%d] contains internal wording %q", i, banned)
		}
		if banned := firstBannedModelUnavailableFallbackTerm(response); banned != "" {
			return CustomerIntentConfig{}, nil, fmt.Errorf("fallbacks.model_unavailable[%d] contains service internals %q", i, banned)
		}
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
			return CustomerIntentConfig{}, nil, fmt.Errorf("rules[%d].name is required", i)
		}
		if !customerIntentNamePattern.MatchString(rule.Name) {
			return CustomerIntentConfig{}, nil, fmt.Errorf("rules[%d].name must use letters, digits, '_' or '-'", i)
		}
		if names[rule.Name] {
			return CustomerIntentConfig{}, nil, fmt.Errorf("duplicate rule name %q", rule.Name)
		}
		names[rule.Name] = true
		if rule.Priority < 0 || rule.Priority > 1000 {
			return CustomerIntentConfig{}, nil, fmt.Errorf("rules[%d].priority must be between 0 and 1000", i)
		}
		var err error
		rule.Match.Exact, err = cleanCustomerIntentPatterns(rule.Match.Exact, fmt.Sprintf("rules[%d].match.exact", i))
		if err != nil {
			return CustomerIntentConfig{}, nil, err
		}
		rule.Match.Contains, err = cleanCustomerIntentPatterns(rule.Match.Contains, fmt.Sprintf("rules[%d].match.contains", i))
		if err != nil {
			return CustomerIntentConfig{}, nil, err
		}
		if !ruleEnabled(*rule) {
			continue
		}
		if len(rule.Match.Exact) == 0 && len(rule.Match.Contains) == 0 {
			return CustomerIntentConfig{}, nil, fmt.Errorf("rules[%d] must define match.exact or match.contains", i)
		}
		if rule.Response == "" {
			return CustomerIntentConfig{}, nil, fmt.Errorf("rules[%d].response is required", i)
		}
		if banned := firstBannedCustomerIntentResponseTerm(rule.Response); banned != "" {
			return CustomerIntentConfig{}, nil, fmt.Errorf("rules[%d].response contains internal wording %q", i, banned)
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

func cleanCustomerFallbackPool(values []string, defaults []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		out = append(out, defaults...)
	}
	return out
}

func cleanCustomerIntentPatterns(values []string, field string) ([]string, error) {
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

func firstBannedCustomerIntentResponseTerm(response string) string {
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

func firstBannedModelUnavailableFallbackTerm(response string) string {
	lower := strings.ToLower(response)
	for _, term := range []string{
		"模型",
		"llm",
		"api key",
		"apikey",
		"base_url",
		"base url",
		"供应商",
		"服务商",
		"余额",
		"欠费",
		"管理员端",
		"后台配置",
	} {
		if strings.Contains(lower, strings.ToLower(term)) {
			return term
		}
	}
	return ""
}

func pickCustomerFallback(values []string, seed string) string {
	if len(values) == 0 {
		return customerLLMUnavailableMessage
	}
	if len(values) == 1 {
		return values[0]
	}
	trimmedSeed := strings.TrimSpace(seed)
	if trimmedSeed == "" {
		trimmedSeed = time.Now().Format(time.RFC3339Nano)
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(trimmedSeed))
	return values[int(hash.Sum32())%len(values)]
}

func genericCustomerFallback(question string) string {
	lower := strings.ToLower(strings.TrimSpace(question))
	switch {
	case containsAny(lower, "关机", "重启", "开机", "启动"):
		return "这项操作要结合设备状态来看。您可以补充设备型号、当前页面提示和想完成的动作，我先帮您判断下一步。"
	case containsAny(lower, "安装", "下载", "设置", "配置", "登录"):
		return "这类操作我需要先确认您使用的产品、设备或页面入口。您把当前步骤和遇到的提示发我，我再继续帮您排查。"
	default:
		return "这个问题我还需要再确认一点信息。您可以把具体产品、套餐或使用场景发我，我会按当前对话继续帮您判断。"
	}
}

func customerIntentRuleMatches(rule CustomerIntentRule, normalizedQuestion string) bool {
	for _, item := range rule.Match.Exact {
		if normalizeCustomerIntentText(item) == normalizedQuestion {
			return true
		}
	}
	for _, item := range rule.Match.Contains {
		pattern := normalizeCustomerIntentText(item)
		if pattern != "" && strings.Contains(normalizedQuestion, pattern) {
			return true
		}
	}
	return false
}

func ruleEnabled(rule CustomerIntentRule) bool {
	return rule.Enabled == nil || *rule.Enabled
}

func normalizeCustomerIntentText(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, " \t\r\n？?。.!！~～")
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized
}

func defaultCustomerIntentConfig() CustomerIntentConfig {
	return CustomerIntentConfig{
		Version: 1,
		Fallbacks: CustomerIntentFallbacks{
			Generic:         "这个问题我还需要再确认一点信息。您可以把具体产品、套餐或使用场景发我，我会按当前对话继续帮您判断。",
			Operation:       "这类操作我需要先确认您使用的产品、设备或页面入口。您把当前步骤和遇到的提示发我，我再继续帮您排查。",
			DeviceOperation: "这项操作要结合设备状态来看。您可以补充设备型号、当前页面提示和想完成的动作，我先帮您判断下一步。",
			ModelUnavailable: []string{
				"当前在线回复暂时有点忙，您可以稍后再发一次，我会继续按当前对话帮您处理。",
				"这边暂时没能生成准确回复，您可以稍后重试一次，前面的对话内容我会继续参考。",
				"当前回复服务短暂不可用，您可以稍后再问一次，我会继续围绕这个问题帮您确认。",
			},
		},
	}
}

func defaultCustomerIntentSource() string {
	return strings.TrimSpace(`version: 1

fallbacks:
  generic: 这个问题我还需要再确认一点信息。您可以把具体产品、套餐或使用场景发我，我会按当前对话继续帮您判断。
  operation: 这类操作我需要先确认您使用的产品、设备或页面入口。您把当前步骤和遇到的提示发我，我再继续帮您排查。
  device_operation: 这项操作要结合设备状态来看。您可以补充设备型号、当前页面提示和想完成的动作，我先帮您判断下一步。
  model_unavailable:
    - 当前在线回复暂时有点忙，您可以稍后再发一次，我会继续按当前对话帮您处理。
    - 这边暂时没能生成准确回复，您可以稍后重试一次，前面的对话内容我会继续参考。
    - 当前回复服务短暂不可用，您可以稍后再问一次，我会继续围绕这个问题帮您确认。

rules: []
`) + "\n"
}
