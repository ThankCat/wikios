package wikiadapter

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Document struct {
	Frontmatter map[string]any
	Body        string
}

func ParseDocument(text string) (*Document, error) {
	doc := &Document{
		Frontmatter: map[string]any{},
		Body:        text,
	}
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return doc, nil
	}
	lines := strings.Split(text, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, fmt.Errorf("unterminated frontmatter")
	}
	fm := make(map[string]any)
	var currentList string
	for _, raw := range lines[1:end] {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if currentList != "" && strings.HasPrefix(strings.TrimSpace(line), "- ") {
			fm[currentList] = append(anySlice(fm[currentList]), parseScalar(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))))
			continue
		}
		currentList = ""
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid frontmatter line: %q", line)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if value == "" {
			fm[key] = []any{}
			currentList = key
			continue
		}
		fm[key] = parseScalar(value)
	}
	doc.Frontmatter = fm
	doc.Body = strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
	return doc, nil
}

func RenderDocument(doc *Document) string {
	var b strings.Builder
	b.WriteString("---\n")
	keys := sortedKeys(doc.Frontmatter)
	for _, key := range keys {
		writeFrontmatterLine(&b, key, doc.Frontmatter[key])
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimLeft(doc.Body, "\n"))
	if !strings.HasSuffix(doc.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func anySlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	if s, ok := v.([]string); ok {
		out := make([]any, 0, len(s))
		for _, item := range s {
			out = append(out, item)
		}
		return out
	}
	return []any{v}
}

func parseScalar(text string) any {
	if text == "true" {
		return true
	}
	if text == "false" {
		return false
	}
	if text == "null" || text == "~" {
		return nil
	}
	if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "["), "]"))
		if inner == "" {
			return []any{}
		}
		parts := strings.Split(inner, ",")
		out := make([]any, 0, len(parts))
		for _, part := range parts {
			out = append(out, parseScalar(strings.TrimSpace(part)))
		}
		return out
	}
	if unquoted, ok := trimQuotes(text); ok {
		return unquoted
	}
	if i, err := strconv.Atoi(text); err == nil {
		return i
	}
	return text
}

func trimQuotes(text string) (string, bool) {
	if len(text) >= 2 && ((text[0] == '"' && text[len(text)-1] == '"') || (text[0] == '\'' && text[len(text)-1] == '\'')) {
		return text[1 : len(text)-1], true
	}
	return "", false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeFrontmatterLine(b *strings.Builder, key string, value any) {
	switch typed := value.(type) {
	case []string:
		b.WriteString(key + ":\n")
		for _, item := range typed {
			b.WriteString("  - " + quoteIfNeeded(item) + "\n")
		}
	case []any:
		b.WriteString(key + ":\n")
		for _, item := range typed {
			b.WriteString("  - " + renderScalar(item) + "\n")
		}
	case nil:
		b.WriteString(key + ": null\n")
	default:
		b.WriteString(key + ": " + renderScalar(value) + "\n")
	}
}

func renderScalar(value any) string {
	switch typed := value.(type) {
	case string:
		return quoteIfNeeded(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return quoteIfNeeded(fmt.Sprintf("%v", value))
	}
}

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#[]{}") || strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return strconv.Quote(s)
	}
	return s
}
