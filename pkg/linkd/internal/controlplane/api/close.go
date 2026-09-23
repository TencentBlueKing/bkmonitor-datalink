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
	"errors"
	"net/http"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	lifecycleprocess "linkd/internal/lifecycle/process"
)

// AlertCloser 仅接受显式关闭命令；领域状态变更不在 HTTP 层实现。
type AlertCloser interface {
	CloseAlert(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error)
}

func (a *API) closeAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.AlertCloser == nil {
		http.Error(w, "close is not configured", http.StatusServiceUnavailable)
		return
	}
	var input struct {
		TenantID    string    `json:"bk_tenant_id"`
		OperationID string    `json:"operation_id"`
		OperatorID  string    `json:"operator_id"`
		Reason      string    `json:"reason"`
		EffectiveAt time.Time `json:"effective_at"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := decode(r, &input); err != nil {
		http.Error(w, "invalid close command", http.StatusBadRequest)
		return
	}
	result, err := a.AlertCloser.CloseAlert(r.Context(), lifecycle.CloseAlertCommand{BKTenantID: input.TenantID, AlertID: r.PathValue("id"), OperationID: input.OperationID, OperatorID: input.OperatorID, OperatorKind: domain.OperatorKindUser, Reason: input.Reason, EffectiveAt: input.EffectiveAt})
	if err != nil {
		var classified *lifecycleprocess.CloseError
		if errors.As(err, &classified) {
			http.Error(w, classified.Message, classified.Status)
			return
		}
		http.Error(w, "close result uncertain; retry the same operation", http.StatusBadGateway)
		return
	}
	output(w, struct {
		Alert         domain.Alert `json:"alert"`
		AlreadyClosed bool         `json:"already_closed"`
	}{result.Alert, result.AlreadyClosed})
}
