// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package script

import (
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

// Event script event
type Event struct {
	DataID    int32
	TaskID    int32
	TaskType  string
	ErrorCode define.BeatErrorCode
	BizID     int32
	StartAt   time.Time
	EndAt     time.Time
	Dimension common.MapStr
	Metric    common.MapStr
	Message   string
	Timestamp int64
	LocalTime string
	UTCTime   string
	UserTime  string
	Labels    []map[string]string
	Exemplar  common.MapStr
}

// IgnoreCMDBLevel :
func (e *Event) IgnoreCMDBLevel() bool { return false }

// AsMapStr :
func (e *Event) AsMapStr() common.MapStr {
	groupInfo := make([]map[string]string, 0)

	// label注入进dimension
	if e.Labels != nil {
		for _, labelInfo := range e.Labels {
			tempGroup := make(map[string]string)
			for key, value := range labelInfo {
				tempGroup[key] = value
			}

			groupInfo = append(groupInfo, tempGroup)
		}
	}
	e.Dimension["bk_biz_id"] = e.BizID

	return common.MapStr{
		"dataid":     e.DataID,
		"task_id":    e.TaskID,
		"bk_biz_id":  e.BizID,
		"task_type":  e.TaskType,
		"error_code": e.ErrorCode,
		"cost_time":  int(e.TaskDuration().Seconds() * 1000),
		"dimensions": e.Dimension,
		"exemplar":   e.Exemplar,
		"metrics":    e.Metric,
		"message":    e.Message,
		"time":       e.Timestamp,
		"localtime":  e.LocalTime,
		"utctime":    e.UTCTime,
		"usertime":   e.UserTime,
		"group_info": groupInfo,
	}
}

func (e *Event) GetType() string {
	return define.ModuleScript
}

// TaskDuration :
func (e *Event) TaskDuration() time.Duration {
	return e.EndAt.Sub(e.StartAt)
}

// NewEvent :
func NewEvent(t define.Task) *Event {
	taskConf := t.GetConfig()
	return &Event{
		DataID:    taskConf.GetDataID(),
		BizID:     taskConf.GetBizID(),
		TaskID:    t.GetTaskID(),
		TaskType:  taskConf.GetType(),
		StartAt:   time.Now().UTC(),
		ErrorCode: define.BeatErrCodeUnknown,
		Dimension: common.MapStr{},
		Metric:    common.MapStr{},
		Exemplar:  common.MapStr{},
		UserTime:  time.Now().UTC().Format(bkcommon.TimeFormat),
		Labels:    taskConf.GetLabels(),
	}
}

// Success 普通指标事件正常结束
func (e *Event) Success() {
	e.ErrorCode = define.BeatErrCodeOK
	e.EndAt = time.Now()
	e.Message = "success"
}
