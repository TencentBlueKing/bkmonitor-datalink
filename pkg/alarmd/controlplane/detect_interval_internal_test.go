// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"encoding/json"
	"testing"
)

func detectItem(value string, sources ...string) legacyItem {
	item := legacyItem{}
	if value != "" {
		item.DetectInterval = json.RawMessage(value)
	}
	if len(sources) == 0 {
		sources = []string{"bk_monitor/time_series"}
	}
	for _, source := range sources {
		label, kind := source, ""
		for index := range source {
			if source[index] == '/' {
				label, kind = source[:index], source[index+1:]
				break
			}
		}
		raw, _ := json.Marshal(map[string]any{"data_source_label": label, "data_type_label": kind, "agg_interval": 60})
		item.QueryConfigs = append(item.QueryConfigs, raw)
	}
	return item
}

// Every reading of a detect_interval against an aggregation interval of a
// minute: which step runs, whether anything compiles differently from an
// item without the field, and what it is noted under.
func TestADetectIntervalIsReadAgainstTheAggregationInterval(t *testing.T) {
	for _, test := range []struct {
		name       string
		item       legacyItem
		aggregate  int64
		step       int64
		configured bool
		warning    string
	}{
		{name: "absent", item: detectItem(""), aggregate: 60, step: 60},
		{name: "null", item: detectItem("null"), aggregate: 60, step: 60},
		{name: "equal", item: detectItem("60"), aggregate: 60, step: 60},
		{name: "dividing", item: detectItem("15"), aggregate: 60, step: 15, configured: true},
		{name: "longer", item: detectItem("120"), aggregate: 60, step: 120, configured: true, warning: ReasonDetectIntervalAboveAgg},
		{name: "not dividing", item: detectItem("45"), aggregate: 60, step: 45, configured: true, warning: ReasonDetectIntervalNotDivisible},
		{name: "below the floor", item: detectItem("5"), aggregate: 60, step: 10, configured: true, warning: ReasonDetectIntervalBelowFloor},
		{name: "below the floor onto the aggregation interval", item: detectItem("5"), aggregate: 10, step: 10, warning: ReasonDetectIntervalBelowFloor},
		{name: "custom time series", item: detectItem("15", "custom/time_series"), aggregate: 60, step: 15, configured: true},
		{name: "log source", item: detectItem("15", "bk_log_search/log"), aggregate: 60, step: 60, warning: ReasonDetectIntervalSourceNotSliding},
		{name: "computing platform", item: detectItem("15", "bk_data/time_series"), aggregate: 60, step: 60, warning: ReasonDetectIntervalSourceNotSliding},
		{name: "PromQL", item: detectItem("15", "prometheus/time_series"), aggregate: 60, step: 60, warning: ReasonDetectIntervalSourceNotSliding},
		{name: "mixed sources", item: detectItem("15", "bk_monitor/time_series", "bk_log_search/time_series"), aggregate: 60, step: 60, warning: ReasonDetectIntervalSourceNotSliding},
		{name: "a source that cannot slide, written as the aggregation interval", item: detectItem("60", "bk_log_search/log"), aggregate: 60, step: 60},
	} {
		t.Run(test.name, func(t *testing.T) {
			step, err := itemDetectStep(test.item, test.aggregate)
			if err != nil {
				t.Fatal(err)
			}
			if step.Seconds != test.step || step.Configured != test.configured || step.Warning != test.warning {
				t.Fatalf("step %+v, want seconds %d configured %t warning %q", step, test.step, test.configured, test.warning)
			}
			if disposition := step.warningDisposition("7", test.aggregate); (disposition != nil) != (test.warning != "") ||
				disposition != nil && (disposition.Disposition != DispositionConfigNormalized || disposition.Reason != test.warning || disposition.FieldPath != "items[0].detect_interval") {
				t.Fatalf("warning disposition %+v for %+v", disposition, step)
			}
		})
	}
	advanced := detectItem("15")
	advanced.Algorithms = []legacyAlgorithm{{Level: 1, Type: "Threshold"}, {Level: 2, Type: "AdvancedRingRatio"}}
	step, err := itemDetectStep(advanced, 60)
	if err != nil || step.Configured || step.Seconds != 60 || step.Warning != ReasonDetectIntervalAlgorithmNotSliding || step.Detail != "algorithm=AdvancedRingRatio" {
		t.Fatalf("an item a Level of which reads AdvancedRingRatio: %+v %v, want it run at the aggregation interval and the algorithm named", step, err)
	}
	for _, value := range []string{`0`, `-30`, `1.5`, `"30"`, `true`, `{}`} {
		if _, err := itemDetectStep(detectItem(value), 60); err == nil {
			t.Fatalf("detect_interval %s was accepted", value)
		}
	}
}

// The shift a configured Plan asks of the query service is the one it gives
// an aligned query itself - a step less a millisecond forward - composed with
// the clause's own time shift.
func TestASlidingClauseIsShiftedAStepLessAMillisecondForward(t *testing.T) {
	for _, test := range []struct {
		offset  string
		forward bool
		want    string
		wantFwd bool
	}{
		{offset: "", want: "299999ms", wantFwd: true},
		{offset: "3600s", forward: false, want: "3300001ms", wantFwd: false},
		{offset: "3600s", forward: true, want: "3899999ms", wantFwd: true},
		{offset: "299s", forward: false, want: "999ms", wantFwd: true},
		{offset: "300s", forward: false, want: "1ms", wantFwd: false},
	} {
		got, forward, err := slidingClauseOffset(test.offset, test.forward, 300)
		if err != nil || got != test.want || forward != test.wantFwd {
			t.Fatalf("offset %q forward %t: got %q forward %t err %v, want %q forward %t", test.offset, test.forward, got, forward, err, test.want, test.wantFwd)
		}
	}
	if _, _, err := slidingClauseOffset("an hour", false, 300); err == nil {
		t.Fatal("an unreadable clause offset was composed")
	}
	for _, test := range []struct{ step, aggregate, want int64 }{{15, 60, 15}, {120, 60, 60}, {60, 60, 60}, {0, 60, 60}} {
		if got := queryDelayUnit(test.step, test.aggregate); got != test.want {
			t.Fatalf("queryDelayUnit(%d, %d) = %d, want %d", test.step, test.aggregate, got, test.want)
		}
	}
}
