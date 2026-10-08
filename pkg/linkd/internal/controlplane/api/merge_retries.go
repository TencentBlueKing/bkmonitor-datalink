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
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"linkd/internal/domain"
	"linkd/internal/merge"
	"linkd/internal/policy"
)

// MergeRetryJournal 提供精确版本控制点和独立持久请求历史，不依赖 Redis 窗口。
type MergeRetryJournal interface {
	ListRetries(context.Context, string, string, string, string, int) (merge.RetryPage, error)
	GetRetry(context.Context, string, string, string, string) (merge.StoredRetry, error)
	ReadControlPoint(context.Context, string, string, string) (merge.ControlPoint, error)
}

// MergeRetryRequests 只提交完整命令，执行由共享正式合并运行器接续。
type MergeRetryRequests interface {
	Request(context.Context, merge.RetryCommand) (merge.RetryRequest, error)
}

// MergeRetries 共享两项管理额度；没有运行器时 Requests 为 nil，拒绝新命令。
type MergeRetries struct {
	journal  MergeRetryJournal
	requests MergeRetryRequests
	slots    chan struct{}
}

// NewMergeRetries 不启动循环或执行合并步骤。
func NewMergeRetries(j MergeRetryJournal, r MergeRetryRequests) *MergeRetries {
	return &MergeRetries{j, r, make(chan struct{}, 2)}
}

func mergeRetryFailure(w http.ResponseWriter, err error) {
	if merge.RetryCanDefer(err) {
		err = policy.ErrPreviewCapacity
	} else if errors.Is(err, policy.ErrInvalid) {
		err = policy.ErrUnavailable
	}
	policyFailure(w, err)
}

func (a *API) mergeRetryCall(w http.ResponseWriter, r *http.Request) (context.Context, func(), bool) {
	w.Header().Set("Cache-Control", "no-store")
	if a.MergeRetries == nil || a.MergeRetries.journal == nil {
		policyFailure(w, policy.ErrUnavailable)
		return nil, nil, false
	}
	select {
	case a.MergeRetries.slots <- struct{}{}:
	default:
		policyFailure(w, policy.ErrPreviewCapacity)
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	return ctx, func() { cancel(); <-a.MergeRetries.slots }, true
}

func (a *API) requestMergeRetry(w http.ResponseWriter, r *http.Request) {
	ctx, done, ok := a.mergeRetryCall(w, r)
	if !ok {
		return
	}
	defer done()
	if a.MergeRetries.requests == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	if policyQuery(r) != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	var body struct {
		TenantID  string `json:"bk_tenant_id"`
		Token     string `json:"expected_token"`
		Operation string `json:"operation_id"`
		Operator  string `json:"operator_id"`
		Reason    string `json:"reason"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	c := merge.RetryCommand{TenantID: body.TenantID, Kind: r.PathValue("kind"), TargetID: r.PathValue("id"), ExpectedToken: body.Token, OperationID: body.Operation, OperatorID: body.Operator, Reason: body.Reason}
	if c.Validate() != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	result, err := a.MergeRetries.requests.Request(ctx, c)
	if err != nil {
		mergeRetryFailure(w, err)
		return
	}
	if result.Validate() != nil || result.Command != c {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > 64<<10 {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if result.State == "pending" {
		w.WriteHeader(http.StatusAccepted)
	}
	_, _ = w.Write(raw)
}

func (a *API) listMergeRetries(w http.ResponseWriter, r *http.Request) {
	a.readMergeRetries(w, r, false)
}

func (a *API) getMergeRetry(w http.ResponseWriter, r *http.Request) {
	a.readMergeRetries(w, r, true)
}

func (a *API) readMergeRetries(w http.ResponseWriter, r *http.Request, exact bool) {
	ctx, done, ok := a.mergeRetryCall(w, r)
	if !ok {
		return
	}
	defer done()
	keys := []string{"bk_tenant_id"}
	if !exact {
		keys = append(keys, "after", "limit")
	}
	if policyQuery(r, keys...) != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	tenant, kind, id := r.URL.Query().Get("bk_tenant_id"), r.PathValue("kind"), r.PathValue("id")
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !mergeRetryScope(kind, id) {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	valid := func(q merge.RetryRequest) bool {
		if q.Command.TenantID != tenant || q.Command.Kind != kind || q.Command.TargetID != id {
			policyFailure(w, policy.ErrAccess)
			return false
		}
		if q.Validate() != nil {
			policyFailure(w, policy.ErrUnavailable)
			return false
		}
		return true
	}
	if exact {
		requestID := r.PathValue("request")
		if !policyRuntimeHashID(requestID) {
			policyFailure(w, policy.ErrInvalid)
			return
		}
		row, err := a.MergeRetries.journal.GetRetry(ctx, tenant, kind, id, requestID)
		if err != nil {
			mergeRetryFailure(w, err)
			return
		}
		if !valid(row.Request) {
			return
		}
		if row.Request.ID != requestID || row.Version == "" {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		writePolicyRuntime(w, row.Request)
		return
	}
	limit := 4
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 4 {
			policyFailure(w, policy.ErrInvalid)
			return
		}
		limit = n
	}
	scope := []string{tenant, kind, id}
	after := ""
	if raw := r.URL.Query().Get("after"); raw != "" {
		if len(raw) > 2048 {
			policyFailure(w, policy.ErrInvalid)
			return
		}
		b, err := base64.RawURLEncoding.DecodeString(raw)
		var parts []string
		if err != nil || json.Unmarshal(b, &parts) != nil || len(parts) != 4 || !slices.Equal(parts[:3], scope) || len(parts[3]) > 256 {
			policyFailure(w, policy.ErrInvalid)
			return
		}
		after = parts[3]
	}
	page, err := a.MergeRetries.journal.ListRetries(ctx, tenant, kind, id, after, limit)
	if err != nil {
		mergeRetryFailure(w, err)
		return
	}
	if len(page.Items) > limit || len(page.Next) > 256 || (page.Next != "" && page.Next <= after) {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	last := after
	for _, r := range page.Items {
		if !valid(r) {
			return
		}
		if r.Cursor() <= last {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		last = r.Cursor()
	}
	if page.Next != "" && page.Next != last {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	next := ""
	if page.Next != "" {
		raw, _ := json.Marshal(append(scope, page.Next))
		next = base64.RawURLEncoding.EncodeToString(raw)
	}
	if page.Items == nil {
		page.Items = []merge.RetryRequest{}
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": tenant, "kind": kind, "target_id": id, "items": page.Items, "next": next})
}

func mergeRetryScope(kind, id string) bool {
	return (kind == "decisions" || kind == "relations") && policyRuntimeHashID(id)
}

// 精确控制点与普通摘要分开读取，使请求依据包含实时存储版本，而非旧列表进度。
func (a *API) mergeRetryPoint(w http.ResponseWriter, r *http.Request) {
	ctx, done, ok := a.mergeRetryCall(w, r)
	if !ok {
		return
	}
	defer done()
	tenant, kind, id := r.URL.Query().Get("bk_tenant_id"), r.PathValue("kind"), r.PathValue("id")
	if policyQuery(r, "bk_tenant_id") != nil || domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !mergeRetryScope(kind, id) {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	point, err := a.MergeRetries.journal.ReadControlPoint(ctx, tenant, kind, id)
	if err != nil {
		mergeRetryFailure(w, err)
		return
	}
	if point.Validate() != nil || point.TenantID != tenant || point.Kind != kind || point.TargetID != id {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	writePolicyRuntime(w, point)
}
