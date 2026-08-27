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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
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

func TestMessageRouterCompletesUntrustedPlanIdentityWithDiagnosticReceipt(t *testing.T) {
	t.Parallel()

	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(encodeG1Envelope(t), &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.PlanSet.EvaluationPlans[0].PlanID = "invalid"
	var err error
	envelope.PlanSet.PlanSetDigest, err = contract.DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatal(err)
	}
	envelope.PayloadDigest, err = contract.DeriveExecutionEnvelopePayloadDigestV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatal(err)
	}

	processor := &messageProcessorSpy{}
	router, err := NewMessageRouter(inputv2.New(g1ReaderLimits()), processor)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := router.Route(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != MessageOutcomeCompleted || outcome.Message == nil || outcome.Rejected != nil {
		t.Fatalf("outcome = %#v, want completed isolation without an untrusted per-Plan identity", outcome)
	}
	receipt := outcome.Message.Receipt
	if receipt == nil || receipt.Status != contract.ReceiptStatusCompletedWithTerminal ||
		receipt.Counts != (contract.ReceiptCountsV1{Received: 1}) ||
		len(receipt.PerPlan) != 0 || receipt.ExecutionID != envelope.ExecutionID || receipt.MessageID != envelope.MessageID {
		t.Fatalf("Receipt = %#v, want trusted message identity and diagnostic-only Plan terminal", receipt)
	}
	if !receiptHasExactReason(receipt, contract.ReasonPlanInvalid, 1) {
		t.Fatalf("reason counts = %#v, want one untrusted Plan diagnostic fact", receipt.ReasonCounts)
	}
	if processor.fullCalls != 0 || processor.detectOnlyCalls != 0 {
		t.Fatalf("processor calls = full:%d detect-only:%d, want 0/0", processor.fullCalls, processor.detectOnlyCalls)
	}
}

func TestMessageRouterCompletesFormalTerminalPlanWithConservedReceipt(t *testing.T) {
	t.Parallel()

	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(encodeG1Envelope(t), &envelope); err != nil {
		t.Fatal(err)
	}
	executable := envelope.PlanSet.EvaluationPlans[0]
	envelope.PlanSet.EvaluationPlans[0] = contract.EvaluationPlanV2{
		PlanID: executable.PlanID, StrategyRef: executable.StrategyRef,
		TerminalReasonCode: contract.ReasonMultipleEvaluationUnitsUnsupported,
	}
	var err error
	envelope.PlanSet.PlanSetDigest, err = contract.DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatal(err)
	}
	envelope.PayloadDigest, err = contract.DeriveExecutionEnvelopePayloadDigestV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatal(err)
	}

	processor := &messageProcessorSpy{}
	router, err := NewMessageRouter(inputv2.New(g1ReaderLimits()), processor)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := router.Route(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != MessageOutcomeCompleted || outcome.Message == nil || outcome.Rejected != nil {
		t.Fatalf("outcome = %#v, want completed terminal Plan", outcome)
	}
	receipt := outcome.Message.Receipt
	wantCounts := contract.ReceiptCountsV1{Received: 1, Selected: 1, Terminal: 1}
	if receipt == nil || receipt.Status != contract.ReceiptStatusCompletedWithTerminal || receipt.Counts != wantCounts ||
		len(receipt.PerPlan) != 1 || receipt.PerPlan[0].PlanID != "1001" || receipt.PerPlan[0].Selected != 1 || receipt.PerPlan[0].Terminal != 1 ||
		!receiptHasExactReason(receipt, contract.ReasonMultipleEvaluationUnitsUnsupported, 1) {
		t.Fatalf("terminal Plan receipt = %#v", receipt)
	}
	if processor.fullCalls != 0 || processor.detectOnlyCalls != 0 || len(outcome.Message.Events) != 0 || len(outcome.Message.StateWrite.Items) != 0 {
		t.Fatalf("terminal Plan invoked Evaluation or produced side effects: outcome=%#v calls=%d/%d", outcome, processor.fullCalls, processor.detectOnlyCalls)
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

func TestMessageRouterCompletesUnavailableWithoutEvaluation(t *testing.T) {
	t.Parallel()

	processor := &messageProcessorSpy{}
	router, err := NewMessageRouter(inputv2.New(g1ReaderLimits()), processor)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := router.Route(context.Background(), envelopePayloadForRoute(
		t, contract.QueryCompletenessUnavailable, contract.ReasonQueryUnavailable, false,
	))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != MessageOutcomeCompleted || outcome.Message == nil || outcome.Rejected != nil {
		t.Fatalf("outcome = %#v, want completed UNAVAILABLE", outcome)
	}
	receipt := outcome.Message.Receipt
	if receipt == nil || receipt.Status != contract.ReceiptStatusCompleted || receipt.Counts != (contract.ReceiptCountsV1{}) ||
		len(receipt.PerPlan) != 1 || receipt.PerPlan[0].Selected != 0 ||
		!receiptHasExactReason(receipt, contract.ReasonQueryUnavailable, 1) {
		t.Fatalf("UNAVAILABLE receipt = %#v", receipt)
	}
	if len(outcome.Message.Events) != 0 || len(outcome.Message.StateWrite.Items) != 0 ||
		processor.fullCalls != 0 || processor.detectOnlyCalls != 0 {
		t.Fatalf("UNAVAILABLE invoked evaluation or produced side effects: outcome=%#v calls=%d/%d", outcome, processor.fullCalls, processor.detectOnlyCalls)
	}
}

func TestMessageRouterDelegatesPartialRecordsToDetectOnly(t *testing.T) {
	t.Parallel()

	want := &contract.MessageReceiptV1{Status: contract.ReceiptStatusCompleted}
	processor := &messageProcessorSpy{detectOnlyResult: MessageResult{Receipt: want}}
	router, err := NewMessageRouter(inputv2.New(g1ReaderLimits()), processor)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := router.Route(context.Background(), envelopePayloadForRoute(
		t, contract.QueryCompletenessPartial, contract.ReasonQueryPartial, true,
	))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Kind != MessageOutcomeCompleted || outcome.Message == nil || outcome.Message.Receipt != want || outcome.Rejected != nil {
		t.Fatalf("outcome = %#v, want delegated PARTIAL result", outcome)
	}
	if processor.fullCalls != 0 || processor.detectOnlyCalls != 1 {
		t.Fatalf("processor calls = full:%d detect-only:%d, want 0/1", processor.fullCalls, processor.detectOnlyCalls)
	}
}

func TestEvaluationPipelinePartialDetectOnlyPreservesQueryReasonAndTerminalIsolation(t *testing.T) {
	t.Parallel()

	payload := payloadWithCompleteness(
		t, encodeSharedG1Envelope(t), contract.QueryCompletenessPartial, contract.ReasonQueryPartial, false,
	)
	decoded, err := inputv2.New(g1ReaderLimits()).Decode(context.Background(), payload)
	if err != nil || decoded.Rejected || decoded.Input == nil {
		t.Fatalf("Decode() = %#v, %v", decoded, err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), g1CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	semantics, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	detectCalls := 0
	detector := detectionEvaluatorFunc(func(_ context.Context, request detect.EvaluateRequest) (detect.DetectionBatch, error) {
		detectCalls++
		if detectCalls == 1 {
			return detect.DetectionBatch{}, &detect.BudgetError{
				Scope: detect.BudgetScopePlan, PlanID: "1001", ReasonCode: contract.ReasonPlanBudgetExceeded,
			}
		}
		if len(request.Plans) != 1 || request.Plans[0].View.PlanID() != "1002" {
			t.Fatalf("Detect retry plans = %#v, want only sibling 1002", request.Plans)
		}
		return detect.DetectionBatch{
			Completeness: contract.QueryCompletenessPartial, ExecutionMode: detect.ExecutionModeStandard,
			DetectionCoverage: contract.QueryCompletenessPartial,
		}, nil
	})
	providerCalls, stateCalls, admissionCalls := 0, 0, 0
	pipeline, err := NewEvaluationPipeline(PipelineOptions{
		Compiler: compiler, Detector: detector,
		EffectiveTime: effectiveTimeProviderFunc(func(context.Context, []strategy.EffectiveTimeRequest) ([]strategy.EffectiveTimeFact, error) {
			providerCalls++
			return nil, nil
		}),
		State: runtimeStateLoaderFunc(func(context.Context, state.LoadWindowsRequest) (state.LoadWindowsResult, error) {
			stateCalls++
			return state.LoadWindowsResult{}, nil
		}),
		StateAdmission: stateBatchAdmitterFunc(func(context.Context, state.WriteWindowsRequest) error {
			admissionCalls++
			return nil
		}),
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: semantics.StateSchemaVersion, CodecSemanticsVersion: semantics.CodecSemanticsVersion,
			IdentitySchemaDigest: semantics.IdentitySchemaDigest, SourceTimeSemanticsVersion: semantics.SourceTimeSemanticsVersion,
			HistoryCellSemanticsVersion: semantics.HistoryCellSemanticsVersion,
		},
		DetectLimits: detect.ExecutionLimits{
			MaxPlans: 4, MaxSelectedRecordsPerPlan: 100, MaxSeriesPerPlan: 100, MaxRecordsPerSeries: 100,
			MaxLevelFacts: 1_000, MaxPredicateEvaluations: 1_000, MaxResultBytes: 1 << 20,
		},
		TriggerLimits: trigger.EvaluationLimitsV2{MaxLevels: 8},
	})
	if err != nil {
		t.Fatal(err)
	}

	message, err := pipeline.EvaluateDetectOnly(context.Background(), decoded.Input)
	if err != nil {
		t.Fatal(err)
	}
	if detectCalls != 2 || providerCalls != 0 || stateCalls != 0 || admissionCalls != 0 ||
		len(message.Events) != 0 || len(message.StateWrite.Items) != 0 {
		t.Fatalf("PARTIAL calls = detect:%d provider:%d state:%d admission:%d, result=%#v", detectCalls, providerCalls, stateCalls, admissionCalls, message)
	}
	wantCounts := contract.ReceiptCountsV1{Received: 2, Selected: 4, Unavailable: 2, Terminal: 2}
	if message.Receipt == nil || message.Receipt.Status != contract.ReceiptStatusCompletedWithTerminal ||
		message.Receipt.Counts != wantCounts || len(message.Receipt.PerPlan) != 2 ||
		message.Receipt.PerPlan[0].PlanID != "1001" || message.Receipt.PerPlan[0].Terminal != 2 ||
		message.Receipt.PerPlan[1].PlanID != "1002" || message.Receipt.PerPlan[1].Unavailable != 2 ||
		!receiptHasExactReason(message.Receipt, contract.ReasonQueryPartial, 1) ||
		!receiptHasExactReason(message.Receipt, contract.ReasonPlanBudgetExceeded, 2) {
		t.Fatalf("PARTIAL receipt = %#v", message.Receipt)
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

type runtimeStateLoaderFunc func(context.Context, state.LoadWindowsRequest) (state.LoadWindowsResult, error)

func (function runtimeStateLoaderFunc) LoadWindows(
	ctx context.Context,
	request state.LoadWindowsRequest,
) (state.LoadWindowsResult, error) {
	return function(ctx, request)
}

func receiptHasExactReason(receipt *contract.MessageReceiptV1, reason string, count uint64) bool {
	for _, item := range receipt.ReasonCounts {
		if item.ReasonCode == reason {
			return item.Count == count
		}
	}
	return false
}

func envelopePayloadForRoute(t testing.TB, completeness, reason string, withRecords bool) []byte {
	t.Helper()
	return payloadWithCompleteness(t, encodeG1Envelope(t), completeness, reason, !withRecords)
}

func payloadWithCompleteness(t testing.TB, encoded []byte, completeness, reason string, clearRecords bool) []byte {
	t.Helper()
	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	if clearRecords {
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
