package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type UploadRequest struct {
	Filename    string
	ContentType string
	Content     []byte
}

type UploadService struct {
	baseService
}

type preparedUpload struct {
	base      string
	ext       string
	kind      string
	storedRel string
	content   string
}

func NewUploadService(deps Deps) *UploadService {
	return &UploadService{baseService: newBaseService(deps)}
}

func (s *UploadService) Save(ctx context.Context, traceID string, req UploadRequest) (map[string]any, error) {
	result, _, err := s.process(ctx, traceID, req, false)
	return result, err
}

func (s *UploadService) SaveStream(ctx context.Context, traceID string, req UploadRequest) (map[string]any, *Execution, error) {
	return s.process(ctx, traceID, req, true)
}

func (s *UploadService) process(ctx context.Context, traceID string, req UploadRequest, stream bool) (map[string]any, *Execution, error) {
	if len(req.Content) == 0 {
		return nil, nil, ValidationError{Message: "上传文件为空，请重新选择文件。"}
	}
	prepared, err := s.prepareUpload(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	absPath := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(prepared.storedRel))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(absPath, req.Content, 0o644); err != nil {
		return nil, nil, err
	}
	execution := NewExecution("ingest")
	if stream {
		emitStreamEvent(ctx, "meta", map[string]any{
			"mode":          "ingest",
			"execution_id":  execution.ID,
			"started_at":    execution.StartedAt.Format(time.RFC3339Nano),
			"file_name":     req.Filename,
			"media_kind":    prepared.kind,
			"stored_path":   prepared.storedRel,
			"source_format": prepared.kind,
		})
	}
	var (
		result    map[string]any
		resultErr error
	)
	result, resultErr = s.runSingleUploadViaDirect(ctx, execution, traceID, prepared)
	if resultErr != nil {
		execution.Status = ExecutionFailed
		execution.Error = resultErr.Error()
		execution.EndedAt = time.Now()
		details := map[string]any{
			"summary":     resultErr.Error(),
			"stored_path": prepared.storedRel,
			"media_kind":  prepared.kind,
			"steps":       execution.Steps,
			"execution":   execution,
		}
		return nil, execution, ExecutionError{
			Message: resultErr.Error(),
			Details: details,
		}
	}
	execution.Status = uploadExecutionStatus(result)
	if execution.Status == ExecutionFailed {
		execution.Error = firstNonEmpty(resultStringValue(result, "summary"), "摄入失败")
	}
	execution.EndedAt = time.Now()
	result["stored_path"] = prepared.storedRel
	result["media_kind"] = prepared.kind
	result["steps"] = execution.Steps
	result["execution"] = execution
	if stream {
		emitStreamEvent(ctx, "result", map[string]any{
			"reply":     firstNonEmpty(resultStringValue(result, "summary"), "摄入完成"),
			"details":   result,
			"execution": execution,
		})
	}
	return result, execution, nil
}

func (s *UploadService) prepareUpload(ctx context.Context, req UploadRequest) (*preparedUpload, error) {
	ext := strings.ToLower(filepath.Ext(req.Filename))
	base := strings.TrimSuffix(filepath.Base(req.Filename), ext)
	if base == "" {
		base = "upload"
	}
	name := slugFromText(base)
	if name == "" {
		name = "upload"
	}
	if ext == "" {
		return nil, unsupportedDocumentUploadError(ext)
	}
	kind := detectUploadKind(ext)
	if kind == "" {
		return nil, unsupportedDocumentUploadError(ext)
	}
	storedRel := filepath.ToSlash(filepath.Join("raw/articles", fmt.Sprintf("%s-%s%s", nowDate(), name, ext)))
	content := string(req.Content)
	if isOfficeDocumentExt(ext) {
		text, err := extractDocumentText(ctx, req.Content, ext)
		if err != nil {
			return nil, ValidationError{Message: fmt.Sprintf("文档正文提取失败：%s", err.Error())}
		}
		content = text
	}
	if err := s.validateUploadSize(ext, len(req.Content)); err != nil {
		return nil, err
	}
	return &preparedUpload{
		base:      base,
		ext:       ext,
		kind:      kind,
		storedRel: storedRel,
		content:   content,
	}, nil
}

func (s *UploadService) runSingleUploadViaDirect(ctx context.Context, execution *Execution, traceID string, prepared *preparedUpload) (map[string]any, error) {
	context := map[string]any{
		"stored_path":   prepared.storedRel,
		"path":          prepared.storedRel,
		"file_name":     prepared.base + prepared.ext,
		"source_format": prepared.kind,
	}
	if strings.TrimSpace(prepared.content) != "" {
		context["document_preview"] = truncateDirectPromptValue(prepared.content, 2200)
	}
	result, err := NewDirectAdminService(s.deps).Run(ctx, execution, traceID, DirectAdminRequest{
		Message:  fmt.Sprintf("请按 AGENT.md 的 INGEST 流程处理刚上传的文档：%s", prepared.storedRel),
		ModeHint: "ingest",
		Attachments: []DirectAdminAttachment{
			{Path: prepared.storedRel, Kind: prepared.kind, Name: prepared.base + prepared.ext},
		},
		Context: context,
	})
	if err != nil {
		return nil, err
	}
	result["stored_path"] = prepared.storedRel
	result["media_kind"] = prepared.kind
	result["source_format"] = firstNonEmpty(resultStringValue(result, "source_format"), context["source_format"].(string))
	return normalizeDirectResult(result), nil
}

func uploadExecutionStatus(result map[string]any) ExecutionStatus {
	if resultBoolValue(result, "partial_success") {
		return ExecutionPartialSuccess
	}
	return ExecutionSuccess
}

func resultStringValue(result map[string]any, key string) string {
	if result == nil {
		return ""
	}
	value, _ := result[key].(string)
	return value
}

func resultBoolValue(result map[string]any, key string) bool {
	if result == nil {
		return false
	}
	value, _ := result[key].(bool)
	return value
}

func truncateDirectPromptValue(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func (s *UploadService) validateUploadSize(ext string, sizeBytes int) error {
	kind := detectUploadKind(ext)
	if kind != "document" {
		return nil
	}
	runtimeSettings := LoadRuntimeSettingsOrDefault(context.Background(), s.deps.Store, s.deps.Config)
	maxBytes := runtimeSettings.Knowledge.MaxTextFileKB * 1024
	if isOfficeDocumentExt(ext) && s.deps.Config.Workspace.MaxFileSizeMB > 0 {
		maxBytes = s.deps.Config.Workspace.MaxFileSizeMB * 1024 * 1024
	}
	if maxBytes <= 0 || sizeBytes <= maxBytes {
		return nil
	}
	return ValidationError{Message: fmt.Sprintf(
		"文件过大，当前文档上传限制约为 %.1fKB，你这次上传约 %.1fKB。请压缩文档或拆成多个文档后再上传。",
		float64(maxBytes)/1024,
		float64(sizeBytes)/1024,
	)}
}

func detectUploadKind(ext string) string {
	switch ext {
	case ".txt", ".text", ".md", ".markdown", ".doc", ".docx", ".rtf":
		return "document"
	default:
		return ""
	}
}

func unsupportedDocumentUploadError(ext string) error {
	if ext == "" {
		ext = "unknown"
	}
	return ValidationError{Message: fmt.Sprintf("暂不支持该文件类型 %s。当前上传摄入只支持文档文件：.md、.markdown、.txt、.text、.doc、.docx、.rtf；不支持 Excel、CSV、TSV、JSON、图片或其它结构化数据。", ext)}
}

func isOfficeDocumentExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".doc", ".docx", ".rtf":
		return true
	default:
		return false
	}
}

func extractDocumentText(ctx context.Context, content []byte, ext string) (string, error) {
	switch strings.ToLower(ext) {
	case ".docx":
		if text, err := extractDocxText(content); err == nil && strings.TrimSpace(text) != "" {
			return text, nil
		}
	case ".rtf":
		if text, err := extractRTFText(content); err == nil && strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	return extractDocumentTextWithTool(ctx, content, ext)
}

func extractDocumentTextWithTool(ctx context.Context, content []byte, ext string) (string, error) {
	tmpFile, err := os.CreateTemp("", "wikios-upload-*"+ext)
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmpFile.Write(content); err != nil {
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		return "", err
	}
	commands := documentTextCommands(tmpPath, ext)
	if len(commands) == 0 {
		return "", fmt.Errorf("no document text extraction tool available")
	}
	var lastErr error
	for _, command := range commands {
		cmd := exec.CommandContext(ctx, command.name, command.args...)
		output, err := cmd.Output()
		if err != nil {
			lastErr = err
			continue
		}
		text := strings.TrimSpace(string(output))
		if text != "" {
			return text, nil
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("document text is empty")
}

type documentTextCommand struct {
	name string
	args []string
}

func documentTextCommands(path string, ext string) []documentTextCommand {
	out := []documentTextCommand{}
	if _, err := exec.LookPath("textutil"); err == nil {
		out = append(out, documentTextCommand{name: "textutil", args: []string{"-convert", "txt", "-stdout", path}})
	}
	if _, err := exec.LookPath("pandoc"); err == nil {
		out = append(out, documentTextCommand{name: "pandoc", args: []string{path, "-t", "plain"}})
	}
	if strings.EqualFold(ext, ".doc") {
		if _, err := exec.LookPath("antiword"); err == nil {
			out = append(out, documentTextCommand{name: "antiword", args: []string{path}})
		}
		if _, err := exec.LookPath("catdoc"); err == nil {
			out = append(out, documentTextCommand{name: "catdoc", args: []string{path}})
		}
	}
	return out
}

func extractDocxText(content []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return "", err
	}
	parts := []string{}
	for _, name := range []string{"word/document.xml", "word/footnotes.xml", "word/endnotes.xml"} {
		for _, file := range reader.File {
			if file.Name != name {
				continue
			}
			text, err := extractDocxXMLText(file)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
			break
		}
	}
	text := normalizeExtractedText(strings.Join(parts, "\n\n"))
	if text == "" {
		return "", fmt.Errorf("document text is empty")
	}
	return text, nil
}

func extractDocxXMLText(file *zip.File) (string, error) {
	rc, err := file.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	decoder := xml.NewDecoder(rc)
	var b strings.Builder
	inText := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			switch typed.Name.Local {
			case "t":
				inText = true
			case "tab":
				b.WriteByte('\t')
			case "br", "cr":
				b.WriteByte('\n')
			}
		case xml.EndElement:
			switch typed.Name.Local {
			case "t":
				inText = false
			case "p":
				b.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				b.Write([]byte(typed))
			}
		}
	}
	return normalizeExtractedText(b.String()), nil
}

func extractRTFText(content []byte) (string, error) {
	text := string(content)
	var b strings.Builder
	for i := 0; i < len(text); {
		ch := text[i]
		switch ch {
		case '{', '}':
			i++
		case '\\':
			i = consumeRTFControl(text, i+1, &b)
		case '\r', '\n':
			i++
		default:
			b.WriteByte(ch)
			i++
		}
	}
	out := normalizeExtractedText(b.String())
	if out == "" {
		return "", fmt.Errorf("document text is empty")
	}
	return out, nil
}

func consumeRTFControl(text string, i int, b *strings.Builder) int {
	if i >= len(text) {
		return i
	}
	switch text[i] {
	case '\\', '{', '}':
		b.WriteByte(text[i])
		return i + 1
	case '\'':
		if i+2 < len(text) {
			if value, err := strconv.ParseUint(text[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(value))
				return i + 3
			}
		}
		return i + 1
	}
	start := i
	for i < len(text) && ((text[i] >= 'a' && text[i] <= 'z') || (text[i] >= 'A' && text[i] <= 'Z')) {
		i++
	}
	word := text[start:i]
	sign := 1
	if i < len(text) && text[i] == '-' {
		sign = -1
		i++
	}
	numStart := i
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	num := 0
	if numStart < i {
		if parsed, err := strconv.Atoi(text[numStart:i]); err == nil {
			num = parsed * sign
		}
	}
	if i < len(text) && text[i] == ' ' {
		i++
	}
	switch strings.ToLower(word) {
	case "par", "line":
		b.WriteByte('\n')
	case "tab":
		b.WriteByte('\t')
	case "emdash":
		b.WriteString("--")
	case "endash":
		b.WriteString("-")
	case "bullet":
		b.WriteString("- ")
	case "u":
		if num < 0 {
			num += 65536
		}
		if num > 0 {
			b.WriteRune(rune(num))
		}
		if i < len(text) && text[i] != '\\' && text[i] != '{' && text[i] != '}' {
			i++
		}
	}
	return i
}

func normalizeExtractedText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(collapseHorizontalWhitespace(line))
		if line == "" {
			if !blank && len(out) > 0 {
				out = append(out, "")
				blank = true
			}
			continue
		}
		out = append(out, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func collapseHorizontalWhitespace(text string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) && r != '\n' {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return b.String()
}
