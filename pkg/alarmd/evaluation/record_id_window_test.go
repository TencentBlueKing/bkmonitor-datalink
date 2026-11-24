// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// derivedRecords is points of one series, latest first so the evaluation
// sorts them, each id the one derived from the series and its time, as the
// UQ decoder makes them: the Dataset keeps no ids.
func derivedRecords(t *testing.T, points int) []contract.CanonicalRecordV2 {
	t.Helper()
	series := strings.Repeat("c", 64)
	records := make([]contract.CanonicalRecordV2, points)
	for index := range records {
		at := int64(100 + (points-1-index)*60)
		id, err := contract.DeriveRecordIDV2(series, at)
		if err != nil {
			t.Fatal(err)
		}
		records[index] = contract.CanonicalRecordV2{RecordID: id, SourceTime: at, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: series},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`80`)},
			Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 100}
	}
	return records
}

// An evaluation inside the window of its series reads every id - the sort
// of its records, each point's trigger, the outcomes, the State points and
// the events - from the window's buffer and derives none: not one per
// comparison, not one per reader. The same evaluation outside a window
// gives the same result and is counted, which is what shows a reader that
// is not inside one.
func TestAnEvaluationInItsWindowDerivesNoID(t *testing.T) {
	records := derivedRecords(t, 6)
	evaluate := func(window bool) (execution.EvaluationResult, uint64) {
		request := requestFixtureForPlan(t, compiled(t), records, nil)
		dataset := request.Inputs[0].Inputs[0].Dataset
		if dataset.RecordIDWindowBytes() == 0 {
			t.Fatal("the fixture's Dataset keeps its ids; want them derived")
		}
		if window {
			if _, err := dataset.MaterializeRecordIDs(); err != nil {
				t.Fatal(err)
			}
			defer dataset.DropRecordIDs()
		}
		_, before := execution.DatasetRecordIDCounts()
		result, err := newEvaluator(t).Evaluate(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if err := result.Validate(request); err != nil {
			t.Fatal(err)
		}
		_, after := execution.DatasetRecordIDCounts()
		return result, after - before
	}
	inside, derivedInside := evaluate(true)
	if derivedInside != 0 {
		t.Fatalf("an evaluation in its window derived %d ids outside it, want none", derivedInside)
	}
	outside, derivedOutside := evaluate(false)
	if derivedOutside == 0 {
		t.Fatal("an evaluation outside a window derived no id; the count would not show a reader there")
	}
	if !reflect.DeepEqual(inside.Plans[0].LevelOutcomes, outside.Plans[0].LevelOutcomes) {
		t.Fatal("the outcomes differ in and out of a window")
	}
	outcomes := inside.Plans[0].LevelOutcomes
	if len(outcomes) != len(records) {
		t.Fatalf("%d outcomes, want one a record", len(outcomes))
	}
	for index, outcome := range outcomes {
		want := records[len(records)-1-index]
		if outcome.Record.RecordID != want.RecordID || outcome.Record.SourceTime != want.SourceTime {
			t.Fatalf("outcome %d anchored at %+v, want the record at %d, %s, in source order", index, outcome.Record, want.SourceTime, want.RecordID)
		}
	}
}
