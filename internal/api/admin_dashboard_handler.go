package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"wikios/internal/service"

	"github.com/gin-gonic/gin"
)

type adminDashboardResponse struct {
	ActiveModel     *adminLLMModelResponse       `json:"active_model,omitempty"`
	ModelsTotal     int                          `json:"models_total"`
	ReviewPending   int                          `json:"review_pending"`
	Sync            adminDashboardSyncSummary    `json:"sync"`
	QMD             adminDashboardQMDSummary     `json:"qmd"`
	CustomerChatLog adminDashboardLogSummary     `json:"customer_chat_log"`
	GeneratedAt     string                       `json:"generated_at"`
	RecentErrors    []adminDashboardErrorSummary `json:"recent_errors"`
}

type adminDashboardSyncSummary struct {
	Branch                  string `json:"branch"`
	Remote                  string `json:"remote"`
	Ahead                   int    `json:"ahead"`
	Behind                  int    `json:"behind"`
	ChangedCount            int    `json:"changed_count"`
	CanPush                 bool   `json:"can_push"`
	RepoReady               bool   `json:"repo_ready"`
	RemoteReady             bool   `json:"remote_ready"`
	BranchReady             bool   `json:"branch_ready"`
	AuthConfigured          bool   `json:"auth_configured"`
	NeedsSetup              bool   `json:"needs_setup"`
	RemoteURLRedacted       string `json:"remote_url_redacted"`
	ConfiguredURLRedacted   string `json:"configured_url_redacted"`
	RemoteMatchesConfigured bool   `json:"remote_matches_configured"`
	SetupHint               string `json:"setup_hint"`
	Error                   string `json:"error,omitempty"`
}

type adminDashboardQMDSummary struct {
	OK      bool   `json:"ok"`
	Index   string `json:"index"`
	Root    string `json:"root"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type adminDashboardLogSummary struct {
	Enabled       bool   `json:"enabled"`
	Redact        bool   `json:"redact"`
	RetentionDays int    `json:"retention_days"`
	Path          string `json:"path,omitempty"`
}

type adminDashboardErrorSummary struct {
	Scope   string `json:"scope"`
	Message string `json:"message"`
}

func (h *Handlers) AdminDashboard(c *gin.Context) {
	resp := adminDashboardResponse{
		GeneratedAt:     time.Now().Format(time.RFC3339Nano),
		CustomerChatLog: h.dashboardCustomerChatLog(),
		QMD:             h.dashboardQMDStatus(c.Request.Context()),
		RecentErrors:    []adminDashboardErrorSummary{},
	}
	models, err := h.Store.ListLLMModels(c.Request.Context())
	if err != nil {
		resp.RecentErrors = append(resp.RecentErrors, adminDashboardErrorSummary{Scope: "models", Message: err.Error()})
	} else {
		resp.ModelsTotal = len(models)
		for _, model := range models {
			if model.IsActive {
				active := adminLLMModelResponseFromStore(model)
				resp.ActiveModel = &active
				break
			}
		}
	}

	if h.ReviewQueue != nil {
		count, err := h.ReviewQueue.PendingCount(c.Request.Context())
		if err != nil {
			resp.RecentErrors = append(resp.RecentErrors, adminDashboardErrorSummary{Scope: "review", Message: err.Error()})
		} else {
			resp.ReviewPending = count
		}
	}

	status, err := h.gitStatus(c.Request.Context())
	if err != nil {
		runtimeSettings := service.LoadRuntimeSettingsOrDefault(c.Request.Context(), h.Store, h.Config)
		resp.Sync = adminDashboardSyncSummary{
			Remote: runtimeSettings.Sync.Remote,
			Error:  err.Error(),
		}
		resp.RecentErrors = append(resp.RecentErrors, adminDashboardErrorSummary{Scope: "sync", Message: err.Error()})
	} else {
		resp.Sync = adminDashboardSyncSummary{
			Branch:                  status.Branch,
			Remote:                  status.Remote,
			Ahead:                   status.Ahead,
			Behind:                  status.Behind,
			ChangedCount:            status.ChangedCount,
			CanPush:                 status.CanPush,
			RepoReady:               status.RepoReady,
			RemoteReady:             status.RemoteReady,
			BranchReady:             status.BranchReady,
			AuthConfigured:          status.AuthConfigured,
			NeedsSetup:              status.NeedsSetup,
			RemoteURLRedacted:       status.RemoteURLRedacted,
			ConfiguredURLRedacted:   status.ConfiguredURLRedacted,
			RemoteMatchesConfigured: status.RemoteMatchesConfigured,
			SetupHint:               status.SetupHint,
		}
	}

	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) dashboardCustomerChatLog() adminDashboardLogSummary {
	settings := service.LoadRuntimeSettingsOrDefault(context.Background(), h.Store, h.Config)
	return adminDashboardLogSummary{
		Enabled:       settings.AnswerLog.Enabled,
		Redact:        settings.AnswerLog.Redact,
		RetentionDays: settings.AnswerLog.RetentionDays,
		Path:          ".workspace/customer_chat_logs/*.jsonl",
	}
}

func (h *Handlers) dashboardQMDStatus(ctx context.Context) adminDashboardQMDSummary {
	summary := adminDashboardQMDSummary{
		Index: h.Config.MountedWiki.QMDIndex,
		Root:  h.Config.MountedWiki.Root,
	}
	if _, err := os.Stat(h.Config.MountedWiki.Root); err != nil {
		summary.Error = fmt.Sprintf("wiki root unavailable: %v", err)
		return summary
	}

	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "qmd", "--index", h.Config.MountedWiki.QMDIndex, "collection", "list")
	cmd.Dir = h.Config.MountedWiki.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		summary.Error = truncateDashboardMessage(strings.TrimSpace(string(out)))
		if summary.Error == "" {
			summary.Error = err.Error()
		}
		return summary
	}
	summary.OK = true
	summary.Message = firstNonEmpty(truncateDashboardMessage(strings.TrimSpace(string(out))), "qmd collection 可用")
	return summary
}

func truncateDashboardMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lines := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' })
	if len(lines) > 0 {
		value = strings.TrimSpace(lines[0])
	}
	runes := []rune(value)
	if len(runes) > 240 {
		return string(runes[:240]) + "..."
	}
	return value
}
