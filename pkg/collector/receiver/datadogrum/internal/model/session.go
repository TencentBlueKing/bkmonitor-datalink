// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

// Session 是 RUM 事件的会话关联实体，不作为独立事件类型。
// 字段语义与出现时机参考 Datadog RUM 文档，注释括号内标注出现时机：
// 常见 / 开启回放后 / View 事件中条件出现 / 查询或聚合结果。
type Session struct {
	// ID 会话 ID（常见）。
	ID string `json:"id,omitempty"`
	// Type 会话类型（常见），常见值为 user，也可能出现 synthetics、ci_test。
	Type string `json:"type,omitempty"`
	// InitialViewID 会话首个 View 的 ID。
	InitialViewID string `json:"initial_view_id,omitempty"`
	// HasReplay 当前会话是否包含 Session Replay（开启回放后）。
	HasReplay *bool `json:"has_replay,omitempty"`
	// SampledForReplay 当前会话是否被回放采样（View 事件中条件出现）。
	SampledForReplay *bool `json:"sampled_for_replay,omitempty"`
	// IsActive 会话是否仍处于活动状态（View 事件中条件出现）。
	IsActive *bool `json:"is_active,omitempty"`
	// TimeSpent 会话持续时间，单位为纳秒（查询或聚合结果）。
	TimeSpent *int64 `json:"time_spent,omitempty"`
	// ViewCount 会话内 View 数量（查询或聚合结果）。
	ViewCount *int64 `json:"view_count,omitempty"`
	// ActionCount 会话内 Action 数量（查询或聚合结果）。
	ActionCount *int64 `json:"action_count,omitempty"`
	// ResourceCount 会话内 Resource 数量（查询或聚合结果）。
	ResourceCount *int64 `json:"resource_count,omitempty"`
	// ErrorCount 会话内 Error 数量（查询或聚合结果）。
	ErrorCount *int64 `json:"error_count,omitempty"`
	// LongTaskCount 会话内 Long Task 数量（查询或聚合结果）。
	LongTaskCount *int64 `json:"long_task_count,omitempty"`
	// Frustrated 当前会话是否被标记为受挫。
	Frustrated *bool `json:"frustrated,omitempty"`
}
