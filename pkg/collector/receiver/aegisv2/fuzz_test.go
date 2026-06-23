package aegisv2

import "testing"

func FuzzUnmarshalFlexibleJSON(f *testing.F) {
	seeds := []string{
		`{"k":"v","n":1}`,
		`"{\"k\":\"v\",\"n\":1}"`,
		`null`,
		``,
		`""`,
		`[]`,
		`{`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		var dst map[string]any
		_ = unmarshalFlexibleJSON([]byte(input), &dst)
	})
}
