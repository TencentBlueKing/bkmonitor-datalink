// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

type LongTaskEvent struct {
	CommonFields
	LongTask LongTask `json:"long_task"`
}

func (e *LongTaskEvent) GetType() EventType { return EventTypeLongTask }
func (e *LongTaskEvent) GetCommon() *CommonFields {
	return &e.CommonFields
}

type LongTask struct {
	Present               bool   `json:"-"`
	ID                    string `json:"id,omitempty"`
	Name                  string `json:"name,omitempty"`
	EntryType             string `json:"entry_type,omitempty"`
	Duration              *int64 `json:"duration,omitempty"`
	BlockingDuration      *int64 `json:"blocking_duration,omitempty"`
	RenderStart           *int64 `json:"render_start,omitempty"`
	StyleAndLayoutStart   *int64 `json:"style_and_layout_start,omitempty"`
	FirstUIEventTimestamp *int64 `json:"first_ui_event_timestamp,omitempty"`
}
