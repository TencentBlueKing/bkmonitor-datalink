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
	"fmt"
	"maps"
	"net/url"
)

// DispatchConfig 定义静态中心连接和控制协议预算；业务配置不从启动 YAML 导入。
type DispatchConfig struct {
	Deployment  string `yaml:"deployment" json:"deployment"`
	Listen      string `yaml:"listen" json:"listen"`
	URL         string `yaml:"url" json:"url"`
	APIToken    string `yaml:"api_token" json:"-"`
	WorkerToken string `yaml:"worker_token" json:"-"`
	MaxTasks    int    `yaml:"max_tasks" json:"max_tasks"`
}

// WithDefaults 补齐单中心地址和进程任务上限。
func (d DispatchConfig) WithDefaults() DispatchConfig {
	if d.Deployment == "" {
		d.Deployment = "default"
	}
	if d.Listen == "" {
		d.Listen = "127.0.0.1:8090"
	}
	if d.URL == "" {
		d.URL = "http://127.0.0.1:8090"
	}
	if d.MaxTasks == 0 {
		d.MaxTasks = 16
	}
	return d
}

// Validate 校验启动控制连接，正式运行另外要求分别配置认证 token。
func (d DispatchConfig) Validate() error {
	d = d.WithDefaults()
	u, e := url.Parse(d.URL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid dispatch URL")
	}
	if d.MaxTasks < 1 || d.MaxTasks > 256 {
		return fmt.Errorf("dispatch.max_tasks must be 1..256")
	}
	return ValidateLabels(map[string]string{d.Deployment: ""})
}

// WorkerConfig 的标签属于进程；all-in-one 两角色共享它们。
type WorkerConfig struct {
	// MaxConcurrency 限制进程所有任务申报的总 worker 数量，零值默认 128。
	MaxConcurrency int `yaml:"max_concurrency" json:"max_concurrency"`
	// MaxInflightBytes 限制所有任务的 inflight 预算之和，零值默认 256 MiB。
	MaxInflightBytes        int64             `yaml:"max_inflight_bytes" json:"max_inflight_bytes"`
	Labels                  map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	RequireExplicitSelector bool              `yaml:"require_explicit_selector" json:"require_explicit_selector"`
}

// Clone 隔离可变标签。
func (w WorkerConfig) Clone() WorkerConfig { w.Labels = maps.Clone(w.Labels); return w }

// Limits 补齐进程资源硬上限；此配置不属于来源动态配置。
func (w WorkerConfig) Limits() (int, int64) {
	workers, bytes := w.MaxConcurrency, w.MaxInflightBytes
	if workers == 0 {
		workers = 128
	}
	if bytes == 0 {
		bytes = 256 << 20
	}
	return workers, bytes
}
