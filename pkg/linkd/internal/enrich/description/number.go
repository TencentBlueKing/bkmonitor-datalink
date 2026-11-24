package description

import (
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Number 保留 JSON 数字的整数/浮点语义；二者在 bk-monitor 文案中可能不同。
// 必须通过 ParseNumber 构造，零值表示缺失，不能隐式作为观测值 0。
type Number struct {
	value   float64
	integer bool
	valid   bool
}

// ParseNumber 接受一个有限 JSON 数字；不接受字符串、null 或超限数字。
func ParseNumber(raw json.RawMessage) (Number, error) {
	if len(raw) == 0 || len(raw) > 128 || !json.Valid(raw) {
		return Number{}, invalid("number_invalid")
	}
	text := strings.TrimSpace(string(raw))
	if text == "" || (text[0] != '-' && (text[0] < '0' || text[0] > '9')) {
		return Number{}, invalid("number_invalid")
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return Number{}, invalid("number_invalid")
	}
	integer := !strings.ContainsAny(text, ".eE")
	exact, _ := new(big.Int).SetString(text, 10)
	if integer && (exact == nil || new(big.Int).Abs(exact).Cmp(new(big.Int).Lsh(big.NewInt(1), 53)) > 0) {
		// Event.values 使用 float64；拒绝已无法精确表达的来源整数。
		return Number{}, invalid("number_precision")
	}
	return Number{value: value, integer: integer, valid: true}, nil
}

// Python round 在二进制浮点的精确值上按十进制位数做 ties-to-even。
// 直接 RoundToEven(value*1e6) 会在乘法时丢失边界信息（例如 2.675）。
func roundDecimal(value float64, decimal int) float64 {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return value
	}
	rational := new(big.Rat).SetFloat64(value)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimal)), nil)
	rational.Mul(rational, new(big.Rat).SetInt(scale))
	negative := rational.Sign() < 0
	if negative {
		rational.Neg(rational)
	}
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(rational.Num(), rational.Denom(), remainder)
	cmp := new(big.Int).Lsh(remainder, 1).Cmp(rational.Denom())
	if cmp > 0 || (cmp == 0 && quotient.Bit(0) == 1) {
		quotient.Add(quotient, big.NewInt(1))
	}
	if negative {
		quotient.Neg(quotient)
	}
	result, _ := new(big.Rat).SetFrac(quotient, scale).Float64()
	if result == 0 {
		result = math.Copysign(result, value)
	}
	return result
}

func pythonNumber(value float64, integer bool) string {
	if integer {
		return strconv.FormatFloat(value, 'f', 0, 64)
	}
	abs := math.Abs(value)
	if abs != 0 && (abs < 1e-4 || abs >= 1e16) {
		return strconv.FormatFloat(value, 'e', -1, 64)
	}
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return text
}
