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

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/suppressioncleanup"
)

// SuppressionCleanupReader 只读取独立持久记录，不能通过历史查询发起 Redis 清理。
type SuppressionCleanupReader interface {
	Get(context.Context, string, string) (suppressioncleanup.Record, string, error)
	List(context.Context, string, string, int) (suppressioncleanup.Page, error)
}

// SuppressionCleanups 提供共享两并发/十秒的历史查询，与可丢失运行态分离。
type SuppressionCleanups struct {
	reader SuppressionCleanupReader
	slots  chan struct{}
}

// NewSuppressionCleanups 不初始化 Redis，也不触发任何终态补偿。
func NewSuppressionCleanups(r SuppressionCleanupReader) *SuppressionCleanups {
	return &SuppressionCleanups{r, make(chan struct{}, 2)}
}

type cleanupQuery struct {
	tenant, source, fingerprint, owner, event, state, after string
	kind, id, epoch                                         string
	limit                                                   int
}

func (q cleanupQuery) scope() []string {
	return []string{q.tenant, q.source, q.fingerprint, q.owner, q.event, q.state, q.kind, q.id, q.epoch}
}

func (q cleanupQuery) cursor(id string) string {
	if id == "" {
		return ""
	}
	b, _ := json.Marshal(append(q.scope(), id))
	return base64.RawURLEncoding.EncodeToString(b)
}

func parseCleanupQuery(r *http.Request) (cleanupQuery, error) {
	keys := []string{"bk_tenant_id"}
	list := r.PathValue("id") == ""
	if list {
		keys = append(keys, "event_source_id", "fingerprint", "alert_id", "event_id", "state", "after", "limit", "window_kind", "window_id", "epoch")
	}
	if policyQuery(r, keys...) != nil {
		return cleanupQuery{}, policy.ErrInvalid
	}
	v := r.URL.Query()
	q := cleanupQuery{tenant: v.Get("bk_tenant_id"), source: v.Get("event_source_id"), fingerprint: v.Get("fingerprint"), owner: v.Get("alert_id"), event: v.Get("event_id"), state: v.Get("state"), limit: 4, kind: v.Get("window_kind"), id: v.Get("window_id"), epoch: v.Get("epoch")}
	if !suppressioncleanup.ValidWindowFilter(q.kind, q.id, q.epoch) {
		return q, policy.ErrInvalid
	}
	if domain.ValidateIdentityPart("tenant", q.tenant, 64) != nil || !suppressioncleanup.ValidFilter(q.source, q.fingerprint, q.owner, q.event) || (q.state != "" && q.state != "pending" && q.state != "completed") || (!list && !policyRuntimeHashID(r.PathValue("id"))) {
		return q, policy.ErrInvalid
	}
	if raw := v.Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 4 {
			return q, policy.ErrInvalid
		}
		q.limit = n
	}
	if raw := v.Get("after"); raw != "" {
		if len(raw) > 2048 {
			return q, policy.ErrInvalid
		}
		b, e := base64.RawURLEncoding.DecodeString(raw)
		var parts []string
		if e != nil || json.Unmarshal(b, &parts) != nil || len(parts) != 10 || !slices.Equal(parts[:9], q.scope()) || !policyRuntimeHashID(parts[9]) {
			return q, policy.ErrInvalid
		}
		q.after = parts[9]
	}
	return q, nil
}

func (a *API) listSuppressionCleanups(w http.ResponseWriter, r *http.Request) {
	a.serveSuppressionCleanups(w, r)
}

func (a *API) getSuppressionCleanup(w http.ResponseWriter, r *http.Request) {
	a.serveSuppressionCleanups(w, r)
}

func (a *API) serveSuppressionCleanups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q, e := parseCleanupQuery(r)
	if e != nil {
		policyFailure(w, e)
		return
	}
	runtime := a.SuppressionCleanups
	if runtime == nil || runtime.reader == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	select {
	case runtime.slots <- struct{}{}:
		defer func() { <-runtime.slots }()
	default:
		policyFailure(w, policy.ErrPreviewCapacity)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	valid := func(v suppressioncleanup.Record) bool {
		if v.Cause.TenantID != q.tenant {
			policyFailure(w, policy.ErrAccess)
			return false
		}
		if v.Validate() != nil {
			policyFailure(w, policy.ErrUnavailable)
			return false
		}
		return true
	}
	if id := r.PathValue("id"); id != "" {
		row, version, err := runtime.reader.Get(ctx, q.tenant, id)
		if err != nil {
			cleanupReadFailure(w, err)
			return
		}
		if !valid(row) {
			return
		}
		if row.ID != id || version == "" {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		writePolicyRuntime(w, row)
		return
	}
	page, err := runtime.reader.List(ctx, q.tenant, q.after, q.limit)
	if err != nil {
		cleanupReadFailure(w, err)
		return
	}
	if len(page.Items) > q.limit || (page.Next != "" && (!policyRuntimeHashID(page.Next) || page.Next <= q.after)) {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	items := []suppressioncleanup.Record{}
	last := q.after
	for _, row := range page.Items {
		if !valid(row) {
			return
		}
		if row.ID <= last {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		last = row.ID
		if suppressioncleanup.IdentityMatches(row, q.source, q.fingerprint, q.owner, q.event) && (q.state == "" || q.state == row.State) && suppressioncleanup.WindowMatches(row, q.kind, q.id, q.epoch) {
			items = append(items, row)
		}
	}
	if page.Next != "" && page.Next != last {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": q.tenant, "items": items, "next": q.cursor(page.Next)})
}

func cleanupReadFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, policy.ErrInvalid) {
		err = policy.ErrUnavailable
	}
	policyFailure(w, err)
}
