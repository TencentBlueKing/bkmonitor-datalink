// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The replay guard estimates when access will read the Slot. Access reads a
// group at its own moment inside the settling spread, so the estimate has to
// be the same moment, or the guard lets through a replay that access then
// holds past its distance.
func TestTheReplayGuardEstimatesTheGroupsOwnReadMoment(t *testing.T) {
	const slot = execution.EvaluationTime(1_700_124_000)
	source, _ := replayClassificationSource(t, 60, slot, 30*time.Second, testRecoveryLimits())
	deadline := int64(slot)*1000 + 55_000
	for _, hold := range []int64{0, 20_000} {
		want := int64(slot)*1000 + execution.ReadWaitWithinQueryBudget("query-group-1", 55*time.Second, 30*time.Second).Milliseconds() + hold
		if got := source.replayReadyAtUnixMilli(slot, deadline+hold, hold); got != want {
			t.Fatalf("hold %d: estimated ready %d, access reads at %d", hold, got, want)
		}
	}
	if phase := execution.ReadWaitWithinQueryBudget("query-group-1", 55*time.Second, 30*time.Second) - execution.MinimumSettlingWait; phase <= 0 {
		t.Fatalf("query-group-1 has no phase on a 60s schedule, so this test cannot tell the estimates apart")
	}
}
