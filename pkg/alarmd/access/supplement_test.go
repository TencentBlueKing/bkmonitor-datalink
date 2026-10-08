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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// providerKeptRead replays a provider's answer for the Slot as a kept read.
type providerKeptRead struct {
	provider execution.QueryProvider
	slot     execution.SlotIdentity
	replays  int
	mutate   func(*execution.ProviderCompletion)
}

func (read *providerKeptRead) Replay(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	read.replays++
	completion, err := read.provider.Execute(ctx, execution.QueryAttempt{Spec: spec, Slot: read.slot, AttemptNo: 1}, sink)
	if read.mutate != nil {
		read.mutate(&completion)
	}
	return completion, err
}

// supplementSource is a Source whose clock is far past the Slot's every
// deadline, and whose wait, permits and provider must not be used.
func supplementSource(t *testing.T, frozen FrozenPlan, config Config) (*Source, *recordingQueryPermits, *fakeProvider) {
	t.Helper()
	provider, permits := &fakeProvider{}, &recordingQueryPermits{}
	config.MinReadyDelay = time.Second
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, config)
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(2_100_000_000_000) }
	source.wait = func(context.Context, time.Duration) error {
		t.Error("a supplement waited")
		return nil
	}
	return source, permits, provider
}

func deliveredOf(t *testing.T, batches []execution.SeriesExecutionBatch) []execution.SeriesDelivery {
	t.Helper()
	var delivered execution.SeriesDelivery
	for _, batch := range batches {
		var err error
		if delivered, err = execution.AccumulateSeriesDelivery(delivered, batch.Delivery); err != nil {
			t.Fatal(err)
		}
	}
	if delivered.PhysicalQuery == "" {
		return nil
	}
	return []execution.SeriesDelivery{delivered}
}

// A supplement runs on the read kept for it: past every deadline of its
// Slot, without waiting for readiness, taking a query permit or asking the
// provider, and its completion is the one the worker validates the replayed
// series against.
func TestASupplementReplaysItsKeptReadWithoutWaitingOrQuerying(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	source, permits, provider := supplementSource(t, frozen, Config{})
	kept := &providerKeptRead{provider: &hostStreamingProvider{hosts: []string{"192.0.2.10", "192.0.2.11"}}, slot: contractRef.Slot}
	consumer := &recordingConsumer{}
	completion, err := source.Execute(WithKeptRead(context.Background(), kept), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationSupplement, AttemptNo: 1,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.begin != 1 || len(consumer.batches) != 2 || kept.replays != 1 || !completion.AllRequiredCompleted {
		t.Fatalf("begin %d batches %d replays %d completion %+v", consumer.begin, len(consumer.batches), kept.replays, completion)
	}
	if err := completion.Validate(consumer.header, deliveredOf(t, consumer.batches)); err != nil {
		t.Fatalf("completion does not conserve the replayed series: %v", err)
	}
	if len(permits.attempts) != 0 || len(provider.attempts) != 0 {
		t.Fatalf("a supplement took permits %+v or queried %+v", permits.attempts, provider.attempts)
	}
}

// A supplement without a kept read is refused before it begins anything:
// it has nothing to run on, and it never queries.
func TestASupplementWithoutAKeptReadIsRefused(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	source, permits, provider := supplementSource(t, frozen, Config{})
	consumer := &recordingConsumer{}
	if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationSupplement, AttemptNo: 1,
	}, consumer); err == nil || consumer.begin != 0 || len(permits.attempts) != 0 || len(provider.attempts) != 0 {
		t.Fatalf("error %v begin %d permits %d queries %d", err, consumer.begin, len(permits.attempts), len(provider.attempts))
	}
}

// The replay goes through the target admission a Slot's read goes through:
// a kept series outside the Plan's target is not delivered, and the
// completion is restated for what was.
func TestASupplementReplayIsAdmittedAsTheSlotWas(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	frozen.DuePlans[0].CompiledPlan = compilePlanForStrategy(t, "1001", hostScopeContract("192.0.2.10|0"))
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	source, _, _ := supplementSource(t, frozen, Config{Admission: scopedChain()})
	kept := &providerKeptRead{provider: &hostStreamingProvider{hosts: []string{"192.0.2.10", "192.0.2.99"}}, slot: contractRef.Slot}
	consumer := &recordingConsumer{}
	completion, err := source.Execute(WithKeptRead(context.Background(), kept), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationSupplement, AttemptNo: 1,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if len(consumer.batches) != 1 {
		t.Fatalf("batches %d, want the in-target series only", len(consumer.batches))
	}
	if err := completion.Validate(consumer.header, deliveredOf(t, consumer.batches)); err != nil {
		t.Fatalf("completion not restated for the admitted series: %v", err)
	}
}

// A kept read whose completion is not of the query it replayed is refused,
// as a provider's is.
func TestASupplementRefusesAnUntrustedKeptCompletion(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	source, _, _ := supplementSource(t, frozen, Config{})
	kept := &providerKeptRead{provider: &hostStreamingProvider{hosts: []string{"192.0.2.10"}}, slot: contractRef.Slot,
		mutate: func(completion *execution.ProviderCompletion) { completion.PhysicalQuery = "another-query" }}
	if _, err := source.Execute(WithKeptRead(context.Background(), kept), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationSupplement, AttemptNo: 1,
	}, &recordingConsumer{}); err == nil {
		t.Fatal("an untrusted kept completion was accepted")
	}
}

// A consumer whose frozen budget ends before its data is ready was set aside
// by the Slot as readiness-invalid, and a supplement sets it aside too: it
// decides nothing the Slot would not have, so nothing is replayed for it.
func TestASupplementSetsAsideWhatItsSlotSetAside(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = int64(contractRef.Slot.EvaluationTime)*1000 + 5_000
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	source, _, _ := supplementSource(t, frozen, Config{})
	kept := &providerKeptRead{provider: &hostStreamingProvider{hosts: []string{"192.0.2.10"}}, slot: contractRef.Slot}
	consumer := &recordingConsumer{}
	completion, err := source.Execute(WithKeptRead(context.Background(), kept), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationSupplement, AttemptNo: 1,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if len(consumer.batches) != 0 || kept.replays != 0 || len(completion.CompletionBindings) != 1 ||
		completion.CompletionBindings[0].ReasonCode != execution.ReasonCode(contract.ReasonReadinessBudgetInvalid) {
		t.Fatalf("batches %d replays %d bindings %+v, want the consumer set aside", len(consumer.batches), kept.replays,
			completion.CompletionBindings)
	}
}
