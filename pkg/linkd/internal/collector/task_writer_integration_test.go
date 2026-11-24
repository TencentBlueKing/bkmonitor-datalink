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
	"os"
	"testing"
	"time"

	"linkd/internal/pluginruntime"
	"linkd/internal/taskdispatch"
)

func TestPullTaskTakeoverRejectsLateCompletionIntegration(t *testing.T) {
	if os.Getenv("LINKD_RECEIPTS_INTEGRATION") != "1" {
		t.Skip("set LINKD_RECEIPTS_INTEGRATION=1")
	}
	client := startReceiptsRedis(t)
	task := taskdispatch.Task{ID: "source:collector:0", Source: "source", Role: "collector", Worker: "worker", Epoch: 1, Phase: "running", Expires: time.Now().Add(time.Minute)}
	publishGrant := func(task taskdispatch.Task) {
		raw, err := json.Marshal(taskdispatch.State{Tasks: map[string]taskdispatch.Task{task.ID: task}})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(t.Context(), "linkd:dispatch:{takeover}:state", raw, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	publishGrant(task)
	receipts := NewRedisReceipts(client, "takeover", 0)
	old := receipts.WithWriter(taskdispatch.NewTaskWriter(client, "takeover", task))
	request := pluginruntime.Request{TenantID: "tenant", SourceID: "source", SourceVersion: 3, RequestID: "pull/2026-11-14T12:00:00Z", ReceivedAt: "2026-11-14T12:00:00Z", Mode: "pull"}
	entered, release := make(chan struct{}), make(chan struct{})
	oldService := New(executorFunc(func(ctx context.Context, _ pluginruntime.Request) (pluginruntime.Result, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return pluginruntime.Result{}, ctx.Err()
		}
		return pluginruntime.Result{ProposedState: map[string]any{"cursor": "stale"}}, nil
	}), nil, old)
	done := make(chan error, 1)
	go func() { _, err := oldService.Run(t.Context(), request); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old task did not execute")
	}
	task.Epoch++
	publishGrant(task)
	next := receipts.WithWriter(taskdispatch.NewTaskWriter(client, "takeover", task))
	nextService := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		return pluginruntime.Result{ProposedState: map[string]any{"cursor": "current"}}, nil
	}), nil, next)
	if _, err := nextService.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	// 不取消旧 worker Context，模拟暂停的旧进程在继任者完成后继续运行。
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, ErrAuthorityLost) {
			t.Fatalf("late old execution: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("old task did not finish")
	}
	progress, err := NewRedisProgress(client, "takeover").Load(t.Context(), "tenant", "source")
	if err != nil || progress["cursor"] != "current" {
		t.Fatalf("cursor regressed: %v %v", progress, err)
	}
	if err := old.Complete(t.Context(), request, pluginruntime.Result{ProposedState: map[string]any{"cursor": "overwritten"}}); !errors.Is(err, ErrAuthorityLost) {
		t.Fatalf("old completion: %v", err)
	}
	stored, err := next.LoadResult(t.Context(), request)
	if err != nil || stored == nil || !stored.Completed || stored.Result.ProposedState["cursor"] != "current" {
		t.Fatalf("result changed: %+v %v", stored, err)
	}
	if allowed, err := old.BeginAttempt(t.Context(), request); allowed || !errors.Is(err, ErrAuthorityLost) {
		t.Fatalf("old attempt reservation: %t %v", allowed, err)
	}
}
