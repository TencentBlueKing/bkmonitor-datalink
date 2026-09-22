// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"context"
	"net/http"
	"time"

	"linkd/internal/config"
	"linkd/internal/runtimeconfig"
)

func (a *API) dynamicStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.DynamicConfig == nil {
		output(w, map[string]any{"config": map[string]any{"enabled": false, "origin": "yaml", "sync_state": "disabled"}, "workers": map[string]Worker{}})
		return
	}
	state, err := a.Controller.Snapshot(r.Context())
	if err != nil {
		http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
		return
	}
	output(w, struct {
		Config  any               `json:"config"`
		Workers map[string]Worker `json:"workers"`
	}{a.DynamicConfig.Status(), state.Workers})
}

func (a *API) workerSeverity(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.DynamicConfig == nil {
		http.Error(w, "dynamic config disabled", http.StatusNotFound)
		return
	}
	output(w, a.DynamicConfig.Status().Current)
}

// SeverityState 返回当前进程等级状态；all-in-one 两角色共享一个原子快照。
func SeverityState(ctx context.Context, c config.SeverityConfig) *runtimeconfig.Severity {
	if h, _ := ctx.Value(hostKey{}).(*Host); h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.severity == nil {
			h.severity = runtimeconfig.NewSeverity(c)
		}
		return h.severity
	}
	return runtimeconfig.NewSeverity(c)
}

// syncSeverity 不把单次配置请求失败计为授权失败；已安装的有效值可继续处理。
func (a *Agent) syncSeverity(ctx context.Context, client Client, digest string) (bool, string) {
	if a.Severity == nil || digest == "" {
		return true, ""
	}
	if digest == "disabled" {
		if a.Severity.SeveritySnapshot().Enabled && len(a.staticSeverity.Severity.Levels) > 0 {
			if err := a.Severity.Install(a.staticSeverity); err != nil {
				return false, "invalid_static_config"
			}
		}
		return true, ""
	}
	current := a.Severity.SeveritySnapshot()
	if current.Enabled && current.Digest == digest {
		return true, ""
	}
	call, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var snapshot runtimeconfig.Snapshot
	if err := client.Call(call, http.MethodGet, "/internal/settings/severity", nil, &snapshot); err != nil {
		return current.Enabled, "fetch_failed"
	}
	if !snapshot.Enabled {
		return current.Enabled, "invalid_snapshot"
	}
	if err := a.Severity.Install(snapshot); err != nil {
		return current.Enabled, "invalid_snapshot"
	}
	return true, ""
}
