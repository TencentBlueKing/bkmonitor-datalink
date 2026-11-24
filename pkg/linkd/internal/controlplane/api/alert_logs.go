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
	"log/slog"
	"net/http"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// AlertLogAppender 只追加不可变日志；逐项结果与输入位置对应，失败可能部分生效。
// 接口调用重试必须复用原日志身份、时间和完整事实。
type AlertLogAppender interface {
	AppendAlertLogs(context.Context, []domain.AlertLog) ([]store.AppendAlertLogItemResult, error)
}

type alertLogAppendError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type alertLogAppendResult struct {
	LogID   string               `json:"log_id"`
	Created bool                 `json:"created"`
	Error   *alertLogAppendError `json:"error"`
}

func alertLogError(err error) *alertLogAppendError {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrIdentityConflict) {
		return &alertLogAppendError{Code: "identity_conflict", Message: "log identity already has different facts"}
	}
	if errors.Is(err, store.ErrInvalidArgument) {
		return &alertLogAppendError{Code: "invalid_argument", Message: "invalid alert log"}
	}
	return &alertLogAppendError{Code: "unavailable", Message: "alert log append failed"}
}

func (a *API) logAlertLogFailure(ctx context.Context, log domain.AlertLog, err error) {
	logger := a.AlertLogLogger
	if logger == nil {
		logger = slog.Default()
	}
	logger.ErrorContext(ctx, "alert log append failed", "module", "controlplane", "operation", "alert_logs_append",
		"bk_tenant_id", log.BKTenantID, "alert_id", log.AlertID, "log_id", log.LogID,
		"error_code", alertLogError(err).Code, "error", err)
}

func (a *API) appendAlertLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.AlertLogAppender == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		output(w, map[string]any{"error": alertLogError(store.ErrNotFound)})
		return
	}
	// 1. 仅解析协议，条目数量、文档大小与业务身份交现有存储合同处理。
	var input struct {
		Logs []domain.AlertLog `json:"logs"`
	}
	// 沿用存储单次请求的 16 MiB 默认传输预算，避免通用 1 MiB 管理 JSON
	// 解码器把合法多条日志批次误当成单文档限制。
	if err := decodeAlertQuery(w, r, 16<<20, &input); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		output(w, map[string]any{"error": alertLogError(store.ErrInvalidArgument)})
		return
	}
	// 2. 等待真实追加结果；不能把接收请求当作落库成功。
	items, err := a.AlertLogAppender.AppendAlertLogs(r.Context(), input.Logs)
	if err != nil {
		a.logAlertLogFailure(r.Context(), domain.AlertLog{}, err)
		status := http.StatusServiceUnavailable
		if errors.Is(err, store.ErrInvalidArgument) {
			status = http.StatusBadRequest
		}
		w.WriteHeader(status)
		output(w, map[string]any{"error": alertLogError(err)})
		return
	}
	// 3. 按原顺序保留部分成功，调用方据原身份重试，不泄漏存储响应正文。
	results := make([]alertLogAppendResult, len(items))
	for i, item := range items {
		if item.Err != nil {
			a.logAlertLogFailure(r.Context(), input.Logs[i], item.Err)
		}
		results[i] = alertLogAppendResult{LogID: input.Logs[i].LogID, Created: item.Result.Created, Error: alertLogError(item.Err)}
	}
	output(w, struct {
		Results []alertLogAppendResult `json:"results"`
	}{results})
}
