// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestProcessorWritesOneOrderedTerminalBatch(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	payload, key := triggerInputPayload(t, strategy,
		newOutcome(t, strategy, 100, map[int]bool{1: false}),
		newOutcome(t, strategy, 110, map[int]bool{1: true}),
	)
	sink := &recordingSink{}
	processor := NewProcessor(sink)

	if err := processor.Process(context.Background(), key, payload); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(sink.batches) != 1 || len(sink.batches[0].Decisions) != 2 {
		t.Fatalf("sink batches = %#v, want one batch with two terminals", sink.batches)
	}
	first, second := sink.batches[0].Decisions[0], sink.batches[0].Decisions[1]
	if first.Outcome != DecisionNoTrigger || first.ReasonCode != contract.DecisionReasonInputNormal || first.Level != nil || len(first.AnomalyTimestamps) != 0 {
		t.Fatalf("normal terminal = %#v, want INPUT_NORMAL NO_TRIGGER", first)
	}
	if second.Outcome != DecisionNoTrigger || second.ReasonCode != contract.DecisionReasonTriggerConditionNotMet || second.Level != nil || len(second.AnomalyTimestamps) != 0 {
		t.Fatalf("anomalous terminal = %#v, want condition-not-met NO_TRIGGER without selected level", second)
	}
	if sink.batches[0].BatchID != "batch-1" || sink.batches[0].StrategyRef != strategy.StrategyRef {
		t.Fatalf("batch identity = %#v, want source batch and strategy", sink.batches[0])
	}
}

func TestProcessorEvaluatesOutOfOrderMicroBatchOnEventTime(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 2, TriggerCount: 2}})
	payload, key := triggerInputPayload(t, strategy,
		newOutcome(t, strategy, 110, map[int]bool{1: true}),
		newOutcome(t, strategy, 100, map[int]bool{1: true}),
	)
	sink := &recordingSink{}

	if err := NewProcessor(sink).Process(context.Background(), key, payload); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	newest, oldest := sink.batches[0].Decisions[0], sink.batches[0].Decisions[1]
	if newest.Outcome != DecisionTrigger || newest.ReasonCode != contract.DecisionReasonTriggerConditionMet || newest.Level == nil || *newest.Level != 1 {
		t.Fatalf("newest terminal = %#v, want level-1 TRIGGER from the complete batch window", newest)
	}
	assertTimestamps(t, newest.AnomalyTimestamps, []int64{100, 110})
	if oldest.Outcome != DecisionNoTrigger || oldest.ReasonCode != contract.DecisionReasonTriggerConditionNotMet {
		t.Fatalf("oldest terminal = %#v, want condition-not-met NO_TRIGGER for its own window", oldest)
	}
}

func TestProcessorMaterializesNonBusinessOutcomesWithoutAdvancingState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		outcome   string
		errorCode string
	}{
		{outcome: contract.OutcomeError, errorCode: "ALGORITHM_ERROR"},
		{outcome: contract.OutcomeUnsupported, errorCode: "UNSUPPORTED_STRATEGY"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.outcome, func(t *testing.T) {
			t.Parallel()

			strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
			nonBusiness := newOutcome(t, strategy, 100, map[int]bool{1: true})
			nonBusiness.Outcome = test.outcome
			nonBusiness.Evaluations = []contract.Evaluation{}
			nonBusiness.ErrorCode = json.RawMessage(`"` + test.errorCode + `"`)
			payload, key := triggerInputPayload(t, strategy, nonBusiness)
			sink := &recordingSink{}
			processor := NewProcessor(sink)

			if err := processor.Process(context.Background(), key, payload); err != nil {
				t.Fatalf("Process(%s outcome) error = %v", test.outcome, err)
			}
			if got := sink.batches[0].Decisions[0]; got.Outcome != test.outcome || got.ReasonCode != test.errorCode {
				t.Fatalf("non-business terminal = %#v", got)
			}

			nextPayload, nextKey := triggerInputPayload(t, strategy, newOutcome(t, strategy, 110, map[int]bool{1: true}))
			if err := processor.Process(context.Background(), nextKey, nextPayload); err != nil {
				t.Fatalf("Process(next anomaly) error = %v", err)
			}
			if got := sink.batches[1].Decisions[0].Outcome; got != DecisionNoTrigger {
				t.Fatalf("outcome after %s = %q, want NO_TRIGGER", test.outcome, got)
			}
		})
	}
}

func TestProcessorWindowStateIsClaimLocal(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	firstPayload, firstKey := triggerInputPayload(t, strategy, newOutcome(t, strategy, 100, map[int]bool{1: true}))
	secondPayload, secondKey := triggerInputPayload(t, strategy, newOutcome(t, strategy, 110, map[int]bool{1: true}))

	sameClaimSink := &recordingSink{}
	sameClaim := NewProcessor(sameClaimSink)
	if err := sameClaim.Process(context.Background(), firstKey, firstPayload); err != nil {
		t.Fatalf("Process(first) error = %v", err)
	}
	if err := sameClaim.Process(context.Background(), secondKey, secondPayload); err != nil {
		t.Fatalf("Process(second) error = %v", err)
	}
	triggerDecision := sameClaimSink.batches[1].Decisions[0]
	if triggerDecision.Outcome != DecisionTrigger || triggerDecision.ReasonCode != contract.DecisionReasonTriggerConditionMet || triggerDecision.Level == nil || *triggerDecision.Level != 1 {
		t.Fatalf("same-claim second decision = %#v, want level-1 TRIGGER", triggerDecision)
	}
	assertTimestamps(t, triggerDecision.AnomalyTimestamps, []int64{100, 110})

	newClaimSink := &recordingSink{}
	if err := NewProcessor(newClaimSink).Process(context.Background(), secondKey, secondPayload); err != nil {
		t.Fatalf("new claim Process(second) error = %v", err)
	}
	if got := newClaimSink.batches[0].Decisions[0].Outcome; got != DecisionNoTrigger {
		t.Fatalf("new-claim outcome = %q, want NO_TRIGGER", got)
	}
}

func TestProcessorMaterializesUnsupportedPurpose(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	strategy.Purpose = contract.PurposeNodata
	outcome := newOutcome(t, strategy, 100, map[int]bool{1: true})
	payload, key := triggerInputPayload(t, strategy, outcome)
	sink := &recordingSink{}

	if err := NewProcessor(sink).Process(context.Background(), key, payload); err != nil {
		t.Fatalf("Process(NODATA) error = %v", err)
	}
	decision := sink.batches[0].Decisions[0]
	if decision.Outcome != contract.OutcomeUnsupported || decision.ReasonCode != "UNSUPPORTED_STRATEGY" || decision.Level != nil || len(decision.AnomalyTimestamps) != 0 {
		t.Fatalf("unsupported-purpose decision = %#v", decision)
	}
}

func TestProcessorRejectsConflictingExecutionPlanForSameStrategyIdentity(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	firstPayload, key := triggerInputPayload(t, strategy, newOutcome(t, strategy, 100, map[int]bool{1: true}))
	sink := &recordingSink{}
	processor := NewProcessor(sink)
	if err := processor.Process(context.Background(), key, firstPayload); err != nil {
		t.Fatalf("Process(first plan) error = %v", err)
	}

	conflicting := *strategy
	conflicting.TriggerConfigs = append([]contract.TriggerConfig(nil), strategy.TriggerConfigs...)
	conflicting.TriggerConfigs[0].TriggerCount = 3
	conflictingOutcome := newOutcome(t, &conflicting, 110, map[int]bool{1: true})
	conflictingPayload, conflictingKey := triggerInputPayload(t, &conflicting, conflictingOutcome)
	if err := processor.Process(context.Background(), conflictingKey, conflictingPayload); err == nil {
		t.Fatal("Process() accepted conflicting execution plan for the same strategy identity")
	}
	if len(sink.batches) != 1 {
		t.Fatalf("sink batches = %d, want only the first plan", len(sink.batches))
	}
}

func TestProcessorRejectsPartitionKeyMismatchBeforeSink(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	payload, _ := triggerInputPayload(t, strategy, newOutcome(t, strategy, 100, map[int]bool{1: false}))
	sink := &recordingSink{}

	if err := NewProcessor(sink).Process(context.Background(), []byte("wrong"), payload); err == nil {
		t.Fatal("Process() accepted mismatched partition key")
	}
	if len(sink.batches) != 0 {
		t.Fatalf("sink batches = %#v, want none", sink.batches)
	}
}

func TestProcessorReturnsSinkFailure(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	payload, key := triggerInputPayload(t, strategy, newOutcome(t, strategy, 100, map[int]bool{1: false}))
	want := errors.New("sink unavailable")

	if err := NewProcessor(&recordingSink{err: want}).Process(context.Background(), key, payload); !errors.Is(err, want) {
		t.Fatalf("Process() error = %v, want %v", err, want)
	}
}

func TestProcessorRollsBackWindowStateWhenSinkFails(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 3, TriggerCount: 2}})
	payload, key := triggerInputPayload(t, strategy,
		newOutcome(t, strategy, 110, map[int]bool{1: true}),
		newOutcome(t, strategy, 100, map[int]bool{1: true}),
	)
	sink := &recordingSink{err: errors.New("sink unavailable")}
	processor := NewProcessor(sink)

	if err := processor.Process(context.Background(), key, payload); err == nil {
		t.Fatal("Process(first attempt) error = nil, want sink failure")
	}
	firstAttempt := append([]byte(nil), sink.payloads[0]...)
	sink.err = nil
	if err := processor.Process(context.Background(), key, payload); err != nil {
		t.Fatalf("Process(replay) error = %v", err)
	}
	if !reflect.DeepEqual(sink.payloads[1], firstAttempt) {
		t.Fatalf("replayed decision batch = %s, want %s", sink.payloads[1], firstAttempt)
	}
}

func TestProcessorRollsBackAllChunksWhenLaterSinkWriteFails(t *testing.T) {
	t.Parallel()

	strategy := newStrategy(t, "generation-1", []contract.TriggerConfig{{Level: 1, CheckWindowSize: 2, TriggerCount: 2}})
	newerPayload, key := triggerInputPayload(t, strategy, newOutcome(t, strategy, 110, map[int]bool{1: true}))
	olderPayload, _ := triggerInputPayload(t, strategy, newOutcome(t, strategy, 100, map[int]bool{1: true}))
	newer, err := contract.DecodeTriggerInput(newerPayload)
	if err != nil {
		t.Fatal(err)
	}
	older, err := contract.DecodeTriggerInput(olderPayload)
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingSink{err: errors.New("sink unavailable"), failAt: 2}
	processor := NewProcessor(sink)

	if err := processor.ProcessInputs(context.Background(), key, []*contract.TriggerInput{newer, older}); err == nil {
		t.Fatal("ProcessInputs(first attempt) error = nil, want sink failure")
	}
	sink.err = nil
	if err := processor.ProcessInputs(context.Background(), key, []*contract.TriggerInput{newer, older}); err != nil {
		t.Fatalf("ProcessInputs(replay) error = %v", err)
	}
	if len(sink.payloads) != 4 || !reflect.DeepEqual(sink.payloads[0], sink.payloads[2]) || !reflect.DeepEqual(sink.payloads[1], sink.payloads[3]) {
		t.Fatal("replayed chunked decisions changed after a partial sink failure")
	}
	if got := sink.batches[0].Decisions[0]; got.Outcome != DecisionTrigger || !reflect.DeepEqual(got.AnomalyTimestamps, []int64{100, 110}) {
		t.Fatalf("newer chunk decision = %#v, want complete event-time window", got)
	}
}

type recordingSink struct {
	batches  []*contract.TriggerDecisionBatch
	payloads [][]byte
	err      error
	failAt   int
}

func (s *recordingSink) WriteBatch(_ context.Context, batch *contract.TriggerDecisionBatch) error {
	payload, err := contract.EncodeTriggerDecisionBatch(batch)
	if err != nil {
		return err
	}
	copyOfBatch, err := contract.DecodeTriggerDecisionBatch(payload)
	if err != nil {
		return err
	}
	s.payloads = append(s.payloads, append([]byte(nil), payload...))
	s.batches = append(s.batches, copyOfBatch)
	if s.err != nil && s.failAt > 0 && len(s.payloads) != s.failAt {
		return nil
	}
	return s.err
}

func triggerInputPayload(t *testing.T, strategy *contract.TriggerStrategyIR, outcomes ...*contract.DetectionOutcome) ([]byte, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"schema":                 map[string]any{"name": "trigger-input", "major": 1, "minor": 0},
		"required_features":      []string{},
		"partition_hash_version": contract.PartitionHashVersionV1,
		"strategy_ir":            strategy,
		"detection_outcomes":     outcomes,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	input, err := contract.DecodeTriggerInput(payload)
	if err != nil {
		t.Fatalf("DecodeTriggerInput() error = %v", err)
	}
	key, err := input.PartitionKey()
	if err != nil {
		t.Fatalf("PartitionKey() error = %v", err)
	}
	return payload, key
}
