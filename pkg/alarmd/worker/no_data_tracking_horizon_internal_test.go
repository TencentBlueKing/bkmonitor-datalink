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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// horizonNoDataStore answers one Plan's load with a v3 memory that satisfies
// the record's own invariant: no group was last seen after the round the Plan
// last had data in. The shared fixture store leaves PresentAsOf at zero, which
// contradicts any group it holds and fails derivation before a horizon is ever
// consulted.
type horizonNoDataStore struct {
	groups  []execution.NoDataGroupMemory
	present int64
}

func (store *horizonNoDataStore) LoadNoData(
	_ context.Context, request execution.NoDataLoadRequest,
) (execution.NoDataLoadResult, error) {
	result := execution.NoDataLoadResult{Items: make([]execution.NoDataMemorySnapshot, len(request.Items))}
	for index, item := range request.Items {
		result.Items[index] = execution.NoDataMemorySnapshot{
			Identity: item.Identity, Status: execution.NoDataMemoryFound, MarkerRevision: 1,
			SchemaVersion: execution.NoDataMemorySchemaV3, PersistedApplyVersion: item.ApplyVersion,
			PersistedMutationDigest: "digest", LastScheduleRevision: item.ScheduleRevision,
			RosterVersion: "HISTORY/1", Representation: execution.NoDataRepresentationPerGroup,
			PresentAsOf: store.present, Groups: store.groups,
		}
	}
	return result, nil
}

func (store *horizonNoDataStore) ApplyNoData(
	_ context.Context, request execution.NoDataApplyRequest,
) (execution.NoDataApplyResult, error) {
	result := execution.NoDataApplyResult{Items: make([]execution.NoDataApplyItemResult, len(request.Items))}
	for index, mutation := range request.Items {
		result.Items[index] = execution.NoDataApplyItemResult{
			Identity: mutation.Identity, Status: execution.NoDataApplied,
		}
	}
	return result, nil
}

// The horizon frozen into the Plan reaches the round that judges absence.
//
// This is the point of the horizon being configurable at all, and until the
// Plan carried it there was nothing to carry: the evaluator read the field,
// every layer below passed it along, and the one production caller never set
// it. So it was zero, zero means no horizon, and no horizon is exactly the
// behaviour the horizon was added to change. Nothing failed, no line said
// anything, and the only symptom was that absences went on being tracked -
// which is the state the feature exists to end.
//
// Asserted on the suppression the horizon produces rather than on the field
// arriving. A case that reads the field back proves the field was copied; this
// one proves the number did something, which is the difference between the
// feature working and the feature being wired to nothing.
func TestTheHorizonFrozenIntoThePlanStopsTrackingAnOldAbsence(t *testing.T) {
	const horizon = int64(600)
	wired := noDataWiredPlan(t)
	evaluation := int64(noDataPreflightContract(t, []execution.DuePlan{wired}).Slot.EvaluationTime)
	group := execution.NoDataGroupMemory{
		GroupKey:    "bk_target_cloud_id=0,bk_target_ip=127.0.0.1," + contract.NoDataDimensionTag + "=true",
		LastSeen:    evaluation - horizon*4,
		FirstAbsent: evaluation - horizon*3,
	}
	if evaluation-group.FirstAbsent <= horizon {
		t.Fatalf("the fixture's absence is %ds old against a %ds horizon, so it would not expire either way",
			evaluation-group.FirstAbsent, horizon)
	}

	for _, testCase := range []struct {
		name        string
		horizon     int64
		wantStopped bool
		because     string
	}{
		{name: "the Plan carries a horizon this absence has outlived", horizon: horizon, wantStopped: true,
			because: "the horizon reached the evaluator and stopped tracking this absence"},
		// The control. Without it, a wiring that suppressed regardless of the
		// number - or that passed some constant - would pass the case above.
		{name: "the Plan carries no horizon, which is every Plan before this", horizon: 0, wantStopped: false,
			because: "no horizon means tracking continues, which is what zero has always meant here"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := wired
			plan.CompiledPlan = noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{
				Continuous: 1, Level: 2, AggDimension: []string{"bk_target_ip", "bk_target_cloud_id"},
				TrackingHorizonSeconds: testCase.horizon,
			})
			store := &horizonNoDataStore{groups: []execution.NoDataGroupMemory{group}, present: group.LastSeen}
			stream := noDataWiredStream(t, plan, store)
			if err := stream.loadNoDataMemory(context.Background()); err != nil {
				t.Fatal(err)
			}
			round, err := stream.noDataRoundFor(plan, nil, execution.CompletenessFull)
			if err != nil {
				t.Fatal(err)
			}
			if round.outcome != nodata.OutcomeEvaluated {
				t.Fatalf("outcome = %q, want the round to have judged", round.outcome)
			}
			stopped := false
			if round.mutation != nil {
				for _, written := range round.mutation.Set {
					if written.GroupKey == group.GroupKey && written.Absent == nil {
						// The group left the memory's absent set, which is what
						// the horizon does to an absence it has outlived.
						stopped = true
					}
				}
				for _, removed := range round.mutation.Del {
					if removed == group.GroupKey {
						stopped = true
					}
				}
			}
			if stopped != testCase.wantStopped {
				t.Fatalf("absence stopped = %t, want %t: %s (mutation = %+v)",
					stopped, testCase.wantStopped, testCase.because, round.mutation)
			}
		})
	}
}
