package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type UploadRequest struct {
	Filename    string
	ContentType string
	Content     []byte
}

type UploadService struct {
	baseService
}

func NewUploadService(deps Deps) *UploadService {
	return &UploadService{baseService: newBaseService(deps)}
}

func (s *UploadService) Save(ctx context.Context, traceID string, req UploadRequest) (map[string]any, error) {
	if len(req.Content) == 0 {
		return nil, fmt.Errorf("upload content is empty")
	}
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
		return nil, fmt.Errorf("unsupported upload type: %s", ext)
	}
	absPath := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(storedRel))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(absPath, req.Content, 0o644); err != nil {
		return nil, err
	}
	if kind == "image" {
		return map[string]any{
			"summary":      fmt.Sprintf("图片已保存到 %s，当前版本不会自动执行视觉摄入。", storedRel),
			"stored_path":  storedRel,
			"media_kind":   "image",
			"pending":      true,
			"content_type": req.ContentType,
		}, nil
	}

	content := string(req.Content)
	if kind == "document" {
		text, err := extractDocumentText(ctx, absPath)
		if err != nil {
			return map[string]any{
				"summary":      fmt.Sprintf("文件已保存到 %s，但文档正文提取失败：%s", storedRel, err.Error()),
				"stored_path":  storedRel,
				"media_kind":   "document",
				"pending":      true,
				"content_type": req.ContentType,
			}, nil
		}
		content = text
	}
	shaSum := sha256.Sum256(req.Content)
	execution := NewExecution("ingest")
	result, err := NewIngestService(s.deps).Run(ctx, execution, traceID, IngestRequest{
		InputType:       "file",
		Path:            storedRel,
		Interactive:     false,
		ContentOverride: content,
		TitleOverride:   base,
		SHA256Override:  hex.EncodeToString(shaSum[:]),
	})
	if err != nil {
		return nil, err
	}
	result["stored_path"] = storedRel
	result["media_kind"] = kind
	result["execution"] = execution
	return result, nil
}

func detectUploadKind(ext string) string {
	switch ext {
	case ".txt", ".md", ".markdown":
		return "article"
	case ".doc", ".docx", ".rtf":
		return "document"
	case ".png", ".jpg", ".jpeg", ".webp":
		return "image"
	default:
		return ""
	}
}

func extractDocumentText(ctx context.Context, absPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "textutil", "-convert", "txt", "-stdout", absPath)
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
