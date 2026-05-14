package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/store"
)

func TestDynamicLLMClientUsesActiveModelWithoutRestart(t *testing.T) {
	ctx := context.Background()
	dataStore := openServiceTestStore(t)
	defer dataStore.Close()

	requests := []string{}
	serverOne := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, requestModelName(t, r)+"|"+r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"one"}}]}`))
	}))
	defer serverOne.Close()
	serverTwo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, requestModelName(t, r)+"|"+r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"two"}}]}`))
	}))
	defer serverTwo.Close()

	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{
		ID:              "one",
		DisplayName:     "One",
		Provider:        "test",
		BaseURL:         serverOne.URL,
		ModelName:       "model-one",
		APIKey:          "key-one",
		IsActive:        true,
		TimeoutSec:      5,
		AdminTimeoutSec: 5,
	}); err != nil {
		t.Fatalf("create one: %v", err)
	}
	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{
		ID:              "two",
		DisplayName:     "Two",
		Provider:        "test",
		BaseURL:         serverTwo.URL,
		ModelName:       "model-two",
		APIKey:          "key-two",
		TimeoutSec:      5,
		AdminTimeoutSec: 5,
	}); err != nil {
		t.Fatalf("create two: %v", err)
	}

	client := NewDynamicLLMClient(dataStore, config.LLMConfig{})
	text, err := client.Chat(ctx, "ignored", []llm.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("chat one: %v", err)
	}
	if text != "one" {
		t.Fatalf("expected one, got %q", text)
	}
	if err := dataStore.ActivateLLMModel(ctx, "two"); err != nil {
		t.Fatalf("activate two: %v", err)
	}
	text, err = client.Chat(ctx, "ignored", []llm.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("chat two: %v", err)
	}
	if text != "two" {
		t.Fatalf("expected two, got %q", text)
	}
	if got := strings.Join(requests, ","); got != "model-one|Bearer key-one,model-two|Bearer key-two" {
		t.Fatalf("unexpected requests: %s", got)
	}
}

func TestDynamicLLMClientStreamsReasoningContent(t *testing.T) {
	ctx := context.Background()
	dataStore := openServiceTestStore(t)
	defer dataStore.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := requestModelName(t, r); got != "stream-model" {
			t.Fatalf("unexpected model %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"先确认。\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"完成\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{
		ID:              "stream",
		DisplayName:     "Stream",
		Provider:        "test",
		BaseURL:         server.URL,
		ModelName:       "stream-model",
		APIKey:          "stream-key",
		IsActive:        true,
		TimeoutSec:      5,
		AdminTimeoutSec: 5,
	}); err != nil {
		t.Fatalf("create stream: %v", err)
	}

	client := NewDynamicLLMClient(dataStore, config.LLMConfig{})
	events := []llm.StreamDelta{}
	text, err := client.StreamChatEvents(ctx, "ignored", []llm.Message{{Role: "user", Content: "hi"}}, func(delta llm.StreamDelta) {
		events = append(events, delta)
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text != "完成" || len(events) != 2 || events[0].ReasoningContent != "先确认。" || events[1].Content != "完成" {
		t.Fatalf("unexpected stream result text=%q events=%#v", text, events)
	}
}

func TestDynamicLLMClientAutoSwitchesUnavailableActiveModelAndPersistsFallback(t *testing.T) {
	ctx := context.Background()
	dataStore := openServiceTestStore(t)
	defer dataStore.Close()

	activeCalls := 0
	activeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeCalls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"Access denied, account overdue"}}`))
	}))
	defer activeServer.Close()
	backupRequests := []string{}
	backupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupRequests = append(backupRequests, requestModelName(t, r)+"|"+r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"backup ok"}}]}`))
	}))
	defer backupServer.Close()

	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{
		ID:              "active",
		DisplayName:     "Active",
		Provider:        "test",
		BaseURL:         activeServer.URL,
		ModelName:       "broken-model",
		APIKey:          "broken-key",
		IsActive:        true,
		TimeoutSec:      5,
		AdminTimeoutSec: 5,
	}); err != nil {
		t.Fatalf("create active: %v", err)
	}
	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{
		ID:              "backup",
		DisplayName:     "Backup",
		Provider:        "test",
		BaseURL:         backupServer.URL,
		ModelName:       "backup-model",
		APIKey:          "backup-key",
		TimeoutSec:      5,
		AdminTimeoutSec: 5,
	}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	client := NewDynamicLLMClient(dataStore, config.LLMConfig{})
	text, err := client.Chat(ctx, "ignored", []llm.Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if text != "backup ok" {
		t.Fatalf("expected backup response, got %q", text)
	}
	if activeCalls != 1 {
		t.Fatalf("expected one active attempt, got %d", activeCalls)
	}
	if got := strings.Join(backupRequests, ","); got != "backup-model|Bearer backup-key" {
		t.Fatalf("unexpected backup request: %s", got)
	}
	activeModel, err := dataStore.GetActiveLLMModel(ctx)
	if err != nil {
		t.Fatalf("get active model: %v", err)
	}
	if activeModel.ID != "backup" {
		t.Fatalf("expected backup to become active, got %s", activeModel.ID)
	}
}

func TestDynamicLLMClientReturnsAllUnavailableAfterTryingEveryServiceFailure(t *testing.T) {
	ctx := context.Background()
	dataStore := openServiceTestStore(t)
	defer dataStore.Close()

	for i, status := range []int{http.StatusForbidden, http.StatusServiceUnavailable} {
		id := fmt.Sprintf("model-%d", i)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"temporarily unavailable"}}`))
		}))
		t.Cleanup(server.Close)
		if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{
			ID:              id,
			DisplayName:     id,
			Provider:        "test",
			BaseURL:         server.URL,
			ModelName:       id,
			APIKey:          "key",
			IsActive:        i == 0,
			TimeoutSec:      5,
			AdminTimeoutSec: 5,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	client := NewDynamicLLMClient(dataStore, config.LLMConfig{})
	_, err := client.Chat(ctx, "ignored", []llm.Message{{Role: "user", Content: "hi"}})
	var allUnavailable allLLMModelsUnavailableError
	if err == nil || !errors.As(err, &allUnavailable) {
		t.Fatalf("expected all unavailable error, got %v", err)
	}
	if len(allUnavailable.failures) != 2 {
		t.Fatalf("expected two failures, got %#v", allUnavailable.failures)
	}
}

func TestDynamicLLMClientStreamSwitchesOnlyBeforeAnyDelta(t *testing.T) {
	ctx := context.Background()
	dataStore := openServiceTestStore(t)
	defer dataStore.Close()

	activeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"temporarily unavailable"}}`))
	}))
	defer activeServer.Close()
	backupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"backup\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer backupServer.Close()
	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{ID: "active", DisplayName: "Active", Provider: "test", BaseURL: activeServer.URL, ModelName: "active", APIKey: "key", IsActive: true, TimeoutSec: 5, AdminTimeoutSec: 5}); err != nil {
		t.Fatalf("create active: %v", err)
	}
	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{ID: "backup", DisplayName: "Backup", Provider: "test", BaseURL: backupServer.URL, ModelName: "backup", APIKey: "key", TimeoutSec: 5, AdminTimeoutSec: 5}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	client := NewDynamicLLMClient(dataStore, config.LLMConfig{})
	events := []llm.StreamDelta{}
	text, err := client.StreamChatEvents(ctx, "ignored", []llm.Message{{Role: "user", Content: "hi"}}, func(delta llm.StreamDelta) {
		events = append(events, delta)
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text != "backup" || len(events) != 1 || events[0].Content != "backup" {
		t.Fatalf("unexpected stream result text=%q events=%#v", text, events)
	}
	activeModel, err := dataStore.GetActiveLLMModel(ctx)
	if err != nil {
		t.Fatalf("get active model: %v", err)
	}
	if activeModel.ID != "backup" {
		t.Fatalf("expected backup active, got %s", activeModel.ID)
	}
}

func TestDynamicLLMClientStreamDoesNotSwitchAfterDelta(t *testing.T) {
	ctx := context.Background()
	dataStore := openServiceTestStore(t)
	defer dataStore.Close()

	backupCalls := 0
	activeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"temporarily unavailable\"}}\n\n"))
	}))
	defer activeServer.Close()
	backupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"backup"}}]}`))
	}))
	defer backupServer.Close()
	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{ID: "active", DisplayName: "Active", Provider: "test", BaseURL: activeServer.URL, ModelName: "active", APIKey: "key", IsActive: true, TimeoutSec: 5, AdminTimeoutSec: 5}); err != nil {
		t.Fatalf("create active: %v", err)
	}
	if err := dataStore.CreateLLMModel(ctx, &store.LLMModel{ID: "backup", DisplayName: "Backup", Provider: "test", BaseURL: backupServer.URL, ModelName: "backup", APIKey: "key", TimeoutSec: 5, AdminTimeoutSec: 5}); err != nil {
		t.Fatalf("create backup: %v", err)
	}

	client := NewDynamicLLMClient(dataStore, config.LLMConfig{})
	events := []llm.StreamDelta{}
	_, err := client.StreamChatEvents(ctx, "ignored", []llm.Message{{Role: "user", Content: "hi"}}, func(delta llm.StreamDelta) {
		events = append(events, delta)
	})
	if err == nil || !strings.Contains(err.Error(), "temporarily unavailable") {
		t.Fatalf("expected active stream error, got %v", err)
	}
	if backupCalls != 0 {
		t.Fatalf("expected no backup call after emitted delta, got %d", backupCalls)
	}
	if len(events) != 1 || events[0].Content != "partial" {
		t.Fatalf("expected one partial event, got %#v", events)
	}
}

func TestDynamicLLMClientRequiresActiveModel(t *testing.T) {
	dataStore := openServiceTestStore(t)
	defer dataStore.Close()
	client := NewDynamicLLMClient(dataStore, config.LLMConfig{})
	_, err := client.Chat(context.Background(), "ignored", []llm.Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), noActiveLLMModelMessage) {
		t.Fatalf("expected active model error, got %v", err)
	}
}

func requestModelName(t *testing.T, r *http.Request) string {
	t.Helper()
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return payload.Model
}

func openServiceTestStore(t *testing.T) *store.Store {
	t.Helper()
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return dataStore
}
