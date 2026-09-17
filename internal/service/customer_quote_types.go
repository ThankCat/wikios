package service

const (
	customerPricingTableVersion = "customer_pricing.v1"

	customerQuoteProductStaticShared          = "static_shared"
	customerQuoteProductResidentialShared     = "residential_shared"
	customerQuoteProductResidentialDedicated  = "residential_dedicated"

	customerQuoteStatusMissingSlots   = "missing_slots"
	customerQuoteStatusQuoted         = "quoted"
	customerQuoteStatusAtFloor        = "at_floor"
	customerQuoteStatusBelowFloor     = "below_floor"
	customerQuoteStatusUnsupported    = "unsupported"
	customerQuoteStatusNotApplicable  = "not_applicable"
)

type CustomerPricingBand struct {
	MinQty int `json:"min_qty"`
	MaxQty int `json:"max_qty"`
	Low    int `json:"low"`
	High   int `json:"high"`
}

type CustomerPricingSKU struct {
	Product   string                `json:"product"`
	Bandwidth string                `json:"bandwidth"`
	Bands     []CustomerPricingBand `json:"bands"`
}

type CustomerPricingTable struct {
	Version string               `json:"version"`
	SKUs    []CustomerPricingSKU `json:"skus"`
}

type CustomerQuoteInput struct {
	Product       string
	Bandwidth     string
	Quantity      int
	NamedPrice    int
	LastQuoted    int
	WantsDiscount bool
}

type CustomerQuoteFacts struct {
	Status       string   `json:"status"`
	Missing      []string `json:"missing,omitempty"`
	Product      string   `json:"product,omitempty"`
	Bandwidth    string   `json:"bandwidth,omitempty"`
	Quantity     int      `json:"quantity,omitempty"`
	UnitPrice    int      `json:"unit_price,omitempty"`
	FloorPrice   int      `json:"floor_price,omitempty"`
	HighPrice    int      `json:"high_price,omitempty"`
	MonthlyTotal int      `json:"monthly_total,omitempty"`
	CanGoLower   bool     `json:"can_go_lower"`
	Speak        string   `json:"speak"`
}
