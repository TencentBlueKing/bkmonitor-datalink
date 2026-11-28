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
	"io"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/pluginruntime"
	"linkd/internal/taskdispatch"
)

func TestPushLeadershipFencesAllWritesIntegration(t *testing.T) {
	if os.Getenv("LINKD_RECEIPTS_INTEGRATION") != "1" {
		t.Skip("set LINKD_RECEIPTS_INTEGRATION=1")
	}
	client := startReceiptsRedis(t)
	sources := eventsource.New(authorityDocuments{}, config.DefaultSeverityConfig())
	oldController, err := taskdispatch.NewController(t.Context(), client, sources, "fencing")
	if err != nil {
		t.Fatal(err)
	}
	key, token := oldController.Leadership()
	base := NewRedisReceipts(client, "fencing", 0)
	old := base.WithLeadership(key, token)
	request, err := old.Accept(t.Context(), pluginruntime.Request{TenantID: "tenant", SourceID: "source", Mode: "push"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = old.SaveRequest(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	computed := pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"original"}`)}, RequestID: request.RequestID, ProposedState: map[string]any{"cursor": "old"}}
	// 已计算输出在交接前冻结；继任者必须复用它，不回头执行插件。
	if _, err = old.SaveResult(t.Context(), request, computed); err != nil {
		t.Fatal(err)
	}
	// 让真实 taskdispatch 授权到期，再创建继任中心；不另造采集锁。
	if err = client.PExpire(t.Context(), key, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for client.Exists(wait, key).Val() != 0 {
		select {
		case <-wait.Done():
			t.Fatal("leader did not expire")
		case <-time.After(time.Millisecond):
		}
	}
	nextController, err := taskdispatch.NewController(t.Context(), client, sources, "fencing")
	if err != nil {
		t.Fatal(err)
	}
	next := base.WithLeadership(nextController.Leadership())
	writes := map[string]func() error{
		"accept":              func() error { _, err := old.Accept(t.Context(), request); return err },
		"input":               func() error { _, err := old.SaveRequest(t.Context(), request); return err },
		"execute attempt":     func() error { _, err := old.BeginAttempt(t.Context(), request); return err },
		"publication attempt": func() error { _, err := old.BeginPublication(t.Context(), request); return err },
		"output":              func() error { _, err := old.SaveResult(t.Context(), request, computed); return err },
		"complete":            func() error { return old.Complete(t.Context(), request, computed) },
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, ErrAuthorityLost) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	result, err := next.LoadResult(t.Context(), request)
	if err != nil || result.Completed {
		t.Fatalf("old writer completed request: %+v %v", result, err)
	}
	progress := NewRedisProgress(client, "fencing")
	if state, err := progress.Load(t.Context(), "tenant", "source"); err != nil || state != nil {
		t.Fatalf("stale cursor committed: %v %v", state, err)
	}
	refs, _, err := next.ScanPushPending(t.Context(), 0, 10)
	if err != nil || !reflect.DeepEqual(refs, []PushRequestRef{{"tenant", "source", request.RequestID}}) {
		t.Fatalf("pending changed: %v %v", refs, err)
	}
	publications := 0
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		t.Fatal("frozen output recomputed")
		return pluginruntime.Result{}, nil
	}),
		publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error { publications++; return nil }), next)
	if _, err = service.Run(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if publications != 1 {
		t.Fatal(publications)
	}
	refs, _, err = next.ScanPushPending(t.Context(), 0, 10)
	if err != nil || len(refs) != 0 {
		t.Fatalf("completion retained push index: %v %v", refs, err)
	}
	stale := computed
	stale.ProposedState = map[string]any{"cursor": "stale"}
	if err := old.Complete(t.Context(), request, stale); !errors.Is(err, ErrAuthorityLost) {
		t.Fatal(err)
	}
	state, err := progress.Load(t.Context(), "tenant", "source")
	if err != nil || state["cursor"] != "old" {
		t.Fatalf("successor state overwritten: %v %v", state, err)
	}
	if err = client.Del(t.Context(), key).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err = next.Accept(t.Context(), request); !errors.Is(err, ErrAuthorityLost) {
		t.Fatalf("expired authority admitted: %v", err)
	}
}

// 测试只需要空来源目录，调度状态及领导权仍由真实 Controller/Redis 管理。
type authorityDocuments struct{}

func (authorityDocuments) Get(context.Context, string, string) (json.RawMessage, string, error) {
	return nil, "", eventsource.ErrNotFound
}

func (authorityDocuments) Put(context.Context, string, string, string, json.RawMessage) error {
	return errors.New("unused document write")
}

func (authorityDocuments) List(context.Context, string, string, int) ([]json.RawMessage, error) {
	return nil, nil
}

func TestPushRecoveryFromReceiptBeforeExecutionSnapshotIntegration(t *testing.T) {
	if os.Getenv("LINKD_RECEIPTS_INTEGRATION") != "1" {
		t.Skip("set LINKD_RECEIPTS_INTEGRATION=1")
	}
	client := startReceiptsRedis(t)
	controller, err := taskdispatch.NewController(t.Context(), client, eventsource.New(authorityDocuments{}, config.DefaultSeverityConfig()), "accept-window")
	if err != nil {
		t.Fatal(err)
	}
	receipts := NewRedisReceipts(client, "accept-window", 0).WithLeadership(controller.Leadership())
	accepted := map[string]pluginruntime.Request{}
	for _, tenant := range []string{"one", "two"} {
		request, err := receipts.Accept(t.Context(), pluginruntime.Request{TenantID: tenant, SourceID: "shared", SourceVersion: 3, Mode: "push", Plugin: "rest_api_push", Configs: map[string]any{"original": tenant}})
		if err != nil {
			t.Fatal(err)
		}
		accepted[tenant] = request
		if client.Exists(t.Context(), receipts.requestKey(request)).Val() != 0 {
			t.Fatal("test already has execution snapshot")
		}
	}
	releases := &releaseStub{err: errors.New("must not consult current pointer"), record: eventsource.Record{Deleted: true, Published: 99}, release: eventsource.Release{ID: "shared", Version: 3, Spec: config.EventSource{Collection: &config.SourceCollection{TenantBindings: []config.CollectionTenantBinding{
		{TenantID: "one", Enabled: true}, {TenantID: "two", Enabled: true},
	}}}}}
	calls := 0
	executions := &Executions{}
	sources := NewPublishedSources(releases, func(context.Context, eventsource.Release) (*Service, error) {
		return New(executorFunc(func(_ context.Context, request pluginruntime.Request) (pluginruntime.Result, error) {
			calls++
			if !reflect.DeepEqual(request, accepted[request.TenantID]) {
				t.Fatalf("first input lost: %+v", request)
			}
			return pluginruntime.Result{RequestID: request.RequestID}, nil
		}), nil, receipts).WithExecutions(executions), nil
	})
	recovery := PushRecovery{Pending: receipts, Replay: sources.Replay, BatchSize: 1, Timeout: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for range 2 {
		if err := recovery.Step(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || releases.version != 3 {
		t.Fatalf("calls=%d version=%d", calls, releases.version)
	}
	for _, request := range accepted {
		stored, err := receipts.LoadResult(t.Context(), request)
		if err != nil || stored == nil || !stored.Completed {
			t.Fatalf("receipt not recovered: %+v %v", stored, err)
		}
	}
	if refs, _, err := receipts.ScanPushPending(t.Context(), 0, 10); err != nil || len(refs) != 0 {
		t.Fatalf("pending=%v error=%v", refs, err)
	}
}
