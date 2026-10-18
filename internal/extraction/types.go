// V1 AI 输出 DTO。数据库系统列由后端管理，不属于模型输出。
package extraction

// DecimalString 仅承载经过 Schema 和业务校验的十进制字符串。
// 不能直接把任意字符串转成此类型就视为合法，也不经 float64 转换。
type DecimalString string
type AreaQualifier string
type AreaScope string
type Orientation string
type ChargeType string
type Direction string
type PricingMode string
type ChargeUnit string
type RatioBase string
type BillingPeriod string
type PaymentTiming string
type IssueCode string

// 枚举有效值以 extraction.schema.json 为准；实现时须校验，Go 字符串别名不自动限制取值。
// 不使用 omitempty：可空字段必须显式输出 null；数组必须初始化为非 nil 空切片。
type ExtractionResult struct {
	SchemaVersion    uint16     `json:"schema_version"`
	Rental           Rental     `json:"rental"`
	Charges          []Charge   `json:"charges"`
	Evidence         []Evidence `json:"evidence"`
	Issues           []Issue    `json:"issues"`
	UnmappedFieldIDs []string   `json:"unmapped_field_ids"`
}

type Rental struct {
	CommunityLocation     *string        `json:"community_location"`
	AreaSqm               *DecimalString `json:"area_sqm"`
	AreaMinSqm            *DecimalString `json:"area_min_sqm"`
	AreaMaxSqm            *DecimalString `json:"area_max_sqm"`
	AreaQualifier         *AreaQualifier `json:"area_qualifier"`
	AreaScope             *AreaScope     `json:"area_scope"`
	Orientation           *Orientation   `json:"orientation"`
	BrokerName            *string        `json:"broker_name"`
	Currency              *string        `json:"currency"`
	MonthlyRent           *DecimalString `json:"monthly_rent"`
	LeaseMonths           *DecimalString `json:"lease_months"`
	DepositMonths         *DecimalString `json:"deposit_months"`
	PrepayMonths          *DecimalString `json:"prepay_months"`
	DepositAmount         *DecimalString `json:"deposit_amount"`
	QuotedFirstMonthTotal *DecimalString `json:"quoted_first_month_total"`
	QuotedMonthlyTotal    *DecimalString `json:"quoted_monthly_total"`
}

type Charge struct {
	ChargeType     ChargeType     `json:"charge_type"`
	ChargeName     string         `json:"charge_name"`
	Direction      Direction      `json:"direction"`
	PricingMode    PricingMode    `json:"pricing_mode"`
	Currency       *string        `json:"currency"`
	Amount         *DecimalString `json:"amount"`
	UnitPrice      *DecimalString `json:"unit_price"`
	Unit           *ChargeUnit    `json:"unit"`
	Ratio          *DecimalString `json:"ratio"`
	RatioBase      *RatioBase     `json:"ratio_base"`
	BillingPeriod  *BillingPeriod `json:"billing_period"`
	PaymentTiming  *PaymentTiming `json:"payment_timing"`
	SourceFieldIDs []string       `json:"source_field_ids"`
	Notes          *string        `json:"notes"`
}

type Evidence struct {
	Path           string   `json:"path"`
	SourceFieldIDs []string `json:"source_field_ids"`
	Note           *string  `json:"note"`
}

type Issue struct {
	Path           string    `json:"path"`
	SourceFieldIDs []string  `json:"source_field_ids"`
	Code           IssueCode `json:"code"`
	Message        string    `json:"message"`
}
