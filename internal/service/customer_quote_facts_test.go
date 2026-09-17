package service

import (
	"strings"
	"testing"

	"wikios/internal/config"
)

func TestBuildCustomerQuoteFactsDiscountWithoutBandwidthIsMissingSlots(t *testing.T) {
	facts := BuildCustomerQuoteFacts(
		CustomerChatRequest{Question: "太贵了能便宜点吗"},
		&CustomerRouterOutput{Slots: CustomerRouterSlots{
			PrimaryProduct: "static_ip",
			IPType:         "datacenter",
			Quantity:       "15",
		}},
		customerSpecialistProfile("pricing"),
	)
	if facts.Status != customerQuoteStatusMissingSlots {
		t.Fatalf("want missing_slots, got %+v", facts)
	}
	if facts.Bandwidth != "" {
		t.Fatalf("bandwidth must stay empty, got %q", facts.Bandwidth)
	}
}

func TestBuildCustomerQuoteFactsDiscountWithHistoryGoesToFloor(t *testing.T) {
	facts := BuildCustomerQuoteFacts(
		CustomerChatRequest{
			Question: "太贵了能便宜点吗",
			History: []ChatMessage{
				{Role: "user", Content: "静态IP 5M要15条多少钱"},
				{Role: "assistant", Content: "5M 15条可以做到 23 元/条/月。"},
			},
		},
		&CustomerRouterOutput{Slots: CustomerRouterSlots{
			PrimaryProduct: "static_ip",
			IPType:         "datacenter",
			Bandwidth:      "5M",
			Quantity:       "15",
		}},
		customerSpecialistProfile("pricing"),
	)
	if facts.Status != customerQuoteStatusQuoted || facts.UnitPrice != 18 {
		t.Fatalf("follow-up discount want 18, got %+v", facts)
	}
}

func TestBuildCustomerQuoteFactsSwitchSpecDoesNotReuseOldFloor(t *testing.T) {
	facts := BuildCustomerQuoteFacts(
		CustomerChatRequest{
			Question: "换成10M，还是15条",
			History: []ChatMessage{
				{Role: "user", Content: "静态IP 5M要15条多少钱"},
				{Role: "assistant", Content: "5M 15条可以做到 20 元/条/月。"},
			},
		},
		&CustomerRouterOutput{Slots: CustomerRouterSlots{
			PrimaryProduct: "static_ip",
			IPType:         "datacenter",
			Bandwidth:      "10M",
			Quantity:       "15",
		}},
		customerSpecialistProfile("pricing"),
	)
	if facts.Status != customerQuoteStatusQuoted || facts.UnitPrice != 25 || facts.Status == customerQuoteStatusAtFloor {
		t.Fatalf("new spec must first-quote high, got %+v", facts)
	}
}

func TestCustomerSpecialistDecisionPromptOmitsPricingPageWhenQuoteResolved(t *testing.T) {
	svc := NewCustomerChatService(Deps{Config: &config.Config{}})
	router := CustomerRouterOutput{
		Specialist: "pricing",
		Slots: CustomerRouterSlots{
			PrimaryProduct: "static_ip",
			IPType:         "datacenter",
			Bandwidth:      "5M",
			Quantity:       "15",
		},
	}
	evidence := customerSpecialistEvidenceResult{
		Profile: customerSpecialistProfile("pricing"),
		ContentBlocks: []string{
			"wiki/knowledge/si-ye-tian-static-ip-pricing.md\n首次报价报最高价，再低走申请。",
		},
	}
	prompt := svc.customerSpecialistDecisionPrompt(
		CustomerChatRequest{
			Question: "太贵了能便宜点吗",
			History: []ChatMessage{
				{Role: "user", Content: "静态IP 5M要15条多少钱"},
				{Role: "assistant", Content: "5M 15条可以做到 23 元/条/月。"},
			},
		},
		"2026-09-17T10:00:00+08:00",
		&router,
		evidence.Profile,
		evidence,
		RuntimeSupportSettings{},
		"hard",
	)
	if strings.Contains(prompt, "再低走申请") || strings.Contains(prompt, "首次报价报最高价") {
		t.Fatalf("resolved quote must not send conflicting pricing page: %s", prompt)
	}
	if !strings.Contains(prompt, "quote_facts:") {
		t.Fatal("missing quote_facts")
	}
	if !strings.Contains(prompt, `"unit_price":18`) {
		t.Fatalf("follow-up quote_facts must use floor 18, got:\n%s", prompt)
	}
}
