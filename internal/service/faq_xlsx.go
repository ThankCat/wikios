package service

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

type xlsxWorkbookSheet struct {
	Name string
	Path string
}

func parseFAQXLSXDataset(filename string, titleHint string, content []byte) (string, *canonicalFAQDataset, error) {
	return parseFAQXLSXDatasetWithProfile(filename, titleHint, content, nil)
}

func parseFAQXLSXDatasetWithProfile(filename string, titleHint string, content []byte, profile *knowledgeProfile) (string, *canonicalFAQDataset, error) {
	sheets, err := readXLSXSheets(content)
	if err != nil {
		return "", nil, ValidationError{Message: fmt.Sprintf("FAQ Excel 解析失败：%s", err.Error())}
	}
	for _, sheet := range sheets {
		dataset := datasetFromXLSXRowsWithProfile(filename, titleHint, sheet.Name, sheet.Rows, profile)
		if dataset == nil {
			continue
		}
		return renderFAQDatasetAsJSON(dataset), dataset, nil
	}
	return "", nil, ValidationError{Message: "FAQ Excel 中未识别到包含“标准问题”和“回复内容”的有效表格。"}
}

func datasetFromXLSXRows(filename string, titleHint string, sheetName string, rows [][]string) *canonicalFAQDataset {
	return datasetFromXLSXRowsWithProfile(filename, titleHint, sheetName, rows, nil)
}

func datasetFromXLSXRowsWithProfile(filename string, titleHint string, sheetName string, rows [][]string, profile *knowledgeProfile) *canonicalFAQDataset {
	headerRow := -1
	headerIndex := map[string]int{}
	for i, row := range rows {
		index := faqHeaderIndexMapWithProfile(row, profile, "faq_xlsx")
		if index["question"] >= 0 && index["answer"] >= 0 {
			headerRow = i
			headerIndex = index
			break
		}
	}
	if headerRow < 0 {
		return nil
	}
	entries := []canonicalFAQEntry{}
	categoryOrder := []string{}
	seenCategories := map[string]bool{}
	for _, row := range rows[headerRow+1:] {
		question := strings.TrimSpace(cellAt(row, headerIndex["question"]))
		answer := normalizeFAQAnswerText(cellAt(row, headerIndex["answer"]))
		if question == "" || answer == "" {
			continue
		}
		category := firstNonEmpty(strings.TrimSpace(cellAt(row, headerIndex["category"])), "未分类")
		if !seenCategories[category] {
			seenCategories[category] = true
			categoryOrder = append(categoryOrder, category)
		}
		entries = append(entries, canonicalFAQEntry{
			Category:         category,
			Question:         question,
			SimilarQuestions: splitFAQVariants(cellAt(row, headerIndex["similar"])),
			Keywords:         splitFAQVariants(cellAt(row, headerIndex["keywords"])),
			Tags:             splitFAQVariants(cellAt(row, headerIndex["tags"])),
			QuickReplies:     splitFAQVariants(cellAt(row, headerIndex["quick_replies"])),
			Answer:           answer,
			ConditionNotes:   splitFAQVariants(cellAt(row, headerIndex["conditions"])),
		})
	}
	if len(entries) == 0 {
		return nil
	}
	titleBase := firstNonEmpty(strings.TrimSpace(titleHint), strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)), "FAQ Excel")
	notes := []string{
		fmt.Sprintf("检测到 FAQ Excel 数据集：sheet=%s，共 %d 条标准问答，已转为统一 FAQ 结构处理。", firstNonEmpty(sheetName, "Sheet1"), len(entries)),
	}
	if hasFAQSpreadsheetMetadata(entries) {
		notes = append(notes, "Excel 中的标签和快捷短语已作为运营元数据保留，不作为客户回答正文。")
	}
	return &canonicalFAQDataset{
		Format:        "faq-xlsx",
		Family:        faqSourceFamily,
		TitleBase:     titleBase,
		SlugBase:      stableFAQSlugBase(titleBase, filename),
		RawPath:       filename,
		Entries:       entries,
		CategoryOrder: categoryOrder,
		Notes:         dedupeStrings(notes),
	}
}

func hasFAQSpreadsheetMetadata(entries []canonicalFAQEntry) bool {
	for _, entry := range entries {
		if len(entry.Tags) > 0 || len(entry.QuickReplies) > 0 {
			return true
		}
	}
	return false
}

func renderFAQDatasetAsMarkdown(dataset *canonicalFAQDataset) string {
	if dataset == nil {
		return ""
	}
	headers := []string{"技能分类", "标准问题", "相似问法", "关键词", "回复内容", "标签", "快捷短语"}
	rows := [][]string{headers}
	for _, entry := range dataset.Entries {
		rows = append(rows, []string{
			entry.Category,
			entry.Question,
			strings.Join(entry.SimilarQuestions, "<br>"),
			strings.Join(entry.Keywords, "<br>"),
			entry.Answer,
			strings.Join(entry.Tags, "<br>"),
			strings.Join(entry.QuickReplies, "<br>"),
		})
	}
	out := []string{
		"# " + firstNonEmpty(dataset.TitleBase, "FAQ Excel"),
		"",
		"Normalized from FAQ Excel upload.",
		"",
		markdownTable(rows),
	}
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}

func markdownTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	lines := []string{
		"| " + strings.Join(escapeMarkdownTableCells(rows[0]), " | ") + " |",
		"| " + strings.Repeat("--- | ", len(rows[0])),
	}
	lines[1] = strings.TrimSuffix(lines[1], " ")
	for _, row := range rows[1:] {
		cells := make([]string, len(rows[0]))
		for i := range cells {
			cells[i] = cellAt(row, i)
		}
		lines = append(lines, "| "+strings.Join(escapeMarkdownTableCells(cells), " | ")+" |")
	}
	return strings.Join(lines, "\n")
}

func escapeMarkdownTableCells(cells []string) []string {
	out := make([]string, len(cells))
	for i, cell := range cells {
		text := strings.TrimSpace(cell)
		text = strings.ReplaceAll(text, "\r\n", "\n")
		text = strings.ReplaceAll(text, "\r", "\n")
		text = strings.ReplaceAll(text, "\n", "<br>")
		text = strings.ReplaceAll(text, "|", "\\|")
		out[i] = text
	}
	return out
}

type xlsxSheetRows struct {
	Name string
	Rows [][]string
}

func readXLSXSheets(content []byte) ([]xlsxSheetRows, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, err
	}
	files := map[string]*zip.File{}
	for _, file := range reader.File {
		files[file.Name] = file
	}
	sharedStrings, err := readXLSXSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return nil, err
	}
	workbookSheets, err := readXLSXWorkbookSheets(files)
	if err != nil {
		return nil, err
	}
	out := []xlsxSheetRows{}
	for _, sheet := range workbookSheets {
		file := files[sheet.Path]
		if file == nil {
			continue
		}
		rows, err := readXLSXSheetRows(file, sharedStrings)
		if err != nil {
			return nil, err
		}
		out = append(out, xlsxSheetRows{Name: sheet.Name, Rows: rows})
	}
	return out, nil
}

func readXLSXSharedStrings(file *zip.File) ([]string, error) {
	if file == nil {
		return nil, nil
	}
	body, err := openZipFile(file)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	out := []string{}
	current := strings.Builder{}
	inSI := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			switch typed.Name.Local {
			case "si":
				inSI = true
				current.Reset()
			case "t":
				if inSI {
					var text string
					if err := decoder.DecodeElement(&text, &typed); err != nil {
						return nil, err
					}
					current.WriteString(text)
				}
			}
		case xml.EndElement:
			if typed.Name.Local == "si" && inSI {
				out = append(out, html.UnescapeString(current.String()))
				inSI = false
			}
		}
	}
	return out, nil
}

func readXLSXWorkbookSheets(files map[string]*zip.File) ([]xlsxWorkbookSheet, error) {
	workbook := files["xl/workbook.xml"]
	if workbook == nil {
		return nil, fmt.Errorf("missing xl/workbook.xml")
	}
	body, err := openZipFile(workbook)
	if err != nil {
		return nil, err
	}
	rels, err := readXLSXWorkbookRelationships(files["xl/_rels/workbook.xml.rels"])
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	out := []xlsxWorkbookSheet{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "sheet" {
			continue
		}
		name := attrValue(start.Attr, "name")
		relID := attrValue(start.Attr, "id")
		target := rels[relID]
		if target == "" {
			continue
		}
		out = append(out, xlsxWorkbookSheet{Name: name, Path: normalizeXLSXTarget(target)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("workbook has no readable sheets")
	}
	return out, nil
}

func readXLSXWorkbookRelationships(file *zip.File) (map[string]string, error) {
	if file == nil {
		return nil, fmt.Errorf("missing xl/_rels/workbook.xml.rels")
	}
	body, err := openZipFile(file)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	out := map[string]string{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "Relationship" {
			continue
		}
		id := attrValue(start.Attr, "Id")
		target := attrValue(start.Attr, "Target")
		if id != "" && target != "" {
			out[id] = target
		}
	}
	return out, nil
}

func readXLSXSheetRows(file *zip.File, sharedStrings []string) ([][]string, error) {
	body, err := openZipFile(file)
	if err != nil {
		return nil, err
	}
	var sheet struct {
		Rows []struct {
			Cells []struct {
				Ref         string `xml:"r,attr"`
				Type        string `xml:"t,attr"`
				Value       string `xml:"v"`
				InlineValue struct {
					Text string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(body, &sheet); err != nil {
		return nil, err
	}
	rows := make([][]string, 0, len(sheet.Rows))
	for _, row := range sheet.Rows {
		values := []string{}
		for cellPosition, cell := range row.Cells {
			col := xlsxColumnIndex(cell.Ref)
			if col < 0 {
				col = cellPosition
			}
			for len(values) <= col {
				values = append(values, "")
			}
			values[col] = xlsxCellText(cell.Type, cell.Value, cell.InlineValue.Text, sharedStrings)
		}
		rows = append(rows, values)
	}
	return rows, nil
}

func xlsxCellText(cellType string, value string, inline string, sharedStrings []string) string {
	switch cellType {
	case "s":
		index, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && index >= 0 && index < len(sharedStrings) {
			return strings.TrimSpace(sharedStrings[index])
		}
	case "inlineStr":
		return strings.TrimSpace(inline)
	}
	return strings.TrimSpace(value)
}

func xlsxColumnIndex(ref string) int {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return -1
	}
	col := 0
	seen := false
	for _, r := range ref {
		if r >= 'A' && r <= 'Z' {
			col = col*26 + int(r-'A'+1)
			seen = true
			continue
		}
		if r >= 'a' && r <= 'z' {
			col = col*26 + int(r-'a'+1)
			seen = true
			continue
		}
		break
	}
	if !seen {
		return -1
	}
	return col - 1
}

func normalizeXLSXTarget(target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	target = strings.TrimPrefix(target, "/")
	if strings.HasPrefix(target, "xl/") {
		return path.Clean(target)
	}
	return path.Clean(path.Join("xl", target))
}

func attrValue(attrs []xml.Attr, localName string) string {
	for _, attr := range attrs {
		if attr.Name.Local == localName {
			return attr.Value
		}
	}
	return ""
}

func openZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
