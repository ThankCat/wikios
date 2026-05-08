package service

import (
	"strconv"
	"strings"
	"time"
)

type publicAnswerStream struct {
	emitter   StreamEmitter
	extractor *jsonStringFieldExtractor
	emitted   strings.Builder
}

func newPublicAnswerStream(emitter StreamEmitter) *publicAnswerStream {
	stream := &publicAnswerStream{emitter: emitter}
	stream.extractor = newJSONStringFieldExtractor("answer_markdown", stream.emitAnswerDelta)
	return stream
}

func (s *publicAnswerStream) emit(eventType string, data any) {
	if s == nil || s.emitter == nil {
		return
	}
	s.emitter.Emit(StreamEvent{Type: eventType, Data: data})
}

func (s *publicAnswerStream) feedLLMContent(delta string) {
	if s == nil || s.extractor == nil || delta == "" {
		return
	}
	s.extractor.Feed(delta)
}

func (s *publicAnswerStream) emitAnswerDelta(delta string) {
	if s == nil || delta == "" {
		return
	}
	s.emitted.WriteString(delta)
	s.emit("delta", map[string]any{
		"delta":      delta,
		"created_at": time.Now().Format(time.RFC3339Nano),
	})
}

func (s *publicAnswerStream) emitReasoning(message string) {
	message = strings.TrimSpace(message)
	if s == nil || message == "" {
		return
	}
	s.emit("llm_reasoning_delta", map[string]any{
		"name":       "public answer trace",
		"delta":      message + "\n",
		"created_at": time.Now().Format(time.RFC3339Nano),
	})
}

func (s *publicAnswerStream) emitStep(name string, output map[string]any) {
	name = strings.TrimSpace(name)
	if s == nil || name == "" {
		return
	}
	now := time.Now()
	s.emit("step_finish", Step{
		Name:      name,
		Tool:      "public.answer",
		Status:    "SUCCESS",
		Output:    output,
		StartedAt: now,
		EndedAt:   now,
	})
}

type jsonStringFieldExtractor struct {
	target        string
	window        string
	state         jsonStringFieldState
	escape        bool
	unicodeEscape string
	done          bool
	onValue       func(string)
}

type jsonStringFieldState int

const (
	jsonStringFieldSearch jsonStringFieldState = iota
	jsonStringFieldAfterKey
	jsonStringFieldWaitValue
	jsonStringFieldInValue
)

func newJSONStringFieldExtractor(field string, onValue func(string)) *jsonStringFieldExtractor {
	return &jsonStringFieldExtractor{
		target:  `"` + field + `"`,
		onValue: onValue,
	}
}

func (e *jsonStringFieldExtractor) Feed(delta string) {
	if e == nil || e.done || delta == "" {
		return
	}
	for _, r := range delta {
		e.feedRune(r)
		if e.done {
			return
		}
	}
}

func (e *jsonStringFieldExtractor) feedRune(r rune) {
	switch e.state {
	case jsonStringFieldSearch:
		e.window += string(r)
		if len([]rune(e.window)) > len([]rune(e.target))+8 {
			windowRunes := []rune(e.window)
			e.window = string(windowRunes[len(windowRunes)-len([]rune(e.target))-8:])
		}
		if strings.Contains(e.window, e.target) {
			e.window = ""
			e.state = jsonStringFieldAfterKey
		}
	case jsonStringFieldAfterKey:
		if r == ':' {
			e.state = jsonStringFieldWaitValue
			return
		}
		if !isJSONWhitespace(r) {
			e.state = jsonStringFieldSearch
			e.window = string(r)
		}
	case jsonStringFieldWaitValue:
		if r == '"' {
			e.state = jsonStringFieldInValue
			return
		}
		if !isJSONWhitespace(r) {
			e.done = true
		}
	case jsonStringFieldInValue:
		e.feedValueRune(r)
	}
}

func (e *jsonStringFieldExtractor) feedValueRune(r rune) {
	if e.unicodeEscape != "" {
		e.unicodeEscape += string(r)
		if len(e.unicodeEscape) < 5 {
			return
		}
		raw := e.unicodeEscape[1:]
		e.unicodeEscape = ""
		value, err := strconv.ParseInt(raw, 16, 32)
		if err == nil {
			e.emit(string(rune(value)))
		}
		e.escape = false
		return
	}
	if e.escape {
		switch r {
		case '"', '\\', '/':
			e.emit(string(r))
		case 'b':
			e.emit("\b")
		case 'f':
			e.emit("\f")
		case 'n':
			e.emit("\n")
		case 'r':
			e.emit("\r")
		case 't':
			e.emit("\t")
		case 'u':
			e.unicodeEscape = "u"
			return
		default:
			e.emit(string(r))
		}
		e.escape = false
		return
	}
	if r == '\\' {
		e.escape = true
		return
	}
	if r == '"' {
		e.done = true
		return
	}
	e.emit(string(r))
}

func (e *jsonStringFieldExtractor) emit(text string) {
	if text == "" || e.onValue == nil {
		return
	}
	e.onValue(text)
}

func isJSONWhitespace(r rune) bool {
	return r == ' ' || r == '\n' || r == '\r' || r == '\t'
}
