package llm

import "testing"

func TestExtractJSONObjectSkipsInvalidBracesAndUsesBalancedObject(t *testing.T) {
	got, err := ExtractJSONObject(`说明里有 {不是 JSON}，最终输出 {"answer":"包含 } 的文本","ok":true} 后面还有文字`)
	if err != nil {
		t.Fatalf("ExtractJSONObject: %v", err)
	}
	if got != `{"answer":"包含 } 的文本","ok":true}` {
		t.Fatalf("unexpected json object: %q", got)
	}
}

func TestDecodeJSONObjectSkipsEarlierInvalidObjectCandidate(t *testing.T) {
	var out struct {
		Action string `json:"action"`
		Reply  string `json:"reply"`
	}
	err := DecodeJSONObject(`先解释 {"action":bad} 再给结果 {"action":"final","reply":"完成"}`, &out)
	if err != nil {
		t.Fatalf("DecodeJSONObject: %v", err)
	}
	if out.Action != "final" || out.Reply != "完成" {
		t.Fatalf("unexpected decoded object: %#v", out)
	}
}

func TestExtractJSONObjectHandlesMarkdownFence(t *testing.T) {
	got, err := ExtractJSONObject("```json\n{\"ok\":true}\n```")
	if err != nil {
		t.Fatalf("ExtractJSONObject: %v", err)
	}
	if got != `{"ok":true}` {
		t.Fatalf("unexpected fenced json object: %q", got)
	}
}
