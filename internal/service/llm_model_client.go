package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/store"
)

const noActiveLLMModelMessage = "当前未启用 LLM 模型，请先在管理员端模型模块配置并启用模型"
const unavailableLLMModelMessage = "当前启用模型服务不可用，请在管理员端模型模块检查账号余额、API Key 或服务状态"
const publicLLMUnavailableMessage = "当前在线回复暂时不可用，请稍后再试。"
const llmModelIDPrefix = "model-id:"

type DynamicLLMClient struct {
	store      *store.Store
	defaults   config.LLMConfig
	clientMu   sync.Mutex
	clientByID map[string]llm.Client
}

type allLLMModelsUnavailableError struct {
	failures []llmModelFailure
}

type llmModelFailure struct {
	ModelID     string
	DisplayName string
	ModelName   string
	Err         error
}

func (e allLLMModelsUnavailableError) Error() string {
	if len(e.failures) == 0 {
		return noActiveLLMModelMessage
	}
	parts := make([]string, 0, len(e.failures))
	for _, failure := range e.failures {
		name := strings.TrimSpace(failure.DisplayName)
		if name == "" {
			name = strings.TrimSpace(failure.ModelName)
		}
		if name == "" {
			name = strings.TrimSpace(failure.ModelID)
		}
		if name == "" {
			name = "未命名模型"
		}
		if failure.Err != nil {
			parts = append(parts, fmt.Sprintf("%s: %s", name, failure.Err.Error()))
		} else {
			parts = append(parts, fmt.Sprintf("%s: unavailable", name))
		}
	}
	return unavailableLLMModelMessage + "；尝试失败：" + strings.Join(parts, "；")
}

func NewDynamicLLMClient(dataStore *store.Store, defaults config.LLMConfig) *DynamicLLMClient {
	return &DynamicLLMClient{store: dataStore, defaults: defaults, clientByID: map[string]llm.Client{}}
}

func (c *DynamicLLMClient) Chat(ctx context.Context, model string, messages []llm.Message) (string, error) {
	return c.chat(ctx, model, messages)
}

func (c *DynamicLLMClient) chat(ctx context.Context, modelToken string, messages []llm.Message) (string, error) {
	if requestedID := requestedLLMModelID(modelToken); requestedID != "" {
		if model, err := c.modelByID(ctx, requestedID); err == nil {
			text, err := c.clientForModel(model).Chat(ctx, model.ModelName, messages)
			if err == nil {
				return text, nil
			}
			if doneErr := llmModelContextError(ctx, err); doneErr != nil {
				return "", doneErr
			}
			log.Printf("specific llm model failed id=%s model=%s err=%v; falling back to active model", model.ID, model.ModelName, err)
		} else {
			if doneErr := llmModelContextError(ctx, err); doneErr != nil {
				return "", doneErr
			}
			log.Printf("specific llm model unavailable id=%s err=%v; falling back to active model", requestedID, err)
		}
	}
	models, err := c.availableModels(ctx)
	if err != nil {
		return "", err
	}
	var failures []llmModelFailure
	for _, model := range models {
		text, err := c.clientForModel(model).Chat(ctx, model.ModelName, messages)
		if err == nil {
			c.activateSuccessfulFallback(ctx, model)
			return text, nil
		}
		failures = append(failures, modelFailure(model, err))
		if !shouldSwitchLLMModel(err) {
			return "", err
		}
	}
	return "", allLLMModelsUnavailableError{failures: failures}
}

func (c *DynamicLLMClient) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	return c.StreamChatEvents(ctx, model, messages, func(delta llm.StreamDelta) {
		if delta.Content != "" && onDelta != nil {
			onDelta(delta.Content)
		}
	})
}

func (c *DynamicLLMClient) StreamChatEvents(ctx context.Context, model string, messages []llm.Message, onDelta func(llm.StreamDelta)) (string, error) {
	return c.streamChatEvents(ctx, model, messages, onDelta)
}

func (c *DynamicLLMClient) streamChatEvents(ctx context.Context, modelToken string, messages []llm.Message, onDelta func(llm.StreamDelta)) (string, error) {
	if requestedID := requestedLLMModelID(modelToken); requestedID != "" {
		if model, err := c.modelByID(ctx, requestedID); err == nil {
			emitted := false
			wrappedDelta := func(delta llm.StreamDelta) {
				if delta.Content != "" || delta.ReasoningContent != "" {
					emitted = true
				}
				if onDelta != nil {
					onDelta(delta)
				}
			}
			client := c.clientForModel(model)
			streamClient, ok := client.(llm.EventStreamClient)
			var text string
			if ok {
				text, err = streamClient.StreamChatEvents(ctx, model.ModelName, messages, wrappedDelta)
			} else {
				text, err = client.StreamChat(ctx, model.ModelName, messages, func(delta string) {
					wrappedDelta(llm.StreamDelta{Content: delta})
				})
			}
			if err == nil {
				return text, nil
			}
			if emitted {
				return "", err
			}
			if doneErr := llmModelContextError(ctx, err); doneErr != nil {
				return "", doneErr
			}
			log.Printf("specific streaming llm model failed id=%s model=%s err=%v; falling back to active model", model.ID, model.ModelName, err)
		} else {
			if doneErr := llmModelContextError(ctx, err); doneErr != nil {
				return "", doneErr
			}
			log.Printf("specific streaming llm model unavailable id=%s err=%v; falling back to active model", requestedID, err)
		}
	}
	models, err := c.availableModels(ctx)
	if err != nil {
		return "", err
	}
	var failures []llmModelFailure
	for _, model := range models {
		emitted := false
		wrappedDelta := func(delta llm.StreamDelta) {
			if delta.Content != "" || delta.ReasoningContent != "" {
				emitted = true
			}
			if onDelta != nil {
				onDelta(delta)
			}
		}
		client := c.clientForModel(model)
		streamClient, ok := client.(llm.EventStreamClient)
		var text string
		if ok {
			text, err = streamClient.StreamChatEvents(ctx, model.ModelName, messages, wrappedDelta)
		} else {
			text, err = client.StreamChat(ctx, model.ModelName, messages, func(delta string) {
				wrappedDelta(llm.StreamDelta{Content: delta})
			})
		}
		if err == nil {
			c.activateSuccessfulFallback(ctx, model)
			return text, nil
		}
		failures = append(failures, modelFailure(model, err))
		if emitted || !shouldSwitchLLMModel(err) {
			return "", err
		}
	}
	return "", allLLMModelsUnavailableError{failures: failures}
}

func (c *DynamicLLMClient) ActiveModelName(ctx context.Context) (string, error) {
	model, err := c.activeModel(ctx)
	if err != nil {
		return "", err
	}
	return model.ModelName, nil
}

func (c *DynamicLLMClient) RequestTimeout(ctx context.Context, admin bool) time.Duration {
	model, err := c.activeModel(ctx)
	if err != nil {
		return fallbackLLMTimeout(c.defaults, admin)
	}
	if admin {
		if model.AdminTimeoutSec > 0 {
			return time.Duration(model.AdminTimeoutSec) * time.Second
		}
		return fallbackLLMTimeout(c.defaults, true)
	}
	if model.TimeoutSec > 0 {
		return time.Duration(model.TimeoutSec) * time.Second
	}
	return fallbackLLMTimeout(c.defaults, false)
}

func (c *DynamicLLMClient) activeModel(ctx context.Context) (*store.LLMModel, error) {
	if c == nil || c.store == nil {
		return nil, errors.New(noActiveLLMModelMessage)
	}
	model, err := c.store.GetActiveLLMModel(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New(noActiveLLMModelMessage)
		}
		if doneErr := llmModelContextError(ctx, err); doneErr != nil {
			return nil, doneErr
		}
		return nil, fmt.Errorf("读取当前启用 LLM 模型失败: %w", err)
	}
	if model.BaseURL == "" || model.ModelName == "" || model.APIKey == "" {
		return nil, errors.New("当前启用 LLM 模型配置不完整，请在管理员端「模型」模块补齐端点、模型名和 API Key")
	}
	return model, nil
}

func (c *DynamicLLMClient) modelByID(ctx context.Context, id string) (*store.LLMModel, error) {
	id = strings.TrimSpace(id)
	if c == nil || c.store == nil {
		return nil, errors.New(noActiveLLMModelMessage)
	}
	if id == "" {
		return nil, errors.New("model id is empty")
	}
	model, err := c.store.GetLLMModel(ctx, id)
	if err != nil {
		if doneErr := llmModelContextError(ctx, err); doneErr != nil {
			return nil, doneErr
		}
		return nil, err
	}
	if model.BaseURL == "" || model.ModelName == "" || model.APIKey == "" {
		return nil, errors.New("指定 LLM 模型配置不完整，请在管理员端「模型」模块补齐端点、模型名和 API Key")
	}
	return model, nil
}

func (c *DynamicLLMClient) availableModels(ctx context.Context) ([]*store.LLMModel, error) {
	if c == nil || c.store == nil {
		return nil, errors.New(noActiveLLMModelMessage)
	}
	active, err := c.activeModel(ctx)
	if err != nil {
		return nil, err
	}
	models, err := c.store.ListLLMModels(ctx)
	if err != nil {
		if doneErr := llmModelContextError(ctx, err); doneErr != nil {
			return nil, doneErr
		}
		return nil, fmt.Errorf("读取 LLM 模型列表失败: %w", err)
	}
	out := make([]*store.LLMModel, 0, len(models)+1)
	out = append(out, active)
	for i := range models {
		model := models[i]
		if model.ID == active.ID {
			continue
		}
		if model.BaseURL == "" || model.ModelName == "" || model.APIKey == "" {
			continue
		}
		modelCopy := model
		out = append(out, &modelCopy)
	}
	return out, nil
}

func (c *DynamicLLMClient) activateSuccessfulFallback(ctx context.Context, model *store.LLMModel) {
	if c == nil || c.store == nil || model == nil || model.IsActive {
		return
	}
	if err := c.store.ActivateLLMModel(ctx, model.ID); err != nil {
		log.Printf("activate fallback llm model failed id=%s err=%v", model.ID, err)
	}
}

func modelFailure(model *store.LLMModel, err error) llmModelFailure {
	failure := llmModelFailure{Err: err}
	if model != nil {
		failure.ModelID = model.ID
		failure.DisplayName = model.DisplayName
		failure.ModelName = model.ModelName
	}
	return failure
}

func (c *DynamicLLMClient) clientForModel(model *store.LLMModel) llm.Client {
	if c == nil || model == nil {
		return llm.NewClient(llm.ClientConfig{})
	}
	key := fmt.Sprintf("%s|%s|%d|%d", model.ID, model.UpdatedAt.Format(time.RFC3339Nano), model.TimeoutSec, c.defaults.TimeoutSec)
	c.clientMu.Lock()
	defer c.clientMu.Unlock()
	if client := c.clientByID[key]; client != nil {
		return client
	}
	client := llm.NewClient(llm.ClientConfig{
		APIKey:     model.APIKey,
		BaseURL:    model.BaseURL,
		TimeoutSec: firstPositive(model.TimeoutSec, c.defaults.TimeoutSec, 90),
	})
	c.clientByID[key] = client
	return client
}

func llmModelContextError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}

func isLLMModelConfigurationError(err error) bool {
	if err == nil {
		return false
	}
	var allUnavailable allLLMModelsUnavailableError
	if errors.As(err, &allUnavailable) {
		if len(allUnavailable.failures) == 0 {
			return true
		}
		for _, failure := range allUnavailable.failures {
			if failure.Err != nil && !isLLMModelConfigurationError(failure.Err) {
				return false
			}
		}
		return true
	}
	message := err.Error()
	return message == noActiveLLMModelMessage ||
		message == "当前启用 LLM 模型配置不完整，请在管理员端「模型」模块补齐端点、模型名和 API Key" ||
		errors.Is(err, sql.ErrNoRows)
}

func isPublicHiddenLLMError(err error) bool {
	return isLLMModelConfigurationError(err) || isLLMProviderUnavailableError(err)
}

func isLLMProviderUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	var allUnavailable allLLMModelsUnavailableError
	if errors.As(err, &allUnavailable) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "access denied") ||
		strings.Contains(message, "overdue") ||
		strings.Contains(message, "payment") ||
		strings.Contains(message, "billing") ||
		strings.Contains(message, "account is in good standing") ||
		strings.Contains(message, "insufficient balance") ||
		strings.Contains(message, "quota") ||
		strings.Contains(message, "rate limit") ||
		strings.Contains(message, "rate_limit") ||
		strings.Contains(message, "too many requests") ||
		strings.Contains(message, "temporarily") ||
		strings.Contains(message, "temporary") ||
		strings.Contains(message, "unavailable") ||
		strings.Contains(message, "overloaded") ||
		strings.Contains(message, "unauthorized") ||
		strings.Contains(message, "forbidden") ||
		strings.Contains(message, "invalid api key") ||
		strings.Contains(message, "api key is not configured") ||
		strings.Contains(message, "timeout") ||
		strings.Contains(message, "deadline exceeded") ||
		strings.Contains(message, "connection refused") ||
		strings.Contains(message, "no such host") ||
		strings.Contains(message, "server misbehaving") ||
		strings.Contains(message, "temporary failure") ||
		strings.Contains(message, "llm api status 429") ||
		strings.Contains(message, "llm api status 500") ||
		strings.Contains(message, "llm api status 502") ||
		strings.Contains(message, "llm api status 503") ||
		strings.Contains(message, "llm api status 504") ||
		strings.Contains(message, "llm api status 401") ||
		strings.Contains(message, "llm api status 403")
}

func shouldSwitchLLMModel(err error) bool {
	return isLLMProviderUnavailableError(err)
}

func fallbackLLMTimeout(cfg config.LLMConfig, admin bool) time.Duration {
	seconds := cfg.TimeoutSec
	if admin {
		seconds = cfg.AdminTimeoutSec
	}
	if seconds <= 0 {
		if admin {
			seconds = 300
		} else {
			seconds = 90
		}
	}
	return time.Duration(seconds) * time.Second
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func llmModelIDToken(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return currentLLMModel
	}
	return llmModelIDPrefix + id
}

func requestedLLMModelID(modelToken string) string {
	modelToken = strings.TrimSpace(modelToken)
	if !strings.HasPrefix(modelToken, llmModelIDPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(modelToken, llmModelIDPrefix))
}
