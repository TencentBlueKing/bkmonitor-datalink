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
	"reflect"
	"testing"
)

// The missing-minute union crosses only in the shape the evaluator builds it.
// Any other shape is dropped and marked truncated, and the rest of the set is
// kept: the union only ever speaks for windows the set does not name, and
// truncated it says nothing about them, which is what it said before it was
// sent. A run with nothing short carries no union at all.
func TestTheMissingMinutesCrossOnlyInTheEvaluatorsShape(t *testing.T) {
	for name, tc := range map[string]struct {
		minutes       []int64
		short         uint32
		shortUnusable uint32
		wantMinutes   []int64
		wantTruncated bool
	}{
		"sorted, within the round":         {[]int64{120, 180, 240}, 3, 0, []int64{120, 180, 240}, false},
		"out of order":                     {[]int64{180, 120}, 3, 0, nil, true},
		"a repeat":                         {[]int64{120, 120}, 3, 0, nil, true},
		"after the round's own minute":     {[]int64{120, 999}, 3, 0, nil, true},
		"not a minute":                     {[]int64{0, 120}, 3, 0, nil, true},
		"more unusable windows than short": {[]int64{120}, 3, 4, nil, true},
		"nothing short":                    {[]int64{120}, 0, 0, nil, false},
	} {
		facts := HistoryCoverageFacts{Levels: 5, Short: tc.short, WorstValid: 1, WorstRequired: 9, End: 540,
			MissingMinutes: tc.minutes, ShortUnusable: tc.shortUnusable}
		kept, rejected := normalizeHistoryCoverageFacts(&facts)
		if rejected != nil || kept == nil {
			t.Fatalf("%s: the set was refused (%v) over its missing minutes; it should have been kept", name, rejected)
		}
		if !reflect.DeepEqual(kept.MissingMinutes, tc.wantMinutes) || kept.MissingMinutesTruncated != tc.wantTruncated {
			t.Errorf("%s: minutes %v truncated %v, want %v truncated %v", name, kept.MissingMinutes, kept.MissingMinutesTruncated,
				tc.wantMinutes, tc.wantTruncated)
		}
	}
	over := make([]int64, MaxHistoryMissingMinutes+1)
	for i := range over {
		over[i] = int64(60 * (i + 1))
	}
	facts := HistoryCoverageFacts{Levels: 5, Short: 3, WorstValid: 1, WorstRequired: 9, End: over[len(over)-1], MissingMinutes: over}
	if kept, _ := normalizeHistoryCoverageFacts(&facts); kept == nil || kept.MissingMinutes != nil || !kept.MissingMinutesTruncated {
		t.Errorf("a union over the bound crossed as %+v, want it dropped and marked truncated", kept)
	}
}
