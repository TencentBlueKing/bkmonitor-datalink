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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

func TestCriticalCompleterOrdersEventACKBeforeStateWrite(t *testing.T) {
	t.Parallel()

	events := []string{}
	completer, err := NewCriticalCompleter(
		triggerEventSinkFunc(func(context.Context, []contract.TriggerEventV1) error {
			events = append(events, "event_ack")
			return nil
		}),
		runtimeStateStoreFunc(func(context.Context, state.WriteWindowsRequest) (state.WriteWindowsResult, error) {
			events = append(events, "state_write")
			return state.WriteWindowsResult{Items: []state.WriteWindowResult{{Status: state.WritePersisted}}}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewCriticalCompleter() error = %v", err)
	}

	if err := completer.Complete(context.Background(), CriticalResult{
		Events:     []contract.TriggerEventV1{{EventID: "event-1"}},
		StateWrite: state.WriteWindowsRequest{Items: []state.LoadedWindow{{Key: "state-1"}}},
	}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if want := []string{"event_ack", "state_write"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("completion order = %v, want %v", events, want)
	}
}

func TestCriticalCompleterDoesNotWriteStateWhenEventACKFails(t *testing.T) {
	t.Parallel()

	want := errors.New("ack unavailable")
	stateCalls := 0
	completer, err := NewCriticalCompleter(
		triggerEventSinkFunc(func(context.Context, []contract.TriggerEventV1) error { return want }),
		runtimeStateStoreFunc(func(context.Context, state.WriteWindowsRequest) (state.WriteWindowsResult, error) {
			stateCalls++
			return state.WriteWindowsResult{}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewCriticalCompleter() error = %v", err)
	}

	err = completer.Complete(context.Background(), CriticalResult{
		Events:     []contract.TriggerEventV1{{EventID: "event-1"}},
		StateWrite: state.WriteWindowsRequest{Items: []state.LoadedWindow{{Key: "state-1"}}},
	})
	if !errors.Is(err, want) {
		t.Fatalf("Complete() error = %v, want %v", err, want)
	}
	if stateCalls != 0 {
		t.Fatalf("state writes = %d, want 0", stateCalls)
	}
}

type triggerEventSinkFunc func(context.Context, []contract.TriggerEventV1) error

func (function triggerEventSinkFunc) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	return function(ctx, events)
}

type runtimeStateStoreFunc func(context.Context, state.WriteWindowsRequest) (state.WriteWindowsResult, error)

func (function runtimeStateStoreFunc) WriteWindows(
	ctx context.Context,
	request state.WriteWindowsRequest,
) (state.WriteWindowsResult, error) {
	return function(ctx, request)
}
