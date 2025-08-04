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

func TestPromEventExemplars(t *testing.T) {
	line := `my_count{le="0.25"} 205 # {traceID="my_trace_id",spanID="my_span_id"} 0.15`
	event, err := NewPromEvent(line, 1637839803, time.Second, func(_ int64, ts int64, _ time.Duration) int64 {
		return ts
	})

	assert.NoError(t, err)
	assert.Equal(t, "my_count", event.Key)
	assert.Equal(t, 1, len(event.Labels))
	assert.Equal(t, "0.25", event.Labels["le"].(string))

	assert.Equal(t, 2, len(event.Exemplar.Labels))
	assert.Equal(t, "spanID", event.Exemplar.Labels[0].Name)
	assert.Equal(t, "my_span_id", event.Exemplar.Labels[0].Value)
	assert.Equal(t, "traceID", event.Exemplar.Labels[1].Name)
	assert.Equal(t, "my_trace_id", event.Exemplar.Labels[1].Value)
	assert.Equal(t, 0.15, event.Exemplar.Value)
}

func TestPromEventTs(t *testing.T) {
	timeOffset := 24 * time.Hour * 365 * 200
	tsHandler, _ := GetTimestampHandler("s")

	t.Run("Without Timestamp", func(t *testing.T) {
		line := `my_histogram_bucketx{le="0.25"} 205.5`
		nowTs := int64(1637839803000) // 设定为当前时间
		event, err := NewPromEvent(line, nowTs, timeOffset, tsHandler)
		assert.NoError(t, err)
		assert.Equal(t, nowTs/1000, event.GetTimestamp())
	})

	t.Run("With Timestamp", func(t *testing.T) {
		line := `my_histogram_bucketx{le="0.25"} 205.5 1637839804000`
		nowTs := int64(1637839803000)
		event, err := NewPromEvent(line, nowTs, timeOffset, tsHandler)
		assert.NoError(t, err)
		assert.Equal(t, int64(1637839804), event.GetTimestamp())
	})
}

func TestPromEvent(t *testing.T) {
	line := `my_histogram_bucketx{le="0.25"} 205.5`
	event, err := NewPromEvent(line, 1637839803, time.Second, func(_ int64, ts int64, _ time.Duration) int64 {
		return ts
	})
	if err != nil {
		panic(err)
	}

	assert.NoError(t, err)
	assert.Equal(t, 205.5, event.Value)
	assert.Equal(t, 1, len(event.Labels))
	assert.Equal(t, "0.25", event.Labels["le"].(string))
	assert.Equal(t, "my_histogram_bucketx", event.Key)
}

func TestPromEventWithTs(t *testing.T) {
	line := `my_histogram_bucket{le="0.25"} 205 1637839802 # {traceID="my_trace_id",spanID="my_span_id"} 0.15 1637839806000`
	event, err := NewPromEvent(line, 1637839803, time.Second, func(_ int64, ts int64, _ time.Duration) int64 {
		return ts
	})
	if err != nil {
		panic(err)
	}

	assert.NoError(t, err)
	assert.Equal(t, event.Key, "my_histogram_bucket")
	assert.Equal(t, int64(1637839802000), event.TS)
	assert.Equal(t, float64(205), event.Value)
	assert.Equal(t, "my_span_id", event.Exemplar.Labels[0].Value)
	assert.Equal(t, "my_trace_id", event.Exemplar.Labels[1].Value)
	assert.Equal(t, 0.15, event.Exemplar.Value)
	assert.Equal(t, int64(1637839806000000), event.Exemplar.Ts)
}

func TestPromEventV1(t *testing.T) {
	line := `my_histogram_bucketx{le="0.25",} 205.5`
	event, err := NewPromEvent(line, 1637839803, time.Second, func(_ int64, ts int64, _ time.Duration) int64 {
		return ts
	})
	if err != nil {
		panic(err)
	}

	assert.NoError(t, err)
	assert.Equal(t, 205.5, event.Value)
	assert.Equal(t, 1, len(event.Labels))
	assert.Equal(t, "0.25", event.Labels["le"].(string))
	assert.Equal(t, "my_histogram_bucketx", event.Key)
}

func TestPromEventV2(t *testing.T) {
	// 带 exemplar 的不支持 "," 结尾的格式
	line := `my_histogram_bucket{le="0.25",} 205 1637839802 # {traceID="my_trace_id",spanID="my_span_id"} 0.15 1637839806000`
	_, err := NewPromEvent(line, 1637839803, time.Second, func(_ int64, ts int64, _ time.Duration) int64 {
		return ts
	})
	assert.Error(t, err)
}
