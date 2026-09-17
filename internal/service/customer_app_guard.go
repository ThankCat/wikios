package service

import "strings"

type customerAppGuardResult struct {
	Triggered bool
	Answer    string
	Hits      []string
	Reason    string
}

func customerMobileAppChannelPolicyPrompt() string {
	return strings.Join([]string{
		"- 当前请求来自手机 App 渠道，客户只能看到 App 已报备、App 内可操作的内容。",
		"- 只回答手机 App 内安装、登录、购买、连接、VPN 权限、IP 未变化、归属地延迟、配置、设置和使用排障。",
		"- App 渠道可见产品只有共享静态 IP 和住宅 IP；客户说“静态 IP”时默认按共享静态 IP 回答。",
		"- 不输出动态 IP、海外 IP、独享静态 IP 的教程、价格、购买入口、销售引导或配置步骤。",
		"- 不输出电脑端配置、API、白名单、SOCKS5、代理地址端口、代码、第三方代理软件教程。",
		"- 如果客户问到 App 渠道外的产品或技术，必须明确说“手机 App 不支持/当前 App 端不支持”，不要顾左右而言他；随后给 App 内可用替代路径。",
		"- 不支持说明要短而直接，例如“手机 App 不支持 API 提取”，然后说明 App 内可选择共享静态 IP 或住宅 IP、连接、确认系统 VPN 权限、重新打开目标 App 检查出口 IP/归属地。",
	}, "\n")
}

func customerAppPolicyAudit(enabled bool) map[string]any {
	return map[string]any{
		"enabled":          enabled,
		"client_channel":   "mobile_app",
		"allowed_products": []string{"shared_static_ip", "residential_ip"},
		"scope":            "mobile_app_operable_answer",
	}
}

func customerAppGuardAnswer(req CustomerChatRequest, answer string) customerAppGuardResult {
	answer = strings.TrimSpace(answer)
	hits := customerAppGuardForbiddenHits(answer)
	if len(hits) == 0 {
		return customerAppGuardResult{Triggered: false, Answer: answer, Hits: nil, Reason: "pass"}
	}
	return customerAppGuardResult{
		Triggered: true,
		Answer:    customerMobileAppFallbackAnswer(req),
		Hits:      hits,
		Reason:    "forbidden_app_channel_content",
	}
}

func (r customerAppGuardResult) Audit() map[string]any {
	return map[string]any{
		"triggered": r.Triggered,
		"reason":    r.Reason,
		"hits":      r.Hits,
		"action":    "audit_only",
	}
}

func customerAppGuardForbiddenHits(answer string) []string {
	text := normalizeCustomerReviewText(answer)
	if text == "" {
		return nil
	}
	hits := []string{}
	check := func(name string, markers ...string) {
		if containsAny(text, markers...) {
			hits = appendUniqueString(hits, name)
		}
	}
	check("dynamic_ip", "动态ip", "动态代理", "动态套餐")
	check("overseas_ip", "海外ip", "海外代理", "跨境", "google", "chatgpt")
	check("dedicated_static_ip", "独享静态", "独享ip", "独享 ip", "独享带宽")
	check("desktop_config", "windows", "macos", "电脑端", "pc端", "浏览器代理", "系统代理")
	check("api", "api", "接口提取", "提取链接", "提取ip", "提取 ip")
	check("whitelist", "白名单", "加白", "授权ip", "授权 ip")
	check("socks5", "socks5", "sock5", "代理端口", "代理地址", "端口号")
	check("code_or_tools", "python", "curl", "代码", "postern", "sstap", "clash", "第三方代理")
	return hits
}

func customerMobileAppFallbackAnswer(req CustomerChatRequest) string {
	text := normalizeCustomerReviewText(req.Question)
	switch {
	case containsAny(text, "api", "接口", "提取"):
		return "手机 App 不支持 API 提取。App 内可以直接选择共享静态 IP 或住宅 IP 后连接，并确认系统 VPN 权限已开启。"
	case containsAny(text, "socks5", "sock5", "端口", "代理地址", "白名单", "加白", "授权ip", "授权 ip"):
		return "手机 App 不支持 SOCKS5、代理地址端口或白名单这类电脑端/API 配置。App 内可以直接选择共享静态 IP 或住宅 IP 后连接，并确认系统 VPN 权限已开启。"
	case containsAny(text, "动态"):
		return "手机 App 当前不支持动态 IP。App 内可以使用共享静态 IP 或住宅 IP，选择对应 IP 后连接即可。"
	case containsAny(text, "海外", "google", "chatgpt"):
		return "手机 App 当前不支持海外 IP。App 内可以使用共享静态 IP 或住宅 IP，选择对应 IP 后连接即可。"
	case containsAny(text, "独享"):
		return "手机 App 当前不支持独享静态 IP。App 内的静态 IP 按共享静态 IP 处理，也可以选择住宅 IP 后连接。"
	case containsAny(text, "电脑", "windows", "mac", "pc"):
		return "手机 App 不支持电脑端配置方式。请在手机 App 内选择共享静态 IP 或住宅 IP 后连接，并确认系统 VPN 权限已开启。"
	}
	if containsAny(text, "没变", "不变", "不显示", "不准", "不准确", "归属地", "定位", "城市") {
		return "手机 App 内可以使用共享静态 IP 或住宅 IP。请先在 App 内选择对应 IP 并连接，确认系统 VPN 权限已开启；如果目标 App 里归属地暂时没变化，先重启目标 App 或等待平台 IP 库刷新后再查看。"
	}
	if containsAny(text, "购买", "怎么买", "开通", "价格", "多少钱", "套餐") {
		return "手机 App 内当前可选择共享静态 IP 或住宅 IP。请在 App 的产品/套餐页面选择需要的类型后按页面提示开通，静态 IP 在 App 场景默认按共享静态 IP 处理。"
	}
	if containsAny(text, "住宅") {
		return "手机 App 内可以使用住宅 IP。请在 App 内选择住宅 IP 后连接，并确认系统 VPN 权限已开启，再打开目标 App 查看当前出口 IP 或归属地。"
	}
	return "手机 App 内可以使用共享静态 IP 或住宅 IP。请在 App 内选择对应 IP 后连接，并确认系统 VPN 权限已开启，再打开目标 App 查看当前出口 IP 或归属地。"
}
