package service

import (
	"testing"
)

func TestFormatCustomerBeijingTime(t *testing.T) {
	got := formatCustomerBeijingTime("2026-05-11T08:58:00Z")
	if got != "2026-05-11 16:58:00 Asia/Shanghai" {
		t.Fatalf("unexpected Beijing time: %q", got)
	}
}

func TestCustomerSafeThinkingForLogUsesProcessSummary(t *testing.T) {
	resp := &CustomerChatResponse{
		Details: map[string]any{
			"process_summary": "1. 已完成公开问答安全检查。\n2. 已生成客户可见回复。",
		},
	}

	got := customerSafeThinkingForLog(resp, "内部推导：1000个静态IP 4折，10元/个/月。")
	if !containsAny(got, "公开问答安全检查") {
		t.Fatalf("expected process summary in log thinking, got %q", got)
	}
}

func TestCustomerRawModelOutputLogSummaryOmitsRawJSON(t *testing.T) {
	got := customerRawModelOutputLogSummary(`{"answer":"这个数量可以申请优惠，折后约 10元/个/月。"}`)
	if !got["omitted"].(bool) || got["chars"].(int) == 0 {
		t.Fatalf("expected omitted raw model output summary, got %#v", got)
	}
	encoded := got["reason"].(string)
	if containsAny(encoded, "10元") {
		t.Fatalf("expected no raw model content in summary, got %#v", got)
	}
}
