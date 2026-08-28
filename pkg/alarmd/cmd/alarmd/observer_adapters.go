// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

type observedMessageDecoder struct {
	next     coordinator.MessageDecoder
	observer observability.Observer
}

func (decoder observedMessageDecoder) Decode(ctx context.Context, payload []byte) (inputv2.DecodeResult, error) {
	started := time.Now()
	result, err := decoder.next.Decode(ctx, payload)
	observation := observability.Observation{
		Component: observability.ComponentAdapter, Stage: observability.StageMessageDecoded,
		Result: observability.ResultSuccess, Operation: observability.OperationDecode,
		Direction: observability.DirectionInput, Duration: time.Since(started),
		Counts: observability.Counts{Messages: 1, Bytes: int64(len(payload))}, Err: err,
	}
	if err != nil {
		observation.Result = observability.Result(observability.ResultFailed)
		observation.ReasonCode = observability.ReasonInternalUnknown
	} else if result.Rejected {
		observation.Result = observability.ResultTerminal
		terminals := result.Terminals.Items()
		if len(terminals) > 0 {
			observation.ReasonCode = observability.ReasonCode(terminals[0].ReasonCode)
		}
		if result.RejectedReceipt != nil {
			observation.Trace.ExecutionID = result.RejectedReceipt.ExecutionID
			observation.Trace.MessageID = result.RejectedReceipt.MessageID
		}
	}
	observeRuntime(ctx, decoder.observer, observation)
	if err != nil || result.Rejected || result.Input == nil {
		return result, err
	}
	levels := int64(0)
	for _, plan := range result.Input.PlanViews() {
		levels += int64(len(plan.Snapshot().StrategyIR.Levels))
	}
	execution := result.Input.Execution()
	observeRuntime(ctx, decoder.observer, observability.Observation{
		Component: observability.ComponentAdapter, Stage: observability.StageRecordBatchReady,
		Result: observability.ResultSuccess, Operation: observability.OperationDecode,
		Direction: observability.DirectionInput, Counts: observability.Counts{
			Messages: 1, Records: int64(result.Input.RecordBatch().Len()), Plans: int64(len(result.Input.PlanSelections())),
			Levels: levels, Bytes: int64(len(payload)),
		},
		Trace: observability.TraceFields{
			ExecutionID: execution.ExecutionID, MessageID: execution.MessageID, QueryGroupKey: execution.QueryGroupKey,
		},
	})
	return result, nil
}

func detectObserver(observer observability.Observer) detect.Observer {
	return detect.ObserverFunc(func(ctx context.Context, source detect.Observation) {
		result := observability.Result(observability.ResultSuccess)
		switch source.Result {
		case detect.ObservationTerminal:
			result = observability.ResultTerminal
		case detect.ObservationFailed:
			result = observability.Result(observability.ResultFailed)
		}
		observeRuntime(ctx, observer, observability.Observation{
			Component: observability.ComponentDetect, Stage: observability.StageDetectCompleted,
			Result: result, Direction: observability.DirectionInternal,
			ReasonCode: observability.ReasonCode(source.ReasonCode), Duration: source.Duration,
			Counts: observability.Counts{
				Records: int64(source.Counts.EvaluatedRecords), Plans: int64(source.Counts.Plans),
				Levels: int64(source.Counts.CompiledLevels), Bytes: int64(source.Counts.EstimatedResultBytes),
			},
		})
	})
}

func stateObserver(observer observability.Observer) state.Observer {
	return state.ObserverFunc(func(ctx context.Context, source state.Observation) {
		result := observability.Result(observability.ResultSuccess)
		switch source.Result {
		case state.OperationPartial:
			result = observability.ResultDegraded
		case state.OperationFailed:
			result = observability.Result(observability.ResultFailed)
		}
		direction := observability.DirectionInternal
		if source.Operation == state.OperationLoad || source.Operation == state.OperationDecode {
			direction = observability.DirectionInput
		} else if source.Operation == state.OperationWrite || source.Operation == state.OperationEncode {
			direction = observability.DirectionOutput
		}
		observeRuntime(ctx, observer, observability.Observation{
			Component: observability.ComponentState, Stage: observability.Stage(source.Stage), Result: result,
			Operation: observability.Operation(source.Operation), Direction: direction,
			ReasonCode: observability.ReasonCode(source.ReasonCode), Duration: source.Duration,
			Counts: observability.Counts{
				Records: int64(source.TouchedPoints), Bytes: int64(source.RequestBytes + source.ResponseBytes),
				Keys: int64(source.TouchedKeys), StateBytes: int64(source.StateBytes),
			},
		})
	})
}

type observedTriggerEventRuntime struct {
	next     triggerEventRuntime
	observer observability.Observer
}

// observedReceiptRuntime only reports the final drain status. Individual
// losses are owned by the wrapped publisher diagnostics and must not be
// counted again here.
type observedReceiptRuntime struct {
	next   receiptRuntime
	logger *observability.Logger
}

func (runtime *observedReceiptRuntime) TryEnqueue(receipt *contract.MessageReceiptV1) bool {
	return runtime.next.TryEnqueue(receipt)
}

func (runtime *observedReceiptRuntime) Shutdown(ctx context.Context) enginekafka.ReceiptDrainResult {
	result := runtime.next.Shutdown(ctx)
	if runtime.logger == nil {
		return result
	}
	var logResult observability.Result
	switch result.Status {
	case enginekafka.ReceiptDrainWithDrop:
		logResult = observability.ResultDegraded
	case enginekafka.ReceiptDrainFailed:
		logResult = observability.ResultFailed
	default:
		return result
	}
	runtime.logger.Error(
		string(observability.StageCoverageGap), string(logResult), 0, 0,
		slog.String("receipt_drain_status", string(result.Status)),
	)
	return result
}

func receiptPublisherDiagnostics(
	observer observability.Observer,
	logger *observability.Logger,
	recorder *metric.Recorder,
) enginekafka.ReceiptPublisherDiagnostics {
	return enginekafka.ReceiptPublisherDiagnostics{
		OnValidated: recorder.RecordValidatedMessageReceipt,
		OnQueued:    recorder.RecordMessageReceiptQueued,
		OnACKed:     recorder.RecordMessageReceiptACKed,
		OnDrop: func(evidence enginekafka.ReceiptDropEvidence) {
			if receiptDropRepresentsLoss(evidence.Kind) {
				recorder.RecordMessageReceiptDropped(evidence.Count)
			}
			observeReceiptDrop(observer, logger, observability.StageCoverageGap, evidence)
		},
	}
}

func receiptDropRepresentsLoss(kind enginekafka.ReceiptDropKind) bool {
	switch kind {
	case enginekafka.ReceiptDropQueueMessages,
		enginekafka.ReceiptDropQueueBytes,
		enginekafka.ReceiptDropBrokerACKFailed,
		enginekafka.ReceiptDropClosed,
		enginekafka.ReceiptDropShutdownTimeout,
		enginekafka.ReceiptDropPublisherUnavailable:
		return true
	default:
		return false
	}
}

func observeReceiptDrop(
	observer observability.Observer,
	logger *observability.Logger,
	stage observability.Stage,
	evidence enginekafka.ReceiptDropEvidence,
	attributes ...slog.Attr,
) {
	if evidence.Count == 0 {
		return
	}
	observeRuntime(context.Background(), observer, observability.Observation{
		Component: observability.ComponentCoverage, Stage: stage,
		Result: observability.ResultDegraded, Operation: observability.OperationProduce,
		Direction: observability.DirectionOutput, ReasonCode: observability.ReasonCode(contract.ReasonAuditDrop),
		Counts: observability.Counts{Messages: int64(evidence.Count)},
	})
	if logger != nil {
		attributes = append([]slog.Attr{
			slog.String("reason_code", contract.ReasonAuditDrop),
			slog.String("drop_kind", string(evidence.Kind)),
			slog.Uint64("drop_count", evidence.Count),
			slog.Bool("coverage_acceptable", false),
		}, attributes...)
		logger.Error(
			string(stage), observability.ResultDropped, int(evidence.Count), 0,
			attributes...,
		)
	}
}

func (runtime *observedTriggerEventRuntime) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	started := time.Now()
	err := runtime.next.WriteBatch(ctx, events)
	result := observability.Result(observability.ResultSuccess)
	reason := observability.ReasonNone
	if err != nil {
		result = observability.Result(observability.ResultFailed)
		reason = observability.ReasonCode(contract.ReasonKafkaUnavailable)
	}
	observeRuntime(ctx, runtime.observer, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageOutputACKed,
		Result: result, Operation: observability.OperationACK, Direction: observability.DirectionOutput,
		ReasonCode: reason, Duration: time.Since(started), Counts: observability.Counts{
			Messages: int64(len(events)), Events: int64(len(events)),
		}, Err: err,
	})
	return err
}

func (runtime *observedTriggerEventRuntime) Shutdown(ctx context.Context) error {
	return runtime.next.Shutdown(ctx)
}

func observeRuntime(ctx context.Context, observer observability.Observer, observation observability.Observation) {
	if observer == nil {
		return
	}
	defer func() { _ = recover() }()
	observer.Observe(ctx, observation)
}

func offsetMarkDiagnostics(observer observability.Observer) func(enginekafka.OffsetMarkEvidence) {
	return func(evidence enginekafka.OffsetMarkEvidence) {
		result := observability.Result(observability.ResultSuccess)
		reason := observability.ReasonNone
		if evidence.Err != nil {
			result = observability.Result(observability.ResultFailed)
			reason = observability.ReasonCode(contract.ReasonKafkaUnavailable)
		}
		observeRuntime(context.Background(), observer, observability.Observation{
			Component: observability.ComponentConsumer, Stage: observability.StageOffsetMarked,
			Result: result, Operation: observability.OperationCommit, Direction: observability.DirectionInput,
			ReasonCode: reason, Duration: evidence.Duration, Err: evidence.Err,
			Trace: observability.TraceFields{
				Topic: evidence.Topic, Partition: evidence.Partition, PartitionKnown: true,
				Offset: evidence.NextOffset, OffsetKnown: true,
			},
		})
	}
}
