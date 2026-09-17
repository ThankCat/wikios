package service

import "strings"


func customerScenarioIsLongTermResidentialFixed(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "product" {
		return false
	}
	return customerRouterTextHasResidentialCue(text) && containsAny(text, "长效", "固定", "是不是固定", "是否固定", "完全固定")
}

func customerAnswerHasLongTermResidentialBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "住宅静态ip") &&
		strings.Contains(text, "不承诺完全固定") &&
		strings.Contains(text, "同一城市范围") &&
		!strings.Contains(text, "绝对固定")
}

func customerScenarioIsGenericIPChangeClarification(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if routerOutput.AnswerStrategy == "ask_clarification" &&
		containsAny(intent, "ip_change_capability", "general_ip_switch", "ip_switch_product_clarification") {
		return true
	}
	if !containsAny(intent, "ip_change", "ip_switch", "proxy_ip_switch", "switch_capability", "change_capability") {
		return false
	}
	if routerOutput.Slots.PrimaryProduct != "" && routerOutput.Slots.PrimaryProduct != "unknown" {
		return false
	}
	return containsAny(text, "哪类产品", "未说明产品类型") &&
		containsAny(text, "改 ip", "改ip", "换 ip", "换ip", "切换 ip", "切换ip", "更换出口 ip", "更换出口ip")
}

func customerAnswerHasGenericIPChangeClarification(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return strings.Contains(text, "哪类产品") &&
		strings.Contains(compact, "动态ip") &&
		strings.Contains(compact, "静态ip")
}

func customerScenarioIsProxyExitIPCapability(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	current := normalizeCustomerReviewText(req.Question)
	if containsAny(current, "不是要切换", "不是切换", "不是要换", "不是换") && containsAny(current, "出口ip", "出口 ip", "电脑") {
		return true
	}
	if containsAny(intentText, "proxy_exit_ip_capability", "出口ip", "出口 ip") &&
		containsAny(intentText, "可以", "能", "能否", "能不能", "支持", "改变", "修改", "改") {
		return true
	}
	return false
}

func customerAnswerHasProxyExitIPCapabilityTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return containsAny(text, "可以", "能") &&
		strings.Contains(compact, "代理出口ip") &&
		containsAny(compact, "目标网站看到", "对外呈现") &&
		containsAny(compact, "本地网络", "本地公网", "本机", "内网ip")
}

func customerScenarioIsVagueOperationQuestion(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "reception" {
		return false
	}
	return containsAny(text, "这个怎么弄", "这个怎么操作", "这个怎么办")
}

func customerAnswerHasVagueOperationClarification(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "哪个产品") && strings.Contains(text, "操作") &&
		!containsAny(text, "动态ip", "静态ip", "价格", "api")
}

func customerScenarioIsReceptionUnknownQuestion(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "reception" {
		return false
	}
	return containsAny(text, "不知道问啥", "不知道该问啥", "不知道咨询什么", "随便看看", "不会描述")
}

func customerAnswerHasReceptionDirections(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return strings.Contains(compact, "换ip") && strings.Contains(compact, "购买") && strings.Contains(compact, "配置") && strings.Contains(compact, "售后")
}

func customerScenarioIsResidentialPurchase(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	if containsAny(current, "数据中心", "机房") {
		return false
	}
	if containsAny(text, "优惠", "折扣", "报价", "多少钱", "价格") {
		return false
	}
	if routerOutput.Slots.IPType == "residential" && containsAny(current, "买", "购买", "入口", "下单", "开通", "这个怎么买") {
		return true
	}
	return customerRouterTextHasResidentialCue(current) && containsAny(current, "购买", "怎么买", "入口", "下单", "开通")
}

func customerAnswerHasResidentialPurchaseEntry(answer string) bool {
	text := strings.ToLower(strings.TrimSpace(answer))
	return strings.Contains(text, "https://www.siyetian.com/product/box.html") &&
		strings.Contains(text, "当前页面") &&
		strings.Contains(text, "购买")
}

func customerScenarioIsGenericPurchaseQuestion(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	if containsAny(intentText, "purchased_resource", "购买后", "买完", "买了", "查看", "在哪里看", "在哪看", "资源", "套餐") {
		return false
	}
	return containsAny(intentText, "generic_purchase", "purchase_inquiry", "怎么购买", "怎么买", "如何购买", "购买入口", "下单入口") &&
		!containsAny(intentText, "动态", "静态", "住宅", "海外", "测试", "下载", "手机", "app")
}

func customerAnswerHasGenericPurchaseClarification(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "动态ip") &&
		strings.Contains(text, "静态ip") &&
		strings.Contains(text, "住宅ip") &&
		strings.Contains(text, "海外ip") &&
		!strings.Contains(text, "https://www.siyetian.com/product.html")
}

func customerScenarioIsMobileAppDownload(req CustomerChatRequest, routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	return containsAny(current, "手机软件", "手机app", "手机 app", "app") && containsAny(current, "下载", "怎么用", "先去哪")
}

func customerAnswerHasMobileAppDownloadTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "app store") &&
		strings.Contains(text, "安卓") &&
		strings.Contains(text, "登录")
}

func customerScenarioIsTrialClaim(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	return customerRouterLooksTrialClaim(text)
}

func customerAnswerHasTrialClaimEntry(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "https://www.siyetian.com/test/index.html") &&
		strings.Contains(text, "注册") &&
		strings.Contains(text, "认证")
}

func customerScenarioIsPurchasedResourceView(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	if customerScenarioIsResidentialPurchaseSpecs(routerOutput, text) {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	if containsAny(intentText, "没有显示", "没显示", "未显示", "套餐没有", "没有套餐", "没套餐", "没有权益", "没权益") {
		return false
	}
	return customerRouterLooksPurchasedResourceView(intentText)
}

func customerAnswerHasPurchasedResourceViewTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "个人中心") &&
		strings.Contains(text, "对应产品后台") &&
		containsAny(text, "没有显示", "未显示") &&
		containsAny(text, "订单状态", "开通状态", "重新登录", "刷新")
}

func customerAnswerHasPurchasedResourceViewForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text,
		"排查流程",
		"付款后没ip",
		"刷新或重新登录后仍未显示",
		"刷新个人中心",
		"准备订单信息",
		"联系人工核查",
		"联系人工客服核查",
		"直接访问以下链接",
		"https://www.siyetian.com/member/",
	)
}

func customerScenarioIsResidentialPurchaseSpecs(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "purchase" {
		return false
	}
	if routerOutput.Slots.IPType != "residential" && !strings.Contains(routerOutput.Intent, "residential") {
		return false
	}
	if !strings.Contains(routerOutput.Intent, "spec") && !strings.Contains(routerOutput.Intent, "规格") {
		return false
	}
	return containsAny(text, "规格", "带宽", "城市", "套餐选项", "可选项") &&
		containsAny(text, "页面", "哪里看", "在哪看", "查看", "展示", "购买")
}

func customerAnswerHasResidentialPurchaseSpecs(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "规格") &&
		strings.Contains(text, "页面") &&
		containsAny(text, "可选城市", "带宽", "套餐选项", "当前可选") &&
		containsAny(text, "以当前页面为准", "以页面为准")
}

func customerScenarioIsPurchasedResourceMissing(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil {
		return false
	}
	if routerOutput.Specialist != "troubleshooting" && routerOutput.Specialist != "purchase" {
		return false
	}
	if customerScenarioIsTrialPackageMissing(routerOutput, text) {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
	}, " "))
	return containsAny(intentText, "没有显示", "没显示", "未显示", "看不到", "没有ip", "没有 ip", "没ip", "没 ip", "套餐没有", "没有套餐", "没套餐")
}

func customerAnswerHasPurchasedResourceMissingSteps(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "刷新") &&
		strings.Contains(text, "重新登录") &&
		containsAny(text, "个人中心", "产品后台", "对应产品后台") &&
		containsAny(text, "订单状态", "开通状态")
}

func customerScenarioIsTrialPackageMissing(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return customerRouterLooksTrialPackageMissing(text)
}

func customerAnswerHasTrialPackageMissingSteps(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "刷新") && strings.Contains(text, "重新登录") && strings.Contains(text, "实名认证") &&
		containsAny(text, "测试入口", "个人中心", "权益状态")
}

func customerScenarioIsBasicConnectionTroubleshooting(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return containsAny(text, "代理连不上", "连不上怎么办") && !containsAny(text, "海外", "503", "407")
}

func customerAnswerHasBasicConnectionTroubleshootingTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "代理地址") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "账号密码") &&
		strings.Contains(text, "白名单") &&
		strings.Contains(text, "错误码") &&
		!containsAny(text, "防火墙安全软件", "防火墙", "安全软件")
}

func customerScenarioIsHTTP503Troubleshooting(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return strings.Contains(text, "503")
}

func customerAnswerHasHTTP503TroubleshootingTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "协议") &&
		strings.Contains(text, "ip") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "认证") &&
		strings.Contains(text, "目标网站")
}

func customerScenarioIsOverseasConnectionTimeout(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return customerRouterTextHasOverseasCue(text) && containsAny(text, "连接超时", "超时")
}

func customerAnswerHasOverseasConnectionTimeoutTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "海外网络环境") &&
		strings.Contains(text, "代理地址") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "有效期")
}

func customerScenarioIsStaticIPTroubleshootingFollowup(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.PrimaryProduct,
		strings.Join(routerOutput.Slots.Products, " "),
	}, " "))
	if containsAny(intentText, "代理连不上", "连不上怎么办") {
		return false
	}
	return customerRouterTextHasStaticCue(intentText) && containsAny(intentText, "不能用", "连不上")
}

func customerAnswerHasStaticIPTroubleshootingTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "静态ip") &&
		strings.Contains(text, "协议") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "https://www.ip138.com/")
}

func customerScenarioIsIPNotChanged(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	if routerOutput.Slots.PrimaryProduct != "" &&
		routerOutput.Slots.PrimaryProduct != "unknown" &&
		routerOutput.Slots.PrimaryProduct != "dynamic_ip" {
		return false
	}
	return containsAny(text, "ip没变", "ip 没变", "ip不变", "ip 不变", "出口没变", "出口不变")
}

func customerAnswerHasIPNotChangedSteps(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "https://www.ip138.com/") &&
		strings.Contains(text, "https://www.ipip.net/") &&
		strings.Contains(text, "重新提取") &&
		strings.Contains(text, "浏览器")
}

func customerScenarioIsOverseasIPSwitchUnsupported(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil {
		return false
	}
	decisionText := normalizeCustomerReviewText(text)
	intentText := normalizeCustomerReviewText(strings.Join([]string{
		routerOutput.Intent,
		routerOutput.UserGoal,
		routerOutput.RewrittenQuestion,
		routerOutput.Slots.PrimaryProduct,
		strings.Join(routerOutput.Slots.Products, " "),
		routerOutput.Slots.IPType,
	}, " "))
	combinedText := normalizeCustomerReviewText(strings.Join([]string{decisionText, intentText}, " "))
	if !customerRouterTextHasOverseasCue(combinedText) &&
		routerOutput.Slots.PrimaryProduct != "overseas_ip" &&
		!customerRouterListContains(routerOutput.Slots.Products, "overseas_ip") &&
		routerOutput.Slots.IPType != "overseas" {
		return false
	}
	return containsAny(combinedText, "切换ip", "切换 ip", "换ip", "换 ip", "更换ip", "更换 ip", "切换出口", "更换出口", "switch")
}

func customerAnswerHasOverseasIPSwitchUnsupportedTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "海外ip") &&
		strings.Contains(text, "不支持切换ip") &&
		!containsAny(text, "手动切换", "重新分配", "切换按钮", "重新提取", "断开重连")
}

func customerScenarioIsPlatformDisplayIssue(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "troubleshooting" {
		return false
	}
	return customerRouterMentionsDomesticPlatform(text) && customerPlatformLocationLooksTroubleshooting("", CustomerRouterOutput{
		Intent:            text,
		RewrittenQuestion: text,
	})
}

func customerAnswerHasPlatformDisplayBoundary(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	return strings.Contains(compact, "设备出口ip") && strings.Contains(compact, "平台ip库") &&
		strings.Contains(compact, "缓存") && containsAny(compact, "显示可能会有延迟", "显示可能有延迟", "可能有延迟", "受平台ip库影响")
}

func customerScenarioIsWhitelistSetup(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	if customerScenarioIsAPIExtraction(req, routerOutput) {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	return intent == "whitelist_configuration_inquiry" ||
		containsAny(intent, "whitelist") ||
		containsAny(current, "白名单怎么设置", "白名单设置", "设置白名单")
}

func customerAnswerHasWhitelistSetupTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	compact := strings.ReplaceAll(text, " ", "")
	if strings.Contains(text, "https://www.siyetian.com/member/whitelist.html") &&
		strings.Contains(text, "出口公网ip") {
		return true
	}
	return strings.Contains(compact, "白名单") &&
		containsAny(compact, "当前出口ip", "出口公网ip", "当前服务器", "当前电脑") &&
		containsAny(text, "添加", "填进去", "填入") &&
		strings.Contains(text, "保存") &&
		containsAny(text, "重新连接", "重新测试", "连接代理测试")
}

func customerScenarioIsAPIExtraction(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if strings.Contains(intent, "whitelist") {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	return customerRouterLooksAPIExtraction(current) || containsAny(intent, "api_extraction", "api_inquiry")
}

func customerAnswerHasAPIExtractionTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "https://www.siyetian.com/apis.html") &&
		strings.Contains(text, "账号密码认证") &&
		strings.Contains(text, "白名单")
}

func customerScenarioIsAccountPasswordAuth(req CustomerChatRequest, routerOutput *CustomerRouterOutput) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	intent := strings.ToLower(strings.TrimSpace(routerOutput.Intent))
	if strings.Contains(intent, "whitelist") || customerScenarioIsAPIExtraction(req, routerOutput) {
		return false
	}
	current := normalizeCustomerReviewText(req.Question)
	return containsAny(intent, "account_password", "password_auth") ||
		containsAny(current, "账号密码认证", "开启账号密码", "账号密码怎么", "账号密码如何")
}

func customerAnswerHasAccountPasswordAuthTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "api页面") &&
		strings.Contains(text, "代理地址") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "账号") &&
		strings.Contains(text, "密码")
}

func customerScenarioIsPythonProxyIntegration(routerOutput *CustomerRouterOutput, text string) bool {
	if routerOutput == nil || routerOutput.Specialist != "technical" {
		return false
	}
	return strings.Contains(text, "python") && containsAny(text, "接入", "配置", "怎么用")
}

func customerAnswerHasPythonProxyIntegrationTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return strings.Contains(text, "协议") &&
		strings.Contains(text, "主机") &&
		strings.Contains(text, "端口") &&
		strings.Contains(text, "账号") &&
		strings.Contains(text, "密码") &&
		strings.Contains(text, "https://www.siyetian.com/help/28/55.html")
}

func customerAnswerHasPythonProxyIntegrationForbiddenTerms(answer string) bool {
	text := normalizeCustomerReviewText(answer)
	return containsAny(text, "token示例", "默认端口")
}

