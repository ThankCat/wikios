package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type customerConversationSummary struct {
	ID             string `json:"id"`
	SessionID      string `json:"session_id"`
	UserID         string `json:"user_id,omitempty"`
	Title          string `json:"title"`
	FirstQuestion  string `json:"first_question"`
	LastQuestion   string `json:"last_question"`
	LastAnswer     string `json:"last_answer"`
	LastAnswerMode string `json:"last_answer_mode,omitempty"`
	MessageCount   int    `json:"message_count"`
	TurnCount      int    `json:"turn_count"`
	StartedAt      string `json:"started_at"`
	UpdatedAt      string `json:"updated_at"`
}

type customerConversationMessage struct {
	ID             string         `json:"id"`
	Role           string         `json:"role"`
	Content        string         `json:"content"`
	CreatedAt      string         `json:"created_at"`
	TraceID        string         `json:"trace_id,omitempty"`
	MessageID      string         `json:"message_id,omitempty"`
	AnswerMode     string         `json:"answer_mode,omitempty"`
	ProcessSummary string         `json:"process_summary,omitempty"`
	Details        map[string]any `json:"details,omitempty"`
}

type customerConversationsResponse struct {
	Conversations []customerConversationSummary `json:"conversations"`
	Total         int                           `json:"total"`
	Page          int                           `json:"page"`
	PageSize      int                           `json:"page_size"`
	HasMore       bool                          `json:"has_more"`
	Log           adminDashboardLogSummary      `json:"log"`
}

type customerConversationDetailResponse struct {
	Conversation customerConversationSummary   `json:"conversation"`
	Messages     []customerConversationMessage `json:"messages"`
	Log          adminDashboardLogSummary      `json:"log"`
}

type customerChatLogRecord struct {
	ID                  string
	SessionKey          string
	SessionID           string
	UserID              string
	TraceID             string
	LoggedAt            string
	QuestionCreatedAt   string
	ReceivedAt          string
	AnsweredAt          string
	Question            string
	Answer              string
	AnswerMode          string
	ProcessSummary      string
	Details             map[string]any
	QuestionMessageID   string
	AnswerMessageID     string
	SearchText          string
	ConversationSortKey time.Time
}

func (h *Handlers) AdminCustomerConversations(c *gin.Context) {
	records, err := h.readCustomerChatLogRecords()
	if err != nil {
		internalError(c, err)
		return
	}
	query := customerConversationQueryFromRequest(c)
	groups := groupCustomerConversationRecords(filterCustomerChatLogRecords(records, query))
	summaries := make([]customerConversationSummary, 0, len(groups))
	for _, records := range groups {
		summaries = append(summaries, summarizeCustomerConversation(records))
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt > summaries[j].UpdatedAt
	})
	total := len(summaries)
	start := (query.Page - 1) * query.PageSize
	if start > total {
		start = total
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	c.JSON(http.StatusOK, customerConversationsResponse{
		Conversations: summaries[start:end],
		Total:         total,
		Page:          query.Page,
		PageSize:      query.PageSize,
		HasMore:       end < total,
		Log:           h.dashboardCustomerChatLog(),
	})
}

func (h *Handlers) AdminCustomerConversationDetail(c *gin.Context) {
	id := strings.TrimSpace(c.Param("session_id"))
	records, err := h.readCustomerChatLogRecords()
	if err != nil {
		internalError(c, err)
		return
	}
	groups := groupCustomerConversationRecords(records)
	records = groups[id]
	if len(records) == 0 {
		notFound(c, "customer conversation not found")
		return
	}
	sortCustomerConversationRecords(records)
	c.JSON(http.StatusOK, customerConversationDetailResponse{
		Conversation: summarizeCustomerConversation(records),
		Messages:     customerConversationMessages(records),
		Log:          h.dashboardCustomerChatLog(),
	})
}

func (h *Handlers) AdminCustomerChatTrace(c *gin.Context) {
	traceID := strings.TrimSpace(c.Param("trace_id"))
	if traceID == "" {
		badRequest(c, fmt.Errorf("trace_id is required"))
		return
	}
	entry, err := h.readCustomerChatTraceEntry(traceID)
	if err != nil {
		internalError(c, err)
		return
	}
	if entry == nil {
		notFound(c, "customer chat trace not found")
		return
	}
	c.JSON(http.StatusOK, entry)
}

type customerConversationQuery struct {
	Search   string
	Page     int
	PageSize int
	From     time.Time
	To       time.Time
}

func customerConversationQueryFromRequest(c *gin.Context) customerConversationQuery {
	page := parsePositiveQueryInt(c.Query("page"), 1)
	pageSize := parsePositiveQueryInt(c.Query("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	query := customerConversationQuery{
		Search:   strings.ToLower(strings.TrimSpace(c.Query("q"))),
		Page:     page,
		PageSize: pageSize,
	}
	query.From = parseCustomerConversationTime(firstNonEmpty(c.Query("from"), c.Query("start")))
	if to := parseCustomerConversationTime(firstNonEmpty(c.Query("to"), c.Query("end"))); !to.IsZero() {
		if len(strings.TrimSpace(firstNonEmpty(c.Query("to"), c.Query("end")))) == len("2006-01-02") {
			to = to.Add(24 * time.Hour)
		}
		query.To = to
	}
	return query
}

func parsePositiveQueryInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func filterCustomerChatLogRecords(records []customerChatLogRecord, query customerConversationQuery) []customerChatLogRecord {
	filtered := make([]customerChatLogRecord, 0, len(records))
	for _, record := range records {
		if query.Search != "" && !strings.Contains(record.SearchText, query.Search) {
			continue
		}
		if !query.From.IsZero() && record.ConversationSortKey.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && !record.ConversationSortKey.Before(query.To) {
			continue
		}
		filtered = append(filtered, record)
	}
	return filtered
}

func (h *Handlers) readCustomerChatLogRecords() ([]customerChatLogRecord, error) {
	logDir := h.customerChatLogDir()
	matches, err := filepath.Glob(filepath.Join(logDir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	records := []customerChatLogRecord{}
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for lineIndex, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				return nil, fmt.Errorf("decode customer chat log %s:%d: %w", path, lineIndex+1, err)
			}
			records = append(records, customerChatLogRecordFromMap(entry, fmt.Sprintf("%s:%d", filepath.Base(path), lineIndex+1)))
		}
	}
	return records, nil
}

func (h *Handlers) readCustomerChatTraceEntry(traceID string) (map[string]any, error) {
	logDir := h.customerChatLogDir()
	matches, err := filepath.Glob(filepath.Join(logDir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				continue
			}
			if stringMapValue(entry, "trace_id") == traceID {
				return entry, nil
			}
		}
	}
	return nil, nil
}

func (h *Handlers) customerChatLogDir() string {
	workspaceDir := ".workspace"
	if h.Config != nil && strings.TrimSpace(h.Config.Workspace.BaseDir) != "" {
		workspaceDir = strings.TrimSpace(h.Config.Workspace.BaseDir)
	}
	return filepath.Join(workspaceDir, "customer_chat_logs")
}

func customerChatLogRecordFromMap(entry map[string]any, fallbackID string) customerChatLogRecord {
	timeInfo := mapValue(entry["time"])
	requestInfo := mapValue(entry["request"])
	finalInfo := mapValue(entry["final"])
	specialistOutput := mapValue(mapValue(entry["specialist"])["output"])
	sessionID := stringMapValue(entry, "session_id")
	traceID := stringMapValue(entry, "trace_id")
	questionMessageID := ""
	answerMessageID := ""
	loggedAt := stringMapValue(timeInfo, "logged_at")
	receivedAt := stringMapValue(timeInfo, "received_at")
	answeredAt := stringMapValue(timeInfo, "answered_at")
	questionCreatedAt := receivedAt
	answerMode := firstNonEmpty(stringMapValue(finalInfo, "answer_mode"), stringMapValue(specialistOutput, "answer_mode"))
	processSummary := customerConversationProcessSummaryFromStandardTrace(entry)
	details := customerConversationSafeDetails(entry)
	sessionKey := strings.TrimSpace(sessionID)
	if sessionKey == "" {
		sessionKey = "anonymous:" + firstNonEmpty(questionMessageID, answerMessageID, traceID, loggedAt, fallbackID)
	}
	sortTime := firstParsedCustomerConversationTime(answeredAt, receivedAt, loggedAt, questionCreatedAt)
	record := customerChatLogRecord{
		ID:                  fallbackID,
		SessionKey:          sessionKey,
		SessionID:           sessionID,
		UserID:              "",
		TraceID:             traceID,
		LoggedAt:            loggedAt,
		QuestionCreatedAt:   questionCreatedAt,
		ReceivedAt:          receivedAt,
		AnsweredAt:          answeredAt,
		Question:            stringMapValue(requestInfo, "message"),
		Answer:              stringMapValue(finalInfo, "answer"),
		AnswerMode:          answerMode,
		ProcessSummary:      processSummary,
		Details:             details,
		QuestionMessageID:   questionMessageID,
		AnswerMessageID:     answerMessageID,
		ConversationSortKey: sortTime,
	}
	record.SearchText = strings.ToLower(strings.Join([]string{
		record.SessionKey,
		record.SessionID,
		record.UserID,
		record.TraceID,
		record.Question,
		record.Answer,
		record.AnswerMode,
		record.ProcessSummary,
	}, " "))
	return record
}

func groupCustomerConversationRecords(records []customerChatLogRecord) map[string][]customerChatLogRecord {
	groups := map[string][]customerChatLogRecord{}
	for _, record := range records {
		groups[record.SessionKey] = append(groups[record.SessionKey], record)
	}
	for _, records := range groups {
		sortCustomerConversationRecords(records)
	}
	return groups
}

func sortCustomerConversationRecords(records []customerChatLogRecord) {
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].ConversationSortKey.Before(records[j].ConversationSortKey)
	})
}

func summarizeCustomerConversation(records []customerChatLogRecord) customerConversationSummary {
	sortCustomerConversationRecords(records)
	first := records[0]
	last := records[len(records)-1]
	return customerConversationSummary{
		ID:             first.SessionKey,
		SessionID:      firstNonEmpty(first.SessionID, "未指定"),
		UserID:         firstNonEmpty(last.UserID, first.UserID),
		Title:          truncateCustomerConversationText(firstNonEmpty(first.Question, last.Question, first.SessionKey), 36),
		FirstQuestion:  truncateCustomerConversationText(first.Question, 120),
		LastQuestion:   truncateCustomerConversationText(last.Question, 120),
		LastAnswer:     truncateCustomerConversationText(last.Answer, 160),
		LastAnswerMode: last.AnswerMode,
		MessageCount:   len(records) * 2,
		TurnCount:      len(records),
		StartedAt:      firstNonEmpty(first.QuestionCreatedAt, first.ReceivedAt, first.LoggedAt),
		UpdatedAt:      firstNonEmpty(last.AnsweredAt, last.ReceivedAt, last.LoggedAt),
	}
}

func customerConversationMessages(records []customerChatLogRecord) []customerConversationMessage {
	messages := make([]customerConversationMessage, 0, len(records)*2)
	for index, record := range records {
		questionID := customerConversationMessageID(record.SessionKey, index, "question")
		answerID := customerConversationMessageID(record.SessionKey, index, "answer")
		messages = append(messages, customerConversationMessage{
			ID:        questionID,
			Role:      "user",
			Content:   record.Question,
			CreatedAt: firstNonEmpty(record.QuestionCreatedAt, record.ReceivedAt, record.LoggedAt),
			TraceID:   record.TraceID,
			MessageID: record.QuestionMessageID,
		})
		messages = append(messages, customerConversationMessage{
			ID:             answerID,
			Role:           "assistant",
			Content:        record.Answer,
			CreatedAt:      firstNonEmpty(record.AnsweredAt, record.LoggedAt),
			TraceID:        record.TraceID,
			MessageID:      record.AnswerMessageID,
			AnswerMode:     record.AnswerMode,
			ProcessSummary: record.ProcessSummary,
			Details:        record.Details,
		})
	}
	return messages
}

func customerConversationSafeDetails(entry map[string]any) map[string]any {
	details := mapValue(entry["details"])
	jsonData := mapValue(entry["json_data"])
	if len(details) == 0 {
		details = mapValue(jsonData["details"])
	}
	if len(details) == 0 {
		details = mapValue(mapValue(jsonData["response"])["details"])
	}
	result := map[string]any{}
	for _, key := range []string{
		"process_summary",
		"steps",
		"execution",
		"answer_mode",
		"specialist",
		"source_count",
		"retrieved_count",
	} {
		if value, ok := details[key]; ok && value != nil {
			result[key] = value
		}
	}
	if thinking := stringMapValue(entry, "thinking"); thinking != "" {
		result["reasoning"] = thinking
		result["reasoning_chars"] = len([]rune(thinking))
	}
	if summary := firstNonEmpty(customerConversationProcessSummaryFromStandardTrace(entry), stringMapValue(details, "process_summary")); summary != "" {
		result["process_summary"] = summary
	}
	if mode := firstNonEmpty(stringMapValue(mapValue(entry["final"]), "answer_mode"), stringMapValue(details, "answer_mode")); mode != "" {
		result["answer_mode"] = mode
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func customerConversationProcessSummaryFromStandardTrace(entry map[string]any) string {
	routerOutput := mapValue(mapValue(entry["router"])["output"])
	finalInfo := mapValue(entry["final"])
	specialist := mapValue(entry["specialist"])
	parts := []string{}
	if specialistName := stringMapValue(specialist, "name"); specialistName != "" {
		parts = append(parts, "Specialist: "+specialistName)
	}
	if intent := stringMapValue(routerOutput, "intent"); intent != "" {
		parts = append(parts, "Intent: "+intent)
	}
	if mode := stringMapValue(finalInfo, "answer_mode"); mode != "" {
		parts = append(parts, "Mode: "+mode)
	}
	return strings.Join(parts, " | ")
}

func customerConversationMessageID(sessionKey string, index int, role string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		sessionKey = "anonymous"
	}
	return fmt.Sprintf("%s:%04d:%s", sessionKey, index+1, role)
}

func stringMapValue(record map[string]any, key string) string {
	if record == nil {
		return ""
	}
	value, ok := record[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func mapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func parseCustomerConversationTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func firstParsedCustomerConversationTime(values ...string) time.Time {
	for _, value := range values {
		if parsed := parseCustomerConversationTime(value); !parsed.IsZero() {
			return parsed
		}
	}
	return time.Time{}
}

func truncateCustomerConversationText(value string, limit int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
