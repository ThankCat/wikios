package service_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wikios/internal/config"
	"wikios/internal/llm"
	"wikios/internal/retrieval"
	"wikios/internal/runtime"
	"wikios/internal/service"
	"wikios/internal/store"
	"wikios/internal/tools"
	"wikios/internal/wikiadapter"
)

type countingPublicLLM struct {
	answer   string
	answers  []string
	calls    int
	messages [][]llm.Message
}

func (m *countingPublicLLM) Chat(_ context.Context, _ string, messages []llm.Message) (string, error) {
	m.calls++
	m.messages = append(m.messages, messages)
	if len(m.answers) >= m.calls {
		return m.answers[m.calls-1], nil
	}
	return m.answer, nil
}

func (m *countingPublicLLM) StreamChat(ctx context.Context, model string, messages []llm.Message, onDelta func(string)) (string, error) {
	text, err := m.Chat(ctx, model, messages)
	if err != nil {
		return "", err
	}
	if onDelta != nil {
		onDelta(text)
	}
	return text, nil
}

type qmdTestTool struct {
	queryStdout    string
	updateErr      bool
	queryCalls     int
	updateCalls    int
	queryQuestions []string
}

func (t *qmdTestTool) Name() string {
	return "exec.qmd"
}

func (t *qmdTestTool) RiskLevel() runtime.RiskLevel {
	return runtime.RiskMedium
}

func (t *qmdTestTool) Validate(args map[string]any) error {
	if _, ok := args["subcommand"].(string); !ok {
		return nil
	}
	return nil
}

func (t *qmdTestTool) Execute(_ context.Context, _ *runtime.ExecEnv, args map[string]any) (runtime.ToolResult, error) {
	subcommand, _ := args["subcommand"].(string)
	switch subcommand {
	case "query":
		t.queryCalls++
		if question, _ := args["question"].(string); strings.TrimSpace(question) != "" {
			t.queryQuestions = append(t.queryQuestions, question)
		}
		stdout := t.queryStdout
		if strings.TrimSpace(stdout) == "" {
			stdout = "[]"
		}
		return runtime.ToolResult{
			Success:   true,
			RiskLevel: runtime.RiskMedium,
			Data:      map[string]any{"subcommand": subcommand, "stdout": stdout, "stderr": "", "exit_code": 0},
		}, nil
	case "update":
		t.updateCalls++
		data := map[string]any{"subcommand": subcommand, "stdout": "", "stderr": "", "exit_code": 0}
		if t.updateErr {
			data["stderr"] = "qmd update failed for test"
			data["exit_code"] = 1
			return runtime.ToolResult{
				Success:   false,
				RiskLevel: runtime.RiskMedium,
				Data:      data,
				Error:     &runtime.ToolError{Code: "EXEC_FAILED", Message: "qmd update failed for test"},
			}, nil
		}
		return runtime.ToolResult{Success: true, RiskLevel: runtime.RiskMedium, Data: data}, nil
	default:
		return runtime.ToolResult{
			Success:   true,
			RiskLevel: runtime.RiskMedium,
			Data:      map[string]any{"subcommand": subcommand, "stdout": "", "stderr": "", "exit_code": 0},
		}, nil
	}
}

func newReviewQueueTestDeps(t *testing.T, answer string) (service.Deps, *countingPublicLLM, string) {
	deps, mock, root, _ := newReviewQueueTestDepsWithQMD(t, answer, &qmdTestTool{})
	return deps, mock, root
}

func newReviewQueueTestDepsWithQMD(t *testing.T, answer string, qmd *qmdTestTool) (service.Deps, *countingPublicLLM, string, *qmdTestTool) {
	t.Helper()
	if qmd == nil {
		qmd = &qmdTestTool{}
	}
	root := createPublicFixtureWiki(t)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/log.md"), "# log\n")
	intentPath := filepath.Join(t.TempDir(), "public_intents.yaml")
	if err := os.WriteFile(intentPath, []byte(defaultPublicIntentTestYAML()), 0o644); err != nil {
		t.Fatalf("write public intents: %v", err)
	}
	enabled := true
	cfg := &config.Config{
		MountedWiki: config.MountedWikiConfig{
			Root:     root,
			QMDIndex: "missing-index-for-test",
		},
		Retrieval:     config.RetrievalConfig{TopK: 3},
		Workspace:     config.WorkspaceConfig{BaseDir: t.TempDir()},
		Sandbox:       config.SandboxConfig{QMDTimeoutSec: 1},
		LLM:           config.LLMConfig{ModelPublic: "test"},
		PublicIntents: config.PublicIntentsConfig{Enabled: &enabled, Path: intentPath},
	}
	dataStore, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	registry := runtime.NewRegistry()
	tools.RegisterAll(registry, tools.Dependencies{
		Config:   cfg,
		Resolver: wikiadapter.NewPathResolver(cfg.MountedWiki.Root),
	})
	registry.Register(qmd)
	rt := runtime.NewRuntime(registry, runtime.NewPolicyEngine(), runtime.NewValidator(), runtime.NewAuditLogger())
	mock := &countingPublicLLM{answer: answer}
	deps := service.Deps{
		Config:        cfg,
		Runtime:       rt,
		LLM:           mock,
		Retriever:     retrieval.NewQMDRetriever(rt),
		Store:         dataStore,
		PublicIntents: service.NewPublicIntentManager(cfg.PublicIntents),
		PromptDir:     "../../internal/llm/prompts",
		WorkspaceDir:  cfg.Workspace.BaseDir,
	}
	return deps, mock, root, qmd
}

func TestPublicFAQHitDoesNotCreateUnconfirmedReview(t *testing.T) {
	qmd := &qmdTestTool{queryStdout: `[{"file":"qmd://wiki/faq/customer-qa.md","score":1}]`}
	deps, mock, root, qmd := newReviewQueueTestDepsWithQMD(t, `{
  "answer_type": "text",
  "answer_markdown": "静态IP适合账号运营、白名单绑定和远程办公。",
 "sources": [{"path":"wiki/faq/customer-qa.md","confidence":"high"}],
  "confidence": 0.9,
  "notes": ""
}`, qmd)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "静态IP的使用场景是什么？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "账号运营") {
		t.Fatalf("expected FAQ-backed answer, got %+v", resp)
	}
	if qmd.queryCalls == 0 {
		t.Fatalf("expected qmd query to be used")
	}
	if mock.calls == 0 {
		t.Fatalf("expected FAQ-backed answer to be generated by LLM")
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read unconfirmed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no unconfirmed review for FAQ hit, got %d", len(entries))
	}
}

func TestPublicFAQEvidenceUsesSingleLLMDecision(t *testing.T) {
	qmd := &qmdTestTool{queryStdout: `[{"file":"qmd://wiki/faq/faq-other.md","score":1}]`}
	deps, mock, root, _ := newReviewQueueTestDepsWithQMD(t, `{
  "answer_mode": "evidence",
  "answer_markdown": "子网掩码是一个32位二进制数，用于划分IP地址的网络部分和主机部分。在代理IP配置中，它会影响设备与代理服务器是否处于同一网络段。",
  "sources": [{"path":"wiki/faq/faq-other.md","confidence":"high"}],
  "confidence": 0.9,
  "evidence_confidence": 0.9,
  "review_required": false,
  "review_reason": "",
  "suggested_faq_path": "wiki/faq/faq-other.md"
}`, qmd)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/faq/faq-other.md"), `---
title: Other FAQ
type: faq
---

## FAQ Entries

### review-f960156ce0 · 什么是子网掩码

- ID：review-f960156ce0
- 标准问法：什么是子网掩码

#### 回复

子网掩码（Subnet Mask）是一个32位二进制数，用于划分IP地址的网络部分和主机部分。在代理IP配置中，设置正确的子网掩码能确保你的设备与代理服务器处于同一网络段，从而正常通信。

- 条件元数据：
  - 来源：wiki/unconfirmed/review-test.md；管理员审查通过。
`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "什么是子网掩码"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "网络部分") {
		t.Fatalf("expected FAQ-backed answer, got %+v", resp)
	}
	if mock.calls != 1 {
		t.Fatalf("expected one unified public LLM call, got %d", mock.calls)
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read unconfirmed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no unconfirmed review for repaired FAQ hit, got %d", len(entries))
	}
}

func TestPublicApprovedFAQExistingButUnretrievedDoesNotBypassQMD(t *testing.T) {
	qmd := &qmdTestTool{queryStdout: `[{"file":"qmd://wiki/index.md","score":1}]`}
	deps, mock, root, _ := newReviewQueueTestDepsWithQMD(t, `{
  "can_answer": true,
  "category": "safe_concept",
  "risk_level": "low",
  "answer_markdown": "这是正式条目未命中后的低置信自答草稿。",
  "confidence": 0.4,
  "boundary_reason": "qmd 未命中正式条目。",
  "suggested_faq_path": "wiki/faq/customer-qa.md",
  "review_required": true
}`, qmd)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/faq/faq-other.md"), `---
title: Other FAQ
type: faq
---

## FAQ Entries

### review-f960156ce0 · 什么是子网掩码

- ID：review-f960156ce0
- 标准问法：什么是子网掩码

#### 回复

子网掩码是一个32位的二进制掩码，用于将IP地址划分为网络地址和主机地址两部分。
- 条件元数据：
  - 来源：wiki/unconfirmed/review-test.md；管理员审查通过。
`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "什么是子网掩码"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if strings.Contains(resp.Answer, "网络地址和主机地址") {
		t.Fatalf("expected qmd-unretrieved FAQ not to be returned directly, got %+v", resp)
	}
	if !strings.Contains(resp.Answer, "低置信自答") {
		t.Fatalf("expected self answer when qmd misses approved FAQ, got %+v", resp)
	}
	if mock.calls != 1 {
		t.Fatalf("expected one self-answer LLM call, got %d", mock.calls)
	}
}

func TestPublicMissSelfAnswersAndCreatesUnconfirmedReview(t *testing.T) {
	deps, _, root := newReviewQueueTestDeps(t, `{
  "answer_markdown": "可以先打开我们的客户端，确认当前选择的是对应线路。您现在是在电脑还是手机上使用？",
  "confidence": 0.42,
  "boundary_reason": "FAQ 未命中，只能作为低置信草稿。",
  "suggested_faq_path": "wiki/faq/customer-qa.md",
  "review_required": true
}`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "代理客户端缓存怎么清理？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "电脑还是手机") {
		t.Fatalf("expected self answer, got %+v", resp)
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed"))
	if err != nil {
		t.Fatalf("read unconfirmed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one unconfirmed review, got %d", len(entries))
	}
}

func TestPublicLowConfidenceWithoutReviewRequiredDoesNotCreateReview(t *testing.T) {
	deps, _, root := newReviewQueueTestDeps(t, `{
  "answer_mode": "clarification",
  "answer_markdown": "您好，请问您具体想咨询什么问题？",
  "confidence": 0.42,
  "evidence_confidence": 0,
  "review_reason": "",
  "suggested_faq_path": "",
  "review_required": false
}`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "随便聊聊"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "具体想咨询") {
		t.Fatalf("expected clarification answer, got %+v", resp)
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read unconfirmed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no unconfirmed review, got %d", len(entries))
	}
}

func TestPublicPureNoiseDoesNotCreateReviewEvenIfLLMRequestsIt(t *testing.T) {
	deps, _, root := newReviewQueueTestDeps(t, `{
  "answer_mode": "clarification",
  "answer_markdown": "您好，我注意到您输入的内容像是测试或误输入。请问您具体想咨询什么问题？",
  "review_question": "用户输入无意义数字时如何回复？",
  "confidence": 0.45,
  "evidence_confidence": 0,
  "review_reason": "测试输入不应沉淀为 FAQ。",
  "suggested_faq_path": "wiki/faq/customer-qa.md",
  "review_required": true
}`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "123455667"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "误输入") {
		t.Fatalf("expected clarification answer, got %+v", resp)
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read unconfirmed: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no unconfirmed review for pure noise, got %d", len(entries))
	}
}

func TestPublicNetworkConceptMissSelfAnswersAndCreatesReview(t *testing.T) {
	deps, _, root := newReviewQueueTestDeps(t, `{
  "answer_markdown": "子网掩码可以理解为用来划分一个 IP 地址里“网络部分”和“主机部分”的规则。您是想在客户端里填写网络配置，还是想了解它和 IP 地址的关系？",
  "confidence": 0.45,
  "boundary_reason": "网络连接基础概念未命中 FAQ，只能作为低置信草稿。",
  "suggested_faq_path": "wiki/faq/customer-qa.md",
  "review_required": true
}`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "什么是子网掩码"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "网络部分") {
		t.Fatalf("expected network concept self answer, got %+v", resp)
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed"))
	if err != nil {
		t.Fatalf("read unconfirmed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one unconfirmed review, got %d", len(entries))
	}
}

func TestPublicReviewQuestionUsesConversationContext(t *testing.T) {
	deps, _, _ := newReviewQueueTestDeps(t, `{
  "answer_mode": "self_answer",
  "answer_markdown": "您好，了解到您的电脑是 Windows 7。配置代理后没有网络，可以先检查 Win7 的 Internet 选项代理设置，再确认代理地址、端口和协议是否填写正确。",
  "review_question": "Windows 7 配置代理 IP 后没有网络怎么办？",
  "confidence": 0.6,
  "evidence_confidence": 0,
  "review_reason": "用户上一轮反馈配置代理后无网络，本轮补充系统为 Win7；低风险排查建议需要人工沉淀。",
  "suggested_faq_path": "wiki/faq/customer-qa.md",
  "review_required": true
}`)
	svc := service.NewPublicQueryService(deps)
	_, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{
		Question:          "我的电脑是win7的",
		SessionID:         "session-win7",
		QuestionMessageID: "question-win7",
		History: []service.ChatMessage{
			{Role: "user", Content: "我的电脑配置上代理IP之后没有网络了"},
			{Role: "assistant", Content: "请问您的电脑是什么系统？"},
		},
	})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	queue := service.NewReviewQueueService(deps)
	next, err := queue.Next(context.Background(), "")
	if err != nil {
		t.Fatalf("next review: %v", err)
	}
	if next.Item == nil {
		t.Fatalf("expected review item")
	}
	if next.Item.Question != "Windows 7 配置代理 IP 后没有网络怎么办？" {
		t.Fatalf("expected contextual review question, got %q", next.Item.Question)
	}
	if next.Item.OriginalQuestion != "我的电脑是win7的" {
		t.Fatalf("expected original question to be preserved, got %q", next.Item.OriginalQuestion)
	}
}

func TestPublicUnlistedSafeConceptLetsLLMDecide(t *testing.T) {
	deps, mock, root := newReviewQueueTestDeps(t, `{
  "can_answer": true,
  "category": "safe_concept",
  "risk_level": "low",
  "answer_markdown": "localhost 通常表示当前这台设备自己。如果您在代理配置里看到 localhost，可能是在指向本机运行的客户端或本地代理服务。您是在哪个配置项里看到这个地址？",
  "confidence": 0.45,
  "boundary_reason": "低风险网络基础概念，可作为待审核草稿。",
  "suggested_faq_path": "wiki/faq/customer-qa.md",
  "review_required": true
}`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "localhost 是什么"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "当前这台设备") {
		t.Fatalf("expected LLM-decided safe concept answer, got %+v", resp)
	}
	if mock.calls != 1 {
		t.Fatalf("expected one self-answer LLM call, got %d", mock.calls)
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed"))
	if err != nil {
		t.Fatalf("read unconfirmed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one unconfirmed review, got %d", len(entries))
	}
}

func TestPublicSelfAnswerSanitizesVisibleFAQRefusalWithoutRetry(t *testing.T) {
	deps, mock, root := newReviewQueueTestDeps(t, `{
  "can_answer": true,
  "category": "safe_concept",
  "risk_level": "low",
  "answer_markdown": "抱歉，这个问题目前不在我们常见FAQ的范围内，暂时没办法给您准确确认。",
  "confidence": 0.4,
  "boundary_reason": "低风险概念但输出了拒答。",
  "suggested_faq_path": "wiki/faq/customer-qa.md",
  "review_required": true
}`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "为什么内网IP是127.0.0.1呢?"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if strings.Contains(strings.ToLower(resp.Answer), "faq") || strings.Contains(resp.Answer, "暂时没办法") {
		t.Fatalf("expected customer-visible FAQ wording to be removed, got %+v", resp)
	}
	if mock.calls != 1 {
		t.Fatalf("expected no semantic retry, got %d calls", mock.calls)
	}
	entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed"))
	if err != nil {
		t.Fatalf("read unconfirmed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one unconfirmed review, got %d", len(entries))
	}
}

func TestPublicQueryCarriesTimedHistoryToRetrievalAndLLM(t *testing.T) {
	qmd := &qmdTestTool{}
	deps, mock, _, qmd := newReviewQueueTestDepsWithQMD(t, `{
  "can_answer": true,
  "category": "safe_concept",
  "risk_level": "low",
  "answer_markdown": "网关是网络出口或路由节点，负责把请求转发到其他网络。您是在系统网络设置还是代理配置里看到这个字段？",
  "confidence": 0.45,
  "boundary_reason": "低风险网络基础概念，可作为待审核草稿。",
  "suggested_faq_path": "wiki/faq/customer-qa.md",
  "review_required": true
}`, qmd)
	svc := service.NewPublicQueryService(deps)
	_, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{
		Question: "什么是网关",
		History: []service.ChatMessage{
			{Role: "user", Content: "为什么内网IP是127.0.0.1呢?", CreatedAt: "2026-04-28T10:00:00Z"},
			{Role: "assistant", Content: "127.0.0.1 是本机回环地址。", CreatedAt: "2026-04-28T10:00:03Z"},
		},
	})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if len(qmd.queryQuestions) != 1 || !strings.Contains(qmd.queryQuestions[0], "127.0.0.1") {
		t.Fatalf("expected retrieval to carry recent history, got %#v", qmd.queryQuestions)
	}
	if len(mock.messages) != 1 || len(mock.messages[0]) < 2 {
		t.Fatalf("expected captured self-answer prompt, got %#v", mock.messages)
	}
	if !strings.Contains(mock.messages[0][1].Content, "2026-04-28T10:00:00Z") || !strings.Contains(mock.messages[0][1].Content, "127.0.0.1") {
		t.Fatalf("expected timed history in LLM prompt, got %s", mock.messages[0][1].Content)
	}
}

func TestPublicForbiddenQuestionDoesNotCallLLM(t *testing.T) {
	deps, mock, root := newReviewQueueTestDeps(t, `{
  "answer_markdown": "不应该调用",
  "confidence": 0.4,
  "boundary_reason": "",
  "suggested_faq_path": "",
  "review_required": true
}`)
	mustWritePublicFixture(t, filepath.Join(root, "wiki/forbidden/review-test.md"), `---
type: forbidden-qa
question: 代理客户端缓存怎么清理
graph-excluded: true
---

# 禁止回复问题
`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "代理客户端缓存如何清理？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if !strings.Contains(resp.Answer, "不能继续回复") {
		t.Fatalf("expected forbidden reply, got %+v", resp)
	}
	if mock.calls != 0 {
		t.Fatalf("expected no LLM call for forbidden question, got %d", mock.calls)
	}
}

func TestPublicHighRiskMissDoesNotSelfAnswerOrCreateReview(t *testing.T) {
	deps, mock, root := newReviewQueueTestDeps(t, `{
  "can_answer": false,
  "category": "business_claim",
  "risk_level": "high",
  "answer_markdown": "",
  "confidence": 0.1,
  "boundary_reason": "退款和到账承诺需要正式 FAQ 或人工确认。",
  "suggested_faq_path": "",
  "review_required": true
}`)
	svc := service.NewPublicQueryService(deps)
	resp, err := svc.Answer(context.Background(), "trace-test", service.PublicAnswerRequest{Question: "退款多少钱可以保证马上到账？"})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if strings.TrimSpace(resp.Answer) == "" {
		t.Fatalf("expected fallback answer")
	}
	if mock.calls != 1 {
		t.Fatalf("expected LLM to classify high-risk miss once, got %d", mock.calls)
	}
	if entries, err := os.ReadDir(filepath.Join(root, "wiki/unconfirmed")); err == nil && len(entries) > 0 {
		t.Fatalf("expected no unconfirmed review, got %d", len(entries))
	}
}

func TestReviewApproveWritesFAQAndRemovesPending(t *testing.T) {
	deps, _, root, qmd := newReviewQueueTestDepsWithQMD(t, "", &qmdTestTool{})
	queue := service.NewReviewQueueService(deps)
	item, err := queue.CreatePending(context.Background(), service.ReviewCreateRequest{
		Question:         "代理客户端缓存怎么清理？",
		DraftAnswer:      "可以先重启客户端再重新连接。",
		SuggestedFAQPath: "wiki/faq/customer-qa.md",
		Confidence:       0.4,
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if _, err := queue.Approve(context.Background(), item.ID, service.ReviewApproveRequest{
		Question:   item.Question,
		Answer:     "可以先重启客户端，再重新选择线路连接。",
		TargetPath: "wiki/faq/customer-qa.md",
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if qmd.updateCalls != 1 {
		t.Fatalf("expected one qmd update, got %d", qmd.updateCalls)
	}
	sourcePath := filepath.Join(root, "wiki/sources", item.ID+".md")
	sourceRaw, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("expected review source archive: %v", err)
	}
	if !strings.Contains(string(sourceRaw), "review-ingest-source") || !strings.Contains(string(sourceRaw), "重新选择线路连接") {
		t.Fatalf("expected approved review source archive, got %s", string(sourceRaw))
	}
	raw, err := os.ReadFile(filepath.Join(root, "wiki/faq/customer-qa.md"))
	if err != nil {
		t.Fatalf("read faq: %v", err)
	}
	if !strings.Contains(string(raw), "重新选择线路连接") || !strings.Contains(string(raw), "wiki/sources/"+item.ID+".md") {
		t.Fatalf("expected approved FAQ entry, got %s", string(raw))
	}
	logRaw, err := os.ReadFile(filepath.Join(root, "wiki/log.md"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(logRaw), "ingest | review-approve") {
		t.Fatalf("expected approve to be logged as ingest, got %s", string(logRaw))
	}
	if count, err := queue.PendingCount(context.Background()); err != nil || count != 0 {
		t.Fatalf("expected pending count 0, got %d err=%v", count, err)
	}
}

func TestReviewApproveRollsBackExistingFAQWhenQMDUpdateFails(t *testing.T) {
	deps, _, root, _ := newReviewQueueTestDepsWithQMD(t, "", &qmdTestTool{updateErr: true})
	queue := service.NewReviewQueueService(deps)
	target := filepath.Join(root, "wiki/faq/customer-qa.md")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read original FAQ: %v", err)
	}
	item, err := queue.CreatePending(context.Background(), service.ReviewCreateRequest{
		Question:         "代理客户端缓存怎么清理？",
		DraftAnswer:      "可以先重启客户端再重新连接。",
		SuggestedFAQPath: "wiki/faq/customer-qa.md",
		Confidence:       0.4,
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	_, err = queue.Approve(context.Background(), item.ID, service.ReviewApproveRequest{
		Question:   item.Question,
		Answer:     "这条内容不应该保留。",
		TargetPath: "wiki/faq/customer-qa.md",
	})
	if err == nil || !strings.Contains(err.Error(), "qmd update") {
		t.Fatalf("expected qmd update error, got %v", err)
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read rolled back FAQ: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("expected FAQ rollback")
	}
	if _, err := os.Stat(filepath.Join(root, item.Path)); err != nil {
		t.Fatalf("expected pending file to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "wiki/sources", item.ID+".md")); !os.IsNotExist(err) {
		t.Fatalf("expected source archive rollback, err=%v", err)
	}
}

func TestReviewApproveRemovesNewFAQWhenQMDUpdateFails(t *testing.T) {
	deps, _, root, _ := newReviewQueueTestDepsWithQMD(t, "", &qmdTestTool{updateErr: true})
	queue := service.NewReviewQueueService(deps)
	item, err := queue.CreatePending(context.Background(), service.ReviewCreateRequest{
		Question:         "代理客户端缓存怎么清理？",
		DraftAnswer:      "可以先重启客户端再重新连接。",
		SuggestedFAQPath: "wiki/faq/new-review-target.md",
		Confidence:       0.4,
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	_, err = queue.Approve(context.Background(), item.ID, service.ReviewApproveRequest{
		Question:   item.Question,
		Answer:     item.DraftAnswer,
		TargetPath: "wiki/faq/new-review-target.md",
	})
	if err == nil || !strings.Contains(err.Error(), "qmd update") {
		t.Fatalf("expected qmd update error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "wiki/faq/new-review-target.md")); !os.IsNotExist(err) {
		t.Fatalf("expected new FAQ target to be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, item.Path)); err != nil {
		t.Fatalf("expected pending file to remain: %v", err)
	}
}

func TestReviewRejectMovesToForbiddenAndBlocksSimilarQuestions(t *testing.T) {
	deps, _, root, qmd := newReviewQueueTestDepsWithQMD(t, "", &qmdTestTool{})
	queue := service.NewReviewQueueService(deps)
	item, err := queue.CreatePending(context.Background(), service.ReviewCreateRequest{
		Question:    "代理客户端缓存怎么清理？",
		DraftAnswer: "可以先重启客户端。",
		Confidence:  0.4,
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if _, err := queue.Reject(context.Background(), item.ID, service.ReviewRejectRequest{Reason: "不适合自动回复"}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if qmd.updateCalls != 1 {
		t.Fatalf("expected one qmd update for reject ingest, got %d", qmd.updateCalls)
	}
	forbiddenRaw, err := os.ReadFile(filepath.Join(root, "wiki/forbidden", item.ID+".md"))
	if err != nil {
		t.Fatalf("expected forbidden file: %v", err)
	}
	if !strings.Contains(string(forbiddenRaw), "review-rejected") || !strings.Contains(string(forbiddenRaw), "不适合自动回复") {
		t.Fatalf("expected rejected review ingest, got %s", string(forbiddenRaw))
	}
	logRaw, err := os.ReadFile(filepath.Join(root, "wiki/log.md"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(logRaw), "ingest | review-reject") {
		t.Fatalf("expected reject to be logged as ingest, got %s", string(logRaw))
	}
	if _, ok, err := queue.MatchForbidden(context.Background(), "代理客户端缓存如何清理？"); err != nil || !ok {
		t.Fatalf("expected similar forbidden match, ok=%t err=%v", ok, err)
	}
}

func TestReviewRejectRollsBackForbiddenWhenQMDUpdateFails(t *testing.T) {
	deps, _, root, _ := newReviewQueueTestDepsWithQMD(t, "", &qmdTestTool{updateErr: true})
	queue := service.NewReviewQueueService(deps)
	item, err := queue.CreatePending(context.Background(), service.ReviewCreateRequest{
		Question:    "代理客户端缓存怎么清理？",
		DraftAnswer: "可以先重启客户端。",
		Confidence:  0.4,
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	_, err = queue.Reject(context.Background(), item.ID, service.ReviewRejectRequest{Reason: "不适合自动回复"})
	if err == nil || !strings.Contains(err.Error(), "qmd update") {
		t.Fatalf("expected qmd update error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "wiki/forbidden", item.ID+".md")); !os.IsNotExist(err) {
		t.Fatalf("expected forbidden rollback, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, item.Path)); err != nil {
		t.Fatalf("expected pending file to remain: %v", err)
	}
}

func TestReviewDeleteRemovesPendingWithoutForbiddenRecord(t *testing.T) {
	deps, _, root := newReviewQueueTestDeps(t, "")
	queue := service.NewReviewQueueService(deps)
	item, err := queue.CreatePending(context.Background(), service.ReviewCreateRequest{
		Question:    "123455667",
		DraftAnswer: "看起来像测试输入。",
		Confidence:  0.4,
	})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	deleted, err := queue.Delete(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted.ID != item.ID {
		t.Fatalf("expected deleted item %s, got %s", item.ID, deleted.ID)
	}
	if _, err := os.Stat(filepath.Join(root, item.Path)); !os.IsNotExist(err) {
		t.Fatalf("expected pending file removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "wiki/forbidden", item.ID+".md")); !os.IsNotExist(err) {
		t.Fatalf("expected no forbidden file, err=%v", err)
	}
	if count, err := queue.PendingCount(context.Background()); err != nil || count != 0 {
		t.Fatalf("expected pending count 0, got %d err=%v", count, err)
	}
}
