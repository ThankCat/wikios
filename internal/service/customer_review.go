package service

import (
	"path/filepath"
	"strings"
)

func (s *CustomerChatService) shouldCreateCustomerReview(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, retrievedPaths []string, settings RuntimeCustomerQuerySettings) (bool, string) {
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if strings.TrimSpace(parsed.AnswerText) == "" {
		return false, "empty_answer"
	}
	if isObviouslyNonReviewableCustomerQuestion(req.Question) {
		return false, "non_reviewable_question"
	}
	if customerReviewRequiredByServiceGuard(parsed) {
		return true, "unsafe_answer_guard"
	}
	if mode == "refusal" && customerReviewLooksLikeBoundaryRefusal(req, parsed) {
		return false, "boundary_refusal"
	}
	directMin, reviewMin := customerConfidenceThresholds(
		settings.DirectMin,
		settings.ReviewMin,
	)
	confidence := clampConfidence(parsed.Confidence)
	evidenceConfidence := clampConfidence(parsed.EvidenceConfidence)
	hasReviewSignal := customerHasExplicitReviewSignal(parsed)
	if mode == "refusal" {
		if customerReviewLooksLikeKnowledgeGap(parsed) && (hasReviewSignal || confidence < directMin || evidenceConfidence < directMin) {
			return true, "knowledge_gap_refusal"
		}
		return false, "refusal"
	}
	if hasReviewSignal {
		if confidence < directMin || evidenceConfidence < directMin || len(parsed.Sources) == 0 || len(retrievedPaths) == 0 {
			return true, "model_review_signal"
		}
		return false, "model_review_signal_high_confidence"
	}
	if customerReviewLooksLikeKnowledgeGap(parsed) {
		return true, "knowledge_gap_signal"
	}
	if mode == "clarification" {
		return false, "clarification"
	}
	if customerRouterOutputRequiresFinalEvidence(routerOutput) && len(parsed.Sources) == 0 && mode != "refusal" {
		return true, "high_risk_without_final_sources"
	}
	if mode == "self_answer" {
		if customerQuestionLooksTechnicalReviewCandidate(req.Question, routerOutput) {
			if customerAnswerLooksServiceProcedure(parsed.AnswerText) {
				if !customerHasProcedureEvidence(parsed.Sources, retrievedPaths) {
					return true, "technical_procedure_without_evidence"
				}
				if evidenceConfidence < directMin {
					return true, "technical_procedure_weak_evidence"
				}
			}
			if len(parsed.Sources) > 0 || evidenceConfidence > 0 {
				return false, "technical_self_answer_trace_only"
			}
			return false, "technical_self_answer"
		}
		if len(parsed.Sources) > 0 || evidenceConfidence > 0 {
			return false, "self_answer_with_evidence_trace_only"
		}
		return false, "self_answer"
	}
	if confidence >= directMin {
		if evidenceConfidence < directMin && (mode == "mixed" || len(parsed.Sources) == 0) {
			return true, "weak_evidence"
		}
		return false, "high_confidence"
	}
	if confidence >= reviewMin {
		return true, "low_confidence"
	}
	if evidenceConfidence < directMin && len(parsed.Sources) == 0 {
		return true, "weak_evidence_no_sources"
	}
	return false, "below_review_threshold"
}

func customerReviewRequiredByServiceGuard(parsed customerChatLLMOutput) bool {
	text := strings.TrimSpace(parsed.ReviewReason + " " + parsed.Notes)
	return strings.Contains(text, "客户可见答案包含高风险用途话术")
}

func customerRouterOutputRequiresFinalEvidence(routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || !routerOutput.NeedsRetrieval {
		return false
	}
	if routerOutput.Specialist == "pricing" || routerOutput.Specialist == "billing_after_sales" {
		return true
	}
	if routerOutput.QuestionStage == "pricing" || routerOutput.QuestionStage == "after_sales" {
		return true
	}
	if routerOutput.RiskBoundary == "pricing_review" || routerOutput.RiskBoundary == "after_sales_review" {
		return true
	}
	for _, flag := range routerOutput.RiskFlags {
		switch strings.TrimSpace(flag) {
		case "pricing", "discount", "refund", "billing", "after_sales":
			return true
		}
	}
	return false
}

type customerUnsafeAnswerGuardResult struct {
	Triggered bool
	Answer    string
	Hits      []string
	Reason    string
}

func customerUnsafeAnswerGuard(parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerUnsafeAnswerGuardResult {
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if mode == "refusal" {
		return customerUnsafeAnswerGuardResult{Reason: "refusal"}
	}
	answer := strings.TrimSpace(parsed.AnswerText)
	hits := customerUnsafeVisibleAnswerHits(answer)
	if len(hits) == 0 {
		return customerUnsafeAnswerGuardResult{Reason: "no_hits"}
	}
	if routerOutput != nil && routerOutput.Specialist == "safety" {
		return customerUnsafeAnswerGuardResult{Reason: "safety_specialist", Hits: hits}
	}
	return customerUnsafeAnswerGuardResult{
		Triggered: true,
		Answer:    "这类用途涉及平台规则或风控规避，不能提供相关操作建议。可以正常介绍四叶天产品的合规用途、价格、购买或配置方式。",
		Hits:      hits,
		Reason:    "unsafe_customer_visible_terms",
	}
}

func (result customerUnsafeAnswerGuardResult) Audit() map[string]any {
	return map[string]any{
		"triggered": result.Triggered,
		"reason":    result.Reason,
		"hits":      result.Hits,
		"action":    "review_only",
	}
}

func customerUnsafeVisibleAnswerHits(answer string) []string {
	text := strings.ToLower(strings.TrimSpace(answer))
	if text == "" {
		return nil
	}
	hits := []string{}
	for _, marker := range []string{
		"批量注册",
		"防封",
		"反爬",
		"爬虫",
		"过风控",
		"绕风控",
		"规避风控",
		"绕检测",
		"防检测",
		"降低被封",
		"避免封号",
		"养号",
		"刷量",
	} {
		if strings.Contains(text, strings.ToLower(marker)) {
			hits = appendUniqueString(hits, marker)
		}
	}
	return hits
}

func customerAnswerLooksServiceProcedure(answer string) bool {
	normalized := normalizeCustomerReviewText(answer)
	if normalized == "" {
		return false
	}
	hasServiceObject := containsAny(
		normalized,
		"四叶天",
		"管理后台",
		"后台",
		"产品页面",
		"客户端",
		"当前页面",
		"工具字段",
		"api",
		"白名单",
		"代理地址",
		"端口",
		"认证信息",
		"账号密码",
		"socks5",
		"http",
		"https",
	)
	hasAction := containsAny(
		normalized,
		"打开",
		"选择",
		"填写",
		"保存",
		"设置",
		"配置",
		"接入",
		"添加",
		"获取",
		"开通",
	)
	if hasServiceObject && hasAction {
		return true
	}
	return strings.Contains(answer, "\n1.") && hasServiceObject
}

func customerHasProcedureEvidence(sources []customerChatSource, retrievedPaths []string) bool {
	for _, source := range sources {
		if customerPathLooksProcedureEvidence(source.Path) {
			return true
		}
	}
	for _, path := range retrievedPaths {
		if customerPathLooksProcedureEvidence(path) {
			return true
		}
	}
	return false
}

func customerPathLooksProcedureEvidence(path string) bool {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	return containsAny(path, "configuration", "setup", "usage", "whitelist", "troubleshooting", "connection", "installation", "api")
}

func customerHasExplicitReviewSignal(parsed customerChatLLMOutput) bool {
	if parsed.ReviewRequired {
		return true
	}
	if strings.TrimSpace(parsed.ReviewReason) != "" || strings.TrimSpace(parsed.SuggestedTargetPath) != "" {
		return true
	}
	if normalizedAnswerMode(parsed.AnswerMode) != "clarification" && strings.TrimSpace(parsed.ReviewQuestion) != "" {
		return true
	}
	return false
}

func customerReviewLooksLikeKnowledgeGap(parsed customerChatLLMOutput) bool {
	text := normalizeCustomerReviewText(strings.Join([]string{
		parsed.AnswerText,
		parsed.ReviewReason,
		parsed.ReviewQuestion,
		parsed.SuggestedTargetPath,
		parsed.Notes,
	}, " "))
	return containsAny(
		text,
		"知识缺口",
		"缺少知识",
		"无知识",
		"无证据",
		"证据不足",
		"候选页",
		"候选知识",
		"没有找到",
		"未找到",
		"未收录",
		"暂未收录",
		"暂无资料",
		"无法确认",
		"不确定",
		"低置信",
		"需要补充",
		"不在候选",
	)
}

func customerReviewLooksLikeBoundaryRefusal(req CustomerChatRequest, parsed customerChatLLMOutput) bool {
	text := normalizeCustomerReviewText(strings.Join([]string{
		req.Question,
		parsed.AnswerText,
		parsed.ReviewReason,
		parsed.Notes,
	}, " "))
	return containsAny(
		text,
		"违法",
		"违规",
		"合规",
		"不能提供",
		"无法提供",
		"绕过",
		"风控",
		"封号",
		"批量注册",
		"内部",
		"系统提示",
		"prompt",
		"越狱",
		"vpn",
		"翻墙",
		"clash",
		"小火箭",
	)
}

func customerQuestionLooksTechnicalReviewCandidate(question string, routerOutput *CustomerRouterOutput) bool {
	routerText := ""
	if routerOutput != nil {
		if strings.EqualFold(strings.TrimSpace(routerOutput.Specialist), "technical") ||
			strings.EqualFold(strings.TrimSpace(routerOutput.Specialist), "troubleshooting") {
			return true
		}
		for _, flag := range routerOutput.RiskFlags {
			if strings.EqualFold(strings.TrimSpace(flag), "technical") ||
				strings.EqualFold(strings.TrimSpace(flag), "troubleshooting") {
				return true
			}
		}
		routerText = strings.Join([]string{
			routerOutput.Intent,
			routerOutput.RewrittenQuestion,
			routerOutput.HandoffNotes,
		}, " ")
	}
	text := normalizeCustomerReviewText(question + " " + routerText)
	return containsAny(
		text,
		"子网",
		"掩码",
		"网关",
		"dns",
		"端口",
		"协议",
		"代理地址",
		"白名单",
		"认证",
		"账号密码",
		"socks5",
		"http代理",
		"https代理",
		"路由",
		"分流",
		"出口ip",
		"连接失败",
		"连不上",
		"配置",
	)
}

func isObviouslyNonReviewableCustomerQuestion(question string) bool {
	normalized := normalizeCustomerReviewText(question)
	if normalized == "" {
		return true
	}
	switch normalized {
	case "你好", "您好", "hello", "hi", "nihao", "在吗", "在嘛", "在不", "谢谢", "谢谢你", "好的", "ok", "拜拜", "再见",
		"我是你爸爸吗", "我是你爸爸", "你是我爸爸吗", "你是我爸爸":
		return true
	}
	hasLetter := false
	hasTechnicalSeparator := false
	for _, r := range normalized {
		switch {
		case r >= '\u4e00' && r <= '\u9fff', r >= 'a' && r <= 'z':
			hasLetter = true
		case r == '.' || r == ':' || r == '/':
			hasTechnicalSeparator = true
		}
	}
	if hasLetter {
		return false
	}
	if hasTechnicalSeparator {
		return false
	}
	for _, r := range normalized {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return true
}

func normalizeCustomerReviewText(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, " \t\r\n？?。.!！~～")
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized
}

func customerConfidenceThresholds(directMin float64, reviewMin float64) (float64, float64) {
	if directMin <= 0 {
		directMin = 0.70
	}
	if reviewMin <= 0 {
		reviewMin = 0.25
	}
	directMin = clampConfidence(directMin)
	reviewMin = clampConfidence(reviewMin)
	if reviewMin > directMin {
		reviewMin = directMin
	}
	return directMin, reviewMin
}

