// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

// EventType 表示稳定支持的 Datadog RUM 事件类型。
type EventType string

func (e EventType) S() string { return string(e) }

const (
	EventTypeView       EventType = "view"
	EventTypeViewUpdate EventType = "view_update"
	EventTypeAction     EventType = "action"
	EventTypeResource   EventType = "resource"
	EventTypeError      EventType = "error"
	EventTypeLongTask   EventType = "long_task"
	EventTypeVital      EventType = "vital"
)

// IsValid 判断事件类型是否在稳定事件范围内。
func (e EventType) IsValid() bool {
	switch e {
	case EventTypeView, EventTypeViewUpdate, EventTypeAction, EventTypeResource, EventTypeError, EventTypeLongTask, EventTypeVital:
		return true
	default:
		return false
	}
}
