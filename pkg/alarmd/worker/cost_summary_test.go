// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestCostSummaryObservesRealWorkerWithoutChangingExecution(t *testing.T) {
	for _, failure := range []string{"", "evaluate", "state_apply", "progress_commit"} {
		t.Run(failure, func(t *testing.T) {
			request := slotRequest(execution.OperationNormal)
			identity := planIdentity()
			owner := observability.CostPlanIdentity{TenantID: identity.TenantID, BusinessID: identity.BusinessID, StrategyID: identity.StrategyID}
			now := time.Unix(int64(request.Contract.Slot.EvaluationTime)+1, 0)
			summary := observability.NewCostSummary(observability.CostSummaryOptions{ProcessID: "worker", Window: time.Minute, GroupCapacity: 1, PlanCapacity: 1, MetadataBytes: 1024, TopN: 1, Now: func() time.Time { return now }})
			summary.Reconcile([]observability.CostGroup{{QueryGroupKey: string(request.Contract.Slot.QueryGroup), SnapshotRevision: string(request.Contract.SnapshotRevision), QueryRevision: string(request.Contract.QueryRevision), ScheduleRevision: string(request.Contract.ScheduleRevision), Members: []observability.CostPlanIdentity{owner}}}, true)
			var evaluations []observability.Observation
			recorder := observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.Stage == observability.StageEvaluationCompleted {
					evaluations = append(evaluations, o)
				}
			})
			enabled := newFixtureWithObserver(t, true, failure, observability.Multi(summary, recorder))
			disabled := newFixtureWithObserver(t, true, failure, observability.NopObserver{})
			got, gotErr := enabled.coordinator.Execute(context.Background(), request)
			want, wantErr := disabled.coordinator.Execute(context.Background(), request)
			if (gotErr != nil) != (wantErr != nil) || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(*enabled.trace, *disabled.trace) || !reflect.DeepEqual(enabled.ports.lastProgress, disabled.ports.lastProgress) || enabled.ports.eventCount != disabled.ports.eventCount || enabled.ports.stateApplyCalls != disabled.ports.stateApplyCalls {
				t.Fatalf("summary changed execution: got=%+v/%v want=%+v/%v", got, gotErr, want, wantErr)
			}
			if len(evaluations) != 1 || evaluations[0].EvaluationOwner != owner || !evaluations[0].DurationKnown || evaluations[0].EvaluationRecordsKnown != (failure != "evaluate") {
				t.Fatalf("real consumer/time facts missing: %+v", evaluations)
			}
			summary.Publish(now)
			snapshot := summary.Snapshot()
			if snapshot.Coverage.ObservedPlans != 1 || snapshot.Coverage.UnattributedEvaluations != 0 {
				t.Fatalf("worker not actually feeding summary: %+v", snapshot)
			}
		})
	}
}

func TestCostCompletionOnlyOwnsIdentityWithoutInventingTimer(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	f, request := newCompletionOnlyFixture(t, plans, requirements, nil)
	if _, err := f.coordinator.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for _, o := range *f.observations {
		if o.Stage == observability.StageEvaluationCompleted {
			if o.EvaluationOwner.TenantID != plans[0].Identity.TenantID || o.EvaluationOwner.StrategyID != plans[0].Identity.StrategyID || !o.EvaluationRecordsKnown || o.DurationKnown || o.Duration != 0 {
				t.Fatalf("completion-only facts fabricated/missing: %+v", o)
			}
			return
		}
	}
	t.Fatal("completion-only evaluation fact missing")
}
