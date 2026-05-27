package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"wikios/internal/wikiadapter"
)

type ReviewQueueService struct {
	baseService
}

type ReviewItem struct {
	ID                  string   `json:"id"`
	Path                string   `json:"path"`
	Question            string   `json:"question"`
	OriginalQuestion    string   `json:"original_question"`
	DraftAnswer         string   `json:"draft_answer"`
	SuggestedTargetPath string   `json:"suggested_target_path"`
	Confidence          float64  `json:"confidence"`
	BoundaryReason      string   `json:"boundary_reason"`
	MatchedPages        []string `json:"matched_pages"`
	CreatedAt           string   `json:"created_at"`
	SessionID           string   `json:"session_id"`
	QuestionMessageID   string   `json:"question_message_id"`
	AnswerMessageID     string   `json:"answer_message_id"`
	QuestionCreatedAt   string   `json:"question_created_at"`
	AnswerCreatedAt     string   `json:"answer_created_at"`
	AnswerMode          string   `json:"answer_mode"`
	EvidenceConfidence  float64  `json:"evidence_confidence"`
	RetrievedPages      []string `json:"retrieved_pages"`
	ConversationExcerpt []string `json:"conversation_excerpt"`
}

type ReviewTarget struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

type ReviewNextResponse struct {
	Item           *ReviewItem    `json:"item,omitempty"`
	PendingCount   int            `json:"pending_count"`
	RemainingCount int            `json:"remaining_count"`
	TargetPaths    []ReviewTarget `json:"target_paths"`
}

type ReviewCreateRequest struct {
	Question            string
	OriginalQuestion    string
	DraftAnswer         string
	SuggestedTargetPath string
	Confidence          float64
	BoundaryReason      string
	MatchedPages        []string
	SessionID           string
	QuestionMessageID   string
	AnswerMessageID     string
	QuestionCreatedAt   string
	AnswerCreatedAt     string
	AnswerMode          string
	EvidenceConfidence  float64
	RetrievedPages      []string
	ConversationExcerpt []string
}

type ReviewApproveRequest struct {
	Question   string
	Answer     string
	TargetPath string
}

type ReviewRejectRequest struct {
	Reason string
}

func NewReviewQueueService(deps Deps) *ReviewQueueService {
	return &ReviewQueueService{baseService: newBaseService(deps)}
}

func (s *ReviewQueueService) PendingCount(_ context.Context) (int, error) {
	items, err := s.pendingItems()
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

func (s *ReviewQueueService) Next(_ context.Context, cursor string) (*ReviewNextResponse, error) {
	items, err := s.pendingItems()
	if err != nil {
		return nil, err
	}
	targets, err := s.reviewTargets()
	if err != nil {
		return nil, err
	}
	resp := &ReviewNextResponse{PendingCount: len(items), TargetPaths: targets}
	if len(items) == 0 {
		return resp, nil
	}
	index := 0
	cursor = strings.TrimSpace(cursor)
	if cursor != "" {
		for i, item := range items {
			if item.ID == cursor {
				index = i + 1
				break
			}
		}
		if index >= len(items) {
			index = 0
		}
	}
	resp.Item = &items[index]
	resp.RemainingCount = len(items) - index - 1
	return resp, nil
}

func (s *ReviewQueueService) CreatePending(_ context.Context, req ReviewCreateRequest) (*ReviewItem, error) {
	question := strings.TrimSpace(req.Question)
	answer := strings.TrimSpace(req.DraftAnswer)
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}
	if answer == "" {
		return nil, fmt.Errorf("draft answer is required")
	}
	targetPath, err := s.normalizeReviewTargetPath(req.SuggestedTargetPath)
	if err != nil {
		targetPath = s.defaultReviewTargetPath()
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := reviewIDForMessage(req, question, answer, now)
	rel := "wiki/unconfirmed/" + id + ".md"
	abs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(rel))
	baseID := id
	for suffix := 2; ; suffix++ {
		if _, err := os.Stat(abs); os.IsNotExist(err) {
			break
		} else if err != nil {
			return nil, err
		}
		id = fmt.Sprintf("%s-%d", baseID, suffix)
		rel = "wiki/unconfirmed/" + id + ".md"
		abs = filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(rel))
	}
	confidence := clampConfidence(req.Confidence)
	if confidence <= 0 {
		confidence = 0.45
	}
	retrievedPages := trimStringSlice(firstNonEmptyStringSlice(req.RetrievedPages, req.MatchedPages), 0)
	doc := &wikiadapter.Document{
		Frontmatter: map[string]any{
			"type":                  "unconfirmed-knowledge",
			"status":                "pending",
			"question":              oneLineFrontmatter(question),
			"original_question":     oneLineFrontmatter(req.OriginalQuestion),
			"draft_answer":          oneLineFrontmatter(answer),
			"created_at":            now,
			"confidence":            confidence,
			"matched_pages":         trimStringSlice(req.MatchedPages, 0),
			"suggested_target_path": targetPath,
			"boundary_reason":       oneLineFrontmatter(req.BoundaryReason),
			"session_id":            oneLineFrontmatter(req.SessionID),
			"question_message_id":   oneLineFrontmatter(req.QuestionMessageID),
			"answer_message_id":     oneLineFrontmatter(req.AnswerMessageID),
			"question_created_at":   oneLineFrontmatter(req.QuestionCreatedAt),
			"answer_created_at":     oneLineFrontmatter(req.AnswerCreatedAt),
			"answer_mode":           oneLineFrontmatter(req.AnswerMode),
			"evidence_confidence":   clampConfidence(req.EvidenceConfidence),
			"retrieved_pages":       retrievedPages,
			"conversation_excerpt":  trimStringSlice(req.ConversationExcerpt, 0),
			"graph-excluded":        true,
		},
		Body: buildPendingReviewBody(question, answer, req, targetPath, retrievedPages),
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(abs, []byte(wikiadapter.RenderDocument(doc)), 0o644); err != nil {
		return nil, err
	}
	return s.readReviewItem(rel)
}

func (s *ReviewQueueService) MatchForbidden(_ context.Context, question string) (*ReviewItem, bool, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, false, nil
	}
	items, err := s.reviewItemsInDir("wiki/forbidden")
	if err != nil {
		return nil, false, err
	}
	for _, item := range items {
		if forbiddenQuestionSimilar(question, item.Question) {
			return &item, true, nil
		}
	}
	return nil, false, nil
}

func (s *ReviewQueueService) Approve(ctx context.Context, id string, req ReviewApproveRequest) (*ReviewItem, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("review id is required")
	}
	item, err := s.readReviewItem("wiki/unconfirmed/" + id + ".md")
	if err != nil {
		return nil, err
	}
	question := firstNonEmpty(strings.TrimSpace(req.Question), item.Question)
	answer := firstNonEmpty(strings.TrimSpace(req.Answer), item.DraftAnswer)
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}
	if answer == "" {
		return nil, fmt.Errorf("answer is required")
	}
	targetPath, err := s.normalizeReviewTargetPath(firstNonEmpty(req.TargetPath, item.SuggestedTargetPath))
	if err != nil {
		return nil, err
	}
	targetBackup, err := s.backupWikiFile(targetPath)
	if err != nil {
		return nil, err
	}
	sourcePath := reviewSourceArchivePath(id)
	sourceBackup, err := s.backupWikiFile(sourcePath)
	if err != nil {
		return nil, err
	}
	reviewedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.writeReviewSourceArchive(sourcePath, item, "approved", question, answer, targetPath, reviewedAt, "管理员审查通过"); err != nil {
		return nil, err
	}
	if err := s.applyApprovedReviewViaLLM(ctx, item, question, answer, targetPath, sourcePath, reviewedAt); err != nil {
		_ = s.restoreWikiFile(sourceBackup)
		_ = s.restoreWikiFile(targetBackup)
		return nil, err
	}
	if err := s.runQMDUpdate(ctx, question); err != nil {
		sourceRollbackErr := s.restoreWikiFile(sourceBackup)
		targetRollbackErr := s.restoreWikiFile(targetBackup)
		if sourceRollbackErr != nil || targetRollbackErr != nil {
			rollbackErr := firstNonNilError(sourceRollbackErr, targetRollbackErr)
			return nil, fmt.Errorf("qmd update failed and knowledge rollback failed: %w; rollback error: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("知识页已回滚，qmd update 失败，请修复 qmd 后重试: %w", err)
	}
	if err := os.Remove(filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(item.Path))); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	s.afterReviewIngest(ctx, "approve", targetPath, sourcePath, question)
	item.Question = question
	item.DraftAnswer = answer
	item.SuggestedTargetPath = targetPath
	return item, nil
}

func (s *ReviewQueueService) Reject(ctx context.Context, id string, req ReviewRejectRequest) (*ReviewItem, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("review id is required")
	}
	item, err := s.readReviewItem("wiki/unconfirmed/" + id + ".md")
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "管理员驳回"
	}
	targetRel := "wiki/forbidden/" + id + ".md"
	targetBackup, err := s.backupWikiFile(targetRel)
	if err != nil {
		return nil, err
	}
	targetAbs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(targetRel))
	reviewedAt := time.Now().UTC().Format(time.RFC3339Nano)
	doc := &wikiadapter.Document{
		Frontmatter: map[string]any{
			"type":                 "forbidden-question",
			"status":               "rejected",
			"ingest_status":        "review-rejected",
			"review_id":            id,
			"source_path":          item.Path,
			"question":             oneLineFrontmatter(item.Question),
			"original_question":    oneLineFrontmatter(item.OriginalQuestion),
			"rejected_answer":      oneLineFrontmatter(item.DraftAnswer),
			"reason":               oneLineFrontmatter(reason),
			"created_at":           item.CreatedAt,
			"reviewed_at":          reviewedAt,
			"confidence":           item.Confidence,
			"answer_mode":          oneLineFrontmatter(item.AnswerMode),
			"session_id":           oneLineFrontmatter(item.SessionID),
			"question_message_id":  oneLineFrontmatter(item.QuestionMessageID),
			"answer_message_id":    oneLineFrontmatter(item.AnswerMessageID),
			"question_created_at":  oneLineFrontmatter(item.QuestionCreatedAt),
			"answer_created_at":    oneLineFrontmatter(item.AnswerCreatedAt),
			"conversation_excerpt": trimStringSlice(item.ConversationExcerpt, 0),
			"graph-excluded":       true,
		},
		Body: buildForbiddenReviewBody(item, reason, reviewedAt),
	}
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(targetAbs, []byte(wikiadapter.RenderDocument(doc)), 0o644); err != nil {
		return nil, err
	}
	if err := s.runQMDUpdate(ctx, item.Question); err != nil {
		if rollbackErr := s.restoreWikiFile(targetBackup); rollbackErr != nil {
			return nil, fmt.Errorf("qmd update failed and forbidden rollback failed: %w; rollback error: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("Forbidden 已回滚，qmd update 失败，请修复 qmd 后重试: %w", err)
	}
	if err := os.Remove(filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(item.Path))); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	s.afterReviewIngest(ctx, "reject", targetRel, "", item.Question)
	item.Path = targetRel
	return item, nil
}

func (s *ReviewQueueService) Delete(_ context.Context, id string) (*ReviewItem, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("review id is required")
	}
	item, err := s.readReviewItem("wiki/unconfirmed/" + id + ".md")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(item.Path))); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return item, nil
}

func (s *ReviewQueueService) pendingItems() ([]ReviewItem, error) {
	items, err := s.reviewItemsInDir("wiki/unconfirmed")
	if err != nil {
		return nil, err
	}
	out := make([]ReviewItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Question) != "" {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}

func (s *ReviewQueueService) reviewItemsInDir(relDir string) ([]ReviewItem, error) {
	root := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(relDir))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := []ReviewItem{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		item, err := s.readReviewItem(filepath.ToSlash(filepath.Join(relDir, entry.Name())))
		if err == nil {
			items = append(items, *item)
		}
	}
	return items, nil
}

func (s *ReviewQueueService) readReviewItem(rel string) (*ReviewItem, error) {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return nil, fmt.Errorf("invalid review path")
	}
	abs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(clean))
	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	doc, err := wikiadapter.ParseDocument(string(raw))
	if err != nil {
		return nil, err
	}
	id := strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean))
	item := &ReviewItem{
		ID:                  id,
		Path:                clean,
		Question:            stringFromFrontmatter(doc.Frontmatter, "question"),
		OriginalQuestion:    stringFromFrontmatter(doc.Frontmatter, "original_question"),
		DraftAnswer:         stringFromFrontmatter(doc.Frontmatter, "draft_answer"),
		SuggestedTargetPath: stringFromFrontmatter(doc.Frontmatter, "suggested_target_path"),
		BoundaryReason:      stringFromFrontmatter(doc.Frontmatter, "boundary_reason"),
		MatchedPages:        stringsFromFrontmatter(doc.Frontmatter, "matched_pages"),
		CreatedAt:           stringFromFrontmatter(doc.Frontmatter, "created_at"),
		SessionID:           stringFromFrontmatter(doc.Frontmatter, "session_id"),
		QuestionMessageID:   stringFromFrontmatter(doc.Frontmatter, "question_message_id"),
		AnswerMessageID:     stringFromFrontmatter(doc.Frontmatter, "answer_message_id"),
		QuestionCreatedAt:   stringFromFrontmatter(doc.Frontmatter, "question_created_at"),
		AnswerCreatedAt:     stringFromFrontmatter(doc.Frontmatter, "answer_created_at"),
		AnswerMode:          stringFromFrontmatter(doc.Frontmatter, "answer_mode"),
		RetrievedPages:      stringsFromFrontmatter(doc.Frontmatter, "retrieved_pages"),
		ConversationExcerpt: stringsFromFrontmatter(doc.Frontmatter, "conversation_excerpt"),
	}
	if item.Question == "" {
		item.Question = extractMarkdownSection(doc.Body, "## Question")
	}
	if item.DraftAnswer == "" {
		item.DraftAnswer = extractMarkdownSection(doc.Body, "## Draft Answer")
	}
	item.Confidence = floatFromFrontmatter(doc.Frontmatter, "confidence")
	item.EvidenceConfidence = floatFromFrontmatter(doc.Frontmatter, "evidence_confidence")
	if len(item.RetrievedPages) == 0 {
		item.RetrievedPages = item.MatchedPages
	}
	if item.CreatedAt == "" {
		item.CreatedAt = "1970-01-01T00:00:00Z"
	}
	return item, nil
}

func (s *ReviewQueueService) reviewTargets() ([]ReviewTarget, error) {
	targets := []ReviewTarget{}
	for _, dir := range reviewWritableDirs() {
		root := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(dir))
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			rel := filepath.ToSlash(filepath.Join(dir, entry.Name()))
			targets = append(targets, ReviewTarget{Path: rel, Title: s.targetTitle(rel)})
		}
	}
	defaultPath := s.defaultReviewTargetPath()
	foundDefault := false
	for _, target := range targets {
		if target.Path == defaultPath {
			foundDefault = true
			break
		}
	}
	if !foundDefault {
		targets = append(targets, ReviewTarget{Path: defaultPath, Title: s.defaultReviewTargetTitle()})
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Title == targets[j].Title {
			return targets[i].Path < targets[j].Path
		}
		return targets[i].Title < targets[j].Title
	})
	return targets, nil
}

func (s *ReviewQueueService) applyApprovedReviewViaLLM(ctx context.Context, item *ReviewItem, question string, answer string, targetPath string, sourceArchivePath string, reviewedAt string) error {
	if s.deps.LLM == nil {
		return fmt.Errorf("review approval requires an LLM client")
	}
	execution := NewExecution("review-approve")
	_, err := NewDirectAdminService(s.deps).Run(ctx, execution, "review-approve-"+stableShortHash(item.ID+question), DirectAdminRequest{
		Message:  buildReviewApprovalDirectMessage(item, question, answer, targetPath, sourceArchivePath, reviewedAt),
		ModeHint: "ingest",
		Context: map[string]any{
			"stored_path":   sourceArchivePath,
			"path":          sourceArchivePath,
			"file_name":     filepath.Base(sourceArchivePath),
			"source_format": "human-review",
		},
	})
	if err != nil {
		return err
	}
	if err := s.verifyReviewTargetUpdated(targetPath, sourceArchivePath); err != nil {
		return err
	}
	return nil
}

func buildReviewApprovalDirectMessage(item *ReviewItem, question string, answer string, targetPath string, sourceArchivePath string, reviewedAt string) string {
	return strings.Join([]string{
		"请按 AGENT.md 将人工审查通过的知识沉淀到正式知识页或意图页。",
		"",
		"要求：",
		"- 读取并保留来源归档中的关键信息，raw/ 只读。",
		"- 创建或更新 target_path 指向的正式页面，并保持 source_pages 可追溯到 source_archive_path。",
		"- 不追加问答模板章节；按 AGENT.md 的知识页、政策页、流程页、对比页、概念页、实体页、综合页或意图页结构沉淀。",
		"- 更新 frontmatter 中的 source_pages 和 last_verified；必要时维护 wikilink、index/log。服务端会在你完成后执行 qmd update。",
		"- 完成后返回 final，并在 artifacts 或 output_files 中列出更新页面。",
		"",
		"review_id: " + item.ID,
		"reviewed_at: " + reviewedAt,
		"target_path: " + targetPath,
		"source_archive_path: " + sourceArchivePath,
		"question:",
		strings.TrimSpace(question),
		"",
		"confirmed_answer:",
		strings.TrimSpace(answer),
	}, "\n")
}

func (s *ReviewQueueService) verifyReviewTargetUpdated(targetPath string, sourceArchivePath string) error {
	abs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(targetPath))
	raw, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("LLM did not create or update target knowledge page %s: %w", targetPath, err)
	}
	content := string(raw)
	if !strings.Contains(content, sourceArchivePath) {
		return fmt.Errorf("LLM-updated target page %s does not reference source archive %s", targetPath, sourceArchivePath)
	}
	return nil
}

func (s *ReviewQueueService) writeReviewSourceArchive(rel string, item *ReviewItem, status string, finalQuestion string, finalAnswer string, targetPath string, reviewedAt string, reason string) error {
	abs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(rel))
	doc := &wikiadapter.Document{
		Frontmatter: map[string]any{
			"title":                item.ID + " 人工审查来源归档",
			"type":                 "source",
			"source_kind":          "human-review",
			"status":               status,
			"review_id":            item.ID,
			"raw_file":             item.Path,
			"target_path":          targetPath,
			"question":             oneLineFrontmatter(finalQuestion),
			"original_question":    oneLineFrontmatter(firstNonEmpty(item.OriginalQuestion, item.Question)),
			"draft_answer":         oneLineFrontmatter(item.DraftAnswer),
			"final_answer":         oneLineFrontmatter(finalAnswer),
			"created_at":           item.CreatedAt,
			"reviewed_at":          reviewedAt,
			"confidence":           "high",
			"answer_mode":          oneLineFrontmatter(item.AnswerMode),
			"evidence_confidence":  item.EvidenceConfidence,
			"matched_pages":        trimStringSlice(item.MatchedPages, 0),
			"retrieved_pages":      trimStringSlice(item.RetrievedPages, 0),
			"session_id":           oneLineFrontmatter(item.SessionID),
			"question_message_id":  oneLineFrontmatter(item.QuestionMessageID),
			"answer_message_id":    oneLineFrontmatter(item.AnswerMessageID),
			"question_created_at":  oneLineFrontmatter(item.QuestionCreatedAt),
			"answer_created_at":    oneLineFrontmatter(item.AnswerCreatedAt),
			"conversation_excerpt": trimStringSlice(item.ConversationExcerpt, 0),
		},
		Body: buildReviewSourceArchiveBody(item, status, finalQuestion, finalAnswer, targetPath, reviewedAt, reason),
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(wikiadapter.RenderDocument(doc)), 0o644)
}

func (s *ReviewQueueService) afterReviewIngest(ctx context.Context, action string, path string, sourcePath string, question string) {
	env := s.env("admin", "review-ingest-"+stableShortHash(action+question), "", "")
	indexEntry := fmt.Sprintf("- %s | review-%s | %s", nowDate(), action, path)
	if sourcePath != "" {
		indexEntry += " | source=" + sourcePath
	}
	_, _ = s.deps.Runtime.Execute(ctx, env, runtimeCall("wiki.update_index_entry", map[string]any{
		"section": "## Review Ingest",
		"entry":   indexEntry,
	}))
	logLine := fmt.Sprintf("%s | ingest | review-%s | %s", nowDate(), action, path)
	if sourcePath != "" {
		logLine += " | source=" + sourcePath
	}
	_, _ = s.deps.Runtime.Execute(ctx, env, runtimeCall("wiki.append_log", map[string]any{"line": logLine}))
}

type wikiFileBackup struct {
	Rel     string
	Existed bool
	Content []byte
}

func reviewSourceArchivePath(reviewID string) string {
	return "wiki/sources/" + strings.TrimSuffix(strings.TrimSpace(reviewID), ".md") + ".md"
}

func (s *ReviewQueueService) backupWikiFile(rel string) (wikiFileBackup, error) {
	backup := wikiFileBackup{Rel: rel}
	abs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return backup, nil
		}
		return backup, err
	}
	backup.Existed = true
	backup.Content = append([]byte(nil), raw...)
	return backup, nil
}

func (s *ReviewQueueService) restoreWikiFile(backup wikiFileBackup) error {
	abs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(backup.Rel))
	if !backup.Existed {
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, backup.Content, 0o644)
}

func firstNonNilError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *ReviewQueueService) runQMDUpdate(ctx context.Context, question string) error {
	env := s.env("admin", "review-qmd-"+stableShortHash(question), "", "")
	result, err := s.deps.Runtime.Execute(ctx, env, runtimeCall("exec.qmd", map[string]any{"subcommand": "update"}))
	if err != nil {
		return err
	}
	if result.Success {
		return nil
	}
	message := "qmd update failed"
	if result.Error != nil && strings.TrimSpace(result.Error.Message) != "" {
		message = result.Error.Message
	}
	if stderr, _ := result.Data["stderr"].(string); strings.TrimSpace(stderr) != "" {
		message += ": " + strings.TrimSpace(stderr)
	}
	return fmt.Errorf("%s", message)
}

func (s *ReviewQueueService) normalizeReviewTargetPath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = s.defaultReviewTargetPath()
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	clean = strings.TrimPrefix(clean, "./")
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid target path")
	}
	if !strings.HasSuffix(clean, ".md") {
		return "", fmt.Errorf("target_path must be a markdown file")
	}
	allowed := false
	for _, dir := range reviewWritableDirs() {
		if strings.HasPrefix(clean, dir+"/") {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("target_path must be under an official knowledge directory")
	}
	slug := strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean))
	if !wikiadapter.IsValidSlug(slug) {
		return "", fmt.Errorf("target_path filename must be a valid slug")
	}
	return clean, nil
}

func reviewWritableDirs() []string {
	return []string{
		"wiki/knowledge",
		"wiki/policies",
		"wiki/procedures",
		"wiki/comparisons",
		"wiki/concepts",
		"wiki/entities",
		"wiki/synthesis",
		"wiki/intents",
	}
}

func (s *ReviewQueueService) defaultReviewTargetPath() string {
	return "wiki/intents/pending-customer-questions.md"
}

func (s *ReviewQueueService) defaultReviewTargetTitle() string {
	return "待沉淀用户意图"
}

func (s *ReviewQueueService) targetTitle(targetPath string) string {
	if targetPath == s.defaultReviewTargetPath() {
		return s.defaultReviewTargetTitle()
	}
	abs := filepath.Join(s.deps.Config.MountedWiki.Root, filepath.FromSlash(targetPath))
	if raw, err := os.ReadFile(abs); err == nil {
		if doc, parseErr := wikiadapter.ParseDocument(string(raw)); parseErr == nil {
			if value, _ := doc.Frontmatter["title"].(string); strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	slug := strings.TrimSuffix(filepath.Base(targetPath), filepath.Ext(targetPath))
	return humanizeSlug(slug)
}

func buildPendingReviewBody(question string, answer string, req ReviewCreateRequest, targetPath string, retrievedPages []string) string {
	lines := []string{
		"# 待沉淀知识",
		"",
		"## Question",
		"",
		strings.TrimSpace(question),
		"",
		"## Original Question",
		"",
		firstNonEmpty(strings.TrimSpace(req.OriginalQuestion), strings.TrimSpace(question)),
		"",
		"## Draft Answer",
		"",
		strings.TrimSpace(answer),
		"",
		"## Boundary",
		"",
		firstNonEmpty(strings.TrimSpace(req.BoundaryReason), "LLM 低可信回答，等待管理员确认。"),
		"",
		"## Suggested Target Path",
		"",
		targetPath,
		"",
		"## Metadata",
		"",
		"- Session ID: " + emptyAsDash(req.SessionID),
		"- Question Message ID: " + emptyAsDash(req.QuestionMessageID),
		"- Answer Message ID: " + emptyAsDash(req.AnswerMessageID),
		"- Question Created At: " + emptyAsDash(req.QuestionCreatedAt),
		"- Answer Created At: " + emptyAsDash(req.AnswerCreatedAt),
		"- Answer Mode: " + emptyAsDash(req.AnswerMode),
		"- Confidence: " + fmt.Sprintf("%.2f", clampConfidence(req.Confidence)),
		"- Evidence Confidence: " + fmt.Sprintf("%.2f", clampConfidence(req.EvidenceConfidence)),
		"",
		"## Matched Pages",
		"",
	}
	if len(req.MatchedPages) == 0 {
		lines = append(lines, "- 暂无")
	} else {
		for _, path := range req.MatchedPages {
			if strings.TrimSpace(path) != "" {
				lines = append(lines, "- "+strings.TrimSpace(path))
			}
		}
	}
	lines = append(lines, "", "## Retrieved Pages", "")
	if len(retrievedPages) == 0 {
		lines = append(lines, "- 暂无")
	} else {
		for _, path := range retrievedPages {
			if strings.TrimSpace(path) != "" {
				lines = append(lines, "- "+strings.TrimSpace(path))
			}
		}
	}
	lines = append(lines, "", "## Conversation Excerpt", "")
	if len(req.ConversationExcerpt) == 0 {
		lines = append(lines, "- 暂无")
	} else {
		for _, item := range req.ConversationExcerpt {
			if strings.TrimSpace(item) != "" {
				lines = append(lines, "- "+strings.TrimSpace(item))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func buildReviewSourceArchiveBody(item *ReviewItem, status string, finalQuestion string, finalAnswer string, targetPath string, reviewedAt string, reason string) string {
	lines := []string{
		"# 人工审查来源归档",
		"",
		"## Status",
		"",
		status,
		"",
		"## Final Question",
		"",
		strings.TrimSpace(finalQuestion),
		"",
		"## Final Answer",
		"",
		strings.TrimSpace(finalAnswer),
		"",
		"## Draft Question",
		"",
		strings.TrimSpace(firstNonEmpty(item.OriginalQuestion, item.Question)),
		"",
		"## Draft Answer",
		"",
		strings.TrimSpace(item.DraftAnswer),
		"",
		"## Review",
		"",
		"- Review ID: " + emptyAsDash(item.ID),
		"- Source Path: " + emptyAsDash(item.Path),
		"- Target Path: " + emptyAsDash(targetPath),
		"- Reviewed At: " + emptyAsDash(reviewedAt),
		"- Reason: " + emptyAsDash(reason),
		"- Confidence: " + fmt.Sprintf("%.2f", clampConfidence(item.Confidence)),
		"- Answer Mode: " + emptyAsDash(item.AnswerMode),
		"",
		"## Conversation Excerpt",
		"",
	}
	if len(item.ConversationExcerpt) == 0 {
		lines = append(lines, "- 暂无")
	} else {
		for _, excerpt := range item.ConversationExcerpt {
			if strings.TrimSpace(excerpt) != "" {
				lines = append(lines, "- "+strings.TrimSpace(excerpt))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func buildForbiddenReviewBody(item *ReviewItem, reason string, reviewedAt string) string {
	lines := []string{
		"# 禁止回复问题",
		"",
		"## Question",
		"",
		strings.TrimSpace(item.Question),
		"",
		"## Rejected Answer",
		"",
		strings.TrimSpace(item.DraftAnswer),
		"",
		"## Reason",
		"",
		strings.TrimSpace(reason),
		"",
		"## Review",
		"",
		"- Review ID: " + emptyAsDash(item.ID),
		"- Source Path: " + emptyAsDash(item.Path),
		"- Reviewed At: " + emptyAsDash(reviewedAt),
		"- Confidence: " + fmt.Sprintf("%.2f", clampConfidence(item.Confidence)),
		"- Answer Mode: " + emptyAsDash(item.AnswerMode),
	}
	if len(item.ConversationExcerpt) > 0 {
		lines = append(lines, "", "## Conversation Excerpt", "")
		for _, excerpt := range item.ConversationExcerpt {
			if strings.TrimSpace(excerpt) != "" {
				lines = append(lines, "- "+strings.TrimSpace(excerpt))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func forbiddenQuestionSimilar(left string, right string) bool {
	a := normalizeReviewQuestion(left)
	b := normalizeReviewQuestion(right)
	if a == "" || b == "" {
		return false
	}
	if a == b || strings.Contains(a, b) || strings.Contains(b, a) {
		return true
	}
	return bigramJaccard(a, b) >= 0.82
}

func normalizeReviewQuestion(text string) string {
	text = strings.ReplaceAll(text, "如何", "怎么")
	text = strings.ReplaceAll(text, "怎样", "怎么")
	text = strings.ReplaceAll(text, "咋样", "怎么")
	text = strings.ReplaceAll(text, "咋", "怎么")
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case unicode.Is(unicode.Han, r), unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

func bigramJaccard(left string, right string) float64 {
	a := runeBigrams(left)
	b := runeBigrams(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for item := range a {
		if b[item] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union <= 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func runeBigrams(text string) map[string]bool {
	runes := []rune(text)
	if len(runes) < 2 {
		return nil
	}
	out := map[string]bool{}
	for i := 0; i < len(runes)-1; i++ {
		out[string(runes[i:i+2])] = true
	}
	return out
}

func reviewIDForMessage(req ReviewCreateRequest, question string, answer string, createdAt string) string {
	timestamp := time.Now().UTC().Format("20060102T150405")
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(createdAt)); err == nil {
		timestamp = parsed.UTC().Format("20060102T150405")
	}
	seed := strings.Join([]string{
		req.SessionID,
		req.QuestionMessageID,
		req.AnswerMessageID,
		question,
		answer,
		createdAt,
	}, "|")
	return "review-" + timestamp + "-" + stableShortHash(seed)
}

func firstNonEmptyStringSlice(values ...[]string) []string {
	for _, value := range values {
		if len(trimStringSlice(value, 0)) > 0 {
			return value
		}
	}
	return nil
}

func emptyAsDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func oneLineFrontmatter(text string) string {
	text = strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.Join(strings.Fields(strings.ReplaceAll(text, "\n", " ")), " ")
	return text
}

func stringFromFrontmatter(frontmatter map[string]any, key string) string {
	if value, ok := frontmatter[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func stringsFromFrontmatter(frontmatter map[string]any, key string) []string {
	raw, ok := frontmatter[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return trimStringSlice(typed, 0)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprintf("%v", item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func floatFromFrontmatter(frontmatter map[string]any, key string) float64 {
	switch typed := frontmatter[key].(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		value, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return value
	default:
		return 0
	}
}

func extractMarkdownSection(body string, heading string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == strings.TrimSpace(heading) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}
