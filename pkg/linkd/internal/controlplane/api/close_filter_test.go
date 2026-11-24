// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	lifecycleprocess "linkd/internal/lifecycle/process"
	"linkd/internal/logging"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

const filteredCloseBody = `{"bk_tenant_id":"tenant","filter":{"term":{"severity":"warning"}},"operation_id":"batch-operation","operator_id":"operator","reason":"verified","effective_at":"2026-09-23T00:00:00Z"}`

// 旧边界测试按固定顺序注入逐项结果；生产入口使用独立的批量实现。
func (f closeFunc) CloseAlerts(ctx context.Context, commands []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
	results := make([]lifecycle.CloseAlertOutcome, len(commands))
	for i, command := range commands {
		if ctx.Err() != nil {
			results[i].Err = &lifecycleprocess.CloseError{Status: 504, Code: "not_attempted"}
			continue
		}
		results[i].CloseAlertResult, results[i].Err = f(ctx, command)
	}
	return results
}

type filteredBatchCloser struct {
	closeFunc
	batch func(context.Context, []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome
}

func (c filteredBatchCloser) CloseAlerts(ctx context.Context, commands []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
	return c.batch(ctx, commands)
}

func TestFilteredCloseCallsBatchOnceAndKeepsCandidateOrder(t *testing.T) {
	calls := 0
	search := alertSearchFunc(func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error) {
		return store.AlertSearchPage{Items: []domain.Alert{{AlertID: "a"}, {AlertID: "b"}}}, nil
	})
	closer := filteredBatchCloser{closeFunc: func(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
		t.Error("filtered close fell back to single CloseAlert")
		return lifecycle.CloseAlertResult{}, nil
	}, batch: func(ctx context.Context, commands []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 19*time.Second || time.Until(deadline) > 20*time.Second {
			t.Fatal("filtered close does not have twenty-second budget", deadline)
		}
		calls++
		if len(commands) != 2 || commands[0].AlertID != "a" || commands[1].AlertID != "b" || commands[0].OperationID != commands[1].OperationID {
			t.Fatal(commands)
		}
		return []lifecycle.CloseAlertOutcome{{CloseAlertResult: lifecycle.CloseAlertResult{AlreadyClosed: true}}, {Err: &lifecycleprocess.CloseError{Status: 429, Code: "busy"}}}
	}}
	w := policyHTTP(t, filteredCloseAPI(t, search, closer, config.AlertSearchConfig{}).Handler(), http.MethodPost, "/api/v1/alerts/close", filteredCloseBody)
	var result closeAlertsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || calls != 1 || result.Closed != 1 || result.Failed != 1 || result.Items[0].AlertID != "a" || !result.Items[0].AlreadyClosed || result.Items[1].AlertID != "b" || result.Items[1].Status != 429 || result.HasMore {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
}

func filteredCloseAPI(t *testing.T, search AlertSearcher, closer AlertCloser, cfg config.AlertSearchConfig) *API {
	t.Helper()
	logger, err := logging.New(logging.DefaultConfig(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return &API{AlertSearch: NewAlertSearch(search, cfg, logger), AlertCloser: closer, Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}, WorkerToken: "worker"}}
}

func TestFilteredCloseReusesLifecycleAndStableOperation(t *testing.T) {
	t.Parallel()
	repository := memory.New()
	for _, id := range []string{"a", "b", "unselected"} {
		if _, err := repository.CreateAlert(t.Context(), storetest.Alert("tenant", id, "event", "fingerprint-"+id, "warning")); err != nil {
			t.Fatal(err)
		}
	}
	logger, err := logging.New(logging.DefaultConfig(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := lifecycle.NewProcessor(repository, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	search := alertSearchFunc(func(_ context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
		if q.TenantID != "tenant" || q.Limit != 1000 || q.After != "" || string(q.Query) != `{"bool":{"filter":[{"term":{"status":"active"}},{"term":{"severity":"warning"}}]}}` {
			t.Fatal("filter or scope changed", q)
		}
		page := store.AlertSearchPage{Items: []domain.Alert{}}
		for _, id := range []string{"a", "b"} {
			stored, err := repository.GetAlert(t.Context(), "tenant", id)
			if err != nil {
				t.Fatal(err)
			}
			page.Items = append(page.Items, stored.Alert)
		}
		return page, nil
	})
	handler := filteredCloseAPI(t, search, closeFunc(processor.CloseAlert), config.AlertSearchConfig{}).Handler()
	for _, replay := range []bool{false, true} {
		w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/close", filteredCloseBody)
		var result closeAlertsResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Items) != 2 || result.Closed != 2 || result.Failed != 0 || result.HasMore || result.DryRun {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, item := range result.Items {
			if item.Status != 200 || item.AlreadyClosed != replay {
				t.Fatal(item)
			}
			stored, err := repository.GetAlert(t.Context(), "tenant", item.AlertID)
			if err != nil || stored.Alert.Status != domain.AlertStatusClosed || stored.Alert.EndOperation == nil || stored.Alert.EndOperation.ID != "batch-operation" || stored.Alert.EndReason != "verified" || stored.Alert.EndAt == nil || !stored.Alert.EndAt.Equal(time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)) {
				t.Fatal(stored, err)
			}
			logs, err := repository.ListAlertLogs(t.Context(), "tenant", item.AlertID, store.PageRequest{Limit: 10})
			if err != nil || len(logs.Logs) != 1 || logs.Logs[0].OperationKind != domain.OperationKindClose {
				t.Fatal("close log missing or duplicated", logs, err)
			}
		}
	}
	unselected, err := repository.GetAlert(t.Context(), "tenant", "unselected")
	if err != nil || unselected.Alert.Status != domain.AlertStatusActive {
		t.Fatal(unselected, err)
	}
}

func TestFilteredCloseDryRunAndItemFailures(t *testing.T) {
	t.Parallel()
	search := alertSearchFunc(func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error) {
		return store.AlertSearchPage{Items: []domain.Alert{{AlertID: "a"}, {AlertID: "b"}, {AlertID: "c"}}}, nil
	})
	calls := 0
	closer := closeFunc(func(_ context.Context, c lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
		calls++
		if c.OperationID != "batch-operation" || c.OperatorID != "operator" || c.OperatorKind != domain.OperatorKindUser {
			t.Fatal(c)
		}
		switch c.AlertID {
		case "a":
			return lifecycle.CloseAlertResult{AlreadyClosed: true}, nil
		case "b":
			return lifecycle.CloseAlertResult{}, &lifecycleprocess.CloseError{Status: 429, Code: "busy", Message: "alert is being processed"}
		default:
			return lifecycle.CloseAlertResult{}, errors.New("private connection details")
		}
	})
	handler := filteredCloseAPI(t, search, closer, config.AlertSearchConfig{}).Handler()
	w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/close", filteredCloseBody)
	var result closeAlertsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || calls != 3 || result.Closed != 1 || result.Failed != 2 || result.Items[1].Status != 429 || result.Items[2].Status != 502 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
	calls = 0
	w = policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/close", `{"bk_tenant_id":"tenant","filter":{"match_all":{}},"dry_run":true}`)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || calls != 0 || !result.DryRun || result.Closed != 0 || result.Failed != 0 || len(result.Items) != 3 {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
}

func TestFilteredCloseRejectsInvalidRequestsBeforeSearch(t *testing.T) {
	t.Parallel()
	search := alertSearchFunc(func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error) {
		t.Error("invalid close request reached search")
		return store.AlertSearchPage{}, nil
	})
	handler := filteredCloseAPI(t, search, nil, config.AlertSearchConfig{}).Handler()
	for _, body := range []string{
		`{}`, `null`, `{"bk_tenant_id":"tenant","dry_run":true}`, `{"bk_tenant_id":"tenant","filter":null,"dry_run":true}`,
		`{"bk_tenant_id":"tenant","filter":[],"dry_run":true}`, `{"bk_tenant_id":"tenant","filter":{"match_all":{}},"limit":5001,"dry_run":true}`,
		strings.Replace(filteredCloseBody, `"operation_id":"batch-operation"`, `"operation_id":""`, 1),
		strings.Replace(filteredCloseBody, `"operator_id":"operator"`, `"operator_id":""`, 1),
		strings.Replace(filteredCloseBody, `"reason":"verified"`, `"reason":""`, 1),
		strings.Replace(filteredCloseBody, `"2026-09-23T00:00:00Z"`, `"2099-09-23T00:00:00Z"`, 1),
		strings.Replace(filteredCloseBody, `"reason":"verified"`, `"reason":"verified","operator_kind":"system","operation_source":"manual"`, 1),
		strings.Replace(filteredCloseBody, `"reason":"verified"`, `"reason":"verified","operator_kind":"invalid"`, 1),
		strings.Replace(filteredCloseBody, `"reason":"verified"`, `"reason":"verified","unknown":true`, 1),
		filteredCloseBody + `{}`,
	} {
		if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/close", body); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/close", filteredCloseBody); w.Code != 503 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/alerts/close", strings.NewReader(filteredCloseBody))
	r.Header.Set("Authorization", "Bearer worker")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestFilteredCloseStopsAfterCancellationAndReportsPendingItems(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	search := alertSearchFunc(func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error) {
		return store.AlertSearchPage{Items: []domain.Alert{{AlertID: "a"}, {AlertID: "b"}}}, nil
	})
	closer := closeFunc(func(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
		calls++
		cancel()
		return lifecycle.CloseAlertResult{}, context.Canceled
	})
	handler := filteredCloseAPI(t, search, closer, config.AlertSearchConfig{}).Handler()
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/alerts/close", strings.NewReader(filteredCloseBody))
	r.Header.Set("Internal-Token", testJWT(t, "admin"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var result closeAlertsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || calls != 1 || result.Failed != 2 || result.Items[0].ErrorCode != "result_uncertain" || result.Items[1].ErrorCode != "not_attempted" {
		t.Fatal(w.Code, w.Body.String(), calls)
	}
}

func TestFilteredCloseSearchFailureNeverCloses(t *testing.T) {
	t.Parallel()
	search := alertSearchFunc(func(context.Context, store.AlertSearchRequest) (store.AlertSearchPage, error) {
		return store.AlertSearchPage{}, errors.New("private query failure")
	})
	closer := closeFunc(func(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
		t.Error("closed after query failure")
		return lifecycle.CloseAlertResult{}, nil
	})
	a := filteredCloseAPI(t, search, closer, config.AlertSearchConfig{})
	if w := policyHTTP(t, a.Handler(), http.MethodPost, "/api/v1/alerts/close", filteredCloseBody); w.Code != 502 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
	a.AlertSearch.reader = nil
	if w := policyHTTP(t, a.Handler(), http.MethodPost, "/api/v1/alerts/close", filteredCloseBody); w.Code != 501 {
		t.Fatal(w.Code)
	}
	a.AlertSearch = nil
	if w := policyHTTP(t, a.Handler(), http.MethodPost, "/api/v1/alerts/close", filteredCloseBody); w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestFilteredCloseDefaultCandidateLimitIsIndependentOfInternalBatchSize(t *testing.T) {
	search := alertSearchFunc(func(_ context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
		if q.Limit != closeReadBatchSize {
			t.Fatal("fixed internal read block changed", q.Limit)
		}
		return store.AlertSearchPage{}, nil
	})
	handler := filteredCloseAPI(t, search, nil, config.AlertSearchConfig{}).Handler()
	if w := policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/close", `{"bk_tenant_id":"tenant","filter":{"match_all":{}},"dry_run":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func closeTestPage(q store.AlertSearchRequest, total int) store.AlertSearchPage {
	start, _ := strconv.Atoi(q.After)
	end := min(start+1000, total)
	page := store.AlertSearchPage{}
	for i := start; i < end; i++ {
		page.Items = append(page.Items, domain.Alert{AlertID: fmt.Sprintf("alert-%05d", i)})
	}
	if end < total {
		page.Next = strconv.Itoa(end)
	}
	return page
}

func successfulCloseItems(commands []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
	return make([]lifecycle.CloseAlertOutcome, len(commands))
}

func TestFilteredCloseAutoScans20000WithEightConcurrentBatches(t *testing.T) {
	var queries, calls, active, peak atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	search := alertSearchFunc(func(_ context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
		queries.Add(1)
		if q.Limit != 1000 {
			t.Error("read block is not 1000")
		}
		return closeTestPage(q, 20000), nil
	})
	closer := filteredBatchCloser{batch: func(_ context.Context, c []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if calls.Add(1) <= 8 {
			entered <- struct{}{}
			<-release
		}
		if len(c) != 1000 {
			t.Error("close block is not 1000")
		}
		return successfulCloseItems(c)
	}}
	handler := filteredCloseAPI(t, search, closer, config.AlertSearchConfig{}).Handler()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/close", filteredCloseBody) }()
	for range 8 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("one request did not start eight internal batches")
		}
	}
	unblock.Do(func() { close(release) })
	select {
	case w := <-done:
		var r closeAlertsResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Closed != 20000 || r.Failed != 0 || r.HasMore || len(r.Items) != 20000 {
			t.Fatal(w.Code, r.Closed, r.Failed, r.HasMore, len(r.Items))
		}
		for i, v := range r.Items {
			if v.AlertID != fmt.Sprintf("alert-%05d", i) {
				t.Fatal("candidate order lost", i, v)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not complete")
	}
	if queries.Load() != 20 || calls.Load() != 20 || peak.Load() != 8 {
		t.Fatal(queries.Load(), calls.Load(), peak.Load())
	}
}

func TestFilteredCloseRequestsHaveIndependentConcurrencyAndNoGlobalSlots(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	search := alertSearchFunc(func(_ context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
		return closeTestPage(q, 2000), nil
	})
	closer := filteredBatchCloser{batch: func(_ context.Context, c []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
		entered <- struct{}{}
		<-release
		return successfulCloseItems(c)
	}}
	api := filteredCloseAPI(t, search, closer, config.AlertSearchConfig{})
	api.CloseConcurrency = 2
	// 普通检索槽占满也不会给关闭路径再叠加全局限流。
	for range cap(api.AlertSearch.slots) {
		api.AlertSearch.slots <- struct{}{}
	}
	defer func() {
		for range cap(api.AlertSearch.slots) {
			<-api.AlertSearch.slots
		}
	}()
	done := make(chan *httptest.ResponseRecorder, 2)
	handler := api.Handler()
	for range 2 {
		go func() { done <- policyHTTP(t, handler, http.MethodPost, "/api/v1/alerts/close", filteredCloseBody) }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("one request was blocked by another request's global budget")
		}
	}
	unblock.Do(func() { close(release) })
	for range 2 {
		select {
		case w := <-done:
			var r closeAlertsResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &r) != nil || r.Closed != 2000 || r.Failed != 0 {
				t.Fatal(w.Code, r)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("request did not complete")
		}
	}
}

func TestFilteredCloseStopsBeforeUnaffordableNextBatch(t *testing.T) {
	var queries atomic.Int32
	search := alertSearchFunc(func(_ context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
		queries.Add(1)
		return closeTestPage(q, 3000), nil
	})
	closer := filteredBatchCloser{batch: func(ctx context.Context, c []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
		timer := time.NewTimer(4500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			t.Error("batch was interrupted")
		}
		return successfulCloseItems(c)
	}}
	a := filteredCloseAPI(t, search, closer, config.AlertSearchConfig{})
	a.CloseConcurrency = 2
	ctx, cancel := context.WithTimeout(t.Context(), 8200*time.Millisecond)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/alerts/close", strings.NewReader(filteredCloseBody))
	r.Header.Set("Internal-Token", testJWT(t, "admin"))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	var out closeAlertsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Closed != 2000 || out.Failed != 0 || !out.HasMore || queries.Load() != 2 || ctx.Err() != nil {
		t.Fatal(w.Code, out.Closed, out.Failed, out.HasMore, queries.Load(), ctx.Err())
	}
}

func TestFilteredCloseKeepsResultsWhenLaterCandidateQueryFails(t *testing.T) {
	search := alertSearchFunc(func(_ context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
		if q.After != "" {
			return store.AlertSearchPage{}, errors.New("later query failed")
		}
		return closeTestPage(q, 2000), nil
	})
	closer := filteredBatchCloser{batch: func(_ context.Context, c []lifecycle.CloseAlertCommand) []lifecycle.CloseAlertOutcome {
		return successfulCloseItems(c)
	}}
	a := filteredCloseAPI(t, search, closer, config.AlertSearchConfig{})
	w := policyHTTP(t, a.Handler(), http.MethodPost, "/api/v1/alerts/close", filteredCloseBody)
	var out closeAlertsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Closed != 1000 || out.Failed != 0 || !out.HasMore || out.ErrorCode != "query_failed" {
		t.Fatal(w.Code, out)
	}
}
