// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package pluginruntime 同步调用独立 Python 执行器，计算成功不表示已发布到 Cleaner。
package pluginruntime

import (
	"encoding/json"
	"fmt"
)

// Request 是一次固定身份的插件调用；重试必须沿用 RequestID。
type Request struct {
	// PluginSourceID 是迁入插件原身份，不替代 SourceID 的租户/来源作用域。
	PluginSourceID          string `json:"plugin_source_id,omitempty"`
	RecoveryIntervalMinutes int    `json:"recovery_interval_minutes,omitempty"`
	// SourceVersion 固定本次接收使用的来源发布，重放不得重新选择最新版本。
	SourceVersion   int64           `json:"source_version,omitempty"`
	IntervalMinutes int             `json:"interval_minutes,omitempty"`
	Version         int             `json:"version"`
	RequestID       string          `json:"request_id"`
	TenantID        string          `json:"bk_tenant_id"`
	SourceID        string          `json:"source_id"`
	SourceName      string          `json:"source_name,omitempty"`
	Plugin          string          `json:"plugin"`
	Mode            string          `json:"mode"`
	Configs         map[string]any  `json:"configs,omitempty"`
	Timezone        string          `json:"timezone,omitempty"`
	ReceivedAt      string          `json:"received_at,omitempty"`
	State           map[string]any  `json:"state,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
}

// Failure 是 Python 返回的脱敏错误分类，不包含插件异常原文。
type Failure struct {
	Code string `json:"code"`
	Type string `json:"type"`
}

// Error 实现 error 接口，保留插件失败分类。
func (f *Failure) Error() string { return fmt.Sprintf("plugin execution: %s (%s)", f.Code, f.Type) }

// Result 是计算输出；调用方负责可靠发布、进度保存及最终清理。
type Result struct {
	// Data 承载指标或自定义字段查询结果，不进入告警发布流程。
	Data          json.RawMessage   `json:"data,omitempty"`
	Version       int               `json:"version"`
	RequestID     string            `json:"request_id"`
	TenantID      string            `json:"bk_tenant_id"`
	SourceID      string            `json:"source_id"`
	Events        []json.RawMessage `json:"events"`
	Failure       *Failure          `json:"error,omitempty"`
	ProposedState map[string]any    `json:"proposed_state,omitempty"`
}
