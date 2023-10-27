// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package static

import (
	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// CMDB自定义的上报类型
const (
	NewOperType    = "add"
	UpdateOperType = "update"
)

// Event :
type Event struct {
	dataid   int32
	time     int64
	version  string
	report   *Report
	operType string
}

// NewStaticEvent :
func NewStaticEvent(dataid int32, time int64, version string, report *Report, operType string) *Event {
	return &Event{
		dataid:   dataid,
		time:     time,
		version:  version,
		report:   report,
		operType: operType,
	}
}

// IgnoreCMDBLevel :
func (e *Event) IgnoreCMDBLevel() bool { return true }

// AsMapStr :
func (e *Event) AsMapStr() common.MapStr {
	data := e.report.AsMapStr()
	data["timestamp"] = e.time
	data["apiVer"] = e.version
	data["oper"] = e.operType
	result := common.MapStr{
		"dataid": e.dataid,
		"data":   data,
	}
	return result
}

func (e *Event) GetType() string {
	return define.ModuleStatic
}
