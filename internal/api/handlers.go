package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"wikios/internal/config"
	wikigit "wikios/internal/git"
	"wikios/internal/llm"
	"wikios/internal/service"
	"wikios/internal/store"
)

type Handlers struct {
	CustomerChatService *service.CustomerChatService
	ReviewQueue         *service.ReviewQueueService
	DirectAdmin         *service.DirectAdminService
	Upload              *service.UploadService
	Sync                *service.SyncService
	Store               *store.Store
	Config              *config.Config
	ContextCounter      *service.ContextCounter
	SafetyTerms         *service.CustomerSafetyTermManager
}

func NewHandlers(
	customerQuery *service.CustomerChatService,
	reviewQueue *service.ReviewQueueService,
	directAdmin *service.DirectAdminService,
	uploadSvc *service.UploadService,
	syncSvc *service.SyncService,
	dataStore *store.Store,
	cfg *config.Config,
	contextCounter *service.ContextCounter,
	safetyTerms *service.CustomerSafetyTermManager,
) *Handlers {
	return &Handlers{
		CustomerChatService: customerQuery,
		ReviewQueue:         reviewQueue,
		DirectAdmin:         directAdmin,
		Upload:              uploadSvc,
		Sync:                syncSvc,
		Store:               dataStore,
		Config:              cfg,
		ContextCounter:      contextCounter,
		SafetyTerms:         safetyTerms,
	}
}

type adminChatRequest struct {
	Message     string         `json:"message"`
	Stream      *bool          `json:"stream,omitempty"`
	ModeHint    string         `json:"mode_hint"`
	Context     map[string]any `json:"context"`
	Attachments []attachment   `json:"attachments"`
	History     []chatMessage  `json:"history"`
}

type customerChatRequest struct {
	Message          string         `json:"message"`
	Stream           bool           `json:"stream,omitempty"`
	Simulation       bool           `json:"simulation,omitempty"`
	Entrypoint       string         `json:"entrypoint,omitempty"`
	ClientChannel    string         `json:"client_channel,omitempty"`
	UserID           string         `json:"user_id"`
	SessionID        string         `json:"session_id"`
	MessageID        string         `json:"message_id"`
	AnswerMessageID  string         `json:"answer_message_id"`
	MessageCreatedAt string         `json:"message_created_at"`
	Context          map[string]any `json:"context"`
	History          []chatMessage  `json:"history"`
}

type adminWikiSaveFileRequest struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

type adminLLMModelRequest struct {
	DisplayName     string `json:"display_name"`
	Provider        string `json:"provider"`
	BaseURL         string `json:"base_url"`
	ModelName       string `json:"model_name"`
	APIKey          string `json:"api_key"`
	TimeoutSec      int    `json:"timeout_sec"`
	AdminTimeoutSec int    `json:"admin_timeout_sec"`
}

type adminLLMModelResponse struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	Provider        string `json:"provider"`
	BaseURL         string `json:"base_url"`
	ModelName       string `json:"model_name"`
	HasAPIKey       bool   `json:"has_api_key"`
	APIKeyMask      string `json:"api_key_mask"`
	IsActive        bool   `json:"is_active"`
	TimeoutSec      int    `json:"timeout_sec"`
	AdminTimeoutSec int    `json:"admin_timeout_sec"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type adminLLMModelTestResponse struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
	TestedAt  string `json:"tested_at"`
}

type chatMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type attachment struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

const chatHistoryLimit = 8
const defaultCustomerChatResponseTimeoutSec = 300
const customerChatTimeoutFallbackMessage = "当前在线回复暂时不可用，请稍后再试。"

type runtimeSettingsRequest = service.RuntimeSettings

type customerSafetyTermsUpdateRequest struct {
	Config service.CustomerSafetyTermsConfig `json:"config"`
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

type syncPullRequest struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
}

type syncSetupRequest struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	URL    string `json:"url"`
}

type syncGenerateMessageRequest struct {
	Paths []string `json:"paths"`
}

type reviewApproveRequest struct {
	Question   string `json:"question"`
	Answer     string `json:"answer"`
	TargetPath string `json:"target_path"`
}

type reviewRejectRequest struct {
	Reason string `json:"reason"`
}

func bindOptionalJSON(c *gin.Context, out any) error {
	if c.Request == nil || c.Request.Body == nil || c.Request.Body == http.NoBody {
		return nil
	}
	if err := c.ShouldBindJSON(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

type sseEmitter struct {
	c  *gin.Context
	mu *sync.Mutex
}

func (e *sseEmitter) Emit(event service.StreamEvent) {
	writeSSEWithLock(e.c, event.Type, event.Data, e.mu)
}

func (h *Handlers) CustomerChat(c *gin.Context) {
	var req customerChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if err := normalizeCustomerChatAPIRequest(&req); err != nil {
		badRequest(c, err)
		return
	}
	if req.Stream {
		h.handleCustomerChatStream(c, req)
		return
	}
	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	answerCtx, cancel := context.WithTimeout(c.Request.Context(), h.customerChatResponseTimeout())
	defer cancel()
	resp, err := h.CustomerChatService.Answer(answerCtx, traceID(c), customerChatServiceRequest(req, false, receivedAt))
	if err != nil {
		if customerChatResponseTimedOut(answerCtx, c.Request.Context(), err) {
			log.Printf("customer chat timed out trace=%s timeout=%s message=%q err=%v", traceID(c), h.customerChatResponseTimeout(), truncateAPIText(req.Message, 80), err)
			c.JSON(http.StatusOK, customerChatClientPayload(&service.CustomerChatResponse{
				Answer:     customerChatTimeoutFallbackMessage,
				ReceivedAt: receivedAt,
				AnsweredAt: time.Now().UTC().Format(time.RFC3339Nano),
			}))
			return
		}
		if requestContextCanceled(c.Request.Context(), err) {
			return
		}
		customerChatFailure(c, err)
		return
	}
	c.JSON(http.StatusOK, customerChatClientPayload(resp))
}

func normalizeCustomerChatAPIRequest(req *customerChatRequest) error {
	if req == nil {
		return fmt.Errorf("request is required")
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return fmt.Errorf("message is required")
	}
	req.Entrypoint = strings.TrimSpace(strings.ToLower(req.Entrypoint))
	if req.Entrypoint == "" {
		req.Entrypoint = "external"
	}
	if req.Entrypoint != "external" && req.Entrypoint != "internal" {
		return fmt.Errorf("entrypoint must be external or internal")
	}
	req.ClientChannel = strings.TrimSpace(strings.ToLower(req.ClientChannel))
	if req.ClientChannel == "" && req.Context != nil {
		if value, ok := req.Context["client_channel"]; ok {
			req.ClientChannel = strings.TrimSpace(strings.ToLower(fmt.Sprint(value)))
		}
	}
	if req.ClientChannel == "" {
		req.ClientChannel = "web"
	}
	if req.ClientChannel != "web" && req.ClientChannel != "mobile_app" {
		return fmt.Errorf("client_channel must be web or mobile_app")
	}
	return nil
}

func (h *Handlers) CustomerContextEstimate(c *gin.Context) {
	var req customerChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if err := normalizeCustomerChatAPIRequest(&req); err != nil {
		badRequest(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"mode":          "customer",
		"context_usage": h.estimateCustomerContext(req),
	})
}

func (h *Handlers) handleCustomerChatStream(c *gin.Context, req customerChatRequest) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	mu := &sync.Mutex{}
	stopKeepalive := startSSEKeepalive(c, mu, 8*time.Second)
	defer stopKeepalive()
	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	serviceReq := customerChatServiceRequest(req, true, receivedAt)
	resp, err := h.CustomerChatService.AnswerStream(c.Request.Context(), traceID(c), serviceReq, &sseEmitter{c: c, mu: mu})
	if err != nil {
		if requestContextCanceled(c.Request.Context(), err) {
			return
		}
		writeSSEWithLock(c, "error", gin.H{"message": err.Error()}, mu)
		writeSSEWithLock(c, "done", gin.H{"ok": false}, mu)
		return
	}
	writeSSEWithLock(c, "result", customerChatClientPayload(resp), mu)
	writeSSEWithLock(c, "done", gin.H{"ok": true}, mu)
}

func customerChatServiceRequest(req customerChatRequest, stream bool, receivedAt string) service.CustomerChatRequest {
	return service.CustomerChatRequest{
		Question:          req.Message,
		Stream:            stream,
		PersistLog:        nil,
		Simulation:        req.Simulation,
		Entrypoint:        req.Entrypoint,
		ClientChannel:     req.ClientChannel,
		UserID:            req.UserID,
		SessionID:         req.SessionID,
		QuestionMessageID: req.MessageID,
		AnswerMessageID:   req.AnswerMessageID,
		QuestionCreatedAt: req.MessageCreatedAt,
		ReceivedAt:        receivedAt,
		Context:           req.Context,
		History:           toServiceHistory(req.History),
	}
}

func customerChatClientPayload(resp *service.CustomerChatResponse) gin.H {
	if resp == nil {
		return gin.H{}
	}
	return gin.H{
		"answer":          resp.Answer,
		"answer_mode":     resp.AnswerMode,
		"review_required": resp.ReviewRequired,
		"source_count":    resp.SourceCount,
		"user_intent":     resp.UserIntent,
		"received_at":     resp.ReceivedAt,
		"answered_at":     resp.AnsweredAt,
	}
}

func requestContextCanceled(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
}

func customerChatResponseTimedOut(answerCtx context.Context, requestCtx context.Context, err error) bool {
	if requestCtx != nil && errors.Is(requestCtx.Err(), context.Canceled) {
		return false
	}
	return errors.Is(answerCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded)
}

func customerChatFailure(c *gin.Context, err error) {
	log.Printf("customer chat generation failed trace=%s err=%v", traceID(c), err)
	c.JSON(http.StatusInternalServerError, gin.H{
		"error": gin.H{
			"code":    "CUSTOMER_CHAT_FAILED",
			"message": "当前回复服务暂时不可用，请稍后再试。",
		},
	})
}

func (h *Handlers) customerChatResponseTimeout() time.Duration {
	seconds := defaultCustomerChatResponseTimeoutSec
	if h != nil && h.Config != nil && h.Config.CustomerChat.ResponseTimeoutSec > 0 {
		seconds = h.Config.CustomerChat.ResponseTimeoutSec
	}
	return time.Duration(seconds) * time.Second
}

func truncateAPIText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func (h *Handlers) AdminGetRuntimeSettings(c *gin.Context) {
	snapshot, err := service.LoadRuntimeSettings(c.Request.Context(), h.Store, h.Config)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func (h *Handlers) AdminGetCustomerSafetyTerms(c *gin.Context) {
	if h.SafetyTerms == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "CUSTOMER_SAFETY_TERMS_UNAVAILABLE", "message": "customer safety terms are not configured"},
		})
		return
	}
	terms, status, err := h.SafetyTerms.Config()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"source": h.SafetyTerms.SourceOrDefault(),
			"config": terms,
			"status": status,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"source": h.SafetyTerms.SourceOrDefault(),
		"config": terms,
		"status": status,
	})
}

func (h *Handlers) AdminUpdateCustomerSafetyTerms(c *gin.Context) {
	if h.SafetyTerms == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "CUSTOMER_SAFETY_TERMS_UNAVAILABLE", "message": "customer safety terms are not configured"},
		})
		return
	}
	var req customerSafetyTermsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	status, err := h.SafetyTerms.SaveConfig(req.Config)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_CUSTOMER_SAFETY_TERMS",
				"message": err.Error(),
			},
			"status": status,
		})
		return
	}
	terms, loadedStatus, err := h.SafetyTerms.Config()
	if err != nil {
		loadedStatus = status
	}
	c.JSON(http.StatusOK, gin.H{
		"source": h.SafetyTerms.SourceOrDefault(),
		"config": terms,
		"status": loadedStatus,
	})
}

func (h *Handlers) AdminUpdateRuntimeSettings(c *gin.Context) {
	var req runtimeSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	snapshot, fields, err := service.SaveRuntimeSettings(c.Request.Context(), h.Store, h.Config, service.RuntimeSettings(req))
	if err != nil {
		internalError(c, err)
		return
	}
	if len(fields) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_RUNTIME_SETTINGS",
				"message": "runtime settings are invalid",
				"fields":  fields,
			},
			"settings":    snapshot.Settings,
			"defaults":    snapshot.Defaults,
			"environment": snapshot.Environment,
		})
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func (h *Handlers) AdminReviewCount(c *gin.Context) {
	if h.ReviewQueue == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "REVIEWS_UNAVAILABLE", "message": "review queue is unavailable"},
		})
		return
	}
	count, err := h.ReviewQueue.PendingCount(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"pending_count": count})
}

func (h *Handlers) AdminReviewNext(c *gin.Context) {
	if h.ReviewQueue == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "REVIEWS_UNAVAILABLE", "message": "review queue is unavailable"},
		})
		return
	}
	resp, err := h.ReviewQueue.Next(c.Request.Context(), c.Query("cursor"))
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) AdminReviewApprove(c *gin.Context) {
	if h.ReviewQueue == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "REVIEWS_UNAVAILABLE", "message": "review queue is unavailable"},
		})
		return
	}
	var req reviewApproveRequest
	if err := bindOptionalJSON(c, &req); err != nil {
		badRequest(c, err)
		return
	}
	item, err := h.ReviewQueue.Approve(c.Request.Context(), c.Param("id"), service.ReviewApproveRequest{
		Question:   req.Question,
		Answer:     req.Answer,
		TargetPath: req.TargetPath,
	})
	if err != nil {
		badRequest(c, err)
		return
	}
	count, _ := h.ReviewQueue.PendingCount(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"ok": true, "item": item, "pending_count": count})
}

func (h *Handlers) AdminReviewReject(c *gin.Context) {
	if h.ReviewQueue == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "REVIEWS_UNAVAILABLE", "message": "review queue is unavailable"},
		})
		return
	}
	var req reviewRejectRequest
	if err := bindOptionalJSON(c, &req); err != nil {
		badRequest(c, err)
		return
	}
	item, err := h.ReviewQueue.Reject(c.Request.Context(), c.Param("id"), service.ReviewRejectRequest{Reason: req.Reason})
	if err != nil {
		badRequest(c, err)
		return
	}
	count, _ := h.ReviewQueue.PendingCount(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"ok": true, "item": item, "pending_count": count})
}

func (h *Handlers) AdminReviewDelete(c *gin.Context) {
	if h.ReviewQueue == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{"code": "REVIEWS_UNAVAILABLE", "message": "review queue is unavailable"},
		})
		return
	}
	item, err := h.ReviewQueue.Delete(c.Request.Context(), c.Param("id"))
	if err != nil {
		badRequest(c, err)
		return
	}
	count, _ := h.ReviewQueue.PendingCount(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"ok": true, "item": item, "pending_count": count})
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

func (h *Handlers) AdminKnowledgeAssistantChat(c *gin.Context) {
	var req adminChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	stream := true
	if req.Stream != nil {
		stream = *req.Stream
	}
	if stream {
		h.handleAdminKnowledgeAssistantStream(c, req)
		return
	}
	h.handleAdminKnowledgeAssistantPlain(c, req)
}

func (h *Handlers) handleAdminKnowledgeAssistantPlain(c *gin.Context, req adminChatRequest) {
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

func (h *Handlers) handleAdminKnowledgeAssistantStream(c *gin.Context, req adminChatRequest) {
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

func (h *Handlers) AdminListLLMModels(c *gin.Context) {
	models, err := h.Store.ListLLMModels(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	items := make([]adminLLMModelResponse, 0, len(models))
	for _, model := range models {
		items = append(items, adminLLMModelResponseFromStore(model))
	}
	c.JSON(http.StatusOK, gin.H{"models": items})
}

func (h *Handlers) AdminGetLLMModel(c *gin.Context) {
	model, err := h.Store.GetLLMModel(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "model not found")
			return
		}
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"model": adminLLMModelResponseFromStore(*model)})
}

func (h *Handlers) AdminCreateLLMModel(c *gin.Context) {
	var req adminLLMModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	model, err := h.llmModelFromRequest(req, nil, true)
	if err != nil {
		badRequest(c, err)
		return
	}
	model.ID = uuid.NewString()
	if err := h.Store.CreateLLMModel(c.Request.Context(), model); err != nil {
		internalError(c, err)
		return
	}
	created, err := h.Store.GetLLMModel(c.Request.Context(), model.ID)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"model": adminLLMModelResponseFromStore(*created)})
}

func (h *Handlers) AdminUpdateLLMModel(c *gin.Context) {
	existing, err := h.Store.GetLLMModel(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "model not found")
			return
		}
		internalError(c, err)
		return
	}
	var req adminLLMModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	model, err := h.llmModelFromRequest(req, existing, false)
	if err != nil {
		badRequest(c, err)
		return
	}
	if err := h.Store.UpdateLLMModel(c.Request.Context(), model); err != nil {
		internalError(c, err)
		return
	}
	updated, err := h.Store.GetLLMModel(c.Request.Context(), model.ID)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"model": adminLLMModelResponseFromStore(*updated)})
}

func (h *Handlers) AdminDeleteLLMModel(c *gin.Context) {
	if err := h.Store.DeleteLLMModel(c.Request.Context(), c.Param("id")); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handlers) AdminActivateLLMModel(c *gin.Context) {
	if err := h.Store.ActivateLLMModel(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "model not found")
			return
		}
		internalError(c, err)
		return
	}
	model, err := h.Store.GetLLMModel(c.Request.Context(), c.Param("id"))
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"model": adminLLMModelResponseFromStore(*model)})
}

func (h *Handlers) AdminTestLLMModel(c *gin.Context) {
	model, err := h.Store.GetLLMModel(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "model not found")
			return
		}
		internalError(c, err)
		return
	}
	startedAt := time.Now()
	ok, message := h.testLLMModelConnection(c.Request.Context(), model)
	c.JSON(http.StatusOK, adminLLMModelTestResponse{
		OK:        ok,
		Message:   message,
		LatencyMS: time.Since(startedAt).Milliseconds(),
		TestedAt:  time.Now().Format(time.RFC3339Nano),
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
	resp, err := h.wikiFileResponse(c.Request.Context(), abs, rel, info)
	if err != nil {
		if errors.Is(err, errWikiFileTooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"code": "FILE_TOO_LARGE", "message": err.Error()}})
			return
		}
		if errors.Is(err, errWikiInvalidEncoding) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "INVALID_ENCODING", "message": err.Error()}})
			return
		}
		internalError(c, err)
		return
	}
	log.Printf("audit wiki.file path=%s preview=%s size=%d", rel, resp["preview"], info.Size())
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) AdminWikiSaveFile(c *gin.Context) {
	var req adminWikiSaveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	abs, rel, err := h.resolveWikiPath(req.Path)
	if err != nil {
		badRequest(c, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "FILE_NOT_FOUND", "message": "file not found"}})
			return
		}
		badRequest(c, err)
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "IS_DIRECTORY", "message": "path is a directory"}})
		return
	}
	if !wikiFileEditable(rel) {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": gin.H{"code": "UNSUPPORTED_EDIT_TYPE", "message": "file type is not editable"}})
		return
	}
	if err := h.validateWikiTextSize(c.Request.Context(), []byte(req.Content)); err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"code": "FILE_TOO_LARGE", "message": err.Error()}})
		return
	}
	current, err := os.ReadFile(abs)
	if err != nil {
		internalError(c, err)
		return
	}
	currentSHA := sha256Hex(current)
	if strings.TrimSpace(req.ExpectedSHA256) != "" && !strings.EqualFold(strings.TrimSpace(req.ExpectedSHA256), currentSHA) {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "FILE_CONFLICT", "message": "file changed since it was loaded"}, "sha256": currentSHA})
		return
	}
	if err := os.WriteFile(abs, []byte(req.Content), 0o644); err != nil {
		internalError(c, err)
		return
	}
	nextInfo, err := os.Stat(abs)
	if err != nil {
		internalError(c, err)
		return
	}
	resp, err := h.wikiFileResponse(c.Request.Context(), abs, rel, nextInfo)
	if err != nil {
		internalError(c, err)
		return
	}
	log.Printf("audit wiki.file.save path=%s size=%d", rel, nextInfo.Size())
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) AdminWikiReplaceFile(c *gin.Context) {
	rawPath := strings.TrimSpace(c.PostForm("path"))
	abs, rel, err := h.resolveWikiPath(rawPath)
	if err != nil {
		badRequest(c, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "FILE_NOT_FOUND", "message": "file not found"}})
			return
		}
		badRequest(c, err)
		return
	}
	if info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "IS_DIRECTORY", "message": "path is a directory"}})
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		badRequest(c, fmt.Errorf("parse replacement file: %w", err))
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
	if len(data) == 0 {
		badRequest(c, fmt.Errorf("replacement file is empty"))
		return
	}
	if wikiFileEditable(rel) {
		if err := h.validateWikiTextSize(c.Request.Context(), data); err != nil {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"code": "FILE_TOO_LARGE", "message": err.Error()}})
			return
		}
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		internalError(c, err)
		return
	}
	nextInfo, err := os.Stat(abs)
	if err != nil {
		internalError(c, err)
		return
	}
	resp, err := h.wikiFileResponse(c.Request.Context(), abs, rel, nextInfo)
	if err != nil {
		internalError(c, err)
		return
	}
	log.Printf("audit wiki.file.replace path=%s size=%d", rel, nextInfo.Size())
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

func (h *Handlers) AdminSyncTest(c *gin.Context) {
	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	runner, _, branch := h.gitRunner(c.Request.Context(), "", "")
	target := strings.TrimSpace(runner.URL())
	useRepo := false
	if target == "" && status.RepoReady && status.RemoteReady {
		target = status.Remote
		useRepo = true
	}
	if strings.TrimSpace(target) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"ok": false,
			"error": gin.H{
				"code":    "SYNC_REMOTE_MISSING",
				"message": "未配置 WIKIOS_WIKI_GIT_URL，且当前仓库没有可用 remote。",
			},
			"status": status,
		})
		return
	}
	branch = firstNonEmpty(branch, status.Branch)
	var result wikigit.Result
	if useRepo {
		result, err = runner.Run(c.Request.Context(), "ls-remote", "--heads", target, branch)
	} else {
		result, err = runner.RunAt(c.Request.Context(), "", "ls-remote", "--heads", target, branch)
	}
	if err != nil {
		internalError(c, err)
		return
	}
	if result.ExitCode != 0 {
		gitCommandError(c, http.StatusBadRequest, "GIT_REMOTE_TEST_FAILED", "git remote test failed", result.Stdout, result.Stderr, result.ExitCode)
		return
	}
	if strings.TrimSpace(result.Stdout) == "" {
		gitCommandError(c, http.StatusBadRequest, "GIT_REMOTE_BRANCH_MISSING", "remote branch was not found", result.Stdout, result.Stderr, result.ExitCode)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"remote":    status.Remote,
		"branch":    branch,
		"status":    status,
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	})
}

func (h *Handlers) AdminSyncSetup(c *gin.Context) {
	var req syncSetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	runner, remote, branch := h.gitRunner(c.Request.Context(), req.Remote, req.Branch)
	if !safeGitName(remote) || !safeGitName(branch) {
		badRequest(c, fmt.Errorf("invalid remote or branch"))
		return
	}
	urlValue := runner.URL()
	if strings.TrimSpace(urlValue) == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "SYNC_URL_MISSING", "message": "WIKIOS_WIKI_GIT_URL is required for setup"},
		})
		return
	}

	root := h.Config.MountedWiki.Root
	empty, err := wikigit.DirectoryEmpty(root)
	if err != nil {
		internalError(c, err)
		return
	}
	if empty {
		if err := os.MkdirAll(root, 0o755); err != nil {
			internalError(c, err)
			return
		}
		result, err := runner.RunAt(c.Request.Context(), filepath.Dir(root), "clone", "--branch", branch, urlValue, root)
		if err != nil {
			internalError(c, err)
			return
		}
		if result.ExitCode != 0 {
			gitCommandError(c, http.StatusBadRequest, "GIT_CLONE_FAILED", "git clone failed", result.Stdout, result.Stderr, result.ExitCode)
			return
		}
		status, statusErr := h.gitStatus(c.Request.Context())
		if statusErr != nil {
			internalError(c, statusErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "action": "clone", "status": status, "stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode})
		return
	}
	if !wikigit.IsGitRepository(root) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "WIKI_ROOT_NOT_GIT",
				"message": "wiki root is not empty and is not a git repository; refusing to overwrite it",
			},
		})
		return
	}

	if _, ok := runner.RemoteURL(c.Request.Context(), remote); ok {
		if result, err := runner.Run(c.Request.Context(), "remote", "set-url", remote, urlValue); err != nil {
			internalError(c, err)
			return
		} else if result.ExitCode != 0 {
			gitCommandError(c, http.StatusBadRequest, "GIT_REMOTE_SET_FAILED", "git remote set-url failed", result.Stdout, result.Stderr, result.ExitCode)
			return
		}
	} else {
		if result, err := runner.Run(c.Request.Context(), "remote", "add", remote, urlValue); err != nil {
			internalError(c, err)
			return
		} else if result.ExitCode != 0 {
			gitCommandError(c, http.StatusBadRequest, "GIT_REMOTE_ADD_FAILED", "git remote add failed", result.Stdout, result.Stderr, result.ExitCode)
			return
		}
	}
	fetch, err := runner.Run(c.Request.Context(), "fetch", remote, branch)
	if err != nil {
		internalError(c, err)
		return
	}
	if fetch.ExitCode != 0 {
		gitCommandError(c, http.StatusBadRequest, "GIT_FETCH_FAILED", "git fetch failed", fetch.Stdout, fetch.Stderr, fetch.ExitCode)
		return
	}
	if exists, err := gitBranchExists(c.Request.Context(), runner, branch); err != nil {
		internalError(c, err)
		return
	} else if !exists {
		if dirty, dirtyErr := gitWorktreeDirty(c.Request.Context(), runner); dirtyErr != nil {
			internalError(c, dirtyErr)
			return
		} else if dirty {
			gitCommandError(c, http.StatusBadRequest, "GIT_WORKTREE_DIRTY", "当前知识库有未提交改动，无法自动切换/创建分支。请先提交或手动处理后再修复同步配置。", "", "", 1)
			return
		}
		result, err := runner.Run(c.Request.Context(), "checkout", "-b", branch, "--track", remote+"/"+branch)
		if err != nil {
			internalError(c, err)
			return
		}
		if result.ExitCode != 0 {
			gitCommandError(c, http.StatusBadRequest, "GIT_BRANCH_SETUP_FAILED", "git branch setup failed", result.Stdout, result.Stderr, result.ExitCode)
			return
		}
	} else {
		result, err := runner.Run(c.Request.Context(), "branch", "--set-upstream-to="+remote+"/"+branch, branch)
		if err != nil {
			internalError(c, err)
			return
		}
		if result.ExitCode != 0 {
			gitCommandError(c, http.StatusBadRequest, "GIT_UPSTREAM_SETUP_FAILED", "git upstream setup failed", result.Stdout, result.Stderr, result.ExitCode)
			return
		}
		current, currentErr := gitCurrentBranch(c.Request.Context(), runner)
		if currentErr != nil {
			internalError(c, currentErr)
			return
		}
		if current != "" && current != branch {
			if dirty, dirtyErr := gitWorktreeDirty(c.Request.Context(), runner); dirtyErr != nil {
				internalError(c, dirtyErr)
				return
			} else if dirty {
				gitCommandError(c, http.StatusBadRequest, "GIT_WORKTREE_DIRTY", "当前知识库有未提交改动，无法自动切换到目标分支。请先提交或手动处理后再修复同步配置。", "", "", 1)
				return
			}
			checkout, checkoutErr := runner.Run(c.Request.Context(), "checkout", branch)
			if checkoutErr != nil {
				internalError(c, checkoutErr)
				return
			}
			if checkout.ExitCode != 0 {
				gitCommandError(c, http.StatusBadRequest, "GIT_CHECKOUT_FAILED", "git checkout failed", checkout.Stdout, checkout.Stderr, checkout.ExitCode)
				return
			}
		}
	}
	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "action": "setup", "status": status, "stdout": fetch.Stdout, "stderr": fetch.Stderr, "exit_code": fetch.ExitCode})
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
	diffStat, _ := h.runWikiGit(c.Request.Context(), append([]string{"diff", "--stat", "--"}, paths...)...)
	nameStatus, _ := h.runWikiGit(c.Request.Context(), append([]string{"diff", "--name-status", "--"}, paths...)...)
	message, rule, err := h.Sync.GenerateCommitMessage(c.Request.Context(), service.SyncCommitMessageRequest{
		Files:      toServiceSyncFiles(files),
		DiffStat:   diffStat.Stdout,
		NameStatus: nameStatus.Stdout,
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
	result, err := h.runWikiGit(c.Request.Context(), args...)
	if err != nil {
		internalError(c, err)
		return
	}
	if result.ExitCode != 0 {
		gitCommandError(c, http.StatusBadRequest, "GIT_ADD_FAILED", "git add failed", result.Stdout, result.Stderr, result.ExitCode)
		return
	}
	result, err = h.runWikiGit(c.Request.Context(), "commit", "-m", message)
	if err != nil {
		internalError(c, err)
		return
	}
	if result.ExitCode != 0 {
		gitCommandError(c, http.StatusBadRequest, "GIT_COMMIT_FAILED", "git commit failed", result.Stdout, result.Stderr, result.ExitCode)
		return
	}
	hashResult, _ := h.runWikiGit(c.Request.Context(), "rev-parse", "--short", "HEAD")
	log.Printf("audit sync.commit paths=%d hash=%s", len(paths), strings.TrimSpace(hashResult.Stdout))
	c.JSON(http.StatusOK, gin.H{
		"ok":        true,
		"hash":      strings.TrimSpace(hashResult.Stdout),
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	})
}

func (h *Handlers) AdminSyncPush(c *gin.Context) {
	var req syncPushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	explicitTarget := strings.TrimSpace(req.Remote) != "" || strings.TrimSpace(req.Branch) != ""
	runner, remote, branch := h.gitRunner(c.Request.Context(), req.Remote, req.Branch)
	if !safeGitName(remote) || !safeGitName(branch) {
		badRequest(c, fmt.Errorf("invalid remote or branch"))
		return
	}
	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	if !status.CanPush && status.Clean && !explicitTarget {
		gitCommandError(c, http.StatusBadRequest, "GIT_NOTHING_TO_PUSH", firstNonEmpty(status.SetupHint, "当前没有待推送提交。"), "", "", 0)
		return
	}
	result, err := runner.Run(c.Request.Context(), "push", remote, branch)
	if err != nil {
		internalError(c, err)
		return
	}
	if result.ExitCode != 0 {
		gitCommandError(c, http.StatusBadRequest, "GIT_PUSH_FAILED", "git push failed", result.Stdout, result.Stderr, result.ExitCode)
		return
	}
	log.Printf("audit sync.push remote=%s branch=%s", remote, branch)
	c.JSON(http.StatusOK, gin.H{"ok": true, "remote": remote, "branch": branch, "stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode})
}

func (h *Handlers) AdminSyncPull(c *gin.Context) {
	var req syncPullRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	runner, remote, branch := h.gitRunner(c.Request.Context(), req.Remote, req.Branch)
	if !safeGitName(remote) || !safeGitName(branch) {
		badRequest(c, fmt.Errorf("invalid remote or branch"))
		return
	}
	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		internalError(c, err)
		return
	}
	if !status.RepoReady || !status.RemoteReady || !status.BranchReady {
		gitCommandError(c, http.StatusBadRequest, "GIT_PULL_NOT_READY", firstNonEmpty(status.SetupHint, "同步配置尚未就绪。"), "", "", 1)
		return
	}
	if status.ChangedCount > 0 {
		gitCommandError(c, http.StatusBadRequest, "GIT_WORKTREE_DIRTY", "当前知识库有未提交改动，无法拉取远程更新。请先提交或手动处理后再拉取。", "", "", 1)
		return
	}
	result, err := runner.Run(c.Request.Context(), "pull", "--ff-only", remote, branch)
	if err != nil {
		internalError(c, err)
		return
	}
	if result.ExitCode != 0 {
		gitCommandError(c, http.StatusBadRequest, "GIT_PULL_FAILED", "git pull failed", result.Stdout, result.Stderr, result.ExitCode)
		return
	}
	log.Printf("audit sync.pull remote=%s branch=%s", remote, branch)
	c.JSON(http.StatusOK, gin.H{"ok": true, "remote": remote, "branch": branch, "stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode})
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

func (h *Handlers) estimateCustomerContext(req customerChatRequest) service.ContextUsage {
	if h.ContextCounter == nil {
		return service.ContextUsage{MaxTokens: 1000000, ReserveTokens: 8192, Estimated: true, Counter: "unavailable"}
	}
	messages := []llm.Message{{Role: "system", Content: "WikiOS customer chat context"}}
	for _, item := range toServiceHistory(req.History) {
		role := strings.TrimSpace(strings.ToLower(item.Role))
		if role != "assistant" {
			role = "user"
		}
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		if strings.TrimSpace(item.CreatedAt) != "" {
			content = "[" + strings.TrimSpace(item.CreatedAt) + "] " + content
		}
		messages = append(messages, llm.Message{Role: role, Content: content})
	}
	if question := strings.TrimSpace(req.Message); question != "" {
		messages = append(messages, llm.Message{Role: "user", Content: question})
	}
	if len(req.Context) > 0 {
		if raw, err := json.Marshal(req.Context); err == nil {
			messages = append(messages, llm.Message{Role: "user", Content: "customer_context: " + string(raw)})
		}
	}
	return h.ContextCounter.CountMessages(messages)
}

type syncStatusResponse struct {
	Branch                  string           `json:"branch"`
	Remote                  string           `json:"remote"`
	Ahead                   int              `json:"ahead"`
	Behind                  int              `json:"behind"`
	Files                   []syncStatusFile `json:"files"`
	ChangedCount            int              `json:"changed_count"`
	PushCount               int              `json:"push_count"`
	CanPush                 bool             `json:"can_push"`
	CanCommit               bool             `json:"can_commit"`
	RepoReady               bool             `json:"repo_ready"`
	RemoteReady             bool             `json:"remote_ready"`
	BranchReady             bool             `json:"branch_ready"`
	AuthConfigured          bool             `json:"auth_configured"`
	NeedsSetup              bool             `json:"needs_setup"`
	Clean                   bool             `json:"clean"`
	ConfiguredURLRedacted   string           `json:"configured_url_redacted"`
	RemoteURLRedacted       string           `json:"remote_url_redacted"`
	RemoteMatchesConfigured bool             `json:"remote_matches_configured"`
	SetupHint               string           `json:"setup_hint"`
	CommitsToPush           []syncCommitInfo `json:"commits_to_push"`
	RecentCommits           []syncCommitInfo `json:"recent_commits"`
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

var (
	errWikiFileTooLarge    = errors.New("wiki file is too large to edit")
	errWikiInvalidEncoding = errors.New("wiki file is not valid utf-8 text")
	wikiEditableTextExts   = map[string]string{
		".md":       "markdown",
		".markdown": "markdown",
		".qmd":      "markdown",
		".yaml":     "yaml",
		".yml":      "yaml",
		".json":     "json",
		".txt":      "text",
		".csv":      "csv",
		".tsv":      "tsv",
		".log":      "text",
		".toml":     "toml",
		".ini":      "ini",
		".html":     "html",
		".css":      "css",
		".js":       "javascript",
		".ts":       "typescript",
	}
)

func (h *Handlers) wikiFileResponse(ctx context.Context, abs string, rel string, info os.FileInfo) (gin.H, error) {
	preview := wikiPreviewKind(rel)
	editable := wikiFileEditable(rel)
	textKind := wikiTextKind(rel)
	resp := gin.H{
		"path":         rel,
		"name":         filepath.Base(rel),
		"size":         info.Size(),
		"modified_at":  info.ModTime().Format(time.RFC3339Nano),
		"preview":      preview,
		"editable":     editable,
		"text_kind":    textKind,
		"encoding":     "",
		"sha256":       "",
		"download_url": "/api/v1/admin/wiki/download?path=" + urlQueryEscape(rel),
	}
	if !editable {
		return resp, nil
	}
	if err := h.validateWikiTextSize(ctx, nil, int64(info.Size())); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(content) {
		return nil, errWikiInvalidEncoding
	}
	resp["content"] = string(content)
	resp["encoding"] = "utf-8"
	resp["sha256"] = sha256Hex(content)
	return resp, nil
}

func (h *Handlers) validateWikiTextSize(ctx context.Context, content []byte, sizes ...int64) error {
	runtimeSettings := service.LoadRuntimeSettingsOrDefault(ctx, h.Store, h.Config)
	maxBytes := int64(runtimeSettings.Knowledge.MaxTextFileKB) * 1024
	if maxBytes <= 0 {
		maxBytes = 500 * 1024
	}
	size := int64(len(content))
	if len(sizes) > 0 {
		size = sizes[0]
	}
	if size > maxBytes {
		return fmt.Errorf("%w: file exceeds editable text limit of %dKB", errWikiFileTooLarge, maxBytes/1024)
	}
	return nil
}

func wikiFileEditable(path string) bool {
	return wikiTextKind(path) != ""
}

func wikiTextKind(path string) string {
	return wikiEditableTextExts[strings.ToLower(filepath.Ext(path))]
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func wikiPreviewKind(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown", ".qmd":
		return "markdown"
	default:
		return "download"
	}
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}

func (h *Handlers) runWikiGit(ctx context.Context, args ...string) (wikigit.Result, error) {
	runner, _, _ := h.gitRunner(ctx, "", "")
	return runner.Run(ctx, args...)
}

func (h *Handlers) gitRunner(ctx context.Context, remoteOverride string, branchOverride string) (*wikigit.Runner, string, string) {
	runtimeSettings := service.LoadRuntimeSettingsOrDefault(ctx, h.Store, h.Config)
	remote := firstNonEmpty(remoteOverride, runtimeSettings.Sync.Remote)
	branch := firstNonEmpty(branchOverride, runtimeSettings.Sync.Branch)
	return wikigit.NewRunner(wikigit.ConfigFromEnv(h.Config.MountedWiki.Root, remote, branch)), remote, branch
}

func gitCommandError(c *gin.Context, status int, code string, fallback string, stdout string, stderr string, exitCode int) {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	message := firstNonEmpty(stdout, stderr, fallback)
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":      code,
			"message":   message,
			"stdout":    stdout,
			"stderr":    stderr,
			"exit_code": exitCode,
		},
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": exitCode,
	})
}

func (h *Handlers) gitStatus(ctx context.Context) (syncStatusResponse, error) {
	runtimeSettings := service.LoadRuntimeSettingsOrDefault(ctx, h.Store, h.Config)
	runner, remote, branch := h.gitRunner(ctx, "", "")
	status := syncStatusResponse{
		Branch:         branch,
		Remote:         remote,
		Files:          []syncStatusFile{},
		RepoReady:      wikigit.IsGitRepository(h.Config.MountedWiki.Root),
		AuthConfigured: runner.AuthConfigured(),
	}
	if runner.URL() != "" {
		status.ConfiguredURLRedacted = runner.RedactedURL(runner.URL())
	}
	if remoteURL, ok := runner.RemoteURL(ctx, remote); ok {
		status.RemoteReady = true
		status.RemoteURLRedacted = runner.RedactedURL(remoteURL)
		status.AuthConfigured = runner.AuthConfiguredFor(remoteURL)
		if configured := strings.TrimSpace(runner.URL()); configured != "" {
			status.RemoteMatchesConfigured = strings.TrimSpace(remoteURL) == configured
		}
	} else if runner.URL() != "" {
		status.RemoteURLRedacted = runner.RedactedURL(runner.URL())
	}
	if !status.RepoReady {
		status.BranchReady = branch != ""
		status.NeedsSetup = true
		status.Clean = true
		status.SetupHint = syncSetupHint(status, runner)
		return status, nil
	}

	result, err := runner.Run(ctx, "status", "--porcelain=v1", "--untracked-files=all", "-b", "-z")
	if err != nil {
		return syncStatusResponse{}, err
	}
	if result.ExitCode != 0 {
		status.NeedsSetup = true
		status.SetupHint = firstNonEmpty(strings.TrimSpace(result.Stderr), "git status failed")
		return status, nil
	}
	status.Files = append(status.Files, parseStatusOutput(result.Stdout, &status)...)
	status.Files = mergeUntrackedFiles(status.Files, h.gitUntrackedFiles(ctx))
	sort.Slice(status.Files, func(i, j int) bool {
		return status.Files[i].Path < status.Files[j].Path
	})
	if strings.TrimSpace(status.Branch) == "" || status.Branch == "HEAD" {
		status.Branch = runtimeSettings.Sync.Branch
	}
	upstreamReady, upstreamErr := gitRefExists(ctx, runner, "@{u}")
	if upstreamErr != nil {
		return syncStatusResponse{}, upstreamErr
	}
	remoteBranchReady := false
	if status.RemoteReady && strings.TrimSpace(status.Branch) != "" {
		if ok, refErr := gitRefExists(ctx, runner, remote+"/"+status.Branch); refErr != nil {
			return syncStatusResponse{}, refErr
		} else {
			remoteBranchReady = ok
		}
	}
	status.BranchReady = strings.TrimSpace(status.Branch) != "" && strings.TrimSpace(status.Branch) != "HEAD" && (upstreamReady || remoteBranchReady)
	if status.RemoteReady && strings.TrimSpace(status.Branch) != "" {
		fetch, fetchErr := runner.Run(ctx, "fetch", remote, status.Branch)
		if fetchErr != nil {
			return syncStatusResponse{}, fetchErr
		}
		if fetch.ExitCode == 0 {
			if count, countErr := gitRevCount(ctx, runner, "HEAD.."+remote+"/"+status.Branch); countErr != nil {
				return syncStatusResponse{}, countErr
			} else {
				status.Behind = count
			}
			if count, countErr := gitRevCount(ctx, runner, remote+"/"+status.Branch+"..HEAD"); countErr != nil {
				return syncStatusResponse{}, countErr
			} else {
				status.Ahead = count
			}
		}
	}
	status.ChangedCount = len(status.Files)
	status.CommitsToPush = h.gitLog(ctx, "@{u}..HEAD", 20)
	if len(status.CommitsToPush) == 0 && remoteBranchReady {
		status.CommitsToPush = h.gitLog(ctx, remote+"/"+status.Branch+"..HEAD", 20)
	}
	status.RecentCommits = h.gitLog(ctx, "", 10)
	status.PushCount = len(status.CommitsToPush)
	if status.PushCount == 0 {
		status.PushCount = status.Ahead
	}
	status.CanCommit = status.RepoReady && status.ChangedCount > 0
	status.CanPush = status.RepoReady && status.RemoteReady && status.BranchReady && status.PushCount > 0
	status.Clean = status.ChangedCount == 0 && status.PushCount == 0 && status.Behind == 0
	status.NeedsSetup = !status.RepoReady || !status.RemoteReady || !status.BranchReady || !status.AuthConfigured
	status.SetupHint = syncSetupHint(status, runner)
	return status, nil
}

func (h *Handlers) gitLog(ctx context.Context, rev string, limit int) []syncCommitInfo {
	args := []string{"log", fmt.Sprintf("-n%d", limit), "--pretty=format:%h%x09%ad%x09%an%x09%s", "--date=short"}
	if strings.TrimSpace(rev) != "" {
		args = append(args, rev)
	}
	result, err := h.runWikiGit(ctx, args...)
	if err != nil || result.ExitCode != 0 {
		return nil
	}
	out := []syncCommitInfo{}
	for _, line := range strings.Split(result.Stdout, "\n") {
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

func (h *Handlers) gitUntrackedFiles(ctx context.Context) []syncStatusFile {
	result, err := h.runWikiGit(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil || result.ExitCode != 0 {
		return nil
	}
	files := []syncStatusFile{}
	for _, entry := range splitNUL(result.Stdout) {
		path := filepath.ToSlash(strings.TrimSpace(entry))
		if path == "" || path == ".." || strings.HasPrefix(path, "../") || strings.Contains("/"+path+"/", "/.git/") {
			continue
		}
		files = append(files, syncStatusFile{
			Path:      path,
			Status:    "?",
			Index:     "?",
			Worktree:  "?",
			Preview:   wikiPreviewKind(path),
			DefaultOn: !strings.HasPrefix(path, ".obsidian/"),
		})
	}
	return files
}

func mergeUntrackedFiles(files []syncStatusFile, untracked []syncStatusFile) []syncStatusFile {
	if len(untracked) == 0 {
		return files
	}
	seen := map[string]bool{}
	for _, file := range files {
		seen[file.Path] = true
	}
	for _, file := range untracked {
		if seen[file.Path] {
			continue
		}
		files = append(files, file)
		seen[file.Path] = true
	}
	return files
}

func syncSetupHint(status syncStatusResponse, runner *wikigit.Runner) string {
	if !status.RepoReady {
		if strings.TrimSpace(runner.URL()) == "" {
			return "知识库目录还不是 Git 仓库。配置 WIKIOS_WIKI_GIT_URL 后可在同步页执行修复同步配置。"
		}
		return "知识库目录还不是 Git 仓库，可执行修复同步配置来 clone/初始化。"
	}
	if strings.TrimSpace(runner.URL()) == "" && !status.RemoteReady {
		return "未配置 WIKIOS_WIKI_GIT_URL，且当前仓库没有可用 remote。"
	}
	if !status.RemoteReady {
		return "Git remote 未配置或不可用，可执行修复同步配置。"
	}
	if !status.BranchReady {
		return "当前分支或 upstream 未就绪，可执行修复同步配置。"
	}
	if !status.AuthConfigured {
		return "HTTPS 同步需要配置 WIKIOS_WIKI_GIT_TOKEN；SSH 同步请确认 key 可非交互使用。"
	}
	return ""
}

func gitCurrentBranch(ctx context.Context, runner *wikigit.Runner) (string, error) {
	result, err := runner.Run(ctx, "branch", "--show-current")
	if err != nil || result.ExitCode != 0 {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func gitBranchExists(ctx context.Context, runner *wikigit.Runner, branch string) (bool, error) {
	return gitRefExists(ctx, runner, "refs/heads/"+branch)
}

func gitRefExists(ctx context.Context, runner *wikigit.Runner, ref string) (bool, error) {
	result, err := runner.Run(ctx, "rev-parse", "--verify", ref)
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

func gitWorktreeDirty(ctx context.Context, runner *wikigit.Runner) (bool, error) {
	result, err := runner.Run(ctx, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		return false, fmt.Errorf("git status failed: %s", strings.TrimSpace(result.Stderr))
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}

func gitRevCount(ctx context.Context, runner *wikigit.Runner, rev string) (int, error) {
	result, err := runner.Run(ctx, "rev-list", "--count", rev)
	if err != nil {
		return 0, err
	}
	if result.ExitCode != 0 {
		return 0, nil
	}
	count, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil {
		return 0, nil
	}
	return count, nil
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

func parseStatusOutput(stdout string, status *syncStatusResponse) []syncStatusFile {
	if strings.Contains(stdout, "\x00") {
		return parseStatusOutputZ(stdout, status)
	}
	files := []syncStatusFile{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			parseBranchLine(strings.TrimPrefix(line, "## "), status)
			continue
		}
		file, ok := parseStatusLine(line)
		if ok {
			files = append(files, file)
		}
	}
	return files
}

func parseStatusOutputZ(stdout string, status *syncStatusResponse) []syncStatusFile {
	files := []syncStatusFile{}
	entries := splitNUL(stdout)
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if strings.TrimSpace(entry) == "" {
			continue
		}
		if strings.HasPrefix(entry, "## ") {
			parseBranchLine(strings.TrimPrefix(entry, "## "), status)
			continue
		}
		file, needsOldPath, ok := parseStatusEntryZ(entry)
		if !ok {
			continue
		}
		if needsOldPath && i+1 < len(entries) {
			i++
			file.OldPath = filepath.ToSlash(strings.TrimSpace(entries[i]))
		}
		files = append(files, file)
	}
	return files
}

func splitNUL(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseStatusEntryZ(entry string) (syncStatusFile, bool, bool) {
	if len(entry) < 4 {
		return syncStatusFile{}, false, false
	}
	index := strings.TrimSpace(entry[:1])
	worktree := strings.TrimSpace(entry[1:2])
	path := strings.TrimSpace(entry[3:])
	status := firstNonEmpty(index, worktree)
	deleted := index == "D" || worktree == "D"
	file := syncStatusFile{
		Path:      filepath.ToSlash(path),
		Status:    status,
		Index:     index,
		Worktree:  worktree,
		Preview:   wikiPreviewKind(path),
		DefaultOn: !strings.HasPrefix(filepath.ToSlash(path), ".obsidian/"),
		Deleted:   deleted,
	}
	needsOldPath := index == "R" || worktree == "R" || index == "C" || worktree == "C"
	return file, needsOldPath, true
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
		for _, path := range normalizeSyncRequestPath(raw, allowed) {
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
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("paths is required")
	}
	return out, nil
}

func normalizeSyncRequestPath(raw string, allowed map[string]bool) []string {
	path := filepath.ToSlash(strings.TrimSpace(raw))
	if path == "" || allowed[path] {
		return []string{path}
	}
	if !strings.ContainsAny(path, " \t\r\n") {
		return []string{path}
	}
	parts := strings.Fields(path)
	if len(parts) <= 1 {
		return []string{path}
	}
	for _, part := range parts {
		if !allowed[filepath.ToSlash(strings.TrimSpace(part))] {
			return []string{path}
		}
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, filepath.ToSlash(strings.TrimSpace(part)))
	}
	return out
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

func (h *Handlers) llmModelFromRequest(req adminLLMModelRequest, existing *store.LLMModel, create bool) (*store.LLMModel, error) {
	displayName := strings.TrimSpace(req.DisplayName)
	provider := firstNonEmpty(strings.TrimSpace(req.Provider), "openai-compatible")
	baseURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	modelName := strings.TrimSpace(req.ModelName)
	apiKey := strings.TrimSpace(req.APIKey)
	timeoutSec := req.TimeoutSec
	adminTimeoutSec := req.AdminTimeoutSec
	model := &store.LLMModel{}
	if existing != nil {
		*model = *existing
		if displayName == "" {
			displayName = existing.DisplayName
		}
		if strings.TrimSpace(req.Provider) == "" {
			provider = existing.Provider
		}
		if baseURL == "" {
			baseURL = existing.BaseURL
		}
		if modelName == "" {
			modelName = existing.ModelName
		}
		if apiKey == "" {
			apiKey = existing.APIKey
		}
		if timeoutSec <= 0 {
			timeoutSec = existing.TimeoutSec
		}
		if adminTimeoutSec <= 0 {
			adminTimeoutSec = existing.AdminTimeoutSec
		}
	}
	if modelName == "" {
		return nil, fmt.Errorf("model_name is required")
	}
	if displayName == "" {
		displayName = modelName
	}
	if baseURL == "" {
		return nil, fmt.Errorf("base_url is required")
	}
	if err := validateLLMBaseURL(baseURL); err != nil {
		return nil, err
	}
	if apiKey == "" && create {
		return nil, fmt.Errorf("api_key is required")
	}
	if timeoutSec <= 0 {
		timeoutSec = firstPositiveInt(h.Config.LLM.TimeoutSec, 90)
	}
	if adminTimeoutSec <= 0 {
		adminTimeoutSec = firstPositiveInt(h.Config.LLM.AdminTimeoutSec, 300)
	}
	model.DisplayName = displayName
	model.Provider = provider
	model.BaseURL = baseURL
	model.ModelName = modelName
	model.APIKey = apiKey
	model.TimeoutSec = timeoutSec
	model.AdminTimeoutSec = adminTimeoutSec
	return model, nil
}

func validateLLMBaseURL(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("base_url must be a full http(s) endpoint")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("base_url must use http or https")
	}
	return nil
}

func (h *Handlers) testLLMModelConnection(ctx context.Context, model *store.LLMModel) (bool, string) {
	if model == nil {
		return false, "模型不存在"
	}
	if strings.TrimSpace(model.BaseURL) == "" || strings.TrimSpace(model.ModelName) == "" || strings.TrimSpace(model.APIKey) == "" {
		return false, "模型配置不完整，请先补齐端点、模型名和 API Key"
	}
	configTimeoutSec := 0
	if h.Config != nil {
		configTimeoutSec = h.Config.LLM.TimeoutSec
	}
	timeoutSec := firstPositiveInt(model.TimeoutSec, configTimeoutSec, 30)
	client := llm.NewClient(llm.ClientConfig{
		APIKey:     model.APIKey,
		BaseURL:    model.BaseURL,
		TimeoutSec: timeoutSec,
	})
	text, err := client.Chat(llm.WithRequestTimeout(ctx, time.Duration(timeoutSec)*time.Second), model.ModelName, []llm.Message{
		{Role: "system", Content: "You are a connection test for an OpenAI-compatible chat completion endpoint. Reply with a short OK."},
		{Role: "user", Content: "Reply with OK."},
	})
	if err != nil {
		return false, "连接失败：" + sanitizeLLMModelTestError(err, model.APIKey)
	}
	if strings.TrimSpace(text) == "" {
		return false, "连接失败：接口可访问，但模型返回为空"
	}
	return true, "连接成功"
}

func sanitizeLLMModelTestError(err error, apiKey string) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		message = "未知错误"
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey != "" {
		message = strings.ReplaceAll(message, apiKey, maskAPIKey(apiKey))
	}
	return message
}

func adminLLMModelResponseFromStore(model store.LLMModel) adminLLMModelResponse {
	return adminLLMModelResponse{
		ID:              model.ID,
		DisplayName:     model.DisplayName,
		Provider:        model.Provider,
		BaseURL:         model.BaseURL,
		ModelName:       model.ModelName,
		HasAPIKey:       strings.TrimSpace(model.APIKey) != "",
		APIKeyMask:      maskAPIKey(model.APIKey),
		IsActive:        model.IsActive,
		TimeoutSec:      model.TimeoutSec,
		AdminTimeoutSec: model.AdminTimeoutSec,
		CreatedAt:       model.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:       model.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func maskAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	runes := []rune(apiKey)
	if len(runes) <= 8 {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:4]) + "..." + string(runes[len(runes)-4:])
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func notFound(c *gin.Context, message string) {
	c.JSON(http.StatusNotFound, gin.H{
		"error": gin.H{
			"code":    "NOT_FOUND",
			"message": message,
		},
	})
}

func badRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{
		"error": gin.H{
			"code":    "BAD_REQUEST",
			"message": err.Error(),
		},
	})
}

func streamNotSupported(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"error": gin.H{
			"code":    "STREAM_NOT_SUPPORTED",
			"message": "external customer chat only supports non-streaming JSON responses",
		},
	})
}

func internalError(c *gin.Context, err error) {
	log.Printf("error %s %s trace=%s err=%v", c.Request.Method, c.Request.URL.Path, traceID(c), err)
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
			ID:        strings.TrimSpace(item.ID),
			Role:      role,
			Content:   content,
			CreatedAt: strings.TrimSpace(item.CreatedAt),
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
