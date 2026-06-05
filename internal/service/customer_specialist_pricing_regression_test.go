package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
)

type pricingRegressionSuite struct {
	Version     string                  `json:"version"`
	Specialist  string                  `json:"specialist"`
	Description string                  `json:"description"`
	Cases       []pricingRegressionCase `json:"cases"`
}

type pricingRegressionCase struct {
	ID             string                            `json:"id"`
	Title          string                            `json:"title"`
	UserMessage    string                            `json:"user_message"`
	ReceivedAt     string                            `json:"received_at"`
	RouterOutput   CustomerRouterOutput              `json:"router_output"`
	CandidatePages []pricingRegressionCandidatePage  `json:"candidate_pages"`
	Expected       pricingRegressionExpectedBehavior `json:"expected"`
}

type pricingRegressionCandidatePage struct {
	Path       string `json:"path"`
	Title      string `json:"title"`
	Confidence string `json:"confidence"`
	Content    string `json:"content"`
}

type pricingRegressionExpectedBehavior struct {
	AllowedAnswerModes []string `json:"allowed_answer_modes"`
	MustInclude        []string `json:"must_include"`
	MustNotInclude     []string `json:"must_not_include"`
	SourcePaths        []string `json:"source_paths"`
	Notes              string   `json:"notes"`
}

func TestCustomerSpecialistPricingRegressionFixture(t *testing.T) {
	suite := loadPricingRegressionSuite(t)
	if suite.Version != "customer_specialist_pricing.regression.v1" {
		t.Fatalf("unexpected pricing regression version %q", suite.Version)
	}
	if suite.Specialist != "pricing" {
		t.Fatalf("unexpected regression specialist %q", suite.Specialist)
	}
	if len(suite.Cases) < 9 {
		t.Fatalf("expected at least 9 pricing regression cases, got %d", len(suite.Cases))
	}

	svc := NewCustomerChatService(Deps{Config: &config.Config{}})
	profile := customerSpecialistProfile("pricing")
	seenIDs := map[string]bool{}
	for _, tc := range suite.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			validatePricingRegressionCase(t, tc, seenIDs, profile)

			evidence := pricingRegressionEvidence(t, profile, tc.CandidatePages)
			userPrompt := svc.customerSpecialistDecisionPrompt(
				CustomerChatRequest{Question: tc.UserMessage},
				tc.ReceivedAt,
				&tc.RouterOutput,
				evidence,
				RuntimeSupportSettings{},
				"## 服务端行为\n\n- 客户可见正文只来自 `answer`。",
			)
			for _, want := range []string{
				"user_message:\n" + strings.TrimSpace(tc.UserMessage),
				"specialist: pricing",
				"hard_boundary:",
				"candidate_page_paths:",
				"candidate_pages:",
			} {
				if !strings.Contains(userPrompt, want) {
					t.Fatalf("expected generated pricing prompt to contain %q, got:\n%s", want, userPrompt)
				}
			}
			if len(tc.CandidatePages) == 0 && !strings.Contains(userPrompt, "candidate_pages:\n[]") {
				t.Fatalf("expected empty candidate pages marker, got:\n%s", userPrompt)
			}
		})
	}
}

func TestCustomerSpecialistPricingPromptCoversRegressionPolicies(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_pricing.md"))
	if err != nil {
		t.Fatalf("read pricing prompt: %v", err)
	}
	prompt := string(content)
	for _, want := range []string{
		"## 报价流程",
		"先判断是否承接上一轮价格问题",
		"抽取报价槽位",
		"客户有明显错别字但上下文能确定含义",
		"router_output.ambiguity.is_ambiguous=true",
		"普通问价且有基础价或起步价证据",
		"有哪些带宽/规格/档位",
		"不要推荐带宽或追问业务场景",
		"已有带宽但缺数量",
		"已有数量但缺带宽",
		"可按各带宽分别计算月费",
		"产品、类型、带宽、数量都齐全",
		"字段齐全报价推荐句式",
		"不要复述、确认或记录客户刚说过的规格",
		"不要另起标题或模板句",
		"不要写来源包装词",
		"不要写服务动作",
		"不要为了销售转化追问业务场景",
		"## 价格边界",
		"后台自动改价",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected pricing prompt to cover policy %q, got:\n%s", want, prompt)
		}
	}
}

func loadPricingRegressionSuite(t *testing.T) pricingRegressionSuite {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "customer_specialist_pricing_regression.json"))
	if err != nil {
		t.Fatalf("read pricing regression fixture: %v", err)
	}
	var suite pricingRegressionSuite
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatalf("decode pricing regression fixture: %v", err)
	}
	return suite
}

func validatePricingRegressionCase(t *testing.T, tc pricingRegressionCase, seenIDs map[string]bool, profile CustomerSpecialistProfile) {
	t.Helper()
	if strings.TrimSpace(tc.ID) == "" || strings.Contains(tc.ID, " ") {
		t.Fatalf("case id must be non-empty and space-free, got %q", tc.ID)
	}
	if seenIDs[tc.ID] {
		t.Fatalf("duplicate pricing regression case id %q", tc.ID)
	}
	seenIDs[tc.ID] = true
	if strings.TrimSpace(tc.Title) == "" || strings.TrimSpace(tc.UserMessage) == "" || strings.TrimSpace(tc.ReceivedAt) == "" {
		t.Fatalf("case %s must include title, user_message and received_at", tc.ID)
	}
	if tc.RouterOutput.ContractVersion != customerRouterContractVersion {
		t.Fatalf("case %s contract_version = %q", tc.ID, tc.RouterOutput.ContractVersion)
	}
	if tc.RouterOutput.Specialist != "pricing" {
		t.Fatalf("case %s specialist = %q", tc.ID, tc.RouterOutput.Specialist)
	}
	if !tc.RouterOutput.NeedsRetrieval || len(tc.RouterOutput.RetrievalQueries) == 0 {
		t.Fatalf("case %s must model retrieval-backed pricing flow", tc.ID)
	}
	if len(tc.Expected.AllowedAnswerModes) == 0 || strings.TrimSpace(tc.Expected.Notes) == "" {
		t.Fatalf("case %s must define expected answer modes and notes", tc.ID)
	}
	for _, mode := range tc.Expected.AllowedAnswerModes {
		if normalizedAnswerMode(mode) != mode {
			t.Fatalf("case %s has invalid expected answer mode %q", tc.ID, mode)
		}
	}
	validatePricingRegressionExpectations(t, tc)
	validatePricingRegressionSources(t, tc, profile)
}

func validatePricingRegressionExpectations(t *testing.T, tc pricingRegressionCase) {
	t.Helper()
	if len(tc.Expected.MustInclude) == 0 || len(tc.Expected.MustNotInclude) == 0 {
		t.Fatalf("case %s must define must_include and must_not_include", tc.ID)
	}
	mustNot := map[string]bool{}
	for _, item := range tc.Expected.MustNotInclude {
		item = strings.TrimSpace(item)
		if item == "" {
			t.Fatalf("case %s has empty must_not_include", tc.ID)
		}
		mustNot[item] = true
	}
	for _, item := range tc.Expected.MustInclude {
		item = strings.TrimSpace(item)
		if item == "" {
			t.Fatalf("case %s has empty must_include", tc.ID)
		}
		if mustNot[item] {
			t.Fatalf("case %s expects and forbids %q", tc.ID, item)
		}
	}
}

func validatePricingRegressionSources(t *testing.T, tc pricingRegressionCase, profile CustomerSpecialistProfile) {
	t.Helper()
	candidatePaths := map[string]bool{}
	for _, page := range tc.CandidatePages {
		if strings.TrimSpace(page.Path) == "" || strings.TrimSpace(page.Title) == "" || strings.TrimSpace(page.Content) == "" {
			t.Fatalf("case %s candidate pages must include path, title and content", tc.ID)
		}
		if !profile.AllowsPath(page.Path) {
			t.Fatalf("case %s candidate page %s is outside pricing scope", tc.ID, page.Path)
		}
		candidatePaths[page.Path] = true
	}
	for _, path := range tc.Expected.SourcePaths {
		if !candidatePaths[path] {
			t.Fatalf("case %s expected source %s must be present in candidate_pages", tc.ID, path)
		}
	}
}

func pricingRegressionEvidence(t *testing.T, profile CustomerSpecialistProfile, pages []pricingRegressionCandidatePage) customerSpecialistEvidenceResult {
	t.Helper()
	result := customerSpecialistEvidenceResult{Profile: profile}
	for _, page := range pages {
		source := SourceRef{
			Path:       page.Path,
			Title:      page.Title,
			Confidence: page.Confidence,
		}
		result.Sources = append(result.Sources, source)
		result.ContentBlocks = append(result.ContentBlocks, formatCandidatePageBlock(source, page.Content))
	}
	return result
}
