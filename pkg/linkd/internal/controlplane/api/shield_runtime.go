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

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// ShieldRuntimeStore 只向管理面提供当前绑定、单条 Alert 和该 Alert 流水的读取能力。
type ShieldRuntimeStore interface {
	store.ShieldAlertReader
	GetAlert(context.Context, string, string) (store.StoredAlert, error)
	ListAlertLogs(context.Context, string, string, store.PageRequest) (store.AlertLogPage, error)
}

// ShieldRuntime 将屏蔽关系与生命周期、历史放行分开展示；不提供强制解除能力。
type ShieldRuntime struct {
	reader ShieldRuntimeStore
	slots  chan struct{}
}

// NewShieldRuntime 注入调用方管理的只读连接，并提供独立的两并发管理预算。
func NewShieldRuntime(reader ShieldRuntimeStore) *ShieldRuntime {
	return &ShieldRuntime{reader: reader, slots: make(chan struct{}, 2)}
}

type shieldQuery struct {
	tenant, policyID, mainID, mode, alert, after string
	limit                                        int
}

func parseShieldQuery(r *http.Request) (shieldQuery, error) {
	if err := policyQuery(r, "bk_tenant_id", "policy_id", "main_alert_id", "binding_type", "after", "limit"); err != nil {
		return shieldQuery{}, err
	}
	values := r.URL.Query()
	q := shieldQuery{tenant: values.Get("bk_tenant_id"), policyID: values.Get("policy_id"), mainID: values.Get("main_alert_id"), mode: values.Get("binding_type"), alert: r.PathValue("id"), limit: 4}
	if domain.ValidateIdentityPart("tenant", q.tenant, 64) != nil || (q.policyID != "" && domain.ValidateIdentityPart("policy", q.policyID, 80) != nil) || len(q.mainID) > domain.EntityIDMaxBytes || len(q.alert) > domain.EntityIDMaxBytes {
		return q, policy.ErrInvalid
	}
	if q.mode != "" && !slices.Contains([]string{"time_shield", "custom_shield", "cmdb_shield"}, q.mode) {
		return q, policy.ErrInvalid
	}
	if q.alert != "" && (q.policyID != "" || q.mainID != "" || q.mode != "") {
		return q, policy.ErrInvalid
	}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 4 {
			return q, policy.ErrInvalid
		}
		q.limit = n
	}
	if raw := values.Get("after"); raw != "" {
		if len(raw) > 8192 {
			return q, policy.ErrInvalid
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		var parts []string
		if err != nil || json.Unmarshal(decoded, &parts) != nil || len(parts) != 6 || !slices.Equal(parts[:5], q.scope()) || len(parts[5]) > 4096 {
			return q, policy.ErrInvalid
		}
		q.after = parts[5]
	}
	return q, nil
}

func (q shieldQuery) scope() []string {
	return []string{q.tenant, q.policyID, q.mainID, q.mode, q.alert}
}

func (q shieldQuery) cursor(after string) string {
	if after == "" {
		return ""
	}
	raw, _ := json.Marshal(append(q.scope(), after))
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (q shieldQuery) matches(a domain.Alert) bool {
	if q.policyID == "" && q.mainID == "" && q.mode == "" {
		return true
	}
	bindings := slices.Clone(a.Shield.Bindings)
	if a.PolicyChange != nil {
		bindings = append(bindings, a.PolicyChange.Before...)
		bindings = append(bindings, a.PolicyChange.After...)
	}
	for _, b := range bindings {
		mode := b.Mode
		if b.Type == "time_shield" {
			mode = b.Type
		}
		if (q.policyID == "" || q.policyID == b.Policy.ID) && (q.mainID == "" || q.mainID == b.MainAlertID) && (q.mode == "" || q.mode == mode) {
			return true
		}
	}
	return false
}

type shieldAlertView struct {
	Tenant    string                    `json:"bk_tenant_id"`
	AlertID   string                    `json:"alert_id"`
	Source    string                    `json:"event_source_id"`
	Revision  int64                     `json:"revision"`
	Status    domain.AlertStatus        `json:"status"`
	Severity  string                    `json:"severity"`
	Shield    domain.AlertShield        `json:"shield"`
	Admission domain.AlertAdmission     `json:"admission"`
	Pending   *domain.AlertPolicyChange `json:"policy_change,omitempty"`
}

func shieldView(a domain.Alert) shieldAlertView {
	return shieldAlertView{Tenant: a.BKTenantID, AlertID: a.AlertID, Source: a.EventSourceID, Revision: a.Revision, Status: a.Status, Severity: a.Severity, Shield: a.Shield.Clone(), Admission: a.Admission.Clone(), Pending: a.PolicyChange.Clone()}
}

func shieldFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		err = policy.ErrNotFound
	case errors.Is(err, store.ErrInvalidArgument), errors.Is(err, store.ErrInvalidCursor):
		err = policy.ErrInvalid
	}
	policyFailure(w, err)
}

func (a *API) beginShieldQuery(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if a.ShieldRuntime == nil || a.ShieldRuntime.reader == nil {
		policyFailure(w, policy.ErrUnavailable)
		return false
	}
	select {
	case a.ShieldRuntime.slots <- struct{}{}:
		return true
	default:
		policyFailure(w, policy.ErrPreviewCapacity)
		return false
	}
}

func (a *API) listShieldRuntime(w http.ResponseWriter, r *http.Request) {
	if !a.beginShieldQuery(w) {
		return
	}
	defer func() { <-a.ShieldRuntime.slots }()
	q, err := parseShieldQuery(r)
	if err != nil {
		shieldFailure(w, err)
		return
	}
	page, err := a.ShieldRuntime.reader.ListShieldAlerts(r.Context(), q.tenant, q.after, q.limit)
	if err != nil {
		shieldFailure(w, err)
		return
	}
	if len(page.Alerts) > q.limit {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	last := q.after
	items := []shieldAlertView{}
	for _, s := range page.Alerts {
		v := s.Alert
		if v.BKTenantID != q.tenant {
			policyFailure(w, policy.ErrAccess)
			return
		}
		if v.AlertID <= last || v.Validate() != nil || s.Version.IsZero() || !store.HasShieldWork(v) {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		last = v.AlertID
		if q.matches(v) {
			items = append(items, shieldView(v))
		}
	}
	if page.Next != "" && (page.Next != last || page.Next <= q.after) {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": q.tenant, "items": items, "next": q.cursor(page.Next)})
}

func (s *ShieldRuntime) get(ctx context.Context, tenant, id string) (domain.Alert, error) {
	get := s.reader.GetAlert
	if current, ok := s.reader.(interface {
		GetAlertCurrent(context.Context, string, string) (store.StoredAlert, error)
	}); ok {
		get = current.GetAlertCurrent
	}
	saved, err := get(ctx, tenant, id)
	if err != nil {
		return domain.Alert{}, err
	}
	if saved.Alert.BKTenantID != tenant || saved.Alert.AlertID != id {
		return domain.Alert{}, policy.ErrAccess
	}
	if saved.Version.IsZero() || saved.Alert.Validate() != nil {
		return domain.Alert{}, policy.ErrUnavailable
	}
	return saved.Alert, nil
}

func (a *API) getShieldRuntime(w http.ResponseWriter, r *http.Request) {
	if !a.beginShieldQuery(w) {
		return
	}
	defer func() { <-a.ShieldRuntime.slots }()
	q, err := parseShieldQuery(r)
	if err != nil || q.alert == "" || q.after != "" || r.URL.Query().Has("limit") {
		shieldFailure(w, policy.ErrInvalid)
		return
	}
	v, err := a.ShieldRuntime.get(r.Context(), q.tenant, q.alert)
	if err != nil {
		shieldFailure(w, err)
		return
	}
	writePolicyRuntime(w, shieldView(v))
}

func (a *API) shieldRuntimeHistory(w http.ResponseWriter, r *http.Request) {
	if !a.beginShieldQuery(w) {
		return
	}
	defer func() { <-a.ShieldRuntime.slots }()
	q, err := parseShieldQuery(r)
	if err != nil || q.alert == "" {
		shieldFailure(w, policy.ErrInvalid)
		return
	}
	if _, err := a.ShieldRuntime.get(r.Context(), q.tenant, q.alert); err != nil {
		shieldFailure(w, err)
		return
	}
	page, err := a.ShieldRuntime.reader.ListAlertLogs(r.Context(), q.tenant, q.alert, store.PageRequest{Cursor: q.after, Limit: q.limit})
	if err != nil {
		shieldFailure(w, err)
		return
	}
	if len(page.Logs) > q.limit || len(page.NextCursor) > 4096 || (page.NextCursor != "" && page.NextCursor == q.after) {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	items := []domain.AlertLog{}
	for _, log := range page.Logs {
		if log.BKTenantID != q.tenant || log.AlertID != q.alert {
			policyFailure(w, policy.ErrAccess)
			return
		}
		if log.Validate() != nil {
			policyFailure(w, policy.ErrUnavailable)
			return
		}
		if log.OperationKind == domain.OperationKindShield || log.OperationKind == domain.OperationKindUnshield {
			items = append(items, log)
		}
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": q.tenant, "alert_id": q.alert, "items": items, "next": q.cursor(page.NextCursor)})
}
