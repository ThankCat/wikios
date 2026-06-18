package app

import (
	"fmt"
	"os"
	"time"

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
	if cfg.Retrieval.Mode == "wiki" {
		retriever = retrieval.NewWikiRetriever(rt)
	} else if cfg.Retrieval.QMDHTTP.Enabled {
		rerank := true
		if cfg.Retrieval.QMDHTTP.Rerank != nil {
			rerank = *cfg.Retrieval.QMDHTTP.Rerank
		}
		httpClient := retrieval.NewQMDHTTPClientWithRerank(
			cfg.Retrieval.QMDHTTP.URL,
			time.Duration(cfg.Retrieval.QMDHTTP.TimeoutSec)*time.Second,
			rerank,
			cfg.Retrieval.QMDHTTP.RerankCandidates,
		)
		retriever = retrieval.NewQMDRetrieverWithHTTP(rt, httpClient)
	}
	safetyTerms := service.NewCustomerSafetyTermManager(cfg.SafetyTerms)
	contextCounter := service.NewContextCounter(cfg.Context)
	deps := service.Deps{
		Config:       cfg,
		Runtime:      rt,
		LLM:          llmClient,
		Retriever:    retriever,
		Store:        dataStore,
		SafetyTerms:  safetyTerms,
		PromptDir:    "internal/llm/prompts",
		WorkspaceDir: cfg.Workspace.BaseDir,
	}
	handlers := api.NewHandlers(
		service.NewCustomerChatService(deps),
		service.NewReviewQueueService(deps),
		service.NewDirectAdminService(deps),
		service.NewUploadService(deps),
		service.NewSyncService(deps),
		dataStore,
		cfg,
		contextCounter,
		safetyTerms,
	)

	a := &App{cfg: cfg}
	a.engine = NewRouter(cfg, handlers, dataStore)
	return a, nil
}

func (a *App) Run() error {
	return a.engine.Run(fmt.Sprintf(":%d", a.cfg.Server.Port))
}
