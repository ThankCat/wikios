package service

import (
	"math"
	"strings"

	tiktoken "github.com/pkoukk/tiktoken-go"

	"wikios/internal/config"
	"wikios/internal/llm"
)

type ContextCounter struct {
	maxTokens     int
	reserveTokens int
	method        string
	tokenizerName string
	encoding      *tiktoken.Tiktoken
	estimated     bool
	initError     string
}

type ContextUsage struct {
	UsedTokens      int    `json:"used_tokens"`
	RemainingTokens int    `json:"remaining_tokens"`
	MaxTokens       int    `json:"max_tokens"`
	ReserveTokens   int    `json:"reserve_tokens"`
	Blocked         bool   `json:"blocked"`
	Estimated       bool   `json:"estimated"`
	Counter         string `json:"counter"`
	Tokenizer       string `json:"tokenizer,omitempty"`
	Error           string `json:"error,omitempty"`
}

func NewContextCounter(cfg config.ContextConfig) *ContextCounter {
	counter := &ContextCounter{
		maxTokens:     cfg.MaxTokens,
		reserveTokens: cfg.ReserveTokens,
		method:        strings.ToLower(strings.TrimSpace(cfg.Counter)),
		tokenizerName: strings.TrimSpace(cfg.Tokenizer),
	}
	if counter.maxTokens <= 0 {
		counter.maxTokens = 1000000
	}
	if counter.reserveTokens < 0 {
		counter.reserveTokens = 0
	}
	if counter.reserveTokens >= counter.maxTokens {
		counter.reserveTokens = int(math.Max(1, float64(counter.maxTokens/10)))
	}
	if counter.method == "" {
		counter.method = "tokenizer"
	}
	if counter.tokenizerName == "" {
		counter.tokenizerName = "cl100k_base"
	}
	if counter.method == "tokenizer" {
		encoding, err := tiktoken.GetEncoding(counter.tokenizerName)
		if err != nil {
			counter.estimated = true
			counter.initError = err.Error()
			counter.method = "estimate"
		} else {
			counter.encoding = encoding
		}
	} else {
		counter.estimated = true
	}
	return counter
}

func (c *ContextCounter) CountMessages(messages []llm.Message) ContextUsage {
	used := 0
	for _, message := range messages {
		used += c.countText(message.Role)
		used += c.countText(message.Content)
		used += 4
	}
	used += 2
	remaining := c.maxTokens - used
	if remaining < 0 {
		remaining = 0
	}
	limit := c.maxTokens - c.reserveTokens
	if limit < 1 {
		limit = c.maxTokens
	}
	return ContextUsage{
		UsedTokens:      used,
		RemainingTokens: remaining,
		MaxTokens:       c.maxTokens,
		ReserveTokens:   c.reserveTokens,
		Blocked:         used > limit,
		Estimated:       c.estimated,
		Counter:         c.method,
		Tokenizer:       c.tokenizerName,
		Error:           c.initError,
	}
}

func (c *ContextCounter) countText(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	if c.encoding != nil {
		return len(c.encoding.Encode(text, nil, nil))
	}
	runes := []rune(text)
	// Conservative multilingual fallback: Chinese text is often close to one token
	// per rune, while ASCII tends to be closer to four chars per token.
	ascii := 0
	for _, r := range runes {
		if r < 128 {
			ascii++
		}
	}
	nonASCII := len(runes) - ascii
	return nonASCII + int(math.Ceil(float64(ascii)/4.0))
}
