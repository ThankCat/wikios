package llm

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func LoadPrompt(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return string(raw), nil
}

func DecodeJSONObject[T any](text string, out *T) error {
	candidates := extractJSONObjectCandidates(text)
	if len(candidates) == 0 {
		return fmt.Errorf("no json object found in llm response")
	}
	var lastErr error
	for _, candidate := range candidates {
		if err := json.Unmarshal([]byte(candidate), out); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("decode json object: %w", lastErr)
	}
	return fmt.Errorf("decode json object: no valid candidate")
}

func extractJSONObjectCandidates(text string) []string {
	trimmed := stripMarkdownFence(strings.TrimSpace(text))
	if trimmed == "" {
		return nil
	}

	candidates := make([]string, 0, 1)
	for start := strings.Index(trimmed, "{"); start >= 0 && start < len(trimmed); {
		if end, ok := balancedJSONObjectEnd(trimmed[start:]); ok {
			candidate := trimmed[start : start+end]
			if json.Valid([]byte(candidate)) {
				candidates = append(candidates, candidate)
			}
			start = start + end
		} else {
			start++
		}
		next := strings.Index(trimmed[start:], "{")
		if next < 0 {
			break
		}
		start += next
	}
	return candidates
}

func stripMarkdownFence(trimmed string) string {
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 3 {
			lines = lines[1:]
			if strings.TrimSpace(lines[len(lines)-1]) == "```" {
				lines = lines[:len(lines)-1]
			}
			trimmed = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}
	return trimmed
}

func balancedJSONObjectEnd(text string) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for idx, r := range text {
		if idx == 0 && r != '{' {
			return 0, false
		}
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch r {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return idx + len(string(r)), true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}
