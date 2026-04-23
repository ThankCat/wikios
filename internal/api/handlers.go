package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	PublicQuery *service.PublicQueryService
	DirectAdmin *service.DirectAdminService
	Upload      *service.UploadService
	Store       *store.Store
	AuthConfig  config.AuthConfig
}

func NewHandlers(
	publicQuery *service.PublicQueryService,
	directAdmin *service.DirectAdminService,
	uploadSvc *service.UploadService,
	dataStore *store.Store,
	authCfg config.AuthConfig,
) *Handlers {
	return &Handlers{
		PublicQuery: publicQuery,
		DirectAdmin: directAdmin,
		Upload:      uploadSvc,
		Store:       dataStore,
		AuthConfig:  authCfg,
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
	writeSSE(c, "result", gin.H{
		"answer":  resp.Answer,
		"details": resp.Details,
	})
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
	execution := service.NewExecution(mode)
	result, err := h.runAdminConversation(c.Request.Context(), traceID(c), execution, req, mode)
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
		"mode":      mode,
		"reply":     adminDisplayReply(mode, result),
		"details":   result,
		"execution": execution,
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
	writeSSEWithLock(c, "meta", gin.H{
		"mode":         mode,
		"execution_id": execution.ID,
		"started_at":   execution.StartedAt.Format(time.RFC3339Nano),
	}, mu)
	streamCtx := service.WithStreamEmitter(c.Request.Context(), &sseEmitter{c: c, mu: mu})
	result, err := h.runAdminConversation(streamCtx, traceID(c), execution, req, mode)
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
		"reply":     adminDisplayReply(mode, result),
		"details":   result,
		"execution": execution,
	}, mu)
	writeSSEWithLock(c, "done", gin.H{"execution": execution}, mu)
}

func (h *Handlers) runAdminConversation(ctx context.Context, trace string, execution *service.Execution, req adminChatRequest, mode string) (map[string]any, error) {
	message := contextualizeMessage(req.Message, req.History, req.Context)
	context := map[string]any{}
	for key, value := range req.Context {
		context[key] = value
	}
	if strings.TrimSpace(contextualizeMessage(req.Message, req.History, req.Context)) != "" {
		context["question"] = firstNonEmpty(strings.TrimSpace(message), stringOption(req.Context, "question"))
	}
	return h.DirectAdmin.Run(ctx, execution, trace, service.DirectAdminRequest{
		Message:     strings.TrimSpace(req.Message),
		ModeHint:    mode,
		History:     toServiceHistory(req.History),
		Attachments: toDirectAdminAttachments(req.Attachments),
		Context:     context,
	})
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
