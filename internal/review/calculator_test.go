package review

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRentalCalculations(t *testing.T) {
	tests := []struct{ name, expression, exact, rounded string }{
		{"押一付三加年费中介费减抵扣", "3000 + 3000*3 + 300 + 1500 - 500", "13300", "13300.00"},
		{"填写总额差额", "14000 - (3000 + 3000*3 + 300 + 1500 - 500)", "700", "700.00"},
		{"物业费月年差异倍数", "300*12/300", "12", "12.00"},
		{"租金溢价百分比", "(3000-1000)/1000*100", "200", "200.00"},
		{"细微浮动", "(1050-1000)/1000*100", "5", "5.00"},
		{"非二进制浮点", "0.1+0.2", "3/10", "0.30"},
		{"月均费用", "3000+300/12", "3025", "3025.00"},
		{"重复收费剔除", "3000+100-100", "3000", "3000.00"},
		{"分以下四舍五入", "10/3", "10/3", "3.33"},
		{"负数舍入", "-1.005", "-201/200", "-1.01"},
		{"零", "0*12", "0", "0.00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{"calculations": []Calculation{{tt.name, tt.expression}}})
			r, err := Calculate(input)
			if err != nil || len(r) != 1 || r[0].Exact != tt.exact || r[0].Rounded != tt.rounded || r[0].Error != "" {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
}

func TestRejectsInvalidArithmetic(t *testing.T) {
	for _, expr := range []string{"1/0", "1<<3", "100%3", "1e3", "0x10", "010", "os.Exit(0)", "1/*comment*/+2", "1//2", "(1+2", strings.Repeat("1+", 130) + "1", strings.Repeat("1", 33)} {
		t.Run(expr, func(t *testing.T) {
			if _, err := evaluate(expr); err == nil {
				t.Fatalf("accepted %q", expr)
			}
		})
	}
}

func TestPartialErrorsAndMalformedRequests(t *testing.T) {
	r, err := Calculate(json.RawMessage(`{"calculations":[{"label":"未知不能按零除","expression":"1/0"},{"label":"已知小计","expression":"2000+2000"}]}`))
	if err != nil || len(r) != 2 || r[0].Error == "" || r[1].Exact != "4000" {
		t.Fatalf("%+v %v", r, err)
	}
	for _, data := range []string{`{}`, `{"calculations":[]}`, `{"calculations":[{"label":"x","expression":"1","command":"bad"}]}`, `{"calculations":[{"label":"x","expression":"1"}]} {}`} {
		if _, err := Calculate(json.RawMessage(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}
