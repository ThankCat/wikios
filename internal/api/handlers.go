package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"wikios/internal/app/middleware"
	"wikios/internal/config"
	"wikios/internal/service"
	"wikios/internal/store"
)

type Handlers struct {
	PublicQuery    *service.PublicQueryService
	DirectAdmin    *service.DirectAdminService
	Upload         *service.UploadService
	Sync           *service.SyncService
	Store          *store.Store
	AuthConfig     config.AuthConfig
	Config         *config.Config
	PublicIntents  *service.PublicIntentManager
	ContextCounter *service.ContextCounter
}

func NewHandlers(
	publicQuery *service.PublicQueryService,
	directAdmin *service.DirectAdminService,
	uploadSvc *service.UploadService,
	syncSvc *service.SyncService,
	dataStore *store.Store,
	cfg *config.Config,
	authCfg config.AuthConfig,
	publicIntents *service.PublicIntentManager,
	contextCounter *service.ContextCounter,
) *Handlers {
	return &Handlers{
		PublicQuery:    publicQuery,
		DirectAdmin:    directAdmin,
		Upload:         uploadSvc,
		Sync:           syncSvc,
		Store:          dataStore,
		AuthConfig:     authCfg,
		Config:         cfg,
		PublicIntents:  publicIntents,
		ContextCounter: contextCounter,
	}
}

type adminChatRequest struct {
	Message     string         `json:"message"`
	Stream      bool           `json:"stream"`
	ModeHint    string         `json:"mode_hint"`
	Context     map[string]any `json:"context"`
	Attachments []attachment   `json:"attachments"`
	History     []chatMessage  `json:"history"`
}

type publicAnswerRequest struct {
	Question  string         `json:"question"`
	UserID    string         `json:"user_id"`
	SessionID string         `json:"session_id"`
	Context   map[string]any `json:"context"`
	History   []chatMessage  `json:"history"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type attachment struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

const chatHistoryLimit = 8

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type publicIntentsUpdateRequest struct {
	Source string `json:"source"`
}

type adminContextEstimateRequest adminChatRequest

type syncCommitRequest struct {
	Message string   `json:"message"`
	Paths   []string `json:"paths"`
}

type syncPushRequest struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}

type syncGenerateMessageRequest struct {
	Paths []string `json:"paths"`
}

type sseEmitter struct {
	c  *gin.Context
	mu *sync.Mutex
}

func (e *sseEmitter) Emit(event service.StreamEvent) {
	writeSSEWithLock(e.c, event.Type, event.Data, e.mu)
}

func (h *Handlers) PublicAnswer(c *gin.Context) {
	var req publicAnswerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	resp, err := h.PublicQuery.Answer(c.Request.Context(), traceID(c), service.PublicAnswerRequest{
		Question:  req.Question,
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Context:   req.Context,
		History:   toServiceHistory(req.History),
	})
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) PublicAnswerStream(c *gin.Context) {
	var req publicAnswerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	writeSSE(c, "meta", gin.H{"stream": true})
	resp, err := h.PublicQuery.Answer(c.Request.Context(), traceID(c), service.PublicAnswerRequest{
		Question:  req.Question,
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Context:   req.Context,
		History:   toServiceHistory(req.History),
	})
	if err != nil {
		writeSSE(c, "error", gin.H{"message": err.Error()})
		writeSSE(c, "done", gin.H{"ok": false})
		return
	}
	for _, chunk := range chunkText(resp.Answer, 24) {
		writeSSE(c, "delta", gin.H{"delta": chunk})
	}
	writeSSE(c, "result", gin.H{"answer": resp.Answer})
	writeSSE(c, "done", gin.H{"ok": true})
}

func (h *Handlers) AdminLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	user, err := h.Store.AuthenticateAdmin(c.Request.Context(), strings.TrimSpace(req.Username), req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"code": "UNAUTHORIZED", "message": err.Error()},
		})
		return
	}
	token := "sess_" + uuid.NewString()
	expiresAt := time.Now().Add(time.Duration(h.AuthConfig.SessionTTLHours) * time.Hour)
	if err := h.Store.CreateSession(c.Request.Context(), store.AdminSession{
		Token:     token,
		UserID:    user.ID,
		ExpiresAt: expiresAt,
	}); err != nil {
		internalError(c, err)
		return
	}
	c.SetCookie(h.AuthConfig.SessionCookieName, token, int(time.Until(expiresAt).Seconds()), "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":       user.ID,
			"username": user.Username,
		},
	})
}

func (h *Handlers) AdminLogout(c *gin.Context) {
	if token, err := c.Cookie(h.AuthConfig.SessionCookieName); err == nil && token != "" {
		_ = h.Store.DeleteSession(c.Request.Context(), token)
	}
	c.SetCookie(h.AuthConfig.SessionCookieName, "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handlers) AdminMe(c *gin.Context) {
	user, ok := middleware.AdminUser(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{"code": "UNAUTHORIZED", "message": "admin login required"},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":       user.ID,
			"username": user.Username,
		},
	})
}

func (h *Handlers) AdminGetPublicIntents(c *gin.Context) {
	if h.PublicIntents == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "PUBLIC_INTENTS_UNAVAILABLE", "message": "public intents are not configured"},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"source": h.PublicIntents.SourceOrDefault(),
		"status": h.PublicIntents.Status(),
	})
}

func (h *Handlers) AdminUpdatePublicIntents(c *gin.Context) {
	if h.PublicIntents == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "PUBLIC_INTENTS_UNAVAILABLE", "message": "public intents are not configured"},
		})
		return
	}
	var req publicIntentsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	status, err := h.PublicIntents.Save(req.Source)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_PUBLIC_INTENTS",
				"message": err.Error(),
			},
			"status": status,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"source": h.PublicIntents.SourceOrDefault(),
		"status": status,
	})
}

func (h *Handlers) AdminUpload(c *gin.Context) {
	req, err := readUploadRequest(c)
	if err != nil {
		var validationErr service.ValidationError
		if errors.As(err, &validationErr) {
			badRequest(c, validationErr)
			return
		}
		badRequest(c, err)
		return
	}
	result, err := h.Upload.Save(c.Request.Context(), traceID(c), req)
	if err != nil {
		var validationErr service.ValidationError
		if errors.As(err, &validationErr) {
			badRequest(c, validationErr)
			return
		}
		var executionErr service.ExecutionError
		if errors.As(err, &executionErr) {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": gin.H{
					"code":    "INTERNAL_ERROR",
					"message": executionErr.Error(),
				},
				"details": executionErr.Details,
			})
			return
		}
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reply":     adminDisplayReply("upload", result),
		"details":   result,
		"execution": result["execution"],
	})
}

func (h *Handlers) AdminUploadStream(c *gin.Context) {
	req, err := readUploadRequest(c)
	if err != nil {
		var validationErr service.ValidationError
		if errors.As(err, &validationErr) {
			badRequest(c, validationErr)
			return
		}
		badRequest(c, err)
		return
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	mu := &sync.Mutex{}
	stopKeepalive := startSSEKeepalive(c, mu, 8*time.Second)
	defer stopKeepalive()

	streamCtx := service.WithStreamEmitter(c.Request.Context(), &sseEmitter{c: c, mu: mu})
	_, execution, err := h.Upload.SaveStream(streamCtx, traceID(c), req)
	if err != nil {
		writeSSEWithLock(c, "error", gin.H{"message": err.Error()}, mu)
		writeSSEWithLock(c, "done", gin.H{"execution": execution}, mu)
		return
	}
	writeSSEWithLock(c, "done", gin.H{"execution": execution}, mu)
}

func readUploadRequest(c *gin.Context) (service.UploadRequest, error) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return service.UploadRequest{}, fmt.Errorf("parse upload file: %w", err)
	}
	file, err := fileHeader.Open()
	if err != nil {
		return service.UploadRequest{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return service.UploadRequest{}, err
	}
	return service.UploadRequest{
		Filename:    fileHeader.Filename,
		ContentType: fileHeader.Header.Get("Content-Type"),
		Content:     data,
	}, nil
}

func (h *Handlers) AdminChat(c *gin.Context) {
	var req adminChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	mode := firstNonEmpty(strings.TrimSpace(req.ModeHint), detectAdminMode(req.Message, req.Context, req.Attachments))
	directReq := h.buildDirectAdminRequest(req, mode)
	contextUsage := h.estimateAdminContext(directReq)
	if contextUsage.Blocked {
		contextLimitExceeded(c, contextUsage)
		return
	}
	execution := service.NewExecution(mode)
	result, err := h.DirectAdmin.Run(c.Request.Context(), execution, traceID(c), directReq)
	if err != nil {
		execution.Status = service.ExecutionFailed
		execution.Error = err.Error()
		execution.EndedAt = time.Now()
		internalError(c, err)
		return
	}
	execution.Status = service.ExecutionSuccess
	execution.EndedAt = time.Now()
	c.JSON(http.StatusOK, gin.H{
		"mode":          mode,
		"reply":         adminDisplayReply(mode, result),
		"details":       result,
		"execution":     execution,
		"context_usage": contextUsage,
	})
}

func (h *Handlers) AdminChatStream(c *gin.Context) {
	var req adminChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	mode := firstNonEmpty(strings.TrimSpace(req.ModeHint), detectAdminMode(req.Message, req.Context, req.Attachments))
	execution := service.NewExecution(mode)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	mu := &sync.Mutex{}
	stopKeepalive := startSSEKeepalive(c, mu, 8*time.Second)
	defer stopKeepalive()
	directReq := h.buildDirectAdminRequest(req, mode)
	contextUsage := h.estimateAdminContext(directReq)
	if contextUsage.Blocked {
		execution.Status = service.ExecutionFailed
		execution.Error = "CONTEXT_LIMIT_EXCEEDED"
		execution.EndedAt = time.Now()
		writeSSEWithLock(c, "error", gin.H{
			"code":          "CONTEXT_LIMIT_EXCEEDED",
			"message":       "当前对话已接近上下文上限，请创建新的对话继续。",
			"context_usage": contextUsage,
		}, mu)
		writeSSEWithLock(c, "done", gin.H{"ok": false, "execution": execution}, mu)
		return
	}
	writeSSEWithLock(c, "meta", gin.H{
		"mode":          mode,
		"execution_id":  execution.ID,
		"started_at":    execution.StartedAt.Format(time.RFC3339Nano),
		"context_usage": contextUsage,
	}, mu)
	streamCtx := service.WithStreamEmitter(c.Request.Context(), &sseEmitter{c: c, mu: mu})
	result, err := h.DirectAdmin.Run(streamCtx, execution, traceID(c), directReq)
	execution.EndedAt = time.Now()
	if err != nil {
		execution.Status = service.ExecutionFailed
		execution.Error = err.Error()
		writeSSEWithLock(c, "error", gin.H{"message": err.Error()}, mu)
		writeSSEWithLock(c, "done", gin.H{"execution": execution}, mu)
		return
	}
	execution.Status = service.ExecutionSuccess
	writeSSEWithLock(c, "result", gin.H{
		"mode":          mode,
		"reply":         adminDisplayReply(mode, result),
		"details":       result,
		"execution":     execution,
		"context_usage": contextUsage,
	}, mu)
	writeSSEWithLock(c, "done", gin.H{"execution": execution}, mu)
}

func (h *Handlers) AdminContextEstimate(c *gin.Context) {
	var req adminChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	mode := firstNonEmpty(strings.TrimSpace(req.ModeHint), detectAdminMode(req.Message, req.Context, req.Attachments))
	directReq := h.buildDirectAdminRequest(req, mode)
	c.JSON(http.StatusOK, gin.H{
		"mode":          mode,
		"context_usage": h.estimateAdminContext(directReq),
	})
}

func (h *Handlers) AdminWikiTree(c *gin.Context) {
	abs, rel, err := h.resolveWikiPath(c.Query("path"))
	if err != nil {
		badRequest(c, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		badRequest(c, err)
		return
	}
	if !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "NOT_DIRECTORY", "message": "path is not a directory"}})
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		internalError(c, err)
		return
	}
	items := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		entryInfo, err := entry.Info()
		if err != nil {
			continue
		}
		entryRel := filepath.ToSlash(filepath.Join(rel, entry.Name()))
		items = append(items, gin.H{
			"name":        entry.Name(),
			"path":        entryRel,
			"is_dir":      entry.IsDir(),
			"size":        entryInfo.Size(),
			"modified_at": entryInfo.ModTime().Format(time.RFC3339Nano),
			"preview":     wikiPreviewKind(entryRel),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		leftDir := items[i]["is_dir"].(bool)
		rightDir := items[j]["is_dir"].(bool)
		if leftDir != rightDir {
			return leftDir
		}
		return strings.ToLower(items[i]["name"].(string)) < strings.ToLower(items[j]["name"].(string))
	})
	log.Printf("audit wiki.tree path=%s rel=%s count=%d", c.Query("path"), rel, len(items))
	c.JSON(http.StatusOK, gin.H{"path": rel, "items": items})
}

func (h *Handlers) AdminWikiFile(c *gin.Context) {
	abs, rel, err := h.resolveWikiPath(c.Query("path"))
	if err != nil {
		badRequest(c, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		badRequest(c, err)
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "IS_DIRECTORY", "message": "path is a directory"}})
		return
	}
	kind := wikiPreviewKind(rel)
	resp := gin.H{
		"path":         rel,
		"name":         filepath.Base(rel),
		"size":         info.Size(),
		"modified_at":  info.ModTime().Format(time.RFC3339Nano),
		"preview":      kind,
		"download_url": "/api/v1/admin/wiki/download?path=" + urlQueryEscape(rel),
	}
	if kind == "download" {
		log.Printf("audit wiki.file path=%s preview=download", rel)
		c.JSON(http.StatusOK, resp)
		return
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		internalError(c, err)
		return
	}
	switch kind {
	case "image":
		mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(rel)))
		if mimeType == "" {
			mimeType = http.DetectContentType(content)
		}
		resp["mime_type"] = mimeType
		resp["data_url"] = "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(content)
	default:
		resp["content"] = string(content)
	}
	log.Printf("audit wiki.file path=%s preview=%s size=%d", rel, kind, info.Size())
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) AdminWikiDownload(c *gin.Context) {
	abs, rel, err := h.resolveWikiPath(c.Query("path"))
	if err != nil {
		badRequest(c, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		badRequest(c, err)
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "IS_DIRECTORY", "message": "path is a directory"}})
		return
	}
	log.Printf("audit wiki.download path=%s size=%d", rel, info.Size())
	c.FileAttachment(abs, filepath.Base(rel))
}

func (h *Handlers) AdminSyncStatus(c *gin.Context) {
	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	log.Printf("audit sync.status files=%d", len(status.Files))
	c.JSON(http.StatusOK, status)
}

func (h *Handlers) AdminSyncGenerateMessage(c *gin.Context) {
	var req syncGenerateMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	paths, err := validateSyncPaths(req.Paths, status.Files)
	if err != nil {
		badRequest(c, err)
		return
	}
	files := syncFilesByPath(paths, status.Files)
	diffStat, _, _, _ := h.runWikiGit(c.Request.Context(), append([]string{"diff", "--stat", "--"}, paths...)...)
	nameStatus, _, _, _ := h.runWikiGit(c.Request.Context(), append([]string{"diff", "--name-status", "--"}, paths...)...)
	message, rule, err := h.Sync.GenerateCommitMessage(c.Request.Context(), service.SyncCommitMessageRequest{
		Files:      toServiceSyncFiles(files),
		DiffStat:   diffStat,
		NameStatus: nameStatus,
	})
	if err != nil {
		internalError(c, err)
		return
	}
	log.Printf("audit sync.generate_message paths=%d", len(paths))
	c.JSON(http.StatusOK, gin.H{
		"message": message,
		"rule":    rule,
		"paths":   paths,
	})
}

func (h *Handlers) AdminSyncCommit(c *gin.Context) {
	var req syncCommitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		badRequest(c, fmt.Errorf("message is required"))
		return
	}
	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	paths, err := validateSyncPaths(req.Paths, status.Files)
	if err != nil {
		badRequest(c, err)
		return
	}
	args := append([]string{"add", "--"}, paths...)
	if _, stderr, exitCode, err := h.runWikiGit(c.Request.Context(), args...); err != nil || exitCode != 0 {
		internalError(c, fmt.Errorf("git add failed: %s", strings.TrimSpace(stderr)))
		return
	}
	stdout, stderr, exitCode, err := h.runWikiGit(c.Request.Context(), "commit", "-m", message)
	if err != nil {
		internalError(c, err)
		return
	}
	if exitCode != 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "GIT_COMMIT_FAILED", "message": strings.TrimSpace(stdout + stderr)},
		})
		return
	}
	hash, _, _, _ := h.runWikiGit(c.Request.Context(), "rev-parse", "--short", "HEAD")
	log.Printf("audit sync.commit paths=%d hash=%s", len(paths), strings.TrimSpace(hash))
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"hash":      strings.TrimSpace(hash),
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": exitCode,
	})
}

func (h *Handlers) AdminSyncPush(c *gin.Context) {
	var req syncPushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	remote := firstNonEmpty(req.Remote, h.Config.Sync.Remote)
	branch := firstNonEmpty(req.Branch, h.Config.Sync.Branch)
	if !safeGitName(remote) || !safeGitName(branch) {
		badRequest(c, fmt.Errorf("invalid remote or branch"))
		return
	}
	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	if status.PushCount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "NO_COMMITS_TO_PUSH", "message": "当前没有未推送的提交，请先选择文件并提交。"},
		})
		return
	}
	stdout, stderr, exitCode, err := h.runWikiGit(c.Request.Context(), "push", remote, branch)
	if err != nil {
		internalError(c, err)
		return
	}
	if exitCode != 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "GIT_PUSH_FAILED", "message": strings.TrimSpace(stdout + stderr)},
		})
		return
	}
	log.Printf("audit sync.push remote=%s branch=%s", remote, branch)
	c.JSON(http.StatusOK, gin.H{"ok": true, "remote": remote, "branch": branch, "stdout": stdout, "stderr": stderr, "exit_code": exitCode})
}

func (h *Handlers) buildDirectAdminRequest(req adminChatRequest, mode string) service.DirectAdminRequest {
	context := map[string]any{}
	for key, value := range req.Context {
		context[key] = value
	}
	if strings.TrimSpace(req.Message) != "" {
		context["question"] = firstNonEmpty(strings.TrimSpace(req.Message), stringOption(req.Context, "question"))
	}
	return service.DirectAdminRequest{
		Message:     strings.TrimSpace(req.Message),
		ModeHint:    mode,
		History:     toServiceHistory(req.History),
		Attachments: toDirectAdminAttachments(req.Attachments),
		Context:     context,
	}
}

func (h *Handlers) estimateAdminContext(req service.DirectAdminRequest) service.ContextUsage {
	if h.ContextCounter == nil {
		return service.ContextUsage{MaxTokens: 1000000, ReserveTokens: 8192, Estimated: true, Counter: "unavailable"}
	}
	return h.ContextCounter.CountMessages(h.DirectAdmin.InitialMessages(req))
}

type syncStatusResponse struct {
	Branch        string           `json:"branch"`
	Remote        string           `json:"remote"`
	Ahead         int              `json:"ahead"`
	Behind        int              `json:"behind"`
	Files         []syncStatusFile `json:"files"`
	ChangedCount  int              `json:"changed_count"`
	PushCount     int              `json:"push_count"`
	CanPush       bool             `json:"can_push"`
	CommitsToPush []syncCommitInfo `json:"commits_to_push"`
	RecentCommits []syncCommitInfo `json:"recent_commits"`
}

type syncStatusFile struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"`
	Index     string `json:"index"`
	Worktree  string `json:"worktree"`
	Preview   string `json:"preview"`
	DefaultOn bool   `json:"default_on"`
	Deleted   bool   `json:"deleted"`
}

type syncCommitInfo struct {
	Hash    string `json:"hash"`
	Date    string `json:"date"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

func (h *Handlers) resolveWikiPath(raw string) (string, string, error) {
	root, err := filepath.Abs(h.Config.MountedWiki.Root)
	if err != nil {
		return "", "", err
	}
	cleaned := filepath.Clean(strings.TrimSpace(raw))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		cleaned = ""
	}
	cleaned = strings.TrimPrefix(filepath.ToSlash(cleaned), "/")
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains("/"+cleaned+"/", "/.git/") {
		return "", "", fmt.Errorf("invalid wiki path")
	}
	abs := filepath.Join(root, filepath.FromSlash(cleaned))
	abs, err = filepath.Abs(abs)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes wiki root")
	}
	if rel == "." {
		rel = ""
	}
	return abs, filepath.ToSlash(rel), nil
}

func wikiPreviewKind(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return "image"
	default:
		return "download"
	}
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}

func (h *Handlers) runWikiGit(ctx context.Context, args ...string) (string, string, int, error) {
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = h.Config.MountedWiki.Root
	out, err := cmd.Output()
	stderr := ""
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
			return string(out), stderr, exitErr.ExitCode(), nil
		}
		return string(out), stderr, -1, err
	}
	return string(out), stderr, 0, nil
}

func (h *Handlers) gitStatus(ctx context.Context) (syncStatusResponse, error) {
	stdout, stderr, exitCode, err := h.runWikiGit(ctx, "status", "--porcelain=v1", "-b")
	if err != nil {
		return syncStatusResponse{}, err
	}
	if exitCode != 0 {
		return syncStatusResponse{}, fmt.Errorf("git status failed: %s", strings.TrimSpace(stderr))
	}
	status := syncStatusResponse{
		Remote: h.Config.Sync.Remote,
		Files:  []syncStatusFile{},
	}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			parseBranchLine(strings.TrimPrefix(line, "## "), &status)
			continue
		}
		file, ok := parseStatusLine(line)
		if ok {
			status.Files = append(status.Files, file)
		}
	}
	sort.Slice(status.Files, func(i, j int) bool {
		return status.Files[i].Path < status.Files[j].Path
	})
	status.ChangedCount = len(status.Files)
	status.CommitsToPush = h.gitLog(ctx, "@{u}..HEAD", 20)
	status.RecentCommits = h.gitLog(ctx, "", 10)
	status.PushCount = len(status.CommitsToPush)
	if status.PushCount == 0 {
		status.PushCount = status.Ahead
	}
	status.CanPush = status.PushCount > 0
	return status, nil
}

func (h *Handlers) gitLog(ctx context.Context, rev string, limit int) []syncCommitInfo {
	args := []string{"log", fmt.Sprintf("-n%d", limit), "--pretty=format:%h%x09%ad%x09%an%x09%s", "--date=short"}
	if strings.TrimSpace(rev) != "" {
		args = append(args, rev)
	}
	stdout, _, exitCode, err := h.runWikiGit(ctx, args...)
	if err != nil || exitCode != 0 {
		return nil
	}
	out := []syncCommitInfo{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			continue
		}
		out = append(out, syncCommitInfo{
			Hash:    parts[0],
			Date:    parts[1],
			Author:  parts[2],
			Subject: parts[3],
		})
	}
	return out
}

func parseBranchLine(line string, status *syncStatusResponse) {
	branch := line
	if idx := strings.Index(branch, "..."); idx >= 0 {
		branch = branch[:idx]
	}
	if idx := strings.Index(branch, " "); idx >= 0 {
		branch = branch[:idx]
	}
	status.Branch = strings.TrimSpace(branch)
	if strings.Contains(line, "ahead ") {
		status.Ahead = parseStatusNumberAfter(line, "ahead ")
	}
	if strings.Contains(line, "behind ") {
		status.Behind = parseStatusNumberAfter(line, "behind ")
	}
}

func parseStatusNumberAfter(text string, marker string) int {
	idx := strings.Index(text, marker)
	if idx < 0 {
		return 0
	}
	rest := text[idx+len(marker):]
	value := 0
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		value = value*10 + int(r-'0')
	}
	return value
}

func parseStatusLine(line string) (syncStatusFile, bool) {
	if len(line) < 4 {
		return syncStatusFile{}, false
	}
	index := strings.TrimSpace(line[:1])
	worktree := strings.TrimSpace(line[1:2])
	path := strings.TrimSpace(line[3:])
	oldPath := ""
	if strings.Contains(path, " -> ") {
		parts := strings.SplitN(path, " -> ", 2)
		oldPath = strings.TrimSpace(parts[0])
		path = strings.TrimSpace(parts[1])
	}
	status := firstNonEmpty(index, worktree)
	deleted := index == "D" || worktree == "D"
	return syncStatusFile{
		Path:      filepath.ToSlash(path),
		OldPath:   filepath.ToSlash(oldPath),
		Status:    status,
		Index:     index,
		Worktree:  worktree,
		Preview:   wikiPreviewKind(path),
		DefaultOn: !strings.HasPrefix(filepath.ToSlash(path), ".obsidian/"),
		Deleted:   deleted,
	}, true
}

func validateSyncPaths(paths []string, files []syncStatusFile) ([]string, error) {
	allowed := map[string]bool{}
	for _, file := range files {
		allowed[file.Path] = true
	}
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, raw := range paths {
		path := filepath.ToSlash(strings.TrimSpace(raw))
		if path == "" {
			continue
		}
		if path == ".." || strings.HasPrefix(path, "../") || strings.Contains("/"+path+"/", "/.git/") {
			return nil, fmt.Errorf("invalid path: %s", raw)
		}
		if !allowed[path] {
			return nil, fmt.Errorf("path is not in git status: %s", path)
		}
		if !seen[path] {
			out = append(out, path)
			seen[path] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("paths is required")
	}
	return out, nil
}

func syncFilesByPath(paths []string, files []syncStatusFile) []syncStatusFile {
	byPath := map[string]syncStatusFile{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	out := make([]syncStatusFile, 0, len(paths))
	for _, path := range paths {
		if file, ok := byPath[path]; ok {
			out = append(out, file)
		}
	}
	return out
}

func toServiceSyncFiles(files []syncStatusFile) []service.SyncChangedFile {
	out := make([]service.SyncChangedFile, 0, len(files))
	for _, file := range files {
		out = append(out, service.SyncChangedFile{
			Path:     file.Path,
			Status:   file.Status,
			Deleted:  file.Deleted,
			Preview:  file.Preview,
			OldPath:  file.OldPath,
			Index:    file.Index,
			Worktree: file.Worktree,
		})
	}
	return out
}

func safeGitName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsAny(value, " \t\r\n;&|`$<>") {
		return false
	}
	return !strings.Contains(value, "..")
}

func writeSSE(c *gin.Context, event string, data any) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	_, _ = c.Writer.WriteString("event: " + event + "\n")
	_, _ = c.Writer.WriteString("data: " + string(payload) + "\n\n")
	c.Writer.Flush()
}

func writeSSEWithLock(c *gin.Context, event string, data any, mu *sync.Mutex) {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	writeSSE(c, event, data)
}

func startSSEKeepalive(c *gin.Context, mu *sync.Mutex, interval time.Duration) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writeSSEWithLock(c, "keepalive", gin.H{"ts": time.Now().Format(time.RFC3339Nano)}, mu)
			case <-stop:
				return
			case <-c.Request.Context().Done():
				return
			}
		}
	}()
	return func() {
		close(stop)
	}
}

func detectAdminMode(message string, context map[string]any, attachments []attachment) string {
	if len(attachments) > 0 && attachmentPath(attachments) != "" {
		return "ingest"
	}
	if mode := strings.TrimSpace(stringOption(context, "last_mode")); mode != "" && (strings.Contains(strings.ToLower(message), "修复") || strings.Contains(strings.ToLower(message), "fix")) {
		if mode == "lint" {
			return "repair"
		}
	}
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(lower, "摄入"), strings.Contains(lower, "ingest"), strings.HasPrefix(lower, "raw/"):
		return "ingest"
	case strings.Contains(lower, "健康检查"), strings.Contains(lower, "lint"), strings.Contains(lower, "检查"):
		return "lint"
	case strings.Contains(lower, "reflect"), strings.Contains(lower, "综合分析"), strings.Contains(lower, "发现规律"):
		return "reflect"
	case strings.Contains(lower, "修复"), strings.Contains(lower, "repair"):
		return "repair"
	case strings.Contains(lower, "merge"), strings.Contains(lower, "去重"), strings.Contains(lower, "合并"):
		return "merge"
	case strings.Contains(lower, "add question"), strings.Contains(lower, "记录一个问题"), strings.Contains(lower, "我想搞清楚"):
		return "add-question"
	case strings.Contains(lower, "同步"), strings.Contains(lower, "sync"), strings.Contains(lower, "push"):
		return "sync"
	default:
		return "query"
	}
}

func adminDisplayReply(mode string, result map[string]any) string {
	switch mode {
	case "query":
		return firstNonEmpty(stringValue(result, "reply"), stringValue(result, "answer"), stringValue(result, "summary"))
	case "ingest":
		return firstNonEmpty(stringValue(result, "reply"), stringValue(result, "summary"), "摄入完成")
	case "lint":
		return firstNonEmpty(stringValue(result, "reply"), stringValue(result, "summary"), "健康检查完成")
	case "reflect":
		return firstNonEmpty(stringValue(result, "reply"), stringValue(result, "summary"), "反思分析完成")
	case "repair":
		return firstNonEmpty(stringValue(result, "reply"), stringValue(result, "summary"), "修复完成")
	case "sync":
		return firstNonEmpty(stringValue(result, "reply"), stringValue(result, "summary"), "同步完成")
	case "upload":
		return firstNonEmpty(stringValue(result, "reply"), stringValue(result, "summary"), "上传完成")
	default:
		return firstNonEmpty(stringValue(result, "reply"), stringValue(result, "summary"), stringValue(result, "answer"))
	}
}

func traceID(c *gin.Context) string {
	return c.GetString("trace_id")
}

func badRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{
		"error": gin.H{
			"code":    "BAD_REQUEST",
			"message": err.Error(),
		},
	})
}

func internalError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{
		"error": gin.H{
			"code":    "INTERNAL_ERROR",
			"message": err.Error(),
		},
	})
}

func contextLimitExceeded(c *gin.Context, usage service.ContextUsage) {
	c.JSON(http.StatusRequestEntityTooLarge, gin.H{
		"error": gin.H{
			"code":    "CONTEXT_LIMIT_EXCEEDED",
			"message": "当前对话已接近上下文上限，请创建新的对话继续。",
		},
		"context_usage": usage,
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringOption(options map[string]any, key string) string {
	if options == nil {
		return ""
	}
	raw, ok := options[key]
	if !ok {
		return ""
	}
	value, _ := raw.(string)
	return value
}

func stringSliceOption(options map[string]any, key string) []string {
	if options == nil {
		return nil
	}
	raw, ok := options[key]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value, ok := item.(string)
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			out = append(out, strings.TrimSpace(value))
		}
		return out
	default:
		return nil
	}
}

func anySliceOption(options map[string]any, key string) []any {
	if options == nil {
		return nil
	}
	raw, ok := options[key]
	if !ok {
		return nil
	}
	values, _ := raw.([]any)
	return values
}

func boolOption(options map[string]any, key string, fallback bool) bool {
	if options == nil {
		return fallback
	}
	raw, ok := options[key]
	if !ok {
		return fallback
	}
	value, ok := raw.(bool)
	if !ok {
		return fallback
	}
	return value
}

func stringValue(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	raw, ok := data[key]
	if !ok {
		return ""
	}
	value, _ := raw.(string)
	return value
}

func attachmentPath(items []attachment) string {
	for _, item := range items {
		if strings.TrimSpace(item.Path) != "" {
			return strings.TrimSpace(item.Path)
		}
	}
	return ""
}

func toServiceHistory(items []chatMessage) []service.ChatMessage {
	history := make([]service.ChatMessage, 0, len(items))
	for _, item := range items {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if role == "" || content == "" {
			continue
		}
		history = append(history, service.ChatMessage{
			Role:    role,
			Content: content,
		})
	}
	return history
}

func toDirectAdminAttachments(items []attachment) []service.DirectAdminAttachment {
	if len(items) == 0 {
		return nil
	}
	out := make([]service.DirectAdminAttachment, 0, len(items))
	for _, item := range items {
		out = append(out, service.DirectAdminAttachment{
			Path: item.Path,
			Kind: item.Kind,
			Name: item.Name,
		})
	}
	return out
}

func contextualizeMessage(message string, history []chatMessage, context map[string]any) string {
	current := strings.TrimSpace(message)
	if current == "" {
		return current
	}
	sections := []string{}
	state := summarizeSessionState(context)
	if state != "" {
		sections = append(sections, "会话状态：\n"+state)
	}
	turns := make([]string, 0, len(history))
	start := 0
	if len(history) > chatHistoryLimit {
		start = len(history) - chatHistoryLimit
	}
	for _, item := range history[start:] {
		role := strings.TrimSpace(item.Role)
		content := strings.TrimSpace(item.Content)
		if role == "" || content == "" {
			continue
		}
		turns = append(turns, fmt.Sprintf("%s: %s", role, content))
	}
	if len(turns) == 0 {
		if len(sections) == 0 {
			return current
		}
		return fmt.Sprintf("%s\n\n当前请求：%s", strings.Join(sections, "\n\n"), current)
	}
	sections = append(sections, "会话上下文：\n"+strings.Join(turns, "\n"))
	return fmt.Sprintf("%s\n\n当前请求：%s", strings.Join(sections, "\n\n"), current)
}

func summarizeSessionState(context map[string]any) string {
	if context == nil {
		return ""
	}
	raw, ok := context["session_state"]
	if !ok {
		return ""
	}
	state, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	lines := []string{}
	if value := strings.TrimSpace(stringOption(state, "lastMode")); value != "" {
		lines = append(lines, "last_mode: "+value)
	}
	if value := strings.TrimSpace(stringOption(state, "lastSummary")); value != "" {
		lines = append(lines, "last_summary: "+truncateContextValue(value, 300))
	} else if value := strings.TrimSpace(stringOption(state, "lastReply")); value != "" {
		lines = append(lines, "last_reply: "+truncateContextValue(value, 500))
	}
	if value := strings.TrimSpace(stringOption(state, "lastReportFile")); value != "" {
		lines = append(lines, "last_report_file: "+value)
	}
	if values := stringSliceOption(state, "uploadedPaths"); len(values) > 0 {
		lines = append(lines, "uploaded_paths: "+strings.Join(values, ", "))
	}
	if values := stringSliceOption(state, "lastOutputFiles"); len(values) > 0 {
		lines = append(lines, "last_output_files: "+strings.Join(values, ", "))
	}
	if values := stringSliceOption(state, "lastCommands"); len(values) > 0 {
		lines = append(lines, "last_commands: "+strings.Join(values, " | "))
	}
	if values := stringSliceOption(state, "lastArtifacts"); len(values) > 0 {
		lines = append(lines, "last_artifacts: "+strings.Join(values, ", "))
	}
	return strings.Join(lines, "\n")
}

func truncateContextValue(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func chunkText(text string, size int) []string {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	if size <= 0 {
		size = 24
	}
	chunks := make([]string, 0, (len(runes)+size-1)/size)
	for start := 0; start < len(runes); start += size {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}
