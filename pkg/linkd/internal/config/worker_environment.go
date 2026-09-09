// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"encoding/json"
	"fmt"
)

// applyWorkerLabels 允许共享配置 Secret 的 worker 组独立声明调度标签。
// 显式提供的 JSON 对象替换整个 YAML worker.labels；空对象清空标签。
// 不回显错误输入，避免把意外注入的凭据写入启动日志。
func applyWorkerLabels(cfg *Config, lookupEnv func(string) (string, bool)) error {
	value, exists := lookupEnv("LINKD_WORKER_LABELS")
	if !exists {
		return nil
	}
	var labels map[string]string
	if len(value) > 16384 || json.Unmarshal([]byte(value), &labels) != nil || labels == nil {
		return fmt.Errorf("LINKD_WORKER_LABELS must be a JSON string map of at most 16384 bytes")
	}
	if err := ValidateLabels(labels); err != nil {
		return fmt.Errorf("LINKD_WORKER_LABELS contains invalid labels")
	}
	cfg.Worker.Labels = labels
	return nil
}
