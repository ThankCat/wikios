package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"wikios/internal/config"
)

type LLMBalanceResponse struct {
	IsAvailable  bool             `json:"is_available"`
	BalanceInfos []LLMBalanceInfo `json:"balance_infos"`
	CheckedAt    string           `json:"checked_at"`
}

type LLMBalanceInfo struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"total_balance"`
	GrantedBalance  string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}

func FetchDeepSeekBalance(ctx context.Context, cfg config.LLMConfig) (*LLMBalanceResponse, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("deepseek api key is not configured")
	}
	endpoint, err := deepSeekBalanceURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("deepseek balance request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed LLMBalanceResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode deepseek balance response: %w", err)
	}
	parsed.CheckedAt = time.Now().Format(time.RFC3339Nano)
	return &parsed, nil
}

func deepSeekBalanceURL(baseURL string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://api.deepseek.com"
	}
	base = strings.TrimSuffix(base, "/v1")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return "", fmt.Errorf("invalid deepseek base url")
	}
	return base + "/user/balance", nil
}
