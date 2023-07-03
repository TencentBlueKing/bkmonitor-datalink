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
	"github.com/elastic/beats/libbeat/common"
)

type Event interface {
	DataId() int32
	Data() common.MapStr
	RecordType() RecordType
}

type CommonEvent struct {
	dataId int32
	data   common.MapStr
}

func (e CommonEvent) DataId() int32       { return e.dataId }
func (e CommonEvent) Data() common.MapStr { return e.data }

func NewCommonEvent(dataId int32, data common.MapStr) CommonEvent {
	return CommonEvent{
		dataId: dataId,
		data:   data,
	}
}

type GatherFunc func(events ...Event)

type EventQueue struct {
	events chan []Event
	mode   PushMode
}

// NewEventQueue 生成 Records 消息队列
func NewEventQueue(mode PushMode) *EventQueue {
	return &EventQueue{
		mode:   mode,
		events: make(chan []Event, Concurrency()*QueueAmplification),
	}
}

func (q *EventQueue) Push(events []Event) {
	switch q.mode {
	case PushModeGuarantee:
		q.events <- events
	case PushModeDropIfFull:
		select {
		case q.events <- events:
		default:
		}
	}
}

func (q *EventQueue) Get() <-chan []Event {
	return q.events
}
