package retrieval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// QMDHTTPClient talks to a long-running `qmd mcp --http` server over the MCP
// (JSON-RPC) HTTP transport. Keeping a single persistent daemon means the local
// embedding/rerank models stay warm, turning a ~15s cold CLI spawn into a
// sub-second query.
type QMDHTTPClient struct {
	url        string
	httpClient *http.Client

	// rerank controls the daemon's LLM reranker. When false, results are ordered
	// by lexical+vector score only, skipping the dominant ~8s cold-query cost.
	rerank bool
	// rerankCandidates caps how many candidates get reranked (candidateLimit).
	// Zero means use the daemon default. Ignored when rerank is false.
	rerankCandidates int

	mu        sync.Mutex
	sessionID string
	queryMu   sync.Mutex
}

const defaultQMDHTTPURL = "http://localhost:8181/mcp"

// NewQMDHTTPClient builds a client that reranks with the daemon defaults.
func NewQMDHTTPClient(url string, timeout time.Duration) *QMDHTTPClient {
	return NewQMDHTTPClientWithRerank(url, timeout, true, 0)
}

// NewQMDHTTPClientWithRerank builds a client with explicit rerank settings.
// rerankCandidates <= 0 leaves the daemon default in place.
func NewQMDHTTPClientWithRerank(url string, timeout time.Duration, rerank bool, rerankCandidates int) *QMDHTTPClient {
	if strings.TrimSpace(url) == "" {
		url = defaultQMDHTTPURL
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if rerankCandidates < 0 {
		rerankCandidates = 0
	}
	return &QMDHTTPClient{
		url:              url,
		httpClient:       &http.Client{Timeout: timeout},
		rerank:           rerank,
		rerankCandidates: rerankCandidates,
	}
}

type jsonRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type qmdQueryResult struct {
	StructuredContent struct {
		Results []struct {
			File  string  `json:"file"`
			Score float64 `json:"score"`
		} `json:"results"`
	} `json:"structuredContent"`
}

// Query runs a hybrid lex+vec search against the warm daemon and returns the
// ranked pages. The daemon's `query` tool expects explicit typed sub-queries
// (it does not run the CLI's LLM query-expansion), which is both faster and, in
// practice, surfaces synthesized knowledge pages more reliably.
func (c *QMDHTTPClient) Query(ctx context.Context, question string, topK int) ([]RetrievedPage, error) {
	c.queryMu.Lock()
	defer c.queryMu.Unlock()
	pages, err := c.queryOnce(ctx, question, topK)
	if err != nil {
		// A stale session is the most common transient failure; drop it and retry once.
		c.resetSession()
		pages, err = c.queryOnce(ctx, question, topK)
	}
	return pages, err
}

func (c *QMDHTTPClient) queryOnce(ctx context.Context, question string, topK int) ([]RetrievedPage, error) {
	sessionID, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	limit := topK
	if limit <= 0 {
		limit = 10
	}
	args := map[string]any{
		"searches": []map[string]string{
			{"type": "lex", "query": question},
			{"type": "vec", "query": question},
		},
		"intent": question,
		"limit":  limit,
		"rerank": c.rerank,
	}
	if c.rerank && c.rerankCandidates > 0 {
		args["candidateLimit"] = c.rerankCandidates
	}
	params := map[string]any{"name": "query", "arguments": args}
	result, err := c.call(ctx, sessionID, "tools/call", params)
	if err != nil {
		return nil, err
	}
	var parsed qmdQueryResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, fmt.Errorf("qmd http: decode query result: %w", err)
	}
	pages := make([]RetrievedPage, 0, len(parsed.StructuredContent.Results))
	for _, item := range parsed.StructuredContent.Results {
		path := normalizeRetrievedPath(item.File)
		if path == "" {
			continue
		}
		pages = append(pages, RetrievedPage{Path: path, Score: item.Score})
	}
	return pages, nil
}

func (c *QMDHTTPClient) ensureSession(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sessionID != "" {
		return c.sessionID, nil
	}
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "wikios", "version": "1"},
	}
	sessionID, _, err := c.post(ctx, "", "initialize", initParams, true)
	if err != nil {
		return "", err
	}
	if sessionID == "" {
		return "", fmt.Errorf("qmd http: server did not return a session id")
	}
	// Best-effort initialized notification; ignore errors so a strict server
	// that omits the ack does not break retrieval.
	_, _, _ = c.post(ctx, sessionID, "notifications/initialized", nil, false)
	c.sessionID = sessionID
	return sessionID, nil
}

func (c *QMDHTTPClient) resetSession() {
	c.mu.Lock()
	c.sessionID = ""
	c.mu.Unlock()
}

func (c *QMDHTTPClient) call(ctx context.Context, sessionID, method string, params any) (json.RawMessage, error) {
	_, result, err := c.post(ctx, sessionID, method, params, false)
	return result, err
}

// post sends a single JSON-RPC request. When isInit is true it returns the
// mcp-session-id response header. Notifications (params method without an id)
// are sent when method starts with "notifications/".
func (c *QMDHTTPClient) post(ctx context.Context, sessionID, method string, params any, isInit bool) (string, json.RawMessage, error) {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if !strings.HasPrefix(method, "notifications/") {
		payload["id"] = 1
	}
	if params != nil {
		payload["params"] = params
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		req.Header.Set("mcp-session-id", sessionID)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	respSession := resp.Header.Get("mcp-session-id")
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respSession, nil, fmt.Errorf("qmd http: %s returned status %d", method, resp.StatusCode)
	}
	if isInit || !strings.HasPrefix(method, "notifications/") {
		raw, err := decodeRPCBody(resp)
		if err != nil {
			return respSession, nil, err
		}
		return respSession, raw, nil
	}
	return respSession, nil, nil
}

// decodeRPCBody reads a JSON-RPC response that may arrive either as plain JSON
// or as a Server-Sent Events stream (MCP StreamableHTTP transport).
func decodeRPCBody(resp *http.Response) (json.RawMessage, error) {
	contentType := resp.Header.Get("Content-Type")
	var raw []byte
	if strings.Contains(contentType, "text/event-stream") {
		data, err := readSSEData(resp)
		if err != nil {
			return nil, err
		}
		raw = data
	} else {
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			return nil, err
		}
		raw = buf.Bytes()
	}
	var rpc jsonRPCResponse
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return nil, fmt.Errorf("qmd http: decode response: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("qmd http: rpc error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	return rpc.Result, nil
}

func readSSEData(resp *http.Response) ([]byte, error) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var data bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}
