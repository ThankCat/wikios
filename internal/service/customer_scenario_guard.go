package service

import (
	"path/filepath"
	"sort"
	"strings"
)

type customerScenarioAnswerGuardResult struct {
	Triggered             bool
	Answer                string
	AnswerMode            string
	ReviewRequired        bool
	ReviewReason          string
	Reason                string
	Blocked               bool
	BlockReason           string
	AllowedProducts       []string
	OutputProducts        []string
	FallbackSources       []customerChatSource
	MinConfidence         float64
	MinEvidenceConfidence float64
}

type customerScenarioGuardInput struct {
	Request     CustomerChatRequest
	Router      *CustomerRouterOutput
	CurrentText string
	IntentText  string
	ContextText string
	ProductLock string
}

func newCustomerScenarioGuardInput(req CustomerChatRequest, routerOutput *CustomerRouterOutput) customerScenarioGuardInput {
	input := customerScenarioGuardInput{
		Request:     req,
		Router:      routerOutput,
		CurrentText: normalizeCustomerReviewText(req.Question),
		ContextText: customerScenarioGuardText(req, routerOutput),
	}
	if routerOutput != nil {
		input.IntentText = strings.ToLower(strings.TrimSpace(strings.Join([]string{
			routerOutput.Specialist,
			routerOutput.Intent,
			routerOutput.UserGoal,
			routerOutput.RewrittenQuestion,
			routerOutput.QuestionStage,
			routerOutput.AnswerStrategy,
			routerOutput.Slots.PrimaryProduct,
			strings.Join(routerOutput.Slots.Products, " "),
			routerOutput.Slots.StaticType,
			routerOutput.Slots.IPType,
			routerOutput.Slots.Device,
			routerOutput.Slots.Scenario,
		}, " ")))
		input.ProductLock = customerScenarioPrimaryProductLock(routerOutput)
	}
	return input
}

func customerScenarioPrimaryProductLock(routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil {
		return ""
	}
	product := strings.TrimSpace(routerOutput.Slots.PrimaryProduct)
	if product != "" && product != "unknown" {
		return product
	}
	for _, item := range routerOutput.Slots.Products {
		item = strings.TrimSpace(item)
		if item != "" && item != "unknown" {
			return item
		}
	}
	return ""
}

func customerScenarioAnswerGuard(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput) customerScenarioAnswerGuardResult {
	if routerOutput == nil {
		return customerScenarioAnswerGuardResult{Reason: "no_router_output"}
	}
	answer := strings.TrimSpace(parsed.AnswerText)
	decisionText := customerScenarioGuardText(req, routerOutput)
	if result, ok := customerScenarioHardGuardResult(req, parsed, routerOutput, decisionText, answer); ok {
		return result
	}
	return customerScenarioAnswerGuardResult{Reason: "pass_model_answer"}
}

func customerScenarioHardGuardResult(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, decisionText string, answer string) (customerScenarioAnswerGuardResult, bool) {
	if strings.TrimSpace(answer) == "" {
		return customerScenarioAnswerGuardResult{Reason: "empty_answer"}, true
	}
	if customerScenarioIsPlatformAntiBan(routerOutput, decisionText) ||
		customerScenarioIsBulkRegisterAntiBan(routerOutput, decisionText) ||
		customerScenarioIsVotingBoost(routerOutput, decisionText) ||
		customerScenarioIsClashShadowrocketVPN(routerOutput, decisionText) {
		return customerScenarioAnswerGuardResult{
			Triggered:      true,
			Answer:         "这类用途涉及规避平台规则或违规联网配置，不能提供操作方案。可以帮您说明代理 IP 的合规使用方式、产品差异或常规连接配置。",
			AnswerMode:     "refusal",
			Reason:         "safety_boundary",
			MinConfidence:  0.9,
			ReviewRequired: false,
		}, true
	}
	return customerScenarioAnswerGuardResult{}, false
}

func customerScenarioShouldPassThroughModelAnswer(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, decisionText string, answer string) bool {
	if strings.TrimSpace(answer) == "" || routerOutput == nil {
		return false
	}
	mode := normalizedAnswerMode(parsed.AnswerMode)
	if mode == "clarification" || mode == "refusal" {
		return false
	}
	if parsed.Confidence < 0.78 || parsed.EvidenceConfidence < 0.70 || len(parsed.Sources) == 0 {
		return false
	}
	if customerScenarioAnswerNeedsHardGuard(req, parsed, routerOutput, decisionText, answer) {
		return false
	}
	input := newCustomerScenarioGuardInput(req, routerOutput)
	if input.ProductLock != "" {
		outputProducts := customerScenarioAnswerOutputProducts(answer)
		allowed := customerScenarioAllowedProductsForResult(input, customerScenarioAnswerGuardResult{Answer: answer})
		if len(outputProducts) > 0 && !customerScenarioProductsAllowed(outputProducts, allowed) {
			return false
		}
	}
	return true
}

func customerScenarioAnswerNeedsHardGuard(req CustomerChatRequest, parsed customerChatLLMOutput, routerOutput *CustomerRouterOutput, decisionText string, answer string) bool {
	if customerAnswerLooksLikeInternalBoundary(answer) ||
		len(customerUnsafeVisibleAnswerHits(answer)) > 0 ||
		customerAnswerHasHumanContactGuidance(answer) {
		return true
	}
	if customerScenarioIsRefund(routerOutput, decisionText) &&
		(customerAnswerAsksForOrderInfo(answer) || customerAnswerHasForbiddenRefundPhrase(answer)) {
		return true
	}
	if customerScenarioIsPackageChange(routerOutput, decisionText) &&
		(customerAnswerHasForbiddenPackageChangePhrase(answer) || customerAnswerHasHumanContactGuidance(answer) || customerAnswerIsClarificationOnly(parsed, answer)) {
		return true
	}
	if customerScenarioIsOverseasIPSwitchUnsupported(routerOutput, decisionText) &&
		!customerAnswerHasOverseasIPSwitchUnsupportedTerms(answer) {
		return true
	}
	if customerScenarioIsPlatformRiskGuarantee(routerOutput, decisionText) &&
		!customerAnswerHasPlatformRiskGuaranteeTerms(answer) {
		return true
	}
	if customerScenarioIsPlatformAntiBan(routerOutput, decisionText) ||
		customerScenarioIsBulkRegisterAntiBan(routerOutput, decisionText) ||
		customerScenarioIsVotingBoost(routerOutput, decisionText) ||
		customerScenarioIsClashShadowrocketVPN(routerOutput, decisionText) {
		return true
	}
	if customerScenarioIsPlatformLocationSelection(routerOutput, decisionText) &&
		customerAnswerHasPlatformLocationSelectionForbiddenTerms(answer) {
		return true
	}
	if customerScenarioIsLiveStreamingSelection(req, routerOutput, decisionText) &&
		customerAnswerHasLiveStreamingSelectionForbiddenTerms(answer) {
		return true
	}
	if customerScenarioIsPythonProxyIntegration(routerOutput, decisionText) &&
		customerAnswerHasPythonProxyIntegrationForbiddenTerms(answer) {
		return true
	}
	if customerScenarioIsStaticIPSwitch(req, routerOutput) &&
		customerAnswerHasStaticIPSwitchForbiddenTerms(answer) {
		return true
	}
	if customerScenarioIsOverseasAccessGoogle(routerOutput, decisionText) &&
		!customerAnswerHasOverseasAccessGoogleTerms(answer) {
		return true
	}
	if customerScenarioIsChatGPTDomesticOverseasIP(routerOutput, decisionText) &&
		!customerAnswerHasChatGPTDomesticOverseasIPTerms(answer) {
		return true
	}
	return false
}

func customerScenarioGuardProductLocked(req CustomerChatRequest, routerOutput *CustomerRouterOutput, result customerScenarioAnswerGuardResult) customerScenarioAnswerGuardResult {
	if !result.Triggered || result.Answer == "" || result.Blocked {
		return result
	}
	input := newCustomerScenarioGuardInput(req, routerOutput)
	if input.ProductLock == "" {
		return result
	}
	outputProducts := customerScenarioAnswerOutputProducts(result.Answer)
	result.OutputProducts = outputProducts
	allowed := customerScenarioAllowedProductsForResult(input, result)
	result.AllowedProducts = allowed
	if len(outputProducts) == 0 || customerScenarioProductsAllowed(outputProducts, allowed) {
		return result
	}
	result.Triggered = false
	result.Blocked = true
	result.BlockReason = "blocked_by_product_lock"
	result.Reason = firstNonEmpty(result.Reason, "blocked_by_product_lock")
	result.Answer = ""
	result.AnswerMode = ""
	result.ReviewRequired = false
	result.ReviewReason = ""
	result.FallbackSources = nil
	result.MinConfidence = 0
	result.MinEvidenceConfidence = 0
	return result
}

func customerScenarioAllowedProductsForResult(input customerScenarioGuardInput, result customerScenarioAnswerGuardResult) []string {
	allowed := map[string]bool{}
	add := func(product string) {
		product = strings.TrimSpace(product)
		if product != "" && product != "unknown" {
			allowed[product] = true
		}
	}
	add(input.ProductLock)
	if input.Router != nil {
		add(input.Router.Slots.PrimaryProduct)
		for _, product := range input.Router.Slots.Products {
			add(product)
		}
		if input.Router.Slots.IPType == "residential" {
			add("residential_ip")
		}
		if input.Router.Slots.IPType == "overseas" {
			add("overseas_ip")
		}
		if input.Router.Slots.StaticType == "dedicated" {
			add("dedicated_static_ip")
		}
		if input.Router.Slots.StaticType == "shared" {
			add("static_ip")
			add("shared_static_ip")
		}
	}
	for _, product := range result.AllowedProducts {
		add(product)
	}
	current := input.CurrentText
	intent := input.IntentText
	if customerRouterTextHasDynamicCue(current) || containsAny(intent, "dynamic_ip", "dynamic") {
		add("dynamic_ip")
	}
	if customerRouterTextHasOverseasCue(current) || containsAny(intent, "overseas_ip", "overseas") {
		add("overseas_ip")
	}
	if customerRouterTextHasResidentialCue(current) || containsAny(intent, "residential_ip", "residential") {
		add("residential_ip")
	}
	if containsAny(current, "独享") || containsAny(intent, "dedicated") {
		add("dedicated_static_ip")
		add("static_ip")
	}
	if containsAny(current, "共享") || containsAny(intent, "shared") {
		add("shared_static_ip")
		add("static_ip")
	}
	if customerScenarioResultAllowsProductExpansion(input, result) {
		for _, product := range customerScenarioAnswerOutputProducts(result.Answer) {
			add(product)
		}
	}
	out := make([]string, 0, len(allowed))
	for product := range allowed {
		out = append(out, product)
	}
	sort.Strings(out)
	return out
}

func customerScenarioResultAllowsProductExpansion(input customerScenarioGuardInput, result customerScenarioAnswerGuardResult) bool {
	if input.Router == nil {
		return false
	}
	if input.Router.Specialist != "product" && input.Router.Specialist != "pricing" {
		return false
	}
	if !customerScenarioGuardReasonCanCompareProducts(result.Reason) {
		return false
	}
	return customerScenarioGuardInputLooksLikeProductComparison(input)
}

func customerScenarioGuardReasonCanCompareProducts(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "ambiguous_dynamic_static_price_clarification",
		"datacenter_residential_price_compare_terms",
		"new_user_selection_neutralized",
		"new_user_selection_complete_boundary",
		"platform_location_selection_terms",
		"dynamic_static_compare_terms",
		"datacenter_residential_boundary",
		"shared_dedicated_selection_terms",
		"data_collection_selection_terms",
		"generic_purchase_product_clarification":
		return true
	default:
		return false
	}
}

func customerScenarioGuardInputLooksLikeProductComparison(input customerScenarioGuardInput) bool {
	if input.Router != nil && input.Router.QuestionStage == "product_selection" {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		input.CurrentText,
		input.IntentText,
	}, " ")))
	return containsAny(text,
		"product_selection",
		"selection",
		"compare",
		"vs",
		"对比",
		"区别",
		"差异",
		"怎么选",
		"选型",
		"选哪个",
		"应该买",
		"买哪个",
		"用哪",
		"哪种",
		"推荐",
	)
}

func customerScenarioAnswerOutputProducts(answer string) []string {
	text := normalizeCustomerReviewText(answer)
	products := map[string]bool{}
	if customerRouterTextHasDynamicCue(text) {
		products["dynamic_ip"] = true
	}
	if customerRouterTextHasOverseasCue(text) {
		products["overseas_ip"] = true
	}
	if customerRouterTextHasResidentialCue(text) {
		products["residential_ip"] = true
	}
	if customerRouterTextHasStaticCue(text) {
		products["static_ip"] = true
	}
	if containsAny(text, "独享静态", "独享ip", "独享 ip", "独享型") {
		products["dedicated_static_ip"] = true
		products["static_ip"] = true
	}
	if containsAny(text, "共享静态", "共享ip", "共享 ip", "共享型") {
		products["shared_static_ip"] = true
		products["static_ip"] = true
	}
	out := make([]string, 0, len(products))
	for product := range products {
		out = append(out, product)
	}
	sort.Strings(out)
	return out
}

func customerScenarioProductsAllowed(outputProducts []string, allowedProducts []string) bool {
	allowed := map[string]bool{}
	for _, product := range allowedProducts {
		allowed[strings.TrimSpace(product)] = true
	}
	for _, product := range outputProducts {
		product = strings.TrimSpace(product)
		if product == "" {
			continue
		}
		if allowed[product] {
			continue
		}
		if product == "shared_static_ip" && allowed["static_ip"] {
			continue
		}
		if product == "dedicated_static_ip" && allowed["static_ip"] {
			continue
		}
		return false
	}
	return true
}

func customerFallbackChatSourcesFromEvidence(sources []SourceRef) []customerChatSource {
	out := make([]customerChatSource, 0, len(sources))
	seen := map[string]bool{}
	for _, source := range sources {
		path := filepath.ToSlash(strings.TrimSpace(source.Path))
		if path == "" || seen[path] {
			continue
		}
		confidence := strings.ToLower(strings.TrimSpace(source.Confidence))
		switch confidence {
		case "low", "medium", "high":
		default:
			confidence = customerSourceConfidence(path)
		}
		out = append(out, customerChatSource{Path: path, Confidence: confidence})
		seen[path] = true
		if len(out) >= 2 {
			break
		}
	}
	return out
}

func (result customerScenarioAnswerGuardResult) Audit() map[string]any {
	out := map[string]any{
		"triggered": result.Triggered,
		"reason":    result.Reason,
		"action":    "review_only",
	}
	if result.Blocked {
		out["action"] = "blocked"
		out["blocked"] = true
		out["block_reason"] = result.BlockReason
	}
	if len(result.AllowedProducts) > 0 {
		out["allowed_products"] = result.AllowedProducts
	}
	if len(result.OutputProducts) > 0 {
		out["output_products"] = result.OutputProducts
	}
	return out
}

func customerScenarioGuardText(req CustomerChatRequest, routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil {
		return strings.ToLower(strings.TrimSpace(req.Question))
	}
	parts := []string{
		req.Question,
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.RoutingReason,
		routerOutput.HandoffNotes,
		routerOutput.Slots.PrimaryProduct,
		strings.Join(routerOutput.Slots.Products, " "),
		routerOutput.Slots.StaticType,
		routerOutput.Slots.IPType,
		routerOutput.Slots.Bandwidth,
		routerOutput.Slots.Quantity,
		routerOutput.Slots.Scenario,
	}
	return strings.ToLower(strings.TrimSpace(strings.Join(parts, " ")))
}

func customerScenarioIsGenericStaticIPPrice(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if routerOutput.Ambiguity.IsAmbiguous || routerOutput.AnswerStrategy == "ask_clarification" {
		return false
	}
	question := strings.TrimSpace(req.Question)
	compactQuestion := strings.ToLower(strings.ReplaceAll(question, " ", ""))
	if compactQuestion != "静态ip怎么卖？" && compactQuestion != "静态ip怎么卖?" {
		return false
	}
	if !customerRouterTextHasStaticCue(text) || !customerRouterLooksPriceQuestion(text) {
		return false
	}
	if strings.TrimSpace(parsedStaticType(routerOutput)) != "" || strings.TrimSpace(routerOutput.Slots.Bandwidth) != "" || strings.TrimSpace(routerOutput.Slots.Quantity) != "" {
		return false
	}
	if containsAny(text, "5m", "10m", "20m", "50个", "100个", "批量", "优惠", "折扣", "海外") {
		return false
	}
	return true
}

func parsedStaticType(routerOutput *CustomerRouterOutput) string {
	if routerOutput == nil {
		return ""
	}
	return strings.TrimSpace(routerOutput.Slots.StaticType)
}

func customerRouterTextHasOverseasCue(text string) bool {
	return containsAny(text, "海外ip", "海外 ip", "海外代理", "海外节点", "海外")
}

func customerAnswerHasGenericStaticIPPriceTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	if containsAny(text, "10m 30", "20m 70", "批量折扣", "最终成交价", "官网原价") {
		return false
	}
	return strings.Contains(text, "共享型") &&
		strings.Contains(text, "独享型") &&
		strings.Contains(text, "25") &&
		strings.Contains(text, "300") &&
		strings.Contains(text, "元/个/月")
}

type customerStaticBandwidthPrice struct {
	Shared    string
	Dedicated string
}

func customerScenarioIsStaticIPBandwidthPrice(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if strings.TrimSpace(routerOutput.Slots.Quantity) != "" || containsAny(text, "50个", "50 个", "100个", "100 个", "批量", "优惠", "折扣") {
		return false
	}
	return customerRouterTextHasStaticCue(text) &&
		customerRouterLooksPriceQuestion(text) &&
		customerScenarioStaticBandwidth(routerOutput, text) != ""
}

func customerScenarioStaticBandwidth(routerOutput *CustomerRouterOutput, text string) string {
	if routerOutput != nil {
		switch strings.ToLower(strings.TrimSpace(routerOutput.Slots.Bandwidth)) {
		case "5m":
			return "5M"
		case "10m":
			return "10M"
		case "20m":
			return "20M"
		}
	}
	compact := normalizeCustomerScenarioCompactText(text)
	switch {
	case strings.Contains(compact, "5m"):
		return "5M"
	case strings.Contains(compact, "10m"):
		return "10M"
	case strings.Contains(compact, "20m"):
		return "20M"
	default:
		return ""
	}
}

func customerScenarioIsOverseasIPPrice(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	return customerRouterTextHasOverseasCue(text) && customerRouterLooksPriceQuestion(text)
}

func customerAnswerHasOverseasIPPriceBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "国家地区") &&
		strings.Contains(text, "购买时长") &&
		strings.Contains(text, "不能直接给固定价格")
}

func customerScenarioIsAmbiguousDynamicStaticPrice(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "pricing" {
		return false
	}
	if routerOutput.AnswerStrategy != "ask_clarification" || !routerOutput.Ambiguity.IsAmbiguous {
		return false
	}
	return customerRouterTextHasDynamicCue(text) &&
		customerRouterTextHasStaticCue(text) &&
		customerRouterLooksPriceQuestion(text)
}

func customerAnswerHasDynamicStaticPriceClarification(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") && strings.Contains(text, "静态ip")
}

func customerScenarioIsDynamicStaticCompare(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	if routerOutput.Slots.IPType == "residential" || strings.Contains(routerOutput.Intent, "residential") {
		return false
	}
	intent := normalizeCustomerReviewText(routerOutput.Intent)
	rewrite := normalizeCustomerReviewText(routerOutput.RewrittenQuestion)
	return containsAny(intent, "dynamic_static_compare", "dynamic_vs_static") ||
		containsAny(rewrite, "动态和静态", "动态ip和静态ip", "动态 ip和静态 ip")
}

func customerAnswerHasDynamicStaticCompareTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "静态ip") &&
		strings.Contains(text, "相对固定") &&
		containsAny(text, "频繁换", "频繁切换")
}

func customerScenarioIsProxyIPConcept(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	questionText := normalizeCustomerScenarioCompactText(req.Question)
	if containsAny(questionText, "代理ip是啥", "代理ip是什么", "什么是代理ip") {
		return true
	}
	return routerOutput.Intent == "proxy_ip_definition_inquiry" ||
		containsAny(normalizeCustomerScenarioCompactText(text), "代理ip是啥", "代理ip是什么", "什么是代理ip")
}

func customerAnswerHasProxyIPConceptTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return strings.Contains(compact, "出口ip") && strings.Contains(text, "目标网站")
}

func customerScenarioIsDatacenterResidentialCompare(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	intentText := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " ")))
	return customerRouterTextHasResidentialCue(intentText) &&
		containsAny(intentText, "数据中心", "机房", "datacenter") &&
		containsAny(intentText, "区别", "对比", "差异", "compare")
}

func customerAnswerHasDatacenterResidentialBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "数据中心") &&
		strings.Contains(text, "家庭宽带") &&
		strings.Contains(text, "同城轮换") &&
		containsAny(text, "平台结果", "平台规则", "平台风控", "账号行为")
}

func customerScenarioIsLiveStreamingSelection(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	questionText := normalizeCustomerScenarioCompactText(req.Question)
	if (strings.Contains(questionText, "直播") || strings.Contains(questionText, "开播")) && strings.Contains(questionText, "ip") {
		return true
	}
	return routerOutput.Intent == "live_streaming_ip_selection" ||
		(containsAny(text, "直播", "开播") && containsAny(text, "用哪", "选", "ip"))
}

func customerAnswerHasLiveStreamingSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return (strings.Contains(text, "静态ip") || strings.Contains(text, "住宅独享")) &&
		strings.Contains(text, "10m") &&
		strings.Contains(text, "测试")
}

func customerAnswerHasLiveStreamingSelectionForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "保证直播画质", "稳定不卡顿", "一定不断线", "不能承诺")
}

func customerScenarioIsPlatformLocationSelection(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	if customerScenarioIsLongTermAccountSelection(routerOutput, text) {
		return false
	}
	intentText := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.Platform,
	}, " ")))
	if !customerRouterMentionsDomesticPlatform(intentText) || customerPlatformLocationLooksTroubleshooting("", CustomerRouterOutput{
		Intent:            intentText,
		RewrittenQuestion: intentText,
		RoutingReason:     intentText,
		HandoffNotes:      intentText,
	}) {
		return false
	}
	return customerRouterMentionsIPLocationChange(intentText) &&
		containsAny(intentText, "怎么选", "应该买", "买哪个", "选型", "用哪", "哪种", "推荐", "selection")
}

func customerAnswerHasPlatformLocationSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "静态ip") &&
		strings.Contains(text, "数据中心静态ip") &&
		strings.Contains(text, "住宅ip") &&
		containsAny(text, "平台ip库", "ip库") &&
		containsAny(text, "延迟", "影响")
}

func customerAnswerHasPlatformLocationSelectionForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "不能保证", "不能承诺")
}

func customerScenarioIsLongTermAccountSelection(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.Scenario,
	}, " "))
	return strings.Contains(intentText, "长期账号")
}

func customerAnswerHasLongTermAccountSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "静态ip") &&
		strings.Contains(text, "相对固定") &&
		containsAny(text, "不能承诺", "不能保证")
}

func customerScenarioIsSharedDedicatedSelection(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	if !customerRouterLooksSharedDedicatedCompare(current) {
		return false
	}
	return customerRouterLooksSharedDedicatedCompare(text) && containsAny(text, "怎么选", "选哪个", "选择", "适合", "区别")
}

func customerAnswerHasSharedDedicatedSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "共享型") &&
		strings.Contains(text, "独享型") &&
		containsAny(text, "带宽独享", "完全独享", "独立带宽")
}

func normalizeCustomerScenarioCompactText(text string) string {
	return strings.ReplaceAll(normalizeCustomerReviewText(text), " ", "")
}

func customerScenarioIsNewUserSelection(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" || routerOutput.QuestionStage != "product_selection" {
		return false
	}
	return strings.Contains(text, "新手") || strings.Contains(text, "第一次") || strings.Contains(text, "首次")
}

func customerNewUserSelectionFallbackSources() []customerChatSource {
	return []customerChatSource{
		{Path: "wiki/knowledge/si-ye-tian-proxy-ip-products.md", Confidence: "high"},
		{Path: "wiki/comparisons/dynamic-vs-static-ip.md", Confidence: "high"},
		{Path: "wiki/knowledge/si-ye-tian-overseas-ip.md", Confidence: "medium"},
	}
}

func customerAnswerHasNewUserSelectionBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "静态ip") &&
		strings.Contains(text, "海外ip") &&
		strings.Contains(text, "使用环境")
}

func customerScenarioIsDataCollectionSelection(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.Scenario,
	}, " "))
	return strings.Contains(intentText, "数据采集") &&
		(customerRouterTextHasDynamicCue(intentText) || customerRouterTextHasStaticCue(intentText) || containsAny(intentText, "选型", "用哪个", "一般用"))
}

func customerAnswerHasDataCollectionSelectionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "频繁换出口") &&
		strings.Contains(text, "静态ip")
}
