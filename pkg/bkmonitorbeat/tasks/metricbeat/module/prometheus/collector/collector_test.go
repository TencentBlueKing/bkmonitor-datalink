// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package collector

import (
	"bytes"
	"io"
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"
)

func TestGetEventFromPromEvent(t *testing.T) {
	mb := &MetricSet{
		normalizeMetricName: true,
	}

	lines1 := `
metric1{label1="value1"} 10
metric2{label1="value2"} 11
metric3:foo:bar{label1="value3"} 12
`
	ch := mb.getEventsFromReader(io.NopCloser(bytes.NewBufferString(lines1)), func() {}, true)
	expected := []common.MapStr{
		{
			"key": "metric1",
			"labels": common.MapStr{
				"label1": "value1",
			},
		},
		{
			"key": "metric2",
			"labels": common.MapStr{
				"label1": "value2",
			},
		},
		{
			"key": "metric3_foo_bar",
			"labels": common.MapStr{
				"label1": "value3",
			},
			"value": float64(12),
		},
		{
			"key":    "bkm_metricbeat_scrape_line",
			"labels": common.MapStr{},
			"value":  float64(3),
		},
		{
			"key": "bkm_metricbeat_endpoint_up",
			"labels": common.MapStr{
				"code":      "0",
				"code_name": "成功",
			},
			"value": float64(1),
		},
		{
			"key":    "bkm_metricbeat_handle_duration_seconds",
			"labels": common.MapStr{},
			"value":  float64(0.1),
		},
	}

	index := 0
	for msg := range ch {
		assert.Equal(t, expected[index]["key"], msg["key"])
		t.Log(msg)
		_, ok := msg["timestamp"]
		assert.True(t, ok)
		index++
	}
}
