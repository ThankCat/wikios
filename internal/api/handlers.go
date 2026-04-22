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

	"wikios/internal/service"
	"wikios/internal/task"
)

type Handlers struct {
	PublicQuery *service.PublicQueryService
	AdminQuery  *service.AdminQueryService
	Ingest      *service.IngestService
	Lint        *service.LintService
	Reflect     *service.ReflectService
	Repair      *service.RepairService
	Sync        *service.SyncService
	Tasks       *task.Manager
}

func NewHandlers(
	publicQuery *service.PublicQueryService,
	adminQuery *service.AdminQueryService,
	ingest *service.IngestService,
	lintSvc *service.LintService,
	reflectSvc *service.ReflectService,
	repairSvc *service.RepairService,
	syncSvc *service.SyncService,
	tasks *task.Manager,
) *Handlers {
	return &Handlers{
		PublicQuery: publicQuery,
		AdminQuery:  adminQuery,
		Ingest:      ingest,
		Lint:        lintSvc,
		Reflect:     reflectSvc,
		Repair:      repairSvc,
		Sync:        syncSvc,
		Tasks:       tasks,
	}
}

func (h *Handlers) PublicAnswer(c *gin.Context) {
	var req service.PublicAnswerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	resp, err := h.PublicQuery.Answer(c.Request.Context(), traceID(c), req)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) AdminIngest(c *gin.Context) {
	var req service.IngestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	h.submitTask(c, "ingest", func(ctx context.Context, trace string, taskModel *task.Task) (map[string]any, error) {
		return h.Ingest.Run(ctx, taskModel, trace, req)
	})
}

func (h *Handlers) AdminQueryRun(c *gin.Context) {
	var req service.AdminQueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	h.submitTask(c, "query", func(ctx context.Context, trace string, taskModel *task.Task) (map[string]any, error) {
		return h.AdminQuery.Run(ctx, taskModel, trace, req)
	})
}

func (h *Handlers) AdminLint(c *gin.Context) {
	var req service.LintRequest
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		badRequest(c, err)
		return
	}
	h.submitTask(c, "lint", func(ctx context.Context, trace string, taskModel *task.Task) (map[string]any, error) {
		return h.Lint.Run(ctx, taskModel, trace, req)
	})
}

func (h *Handlers) AdminReflect(c *gin.Context) {
	var req service.ReflectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	h.submitTask(c, "reflect", func(ctx context.Context, trace string, taskModel *task.Task) (map[string]any, error) {
		return h.Reflect.Run(ctx, taskModel, trace, req)
	})
}

func (h *Handlers) AdminRepairApplyLowRisk(c *gin.Context) {
	var req service.ApplyLowRiskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	h.submitTask(c, "repair_low_risk", func(ctx context.Context, trace string, taskModel *task.Task) (map[string]any, error) {
		return h.Repair.ApplyLowRisk(ctx, taskModel, trace, req)
	})
}

func (h *Handlers) AdminRepairApplyProposal(c *gin.Context) {
	var req service.ApplyProposalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	h.submitTask(c, "repair_proposal", func(ctx context.Context, trace string, taskModel *task.Task) (map[string]any, error) {
		return h.Repair.ApplyProposal(ctx, taskModel, trace, req)
	})
}

func (h *Handlers) AdminSync(c *gin.Context) {
	var req service.SyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	h.submitTask(c, "sync", func(ctx context.Context, trace string, taskModel *task.Task) (map[string]any, error) {
		return h.Sync.Run(ctx, taskModel, trace, req)
	})
}

type AdminChatStreamRequest struct {
	Mode    string         `json:"mode"`
	Message string         `json:"message"`
	Options map[string]any `json:"options"`
}

type sseEmitter struct {
	c *gin.Context
}

func (e *sseEmitter) Emit(event service.StreamEvent) {
	writeSSE(e.c, event.Type, event.Data)
}

func (h *Handlers) AdminChatStream(c *gin.Context) {
	var req AdminChatStreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		badRequest(c, fmt.Errorf("mode is required"))
		return
	}
	taskType := mode
	if mode == "repair" {
		action := strings.TrimSpace(stringOption(req.Options, "action"))
		if action == "proposal" {
			taskType = "repair_proposal"
		} else {
			taskType = "repair_low_risk"
		}
	}
	taskModel, err := h.Tasks.Create(c.Request.Context(), taskType)
	if err != nil {
		internalError(c, err)
		return
	}
	if err := h.Tasks.MarkRunning(c.Request.Context(), taskModel); err != nil {
		internalError(c, err)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	writeSSE(c, "task", taskModel)
	writeSSE(c, "meta", map[string]any{
		"mode":    mode,
		"task_id": taskModel.ID,
		"started": time.Now().Format(time.RFC3339Nano),
	})

	streamCtx := service.WithStreamEmitter(c.Request.Context(), &sseEmitter{c: c})
	trace := traceID(c)
	result, runErr := h.runAdminConversation(streamCtx, trace, taskModel, req)
	if err := h.Tasks.Complete(c.Request.Context(), taskModel, result, runErr); err != nil && runErr == nil {
		runErr = err
	}
	writeSSE(c, "task", taskModel)
	if runErr != nil {
		writeSSE(c, "error", map[string]any{
			"message": runErr.Error(),
			"task_id": taskModel.ID,
		})
	} else {
		writeSSE(c, "result", result)
	}
	writeSSE(c, "done", map[string]any{"task_id": taskModel.ID})
}

func (h *Handlers) AdminTaskStatus(c *gin.Context) {
	taskModel, err := h.Tasks.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, taskModel)
}

type taskRunner func(ctx context.Context, traceID string, taskModel *task.Task) (map[string]any, error)

func (h *Handlers) submitTask(c *gin.Context, taskType string, runner taskRunner) {
	trace := traceID(c)
	taskModel, err := h.Tasks.Submit(c.Request.Context(), taskType, func(ctx context.Context, taskModel *task.Task) (map[string]any, error) {
		return runner(ctx, trace, taskModel)
	})
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"task_id": taskModel.ID,
		"status":  taskModel.Status,
	})
}

func (h *Handlers) runAdminConversation(ctx context.Context, trace string, taskModel *task.Task, req AdminChatStreamRequest) (map[string]any, error) {
	switch req.Mode {
	case "query":
		return h.AdminQuery.Run(ctx, taskModel, trace, service.AdminQueryRequest{
			Question:    firstNonEmpty(strings.TrimSpace(req.Message), stringOption(req.Options, "question")),
			WriteOutput: boolOption(req.Options, "write_output", true),
		})
	case "ingest":
		return h.Ingest.Run(ctx, taskModel, trace, service.IngestRequest{
			InputType:   firstNonEmpty(stringOption(req.Options, "input_type"), "file"),
			Path:        firstNonEmpty(stringOption(req.Options, "path"), strings.TrimSpace(req.Message)),
			Interactive: boolOption(req.Options, "interactive", false),
		})
	case "lint":
		return h.Lint.Run(ctx, taskModel, trace, service.LintRequest{
			WriteReport:    boolOption(req.Options, "write_report", true),
			AutoFixLowRisk: boolOption(req.Options, "auto_fix_low_risk", false),
		})
	case "reflect":
		return h.Reflect.Run(ctx, taskModel, trace, service.ReflectRequest{
			Topic:          firstNonEmpty(strings.TrimSpace(req.Message), stringOption(req.Options, "topic")),
			WriteReport:    boolOption(req.Options, "write_report", true),
			AutoFixLowRisk: boolOption(req.Options, "auto_fix_low_risk", false),
		})
	case "repair":
		if strings.TrimSpace(stringOption(req.Options, "action")) == "proposal" {
			return h.Repair.ApplyProposal(ctx, taskModel, trace, service.ApplyProposalRequest{
				ProposalID: firstNonEmpty(stringOption(req.Options, "proposal_id"), strings.TrimSpace(req.Message)),
			})
		}
		return h.Repair.ApplyLowRisk(ctx, taskModel, trace, service.ApplyLowRiskRequest{
			Path: firstNonEmpty(stringOption(req.Options, "path"), strings.TrimSpace(req.Message)),
			Ops:  anySliceOption(req.Options, "ops"),
		})
	case "sync":
		return h.Sync.Run(ctx, taskModel, trace, service.SyncRequest{
			Message: firstNonEmpty(strings.TrimSpace(req.Message), stringOption(req.Options, "message")),
		})
	default:
		return nil, fmt.Errorf("unsupported admin chat mode %q", req.Mode)
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

func stringOption(options map[string]any, key string) string {
	if options == nil {
		return ""
	}
	raw, ok := options[key]
	if !ok {
		return ""
	}
	if value, ok := raw.(string); ok {
		return value
	}
	return ""
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

func optionsValue(options map[string]any, key string) any {
	if options == nil {
		return nil
	}
	return options[key]
}

func anySliceOption(options map[string]any, key string) []any {
	if options == nil {
		return nil
	}
	raw, ok := options[key]
	if !ok || raw == nil {
		return nil
	}
	if values, ok := raw.([]any); ok {
		return values
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
