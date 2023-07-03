// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package monitoring

import (
	"strconv"
	"sync"

	"github.com/elastic/beats/libbeat/monitoring"
)

var (
	bkbeat                      = monitoring.Default.NewRegistry("bkbeat")
	bkbeatTask                  = monitoring.Default.NewRegistry("bkbeat_tasks")
	taskRegistry                = make(map[string]*monitoring.Registry)
	taskMetricsMut              = sync.Mutex{}
	taskMetrics    *TaskMetrics = &TaskMetrics{
		Bools:   make(map[string]*monitoring.Bool),
		Strings: make(map[string]*monitoring.String),
		Ints:    make(map[string]*monitoring.Int),
		Floats:  make(map[string]*monitoring.Float),
	}
)

// NewBool 创建bkbeat指标-bool
func NewBool(name string, opts ...monitoring.Option) *monitoring.Bool {
	return monitoring.NewBool(bkbeat, name, opts...)
}

// NewString 创建bkbeat指标-string
func NewString(name string, opts ...monitoring.Option) *monitoring.String {
	return monitoring.NewString(bkbeat, name, opts...)
}

// NewInt 创建bkbeat指标-int
func NewInt(name string, opts ...monitoring.Option) *monitoring.Int {
	return monitoring.NewInt(bkbeat, name, opts...)
}

// NewFloat 创建bkbeat指标-float
func NewFloat(name string, opts ...monitoring.Option) *monitoring.Float {
	return monitoring.NewFloat(bkbeat, name, opts...)
}

// newRegistry creates and register a new registry by dataID
func newRegistryWithDataID(dataID int) *monitoring.Registry {
	registryKey := strconv.Itoa(dataID)
	if _, found := taskRegistry[registryKey]; !found {
		taskRegistry[registryKey] = bkbeatTask.NewRegistry(registryKey)
	}
	return taskRegistry[registryKey]
}

// TaskMetrics 用于动态获取任务指标
type TaskMetrics struct {
	Bools   map[string]*monitoring.Bool
	Strings map[string]*monitoring.String
	Ints    map[string]*monitoring.Int
	Floats  map[string]*monitoring.Float
}

// NewBoolWithDataID：获取任务指标-bool
func NewBoolWithDataID(dataID int, name string, opts ...monitoring.Option) *monitoring.Bool {
	taskMetricsMut.Lock()
	defer taskMetricsMut.Unlock()

	reg := newRegistryWithDataID(dataID)
	metricKey := strconv.Itoa(dataID) + "_" + name
	if _, found := taskMetrics.Bools[metricKey]; !found {
		taskMetrics.Bools[metricKey] = monitoring.NewBool(reg, name, opts...)
	}
	return taskMetrics.Bools[metricKey]
}

// NewStringWithDataID：获取任务指标-string
func NewStringWithDataID(dataID int, name string, opts ...monitoring.Option) *monitoring.String {
	taskMetricsMut.Lock()
	defer taskMetricsMut.Unlock()

	reg := newRegistryWithDataID(dataID)
	metricKey := strconv.Itoa(dataID) + "_" + name
	if _, found := taskMetrics.Strings[metricKey]; !found {
		taskMetrics.Strings[metricKey] = monitoring.NewString(reg, name, opts...)
	}
	return taskMetrics.Strings[metricKey]
}

// NewIntWithDataID：获取任务指标-int
func NewIntWithDataID(dataID int, name string, opts ...monitoring.Option) *monitoring.Int {
	taskMetricsMut.Lock()
	defer taskMetricsMut.Unlock()

	reg := newRegistryWithDataID(dataID)
	metricKey := strconv.Itoa(dataID) + "_" + name
	if _, found := taskMetrics.Ints[metricKey]; !found {
		taskMetrics.Ints[metricKey] = monitoring.NewInt(reg, name, opts...)
	}
	return taskMetrics.Ints[metricKey]
}

// NewFloatWithDataID：获取任务指标-float
func NewFloatWithDataID(dataID int, name string, opts ...monitoring.Option) *monitoring.Float {
	taskMetricsMut.Lock()
	defer taskMetricsMut.Unlock()

	reg := newRegistryWithDataID(dataID)
	metricKey := strconv.Itoa(dataID) + "_" + name
	if _, found := taskMetrics.Floats[metricKey]; !found {
		taskMetrics.Floats[metricKey] = monitoring.NewFloat(reg, name, opts...)
	}
	return taskMetrics.Floats[metricKey]
}
