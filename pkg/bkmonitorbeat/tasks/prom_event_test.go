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

	"github.com/stretchr/testify/assert"
)

func TestDecodePromEvent(t *testing.T) {
	const ts = 1637839803000

	tests := []struct {
		Line       string
		Name       string
		Value      float64
		Timestamp  int64
		Exemplar   map[string]string
		ExemplarTs int64
	}{
		{
			Line:      `my_histogram_bucketx{le="0.25"} 205.5`,
			Name:      "my_histogram_bucketx",
			Value:     205.5,
			Timestamp: ts,
		},
		{
			Line:      `my_count{le="0.25"} 205 # {traceID="my_trace_id",spanID="my_span_id"} 0.15`,
			Name:      "my_count",
			Value:     205,
			Timestamp: ts,
			Exemplar: map[string]string{
				"traceID": "my_trace_id",
				"spanID":  "my_span_id",
			},
		},
		{
			Line:      `my_bucket{le="0.25"} 205 # {trace_id="my_trace_id",span_id="my_span_id"} 0.15`,
			Name:      "my_bucket",
			Value:     205,
			Timestamp: ts,
			Exemplar: map[string]string{
				"trace_id": "my_trace_id",
				"span_id":  "my_span_id",
			},
		},
		{
			Line:      `my_histogram_bucket{le="0.25"} 205 1637839802 # {traceID="my_trace_id",spanID="my_span_id"} 0.15`,
			Name:      "my_histogram_bucket",
			Value:     205,
			Timestamp: 1637839802000,
			Exemplar: map[string]string{
				"traceID": "my_trace_id",
				"spanID":  "my_span_id",
			},
		},
		{
			Line:      `my_histogram_bucket{le="0.25"} 205 1637839802 # {traceID="my_trace_id",spanID="my_span_id"} 0.15 1637839806000`,
			Name:      "my_histogram_bucket",
			Value:     205,
			Timestamp: 1637839802000,
			Exemplar: map[string]string{
				"traceID": "my_trace_id",
				"spanID":  "my_span_id",
			},
			ExemplarTs: 1637839806000000,
		},
		{
			Line:      `my_histogram_bucket{le="0.25",} 205 1637839802 # {traceID="my_trace_id",spanID="my_span_id"} 0.15 1637839806000`, // backwards v1
			Name:      "my_histogram_bucket",
			Value:     205,
			Timestamp: 1637839802,
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			event, err := NewPromEvent(tt.Line, ts, time.Second, func(_ int64, t int64, _ time.Duration) int64 {
				return t
			})
			assert.NoError(t, err)
			assert.Equal(t, tt.Timestamp, event.TS)
			assert.Equal(t, tt.Value, event.Value)
			assert.Equal(t, tt.Name, event.Key)

			if len(tt.Exemplar) > 0 {
				m := make(map[string]string)
				for _, lb := range event.Exemplar.Labels {
					m[lb.Name] = lb.Value
				}
				assert.Equal(t, tt.Exemplar, m)
				assert.Equal(t, tt.ExemplarTs, event.Exemplar.Ts)
			} else {
				assert.Nil(t, event.Exemplar)
			}
		})
	}
}
