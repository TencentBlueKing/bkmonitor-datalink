package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func coverageEnvelopeV2Fixture(t *testing.T) GoCoverageEnvelopeV2 {
	t.Helper()
	old := coverageEnvelopeFixture(t)
	wire, err := os.ReadFile("testdata/query-v3/config.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DecodeComparisonConfigV3(wire, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r := old.Receipt
	_, r.Context.ComparisonConfigDigest, err = CanonicalComparisonConfigV3(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.Input.SourceWindow.FromTime = r.Context.EvaluationTime - int64(cfg.Schedule.WindowSeconds)
	level := r.Levels[0]
	r.Levels = nil
	for _, l := range cfg.Levels {
		v := level
		v.LevelID = l.LevelID
		r.Levels = append(r.Levels, v)
	}
	return GoCoverageEnvelopeV2{EpochID: r.EpochID, Receipt: r, Config: *cfg, CompletedAt: old.CompletedAt}
}

func TestGoCoverageV2VersionedStableImmutable(t *testing.T) {
	e := coverageEnvelopeV2Fixture(t)
	receipt, err := EncodeChainCoverageReceiptV1(&e.Receipt, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeGoCoverageEnvelopeV2(e, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	want := encoded.CopyBytes()
	d, err := DecodeGoCoverageEnvelopeV2(want, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodeGoCoverageEnvelopeV2(*d, 1<<20)
	if err != nil || !bytes.Equal(want, again.CopyBytes()) {
		t.Fatal("unstable replay", err)
	}
	actual, err := EncodeChainCoverageReceiptV1(&d.Receipt, 1<<20)
	if err != nil || !bytes.Equal(receipt, actual) {
		t.Fatal("ReceiptV1 changed", err)
	}
	view, err := DecodeGoCoverageRecord(want, 1<<20)
	_, business, _ := CanonicalBusinessConfigV2(d.Config)
	if err != nil || view.FullConfigDigest != e.Receipt.Context.ComparisonConfigDigest || view.BusinessConfigDigest != business || view.Identity != d.Identity {
		t.Fatal("consumer bindings", err)
	}
	*e.CompletedAt += 10
	e.Config.Query.Selectors[0].Metric = "changed"
	if !bytes.Equal(want, encoded.CopyBytes()) {
		t.Fatal("caller changed wire")
	}
	if _, err := DecodeGoCoverageEnvelopeV1(want, 1<<20); err == nil {
		t.Fatal("v3 config accepted as old envelope")
	}
	old, err := EncodeGoCoverageEnvelopeV1(coverageEnvelopeFixture(t), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeGoCoverageRecord(old.CopyBytes(), 1<<20); err != nil {
		t.Fatal("old consumer path lost", err)
	}
	if _, err := EncodeGoCoverageEnvelopeV2(*d, len(want)-1); err == nil {
		t.Fatal("size bound lost")
	}
}

func TestGoCoverageV2RejectsUnknownAndChangedFacts(t *testing.T) {
	e := coverageEnvelopeV2Fixture(t)
	encoded, err := EncodeGoCoverageEnvelopeV2(e, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"progress_completed_at_unix_milli", "comparison_config", "receipt_identity", "record_type", "envelope_digest"} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded.CopyBytes(), &fields); err != nil {
			t.Fatal(err)
		}
		fields[field] = json.RawMessage(`null`)
		wire, _ := json.Marshal(fields)
		if _, err := DecodeGoCoverageRecord(wire, 1<<20); err == nil {
			t.Fatal("accepted null", field)
		}
	}
	for _, change := range []func(*GoCoverageEnvelopeV2){
		func(e *GoCoverageEnvelopeV2) { e.Config.Query.MetricMerge = "b-a" },
		func(e *GoCoverageEnvelopeV2) { e.Receipt.Input.SourceWindow.FromTime++ },
		func(e *GoCoverageEnvelopeV2) { e.Receipt.Levels[0].LevelID++ },
		func(e *GoCoverageEnvelopeV2) { e.CompletedAt = nil },
	} {
		e := coverageEnvelopeV2Fixture(t)
		change(&e)
		if _, err := EncodeGoCoverageEnvelopeV2(e, 1<<20); err == nil {
			t.Fatal("changed facts accepted")
		}
	}
	e.CompletedAt = nil
	e.Receipt.CoverageComplete = false
	e.Receipt.GapReasons = []string{"TERMINAL_TIME_UNKNOWN"}
	e.Receipt.Input.QueryAttempts.LogicalSlot = KnownCountV1{}
	unknown, err := EncodeGoCoverageEnvelopeV2(e, 1<<20)
	if err != nil {
		t.Fatal("unknown became success or unrepresentable", err)
	}
	view, err := DecodeGoCoverageRecord(unknown.CopyBytes(), 1<<20)
	if err != nil || view.CompletedAt != nil || view.Receipt.CoverageComplete {
		t.Fatal("unknown terminal changed", err)
	}
}
