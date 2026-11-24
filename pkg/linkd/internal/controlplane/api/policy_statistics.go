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
	"strconv"
	"strings"
	"time"

	"linkd/internal/policy"
)

// PolicyStatisticsReader 只能读取显式策略身份的有界时间桶。
type PolicyStatisticsReader interface {
	PolicyStatistics(context.Context, policy.StatisticsQuery, time.Time) (policy.StatisticsPage, error)
}

func (a *API) policyStatistics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.PolicyStatistics == nil {
		policyFailure(w, policy.ErrUnavailable)
		return
	}

	if err := policyQuery(r, "bk_tenant_id", "type", "ids", "hours"); err != nil {
		policyFailure(w, err)
		return
	}
	raw := r.URL.Query()
	hours, err := strconv.Atoi(raw.Get("hours"))
	if err != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	q := policy.StatisticsQuery{Scope: policy.Scope{TenantID: raw.Get("bk_tenant_id"), Kind: policy.Kind(raw.Get("type"))}, IDs: strings.Split(raw.Get("ids"), ","), Hours: hours}
	if err := q.Validate(); err != nil {
		policyFailure(w, err)
		return
	}
	result, err := a.PolicyStatistics.PolicyStatistics(r.Context(), q, time.Now())
	if err != nil {
		policyFailure(w, err)
		return
	}
	output(w, result)
}
