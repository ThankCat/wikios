package wikiadapter

import (
	"fmt"
	"strings"
)

type PatchOp struct {
	Type    string         `json:"type"`
	Section string         `json:"section,omitempty"`
	Target  string         `json:"target,omitempty"`
	Content string         `json:"content,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

func ApplyPatch(text string, ops []PatchOp) (string, error) {
	doc, err := ParseDocument(text)
	if err != nil {
		return "", err
	}
	body := doc.Body
	for _, op := range ops {
		switch op.Type {
		case "append_section":
			body, err = appendSection(body, op.Section, op.Content)
		case "replace_section":
			body, err = replaceSection(body, op.Section, op.Content)
		case "insert_after":
			body, err = insertAfter(body, op.Target, op.Content)
		case "update_frontmatter":
			if len(op.Fields) == 0 {
				err = fmt.Errorf("update_frontmatter fields are required")
				break
			}
			for key, value := range op.Fields {
				doc.Frontmatter[key] = value
			}
		default:
			err = fmt.Errorf("unsupported patch type %q", op.Type)
		}
		if err != nil {
			return "", err
		}
	}
	doc.Body = body
	return RenderDocument(doc), nil
}

func appendSection(body string, heading string, content string) (string, error) {
	start, end, err := findSection(body, heading)
	if err != nil {
		return "", err
	}
	section := strings.TrimRight(body[start:end], "\n")
	section += "\n" + strings.TrimSpace(content) + "\n"
	return body[:start] + section + body[end:], nil
}

func replaceSection(body string, heading string, content string) (string, error) {
	start, end, err := findSection(body, heading)
	if err != nil {
		return "", err
	}
	lines := strings.Split(body[start:end], "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("empty section")
	}
	replaced := strings.TrimRight(lines[0], "\n") + "\n\n" + strings.TrimSpace(content) + "\n"
	return body[:start] + replaced + body[end:], nil
}

func insertAfter(body string, target string, content string) (string, error) {
	needle := strings.TrimSpace(target)
	idx := strings.Index(body, needle)
	if idx == -1 {
		return "", fmt.Errorf("target heading not found: %s", target)
	}
	insertAt := idx + len(needle)
	return body[:insertAt] + "\n\n" + strings.TrimSpace(content) + body[insertAt:], nil
}

func findSection(body string, heading string) (int, int, error) {
	needle := strings.TrimSpace(heading)
	idx := strings.Index(body, needle)
	if idx == -1 {
		return 0, 0, fmt.Errorf("section not found: %s", heading)
	}
	next := strings.Index(body[idx+len(needle):], "\n## ")
	if next == -1 {
		return idx, len(body), nil
	}
	return idx, idx + len(needle) + next + 1, nil
}
