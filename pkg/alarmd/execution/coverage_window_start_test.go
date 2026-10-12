// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "testing"

// The window start of a run is the oldest any of its windows reaches back
// to: the least over what each window said, a window that said nothing left
// out, and the same across a Merge of two runs' coverage.
func TestTheWindowStartIsTheOldestAnyWindowReachesBackTo(t *testing.T) {
	var coverage HistoryCoverage
	for _, start := range []int64{600, 0, 420, 540, -60} {
		coverage.ObserveWindowStart(start)
	}
	if coverage.WindowStart != 420 {
		t.Fatalf("window start = %d, want 420, the oldest a window said", coverage.WindowStart)
	}
	other := HistoryCoverage{Levels: 1, WindowStart: 300}
	coverage.Merge(other)
	if coverage.WindowStart != 300 {
		t.Fatalf("after a merge window start = %d, want 300, the older of the two", coverage.WindowStart)
	}
	coverage.Merge(HistoryCoverage{Levels: 1})
	if coverage.WindowStart != 300 {
		t.Fatalf("a merge whose other side said nothing moved the window start to %d", coverage.WindowStart)
	}
}
