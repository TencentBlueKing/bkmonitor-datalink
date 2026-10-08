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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

func (a *API) policyAvailable(w http.ResponseWriter) bool {
	w.Header().Set("Cache-Control", "no-store")
	if a.Policies == nil {
		http.Error(w, "policy service unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func policyFailure(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, policy.ErrAccess):
		code = http.StatusForbidden
	case errors.Is(err, policy.ErrPreviewCapacity):
		code = http.StatusTooManyRequests
	case errors.Is(err, policy.ErrUnavailable):
		code = http.StatusServiceUnavailable
	case errors.Is(err, policy.ErrInvalid):
		code = http.StatusBadRequest
	case errors.Is(err, policy.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, policy.ErrConflict):
		code = http.StatusConflict
	case errors.Is(err, context.DeadlineExceeded):
		code = http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		code = http.StatusRequestTimeout
	}
	// 原始驱动错误和配置内容可能含敏感数据，诊断应通过日志与发布状态查询。
	http.Error(w, http.StatusText(code), code)
}

func policyQuery(r *http.Request, keys ...string) error {
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			return fmt.Errorf("%w: invalid query", policy.ErrInvalid)
		}
	}
	return nil
}

func policyScope(r *http.Request) policy.Scope {
	return policy.Scope{TenantID: r.URL.Query().Get("bk_tenant_id"), Kind: policy.Kind(r.PathValue("type"))}
}

func decodePolicy(r *http.Request, v any) error {
	const maxRequest = 3 << 20
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequest+1))
	if err != nil {
		return err
	}
	if len(raw) > maxRequest {
		return policy.ErrInvalid
	}
	normalized, err := (domain.JSONObject{"request": raw}).Normalize()
	if err != nil {
		return policy.ErrInvalid
	}
	// 重复键在 Normalize 中拒绝，字段与类型错误由严格 decoder 拒绝。
	decoder := json.NewDecoder(bytes.NewReader(normalized["request"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return policy.ErrInvalid
	}
	return nil
}

func (a *API) listPolicies(w http.ResponseWriter, r *http.Request) {
	if !a.policyAvailable(w) {
		return
	}
	if err := policyQuery(r, "bk_tenant_id", "type", "after", "limit", "is_enable"); err != nil {
		policyFailure(w, err)
		return
	}
	query := r.URL.Query()
	scope := policy.Scope{TenantID: query.Get("bk_tenant_id"), Kind: policy.Kind(query.Get("type"))}
	limit := policy.MaxPageSize
	var err error
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			policyFailure(w, policy.ErrInvalid)
			return
		}
	}
	enabled := query.Get("is_enable")
	if enabled != "" && enabled != "true" && enabled != "false" {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	rows, err := a.Policies.List(r.Context(), scope, query.Get("after"), limit)
	if err != nil {
		policyFailure(w, err)
		return
	}
	next := ""
	if len(rows) == limit {
		next = rows[len(rows)-1].ID
	}
	items := make([]policy.Record, 0, len(rows))
	for _, record := range rows {
		if enabled != "" {
			var common struct {
				Enabled bool `json:"is_enable"`
			}
			if len(record.Spec) > 0 {
				if err := json.Unmarshal(record.Spec, &common); err != nil {
					policyFailure(w, err)
					return
				}
			}
			if (common.Enabled && !record.Deleted) != (enabled == "true") {
				continue
			}
		}
		items = append(items, record)
	}
	output(w, struct {
		Items []policy.Record `json:"items"`
		Next  string          `json:"next"`
	}{items, next})
}

func (a *API) getPolicy(w http.ResponseWriter, r *http.Request) {
	if !a.policyAvailable(w) {
		return
	}
	if err := policyQuery(r, "bk_tenant_id"); err != nil {
		policyFailure(w, err)
		return
	}
	record, err := a.Policies.Get(r.Context(), policyScope(r), r.PathValue("id"))
	if err != nil {
		policyFailure(w, err)
		return
	}
	output(w, record)
}

func (a *API) putPolicy(w http.ResponseWriter, r *http.Request) {
	if !a.policyAvailable(w) {
		return
	}
	if err := policyQuery(r); err != nil {
		policyFailure(w, err)
		return
	}
	var request policy.ApplyRequest
	if err := decodePolicy(r, &request); err != nil {
		policyFailure(w, err)
		return
	}
	if request.ID != r.PathValue("id") || string(request.Kind) != r.PathValue("type") || request.Deleted {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	release, err := a.Policies.Apply(r.Context(), request)
	if err != nil {
		policyFailure(w, err)
		return
	}
	output(w, release)
}

func (a *API) deletePolicy(w http.ResponseWriter, r *http.Request) {
	if !a.policyAvailable(w) {
		return
	}
	if err := policyQuery(r); err != nil {
		policyFailure(w, err)
		return
	}
	var request struct {
		TenantID      string `json:"bk_tenant_id"`
		SchemaVersion int    `json:"schema_version"`
		Expected      int64  `json:"expected_version"`
		OperationID   string `json:"operation_id"`
	}
	if err := decodePolicy(r, &request); err != nil {
		policyFailure(w, err)
		return
	}
	if request.SchemaVersion != 1 {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	release, err := a.Policies.Delete(r.Context(), policy.Scope{TenantID: request.TenantID, Kind: policy.Kind(r.PathValue("type"))}, r.PathValue("id"), request.Expected, request.OperationID)
	if err != nil {
		policyFailure(w, err)
		return
	}
	output(w, release)
}

func (a *API) policyRelease(w http.ResponseWriter, r *http.Request) {
	if !a.policyAvailable(w) {
		return
	}
	if err := policyQuery(r, "bk_tenant_id"); err != nil {
		policyFailure(w, err)
		return
	}
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil {
		policyFailure(w, policy.ErrInvalid)
		return
	}
	release, err := a.Policies.GetRelease(r.Context(), policyScope(r), r.PathValue("id"), version)
	if err != nil {
		policyFailure(w, err)
		return
	}
	output(w, release)
}

func (a *API) previewPolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.PolicyPreviewer == nil {
		http.Error(w, "policy preview unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := policyQuery(r); err != nil {
		policyFailure(w, err)
		return
	}
	var request policy.PreviewRequest
	if err := decodePolicy(r, &request); err != nil {
		policyFailure(w, err)
		return
	}
	response, err := a.PolicyPreviewer.Preview(r.Context(), request)
	if err != nil {
		policyFailure(w, err)
		return
	}
	output(w, response)
}
