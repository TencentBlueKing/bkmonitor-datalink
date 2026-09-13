package strategy

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestTraditionalComparisonCompilerContracts(t *testing.T) {
	cases := []struct {
		kind   string
		params TraditionalComparisonParameters
	}{
		{DetectorKindSimpleYearRound, TraditionalComparisonParameters{Ceil: comparisonNumber("20")}},
		{DetectorKindAdvancedRingRatio, TraditionalComparisonParameters{Ceil: comparisonNumber("20"), CeilInterval: 3, FetchType: "last"}},
		{DetectorKindAdvancedYearRound, TraditionalComparisonParameters{Floor: comparisonNumber("20"), FloorInterval: 2, Ceil: comparisonNumber("30"), CeilInterval: 3, FetchType: "avg"}},
		{DetectorKindRingRatioAmplitude, TraditionalComparisonParameters{Ratio: comparisonNumber("-1"), Shock: comparisonNumber("-2"), Threshold: comparisonNumber("-3")}},
		{DetectorKindYearRoundAmplitude, TraditionalComparisonParameters{Days: 2, Method: "gte", Ratio: comparisonNumber("-1"), Shock: comparisonNumber("-2")}},
		{DetectorKindYearRoundRange, TraditionalComparisonParameters{Days: 2, Method: "lt", Ratio: comparisonNumber("-1"), Shock: comparisonNumber("-2")}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			ctx, raw := traditionalCompileFixture(t, tc.kind, tc.params)
			compiler := traditionalComparisonCompiler{tc.kind}
			compiled, err := compiler.Compile(context.Background(), ctx, raw)
			if err != nil {
				t.Fatal(err)
			}
			if compiled.Config.TraditionalComparison == nil {
				t.Fatal("missing config")
			}
			if tc.kind == DetectorKindAdvancedRingRatio && len(compiled.InputRequirements) != 2 {
				t.Fatal("continuous history was fanned out")
			}
			tests := []struct {
				name   string
				mutate func(map[string]any)
			}{
				{"precision", func(m map[string]any) { m["precision"] = 2 }},
				{"unit", func(m map[string]any) { m["data_unit"] = "unknown" }},
				{"unknown operand", func(m map[string]any) { m["typo"] = 1 }},
				{"query mismatch", func(m map[string]any) {
					rs := m["requirements"].([]any)
					rs[1].(map[string]any)["logical_query_ref"] = "other"
				}},
				{"wrong window", func(m map[string]any) {
					rs := m["requirements"].([]any)
					rs[1].(map[string]any)["relative_window"].(map[string]any)["start_offset_seconds"] = -1
				}},
				{"wrong readiness", func(m map[string]any) {
					rs := m["requirements"].([]any)
					rs[1].(map[string]any)["readiness_class"] = "EAGER"
				}},
			}
			if tc.params.Method != "" {
				tests = append(tests, struct {
					name   string
					mutate func(map[string]any)
				}{"neq", func(m map[string]any) { m["method"] = "neq" }})
			}
			for _, negative := range tests {
				t.Run(negative.name, func(t *testing.T) {
					var m map[string]any
					if err := json.Unmarshal(raw.Config, &m); err != nil {
						t.Fatal(err)
					}
					negative.mutate(m)
					payload, _ := json.Marshal(m)
					changed := raw
					changed.Config = payload
					if _, err := compiler.Compile(context.Background(), ctx, changed); err == nil {
						t.Fatal("invalid config accepted")
					}
				})
			}
			if len(compiled.InputRequirements) > 2 || tc.kind == DetectorKindAdvancedRingRatio {
				ctx.Limits.MaxRequiredHistoryPoints = 1
				if _, err = compiler.Compile(context.Background(), ctx, raw); err == nil {
					t.Fatal("history budget bypassed")
				}
			}
		})
	}
}

func comparisonNumber(value string) *json.Number { n := json.Number(value); return &n }

func traditionalCompileFixture(t *testing.T, kind string, params TraditionalComparisonParameters) (AlgorithmCompileContext, contract.AlgorithmIRV2) {
	t.Helper()
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	primary := AlgorithmInputRequirement{RequirementID: strings.Repeat("0", 64), DatasetName: "primary", Role: AlgorithmInputPrimary, ConsumerLevelID: 1, LogicalQueryRef: "query", RelativeWindow: AlgorithmRelativeWindow{-60, 0, true}, StepMillis: 60000, AlignmentMillis: 60000, ReadinessClass: AlgorithmReadinessEager, InputProjection: projection}
	requirements := []AlgorithmInputRequirement{primary}
	offsets, err := TraditionalHistoryOffsets(kind, params, 60)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range TraditionalHistoryGroups(kind, offsets) {
		r := primary
		r.Role = AlgorithmInputDependency
		r.DatasetName = TraditionalHistoryDataset(kind, group)
		r.RequirementID, _ = contract.DeriveCanonicalDigestV2("test", r.DatasetName)
		r.RelativeWindow = AlgorithmRelativeWindow{-(group[len(group)-1] + 60), -group[0], true}
		r.ReadinessClass = AlgorithmReadinessFinalizedRequired
		r.PointOffsetsSeconds = group
		for _, offset := range group {
			r.NamedPoints = append(r.NamedPoints, AlgorithmNamedInputPoint{TraditionalHistoryName(offset), offset})
		}
		requirements = append(requirements, r)
	}
	payload, _ := json.Marshal(params)
	var config map[string]any
	json.Unmarshal(payload, &config)
	config["data_unit"] = "short"
	config["algorithm_unit"] = ""
	config["precision"] = 6
	config["input_projection"] = projection
	config["requirements"] = requirements
	payload, _ = json.Marshal(config)
	ctx := AlgorithmCompileContext{Projection: contract.InputProjectionV2{ValueFields: projection.ValueFields}, IdentityFields: projection.IdentityFields, ExecutionSemantics: contract.ExecutionSemanticsV2{AggregationInterval: 60}, Limits: Limits{MaxRequiredHistoryPoints: 4096}}
	return ctx, contract.AlgorithmIRV2{Type: kind, Version: 1, Config: payload}
}

func TestTraditionalComparisonHistoryBounds(t *testing.T) {
	for _, test := range []struct {
		count    int
		interval int64
	}{{4097, 60}, {-1, 60}, {2, math.MaxInt64}} {
		if _, err := TraditionalHistoryOffsets(DetectorKindAdvancedRingRatio, TraditionalComparisonParameters{Ceil: comparisonNumber("1"), CeilInterval: test.count}, test.interval); err == nil {
			t.Fatal("unbounded history accepted")
		}
	}
	offsets, err := TraditionalHistoryOffsets(DetectorKindAdvancedRingRatio, TraditionalComparisonParameters{Ceil: comparisonNumber("1"), CeilInterval: 4096}, 60)
	if err != nil || len(offsets) != 4096 {
		t.Fatalf("bounded maximum %d, %v", len(offsets), err)
	}
}
