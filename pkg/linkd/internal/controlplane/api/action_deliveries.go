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
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
	"linkd/internal/policy"
)

// ActionDeliveryReader 提供明确租户的持久任务读取；管理接口不暴露全局工作扫描。
type ActionDeliveryReader interface {
	Get(context.Context, string, string) (actiondelivery.StoredTask, error)
	List(context.Context, actiondelivery.Query) ([]actiondelivery.StoredTask, error)
	OldestUnsettled(context.Context, string, string, string) (actiondelivery.StoredTask, error)
}

// ActionRetrier 只恢复带完整审计命令的失败任务，不允许构造目标或修改快照。
type ActionRetrier interface {
	Retry(context.Context, actiondelivery.RetryCommand) (actiondelivery.StoredTask, error)
}

// ActionDeliveries 共享两并发管理额度，读取和恢复均限定十秒，不在请求内发送动作投递。
type ActionDeliveries struct {
	reader  ActionDeliveryReader
	retrier ActionRetrier
	slots   chan struct{}
}

// NewActionDeliveries 注入部署专属存储及可选重试入口，不启动自动投递器。
func NewActionDeliveries(reader ActionDeliveryReader, retrier ActionRetrier) *ActionDeliveries {
	return &ActionDeliveries{reader: reader, retrier: retrier, slots: make(chan struct{}, 2)}
}

type actionDeliveryQuery struct {
	tenant, alert, target, source, state, action, after string
	limit                                               int
}

func (q actionDeliveryQuery) scope() []string {
	return []string{q.tenant, q.alert, q.target, q.source, q.state, q.action}
}

func (q actionDeliveryQuery) cursor(id string) string {
	if id == "" {
		return ""
	}
	raw, _ := json.Marshal(append(q.scope(), id))
	return base64.RawURLEncoding.EncodeToString(raw)
}

func parseActionDeliveryQuery(r *http.Request, list bool) (actionDeliveryQuery, error) {
	keys := []string{"bk_tenant_id"}
	if list {
		keys = append(keys, "alert_id", "target_id", "source_id", "state", "action", "after", "limit")
	}
	if policyQuery(r, keys...) != nil {
		return actionDeliveryQuery{}, policy.ErrInvalid
	}
	v := r.URL.Query()
	q := actionDeliveryQuery{tenant: v.Get("bk_tenant_id"), alert: v.Get("alert_id"), target: v.Get("target_id"), source: v.Get("source_id"), state: v.Get("state"), action: v.Get("action"), limit: 4}
	if domain.ValidateIdentityPart("tenant", q.tenant, 64) != nil || len(q.alert) > domain.EntityIDMaxBytes || (q.target != "" && domain.ValidateIdentityPart("target", q.target, 64) != nil) || (q.source != "" && domain.ValidateIdentityPart("source", q.source, 32) != nil) || (q.action != "" && !slices.Contains([]string{"firing", "resolved", "close"}, q.action)) || (q.state != "" && !slices.Contains([]string{"pending", "waiting_projection", "sending", "retry", "succeeded", "skipped", "failed"}, q.state)) {
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
		if err != nil || json.Unmarshal(data, &parts) != nil || len(parts) != 7 || !slices.Equal(parts[:6], q.scope()) || !policyRuntimeHashID(parts[6]) {
			return q, policy.ErrInvalid
		}
		q.after = parts[6]
	}
	return q, nil
}

func (q actionDeliveryQuery) matches(t actiondelivery.Task) bool {
	return (q.alert == "" || q.alert == t.Request.AlertID) && (q.target == "" || q.target == t.Request.TargetID) && (q.source == "" || q.source == t.SourceID) && (q.state == "" || q.state == t.Progress.State) && (q.action == "" || q.action == t.Request.Action)
}

type actionDeliveryView struct {
	ActionID      string                  `json:"action_id"`
	Action        string                  `json:"action"`
	Cause         actiondelivery.Cause    `json:"cause"`
	RequestHash   string                  `json:"request_hash"`
	Tenant        string                  `json:"bk_tenant_id"`
	ID            string                  `json:"id"`
	AlertID       string                  `json:"alert_id"`
	AlarmID       string                  `json:"alarm_id"`
	TargetID      string                  `json:"target_id"`
	SourceID      string                  `json:"source_id"`
	SourceVersion int64                   `json:"source_version"`
	Revision      int64                   `json:"revision"`
	AlertStatus   domain.AlertStatus      `json:"alert_status"`
	ContentHash   string                  `json:"content_hash"`
	TaskVersion   string                  `json:"task_version"`
	CreatedAt     time.Time               `json:"created_at"`
	Progress      actiondelivery.Progress `json:"progress"`
}

func actionView(row actiondelivery.StoredTask, tenant, id string) (actionDeliveryView, error) {
	t := row.Task
	if t.Request.TenantID != tenant || (id != "" && t.ID != id) {
		return actionDeliveryView{}, policy.ErrAccess
	}
	if t.Validate() != nil || row.Version == "" || len(row.Version) > 128 {
		return actionDeliveryView{}, policy.ErrUnavailable
	}
	var snapshot struct {
		Status domain.AlertStatus `json:"status"`
	}
	if json.Unmarshal(t.Request.Alert, &snapshot) != nil {
		return actionDeliveryView{}, policy.ErrUnavailable
	}
	return actionDeliveryView{ActionID: t.Request.ActionID, Action: t.Request.Action, Cause: t.Request.Cause, RequestHash: t.Request.Hash(), AlertStatus: snapshot.Status, Tenant: tenant, ID: t.ID, AlertID: t.Request.AlertID, AlarmID: t.Request.AlarmID, TargetID: t.Request.TargetID, SourceID: t.SourceID, SourceVersion: t.SourceVersion, Revision: t.Request.Revision, ContentHash: t.Request.ContentHash, TaskVersion: row.Version, CreatedAt: t.CreatedAt, Progress: t.Clone().Progress}, nil
}

func actionDeliveryFailure(w http.ResponseWriter, err error) {
	switch {
	case actionErrorOnly(err, actiondelivery.ErrNotFound):
		err = policy.ErrNotFound
	case actionErrorOnly(err, actiondelivery.ErrConflict):
		err = policy.ErrConflict
	case actiondelivery.CanDefer(err):
		err = policy.ErrPreviewCapacity
	case errors.Is(err, actiondelivery.ErrInvalid), errors.Is(err, actiondelivery.ErrInvalidReceipt):
		err = policy.ErrUnavailable
	}
	policyFailure(w, err)
}

// actionErrorOnly 不把混有租约释放或存储失败的错误降为普通不存在/版本冲突。
func actionErrorOnly(err, target error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			if !actionErrorOnly(part, target) {
				return false
			}
		}
		return true
	}
	if inner := errors.Unwrap(err); inner != nil {
		return actionErrorOnly(inner, target)
	}
	return errors.Is(err, target)
}

func (a *API) actionDeliveryCall(w http.ResponseWriter, r *http.Request) (context.Context, func(), bool) {
	w.Header().Set("Cache-Control", "no-store")
	runtime := a.ActionDeliveries
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

func (a *API) listActionDeliveries(w http.ResponseWriter, r *http.Request) {
	q, err := parseActionDeliveryQuery(r, true)
	if err != nil {
		policyFailure(w, err)
		return
	}
	ctx, done, ok := a.actionDeliveryCall(w, r)
	if !ok {
		return
	}
	defer done()
	rows, err := a.ActionDeliveries.reader.List(ctx, actiondelivery.Query{TenantID: q.tenant, After: q.after, Limit: q.limit})
	if err != nil {
		actionDeliveryFailure(w, err)
		return
	}
	if len(rows) > q.limit {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	items := []actionDeliveryView{}
	last := q.after
	for _, row := range rows {
		v, err := actionView(row, q.tenant, "")
		if err != nil {
			actionDeliveryFailure(w, err)
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

func (a *API) getActionDelivery(w http.ResponseWriter, r *http.Request) {
	a.readActionDelivery(w, r, false)
}

func (a *API) actionDeliverySnapshot(w http.ResponseWriter, r *http.Request) {
	a.readActionDelivery(w, r, true)
}

func (a *API) readActionDelivery(w http.ResponseWriter, r *http.Request, snapshot bool) {
	q, err := parseActionDeliveryQuery(r, false)
	if err != nil {
		policyFailure(w, err)
		return
	}
	ctx, done, ok := a.actionDeliveryCall(w, r)
	if !ok {
		return
	}
	defer done()
	row, err := a.ActionDeliveries.reader.Get(ctx, q.tenant, r.PathValue("id"))
	if err != nil {
		actionDeliveryFailure(w, err)
		return
	}
	v, err := actionView(row, q.tenant, r.PathValue("id"))
	if err != nil {
		actionDeliveryFailure(w, err)
		return
	}
	if snapshot {
		writePolicyRuntimeLimit(w, map[string]any{"bk_tenant_id": q.tenant, "id": v.ID, "revision": v.Revision, "request_hash": v.RequestHash, "request": row.Task.Request}, actiondelivery.MaxTaskBytes)
		return
	}
	writePolicyRuntime(w, v)
}

func (a *API) retryActionDelivery(w http.ResponseWriter, r *http.Request) {
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
	command := actiondelivery.RetryCommand{TenantID: body.TenantID, TaskID: r.PathValue("id"), ExpectedVersion: body.ExpectedVersion, OperationID: body.OperationID, OperatorID: body.OperatorID, Reason: body.Reason}
	if command.Validate() != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	ctx, done, ok := a.actionDeliveryCall(w, r)
	if !ok {
		return
	}
	defer done()
	if a.ActionDeliveries.retrier == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	row, err := a.ActionDeliveries.retrier.Retry(ctx, command)
	if err != nil {
		actionDeliveryFailure(w, err)
		return
	}
	v, err := actionView(row, command.TenantID, command.TaskID)
	if err != nil {
		actionDeliveryFailure(w, err)
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

// actionDeliveryOrder 只观察同租户/Alert/目标的未结清队首，不表示投影门槛已满足或允许发送。
func (a *API) actionDeliveryOrder(w http.ResponseWriter, r *http.Request) {
	q, err := parseActionDeliveryQuery(r, false)
	if err != nil {
		policyFailure(w, err)
		return
	}
	ctx, done, ok := a.actionDeliveryCall(w, r)
	if !ok {
		return
	}
	defer done()
	row, err := a.ActionDeliveries.reader.Get(ctx, q.tenant, r.PathValue("id"))
	if err != nil {
		actionDeliveryFailure(w, err)
		return
	}
	selected, err := actionView(row, q.tenant, r.PathValue("id"))
	if err != nil {
		actionDeliveryFailure(w, err)
		return
	}
	head, err := a.ActionDeliveries.reader.OldestUnsettled(ctx, q.tenant, selected.AlertID, selected.TargetID)
	var result *actionDeliveryView
	if err != nil && !actionErrorOnly(err, actiondelivery.ErrNotFound) {
		actionDeliveryFailure(w, err)
		return
	}
	if err == nil {
		value, e := actionView(head, q.tenant, "")
		if e != nil {
			actionDeliveryFailure(w, e)
			return
		}
		if value.AlertID != selected.AlertID || value.TargetID != selected.TargetID || !head.Task.Unsettled() {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		// 排序索引可能落后于人工恢复或终结；展示前实时重读，已结清时不伪造新的队首。
		current, e := a.ActionDeliveries.reader.Get(ctx, q.tenant, value.ID)
		if e != nil {
			actionDeliveryFailure(w, e)
			return
		}
		value, e = actionView(current, q.tenant, value.ID)
		if e != nil {
			actionDeliveryFailure(w, e)
			return
		}
		if value.AlertID != selected.AlertID || value.TargetID != selected.TargetID {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		if current.Task.Unsettled() {
			result = &value
		}
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": q.tenant, "id": selected.ID, "alert_id": selected.AlertID, "target_id": selected.TargetID, "observed_at": time.Now().UTC(), "head": result})
}
