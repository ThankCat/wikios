package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"wikios/internal/config"
)

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

type OpenAICompatibleClient struct {
	baseURL      string
	apiKey       string
	timeout      time.Duration
	adminTimeout time.Duration
	modelPublic  string
	modelAdmin   string
	client       *http.Client
}

func NewClient(cfg config.LLMConfig) Client {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	adminTimeout := time.Duration(cfg.AdminTimeoutSec) * time.Second
	if adminTimeout <= 0 {
		adminTimeout = 300 * time.Second
	}
	return &OpenAICompatibleClient{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:       cfg.APIKey,
		timeout:      timeout,
		adminTimeout: adminTimeout,
		modelPublic:  strings.TrimSpace(cfg.ModelPublic),
		modelAdmin:   strings.TrimSpace(cfg.ModelAdmin),
		client:       &http.Client{},
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
	timeout := c.timeoutForModel(model)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return "", c.wrapTimeoutError(ctx, timeout, err)
	}
	defer resp.Body.Close()
	if stream {
		text, err := c.readStreamResponse(resp, onDelta)
		if err != nil {
			return "", c.wrapTimeoutError(ctx, timeout, err)
		}
		return text, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", c.wrapTimeoutError(ctx, timeout, err)
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode llm response: %w", err)
	}
	if resp.StatusCode >= 400 {
		if parsed.Error != nil {
			return "", fmt.Errorf("llm api error: %s", parsed.Error.Message)
		}
		return "", fmt.Errorf("llm api status %d", resp.StatusCode)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func (c *OpenAICompatibleClient) timeoutForModel(model string) time.Duration {
	model = strings.TrimSpace(model)
	if c.modelAdmin != "" && model == c.modelAdmin && model != c.modelPublic {
		return c.adminTimeout
	}
	return c.timeout
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

func (c *OpenAICompatibleClient) readStreamResponse(resp *http.Response, onDelta func(StreamDelta)) (string, error) {
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		var parsed chatResponse
		if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error != nil {
			return "", fmt.Errorf("llm api error: %s", parsed.Error.Message)
		}
		return "", fmt.Errorf("llm api status %d", resp.StatusCode)
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
			return "", fmt.Errorf("llm api error: %s", chunk.Error.Message)
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
