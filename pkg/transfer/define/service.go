// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"time"
)

// WatchEventType : session event type
type WatchEventType int

// ServiceType
type ServiceType int

const (
	WatchEventUnknown WatchEventType = iota
	WatchEventAdded
	WatchEventDeleted
	WatchEventModified
	WatchEventNoChange
)

const (
	ServiceTypeMe ServiceType = iota
	ServiceTypeLeader
	ServiceTypeAll
	ServiceTypeClusterAll
	ServiceTypeLeaderAll
)

// WatchEvent : session event info
type WatchEvent struct {
	Time time.Time
	Type WatchEventType
	ID   string
	Data interface{}
}

// ServiceTagType
type ServiceTagType string

// ServiceInfo : session service info
type ServiceInfo struct {
	ID      string            `json:"id"`
	Address string            `json:"address"`
	Port    int               `json:"port"`
	Tags    []string          `json:"tags"`
	Meta    map[string]string `json:"meta"`
	Detail  interface{}       `json:"-"`
}

//go:generate stringer -type=WatchEventType -trimprefix WatchEvent
//go:generate stringer -type=ServiceType -trimprefix ServiceType
