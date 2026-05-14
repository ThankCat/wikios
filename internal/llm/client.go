package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxTransientLLMAttempts = 3

type Client interface {
	Chat(ctx context.Context, model string, messages []Message) (string, error)
	StreamChat(ctx context.Context, model string, messages []Message, onDelta func(string)) (string, error)
}

type StreamDelta struct {
	Content          string
	ReasoningContent string
}

type EventStreamClient interface {
	StreamChatEvents(ctx context.Context, model string, messages []Message, onDelta func(StreamDelta)) (string, error)
}

type ClientConfig struct {
	APIKey     string
	BaseURL    string
	TimeoutSec int
}

type OpenAICompatibleClient struct {
	baseURL string
	apiKey  string
	timeout time.Duration
	client  *http.Client
}

func NewClient(cfg ClientConfig) Client {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &OpenAICompatibleClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		timeout: timeout,
		client:  &http.Client{},
	}
}

type requestTimeoutKey struct{}

func WithRequestTimeout(ctx context.Context, timeout time.Duration) context.Context {
	if timeout <= 0 {
		return ctx
	}
	return context.WithValue(ctx, requestTimeoutKey{}, timeout)
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *OpenAICompatibleClient) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	return c.doChat(ctx, model, messages, false, nil)
}

func (c *OpenAICompatibleClient) StreamChat(ctx context.Context, model string, messages []Message, onDelta func(string)) (string, error) {
	return c.StreamChatEvents(ctx, model, messages, func(delta StreamDelta) {
		if delta.Content != "" && onDelta != nil {
			onDelta(delta.Content)
		}
	})
}

func (c *OpenAICompatibleClient) StreamChatEvents(ctx context.Context, model string, messages []Message, onDelta func(StreamDelta)) (string, error) {
	return c.doChat(ctx, model, messages, true, onDelta)
}

func (c *OpenAICompatibleClient) doChat(ctx context.Context, model string, messages []Message, stream bool, onDelta func(StreamDelta)) (string, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return "", fmt.Errorf("llm api key is not configured")
	}
	timeout := c.timeout
	if override, ok := ctx.Value(requestTimeoutKey{}).(time.Duration); ok && override > 0 {
		timeout = override
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	payload, err := json.Marshal(chatRequest{
		Model:    model,
		Messages: messages,
		Stream:   stream,
	})
	if err != nil {
		return "", err
	}

	var lastErr error
	for attempt := 0; attempt < maxTransientLLMAttempts; attempt++ {
		emittedDelta := false
		attemptDelta := onDelta
		if stream && onDelta != nil {
			attemptDelta = func(delta StreamDelta) {
				if delta.Content != "" || delta.ReasoningContent != "" {
					emittedDelta = true
				}
				onDelta(delta)
			}
		}
		text, err := c.doChatOnce(ctx, payload, stream, attemptDelta)
		if err != nil {
			err = c.wrapTimeoutError(ctx, timeout, err)
			lastErr = err
			if !canRetryLLMError(err) || (stream && emittedDelta) || attempt == maxTransientLLMAttempts-1 {
				return "", err
			}
			if sleepErr := sleepBeforeLLMRetry(ctx, attempt); sleepErr != nil {
				return "", c.wrapTimeoutError(ctx, timeout, sleepErr)
			}
			continue
		}
		return text, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("llm request failed")
}

func (c *OpenAICompatibleClient) doChatOnce(ctx context.Context, payload []byte, stream bool, onDelta func(StreamDelta)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if stream {
		return c.readStreamResponse(resp, onDelta)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		var parsed chatResponse
		if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != nil {
			return "", llmAPIError(resp.StatusCode, parsed.Error.Message)
		}
		if bodyText := strings.TrimSpace(string(body)); bodyText != "" {
			return "", llmAPIStatusError(resp.StatusCode, truncateErrorBody(bodyText, 300))
		}
		return "", llmAPIStatusError(resp.StatusCode, "")
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode llm response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

type transientLLMError struct {
	err error
}

func (e transientLLMError) Error() string {
	return e.err.Error()
}

func (e transientLLMError) Unwrap() error {
	return e.err
}

func llmAPIError(statusCode int, message string) error {
	var err error
	if message != "" {
		err = fmt.Errorf("llm api error: %s", message)
	} else {
		err = fmt.Errorf("llm api status %d", statusCode)
	}
	if isTransientLLMFailure(statusCode, message) {
		return transientLLMError{err: err}
	}
	return err
}

func llmAPIStatusError(statusCode int, body string) error {
	var err error
	if body != "" {
		err = fmt.Errorf("llm api status %d: %s", statusCode, body)
	} else {
		err = fmt.Errorf("llm api status %d", statusCode)
	}
	if isTransientLLMFailure(statusCode, body) {
		return transientLLMError{err: err}
	}
	return err
}

func canRetryLLMError(err error) bool {
	var transient transientLLMError
	return errors.As(err, &transient)
}

func isTransientLLMFailure(statusCode int, message string) bool {
	switch statusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(message))
	return containsAnyLLMErrorTerm(lower,
		"service is too busy",
		"too busy",
		"temporarily",
		"temporary",
		"overloaded",
		"rate limit",
		"rate_limit",
		"try again",
		"unavailable",
		"timeout",
	)
}

func containsAnyLLMErrorTerm(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func sleepBeforeLLMRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(250*(attempt+1)*(attempt+1)) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *OpenAICompatibleClient) wrapTimeoutError(ctx context.Context, timeout time.Duration, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("llm request timeout after %s: %w", formatTimeout(timeout), err)
	}
	return err
}

func formatTimeout(timeout time.Duration) string {
	if timeout <= 0 {
		return "unknown"
	}
	if timeout%time.Second == 0 {
		return fmt.Sprintf("%ds", int(timeout/time.Second))
	}
	return timeout.String()
}

func truncateErrorBody(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		limit = 300
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func (c *OpenAICompatibleClient) readStreamResponse(resp *http.Response, onDelta func(StreamDelta)) (string, error) {
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		var parsed chatResponse
		if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != nil {
			return "", llmAPIError(resp.StatusCode, parsed.Error.Message)
		}
		if bodyText := strings.TrimSpace(string(body)); bodyText != "" {
			return "", llmAPIStatusError(resp.StatusCode, truncateErrorBody(bodyText, 300))
		}
		return "", llmAPIStatusError(resp.StatusCode, "")
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	var full strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return "", fmt.Errorf("decode llm stream chunk: %w", err)
		}
		if chunk.Error != nil {
			return "", llmAPIError(0, chunk.Error.Message)
		}
		for _, choice := range chunk.Choices {
			delta := StreamDelta{
				Content:          choice.Delta.Content,
				ReasoningContent: choice.Delta.ReasoningContent,
			}
			if delta.Content != "" {
				full.WriteString(delta.Content)
			}
			if onDelta != nil {
				onDelta(delta)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(full.String()), nil
}
