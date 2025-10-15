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
	ErrorCode         define.NamedCode
	StartAt           time.Time
	EndAt             time.Time
	AvailableDuration time.Duration
	Labels            []map[string]string
}

// IgnoreCMDBLevel :
func (e *Event) IgnoreCMDBLevel() bool { return false }

// Fail :
func (e *Event) Fail(code define.NamedCode) {
	e.Status = define.GatherStatusError
	e.ErrorCode = code
	e.EndAt = time.Now()
	e.Available = 0
}

func (e *Event) FailWithTime(code define.NamedCode, start, end time.Time) {
	e.Status = define.GatherStatusError
	e.ErrorCode = code
	e.Available = 0
	e.StartAt = start
	e.EndAt = end
}

// Success :
func (e *Event) Success() {
	e.Status = define.GatherStatusOK
	e.ErrorCode = define.CodeOK
	e.EndAt = time.Now()
	e.Available = 1
}

func (e *Event) SuccessWithTime(start, end time.Time) {
	e.Status = define.GatherStatusOK
	e.ErrorCode = define.CodeOK
	e.Available = 1
	e.StartAt = start
	e.EndAt = end
}

// SuccessOrTimeout :
func (e *Event) SuccessOrTimeout() {
	if e.EndAt.IsZero() {
		e.EndAt = time.Now()
	}
	if e.AvailableDuration > time.Nanosecond && e.TaskDuration() > e.AvailableDuration {
		logger.Debugf("fail because task duration exceed")
		e.Fail(define.CodeTimeout)
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
		"error_code":    e.ErrorCode.Code(),
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
		ErrorCode:         define.CodeUnknown,
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
	labels := task.GetLabels()
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
				// 1）采集到 prometheus 数据中已经包含了 key
				// 2) 采集到的 prometheus 数据中不包含 newKey
				// 3) 配置中额外追加的 labels 中没有这个 newKey
				if mapStrKeyExists(dimensions, key) {
					newKey := "exported_" + key
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
	labels := task.GetLabels()
	return &MetricEvent{
		Labels: labels,
		BizID:  task.GetBizID(),
	}
}

// CustomEvent 自定义消息事件
type CustomEvent struct {
	Type            string
	Data            common.MapStr
	ignoreCmdbLevel bool
	Labels          []map[string]string
}

// NewCustomEvent 创建自定义事件
func NewCustomEvent(t string, data common.MapStr, ignoreCmdbLevel bool, labels []map[string]string) *CustomEvent {
	return &CustomEvent{
		Type:            t,
		Data:            data,
		ignoreCmdbLevel: ignoreCmdbLevel,
		Labels:          labels,
	}
}

// NewCustomEventBySimpleEvent 通过SimpleEvent创建自定义事件
func NewCustomEventBySimpleEvent(e *SimpleEvent) *CustomEvent {
	ts := e.StartAt.Unix()
	// 补充节点信息
	info, _ := gse.GetAgentInfo()

	// 维度取值
	dimensions := map[string]string{
		"bk_biz_id":   strconv.Itoa(int(e.BizID)),
		"target_host": e.TargetHost,
		"target_port": strconv.Itoa(e.TargetPort),
		"task_id":     strconv.Itoa(int(e.TaskID)),
		"task_type":   e.TaskType,
		"status":      strconv.Itoa(int(e.Status)),
		"resolved_ip": e.ResolvedIP,
		"error_code":  strconv.Itoa(e.ErrorCode.Code()),
		"node_id":     fmt.Sprintf("%d:%s", info.Cloudid, info.IP),
		"ip":          info.IP,
		"bk_cloud_id": strconv.Itoa(int(info.Cloudid)),
		"bk_agent_id": info.BKAgentID,
	}

	data := common.MapStr{
		"dataid": e.DataID,
		"data": []map[string]interface{}{
			{
				"target":    fmt.Sprintf("%s:%d", e.TargetHost, e.TargetPort),
				"dimension": dimensions,
				"metrics": map[string]interface{}{
					"available":     e.Available,
					"task_duration": int(e.TaskDuration().Milliseconds()),
				},
				"timestamp": ts * 1000,
			},
		},
		"time":      ts,
		"timestamp": ts,
	}

	return NewCustomEvent(e.GetType(), data, e.IgnoreCMDBLevel(), e.Labels)
}

// NewCustomEventByPingEvent 通过PingEvent创建自定义事件
func NewCustomEventByPingEvent(events ...*PingEvent) *CustomEvent {
	var data []map[string]interface{}
	for _, e := range events {
		ts := e.Time.Unix()

		// 触发维度补充
		e.AsMapStr()

		// 维度取值
		dimensions := map[string]string{}
		for k, v := range e.Dimensions {
			dimensions[k] = v
		}

		// 补充节点信息
		info, _ := gse.GetAgentInfo()
		dimensions["node_id"] = fmt.Sprintf("%d:%s", info.Cloudid, info.IP)
		dimensions["ip"] = info.IP
		dimensions["bk_cloud_id"] = strconv.Itoa(int(info.Cloudid))
		dimensions["bk_agent_id"] = info.BKAgentID

		// 指标取值
		metrics := map[string]interface{}{}
		for k, v := range e.Metrics {
			metrics[k] = v
		}

		data = append(data, map[string]interface{}{
			"target":    dimensions["target"],
			"dimension": dimensions,
			"metrics":   metrics,
			"timestamp": ts * 1000,
		})
	}

	event := events[0]
	customEvent := common.MapStr{
		"dataid":    event.DataID,
		"data":      data,
		"time":      event.Time.Unix(),
		"timestamp": event.Time.Unix(),
	}

	return NewCustomEvent(event.GetType(), customEvent, event.IgnoreCMDBLevel(), event.Labels)
}

// GetType 获取事件类型
func (e *CustomEvent) GetType() string {
	return e.Type
}

// deepCopyMap 深拷贝嵌套 map
func deepCopyMap(src map[string]interface{}) map[string]interface{} {
	dst := make(map[string]interface{})
	for k, v := range src {
		switch obj := v.(type) {
		case map[string]string:
			newValue := make(map[string]string)
			for kk, vv := range obj {
				newValue[kk] = vv
			}
			dst[k] = newValue
		case map[string]interface{}:
			newValue := make(map[string]interface{})
			for kk, vv := range obj {
				newValue[kk] = vv
			}
			dst[k] = newValue
		default:
			dst[k] = v
		}
	}
	return dst
}

// AsMapStr 转换为mapstr
func (e *CustomEvent) AsMapStr() common.MapStr {
	// 如果没有labels，直接返回data
	if len(e.Labels) == 0 {
		return e.Data
	}

	// 数据断言
	data, ok := e.Data["data"].([]map[string]interface{})
	if !ok {
		e.Data["data"] = []map[string]interface{}{}
		return e.Data
	}

	// 将 data 和 labels 进行组合
	var records []map[string]interface{}
	for _, record := range data {
		for _, labels := range e.Labels {
			// 深拷贝
			newRecord := deepCopyMap(record)

			// 将labels注入到dimensions中
			dimensions, ok := newRecord["dimension"].(map[string]string)
			if !ok {
				dimensions = make(map[string]string)
				newRecord["dimension"] = dimensions
			}
			for k, v := range labels {
				dimensions[k] = v
			}

			records = append(records, newRecord)
		}
	}
	e.Data["data"] = records

	return e.Data
}

// IgnoreCMDBLevel 是否忽略CMDB层级
func (e *CustomEvent) IgnoreCMDBLevel() bool {
	return e.ignoreCmdbLevel
}
