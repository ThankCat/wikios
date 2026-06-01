package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	customerChatAuditSchemaVersion = "customer_chat_audit.v1"
	customerChatAuditRecordType    = "customer_chat_trace"
	customerChatModeRouted         = "routed"
)

type customerChatAuditRecord struct {
	SchemaVersion string                      `json:"schema_version"`
	RecordType    string                      `json:"record_type"`
	TraceID       string                      `json:"trace_id"`
	SessionID     string                      `json:"session_id"`
	Time          customerChatAuditTime       `json:"time"`
	Runtime       customerChatAuditRuntime    `json:"runtime"`
	Request       customerChatAuditRequest    `json:"request"`
	Router        customerChatAuditRouter     `json:"router"`
	Retrieval     map[string]any              `json:"retrieval"`
	Specialist    customerChatAuditSpecialist `json:"specialist"`
	Final         customerChatAuditFinal      `json:"final"`
	Error         *customerChatAuditError     `json:"error"`
	Review        customerChatAuditReview     `json:"review"`
}

type customerChatAuditTime struct {
	LoggedAt        string `json:"logged_at"`
	ReceivedAt      string `json:"received_at"`
	AnsweredAt      string `json:"answered_at"`
	TotalDurationMS int64  `json:"total_duration_ms"`
}

type customerChatAuditRuntime struct {
	Environment           string `json:"environment"`
	Entrypoint            string `json:"entrypoint"`
	Simulation            bool   `json:"simulation"`
	GitCommit             string `json:"git_commit"`
	CustomerChatMode      string `json:"customer_chat_mode"`
	RouterModelID         string `json:"router_model_id"`
	SpecialistModelID     string `json:"specialist_model_id"`
	RouterContractVersion string `json:"router_contract_version"`
}

type customerChatAuditRequest struct {
	Message             string           `json:"message"`
	HistoryTurns        int              `json:"history_turns"`
	HistoryMessageCount int              `json:"history_message_count"`
	HistorySummary      string           `json:"history_summary"`
	ConversationContext []map[string]any `json:"conversation_context"`
}

type customerChatAuditModel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ThinkingEnabled bool   `json:"thinking_enabled"`
}

type customerChatAuditRouter struct {
	Model      customerChatAuditModel `json:"model"`
	DurationMS int64                  `json:"duration_ms"`
	Thinking   map[string]any         `json:"thinking"`
	RawOutput  string                 `json:"raw_output"`
	Output     map[string]any         `json:"output"`
}

type customerChatAuditSpecialist struct {
	Name       string                 `json:"name"`
	Model      customerChatAuditModel `json:"model"`
	DurationMS int64                  `json:"duration_ms"`
	Thinking   map[string]any         `json:"thinking"`
	Input      map[string]any         `json:"input"`
	RawOutput  string                 `json:"raw_output"`
	Output     map[string]any         `json:"output"`
}

type customerChatAuditFinal struct {
	Answer         string `json:"answer"`
	AnswerMode     string `json:"answer_mode"`
	SourceCount    int64  `json:"source_count"`
	ReviewRequired bool   `json:"review_required"`
}

type customerChatAuditError struct {
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	RawOutput string `json:"raw_output,omitempty"`
}

type customerChatAuditReview struct {
	Status        string `json:"status"`
	IsGoodAnswer  *bool  `json:"is_good_answer"`
	ErrorType     string `json:"error_type"`
	CorrectAnswer string `json:"correct_answer"`
	Note          string `json:"note"`
	ReviewedBy    string `json:"reviewed_by"`
	ReviewedAt    string `json:"reviewed_at"`
}

func customerChatAuditRecordToMap(record customerChatAuditRecord) map[string]any {
	raw, err := json.Marshal(record)
	if err != nil {
		return map[string]any{"error": "marshal customer chat audit record failed"}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"error": "decode customer chat audit record failed"}
	}
	return out
}

func customerChatAuditReviewPlaceholder() customerChatAuditReview {
	return customerChatAuditReview{
		Status:        "unreviewed",
		IsGoodAnswer:  nil,
		ErrorType:     "",
		CorrectAnswer: "",
		Note:          "",
		ReviewedBy:    "",
		ReviewedAt:    "",
	}
}

func newCustomerChatAuditError(stage string, err error, rawOutput string) *customerChatAuditError {
	stage = normalizeCustomerChatAuditErrorStage(stage)
	message := strings.TrimSpace(fmt.Sprint(err))
	if message == "" || err == nil {
		message = "customer chat failed"
	}
	return &customerChatAuditError{
		Stage:     stage,
		Message:   truncateForPrompt(message, 2000),
		RawOutput: strings.TrimSpace(rawOutput),
	}
}

func normalizeCustomerChatAuditErrorStage(stage string) string {
	switch strings.TrimSpace(stage) {
	case "router_call", "router_parse", "retrieval", "specialist_call", "specialist_parse", "final_response":
		return strings.TrimSpace(stage)
	default:
		return "final_response"
	}
}

func customerRouterAuditErrorStage(raw string, err error) string {
	if strings.TrimSpace(raw) != "" {
		return "router_parse"
	}
	if err != nil {
		text := strings.ToLower(err.Error())
		for _, marker := range []string{"decode", "contract", "validate", "missing customer router", "invalid customer router"} {
			if strings.Contains(text, marker) {
				return "router_parse"
			}
		}
	}
	return "router_call"
}

func customerAuditBoolPtrValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func customerAuditGitCommit() string {
	for _, key := range []string{"WIKIOS_GIT_COMMIT", "GIT_COMMIT", "VERCEL_GIT_COMMIT_SHA"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			if len(value) > 12 {
				return value[:12]
			}
			return value
		}
	}
	return ""
}

func (s *CustomerChatService) customerAuditModelName(ctx context.Context, id string) string {
	if s == nil {
		return ""
	}
	id = strings.TrimSpace(id)
	if id == "active" || id == currentLLMModel {
		id = ""
	}
	if s.deps.Store != nil {
		var modelName string
		var err error
		if id == "" {
			model, getErr := s.deps.Store.GetActiveLLMModel(ctx)
			err = getErr
			if model != nil {
				modelName = firstNonEmpty(model.ModelName, model.DisplayName)
			}
		} else {
			model, getErr := s.deps.Store.GetLLMModel(ctx, id)
			err = getErr
			if model != nil {
				modelName = firstNonEmpty(model.ModelName, model.DisplayName)
			}
		}
		if err == nil && strings.TrimSpace(modelName) != "" {
			return strings.TrimSpace(modelName)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ""
		}
	}
	if id == "" {
		if namer, ok := s.deps.LLM.(activeLLMModelNamer); ok {
			if name, err := namer.ActiveModelName(ctx); err == nil {
				return strings.TrimSpace(name)
			}
		}
	}
	return ""
}

func (s *CustomerChatService) customerAuditModelID(ctx context.Context, id string) string {
	id = strings.TrimSpace(id)
	if id != "" && id != "active" && id != currentLLMModel {
		return id
	}
	if s == nil || s.deps.Store == nil {
		return ""
	}
	model, err := s.deps.Store.GetActiveLLMModel(ctx)
	if err == nil && model != nil {
		return strings.TrimSpace(model.ID)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	return ""
}
