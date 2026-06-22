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

func TestCustomerSpecialistBasePromptCoversConciseAnswerPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_base.md"))
	if err != nil {
		t.Fatalf("read base prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"`conversation_context`",
		"Router 的 `history_summary`、`rewritten_question` 和 `handoff_notes` 只作为分诊参考",
		"不要重复追问 `conversation_context` 中客户已经明确回答过的信息",
		"简短优先",
		"能 1 句回答就不要写 2 句",
		"按问题复杂度控制长度",
		"简单问答、寒暄、拒答、边界说明、能不能、有没有、入口确认，默认 1 句",
		"普通配置或排障默认 2～3 条短步骤",
		"复杂配置、多条件排障或客户已排除多项时最多 4 条",
		"价格、套餐、状态边界、无证据澄清，默认 1～2 句",
		"不要为了完整把相邻场景也讲一遍",
		"用 2～4 条有序列表",
		"每条只说一个动作或一个判断",
		"不写“建议按以下步骤排查”这类空开场",
		"若客户询问入口、购买、切换、设置、领取、下载等操作路径",
		"应把与本轮产品和动作直接相关的 URL 当作核心答案优先给出",
		"不要为了简短省略入口，也不要列无关入口",
		"若证据明确给出多个同类入口，先选最直接匹配本轮动作的入口",
		"公共入口按页面入口表达，后台操作入口才说明以账号后台实际显示为准",
		"只有缺少该信息就无法回答本轮问题时，才追问客户",
		"产品不明时只在强依赖产品的高风险事项上硬停",
		"答复优先例外",
		"我应该买哪个/需要买哪个/选哪个/哪个适合",
		"`question_stage=product_selection`、`goal_consulting`、`operation_howto`、`troubleshooting`",
		"即使 `primary_product=unknown` 也不要自动触发产品不明硬停",
		"基于 `candidate_pages` 给出证据支持的通用能力、入口、字段、排查方向或可执行推荐",
		"不要再问“动态还是静态/哪类产品”",
		"`answer_mode=clarification`",
		"不要固定枚举",
		"优先结合客户原话",
		"`sources=[]`",
		"回答已经完成时就停",
		"删除不推进答案的尾巴和缓冲语",
		"以便进一步定位",
		"方便进一步排查",
		"不要追加“如有其他问题”",
		"不要补“合法场景/实际环境/平台策略/可继续提供方案”",
		"“请注意”“需要注意的是”",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected base prompt to include %q, got:\n%s", want, prompt)
		}
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
		"不要补“确定带宽和数量后可以核算月费”",
		"不解释带宽含义、速度影响或业务场景",
		"不要自动进入带宽推荐",
		"不要问“跑什么业务场景”",
		"明显错别字且上下文能确定含义",
		"不要显式解释",
		"只有客户明确问“哪个适合/怎么选”",
		"能给方向时不要先反问",
		"平台归属地选型",
		"不要反问“动态还是静态/住宅还是数据中心”",
		"不要在已经给出建议后追加“单设备/多账号/具体游戏”等扩展问题",
		"禁止把产品和“批量注册、防封、反爬、降低被封、过风控、绕检测、规避风控、养号、刷量”绑定成卖点",
		"只用“频繁更换出口”“固定出口”“地区/城市出口”“稳定连接”“长期固定账号环境”等中性描述",
		"如果客户明确要求绕风控、防封、批量注册、反爬或规避检测，不做产品推荐",
		"新手/第一次使用代理 IP 问“买哪个/选哪个”时",
		"海外平台场景再看海外 IP",
		"新手选型推荐句式",
		"`住宅 IP 可选 5M、10M、20M。`",
		"`游戏更建议先看静态 IP，稳定性要求高的话优先独享静态 IP；带宽可以从 10M 起看，实际体验还需要测试。`",
		"`如果需要频繁换出口，先看动态 IP；需要固定地区或长期账号环境，先看静态 IP；海外平台场景再看海外 IP，并先确认使用环境。`",
		"`改抖音 IP 归属地这类场景，更建议先看静态 IP；要相对稳定城市出口可看数据中心静态 IP，想更贴近家庭宽带场景可看住宅 IP。平台显示可能会有延迟，也会受平台 IP 库影响。`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected product prompt to include %q, got:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "可补一句“确定带宽和数量后可以核算月费”") {
		t.Fatalf("product prompt still allows sales quote tail:\n%s", prompt)
	}
}

func TestCustomerSpecialistPricingPromptCoversSpecListNoSalesTailPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_pricing.md"))
	if err != nil {
		t.Fatalf("read pricing prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"如果客户问“有哪些带宽/规格/档位”，只列可选项",
		"不要补“确定带宽和数量后可以核算月费”",
		"不要推荐带宽或追问业务场景",
		"如果客户问“按什么计费/怎么计费”，只说明计费维度",
		"不要说“我可以核算金额/立即核算具体金额”",
		"`router_output.slots.primary_product=unknown`",
		"不要猜动态 IP、静态 IP 或其它产品价格",
		"`动态 IP 主要按提取次数或使用时长计费。`",
		"静态 IP 已指定带宽但未指定共享/独享时",
		"同时给该带宽的数据中心共享型和数据中心独享型单价",
		"独享 IP/独享静态问价且客户未指定带宽时",
		"直接列出数据中心独享型 5M、10M、20M 三档月价",
		"10M 应回答数据中心共享型 30 元/个/月、数据中心独享型 500 元/个/月",
		"不要主动加入住宅 10M 价格",
		"没有数量时只能报单价或原价",
		"住宅 10M 这类只给了带宽、没给数量的问题",
		"独享型静态 IP 不参与数量折扣",
		"5M 300 元/个/月，10M 500 元/个/月，20M 800 元/个/月",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected pricing prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistTechnicalPromptCoversShortConfigNoFollowupPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_technical.md"))
	if err != nil {
		t.Fatalf("read technical prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"回答完不要再追问客户用什么工具或脚本",
		"也不要追问工具环境",
		"配置问题优先用 2～4 条有序步骤",
		"客户只问“白名单怎么设置”时，使用下方推荐句式原文",
		"不扩展设备绑定、数量限制、产品页面或后续操作",
		"有通用代理配置证据时，先给协议、代理地址、端口、认证信息这类通用顺序",
		"只有完全没有通用证据时才问产品类型",
		"客户已明确说海外 IP 时",
		"不要把静态 IP、住宅 IP 的手动切换、每月次数、重新分配或后台按钮规则套用到海外 IP",
		"产品不明确但有通用切换或选型证据时",
		"回答入口或字段后就停",
		"不追问工具、脚本、系统或设备",
		"手机 App 渠道问动态 IP、API、白名单、SOCKS5、电脑端配置时",
		"不要把系统代理或电脑端教程泛化到 App",
		"`白名单在 https://www.siyetian.com/member/whitelist.html 设置，把当前服务器或电脑的出口公网 IP 填进去并保存即可。`",
		"`SOCKS5 配置时协议选 SOCKS5，代理地址填实际获取的 IP，端口填页面给出的端口，认证信息按当前页面或工具字段填写。`",
		"`API 入口是 https://www.siyetian.com/apis.html ；使用前先开通账号密码认证，并把服务器出口公网 IP 加到白名单，再按接口返回的代理地址、端口和认证信息接入。`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected technical prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistTroubleshootingPromptCoversConciseNoInternalPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_troubleshooting.md"))
	if err != nil {
		t.Fatalf("read troubleshooting prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"不要说“资料库/知识库尚未收录”这类内部视角",
		"排障回答优先短句直答",
		"单点判断 1～2 句",
		"普通排障 2～3 条",
		"复杂排障最多 4 条",
		"不写“建议按以下步骤排查”",
		"只问 1 个最影响排查的问题",
		"如果 `router_output` 显示产品不明确且本轮排查结论会因产品不同而改变，先只问产品类型",
		"客户只问“代理连不上怎么办”且产品已明确时，使用下方推荐句式原文",
		"连接 IP 后某个 App/网站没网/打不开",
		"`https://www.baidu.com/`",
		"测试结果出来前不要提前归因",
		"不要写“通常是微信限制/目标侧限制”",
		"普通网站能打开而微信/单一目标打不开",
		"不要编造“部分产品/套餐对社交软件有特定限制”",
		"普通网站也打不开时，代理 IP、节点、线路或全局连接异常可能性上升",
		"客户已反馈普通网站或百度也打不开时",
		"用 1～2 句或最多 2 条短步骤",
		"不要追问错误码替代资源侧动作",
		"不要只列本地配置、防火墙、安全软件、DNS 或错误码追问",
		"普通网站也打不开推荐句式",
		"微信/单一 App 没网推荐句式",
		"连接异常不要默认只有客户配置问题",
		"必须承认代理 IP、节点、线路、带宽或资源状态本身也可能异常",
		"没有后台资源状态证据时不能断言一定是资源故障",
		"不要把主要原因默认归结为客户配置或目标网站限制",
		"多个目标是否都不通",
		"切换/重新分配静态 IP",
		"更换地区、线路、节点后再测",
		"不要继续重复已排除的配置检查",
		"先承认资源侧可能性，再给一个新的隔离动作",
		"可能性上升",
		"不要写“通常是资源侧导致”",
		"很可能是线路/节点异常",
		"不要把记录信息写成“发给我们/提交后台/为您核实/帮您排查”",
		"产品不明确时先问产品类型",
		"不要默认动态 IP",
		"即使产品不明确，也先给出口 IP 验证和是否走代理的通用检查",
		"IP 没变这类通用症状先给出口 IP 验证步骤，再问产品或工具",
		"不加防火墙、安全软件或错误码追问",
		"不要在排障步骤后写“发给我们进一步排查”“提供给客服核实”等索要信息或服务承诺",
		"未出现的 IP 查询网站",
		"`https://www.ip138.com/`、`https://www.ipip.net/` 或 `http://httpbin.org/ip`",
		"`407 一般是认证失败。核对账号密码、账号状态和白名单；刚改过配置就重新登录或重新获取认证信息后再试。`",
		"IP 没变推荐句式：`先确认请求确实走代理",
		"`先核对代理地址、端口、账号密码、协议和白名单；仍连不上，再看具体错误码。`",
		"`先连着代理打开 https://www.baidu.com/ 测试。百度能开、微信不能开，偏微信/目标侧限制；百度也打不开，再查 IP、节点或线路。`",
		"`这不是微信单点问题，IP、节点或线路也可能异常。先核对连接、账号端口和白名单；基础项没问题就换 IP、线路或节点再测。`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected troubleshooting prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistSafetyPromptCoversForbiddenIntentPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_safety.md"))
	if err != nil {
		t.Fatalf("read safety prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"敏感词、违禁词、黑灰产词汇试探",
		"翻墙、机场、Clash、小火箭、VPN 等违规跨境联网工具",
		"不要解释这个词是什么意思",
		"不要按关键词机械拒答",
		"先判断客户真实诉求",
		"若只是企业内网 VPN、普通网络配置或产品技术问题，不因单个词误拒",
		"用 1 句拒绝提供方法",
		"拒答场景不要补合规教育",
		"拒答只表达语义目标",
		"不要把 `response_goal`、示例或配置内容逐字当成客户可见文案",
		"内部信息回复目标",
		"违规用途回复目标",
		"违规跨境联网回复目标",
		"风控承诺回复目标",
		"问隧道 IP、IPSec 或把代理当隧道/VPN 使用时",
		"动态 IP、静态 IP 和 API 提取不等同于隧道代理",
		"IPSec 不作为支持能力承诺",
		"隧道/IPSec 回复目标",
		"拒答后不要继续销售产品",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected safety prompt to include %q, got:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "请按法律法规和平台规则合规使用") {
		t.Fatalf("safety refusal still contains verbose compliance education:\n%s", prompt)
	}
}

func TestCustomerSpecialistBillingPromptCoversRenewalNoFollowupPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_billing_after_sales.md"))
	if err != nil {
		t.Fatalf("read billing prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"客户只问“续费能不能保留 IP”且产品已明确时，先答保留边界",
		"如果客户已经补充“过期了/还能续吗”，不要复述上一轮原句",
		"要推进到后台续费入口、自查订单/资源状态和可能需要重新购买的边界",
		"买错套餐了能换吗/能不能换套餐",
		"先用推荐句式给订单状态边界",
		"产品不明确时先问产品类型",
		"不要套用静态 IP、动态 IP 或住宅 IP 的规则",
		"多轮里当前轮售后意图优先于历史支付上下文",
		"不要沿用上一轮“微信支付/支付宝/对公打款”的答案",
		"客户只问“可以退款吗”时，使用下方推荐句式原文",
		"不要补“准备订单信息/查询/评估退款方案/其它调整方式”等扩展话术",
		"客户问“有没有微信/加微信/企业微信/微信客服”",
		"这是联系方式问题，不是微信支付",
		"不要改答微信支付、支付宝或对公打款",
		"不要追加联系人工、准备订单信息、具体订单处理情况或其它办理建议",
		"只有缺该信息就无法回答时，才问 1 个必要信息",
		"不要在末尾加“准备订单信息”“详细核算”“为您处理”“联系人工客服确认具体订单”等扩展服务话术",
		"联系人工客服确认具体订单",
		"除退款外，也不要写“联系人工确认/人工核查/人工处理/人工核实/人工排查”",
		"`退款条件、金额和时效需要人工按订单状态确认；您可以联系人工客服处理。`",
		"`先确认支付是否成功，并在个人中心查看余额或订单状态；也可以刷新页面或重新登录后再看。支付多笔、充值未到账或苹果订单未到账，需要按支付记录和订单状态核实。`",
		"`到期前续费更有利于保留原 IP；如果已经过期，IP 可能被释放或需要重新分配。能否保留要以当前后台资源状态为准。`",
		"`已经过期的静态 IP 可以先到个人中心对应产品页查看是否还有续费入口： https://www.siyetian.com/member/staticip.html 或 https://www.siyetian.com/member/jingtai.html 。如果页面还能续，就按页面续费；如果原 IP 已释放或页面不再显示续费入口，可能需要重新分配或重新购买。`",
		"`买错套餐需要按订单状态确认是否能调整方案或多退少补。`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected billing prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistPurchasePromptCoversEntryMappingAndShortClarification(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_purchase.md"))
	if err != nil {
		t.Fatalf("read purchase prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"不要把动态 IP 入口当成通用购买入口",
		"`https://www.siyetian.com/product.html` 只作为动态 IP 购买入口",
		"`https://www.siyetian.com/staticip.html` 作为静态 IP 购买入口",
		"`https://www.siyetian.com/product/os.html` 作为海外 IP 购买入口",
		"海外 IP 需要海外网络环境或海外服务器环境",
		"产品不明确时只问“你买的是动态 IP 还是静态 IP/住宅 IP？”",
		"不要加“确认后我会...”或预演后续步骤",
		"不要追加试用、选型、场景或其它产品分流问题",
		"客户问“买完后在哪看 IP/购买后怎么看资源/付款后在哪看套餐”",
		"购买后可在个人中心或对应产品后台查看资源；如果没有显示，先刷新页面或重新登录，再以订单状态和对应产品后台当前开通状态为准。",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected purchase prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistReceptionPromptCoversShortNoServiceActionPolicy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_reception.md"))
	if err != nil {
		t.Fatalf("read reception prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"不要说“前台接待”“专家”“分诊”等内部角色",
		"只请客户简单说具体问题",
		"不要说“正在安排”“已为您转接”“分派给专家”",
		"不要主动附带电话、企业微信",
		"不要承诺“为您安排专家解答”",
		"也不要说“帮您安排”",
		"不要承诺“为您安排专家解答”，也不要说“帮您安排”“我好为您详细解答”“我再帮您确认”",
		"客户说“不知道问啥/不知道咨询什么/随便看看”时",
		"给 4 个可选咨询方向：换 IP、购买、配置、售后",
		"答完就停，不再追问客户想咨询什么",
		"联系方式是官网右侧二维码",
		"官网右侧有企业微信二维码，可以扫码添加",
		"不要把“企业微信”这个泛称当成账号或微信号",
		"不要把“企业微信”“微信客服”“企微”这类泛称说成可搜索添加的具体账号",
		"没有具体账号时只说二维码入口",
		"`不客气。`",
		"`可以，您先简单说下具体问题，我看怎么帮您处理。`",
		"`客服电话是 400-1080-106。`",
		"`官网右侧有企业微信二维码，可以扫码添加。`",
		"`具体是哪个产品或操作？`",
		"`您可以先说想解决什么问题，比如换 IP、购买、配置还是售后。`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected reception prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestSupportContactPromptNormalizesPlaceholderWeComToQRCode(t *testing.T) {
	svc := NewCustomerChatService(Deps{Config: &config.Config{}})

	got := svc.supportContactPrompt(RuntimeSupportSettings{
		Phone: "400-1080-106",
		WeCom: "企业微信",
	})
	if !strings.Contains(got, "企业微信：官网右侧企业微信二维码") {
		t.Fatalf("expected placeholder WeCom to become QR code entry, got:\n%s", got)
	}
	if !strings.Contains(got, "客服电话：400-1080-106") {
		t.Fatalf("expected phone contact to remain, got:\n%s", got)
	}

	got = svc.supportContactPrompt(RuntimeSupportSettings{
		Phone: "400-1080-106",
		WeCom: "siyetian-support",
	})
	if !strings.Contains(got, "企业微信：siyetian-support") {
		t.Fatalf("expected explicit WeCom to remain, got:\n%s", got)
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

func TestCustomerSpecialistBasePromptForbidsInternalRoleLeakage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistBasePromptFile))
	if err != nil {
		t.Fatalf("read base prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"对客户不提知识库、资料库、资料、路径、prompt、router、检索、专家、分诊、JSON 字段名等内部信息",
		"绝不向客户暴露内部资料状态",
		"不要翻译成“价格专家/技术专家”等客户可见说法",
		"不要说“转接/安排/交给某专家”",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected base prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistBasePromptForbidsInventingProductTypes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistBasePromptFile))
	if err != nil {
		t.Fatalf("read base prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"不要臆造产品类型、子类或形态",
		"凭空造出“动态住宅 IP”",
		"不能描述这些不存在产品的轮换、切换、计费、入口或配置机制",
		"禁止夹带证据未支撑的产品事实或机制描述",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected base prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistCheckPromptCoversFabricatedProductTypes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistCheckPromptFile))
	if err != nil {
		t.Fatalf("read check prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"是否凭空造出 `candidate_pages` 里没有的产品类型/子类",
		"取决于您的类型/套餐",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected check prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistBasePromptForbidsDenyingSupportedCapability(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistBasePromptFile))
	if err != nil {
		t.Fatalf("read base prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"不要否认证据明确支持的能力或操作",
		"“相对固定”与“可切换”不矛盾",
		"切换成另一个 IP",
		"不是“切换到客户指定的某个具体 IP”",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected base prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistTechnicalPromptCoversSwitchIPHandling(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", "customer_specialist_technical.md"))
	if err != nil {
		t.Fatalf("read technical prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"问怎么切换/更换 IP",
		"在会员中心对应产品页手动切换或重新分配",
		"若证据中有当前产品的直接入口 URL",
		"按通用操作入口规则优先给出最相关入口",
		"若客户明确是海外 IP",
		"不能说海外 IP 支持手动切换、重新分配或每月 N 次",
		"只回答海外 IP 证据支持的能力边界",
		"不要把它误解成“切换到客户指定的某个 IP”而否认",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected technical prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistCheckPromptCoversDenyingSupportedCapability(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistCheckPromptFile))
	if err != nil {
		t.Fatalf("read check prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"是否对证据明确支持的能力/操作",
		"用“相对固定”否定了可切换",
		"把“换成另一个 IP”误解成“切换到指定 IP”而否认",
		"客户已明确说海外 IP 时",
		"同样支持手动切换",
		"更换地区/线路或重新分配",
		"每月 5 次",
		"没有海外 IP 证据直接支持",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected check prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistCheckPromptCoversSingleAppConnectivityPrematureAttribution(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistCheckPromptFile))
	if err != nil {
		t.Fatalf("read check prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"客户说连接代理 IP 后只有微信、QQ、某个 App 或某个网站没网/打不开",
		"客户还没反馈普通网站测试结果",
		"通常是微信限制/目标侧限制",
		"其它提前归因",
		"`https://www.baidu.com/`",
		"普通公开网站做分流",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected check prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistCheckPromptCoversConciseFinalAnswerAudit(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistCheckPromptFile))
	if err != nil {
		t.Fatalf("read check prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"`answer` 是否能删掉开场、背景、重复结论、相邻场景、客套尾巴或无必要追问",
		"若能删，先删短",
		"简单问答默认 1 句",
		"普通配置/排障默认 2～3 条",
		"复杂步骤最多 4 条",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected check prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistCheckPromptCoversNormalSiteCannotOpenResourceAttribution(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistCheckPromptFile))
	if err != nil {
		t.Fatalf("read check prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"客户已反馈百度或普通公开网站也打不开",
		"只要求客户检查本地配置、防火墙、安全软件、DNS 或提供错误码",
		"代理 IP、节点、线路或全局连接异常可能性上升",
		"这不是微信/单一目标限制",
		"切换/重新提取/重新分配 IP",
		"换地区/线路/节点",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected check prompt to include %q, got:\n%s", want, prompt)
		}
	}
}

func TestCustomerSpecialistCheckPromptForbidsUnsupportedSocialAppProductLimit(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "llm", "prompts", customerSpecialistCheckPromptFile))
	if err != nil {
		t.Fatalf("read check prompt: %v", err)
	}
	prompt := string(raw)
	for _, want := range []string{
		"客户已反馈普通网站能打开",
		"微信/QQ/单一 App 或网站打不开",
		"部分产品/套餐对社交软件有特定限制",
		"本产品限制微信",
		"`candidate_pages` 未支持的产品侧限制",
		"只保留微信、平台或目标侧访问策略可能性",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected check prompt to include %q, got:\n%s", want, prompt)
		}
	}
}
