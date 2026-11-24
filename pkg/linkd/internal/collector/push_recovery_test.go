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

	"linkd/internal/pluginruntime"
)

type pushPendingFixture struct {
	refs  []PushRequestRef
	scans int
}

func (f *pushPendingFixture) ScanPushPending(context.Context, uint64, int64) ([]PushRequestRef, uint64, error) {
	f.scans++
	return f.refs, 0, nil
}

func (*pushPendingFixture) LoadRequest(_ context.Context, tenant, source, id string) (pluginruntime.Request, error) {
	return pluginruntime.Request{TenantID: tenant, SourceID: source, RequestID: id, Mode: "push", SourceVersion: 3}, nil
}

func TestPushRecoveryBoundsScanAndDoesNotBlockOtherTenants(t *testing.T) {
	pending := &pushPendingFixture{refs: []PushRequestRef{{"one", "shared", "busy"}, {"one", "shared", "failed"}, {"two", "shared", "good"}}}
	var calls []string
	recovery := PushRecovery{Pending: pending, BatchSize: 1, Timeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Replay: func(ctx context.Context, r pluginruntime.Request) (pluginruntime.Result, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("unbounded replay")
		}
		calls = append(calls, r.TenantID+"/"+r.RequestID)
		switch r.RequestID {
		case "busy":
			return pluginruntime.Result{}, ErrExecutionBusy
		case "failed":
			return pluginruntime.Result{}, errors.New("temporary dependency failure")
		}
		return pluginruntime.Result{}, nil
	}}
	for count := 1; count <= 3; count++ {
		if err := recovery.Step(t.Context()); err != nil {
			t.Fatal(err)
		}
		if len(calls) != count {
			t.Fatal("batch limit ignored")
		}
	}
	if pending.scans != 1 || !reflect.DeepEqual(calls, []string{"one/busy", "one/failed", "two/good"}) {
		t.Fatalf("calls=%v scans=%d", calls, pending.scans)
	}
}

func TestPushRecoveryStartsImmediatelyAndStopsWithAuthority(t *testing.T) {
	for _, lost := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		pending := &pushPendingFixture{refs: []PushRequestRef{{"one", "source", "request"}, {"two", "source", "must-not-run"}}}
		calls := 0
		recovery := PushRecovery{Pending: pending, BatchSize: 2, Timeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Replay: func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
			calls++
			if lost {
				return pluginruntime.Result{}, ErrAuthorityLost
			}
			cancel()
			return pluginruntime.Result{}, nil
		}}
		done := make(chan error, 1)
		go func() { done <- recovery.Run(ctx) }()
		select {
		case err := <-done:
			if lost && !errors.Is(err, ErrAuthorityLost) {
				t.Fatalf("lost authorization concealed: %v", err)
			}
			if !lost && err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			cancel()
			t.Fatal("recovery did not start/stop promptly")
		}
		cancel()
		if calls != 1 {
			t.Fatalf("continued after cancellation: %d", calls)
		}
	}
}
