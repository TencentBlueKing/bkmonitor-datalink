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

// Every physical query carries what it had from its send - after its permit,
// not from its arrival - what the provider says it took, and when it was
// planned ready: the Slot plus the settling wait. A replay has
// its recovery budget, the 30 s interval less the reserve from its arrival,
// 25 s; a permit that took 2 s leaves it 23 s at its send.
func TestEveryPhysicalQueryCarriesItsClock(t *testing.T) {
	ref, frozen := frozenExecution(t)
	frozen.DuePlans[0].ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: 30, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
	eval := int64(ref.Slot.EvaluationTime) * 1000
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = eval + 30_000
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
	ref = bindFrozenDueDigest(t, ref, frozen)
	arrived := time.UnixMilli(eval + 600_000)
	clock := arrived
	provider := &timedProvider{queryMillis: 1_234}
	permits := &recordingQueryPermits{onAcquire: func() { clock = clock.Add(2 * time.Second) }}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits,
		Config{MinReadyDelay: 10 * time.Second, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	source.wait = func(context.Context, time.Duration) error { return nil }
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{Contract: ref, Operation: execution.OperationReplay, AttemptNo: 2}, &recordingConsumer{})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.attempts) != 1 || provider.attempts[0].DeadlineUnixMilli != arrived.UnixMilli()+25_000 {
		t.Fatalf("attempts %+v, want one with deadline arrival + 25 s", provider.attempts)
	}
	if len(completion.PhysicalQueries) != 1 || completion.PhysicalQueries[0].Clock == nil ||
		*completion.PhysicalQueries[0].Clock != (execution.PhysicalQueryClock{BudgetMillis: 23_000, ElapsedMillis: 1_234, ReadyAtUnixMilli: eval + 10_000}) {
		t.Fatalf("physical queries %+v, want one with clock {23000 1234 ready at the Slot + 10 s}", completion.PhysicalQueries)
	}
}
