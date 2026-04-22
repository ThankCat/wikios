package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	AdminQuery  *service.AdminQueryService
	Ingest      *service.IngestService
	Lint        *service.LintService
	Reflect     *service.ReflectService
	Repair      *service.RepairService
	Sync        *service.SyncService
	Upload      *service.UploadService
	Store       *store.Store
	AuthConfig  config.AuthConfig
}

func NewHandlers(
	publicQuery *service.PublicQueryService,
	adminQuery *service.AdminQueryService,
	ingest *service.IngestService,
	lintSvc *service.LintService,
	reflectSvc *service.ReflectService,
	repairSvc *service.RepairService,
	syncSvc *service.SyncService,
	uploadSvc *service.UploadService,
	dataStore *store.Store,
	authCfg config.AuthConfig,
) *Handlers {
	return &Handlers{
		PublicQuery: publicQuery,
		AdminQuery:  adminQuery,
		Ingest:      ingest,
		Lint:        lintSvc,
		Reflect:     reflectSvc,
		Repair:      repairSvc,
		Sync:        syncSvc,
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

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sseEmitter struct {
	c *gin.Context
}

func (e *sseEmitter) Emit(event service.StreamEvent) {
	writeSSE(e.c, event.Type, event.Data)
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
	fileHeader, err := c.FormFile("file")
	if err != nil {
		badRequest(c, fmt.Errorf("file is required"))
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		internalError(c, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		internalError(c, err)
		return
	}
	result, err := h.Upload.Save(c.Request.Context(), traceID(c), service.UploadRequest{
		Filename:    fileHeader.Filename,
		ContentType: fileHeader.Header.Get("Content-Type"),
		Content:     data,
	})
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reply":   adminDisplayReply("upload", result),
		"details": result,
	})
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
	writeSSE(c, "meta", gin.H{
		"mode":         mode,
		"execution_id": execution.ID,
		"started_at":   execution.StartedAt.Format(time.RFC3339Nano),
	})
	streamCtx := service.WithStreamEmitter(c.Request.Context(), &sseEmitter{c: c})
	result, err := h.runAdminConversation(streamCtx, traceID(c), execution, req, mode)
	execution.EndedAt = time.Now()
	if err != nil {
		execution.Status = service.ExecutionFailed
		execution.Error = err.Error()
		writeSSE(c, "error", gin.H{"message": err.Error()})
		writeSSE(c, "done", gin.H{"execution": execution})
		return
	}
	execution.Status = service.ExecutionSuccess
	writeSSE(c, "result", gin.H{
		"reply":     adminDisplayReply(mode, result),
		"details":   result,
		"execution": execution,
	})
	writeSSE(c, "done", gin.H{"execution": execution})
}

func (h *Handlers) runAdminConversation(ctx context.Context, trace string, execution *service.Execution, req adminChatRequest, mode string) (map[string]any, error) {
	message := contextualizeMessage(req.Message, req.History)
	switch mode {
	case "ingest":
		path := firstNonEmpty(stringOption(req.Context, "path"), attachmentPath(req.Attachments), strings.TrimSpace(req.Message))
		return h.Ingest.Run(ctx, execution, trace, service.IngestRequest{
			InputType:   "file",
			Path:        path,
			Interactive: false,
		})
	case "lint":
		return h.Lint.Run(ctx, execution, trace, service.LintRequest{
			WriteReport:    true,
			AutoFixLowRisk: boolOption(req.Context, "auto_fix_low_risk", false),
		})
	case "reflect":
		return h.Reflect.Run(ctx, execution, trace, service.ReflectRequest{
			Topic:          firstNonEmpty(strings.TrimSpace(message), stringOption(req.Context, "topic")),
			WriteReport:    true,
			AutoFixLowRisk: boolOption(req.Context, "auto_fix_low_risk", false),
		})
	case "repair":
		action := strings.TrimSpace(stringOption(req.Context, "action"))
		if action == "proposal" {
			return h.Repair.ApplyProposal(ctx, execution, trace, service.ApplyProposalRequest{
				ProposalID: firstNonEmpty(stringOption(req.Context, "proposal_id"), strings.TrimSpace(req.Message)),
			})
		}
		if action == "manual" {
			return h.Repair.ApplyLowRisk(ctx, execution, trace, service.ApplyLowRiskRequest{
				Path: stringOption(req.Context, "path"),
				Ops:  anySliceOption(req.Context, "ops"),
			})
		}
		return h.Repair.AutoDetect(ctx, execution, trace, service.AutoRepairRequest{
			Topic: firstNonEmpty(strings.TrimSpace(message), stringOption(req.Context, "topic")),
			Apply: boolOption(req.Context, "apply", true),
		})
	case "sync":
		return h.Sync.Run(ctx, execution, trace, service.SyncRequest{
			Message: firstNonEmpty(strings.TrimSpace(message), "chore: sync wiki updates"),
		})
	default:
		return h.AdminQuery.Run(ctx, execution, trace, service.AdminQueryRequest{
			Question:    firstNonEmpty(strings.TrimSpace(message), stringOption(req.Context, "question")),
			WriteOutput: boolOption(req.Context, "write_output", false),
		})
	}
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
		return stringValue(result, "answer")
	case "ingest":
		return firstNonEmpty(stringValue(result, "summary"), "摄入完成")
	case "lint":
		return firstNonEmpty(stringValue(result, "summary"), "健康检查完成")
	case "reflect":
		return firstNonEmpty(stringValue(result, "summary"), "反思分析完成")
	case "repair":
		return firstNonEmpty(stringValue(result, "summary"), "修复完成")
	case "sync":
		return "同步完成"
	case "upload":
		return firstNonEmpty(stringValue(result, "summary"), "上传完成")
	default:
		return firstNonEmpty(stringValue(result, "summary"), stringValue(result, "answer"))
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

func contextualizeMessage(message string, history []chatMessage) string {
	current := strings.TrimSpace(message)
	if current == "" {
		return current
	}
	turns := make([]string, 0, len(history))
	start := 0
	if len(history) > 6 {
		start = len(history) - 6
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
		return current
	}
	return fmt.Sprintf("会话上下文：\n%s\n\n当前请求：%s", strings.Join(turns, "\n"), current)
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
