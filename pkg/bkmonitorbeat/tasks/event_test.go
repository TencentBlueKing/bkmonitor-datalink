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
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

func TestEventStatus(t *testing.T) {
	ev := new(Event)
	ev.Success()
	assert.Equal(t, define.GatherStatusOK, int(ev.Status))
	assert.Equal(t, define.CodeOK, ev.ErrorCode)

	ev.Fail(define.CodeUnknown)
	assert.Equal(t, define.GatherStatusError, int(ev.Status))
	assert.Equal(t, define.CodeUnknown, ev.ErrorCode)
}

func TestBaseEvent(t *testing.T) {
	baseEvent := new(Event)
	res := baseEvent.AsMapStr()
	assert.NotNil(t, res["dataid"])
	assert.NotNil(t, res["bk_biz_id"])
	assert.NotNil(t, res["task_id"])
	assert.NotNil(t, res["timestamp"])
	assert.NotNil(t, res["task_type"])
	assert.NotNil(t, res["status"])
	assert.NotNil(t, res["error_code"])
	assert.NotNil(t, res["available"])
	assert.NotNil(t, res["task_duration"])
}

func TestStatusEvent(t *testing.T) {
	statusEvent := new(StatusEvent)
	res := statusEvent.AsMapStr()
	assert.NotNil(t, res["dataid"])
	assert.NotNil(t, res["status"])
	assert.NotNil(t, res["not_uptimecheck"])
}

func TestSimpleEvent(t *testing.T) {
	simpleEvent := new(SimpleEvent)
	simpleEvent.Event = new(Event)
	res := simpleEvent.AsMapStr()
	assert.NotNil(t, res["dataid"])
	assert.NotNil(t, res["bk_biz_id"])
	assert.NotNil(t, res["task_id"])
	assert.NotNil(t, res["timestamp"])
	assert.NotNil(t, res["task_type"])
	assert.NotNil(t, res["status"])
	assert.NotNil(t, res["error_code"])
	assert.NotNil(t, res["available"])
	assert.NotNil(t, res["task_duration"])
	assert.NotNil(t, res["target_host"])
	assert.NotNil(t, res["target_port"])
}

func TestStandardEvent(t *testing.T) {
	standardEvent := new(StandardEvent)
	standardEvent.Dimensions = make(map[string]string)
	standardEvent.Metrics = make(map[string]interface{})
	res := standardEvent.AsMapStr()
	assert.NotNil(t, res["dimensions"])
	assert.NotNil(t, res["dataid"])
	assert.NotNil(t, res["time"])
	assert.NotNil(t, res["metrics"])
}

func TestMetricEvent(t *testing.T) {
	metricEvent := new(MetricEvent)
	metricEvent.Data = make(map[string]interface{})
	metricEvent.AsMapStr()
}

func TestCustomEvent(t *testing.T) {
	dataID := int32(100)
	ts := time.Now().Unix()
	customMetricEvent := &CustomMetricEvent{
		MetricEvent: &MetricEvent{
			StatusEvent: StatusEvent{
				DataID: dataID,
			},
			Data: common.MapStr{
				"prometheus": common.MapStr{
					"collector": common.MapStr{
						"metrics": []common.MapStr{
							{
								"key":   "requests_total",
								"value": 100,
								"labels": common.MapStr{
									"node":      "love-peace",
									"namespace": "blueking",
									"endpoint":  "endpoint1",
									"instance":  "instance1",
									"target":    "1.1.1.1",
								},
								"exemplar": "e1",
							},
						},
					},
				},
			},
			Labels: []map[string]string{
				{
					"biz":               "1001",
					"namespace":         "bkmonitor",
					"exported_endpoint": "endpoint2",
					"instance":          "instance2",
					"exported_instance": "instance3",
				},
			},
		},
		Timestamp: ts,
	}

	m := customMetricEvent.AsMapStr()

	assert.Equal(t, common.MapStr{
		"dataid": dataID,
		"data": []map[string]interface{}{
			{
				"target": "1.1.1.1",
				"dimension": map[string]string{
					"biz":                "1001",
					"endpoint":           "endpoint1",
					"exported_endpoint":  "endpoint2",
					"exported_instance":  "instance3",
					"exported_namespace": "bkmonitor",
					"instance":           "instance1",
					"namespace":          "blueking",
					"node":               "love-peace",
					"target":             "1.1.1.1",
				},
				"metrics": map[string]interface{}{
					"requests_total": 100,
				},
				"timestamp": ts * 1000,
				"exemplar":  "e1",
			},
		},
		"time":      ts,
		"timestamp": ts,
	}, m)
}
