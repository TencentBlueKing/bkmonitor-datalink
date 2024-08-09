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
	"time"

	"github.com/influxdata/telegraf"
)

type accumulator struct {
	metrics chan<- map[string]interface{}
}

// NewAccumulator 获取一个Accumulator
func NewAccumulator(metrics chan<- map[string]interface{}) telegraf.Accumulator {
	acc := accumulator{
		metrics: metrics,
	}
	return &acc
}

func (ac *accumulator) AddFields(
	measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time,
) {
	ac.addFields(measurement, tags, fields, telegraf.Untyped, t...)
}

func (ac *accumulator) AddGauge(
	measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time,
) {
	ac.addFields(measurement, tags, fields, telegraf.Gauge, t...)
}

func (ac *accumulator) AddCounter(
	measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time,
) {
	ac.addFields(measurement, tags, fields, telegraf.Counter, t...)
}

func (ac *accumulator) AddSummary(
	measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time,
) {
	ac.addFields(measurement, tags, fields, telegraf.Summary, t...)
}

func (ac *accumulator) AddHistogram(
	measurement string,
	fields map[string]interface{},
	tags map[string]string,
	t ...time.Time,
) {
	ac.addFields(measurement, tags, fields, telegraf.Histogram, t...)
}

func (ac *accumulator) AddMetric(m telegraf.Metric) {
}

func (ac *accumulator) addFields(
	measurement string,
	tags map[string]string,
	fields map[string]interface{},
	tp telegraf.ValueType,
	t ...time.Time,
) {
	totalMap := make(map[string]interface{})
	totalMap["measurement"] = measurement
	tagMap := make(map[string]string)
	for k, v := range tags {
		tagMap[k] = v
	}

	totalMap["tag"] = tagMap
	fieldsMap := make(map[string]interface{})
	for k, v := range fields {
		fieldsMap[k] = v
	}

	totalMap["fields"] = fieldsMap
	totalMap["ValueType"] = tp
	timeMap := make([]time.Time, 0)
	for _, v := range t {
		timeMap = append(timeMap, v)
	}

	totalMap["time"] = timeMap
	ac.metrics <- totalMap
}

// AddError passes a runtime error to the accumulator.
// The error will be tagged with the plugin name and written to the log.
func (ac *accumulator) AddError(err error) {}

func (ac *accumulator) SetPrecision(precision time.Duration) {}

func (ac *accumulator) getTime(t []time.Time) time.Time {
	return time.Now()
}

func (ac *accumulator) WithTracking(maxTracked int) telegraf.TrackingAccumulator {
	return nil
}
