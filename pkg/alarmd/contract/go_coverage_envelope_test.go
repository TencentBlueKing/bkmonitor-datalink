package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func coverageEnvelopeFixture(t *testing.T) GoCoverageEnvelopeV1 {
	wire, err := os.ReadFile("testdata/shadow-final-v2/frozen_expected.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DecodeComparisonConfigV2(wire, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r := shadowTestReceipt()
	_, digest, _ := CanonicalComparisonConfigV2(*cfg)
	r.Context.ComparisonConfigDigest = digest
	r.Context.EvaluationTime = 1000
	r.Input.SourceWindow = SourceWindowV2{FromTime: 1000 - int64(cfg.Schedule.WindowSeconds), UntilTime: 1000}
	level := r.Levels[0]
	r.Levels = nil
	for _, l := range cfg.Levels {
		copy := level
		copy.LevelID = l.LevelID
		r.Levels = append(r.Levels, copy)
	}
	at := int64(1000000)
	return GoCoverageEnvelopeV1{EpochID: r.EpochID, Receipt: r, Config: *cfg, CompletedAt: &at}
}
func TestGoCoverageEnvelopeStableSelfContainedAndStrict(t *testing.T) {
	e := coverageEnvelopeFixture(t)
	original, _ := EncodeChainCoverageReceiptV1(&e.Receipt, 1<<20)
	encoded, err := EncodeGoCoverageEnvelopeV1(e, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want := encoded.CopyBytes()
	decoded, err := DecodeGoCoverageEnvelopeV1(want, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodeGoCoverageEnvelopeV1(*decoded, 1<<20)
	if err != nil || !bytes.Equal(want, again.CopyBytes()) {
		t.Fatal("replay not stable", err)
	}
	*e.CompletedAt = 2000000
	e.Config.Levels[0].Priority++
	if !bytes.Equal(want, encoded.CopyBytes()) {
		t.Fatal("caller changed immutable wire")
	}
	receiptBytes, _ := EncodeChainCoverageReceiptV1(&decoded.Receipt, 1<<20)
	if !bytes.Equal(original, receiptBytes) {
		t.Fatal("old Receipt bytes changed")
	}
	for _, field := range []string{"progress_completed_at_unix_milli", "comparison_config", "receipt_identity", "record_type"} {
		var obj map[string]json.RawMessage
		_ = json.Unmarshal(want, &obj)
		obj[field] = json.RawMessage(`null`)
		bad, _ := json.Marshal(obj)
		if _, err := DecodeGoCoverageEnvelopeV1(bad, 1<<20); err == nil {
			t.Fatal("accepted null/tampered", field)
		}
	}
	decoded.CompletedAt = nil
	if _, err := EncodeGoCoverageEnvelopeV1(*decoded, 1<<20); err == nil {
		t.Fatal("complete without actual terminal time")
	}
	decoded.Receipt.CoverageComplete = false
	decoded.Receipt.GapReasons = []string{"TERMINAL_TIME_UNKNOWN"}
	decoded.Receipt.Input.QueryAttempts.LogicalSlot = KnownCountV1{}
	if _, err := EncodeGoCoverageEnvelopeV1(*decoded, 1<<20); err != nil {
		t.Fatal("unknown history cannot be represented", err)
	}
}
