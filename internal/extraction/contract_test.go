package extraction

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func fixture(t *testing.T) (Input, []byte) {
	t.Helper()
	source, err := os.ReadFile("../../docs/design/rental-persistence-v1/input.example.json")
	if err != nil {
		t.Fatal(err)
	}
	var input Input
	if err = json.Unmarshal(source, &input); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../docs/design/rental-persistence-v1/extraction.example.json")
	if err != nil {
		t.Fatal(err)
	}
	return input, raw
}
func TestContractFixtureAndSchemaStayInSync(t *testing.T) {
	input, raw := fixture(t)
	result, err := Decode(raw, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Quality() != "NEEDS_REVIEW" || result.Rental.DepositAmount != nil || result.Charges[4].Amount != nil || *result.Charges[3].Amount != "0.00" {
		t.Fatal("unknown/included/free distinctions lost")
	}
	schema, err := os.ReadFile("../../docs/design/rental-persistence-v1/extraction.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal(schema, &a)
	_ = json.Unmarshal(Schema(), &b)
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if !bytes.Equal(aa, bb) {
		t.Fatal("published schema differs from executable contract")
	}
}
func TestRejectMalformedAndInconsistentExtraction(t *testing.T) {
	input, raw := fixture(t)
	tests := map[string]func(map[string]any){
		"missing key":      func(m map[string]any) { delete(m["rental"].(map[string]any), "orientation") },
		"unknown key":      func(m map[string]any) { m["extra"] = true },
		"float money":      func(m map[string]any) { m["rental"].(map[string]any)["monthly_rent"] = 2500.01 },
		"excess precision": func(m map[string]any) { m["rental"].(map[string]any)["monthly_rent"] = "2500.001" },
		"overflow":         func(m map[string]any) { m["rental"].(map[string]any)["monthly_rent"] = "1000000000000.00" },
		"area reversed":    func(m map[string]any) { m["rental"].(map[string]any)["area_min_sqm"] = "31.00" },
		"area hybrid":      func(m map[string]any) { m["rental"].(map[string]any)["area_sqm"] = "27.50" },
		"prepay zero":      func(m map[string]any) { m["rental"].(map[string]any)["prepay_months"] = "0.00" },
		"lease zero":       func(m map[string]any) { m["rental"].(map[string]any)["lease_months"] = "0.00" },
		"lease negative":   func(m map[string]any) { m["rental"].(map[string]any)["lease_months"] = "-1.00" },
		"lease number":     func(m map[string]any) { m["rental"].(map[string]any)["lease_months"] = 12 },
		"lease precision":  func(m map[string]any) { m["rental"].(map[string]any)["lease_months"] = "0.125" },
		"lease overflow":   func(m map[string]any) { m["rental"].(map[string]any)["lease_months"] = "10000.00" },
		"lease no evidence": func(m map[string]any) {
			m["evidence"] = m["evidence"].([]any)[1:]
		},
		"no currency":          func(m map[string]any) { m["rental"].(map[string]any)["currency"] = nil },
		"invalid enum":         func(m map[string]any) { m["rental"].(map[string]any)["orientation"] = "SOUTH" },
		"included is not zero": func(m map[string]any) { m["charges"].([]any)[4].(map[string]any)["amount"] = "0.00" },
		"free is zero":         func(m map[string]any) { m["charges"].([]any)[3].(map[string]any)["amount"] = "1.00" },
		"mixed pricing":        func(m map[string]any) { m["charges"].([]any)[1].(map[string]any)["amount"] = "5.00" },
		"no evidence":          func(m map[string]any) { m["evidence"] = []any{} },
		"invented source": func(m map[string]any) {
			m["charges"].([]any)[0].(map[string]any)["source_field_ids"] = []string{"fake"}
		},
		"bad evidence path":   func(m map[string]any) { m["evidence"].([]any)[0].(map[string]any)["path"] = "/rental/missing" },
		"null arrays":         func(m map[string]any) { m["charges"] = nil },
		"unexplained unknown": func(m map[string]any) { m["issues"] = []any{} },
	}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			edit(value)
			changed, _ := json.Marshal(value)
			if _, err := Decode(changed, input); err == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
	for _, changed := range [][]byte{append(raw, []byte(` {}`)...), bytes.Replace(raw, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "schema_version": 1`), 1)} {
		if _, err := Decode(changed, input); err == nil {
			t.Fatal("duplicate/trailing content accepted")
		}
	}
	value := "unrepresented"
	input.Document.Fields = append(input.Document.Fields, input.Document.Fields[0])
	input.Document.Fields[len(input.Document.Fields)-1].ID = "extra"
	input.Document.Fields[len(input.Document.Fields)-1].Value = &value
	if _, err := Decode(raw, input); err == nil {
		t.Fatal("uncovered nonblank source accepted")
	}
}

func TestLeaseDurationAndLegacyResult(t *testing.T) {
	for _, duration := range []string{"12.00", "0.50"} {
		t.Run(duration, func(t *testing.T) {
			input, raw := fixture(t)
			value := duration + "个月"
			input.Document.Fields[0].Value = &value
			raw = bytes.Replace(raw, []byte(`"lease_months": "12.00"`), []byte(`"lease_months": "`+duration+`"`), 1)
			result, err := Decode(raw, input)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rental.LeaseMonths == nil || string(*result.Rental.LeaseMonths) != duration || *result.Rental.PrepayMonths != "3.00" || *result.Rental.DepositMonths != "1.00" {
				t.Fatal("lease duration confused with deposit or payment period")
			}
		})
	}
	for _, legacy := range []bool{false, true} {
		input, raw := fixture(t)
		input.Document.Fields = input.Document.Fields[1:]
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		rental := value["rental"].(map[string]any)
		rental["lease_months"] = nil
		value["evidence"] = value["evidence"].([]any)[1:]
		if legacy {
			delete(rental, "lease_months")
			schema, err := compiled()
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(value); err == nil {
				t.Fatal("new model output must explicitly include lease_months")
			}
		}
		raw, _ = json.Marshal(value)
		result, err := Decode(raw, input)
		if err != nil || result.Rental.LeaseMonths != nil {
			t.Fatalf("unknown/legacy duration must remain null: %v", err)
		}
	}
}
