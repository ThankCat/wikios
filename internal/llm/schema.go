package llm

import (
	"fmt"
	"os"
)

func LoadPrompt(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return string(raw), nil
}
