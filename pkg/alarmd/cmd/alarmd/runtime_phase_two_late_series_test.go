// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
)

// The lookback's readings reach the fleet rows field for field, samples
// included: the suggestion and what it rests on travel together.
func TestTheLookbacksUnrecoveredLateSeriesReachTheFleetRows(t *testing.T) {
	since := time.Date(2026, 9, 29, 20, 0, 0, 0, time.UTC)
	past, missed := lateSeriesFacts(
		[]lookback.LatePastRoundReading{{QueryGroup: "qg-a", StepSeconds: 60, CurrentDelaySeconds: 60, SuggestedDelaySeconds: 300,
			Since: since, Samples: []lookback.LatePastRoundSample{{EvaluationTime: 600, Rung: "x3.5", SeenAgeSeconds: 210, OnTimeSeries: 40, LateSeries: 7, CrossedSeries: 4}}}},
		[]lookback.ResidualMissReading{{QueryGroup: "qg-b", Windows: 2, CrossedSeries: 5, Since: since,
			Samples: []lookback.ResidualMissSample{{EvaluationTime: 660, OnTimeSeries: 50, LateSeries: 9, AdmittedSeries: 3, CrossedSeries: 2}}}})
	wantPast := map[string]fleet.LatePastRoundFacts{"qg-a": {StepSeconds: 60, CurrentDelaySeconds: 60, SuggestedDelaySeconds: 300,
		Since: since, Samples: []fleet.LatePastRoundSample{{EvaluationTime: 600, Rung: "x3.5", SeenAgeSeconds: 210, OnTimeSeries: 40,
			LateSeries: 7, CrossedSeries: 4}}}}
	wantMissed := map[string]fleet.LateSeriesMissedFacts{"qg-b": {Windows: 2, CrossedSeries: 5, Since: since,
		Samples: []fleet.LateSeriesMissedSample{{EvaluationTime: 660, OnTimeSeries: 50, LateSeries: 9, AdmittedSeries: 3, CrossedSeries: 2}}}}
	if !reflect.DeepEqual(past, wantPast) || !reflect.DeepEqual(missed, wantMissed) {
		t.Fatalf("past %+v missed %+v", past, missed)
	}
	if lookbackLateSeries(nil) != nil {
		t.Fatal("a process without a lookback reported late series")
	}
}
