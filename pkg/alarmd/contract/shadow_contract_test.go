// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package contract

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func shadowTestContext() ShadowContextV1 {
	d := strings.Repeat("a", 64)
	return ShadowContextV1{ComparisonConfigDigest: d, PlanScheduleRevision: "plan-schedule", EvaluationTime: 60, SlotIdentity: "slot", SnapshotRevision: "snapshot", QueryRevision: "query", QueryGroupScheduleRevision: "qg-schedule", DuePlanSetDigest: d, EffectiveTimeRequirementDigest: d, EffectiveTimeFactDigest: d}
}

func shadowTestReceipt() ChainCoverageReceiptV1 {
	zero, one := KnownShadowCountV1(0), KnownShadowCountV1(1)
	return ChainCoverageReceiptV1{Schema: Schema{Name: ChainCoverageReceiptSchemaV1, Major: 1}, RequiredFeatures: []string{}, RecordType: ShadowCoverage, EpochID: "clean-epoch", Chain: ShadowGo, TenantID: "tenant", BusinessID: "1", StrategyID: "2", Context: shadowTestContext(),
		Input:   ShadowInputCoverageV1{QueryAttempts: ShadowAttemptObservationV1{ExecutionRef: "execution", CurrentExecution: one, LogicalSlot: one, ObservedFromSlotStart: true, ContinuousThroughTerminal: true}, Completion: "FULL", Series: one, Records: one, SelectedPlanRecords: one, ResultBytesDigest: strings.Repeat("b", 64)},
		Records: ShadowRecordOutcomesV1{PrimaryAbnormal: zero, PrimaryRecovery: zero, NoEvent: one, Excluded: zero, Unavailable: zero, Terminal: zero},
		Levels:  []ShadowLevelCoverageV1{{LevelID: 5, Selected: one, Normal: one, Abnormal: zero, Recovery: zero, Unavailable: zero, Terminal: zero, Excluded: zero, PythonShortCircuited: zero, PartialAcceptedAbnormal: zero, SuppressedAbnormal: zero, Primary: one, SiblingDiagnostic: zero, LateAfterComplete: zero}}, PhysicalProduced: zero, PhysicalACKed: zero, TerminalFact: true, TerminalFactRef: "progress", CoverageComplete: true, GapReasons: []string{}, ReasonCounts: []ReasonCountV1{}}
}

func TestShadowReceiptKnownScopeAndConservation(t *testing.T) {
	clean := shadowTestReceipt()
	if err := ValidateChainCoverageReceiptV1(&clean); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*ChainCoverageReceiptV1)
	}{
		{"unknown cannot pass", func(r *ChainCoverageReceiptV1) { r.Input.QueryAttempts.LogicalSlot = KnownCountV1{} }},
		{"known requires start", func(r *ChainCoverageReceiptV1) { r.Input.QueryAttempts.ObservedFromSlotStart = false }},
		{"normal is not recovery", func(r *ChainCoverageReceiptV1) { r.Levels[0].Recovery = KnownShadowCountV1(1) }},
		{"Level count is not envelope", func(r *ChainCoverageReceiptV1) { r.PhysicalProduced = KnownShadowCountV1(2) }},
		{"partial cannot normal", func(r *ChainCoverageReceiptV1) { r.Input.Completion = "PARTIAL" }},
		{"unknown cannot contain zero", func(r *ChainCoverageReceiptV1) { r.Input.Records.Known = false }},
		{"subset cannot exceed abnormal", func(r *ChainCoverageReceiptV1) { r.Levels[0].SuppressedAbnormal = KnownShadowCountV1(1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := shadowTestReceipt()
			tt.change(&r)
			if err := ValidateChainCoverageReceiptV1(&r); err == nil {
				t.Fatal("accepted invalid coverage")
			}
		})
	}
	unknown := shadowTestReceipt()
	unknown.Input.QueryAttempts.LogicalSlot = KnownCountV1{}
	unknown.CoverageComplete = false
	unknown.GapReasons = []string{"ATTEMPT_HISTORY_UNKNOWN"}
	if err := ValidateChainCoverageReceiptV1(&unknown); err != nil {
		t.Fatal("unknown gap must remain representable", err)
	}
	// Orthogonal subsets overlap without entering the Level outcome sum.
	partial := shadowTestReceipt()
	partial.Input.Completion = "PARTIAL"
	partial.Levels[0].Normal = KnownShadowCountV1(0)
	partial.Levels[0].Abnormal = KnownShadowCountV1(1)
	partial.Levels[0].PartialAcceptedAbnormal = KnownShadowCountV1(1)
	partial.Levels[0].SuppressedAbnormal = KnownShadowCountV1(1)
	if err := ValidateChainCoverageReceiptV1(&partial); err != nil {
		t.Fatal(err)
	}
}

func TestShadowRecordStrictClassification(t *testing.T) {
	payload, err := os.ReadFile("testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := DecodeShadowResultRecordV1(payload, 1<<20)
	if err != nil || r.Kind != ShadowNativeEvent {
		t.Fatalf("native: %v %v", r.Kind, err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatal(err)
	}
	object["record_type"] = json.RawMessage(`"FINAL_RESULT_EVIDENCE"`)
	bad, _ := json.Marshal(object)
	if _, err := DecodeShadowResultRecordV1(bad, 1<<20); err == nil {
		t.Fatal("type/schema conflict accepted")
	}
	receipt := shadowTestReceipt()
	encoded, err := EncodeChainCoverageReceiptV1(&receipt, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r, err = DecodeShadowResultRecordV1(encoded, 1<<20)
	if err != nil || r.Kind != ShadowCoverage {
		t.Fatal(err)
	}
	if _, err := DecodeShadowResultRecordV1(encoded, len(encoded)-1); err == nil {
		t.Fatal("unbounded decode")
	}
	if _, err := DecodeShadowResultRecordV1(append(encoded, encoded...), 1<<20); err == nil {
		t.Fatal("trailing record accepted")
	}
	if err := json.Unmarshal([]byte(`{"value":0}`), new(KnownCountV1)); err == nil {
		t.Fatal("missing known accepted")
	}
	if err := json.Unmarshal([]byte(`{"known":false,"value":0}`), new(KnownCountV1)); err == nil {
		t.Fatal("unknown zero accepted")
	}
}

func TestShadowDecimalNormalization(t *testing.T) {
	for input, want := range map[string]string{"01.00": "1", "-0.00": "0", "12.3400": "12.34", "9007199254740993.00": "9007199254740993"} {
		got, err := NormalizeShadowDecimalV1(input)
		if err != nil || got != want {
			t.Fatalf("%s: %s %v", input, got, err)
		}
	}
	for _, input := range []string{"NaN", "1e3", "1/3", "+1", ""} {
		if _, err := NormalizeShadowDecimalV1(input); err == nil {
			t.Fatal("invalid decimal accepted", input)
		}
	}
}

func TestShadowComparisonCanonicalGolden(t *testing.T) {
	payload, err := os.ReadFile("testdata/shadow-final-v1/comparison_config.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Vectors []struct {
			Name      string             `json:"name"`
			Input     ComparisonConfigV1 `json:"canonical_input"`
			Canonical string             `json:"expected_canonical_utf8"`
			Digest    string             `json:"expected_sha256"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	digests := map[string]bool{}
	for _, v := range fixture.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			b, d, err := CanonicalComparisonConfigV1(v.Input)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != v.Canonical || d != v.Digest {
				t.Fatalf("golden mismatch: %s", b)
			}
			digests[d] = true
			input := v.Input
			input.Levels = append([]ShadowLevelConfigV1(nil), input.Levels...)
			input.Levels[0], input.Levels[1] = input.Levels[1], input.Levels[0]
			_, again, err := CanonicalComparisonConfigV1(input)
			if err != nil || again != d {
				t.Fatal("unordered Level input changed digest", err)
			}
		})
	}
	if len(digests) != 3 {
		t.Fatal("sibling config or order changes did not change digest")
	}
	c := fixture.Vectors[0].Input
	c.Levels[0].Detectors[0].Threshold = "1.00"
	_, d, err := CanonicalComparisonConfigV1(c)
	if err != nil || d != fixture.Vectors[0].Digest {
		t.Fatal("decimal normalization mismatch", err)
	}
	c.Numeric.Rounding = ""
	if _, _, err := CanonicalComparisonConfigV1(c); err == nil {
		t.Fatal("unknown numeric semantics accepted")
	}
}
