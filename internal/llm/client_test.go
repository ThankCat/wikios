package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wikios/internal/config"
)

func TestNewClientUsesRequestTimeoutOnly(t *testing.T) {
	client := NewClient(config.LLMConfig{
		APIKey:          "test-key",
		BaseURL:         "http://example.invalid",
		TimeoutSec:      5,
		AdminTimeoutSec: 30,
	}).(*OpenAICompatibleClient)
	if client.client.Timeout != 0 {
		t.Fatalf("expected http.Client.Timeout to be unset, got %s", client.client.Timeout)
	}
	if client.timeout != 5*time.Second || client.adminTimeout != 30*time.Second {
		t.Fatalf("unexpected request timeouts: default=%s admin=%s", client.timeout, client.adminTimeout)
	}
}

func TestStreamChatIgnoresReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"我应该先思考内部步骤。\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"action\\\":\\\"final\\\",\\\"reply\\\":\\\"完成\\\"}\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		TimeoutSec: 5,
	})
	deltas := []string{}
	text, err := client.StreamChat(context.Background(), "test-model", []Message{{Role: "user", Content: "hi"}}, func(delta string) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatalf("stream chat: %v", err)
	}
	if strings.Contains(text, "思考") || strings.Contains(strings.Join(deltas, ""), "思考") {
		t.Fatalf("reasoning content leaked: text=%q deltas=%q", text, deltas)
	}
	if text != `{"action":"final","reply":"完成"}` {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(deltas) != 1 || deltas[0] != text {
		t.Fatalf("unexpected deltas: %#v", deltas)
	}
}

func TestStreamChatEventsSeparatesReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"先查规则。\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"正式回答\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		TimeoutSec: 5,
	}).(*OpenAICompatibleClient)
	events := []StreamDelta{}
	text, err := client.StreamChatEvents(context.Background(), "test-model", []Message{{Role: "user", Content: "hi"}}, func(delta StreamDelta) {
		events = append(events, delta)
	})
	if err != nil {
		t.Fatalf("stream chat events: %v", err)
	}
	if text != "正式回答" {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(events) != 2 || events[0].ReasoningContent != "先查规则。" || events[1].Content != "正式回答" {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestStreamChatEventsReturnsClearRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		time.Sleep(1500 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"late\"}}]}\n\n"))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		TimeoutSec: 10,
	}).(*OpenAICompatibleClient)
	_, err := client.StreamChatEvents(WithRequestTimeout(context.Background(), time.Second), "test-model", []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatalf("expected request timeout")
	}
	if !strings.Contains(err.Error(), "llm request timeout after 1s") {
		t.Fatalf("expected clear timeout error, got %v", err)
	}
}
