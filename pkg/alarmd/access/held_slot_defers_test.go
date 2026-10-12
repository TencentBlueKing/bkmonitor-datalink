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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Slot carrying a read hold waits in the due index, not inside its
// execution (h delivery: a held Slot's due time is its held readiness). With
// a dependency on yesterday's window already readable and the primary window
// held 300 s past its 30 s settling, the Slot is deferred to the primary's
// held readiness and does no work now: it does not sleep the 330 s holding a
// dispatch slot and its Query Group's flight.
func TestAHeldSlotWithQueriesReadyAtDifferentTimesWaitsInTheDueIndex(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	evaluationTime := time.UnixMilli(int64(contractRef.Slot.EvaluationTime) * 1000)
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationTime.Add(10 * time.Minute).UnixMilli()
	frozen.DuePlans[0].ScheduleSpec.EvaluationIntervalSeconds = 60
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
	primaryFacts := frozen.QueryFacts[frozen.Requirements[0].LogicalQueryRef]
	yesterdayFacts, err := primaryFacts.WithMetricMerge("a + 1")
	if err != nil {
		t.Fatal(err)
	}
	yesterdayRef := execution.LogicalQueryRef(yesterdayFacts.QueryRevision)
	yesterday := frozen.Requirements[0]
	yesterday.RequirementID, yesterday.DatasetName = "yesterday", "yesterday"
	yesterday.Role = execution.InputRoleAlgorithmDependency
	yesterday.LogicalQueryRef = yesterdayRef
	yesterday.RelativeWindow = execution.RelativeQueryWindow{StartOffsetSeconds: -86460, EndOffsetSeconds: -86400, HalfOpen: true}
	frozen.Requirements = append(frozen.Requirements, yesterday)
	frozen.QueryFacts[yesterdayRef] = yesterdayFacts
	contractRef.ReadHoldMillis = 300_000
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)

	provider := &fakeProvider{}
	permits := &recordingQueryPermits{}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return evaluationTime }
	var waited time.Duration
	source.wait = func(_ context.Context, delay time.Duration) error {
		waited += delay
		return nil
	}
	consumer := &recordingConsumer{}
	_, err = source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, consumer)
	var deferred interface{ ReadinessReadyAt() time.Time }
	if !errors.As(err, &deferred) {
		t.Fatalf("Execute(held, mixed readiness) error=%v waited=%s, want a readiness deferral", err, waited)
	}
	if want := evaluationTime.Add(330 * time.Second); !deferred.ReadinessReadyAt().Equal(want) {
		t.Fatalf("deferred to %s, want the primary's held readiness %s", deferred.ReadinessReadyAt(), want)
	}
	if waited != 0 || consumer.begin != 0 || len(provider.attempts) != 0 || len(permits.attempts) != 0 {
		t.Fatalf("deferred Slot did work: waited=%s begin=%d provider=%d permits=%d", waited, consumer.begin, len(provider.attempts), len(permits.attempts))
	}
}

// A held Slot comes back when the last of its queries is ready, whether or
// not another is readable now; a Slot without a hold keeps the shared rule
// and is deferred only when every query waits for the same moment.
func TestAHeldSlotComesBackWhenItsLastQueryIsReady(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	query := func(id string, after time.Duration) PlannedQuery {
		return PlannedQuery{Requirements: []execution.DataRequirement{{RequirementID: execution.RequirementID(id)}}, ReadyAtUnixMilli: now.Add(after).UnixMilli()}
	}
	pending := []PlannedQuery{query("history", 310*time.Second), query("primary", 330*time.Second)}
	if got := slotPendingReadiness(pending, 300_000, now); !got.Equal(now.Add(330 * time.Second)) {
		t.Fatalf("held, two pending = %s, want the last ready %s", got, now.Add(330*time.Second))
	}
	readable := []PlannedQuery{query("history", -time.Hour), query("primary", 330*time.Second)}
	if got := slotPendingReadiness(readable, 300_000, now); !got.Equal(now.Add(330 * time.Second)) {
		t.Fatalf("held, one readable = %s, want the pending one's %s", got, now.Add(330*time.Second))
	}
	if got := slotPendingReadiness(pending, 0, now); !got.IsZero() {
		t.Fatalf("not held, two pending at different times = %s, want no deferral (settling waited inside)", got)
	}
	if got := slotPendingReadiness([]PlannedQuery{query("primary", -time.Second)}, 300_000, now); !got.IsZero() {
		t.Fatalf("held and ready = %s, want no deferral", got)
	}
}
