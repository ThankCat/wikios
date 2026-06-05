package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
)

func TestLoadCustomerSpecialistSystemPromptComposesBaseAndRole(t *testing.T) {
	root := t.TempDir()
	promptDir := testCustomerRouterPromptDir(t)
	writeCustomerRoutedTestPrompts(t, root, promptDir)

	svc := NewCustomerChatService(Deps{Config: &config.Config{}, PromptDir: promptDir})
	systemPrompt, err := svc.loadCustomerSpecialistSystemPrompt(customerSpecialistProfile("pricing"))
	if err != nil {
		t.Fatalf("loadCustomerSpecialistSystemPrompt: %v", err)
	}
	for _, want := range []string{
		"user 消息字段",
		"不要机械复述客户刚说过的话",
		"不要使用制式回答骨架",
		"月费参考",
		"不要用“官方/官网/公开/公开定价",
		"不要编造服务动作或指令",
		"价格套餐客服",
		"输出前自检（L4）",
		customerSpecialistPromptSeparator,
		"完成上文「输出前自检（L4）」后，只返回一个 JSON 对象",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("expected system prompt to include %q, got:\n%s", want, systemPrompt)
		}
	}
}

func TestLoadCustomerSpecialistBoundaryUsesPromptFile(t *testing.T) {
	root := t.TempDir()
	promptDir := testCustomerRouterPromptDir(t)
	writeCustomerRoutedTestPrompts(t, root, promptDir)

	svc := NewCustomerChatService(Deps{Config: &config.Config{}, PromptDir: promptDir})
	boundary, err := svc.loadCustomerSpecialistBoundary()
	if err != nil {
		t.Fatalf("loadCustomerSpecialistBoundary: %v", err)
	}
	if !strings.Contains(boundary, "服务端行为") {
		t.Fatalf("expected boundary prompt content, got:\n%s", boundary)
	}
}

func TestCustomerSpecialistProductPromptCoversSpecListAndTypoPolicies(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_product.md"))
	if err != nil {
		t.Fatalf("read product prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"## 回答流程",
		"只问规格列表",
		"有哪些带宽/规格/档位/类型",
		"只列可选项",
		"不要自动进入带宽推荐",
		"不要问“跑什么业务场景”",
		"明显错别字且上下文能确定含义",
		"不要显式解释",
		"只有客户明确问“哪个适合/怎么选”",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected product prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistRolePromptsUseWorkflowCards(t *testing.T) {
	cases := map[string][]string{
		"customer_specialist_pricing.md": {
			"## 职责边界",
			"## 证据原则",
			"## 报价流程",
			"## 输出形态",
		},
		"customer_specialist_product.md": {
			"## 职责边界",
			"## 证据原则",
			"## 回答流程",
			"## 输出形态",
		},
		"customer_specialist_purchase.md": {
			"## 职责边界",
			"## 证据原则",
			"## 回答流程",
			"## 输出形态",
		},
		"customer_specialist_technical.md": {
			"## 职责边界",
			"## 证据原则",
			"## 回答流程",
			"## 输出形态",
		},
		"customer_specialist_troubleshooting.md": {
			"## 职责边界",
			"## 证据原则",
			"## 排障流程",
			"## 输出形态",
		},
		"customer_specialist_billing_after_sales.md": {
			"## 职责边界",
			"## 证据原则",
			"## 售后流程",
			"## 输出形态",
		},
		"customer_specialist_safety.md": {
			"## 职责边界",
			"## 证据原则",
			"## 安全流程",
			"## 输出形态",
		},
		"customer_specialist_reception.md": {
			"## 职责边界",
			"## 证据原则",
			"## 接待流程",
			"## 输出形态",
		},
	}

	for file, wants := range cases {
		t.Run(file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", file))
			if err != nil {
				t.Fatalf("read role prompt: %v", err)
			}
			prompt := string(raw)
			for _, want := range wants {
				if !strings.Contains(prompt, want) {
					t.Fatalf("expected %s to include %q, got:\n%s", file, want, prompt)
				}
			}
			if strings.Contains(prompt, "`candidate_pages` 已按") {
				t.Fatalf("%s still uses old scoped-candidates wording:\n%s", file, prompt)
			}
		})
	}
}
