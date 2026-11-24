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
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"linkd/internal/pluginruntime"
)

// TestRedisReceiptsIntegration 显式启动临时 Redis，不使用开发者已有数据库。
func TestRedisReceiptsIntegration(t *testing.T) {
	if os.Getenv("LINKD_RECEIPTS_INTEGRATION") != "1" {
		t.Skip("set LINKD_RECEIPTS_INTEGRATION=1")
	}
	client := startReceiptsRedis(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var err error
	receipts := NewRedisReceipts(client, "test", 0)
	scheduled := pluginruntime.Request{TenantID: "tenant:a", SourceID: "source", RequestID: "pull/2026-11-14T12:00:00Z", SourceVersion: 7, Mode: "pull", ReceivedAt: "2026-11-14T12:00:00Z", State: map[string]any{"meta_info": "first"}, Configs: map[string]any{"host": "initial"}}
	if _, err = receipts.SaveRequest(ctx, scheduled); err != nil {
		t.Fatal(err)
	}
	changed := scheduled
	changed.State = map[string]any{"meta_info": "later"}
	changed.Configs = map[string]any{"host": "changed"}
	if _, err = receipts.SaveRequest(ctx, changed); err != nil {
		t.Fatal(err)
	}
	restoredInput, err := NewRedisReceipts(client, "test", 0).LoadRequest(ctx, scheduled.TenantID, scheduled.SourceID, scheduled.RequestID)
	if err != nil || !reflect.DeepEqual(scheduled, restoredInput) {
		t.Fatalf("scheduled input changed: %+v %v", restoredInput, err)
	}
	if _, err = receipts.LoadRequest(ctx, "other", scheduled.SourceID, scheduled.RequestID); !errors.Is(err, redis.Nil) {
		t.Fatalf("cross tenant execution input: %v", err)
	}
	older := scheduled
	older.RequestID, older.ReceivedAt = "pull/2026-11-14T11:00:00Z", "2026-11-14T11:00:00Z"
	if _, err = receipts.SaveRequest(ctx, older); err != nil {
		t.Fatal(err)
	}
	ids, err := receipts.PendingIDs(ctx, scheduled.TenantID, scheduled.SourceID, 1)
	if err != nil || !reflect.DeepEqual(ids, []string{older.RequestID}) {
		t.Fatalf("pending order/batch: %v %v", ids, err)
	}
	if err = receipts.Complete(ctx, older, pluginruntime.Result{}); err != nil {
		t.Fatal(err)
	}
	if _, err = receipts.SaveRequest(ctx, older); err != nil {
		t.Fatal(err)
	}
	ids, err = receipts.PendingIDs(ctx, scheduled.TenantID, scheduled.SourceID, 10)
	if err != nil || !reflect.DeepEqual(ids, []string{scheduled.RequestID}) {
		t.Fatalf("completed request reentered pending: %v %v", ids, err)
	}
	if ids, err = receipts.PendingIDs(ctx, "other", scheduled.SourceID, 10); err != nil || len(ids) != 0 {
		t.Fatalf("cross tenant pending: %v %v", ids, err)
	}
	if _, err = receipts.PendingIDs(ctx, scheduled.TenantID, scheduled.SourceID, 0); err == nil {
		t.Fatal("unbounded pending query accepted")
	}
	request := pluginruntime.Request{TenantID: "tenant:a", SourceID: "来源 /", Plugin: "rest_api_push", Mode: "push", Configs: map[string]any{"version": "initial"}, Data: json.RawMessage(`{"title":"first"}`)}
	saved, err := receipts.Accept(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RequestID == "" || saved.ReceivedAt == "" {
		t.Fatal("missing persistent receipt identity")
	}
	ids, err = receipts.PendingIDs(ctx, saved.TenantID, saved.SourceID, 10)
	if err != nil || !reflect.DeepEqual(ids, []string{saved.RequestID}) {
		t.Fatalf("accepted receipt not discoverable before execution: %v %v", ids, err)
	}
	acceptedInput, err := NewRedisReceipts(client, "test", 0).LoadRequest(ctx, saved.TenantID, saved.SourceID, saved.RequestID)
	if err != nil || !reflect.DeepEqual(acceptedInput, saved) {
		t.Fatalf("accepted input not recoverable before snapshot: %+v %v", acceptedInput, err)
	}
	computed := pluginruntime.Result{Events: []json.RawMessage{json.RawMessage(`{"fingerprint":"original"}`)}}
	if _, err = receipts.SaveResult(ctx, saved, computed); err != nil {
		t.Fatal(err)
	}
	retained, err := receipts.SaveResult(ctx, saved, pluginruntime.Result{})
	if err != nil || len(retained.Events) != 1 {
		t.Fatalf("computed batch replaced: %+v %v", retained, err)
	}
	stored, err := receipts.LoadResult(ctx, saved)
	if err != nil || stored == nil || stored.Completed {
		t.Fatalf("computation prematurely complete: %+v %v", stored, err)
	}
	if err = receipts.Complete(ctx, saved, retained); err != nil {
		t.Fatal(err)
	}
	if _, err = receipts.SaveResult(ctx, saved, pluginruntime.Result{}); err != nil {
		t.Fatal(err)
	}
	stored, err = NewRedisReceipts(client, "test", 0).LoadResult(ctx, saved)
	if err != nil || stored == nil || !stored.Completed || len(stored.Result.Events) != 1 {
		t.Fatalf("completion not retained: %+v %v", stored, err)
	}
	other := saved
	other.TenantID = "other"
	if stored, err = receipts.LoadResult(ctx, other); err != nil || stored != nil {
		t.Fatalf("cross tenant completion: %+v %v", stored, err)
	}
	request.Configs["version"] = "changed"
	recovered, err := NewRedisReceipts(client, "test", 0).Load(ctx, request.TenantID, request.SourceID, saved.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.RequestID != saved.RequestID || recovered.ReceivedAt != saved.ReceivedAt || recovered.Configs["version"] != "initial" {
		t.Fatal("replay did not preserve receipt facts")
	}
	if !reflect.DeepEqual(recovered.Data, saved.Data) {
		t.Fatal("payload changed")
	}
	if _, err = receipts.Load(ctx, "other", request.SourceID, saved.RequestID); !errors.Is(err, redis.Nil) {
		t.Fatalf("cross-tenant receipt: %v", err)
	}
	again, err := receipts.Accept(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if again.RequestID == saved.RequestID {
		t.Fatal("independent HTTP receipt reused identity")
	}
	progress := NewRedisProgress(client, "test")
	if state, err := progress.Load(ctx, request.TenantID, request.SourceID); err != nil || state != nil {
		t.Fatalf("initial progress: %v %v", state, err)
	}
	computed.ProposedState = map[string]any{"meta_info": "cursor-1"}
	if err = receipts.Complete(ctx, again, computed); err != nil {
		t.Fatal(err)
	}
	state, err := NewRedisProgress(client, "test").Load(ctx, request.TenantID, request.SourceID)
	if err != nil || state["meta_info"] != "cursor-1" {
		t.Fatalf("progress restore: %v %v", state, err)
	}
	if state, err = progress.Load(ctx, "other", request.SourceID); err != nil || state != nil {
		t.Fatalf("cross tenant progress: %v %v", state, err)
	}
	// Redis 已原子提交但响应丢失：调用方收到失败，恢复后必须发现完成，不能再次发布。
	lostReply := errors.New("simulated lost Redis completion reply")
	client.AddHook(&completionReplyHook{failure: lostReply})
	executions, publications := 0, 0
	service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		executions++
		return pluginruntime.Result{Events: computed.Events, ProposedState: map[string]any{"meta_info": "cursor-2"}}, nil
	}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
		publications++
		return nil
	}), receipts)
	next, err := receipts.Accept(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Run(ctx, next); !errors.Is(err, lostReply) {
		t.Fatalf("lost completion reply hidden: %v", err)
	}
	stored, err = receipts.LoadResult(ctx, next)
	if err != nil || stored == nil || !stored.Completed {
		t.Fatalf("completion missing after lost reply: %+v %v", stored, err)
	}
	state, err = progress.Load(ctx, request.TenantID, request.SourceID)
	if err != nil || state["meta_info"] != "cursor-2" {
		t.Fatalf("progress not committed with completion: %v %v", state, err)
	}
	service.results = NewRedisReceipts(client, "test", 0)
	if _, err = service.Run(ctx, next); err != nil {
		t.Fatal(err)
	}
	if executions != 1 || publications != 1 {
		t.Fatalf("completed replay repeated effects: %d %d", executions, publications)
	}
	if ids, err = receipts.PendingIDs(ctx, next.TenantID, next.SourceID, 10); err != nil || len(ids) != 0 {
		t.Fatalf("completed transaction retained pending record: %v %v", ids, err)
	}
	failedRequest, err := receipts.Accept(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	failedCalls := 0
	failedService := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
		failedCalls++
		return pluginruntime.Result{ProposedState: map[string]any{"meta_info": "must-not-commit"}}, &pluginruntime.Failure{Code: "plugin_error", Type: "ValueError"}
	}), nil, receipts)
	for range 2 {
		_, err = failedService.Run(ctx, failedRequest)
		var failure *pluginruntime.Failure
		if !errors.As(err, &failure) || failure.Code != "plugin_error" {
			t.Fatalf("stored failure lost: %v", err)
		}
	}
	if failedCalls != 1 {
		t.Fatal("terminal failure reexecuted plugin")
	}
	if ids, err = receipts.PendingIDs(ctx, failedRequest.TenantID, failedRequest.SourceID, 10); err != nil || len(ids) != 0 {
		t.Fatalf("terminal failure retained pending record: %v %v", ids, err)
	}
	state, err = progress.Load(ctx, request.TenantID, request.SourceID)
	if err != nil || state["meta_info"] != "cursor-2" {
		t.Fatalf("failed plugin advanced progress: %v %v", state, err)
	}
	budgetRequest, err := receipts.Accept(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	unknown := errors.New("execution response lost")
	budgetCalls := 0
	for attempt := 1; attempt <= 4; attempt++ {
		restarted := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
			budgetCalls++
			return pluginruntime.Result{}, unknown
		}), nil, NewRedisReceipts(client, "test", 0))
		_, err = restarted.Run(ctx, budgetRequest)
		if attempt <= 3 {
			if !errors.Is(err, unknown) {
				t.Fatalf("unexpected replay failure: %v", err)
			}
		} else {
			var failure *pluginruntime.Failure
			if !errors.As(err, &failure) || failure.Code != "attempts_exhausted" {
				t.Fatalf("attempt budget reset: %v", err)
			}
		}
	}
	if budgetCalls != 3 {
		t.Fatalf("executions=%d", budgetCalls)
	}
	if ids, err = receipts.PendingIDs(ctx, budgetRequest.TenantID, budgetRequest.SourceID, 10); err != nil || len(ids) != 0 {
		t.Fatalf("exhausted request retained pending: %v %v", ids, err)
	}
	otherBudget := budgetRequest
	otherBudget.TenantID = "another-tenant"
	if allowed, err := receipts.BeginAttempt(ctx, otherBudget); err != nil || !allowed {
		t.Fatalf("tenant attempt budgets shared: %v %v", allowed, err)
	}
	publicationRequest, err := receipts.Accept(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	publicationCalls, computationCalls := 0, 0
	for range 4 {
		service := New(executorFunc(func(context.Context, pluginruntime.Request) (pluginruntime.Result, error) {
			computationCalls++
			return pluginruntime.Result{Events: computed.Events, ProposedState: map[string]any{"meta_info": "must-not-commit"}}, nil
		}), publisherFunc(func(context.Context, pluginruntime.Request, pluginruntime.Result) error {
			publicationCalls++
			return errors.New("publication unavailable")
		}), NewRedisReceipts(client, "test", 0))
		if _, err := service.Run(ctx, publicationRequest); err == nil {
			t.Fatal("failed publication returned success")
		}
	}
	if publicationCalls != 3 || computationCalls != 1 {
		t.Fatalf("publish=%d execute=%d", publicationCalls, computationCalls)
	}
	if ids, err = receipts.PendingIDs(ctx, request.TenantID, request.SourceID, 10); err != nil || len(ids) != 0 {
		t.Fatalf("publication exhaustion retained pending: %v %v", ids, err)
	}
	state, err = progress.Load(ctx, request.TenantID, request.SourceID)
	if err != nil || state["meta_info"] != "cursor-2" {
		t.Fatalf("failed publication advanced cursor: %v %v", state, err)
	}
}

type completionReplyHook struct{ failure error }

func (*completionReplyHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (*completionReplyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h *completionReplyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, command redis.Cmder) error {
		if err := next(ctx, command); err != nil {
			return err
		}
		if command.Name() == "eval" && h.failure != nil {
			script, _ := command.Args()[1].(string)
			if strings.HasSuffix(script, completeRequestScript) {
				err := h.failure
				h.failure = nil
				return err
			}
		}
		return nil
	}
}

func TestCompletedReceiptRetentionIntegration(t *testing.T) {
	if os.Getenv("LINKD_RECEIPTS_INTEGRATION") != "1" {
		t.Skip("set LINKD_RECEIPTS_INTEGRATION=1")
	}
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%t", failed), func(t *testing.T) {
			client := startReceiptsRedis(t)
			receipts := NewRedisReceipts(client, "retention", time.Second)
			request := pluginruntime.Request{TenantID: "tenant", SourceID: "source", Mode: "push", Plugin: "rest_api_push", Configs: map[string]any{"secret": "private"}}
			completed, err := receipts.Accept(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := receipts.Accept(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range []pluginruntime.Request{completed, pending} {
				if _, err := receipts.SaveRequest(t.Context(), r); err != nil {
					t.Fatal(err)
				}
				if _, err := receipts.BeginAttempt(t.Context(), r); err != nil {
					t.Fatal(err)
				}
				if _, err := receipts.BeginPublication(t.Context(), r); err != nil {
					t.Fatal(err)
				}
			}
			progress := NewRedisProgress(client, "retention")
			if err := client.Set(t.Context(), progress.key("tenant", "source"), `{"meta_info":"original"}`, 0).Err(); err != nil {
				t.Fatal(err)
			}
			result := pluginruntime.Result{ProposedState: map[string]any{"meta_info": "advanced"}}
			expectedCursor := "advanced"
			if failed {
				result = pluginruntime.Result{Failure: &pluginruntime.Failure{Code: "plugin_error"}}
				expectedCursor = "original"
			}
			if err := receipts.Complete(t.Context(), completed, result); err != nil {
				t.Fatal(err)
			}
			if n, err := client.XLen(t.Context(), receipts.key("tenant", "source")).Result(); err != nil || n != 1 {
				t.Fatalf("completed raw input retained: %d %v", n, err)
			}
			restored, err := receipts.Load(t.Context(), "tenant", "source", completed.RequestID)
			if err != nil || !reflect.DeepEqual(restored, completed) {
				t.Fatalf("retained receipt: %+v %v", restored, err)
			}
			for _, r := range []pluginruntime.Request{completed, pending} {
				keys := []string{receipts.requestKey(r), receipts.key("tenant", "source") + ":attempts:" + url.QueryEscape(r.RequestID), receipts.key("tenant", "source") + ":publications:" + url.QueryEscape(r.RequestID)}
				if r.RequestID == completed.RequestID {
					keys = append(keys, receipts.resultKey(r))
				}
				for _, key := range keys {
					ttl, err := client.PTTL(t.Context(), key).Result()
					if err != nil {
						t.Fatal(err)
					}
					if r.RequestID == completed.RequestID {
						if ttl <= 0 || ttl > time.Second {
							t.Fatalf("completed TTL=%s", ttl)
						}
					} else if ttl != -1 {
						t.Fatalf("pending TTL=%s", ttl)
					}
				}
			}
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				_, err := receipts.LoadRequest(t.Context(), "tenant", "source", completed.RequestID)
				if errors.Is(err, redis.Nil) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-deadline.C:
					t.Fatal("completed input did not expire")
				case <-ticker.C:
				}
			}
			if result, err := receipts.LoadResult(t.Context(), completed); err != nil || result != nil {
				t.Fatalf("completed result survived retention: %+v %v", result, err)
			}
			if _, err := receipts.LoadRequest(t.Context(), "tenant", "source", pending.RequestID); err != nil {
				t.Fatalf("pending input expired: %v", err)
			}
			ids, err := receipts.PendingIDs(t.Context(), "tenant", "source", 10)
			if err != nil || !reflect.DeepEqual(ids, []string{pending.RequestID}) {
				t.Fatalf("pending index changed: %v %v", ids, err)
			}
			state, err := progress.Load(t.Context(), "tenant", "source")
			if err != nil || state["meta_info"] != expectedCursor {
				t.Fatalf("progress lost: %v %v", state, err)
			}
		})
	}
}
