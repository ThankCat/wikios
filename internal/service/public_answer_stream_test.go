package service

import (
	"strings"
	"testing"
)

func TestJSONStringFieldExtractorStreamsAnswerMarkdown(t *testing.T) {
	var out strings.Builder
	extractor := newJSONStringFieldExtractor("answer_markdown", func(delta string) {
		out.WriteString(delta)
	})

	for _, chunk := range []string{
		`{"answer_mode":"evidence",`,
		`"answer_markdown":"静态 IP\n适合白名单绑定，`,
		`也适合远程办公。",`,
		`"confidence":0.9}`,
	} {
		extractor.Feed(chunk)
	}

	if got := out.String(); got != "静态 IP\n适合白名单绑定，也适合远程办公。" {
		t.Fatalf("unexpected extracted answer: %q", got)
	}
}

func TestJSONStringFieldExtractorDecodesEscapedUnicode(t *testing.T) {
	var out strings.Builder
	extractor := newJSONStringFieldExtractor("answer_markdown", func(delta string) {
		out.WriteString(delta)
	})

	extractor.Feed(`{"answer_markdown":"\u9759\u6001 IP \"稳定\""}`)

	if got := out.String(); got != `静态 IP "稳定"` {
		t.Fatalf("unexpected extracted answer: %q", got)
	}
}
