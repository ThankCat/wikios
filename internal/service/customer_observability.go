package service

import (
	"fmt"
	"strings"

	"wikios/internal/retrieval"
)

func customerRouterDecisionAudit(output *CustomerRouterOutput) map[string]any {
	if output == nil {
		return nil
	}
	return map[string]any{
		"question_stage":              output.QuestionStage,
		"user_goal":                   output.UserGoal,
		"specialist":                  output.Specialist,
		"intent":                      output.Intent,
		"answer_strategy":             output.AnswerStrategy,
		"risk_boundary":               output.RiskBoundary,
		"has_product":                 output.HasProduct,
		"needs_product_clarification": output.NeedsProductClarification,
		"clarification_target":        output.ClarificationTarget,
		"routing_confidence":          output.RoutingConfidence,
		"needs_retrieval":             output.NeedsRetrieval,
		"retrieval_query_count":       len(output.RetrievalQueries),
		"ambiguity":                   output.Ambiguity,
		"missing_info":                append([]string(nil), output.MissingInfo...),
		"risk_flags":                  append([]string(nil), output.RiskFlags...),
	}
}

func customerClarificationAudit(output *CustomerRouterOutput) map[string]any {
	if output == nil {
		return nil
	}
	return map[string]any{
		"needed":           output.NeedsProductClarification || output.AnswerStrategy == "ask_clarification",
		"target":           firstNonEmpty(output.ClarificationTarget, "none"),
		"missing_info":     append([]string(nil), output.MissingInfo...),
		"ambiguous_fields": append([]string(nil), output.Ambiguity.AmbiguousFields...),
		"reason":           output.Ambiguity.Reason,
	}
}

func customerHardStopAudit(output *CustomerRouterOutput) map[string]any {
	if output == nil {
		return nil
	}
	stopType := "none"
	switch {
	case output.Specialist == "safety" || output.AnswerStrategy == "refuse_with_boundary":
		stopType = "safety_boundary"
	case output.NeedsProductClarification || output.AnswerStrategy == "ask_clarification":
		stopType = "clarification"
	case !output.NeedsRetrieval:
		stopType = "retrieval_skipped"
	}
	return map[string]any{
		"type":                 stopType,
		"question_stage":       output.QuestionStage,
		"risk_boundary":        output.RiskBoundary,
		"clarification_target": output.ClarificationTarget,
		"handoff_notes":        output.HandoffNotes,
	}
}

func customerRetrievalDiagnostics(output *CustomerRouterOutput, evidence customerSpecialistEvidenceResult, durationMs int64, topK int, maxEvidenceChars int) map[string]any {
	diagnostics := map[string]any{
		"requested":               false,
		"executed":                false,
		"duration_ms":             durationMs,
		"target_specialist":       evidence.Profile.Name,
		"candidate_top_k":         topK,
		"max_evidence_chars":      maxEvidenceChars,
		"attempted_query_count":   len(evidence.CacheTrace.AttemptedRetrievalQueries),
		"executed_query_count":    len(evidence.CacheTrace.ExecutedRetrievalQueries),
		"skipped_query_count":     evidence.CacheTrace.SkippedRetrievalQueryCount,
		"candidate_count":         len(evidence.Candidates),
		"source_count":            len(evidence.Sources),
		"scope_filtered_count":    len(evidence.CacheTrace.ScopeFilteredPages),
		"wikilink_expanded_count": len(evidence.CacheTrace.WikilinkExpandedPages),
		"queries":                 append([]string(nil), evidence.Queries...),
		"router_queries":          []string{},
		"candidate_paths":         customerRetrievedPagePaths(evidence.Candidates, 12),
		"source_paths":            customerSourcePaths(evidence.Sources),
	}
	if output != nil {
		diagnostics["requested"] = output.NeedsRetrieval
		diagnostics["router_queries"] = append([]string(nil), output.RetrievalQueries...)
	}
	diagnostics["executed"] = len(evidence.CacheTrace.ExecutedRetrievalQueries) > 0
	if evidence.Error != "" {
		diagnostics["error"] = evidence.Error
	}
	return diagnostics
}

func customerQualitySignals(req CustomerChatRequest, output *CustomerRouterOutput, evidence customerSpecialistEvidenceResult, parsed *customerChatLLMOutput, topK int) []map[string]any {
	if output == nil {
		return nil
	}
	signals := []map[string]any{}
	add := func(code string, severity string, message string, fields map[string]any) {
		item := map[string]any{
			"code":     code,
			"severity": severity,
			"message":  message,
		}
		for key, value := range fields {
			item[key] = value
		}
		signals = append(signals, item)
	}
	if output.QuestionStage == "product_selection" && output.NeedsProductClarification && strings.TrimSpace(output.ClarificationTarget) == "primary_product" {
		add("possible_wrong_stage_clarification", "warning", "选型问题被转成产品类型澄清，容易形成反复追问。", nil)
	}
	if (output.QuestionStage == "goal_consulting" || output.QuestionStage == "product_selection") && !output.NeedsRetrieval {
		add("stage_expected_retrieval", "warning", "目标咨询或选型问题通常需要检索资料支撑。", nil)
	}
	if output.Specialist == "safety" && customerRouterMentionsDomesticPlatform(strings.ToLower(req.Question)) && customerRouterMentionsIPLocationChange(strings.ToLower(req.Question)) && !customerRouterMentionsExplicitSafetyAbuse(strings.ToLower(req.Question)) {
		add("possible_over_safety", "critical", "普通平台 IP/归属地问题被送入安全边界。", map[string]any{"question": truncateForPrompt(req.Question, 160)})
	}
	if output.NeedsRetrieval && len(evidence.Sources) == 0 && evidence.Error == "" {
		add("retrieval_no_sources", "warning", "路由要求检索，但没有读取到证据源。", nil)
	}
	if output.NeedsRetrieval && topK > 0 && len(evidence.Candidates) > len(evidence.Sources) && len(evidence.Sources) >= topK {
		add("candidate_may_be_dropped_by_topk", "info", "候选结果多于已读取证据，可能存在 topK 截断。", map[string]any{
			"candidate_count": len(evidence.Candidates),
			"source_count":    len(evidence.Sources),
		})
	}
	if parsed != nil && normalizedAnswerMode(parsed.AnswerMode) == "clarification" && output.AnswerStrategy != "ask_clarification" {
		add("specialist_unexpected_clarification", "warning", "路由未要求澄清，但专家最终仍以澄清方式回答。", nil)
	}
	if parsed != nil && normalizedAnswerMode(parsed.AnswerMode) == "refusal" && output.AnswerStrategy != "refuse_with_boundary" {
		add("specialist_unexpected_refusal", "warning", "路由未要求拒答，但专家最终拒答。", nil)
	}
	return signals
}

func customerRetrievedPagePaths(pages any, limit int) []string {
	raw := customerRetrievedPageSummariesAny(pages)
	if limit <= 0 || limit > len(raw) {
		limit = len(raw)
	}
	out := make([]string, 0, limit)
	for _, item := range raw[:limit] {
		if path := auditStringMapValue(item, "path"); path != "" {
			out = append(out, path)
		}
	}
	return out
}

func customerRetrievedPageSummariesAny(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []SourceRef:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, map[string]any{"path": item.Path})
		}
		return out
	case []retrieval.RetrievedPage:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, map[string]any{"path": item.Path, "score": item.Score})
		}
		return out
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return nil
		}
		return nil
	}
}

func customerSourcePaths(sources []SourceRef) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		if path := strings.TrimSpace(source.Path); path != "" {
			out = append(out, path)
		}
	}
	return out
}
