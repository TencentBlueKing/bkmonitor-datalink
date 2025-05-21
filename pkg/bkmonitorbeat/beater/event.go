// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beater

import (
	"strconv"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
)

// Event :
type Event struct {
	NodeID  string
	CloudID int32
	IP      string
	Type    string
}

// BeatEvent :
type BeatEvent struct {
	*Event
	event define.Event
}

func updateMapStr(target common.MapStr, source common.MapStr) {
	for key, value := range source {
		target[key] = value
	}
}

// AsMapStr :
func (e *BeatEvent) AsMapStr() common.MapStr {
	event := e.event.AsMapStr()
	updateMapStr(event, common.MapStr{
		"node_id":     e.NodeID,
		"bk_cloud_id": e.CloudID,
		"ip":          e.IP,
		"type":        e.Type,
	})
	return event
}

// NewBeatEvent :
func NewBeatEvent(bt *MonitorBeater, event define.Event) *BeatEvent {
	conf := bt.config
	return &BeatEvent{
		event: event,
		Event: &Event{
			NodeID:  conf.NodeID,
			CloudID: conf.CloudID,
			IP:      conf.IP,
			Type:    event.GetType(),
		},
	}
}

// HeartBeatEvent :
type HeartBeatEvent struct {
	*Event
	BizID        int32
	DataID       int32
	Status       int32
	Time         time.Time
	IP           string
	Version      string
	Uptime       time.Duration
	Reload       int32
	ReloadTime   time.Time
	LoadedTasks  int32
	RunningTasks int32
	Success      int32
	Fail         int32
	Error        int32
}

// AsMapStr :
func (e *HeartBeatEvent) AsMapStr() common.MapStr {
	return common.MapStr{
		"type":             e.Type,
		"node_id":          e.NodeID,
		"bk_biz_id":        e.BizID,
		"bk_cloud_id":      e.CloudID,
		"ip":               e.IP,
		"dataid":           e.DataID,
		"status":           e.Status,
		"timestamp":        e.Time.Unix(),
		"version":          e.Version,
		"uptime":           int(e.Uptime.Seconds() * 1000),
		"reload":           e.Reload,
		"reload_timestamp": e.ReloadTime.Unix(),
		"loaded_tasks":     e.LoadedTasks,
		"running_tasks":    e.RunningTasks,
		"success":          e.Success,
		"fail":             e.Fail,
		"error":            e.Error,
	}
}

// NewHeartBeatEvent :
func NewHeartBeatEvent(bt *MonitorBeater) *HeartBeatEvent {
	state := bt.beaterState
	status := bt.beaterStatus
	stateConfig := state.config
	scheduler := state.Scheduler
	now := time.Now()
	return &HeartBeatEvent{
		Event: &Event{
			NodeID:  stateConfig.NodeID,
			CloudID: stateConfig.CloudID,
			IP:      stateConfig.IP,
		},
		BizID:        stateConfig.BizID,
		DataID:       stateConfig.HeartBeat.DataID,
		Status:       0,
		Time:         now,
		IP:           stateConfig.IP,
		Uptime:       now.Sub(status.startAt),
		Reload:       status.reloadCount,
		ReloadTime:   status.reloadAt,
		Success:      status.successCount,
		Fail:         status.failCount,
		Error:        status.errorCount,
		LoadedTasks:  status.loadedTasks,
		RunningTasks: int32(scheduler.Count()),
	}
}

// GlobalHeartBeatEvent 全局心跳
type GlobalHeartBeatEvent struct {
	DataID          int32
	Status          int
	Uptime          int
	Version         string
	Tasks           int32
	ConfigLoadAt    int64
	Published       int32
	Errors          int32
	ConfigErrorCode int
	ErrorTasks      int
}

// IgnoreCMDBLevel :
func (g *GlobalHeartBeatEvent) IgnoreCMDBLevel() bool { return false }

// AsMapStr :
func (g *GlobalHeartBeatEvent) AsMapStr() common.MapStr {
	dimensions := make(map[string]interface{})
	dimensions["status"] = g.Status
	dimensions["version"] = g.Version

	info, _ := gse.GetAgentInfo()
	dimensions["bk_host_id"] = strconv.Itoa(int(info.HostID))

	metrics := make(map[string]interface{})
	metrics["uptime"] = g.Uptime
	metrics["tasks"] = g.Tasks
	metrics["config_load_at"] = g.ConfigLoadAt
	metrics["published"] = g.Published
	metrics["errors"] = g.Errors
	metrics["config_error_code"] = g.ConfigErrorCode
	metrics["error_tasks"] = g.ErrorTasks
	return common.MapStr{
		"time":       time.Now().Unix(),
		"dataid":     g.DataID,
		"dimensions": dimensions,
		"metrics":    metrics,
	}
}

func (g *GlobalHeartBeatEvent) GetType() string {
	return define.ModuleGlobalHeartBeat
}

func NewGlobalHeartBeatEvent(bt *MonitorBeater) define.Event {
	ce := bt.configEngine
	hasChildPath := ce.HasChildPath()
	correctNum := ce.GetTaskNum()
	errNum := ce.GetWrongTaskNum()
	status := bt.beaterStatus
	beatConfig := bt.beaterState.config
	now := time.Now()
	var errCode int
	if !hasChildPath {
		errCode = define.GetErrorCodeByError(define.ErrNoChildPath)
	}

	dataID := beatConfig.HeartBeat.GlobalDataID
	storage := tenant.DefaultStorage()
	if v, ok := storage.GetTaskDataID(define.ModuleGlobalHeartBeat); ok {
		dataID = v
	}

	return &GlobalHeartBeatEvent{
		DataID:          dataID,
		Status:          0,
		Uptime:          int(now.Sub(status.startAt).Seconds()),
		Tasks:           int32(correctNum),
		ConfigLoadAt:    status.reloadAt.Unix(),
		Published:       status.successCount,
		Errors:          status.failCount,
		ConfigErrorCode: errCode,
		ErrorTasks:      errNum,
	}
}

// ChildTaskHeartbeatEvent :
type ChildTaskHeartbeatEvent struct {
	DataID          int32
	Version         string
	Name            string
	TaskID          int32
	ConfigErrorCode int
	Path            string
}

// IgnoreCMDBLevel :
func (c *ChildTaskHeartbeatEvent) IgnoreCMDBLevel() bool { return false }

// AsMapStr :
func (c *ChildTaskHeartbeatEvent) AsMapStr() common.MapStr {
	dimensions := make(map[string]interface{})
	dimensions["version"] = c.Version
	dimensions["name"] = c.Name
	dimensions["path"] = c.Path
	dimensions["taskid"] = c.TaskID
	metrics := make(map[string]interface{})
	metrics["config_error_code"] = c.ConfigErrorCode
	return common.MapStr{
		"time":       time.Now().Unix(),
		"dataid":     c.DataID,
		"dimensions": dimensions,
		"metrics":    metrics,
	}
}

// GetType :
func (c ChildTaskHeartbeatEvent) GetType() string {
	return define.ModuleChildHeartBeat
}
