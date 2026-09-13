package detect

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestTraditionalComparisonPythonOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/remaining_algorithms_python.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			ID                  string              `json:"id"`
			Kind                string              `json:"kind"`
			Config              map[string]any      `json:"config"`
			Current             float64             `json:"current"`
			History             map[string]*float64 `json:"history"`
			AggregationInterval int64               `json:"aggregation_interval"`
			DataUnit            string              `json:"data_unit"`
			AlgorithmUnit       string              `json:"algorithm_unit"`
			Precision           int                 `json:"precision"`
			SourceType          string              `json:"source_type"`
			HistoryMode         string              `json:"history_mode"`
			QueryState          string              `json:"query_state"`
			Expected            struct {
				Status string `json:"status"`
			} `json:"expected"`
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			tc.Config["data_unit"], tc.Config["algorithm_unit"], tc.Config["precision"] = tc.DataUnit, tc.AlgorithmUnit, 6
			plan := compileNamedInputPlan(t, tc.Kind, tc.Config, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
			config, ok := plan.Levels()[0].Algorithms()[0].TraditionalComparisonConfig()
			if !ok {
				t.Fatal("missing config")
			}
			config.Precision = tc.Precision
			config.AggregationInterval = tc.AggregationInterval
			if tc.QueryState != "FULL" {
				algorithm := plan.Levels()[0].Algorithms()[0]
				const sourceTime = int64(864000)
				records := map[string][]contract.CanonicalRecordV2{
					"primary": {namedRecord(t, sourceTime, strconv.FormatFloat(tc.Current, 'g', -1, 64), nil)},
				}
				bindings, primary := namedBindings(t, algorithm.InputRequirements(), records)
				for i := range bindings {
					if bindings[i].Role != execution.InputRoleAlgorithmDependency {
						continue
					}
					bindings[i].Completeness = execution.CompletenessUnavailable
					if tc.QueryState == "PARTIAL" {
						bindings[i].Completeness = execution.CompletenessPartial
					}
				}
				got := (traditionalComparisonDetector{tc.Kind}).Evaluate(context.Background(), algorithm,
					execution.SeriesEvaluationInputRequest{Inputs: bindings}, primary)
				if got.result != FactResultUnavailable || tc.Expected.Status != "UNAVAILABLE" {
					t.Fatalf("provider %s produced %+v, Python %s", tc.QueryState, got, tc.Expected.Status)
				}
				return
			}
			history := map[int64]*float64{}
			offsets, err := strategy.TraditionalHistoryOffsets(tc.Kind, config.TraditionalComparisonParameters, tc.AggregationInterval)
			if err != nil {
				t.Fatal(err)
			}
			if tc.Kind == strategy.DetectorKindYearRoundAmplitude {
				offsets = append(offsets, 0)
			}
			for _, offset := range offsets {
				value := tc.History[strconv.FormatInt(offset, 10)]
				if tc.HistoryMode == "history_loader" && value != nil && *value == 0 {
					value = nil
				}
				if tc.HistoryMode == "history_loader" && value == nil && (tc.SourceType == "log" || tc.SourceType == "event") {
					v := float64(0)
					value = &v
				}
				history[offset] = value
			}
			status := evaluateTraditionalComparison(tc.Kind, config, tc.Current, history)
			got := string(status)
			if status == pureDetectionUnknown {
				got = "UNAVAILABLE"
			}
			if got != tc.Expected.Status {
				t.Fatalf("got %s, Python %s", got, tc.Expected.Status)
			}
		})
	}
}

func TestTraditionalNamedHistoryQualityAndBindingIdentity(t *testing.T) {
	config := map[string]any{"ceil": 20, "ceil_interval": 2, "fetch_type": "avg", "data_unit": "short", "algorithm_unit": "", "precision": 6}
	plan := compileNamedInputPlan(t, strategy.DetectorKindAdvancedYearRound, config, strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	algorithm := plan.Levels()[0].Algorithms()[0]
	for _, test := range []struct {
		name, value           string
		partial, wrongBinding bool
		want                  string
	}{
		{"FULL missing", "", false, false, FactResultAnomalous},
		{"FULL zero filtered", "0", false, false, FactResultAnomalous},
		{"FULL null filtered", "null", false, false, FactResultAnomalous},
		{"PARTIAL cannot use remaining point", "", true, false, FactResultUnavailable},
		{"same name other requirement cannot shadow", "", false, true, FactResultAnomalous},
	} {
		t.Run(test.name, func(t *testing.T) {
			records := map[string][]contract.CanonicalRecordV2{"primary": {namedRecord(t, 604800, "150", nil)}, "history_172800": {namedRecord(t, 432000, "100", nil)}}
			if test.value != "" {
				records["history_86400"] = []contract.CanonicalRecordV2{namedRecord(t, 518400, test.value, nil)}
			}
			bindings, primary := namedBindings(t, algorithm.InputRequirements(), records)
			if test.partial {
				bindings[1].Completeness = execution.CompletenessPartial
			}
			if test.wrongBinding {
				wrong := bindings[1]
				wrong.RequirementID = "other-algorithm"
				wrong.Completeness = execution.CompletenessPartial
				bindings = append([]execution.NamedInputBinding{wrong}, bindings...)
			}
			result := (traditionalComparisonDetector{algorithm.Kind()}).Evaluate(context.Background(), algorithm, execution.SeriesEvaluationInputRequest{Inputs: bindings}, primary)
			if result.result != test.want {
				t.Fatalf("result %+v, want %s", result, test.want)
			}
		})
	}
}
