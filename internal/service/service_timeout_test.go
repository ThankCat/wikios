package service

import (
	"testing"
	"time"

	"wikios/internal/config"
)

func TestLLMRequestTimeoutTreatsCustomerExecutionsAsCustomer(t *testing.T) {
	svc := baseService{deps: Deps{Config: &config.Config{LLM: config.LLMConfig{TimeoutSec: 11, AdminTimeoutSec: 22}}}}

	if got := svc.llmRequestTimeout(nil); got != 11*time.Second {
		t.Fatalf("expected nil execution to use customer timeout, got %s", got)
	}
	if got := svc.llmRequestTimeout(NewExecution("customer-answer")); got != 11*time.Second {
		t.Fatalf("expected customer-answer execution to use customer timeout, got %s", got)
	}
	if got := svc.llmRequestTimeout(NewExecution("customer-routed-answer")); got != 11*time.Second {
		t.Fatalf("expected customer-routed-answer execution to use customer timeout, got %s", got)
	}
	if got := svc.llmRequestTimeout(NewExecution("direct")); got != 22*time.Second {
		t.Fatalf("expected direct execution to use admin timeout, got %s", got)
	}
}

func TestLLMRequestTimeoutUsesCustomerResponseTimeoutAsFloor(t *testing.T) {
	svc := baseService{deps: Deps{Config: &config.Config{
		LLM:          config.LLMConfig{TimeoutSec: 11, AdminTimeoutSec: 22},
		CustomerChat: config.CustomerQueryConfig{ResponseTimeoutSec: 300},
	}}}

	if got := svc.llmRequestTimeout(NewExecution("customer-routed-answer")); got != 300*time.Second {
		t.Fatalf("expected customer response timeout floor, got %s", got)
	}
	if got := svc.llmRequestTimeout(NewExecution("direct")); got != 22*time.Second {
		t.Fatalf("expected admin execution to keep admin timeout, got %s", got)
	}
}
