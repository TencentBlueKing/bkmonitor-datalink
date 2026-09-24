// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// The absence line renders every count under its own key, zeros included,
// with the horizon and the roster beside them. Asserted on the keys the log
// actually carries rather than on the struct: a field added to the struct
// and never rendered reads, from a log, exactly like a build that does not
// report it -- which is how the window coverage facts went unreadable.
func TestTheAbsenceLineRendersEveryCountUnderItsOwnKey(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	withheldObserver(t, &output).Observe(context.Background(), Observation{
		Component: ComponentEvaluation, Stage: StageNoDataDecided, Result: ResultSuccess,
		Trace: TraceFields{StrategyID: "4101", BusinessID: "7", QueryGroupKey: "qg-absence", EvaluationTime: 600},
		NoDataAbsence: &NoDataAbsenceFacts{
			Outcome: "EVALUATED", HorizonSeconds: 3600, HorizonSource: "STRATEGY", RosterSource: "TARGET_STATIC",
			Expected: 10, Present: 6, Absent: 2, Unavailable: 0, Dropped: 0, Expired: 1, Suppressed: 1,
			AbsentAges: NoDataAbsentAges{ThisRound: 0, UnderHour: 1, UnderDay: 0, DayOrMore: 1},
		},
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode absence log: %v; log=%s", err, output.String())
	}
	want := map[string]any{
		"strategy_id":             "4101",
		"no_data_outcome":         "EVALUATED",
		"no_data_horizon_seconds": float64(3600),
		"no_data_horizon_source":  "STRATEGY",
		"no_data_roster_source":   "TARGET_STATIC",
		"no_data_expected":        float64(10),
		"no_data_present":         float64(6),
		"no_data_absent":          float64(2),
		"no_data_unavailable":     float64(0),
		"no_data_dropped":         float64(0),
		"no_data_expired":         float64(1),
		"no_data_suppressed":      float64(1),
		// The ages, zeros included: the last bucket at zero is the reading
		// that a horizon has nothing to reach.
		"no_data_absent_this_round":  float64(0),
		"no_data_absent_under_hour":  float64(1),
		"no_data_absent_under_day":   float64(0),
		"no_data_absent_day_or_more": float64(1),
	}
	for field, value := range want {
		got, present := event[field]
		if !present {
			t.Fatalf("the line has no %q: a reader cannot tell 'none' from 'not reported'; event=%#v", field, event)
		}
		if got != value {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, got, value, event)
		}
	}
}
