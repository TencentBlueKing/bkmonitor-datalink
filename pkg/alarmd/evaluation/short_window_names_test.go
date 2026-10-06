// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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

// A short window is named: which series, how full, and which positions are
// empty. The counts beside it say "one of three windows is short by one";
// this says it is series c's, and the minute it is missing is 240 -- the
// question a reader has next, and the one that until now was answered from
// the logs by hand. Read from the same window the counts came from, so the
// named holes and the shortfall agree.
func TestEvaluationNamesTheShortWindowAndItsEmptyPositions(t *testing.T) {
	plan := compiledWindow(t, 3, 3)
	fingerprint := plan.Levels()[0].Fingerprints().Detect
	stored := []execution.StateHistoryPoint{{RecordID: strings.Repeat("d", 64), SourceTime: 180,
		Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: fingerprint, Result: execution.LevelFactNormal}}}}
	record := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}
	result, err := newEvaluator(t).Evaluate(context.Background(), requestFixtureForPlan(t, plan, record, stored))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	coverage := result.Plans[0].HistoryCoverage
	if coverage.Short != 1 || len(coverage.Windows) != 1 {
		t.Fatalf("short = %d, windows named = %d, want the one short window named", coverage.Short, len(coverage.Windows))
	}
	if coverage.End != 300 {
		t.Fatalf("end = %d, want the record's minute 300 on the run", coverage.End)
	}
	window := coverage.Windows[0]
	if window.Series != execution.SeriesIdentityDigest(strings.Repeat("c", 64)) || window.LevelID != 5 || window.End != 300 {
		t.Fatalf("window = %+v, want series c, Level 5, ending at the record's source time 300", window)
	}
	if window.Valid != coverage.WorstValid || window.Required != coverage.WorstRequired {
		t.Fatalf("window %d/%d and worst pair %d/%d disagree on one window", window.Valid, window.Required, coverage.WorstValid, coverage.WorstRequired)
	}
	if !reflect.DeepEqual(window.Missing, []int64{240}) || window.MissingTotal != 1 || window.UnusableTotal != 0 {
		t.Fatalf("holes = %+v, want the one minute no record arrived at, 240", window)
	}
	if window.MissingTotal+window.UnusableTotal != window.Required-window.Valid {
		t.Fatalf("named holes %d+%d do not add up to the shortfall %d", window.MissingTotal, window.UnusableTotal, window.Required-window.Valid)
	}
	if window.Guarded || window.GuardReason != "" || window.Fresh {
		t.Fatalf("window = %+v, want neither guarded nor fresh: the state was loaded and no guard holds", window)
	}
	// A window the record fills names nothing: the list is of holes.
	full, err := newEvaluator(t).Evaluate(context.Background(), requestFixtureForPlan(t, compiledWindow(t, 1, 1), record, nil))
	if err != nil {
		t.Fatalf("Evaluate() on a complete window error = %v", err)
	}
	if got := full.Plans[0].HistoryCoverage; got.Short != 0 || len(got.Windows) != 0 {
		t.Fatalf("a complete window was named as short: %+v", got.Windows)
	}
}

// The guard's own reason rides on the window it holds, and the series' state
// status rides as fresh. Both were round-level folds before: one reason for
// a round whose windows can be held under different guards, and a fresh
// count with no way to say which windows it counted.
func TestANamedWindowCarriesItsOwnGuardReasonAndFreshness(t *testing.T) {
	record := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("f", 64), SourceTime: 300, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)},
		Dimensions:        map[string]json.RawMessage{}, ReceivedTime: 300}}
	held := requestFixtureForPlan(t, compiledWindow(t, 2, 1), record, nil)
	held.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryGapped
	held.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonConfigDrift)
	held.State.Items[0].Levels[0].LastProcessedEventTime = 60
	result, err := newEvaluator(t).Evaluate(context.Background(), held)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	windows := result.Plans[0].HistoryCoverage.Windows
	if len(windows) != 1 || !windows[0].Guarded || windows[0].GuardReason != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("windows = %+v, want the one short window held under CONFIG_DRIFT", windows)
	}
	fresh := requestFixtureForPlan(t, compiledWindow(t, 2, 1), record, nil)
	fresh.State.Items[0].Status = execution.StateMissingWarming
	result, err = newEvaluator(t).Evaluate(context.Background(), fresh)
	if err != nil {
		t.Fatalf("Evaluate() on a series with no persisted state error = %v", err)
	}
	if windows := result.Plans[0].HistoryCoverage.Windows; len(windows) != 1 || !windows[0].Fresh {
		t.Fatalf("windows = %+v, want the one short window marked fresh", windows)
	}
}
