// Package review provides deterministic arithmetic for the rental review agent.
package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math/big"
	"regexp"
	"strings"
)

const ToolName = "rental_calculate"

// ToolSpec describes the bounded arithmetic function for the HTTP API adapter.
func ToolSpec() map[string]any {
	return map[string]any{
		"type": "function", "name": ToolName,
		"description": "精确核算租房费用：十进制四则运算和括号，不执行代码。为每个表达式提供含单位/前提/字段依据的 label。返回 exact 分数、value 八位小数及 rounded 两位小数；货币使用 rounded，比例可用 value。不能替代收费语义核实。",
		"inputSchema": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"calculations"},
			"properties": map[string]any{"calculations": map[string]any{
				"type": "array", "minItems": 1, "maxItems": 64,
				"items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"label", "expression"},
					"properties": map[string]any{
						"label":      map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
						"expression": map[string]any{"type": "string", "minLength": 1, "maxLength": 1024},
					},
				},
			}},
		},
	}
}

type Calculation struct {
	Label      string `json:"label"`
	Expression string `json:"expression"`
}

type CalculationResult struct {
	Calculation
	Exact   string `json:"exact,omitempty"`
	Value   string `json:"value,omitempty"`
	Rounded string `json:"rounded,omitempty"`
	Error   string `json:"error,omitempty"`
}

func Calculate(input json.RawMessage) ([]CalculationResult, error) {
	if len(input) > 256*1024 {
		return nil, errors.New("计算请求过大")
	}
	var args struct {
		Calculations []Calculation `json:"calculations"`
	}
	d := json.NewDecoder(bytes.NewReader(input))
	d.DisallowUnknownFields()
	if d.Decode(&args) != nil || d.Decode(new(any)) != io.EOF || len(args.Calculations) < 1 || len(args.Calculations) > 64 {
		return nil, errors.New("需要 1 至 64 个包含 label 和 expression 的计算项")
	}
	results := make([]CalculationResult, 0, len(args.Calculations))
	for _, c := range args.Calculations {
		r := CalculationResult{Calculation: c}
		if len(c.Label) == 0 || len(c.Label) > 4000 {
			r.Error = "需要简短的计算依据 label"
		} else if n, err := evaluate(c.Expression); err != nil {
			r.Error = err.Error()
		} else {
			r.Exact, r.Value, r.Rounded = n.RatString(), n.FloatString(8), n.FloatString(2)
		}
		results = append(results, r)
	}
	return results, nil
}

var decimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?$`)
var arithmetic = regexp.MustCompile(`^[0-9+*/(). \t\r\n-]+$`)

func evaluate(expression string) (*big.Rat, error) {
	if len(expression) == 0 || len(expression) > 1024 || !arithmetic.MatchString(expression) || strings.Contains(expression, "//") || strings.Contains(expression, "/*") {
		return nil, errors.New("仅支持十进制数字、+ - * / 和括号，表达式最多 1024 字节")
	}
	expr, err := parser.ParseExpr(expression)
	if err != nil {
		return nil, errors.New("表达式格式不正确")
	}
	budget := 256
	return compute(expr, &budget)
}

func compute(expr ast.Expr, budget *int) (*big.Rat, error) {
	*budget--
	if *budget < 0 {
		return nil, errors.New("表达式过于复杂，请分项计算")
	}
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return compute(e.X, budget)
	case *ast.BasicLit:
		if len(e.Value) > 32 || !decimal.MatchString(e.Value) {
			return nil, errors.New("数字需为不超过 32 字符的十进制数，不支持科学记数法或其他进制")
		}
		n, ok := new(big.Rat).SetString(e.Value)
		if ok {
			return n, nil
		}
	case *ast.UnaryExpr:
		n, err := compute(e.X, budget)
		if err != nil {
			return nil, err
		}
		if e.Op == token.SUB {
			return n.Neg(n), nil
		}
		if e.Op == token.ADD {
			return n, nil
		}
	case *ast.BinaryExpr:
		a, err := compute(e.X, budget)
		if err != nil {
			return nil, err
		}
		b, err := compute(e.Y, budget)
		if err != nil {
			return nil, err
		}
		switch e.Op {
		case token.ADD:
			a.Add(a, b)
		case token.SUB:
			a.Sub(a, b)
		case token.MUL:
			a.Mul(a, b)
		case token.QUO:
			if b.Sign() == 0 {
				return nil, errors.New("不能除以零")
			}
			a.Quo(a, b)
		default:
			return nil, errors.New("仅支持 + - * / 运算")
		}
		if a.Num().BitLen() > 4096 || a.Denom().BitLen() > 4096 {
			return nil, errors.New("计算值过大，请分项核实")
		}
		return a, nil
	}
	return nil, errors.New("不支持的计算内容")
}
