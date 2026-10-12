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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// timedProvider answers as fakeProvider does and says its request took
// queryMillis, as the query service client measures it.
type timedProvider struct {
	fakeProvider
	queryMillis uint64
}

func (provider *timedProvider) Execute(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	completion, err := provider.fakeProvider.Execute(ctx, attempt, sink)
	completion.Stats.QueryMillis = provider.queryMillis
	return completion, err
}

// A replay marked as deciding the query cooldown pool gets, from its
// arrival, each query's span from readiness to deadline as the normal round
// plans it - 15 s here - and not the 25 s a recovery has. Unmarked, the same
// replay keeps the recovery budget. Either way every physical query carries
// what it had from its send - after its permit, not from its arrival - and
// what the provider says it took.
func TestAMarkedReplayHasANormalRoundsBudgetAndEveryQueryItsClock(t *testing.T) {
	ref, frozen := frozenExecution(t)
	frozen.DuePlans[0].ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: 30, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
	eval := int64(ref.Slot.EvaluationTime) * 1000
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = eval + 30_000
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
	ref = bindFrozenDueDigest(t, ref, frozen)
	for _, test := range []struct {
		name   string
		marked bool
		budget int64
	}{
		// Normal: deadline eval+25 s (consumer deadline less the 5 s
		// reserve), ready at eval+10 s: 15 s. Recovery: the 30 s interval
		// less the reserve, 25 s.
		{"marked", true, 15_000},
		{"unmarked", false, 25_000},
	} {
		t.Run(test.name, func(t *testing.T) {
			arrived := time.UnixMilli(eval + 600_000)
			clock := arrived
			provider := &timedProvider{queryMillis: 1_234}
			// The permit takes 2 s: the query is sent 2 s after its arrival,
			// with 2 s less of its budget left.
			permits := &recordingQueryPermits{onAcquire: func() { clock = clock.Add(2 * time.Second) }}
			source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits,
				Config{MinReadyDelay: 10 * time.Second, Now: func() time.Time { return clock }})
			if err != nil {
				t.Fatal(err)
			}
			source.wait = func(context.Context, time.Duration) error { return nil }
			ctx := context.Background()
			if test.marked {
				ctx = execution.WithNormalQueryBudget(ctx)
			}
			completion, err := source.Execute(ctx, execution.QueryExecutionRequest{Contract: ref, Operation: execution.OperationReplay, AttemptNo: 2}, &recordingConsumer{})
			if err != nil {
				t.Fatal(err)
			}
			if len(provider.attempts) != 1 || provider.attempts[0].DeadlineUnixMilli != arrived.UnixMilli()+test.budget {
				t.Fatalf("attempts %+v, want one with deadline arrival + %d ms", provider.attempts, test.budget)
			}
			if len(completion.PhysicalQueries) != 1 || completion.PhysicalQueries[0].Clock == nil ||
				*completion.PhysicalQueries[0].Clock != (execution.PhysicalQueryClock{BudgetMillis: test.budget - 2_000, ElapsedMillis: 1_234}) {
				t.Fatalf("physical queries %+v, want one with clock {%d 1234}", completion.PhysicalQueries, test.budget-2_000)
			}
		})
	}
}
