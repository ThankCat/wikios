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

func TestParseCustomerRoutedSpecialistOutputRejectsMissingRequiredFields(t *testing.T) {
	svc := NewCustomerChatService(Deps{})
	_, err := svc.parseCustomerRoutedSpecialistOutput(`{}`)
	if err == nil || !strings.Contains(err.Error(), "missing required fields") {
		t.Fatalf("expected missing required fields error, got %v", err)
	}
}
