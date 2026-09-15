package contract

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestComparisonConfigV2SharedVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/shadow-final-v2/comparison_config.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Vectors []struct {
			Name   string
			Input  json.RawMessage `json:"canonical_input"`
			Bytes  string          `json:"expected_canonical_utf8"`
			Digest string          `json:"expected_sha256"`
		}
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, v := range fixture.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			c, err := DecodeComparisonConfigV2(v.Input, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			got, digest, err := CanonicalComparisonConfigV2(*c)
			if err != nil || string(got) != v.Bytes || digest != v.Digest {
				t.Fatalf("canonical mismatch %s %s %v", got, digest, err)
			}
			for _, bad := range []string{
				strings.Replace(string(got), `"source_unit":""`, `"source_unit":null`, 1),
				strings.Replace(string(got), `"source_unit":"",`, ``, 1),
				strings.Replace(string(got), `"not_time_align":false,`, ``, 1),
				strings.Replace(string(got), `"table":`, `"unknown":false,"table":`, 1),
				strings.Replace(string(got), `"value_fields":["value"]`, `"value_fields":[null]`, 1),
				strings.Replace(string(got), `"schema_version":"comparison-config-v2"`, `"schema_version":"comparison-config-v1"`, 1),
			} {
				if _, err := DecodeComparisonConfigV2([]byte(bad), 1<<20); err == nil {
					t.Fatalf("accepted incomplete/unknown config %s", bad)
				}
			}
			c.Selector.Table = ""
			if _, _, err := CanonicalComparisonConfigV2(*c); err != nil {
				t.Fatalf("known empty table: %v", err)
			}
		})
	}
}
