// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A host detected every fifteen seconds over one-minute windows that
// reboots after two minutes without data is anomalous for the first minute
// of its uptime only, as the Python detector counts (os_restart.py:
// previous is the point agg_interval back, "(previous is None or value <
// previous) and (is_ok_10minute_ago or is_ok_25minute_ago)"): at uptime 15
// to 60 the point a minute back is missing, from 75 on it is lower. A
// previous point a step back would stop after one detection; one never read
// would run all ten minutes.
func TestAnOsRestartDetectedFasterThanItAggregatesIsAnomalousForTheFirstMinute(t *testing.T) {
	const boot = int64(1_800_000_000)
	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	levels := compileSteppedNamedInputPlan(t, strategy.DetectorKindOsRestart, map[string]any{}, projection, 15).Levels()
	if levels.Len() != 1 {
		t.Fatalf("compiled %d levels, want its one level", levels.Len())
	}
	algorithm := levels.At(0).Algorithms().At(0)
	uptime := func(at int64) (string, bool) {
		switch {
		case at > boot:
			return strconv.FormatInt(at-boot, 10), true
		case at < boot-120:
			return strconv.FormatInt(at-boot+864000, 10), true
		}
		return "", false
	}
	record := func(at int64, value string) contract.CanonicalRecordV2 {
		r := namedRecord(t, at, value, nil)
		r.RecordID = fmt.Sprintf("%064x", at)
		return r
	}
	var anomalous []int64
	for at := boot + 15; at <= boot+660; at += 15 {
		current, _ := uptime(at)
		var history []contract.CanonicalRecordV2
		for back := int64(15); back <= 1500; back += 15 {
			if value, ok := uptime(at - back); ok {
				history = append(history, record(at-back, value))
			}
		}
		bindings, primary := namedBindings(t, algorithm.InputRequirements(), map[string][]contract.CanonicalRecordV2{
			"primary": {record(at, current)}, "uptime_history": history})
		got := osRestartDetector{}.Evaluate(context.Background(), algorithm, execution.SeriesEvaluationInputRequest{Inputs: bindings}, primary)
		if got.result == FactResultAnomalous {
			anomalous = append(anomalous, at-boot)
		}
	}
	if want := []int64{15, 30, 45, 60}; !reflect.DeepEqual(anomalous, want) {
		t.Fatalf("anomalous at uptime %v, want %v", anomalous, want)
	}
}

// An item detected every fifteen seconds over one-minute windows compares
// its window with the previous one, a minute back, as the Python detector
// does (simple_ring_ratio.py get_history_offsets [agg_interval],
// ring_ratio_amplitude.py inherits it; year_round_amplitude.py reads
// i*86400 and i*86400+agg_interval). The windows fifteen seconds back
// overlap the ones compared by three quarters; delivered beside them, they
// are not what the comparison reads.
func TestARingComparisonDetectedFasterThanItAggregatesComparesWithThePreviousWindow(t *testing.T) {
	const at = int64(864000)
	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	traditional := func(kind string) func(strategy.CompiledAlgorithmPlan, execution.SeriesEvaluationInputRequest, execution.RecordView) namedAlgorithmResult {
		return func(a strategy.CompiledAlgorithmPlan, in execution.SeriesEvaluationInputRequest, p execution.RecordView) namedAlgorithmResult {
			return traditionalComparisonDetector{kind}.Evaluate(context.Background(), a, in, p)
		}
	}
	for _, test := range []struct {
		name     string
		kind     string
		config   map[string]any
		evaluate func(strategy.CompiledAlgorithmPlan, execution.SeriesEvaluationInputRequest, execution.RecordView) namedAlgorithmResult
		current  float64
		// history by seconds back: the previous windows make the point
		// anomalous, the overlapping ones would not.
		history map[int64]float64
	}{
		{name: "simple ring ratio, a drop of 60 % against a floor of 50 %", kind: strategy.DetectorKindSimpleRingRatio,
			config: map[string]any{"floor": 50, "ceil": nil},
			evaluate: func(a strategy.CompiledAlgorithmPlan, in execution.SeriesEvaluationInputRequest, p execution.RecordView) namedAlgorithmResult {
				return simpleRingRatioDetector{}.Evaluate(context.Background(), a, in, p)
			}, current: 40, history: map[int64]float64{60: 100, 15: 60}},
		{name: "ring ratio amplitude, |250 - 100| >= 100 x 1 + 0", kind: strategy.DetectorKindRingRatioAmplitude,
			config:   map[string]any{"ratio": 1, "shock": 0, "threshold": 0, "data_unit": "short", "algorithm_unit": "", "precision": 6},
			evaluate: traditional(strategy.DetectorKindRingRatioAmplitude), current: 250, history: map[int64]float64{60: 100, 15: 200}},
		{name: "year round amplitude, |250 - 100| >= |100 - 90| x 10 + 0 on both days", kind: strategy.DetectorKindYearRoundAmplitude,
			config:   map[string]any{"days": 2, "method": "gte", "ratio": 10, "shock": 0, "data_unit": "short", "algorithm_unit": "", "precision": 6},
			evaluate: traditional(strategy.DetectorKindYearRoundAmplitude), current: 250,
			history: map[int64]float64{60: 100, 15: 200, 86400: 100, 86460: 90, 86415: 90, 172800: 100, 172860: 90, 172815: 90}},
	} {
		t.Run(test.name, func(t *testing.T) {
			levels := compileSteppedNamedInputPlan(t, test.kind, test.config, projection, 15).Levels()
			if levels.Len() != 1 {
				t.Fatalf("compiled %d levels, want its one level reading the previous window", levels.Len())
			}
			algorithm := levels.At(0).Algorithms().At(0)
			record := func(back int64, v float64) contract.CanonicalRecordV2 {
				r := namedRecord(t, at-back, strconv.FormatFloat(v, 'g', -1, 64), nil)
				r.RecordID = fmt.Sprintf("%064x", at-back)
				return r
			}
			records := map[string][]contract.CanonicalRecordV2{"primary": {record(0, test.current)}}
			for _, requirement := range algorithm.InputRequirements() {
				if requirement.Role != strategy.AlgorithmInputDependency {
					continue
				}
				for back, v := range test.history {
					records[requirement.DatasetName] = append(records[requirement.DatasetName], record(back, v))
				}
			}
			bindings, primary := namedBindings(t, algorithm.InputRequirements(), records)
			if got := test.evaluate(algorithm, execution.SeriesEvaluationInputRequest{Inputs: bindings}, primary); got.result != FactResultAnomalous {
				t.Fatalf("result %+v, want anomalous against the previous window", got)
			}
		})
	}
}
