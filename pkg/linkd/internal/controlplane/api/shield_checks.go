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
	"strings"
	"time"

	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	"linkd/internal/shieldcheck"
)

// ShieldDiagnostics 只读独立检查记录，不从历史诊断推断当前屏蔽状态。
type ShieldDiagnostics interface {
	Latest(context.Context, string, string) (shieldcheck.Check, error)
	List(context.Context, string, string, string, int) (shieldcheck.Page, error)
	GetRequest(context.Context, string, string, string) (shieldcheck.StoredRequest, error)
}

// ShieldRequests 仅提交带业务版本和稳定身份的复查命令，不提供强制解除。
type ShieldRequests interface {
	Request(context.Context, shieldcheck.Command) (shieldcheck.Request, error)
}

func (a *API) shieldCheckQuery(w http.ResponseWriter, r *http.Request, page bool) (shieldQuery, bool) {
	q, err := parseShieldQuery(r)
	if err != nil || q.alert == "" || (!page && (q.after != "" || r.URL.Query().Has("limit"))) {
		shieldFailure(w, policy.ErrInvalid)
		return q, false
	}
	if a.ShieldDiagnostics == nil {
		shieldFailure(w, policy.ErrUnavailable)
		return q, false
	}
	if _, err := a.ShieldRuntime.get(r.Context(), q.tenant, q.alert); err != nil {
		shieldFailure(w, err)
		return q, false
	}
	return q, true
}

func (a *API) shieldLatestCheck(w http.ResponseWriter, r *http.Request) {
	if !a.beginShieldQuery(w) {
		return
	}
	defer func() { <-a.ShieldRuntime.slots }()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	q, ok := a.shieldCheckQuery(w, r, false)
	if !ok {
		return
	}
	c, err := a.ShieldDiagnostics.Latest(ctx, q.tenant, q.alert)
	if errors.Is(err, policy.ErrNotFound) {
		writePolicyRuntime(w, map[string]any{"bk_tenant_id": q.tenant, "alert_id": q.alert, "check": nil})
		return
	}
	if err != nil {
		shieldFailure(w, err)
		return
	}
	if c.TenantID != q.tenant || c.AlertID != q.alert {
		shieldFailure(w, policy.ErrAccess)
		return
	}
	if c.Validate() != nil {
		shieldFailure(w, policy.ErrUnavailable)
		return
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": q.tenant, "alert_id": q.alert, "check": c})
}

func (a *API) shieldRequestHistory(w http.ResponseWriter, r *http.Request) {
	if !a.beginShieldQuery(w) {
		return
	}
	defer func() { <-a.ShieldRuntime.slots }()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	q, ok := a.shieldCheckQuery(w, r, true)
	if !ok {
		return
	}
	after := ""
	if q.after != "" {
		if !strings.HasPrefix(q.after, "requests:") {
			shieldFailure(w, policy.ErrInvalid)
			return
		}
		after = strings.TrimPrefix(q.after, "requests:")
	}
	p, err := a.ShieldDiagnostics.List(ctx, q.tenant, q.alert, after, q.limit)
	if err != nil {
		shieldFailure(w, err)
		return
	}
	if len(p.Items) > q.limit || len(p.Next) > 256 || (p.Next != "" && p.Next <= after) {
		shieldFailure(w, policy.ErrUnavailable)
		return
	}
	for _, v := range p.Items {
		if v.Command.TenantID != q.tenant || v.Command.AlertID != q.alert {
			shieldFailure(w, policy.ErrAccess)
			return
		}
		if v.Validate() != nil {
			shieldFailure(w, policy.ErrUnavailable)
			return
		}
	}
	if p.Items == nil {
		p.Items = []shieldcheck.Request{}
	}
	next := ""
	if p.Next != "" {
		next = q.cursor("requests:" + p.Next)
	}
	writePolicyRuntimeLimit(w, map[string]any{"bk_tenant_id": q.tenant, "alert_id": q.alert, "items": p.Items, "next": next}, 2<<20)
}

func (a *API) shieldRequestDetail(w http.ResponseWriter, r *http.Request) {
	if !a.beginShieldQuery(w) {
		return
	}
	defer func() { <-a.ShieldRuntime.slots }()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	q, ok := a.shieldCheckQuery(w, r, false)
	if !ok {
		return
	}
	value, err := a.ShieldDiagnostics.GetRequest(ctx, q.tenant, q.alert, r.PathValue("request"))
	if err != nil {
		shieldFailure(w, err)
		return
	}
	v := value.Request
	if v.Command.TenantID != q.tenant || v.Command.AlertID != q.alert || v.ID != r.PathValue("request") {
		shieldFailure(w, policy.ErrAccess)
		return
	}
	if v.Validate() != nil {
		shieldFailure(w, policy.ErrUnavailable)
		return
	}
	writePolicyRuntime(w, v)
}

func (a *API) requestShieldCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.ShieldRequests == nil {
		shieldFailure(w, policy.ErrUnavailable)
		return
	}
	if r.URL.RawQuery != "" {
		shieldFailure(w, policy.ErrInvalid)
		return
	}
	var input struct {
		TenantID         string `json:"bk_tenant_id"`
		OperationID      string `json:"operation_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		OperatorID       string `json:"operator_id"`
		Reason           string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if decode(r, &input) != nil {
		shieldFailure(w, policy.ErrInvalid)
		return
	}
	c := shieldcheck.Command{TenantID: input.TenantID, AlertID: r.PathValue("id"), OperationID: input.OperationID, ExpectedRevision: input.ExpectedRevision, OperatorID: input.OperatorID, Reason: input.Reason}
	if c.Validate() != nil {
		shieldFailure(w, policy.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := a.ShieldRequests.Request(ctx, c)
	if errors.Is(err, scheduler.ErrLockBusy) {
		err = policy.ErrPreviewCapacity
	}
	if err != nil {
		shieldFailure(w, err)
		return
	}
	if result.Validate() != nil || result.Command != c {
		shieldFailure(w, policy.ErrUnavailable)
		return
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > shieldcheck.MaxDocumentBytes {
		shieldFailure(w, policy.ErrUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if result.State == "pending" {
		w.WriteHeader(http.StatusAccepted)
	}
	_, _ = w.Write(raw)
}
