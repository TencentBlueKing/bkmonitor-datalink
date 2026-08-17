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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
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
	if len(sink.batches) != 1 || len(sink.batches[0]) != 2 {
		t.Fatalf("sink batches = %#v, want one batch with two terminals", sink.batches)
	}
	if sink.batches[0][0].Outcome != DecisionNoTrigger || sink.batches[0][1].Outcome != DecisionNoTrigger {
		t.Fatalf("terminal outcomes = %#v, want ordered NO_TRIGGER decisions", sink.batches[0])
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
			if got := sink.batches[0][0]; got.Outcome != test.outcome || got.ErrorCode != test.errorCode {
				t.Fatalf("non-business terminal = %#v", got)
			}

			nextPayload, nextKey := triggerInputPayload(t, strategy, newOutcome(t, strategy, 110, map[int]bool{1: true}))
			if err := processor.Process(context.Background(), nextKey, nextPayload); err != nil {
				t.Fatalf("Process(next anomaly) error = %v", err)
			}
			if got := sink.batches[1][0].Outcome; got != DecisionNoTrigger {
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
	if got := sameClaimSink.batches[1][0].Outcome; got != DecisionTrigger {
		t.Fatalf("same-claim second outcome = %q, want TRIGGER", got)
	}

	newClaimSink := &recordingSink{}
	if err := NewProcessor(newClaimSink).Process(context.Background(), secondKey, secondPayload); err != nil {
		t.Fatalf("new claim Process(second) error = %v", err)
	}
	if got := newClaimSink.batches[0][0].Outcome; got != DecisionNoTrigger {
		t.Fatalf("new-claim outcome = %q, want NO_TRIGGER", got)
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
	firstAttempt := append([]Terminal(nil), sink.batches[0]...)
	sink.err = nil
	if err := processor.Process(context.Background(), key, payload); err != nil {
		t.Fatalf("Process(replay) error = %v", err)
	}
	if !reflect.DeepEqual(sink.batches[1], firstAttempt) {
		t.Fatalf("replayed terminals = %#v, want %#v", sink.batches[1], firstAttempt)
	}
}

type recordingSink struct {
	batches [][]Terminal
	err     error
}

func (s *recordingSink) WriteBatch(_ context.Context, terminals []Terminal) error {
	copyOfTerminals := append([]Terminal(nil), terminals...)
	s.batches = append(s.batches, copyOfTerminals)
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
