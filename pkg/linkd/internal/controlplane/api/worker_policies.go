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
	"net/http"
	"strconv"

	"linkd/internal/policy"
)

// authorizeWorkerPolicies 只允许当前来源任务的 Lifecycle Worker 读取策略。
// 绑定租户来源只能读取该租户；显式多租户来源也必须为每个请求提供独立租户作用域。
func (a *API) authorizeWorkerPolicies(w http.ResponseWriter, r *http.Request, scope policy.Scope) bool {
	if err := scope.Validate(); err != nil {
		policyFailure(w, err)
		return false
	}
	if a.Controller == nil || a.Sources == nil {
		http.Error(w, "assignment unavailable", http.StatusServiceUnavailable)
		return false
	}
	state, err := a.Controller.Snapshot(r.Context())
	if err != nil {
		http.Error(w, "assignment unavailable", http.StatusServiceUnavailable)
		return false
	}
	task, found := state.Tasks[r.URL.Query().Get("task")]
	worker := r.Header.Get("X-Worker-ID")
	if !found || worker == "" || task.Worker != worker || task.Role != "lifecycle" || task.Phase == "stopped" || task.Source == "" || task.Version < 1 {
		http.Error(w, "assignment required", http.StatusForbidden)
		return false
	}
	release, err := a.Sources.GetRelease(r.Context(), task.Source, task.Version)
	if err != nil {
		http.Error(w, "assignment unavailable", http.StatusServiceUnavailable)
		return false
	}
	if release.ID != task.Source || release.Version != task.Version || (release.Spec.RelatedTenantID != "" && release.Spec.RelatedTenantID != scope.TenantID) {
		http.Error(w, "source tenant mismatch", http.StatusForbidden)
		return false
	}
	return true
}

func (a *API) workerPolicies(w http.ResponseWriter, r *http.Request) {
	if !a.policyAvailable(w) {
		return
	}
	if err := policyQuery(r, "task", "bk_tenant_id", "type", "after", "limit"); err != nil {
		policyFailure(w, err)
		return
	}
	q := r.URL.Query()
	scope := policy.Scope{TenantID: q.Get("bk_tenant_id"), Kind: policy.Kind(q.Get("type"))}
	if !a.authorizeWorkerPolicies(w, r, scope) {
		return
	}
	limit := 3
	if q.Get("limit") != "" {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 3 {
			policyFailure(w, policy.ErrInvalid)
			return
		}
		limit = n
	}
	rows, err := a.Policies.List(r.Context(), scope, q.Get("after"), limit)
	if err != nil {
		policyFailure(w, err)
		return
	}
	for i := range rows {
		rows[i].Pending = nil
	} // Worker 只能消费已经发布的 Spec；待发布配置仍由管理 API 展示。
	output(w, rows)
}

func (a *API) workerPolicyRelease(w http.ResponseWriter, r *http.Request) {
	if !a.policyAvailable(w) {
		return
	}
	if err := policyQuery(r, "task", "bk_tenant_id"); err != nil {
		policyFailure(w, err)
		return
	}
	scope := policyScope(r)
	if !a.authorizeWorkerPolicies(w, r, scope) {
		return
	}
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	release, err := a.Policies.GetRelease(r.Context(), scope, r.PathValue("id"), version)
	if err != nil {
		policyFailure(w, err)
		return
	}
	output(w, release)
}
