// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package internal

import (
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/example"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/processor"
)

// RegisterTaskHandleFunc async task map, format: task: taskhandler
var RegisterTaskHandleFunc = map[string]processor.HandlerFunc{
	"async:test_example": example.HandleExampleTask,
}

// RegisterPeriodicTaskHandlerFunc periodic task map
var RegisterPeriodicTaskHandlerFunc = map[string]processor.HandlerFunc{
	"periodic:metadata:refresh_ts_metric":        task.RefreshTimeSeriesMetric,
	"periodic:metadata:refresh_event_dimension":  task.RefreshEventDimension,
	"periodic:metadata:refresh_es_storage":       task.RefreshESStorage,
	"periodic:metadata:refresh_influxdb_route":   task.RefreshInfluxdbRoute,
	"periodic:metadata:refresh_datasource":       task.RefreshDatasource,
	"periodic:metadata:discover_bcs_clusters":    task.DiscoverBcsClusters,
	"periodic:metadata:refresh_bcs_monitor_info": task.RefreshBcsMonitorInfo,
}

var RegisterPeriodicTask = map[string]string{
	"periodic:metadata:refresh_ts_metric":       "*/2 * * * *",
	"periodic:metadata:refresh_event_dimension": "*/3 * * * *",
	"periodic:metadata:refresh_es_storage":      "*/10 * * * *",
	"periodic:metadata:refresh_influxdb_route":  "*/10 * * * *",
	"periodic:metadata:refresh_datasource":      "*/10 * * * *",
	"periodic:metadata:discover_bcs_clusters":   "*/10 * * * *",
}

type RegisterPeriodicTaskDetail struct {
	*sync.Map
}

func NewRegisterPeriodicTaskDetail() *RegisterPeriodicTaskDetail {
	return &RegisterPeriodicTaskDetail{
		Map: &sync.Map{},
	}
}

var (
	registerPeriodicTaskDetail = NewRegisterPeriodicTaskDetail()
)

// GetRegisterPeriodicTaskDetail get register task desc
func GetRegisterPeriodicTaskDetail() *RegisterPeriodicTaskDetail {
	return registerPeriodicTaskDetail
}

// AddConstantPeriodicTask add hard code task config
func (pt *RegisterPeriodicTaskDetail) AddConstantPeriodicTask() {
	// based on redis data
	for name, cronSpec := range RegisterPeriodicTask {
		if _, ok := pt.Load(name); !ok {
			pt.Store(name, map[string]interface{}{"cronSpec": cronSpec, "enable": true})
		}
	}
}

func InitPeriodicTask() {
	pt := NewRegisterPeriodicTaskDetail()
	// TODO: from redis init data
	pt.AddConstantPeriodicTask()
	registerPeriodicTaskDetail = pt
}
