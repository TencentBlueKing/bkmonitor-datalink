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
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Each sample has its own key beside it, in the same order and under the
// same bound, and a cutover that holds back fewer groups than the last one
// names only its own: a key left over from an earlier cutover would have an
// operator delete a timeline that is no longer held back.
func TestEverySampleHasItsOwnKeyFromTheLatestCutover(t *testing.T) {
	keyOf := func(qg execution.QueryGroupIdentity) string { return "p:schedule_timeline:" + string(qg) }
	held := func(n int) []BlockedQueryGroup {
		groups := make([]BlockedQueryGroup, n)
		for i := range groups {
			groups[i] = BlockedQueryGroup{QueryGroup: execution.QueryGroupIdentity(fmt.Sprintf("qg-%02d", i)), Reason: CutoverReasonOpenDigestMismatch}
		}
		return groups
	}
	aligned := func(counts *blockedCounts, want int) {
		t.Helper()
		if len(counts.samples) != want || len(counts.keys) != want {
			t.Fatalf("samples %d keys %d, want %d of each", len(counts.samples), len(counts.keys), want)
		}
		for i := range counts.samples {
			qg := fmt.Sprintf("qg-%02d", i)
			if counts.samples[i] != qg+":"+CutoverReasonOpenDigestMismatch || counts.keys[i] != keyOf(execution.QueryGroupIdentity(qg)) {
				t.Fatalf("sample %d is %q beside key %q", i, counts.samples[i], counts.keys[i])
			}
		}
	}
	counts := &blockedCounts{}
	counts.record(held(blockedSampleMax+4), "", 0, keyOf)
	aligned(counts, blockedSampleMax)
	counts.record(held(2), "", 0, keyOf)
	aligned(counts, 2)
	counts.record(nil, "", 0, keyOf)
	aligned(counts, 0)
}
