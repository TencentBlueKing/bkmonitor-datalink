// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A gap marker newer than the Slot writing to it is answered by what made it
// newer, on a real redis-server. A later Slot's marker -- a later evaluation
// time, at the Slot's epoch or a newer one -- is SUPERSEDED and left as it
// was. A newer epoch at a time no later than the Slot, and the tie of one
// moment broken by a larger Slot digest, stay STALE_VERSION, also untouched;
// and a marker that is older by epoch is written over whatever its time.
func TestAGapMarkerALaterSlotWroteIsAnsweredSuperseded(t *testing.T) {
	slot := execution.ApplyVersion{StateApplyEpoch: 2, EvaluationTime: 120, SlotDigest: "slot-m"}
	for _, test := range []struct {
		name   string
		stored execution.ApplyVersion
		want   execution.GapGuardApplyStatus
	}{
		{"a later Slot of the same activation", execution.ApplyVersion{StateApplyEpoch: 2, EvaluationTime: 180, SlotDigest: "slot-a"}, execution.GapGuardSuperseded},
		{"a later Slot of a newer activation", execution.ApplyVersion{StateApplyEpoch: 3, EvaluationTime: 180, SlotDigest: "slot-a"}, execution.GapGuardSuperseded},
		{"a newer activation at the same time", execution.ApplyVersion{StateApplyEpoch: 3, EvaluationTime: 120, SlotDigest: "slot-m"}, execution.GapGuardStale},
		{"a newer activation at an earlier time", execution.ApplyVersion{StateApplyEpoch: 3, EvaluationTime: 60, SlotDigest: "slot-a"}, execution.GapGuardStale},
		{"the same moment with a larger digest", execution.ApplyVersion{StateApplyEpoch: 2, EvaluationTime: 120, SlotDigest: "slot-z"}, execution.GapGuardStale},
		{"an older activation at a later time", execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 180, SlotDigest: "slot-a"}, execution.GapGuardApplied},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := startNoDataServer(t)
			store := server.store(t)
			identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
			open := func(version execution.ApplyVersion, revision uint64) execution.GapGuardApplyItemResult {
				t.Helper()
				mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{Identity: identity, ExpectedMarkerRevision: revision,
					ApplyVersion: version, ScheduleRevision: "plan-r1",
					Scopes: []execution.GapScopeMutation{{Kind: execution.GapOpen,
						ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 2}}})
				if err != nil {
					t.Fatal(err)
				}
				result, err := store.ApplyGap(context.Background(), execution.GapGuardApplyRequest{Contract: frozenRef(),
					Retention: execution.GenerationRetention{Unknown: true}, Items: []execution.PlanGapMutation{mutation}})
				if err != nil || len(result.Items) != 1 {
					t.Fatalf("ApplyGap = (%+v, %v)", result, err)
				}
				return result.Items[0]
			}
			if first := open(test.stored, 0); first.Status != execution.GapGuardApplied {
				t.Fatalf("the stored marker was not written: %+v", first)
			}
			key, err := PlanGapKeyV2("alarmd", identity)
			if err != nil {
				t.Fatal(err)
			}
			before, err := server.client.Get(context.Background(), key).Result()
			if err != nil {
				t.Fatal(err)
			}
			got := open(slot, 1)
			if got.Status != test.want || got.ReasonCode != "" {
				t.Fatalf("the Slot's write = %+v, want %s with no reason", got, test.want)
			}
			after, err := server.client.Get(context.Background(), key).Result()
			if err != nil {
				t.Fatal(err)
			}
			if wrote := after != before; wrote != (test.want == execution.GapGuardApplied) {
				t.Fatalf("marker rewritten = %v for %s", wrote, test.want)
			}
		})
	}
}
