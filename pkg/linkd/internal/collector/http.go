// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"linkd/internal/pluginruntime"
)

// ErrSourceUnauthorized 表示来源密钥不匹配或来源不可接入，不向外泄露来源存在性。
var ErrSourceUnauthorized = errors.New("source is unavailable or unauthorized")

// Intake 在读取正文前鉴权，在执行前保存稳定接收身份与时间。
// Prepare 返回的请求必须绑定已鉴权来源；重投不能重新生成身份或接收时间。
// 实现负责配置版本和来源启停，不允许外部 payload 覆盖租户与插件参数。
type Intake interface {
	Authorize(context.Context, string, string) error
	Prepare(context.Context, string, string, json.RawMessage) (pluginruntime.Request, *Service, error)
}

// Receiver 接收原始 JSON，同步完成插件执行与发布后返回 KAC 风格结果。
type Receiver struct {
	intake       Intake
	maxBodyBytes int64
	logger       *slog.Logger
	slots        chan struct{}
}

// NewReceiver 注入来源鉴权/接收存储和请求体预算；应挂载到带 source_id 路径参数的 POST 路由。
func NewReceiver(intake Intake, maxBodyBytes int64, maxConcurrent int, logger *slog.Logger) (*Receiver, error) {
	if intake == nil || maxBodyBytes <= 0 || maxConcurrent <= 0 || logger == nil {
		return nil, errors.New("intake, logger and positive body/concurrency budgets are required")
	}
	return &Receiver{intake: intake, maxBodyBytes: maxBodyBytes, logger: logger, slots: make(chan struct{}, maxConcurrent)}, nil
}

// ServeHTTP 不将“收到请求”当作成功，只有 Service 完成才返回 received_alarm_count。
func (h *Receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		h.respond(w, 405, false, 1400, "method not allowed", 0)
		return
	}
	source := r.PathValue("source_id")
	secret := r.Header.Get("X-Secret")
	if source == "" || secret == "" {
		h.respond(w, 401, false, 1400, "source authentication failed", 0)
		return
	}
	// 在配置查询和正文读取前取得槽位，覆盖接收、计算和投递全过程；满载不排队。
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		h.respond(w, http.StatusServiceUnavailable, false, 1500, "source intake busy", 0)
		return
	}
	if err := h.intake.Authorize(r.Context(), source, secret); err != nil {
		if errors.Is(err, ErrSourceUnauthorized) {
			h.respond(w, 401, false, 1400, "source authentication failed", 0)
		} else {
			h.logger.ErrorContext(r.Context(), "source lookup failed", "module", "collector", "operation", "source_lookup", "error", err, "source_id", source)
			h.respond(w, 503, false, 1500, "source lookup unavailable", 0)
		}
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.respond(w, 413, false, 1400, "request too large", 0)
		} else {
			h.respond(w, 400, false, 1400, "invalid request body", 0)
		}
		return
	}
	if !json.Valid(body) {
		h.respond(w, 400, false, 1400, "invalid JSON", 0)
		return
	}
	request, service, err := h.intake.Prepare(r.Context(), source, secret, body)
	if err != nil {
		if errors.Is(err, ErrSourceUnauthorized) {
			h.respond(w, 401, false, 1400, "source authentication failed", 0)
			return
		}
		h.logger.ErrorContext(r.Context(), "source intake failed", "module", "collector", "operation", "source_intake", "error", err, "source_id", source, "bk_tenant_id", request.TenantID, "event_source_id", request.SourceID, "event_source_version", request.SourceVersion, "request_id", request.RequestID)
		h.respond(w, 503, false, 1500, "source intake failed", 0)
		return
	}
	result, err := service.Run(r.Context(), request)
	if err != nil {
		if errors.Is(err, ErrExecutionBusy) {
			h.respond(w, 503, false, 1500, "source request is already executing", 0)
			return
		}
		h.logger.ErrorContext(r.Context(), "source processing failed", "module", "collector", "operation", "source_process", "error", err, "bk_tenant_id", request.TenantID, "event_source_id", request.SourceID, "event_source_version", request.SourceVersion, "request_id", request.RequestID)
		var runtimeError *pluginruntime.HTTPError
		if errors.As(err, &runtimeError) && runtimeError.StatusCode == http.StatusServiceUnavailable {
			h.respond(w, http.StatusServiceUnavailable, false, 1500, "source runtime busy", 0)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || (runtimeError != nil && runtimeError.StatusCode == http.StatusGatewayTimeout) {
			h.respond(w, http.StatusGatewayTimeout, false, 1500, "source processing timed out", 0)
			return
		}
		h.respond(w, 502, false, 1500, "source execution or publication failed", 0)
		return
	}
	h.respond(w, 200, true, 1200, "push api event success", len(result.Events))
}

func (h *Receiver) respond(w http.ResponseWriter, status int, success bool, code int, message string, count int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// 只序列化本地固定类型；客户端断开后的写错误无需重试，避免重复发送响应。
	_ = json.NewEncoder(w).Encode(struct {
		Result  bool           `json:"result"`
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]int `json:"data"`
	}{success, code, message, map[string]int{"received_alarm_count": count}})
}
