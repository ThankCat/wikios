package llm

import "testing"

func TestDecodeJSONObjectSkipsInvalidBracesAndUsesBalancedObject(t *testing.T) {
	var got struct {
		Answer string `json:"answer"`
		OK     bool   `json:"ok"`
	}
	err := DecodeJSONObject(`说明里有 {不是 JSON}，最终输出 {"answer":"包含 } 的文本","ok":true} 后面还有文字`, &got)
	if err != nil {
		t.Fatalf("DecodeJSONObject: %v", err)
	}
	if got.Answer != "包含 } 的文本" || !got.OK {
		t.Fatalf("unexpected decoded object: %#v", got)
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

func TestDecodeJSONObjectHandlesMarkdownFence(t *testing.T) {
	var got struct {
		OK bool `json:"ok"`
	}
	err := DecodeJSONObject("```json\n{\"ok\":true}\n```", &got)
	if err != nil {
		t.Fatalf("DecodeJSONObject: %v", err)
	}
	if !got.OK {
		t.Fatalf("unexpected decoded object: %#v", got)
	}
}
