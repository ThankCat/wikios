package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientUsesRequestTimeoutOnly(t *testing.T) {
	client := NewClient(ClientConfig{
		APIKey:     "test-key",
		BaseURL:    "http://example.invalid",
		TimeoutSec: 5,
	}).(*OpenAICompatibleClient)
	if client.client.Timeout != 0 {
		t.Fatalf("expected http.Client.Timeout to be unset, got %s", client.client.Timeout)
	}
	if client.timeout != 5*time.Second {
		t.Fatalf("unexpected request timeout: %s", client.timeout)
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

	client := NewClient(ClientConfig{
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

	client := NewClient(ClientConfig{
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

	client := NewClient(ClientConfig{
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

func TestStreamChatEventsRetriesTransientBusyBeforeDeltas(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"Service is too busy. Please try again."}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		TimeoutSec: 5,
	})
	deltas := []StreamDelta{}
	text, err := client.(EventStreamClient).StreamChatEvents(context.Background(), "test-model", []Message{{Role: "user", Content: "hi"}}, func(delta StreamDelta) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatalf("StreamChatEvents: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if text != "ok" || len(deltas) != 1 || deltas[0].Content != "ok" {
		t.Fatalf("unexpected retry result text=%q deltas=%#v", text, deltas)
	}
}

func TestStreamChatEventsDoesNotRetryAfterDelta(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"Service is too busy.\"}}\n\n"))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		TimeoutSec: 5,
	})
	_, err := client.(EventStreamClient).StreamChatEvents(context.Background(), "test-model", []Message{{Role: "user", Content: "hi"}}, func(StreamDelta) {})
	if err == nil {
		t.Fatalf("expected stream error")
	}
	if attempts != 1 {
		t.Fatalf("must not retry after emitting a delta, got %d attempts", attempts)
	}
}

func TestChatReturnsHTTPStatusForNonJSONError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream overloaded", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		APIKey:     "test-key",
		BaseURL:    server.URL,
		TimeoutSec: 5,
	})
	_, err := client.Chat(context.Background(), "test-model", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatalf("expected http status error")
	}
	if !strings.Contains(err.Error(), "llm api status 502") || !strings.Contains(err.Error(), "upstream overloaded") {
		t.Fatalf("expected status and response body, got %v", err)
	}
}
