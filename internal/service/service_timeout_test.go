package service

import (
	"testing"
	"time"

	"wikios/internal/config"
)

func TestLLMRequestTimeoutTreatsPublicExecutionsAsPublic(t *testing.T) {
	svc := baseService{deps: Deps{Config: &config.Config{LLM: config.LLMConfig{TimeoutSec: 11, AdminTimeoutSec: 22}}}}

	if got := svc.llmRequestTimeout(nil); got != 11*time.Second {
		t.Fatalf("expected nil execution to use public timeout, got %s", got)
	}
	if got := svc.llmRequestTimeout(NewExecution("public-answer")); got != 11*time.Second {
		t.Fatalf("expected public-answer execution to use public timeout, got %s", got)
	}
	if got := svc.llmRequestTimeout(NewExecution("public-routed-answer")); got != 11*time.Second {
		t.Fatalf("expected public-routed-answer execution to use public timeout, got %s", got)
	}
	if got := svc.llmRequestTimeout(NewExecution("direct")); got != 22*time.Second {
		t.Fatalf("expected direct execution to use admin timeout, got %s", got)
	}
}

func TestLLMRequestTimeoutUsesPublicResponseTimeoutAsFloor(t *testing.T) {
	svc := baseService{deps: Deps{Config: &config.Config{
		LLM:         config.LLMConfig{TimeoutSec: 11, AdminTimeoutSec: 22},
		PublicQuery: config.PublicQueryConfig{ResponseTimeoutSec: 300},
	}}}

	if got := svc.llmRequestTimeout(NewExecution("public-routed-answer")); got != 300*time.Second {
		t.Fatalf("expected public response timeout floor, got %s", got)
	}
	if got := svc.llmRequestTimeout(NewExecution("direct")); got != 22*time.Second {
		t.Fatalf("expected admin execution to keep admin timeout, got %s", got)
	}
}
