package service

import "testing"

func mustPricingTable(t *testing.T) CustomerPricingTable {
	t.Helper()
	table, err := DefaultCustomerPricingTable()
	if err != nil {
		t.Fatalf("load pricing table: %v", err)
	}
	return table
}

func lookupBand(t *testing.T, table CustomerPricingTable, product, bandwidth string, qty int) CustomerPricingBand {
	t.Helper()
	band, ok := table.lookupBand(product, bandwidth, qty)
	if !ok {
		t.Fatalf("missing band %s %s qty=%d", product, bandwidth, qty)
	}
	return band
}

func TestParseCustomerQuantityAndNamedPrice(t *testing.T) {
	if got := ParseCustomerQuantity("5M要15条"); got != 15 {
		t.Fatalf("quantity=%d", got)
	}
	if got := ParseCustomerQuantity("15"); got != 15 {
		t.Fatalf("bare quantity=%d", got)
	}
	if got := ParseCustomerQuantity("我想20元一条每月"); got != 0 {
		t.Fatalf("yuan-per-line must not parse as quantity, got %d", got)
	}
	if got := ParseCustomerNamedPrice("我想20元一条每月"); got != 20 {
		t.Fatalf("named=%d", got)
	}
	if got := ParseCustomerNamedPrice("我想20"); got != 20 {
		t.Fatalf("named shorthand=%d", got)
	}
	if got := ParseCustomerQuantity("15个"); got != 15 {
		t.Fatalf("quantity 个=%d", got)
	}
}

func TestNormalizeCustomerBandwidthIgnoresNonBandwidthText(t *testing.T) {
	if got := normalizeCustomerBandwidth("太贵了能便宜点吗"); got != "" {
		t.Fatalf("discount text must not become bandwidth, got %q", got)
	}
	if got := normalizeCustomerBandwidth("静态IP 5M要15条"); got != "5M" {
		t.Fatalf("bandwidth=%q", got)
	}
}

func TestLoadCustomerPricingTableReadsFile(t *testing.T) {
	table, err := LoadCustomerPricingTable("testdata/customer_pricing_table.v1.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if table.Version != customerPricingTableVersion {
		t.Fatalf("version=%q", table.Version)
	}
}

func TestCustomerPricingTableMatchesWikiRanges(t *testing.T) {
	table := mustPricingTable(t)
	cases := []struct {
		product    string
		bandwidth  string
		qty        int
		low, high  int
	}{
		{customerQuoteProductStaticShared, "5M", 10, 20, 25},
		{customerQuoteProductStaticShared, "5M", 15, 18, 23},
		{customerQuoteProductStaticShared, "5M", 40, 16, 20},
		{customerQuoteProductStaticShared, "5M", 80, 14, 18},
		{customerQuoteProductStaticShared, "10M", 15, 20, 25},
		{customerQuoteProductStaticShared, "20M", 15, 50, 60},
		{customerQuoteProductStaticShared, "20M", 501, 35, 37},
		{customerQuoteProductResidentialShared, "5M", 10, 25, 30},
		{customerQuoteProductResidentialShared, "10M", 15, 40, 45},
		{customerQuoteProductResidentialDedicated, "5M", 10, 40, 45},
		{customerQuoteProductResidentialDedicated, "10M", 40, 50, 60},
		{customerQuoteProductResidentialDedicated, "20M", 8, 90, 100},
	}
	for _, tc := range cases {
		band := lookupBand(t, table, tc.product, tc.bandwidth, tc.qty)
		if band.Low != tc.low || band.High != tc.high {
			t.Fatalf("%s %s qty=%d want %d-%d, got %d-%d", tc.product, tc.bandwidth, tc.qty, tc.low, tc.high, band.Low, band.High)
		}
	}
}

func TestResolveCustomerQuoteFirstQuoteUsesHigh(t *testing.T) {
	table := mustPricingTable(t)
	got := ResolveCustomerQuote(table, CustomerQuoteInput{
		Product: "static_shared", Bandwidth: "5M", Quantity: 15,
	})
	if got.Status != customerQuoteStatusQuoted || got.UnitPrice != got.HighPrice || !got.CanGoLower {
		t.Fatalf("%+v", got)
	}
	if got.UnitPrice != 23 {
		t.Fatalf("5M 15 first quote want 23, got %+v", got)
	}
}

func TestResolveCustomerQuoteNamedPriceAtOrAboveFloor(t *testing.T) {
	table := mustPricingTable(t)
	band := lookupBand(t, table, "static_shared", "5M", 15)
	got := ResolveCustomerQuote(table, CustomerQuoteInput{
		Product: "static_shared", Bandwidth: "5M", Quantity: 15,
		LastQuoted: band.High, NamedPrice: band.Low, WantsDiscount: true,
	})
	if got.UnitPrice != band.Low || got.Status != customerQuoteStatusQuoted {
		t.Fatalf("want named floor, got %+v", got)
	}
}

func TestResolveCustomerQuoteBelowFloorStaysAtFloor(t *testing.T) {
	table := mustPricingTable(t)
	band := lookupBand(t, table, "static_shared", "5M", 15)
	got := ResolveCustomerQuote(table, CustomerQuoteInput{
		Product: "static_shared", Bandwidth: "5M", Quantity: 15,
		LastQuoted: band.Low, NamedPrice: 10, WantsDiscount: true,
	})
	if got.Status != customerQuoteStatusBelowFloor || got.UnitPrice != band.Low || got.CanGoLower {
		t.Fatalf("%+v", got)
	}
}

func TestResolveCustomerQuoteMissingSlots(t *testing.T) {
	got := ResolveCustomerQuote(mustPricingTable(t), CustomerQuoteInput{Product: "static_shared"})
	if got.Status != customerQuoteStatusMissingSlots || len(got.Missing) == 0 {
		t.Fatalf("%+v", got)
	}
}

func TestResolveCustomerQuoteDiscountWithoutNamedGoesToFloor(t *testing.T) {
	table := mustPricingTable(t)
	band := lookupBand(t, table, "static_shared", "10M", 15)
	got := ResolveCustomerQuote(table, CustomerQuoteInput{
		Product: "static_shared", Bandwidth: "10M", Quantity: 15,
		LastQuoted: band.High, WantsDiscount: true,
	})
	if got.UnitPrice != band.Low || got.Status != customerQuoteStatusQuoted {
		t.Fatalf("%+v", got)
	}
}
