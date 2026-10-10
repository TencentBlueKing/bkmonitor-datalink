// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// chunkedStatePorts answers each state apply call from a script: an entry is
// the status every key of that call gets, or the error the call fails with.
type chunkedStatePorts struct {
	activationSiblingPorts
	script []chunkedStateAnswer
	calls  int
}

type chunkedStateAnswer struct {
	status execution.StateApplyStatus
	err    error
}

func (ports *chunkedStatePorts) ApplyRuntime(_ context.Context, request execution.StateApplyRequest) (execution.StateApplyResult, error) {
	answer := chunkedStateAnswer{status: execution.StateApplied}
	if ports.calls < len(ports.script) {
		answer = ports.script[ports.calls]
	}
	ports.calls++
	if answer.err != nil {
		return execution.StateApplyResult{}, answer.err
	}
	items := make([]execution.StateApplyItemResult, len(request.Items))
	for index, item := range request.Items {
		items[index] = execution.StateApplyItemResult{Identity: item.Identity, Status: answer.status}
		if answer.status == execution.StateApplyAlreadyApplied {
			items[index].AlreadyApplied = execution.StateAlreadyAppliedStable
			items[index].StoredBlobRevision = 1
		}
	}
	return execution.StateApplyResult{Items: items}, nil
}

func chunkedStateMutations(t *testing.T, contractRef execution.FrozenExecutionContractRef, plan execution.PlanIdentity, keys int) []execution.StateMutation {
	t.Helper()
	applyVersion, err := execution.BuildApplyVersion(contractRef, 1)
	if err != nil {
		t.Fatal(err)
	}
	mutations := make([]execution.StateMutation, 0, keys)
	for index := 0; index < keys; index++ {
		identity := execution.StateKeyIdentity{Plan: plan, StateGeneration: "generation-1", SeriesIdentityDigest: execution.SeriesIdentityDigest("series-" + string(rune('a'+index)))}
		mutations = append(mutations, execution.StateMutation{Identity: identity, ApplyVersion: applyVersion, MutationDigest: execution.MutationDigest("digest-" + string(rune('a'+index)))})
	}
	return mutations
}

// A Plan whose keys go to the store in more than one call is this attempt's
// to vouch for when every call landed, and not before.
//
// The mark is read by a query-free finalization to say "this Slot was
// evaluated" and drop the gap. A Plan noted off its first chunk with the
// second lost would have that said of a Slot with half the Plan's series one
// Slot behind, and the Slot never replayed. So the decision is made once, when
// every chunk has run, from the count of keys this attempt wrote against the
// Plan's list; any key that failed, was refused, was never sent, or was an
// earlier attempt's leaves the Plan out.
func TestAPlanIsThisAttemptsOnlyWhenEveryChunkOfItsKeysLanded(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	contractRef := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_060},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1", ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: 1_788_000_000, DuePlanSetDigest: "due-set-v1",
	}
	cases := []struct {
		name   string
		script []chunkedStateAnswer
		marked bool
		fails  bool
	}{
		{name: "both chunks landed", script: nil, marked: true},
		{name: "second chunk lost", script: []chunkedStateAnswer{{status: execution.StateApplied}, {err: errors.New("the store went away")}}, fails: true},
		{name: "second chunk refused", script: []chunkedStateAnswer{{status: execution.StateApplied}, {status: execution.StateApplyStale}}, fails: true},
		{name: "first chunk was an earlier attempt's", script: []chunkedStateAnswer{{status: execution.StateApplyAlreadyApplied}, {status: execution.StateApplied}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ports := &chunkedStatePorts{script: tc.script}
			coordinator := &SlotExecutionCoordinator{
				ports: Ports{State: ports, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})},
				// One key per store call, so the Plan's two keys are two calls.
				budget: ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10, StoreMaxItems: 1},
			}
			ctx, recorder := withAppliedPlans(context.Background())
			_, err := coordinator.applyState(ctx, execution.OperationNormal, contractRef, execution.OwnerFence{}, "",
				nil, 0, chunkedStateMutations(t, contractRef, plan, 2), nil)
			if (err != nil) != tc.fails {
				t.Fatalf("applyState() error = %v, want failure %v", err, tc.fails)
			}
			if ports.calls != 2 {
				t.Fatalf("the Plan's keys went to the store in %d call(s); this test needs two", ports.calls)
			}
			marked := recorder.unrecorded()
			if tc.marked && (len(marked) != 1 || marked[0] != plan) {
				t.Fatalf("marked = %v, want the Plan whose every key this attempt wrote", marked)
			}
			if !tc.marked && len(marked) != 0 {
				t.Fatalf("marked = %v, want none: a Plan with a key this attempt did not write is not one it can vouch for, "+
					"and the mark would have the Slot finalized as evaluated with that key's series one Slot behind", marked)
			}
		})
	}
}
