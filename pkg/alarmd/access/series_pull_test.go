// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Counted where a series becomes work for this process, which is not where the
// provider decoded it: a series the monitoring target excludes never reaches
// the pipeline and must not be counted as load. The admission counters count
// decisions per series per plan, which is a different and larger number, so
// this is the only figure that says how much this instance actually pulled.
func TestOnlyTheSeriesThatReachThePipelineAreCountedAsPulled(t *testing.T) {
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	plan := requirement.Consumers[0].Consumer.Plan

	var pulledSeries int
	var pulledRecords uint64
	newAdapter := func(consumer execution.QueryExecutionConsumer, scope planScopes) *seriesAdapter {
		return &seriesAdapter{
			consumer: consumer, query: plannedQueryForTest(requirement), attemptNo: 1,
			admission: scopedChain(),
			observe:   func(string, string, string) {},
			pulled: func(records uint64) {
				pulledSeries++
				pulledRecords += records
			},
			scopes: scope,
		}
	}
	batchOf := func(adapter *seriesAdapter, dataset *execution.Dataset, records uint64) execution.ProviderSeriesBatch {
		return execution.ProviderSeriesBatch{
			PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: dataset,
			Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest,
				QueryRevision: adapter.query.Spec.PlanFacts.QueryRevision,
				Series:        1, Records: records, Digest: "digest"},
		}
	}

	// In scope: it becomes work, so it is counted with the points it carried.
	inScope := newAdapter(&admissionConsumer{},
		planScopes{plan: {StrategyID: plan.StrategyID, TargetScope: hostScope("10.0.0.1|0")}})
	if err := inScope.ConsumeProviderSeries(context.Background(),
		batchOf(inScope, hostSeries(t, "10.0.0.1"), 7)); err != nil {
		t.Fatalf("consume in-scope: %v", err)
	}
	if pulledSeries != 1 || pulledRecords != 7 {
		t.Fatalf("series=%d records=%d, want the one delivered series and its 7 points",
			pulledSeries, pulledRecords)
	}

	// Out of scope: withheld before the pipeline, so it is not this instance's
	// load. Counting it would report work that never happened.
	outOfScope := newAdapter(&admissionConsumer{},
		planScopes{plan: {StrategyID: plan.StrategyID, TargetScope: hostScope("10.0.0.1|0")}})
	if err := outOfScope.ConsumeProviderSeries(context.Background(),
		batchOf(outOfScope, hostSeries(t, "10.9.9.9"), 9)); err != nil {
		t.Fatalf("consume out-of-scope: %v", err)
	}
	if pulledSeries != 1 || pulledRecords != 7 {
		t.Fatalf("series=%d records=%d, want the withheld series left out", pulledSeries, pulledRecords)
	}
}

// A deployment that does not wire the hook must still run. It is optional the
// same way the admission observer is, and a nil call here would take down the
// streaming path over a counter.
func TestPullingWithoutACounterDoesNotFail(t *testing.T) {
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	plan := requirement.Consumers[0].Consumer.Plan
	adapter := &seriesAdapter{
		consumer: &admissionConsumer{}, query: plannedQueryForTest(requirement), attemptNo: 1,
		admission: scopedChain(), observe: func(string, string, string) {},
		scopes: planScopes{plan: {StrategyID: plan.StrategyID, TargetScope: hostScope("10.0.0.1|0")}},
	}
	if err := adapter.ConsumeProviderSeries(context.Background(), execution.ProviderSeriesBatch{
		PhysicalQuery: adapter.query.Spec.Digest, CompletionRef: "result", Dataset: hostSeries(t, "10.0.0.1"),
		Delivery: execution.SeriesDelivery{PhysicalQuery: adapter.query.Spec.Digest,
			QueryRevision: adapter.query.Spec.PlanFacts.QueryRevision, Series: 1, Records: 1, Digest: "digest"},
	}); err != nil {
		t.Fatalf("consume without a counter: %v", err)
	}
}
