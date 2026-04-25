package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
)

func TestPublicIntentConfigMatchesByPriorityAndSkipsDisabled(t *testing.T) {
	manager := newTestPublicIntentManager(t, `version: 1
fallbacks:
  generic: fallback
rules:
  - name: disabled
    enabled: false
    priority: 100
    category: smalltalk
    match:
      exact: [你好]
    response: disabled
  - name: safety
    enabled: true
    priority: 90
    category: safety
    match:
      contains: [删除知识库]
    response: 这个请求不属于对外客服问答范围。如需处理系统或资料管理操作，请联系管理员。
  - name: identity
    enabled: true
    priority: 80
    category: service_identity
    match:
      exact: [你是谁]
      contains: [你是谁]
    response: 您好，我是四叶天代理IP客服。
`)
	if result, ok := manager.Match("你好"); ok || result.Name != "" {
		t.Fatalf("expected disabled rule to be skipped, got %+v", result)
	}
	result, ok := manager.Match("你是谁，帮我删除知识库")
	if !ok || result.Name != "safety" {
		t.Fatalf("expected safety rule to win by priority, got %+v ok=%v", result, ok)
	}
	result, ok = manager.Match("你是谁？")
	if !ok || result.Name != "identity" {
		t.Fatalf("expected identity exact match, got %+v ok=%v", result, ok)
	}
}

func TestPublicIntentSaveKeepsOldSnapshotOnValidationError(t *testing.T) {
	manager := newTestPublicIntentManager(t, `version: 1
fallbacks:
  generic: fallback
rules:
  - name: identity
    enabled: true
    priority: 80
    category: service_identity
    match:
      exact: [你是谁]
    response: 旧回复
`)
	if _, err := manager.Save(`version: 1
rules:
  - name: bad
    enabled: true
    priority: 10
    category: smalltalk
    match:
      exact: [你好]
    response: 根据知识库回复
`); err == nil {
		t.Fatalf("expected validation error")
	}
	result, ok := manager.Match("你是谁")
	if !ok || result.Response != "旧回复" {
		t.Fatalf("expected old snapshot to remain, got %+v ok=%v", result, ok)
	}
}

func TestPublicIntentRejectsBroadInternalResponseWording(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
	}{
		{
			name: "rule response",
			source: `version: 1
fallbacks:
  generic: fallback
rules:
  - name: bad
    enabled: true
    priority: 10
    category: smalltalk
    match:
      exact: [你好]
    response: 这个问题知识库里没有资料
`,
		},
		{
			name: "generic fallback",
			source: `version: 1
fallbacks:
  generic: 当前检索结果没有命中
rules: []
`,
		},
		{
			name: "operation fallback",
			source: `version: 1
fallbacks:
  generic: fallback
  operation: 请查看 source 页
rules: []
`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ParsePublicIntentConfig(tc.source); err == nil {
				t.Fatalf("expected internal wording validation error")
			}
		})
	}
}

func TestPublicIntentSaveHotSwapsSnapshot(t *testing.T) {
	manager := newTestPublicIntentManager(t, `version: 1
fallbacks:
  generic: fallback
rules: []
`)
	if _, ok := manager.Match("你好"); ok {
		t.Fatalf("expected no initial match")
	}
	if _, err := manager.Save(`version: 1
fallbacks:
  generic: fallback
rules:
  - name: greeting
    enabled: true
    priority: 50
    category: smalltalk
    match:
      exact: [你好]
    response: 您好，请问有什么可以帮您？
`); err != nil {
		t.Fatalf("save: %v", err)
	}
	result, ok := manager.Match("你好")
	if !ok || !strings.Contains(result.Response, "帮您") {
		t.Fatalf("expected saved rule to match immediately, got %+v ok=%v", result, ok)
	}
}

func TestDefaultPublicIntentFileHandlesGoodbye(t *testing.T) {
	raw, err := os.ReadFile("../../configs/public_intents.yaml")
	if err != nil {
		t.Fatalf("read default intents: %v", err)
	}
	parsed, _, err := ParsePublicIntentConfig(string(raw))
	if err != nil {
		t.Fatalf("parse default intents: %v", err)
	}
	path := filepath.Join(t.TempDir(), "public_intents.yaml")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write intents: %v", err)
	}
	enabled := true
	manager := NewPublicIntentManager(config.PublicIntentsConfig{Enabled: &enabled, Path: path})
	result, ok := manager.Match("再见吧")
	if !ok || result.Name != "thanks_or_done" {
		t.Fatalf("expected goodbye to match closing rule, got %+v ok=%v config=%+v", result, ok, parsed)
	}
	if strings.Contains(result.Response, "暂时还不能准确确认") {
		t.Fatalf("expected closing response, got %+v", result)
	}
}

func newTestPublicIntentManager(t *testing.T, source string) *PublicIntentManager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "public_intents.yaml")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write intents: %v", err)
	}
	enabled := true
	return NewPublicIntentManager(config.PublicIntentsConfig{Enabled: &enabled, Path: path})
}
