package service

import (
	"strings"
	"testing"

	"wikios/internal/config"
)

func TestNormalizeCustomerRouterSkillsDedupesAndCaps(t *testing.T) {
	got := normalizeCustomerRouterSkills([]string{
		customerSkillQuoteStaticIP,
		customerSkillQuoteStaticIP,
		customerSkillQuoteResidential,
		"unknown_skill",
		customerSkillCompareQuantity,
	})
	if len(got) != 2 {
		t.Fatalf("expected at most 2 skills, got %+v", got)
	}
	if got[0] != customerSkillQuoteStaticIP || got[1] != customerSkillQuoteResidential {
		t.Fatalf("unexpected skill order/content: %+v", got)
	}
}

func TestCustomerSpecialistDecisionPromptInjectsActiveSkillForPricing(t *testing.T) {
	root := t.TempDir()
	promptDir := root
	writeCustomerRoutedTestPrompts(t, root, promptDir)
	svc := NewCustomerChatService(Deps{Config: &config.Config{}, PromptDir: promptDir})
	profile := customerSpecialistProfile("pricing")
	prompt := svc.customerSpecialistDecisionPrompt(
		CustomerChatRequest{Question: "静态 IP 10M 多少钱"},
		"2026-05-27T10:00:00+08:00",
		&CustomerRouterOutput{
			ContractVersion: customerRouterContractVersion,
			Specialist:      "pricing",
			Skills:          []string{customerSkillQuoteStaticIP, customerSkillCompareShared},
			Slots:           CustomerRouterSlots{PrimaryProduct: "static_ip", Products: []string{"static_ip"}},
		},
		profile,
		customerSpecialistEvidenceResult{Profile: profile},
		RuntimeSupportSettings{},
		"boundary",
	)
	if !strings.Contains(prompt, "active_skills:") || !strings.Contains(prompt, "### skill: "+customerSkillQuoteStaticIP) {
		t.Fatalf("expected static quote skill in prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "### skill: "+customerSkillCompareShared) {
		t.Fatalf("product-only skill must not inject into pricing specialist, got:\n%s", prompt)
	}
}
