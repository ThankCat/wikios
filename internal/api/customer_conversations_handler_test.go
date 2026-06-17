package api

import "testing"

func customerChatLogEntry(reviewRequired, createReview, simulation bool) map[string]any {
	return map[string]any{
		"session_id": "s-review-count",
		"trace_id":   "trace-review-count",
		"runtime": map[string]any{
			"entrypoint":     "external",
			"client_channel": "mobile_app",
			"simulation":     simulation,
		},
		"request": map[string]any{"message": "我的IP不好用了"},
		"final": map[string]any{
			"answer":          "静态 IP 为固定地址……",
			"answer_mode":     "self_answer",
			"review_required": reviewRequired,
		},
		"review_decision": map[string]any{
			"create_review": createReview,
		},
	}
}

func TestSummarizeCustomerConversationCountsQueuedReviewsOnly(t *testing.T) {
	// Simulation turn: model flagged review_required but nothing was queued.
	simulationRecord := customerChatLogRecordFromMap(customerChatLogEntry(true, false, true), "0")
	if !simulationRecord.ReviewRequired {
		t.Fatalf("expected simulation record to keep model review_required flag")
	}
	if simulationRecord.ReviewQueued {
		t.Fatalf("expected simulation record to not be queued")
	}

	summary := summarizeCustomerConversation([]customerChatLogRecord{simulationRecord})
	if summary.ReviewRequiredCount != 0 {
		t.Fatalf("expected review_required_count 0 for non-queued review, got %d", summary.ReviewRequiredCount)
	}

	// Real queued review should be counted.
	queuedRecord := customerChatLogRecordFromMap(customerChatLogEntry(true, true, false), "1")
	if !queuedRecord.ReviewQueued {
		t.Fatalf("expected queued record to be marked as queued")
	}
	queuedSummary := summarizeCustomerConversation([]customerChatLogRecord{queuedRecord})
	if queuedSummary.ReviewRequiredCount != 1 {
		t.Fatalf("expected review_required_count 1 for queued review, got %d", queuedSummary.ReviewRequiredCount)
	}
}

func TestCustomerConversationIncludesClientChannel(t *testing.T) {
	record := customerChatLogRecordFromMap(customerChatLogEntry(false, false, false), "0")
	if record.ClientChannel != "mobile_app" {
		t.Fatalf("expected mobile_app record channel, got %q", record.ClientChannel)
	}
	summary := summarizeCustomerConversation([]customerChatLogRecord{record})
	if summary.LastClientChannel != "mobile_app" || len(summary.ClientChannels) != 1 || summary.ClientChannels[0] != "mobile_app" {
		t.Fatalf("expected mobile_app summary channel, got %+v", summary)
	}
	messages := customerConversationMessages([]customerChatLogRecord{record})
	if len(messages) != 2 || messages[0].ClientChannel != "mobile_app" || messages[1].ClientChannel != "mobile_app" {
		t.Fatalf("expected message client_channel fields, got %+v", messages)
	}
}
