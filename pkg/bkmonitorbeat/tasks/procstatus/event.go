// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package procstatus

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

const VERSION = "v1.0"

// Event 上报事件
type Event struct {
	dataid  int32
	time    int64
	version string
	report  *Report
}

func NewEvent(dataid int32, version string, time int64, report *Report) *Event {
	return &Event{
		dataid:  dataid,
		time:    time,
		version: version,
		report:  report,
	}
}

func (e *Event) IgnoreCMDBLevel() bool {
	return true
}

func (e *Event) GetType() string {
	return define.ModuleProcStatus
}

func (e *Event) AsMapStr() common.MapStr {
	data := e.report.AsMapStr()
	data["timestamp"] = e.time
	data["apiVer"] = e.version
	result := common.MapStr{
		"dataid": e.dataid,
		"data":   data,
	}
	return result
}
