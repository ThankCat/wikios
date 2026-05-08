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
