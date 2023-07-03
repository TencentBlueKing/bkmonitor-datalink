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
	deltaKeys := map[string]struct{}{}
	lastDeltaMetrics := make(map[string]map[string]float64)
	for _, key := range []string{"metric1", "metric2"} {
		deltaKeys[key] = struct{}{}
		lastDeltaMetrics[key] = make(map[string]float64)
	}

	mb := &MetricSet{
		deltaKeys:        deltaKeys,
		lastDeltaMetrics: lastDeltaMetrics,
	}

	lines1 := `
metric1{label1="value1"} 10
metric2{label1="value2"} 11
metric3{label1="value3"} 12
`
	ch := mb.getEventsFromReader(io.NopCloser(bytes.NewBufferString(lines1)), func() {}, false)
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
			"key": "metric3",
			"labels": common.MapStr{
				"label1": "value3",
			},
			"value": float64(12),
		},
	}

	index := 0
	for msg := range ch {
		assert.Equal(t, expected[index], msg)
		index++
	}

	lines2 := `
metric1{label1="value1"} 20
metric2{label1="value2"} 21
metric3{label1="value3"} 22
`
	ch = mb.getEventsFromReader(io.NopCloser(bytes.NewBufferString(lines2)), func() {}, false)
	expected = []common.MapStr{
		{
			"key": "metric1",
			"labels": common.MapStr{
				"label1": "value1",
			},
			"value": float64(10),
		},
		{
			"key": "metric2",
			"labels": common.MapStr{
				"label1": "value2",
			},
			"value": float64(10),
		},
		{
			"key": "metric3",
			"labels": common.MapStr{
				"label1": "value3",
			},
			"value": float64(22),
		},
	}

	index = 0
	for msg := range ch {
		assert.Equal(t, expected[index], msg)
		index++
	}
}
