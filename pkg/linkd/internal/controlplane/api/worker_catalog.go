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

	"linkd/internal/policy"
)

func (a *API) workerPolicyCatalog(w http.ResponseWriter, r *http.Request) {
	if err := policyQuery(r, "task", "bk_tenant_id"); err != nil {
		policyFailure(w, err)
		return
	}
	tenant := r.URL.Query().Get("bk_tenant_id")
	if !a.authorizeWorkerPolicies(w, r, policy.Scope{TenantID: tenant, Kind: policy.Suppression}) {
		return
	}
	if a.PolicyCatalog == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	rows, err := a.PolicyCatalog.Freeze(r.Context(), tenant)
	if err != nil {
		policyFailure(w, err)
		return
	}
	if a.PolicyPreparation != nil {
		if err := a.PolicyPreparation.Schedule(r.Context(), tenant); err != nil {
			policyFailure(w, err)
			return
		}
	}
	releases := make([]policy.Release, 0, len(rows))
	for _, row := range rows {
		releases = append(releases, row.Release)
	}
	output(w, releases)
}

func (a *API) policyPublished(tenant string) {
	if a.PolicyCatalog != nil {
		a.PolicyCatalog.Invalidate(tenant)
	}
	if a.PolicyPreparation != nil {
		a.PolicyPreparation.Invalidate(tenant)
	}
}
