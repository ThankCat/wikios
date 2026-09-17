package service

import "testing"

func TestSanitizeCustomerHistoryDropsUserTextLabeledAssistant(t *testing.T) {
	got := SanitizeCustomerHistory([]ChatMessage{
		{Role: "user", Content: "静态IP 5M 15条多少钱"},
		{Role: "assistant", Content: "你好"},
		{Role: "user", Content: "太贵了"},
	})
	for _, item := range got {
		if item.Role == "assistant" && item.Content == "你好" {
			t.Fatal("short user-like greeting must not stay as assistant quote context")
		}
	}
}

func TestLastQuotedUnitPriceReadsAssistantYuanPerLine(t *testing.T) {
	got := LastQuotedUnitPrice([]ChatMessage{
		{Role: "assistant", Content: "5M 15条可以做到 23 元/条/月。"},
		{Role: "user", Content: "我想20元一条每月"},
	})
	if got != 23 {
		t.Fatalf("last quoted=%d", got)
	}
}

func TestLastQuotedUnitPriceReadsInformalYuanYiTiao(t *testing.T) {
	got := LastQuotedUnitPrice([]ChatMessage{
		{Role: "assistant", Content: "5M 15条可以做到 23元一条。"},
	})
	if got != 23 {
		t.Fatalf("informal last quoted=%d", got)
	}
}
