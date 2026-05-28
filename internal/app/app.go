package app

import (
	"fmt"
	"os"

	"github.com/gin-gonic/gin"

	"wikios/internal/api"
	"wikios/internal/config"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/service"
	"wikios/internal/store"
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

	dataStore, err := store.Open(cfg.Storage.SQLitePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite store: %w", err)
	}

	resolver := wikiadapter.NewPathResolver(cfg.MountedWiki.Root)
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: resolver,
	})
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	llmClient := service.NewDynamicLLMClient(dataStore, cfg.LLM)
	retriever := retrieval.NewQMDRetriever(rt)
	customerIntents := service.NewCustomerIntentManager(cfg.CustomerIntents)
	contextCounter := service.NewContextCounter(cfg.Context)
	deps := service.Deps{
		Config:          cfg,
		Runtime:         rt,
		LLM:             llmClient,
		Retriever:       retriever,
		Store:           dataStore,
		CustomerIntents: customerIntents,
		PromptDir:       "internal/llm/prompts",
		WorkspaceDir:    cfg.Workspace.BaseDir,
	}
	handlers := api.NewHandlers(
		service.NewCustomerChatService(deps),
		service.NewReviewQueueService(deps),
		service.NewDirectAdminService(deps),
		service.NewUploadService(deps),
		service.NewSyncService(deps),
		dataStore,
		cfg,
		customerIntents,
		contextCounter,
	)

	a := &App{cfg: cfg}
	a.engine = NewRouter(cfg, handlers, dataStore)
	return a, nil
}

func (a *App) Run() error {
	return a.engine.Run(fmt.Sprintf(":%d", a.cfg.Server.Port))
}
