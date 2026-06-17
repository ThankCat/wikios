package retrieval

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestQMDHTTPClientLive exercises the real Go client against a running
// `qmd mcp --http` daemon. Gated behind QMD_HTTP_LIVE so it never runs in CI.
//
//	QMD_HTTP_LIVE=1 go test ./internal/retrieval/ -run Live -v
func TestQMDHTTPClientLive(t *testing.T) {
	if os.Getenv("QMD_HTTP_LIVE") == "" {
		t.Skip("set QMD_HTTP_LIVE=1 with a running qmd mcp --http daemon to run")
	}
	url := os.Getenv("QMD_HTTP_URL")
	if url == "" {
		url = defaultQMDHTTPURL
	}
	client := NewQMDHTTPClient(url, 60*time.Second)
	q := "四叶天 静态 IP 切换 IP 方法 控制台操作"

	start := time.Now()
	pages, err := client.Query(context.Background(), q, 6)
	cold := time.Since(start)
	if err != nil {
		t.Fatalf("live query failed: %v", err)
	}
	start = time.Now()
	if _, err := client.Query(context.Background(), q, 6); err != nil {
		t.Fatalf("warm query failed: %v", err)
	}
	warm := time.Since(start)
	t.Logf("live qmd http query: cold=%s warm=%s results=%d", cold, warm, len(pages))
	for _, p := range pages {
		t.Logf("  %.2f  %s", p.Score, p.Path)
	}
}

func TestQMDHTTPClientQueryParsesResults(t *testing.T) {
	var sawInit, sawInitialized, sawQuery bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string `json:"name"`
				Arguments struct {
					Limit          int    `json:"limit"`
					Intent         string `json:"intent"`
					Rerank         *bool  `json:"rerank"`
					CandidateLimit *int   `json:"candidateLimit"`
					Searches       []struct {
						Type  string `json:"type"`
						Query string `json:"query"`
					} `json:"searches"`
				} `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		switch req.Method {
		case "initialize":
			sawInit = true
			w.Header().Set("mcp-session-id", "sess-123")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05"}}`))
		case "notifications/initialized":
			sawInitialized = true
			if r.Header.Get("mcp-session-id") != "sess-123" {
				t.Errorf("expected session header on initialized")
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			sawQuery = true
			if req.Params.Name != "query" {
				t.Errorf("expected query tool, got %q", req.Params.Name)
			}
			if r.Header.Get("mcp-session-id") != "sess-123" {
				t.Errorf("expected session header on query")
			}
			if len(req.Params.Arguments.Searches) != 2 {
				t.Errorf("expected lex+vec searches, got %d", len(req.Params.Arguments.Searches))
			}
			if req.Params.Arguments.Limit != 3 {
				t.Errorf("expected limit 3, got %d", req.Params.Arguments.Limit)
			}
			if req.Params.Arguments.Rerank == nil || !*req.Params.Arguments.Rerank {
				t.Errorf("expected default client to request rerank=true, got %v", req.Params.Arguments.Rerank)
			}
			if req.Params.Arguments.CandidateLimit != nil {
				t.Errorf("expected no candidateLimit by default, got %v", *req.Params.Arguments.CandidateLimit)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"results":[
				{"file":"qmd://wiki/knowledge/si-ye-tian-static-ip.md","score":0.91},
				{"file":"wiki/concepts/static-ip.md","score":0.55}
			]}}}`))
		default:
			t.Errorf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	client := NewQMDHTTPClient(server.URL, 5*time.Second)
	pages, err := client.Query(context.Background(), "静态IP 切换", 3)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if !sawInit || !sawInitialized || !sawQuery {
		t.Fatalf("handshake incomplete: init=%v initialized=%v query=%v", sawInit, sawInitialized, sawQuery)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %+v", pages)
	}
	if pages[0].Path != "wiki/knowledge/si-ye-tian-static-ip.md" {
		t.Fatalf("expected normalized qmd:// path, got %q", pages[0].Path)
	}
	if pages[0].Score != 0.91 {
		t.Fatalf("expected score 0.91, got %v", pages[0].Score)
	}
}

func TestQMDHTTPClientRerankOptions(t *testing.T) {
	type capturedArgs struct {
		Rerank         *bool `json:"rerank"`
		CandidateLimit *int  `json:"candidateLimit"`
	}
	newServer := func(capture *capturedArgs) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "initialize") {
				w.Header().Set("mcp-session-id", "s1")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
				return
			}
			if strings.Contains(string(body), "tools/call") {
				var req struct {
					Params struct {
						Arguments capturedArgs `json:"arguments"`
					} `json:"params"`
				}
				_ = json.Unmarshal(body, &req)
				*capture = req.Params.Arguments
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"results":[{"file":"wiki/a.md","score":0.7}]}}}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
		}))
	}

	t.Run("rerank disabled omits candidateLimit", func(t *testing.T) {
		var got capturedArgs
		server := newServer(&got)
		defer server.Close()
		client := NewQMDHTTPClientWithRerank(server.URL, 5*time.Second, false, 8)
		if _, err := client.Query(context.Background(), "q", 5); err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if got.Rerank == nil || *got.Rerank {
			t.Fatalf("expected rerank=false, got %v", got.Rerank)
		}
		if got.CandidateLimit != nil {
			t.Fatalf("expected no candidateLimit when rerank disabled, got %v", *got.CandidateLimit)
		}
	})

	t.Run("rerank with candidate cap", func(t *testing.T) {
		var got capturedArgs
		server := newServer(&got)
		defer server.Close()
		client := NewQMDHTTPClientWithRerank(server.URL, 5*time.Second, true, 8)
		if _, err := client.Query(context.Background(), "q", 5); err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if got.Rerank == nil || !*got.Rerank {
			t.Fatalf("expected rerank=true, got %v", got.Rerank)
		}
		if got.CandidateLimit == nil || *got.CandidateLimit != 8 {
			t.Fatalf("expected candidateLimit=8, got %v", got.CandidateLimit)
		}
	})
}

func TestQMDHTTPClientHandlesSSEResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "initialize") {
			w.Header().Set("mcp-session-id", "s1")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
			return
		}
		if strings.Contains(string(body), "tools/call") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"structuredContent\":{\"results\":[{\"file\":\"wiki/a.md\",\"score\":0.7}]}}}\n\n"))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client := NewQMDHTTPClient(server.URL, 5*time.Second)
	pages, err := client.Query(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(pages) != 1 || pages[0].Path != "wiki/a.md" {
		t.Fatalf("expected sse-parsed result, got %+v", pages)
	}
}

func TestQMDHTTPClientRetriesAfterSessionFailure(t *testing.T) {
	var queryCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.Contains(string(body), "initialize"):
			w.Header().Set("mcp-session-id", "live")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
		case strings.Contains(string(body), "tools/call"):
			queryCalls++
			if queryCalls == 1 {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"structuredContent":{"results":[{"file":"wiki/b.md","score":0.5}]}}}`))
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer server.Close()

	client := NewQMDHTTPClient(server.URL, 5*time.Second)
	pages, err := client.Query(context.Background(), "q", 5)
	if err != nil {
		t.Fatalf("query failed after retry: %v", err)
	}
	if queryCalls != 2 {
		t.Fatalf("expected 2 query attempts, got %d", queryCalls)
	}
	if len(pages) != 1 || pages[0].Path != "wiki/b.md" {
		t.Fatalf("expected recovered result, got %+v", pages)
	}
}
