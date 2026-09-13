package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestBusinessActualPythonReference(t *testing.T) {
	wire, err := os.ReadFile("testdata/business-kafka-v1/reference.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := DecodeBusinessAbnormalV1(wire, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if r.SemanticDigest != "e565c3780cf28aa9c0e19940b7058b75c79e92c851772e0db7e3b31835adf9df" {
		t.Fatal("shared Python semantic drift")
	}
	want := r.SemanticDigest
	r.InputQuality = "FULL"
	r.Native.EventID = "go-native-id"
	got, err := BusinessSemanticDigestV1(*r)
	if err != nil || got != want {
		t.Fatal("quality or native identity entered business semantics")
	}
	e, err := EncodeBusinessAbnormalV1(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	copy := e.CopyBytes()
	copy[0] = 'x'
	if bytes.Equal(copy, e.CopyBytes()) {
		t.Fatal("mutable encoded payload")
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(wire, &obj); err != nil {
		t.Fatal(err)
	}
	for _, unit := range []string{"", "null", "1"} {
		var primary map[string]json.RawMessage
		_ = json.Unmarshal(obj["primary"], &primary)
		if unit == "" {
			delete(primary, "unit")
		} else {
			primary["unit"] = json.RawMessage(unit)
		}
		obj["primary"], _ = json.Marshal(primary)
		bad, _ := json.Marshal(obj)
		if _, err = DecodeBusinessAbnormalV1(bad, 1<<20); err == nil {
			t.Fatal("unknown unit accepted", unit)
		}
	}
}

func TestBusinessRecoveryDifferenceDoesNotReopenEquivalence(t *testing.T) {
	wire, _ := os.ReadFile("testdata/business-kafka-v1/reference.json")
	r, err := DecodeBusinessAbnormalV1(wire, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(r.Config, &object); err != nil {
		t.Fatal(err)
	}
	object["schema_version"] = json.RawMessage(`"comparison-config-v2"`)
	var levels []map[string]json.RawMessage
	if err = json.Unmarshal(object["levels"], &levels); err != nil {
		t.Fatal(err)
	}
	for _, level := range levels {
		level["recovery"] = json.RawMessage(`{"enabled":false,"consecutive_windows":0,"mode":"CONTINUOUS_TRIGGER_MISS","input_requirement":"DATA_DRIVEN"}`)
	}
	object["levels"], _ = json.Marshal(levels)
	full, _ := json.Marshal(object)
	c, err := DecodeComparisonConfigV2(full, len(full))
	if err != nil {
		t.Fatal(err)
	}
	a, ad, err := CanonicalBusinessConfigV1(*c)
	if err != nil {
		t.Fatal(err)
	}
	c.Levels[0].Recovery.Enabled = true
	c.Levels[0].Recovery.ConsecutiveWindows = 100
	b, bd, err := CanonicalBusinessConfigV1(*c)
	if err != nil || !bytes.Equal(a, b) || ad != bd {
		t.Fatal("Recovery-only difference changed ABNORMAL")
	}
	c.Levels[0].Trigger.WindowPoints = 3
	_, changed, err := CanonicalBusinessConfigV1(*c)
	if err != nil || changed == bd {
		t.Fatal("actual Trigger difference omitted")
	}
}
