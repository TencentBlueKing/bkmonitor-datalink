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
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/pluginruntime"
)

type recoveryFixture struct {
	ids      []string
	requests map[string]pluginruntime.Request
	stores   map[string]*resultFixture
}

func (f *recoveryFixture) PendingIDs(_ context.Context, _, _ string, limit int64) ([]string, error) {
	var pending []string
	for _, id := range f.ids {
		if !f.stores[id].completed {
			pending = append(pending, id)
		}
	}
	return pending[:min(int(limit), len(pending))], nil
}

func (f *recoveryFixture) LoadRequest(_ context.Context, _, _, id string) (pluginruntime.Request, error) {
	return f.requests[id], nil
}

func (f *recoveryFixture) SaveRequest(ctx context.Context, r pluginruntime.Request) (pluginruntime.Request, error) {
	return f.stores[r.RequestID].SaveRequest(ctx, r)
}

func (f *recoveryFixture) LoadResult(ctx context.Context, r pluginruntime.Request) (*StoredResult, error) {
	return f.stores[r.RequestID].LoadResult(ctx, r)
}

func (f *recoveryFixture) BeginAttempt(ctx context.Context, r pluginruntime.Request) (bool, error) {
	return f.stores[r.RequestID].BeginAttempt(ctx, r)
}

func (f *recoveryFixture) SaveResult(ctx context.Context, r pluginruntime.Request, result pluginruntime.Result) (pluginruntime.Result, error) {
	return f.stores[r.RequestID].SaveResult(ctx, r, result)
}

func (f *recoveryFixture) Complete(ctx context.Context, r pluginruntime.Request, result pluginruntime.Result) error {
	return f.stores[r.RequestID].Complete(ctx, r, result)
}

func TestRecoverPendingOrderAndFailureBoundary(t *testing.T) {
	for _, tc := range []struct {
		name          string
		failure       error
		finishFailure bool
		cancel        bool
		limit         int64
		want          int
		calls         []string
	}{
		{name: "success respects batch", limit: 1, want: 1, calls: []string{"first"}},
		{name: "terminal plugin failure continues", failure: &pluginruntime.Failure{Code: "plugin_error", Type: "ValueError"}, limit: 2, want: 2, calls: []string{"first", "second"}},
		{name: "unknown outcome stops batch", failure: errors.New("connection lost"), limit: 2, calls: []string{"first"}},
		{name: "terminal persistence failure stops batch", failure: &pluginruntime.Failure{Code: "plugin_error", Type: "ValueError"}, finishFailure: true, limit: 2, calls: []string{"first"}},
		{name: "revocation stops batch", cancel: true, limit: 2, calls: []string{"first"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fixture := &recoveryFixture{ids: []string{"first", "second"}, requests: map[string]pluginruntime.Request{}, stores: map[string]*resultFixture{}}
			for _, id := range fixture.ids {
				fixture.requests[id] = pluginruntime.Request{RequestID: id, TenantID: "tenant", SourceID: "source", SourceVersion: 3}
				fixture.stores[id] = &resultFixture{}
			}
			if tc.finishFailure {
				fixture.stores["first"].completeError = errors.New("store unavailable")
			}
			var calls []string
			service := New(executorFunc(func(callCtx context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
				if _, ok := callCtx.Deadline(); !ok {
					t.Fatal("recovery has no per-request budget")
				}
				calls = append(calls, r.RequestID)
				if r.RequestID == "first" {
					if tc.cancel {
						cancel()
					}
					return pluginruntime.Result{}, tc.failure
				}
				return pluginruntime.Result{}, nil
			}), nil, fixture)
			releases := &releaseStub{release: eventsource.Release{Version: 3, Spec: config.EventSource{RelatedTenantID: "tenant"}}}
			sources := NewPublishedSources(releases, func(context.Context, eventsource.Release) (*Service, error) { return service, nil })
			recovery := Recovery{Pending: fixture, Replay: sources.Replay, BatchSize: tc.limit, Timeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			count, err := recovery.Run(ctx, "tenant", "source")
			if count != tc.want || !reflect.DeepEqual(calls, tc.calls) {
				t.Fatalf("count=%d calls=%v err=%v", count, calls, err)
			}
			if tc.want == 0 && err == nil {
				t.Fatal("unfinished recovery reported success")
			}
			if tc.want > 0 && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func (f *recoveryFixture) BeginPublication(ctx context.Context, r pluginruntime.Request) (int, error) {
	return f.stores[r.RequestID].BeginPublication(ctx, r)
}
