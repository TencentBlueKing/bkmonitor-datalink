package description

import (
	"encoding/json"
	"math"
	"testing"
)

func TestNumberRejectsMissingAndNonNumericValues(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "null", "true", "\"1\"", "{}", "[]", "NaN", "1e999", "9007199254740993"} {
		if _, err := ParseNumber(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestPythonDecimalRoundingAndRepresentation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value     float64
		precision int
		want      float64
	}{{2.675, 2, 2.67}, {1.125, 2, 1.12}, {1.375, 2, 1.38}, {-2.675, 2, -2.67}, {0.0000005, 6, 0}, {0.0000015, 6, 0.000002}} {
		if got := roundDecimal(tc.value, tc.precision); got != tc.want {
			t.Errorf("round(%v,%d)=%v want %v", tc.value, tc.precision, got, tc.want)
		}
	}
	for _, tc := range []struct {
		value float64
		want  string
	}{{1, "1.0"}, {1e6, "1000000.0"}, {1e16, "1e+16"}, {1e-5, "1e-05"}, {1e-4, "0.0001"}, {math.Copysign(0, -1), "-0.0"}} {
		if got := pythonNumber(tc.value, false); got != tc.want {
			t.Errorf("repr(%v)=%q want %q", tc.value, got, tc.want)
		}
	}
}
