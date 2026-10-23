// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "testing"

// The window start crosses only as a minute no later than the round's own:
// any other is dropped and the set kept, and the reader then keeps rounds as
// it would for a worker that sends none.
func TestTheWindowStartCrossesOnlyAsAMinuteNoLaterThanTheRound(t *testing.T) {
	for name, tc := range map[string]struct {
		start, end, want int64
	}{
		"before the round's minute": {420, 540, 420},
		"at the round's minute":     {540, 540, 540},
		"after the round's minute":  {600, 540, 0},
		"no round minute":           {420, 0, 0},
		"not a minute":              {-60, 540, 0},
	} {
		facts := HistoryCoverageFacts{Levels: 5, End: tc.end, WindowStart: tc.start}
		kept, rejected := normalizeHistoryCoverageFacts(&facts)
		if rejected != nil || kept == nil {
			t.Fatalf("%s: the set was refused (%v) over its window start; it should have been kept", name, rejected)
		}
		if kept.WindowStart != tc.want {
			t.Errorf("%s: window start %d, want %d", name, kept.WindowStart, tc.want)
		}
	}
}
