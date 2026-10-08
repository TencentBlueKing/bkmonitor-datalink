package description

import (
	"encoding/json"
	"os"
	"testing"
)

func TestAllRegisteredUnitsAgainstBKMonitorPython(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/units.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []struct {
		Unit         string  `json:"unit"`
		CategoryUnit string  `json:"category_unit"`
		Raw          string  `json:"raw"`
		Formatted    string  `json:"formatted"`
		Minimum      float64 `json:"minimum"`
	}
	if err := json.Unmarshal(raw, &samples); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 52*20 {
		t.Fatalf("missing unit samples: %d", len(samples))
	}
	for _, sample := range samples {
		for _, id := range []string{sample.Unit, sample.CategoryUnit} {
			t.Run(id+"/"+sample.Raw, func(t *testing.T) {
				u, err := loadUnit(id)
				if err != nil {
					t.Fatal(err)
				}
				n := number(t, sample.Raw)
				formatted, err := u.format(n)
				if err != nil || formatted != sample.Formatted {
					t.Fatalf("formatted=%q error=%v want=%q", formatted, err, sample.Formatted)
				}
				if got := u.toMinimum(n.value, u.index); got != sample.Minimum {
					t.Fatalf("minimum=%v want=%v", got, sample.Minimum)
				}
			})
		}
	}
}
