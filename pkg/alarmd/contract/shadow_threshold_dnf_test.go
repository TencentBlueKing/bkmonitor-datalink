package contract

import (
	"encoding/json"
	"os"
	"testing"
)

func TestThresholdDNFV2StrictSemanticConfig(t *testing.T) {
	wire, err := os.ReadFile("testdata/shadow-final-v2/frozen_dnf_or.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := DecodeComparisonConfigV2(wire, len(wire))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{}`, `{"groups":[]}`, `{"groups":[null]}`, `{"groups":[{"conditions":[]}]}`, `{"groups":[{"conditions":[{"operator":"EQ"}]}]}`, `{"groups":[{"conditions":[{"operator":"EQ","threshold":null}]}]}`, `{"groups":[{"conditions":[{"operator":"CONTAINS","threshold":"1"}]}]}`, `{"groups":[{"conditions":[{"operator":"EQ","threshold":"1","ignored":true}]}]}`, `{"groups":[{"conditions":[{"operator":"EQ","threshold":"1","threshold":"2"}]}]}`} {
		t.Run(raw, func(t *testing.T) {
			copy := *c
			copy.Levels = append([]ShadowLevelConfigV2(nil), c.Levels...)
			copy.Levels[0].Detectors = append([]ShadowDetectorConfigV2(nil), c.Levels[0].Detectors...)
			copy.Levels[0].Detectors[0].SemanticConfig = json.RawMessage(raw)
			if _, _, err := CanonicalComparisonConfigV2(copy); err == nil {
				t.Fatal("invalid DNF accepted")
			}
		})
	}
	var document map[string]any
	if err = json.Unmarshal(wire, &document); err != nil {
		t.Fatal(err)
	}
	detector := document["levels"].([]any)[0].(map[string]any)["detectors"].([]any)[0].(map[string]any)
	detector["operator"] = ""
	bad, _ := json.Marshal(document)
	if _, err = DecodeComparisonConfigV2(bad, len(bad)); err == nil {
		t.Fatal("wire mixed scalar and DNF fields")
	}
}
