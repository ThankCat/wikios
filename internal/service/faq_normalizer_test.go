package service

import (
	"archive/zip"
	"bytes"
	"strconv"
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

func TestParseFAQXLSXDatasetFromCustomerWorkbookShape(t *testing.T) {
	raw := buildTestFAQXLSX(t, [][]string{
		{"技能分类", "标准问题", "相似问法", "关键词", "回复内容", "标签", "快捷短语"},
		{"下载与安装", "怎么下载", "下载什么软件\n下载地址在哪", "下载\n安装\n客户端", "您可以通过我们的官网下载页面进行下载。", "下载与安装\n软件下载", "Windows安装\n需要人工服务"},
		{"产品咨询", "静态IP适合什么场景", "固定IP适合做什么\n静态IP有什么用途", "静态ip\n固定ip", "静态IP适合长期稳定网络环境。", "产品咨询\n静态IP", "价格怎么咨询"},
	})

	jsonContent, dataset, err := parseFAQXLSXDataset("知识库问答整理.xlsx", "知识库问答整理", raw)
	if err != nil {
		t.Fatalf("parse xlsx: %v", err)
	}
	if dataset == nil {
		t.Fatalf("expected FAQ dataset")
	}
	if dataset.Format != "faq-xlsx" {
		t.Fatalf("unexpected format: %s", dataset.Format)
	}
	if len(dataset.Entries) != 2 {
		t.Fatalf("unexpected entries: %+v", dataset.Entries)
	}
	entry := dataset.Entries[0]
	if entry.Category != "下载与安装" || entry.Question != "怎么下载" {
		t.Fatalf("unexpected first entry: %+v", entry)
	}
	if len(entry.SimilarQuestions) != 2 || len(entry.Keywords) != 3 || len(entry.Tags) != 2 || len(entry.QuickReplies) != 2 {
		t.Fatalf("expected spreadsheet metadata to be parsed, got %+v", entry)
	}
	if !strings.Contains(jsonContent, `"source_format":"faq-xlsx"`) || !strings.Contains(jsonContent, `"faq":[`) {
		t.Fatalf("expected normalized FAQ JSON, got %s", jsonContent)
	}
	parsed, err := detectCanonicalFAQDataset("raw/articles/faq-xlsx.json", "知识库问答整理", jsonContent)
	if err != nil {
		t.Fatalf("parse normalized json: %v", err)
	}
	if parsed == nil || parsed.Format != "faq-xlsx" || len(parsed.Entries) != 2 {
		t.Fatalf("expected normalized JSON to round trip as faq-xlsx, got %+v", parsed)
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

func buildTestFAQXLSX(t *testing.T, rows [][]string) []byte {
	t.Helper()
	shared := []string{}
	index := map[string]int{}
	sharedIndex := func(value string) int {
		if existing, ok := index[value]; ok {
			return existing
		}
		index[value] = len(shared)
		shared = append(shared, value)
		return len(shared) - 1
	}
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for r, row := range rows {
		sheet.WriteString(`<row r="`)
		sheet.WriteString(strconv.Itoa(r + 1))
		sheet.WriteString(`">`)
		for c, cell := range row {
			ref := string(rune('A'+c)) + strconv.Itoa(r+1)
			sheet.WriteString(`<c r="`)
			sheet.WriteString(ref)
			sheet.WriteString(`" t="s"><v>`)
			sheet.WriteString(strconv.Itoa(sharedIndex(cell)))
			sheet.WriteString(`</v></c>`)
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)

	var sst strings.Builder
	sst.WriteString(`<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, value := range shared {
		sst.WriteString(`<si><t>`)
		sst.WriteString(xmlEscape(value))
		sst.WriteString(`</t></si>`)
	}
	sst.WriteString(`</sst>`)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeZipFile(t, zw, "xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="正式知识库" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	writeZipFile(t, zw, "xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`)
	writeZipFile(t, zw, "xl/worksheets/sheet1.xml", sheet.String())
	writeZipFile(t, zw, "xl/sharedStrings.xml", sst.String())
	if err := zw.Close(); err != nil {
		t.Fatalf("close xlsx zip: %v", err)
	}
	return buf.Bytes()
}

func writeZipFile(t *testing.T, zw *zip.Writer, name string, content string) {
	t.Helper()
	writer, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create zip file %s: %v", name, err)
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatalf("write zip file %s: %v", name, err)
	}
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}
