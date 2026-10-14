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
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// InlineProjection 实时读取已保存的业务事实；调用方不能通过此入口上传或覆盖 Alert。
type InlineProjection interface {
	ProjectCurrent(context.Context, string, string) (store.StoredAlert, error)
}

func (a *API) projectAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if a.InlineProjection == nil {
		http.Error(w, "projection unavailable", http.StatusServiceUnavailable)
		return
	}
	var input struct {
		TenantID string `json:"bk_tenant_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	id := r.PathValue("id")
	if decode(r, &input) != nil || domain.ValidateIdentityPart("tenant", input.TenantID, 64) != nil || id == "" || len(id) > domain.EntityIDMaxBytes {
		http.Error(w, "invalid projection scope", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	row, err := a.InlineProjection.ProjectCurrent(ctx, input.TenantID, id)
	if row.Version.IsZero() {
		http.Error(w, "projection lookup failed", http.StatusBadGateway)
		return
	}
	// 202 明确表示未确认，不能让主流程把补偿排队解释为 KAC 已同步。
	if err != nil {
		w.WriteHeader(http.StatusAccepted)
	}
	output(w, struct {
		Alert    domain.Alert `json:"alert"`
		Version  string       `json:"version"`
		Deferred bool         `json:"deferred"`
	}{row.Alert, row.Version.String(), err != nil})
}
