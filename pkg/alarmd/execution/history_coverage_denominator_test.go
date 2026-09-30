// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A record that summarised no window still merges, because it says why it
// summarised none. The short-circuit used to read Levels alone, which dropped
// exactly the counts that exist to explain a low Levels -- and dropped them
// hardest in the case they exist for, a round where every series was resumed
// or none of their State could be loaded and Levels is zero for all of them.
func TestMergeCarriesTheCountsThatExplainAZeroLevels(t *testing.T) {
	for name, testCase := range map[string]struct {
		into, other, want execution.HistoryCoverage
	}{
		"a resumed series into an empty fold": {
			execution.HistoryCoverage{}, execution.HistoryCoverage{Resumed: 1},
			execution.HistoryCoverage{Resumed: 1},
		},
		"a constrained series into an empty fold": {
			execution.HistoryCoverage{}, execution.HistoryCoverage{Constrained: 1},
			execution.HistoryCoverage{Constrained: 1},
		},
		"both kinds beside a summarised window": {
			execution.HistoryCoverage{Levels: 1, Resumed: 2}, execution.HistoryCoverage{Constrained: 3},
			execution.HistoryCoverage{Levels: 1, Resumed: 2, Constrained: 3},
		},
		"a summarised window into a fold that has only reasons": {
			execution.HistoryCoverage{Resumed: 4}, execution.HistoryCoverage{Levels: 2, Short: 1},
			execution.HistoryCoverage{Levels: 2, Short: 1, Resumed: 4},
		},
		// A record that says nothing at all still merges to nothing.
		"an empty record": {
			execution.HistoryCoverage{Levels: 3}, execution.HistoryCoverage{}, execution.HistoryCoverage{Levels: 3},
		},
	} {
		got := testCase.into
		got.Merge(testCase.other)
		if !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("%s: merged = %+v, want %+v", name, got, testCase.want)
		}
	}
}

// The whole point of the two counts, as one number: what the Slot handled.
// A round of 249 series that resumed 227 and evaluated 22 reconciles; a round
// that reports 22 and nothing else does not, and is the reading that used to
// render as a small healthy object.
func TestTheCountsReconcileAgainstTheSeriesTheRoundHandled(t *testing.T) {
	fold := execution.HistoryCoverage{}
	for i := 0; i < 227; i++ {
		fold.Merge(execution.HistoryCoverage{Resumed: 1})
	}
	for i := 0; i < 22; i++ {
		fold.Merge(execution.HistoryCoverage{Levels: 1})
	}
	if handled := fold.Levels + fold.Resumed + fold.Constrained; handled != 249 {
		t.Fatalf("levels %d + resumed %d + constrained %d = %d, want the 249 series the round handled",
			fold.Levels, fold.Resumed, fold.Constrained, handled)
	}
}
