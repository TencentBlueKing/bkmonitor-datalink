// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestConvertMetricV2Data(t *testing.T) {
	ts := time.Now().UnixMicro()
	data := &define.MetricV2Data{
		Data: []define.MetricV2{
			{
				Metrics: map[string]float64{
					"load1": 1,
					"load5": 2,
				},
				Timestamp: ts,
				Dimension: map[string]string{
					"module":   "db",
					"location": "guangdong",
				},
			},
			{
				Metrics: map[string]float64{
					"load15": 3,
				},
				Timestamp: ts,
				Dimension: map[string]string{
					"module":   "db",
					"location": "guangdong",
				},
			},
		},
	}

	var conv metricV2Converter
	events := make([]define.Event, 0)
	conv.Convert(&define.Record{
		RecordType: define.RecordMetricV2,
		Data:       data,
	}, func(evts ...define.Event) {
		for i := 0; i < len(evts); i++ {
			evt := evts[i]
			assert.Equal(t, define.RecordMetricV2, evt.RecordType())
			events = append(events, evt)
		}
	})

	excepted := []common.MapStr{
		{
			"metrics": map[string]float64{
				"load1": 1,
				"load5": 2,
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"module":   "db",
				"location": "guangdong",
			},
			"timestamp": ts,
		},
		{
			"metrics": map[string]float64{
				"load15": 3,
			},
			"target": define.Identity(),
			"dimension": map[string]string{
				"module":   "db",
				"location": "guangdong",
			},
			"timestamp": ts,
		},
	}

	assert.Len(t, events, 2)
	for i, event := range events {
		assert.Equal(t, excepted[i], event.Data())
	}
}
