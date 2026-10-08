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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/projection"
)

// ProjectionTaskReader 提供明确租户的持久任务读取；管理接口不暴露全局工作扫描。
type ProjectionTaskReader interface {
	Get(context.Context, string, string) (projection.StoredTask, error)
	List(context.Context, projection.Query) ([]projection.StoredTask, error)
}

// ProjectionRetrier 只恢复带完整审计命令的失败任务，不允许构造目标或修改快照。
type ProjectionRetrier interface {
	Retry(context.Context, projection.RetryCommand) (projection.StoredTask, error)
}

// ProjectionTasks 共享两并发管理额度，读取和恢复均限定十秒，不在请求内发送投影。
type ProjectionTasks struct {
	reader  ProjectionTaskReader
	retrier ProjectionRetrier
	slots   chan struct{}
}

// NewProjectionTasks 注入部署专属存储及可选重试入口，不启动自动投递器。
func NewProjectionTasks(reader ProjectionTaskReader, retrier ProjectionRetrier) *ProjectionTasks {
	return &ProjectionTasks{reader: reader, retrier: retrier, slots: make(chan struct{}, 2)}
}

type projectionTaskQuery struct {
	tenant, alert, target, source, state, after string
	limit                                       int
}

func policyRuntimeHashID(v string) bool {
	b, err := hex.DecodeString(v)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == v
}

func (q projectionTaskQuery) scope() []string {
	return []string{q.tenant, q.alert, q.target, q.source, q.state}
}

func (q projectionTaskQuery) cursor(id string) string {
	if id == "" {
		return ""
	}
	raw, _ := json.Marshal(append(q.scope(), id))
	return base64.RawURLEncoding.EncodeToString(raw)
}

func parseProjectionTaskQuery(r *http.Request, list bool) (projectionTaskQuery, error) {
	keys := []string{"bk_tenant_id"}
	if list {
		keys = append(keys, "alert_id", "target_id", "source_id", "state", "after", "limit")
	}
	if policyQuery(r, keys...) != nil {
		return projectionTaskQuery{}, policy.ErrInvalid
	}
	v := r.URL.Query()
	q := projectionTaskQuery{tenant: v.Get("bk_tenant_id"), alert: v.Get("alert_id"), target: v.Get("target_id"), source: v.Get("source_id"), state: v.Get("state"), limit: 4}
	if domain.ValidateIdentityPart("tenant", q.tenant, 64) != nil || len(q.alert) > domain.EntityIDMaxBytes || (q.target != "" && domain.ValidateIdentityPart("target", q.target, 64) != nil) || (q.source != "" && domain.ValidateIdentityPart("source", q.source, 32) != nil) || (q.state != "" && !slices.Contains([]string{"pending", "sending", "retry", "delivered", "succeeded", "failed"}, q.state)) {
		return q, policy.ErrInvalid
	}
	if !list && !policyRuntimeHashID(r.PathValue("id")) {
		return q, policy.ErrInvalid
	}
	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 4 {
			return q, policy.ErrInvalid
		}
		q.limit = n
	}
	if raw := v.Get("after"); raw != "" {
		if len(raw) > 2048 {
			return q, policy.ErrInvalid
		}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		var parts []string
		if err != nil || json.Unmarshal(data, &parts) != nil || len(parts) != 6 || !slices.Equal(parts[:5], q.scope()) || !policyRuntimeHashID(parts[5]) {
			return q, policy.ErrInvalid
		}
		q.after = parts[5]
	}
	return q, nil
}

func (q projectionTaskQuery) matches(t projection.Task) bool {
	return (q.alert == "" || q.alert == t.Request.AlertID) && (q.target == "" || q.target == t.Request.TargetID) && (q.source == "" || q.source == t.SourceID) && (q.state == "" || q.state == t.Progress.State)
}

type projectionTaskView struct {
	Tenant        string              `json:"bk_tenant_id"`
	ID            string              `json:"id"`
	AlertID       string              `json:"alert_id"`
	AlarmID       string              `json:"alarm_id"`
	TargetID      string              `json:"target_id"`
	SourceID      string              `json:"source_id"`
	SourceVersion int64               `json:"source_version"`
	Revision      int64               `json:"revision"`
	AlertStatus   domain.AlertStatus  `json:"alert_status"`
	ContentHash   string              `json:"content_hash"`
	TaskVersion   string              `json:"task_version"`
	CreatedAt     time.Time           `json:"created_at"`
	Progress      projection.Progress `json:"progress"`
}

func projectTask(row projection.StoredTask, tenant, id string) (projectionTaskView, error) {
	t := row.Task
	if t.Request.TenantID != tenant || (id != "" && t.ID != id) {
		return projectionTaskView{}, policy.ErrAccess
	}
	if t.Validate() != nil || row.Version == "" || len(row.Version) > 128 {
		return projectionTaskView{}, policy.ErrUnavailable
	}
	var snapshot struct {
		Status domain.AlertStatus `json:"status"`
	}
	if json.Unmarshal(t.Request.Alert, &snapshot) != nil {
		return projectionTaskView{}, policy.ErrUnavailable
	}
	return projectionTaskView{AlertStatus: snapshot.Status, Tenant: tenant, ID: t.ID, AlertID: t.Request.AlertID, AlarmID: t.Request.AlarmID, TargetID: t.Request.TargetID, SourceID: t.SourceID, SourceVersion: t.SourceVersion, Revision: t.Request.Revision, ContentHash: t.Request.ContentHash, TaskVersion: row.Version, CreatedAt: t.CreatedAt, Progress: t.Clone().Progress}, nil
}

func projectionTaskFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projection.ErrNotFound):
		err = policy.ErrNotFound
	case errors.Is(err, projection.ErrConflict):
		err = policy.ErrConflict
	case projection.CanDefer(err):
		err = policy.ErrPreviewCapacity
	case errors.Is(err, projection.ErrInvalid), errors.Is(err, projection.ErrInvalidReceipt):
		err = policy.ErrUnavailable
	}
	policyFailure(w, err)
}

func (a *API) projectionTaskCall(w http.ResponseWriter, r *http.Request) (context.Context, func(), bool) {
	w.Header().Set("Cache-Control", "no-store")
	runtime := a.ProjectionTasks
	if runtime == nil || runtime.reader == nil {
		policyFailure(w, policy.ErrUnavailable)
		return nil, nil, false
	}
	select {
	case runtime.slots <- struct{}{}:
	default:
		policyFailure(w, policy.ErrPreviewCapacity)
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	return ctx, func() { cancel(); <-runtime.slots }, true
}

func (a *API) listProjectionTasks(w http.ResponseWriter, r *http.Request) {
	q, err := parseProjectionTaskQuery(r, true)
	if err != nil {
		policyFailure(w, err)
		return
	}
	ctx, done, ok := a.projectionTaskCall(w, r)
	if !ok {
		return
	}
	defer done()
	rows, err := a.ProjectionTasks.reader.List(ctx, projection.Query{TenantID: q.tenant, After: q.after, Limit: q.limit})
	if err != nil {
		projectionTaskFailure(w, err)
		return
	}
	if len(rows) > q.limit {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	items := []projectionTaskView{}
	last := q.after
	for _, row := range rows {
		v, err := projectTask(row, q.tenant, "")
		if err != nil {
			projectionTaskFailure(w, err)
			return
		}
		if v.ID <= last {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		last = v.ID
		if q.matches(row.Task) {
			items = append(items, v)
		}
	}
	next := ""
	if len(rows) == q.limit {
		next = q.cursor(last)
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": q.tenant, "items": items, "next": next})
}

func (a *API) getProjectionTask(w http.ResponseWriter, r *http.Request) {
	a.readProjectionTask(w, r, false)
}

func (a *API) projectionTaskSnapshot(w http.ResponseWriter, r *http.Request) {
	a.readProjectionTask(w, r, true)
}

func (a *API) readProjectionTask(w http.ResponseWriter, r *http.Request, snapshot bool) {
	q, err := parseProjectionTaskQuery(r, false)
	if err != nil {
		policyFailure(w, err)
		return
	}
	ctx, done, ok := a.projectionTaskCall(w, r)
	if !ok {
		return
	}
	defer done()
	row, err := a.ProjectionTasks.reader.Get(ctx, q.tenant, r.PathValue("id"))
	if err != nil {
		projectionTaskFailure(w, err)
		return
	}
	v, err := projectTask(row, q.tenant, r.PathValue("id"))
	if err != nil {
		projectionTaskFailure(w, err)
		return
	}
	if snapshot {
		writePolicyRuntimeLimit(w, map[string]any{"bk_tenant_id": q.tenant, "id": v.ID, "revision": v.Revision, "content_hash": v.ContentHash, "alert": row.Task.Request.Alert}, projection.MaxTaskBytes)
		return
	}
	writePolicyRuntime(w, v)
}

func (a *API) retryProjectionTask(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" || !policyRuntimeHashID(r.PathValue("id")) {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	var body struct {
		TenantID        string `json:"bk_tenant_id"`
		ExpectedVersion string `json:"expected_version"`
		OperationID     string `json:"operation_id"`
		OperatorID      string `json:"operator_id"`
		Reason          string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if decode(r, &body) != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	command := projection.RetryCommand{TenantID: body.TenantID, TaskID: r.PathValue("id"), ExpectedVersion: body.ExpectedVersion, OperationID: body.OperationID, OperatorID: body.OperatorID, Reason: body.Reason}
	if command.Validate() != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	ctx, done, ok := a.projectionTaskCall(w, r)
	if !ok {
		return
	}
	defer done()
	if a.ProjectionTasks.retrier == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	row, err := a.ProjectionTasks.retrier.Retry(ctx, command)
	if err != nil {
		projectionTaskFailure(w, err)
		return
	}
	v, err := projectTask(row, command.TenantID, command.TaskID)
	if err != nil {
		projectionTaskFailure(w, err)
		return
	}
	if v.Progress.LastRetry == nil || v.Progress.LastRetry.Command != command {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > 64<<10 {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if row.Task.HasWork() {
		w.WriteHeader(http.StatusAccepted)
	}
	_, _ = w.Write(raw)
}
