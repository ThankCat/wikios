package service

import "strings"

type customerHumanContactGuardResult struct {
	Triggered bool
	Answer    string
	Reason    string
	Allowed   bool
	Removed   []string
}

func customerHumanContactGuard(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerHumanContactGuardResult {
	answer := strings.TrimSpace(parsed.AnswerText)
	if answer == "" {
		return customerHumanContactGuardResult{Reason: "empty_answer"}
	}
	if customerHumanContactAllowed(req, routerOutput) {
		return customerHumanContactGuardResult{Reason: "allowed_refund_or_explicit_contact", Allowed: true}
	}
	if !customerAnswerHasHumanContactGuidance(answer) {
		return customerHumanContactGuardResult{Reason: "no_human_contact_guidance"}
	}
	sanitized, removed := sanitizeCustomerHumanContactGuidance(answer)
	if strings.TrimSpace(sanitized) == strings.TrimSpace(answer) {
		return customerHumanContactGuardResult{Reason: "no_rewrite_available"}
	}
	return customerHumanContactGuardResult{
		Triggered: true,
		Answer:    sanitized,
		Reason:    "removed_unprompted_human_contact_guidance",
		Removed:   removed,
	}
}

func customerHumanContactAllowed(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	decisionText := customerScenarioGuardText(req, routerOutput)
	if customerScenarioIsRefund(routerOutput, decisionText) {
		return true
	}
	current := normalizeCustomerReviewText(req.Question)
	if customerRouterLooksWeComContactQuestion(current) || customerRouterLooksHumanContactQuestion(current) ||
		containsAny(current, "客服电话", "电话多少", "联系方式", "怎么联系", "联系你们", "找你们") {
		return true
	}
	if routerOutput == nil {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if containsAny(intent, "contact", "human", "wecom", "wechat", "customer_service") &&
		containsAny(current, "客服", "电话", "联系方式", "微信", "企微", "联系", "找谁", "找人", "人工") {
		return true
	}
	return false
}

func customerAnswerHasHumanContactGuidance(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text,
		"联系人工",
		"人工客服",
		"联系客服",
		"联系在线客服",
		"企业微信",
		"企微",
		"微信客服",
		"客服电话",
		"400-1080-106",
		"联系微信",
		"添加客服",
		"扫码添加客服",
		"人工核查",
		"人工处理",
		"人工确认",
		"人工核实",
		"人工排查",
		"人工协助",
		"专人协助",
		"由人工",
		"找人工",
		"找客服",
	)
}

func sanitizeCustomerHumanContactGuidance(answer string) (string, []string) {
	parts := splitCustomerAnswerSentences(answer)
	if len(parts) == 0 {
		parts = []string{strings.TrimSpace(answer)}
	}
	kept := make([]string, 0, len(parts))
	removed := []string{}
	for _, part := range parts {
		replaced, changed := rewriteCustomerHumanContactSentence(part)
		if strings.TrimSpace(replaced) == "" {
			removed = append(removed, truncateForPrompt(part, 120))
			continue
		}
		if changed {
			removed = append(removed, truncateForPrompt(part, 120))
		}
		kept = append(kept, replaced)
	}
	sanitized := strings.TrimSpace(strings.Join(kept, ""))
	if sanitized == "" {
		sanitized = "这个问题需要按当前页面和订单状态确认。您可以先刷新页面、重新登录，并在个人中心或对应产品后台查看当前状态。"
	}
	return sanitized, removed
}

func rewriteCustomerHumanContactSentence(sentence string) (string, bool) {
	original := strings.TrimSpace(sentence)
	if original == "" {
		return "", false
	}
	text := normalizeCustomerReviewText(original)
	if containsAny(text,
		"联系人工",
		"人工客服",
		"联系客服",
		"联系在线客服",
		"企业微信",
		"企微",
		"微信客服",
		"客服电话",
		"400-1080-106",
		"联系微信",
		"添加客服",
		"扫码添加客服",
		"人工协助",
		"专人协助",
		"找人工",
		"找客服",
	) {
		return "", true
	}
	replacer := strings.NewReplacer(
		"需要准备订单信息由人工核实", "需要按支付记录和订单状态核实",
		"由人工核实", "按订单状态核实",
		"人工核实", "按订单状态核实",
		"人工核查", "按页面状态核查",
		"人工处理", "按页面状态处理",
		"人工确认", "按当前规则确认",
		"人工排查", "按当前配置继续排查",
		"由人工确认", "按订单状态确认",
		"需要人工按当前规则确认", "需要按当前规则确认",
		"需要人工按订单状态确认", "需要按订单状态确认",
		"需要人工确认", "需要按当前资源和订单状态确认",
		"人工按订单状态确认", "按订单状态确认",
		"人工按当前规则确认", "按当前规则确认",
	)
	rewritten := strings.TrimSpace(replacer.Replace(original))
	return rewritten, rewritten != original
}

func (result customerHumanContactGuardResult) Audit() map[string]any {
	return map[string]any{
		"triggered":     result.Triggered,
		"reason":        result.Reason,
		"allowed":       result.Allowed,
		"removed_count": len(result.Removed),
		"removed":       result.Removed,
		"action":        "audit_only",
	}
}
