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
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

type MessageDecoder interface {
	Decode(context.Context, []byte) (inputv2.DecodeResult, error)
}

type MessageProcessor interface {
	EvaluateMessage(context.Context, *inputv2.EvaluationInput) (MessageResult, error)
	EvaluateDetectOnly(context.Context, *inputv2.EvaluationInput) (MessageResult, error)
}

type FullMessageTaskBuilder interface {
	BuildFullMessageTask(context.Context, *inputv2.EvaluationInput) (RoutedMessageTask, error)
}

type MessageOutcomeKind string

const (
	MessageOutcomeCompleted MessageOutcomeKind = "COMPLETED"
	MessageOutcomeRejected  MessageOutcomeKind = "REJECTED"
)

type RejectedOutcome struct {
	Terminals []inputv2.Terminal
	Receipt   *contract.MessageReceiptV1
}

type MessageOutcome struct {
	Kind     MessageOutcomeKind
	Message  *MessageResult
	Rejected *RejectedOutcome
}

type MessageRouter struct {
	decoder   MessageDecoder
	processor MessageProcessor
}

func NewMessageRouter(decoder MessageDecoder, processor MessageProcessor) (*MessageRouter, error) {
	if decoder == nil || processor == nil {
		return nil, errors.New("alarmd coordinator: message decoder and processor are required")
	}
	return &MessageRouter{decoder: decoder, processor: processor}, nil
}

func (router *MessageRouter) Route(ctx context.Context, payload []byte) (MessageOutcome, error) {
	task, err := router.BuildMessageTask(ctx, payload)
	if err != nil {
		return MessageOutcome{}, err
	}
	if err := task.Prepare(ctx); err != nil {
		return MessageOutcome{}, err
	}
	return task.Evaluate(ctx)
}

func (router *MessageRouter) BuildMessageTask(ctx context.Context, payload []byte) (RoutedMessageTask, error) {
	if router == nil || router.decoder == nil || router.processor == nil {
		return nil, errors.New("alarmd coordinator: initialized message router is required")
	}
	decoded, err := router.decoder.Decode(ctx, payload)
	if err != nil {
		return nil, err
	}
	if decoded.Rejected {
		if decoded.Input != nil || decoded.Terminals.Len() == 0 {
			return nil, errors.New("alarmd coordinator: invalid rejected decode result")
		}
		return newPreparedMessageTask(MessageOutcome{
			Kind:     MessageOutcomeRejected,
			Rejected: &RejectedOutcome{Terminals: decoded.Terminals.Items(), Receipt: decoded.RejectedReceipt},
		}), nil
	}
	if decoded.Input == nil {
		return nil, errors.New("alarmd coordinator: accepted decode result has no input")
	}
	switch decoded.Input.ProcessingRoute() {
	case inputv2.RouteFullPipeline:
		if decoded.Input.RecordBatch().Len() != 0 && len(decoded.Input.PlanViews()) != 0 {
			if builder, ok := router.processor.(FullMessageTaskBuilder); ok {
				return builder.BuildFullMessageTask(ctx, decoded.Input)
			}
			return newDeferredMessageTask(func(ctx context.Context) (MessageOutcome, error) {
				message, err := router.processor.EvaluateMessage(ctx, decoded.Input)
				return completedMessageOutcome(message, err)
			}), nil
		}
		receipt, err := buildMessageReceipt(decoded.Input, nil, decoded.Terminals.Items())
		if err != nil {
			return nil, err
		}
		return newPreparedMessageTask(MessageOutcome{
			Kind: MessageOutcomeCompleted, Message: &MessageResult{Receipt: receipt},
		}), nil
	case inputv2.RouteNoEvaluation:
		receipt, err := buildMessageReceiptWithOptions(decoded.Input, nil, decoded.Terminals.Items(), receiptBuildOptions{
			DefaultUnavailable: true, QueryResultReason: decoded.Input.Execution().QueryResultReason,
		})
		if err != nil {
			return nil, err
		}
		return newPreparedMessageTask(MessageOutcome{
			Kind: MessageOutcomeCompleted, Message: &MessageResult{Receipt: receipt},
		}), nil
	case inputv2.RouteDetectOnly:
		return newDeferredMessageTask(func(ctx context.Context) (MessageOutcome, error) {
			message, err := router.processor.EvaluateDetectOnly(ctx, decoded.Input)
			return completedMessageOutcome(message, err)
		}), nil
	default:
		return nil, fmt.Errorf("alarmd coordinator: unsupported completeness route %s", decoded.Input.ProcessingRoute())
	}
}

type deferredMessageTask struct {
	prepare  func(context.Context) (MessageOutcome, error)
	outcome  MessageOutcome
	prepared bool
}

func newPreparedMessageTask(outcome MessageOutcome) *deferredMessageTask {
	return &deferredMessageTask{outcome: outcome, prepared: true}
}

func newDeferredMessageTask(prepare func(context.Context) (MessageOutcome, error)) *deferredMessageTask {
	return &deferredMessageTask{prepare: prepare}
}

func (*deferredMessageTask) RuntimeKeys() []RuntimeKey { return nil }

func (task *deferredMessageTask) Prepare(ctx context.Context) error {
	if task == nil {
		return errors.New("alarmd coordinator: message task is required")
	}
	if task.prepared {
		return nil
	}
	if task.prepare == nil {
		return errors.New("alarmd coordinator: message task preparation is required")
	}
	outcome, err := task.prepare(ctx)
	if err != nil {
		return err
	}
	task.outcome = outcome
	task.prepared = true
	return nil
}

func (task *deferredMessageTask) Evaluate(context.Context) (MessageOutcome, error) {
	if task == nil || !task.prepared {
		return MessageOutcome{}, errors.New("alarmd coordinator: prepared message task is required")
	}
	return task.outcome, nil
}

func completedMessageOutcome(message MessageResult, err error) (MessageOutcome, error) {
	if err != nil {
		return MessageOutcome{}, err
	}
	return MessageOutcome{Kind: MessageOutcomeCompleted, Message: &message}, nil
}
