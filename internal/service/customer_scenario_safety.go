package service

import "strings"

func customerScenarioIsFingerprintBrowserConfig(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	return containsAny(text, "指纹浏览器", "浏览器") && containsAny(text, "代理怎么填", "怎么填", "配置")
}

func customerAnswerHasFingerprintBrowserConfigTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "代理协议") &&
		strings.Contains(text, "主机") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "认证信息")
}

func customerScenarioHasStaticIPProduct(routerOutput *CustomerRouterOutput) bool {
	return routerOutput != nil &&
		(routerOutput.Slots.PrimaryProduct == "static_ip" || customerRouterListContains(routerOutput.Slots.Products, "static_ip"))
}

func customerScenarioExplicitStaticSwitchText(text string) bool {
	return containsAny(text, "切换ip", "切换 ip", "换ip", "换 ip", "手动切换", "更换ip", "更换 ip")
}

func customerScenarioExplicitStaticRegionSwitchText(text string) bool {
	return containsAny(text, "换地区", "换城市", "切换地区", "切换城市", "更换地区", "更换城市")
}

func customerScenarioIsStaticIPSwitch(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if !customerScenarioHasStaticIPProduct(routerOutput) {
		return false
	}
	if customerScenarioIsStaticIPSpecifyAddress(req, routerOutput) || customerScenarioIsStaticIPRegionSwitch(req, routerOutput) {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if containsAny(intent, "static_ip_switch", "static_ip_change", "ip_switch") && !containsAny(intent, "usage", "configuration", "troubleshooting") {
		return true
	}
	return customerScenarioExplicitStaticSwitchText(normalizeCustomerReviewText(req.Question))
}

func customerAnswerHasStaticIPSwitchTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "手动切换") &&
		strings.Contains(text, "每月5次") &&
		strings.Contains(text, "https://www.siyetian.com/member/staticip.html")
}

func customerAnswerHasStaticIPSwitchForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "指定某个具体ip", "指定某个具体 ip")
}

func customerScenarioIsStaticIPSpecifyAddress(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if !customerScenarioHasStaticIPProduct(routerOutput) {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if strings.Contains(intent, "region") {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	return containsAny(intent, "specify_ip", "specified_ip") ||
		containsAny(current, "指定某一个ip", "指定某一个 ip", "指定某个ip", "指定某个 ip", "指定ip", "指定 ip")
}

func customerAnswerHasStaticIPSpecifyAddressTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "指定到某一个具体ip", "指定某一个具体ip", "指定具体ip", "指定到某个具体ip") &&
		containsAny(text, "不能承诺", "不能保证", "以后台当前可用资源为准", "以后台可用资源为准")
}

func customerScenarioIsStaticIPRegionSwitch(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if !customerScenarioHasStaticIPProduct(routerOutput) {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if strings.Contains(intent, "specify_ip") && !strings.Contains(intent, "region") {
		return false
	}
	if containsAny(intent, "region_switch", "region_change", "city_switch", "city_change") {
		return true
	}
	return customerScenarioExplicitStaticRegionSwitchText(normalizeCustomerReviewText(req.Question))
}

func customerAnswerHasStaticIPRegionSwitchTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "地区") &&
		containsAny(text, "后台可用资源", "后台可选项", "页面当前")
}

func customerScenarioIsSubnetGatewayConfig(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if containsAny(text, "什么是", "是什么", "是啥") {
		return false
	}
	return containsAny(text, "子网掩码", "网关") && containsAny(text, "怎么填", "如何填", "配置", "设置", "填什么")
}

func customerAnswerHasSubnetGatewayConfigTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "网络配置") &&
		strings.Contains(text, "代理协议") &&
		strings.Contains(text, "地址") &&
		strings.Contains(text, "端口")
}

func customerScenarioIsAppDynamicMobile(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	if customerRequestClientChannel(req) == "mobile_app" {
		return false
	}
	if routerOutput.Slots.PrimaryProduct == "static_ip" || customerRouterListContains(routerOutput.Slots.Products, "static_ip") {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	current := normalizeCustomerReviewText(req.Question)
	return (containsAny(intent, "dynamic") || customerRouterTextHasDynamicCue(current)) &&
		containsAny(current, "手机", "app", "移动端")
}

func customerAnswerHasAppDynamicMobileTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "手机端") &&
		strings.Contains(text, "app") &&
		strings.Contains(text, "当前页面")
}

func customerScenarioIsBalanceWithdraw(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return strings.Contains(intentText, "余额") && containsAny(intentText, "提现", "提出来", "退回", "withdraw")
}

func customerAnswerHasBalanceWithdrawTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "平台内购买") &&
		strings.Contains(text, "一般不支持直接提现") &&
		containsAny(text, "页面", "规则说明", "当前规则")
}

func customerScenarioIsRechargeNotReceived(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return containsAny(routerOutput.Intent, "balance_not_received", "recharge_not_received", "payment_not_received") ||
		(containsAny(intentText, "充值", "余额", "支付") && containsAny(intentText, "没到账", "未到账", "没有到账", "未增加"))
}

func customerAnswerHasRechargeNotReceivedTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "支付是否成功") &&
		strings.Contains(text, "个人中心") &&
		strings.Contains(text, "刷新") &&
		containsAny(text, "支付记录", "订单状态")
}

func customerScenarioIsPaymentMethod(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	compact := normalizeCustomerScenarioCompactText(text)
	if containsAny(intent, "package_change", "plan_change", "refund", "invoice", "renewal", "upgrade", "balance_not_received", "recharge_not_received", "payment_not_received", "account_verification") {
		return false
	}
	if containsAny(intent, "payment_method", "payment_options", "corporate_payment") {
		return true
	}
	if containsAny(compact, "加微信", "有没有微信", "有微信吗", "微信客服", "企业微信", "企微", "微信联系", "微信沟通", "联系方式", "联系电话", "客服微信") {
		return false
	}
	currentText := normalizeCustomerScenarioCompactText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return (containsAny(currentText, "支付方式", "怎么支付", "如何支付", "微信支付", "微信买", "微信付款", "微信付", "支付宝", "对公", "打款", "付款方式", "付款") &&
		containsAny(compact, "支持", "可以", "能", "支付", "付款", "对公", "打款"))
}

func customerAnswerHasPaymentMethodTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "微信支付") &&
		strings.Contains(text, "支付宝") &&
		strings.Contains(text, "https://www.siyetian.com/member/recharge.html")
}

func customerScenarioIsAccountVerification(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return containsAny(intentText, "实名", "实名认证", "不实名", "verification")
}

func customerAnswerHasAccountVerificationTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "实名认证") &&
		strings.Contains(text, "https://www.siyetian.com/authent/index.html") &&
		strings.Contains(text, "企业认证")
}

func customerScenarioIsExpiredStaticRenewal(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.PrimaryProduct,
		strings.Join(routerOutput.Slots.Products, " "),
	}, " "))
	return customerRouterTextHasStaticCue(intentText) && containsAny(intentText, "到期", "过期", "expired") && containsAny(intentText, "续费", "续回", "renewal")
}

func customerAnswerHasExpiredStaticRenewalTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "到期前") &&
		strings.Contains(text, "可能被释放") &&
		(strings.Contains(text, "后台资源状态") || strings.Contains(text, "续费入口") || strings.Contains(text, "重新购买"))
}

func customerAnswerHasExpiredStaticRenewalTermsForRequest(req CustomerChatRequest, decisionText string, answer string) bool {
	text := normalizeCustomerReviewText(answer)
	if customerExpiredStaticRenewalNeedsActionPath(req, decisionText) {
		current := normalizeCustomerReviewText(req.Question)
		if containsAny(current, "还能续", "还可以续", "能续吗", "续回") {
			return containsAny(text, "能不能续", "主要看", "是否还显示续费入口") &&
				containsAny(text, "续费入口", "member/staticip", "member/jingtai") &&
				strings.Contains(text, "重新购买")
		}
		return containsAny(text, "续费入口", "member/staticip", "member/jingtai", "重新购买") &&
			!strings.Contains(text, "到期前")
	}
	return customerAnswerHasExpiredStaticRenewalTerms(answer)
}

func customerExpiredStaticRenewalAnswer(req CustomerChatRequest, text string) string {
	if customerExpiredStaticRenewalNeedsActionPath(req, text) {
		current := normalizeCustomerReviewText(req.Question)
		if containsAny(current, "还能续", "还可以续", "能续吗", "续回") {
			return "能不能续主要看个人中心是否还显示续费入口。您可以到 https://www.siyetian.com/member/staticip.html 或 https://www.siyetian.com/member/jingtai.html 查看；有入口就按页面续费，没有入口或原 IP 已释放时，通常需要重新分配或重新购买。"
		}
		return "已经过期的静态 IP 可以先到个人中心对应产品页查看是否还有续费入口： https://www.siyetian.com/member/staticip.html 或 https://www.siyetian.com/member/jingtai.html 。如果页面还能续，就按页面续费；如果原 IP 已释放或页面不再显示续费入口，可能需要重新分配或重新购买。"
	}
	return "到期前续费更有利于保留原 IP；如果已经过期，IP 可能被释放或需要重新分配。能否保留要以当前后台资源状态为准。"
}

func customerExpiredStaticRenewalNeedsActionPath(req CustomerChatRequest, text string) bool {
	current := normalizeCustomerReviewText(req.Question)
	return containsAny(current, "已经过期", "过期了", "还能续", "还可以续", "能续吗", "续回") ||
		(containsAny(text, "已经过期", "过期了") && containsAny(text, "还能续", "还可以续", "能续吗", "续回"))
}

func customerScenarioIsPackageChange(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	if containsAny(routerOutput.Intent, "package_change", "package_change_policy", "change_request", "plan_change", "套餐") {
		return true
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return strings.Contains(intentText, "买错套餐") || strings.Contains(intentText, "换套餐") || strings.Contains(intentText, "调整方案") || strings.Contains(intentText, "多退少补")
}

func customerAnswerHasPackageChangeTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "订单状态") &&
		containsAny(text, "调整方案", "多退少补", "换套餐")
}

func customerAnswerIsClarificationOnly(parsed customerChatLLMOutput, answer string) bool {
	if normalizedAnswerMode(parsed.AnswerMode) == "clarification" {
		return true
	}
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "请问") && strings.Contains(text, "哪") && !containsAny(text, "订单状态", "当前资源")
}

func customerAnswerHasForbiddenPackageChangePhrase(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "一定能换", "一定能退差价", "不能直接承诺")
}

func customerScenarioIsBandwidthUpgrade(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return containsAny(intentText, "升级带宽", "带宽升级", "升带宽", "bandwidth_upgrade") ||
		(strings.Contains(intentText, "5m") && strings.Contains(intentText, "10m") && containsAny(intentText, "换", "升级"))
}

func customerAnswerHasBandwidthUpgradeTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "产品类型") &&
		strings.Contains(text, "补差价") &&
		containsAny(text, "当前资源", "订单状态")
}

func customerScenarioIsRefund(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "billing_after_sales" {
		return false
	}
	return customerRouterListContains(routerOutput.RiskFlags, "refund") || routerOutput.UserIntentSignals.RefundStrong || strings.Contains(text, "退款") || strings.Contains(text, "退费")
}

func customerAnswerAsksForOrderInfo(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "订单号", "订单信息", "购买的产品信息", "支付截图", "提供具体", "为您核实", "人工为您核实", "核实订单", "订单情况")
}

func customerAnswerHasForbiddenRefundPhrase(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "不能直接承诺可以退款", "不能承诺可以退款", "不能直接承诺")
}

func customerScenarioIsLegalProxySafety(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return containsAny(text, "合法吗", "合不合法", "代理ip合法", "代理 ip合法", "代理ip 合法", "是否违法", "违法吗")
}

func customerAnswerHasLegalProxySafetyTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "工具") &&
		strings.Contains(text, "合法合规") &&
		strings.Contains(text, "不能简单承诺零风险")
}

func customerScenarioIsOverseasAccessGoogle(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return customerRouterTextHasOverseasCue(text) && strings.Contains(text, "google")
}

func customerAnswerHasOverseasAccessGoogleTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能承诺") &&
		strings.Contains(text, "海外网络环境") &&
		strings.Contains(text, "目标站点策略")
}

func customerScenarioIsChatGPTDomesticOverseasIP(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "chatgpt") && customerRouterTextHasOverseasCue(text) && containsAny(text, "国内", "本地")
}

func customerAnswerHasChatGPTDomesticOverseasIPTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能承诺") &&
		strings.Contains(text, "chatgpt") &&
		strings.Contains(text, "平台策略") &&
		strings.Contains(text, "代理检测")
}

func customerScenarioIsPlatformAntiBan(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	if customerScenarioIsPlatformRiskGuarantee(routerOutput, text) {
		return false
	}
	return containsAny(text, "防封", "绕检测", "规避风控", "过风控") && containsAny(text, "抖音", "平台", "账号", "ip")
}

func customerScenarioIsPlatformRiskGuarantee(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	if containsAny(text, "正常用途", "合法用途", "合规用途") {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if containsAny(text, "绕过平台检测", "绕检测", "规避风控", "规避平台", "绕风控", "过风控") {
		return false
	}
	return containsAny(intent, "platform_risk_guarantee") ||
		(containsAny(text, "保证", "能不能", "能否", "不会被", "不被") && containsAny(text, "风控", "平台", "账号结果"))
}

func customerScenarioIsNormalUseAfterPlatformRisk(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return containsAny(text, "正常用途", "合法用途", "合规用途")
}

func customerAnswerHasNormalUseBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "合法合规") &&
		containsAny(text, "固定出口", "白名单", "数据采集", "连接排障") &&
		containsAny(text, "不能承诺", "不承诺")
}

func customerAnswerHasPlatformRiskGuaranteeTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能承诺") &&
		strings.Contains(text, "风控") &&
		containsAny(text, "账号结果", "账号行为", "平台规则")
}

func customerAnswerHasPlatformAntiBanTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能提供") &&
		strings.Contains(text, "风控") &&
		strings.Contains(text, "防封")
}

func customerScenarioIsVotingBoost(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "投票") || strings.Contains(text, "刷量")
}

func customerAnswerHasVotingBoostTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能提供") &&
		strings.Contains(text, "投票") &&
		strings.Contains(text, "刷量")
}

func customerScenarioIsBulkRegisterAntiBan(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "批量注册") && containsAny(text, "防封", "风控", "检测")
}

func customerAnswerHasBulkRegisterAntiBanTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能提供") &&
		strings.Contains(text, "批量注册") &&
		strings.Contains(text, "防封")
}

func customerScenarioIsXiaohongshuRiskAvoidance(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "小红书") && containsAny(text, "避免被风控", "风控", "账号结果")
}

func customerAnswerHasXiaohongshuRiskAvoidanceTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能承诺") &&
		strings.Contains(text, "风控") &&
		strings.Contains(text, "账号结果")
}

func customerScenarioIsClashShadowrocketVPN(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return containsAny(text, "clash", "小火箭", "vpn", "机场节点")
}

func customerAnswerHasClashShadowrocketVPNTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "不能提供") &&
		strings.Contains(text, "clash") &&
		strings.Contains(text, "小火箭") &&
		strings.Contains(text, "vpn")
}

func customerAnswerHasClashShadowrocketVPNForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "订阅链接", "节点", "替代工具", "配置步骤")
}

func customerScenarioIsTunnelIPSec(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "safety" {
		return false
	}
	return strings.Contains(text, "隧道") || strings.Contains(text, "ipsec")
}

func customerAnswerHasTunnelIPSecBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") && strings.Contains(text, "静态ip") &&
		strings.Contains(text, "api") && strings.Contains(text, "隧道") &&
		strings.Contains(text, "http") && strings.Contains(text, "socks5") &&
		strings.Contains(text, "ipsec") && containsAny(text, "不作为", "不能", "不支持")
}
