package service

import (
	"strings"
	"testing"
)

func TestNormalizeAnalyzedIngestContentUsesSegmentIdentityForKnowledgeBaseTable(t *testing.T) {
	content := `| 技能分类 | 标准问题 | 相似问法 | 关键词 | 回复内容 | 标签 | 快捷短语 |
|-------|-------|-------|-------|-------|-------|-------|
| 账号与登录 | 你们的IP能访问微信不 | 你们的IP能访问微信不 | 登录 | 不可以用于微信登录业务。 | 账号与登录 | 登录账号 |
| 产品咨询 | 你们的海外IP支持TikTok运营吗 | 你们的海外IP支持TikTok运营吗 | 海外IP | 可以的。 | 产品咨询 | 需要人工服务 |
| 人工服务 | 联系人工客服 | 联系人工客服 | 人工客服 | 请联系人工客服。 | 人工服务 | 需要人工服务 |
| 技术配置 | 你们的海外IP支持API提取吗 | 你们的海外IP支持API提取吗 | API | 不支持 API 提取 IP。 | 技术配置 | 需要人工服务 |
| 常见问题 | 海外IP更换后还能继续使用吗 | 海外IP更换后还能继续使用吗 | 海外IP | 不能。 | 常见问题 | 需要人工服务 |
| 产品咨询 | 你们的海外代理IP支持哪些传输协议 | 你们的海外代理IP支持哪些传输协议 | 协议 | 支持 HTTP、HTTPS、Socks5。 | 产品咨询 | 需要人工服务 |
`
	parsed := ingestLLMOutput{
		SourceTitle: "海外代理IP服务FAQ",
		SourceSlug:  "overseas-proxy-ip-service-faq",
		Summary:     "本次 ingest 处理了一份关于海外代理IP服务的常见问题解答（FAQ）表格。",
	}

	normalized := normalizeAnalyzedIngestContent("raw/articles/2026-04-23-101-200.md", content, parsed)
	if strings.Contains(normalized.SourceTitle, "海外代理IP服务FAQ") {
		t.Fatalf("expected segment title, got %q", normalized.SourceTitle)
	}
	if !strings.Contains(normalized.SourceTitle, "知识库分段") {
		t.Fatalf("expected generic knowledge-base title, got %q", normalized.SourceTitle)
	}
	if normalized.SourceSlug == parsed.SourceSlug {
		t.Fatalf("expected slug override for mixed table, got %q", normalized.SourceSlug)
	}
	if !strings.Contains(normalized.Summary, "多主题客服问答集合") {
		t.Fatalf("expected mixed-table summary, got %q", normalized.Summary)
	}
	if !strings.Contains(strings.Join(normalized.Warnings, "\n"), "多主题客服知识库表格") {
		t.Fatalf("expected warning about mixed knowledge base table, got %+v", normalized.Warnings)
	}
}
