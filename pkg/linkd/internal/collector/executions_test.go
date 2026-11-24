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
	"testing"
	"time"

	"linkd/internal/pluginruntime"
)

func TestExecutionsExcludeSameRequestAndReleaseAfterCancel(t *testing.T) {
	executions := &Executions{}
	entered := make(chan struct{})
	service := New(executorFunc(func(ctx context.Context, _ pluginruntime.Request) (pluginruntime.Result, error) {
		close(entered)
		<-ctx.Done()
		return pluginruntime.Result{}, ctx.Err()
	}), nil, nil).WithExecutions(executions)
	request := pluginruntime.Request{TenantID: "tenant", SourceID: "source", RequestID: "request"}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := service.Run(ctx, request); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("execution not started")
	}
	replay := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{}, nil
	}), nil, nil).WithExecutions(executions)
	if _, err := replay.Run(t.Context(), request); !errors.Is(err, ErrExecutionBusy) {
		t.Fatalf("duplicate execution: %v", err)
	}
	for _, other := range []pluginruntime.Request{
		{TenantID: "other", SourceID: request.SourceID, RequestID: request.RequestID},
		{TenantID: request.TenantID, SourceID: "other", RequestID: request.RequestID},
		{TenantID: request.TenantID, SourceID: request.SourceID, RequestID: "other"},
	} {
		if _, err := replay.Run(t.Context(), other); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("execution did not cancel")
	}
	if _, err := replay.Run(t.Context(), request); err != nil {
		t.Fatalf("registration leaked: %v", err)
	}
}
