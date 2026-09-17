package service

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed testdata/customer_pricing_table.v1.json
var customerPricingTableJSON []byte

func DefaultCustomerPricingTable() (CustomerPricingTable, error) {
	return LoadCustomerPricingTableBytes(customerPricingTableJSON)
}

func LoadCustomerPricingTable(path string) (CustomerPricingTable, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return CustomerPricingTable{}, err
	}
	return LoadCustomerPricingTableBytes(raw)
}

func LoadCustomerPricingTableBytes(raw []byte) (CustomerPricingTable, error) {
	var table CustomerPricingTable
	if err := json.Unmarshal(raw, &table); err != nil {
		return CustomerPricingTable{}, err
	}
	if strings.TrimSpace(table.Version) != customerPricingTableVersion {
		return CustomerPricingTable{}, fmt.Errorf("unsupported pricing table version %q", table.Version)
	}
	return table, nil
}

func ResolveCustomerQuote(table CustomerPricingTable, in CustomerQuoteInput) CustomerQuoteFacts {
	in.Bandwidth = normalizeCustomerBandwidth(in.Bandwidth)
	missing := make([]string, 0, 3)
	if strings.TrimSpace(in.Product) == "" {
		missing = append(missing, "product")
	}
	if strings.TrimSpace(in.Bandwidth) == "" {
		missing = append(missing, "bandwidth")
	}
	if in.Quantity <= 0 {
		missing = append(missing, "quantity")
	}
	if len(missing) > 0 {
		return CustomerQuoteFacts{
			Status:  customerQuoteStatusMissingSlots,
			Missing: missing,
			Product: in.Product,
			Speak:   "还缺" + strings.Join(quoteMissingLabels(missing), "、") + "，先问这些再报价。",
		}
	}
	band, ok := table.lookupBand(in.Product, in.Bandwidth, in.Quantity)
	if !ok {
		return CustomerQuoteFacts{
			Status:    customerQuoteStatusUnsupported,
			Product:   in.Product,
			Bandwidth: in.Bandwidth,
			Quantity:  in.Quantity,
			Speak:     "当前价格表没有这个规格，不要编数字。",
		}
	}
	unit := band.High
	status := customerQuoteStatusQuoted
	switch {
	case in.LastQuoted <= 0 && in.NamedPrice >= band.Low && in.NamedPrice <= band.High:
		unit = in.NamedPrice
	case in.LastQuoted <= 0:
		unit = band.High
	case in.NamedPrice > 0 && in.NamedPrice < band.Low:
		unit = band.Low
		status = customerQuoteStatusBelowFloor
	case in.NamedPrice >= band.Low && in.NamedPrice <= band.High:
		unit = in.NamedPrice
	case in.LastQuoted <= band.Low && (in.WantsDiscount || in.NamedPrice > 0):
		unit = band.Low
		if in.NamedPrice > 0 && in.NamedPrice < band.Low {
			status = customerQuoteStatusBelowFloor
		} else {
			status = customerQuoteStatusAtFloor
		}
	case in.WantsDiscount && in.LastQuoted > band.Low:
		unit = band.Low
	case in.LastQuoted >= band.Low && in.LastQuoted <= band.High:
		unit = in.LastQuoted
		if unit == band.Low {
			status = customerQuoteStatusAtFloor
		}
	}
	if unit < band.Low {
		unit = band.Low
	}
	if unit > band.High {
		unit = band.High
	}
	facts := CustomerQuoteFacts{
		Status:       status,
		Product:      in.Product,
		Bandwidth:    in.Bandwidth,
		Quantity:     in.Quantity,
		UnitPrice:    unit,
		FloorPrice:   band.Low,
		HighPrice:    band.High,
		MonthlyTotal: unit * in.Quantity,
		CanGoLower:   unit > band.Low,
	}
	facts.Speak = formatCustomerQuoteSpeak(facts)
	return facts
}

func (table CustomerPricingTable) lookupBand(product, bandwidth string, qty int) (CustomerPricingBand, bool) {
	product = strings.TrimSpace(product)
	bandwidth = normalizeCustomerBandwidth(bandwidth)
	for _, sku := range table.SKUs {
		if sku.Product != product || normalizeCustomerBandwidth(sku.Bandwidth) != bandwidth {
			continue
		}
		for _, band := range sku.Bands {
			if qty < band.MinQty {
				continue
			}
			if band.MaxQty > 0 && qty > band.MaxQty {
				continue
			}
			return band, true
		}
	}
	return CustomerPricingBand{}, false
}

func formatCustomerQuoteSpeak(facts CustomerQuoteFacts) string {
	label := customerQuoteProductLabel(facts.Product)
	switch facts.Status {
	case customerQuoteStatusAtFloor, customerQuoteStatusBelowFloor:
		return fmt.Sprintf("已经是底价 %d元/条/月，不能再低。本轮可说：%s %s %d条，%d元/条/月，月费%d元。",
			facts.FloorPrice, label, facts.Bandwidth, facts.Quantity, facts.UnitPrice, facts.MonthlyTotal)
	default:
		extra := "已经是可报价格。"
		if facts.CanGoLower {
			extra = "还可以再低。"
		}
		return fmt.Sprintf("本轮可说：%s %s %d条，%d元/条/月，月费%d元。%s",
			label, facts.Bandwidth, facts.Quantity, facts.UnitPrice, facts.MonthlyTotal, extra)
	}
}

func customerQuoteProductLabel(product string) string {
	switch product {
	case customerQuoteProductResidentialDedicated:
		return "住宅独享"
	case customerQuoteProductResidentialShared:
		return "住宅共享"
	default:
		return "静态 IP"
	}
}

func quoteMissingLabels(missing []string) []string {
	out := make([]string, 0, len(missing))
	for _, item := range missing {
		switch item {
		case "product":
			out = append(out, "产品类型")
		case "bandwidth":
			out = append(out, "带宽")
		case "quantity":
			out = append(out, "数量")
		default:
			out = append(out, item)
		}
	}
	return out
}
