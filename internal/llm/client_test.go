package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wikios/internal/config"
)

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
