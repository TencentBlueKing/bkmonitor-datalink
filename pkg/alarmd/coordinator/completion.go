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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

type TriggerEventSink interface {
	WriteBatch(context.Context, []contract.TriggerEventV1) error
}

type RuntimeStateWriter interface {
	WriteWindows(context.Context, state.WriteWindowsRequest) (state.WriteWindowsResult, error)
}

type CriticalPhaseCompletion interface {
	CompleteEvents(context.Context, []contract.TriggerEventV1) error
	CompleteState(context.Context, state.WriteWindowsRequest) error
}

type CriticalResult struct {
	Events     []contract.TriggerEventV1
	StateWrite state.WriteWindowsRequest
}

type CriticalCompleter struct {
	events TriggerEventSink
	state  RuntimeStateWriter
}

func NewCriticalCompleter(events TriggerEventSink, runtimeState RuntimeStateWriter) (*CriticalCompleter, error) {
	if events == nil || runtimeState == nil {
		return nil, errors.New("alarmd coordinator: event sink and runtime state writer are required")
	}
	return &CriticalCompleter{events: events, state: runtimeState}, nil
}

func (completer *CriticalCompleter) Complete(ctx context.Context, result CriticalResult) error {
	if completer == nil || completer.events == nil || completer.state == nil {
		return errors.New("alarmd coordinator: initialized critical completer is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := completer.CompleteEvents(ctx, result.Events); err != nil {
		return err
	}
	return completer.CompleteState(ctx, result.StateWrite)
}

func (completer *CriticalCompleter) CompleteEvents(ctx context.Context, events []contract.TriggerEventV1) error {
	if completer == nil || completer.events == nil {
		return errors.New("alarmd coordinator: initialized critical event completer is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	if err := completer.events.WriteBatch(ctx, events); err != nil {
		return fmt.Errorf("alarmd coordinator: acknowledge TriggerEvent batch: %w", err)
	}
	return nil
}

func (completer *CriticalCompleter) CompleteState(ctx context.Context, request state.WriteWindowsRequest) error {
	if completer == nil || completer.state == nil {
		return errors.New("alarmd coordinator: initialized critical state completer is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(request.Items) == 0 {
		return nil
	}
	written, err := completer.state.WriteWindows(ctx, request)
	if err != nil {
		return fmt.Errorf("alarmd coordinator: write runtime state: %w", err)
	}
	if len(written.Items) != len(request.Items) {
		return errors.New("alarmd coordinator: runtime state writer returned incomplete results")
	}
	for index, item := range written.Items {
		if item.Status != state.WriteNoop && item.Status != state.WritePersisted {
			return fmt.Errorf("alarmd coordinator: runtime state write %d incomplete: status=%s: %w", index, item.Status, item.Err)
		}
	}
	return nil
}
