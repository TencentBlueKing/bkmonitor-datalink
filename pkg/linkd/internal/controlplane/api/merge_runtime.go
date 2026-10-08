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
	"net/http"
	"slices"
	"strconv"
	"time"

	"linkd/internal/domain"
	"linkd/internal/merge"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
)

// MergeWindows 只允许观察当前窗口，不向控制台暴露裁决、删除或强制合并端口。
type MergeWindows interface {
	ListMergeWindows(context.Context, string, string, int) (redisstate.MergeWindowPage, error)
	ReadMergeWindow(context.Context, string, string) (redisstate.MergeWindow, bool, error)
}

// MergeRuntime 共享两条管理查询额度；持久化裁决与易失窗口分别读取，不相互兜底。
type MergeRuntime struct {
	journal *merge.Journal
	windows MergeWindows
	slots   chan struct{}
}

// NewMergeRuntime 注入只读依赖，不连接后端、不启动任务；窗口依赖可缺省并明确返回不可用。
func NewMergeRuntime(journal *merge.Journal, windows MergeWindows) *MergeRuntime {
	return &MergeRuntime{journal: journal, windows: windows, slots: make(chan struct{}, 2)}
}

type mergeRuntimeQuery struct {
	Tenant   string
	Resource string
	PolicyID string
	Phase    string
	AlertID  string
	ParentID string
	After    string
	Limit    int
}

func runtimeQuery(r *http.Request, detail bool) (mergeRuntimeQuery, error) {
	if err := policyQuery(r, "bk_tenant_id", "policy_id", "phase", "alert_id", "after", "limit"); err != nil {
		return mergeRuntimeQuery{}, err
	}
	q := r.URL.Query()
	v := mergeRuntimeQuery{Tenant: q.Get("bk_tenant_id"), Resource: r.PathValue("resource"), PolicyID: q.Get("policy_id"), Phase: q.Get("phase"), AlertID: q.Get("alert_id"), ParentID: r.PathValue("id"), Limit: 4}
	if domain.ValidateIdentityPart("tenant", v.Tenant, 64) != nil || (v.PolicyID != "" && domain.ValidateIdentityPart("policy", v.PolicyID, 80) != nil) || len(v.AlertID) > domain.EntityIDMaxBytes {
		return v, policy.ErrInvalid
	}
	if q.Get("limit") != "" {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 4 {
			return v, policy.ErrInvalid
		}
		v.Limit = n
	}
	if v.Resource != "decisions" && v.Resource != "windows" && v.Resource != "relations" {
		return v, policy.ErrInvalid
	}
	if v.Phase != "" && (v.Resource != "decisions" || !slices.Contains([]string{"capturing", "prepared", "waiting_parent", "linking", "ending", "releasing", "completed"}, v.Phase)) {
		return v, policy.ErrInvalid
	}
	if v.Resource != "relations" && v.AlertID != "" {
		return v, policy.ErrInvalid
	}
	if !detail && v.Resource == "relations" && v.AlertID == "" {
		return v, policy.ErrInvalid
	}
	if detail {
		raw, err := hex.DecodeString(v.ParentID)
		if err != nil || len(raw) != 32 || v.PolicyID != "" || v.Phase != "" || v.AlertID != "" {
			return v, policy.ErrInvalid
		}
	}
	if after := q.Get("after"); after != "" {
		if len(after) > 1024 {
			return v, policy.ErrInvalid
		}
		raw, err := base64.RawURLEncoding.DecodeString(after)
		var parts []string
		if err != nil || json.Unmarshal(raw, &parts) != nil || len(parts) != 7 || !slices.Equal(parts[:6], v.cursorScope()) || len(parts[6]) > 256 {
			return v, policy.ErrInvalid
		}
		v.After = parts[6]
	}
	return v, nil
}

func (q mergeRuntimeQuery) cursorScope() []string {
	return []string{q.Tenant, q.Resource, q.PolicyID, q.Phase, q.AlertID, q.ParentID}
}

func (q mergeRuntimeQuery) cursor(next string) string {
	if next == "" {
		return ""
	}
	raw, _ := json.Marshal(append(q.cursorScope(), next))
	return base64.RawURLEncoding.EncodeToString(raw)
}

type mergeDecisionView struct {
	ID              string               `json:"id"`
	Tenant          string               `json:"bk_tenant_id"`
	WindowID        string               `json:"window_id"`
	GroupKey        string               `json:"group_key"`
	Policy          domain.PolicyVersion `json:"policy"`
	StartedAt       time.Time            `json:"started_at"`
	Deadline        time.Time            `json:"deadline"`
	FrozenAt        time.Time            `json:"frozen_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	Outcome         string               `json:"outcome"`
	Phase           string               `json:"phase"`
	ReasonCode      string               `json:"reason_code,omitempty"`
	ParentEventID   string               `json:"parent_event_id,omitempty"`
	ParentAlertID   string               `json:"parent_alert_id,omitempty"`
	MemberCount     int                  `json:"member_count"`
	WaitMemberCount int                  `json:"wait_member_count"`
	CaptureOffset   int                  `json:"capture_offset"`
	MemberOffset    int                  `json:"member_offset"`
	WindowFinished  bool                 `json:"window_finished"`
}

func decisionView(d merge.Decision) mergeDecisionView {
	p := d.Progress
	v := mergeDecisionView{ID: d.ID, Tenant: d.TenantID, WindowID: d.WindowID, GroupKey: d.GroupKey, Policy: domain.PolicyVersion{ID: d.Policy.ID, Version: d.Policy.Version, Digest: d.Policy.Compiled.Digest}, StartedAt: d.StartedAt, Deadline: d.Deadline, FrozenAt: d.FrozenAt, UpdatedAt: p.UpdatedAt, Outcome: d.Outcome, Phase: p.Phase, ReasonCode: p.ReasonCode, ParentAlertID: p.ParentAlertID, MemberCount: len(d.MemberIDs), WaitMemberCount: len(d.WaitMemberIDs), CaptureOffset: p.CaptureOffset, MemberOffset: p.MemberOffset, WindowFinished: p.WindowFinished}
	if p.ParentEvent != nil {
		v.ParentEventID = p.ParentEvent.EventID
	}
	return v
}

type mergeWindowView struct {
	ID             string                   `json:"id"`
	Tenant         string                   `json:"bk_tenant_id"`
	Policy         domain.PolicyVersion     `json:"policy"`
	GroupKey       string                   `json:"group_key"`
	StartedAt      time.Time                `json:"started_at"`
	Deadline       time.Time                `json:"deadline"`
	Cyclic         bool                     `json:"cyclic"`
	Revision       int64                    `json:"revision"`
	MemberCount    int                      `json:"member_count"`
	CommittedCount int                      `json:"committed_count"`
	GroupCounts    []int                    `json:"group_counts"`
	Frozen         *redisstate.MergeVerdict `json:"frozen,omitempty"`
	Members        []redisstate.MergeMember `json:"members,omitempty"`
}

func windowView(w redisstate.MergeWindow, detail bool) (mergeWindowView, error) {
	if w.GroupCount < 1 || w.GroupCount > 32 || len(w.Members) > 256 || w.Policy.Validate() != nil {
		return mergeWindowView{}, policy.ErrUnavailable
	}
	v := mergeWindowView{ID: w.ID, Tenant: w.TenantID, Policy: w.Policy, GroupKey: w.GroupKey, StartedAt: time.UnixMilli(w.StartedAtMillis).UTC(), Deadline: time.UnixMilli(w.DeadlineMillis).UTC(), Cyclic: w.Cyclic, Revision: w.Revision, MemberCount: len(w.Members), GroupCounts: make([]int, w.GroupCount), Frozen: w.Frozen}
	for _, m := range w.Members {
		if m.Committed {
			v.CommittedCount++
			for _, g := range m.Groups {
				if g < 0 || g >= len(v.GroupCounts) {
					return mergeWindowView{}, policy.ErrUnavailable
				}
				v.GroupCounts[g]++
			}
		}
	}
	if detail {
		v.Members = w.Members
	}
	return v, nil
}

func (a *API) mergeRuntimeAvailable(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if a.MergeRuntime == nil || a.MergeRuntime.journal == nil {
		policyFailure(w, policy.ErrUnavailable)
		return false
	}
	return true
}

func (a *API) acquireMergeQuery(w http.ResponseWriter) bool {
	select {
	case a.MergeRuntime.slots <- struct{}{}:
		return true
	default:
		policyFailure(w, policy.ErrPreviewCapacity)
		return false
	}
}

func (a *API) listMergeRuntime(w http.ResponseWriter, r *http.Request) {
	if !a.mergeRuntimeAvailable(w) {
		return
	}
	q, err := runtimeQuery(r, false)
	if err != nil {
		policyFailure(w, err)
		return
	}
	if !a.acquireMergeQuery(w) {
		return
	}
	defer func() { <-a.MergeRuntime.slots }()
	items := []any{}
	next := ""
	switch q.Resource {
	case "decisions":
		page, e := a.MergeRuntime.journal.ListDecisions(r.Context(), q.Tenant, q.After, q.Limit)
		err = e
		for _, d := range page.Items {
			if (q.PolicyID == "" || d.Policy.ID == q.PolicyID) && (q.Phase == "" || d.Progress.Phase == q.Phase) {
				items = append(items, decisionView(d))
			}
		}
		next = page.Next
	case "windows":
		if a.MergeRuntime.windows == nil {
			err = policy.ErrUnavailable
			break
		}
		page, e := a.MergeRuntime.windows.ListMergeWindows(r.Context(), q.Tenant, q.After, q.Limit)
		err = e
		if e != nil {
			break
		}
		if len(page.Items) > q.Limit {
			err = policy.ErrUnavailable
			break
		}
		for _, v := range page.Items {
			if v.TenantID != q.Tenant {
				err = policy.ErrAccess
				break
			}
			if q.PolicyID == "" || v.Policy.ID == q.PolicyID {
				view, e := windowView(v, false)
				if e != nil {
					err = e
					break
				}
				items = append(items, view)
			}
		}
		next = page.Next
	case "relations":
		page, e := a.MergeRuntime.journal.ListRelations(r.Context(), q.Tenant, q.AlertID, q.After, q.Limit)
		err = e
		for _, v := range page.Relations {
			if q.PolicyID == "" || v.Policy.ID == q.PolicyID {
				items = append(items, v)
			}
		}
		next = page.Next
	}
	if err != nil {
		policyFailure(w, err)
		return
	}
	writePolicyRuntime(w, map[string]any{"items": items, "next": q.cursor(next), "bk_tenant_id": q.Tenant})
}

func (a *API) getMergeRuntime(w http.ResponseWriter, r *http.Request) {
	if !a.mergeRuntimeAvailable(w) {
		return
	}
	q, err := runtimeQuery(r, true)
	if err != nil || q.After != "" || r.URL.Query().Has("limit") {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	if !a.acquireMergeQuery(w) {
		return
	}
	defer func() { <-a.MergeRuntime.slots }()
	var result any
	switch q.Resource {
	case "decisions":
		d, e := a.MergeRuntime.journal.Get(r.Context(), q.Tenant, q.ParentID)
		err = e
		result = decisionView(d.Decision)
	case "relations":
		d, e := a.MergeRuntime.journal.GetRelation(r.Context(), q.Tenant, q.ParentID)
		err = e
		result = d.Relation
	case "windows":
		if a.MergeRuntime.windows == nil {
			err = policy.ErrUnavailable
			break
		}
		d, found, e := a.MergeRuntime.windows.ReadMergeWindow(r.Context(), q.Tenant, q.ParentID)
		err = e
		if e == nil {
			if !found {
				err = policy.ErrNotFound
			} else if d.TenantID != q.Tenant || d.ID != q.ParentID {
				err = policy.ErrAccess
			} else {
				result, err = windowView(d, true)
			}
		}
	}
	if err != nil {
		policyFailure(w, err)
		return
	}
	writePolicyRuntime(w, result)
}

func (a *API) mergeRuntimeMembers(w http.ResponseWriter, r *http.Request) {
	if !a.mergeRuntimeAvailable(w) {
		return
	}
	q, err := runtimeQuery(r, true)
	if err != nil || q.Resource != "decisions" {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	if !a.acquireMergeQuery(w) {
		return
	}
	defer func() { <-a.MergeRuntime.slots }()
	// 查询固定快照；缺少快照表示捕获尚未完成，不退回实时 Alert 伪装为原始成员。
	if _, err := a.MergeRuntime.journal.Get(r.Context(), q.Tenant, q.ParentID); err != nil {
		policyFailure(w, err)
		return
	}
	rows, next, err := a.MergeRuntime.journal.ListMembers(r.Context(), q.Tenant, q.ParentID, q.After, q.Limit)
	if err != nil {
		policyFailure(w, err)
		return
	}
	items := []any{}
	for _, s := range rows {
		a := s.Alert
		items = append(items, map[string]any{"bk_tenant_id": a.BKTenantID, "decision_id": s.DecisionID, "alert_id": a.AlertID, "event_source_id": a.EventSourceID, "severity": a.Severity, "status": a.Status, "trigger_event_id": a.TriggerEventID, "enrich_status": a.EnrichStatus, "revision": a.Revision})
	}
	writePolicyRuntime(w, map[string]any{"bk_tenant_id": q.Tenant, "items": items, "next": q.cursor(next)})
}

func (a *API) mergeRuntimeSnapshot(w http.ResponseWriter, r *http.Request) {
	if !a.mergeRuntimeAvailable(w) {
		return
	}
	q, err := runtimeQuery(r, true)
	alert := r.PathValue("alert")
	if err != nil || q.Resource != "decisions" || q.After != "" || r.URL.Query().Has("limit") || alert == "" || len(alert) > domain.EntityIDMaxBytes {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	if !a.acquireMergeQuery(w) {
		return
	}
	defer func() { <-a.MergeRuntime.slots }()
	d, err := a.MergeRuntime.journal.Get(r.Context(), q.Tenant, q.ParentID)
	if err != nil {
		policyFailure(w, err)
		return
	}
	if !slices.Contains(d.Decision.MemberIDs, alert) {
		policyFailure(w, policy.ErrNotFound)
		return
	}
	snapshot, err := a.MergeRuntime.journal.Snapshot(r.Context(), q.Tenant, q.ParentID, alert)
	if err != nil {
		policyFailure(w, err)
		return
	}
	writePolicyRuntimeLimit(w, snapshot, 8<<20)
}
