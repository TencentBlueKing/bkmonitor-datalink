// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"fmt"
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

type GatherUpEvent struct {
	DataID     int32
	Time       time.Time
	Metrics    common.MapStr
	Dimensions common.MapStr
}

func (e *GatherUpEvent) IgnoreCMDBLevel() bool {
	return true
}

func (e *GatherUpEvent) GetType() string {
	return define.ModuleStatus
}

func (e *GatherUpEvent) AsMapStr() common.MapStr {
	mapStr := common.MapStr{}
	mapStr["dataid"] = e.DataID
	mapStr["data"] = []common.MapStr{{
		"metrics":   e.Metrics,
		"dimension": e.Dimensions,
		"timestamp": e.Time.UnixMilli(),
	}}
	return mapStr
}

func (e *GatherUpEvent) KVs() []define.LogKV {
	var kvs []define.LogKV
	for k, v := range e.Dimensions {
		if s, ok := v.(string); ok && s != "" {
			kvs = append(kvs, define.LogKV{
				K: k,
				V: s,
			})
		}
	}
	return kvs
}

func NewGatherUpEvent(task define.Task, upCode define.NamedCode) *GatherUpEvent {
	return NewGatherUpEventWithDims(task, upCode, nil)
}

func NewGatherUpEventWithValue(task define.Task, upCode define.NamedCode, value float64) *GatherUpEvent {
	return NewGatherUpEventWithConfig(task.GetGlobalConfig().GetGatherUpDataID(), task.GetConfig(), upCode, nil, value)
}

func NewGatherUpEventWithDims(task define.Task, upCode define.NamedCode, customDims common.MapStr) *GatherUpEvent {
	return NewGatherUpEventWithConfig(task.GetGlobalConfig().GetGatherUpDataID(), task.GetConfig(), upCode, customDims, 1)
}

func NewGatherUpEventWithConfig(dataID int32, taskConfig define.TaskConfig, upCode define.NamedCode, customDims common.MapStr, value float64) *GatherUpEvent {
	name := upCode.Name()
	if name == "" {
		name = "UnknownCode"
	}
	dims := common.MapStr{
		"task_id":              strconv.Itoa(int(taskConfig.GetTaskID())),
		"bk_collect_type":      taskConfig.GetType(),
		"bk_biz_id":            strconv.Itoa(int(taskConfig.GetBizID())),
		define.LabelUpCode:     strconv.Itoa(upCode.Code()),
		define.LabelUpCodeName: name,
	}

	// 从配置文件中获取维度字段
	for _, labels := range taskConfig.GetLabels() {
		for k, v := range labels {
			dims[k] = v
		}
	}
	// 主动传入自定义维度值覆盖默认值
	for k, v := range customDims {
		dims[k] = v
	}
	ev := &GatherUpEvent{
		DataID:     dataID,
		Time:       time.Now(),
		Dimensions: dims,
		Metrics:    common.MapStr{define.NameGatherUp: value},
	}

	define.RecordLog(fmt.Sprintf("[%s] %s{} %f", taskConfig.GetType(), define.NameGatherUp, value), ev.KVs())
	return ev
}
