package app

import (
	"fmt"
	"os"

	"github.com/gin-gonic/gin"

	"wikios/internal/api"
	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/service"
	"wikios/internal/task"
	"wikios/internal/tools"
	"wikios/internal/wikiadapter"
)

type App struct {
	cfg    *config.Config
	engine *gin.Engine
}

func New(cfg *config.Config) (*App, error) {
	if err := os.MkdirAll(cfg.Workspace.BaseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	store, err := task.OpenStore(cfg.TaskStore.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("open task store: %w", err)
	}

	resolver := wikiadapter.NewPathResolver(cfg.MountedWiki.Root)
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: resolver,
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	taskManager := task.NewManager(store)
	llmClient := llm.NewClient(cfg.LLM)
	retriever := retrieval.NewQMDRetriever(rt)
	deps := service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          llmClient,
		Retriever:    retriever,
		TaskStore:    store,
		PromptDir:    "internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	}
	handlers := api.NewHandlers(
		service.NewPublicQueryService(deps),
		service.NewAdminQueryService(deps),
		service.NewIngestService(deps),
		service.NewLintService(deps),
		service.NewReflectService(deps),
		service.NewRepairService(deps),
		service.NewSyncService(deps),
		taskManager,
	)

	a := &App{cfg: cfg}
	a.engine = NewRouter(cfg, handlers)
	return a, nil
}

func (a *App) Run() error {
	return a.engine.Run(fmt.Sprintf(":%d", a.cfg.Server.Port))
}

func (a *App) Engine() *gin.Engine {
	return a.engine
}
