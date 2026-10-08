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
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"linkd/internal/actiondelivery"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store/storetest"
)

type actionDeliveryReader struct {
	rows  []actiondelivery.StoredTask
	cross bool
	err   error
}

func (r *actionDeliveryReader) Get(_ context.Context, tenant, id string) (actiondelivery.StoredTask, error) {
	if r.err != nil {
		return actiondelivery.StoredTask{}, r.err
	}
	for _, row := range r.rows {
		if row.Task.ID == id && row.Task.Request.TenantID == tenant {
			row.Task = row.Task.Clone()
			if r.cross {
				row.Task.Request.TenantID = "other"
			}
			return row, nil
		}
	}
	return actiondelivery.StoredTask{}, actiondelivery.ErrNotFound
}

func (r *actionDeliveryReader) List(ctx context.Context, q actiondelivery.Query) ([]actiondelivery.StoredTask, error) {
	if r.err != nil {
		return nil, r.err
	}
	rows := []actiondelivery.StoredTask{}
	for _, row := range r.rows {
		if row.Task.ID > q.After && row.Task.Request.TenantID == q.TenantID {
			copy, err := r.Get(ctx, q.TenantID, row.Task.ID)
			if err != nil {
				return nil, err
			}
			rows = append(rows, copy)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Task.ID < rows[j].Task.ID })
	return rows[:min(q.Limit, len(rows))], nil
}

type retryActionFunc func(context.Context, actiondelivery.RetryCommand) (actiondelivery.StoredTask, error)

func (f retryActionFunc) Retry(ctx context.Context, c actiondelivery.RetryCommand) (actiondelivery.StoredTask, error) {
	return f(ctx, c)
}

func actionAPIFixture(t *testing.T, tenant, id string) actiondelivery.StoredTask {
	t.Helper()
	a := storetest.Alert(tenant, id, "opening", id, "warning")
	a.Title = "private snapshot title"
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1}}}
	at := a.UpdateAt
	a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "source_event", CauseID: a.LatestEventID}
	task, err := actiondelivery.NewTask(a, "kac", actiondelivery.Cause{Type: "source_event", ID: a.LatestEventID}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	task.Progress.State = "failed"
	task.Progress.Attempts = 1
	task.Progress.TotalAttempts = 1
	task.Progress.DueAt = nil
	task.Progress.ErrorCode = "remote_unauthorized"
	if err := task.Validate(); err != nil {
		t.Fatal(err)
	}
	return actiondelivery.StoredTask{Task: task, Version: "1"}
}

func TestActionDeliveryReadsAreScopedBoundedAndSeparateFrozenPayload(t *testing.T) {
	reader := &actionDeliveryReader{rows: []actiondelivery.StoredTask{actionAPIFixture(t, "tenant", "a"), actionAPIFixture(t, "tenant", "b"), actionAPIFixture(t, "other", "a")}}
	api := &API{ActionDeliveries: NewActionDeliveries(reader, nil), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	h := api.Handler()
	base := "/api/v1/action-deliveries"
	id := reader.rows[0].Task.ID
	w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant&state=succeeded&limit=1", "")
	var page struct {
		Items []actionDeliveryView `json:"items"`
		Next  string               `json:"next"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 0 || page.Next == "" {
		t.Fatal("empty filtered page lost cursor", w.Code, w.Body.String())
	}
	if w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=other&state=succeeded&after="+url.QueryEscape(page.Next), ""); w.Code != 400 {
		t.Fatal("cross-scope cursor", w.Code)
	}
	for _, path := range []string{base + "?bk_tenant_id=tenant", base + "/" + id + "?bk_tenant_id=tenant"} {
		w := policyHTTP(t, h, "GET", path, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "private snapshot") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w = policyHTTP(t, h, "GET", base+"/"+id+"/snapshot?bk_tenant_id=tenant", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "private snapshot title") {
		t.Fatal("explicit snapshot missing", w.Code, w.Body.String())
	}
	for _, query := range []string{"", "?bk_tenant_id=tenant&state=queued", "?bk_tenant_id=tenant&limit=5", "?bk_tenant_id=tenant&bk_tenant_id=other", "?bk_tenant_id=tenant&url=external"} {
		if w := policyHTTP(t, h, "GET", base+query, ""); w.Code != 400 {
			t.Fatal("bad query accepted", query, w.Code)
		}
	}
	if w := policyHTTP(t, h, "GET", base+"/"+id+"?bk_tenant_id=other", ""); w.Code != 404 {
		t.Fatal("foreign task found", w.Code)
	}
	reader.cross = true
	if w := policyHTTP(t, h, "GET", base+"/"+id+"?bk_tenant_id=tenant", ""); w.Code != 403 {
		t.Fatal("store scope ignored", w.Code)
	}
	reader.cross = false
	reader.err = errors.New("private database address")
	w = policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
	reader.err = nil
	api.ActionDeliveries.slots <- struct{}{}
	api.ActionDeliveries.slots <- struct{}{}
	if w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant", ""); w.Code != 429 {
		t.Fatal(w.Code)
	}
	<-api.ActionDeliveries.slots
	<-api.ActionDeliveries.slots
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequestWithContext(t.Context(), http.MethodGet, base+"?bk_tenant_id=tenant", nil))
	if unauth.Code != 401 {
		t.Fatal("management auth missing", unauth.Code)
	}
}

func TestActionRetryCommandMustMatchReceiptAndCannotHideReleaseError(t *testing.T) {
	row := actionAPIFixture(t, "tenant", "alert")
	reader := &actionDeliveryReader{rows: []actiondelivery.StoredTask{row}}
	var calls int
	var failure error
	retry := retryActionFunc(func(_ context.Context, c actiondelivery.RetryCommand) (actiondelivery.StoredTask, error) {
		calls++
		if failure != nil {
			return actiondelivery.StoredTask{}, failure
		}
		next := row
		next.Task = next.Task.Clone()
		p := &next.Task.Progress
		p.State = "pending"
		p.Generation = 2
		p.Attempts = 0
		p.ErrorCode = ""
		p.DueAt = &p.UpdatedAt
		p.LastRetry = &actiondelivery.RetryRecord{Command: c, RequestedAt: p.UpdatedAt}
		next.Version = "2"
		return next, nil
	})
	api := &API{ActionDeliveries: NewActionDeliveries(reader, retry), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	h := api.Handler()
	path := "/api/v1/action-deliveries/" + row.Task.ID + "/retry"
	command := `{"bk_tenant_id":"tenant","expected_version":"1","operation_id":"op","operator_id":"tester","reason":"恢复投递"}`
	w := policyHTTP(t, h, "POST", path, command)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"last_retry"`) || strings.Contains(w.Body.String(), "private snapshot") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, bad := range []string{strings.Replace(command, `"reason":"恢复投递"`, `"reason":""`, 1), strings.Replace(command, `"reason":"恢复投递"`, `"reason":"恢复投递","force":true`, 1), command + `{}`} {
		if w := policyHTTP(t, h, "POST", path, bad); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	if calls != 1 {
		t.Fatal("invalid command reached use case")
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{actiondelivery.ErrConflict, 409}, {actiondelivery.ErrBusy, 429}, {actiondelivery.ErrCapacity, 429}, {errors.Join(actiondelivery.ErrBusy, errors.New("release failed")), 500}, {errors.Join(actiondelivery.ErrConflict, errors.New("release failed")), 500}, {errors.Join(actiondelivery.ErrNotFound, errors.New("release failed")), 500}, {actiondelivery.ErrNotFound, 404}, {context.DeadlineExceeded, 504}} {
		failure = tc.err
		if w := policyHTTP(t, h, "POST", path, command); w.Code != tc.status {
			t.Fatal(tc.err, w.Code)
		}
	}
}

func (r *actionDeliveryReader) OldestUnsettled(ctx context.Context, tenant, alert, target string) (actiondelivery.StoredTask, error) {
	if r.err != nil {
		return actiondelivery.StoredTask{}, r.err
	}
	var head actiondelivery.StoredTask
	for _, row := range r.rows {
		if row.Task.Request.TenantID == tenant && row.Task.Request.AlertID == alert && row.Task.Request.TargetID == target && row.Task.Unsettled() && (head.Version == "" || row.Task.Request.Revision < head.Task.Request.Revision) {
			head = row
		}
	}
	if head.Version == "" {
		return head, actiondelivery.ErrNotFound
	}
	return r.Get(ctx, tenant, head.Task.ID)
}

func TestActionOrderShowsFailedHeadAndDoesNotInterpretMissingAsReady(t *testing.T) {
	row := actionAPIFixture(t, "tenant", "alert")
	reader := &actionDeliveryReader{rows: []actiondelivery.StoredTask{row}}
	a := &API{ActionDeliveries: NewActionDeliveries(reader, nil), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	path := "/api/v1/action-deliveries/" + row.Task.ID + "/order?bk_tenant_id=tenant"
	w := policyHTTP(t, a.Handler(), "GET", path, "")
	var order struct {
		Head *actionDeliveryView `json:"head"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &order) != nil || order.Head == nil || order.Head.ID != row.Task.ID || order.Head.Progress.State != "failed" {
		t.Fatal("failed ordering barrier missing", w.Code, w.Body.String())
	}
	reader.cross = true
	if w := policyHTTP(t, a.Handler(), "GET", path, ""); w.Code != 403 {
		t.Fatal("foreign order lookup accepted", w.Code)
	}
}

type staleActionOrderReader struct {
	*actionDeliveryReader
	head     actiondelivery.StoredTask
	orderErr error
}

func (r *staleActionOrderReader) OldestUnsettled(context.Context, string, string, string) (actiondelivery.StoredTask, error) {
	return r.head, r.orderErr
}

func TestActionOrderRereadsIndexedHeadAndPreservesQueryErrors(t *testing.T) {
	old := actionAPIFixture(t, "tenant", "alert")
	current := old
	current.Task = old.Task.Clone()
	current.Version = "2"
	p := &current.Task.Progress
	p.State = "pending"
	p.Attempts = 0
	p.Generation = 2
	p.ErrorCode = ""
	p.DueAt = &p.UpdatedAt
	p.LastRetry = &actiondelivery.RetryRecord{RequestedAt: p.UpdatedAt, Command: actiondelivery.RetryCommand{TenantID: "tenant", TaskID: old.Task.ID, ExpectedVersion: "1", OperationID: "retry", OperatorID: "operator", Reason: "repair"}}
	reader := &staleActionOrderReader{actionDeliveryReader: &actionDeliveryReader{rows: []actiondelivery.StoredTask{current}}, head: old}
	a := &API{ActionDeliveries: NewActionDeliveries(reader, nil), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	path := "/api/v1/action-deliveries/" + old.Task.ID + "/order?bk_tenant_id=tenant"
	w := policyHTTP(t, a.Handler(), "GET", path, "")
	var response struct {
		Head *actionDeliveryView `json:"head"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Head == nil || response.Head.Progress.State != "pending" || response.Head.Progress.Generation != 2 {
		t.Fatal("stale search head exposed", w.Code, w.Body.String())
	}
	reader.orderErr = actiondelivery.ErrNotFound
	w = policyHTTP(t, a.Handler(), "GET", path, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"head":null`) {
		t.Fatal("empty index invented a ready task", w.Code, w.Body.String())
	}
	reader.orderErr = errors.Join(actiondelivery.ErrNotFound, errors.New("private storage failure"))
	w = policyHTTP(t, a.Handler(), "GET", path, "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("partial order failure swallowed", w.Code, w.Body.String())
	}
	reader.orderErr = nil
	reader.head = actionAPIFixture(t, "tenant", "different-alert")
	if w := policyHTTP(t, a.Handler(), "GET", path, ""); w.Code != 503 {
		t.Fatal("foreign Alert head accepted", w.Code)
	}
}

type blockedActionReader struct {
	*actionDeliveryReader
	entered chan struct{}
}

func (r *blockedActionReader) Get(ctx context.Context, _, _ string) (actiondelivery.StoredTask, error) {
	close(r.entered)
	<-ctx.Done()
	return actiondelivery.StoredTask{}, ctx.Err()
}

func TestActionManagementCancellationReleasesAdmissionSlot(t *testing.T) {
	row := actionAPIFixture(t, "tenant", "alert")
	reader := &blockedActionReader{actionDeliveryReader: &actionDeliveryReader{}, entered: make(chan struct{})}
	a := &API{ActionDeliveries: NewActionDeliveries(reader, nil), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, "GET", "/api/v1/action-deliveries/"+row.Task.ID+"?bk_tenant_id=tenant", nil)
	req.Header.Set("Internal-Token", testJWT(t, "admin"))
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); a.Handler().ServeHTTP(w, req) }()
	select {
	case <-reader.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reader ignored cancellation")
	}
	if len(a.ActionDeliveries.slots) != 0 || w.Code == http.StatusOK {
		t.Fatal("cancel retained slot or returned success", w.Code)
	}
}
