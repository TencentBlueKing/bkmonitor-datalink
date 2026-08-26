// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

func TestMessageRouterRejectsUnframedPayloadWithoutBusinessReceipt(t *testing.T) {
	t.Parallel()

	processor := &messageProcessorSpy{}
	router, err := NewMessageRouter(inputv2.New(g1ReaderLimits()), processor)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := router.Route(context.Background(), []byte(`{"schema":`))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != MessageOutcomeRejected || outcome.Message != nil || outcome.Rejected == nil {
		t.Fatalf("outcome = %#v, want typed REJECTED without MessageReceipt", outcome)
	}
	if processor.fullCalls != 0 || processor.detectOnlyCalls != 0 {
		t.Fatalf("processor calls = full:%d detect-only:%d, want 0/0", processor.fullCalls, processor.detectOnlyCalls)
	}
	terminals := outcome.Rejected.Terminals
	if len(terminals) != 1 || terminals[0].Scope != inputv2.ScopeMessage || terminals[0].ReasonCode != contract.ReasonMalformedJSON {
		t.Fatalf("REJECTED terminals = %#v", terminals)
	}
}

func TestMessageRouterCompletesFullEmptyWithoutEvaluation(t *testing.T) {
	t.Parallel()

	processor := &messageProcessorSpy{}
	router, err := NewMessageRouter(inputv2.New(g1ReaderLimits()), processor)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := router.Route(context.Background(), envelopePayloadForRoute(t, contract.QueryCompletenessFull, "", false))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != MessageOutcomeCompleted || outcome.Message == nil || outcome.Rejected != nil {
		t.Fatalf("outcome = %#v, want completed empty message", outcome)
	}
	receipt := outcome.Message.Receipt
	if receipt == nil || receipt.Status != contract.ReceiptStatusCompleted || receipt.Counts != (contract.ReceiptCountsV1{}) ||
		len(receipt.PerPlan) != 1 || receipt.PerPlan[0].PlanID != "1001" || receipt.PerPlan[0].Selected != 0 ||
		len(receipt.ReasonCounts) != 0 {
		t.Fatalf("FULL empty receipt = %#v", receipt)
	}
	if len(outcome.Message.Events) != 0 || len(outcome.Message.StateWrite.Items) != 0 ||
		processor.fullCalls != 0 || processor.detectOnlyCalls != 0 {
		t.Fatalf("FULL empty invoked evaluation or produced side effects: outcome=%#v calls=%d/%d", outcome, processor.fullCalls, processor.detectOnlyCalls)
	}
}

func TestMessageRouterDelegatesFullRecordsToEvaluationPipeline(t *testing.T) {
	t.Parallel()

	want := &contract.MessageReceiptV1{Status: contract.ReceiptStatusCompleted}
	processor := &messageProcessorSpy{fullResult: MessageResult{Receipt: want}}
	router, err := NewMessageRouter(inputv2.New(g1ReaderLimits()), processor)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := router.Route(context.Background(), encodeG1Envelope(t))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != MessageOutcomeCompleted || outcome.Message == nil || outcome.Message.Receipt != want || outcome.Rejected != nil {
		t.Fatalf("outcome = %#v, want delegated FULL result", outcome)
	}
	if processor.fullCalls != 1 || processor.detectOnlyCalls != 0 {
		t.Fatalf("processor calls = full:%d detect-only:%d, want 1/0", processor.fullCalls, processor.detectOnlyCalls)
	}
}

type messageProcessorSpy struct {
	fullCalls        int
	detectOnlyCalls  int
	fullResult       MessageResult
	detectOnlyResult MessageResult
}

func (processor *messageProcessorSpy) EvaluateMessage(context.Context, *inputv2.EvaluationInput) (MessageResult, error) {
	processor.fullCalls++
	return processor.fullResult, nil
}

func (processor *messageProcessorSpy) EvaluateDetectOnly(context.Context, *inputv2.EvaluationInput) (MessageResult, error) {
	processor.detectOnlyCalls++
	return processor.detectOnlyResult, nil
}

func envelopePayloadForRoute(t testing.TB, completeness, reason string, withRecords bool) []byte {
	t.Helper()
	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(encodeG1Envelope(t), &envelope); err != nil {
		t.Fatal(err)
	}
	if !withRecords {
		envelope.Records = []contract.CanonicalRecordV2{}
		ranges := []contract.SelectorRangeV2{}
		envelope.Selectors[0].Selector.Ranges = &ranges
	}
	envelope.QueryResult = contract.QueryResultV2{Completeness: completeness, ReasonCode: reason}
	var err error
	envelope.PayloadDigest, err = contract.DeriveExecutionEnvelopePayloadDigestV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
