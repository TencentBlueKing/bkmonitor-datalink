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
	"time"

	"linkd/internal/eventsource"
	"linkd/internal/pluginruntime"
)

// InspectionInput 指定一次不发布事件的调试或辅助查询；不接受插件名、凭据或来源身份覆盖。
type InspectionInput struct {
	RequestID  string          `json:"request_id"`
	TenantID   string          `json:"bk_tenant_id,omitempty"`
	Mode       string          `json:"mode"`
	ReceivedAt string          `json:"received_at,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	// State 非 nil 时用于调试指定游标；省略时读取已提交进度，再回退来源初始状态。
	State map[string]any `json:"state,omitempty"`
}

// InspectionSources 读取当前管理草稿；预览不会发布草稿或修改来源启停。
type InspectionSources interface {
	Get(context.Context, string) (eventsource.Record, error)
}

// Inspector 提供管理鉴权之后的同步插件预览，没有发布器和进度写入能力。
// 请求仍可能访问厂商及宿主查询服务；“预览”不代表任意插件代码没有外部副作用。
type Inspector struct {
	sources      InspectionSources
	executor     Executor
	progress     ProgressReader
	logger       *slog.Logger
	slots        chan struct{}
	maxBodyBytes int64
}

// NewInspector 为管理预览配置独立的请求并发和字节预算，满载立即拒绝。
func NewInspector(sources InspectionSources, executor Executor, progress ProgressReader, logger *slog.Logger, concurrency int, maxBodyBytes int64) (*Inspector, error) {
	if sources == nil || executor == nil || logger == nil || concurrency <= 0 || maxBodyBytes <= 0 {
		return nil, errors.New("inspection dependencies and positive budgets required")
	}
	return &Inspector{sources: sources, executor: executor, progress: progress, logger: logger, slots: make(chan struct{}, concurrency), maxBodyBytes: maxBodyBytes}, nil
}

// ServeHTTP 应由管理 JWT 路由调用，source_id 来自路径。字段/指标返回 data，其他模式返回候选事件/游标。
func (i *Inspector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	select {
	case i.slots <- struct{}{}:
		defer func() { <-i.slots }()
	default:
		http.Error(w, "plugin preview busy", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, i.maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input InspectionInput
	if err := decoder.Decode(&input); err != nil {
		inspectionDecodeError(w, err)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		inspectionDecodeError(w, err)
		return
	}
	if input.RequestID == "" {
		http.Error(w, "request_id required", http.StatusBadRequest)
		return
	}
	switch input.Mode {
	case "push", "pull", "recovery":
		if _, err := time.Parse(time.RFC3339Nano, input.ReceivedAt); err != nil {
			http.Error(w, "received_at must be RFC3339 for event preview", http.StatusBadRequest)
			return
		}
	case "fields", "metric":
	default:
		http.Error(w, "unsupported preview mode", http.StatusBadRequest)
		return
	}
	if (input.Mode == "push" || input.Mode == "metric") && len(input.Data) == 0 {
		http.Error(w, "data required", http.StatusBadRequest)
		return
	}
	sourceID := r.PathValue("source_id")
	record, err := i.sources.Get(r.Context(), sourceID)
	if err != nil {
		if errors.Is(err, eventsource.ErrNotFound) {
			http.Error(w, "source unavailable", http.StatusNotFound)
		} else {
			i.fail(w, r, sourceID, input, err)
		}
		return
	}
	if record.Deleted || record.Spec.Collection == nil {
		http.Error(w, "source has no plugin configuration", http.StatusNotFound)
		return
	}
	// 管理预览允许停用或尚未发布的草稿；租户与参数仍由来源配置确定。
	tenant := input.TenantID
	if tenant == "" {
		tenant = record.Spec.RelatedTenantID
	}
	var request pluginruntime.Request
	for _, binding := range collectionBindings(record.Spec, record.Revision, nil) {
		if tenant != "" && binding.Request.TenantID == tenant {
			request = binding.Request
			break
		}
	}
	if request.TenantID == "" {
		http.Error(w, "explicit source tenant binding required", http.StatusBadRequest)
		return
	}
	input.TenantID = request.TenantID
	request.Version, request.RequestID, request.Mode, request.ReceivedAt, request.Data = 1, input.RequestID, input.Mode, input.ReceivedAt, input.Data
	if input.State != nil {
		request.State = input.State
	} else if i.progress != nil && (input.Mode == "pull" || input.Mode == "recovery") {
		state, err := i.progress.Load(r.Context(), request.TenantID, request.SourceID)
		if err != nil {
			i.fail(w, r, sourceID, input, err)
			return
		}
		if state != nil {
			request.State = state
		}
	}
	// 直接调用执行器一次；不经过 Service.Run，不能生成接收记录、自动重试、发布或推进游标。
	result, err := i.executor.Execute(r.Context(), request)
	if err != nil {
		i.fail(w, r, sourceID, input, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func inspectionDecodeError(w http.ResponseWriter, err error) {
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		http.Error(w, "preview request too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid plugin preview request", http.StatusBadRequest)
}

func (i *Inspector) fail(w http.ResponseWriter, r *http.Request, source string, input InspectionInput, err error) {
	i.logger.ErrorContext(r.Context(), "plugin preview failed", "module", "collector", "operation", "plugin_preview", "error", err, "event_source_id", source, "bk_tenant_id", input.TenantID, "request_id", input.RequestID)
	var failure *pluginruntime.Failure
	if errors.As(err, &failure) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": failure})
		return
	}
	var runtimeError *pluginruntime.HTTPError
	if errors.As(err, &runtimeError) && runtimeError.StatusCode == http.StatusServiceUnavailable {
		http.Error(w, "plugin runtime busy", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || (runtimeError != nil && runtimeError.StatusCode == http.StatusGatewayTimeout) {
		http.Error(w, "plugin preview timed out", http.StatusGatewayTimeout)
		return
	}
	http.Error(w, "plugin preview unavailable", http.StatusBadGateway)
}
