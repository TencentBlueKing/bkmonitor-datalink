// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// recordedSource answers SourceAlive with alive and records every call.
type recordedSource struct {
	alive   bool
	asked   []int64
	last    []int64
	dueNext int
}

func (source *recordedSource) SourceAlive(_ uint32, spanSeconds, lastRealPoint int64) bool {
	source.asked = append(source.asked, spanSeconds)
	source.last = append(source.last, lastRealPoint)
	return source.alive
}

func (source *recordedSource) DueNext(_ uint32, lastRealPoint, _ int64) {
	source.dueNext++
	source.last = append(source.last, lastRealPoint)
}

// A Level written to end on no data asks its source on the round its end is
// due and on no other: not shown alive, it stays NORMAL, withheld because
// the source is unknown; alive, it ends as it did. The round before is told
// the end is due next. N = 1, T = 1, R = 4: due on round 6, (R + T + 1) = 6
// minutes after the last real point.
func TestNoDataEndsALevelOnlyWhenItsSourceIsAlive(t *testing.T) {
	level := levelV2(5, 1, 1, 1, 4, nil)
	level.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": 4,
		"missing_data": contract.RecoveryMissingDataEvaluateAsZero})
	plan := compilePlanV2(t, []contract.LevelIRV2{level})
	points := []countPoint{{"5.000000", true}}
	for range 6 {
		points = append(points, countPoint{"0.000000", false})
	}
	base := int64(1000 * 60)
	recovery := func(result EvaluationResultV2) contract.RecoveryWindowEvidenceV1 {
		return result.LevelOutcomes[0].DecisionWindow.Recovery
	}
	for _, alive := range []bool{false, true} {
		source := &recordedSource{alive: alive}
		got, evaluated := evaluateCountsAsking(t, plan, points, true, nil, base, source)
		if len(source.asked) != 1 || source.asked[0] != 360 {
			t.Fatalf("alive %t: asked %v, want once, on the due round, with the 6-minute span", alive, source.asked)
		}
		if source.dueNext != 1 {
			t.Fatalf("alive %t: told due next %d times, want once", alive, source.dueNext)
		}
		// Both asked from the series' last real point, where its span begins.
		for _, last := range source.last {
			if last != base {
				t.Fatalf("alive %t: asked with last real point %v, want %d", alive, source.last, base)
			}
		}
		if alive {
			if got[6] != contract.LevelResultRecovery || recovery(evaluated[6]).EndedBy != contract.RecoveryEndedByNoData {
				t.Fatalf("alive: round 6 = %s %+v, want RECOVERY ended by no data", got[6], recovery(evaluated[6]))
			}
			continue
		}
		if got[6] != contract.LevelResultNormal || recovery(evaluated[6]).Withheld != contract.RecoveryWithheldSourceUnknown ||
			recovery(evaluated[6]).EndedBy != "" {
			t.Fatalf("unknown: round 6 = %s %+v, want NORMAL withheld as source_unknown", got[6], recovery(evaluated[6]))
		}
	}
}

// The quiet-group zero count path asks no source: a Level not written to end
// on no data never asks, whatever its zeros.
func TestAZeroCountWithoutNoDataEndAsksNoSource(t *testing.T) {
	level := levelV2(5, 1, 1, 1, 4, nil)
	level.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": 4})
	plan := compilePlanV2(t, []contract.LevelIRV2{level})
	points := []countPoint{{"5.000000", true}}
	for range 7 {
		points = append(points, countPoint{"0.000000", false})
	}
	source := &recordedSource{}
	got, _ := evaluateCountsAsking(t, plan, points, true, nil, int64(1000*60), source)
	if len(source.asked) != 0 || source.dueNext != 0 {
		t.Fatalf("asked %v, due next %d, want nothing (results %v)", source.asked, source.dueNext, got)
	}
}

// A no-data end is a RECOVERY like any other at the open-alert gate: it goes
// when its Level is the alert's current one, and is held when the alert is
// at another Level, as Python ends an alert only at its own Level.
// The Level here is 2; the alert is at Level 2, or at Level 1.
func TestANoDataEndGoesOnlyForTheLevelTheAlertIsAt(t *testing.T) {
	identity := &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
	level := levelV2(2, 2, 1, 1, 4, nil)
	level.RecoveryPlan.Config = mustJSONV2(map[string]any{"enabled": true, "consecutive_windows": 4,
		"missing_data": contract.RecoveryMissingDataEvaluateAsZero})
	plan := nativePlanV2(t, []contract.LevelIRV2{level}, identity)
	md5, err := contract.MonitorDedupeMD5("1001", "2", map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)}, *identity)
	if err != nil {
		t.Fatal(err)
	}
	points := []countPoint{{"5.000000", true}}
	for range 6 {
		points = append(points, countPoint{"0.000000", false})
	}
	base := int64(1000 * 60)
	for _, test := range []struct {
		name     string
		open     []uint32
		wantGate string
	}{
		{name: "the alert is at the ending Level: the end goes", open: []uint32{2}, wantGate: OpenAlertGatePassed},
		{name: "the alert is at a more severe Level: the end is held", open: []uint32{1, 2}, wantGate: OpenAlertGateHeldLevelNotOpen},
	} {
		t.Run(test.name, func(t *testing.T) {
			set := &openLevelSetFixture{openAlertSetFixture: openAlertSetFixture{members: map[string]bool{"default/1001/" + md5: true}}, open: test.open, known: true}
			_, evaluated := evaluateCountsWith(t, plan, points, true, nil, base, &recordedSource{alive: true},
				func(request *EvaluationRequestV2) { request.OpenAlerts = set })
			round := evaluated[6]
			if window := round.LevelOutcomes[0].DecisionWindow; window == nil || window.Recovery.EndedBy != contract.RecoveryEndedByNoData {
				t.Fatalf("round 6 outcome %+v, want the Level ended by no data", round.LevelOutcomes[0])
			}
			if round.RecoveryGate.OpenAlertGate != test.wantGate {
				t.Fatalf("round 6 gate %+v, want %s", round.RecoveryGate, test.wantGate)
			}
			if passed := test.wantGate == OpenAlertGatePassed; passed != (round.TriggerEvent != nil) || round.RecoveryGate.Held == passed {
				t.Fatalf("round 6 gate %+v, event %v: want an envelope only when the end goes", round.RecoveryGate, round.TriggerEvent != nil)
			}
		})
	}
}
