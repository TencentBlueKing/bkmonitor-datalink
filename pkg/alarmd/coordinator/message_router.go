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

	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

type MessageDecoder interface {
	Decode(context.Context, []byte) (inputv2.DecodeResult, error)
}

type MessageProcessor interface {
	EvaluateMessage(context.Context, *inputv2.EvaluationInput) (MessageResult, error)
	EvaluateDetectOnly(context.Context, *inputv2.EvaluationInput) (MessageResult, error)
}

type MessageOutcomeKind string

const (
	MessageOutcomeCompleted MessageOutcomeKind = "COMPLETED"
	MessageOutcomeRejected  MessageOutcomeKind = "REJECTED"
)

type RejectedOutcome struct {
	Terminals []inputv2.Terminal
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
	if router == nil || router.decoder == nil || router.processor == nil {
		return MessageOutcome{}, errors.New("alarmd coordinator: initialized message router is required")
	}
	decoded, err := router.decoder.Decode(ctx, payload)
	if err != nil {
		return MessageOutcome{}, err
	}
	if decoded.Rejected {
		if decoded.Input != nil || decoded.Terminals.Len() == 0 {
			return MessageOutcome{}, errors.New("alarmd coordinator: invalid rejected decode result")
		}
		return MessageOutcome{
			Kind:     MessageOutcomeRejected,
			Rejected: &RejectedOutcome{Terminals: decoded.Terminals.Items()},
		}, nil
	}
	if decoded.Input == nil {
		return MessageOutcome{}, errors.New("alarmd coordinator: accepted decode result has no input")
	}
	if hasUntrustedPlanIdentity(decoded.Input) {
		if decoded.Terminals.Len() == 0 {
			return MessageOutcome{}, errors.New("alarmd coordinator: untrusted Plan identity has no terminal evidence")
		}
		return MessageOutcome{
			Kind:     MessageOutcomeRejected,
			Rejected: &RejectedOutcome{Terminals: decoded.Terminals.Items()},
		}, nil
	}

	var message MessageResult
	switch decoded.Input.ProcessingRoute() {
	case inputv2.RouteFullPipeline:
		if decoded.Input.RecordBatch().Len() != 0 {
			message, err = router.processor.EvaluateMessage(ctx, decoded.Input)
			break
		}
		receipt, err := buildMessageReceipt(decoded.Input, nil, decoded.Terminals.Items())
		if err != nil {
			return MessageOutcome{}, err
		}
		message = MessageResult{Receipt: receipt}
	case inputv2.RouteNoEvaluation:
		receipt, err := buildMessageReceiptWithOptions(decoded.Input, nil, decoded.Terminals.Items(), receiptBuildOptions{
			DefaultUnavailable: true, QueryResultReason: decoded.Input.Execution().QueryResultReason,
		})
		if err != nil {
			return MessageOutcome{}, err
		}
		message = MessageResult{Receipt: receipt}
	case inputv2.RouteDetectOnly:
		message, err = router.processor.EvaluateDetectOnly(ctx, decoded.Input)
	default:
		return MessageOutcome{}, fmt.Errorf("alarmd coordinator: unsupported completeness route %s", decoded.Input.ProcessingRoute())
	}
	if err != nil {
		return MessageOutcome{}, err
	}
	return MessageOutcome{Kind: MessageOutcomeCompleted, Message: &message}, nil
}

func hasUntrustedPlanIdentity(input *inputv2.EvaluationInput) bool {
	for _, selection := range input.PlanSelections() {
		if selection.PlanID() == "" {
			return true
		}
	}
	return false
}
