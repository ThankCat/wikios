package service

import (
	"strings"
	"testing"
)

func TestDetectCanonicalFAQDatasetFromJSON(t *testing.T) {
	raw := `{
  "types": [{"id": "type-1", "category": "账号与登录"}],
  "faq": [{
    "id": "faq-1",
    "question": "你们的IP能访问微信不",
    "answer": "<p>不可以用于微信登录业务。</p>",
    "type_id": "type-1",
    "condition_template": [{
      "condition_list": [{
        "value": "实名"
      }]
    }]
  }],
  "sims": [{
    "parent_id": "faq-1",
    "question": "你们的IP能访问微信不吗"
  }],
  "ws_info": {
    "wordslots": [{"name": "实名"}]
  }
}`

	dataset, err := detectCanonicalFAQDataset("raw/articles/faq.json", "FAQ数据", raw)
	if err != nil {
		t.Fatalf("detect dataset: %v", err)
	}
	if dataset == nil {
		t.Fatalf("expected FAQ dataset")
	}
	if dataset.Format != "faq-json" {
		t.Fatalf("unexpected format: %s", dataset.Format)
	}
	if len(dataset.Entries) != 1 {
		t.Fatalf("unexpected entries: %+v", dataset.Entries)
	}
	entry := dataset.Entries[0]
	if entry.Category != "账号与登录" {
		t.Fatalf("unexpected category: %+v", entry)
	}
	if entry.Answer != "不可以用于微信登录业务。" {
		t.Fatalf("expected html to plain text, got %q", entry.Answer)
	}
	if len(entry.SimilarQuestions) != 1 || entry.SimilarQuestions[0] != "你们的IP能访问微信不吗" {
		t.Fatalf("expected sims merged, got %+v", entry.SimilarQuestions)
	}
	if len(entry.ConditionNotes) == 0 || !strings.Contains(strings.Join(entry.ConditionNotes, "\n"), "实名") {
		t.Fatalf("expected condition notes, got %+v", entry.ConditionNotes)
	}
	if !strings.Contains(strings.Join(dataset.Notes, "\n"), "ws_info") {
		t.Fatalf("expected ws_info note, got %+v", dataset.Notes)
	}
}

func TestDetectCanonicalFAQDatasetFromMarkdownTable(t *testing.T) {
	raw := `| 技能分类 | 标准问题 | 相似问法 | 回复内容 |
| --- | --- | --- | --- |
| 产品咨询 | 静态IP适用什么场景 | 静态IP适合什么场景<br>静态IP能做什么 | <p>适合账号运营。</p> |
`

	dataset, err := detectCanonicalFAQDataset("raw/articles/faq.md", "FAQ表格", raw)
	if err != nil {
		t.Fatalf("detect dataset: %v", err)
	}
	if dataset == nil {
		t.Fatalf("expected FAQ dataset")
	}
	if dataset.Format != "faq-markdown-table" {
		t.Fatalf("unexpected format: %s", dataset.Format)
	}
	if len(dataset.Entries) != 1 {
		t.Fatalf("unexpected entries: %+v", dataset.Entries)
	}
	entry := dataset.Entries[0]
	if entry.Category != "产品咨询" {
		t.Fatalf("unexpected category: %+v", entry)
	}
	if entry.Answer != "适合账号运营。" {
		t.Fatalf("expected plain text answer, got %q", entry.Answer)
	}
	if len(entry.SimilarQuestions) != 2 {
		t.Fatalf("expected similar questions, got %+v", entry.SimilarQuestions)
	}
}

func TestBuildFAQEvidencePreviewSelectsMatchingEntry(t *testing.T) {
	body := `## Summary

这是 FAQ 分段摘要。

## Key Points

- 相似问法已并入主问法。

## FAQ Entries

### 静态IP适用什么场景

分类：产品咨询

回复：
适合账号运营。

### 你们的IP能访问微信不

分类：账号与登录

回复：
不可以用于微信登录业务。
`

	preview := buildFAQEvidencePreview(body, "你们的IP能访问微信不")
	if !strings.Contains(preview, "不可以用于微信登录业务") {
		t.Fatalf("expected matched faq entry in preview, got %s", preview)
	}
	if strings.Index(preview, "你们的IP能访问微信不") > strings.Index(preview, "静态IP适用什么场景") && strings.Contains(preview, "静态IP适用什么场景") {
		t.Fatalf("expected matching entry to rank before unrelated one, got %s", preview)
	}
}
