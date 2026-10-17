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
	"net/http"

	"linkd/internal/onemodel"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
)

// PolicyDependencies 分发同部署共享的只读依赖，不接受连接或原生查询参数。
type PolicyDependencies interface {
	Read(context.Context, policyruntime.DependencyRequest) (policyruntime.DependencySnapshot, error)
}

func (a *API) workerDependencies(w http.ResponseWriter, r *http.Request) {
	if err := policyQuery(r, "task", "bk_tenant_id"); err != nil {
		policyFailure(w, err)
		return
	}
	tenant := r.URL.Query().Get("bk_tenant_id")
	if !a.authorizeWorkerPolicies(w, r, policy.Scope{TenantID: tenant, Kind: policy.Suppression}) {
		return
	}
	if a.PolicyDependencies == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}
	var request policyruntime.DependencyRequest
	if err := decodePolicy(r, &request); err != nil {
		policyFailure(w, err)
		return
	}
	if request.TenantID != tenant {
		policyFailure(w, policy.ErrAccess)
		return
	}
	switch request.Kind {
	case "scope", "targets":
		if _, err := onemodel.ParseBusinessSpace(request.SpaceCode); err != nil {
			policyFailure(w, policy.ErrInvalid)
			return
		}
		if request.Kind == "targets" && (request.Target == nil || request.Target.Validate() != nil) {
			policyFailure(w, policy.ErrInvalid)
			return
		}
	case "relations":
		d := onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: request.Origin.ModelID, Selectors: []onemodel.TargetSelector{{Type: "instances", Instances: []onemodel.InstanceRef{request.Origin}}}}
		if d.Validate() != nil || request.Relation == "" || request.ModelID == "" {
			policyFailure(w, policy.ErrInvalid)
			return
		}
	default:
		policyFailure(w, policy.ErrInvalid)
		return
	}
	value, err := a.PolicyDependencies.Read(r.Context(), request)
	if err != nil {
		policyFailure(w, err)
		return
	}
	output(w, value)
}
