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
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
)

// SuppressionRuntimeStore 只提供已有缓存的分页与精确读取，不允许 API 调用计数、占位或清理。
type SuppressionRuntimeStore interface {
	ListSuppressionWindows(context.Context, string, string, string, int) (redisstate.SuppressionWindowPage, error)
	ReadSuppressionWindow(context.Context, string, string, string) (redisstate.SuppressionWindow, bool, error)
	ListSuppressionMembers(context.Context, string, string, string, string, string, int) (redisstate.SuppressionMemberPage, error)
}

// SuppressionRuntime 持有调用方管理的只读依赖，所有抑制查询共享两个并发名额。
type SuppressionRuntime struct {
	reader SuppressionRuntimeStore
	slots  chan struct{}
}

// NewSuppressionRuntime 不探测 Redis，也不启动修复或定时任务。
func NewSuppressionRuntime(reader SuppressionRuntimeStore) *SuppressionRuntime {
	return &SuppressionRuntime{reader: reader, slots: make(chan struct{}, 2)}
}

type suppressionQuery struct {
	tenant, kind, id, policyID, source, owner, epoch, after string
	limit                                                   int
}

func suppressionID(kind, id string) bool {
	parts := strings.Split(id, ":")
	if (kind == "clip" && len(parts) != 2) || (kind == "aggregation" && len(parts) != 1) {
		return false
	}
	for _, part := range parts {
		raw, err := hex.DecodeString(part)
		if err != nil || len(raw) != 32 || strings.ToLower(part) != part {
			return false
		}
	}
	return true
}

func parseSuppressionQuery(r *http.Request, mode string) (suppressionQuery, error) {
	if err := policyQuery(r, "bk_tenant_id", "policy_id", "event_source_id", "owner_alert_id", "epoch", "after", "limit"); err != nil {
		return suppressionQuery{}, err
	}
	v := r.URL.Query()
	q := suppressionQuery{tenant: v.Get("bk_tenant_id"), kind: r.PathValue("kind"), id: r.PathValue("id"), policyID: v.Get("policy_id"), source: v.Get("event_source_id"), owner: v.Get("owner_alert_id"), epoch: v.Get("epoch"), limit: 4}
	if domain.ValidateIdentityPart("tenant", q.tenant, 64) != nil || (q.kind != "clip" && q.kind != "aggregation") || (q.policyID != "" && domain.ValidateIdentityPart("policy", q.policyID, 80) != nil) || (q.source != "" && domain.ValidateIdentityPart("source", q.source, 32) != nil) || len(q.owner) > domain.EntityIDMaxBytes || len(q.epoch) > domain.EntityIDMaxBytes {
		return q, policy.ErrInvalid
	}
	if mode != "list" && (!suppressionID(q.kind, q.id) || q.policyID != "" || q.source != "" || q.owner != "") {
		return q, policy.ErrInvalid
	}
	if (mode == "members") != (q.epoch != "") {
		return q, policy.ErrInvalid
	}
	if mode == "detail" && (v.Has("after") || v.Has("limit")) {
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
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		var parts []string
		if err != nil || json.Unmarshal(decoded, &parts) != nil || len(parts) != 8 || !slices.Equal(parts[:7], q.scope()) || len(parts[7]) > 160 {
			return q, policy.ErrInvalid
		}
		q.after = parts[7]
	}
	if mode == "list" && q.after != "" && !suppressionID(q.kind, q.after) {
		return q, policy.ErrInvalid
	}
	if mode == "members" && q.after != "" {
		n, err := strconv.Atoi(q.after)
		if err != nil || n < 1 || n > 10000 || strconv.Itoa(n) != q.after {
			return q, policy.ErrInvalid
		}
	}
	return q, nil
}

func (q suppressionQuery) scope() []string {
	return []string{q.tenant, q.kind, q.id, q.policyID, q.source, q.owner, q.epoch}
}

func (q suppressionQuery) cursor(next string) string {
	if next == "" {
		return ""
	}
	raw, _ := json.Marshal(append(q.scope(), next))
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (q suppressionQuery) matches(v redisstate.SuppressionWindow) bool {
	return (q.policyID == "" || q.policyID == v.Policy.ID) && (q.source == "" || q.source == v.SourceID || q.source == v.OwnerSourceID) && (q.owner == "" || q.owner == v.OwnerAlertID)
}

func (a *API) listSuppressionRuntime(w http.ResponseWriter, r *http.Request) {
	a.serveSuppressionRuntime(w, r, "list")
}

func (a *API) getSuppressionRuntime(w http.ResponseWriter, r *http.Request) {
	a.serveSuppressionRuntime(w, r, "detail")
}

func (a *API) suppressionRuntimeMembers(w http.ResponseWriter, r *http.Request) {
	a.serveSuppressionRuntime(w, r, "members")
}

func (a *API) serveSuppressionRuntime(w http.ResponseWriter, r *http.Request, mode string) {
	w.Header().Set("Cache-Control", "no-store")
	if a.SuppressionRuntime == nil || a.SuppressionRuntime.reader == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	q, err := parseSuppressionQuery(r, mode)
	if err != nil {
		policyFailure(w, err)
		return
	}
	select {
	case a.SuppressionRuntime.slots <- struct{}{}:
		defer func() { <-a.SuppressionRuntime.slots }()
	default:
		policyFailure(w, policy.ErrPreviewCapacity)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var result any
	if mode == "list" {
		result, err = a.SuppressionRuntime.list(ctx, q)
	} else {
		var current redisstate.SuppressionWindow
		current, err = a.SuppressionRuntime.get(ctx, q)
		result = current
		if err == nil && mode == "members" {
			result, err = a.SuppressionRuntime.members(ctx, q, current)
		}
	}
	if err != nil {
		if errors.Is(err, redisstate.ErrState) || errors.Is(err, redisstate.ErrBudget) {
			err = policy.ErrUnavailable
		}
		policyFailure(w, err)
		return
	}
	writePolicyRuntime(w, result)
}

func (s *SuppressionRuntime) get(ctx context.Context, q suppressionQuery) (redisstate.SuppressionWindow, error) {
	v, found, err := s.reader.ReadSuppressionWindow(ctx, q.tenant, q.kind, q.id)
	if err != nil {
		return v, err
	}
	if !found {
		return v, policy.ErrNotFound
	}
	if v.TenantID != q.tenant || v.Kind != q.kind || v.ID != q.id {
		return v, policy.ErrAccess
	}
	if v.Validate() != nil {
		return v, policy.ErrUnavailable
	}
	return v, nil
}

func (s *SuppressionRuntime) list(ctx context.Context, q suppressionQuery) (any, error) {
	page, err := s.reader.ListSuppressionWindows(ctx, q.tenant, q.kind, q.after, q.limit)
	if err != nil {
		return nil, err
	}
	if len(page.Items) > q.limit || (page.Next != "" && (!suppressionID(q.kind, page.Next) || page.Next == q.after)) {
		return nil, policy.ErrUnavailable
	}
	items := []redisstate.SuppressionWindow{}
	seen := map[string]bool{}
	for _, v := range page.Items {
		if v.TenantID != q.tenant || v.Kind != q.kind {
			return nil, policy.ErrAccess
		}
		if v.Validate() != nil || seen[v.ID] {
			return nil, policy.ErrUnavailable
		}
		seen[v.ID] = true
		if q.matches(v) {
			items = append(items, v)
		}
	}
	return map[string]any{"bk_tenant_id": q.tenant, "kind": q.kind, "items": items, "next": q.cursor(page.Next)}, nil
}

func (s *SuppressionRuntime) members(ctx context.Context, q suppressionQuery, current redisstate.SuppressionWindow) (any, error) {
	if current.Epoch != q.epoch {
		return nil, policy.ErrConflict
	}
	page, err := s.reader.ListSuppressionMembers(ctx, q.tenant, q.kind, q.id, q.epoch, q.after, q.limit)
	if err != nil {
		return nil, err
	}
	if page.Epoch != q.epoch || len(page.Items) > q.limit || page.ObservedAtMillis < 0 || page.ObservedAtMillis > 1<<46 {
		return nil, policy.ErrUnavailable
	}
	if page.Next != "" {
		n, err := strconv.Atoi(page.Next)
		before, _ := strconv.Atoi(q.after)
		if err != nil || n != before+q.limit || n > 10000 || strconv.Itoa(n) != page.Next {
			return nil, policy.ErrUnavailable
		}
	}
	seen := map[string]bool{}
	for _, v := range page.Items {
		if v.EventID == "" || len(v.EventID) > domain.EntityIDMaxBytes || seen[v.EventID] || (redisstate.Identity{TenantID: q.tenant, SourceID: v.SourceID, Fingerprint: v.Fingerprint}).Validate() != nil || v.AtMillis < 0 || v.AtMillis > 1<<46 {
			return nil, policy.ErrUnavailable
		}
		if q.kind == "clip" && (v.SourceID != current.SourceID || v.Fingerprint != current.Fingerprint) {
			return nil, policy.ErrAccess
		}
		if q.kind == "aggregation" && (v.AtMillis < current.StartedAtMillis || v.AtMillis > current.ExpiresAtMillis) {
			return nil, policy.ErrUnavailable
		}
		seen[v.EventID] = true
	}
	if page.Items == nil {
		page.Items = []redisstate.SuppressionMember{}
	}
	return map[string]any{"bk_tenant_id": q.tenant, "kind": q.kind, "id": q.id, "epoch": q.epoch, "observed_at_ms": page.ObservedAtMillis, "items": page.Items, "next": q.cursor(page.Next)}, nil
}
