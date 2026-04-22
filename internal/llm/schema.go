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
	candidate, err := ExtractJSONObject(text)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(candidate), out); err != nil {
		return fmt.Errorf("decode json object: %w", err)
	}
	return nil
}

func ExtractJSONObject(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", fmt.Errorf("empty llm response")
	}
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
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start == -1 || end == -1 || end < start {
		return "", fmt.Errorf("no json object found in llm response")
	}
	return trimmed[start : end+1], nil
}
