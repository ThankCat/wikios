package api

import (
	"context"
	"io"
	"net/http"

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
