package service

import "strings"

const (
	customerSpecialistBasePromptFile     = "customer_specialist_base.md"
	customerSpecialistCheckPromptFile    = "customer_specialist_check.md"
	customerSpecialistBoundaryPromptFile = "customer_specialist_boundary.md"
	customerSpecialistPromptSeparator    = "\n\n---\n\n"
	customerSpecialistJSONOnlySuffix     = "\n\n完成上文「输出前自检（L4）」后，只返回一个 JSON 对象，不要用代码块包裹最终 JSON，不要解释。`answer` 必须是面向客户的非空字符串，禁止 `\"answer\":\"\"`；`answer` 字段内部可以使用 Markdown；要问客户的话写在 `answer`，不要只写在 `review_question`。"
)

func (s *CustomerChatService) loadCustomerSpecialistSystemPrompt(profile CustomerSpecialistProfile) (string, error) {
	basePrompt, err := s.loadPrompt(customerSpecialistBasePromptFile)
	if err != nil {
		return "", err
	}
	rolePrompt, err := s.loadPrompt(profile.PromptFile)
	if err != nil {
		return "", err
	}
	checkPrompt, err := s.loadPrompt(customerSpecialistCheckPromptFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(basePrompt) +
		customerSpecialistPromptSeparator + strings.TrimSpace(rolePrompt) +
		customerSpecialistPromptSeparator + strings.TrimSpace(checkPrompt) +
		customerSpecialistJSONOnlySuffix, nil
}

func (s *CustomerChatService) loadCustomerSpecialistBoundary() (string, error) {
	return s.loadPrompt(customerSpecialistBoundaryPromptFile)
}
