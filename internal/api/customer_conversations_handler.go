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
	ID                  string   `json:"id"`
	SessionID           string   `json:"session_id"`
	UserID              string   `json:"user_id,omitempty"`
	Title               string   `json:"title"`
	FirstQuestion       string   `json:"first_question"`
	LastQuestion        string   `json:"last_question"`
	LastAnswer          string   `json:"last_answer"`
	LastAnswerMode      string   `json:"last_answer_mode,omitempty"`
	ClientChannels      []string `json:"client_channels"`
	LastClientChannel   string   `json:"last_client_channel"`
	Entrypoints         []string `json:"entrypoints"`
	LastEntrypoint      string   `json:"last_entrypoint"`
	LastSimulation      bool     `json:"last_simulation"`
	LastSpecialist      string   `json:"last_specialist"`
	LastTotalDurationMS int64    `json:"last_total_duration_ms"`
	AverageDurationMS   int64    `json:"average_duration_ms"`
	LastSourceCount     int64    `json:"last_source_count"`
	ErrorCount          int      `json:"error_count"`
	ReviewRequiredCount int      `json:"review_required_count"`
	MessageCount        int      `json:"message_count"`
	TurnCount           int      `json:"turn_count"`
	StartedAt           string   `json:"started_at"`
	UpdatedAt           string   `json:"updated_at"`
}

type customerConversationMessage struct {
	ID             string         `json:"id"`
	Role           string         `json:"role"`
	Content        string         `json:"content"`
	CreatedAt      string         `json:"created_at"`
	TraceID        string         `json:"trace_id,omitempty"`
	MessageID      string         `json:"message_id,omitempty"`
	AnswerMode     string         `json:"answer_mode,omitempty"`
	ClientChannel  string         `json:"client_channel"`
	Entrypoint     string         `json:"entrypoint"`
	Simulation     bool           `json:"simulation"`
	Specialist     string         `json:"specialist,omitempty"`
	DurationMS     int64          `json:"duration_ms"`
	SourceCount    int64          `json:"source_count"`
	ReviewRequired bool           `json:"review_required"`
	ErrorStage     string         `json:"error_stage,omitempty"`
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

type customerConversationDeleteResponse struct {
	OK             bool   `json:"ok"`
	ID             string `json:"id"`
	DeletedRecords int    `json:"deleted_records"`
	TouchedFiles   int    `json:"touched_files"`
	DeletedFiles   int    `json:"deleted_files"`
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
	ClientChannel       string
	Entrypoint          string
	Simulation          bool
	Specialist          string
	TotalDurationMS     int64
	SourceCount         int64
	ReviewRequired      bool
	ReviewQueued        bool
	ErrorStage          string
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

func (h *Handlers) AdminDeleteCustomerConversation(c *gin.Context) {
	id := strings.TrimSpace(c.Param("session_id"))
	if id == "" {
		badRequest(c, fmt.Errorf("session_id is required"))
		return
	}
	result, err := h.deleteCustomerConversationLogRecords(id)
	if err != nil {
		internalError(c, err)
		return
	}
	if result.DeletedRecords == 0 {
		notFound(c, "customer conversation not found")
		return
	}
	c.JSON(http.StatusOK, result)
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
	Search        string
	Page          int
	PageSize      int
	From          time.Time
	To            time.Time
	Entrypoint    string
	ClientChannel string
	Simulation    *bool
}

func customerConversationQueryFromRequest(c *gin.Context) customerConversationQuery {
	page := parsePositiveQueryInt(c.Query("page"), 1)
	pageSize := parsePositiveQueryInt(c.Query("page_size"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	query := customerConversationQuery{
		Search:        strings.ToLower(strings.TrimSpace(c.Query("q"))),
		Page:          page,
		PageSize:      pageSize,
		Entrypoint:    normalizeCustomerConversationEntrypoint(c.Query("entrypoint")),
		ClientChannel: normalizeCustomerConversationClientChannel(c.Query("client_channel")),
	}
	if simulation, ok := parseCustomerConversationBool(c.Query("simulation")); ok {
		query.Simulation = &simulation
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
		if query.Entrypoint != "" && record.Entrypoint != query.Entrypoint {
			continue
		}
		if query.ClientChannel != "" && record.ClientChannel != query.ClientChannel {
			continue
		}
		if query.Simulation != nil && record.Simulation != *query.Simulation {
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

type customerConversationDeletePlan struct {
	path     string
	kept     []string
	deleted  int
	hasMatch bool
}

func (h *Handlers) deleteCustomerConversationLogRecords(id string) (customerConversationDeleteResponse, error) {
	logDir := h.customerChatLogDir()
	matches, err := filepath.Glob(filepath.Join(logDir, "*.jsonl"))
	if err != nil {
		return customerConversationDeleteResponse{}, err
	}
	sort.Strings(matches)
	plans := []customerConversationDeletePlan{}
	totalDeleted := 0
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return customerConversationDeleteResponse{}, err
		}
		plan := customerConversationDeletePlan{path: path}
		for lineIndex, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				return customerConversationDeleteResponse{}, fmt.Errorf("decode customer chat log %s:%d: %w", path, lineIndex+1, err)
			}
			record := customerChatLogRecordFromMap(entry, fmt.Sprintf("%s:%d", filepath.Base(path), lineIndex+1))
			if record.SessionKey == id {
				plan.deleted++
				plan.hasMatch = true
				continue
			}
			plan.kept = append(plan.kept, line)
		}
		if plan.hasMatch {
			plans = append(plans, plan)
			totalDeleted += plan.deleted
		}
	}
	if totalDeleted == 0 {
		return customerConversationDeleteResponse{OK: true, ID: id}, nil
	}
	deletedFiles := 0
	for _, plan := range plans {
		if len(plan.kept) == 0 {
			if err := os.Remove(plan.path); err != nil {
				return customerConversationDeleteResponse{}, err
			}
			deletedFiles++
			continue
		}
		if err := replaceCustomerConversationJSONL(plan.path, plan.kept); err != nil {
			return customerConversationDeleteResponse{}, err
		}
	}
	return customerConversationDeleteResponse{
		OK:             true,
		ID:             id,
		DeletedRecords: totalDeleted,
		TouchedFiles:   len(plans),
		DeletedFiles:   deletedFiles,
	}, nil
}

func replaceCustomerConversationJSONL(path string, lines []string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
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
	runtimeInfo := mapValue(entry["runtime"])
	requestInfo := mapValue(entry["request"])
	finalInfo := mapValue(entry["final"])
	reviewDecisionInfo := mapValue(entry["review_decision"])
	specialistInfo := mapValue(entry["specialist"])
	specialistOutput := mapValue(specialistInfo["output"])
	errorInfo := mapValue(entry["error"])
	sessionID := stringMapValue(entry, "session_id")
	traceID := stringMapValue(entry, "trace_id")
	questionMessageID := ""
	answerMessageID := ""
	loggedAt := stringMapValue(timeInfo, "logged_at")
	receivedAt := stringMapValue(timeInfo, "received_at")
	answeredAt := stringMapValue(timeInfo, "answered_at")
	questionCreatedAt := receivedAt
	answerMode := firstNonEmpty(stringMapValue(finalInfo, "answer_mode"), stringMapValue(specialistOutput, "answer_mode"))
	entrypoint := firstNonEmpty(normalizeCustomerConversationEntrypoint(stringMapValue(runtimeInfo, "entrypoint")), "external")
	clientChannel := firstNonEmpty(normalizeCustomerConversationClientChannel(stringMapValue(runtimeInfo, "client_channel")), "web")
	errorStage := stringMapValue(errorInfo, "stage")
	if errorStage == "" && entry["error"] != nil {
		errorStage = "unknown"
	}
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
		ClientChannel:       clientChannel,
		Entrypoint:          entrypoint,
		Simulation:          boolMapValue(runtimeInfo, "simulation"),
		Specialist:          firstNonEmpty(stringMapValue(specialistInfo, "name"), stringMapValue(specialistOutput, "specialist")),
		TotalDurationMS:     int64MapValue(timeInfo, "total_duration_ms"),
		SourceCount:         int64MapValue(finalInfo, "source_count"),
		ReviewRequired:      boolMapValue(finalInfo, "review_required"),
		ReviewQueued:        boolMapValue(reviewDecisionInfo, "create_review"),
		ErrorStage:          errorStage,
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
		record.ClientChannel,
		record.Entrypoint,
		record.Specialist,
		record.ErrorStage,
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
	entrypoints := customerConversationEntrypoints(records)
	clientChannels := customerConversationClientChannels(records)
	errorCount := 0
	reviewRequiredCount := 0
	totalDurationMS := int64(0)
	durationCount := int64(0)
	for _, record := range records {
		if record.ErrorStage != "" {
			errorCount++
		}
		// Count turns that actually entered the human review queue
		// (review_decision.create_review), not the model's raw review_required
		// flag, so the badge matches the 审查 queue. Simulation runs and other
		// non-queued cases (boundary_refusal, high-confidence answers) are excluded.
		if record.ReviewQueued {
			reviewRequiredCount++
		}
		if record.TotalDurationMS > 0 {
			totalDurationMS += record.TotalDurationMS
			durationCount++
		}
	}
	averageDurationMS := int64(0)
	if durationCount > 0 {
		averageDurationMS = totalDurationMS / durationCount
	}
	return customerConversationSummary{
		ID:                  first.SessionKey,
		SessionID:           firstNonEmpty(first.SessionID, "未指定"),
		UserID:              firstNonEmpty(last.UserID, first.UserID),
		Title:               truncateCustomerConversationText(firstNonEmpty(first.Question, last.Question, first.SessionKey), 36),
		FirstQuestion:       truncateCustomerConversationText(first.Question, 120),
		LastQuestion:        truncateCustomerConversationText(last.Question, 120),
		LastAnswer:          truncateCustomerConversationText(last.Answer, 160),
		LastAnswerMode:      last.AnswerMode,
		ClientChannels:      clientChannels,
		LastClientChannel:   firstNonEmpty(last.ClientChannel, "web"),
		Entrypoints:         entrypoints,
		LastEntrypoint:      last.Entrypoint,
		LastSimulation:      last.Simulation,
		LastSpecialist:      last.Specialist,
		LastTotalDurationMS: last.TotalDurationMS,
		AverageDurationMS:   averageDurationMS,
		LastSourceCount:     last.SourceCount,
		ErrorCount:          errorCount,
		ReviewRequiredCount: reviewRequiredCount,
		MessageCount:        len(records) * 2,
		TurnCount:           len(records),
		StartedAt:           firstNonEmpty(first.QuestionCreatedAt, first.ReceivedAt, first.LoggedAt),
		UpdatedAt:           firstNonEmpty(last.AnsweredAt, last.ReceivedAt, last.LoggedAt),
	}
}

func customerConversationClientChannels(records []customerChatLogRecord) []string {
	seen := map[string]bool{}
	channels := []string{}
	for _, record := range records {
		channel := firstNonEmpty(normalizeCustomerConversationClientChannel(record.ClientChannel), "web")
		if seen[channel] {
			continue
		}
		seen[channel] = true
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	return channels
}

func customerConversationEntrypoints(records []customerChatLogRecord) []string {
	seen := map[string]bool{}
	entrypoints := []string{}
	for _, record := range records {
		entrypoint := strings.TrimSpace(record.Entrypoint)
		if entrypoint == "" || seen[entrypoint] {
			continue
		}
		seen[entrypoint] = true
		entrypoints = append(entrypoints, entrypoint)
	}
	sort.Strings(entrypoints)
	return entrypoints
}

func customerConversationMessages(records []customerChatLogRecord) []customerConversationMessage {
	messages := make([]customerConversationMessage, 0, len(records)*2)
	for index, record := range records {
		questionID := customerConversationMessageID(record.SessionKey, index, "question")
		answerID := customerConversationMessageID(record.SessionKey, index, "answer")
		messages = append(messages, customerConversationMessage{
			ID:             questionID,
			Role:           "user",
			Content:        record.Question,
			CreatedAt:      firstNonEmpty(record.QuestionCreatedAt, record.ReceivedAt, record.LoggedAt),
			TraceID:        record.TraceID,
			MessageID:      record.QuestionMessageID,
			ClientChannel:  record.ClientChannel,
			Entrypoint:     record.Entrypoint,
			Simulation:     record.Simulation,
			Specialist:     record.Specialist,
			DurationMS:     record.TotalDurationMS,
			SourceCount:    record.SourceCount,
			ReviewRequired: record.ReviewRequired,
			ErrorStage:     record.ErrorStage,
		})
		messages = append(messages, customerConversationMessage{
			ID:             answerID,
			Role:           "assistant",
			Content:        record.Answer,
			CreatedAt:      firstNonEmpty(record.AnsweredAt, record.LoggedAt),
			TraceID:        record.TraceID,
			MessageID:      record.AnswerMessageID,
			AnswerMode:     record.AnswerMode,
			ClientChannel:  record.ClientChannel,
			Entrypoint:     record.Entrypoint,
			Simulation:     record.Simulation,
			Specialist:     record.Specialist,
			DurationMS:     record.TotalDurationMS,
			SourceCount:    record.SourceCount,
			ReviewRequired: record.ReviewRequired,
			ErrorStage:     record.ErrorStage,
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

func boolMapValue(record map[string]any, key string) bool {
	if record == nil {
		return false
	}
	value, ok := record[key]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, ok := parseCustomerConversationBool(typed)
		return ok && parsed
	default:
		return false
	}
}

func int64MapValue(record map[string]any, key string) int64 {
	if record == nil {
		return 0
	}
	value, ok := record[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func mapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func normalizeCustomerConversationEntrypoint(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "external":
		return "external"
	case "internal":
		return "internal"
	default:
		return ""
	}
}

func normalizeCustomerConversationClientChannel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "web":
		return "web"
	case "mobile_app":
		return "mobile_app"
	default:
		return ""
	}
}

func parseCustomerConversationBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y":
		return true, true
	case "false", "0", "no", "n":
		return false, true
	default:
		return false, false
	}
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
