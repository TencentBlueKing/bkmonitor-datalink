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
	"linkd/internal/policy"
	"linkd/internal/suppressioncheck"
)

// SuppressionCheckJournal 不要求 Redis 窗口仍存在，历史始终从持久记录读取。
type SuppressionCheckJournal interface {
	List(context.Context, string, string, string, string, int) (suppressioncheck.Page, error)
	Get(context.Context, string, string, string, string) (suppressioncheck.Stored, error)
}

// SuppressionCheckRequests 只提交完整命令，实际复核由独立受控任务执行。
type SuppressionCheckRequests interface {
	Request(context.Context, suppressioncheck.Command) (suppressioncheck.Request, error)
}

// SuppressionChecks 共享两项管理额度；没有运行器时 Requests 为 nil，拒绝新命令。
type SuppressionChecks struct {
	journal  SuppressionCheckJournal
	requests SuppressionCheckRequests
	slots    chan struct{}
}

// NewSuppressionChecks 不启动循环或清理窗口。
func NewSuppressionChecks(j SuppressionCheckJournal, r SuppressionCheckRequests) *SuppressionChecks {
	return &SuppressionChecks{j, r, make(chan struct{}, 2)}
}

func suppressionCheckFailure(w http.ResponseWriter, err error) {
	if suppressioncheck.CanDefer(err) {
		err = policy.ErrPreviewCapacity
	} else if errors.Is(err, policy.ErrInvalid) {
		err = policy.ErrUnavailable
	}
	policyFailure(w, err)
}

func (a *API) suppressionCheckCall(w http.ResponseWriter, r *http.Request) (context.Context, func(), bool) {
	w.Header().Set("Cache-Control", "no-store")
	if a.SuppressionChecks == nil || a.SuppressionChecks.journal == nil {
		policyFailure(w, policy.ErrUnavailable)
		return nil, nil, false
	}
	select {
	case a.SuppressionChecks.slots <- struct{}{}:
	default:
		policyFailure(w, policy.ErrPreviewCapacity)
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	return ctx, func() { cancel(); <-a.SuppressionChecks.slots }, true
}

func (a *API) requestSuppressionCheck(w http.ResponseWriter, r *http.Request) {
	ctx, done, ok := a.suppressionCheckCall(w, r)
	if !ok {
		return
	}
	defer done()
	if a.SuppressionChecks.requests == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	if policyQuery(r) != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	var body struct {
		TenantID  string  `json:"bk_tenant_id"`
		Epoch     string  `json:"expected_epoch"`
		Owner     *string `json:"expected_owner_alert_id"`
		Operation string  `json:"operation_id"`
		Operator  string  `json:"operator_id"`
		Reason    string  `json:"reason"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || body.Owner == nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	c := suppressioncheck.Command{TenantID: body.TenantID, Kind: r.PathValue("kind"), WindowID: r.PathValue("id"), ExpectedEpoch: body.Epoch, ExpectedOwner: *body.Owner, OperationID: body.Operation, OperatorID: body.Operator, Reason: body.Reason}
	if c.Validate() != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	result, err := a.SuppressionChecks.requests.Request(ctx, c)
	if err != nil {
		suppressionCheckFailure(w, err)
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

func (a *API) listSuppressionChecks(w http.ResponseWriter, r *http.Request) {
	a.readSuppressionChecks(w, r, false)
}

func (a *API) getSuppressionCheck(w http.ResponseWriter, r *http.Request) {
	a.readSuppressionChecks(w, r, true)
}

func (a *API) readSuppressionChecks(w http.ResponseWriter, r *http.Request, exact bool) {
	ctx, done, ok := a.suppressionCheckCall(w, r)
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
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !suppressionID(kind, id) {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	valid := func(q suppressioncheck.Request) bool {
		if q.Command.TenantID != tenant || q.Command.Kind != kind || q.Command.WindowID != id {
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
		row, err := a.SuppressionChecks.journal.Get(ctx, tenant, kind, id, requestID)
		if err != nil {
			suppressionCheckFailure(w, err)
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
	page, err := a.SuppressionChecks.journal.List(ctx, tenant, kind, id, after, limit)
	if err != nil {
		suppressionCheckFailure(w, err)
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
		page.Items = []suppressioncheck.Request{}
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": tenant, "kind": kind, "window_id": id, "items": page.Items, "next": next})
}
