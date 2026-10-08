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

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/projection"
	"linkd/internal/store/storetest"
)

type projectionReader struct {
	rows  []projection.StoredTask
	cross bool
	err   error
}

func (r *projectionReader) Get(_ context.Context, tenant, id string) (projection.StoredTask, error) {
	if r.err != nil {
		return projection.StoredTask{}, r.err
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
	return projection.StoredTask{}, projection.ErrNotFound
}

func (r *projectionReader) List(ctx context.Context, q projection.Query) ([]projection.StoredTask, error) {
	if r.err != nil {
		return nil, r.err
	}
	rows := []projection.StoredTask{}
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

type retryTaskFunc func(context.Context, projection.RetryCommand) (projection.StoredTask, error)

func (f retryTaskFunc) Retry(ctx context.Context, c projection.RetryCommand) (projection.StoredTask, error) {
	return f(ctx, c)
}

func projectionAPIFixture(t *testing.T, tenant, id string) projection.StoredTask {
	t.Helper()
	a := storetest.Alert(tenant, id, "opening", id, "warning")
	a.Title = "private snapshot title"
	a.Projection = domain.AlertProjection{Targets: map[string]domain.ProjectionTargetState{"kac": {SourceVersion: 1, RequiredRevision: 1}}}
	task, err := projection.NewTask(a, "kac", time.Now().UTC())
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
	return projection.StoredTask{Task: task, Version: "1"}
}

func TestProjectionTaskReadsAreScopedBoundedAndSeparateFrozenPayload(t *testing.T) {
	reader := &projectionReader{rows: []projection.StoredTask{projectionAPIFixture(t, "tenant", "a"), projectionAPIFixture(t, "tenant", "b"), projectionAPIFixture(t, "other", "a")}}
	api := &API{ProjectionTasks: NewProjectionTasks(reader, nil), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	h := api.Handler()
	base := "/api/v1/projection-tasks"
	id := reader.rows[0].Task.ID
	w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant&state=succeeded&limit=1", "")
	var page struct {
		Items []projectionTaskView `json:"items"`
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
	api.ProjectionTasks.slots <- struct{}{}
	api.ProjectionTasks.slots <- struct{}{}
	if w := policyHTTP(t, h, "GET", base+"?bk_tenant_id=tenant", ""); w.Code != 429 {
		t.Fatal(w.Code)
	}
	<-api.ProjectionTasks.slots
	<-api.ProjectionTasks.slots
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequestWithContext(t.Context(), http.MethodGet, base+"?bk_tenant_id=tenant", nil))
	if unauth.Code != 401 {
		t.Fatal("management auth missing", unauth.Code)
	}
}

func TestProjectionRetryCommandMustMatchReceiptAndCannotHideReleaseError(t *testing.T) {
	row := projectionAPIFixture(t, "tenant", "alert")
	reader := &projectionReader{rows: []projection.StoredTask{row}}
	var calls int
	var failure error
	retry := retryTaskFunc(func(_ context.Context, c projection.RetryCommand) (projection.StoredTask, error) {
		calls++
		if failure != nil {
			return projection.StoredTask{}, failure
		}
		next := row
		next.Task = next.Task.Clone()
		p := &next.Task.Progress
		p.State = "pending"
		p.Generation = 2
		p.Attempts = 0
		p.ErrorCode = ""
		p.DueAt = &p.UpdatedAt
		p.LastRetry = &projection.RetryRecord{Command: c, RequestedAt: p.UpdatedAt}
		next.Version = "2"
		return next, nil
	})
	api := &API{ProjectionTasks: NewProjectionTasks(reader, retry), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "admin"}}}
	h := api.Handler()
	path := "/api/v1/projection-tasks/" + row.Task.ID + "/retry"
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
	}{{projection.ErrConflict, 409}, {projection.ErrBusy, 429}, {projection.ErrCapacity, 429}, {errors.Join(projection.ErrBusy, errors.New("release failed")), 500}, {projection.ErrNotFound, 404}, {context.DeadlineExceeded, 504}} {
		failure = tc.err
		if w := policyHTTP(t, h, "POST", path, command); w.Code != tc.status {
			t.Fatal(tc.err, w.Code)
		}
	}
}
