package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	customerRouterSkillsCatalogFile = "customer_router_skills.md"
	customerSkillQuoteStaticIP      = "quote_static_ip"
	customerSkillQuoteResidential   = "quote_residential"
	customerSkillCompareShared      = "compare_shared_dedicated"
	customerSkillCompareQuantity    = "compare_quantity_tier"
)

type customerSkillDefinition struct {
	ID         string
	PromptFile string
	Specialist string
}

var customerSkillDefinitions = map[string]customerSkillDefinition{
	customerSkillQuoteStaticIP: {
		ID:         customerSkillQuoteStaticIP,
		PromptFile: "skills/quote_static_ip.md",
		Specialist: "pricing",
	},
	customerSkillQuoteResidential: {
		ID:         customerSkillQuoteResidential,
		PromptFile: "skills/quote_residential.md",
		Specialist: "pricing",
	},
	customerSkillCompareShared: {
		ID:         customerSkillCompareShared,
		PromptFile: "skills/compare_shared_dedicated.md",
		Specialist: "product",
	},
	customerSkillCompareQuantity: {
		ID:         customerSkillCompareQuantity,
		PromptFile: "skills/compare_quantity_tier.md",
		Specialist: "pricing",
	},
}

func customerRouterSkillIDs() []string {
	return []string{
		customerSkillQuoteStaticIP,
		customerSkillQuoteResidential,
		customerSkillCompareShared,
		customerSkillCompareQuantity,
	}
}

func normalizeCustomerRouterSkills(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, 2)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if _, ok := customerSkillDefinitions[id]; !ok {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= 2 {
			break
		}
	}
	return out
}

func applyCustomerRouterSkillFallbacks(req CustomerChatRequest, output CustomerRouterOutput) CustomerRouterOutput {
	userText := strings.ToLower(strings.TrimSpace(req.Question))
	output.Skills = normalizeCustomerRouterSkills(output.Skills)
	appendSkill := func(id string) {
		if len(output.Skills) >= 2 {
			return
		}
		if customerRouterListContains(output.Skills, id) {
			return
		}
		output.Skills = append(output.Skills, id)
	}
	if customerRouterLooksDedicatedPrice(userText) {
		appendSkill(customerSkillQuoteResidential)
	}
	if customerRouterLooksStaticBandwidthPrice(userText) {
		appendSkill(customerSkillQuoteStaticIP)
	}
	if customerRouterLooksQuantityCompare(userText) {
		appendSkill(customerSkillCompareQuantity)
	}
	if customerRouterLooksSharedDedicatedCompare(userText) && !customerRouterLooksPriceQuestion(userText) {
		appendSkill(customerSkillCompareShared)
	}
	output.Skills = normalizeCustomerRouterSkills(output.Skills)
	return output
}

func (s *CustomerChatService) loadCustomerSkillBody(id string) (string, error) {
	def, ok := customerSkillDefinitions[id]
	if !ok {
		return "", fmt.Errorf("unknown customer skill %q", id)
	}
	return s.loadPrompt(def.PromptFile)
}

func (s *CustomerChatService) formatCustomerActiveSkills(profile CustomerSpecialistProfile, routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil || len(routerOutput.Skills) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(routerOutput.Skills))
	for _, id := range routerOutput.Skills {
		def, ok := customerSkillDefinitions[id]
		if !ok {
			continue
		}
		if def.Specialist != "" && def.Specialist != profile.Name {
			continue
		}
		body, err := s.loadCustomerSkillBody(id)
		if err != nil {
			continue
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		parts = append(parts, "### skill: "+id+"\n"+body)
	}
	if len(parts) == 0 {
		return "[]"
	}
	return strings.Join(parts, "\n\n")
}

func customerSkillPromptFilesExist(promptDir string) bool {
	if strings.TrimSpace(promptDir) == "" {
		return false
	}
	for _, id := range customerRouterSkillIDs() {
		def := customerSkillDefinitions[id]
		path := filepath.Join(promptDir, filepath.FromSlash(def.PromptFile))
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}
