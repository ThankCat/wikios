package service

import "testing"

func TestNormalizePublicAnswerOutputKeepsValidPriceAdjustmentIntent(t *testing.T) {
	parsed := normalizePublicAnswerOutput(publicAnswerLLMOutput{
		UserIntent: &PublicUserIntent{
			Type: "申请优惠",
			PriceInfo: &PublicPriceInfo{
				ExpectedPrice:            "90元/个",
				ProductType:              "住宅IP",
				ProductBandwidth:         5,
				IntendedPurchaseQuantity: 10,
				BoxUsageTime:             30,
			},
		},
	})

	if parsed.UserIntent == nil || parsed.UserIntent.Type != "price_adjustment" {
		t.Fatalf("expected price_adjustment intent, got %#v", parsed.UserIntent)
	}
	price := parsed.UserIntent.PriceInfo
	if price == nil {
		t.Fatal("expected price_info")
	}
	if price.ExpectedPrice != "90元/个" || price.ProductType != "box" || price.ProductBandwidth != 5 || price.IntendedPurchaseQuantity != 10 {
		t.Fatalf("unexpected normalized price_info: %#v", price)
	}
	if price.BoxUsageTime != 0 || price.BoxUsageQuantityMin != 0 || price.BoxUsageQuantityMax != 0 {
		t.Fatalf("expected static/box-only fields to clear dynamic package fields, got %#v", price)
	}
}

func TestNormalizePublicAnswerOutputDropsInvalidPriceAdjustmentIntent(t *testing.T) {
	cases := []struct {
		name string
		info *PublicPriceInfo
	}{
		{
			name: "unknown product type",
			info: &PublicPriceInfo{ExpectedPrice: "90元/个", ProductType: "mobile", ProductBandwidth: 5, IntendedPurchaseQuantity: 10},
		},
		{
			name: "missing static quantity",
			info: &PublicPriceInfo{ExpectedPrice: "90元/个", ProductType: "static", ProductBandwidth: 5},
		},
		{
			name: "invalid dynamic package",
			info: &PublicPriceInfo{ExpectedPrice: "100元", ProductType: "dynamic"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := normalizePublicAnswerOutput(publicAnswerLLMOutput{
				UserIntent: &PublicUserIntent{Type: "price_adjustment", PriceInfo: tc.info},
			})
			if parsed.UserIntent != nil {
				t.Fatalf("expected invalid intent to be dropped, got %#v", parsed.UserIntent)
			}
		})
	}
}

func TestNormalizePublicAnswerOutputNormalizesDynamicAndSwitchIntent(t *testing.T) {
	dynamic := normalizePublicAnswerOutput(publicAnswerLLMOutput{
		UserIntent: &PublicUserIntent{
			Type: "price_adjustment",
			PriceInfo: &PublicPriceInfo{
				ExpectedPrice:            "300元/30天",
				ProductType:              "dynamic",
				ProductBandwidth:         20,
				BoxUsageTime:             30,
				BoxUsageQuantityMin:      100,
				BoxUsageQuantityMax:      99,
				IntendedPurchaseQuantity: 5,
			},
		},
	})
	if dynamic.UserIntent == nil || dynamic.UserIntent.PriceInfo == nil {
		t.Fatalf("expected dynamic price intent, got %#v", dynamic.UserIntent)
	}
	price := dynamic.UserIntent.PriceInfo
	if price.ProductBandwidth != 0 || price.IntendedPurchaseQuantity != 0 || price.BoxUsageTime != 30 || price.BoxUsageQuantityMin != 0 || price.BoxUsageQuantityMax != 0 {
		t.Fatalf("unexpected normalized dynamic price_info: %#v", price)
	}

	switched := normalizePublicAnswerOutput(publicAnswerLLMOutput{
		UserIntent: &PublicUserIntent{
			Type:      "切换IP",
			PriceInfo: &PublicPriceInfo{ExpectedPrice: "90元/个", ProductType: "static", ProductBandwidth: 5, IntendedPurchaseQuantity: 10},
		},
	})
	if switched.UserIntent == nil || switched.UserIntent.Type != "switch_ip" || switched.UserIntent.PriceInfo != nil {
		t.Fatalf("expected switch_ip without price_info, got %#v", switched.UserIntent)
	}
}

func TestPublicResponseUserIntentRequiresPurchaseDiscountAndEvidence(t *testing.T) {
	intent := &PublicUserIntent{
		Type: "price_adjustment",
		PriceInfo: &PublicPriceInfo{
			ExpectedPrice:            "90元/个",
			ProductType:              "static",
			ProductBandwidth:         5,
			IntendedPurchaseQuantity: 10,
		},
	}
	sources := []publicAnswerSource{{Path: "wiki/knowledge/static-ip.md", Confidence: "high"}}

	strongReq := PublicAnswerRequest{Question: "我想买10个5M静态IP，可以申请优惠吗？"}
	if got := publicResponseUserIntent(strongReq, intent, sources); got == nil || got.Type != "price_adjustment" {
		t.Fatalf("expected strong purchase discount request to keep intent, got %#v", got)
	}

	priceOnlyReq := PublicAnswerRequest{Question: "5M静态IP多少钱？"}
	if got := publicResponseUserIntent(priceOnlyReq, intent, sources); got != nil {
		t.Fatalf("expected ordinary price question to drop intent, got %#v", got)
	}

	noEvidenceReq := PublicAnswerRequest{Question: "我想买10个5M静态IP，可以申请优惠吗？"}
	if got := publicResponseUserIntent(noEvidenceReq, intent, nil); got != nil {
		t.Fatalf("expected missing evidence to drop price intent, got %#v", got)
	}
}

func TestPublicResponseUserIntentAllowsQualifiedPriceCalculation(t *testing.T) {
	intent := &PublicUserIntent{
		Type: "price_adjustment",
		PriceInfo: &PublicPriceInfo{
			ExpectedPrice:            "27元/个",
			ProductType:              "static",
			ProductBandwidth:         10,
			IntendedPurchaseQuantity: 20,
		},
	}
	sources := []publicAnswerSource{{Path: "wiki/knowledge/static-ip.md", Confidence: "high"}}
	req := PublicAnswerRequest{
		Question: "数据中心 10M 的吧，帮我算一下价格",
		History: []ChatMessage{
			{Role: "user", Content: "我有20个账号，每个账号需要不同的IP"},
			{Role: "assistant", Content: "可以按 20 个北京节点的共享型静态 IP 来看。"},
		},
	}

	if got := publicResponseUserIntent(req, intent, sources); got == nil || got.Type != "price_adjustment" {
		t.Fatalf("expected qualified price calculation to keep intent, got %#v", got)
	}
}

func TestPublicResponseUserIntentDropsMismatchedProductType(t *testing.T) {
	intent := &PublicUserIntent{
		Type: "price_adjustment",
		PriceInfo: &PublicPriceInfo{
			ExpectedPrice:            "90元/个",
			ProductType:              "box",
			ProductBandwidth:         5,
			IntendedPurchaseQuantity: 10,
		},
	}
	sources := []publicAnswerSource{{Path: "wiki/knowledge/static-ip.md", Confidence: "high"}}
	req := PublicAnswerRequest{Question: "我想买10个5M静态IP，可以申请优惠吗？"}

	if got := publicResponseUserIntent(req, intent, sources); got != nil {
		t.Fatalf("expected static IP question to drop residential IP price intent, got %#v", got)
	}
}

func TestPublicResponseUserIntentRequiresKnownProductType(t *testing.T) {
	intent := &PublicUserIntent{
		Type: "price_adjustment",
		PriceInfo: &PublicPriceInfo{
			ExpectedPrice:            "90元/个",
			ProductType:              "static",
			ProductBandwidth:         5,
			IntendedPurchaseQuantity: 10,
		},
	}
	sources := []publicAnswerSource{{Path: "wiki/knowledge/static-ip.md", Confidence: "high"}}
	req := PublicAnswerRequest{Question: "我想买10个5M，可以申请优惠吗？"}

	if got := publicResponseUserIntent(req, intent, sources); got != nil {
		t.Fatalf("expected unknown product type to drop price intent, got %#v", got)
	}
}

func TestPublicResponseUserIntentUsesHistoryForKnownProductType(t *testing.T) {
	intent := &PublicUserIntent{
		Type: "price_adjustment",
		PriceInfo: &PublicPriceInfo{
			ExpectedPrice:            "90元/个",
			ProductType:              "static",
			ProductBandwidth:         5,
			IntendedPurchaseQuantity: 10,
		},
	}
	sources := []publicAnswerSource{{Path: "wiki/knowledge/static-ip.md", Confidence: "high"}}
	req := PublicAnswerRequest{
		Question: "我想买10个5M，可以申请优惠吗？",
		History:  []ChatMessage{{Role: "user", Content: "我看的是静态IP"}},
	}

	if got := publicResponseUserIntent(req, intent, sources); got == nil || got.Type != "price_adjustment" {
		t.Fatalf("expected history-confirmed product type to keep price intent, got %#v", got)
	}
}

func TestPublicResponseUserIntentUsesCurrentQuestionForProductType(t *testing.T) {
	intent := &PublicUserIntent{
		Type: "price_adjustment",
		PriceInfo: &PublicPriceInfo{
			ExpectedPrice:            "90元/个",
			ProductType:              "static",
			ProductBandwidth:         5,
			IntendedPurchaseQuantity: 10,
		},
	}
	sources := []publicAnswerSource{{Path: "wiki/knowledge/static-ip.md", Confidence: "high"}}
	req := PublicAnswerRequest{
		Question: "我想买10个5M静态IP，可以申请优惠吗？",
		History:  []ChatMessage{{Role: "user", Content: "刚才看的是住宅IP"}},
	}

	if got := publicResponseUserIntent(req, intent, sources); got == nil || got.Type != "price_adjustment" {
		t.Fatalf("expected current static IP question to keep static price intent, got %#v", got)
	}
}

func TestPublicResponseUserIntentDropsMismatchedQuantityAndBandwidth(t *testing.T) {
	sources := []publicAnswerSource{{Path: "wiki/knowledge/static-ip.md", Confidence: "high"}}
	req := PublicAnswerRequest{Question: "我想买10个5M静态IP，可以申请优惠吗？"}

	wrongBandwidth := &PublicUserIntent{
		Type: "price_adjustment",
		PriceInfo: &PublicPriceInfo{
			ExpectedPrice:            "90元/个",
			ProductType:              "static",
			ProductBandwidth:         10,
			IntendedPurchaseQuantity: 10,
		},
	}
	if got := publicResponseUserIntent(req, wrongBandwidth, sources); got != nil {
		t.Fatalf("expected mismatched bandwidth to drop price intent, got %#v", got)
	}

	wrongQuantity := &PublicUserIntent{
		Type: "price_adjustment",
		PriceInfo: &PublicPriceInfo{
			ExpectedPrice:            "90元/个",
			ProductType:              "static",
			ProductBandwidth:         5,
			IntendedPurchaseQuantity: 20,
		},
	}
	if got := publicResponseUserIntent(req, wrongQuantity, sources); got != nil {
		t.Fatalf("expected mismatched quantity to drop price intent, got %#v", got)
	}
}

func TestSanitizePublicHumanHandoffAnswerRemovesProactiveHandoff(t *testing.T) {
	answer := "这个情况建议您联系人工客服进一步确认，也可以拨打客服电话 400-1080-106。"

	got, ok := sanitizePublicHumanHandoffAnswer(answer, "我的静态 IP 用不了，怎么办？")
	if !ok {
		t.Fatal("expected proactive human handoff to be sanitized")
	}
	if got == "" || got == answer {
		t.Fatalf("expected replacement answer, got %q", got)
	}
	if containsAny(normalizePublicIntentText(got), "人工客服", "客服电话", "联系人工") {
		t.Fatalf("expected sanitized answer not to mention human handoff, got %q", got)
	}
}

func TestSanitizePublicConversationMarkerAnswerRemovesContextTell(t *testing.T) {
	answer := "结合刚才聊到的游戏场景，数据中心 10M 更适合低延迟使用。"

	got, ok := sanitizePublicConversationMarkerAnswer(answer)
	if !ok {
		t.Fatal("expected context marker to be sanitized")
	}
	if containsAny(got, "刚才聊到", "结合") {
		t.Fatalf("expected context marker removed, got %q", got)
	}
}

func TestSanitizePublicTimeOfDayGreetingAnswerUsesNeutralGreeting(t *testing.T) {
	answer := "早上好呀！我是四叶天在线客服。今天主要是想了解套餐资费，还是使用中遇到了具体问题？"

	got, ok := sanitizePublicTimeOfDayGreetingAnswer(answer)
	if !ok {
		t.Fatal("expected time-of-day greeting to be sanitized")
	}
	if containsAny(got, "早上好", "上午好", "下午好", "晚上好") {
		t.Fatalf("expected neutral greeting, got %q", got)
	}
	if !containsAny(got, "你好") {
		t.Fatalf("expected greeting to remain natural, got %q", got)
	}
}

func TestFormatPublicBeijingTime(t *testing.T) {
	got := formatPublicBeijingTime("2026-05-11T08:58:00Z")
	if got != "2026-05-11 16:58:00 Asia/Shanghai" {
		t.Fatalf("unexpected Beijing time: %q", got)
	}
}

func TestSanitizePublicPricingWorkflowAnswerRemovesAutomaticDiscountClaim(t *testing.T) {
	answer := "基础总价是 600 元，后台会自动按采购数量匹配对应的折扣档位，最终金额以结算页为准。"

	got, ok := sanitizePublicPricingWorkflowAnswer(answer)
	if !ok {
		t.Fatal("expected automatic discount claim to be sanitized")
	}
	if containsAny(normalizePublicIntentText(got), "自动", "结算页", "后台") {
		t.Fatalf("expected automatic workflow claim removed, got %q", got)
	}
}

func TestSanitizePublicUnsupportedPricePromiseAnswerKeepsScope(t *testing.T) {
	answer := "数据采集通常需要动态 IP 来模拟真实访问。您预估每天大概需要提取多少条 IP？确认后我马上为您匹配合适的套餐与价格。"

	got, ok := sanitizePublicUnsupportedPricePromiseAnswer(answer)
	if !ok {
		t.Fatal("expected unsupported price promise to be sanitized")
	}
	normalized := normalizePublicIntentText(got)
	if containsAny(normalized, "匹配合适的套餐与价格", "匹配套餐与价格") {
		t.Fatalf("expected price promise removed, got %q", got)
	}
	if !containsAny(normalized, "判断更适合哪种套餐") {
		t.Fatalf("expected scoped package guidance, got %q", got)
	}
}

func TestSanitizePublicUnsupportedPricePromiseAnswerRemovesSavingsPromise(t *testing.T) {
	answer := "您平常主要跑数据采集还是做账号维护？大概每天需要提多少条？我好帮您算笔账看哪种更省。"

	got, ok := sanitizePublicUnsupportedPricePromiseAnswer(answer)
	if !ok {
		t.Fatal("expected savings promise to be sanitized")
	}
	normalized := normalizePublicIntentText(got)
	if containsAny(normalized, "算笔账", "哪种更省") {
		t.Fatalf("expected savings promise removed, got %q", got)
	}
	if !containsAny(normalized, "判断哪种计费方式更合适") {
		t.Fatalf("expected scoped billing guidance, got %q", got)
	}
}

func TestSanitizePublicHumanHandoffAnswerAllowsExplicitContactQuestion(t *testing.T) {
	answer := "客服电话是 400-1080-106。"

	if got, ok := sanitizePublicHumanHandoffAnswer(answer, "客服电话是多少？"); ok {
		t.Fatalf("expected explicit contact question to keep answer, got %q", got)
	}
}
