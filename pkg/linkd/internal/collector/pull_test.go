// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"linkd/internal/pluginruntime"
	"testing/synctest"
)

type progressReaderFunc func(context.Context, string, string) (map[string]any, error)

func (f progressReaderFunc) Load(ctx context.Context, tenant, source string) (map[string]any, error) {
	return f(ctx, tenant, source)
}

func TestMinuteCronSchedule(t *testing.T) {
	for _, tc := range []struct {
		now, want string
		minutes   int
	}{
		{"2026-11-14T12:13:59Z", "2026-11-14T12:15:00Z", 5},
		{"2026-11-14T12:56:00Z", "2026-11-14T13:00:00Z", 7},
		{"2026-11-14T23:59:59Z", "2026-11-15T00:00:00Z", 60},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := nextPullTime(now, tc.minutes).Format(time.RFC3339); got != tc.want {
			t.Fatalf("got %s want %s", got, tc.want)
		}
	}
}

func TestPullRunsWithProgressAndStopsOnRevocation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := make(chan pluginruntime.Request, 1)
		store := &resultFixture{}
		service := New(executorFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
			calls <- r
			cancel()
			return pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"a"}`)}, ProposedState: map[string]any{"meta_info": "new"}}, nil
		}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
			t.Error("published after schedule revocation")
			return nil
		}), store)
		progress := progressReaderFunc(func(_ context.Context, tenant, source string) (map[string]any, error) {
			if tenant != "tenant" || source != "source" {
				t.Error("query scope lost")
			}
			return map[string]any{"meta_info": "previous"}, nil
		})
		done := make(chan error, 1)
		go func() {
			done <- RunPull(ctx, pluginruntime.Request{TenantID: "tenant", SourceID: "source", Mode: "pull", IntervalMinutes: 1}, service, progress, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
		}()
		request := <-calls
		if request.State["meta_info"] != "previous" || request.RequestID != "pull/"+request.ReceivedAt {
			t.Fatal("pull facts not fixed")
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if store.found || store.completed {
			t.Fatal("revoked schedule persisted execution or progress")
		}
	})
}

func TestPullAndRecoveryAreDistinctSerialOperations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := make(chan pluginruntime.Request, 2)
		service := New(executorFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
			calls <- r
			return pluginruntime.Result{}, nil
		}), nil, nil)
		progress := progressReaderFunc(func(context.Context, string, string) (map[string]any, error) { return nil, nil })
		done := make(chan error, 1)
		go func() {
			done <- RunPull(ctx, pluginruntime.Request{Mode: "pull", IntervalMinutes: 1, RecoveryIntervalMinutes: 1}, service, progress, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
		}()
		first, second := <-calls, <-calls
		cancel()
		if first.Mode != "pull" || second.Mode != "recovery" || first.RequestID == second.RequestID || first.ReceivedAt != second.ReceivedAt {
			t.Fatalf("wrong scheduled operations: %+v %+v", first, second)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestPushSourceCanScheduleOnlyRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := make(chan string, 1)
		service := New(executorFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
			calls <- r.Mode
			cancel()
			return pluginruntime.Result{}, nil
		}), nil, nil)
		progress := progressReaderFunc(func(context.Context, string, string) (map[string]any, error) { return nil, nil })
		done := make(chan error, 1)
		go func() {
			done <- RunPull(ctx, pluginruntime.Request{Mode: "push", RecoveryIntervalMinutes: 1}, service, progress, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
		}()
		if mode := <-calls; mode != "recovery" {
			t.Fatal(mode)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})
}

func TestPullRecoversBeforeStartingNewSchedule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		pending := &recoveryFixture{ids: []string{"old"}, requests: map[string]pluginruntime.Request{"old": {RequestID: "old", TenantID: "tenant", SourceID: "source"}}, stores: map[string]*resultFixture{"old": {}}}
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		var order []string
		attempts := 0
		recovery := &Recovery{Pending: pending, BatchSize: 1, Timeout: time.Second, Logger: logger, Replay: func(ctx context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
			attempts++
			order = append(order, "recover")
			if attempts == 1 {
				return pluginruntime.Result{}, fmt.Errorf("temporary control plane failure")
			}
			return pluginruntime.Result{}, pending.Complete(ctx, r, pluginruntime.Result{})
		}}
		service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
			order = append(order, "new")
			cancel()
			return pluginruntime.Result{}, nil
		}), nil, nil)
		progress := progressReaderFunc(func(context.Context, string, string) (map[string]any, error) { return nil, nil })
		if err := RunPull(ctx, pluginruntime.Request{TenantID: "tenant", SourceID: "source", Mode: "pull", IntervalMinutes: 1}, service, progress, time.Second, logger, recovery); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(order, []string{"recover", "recover", "new"}) {
			t.Fatalf("schedule overtook recovery: %v", order)
		}
	})
}

func TestPullInitialStateDoesNotOverrideCommittedProgress(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				service := New(executorFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
					want := "initial"
					if committed {
						want = "committed"
					}
					if r.State["meta_info"] != want {
						t.Errorf("state=%v want=%s", r.State, want)
					}
					cancel()
					return pluginruntime.Result{}, nil
				}), nil, nil)
				progress := progressReaderFunc(func(context.Context, string, string) (map[string]any, error) {
					if committed {
						return map[string]any{"meta_info": "committed"}, nil
					}
					return nil, nil
				})
				err := RunPull(ctx, pluginruntime.Request{Mode: "pull", IntervalMinutes: 1, State: map[string]any{"meta_info": "initial"}}, service, progress, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
				if err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestPullStopsImmediatelyWhenTaskAuthorityIsLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		calls := 0
		service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
			calls++
			return pluginruntime.Result{}, ErrAuthorityLost
		}), nil, nil)
		progress := progressReaderFunc(func(context.Context, string, string) (map[string]any, error) { return nil, nil })
		err := RunPull(ctx, pluginruntime.Request{Mode: "pull", IntervalMinutes: 1}, service, progress, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
		if !errors.Is(err, ErrAuthorityLost) || calls != 1 {
			t.Fatalf("lost task kept scheduling: calls=%d err=%v", calls, err)
		}
	})
}

func TestPullDoesNotScheduleAfterRecoveryLosesAuthority(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	pending := &recoveryFixture{ids: []string{"old"}, requests: map[string]pluginruntime.Request{"old": {TenantID: "tenant", SourceID: "source", RequestID: "old"}}, stores: map[string]*resultFixture{"old": {}}}
	recovery := &Recovery{Pending: pending, BatchSize: 1, Timeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Replay: func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{}, ErrAuthorityLost
	}}
	err := RunPull(ctx, pluginruntime.Request{Mode: "pull", IntervalMinutes: 1}, nil, nil, time.Second, recovery.Logger, recovery)
	if !errors.Is(err, ErrAuthorityLost) {
		t.Fatalf("initial recovery authority loss concealed: %v", err)
	}
}
