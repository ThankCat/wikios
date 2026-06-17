package service

import (
	"strings"
	"testing"
)

func TestCustomerSpecialistResponseFormatRequiresNonEmptyAnswer(t *testing.T) {
	format := customerSpecialistResponseFormat()
	if format == nil || format.JSONSchema == nil {
		t.Fatal("expected specialist response format schema")
	}
	properties, _ := format.JSONSchema.Schema["properties"].(map[string]any)
	answer, _ := properties["answer"].(map[string]any)
	if answer["minLength"] != 1 {
		t.Fatalf("expected answer minLength=1, got %#v", answer)
	}
}

func TestCustomerSpecialistResponseFormatRequiresConfidenceBreakdown(t *testing.T) {
	format := customerSpecialistResponseFormat()
	if format == nil || format.JSONSchema == nil {
		t.Fatal("expected specialist response format schema")
	}
	required, _ := format.JSONSchema.Schema["required"].([]any)
	if !containsAnyValue(required, "confidence_breakdown") {
		t.Fatalf("expected confidence_breakdown to be required, got %#v", required)
	}
	properties, _ := format.JSONSchema.Schema["properties"].(map[string]any)
	breakdown, _ := properties["confidence_breakdown"].(map[string]any)
	breakdownRequired, _ := breakdown["required"].([]any)
	for _, want := range []string{"evidence_coverage", "source_directness", "answer_specificity", "missing_info_impact", "risk_sensitivity"} {
		if !containsAnyValue(breakdownRequired, want) {
			t.Fatalf("expected confidence_breakdown.%s to be required, got %#v", want, breakdownRequired)
		}
	}
}

func TestNormalizeCustomerChatOutputDerivesConfidenceFromBreakdown(t *testing.T) {
	parsed := normalizeCustomerChatOutput(customerChatLLMOutput{
		AnswerMode: "evidence",
		AnswerText: "可以先核对代理地址、端口、账号密码和协议类型。",
		ConfidenceBreakdown: customerConfidenceBreakdown{
			EvidenceCoverage:  0.2,
			SourceDirectness:  0.4,
			AnswerSpecificity: 0.8,
			MissingInfoImpact: 0.7,
			RiskSensitivity:   0.5,
		},
		Confidence:         0.99,
		EvidenceConfidence: 0.99,
	})
	if parsed.Confidence != 0.52 {
		t.Fatalf("expected confidence to be breakdown average 0.52, got %.2f", parsed.Confidence)
	}
	if parsed.EvidenceConfidence != 0.3 {
		t.Fatalf("expected evidence_confidence to use evidence dimensions 0.30, got %.2f", parsed.EvidenceConfidence)
	}
}

func TestParseCustomerRoutedSpecialistOutputRejectsEmptyContent(t *testing.T) {
	svc := NewCustomerChatService(Deps{})
	_, err := svc.parseCustomerRoutedSpecialistOutput(`{}`)
	if err == nil || !strings.Contains(err.Error(), "empty answer") {
		t.Fatalf("expected empty answer error, got %v", err)
	}
}

func TestCustomerAppGuardRewritesForbiddenMobileAppAnswer(t *testing.T) {
	result := customerAppGuardAnswer(
		CustomerChatRequest{Question: "API 怎么提取？", ClientChannel: "mobile_app"},
		"可以在电脑端通过 API 提取链接并配置 SOCKS5 代理地址和端口。",
	)
	if !result.Triggered {
		t.Fatalf("expected app guard to trigger")
	}
	if !strings.Contains(result.Answer, "不支持") {
		t.Fatalf("expected explicit unsupported wording, got %q", result.Answer)
	}
	for _, forbidden := range []string{"提取链接", "代理地址和端口", "配置 SOCKS5"} {
		if strings.Contains(strings.ToLower(result.Answer), forbidden) {
			t.Fatalf("fallback answer should avoid %q, got %q", forbidden, result.Answer)
		}
	}
	if !strings.Contains(result.Answer, "手机 App") || !strings.Contains(result.Answer, "共享静态 IP") {
		t.Fatalf("expected mobile app operable fallback, got %q", result.Answer)
	}
}

func TestParseCustomerRoutedSpecialistOutputToleratesMissingAnswerMode(t *testing.T) {
	svc := NewCustomerChatService(Deps{})
	// Some providers don't fully honor strict json_schema and drop a metadata
	// field. A complete, well-sourced answer must not be discarded just because
	// answer_mode is missing; it should be defaulted/inferred instead.
	raw := `{
		"answer": "静态 IP 切换前，请先登录会员中心确认当前套餐、地区和剩余切换次数。",
		"review_question": "",
		"confidence_breakdown": {"evidence_coverage": 0.95, "source_directness": 0.95, "answer_specificity": 0.85, "missing_info_impact": 0.95, "risk_sensitivity": 0.70},
		"confidence": 0.88,
		"evidence_confidence": 0.95,
		"review_required": false,
		"review_reason": "",
		"suggested_target_path": "",
		"sources": [{"path": "wiki/procedures/si-ye-tian-static-ip-usage.md", "confidence": "high"}],
		"notes": ""
	}`
	parsed, err := svc.parseCustomerRoutedSpecialistOutput(raw)
	if err != nil {
		t.Fatalf("expected missing answer_mode to be tolerated, got error: %v", err)
	}
	if strings.TrimSpace(parsed.AnswerText) == "" {
		t.Fatalf("expected answer to be preserved, got empty")
	}
	if parsed.AnswerMode != "evidence" {
		t.Fatalf("expected answer_mode to be inferred as evidence from sources, got %q", parsed.AnswerMode)
	}
}
