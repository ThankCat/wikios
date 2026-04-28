package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"wikios/internal/config"
)

func TestFetchDeepSeekBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Fatalf("expected /user/balance, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"is_available": true,
			"balance_infos": [
				{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}
			]
		}`))
	}))
	defer server.Close()

	resp, err := FetchDeepSeekBalance(context.Background(), config.LLMConfig{
		APIKey:  "test-key",
		BaseURL: server.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("fetch balance: %v", err)
	}
	if !resp.IsAvailable || len(resp.BalanceInfos) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.BalanceInfos[0].Currency != "CNY" || resp.BalanceInfos[0].TotalBalance != "110.00" {
		t.Fatalf("unexpected balance info: %+v", resp.BalanceInfos[0])
	}
	if resp.CheckedAt == "" {
		t.Fatalf("expected checked_at")
	}
}

func TestFetchDeepSeekBalanceRequiresAPIKey(t *testing.T) {
	_, err := FetchDeepSeekBalance(context.Background(), config.LLMConfig{BaseURL: "https://api.deepseek.com/v1"})
	if err == nil {
		t.Fatalf("expected missing api key error")
	}
}
