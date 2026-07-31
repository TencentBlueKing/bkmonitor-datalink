// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

type ActionEvent struct {
	CommonFields
	Action Action `json:"action"`
}

func (e *ActionEvent) GetType() EventType { return EventTypeAction }
func (e *ActionEvent) GetCommon() *CommonFields {
	return &e.CommonFields
}

type Action struct {
	Present       bool               `json:"-"`
	ID            string             `json:"id,omitempty"`
	Type          string             `json:"type,omitempty"`
	Name          string             `json:"name,omitempty"`
	Target        *ActionTarget      `json:"target,omitempty"`
	Frustration   *ActionFrustration `json:"frustration,omitempty"`
	EventCount    *ActionEventCount  `json:"event,omitempty"`
	CrashCount    *ActionCount       `json:"crash,omitempty"`
	Internal      *ActionInternal    `json:"_dd,omitempty"`
	Position      *ActionPosition    `json:"position,omitempty"`
	LoadingTime   *int64             `json:"loading_time,omitempty"`
	LongTaskCount *int64             `json:"long_task_count,omitempty"`
	ResourceCount *int64             `json:"resource_count,omitempty"`
	ErrorCount    *int64             `json:"error_count,omitempty"`
	ViewActive    *bool              `json:"in_foreground,omitempty"`
}

type ActionTarget struct {
	Name     string   `json:"name,omitempty"`
	Selector string   `json:"selector,omitempty"`
	Width    *float64 `json:"width,omitempty"`
	Height   *float64 `json:"height,omitempty"`
}

type ActionFrustration struct {
	Type []string `json:"type,omitempty"`
}

type ActionEventCount struct {
	Error    *int64 `json:"error_count,omitempty"`
	Resource *int64 `json:"resource_count,omitempty"`
	LongTask *int64 `json:"long_task_count,omitempty"`
}

type ActionCount struct {
	Count *int64 `json:"count,omitempty"`
}

type ActionInternal struct {
	TargetSelectorPath   string   `json:"target_selector_path,omitempty"`
	Selector             string   `json:"selector,omitempty"`
	ComposedPathSelector string   `json:"composed_path_selector,omitempty"`
	Width                *float64 `json:"width,omitempty"`
	Height               *float64 `json:"height,omitempty"`
	PermanentID          string   `json:"permanent_id,omitempty"`
	NameSource           string   `json:"name_source,omitempty"`
	PositionX            *float64 `json:"position_x,omitempty"`
	PositionY            *float64 `json:"position_y,omitempty"`
}

type ActionPosition struct {
	X *float64 `json:"x,omitempty"`
	Y *float64 `json:"y,omitempty"`
}
