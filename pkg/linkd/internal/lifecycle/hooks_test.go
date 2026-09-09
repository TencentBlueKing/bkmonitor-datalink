// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type hookFunc func(context.Context, FinalHookInput) (FinalHookResult, error)

func (f hookFunc) Execute(ctx context.Context, input FinalHookInput) (FinalHookResult, error) {
	return f(ctx, input)
}

func successfulHookResult() FinalHookResult {
	return FinalHookResult{Transport: "memory", Destination: "test", MessageID: "stable"}
}

func TestNamedHooksIsolationAndFailureLogs(t *testing.T) {
	input := storetest.Alert("tenant", "alert", "event", "fp", "warning")
	cause := AlertChangeCause{Type: AlertChangeCauseSourceEvent, ID: "event"}
	calls := []string{}
	processor := newTestProcessor(t, memory.New(), NoopFinalHook{})
	processor.finalHooks = []NamedFinalHook{
		{Name: "broken", Hook: hookFunc(func(_ context.Context, in FinalHookInput) (FinalHookResult, error) {
			calls = append(calls, "broken")
			in.Alert.Labels["mutated"] = domain.NewBoolScalar(true)
			return successfulHookResult(), errors.New("failed")
		})},
		{Name: "panic", Hook: hookFunc(func(context.Context, FinalHookInput) (FinalHookResult, error) {
			calls = append(calls, "panic")
			panic("private payload")
		})},
		{Name: "invalid", Hook: hookFunc(func(context.Context, FinalHookInput) (FinalHookResult, error) {
			calls = append(calls, "invalid")
			return FinalHookResult{}, nil
		})},
		{Name: "timeout", Hook: hookFunc(func(ctx context.Context, _ FinalHookInput) (FinalHookResult, error) {
			calls = append(calls, "timeout")
			call, cancel := context.WithTimeout(ctx, time.Millisecond)
			defer cancel()
			<-call.Done()
			return successfulHookResult(), call.Err()
		})},
		{Name: "good", Hook: hookFunc(func(_ context.Context, in FinalHookInput) (FinalHookResult, error) {
			calls = append(calls, "good")
			if _, ok := in.Alert.Labels["mutated"]; ok {
				t.Fatal("hook snapshot aliased")
			}
			return successfulHookResult(), nil
		})},
	}
	logs, err := processor.runFinalHooks(context.Background(), cause, input, OutcomeAlertCreated)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 5 || !slices.Equal(calls, []string{"broken", "panic", "invalid", "timeout", "good"}) {
		t.Fatalf("calls=%v logs=%d", calls, len(logs))
	}
	ids := map[string]bool{}
	for i, log := range logs {
		if ids[log.LogID] {
			t.Fatal("hook instances collided")
		}
		ids[log.LogID] = true
		want := `"hook_failed"`
		if i == 4 {
			want = `"hook_succeeded"`
		}
		if string(log.Params["reason_code"]) != want {
			t.Fatalf("log=%+v", log)
		}
	}
	repeated, err := processor.runFinalHooks(context.Background(), cause, input, OutcomeAlertCreated)
	if err != nil {
		t.Fatal(err)
	}
	for i := range logs {
		if repeated[i].LogID != logs[i].LogID {
			t.Fatal("retry identity changed")
		}
	}
}

func TestHookParentCancellationStopsRemainingHooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	processor := newTestProcessor(t, memory.New(), NoopFinalHook{})
	processor.finalHooks = []NamedFinalHook{
		{Name: "cancel", Hook: hookFunc(func(context.Context, FinalHookInput) (FinalHookResult, error) {
			cancel()
			return successfulHookResult(), nil
		})},
		{Name: "later", Hook: hookFunc(func(context.Context, FinalHookInput) (FinalHookResult, error) {
			called = true
			return successfulHookResult(), nil
		})},
	}
	_, err := processor.runFinalHooks(ctx, AlertChangeCause{Type: AlertChangeCauseSourceEvent, ID: "event"}, storetest.Alert("tenant", "alert", "event", "fp", "warning"), OutcomeAlertCreated)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancel=%v later=%v", err, called)
	}
}

func TestMultiHooksPersistPerInstanceAndRotateInOrder(t *testing.T) {
	repo := memory.New()
	processor := newTestProcessor(t, repo, NoopFinalHook{})
	calls := []string{}
	for _, name := range []string{"first", "second"} {
		processor.finalHooks = append(processor.finalHooks, NamedFinalHook{Name: name, Hook: hookFunc(func(_ context.Context, in FinalHookInput) (FinalHookResult, error) {
			calls = append(calls, name+":"+string(in.Alert.Status))
			return successfulHookResult(), nil
		})})
	}
	opening := testEvent("opening", "warning")
	created := persistAndProcess(t, repo, processor, opening)
	entries, err := repo.ListAlertLogs(context.Background(), opening.BKTenantID, created.AlertID, store.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries.Logs) != 3 {
		t.Fatalf("create logs=%d", len(entries.Logs))
	}
	calls = nil
	upgrade := testEvent("upgrade", "critical")
	upgrade.OccurredAt = opening.OccurredAt.Add(time.Minute)
	upgrade.CreateAt = upgrade.OccurredAt
	persistAndProcess(t, repo, processor, upgrade)
	if !slices.Equal(calls, []string{"first:closed", "second:closed", "first:active", "second:active"}) {
		t.Fatalf("rotation order=%v", calls)
	}
}
