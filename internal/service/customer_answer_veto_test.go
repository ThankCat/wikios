package service

import (
	"strings"
	"testing"
)

func TestVetoCustomerVisibleAnswerDoesNotInsertFallback(t *testing.T) {
	kept, changed, reason := VetoCustomerVisibleAnswer(
		"根据资料库，5M 可以帮您提交特价申请到 10 元/条/月。",
		CustomerQuoteFacts{Status: customerQuoteStatusAtFloor, UnitPrice: 20, FloorPrice: 20},
	)
	if kept != "" {
		t.Fatalf("expected empty after veto, got %q", kept)
	}
	if !changed || reason == "" {
		t.Fatal("expected veto reason")
	}
	if strings.Contains(kept, "请告诉我需要的带宽") || strings.Contains(kept, "我可以直接回答") {
		t.Fatal("veto must not author a replacement sentence")
	}
}

func TestVetoCustomerVisibleAnswerBlocksBelowQuoteFacts(t *testing.T) {
	kept, _, _ := VetoCustomerVisibleAnswer(
		"那就给您 10 元/条/月吧。",
		CustomerQuoteFacts{Status: customerQuoteStatusQuoted, UnitPrice: 20, FloorPrice: 18},
	)
	if strings.Contains(kept, "10") {
		t.Fatalf("below-facts price leaked: %q", kept)
	}
}

func TestVetoCustomerVisibleAnswerBlocksBelowUnitPrice(t *testing.T) {
	kept, changed, reason := VetoCustomerVisibleAnswer(
		"那就给您 20 元/条/月吧。",
		CustomerQuoteFacts{Status: customerQuoteStatusQuoted, UnitPrice: 25, FloorPrice: 20},
	)
	if strings.Contains(kept, "20") {
		t.Fatalf("below unit_price leaked: %q changed=%v reason=%s", kept, changed, reason)
	}
}
