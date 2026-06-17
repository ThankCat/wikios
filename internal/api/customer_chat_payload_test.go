package api

import (
	"testing"

	"wikios/internal/service"
)

func TestCustomerChatClientPayloadIncludesIntentFields(t *testing.T) {
	resp := &service.CustomerChatResponse{
		Answer:         "数据中心 IP 批量可以走商务报价。",
		AnswerMode:     "evidence",
		ReviewRequired: false,
		SourceCount:    1,
		UserIntent: &service.CustomerUserIntent{
			Type: "discount",
			Extra: &service.CustomerUserIntentExtra{
				ProductType: "datacenter_ip",
				Quantity:    1000,
			},
		},
		ReceivedAt: "2026-05-28T02:00:00Z",
		AnsweredAt: "2026-05-28T02:00:03Z",
	}
	payload := customerChatClientPayload(resp)

	if payload["answer"] != resp.Answer {
		t.Fatalf("answer mismatch: %v", payload["answer"])
	}
	if payload["answer_mode"] != "evidence" {
		t.Fatalf("answer_mode mismatch: %v", payload["answer_mode"])
	}
	if payload["review_required"] != false {
		t.Fatalf("review_required mismatch: %v", payload["review_required"])
	}
	if payload["source_count"] != 1 {
		t.Fatalf("source_count mismatch: %v", payload["source_count"])
	}
	intent, ok := payload["user_intent"].(*service.CustomerUserIntent)
	if !ok || intent == nil {
		t.Fatalf("expected user_intent in payload, got %#v", payload["user_intent"])
	}
	if intent.Type != "discount" || intent.Extra == nil || intent.Extra.Quantity != 1000 {
		t.Fatalf("unexpected user_intent payload: %+v", intent)
	}
}

func TestCustomerChatClientPayloadNilIntent(t *testing.T) {
	resp := &service.CustomerChatResponse{Answer: "你好。"}
	payload := customerChatClientPayload(resp)
	if intent := payload["user_intent"]; intent != (*service.CustomerUserIntent)(nil) {
		t.Fatalf("expected nil user_intent, got %#v", intent)
	}
}

func TestCustomerChatClientPayloadNilResponse(t *testing.T) {
	if payload := customerChatClientPayload(nil); len(payload) != 0 {
		t.Fatalf("expected empty payload for nil response, got %#v", payload)
	}
}

func TestNormalizeCustomerChatAPIRequestClientChannel(t *testing.T) {
	req := customerChatRequest{Message: "手机端怎么用静态 IP？", ClientChannel: "mobile_app"}
	if err := normalizeCustomerChatAPIRequest(&req); err != nil {
		t.Fatalf("normalize request: %v", err)
	}
	if req.ClientChannel != "mobile_app" {
		t.Fatalf("expected mobile_app channel, got %q", req.ClientChannel)
	}

	contextReq := customerChatRequest{Message: "手机端怎么用静态 IP？", Context: map[string]any{"client_channel": "mobile_app"}}
	if err := normalizeCustomerChatAPIRequest(&contextReq); err != nil {
		t.Fatalf("normalize context request: %v", err)
	}
	if contextReq.ClientChannel != "mobile_app" {
		t.Fatalf("expected context client_channel fallback, got %q", contextReq.ClientChannel)
	}

	defaultReq := customerChatRequest{Message: "静态 IP 怎么卖？"}
	if err := normalizeCustomerChatAPIRequest(&defaultReq); err != nil {
		t.Fatalf("normalize default request: %v", err)
	}
	if defaultReq.ClientChannel != "web" {
		t.Fatalf("expected default web channel, got %q", defaultReq.ClientChannel)
	}
}

func TestNormalizeCustomerChatAPIRequestRejectsInvalidClientChannel(t *testing.T) {
	req := customerChatRequest{Message: "你好", ClientChannel: "desktop"}
	if err := normalizeCustomerChatAPIRequest(&req); err == nil {
		t.Fatal("expected invalid client_channel to be rejected")
	}
}
