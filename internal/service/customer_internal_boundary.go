package service

import "strings"

type customerInternalBoundaryGuardResult struct {
	Triggered bool
	Answer    string
	Reason    string
}

func customerInternalBoundaryGuard(parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerInternalBoundaryGuardResult {
	if routerOutput == nil {
		return customerInternalBoundaryGuardResult{Reason: "no_router_output"}
	}
	if !customerRouterIsInternalBoundary(routerOutput) {
		return customerInternalBoundaryGuardResult{Reason: "not_internal_boundary"}
	}
	return customerInternalBoundaryGuardResult{Reason: "internal_boundary_observed"}
}

func customerRouterIsInternalBoundary(routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil {
		return false
	}
	if customerRouterListContains(routerOutput.RiskFlags, "internal") {
		return true
	}
	return routerOutput.RiskBoundary == "internal_security_boundary" && customerRouterLooksInternalIntent(routerOutput.Intent)
}

func (result customerInternalBoundaryGuardResult) Audit() map[string]any {
	return map[string]any{
		"triggered": result.Triggered,
		"reason":    result.Reason,
		"action":    "audit_only",
	}
}

func customerAnswerLooksLikeInternalBoundary(answer string) bool {
	text := strings.ToLower(strings.TrimSpace(answer))
	if text == "" {
		return false
	}
	hasInternal := containsAny(text, "内部", "prompt", "提示词", "后台规则", "路由规则", "内部配置", "系统提示")
	hasRefusal := containsAny(text, "不能", "无法", "不便", "不提供", "不能对外提供", "不可公开")
	return hasInternal && hasRefusal
}
