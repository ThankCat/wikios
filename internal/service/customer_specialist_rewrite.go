package service

import "fmt"

type customerSpecialistRewriteHooks struct {
	Parse func(raw string) (customerChatLLMOutput, string, error)
	Call  func() (string, error)
}

func applyCustomerAnswerVeto(parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, facts CustomerQuoteFacts, hooks customerSpecialistRewriteHooks) (customerChatLLMOutput, map[string]any, error) {
	kept, changed, reason := VetoCustomerVisibleAnswerWithParsed(parsed.AnswerText, parsed, routerOutput, facts)
	if !changed {
		return parsed, nil, nil
	}
	audit := map[string]any{
		"reason":          firstNonEmpty(reason, customerSanitizeInternalContext),
		"original_chars":  len([]rune(parsed.AnswerText)),
		"sanitized_chars": len([]rune(kept)),
	}
	if kept != "" {
		parsed.AnswerText = kept
		return parsed, audit, nil
	}
	if hooks.Call == nil || hooks.Parse == nil {
		return parsed, audit, fmt.Errorf("specialist answer emptied by veto (%s)", reason)
	}
	raw, err := hooks.Call()
	if err != nil {
		return parsed, audit, err
	}
	retry, _, err := hooks.Parse(raw)
	if err != nil {
		return parsed, audit, err
	}
	kept, _, reason2 := VetoCustomerVisibleAnswerWithParsed(retry.AnswerText, retry, routerOutput, facts)
	audit["rewritten"] = true
	audit["rewrite_reason"] = firstNonEmpty(reason2, reason)
	if kept == "" {
		return retry, audit, fmt.Errorf("specialist answer emptied by veto after rewrite (%s)", firstNonEmpty(reason2, reason))
	}
	retry.AnswerText = kept
	audit["sanitized_chars"] = len([]rune(kept))
	return retry, audit, nil
}

func customerSpecialistVetoRewriteHint() string {
	return "上一轮草稿因内部话术或错价被丢弃。请重写 answer，数字必须与 quote_facts 一致，不要提资料库或提交申请。"
}
