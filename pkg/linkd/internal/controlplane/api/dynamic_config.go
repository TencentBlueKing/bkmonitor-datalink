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
	"net/http"

	"linkd/internal/taskdispatch"
)

func (a *API) dynamicStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.DynamicConfig == nil {
		output(w, map[string]any{"config": map[string]any{"enabled": false, "origin": "yaml", "sync_state": "disabled"}, "workers": map[string]taskdispatch.Worker{}})
		return
	}
	state, err := a.Controller.Snapshot(r.Context())
	if err != nil {
		http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
		return
	}
	output(w, struct {
		Config  any                            `json:"config"`
		Workers map[string]taskdispatch.Worker `json:"workers"`
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
