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

// retainingConsumer is a recordingConsumer that says how much input it holds.
type retainingConsumer struct {
	recordingConsumer
	held uint64
}

func (consumer *retainingConsumer) RetainedInputBytesNow() uint64 { return consumer.held }

// historyAheadExecution is a Slot whose dependency query reads a history
// window that ends 20 s before the primary's, so it is readable 30 s before
// the primary under a 30 s settling wait: the Slot is not deferred as a
// whole, and its dispatch sends the dependency and waits in place for the
// primary.
func historyAheadExecution(t *testing.T) (execution.FrozenExecutionContractRef, FrozenPlan, time.Time) {
	t.Helper()
	contractRef, frozen := frozenExecution(t)
	evaluationTime := time.UnixMilli(int64(contractRef.Slot.EvaluationTime) * 1000)
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationTime.Add(2 * time.Minute).UnixMilli()
	frozen.DuePlans[0].ScheduleSpec.EvaluationIntervalSeconds = 120
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
	primaryFacts := frozen.QueryFacts[frozen.Requirements[0].LogicalQueryRef]
	auxiliaryFacts, err := primaryFacts.WithMetricMerge("a + 1")
	if err != nil {
		t.Fatal(err)
	}
	auxiliaryRef := execution.LogicalQueryRef(auxiliaryFacts.QueryRevision)
	auxiliary := frozen.Requirements[0]
	auxiliary.RequirementID, auxiliary.DatasetName = "earlier-ready-history", "earlier_ready_history"
	auxiliary.Role, auxiliary.LogicalQueryRef = execution.InputRoleAlgorithmDependency, auxiliaryRef
	auxiliary.RelativeWindow = execution.RelativeQueryWindow{StartOffsetSeconds: -120, EndOffsetSeconds: -5, HalfOpen: true}
	frozen.Requirements = append(frozen.Requirements, auxiliary)
	frozen.QueryFacts[auxiliaryRef] = auxiliaryFacts
	return bindFrozenDueDigest(t, contractRef, frozen), frozen, evaluationTime
}

// A Slot that arrives before any of its queries is readable, with their
// readiness 5 s apart, is not deferred as a whole: it waits in place twice,
// 5 s with nothing sent and 5 s with its dependency sent, and says so on
// its completion - the most input its consumer had retained at either end of
// a wait, exactly, and a non-zero estimate of the copies it made for itself.
// Here 4096 bytes arrive during each wait, so the most is the 8192 held when
// the second wait ends, which no reading at a wait's start sees.
func TestAnInPlaceReadinessWaitIsMeasured(t *testing.T) {
	contractRef, frozen, evaluationTime := historyAheadExecution(t)
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, &recordingQueryPermits{}, Config{MinReadyDelay: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	clock := evaluationTime
	source.now = func() time.Time { return clock }
	consumer := &retainingConsumer{}
	source.wait = func(_ context.Context, delay time.Duration) error {
		clock = clock.Add(delay)
		consumer.held += 4_096
		return nil
	}
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	wait := completion.InPlaceWait
	if wait == nil || wait.Waits != 2 || wait.TotalMillis != 10_000 || wait.LongestMillis != 5_000 ||
		wait.SentBeforeFirstWait != 0 || wait.BeforeAnySentMillis != 5_000 || wait.HeldInputBytes != 8_192 || wait.HeldPreparedBytes == 0 {
		t.Fatalf("in-place wait %+v, want 5 s with nothing sent then 5 s, the 8192 input bytes held at the second wait's end and a prepared estimate", wait)
	}
}

// A Slot whose queries are all readable when it arrives does not wait, and
// its completion carries no wait at all - not a zero one.
func TestASlotThatDoesNotWaitCarriesNoInPlaceWait(t *testing.T) {
	contractRef, frozen, evaluationTime := historyAheadExecution(t)
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, &recordingQueryPermits{}, Config{MinReadyDelay: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	clock := evaluationTime.Add(time.Minute)
	source.now = func() time.Time { return clock }
	source.wait = func(context.Context, time.Duration) error {
		t.Fatal("a Slot readable on arrival waited")
		return nil
	}
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}, &retainingConsumer{held: 4_096})
	if err != nil {
		t.Fatal(err)
	}
	if completion.InPlaceWait != nil {
		t.Fatalf("in-place wait %+v on a Slot that did not wait", completion.InPlaceWait)
	}
}
