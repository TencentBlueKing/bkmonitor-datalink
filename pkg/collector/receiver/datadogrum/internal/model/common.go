// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

import "encoding/json"

// CommonFields 是所有 RUM 事件共享的关联上下文。
type CommonFields struct {
	Date         int64           `json:"date"`
	Timestamp    int64           `json:"-"`
	Type         EventType       `json:"type"`
	Source       string          `json:"source,omitempty"`
	Service      string          `json:"service,omitempty"`
	Version      string          `json:"version,omitempty"`
	BuildVersion string          `json:"build_version,omitempty"`
	BuildID      string          `json:"build_id,omitempty"`
	Tags         string          `json:"ddtags,omitempty"`
	Application  Application     `json:"application"`
	Session      *Session        `json:"session,omitempty"`
	ViewContext  *ViewContext    `json:"-"`
	User         *User           `json:"usr,omitempty"`
	Account      *Account        `json:"account,omitempty"`
	Tab          *Tab            `json:"tab,omitempty"`
	Connectivity *Connectivity   `json:"connectivity,omitempty"`
	Display      *Display        `json:"display,omitempty"`
	Device       *Device         `json:"device,omitempty"`
	OS           *OS             `json:"os,omitempty"`
	Context      map[string]any  `json:"context,omitempty"`
	FeatureFlags map[string]any  `json:"feature_flags,omitempty"`
	Privacy      map[string]any  `json:"privacy,omitempty"`
	Container    map[string]any  `json:"container,omitempty"`
	Stream       map[string]any  `json:"stream,omitempty"`
	Internal     *InternalFields `json:"_dd,omitempty"`
	Raw          json.RawMessage `json:"-"`
}

func (c *CommonFields) Normalize() {
	if c.Date <= 0 && c.Timestamp > 0 {
		c.Date = c.Timestamp
	}
}
