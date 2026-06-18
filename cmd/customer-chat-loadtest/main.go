package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type requestPayload struct {
	Message         string        `json:"message"`
	SessionID       string        `json:"session_id"`
	MessageID       string        `json:"message_id"`
	AnswerMessageID string        `json:"answer_message_id"`
	Entrypoint      string        `json:"entrypoint"`
	History         []interface{} `json:"history"`
}

type result struct {
	Index      int
	StatusCode int
	Duration   time.Duration
	TraceID    string
	Err        string
	BodySample string
}

func main() {
	baseURL := flag.String("url", "http://127.0.0.1:9025", "WikiOS base URL, for example http://1.2.3.4:9025")
	message := flag.String("message", "动态 IP 价格", "question sent to Customer Chat")
	total := flag.Int("n", 4, "total request count")
	concurrency := flag.Int("c", 2, "client-side concurrent workers")
	timeout := flag.Duration("timeout", 5*time.Minute, "timeout per request")
	flag.Parse()

	if *total <= 0 {
		fmt.Fprintln(os.Stderr, "-n must be greater than 0")
		os.Exit(2)
	}
	if *concurrency <= 0 {
		fmt.Fprintln(os.Stderr, "-c must be greater than 0")
		os.Exit(2)
	}

	endpoint := strings.TrimRight(*baseURL, "/") + "/api/v1/customer/chat"
	client := &http.Client{Timeout: *timeout}
	jobs := make(chan int)
	results := make(chan result, *total)
	var inFlight int64
	var maxInFlight int64

	start := time.Now()
	var wg sync.WaitGroup
	for worker := 0; worker < *concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				nowInFlight := atomic.AddInt64(&inFlight, 1)
				for {
					currentMax := atomic.LoadInt64(&maxInFlight)
					if nowInFlight <= currentMax || atomic.CompareAndSwapInt64(&maxInFlight, currentMax, nowInFlight) {
						break
					}
				}
				results <- doRequest(client, endpoint, *message, index)
				atomic.AddInt64(&inFlight, -1)
			}
		}()
	}

	for i := 1; i <= *total; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(results)

	collected := make([]result, 0, *total)
	for item := range results {
		collected = append(collected, item)
	}
	sort.Slice(collected, func(i, j int) bool {
		return collected[i].Index < collected[j].Index
	})

	var okCount int
	var errCount int
	var durations []time.Duration
	statusCounts := map[int]int{}
	for _, item := range collected {
		statusCounts[item.StatusCode]++
		if item.Err == "" && item.StatusCode >= 200 && item.StatusCode < 300 {
			okCount++
			durations = append(durations, item.Duration)
		} else {
			errCount++
		}
		trace := item.TraceID
		if trace == "" {
			trace = "-"
		}
		errText := item.Err
		if errText == "" && item.StatusCode >= 400 {
			errText = item.BodySample
		}
		if errText == "" {
			errText = "-"
		}
		fmt.Printf("#%02d status=%d duration=%s trace=%s err=%s\n", item.Index, item.StatusCode, item.Duration.Round(time.Millisecond), trace, errText)
	}

	fmt.Println()
	fmt.Printf("endpoint: %s\n", endpoint)
	fmt.Printf("message: %q\n", *message)
	fmt.Printf("client concurrency: %d\n", *concurrency)
	fmt.Printf("max observed client in-flight: %d\n", maxInFlight)
	fmt.Printf("total: %d ok: %d failed: %d wall_time: %s\n", len(collected), okCount, errCount, time.Since(start).Round(time.Millisecond))
	fmt.Printf("status_counts: %v\n", statusCounts)
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		fmt.Printf("latency min=%s p50=%s p95=%s max=%s\n",
			durations[0].Round(time.Millisecond),
			percentile(durations, 0.50).Round(time.Millisecond),
			percentile(durations, 0.95).Round(time.Millisecond),
			durations[len(durations)-1].Round(time.Millisecond),
		)
	}
}

func doRequest(client *http.Client, endpoint string, message string, index int) result {
	payload := requestPayload{
		Message:         message,
		SessionID:       fmt.Sprintf("loadtest_session_%d_%d", time.Now().Unix(), index),
		MessageID:       fmt.Sprintf("loadtest_user_%d", index),
		AnswerMessageID: fmt.Sprintf("loadtest_assistant_%d", index),
		Entrypoint:      "external",
		History:         []interface{}{},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return result{Index: index, Err: err.Error()}
	}

	ctx, cancel := context.WithTimeout(context.Background(), client.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result{Index: index, Err: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(start)
	if err != nil {
		return result{Index: index, Duration: duration, Err: err.Error()}
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	return result{
		Index:      index,
		StatusCode: resp.StatusCode,
		Duration:   duration,
		TraceID:    resp.Header.Get("X-Trace-ID"),
		BodySample: strings.TrimSpace(string(raw)),
	}
}

func percentile(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if len(values) == 1 {
		return values[0]
	}
	index := int(float64(len(values)-1) * p)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}
