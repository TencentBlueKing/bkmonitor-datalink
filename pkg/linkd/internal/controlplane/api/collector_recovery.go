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

	"linkd/internal/pluginruntime"
)

// SourceExecutionRequests 只按授权租户、来源和请求身份读取首次执行输入。
type SourceExecutionRequests interface {
	LoadRequest(context.Context, string, string, string) (pluginruntime.Request, error)
}

func (a *API) collectorReplayRelease(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.SourceRequests == nil || a.Controller == nil || a.Sources == nil {
		http.Error(w, "collector recovery unavailable", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("request_id")
	if id == "" {
		http.Error(w, "request_id required", http.StatusBadRequest)
		return
	}
	state, err := a.Controller.Snapshot(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	task, ok := state.Tasks[r.URL.Query().Get("task")]
	worker := r.Header.Get("X-Worker-ID")
	// Agent 在 starting 授权阶段启动 runner，首次恢复不能等待下一次心跳才获准读取。
	if !ok || worker == "" || task.Worker != worker || task.Role != "collector" || (task.Phase != "running" && task.Phase != "starting") || !task.Expires.After(time.Now()) {
		http.Error(w, "active collector assignment required", http.StatusForbidden)
		return
	}
	assigned, err := a.Sources.GetRelease(r.Context(), task.Source, task.Version)
	if err != nil {
		failure(w, err)
		return
	}
	tenant := assigned.Spec.RelatedTenantID
	if tenant == "" {
		http.Error(w, "explicit tenant assignment required", http.StatusForbidden)
		return
	}
	request, err := a.SourceRequests.LoadRequest(r.Context(), tenant, task.Source, id)
	if err != nil {
		failure(w, err)
		return
	}
	// 历史配置访问必须由持久请求证明租户/来源归属；不使用 URL 提供的 tenant 或 version。
	if request.TenantID != tenant || request.SourceID != task.Source {
		http.Error(w, "request scope mismatch", http.StatusForbidden)
		return
	}
	release, err := a.Sources.GetRelease(r.Context(), task.Source, request.SourceVersion)
	if err != nil {
		failure(w, err)
		return
	}
	if release.Spec.RelatedTenantID != tenant {
		http.Error(w, "release tenant mismatch", http.StatusForbidden)
		return
	}
	output(w, releaseForRole(release, task.Role))
}
