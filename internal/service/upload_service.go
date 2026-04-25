package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
	base          string
	ext           string
	kind          string
	storedRel     string
	content       string
	storedContent []byte
	contentSHA    string
	faqDataset    *canonicalFAQDataset
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
	storedContent := req.Content
	if len(prepared.storedContent) > 0 {
		storedContent = prepared.storedContent
	}
	if err := os.WriteFile(absPath, storedContent, 0o644); err != nil {
		return nil, nil, err
	}
	if prepared.kind == "image" {
		result := map[string]any{
			"summary":      fmt.Sprintf("图片已保存到 %s，当前版本不会自动执行视觉摄入。", prepared.storedRel),
			"stored_path":  prepared.storedRel,
			"media_kind":   "image",
			"pending":      true,
			"content_type": req.ContentType,
		}
		if stream {
			emitStreamEvent(ctx, "meta", map[string]any{
				"mode":        "upload",
				"file_name":   req.Filename,
				"media_kind":  prepared.kind,
				"stored_path": prepared.storedRel,
			})
			emitStreamEvent(ctx, "result", map[string]any{
				"reply":   result["summary"],
				"details": result,
			})
		}
		return result, nil, nil
	}
	execution := NewExecution("ingest")
	if stream {
		emitStreamEvent(ctx, "meta", map[string]any{
			"mode":         "ingest",
			"execution_id": execution.ID,
			"started_at":   execution.StartedAt.Format(time.RFC3339Nano),
			"file_name":    req.Filename,
			"media_kind":   prepared.kind,
			"stored_path":  prepared.storedRel,
			"source_format": func() string {
				if prepared.faqDataset != nil {
					return prepared.faqDataset.Format
				}
				return prepared.kind
			}(),
		})
		if plan := buildUploadIngestPlan(prepared); plan != nil {
			emitStreamEvent(ctx, "ingest_plan", plan)
		}
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
	knowledgeProfile, _ := s.loadKnowledgeProfile()
	ext := strings.ToLower(filepath.Ext(req.Filename))
	base := strings.TrimSuffix(filepath.Base(req.Filename), ext)
	if base == "" {
		base = "upload"
	}
	name := slugFromText(base)
	if name == "" {
		name = "upload"
	}
	if ext == ".xlsx" {
		parsed, err := parseStructuredFAQUploadWithProfile(req.Filename, base, ext, req.Content, "", knowledgeProfile)
		if err != nil {
			return nil, err
		}
		if parsed == nil || parsed.Dataset == nil {
			return nil, ValidationError{Message: "FAQ Excel 中未识别到包含“标准问题”和“回复内容”的有效表格。"}
		}
		if err := s.validateUploadSizeWithStructured(ext, len(req.Content), true); err != nil {
			return nil, err
		}
		shaSum := sha256.Sum256(req.Content)
		storedRel := filepath.ToSlash(filepath.Join("raw/articles", fmt.Sprintf("%s-%s.json", nowDate(), name)))
		return &preparedUpload{
			base:          base,
			ext:           ".json",
			kind:          "article",
			storedRel:     storedRel,
			content:       parsed.Content,
			storedContent: []byte(parsed.Content),
			contentSHA:    hex.EncodeToString(shaSum[:]),
			faqDataset:    parsed.Dataset,
		}, nil
	}
	if ext == "" {
		ext = ".txt"
	}
	storedRel := ""
	kind := detectUploadKind(ext)
	switch kind {
	case "article", "document":
		storedRel = filepath.ToSlash(filepath.Join("raw/articles", fmt.Sprintf("%s-%s%s", nowDate(), name, ext)))
	case "image":
		storedRel = filepath.ToSlash(filepath.Join("raw/images", fmt.Sprintf("%s-%s%s", nowDate(), name, ext)))
	default:
		return nil, ValidationError{Message: fmt.Sprintf("暂不支持该文件类型 %s。当前仅支持文章文档（txt、md、markdown、json、xlsx、doc、docx、rtf）和图片（png、jpg、jpeg、webp）。", ext)}
	}
	content := string(req.Content)
	if kind == "document" {
		text, err := extractDocumentText(ctx, req.Content, ext)
		if err != nil {
			return nil, ValidationError{Message: fmt.Sprintf("文档正文提取失败：%s", err.Error())}
		}
		content = text
	}
	parsed, faqErr := parseStructuredFAQUploadWithProfile(req.Filename, base, ext, req.Content, content, knowledgeProfile)
	if faqErr != nil {
		return nil, faqErr
	}
	var faqDataset *canonicalFAQDataset
	if parsed != nil {
		content = parsed.Content
		faqDataset = parsed.Dataset
	}
	if kind == "article" && ext == ".json" && faqDataset == nil {
		return nil, ValidationError{Message: "当前仅支持 FAQ JSON 导入，文件中未识别到顶层 faq 数组。"}
	}
	if err := s.validateUploadSizeWithStructured(ext, len(req.Content), faqDataset != nil); err != nil {
		return nil, err
	}
	if kind == "article" || kind == "document" {
		if err := s.validateTextStructure(content, faqDataset != nil); err != nil {
			return nil, err
		}
	}
	shaSum := sha256.Sum256(req.Content)
	return &preparedUpload{
		base:       base,
		ext:        ext,
		kind:       kind,
		storedRel:  storedRel,
		content:    content,
		contentSHA: hex.EncodeToString(shaSum[:]),
		faqDataset: faqDataset,
	}, nil
}

func buildUploadIngestPlan(prepared *preparedUpload) map[string]any {
	if prepared == nil {
		return nil
	}
	if prepared.faqDataset == nil {
		return map[string]any{
			"source_format":      prepared.kind,
			"segments_total":     1,
			"segmented":          false,
			"category_breakdown": nil,
		}
	}
	segments := prepared.faqDataset.segments(faqSegmentSize)
	segmentItems := make([]map[string]any, 0, len(segments))
	categoryCounts := map[string]int{}
	for _, segment := range segments {
		category := firstNonEmpty(strings.TrimSpace(segment.Tag), "未分类")
		categoryCounts[category] += len(segment.Entries)
		segmentItems = append(segmentItems, map[string]any{
			"index":       segment.Index,
			"title":       segment.title(),
			"slug":        segment.slug(),
			"category":    category,
			"entry_count": len(segment.Entries),
		})
	}
	return map[string]any{
		"source_format":      prepared.faqDataset.Format,
		"segments_total":     len(segments),
		"segmented":          true,
		"category_breakdown": categoryCounts,
		"segments":           segmentItems,
	}
}

func (s *UploadService) runSingleUploadViaDirect(ctx context.Context, execution *Execution, traceID string, prepared *preparedUpload) (map[string]any, error) {
	context := map[string]any{
		"stored_path":   prepared.storedRel,
		"path":          prepared.storedRel,
		"file_name":     prepared.base + prepared.ext,
		"source_format": prepared.kind,
	}
	if prepared.faqDataset != nil {
		context["source_format"] = prepared.faqDataset.Format
		context["faq_entry_count"] = len(prepared.faqDataset.Entries)
		if plan := buildUploadIngestPlan(prepared); plan != nil {
			if data, err := json.MarshalIndent(plan, "", "  "); err == nil {
				context["ingest_plan"] = string(data)
			}
		}
	}
	if strings.TrimSpace(prepared.content) != "" {
		context["segment_preview"] = truncateDirectPromptValue(prepared.content, 2200)
	}
	result, err := NewDirectAdminService(s.deps).Run(ctx, execution, traceID, DirectAdminRequest{
		Message:  fmt.Sprintf("请处理刚上传的文件并完成后续管理员操作：%s", prepared.storedRel),
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
	completed := resultIntValue(result, "segments_completed")
	failed := resultIntValue(result, "segments_failed")
	if failed > 0 && completed == 0 {
		return ExecutionFailed
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

func resultIntValue(result map[string]any, key string) int {
	if result == nil {
		return 0
	}
	switch value := result[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func stringSliceValue(result map[string]any, key string) []string {
	if result == nil {
		return nil
	}
	return appendUniqueStrings(nil, stringArrayValue(result[key])...)
}

func stringArrayValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return appendUniqueStrings(nil, typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			out = append(out, text)
		}
		return appendUniqueStrings(nil, out...)
	default:
		return nil
	}
}

func appendUniqueStrings(base []string, values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func commandRecords(value any) []map[string]any {
	items, ok := value.([]map[string]any)
	if ok {
		return items
	}
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, record)
	}
	return out
}

func collectUploadFiles(result map[string]any) []string {
	files := appendUniqueStrings(nil, resultStringValue(result, "output_file"), resultStringValue(result, "report_file"))
	return appendUniqueStrings(files, stringSliceValue(result, "output_files")...)
}

func collectUploadArtifacts(result map[string]any) []string {
	artifacts := appendUniqueStrings(nil, collectUploadFiles(result)...)
	return appendUniqueStrings(artifacts, stringSliceValue(result, "artifacts")...)
}

func marshalCompactJSON(value any) string {
	if value == nil {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
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
	if kind != "article" && kind != "document" {
		return nil
	}
	return s.validateUploadSizeWithStructured(ext, sizeBytes, false)
}

func (s *UploadService) validateUploadSizeWithStructured(ext string, sizeBytes int, structuredFAQ bool) error {
	kind := detectUploadKind(ext)
	if kind != "article" && kind != "document" {
		return nil
	}
	maxBytes := s.deps.Config.Upload.MaxTextFileKB * 1024
	if structuredFAQ {
		if workspaceMax := s.deps.Config.Workspace.MaxFileSizeMB * 1024 * 1024; workspaceMax > maxBytes {
			maxBytes = workspaceMax
		}
	}
	if maxBytes <= 0 || sizeBytes <= maxBytes {
		return nil
	}
	if structuredFAQ {
		return ValidationError{Message: fmt.Sprintf(
			"FAQ 数据文件过大，当前安全上限约为 %.1fMB，你这次上传约 %.1fKB。请先按业务主题或分类拆分后再上传。",
			float64(maxBytes)/1024/1024,
			float64(sizeBytes)/1024,
		)}
	}
	return ValidationError{Message: fmt.Sprintf(
		"文件过大，当前客服上传的文本/文档文件限制为 %dKB，你这次上传约 %.1fKB。请按主题拆分后再上传，例如拆成“下载与安装”“产品咨询”“价格与购买”“售后与排查”几个文件。",
		s.deps.Config.Upload.MaxTextFileKB,
		float64(sizeBytes)/1024,
	)}
}

func (s *UploadService) validateTextStructure(content string, structuredFAQ bool) error {
	if structuredFAQ {
		return nil
	}
	tableRows := countTableRows(content)
	if tableRows <= s.deps.Config.Upload.MaxTableRows {
		return nil
	}
	if !looksLikeFAQTable(content) {
		return nil
	}
	return ValidationError{Message: fmt.Sprintf(
		"检测到超大 FAQ 表格，当前版本最多支持约 %d 行表格，你这次文件检测到 %d 行。为避免表面摄入成功但实际只处理前面一部分，请拆分上传。建议按“下载与安装 / 产品咨询 / 价格与购买 / 售后与排查 / 人工服务”分别整理成多个文件。",
		s.deps.Config.Upload.MaxTableRows,
		tableRows,
	)}
}

func detectUploadKind(ext string) string {
	switch ext {
	case ".txt", ".md", ".markdown", ".json", ".xlsx":
		return "article"
	case ".doc", ".docx", ".rtf":
		return "document"
	case ".png", ".jpg", ".jpeg", ".webp":
		return "image"
	default:
		return ""
	}
}

func extractDocumentText(ctx context.Context, content []byte, ext string) (string, error) {
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
	cmd := exec.CommandContext(ctx, "textutil", "-convert", "txt", "-stdout", tmpPath)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "", fmt.Errorf("document text is empty")
	}
	return text, nil
}

var tableRowPattern = regexp.MustCompile(`(?m)^\|`)

func countTableRows(content string) int {
	return len(tableRowPattern.FindAllString(content, -1))
}

func looksLikeFAQTable(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(content, "标准问题") ||
		strings.Contains(content, "相似问法") ||
		strings.Contains(content, "回复内容") ||
		strings.Contains(content, "快捷短语") ||
		(strings.Contains(lower, "faq") && countTableRows(content) > 0)
}
