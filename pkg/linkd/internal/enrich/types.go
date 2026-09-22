// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import "linkd/internal/domain"

// Input 是丰富执行可读取但不得修改的 Alert 副本，由正式处理和预览复用。
type Input struct {
	Alert domain.Alert
	// Preview 只放宽未持久化调试输入的生命周期元数据校验，不放宽字段写入限制。
	Preview bool
}

// Result 只允许设置 Alert 的 enrich_status 与 enrich。
type Result struct {
	Status domain.EnrichStatus
	Data   domain.JSONObject
}

// ChainKind 描述 EventSource 实际使用的丰富链类型。
type ChainKind string

const (
	ChainConfigured ChainKind = "configured"
	ChainNoop       ChainKind = "noop"
	ChainUnknown    ChainKind = "unknown"
)
