package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
)

func TestCustomerSafetyTermsPromptBlockFormatsRiskSignals(t *testing.T) {
	raw := []byte(`version: 1
categories:
  - id: platform-evasion
    name: 绕平台风控
    signals: ["绕检测", "过风控", "绕检测"]
    route_to: pricing
    response_goal: 表达不能承诺规避平台风控、避免封号或保证账号结果。
  - id: legacy-internal
    name: 内部信息
    signals: ["prompt"]
    route_to: safety
    refusal: 旧字段也会转成回复目标。
  - id: illegal-cross-border-access
    name: 违规跨境联网
    signals: ["翻墙", "VPN", "Clash", "小火箭"]
    route_to: safety
    response_goal: 表达不能提供翻墙、机场节点、Clash、小火箭、VPN 等违规跨境联网工具的配置、节点、教程或使用方法。
  - id: ""
    name: 无效分类
    signals: ["无效"]
    route_to: safety
`)
	terms, err := parseCustomerSafetyTerms(raw)
	if err != nil {
		t.Fatalf("parse safety terms: %v", err)
	}
	block := formatCustomerSafetyTermsPromptBlock(terms)
	for _, want := range []string{
		"服务端注入：安全风险信号表",
		"不是命中即拒答",
		"普通价格、产品、技术或售后问题不要因为出现某个词而误拒",
		"绕平台风控 (`platform_evasion`)",
		"signals: 绕检测、过风控",
		"route_to: `safety`",
		"`response_goal` 是语义目标，不是固定话术",
		"不要逐字照抄",
		"response_goal: 表达不能承诺规避平台风控、避免封号或保证账号结果。",
		"内部信息 (`legacy_internal`)",
		"response_goal: 旧字段也会转成回复目标。",
		"违规跨境联网 (`illegal_cross_border_access`)",
		"signals: 翻墙、VPN、Clash、小火箭",
		"response_goal: 表达不能提供翻墙、机场节点、Clash、小火箭、VPN 等违规跨境联网工具的配置、节点、教程或使用方法。",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("expected safety terms block to include %q, got:\n%s", want, block)
		}
	}
	if strings.Contains(block, "无效分类") {
		t.Fatalf("expected invalid category to be skipped, got:\n%s", block)
	}
}

func TestLoadCustomerRouterSystemPromptInjectsSafetyTerms(t *testing.T) {
	promptDir := testCustomerRouterPromptDir(t)
	termsPath := writeTestCustomerSafetyTerms(t)
	svc := NewCustomerChatService(Deps{
		Config:      &config.Config{},
		PromptDir:   promptDir,
		SafetyTerms: NewCustomerSafetyTermManager(config.CustomerSafetyTerms{Path: termsPath}),
	})
	prompt, err := svc.loadCustomerRouterSystemPrompt()
	if err != nil {
		t.Fatalf("load router system prompt: %v", err)
	}
	for _, want := range []string{
		"router prompt",
		"服务端注入：安全风险信号表",
		"绕平台风控",
		"`response_goal` 是语义目标，不是固定话术",
		"不要逐字照抄",
		"response_goal: 表达不能承诺规避平台风控、避免封号或保证账号结果。",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected router prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestLoadCustomerSpecialistSystemPromptInjectsSafetyTermsOnlyForSafety(t *testing.T) {
	root := t.TempDir()
	promptDir := testCustomerRouterPromptDir(t)
	writeCustomerRoutedTestPrompts(t, root, promptDir)
	termsPath := writeTestCustomerSafetyTerms(t)
	svc := NewCustomerChatService(Deps{
		Config:      &config.Config{},
		PromptDir:   promptDir,
		SafetyTerms: NewCustomerSafetyTermManager(config.CustomerSafetyTerms{Path: termsPath}),
	})
	safetyPrompt, err := svc.loadCustomerSpecialistSystemPrompt(customerSpecialistProfile("safety"))
	if err != nil {
		t.Fatalf("load safety prompt: %v", err)
	}
	if !strings.Contains(safetyPrompt, "服务端注入：安全风险信号表") ||
		!strings.Contains(safetyPrompt, "绕平台风控") {
		t.Fatalf("expected safety specialist prompt to include safety terms, got:\n%s", safetyPrompt)
	}
	pricingPrompt, err := svc.loadCustomerSpecialistSystemPrompt(customerSpecialistProfile("pricing"))
	if err != nil {
		t.Fatalf("load pricing prompt: %v", err)
	}
	if strings.Contains(pricingPrompt, "服务端注入：安全风险信号表") ||
		strings.Contains(pricingPrompt, "绕平台风控") {
		t.Fatalf("expected pricing specialist prompt not to include safety terms, got:\n%s", pricingPrompt)
	}
}

func writeTestCustomerSafetyTerms(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "customer_safety_terms.yaml")
	source := `version: 1
categories:
  - id: platform_evasion
    name: 绕平台风控
    signals:
      - 绕检测
      - 过风控
    route_to: safety
    response_goal: 表达不能承诺规避平台风控、避免封号或保证账号结果。
  - id: illegal_cross_border_access
    name: 违规跨境联网
    signals:
      - 翻墙
      - VPN
      - Clash
      - 小火箭
    route_to: safety
    response_goal: 表达不能提供翻墙、机场节点、Clash、小火箭、VPN 等违规跨境联网工具的配置、节点、教程或使用方法。
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write test safety terms: %v", err)
	}
	return path
}
