// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// EventItem :
type EventItem struct {
	EventType config.EventType
	DataPath  string
	DataValue []byte
}

// Event :
type Event struct {
	EventTime time.Time
	Detail    []EventItem
}

// NewConsulEvent :
func NewConsulEvent() *Event {
	return &Event{
		EventTime: time.Now(),
		Detail:    make([]EventItem, 0),
	}
}

// CfgEventItem :
type CfgEventItem struct {
	DataID int
	EventItem
}

// CfgEvent :
type CfgEvent struct {
	EventTime time.Time
	Detail    []CfgEventItem
}

// NewCfgEvent :
func NewCfgEvent() *CfgEvent {
	return &CfgEvent{
		EventTime: time.Now(),
		Detail:    make([]CfgEventItem, 0),
	}
}

// SamplingItem :
type SamplingItem struct {
	Type  string      `json:"type"`
	Tag   string      `json:"tag"`
	Name  string      `json:"field_name"`
	Value interface{} `json:"field_value"`
}

// NewSamplingItem
func NewSamplingItem(typ define.MetaFieldType, tag define.MetaFieldTagType, name string, value interface{}) *SamplingItem {
	return &SamplingItem{
		Type:  string(typ),
		Tag:   string(tag),
		Name:  name,
		Value: value,
	}
}

// SourceClient :
type SourceClient interface {
	Get(key string) ([]byte, error)
	GetValues(keys []string) (map[string][]byte, error)
	MonitorPath(conPaths []string) (<-chan *Event, error)
	SetContext(ctx context.Context)
	Put(key string, value []byte) error
	Delete(key string) error
	CreateTempNode(key, value, sessionID string) error
	KeepSession() (string, error)
	DestroySession(sessionID string) error
	GetKeys(prefix string) ([]string, error)
}
