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
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Event :
type Event struct {
	DataID            int32
	BizID             int32
	TaskID            int32
	TaskType          string
	Available         float64
	Status            int32
	ErrorCode         define.BeatErrorCode
	StartAt           time.Time
	EndAt             time.Time
	AvailableDuration time.Duration
	Labels            []map[string]string
}

// IgnoreCMDBLevel :
func (e *Event) IgnoreCMDBLevel() bool { return false }

// Fail :
func (e *Event) Fail(code define.BeatErrorCode) {
	e.Status = define.GatherStatusError
	e.ErrorCode = code
	e.EndAt = time.Now()
	e.Available = 0
}

func (e *Event) FailWithTime(code define.BeatErrorCode, start, end time.Time) {
	e.Status = define.GatherStatusError
	e.ErrorCode = code
	e.Available = 0
	e.StartAt = start
	e.EndAt = end
}

// Success :
func (e *Event) Success() {
	e.Status = define.GatherStatusOK
	e.ErrorCode = define.BeatErrCodeOK
	e.EndAt = time.Now()
	e.Available = 1
}

func (e *Event) SuccessWithTime(start, end time.Time) {
	e.Status = define.GatherStatusOK
	e.ErrorCode = define.BeatErrCodeOK
	e.Available = 1
	e.StartAt = start
	e.EndAt = end
}

// SuccessOrTimeout :
func (e *Event) SuccessOrTimeout() {
	e.EndAt = time.Now()
	if e.AvailableDuration > time.Nanosecond && e.TaskDuration() > e.AvailableDuration {
		logger.Debugf("fail because task duration exceed")
		e.Fail(define.BeatErrCodeTimeout)
	} else {
		e.Success()
	}
}

// TaskDuration :
func (e *Event) TaskDuration() time.Duration {
	return e.EndAt.Sub(e.StartAt)
}

// AsMapStr :
func (e *Event) AsMapStr() common.MapStr {
	mapStr := common.MapStr{
		"dataid":        e.DataID,
		"bk_biz_id":     e.BizID,
		"task_id":       e.TaskID,
		"timestamp":     e.StartAt.Unix(),
		"task_type":     e.TaskType,
		"status":        e.Status,
		"error_code":    e.ErrorCode,
		"available":     e.Available,
		"task_duration": int(e.TaskDuration().Milliseconds()),
	}
	mapStr["group_info"] = make([]map[string]string, 0)

	// label注入
	if e.Labels != nil {
		mapStr["group_info"] = e.Labels
	}
	return mapStr
}

func (e *Event) GetType() string {
	return e.TaskType
}

// NewEvent :
func NewEvent(task define.Task) *Event {
	taskConf := task.GetConfig()
	return &Event{
		AvailableDuration: taskConf.GetAvailableDuration(),
		DataID:            taskConf.GetDataID(),
		StartAt:           time.Now(),
		BizID:             taskConf.GetBizID(),
		TaskID:            task.GetTaskID(),
		TaskType:          taskConf.GetType(),
		Status:            define.GatherStatusUnknown,
		ErrorCode:         define.BeatErrCodeUnknown,
		Labels:            taskConf.GetLabels(),
	}
}

// SimpleEvent :
type SimpleEvent struct {
	*Event
	TargetHost string
	TargetPort int
	ResolvedIP string // DNS解析模式为全部时对应的实际请求IP，其他情况为空
}

// AsMapStr :
func (e *SimpleEvent) AsMapStr() common.MapStr {
	mapStr := e.Event.AsMapStr()
	mapStr["target_host"] = e.TargetHost
	mapStr["target_port"] = e.TargetPort
	mapStr["resolved_ip"] = e.ResolvedIP // 增加实际请求IP
	return mapStr
}

// NewSimpleEvent :
func NewSimpleEvent(task define.Task) *SimpleEvent {
	return &SimpleEvent{
		Event: NewEvent(task),
	}
}

// StatusEvent 状态参数
type StatusEvent struct {
	DataID         int32
	Status         int32
	NotUptimecheck int32
}

// Fail :
func (e *StatusEvent) Fail() {
	e.Status = define.GatherStatusError
}

// Success :
func (e *StatusEvent) Success() {
	e.Status = define.GatherStatusOK
}

// IgnoreCMDBLevel :
func (e *StatusEvent) IgnoreCMDBLevel() bool { return false }

// AsMapStr :
func (e *StatusEvent) AsMapStr() common.MapStr {
	mapStr := make(common.MapStr)
	mapStr["dataid"] = e.DataID
	mapStr["status"] = e.Status
	mapStr["not_uptimecheck"] = e.NotUptimecheck
	return mapStr
}

func (e *StatusEvent) GetType() string {
	return define.ModuleStatus
}

// SendFailEvent :
func SendFailEvent(dataID int32, e chan<- define.Event) {
	event := NewStatusEvent()
	event.DataID = dataID
	event.Fail()
	e <- event
}

// NewStatusEvent :
func NewStatusEvent() *StatusEvent {
	return new(StatusEvent)
}

// StandardEvent :
type StandardEvent struct {
	StatusEvent
	Labels     []map[string]string
	DataID     int32
	BizID      int32
	TaskID     int32
	Time       time.Time
	Metrics    map[string]interface{}
	Dimensions map[string]string
}

// AsMapStr :
func (e *StandardEvent) AsMapStr() common.MapStr {
	mapStr := e.StatusEvent.AsMapStr()
	mapStr["dataid"] = e.DataID
	mapStr["time"] = e.Time.Unix()
	mapStr["metrics"] = e.Metrics
	mapStr["group_info"] = make([]map[string]string, 0)

	// 将labels注入到dimensions中
	if e.Labels != nil {
		mapStr["group_info"] = e.Labels
	}

	info, _ := gse.GetAgentInfo()
	e.Dimensions["bk_host_id"] = strconv.Itoa(int(info.HostID))
	e.Dimensions["bk_biz_id"] = strconv.Itoa(int(e.BizID))
	e.Dimensions["task_id"] = strconv.Itoa(int(e.TaskID))
	mapStr["dimensions"] = e.Dimensions
	return mapStr
}

// NewStandardEvent :
func NewStandardEvent(task define.TaskConfig) *StandardEvent {
	var labels = task.GetLabels()
	return &StandardEvent{
		Labels: labels,
		BizID:  task.GetBizID(),
		TaskID: task.GetTaskID(),
	}
}

// PingEvent
type PingEvent struct {
	StandardEvent
}

// IgnoreCMDBLevel
func (e *PingEvent) IgnoreCMDBLevel() bool { return true }

func (e *PingEvent) GetType() string {
	return define.ModulePing
}

func (e *PingEvent) AsMapStr() common.MapStr {
	mapStr := e.StandardEvent.AsMapStr()
	mapStr["bk_biz_id"] = e.BizID
	return mapStr
}

// NewPingEvent
func NewPingEvent(task define.TaskConfig) *PingEvent {
	return &PingEvent{
		StandardEvent: *NewStandardEvent(task),
	}
}

// CustomMetricEvent metricbeat转自定义时序上报
type CustomMetricEvent struct {
	*MetricEvent
	Timestamp int64
}

func mapKeyExists(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

func mapStrKeyExists(m common.MapStr, k string) bool {
	_, ok := m[k]
	return ok
}

func (e *CustomMetricEvent) AsMapStr() common.MapStr {
	result := make(common.MapStr)
	result["dataid"] = e.MetricEvent.DataID

	// 基于exporter格式下潜到数据位置
	prometheus := e.Data["prometheus"]
	if prometheus == nil {
		logger.Errorf("cannot get prometheus in data:%v", e.Data)
		return result
	}
	collector := prometheus.(common.MapStr)["collector"]
	if collector == nil {
		logger.Errorf("cannot get collector in data:%v", e.Data)
		return result
	}
	metrics := collector.(common.MapStr)["metrics"]
	if metrics == nil {
		logger.Errorf("cannot get metrics in data:%v", e.Data)
		return result
	}
	datas := make([]map[string]interface{}, 0)
	for _, metricItem := range metrics.([]common.MapStr) {
		key, ok := metricItem["key"]
		if !ok {
			logger.Warnf("cannot get key in metricItem:%v", metricItem)
			continue
		}
		value, ok := metricItem["value"]
		if !ok {
			logger.Warnf("cannot get value in metricItem:%v", metricItem)
			continue
		}
		dimensionsItem := metricItem["labels"]
		if dimensionsItem == nil {
			logger.Warnf("cannot get dimensions in metricItem:%v", metricItem)
			continue
		}
		dimensions := dimensionsItem.(common.MapStr)

		var originTs int64 // 单位为秒
		originTsObj, ok := metricItem["timestamp"]
		if ok {
			originTs, _ = originTsObj.(int64)
		}

		if len(e.Labels) == 0 {
			e.Labels = []map[string]string{{}}
		}
		// 注入groupInfo到dimensions里,基于group对数据进行复制上报
		for _, labelGroup := range e.Labels {
			data := make(map[string]interface{})
			dimension := make(map[string]string)
			for key, value := range dimensions {
				dimension[key] = value.(string)
			}
			for key, value := range labelGroup {
				newKey := "exported_" + key

				// 1）采集到 prometheus 数据中已经包含了 key
				// 2) 采集到的 prometheus 数据中不包含 newKey
				// 3) 配置中额外追加的 labels 中没有这个 newKey
				if mapStrKeyExists(dimensions, key) {
					if !mapStrKeyExists(dimensions, newKey) && !mapKeyExists(labelGroup, newKey) {
						dimension[newKey] = value
					}
				} else {
					dimension[key] = value
				}
			}
			// copy target to data when exists in dimension
			if target, ok := dimension["target"]; ok {
				data["target"] = target
			}
			data["dimension"] = dimension
			data["metrics"] = map[string]interface{}{
				key.(string): value,
			}

			// 使用数据本身时间戳（如果有的话
			if originTs > 0 {
				data["timestamp"] = originTs * 1000
			} else {
				data["timestamp"] = e.Timestamp * 1000
			}

			if exemplar, ok := metricItem["exemplar"]; ok {
				data["exemplar"] = exemplar
			}
			datas = append(datas, data)
		}
	}
	result["data"] = datas
	result["time"] = e.Timestamp
	result["timestamp"] = e.Timestamp

	return result
}

// MetricEvent :
type MetricEvent struct {
	StatusEvent
	BizID  int32
	Labels []map[string]string
	Data   common.MapStr
}

// AsMapStr :
func (e *MetricEvent) AsMapStr() common.MapStr {
	e.Data["bk_biz_id"] = e.BizID
	// 在这里插入label注入操作
	e.Data["group_info"] = e.Labels
	return e.Data
}

func (e *MetricEvent) GetType() string {
	return define.ModuleMetricbeat
}

// NewMetricEvent :
func NewMetricEvent(task define.TaskConfig) *MetricEvent {
	var labels = task.GetLabels()
	return &MetricEvent{
		Labels: labels,
		BizID:  task.GetBizID(),
	}
}

// GatherUpEvent :
type GatherUpEvent struct {
	DataID     int32
	Time       time.Time
	Metrics    common.MapStr
	Dimensions common.MapStr
}

func (e *GatherUpEvent) IgnoreCMDBLevel() bool { return true }

func (e *GatherUpEvent) GetType() string {
	return define.ModuleStatus
}

// AsMapStr :
func (e *GatherUpEvent) AsMapStr() common.MapStr {
	mapStr := common.MapStr{}
	mapStr["dataid"] = e.DataID
	mapStr["data"] = []common.MapStr{
		{"metrics": e.Metrics, "dimension": e.Dimensions, "timestamp": e.Time.UnixMilli()},
	}
	return mapStr
}

func NewGatherUpEvent(task define.Task, upCode define.BeatErrorCode) *GatherUpEvent {
	return NewGatherUpEventWithDims(task, upCode, nil)
}

func NewGatherUpEventWithDims(task define.Task, upCode define.BeatErrorCode, customDims common.MapStr) *GatherUpEvent {
	return NewGatherUpEventWithConfig(task.GetConfig(), task.GetGlobalConfig(), upCode, customDims)
}

func NewGatherUpEventWithConfig(taskConfig define.TaskConfig, globalConfig define.Config, upCode define.BeatErrorCode,
	customDims common.MapStr) *GatherUpEvent {
	name, ok := define.BeatErrorCodeNameMap[upCode]
	if !ok {
		name = "NotKnownErrorCode"
	}
	dims := common.MapStr{
		"task_id":                          strconv.Itoa(int(taskConfig.GetTaskID())),
		"bk_collect_type":                  taskConfig.GetType(),
		"bk_biz_id":                        strconv.Itoa(int(taskConfig.GetBizID())),
		"bk_collect_config_id":             "",
		"bk_target_cloud_id":               "",
		"bk_target_host_id":                "",
		"bk_target_ip":                     "",
		define.BeaterUpMetricCodeLabel:     strconv.Itoa(int(upCode)),
		define.BeaterUpMetricCodeNameLabel: name,
	}
	// 从配置文件中获取维度字段
	for _, labels := range taskConfig.GetLabels() {
		for k, v := range labels {
			if _, ok := dims[k]; ok {
				dims[k] = v
			}
		}
	}
	// 主动传入自定义维度值覆盖默认值
	for k, v := range customDims {
		if _, ok := customDims[k]; ok {
			dims[k] = v
		}
	}
	ev := &GatherUpEvent{
		DataID:     globalConfig.GetGatherUpDataID(),
		Time:       time.Now(),
		Dimensions: dims,
		Metrics:    common.MapStr{define.BeaterUpMetric: 1},
	}
	return ev
}
