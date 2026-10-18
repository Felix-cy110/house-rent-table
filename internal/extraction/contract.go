// Package extraction defines and validates the versioned AI business contract.
package extraction

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"sync"

	"github.com/Felix-cy110/house-rent-table/internal/document"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const Version = 1
const RulesVersion = "rental-extraction-v1.1.0"
const MaxResultBytes = 16 << 20

//go:embed schema.json instructions.md
var assets embed.FS

func Schema() json.RawMessage { b, _ := assets.ReadFile("schema.json"); return b }
func Instructions() string    { b, _ := assets.ReadFile("instructions.md"); return string(b) }

type Context struct {
	DefaultCurrency *string `json:"default_currency"`
}
type Input struct {
	Document document.Document `json:"document"`
	Context  Context           `json:"extraction_context"`
}
type Output struct {
	JSON     json.RawMessage
	Provider string
	Model    string
}

var compiled = sync.OnceValues(func() (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	var data any
	if err := json.Unmarshal(Schema(), &data); err != nil {
		return nil, err
	}
	if err := c.AddResource("rental.json", data); err != nil {
		return nil, err
	}
	return c.Compile("rental.json")
})

// Decode rejects duplicate keys, trailing documents, unknown/missing keys
// (except lease_months, which defaults to null for pre-upgrade checkpoints),
// imprecise decimals, invalid combinations and references outside this input.
// It cannot prove that a model's natural-language interpretation is true.
func Decode(raw []byte, input Input) (*ExtractionResult, error) {
	if len(raw) == 0 || len(raw) > MaxResultBytes {
		return nil, errors.New("提取结果大小无效")
	}
	if err := input.Document.Validate(); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := readValue(dec)
	if err != nil {
		return nil, errors.New("提取结果不是唯一、无重复键的 JSON 对象")
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, errors.New("提取结果包含多余内容")
	}
	// Older persisted V1 results predate the optional lease duration. The schema
	// sent to new extraction sessions still requires an explicit value or null.
	if root, ok := value.(map[string]any); ok {
		if rental, ok := root["rental"].(map[string]any); ok {
			if _, exists := rental["lease_months"]; !exists {
				rental["lease_months"] = nil
			}
		}
	}
	schema, err := compiled()
	if err != nil {
		return nil, err
	}
	if err = schema.Validate(value); err != nil {
		return nil, errors.New("提取结果不符合 V1 字段、类型或精度约束")
	}
	var result ExtractionResult
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, errors.New("无法读取提取对象")
	}
	if err = result.validate(input, value); err != nil {
		return nil, err
	}
	return &result, nil
}

func readValue(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, errors.New("key")
			}
			if _, exists := m[key]; exists {
				return nil, errors.New("duplicate")
			}
			v, e := readValue(d)
			if e != nil {
				return nil, e
			}
			m[key] = v
		}
		_, err = d.Token()
		return m, err
	case '[':
		a := []any{}
		for d.More() {
			v, e := readValue(d)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	}
	return nil, errors.New("delimiter")
}

func positive(v *DecimalString) bool { return v != nil && decimal(v).Sign() > 0 }
func decimal(v *DecimalString) *big.Rat {
	r := new(big.Rat)
	if v != nil {
		r.SetString(string(*v))
	}
	return r
}
func str[T ~string](v *T) string {
	if v == nil {
		return ""
	}
	return string(*v)
}

func (r *ExtractionResult) Quality() string {
	if len(r.Issues) > 0 || len(r.UnmappedFieldIDs) > 0 {
		return "NEEDS_REVIEW"
	}
	return "NORMALIZED"
}

func (r *ExtractionResult) validate(input Input, value any) error {
	fail := func(s string) error { return fmt.Errorf("提取业务校验失败：%s", s) }
	v := r.Rental
	if v.LeaseMonths != nil && !positive(v.LeaseMonths) {
		return fail("租期月数必须为正")
	}
	switch str(v.AreaQualifier) {
	case "":
		if v.AreaSqm != nil || v.AreaMinSqm != nil || v.AreaMaxSqm != nil {
			return fail("面积缺少限定类型")
		}
	case "EXACT", "APPROXIMATE":
		if !positive(v.AreaSqm) || v.AreaMinSqm != nil || v.AreaMaxSqm != nil {
			return fail("单值面积组合错误")
		}
	case "RANGE":
		if v.AreaSqm != nil || !positive(v.AreaMinSqm) || !positive(v.AreaMaxSqm) || decimal(v.AreaMinSqm).Cmp(decimal(v.AreaMaxSqm)) > 0 {
			return fail("面积范围错误")
		}
	}
	if v.PrepayMonths != nil && !positive(v.PrepayMonths) {
		return fail("预付月数必须为正")
	}
	if v.Currency == nil && (v.MonthlyRent != nil || v.DepositAmount != nil || v.QuotedFirstMonthTotal != nil || v.QuotedMonthlyTotal != nil) {
		return fail("金额缺少币种")
	}
	for _, c := range r.Charges {
		switch c.PricingMode {
		case "FIXED":
			if c.Amount == nil || c.UnitPrice != nil || c.Unit != nil || c.Ratio != nil || c.RatioBase != nil {
				return fail("固定费用组合错误")
			}
		case "UNIT_PRICE":
			if c.Amount != nil || c.UnitPrice == nil || c.Unit == nil || c.Ratio != nil || c.RatioBase != nil {
				return fail("单价组合错误")
			}
		case "RENT_RATIO":
			if c.Amount != nil || c.UnitPrice != nil || c.Unit != nil || c.Ratio == nil || c.RatioBase == nil {
				return fail("比例费用组合错误")
			}
		case "FREE":
			if c.Amount == nil || decimal(c.Amount).Sign() != 0 || c.UnitPrice != nil || c.Unit != nil || c.Ratio != nil || c.RatioBase != nil {
				return fail("免费不能包含非零金额或其他计价方式")
			}
		case "INCLUDED", "UNKNOWN":
			if c.Amount != nil || c.UnitPrice != nil || c.Unit != nil || c.Ratio != nil || c.RatioBase != nil {
				return fail("已含或未知不能伪造金额")
			}
		}
		if c.PricingMode != "INCLUDED" && c.PricingMode != "UNKNOWN" && c.Currency == nil {
			return fail("收费缺少币种")
		}
		if (str(c.Unit) == "OTHER" || str(c.RatioBase) == "OTHER" || str(c.BillingPeriod) == "OTHER" || str(c.PaymentTiming) == "OTHER" || c.PricingMode == "INCLUDED") && c.Notes == nil {
			return fail("特殊计费或包含关系缺少说明")
		}
	}
	fields := map[string]document.Field{}
	covered := map[string]bool{}
	for _, f := range input.Document.Fields {
		fields[f.ID] = f
	}
	refs := func(ids []string, requireFilled bool) error {
		seen := map[string]bool{}
		for _, id := range ids {
			f, ok := fields[id]
			if !ok || seen[id] {
				return fail("无效或重复来源 ID")
			}
			if requireFilled && (f.Value == nil || strings.TrimSpace(*f.Value) == "") {
				return fail("非空业务值引用空白来源")
			}
			seen[id] = true
			covered[id] = true
		}
		return nil
	}
	paths := map[string]bool{}
	for _, e := range r.Evidence {
		if paths[e.Path] {
			return fail("重复证据路径")
		}
		paths[e.Path] = true
		target, ok := resolve(value, e.Path)
		if !ok || target == nil {
			return fail("证据路径不存在或指向空值")
		}
		if !strings.HasPrefix(e.Path, "/rental/") && !strings.HasPrefix(e.Path, "/charges/") {
			return fail("证据必须指向业务字段")
		}
		if err := refs(e.SourceFieldIDs, true); err != nil {
			return err
		}
	}
	for _, i := range r.Issues {
		if _, ok := resolve(value, i.Path); !ok {
			return fail("问题路径不存在")
		}
		if err := refs(i.SourceFieldIDs, false); err != nil {
			return err
		}
	}
	for _, c := range r.Charges {
		if err := refs(c.SourceFieldIDs, c.PricingMode != "UNKNOWN"); err != nil {
			return err
		}
		if c.PricingMode == "UNKNOWN" {
			for _, id := range c.SourceFieldIDs {
				f := fields[id]
				if f.Value == nil || strings.TrimSpace(*f.Value) == "" {
					continue
				}
				explained := false
				for _, issue := range r.Issues {
					for _, source := range issue.SourceFieldIDs {
						if source == id {
							explained = true
						}
					}
				}
				if !explained {
					return fail("非空未知费用缺少待复核说明")
				}
			}
		}
	}
	if err := refs(r.UnmappedFieldIDs, false); err != nil {
		return err
	}
	rental := value.(map[string]any)["rental"].(map[string]any)
	for key, val := range rental {
		if val != nil && !paths["/rental/"+key] {
			return fail("非空房源字段缺少证据")
		}
	}
	for id, f := range fields {
		if f.Value != nil && strings.TrimSpace(*f.Value) != "" && !covered[id] {
			return fail("遗漏非空输入字段")
		}
	}
	return nil
}

func resolve(root any, path string) (any, bool) {
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	v := root
	for _, key := range strings.Split(path[1:], "/") {
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			v, ok = x[key]
			if !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(x) || strconv.Itoa(i) != key {
				return nil, false
			}
			v = x[i]
		default:
			return nil, false
		}
	}
	return v, true
}
