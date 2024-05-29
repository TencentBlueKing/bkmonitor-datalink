// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procsnapshot

import (
	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

const (
	eventTypeProcess = "process"
	eventTypeSocket  = "socket"
)

type pEvent struct {
	Type  string
	Event common.MapStr
}

func (e pEvent) AsMap() common.MapStr {
	return common.MapStr{
		"type":  e.Type,
		"event": e.Event,
	}
}

func newProcessEvent(process ProcMeta) pEvent {
	return pEvent{
		Type: eventTypeProcess,
		Event: common.MapStr{
			"process": process,
		},
	}
}

func newSocketEvent(socket ProcConn) pEvent {
	return pEvent{
		Type: eventTypeSocket,
		Event: common.MapStr{
			"socket": socket,
		},
	}
}

type Event struct {
	dataid int32
	data   []common.MapStr
}

func (e *Event) AsMapStr() common.MapStr {
	return common.MapStr{
		"dataid": e.dataid,
		"data":   e.data,
	}
}

func (e *Event) IgnoreCMDBLevel() bool {
	return true
}

func (e *Event) GetType() string {
	return define.ModuleProcSnapshot
}
