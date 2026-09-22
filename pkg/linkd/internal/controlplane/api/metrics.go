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
	"encoding/json"
	"net/http"

	"linkd/internal/telemetry"
)

func metricCatalogHandler() http.Handler {
	// 每个控制面 Handler 构造一次只读快照；请求不采集样本，也不依赖业务存储或 exporter 开关。
	catalog, err := telemetry.MetricCatalog()
	var body []byte
	if err == nil {
		body, err = json.Marshal(catalog)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err != nil {
			http.Error(w, "metric catalog unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	})
}
