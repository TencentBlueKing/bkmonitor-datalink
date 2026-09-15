package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFinalEvidenceKnownUnitBoundary(t *testing.T) {
	d := strings.Repeat("a", 64)
	e := FinalResultEvidenceV1{
		Schema: Schema{Name: FinalResultEvidenceSchemaV1, Major: 1}, RequiredFeatures: []string{}, RecordType: ShadowFinalResult,
		EpochID: "epoch", Chain: ShadowPython, ResultKind: TriggerEventAbnormal,
		Subject: ShadowSubjectV1{IdentityVersion: "identity-v1", TenantID: "tenant", BusinessID: "1", StrategyID: "2", DimensionIdentityDigest: d, SourceTime: 60},
		Context: shadowTestContext(), Completeness: ShadowCompletenessV1{Input: "FULL", Readiness: "READY", History: "FULL"},
		Delivery: ShadowDeliveryV1{FinalAdmission: "PRODUCED", BusinessACK: true, ReasonCode: "BUSINESS_ACK_CONFIRMED"},
		Native:   ShadowNativeRefV1{EventID: "event", SemanticDigest: d, LevelResults: []LevelResultV1{{LevelID: 1, Priority: 1, Result: LevelResultAbnormal}}},
		Primary:  PrimaryComparableProjectionV1{Version: "primary-v1", SelectionMappingVersion: "selection-v1", LevelID: 1, Priority: 1, Result: TriggerEventAbnormal, Values: map[string]string{"value": "1"}, WindowStart: 0, WindowEnd: 60, DetectConfigDigest: d, TriggerConfigDigest: d},
	}
	e.Native.EvidenceDigest, _ = ShadowNativeDigestV1(e.Native)
	for _, unit := range []string{"percent", ""} {
		t.Run("known_"+unit, func(t *testing.T) {
			e.Primary.Unit = unit
			e.Primary.Digest, _ = ShadowProjectionDigestV1(e.Primary)
			wire, err := EncodeFinalResultEvidenceV1(&e, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeShadowResultRecordV1(wire, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Evidence.Primary.Unit != unit || decoded.Evidence.Primary.Digest != e.Primary.Digest {
				t.Fatal("unit or digest changed")
			}
			again, err := EncodeFinalResultEvidenceV1(decoded.Evidence, 1<<20)
			if err != nil || string(again) != string(wire) {
				t.Fatal("wire changed", err)
			}
		})
	}
	// Matching digest for empty unit prevents the digest check from hiding a
	// missing/null field that encoding/json would otherwise collapse to empty.
	e.Primary.Unit = ""
	e.Primary.Digest, _ = ShadowProjectionDigestV1(e.Primary)
	b, _ := json.Marshal(e)
	var root map[string]json.RawMessage
	_ = json.Unmarshal(b, &root)
	var projection map[string]json.RawMessage
	_ = json.Unmarshal(root["primary_comparable_projection"], &projection)
	for _, raw := range []string{"missing", "null", "0", "false", "{}", "[]"} {
		t.Run(raw, func(t *testing.T) {
			projection["unit"] = json.RawMessage(raw)
			if raw == "missing" {
				delete(projection, "unit")
			}
			root["primary_comparable_projection"], _ = json.Marshal(projection)
			wire, _ := json.Marshal(root)
			if _, err := DecodeShadowResultRecordV1(wire, 1<<20); err == nil {
				t.Fatal("accepted unknown or non-string unit")
			}
		})
	}
}
