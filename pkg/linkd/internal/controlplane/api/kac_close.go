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
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	lifecycleprocess "linkd/internal/lifecycle/process"
)

// KACCloser 为 KAC 命令提供同步关闭及租户范围内的结果核对。
type KACCloser interface {
	CloseKACAlert(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error)
	GetAlert(context.Context, string, string) (domain.Alert, error)
}

type kacCloseInput struct {
	TenantID        string              `json:"bk_tenant_id"`
	OperationID     string              `json:"operation_id"`
	OperatorKind    domain.OperatorKind `json:"operator_kind"`
	OperatorID      string              `json:"operator_id"`
	OperationSource string              `json:"operation_source"`
	Reason          string              `json:"reason"`
	EffectiveAt     time.Time           `json:"effective_at"`
}

// KAC 只需核对关闭身份；无鉴权入口不返回 Alert 的正文、维度或扩展数据。
type kacAlertSnapshot struct {
	BKTenantID       string             `json:"bk_tenant_id"`
	AlertID          string             `json:"alert_id"`
	Status           domain.AlertStatus `json:"status"`
	CloseOperationID string             `json:"close_operation_id,omitempty"`
}

func kacSnapshot(alert domain.Alert) kacAlertSnapshot {
	snapshot := kacAlertSnapshot{
		BKTenantID: alert.BKTenantID, AlertID: alert.AlertID,
		Status: alert.Status,
	}
	if alert.EndOperation != nil {
		snapshot.CloseOperationID = alert.EndOperation.ID
	}
	return snapshot
}

func (a *API) closeKACAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.KACCloser == nil {
		kacFailure(w, &lifecycleprocess.CloseError{Status: 503, Code: "unavailable", Message: "close is not configured"})
		return
	}
	var input kacCloseInput
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := decode(r, &input); err != nil {
		kacFailure(w, &lifecycleprocess.CloseError{Status: 400, Code: "invalid_command", Message: "invalid close command"})
		return
	}
	result, err := a.KACCloser.CloseKACAlert(r.Context(), lifecycle.CloseAlertCommand{
		BKTenantID: input.TenantID, AlertID: r.PathValue("id"), OperationID: input.OperationID,
		OperatorKind: input.OperatorKind, OperatorID: input.OperatorID, OperationSource: input.OperationSource,
		Reason: input.Reason, EffectiveAt: input.EffectiveAt,
	})
	if err != nil {
		kacFailure(w, err)
		return
	}
	output(w, struct {
		Alert         kacAlertSnapshot `json:"alert"`
		AlreadyClosed bool             `json:"already_closed"`
	}{kacSnapshot(result.Alert), result.AlreadyClosed})
}

func (a *API) getKACAlert(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.KACCloser == nil {
		kacFailure(w, &lifecycleprocess.CloseError{Status: 503, Code: "unavailable", Message: "close is not configured"})
		return
	}
	alert, err := a.KACCloser.GetAlert(r.Context(), r.URL.Query().Get("bk_tenant_id"), r.PathValue("id"))
	if err != nil {
		kacFailure(w, err)
		return
	}
	output(w, struct {
		Alert kacAlertSnapshot `json:"alert"`
	}{kacSnapshot(alert)})
}

func kacFailure(w http.ResponseWriter, err error) {
	var classified *lifecycleprocess.CloseError
	if !errors.As(err, &classified) {
		classified = &lifecycleprocess.CloseError{Status: 502, Code: "result_uncertain", Message: "close result uncertain; retry the same operation"}
	}
	code := classified.Code
	if code == "" {
		code = "unknown"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(classified.Status)
	_ = json.NewEncoder(w).Encode(struct {
		ErrorCode string `json:"error_code"`
		Message   string `json:"message"`
	}{code, classified.Message})
}
