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
	"encoding/json"
	"testing"

	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func unmarshalProxyData(s string) interface{} {
	var data interface{}
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		panic(err)
	}
	return data
}

const (
	proxyEventData = `
[{
	"event_name": "event1",
	"event": {
		"content": "user foo login failed"
	},
	"target": "127.0.0.1",
	"dimension": {
		"module": "db",
		"location": "guangdong",
		"event_type": "abnormal"
	},
	"timestamp": 1709899044852
},
{
	"event_name": "event2",
	"event": {
		"content": "user bar login failed"
	},
	"target": "127.0.0.1",
	"dimension": {
		"module": "db",
		"location": "shenzhen",
		"event_type": "abnormal"
	},
	"timestamp": 1709899044853
}]
`
)

func TestConvertProxyEventsData(t *testing.T) {
	pd := &define.ProxyData{
		DataId:      1001,
		AccessToken: "1000_accesstoken",
		Version:     "v2",
		Type:        define.ProxyEventType,
		Data:        unmarshalProxyData(proxyEventData),
	}

	events := make([]define.Event, 0)
	var conv proxyConverter
	conv.Convert(&define.Record{
		RecordType: define.RecordProxy,
		Data:       pd,
	}, func(evts ...define.Event) {
		for i := 0; i < len(evts); i++ {
			evt := evts[i]
			assert.Equal(t, define.RecordProxy, evt.RecordType())
			assert.Equal(t, int32(1001), evt.DataId())
			events = append(events, evt)
		}
	})

	excepted := []common.MapStr{
		{
			"event_name": "event1",
			"event": map[string]interface{}{
				"content": "user foo login failed",
			},
			"target": "127.0.0.1",
			"dimension": map[string]string{
				"module":     "db",
				"location":   "guangdong",
				"event_type": "abnormal",
			},
			"timestamp": int64(1709899044852),
		},
		{
			"event_name": "event2",
			"event": map[string]interface{}{
				"content": "user bar login failed",
			},
			"target": "127.0.0.1",
			"dimension": map[string]string{
				"module":     "db",
				"location":   "shenzhen",
				"event_type": "abnormal",
			},
			"timestamp": int64(1709899044853),
		},
	}

	assert.Len(t, events, 2)
	for i, event := range events {
		assert.Equal(t, excepted[i], event.Data())
	}
}

func BenchmarkConvertProxyEventsData(b *testing.B) {
	pd := &define.ProxyData{
		DataId:      1001,
		AccessToken: "1000_accesstoken",
		Version:     "v2",
		Type:        define.ProxyEventType,
		Data:        unmarshalProxyData(proxyEventData),
	}

	var conv proxyConverter
	for i := 0; i < b.N; i++ {
		conv.Convert(&define.Record{
			RecordType: define.RecordProxy,
			Data:       pd,
		}, func(evts ...define.Event) {})
	}
}

const (
	proxyMetricData = `
[{
	"metrics": {
		"load1": 1
	},
	"target": "127.0.0.1",
	"dimension": {
		"module": "db",
		"location": "guangdong",
		"event_type": "abnormal"
	},
	"timestamp": 1709899044852
},
{
	"metrics": {
		"load5": 2
	},
	"target": "127.0.0.1",
	"dimension": {
		"module": "db",
		"location": "shenzhen",
		"event_type": "abnormal"
	},
	"timestamp": 1709899044853
}]
`
)

func TestConvertProxyMetricsData(t *testing.T) {
	pd := &define.ProxyData{
		DataId:      1001,
		AccessToken: "1000_accesstoken",
		Version:     "v2",
		Type:        define.ProxyMetricType,
		Data:        unmarshalProxyData(proxyMetricData),
	}

	var conv proxyConverter
	events := make([]define.Event, 0)
	conv.Convert(&define.Record{
		RecordType: define.RecordProxy,
		Data:       pd,
	}, func(evts ...define.Event) {
		for i := 0; i < len(evts); i++ {
			evt := evts[i]
			assert.Equal(t, define.RecordProxy, evt.RecordType())
			assert.Equal(t, int32(1001), evt.DataId())
			events = append(events, evt)
		}
	})

	excepted := []common.MapStr{
		{
			"metrics": map[string]float64{
				"load1": 1,
			},
			"target": "127.0.0.1",
			"dimension": map[string]string{
				"module":     "db",
				"location":   "guangdong",
				"event_type": "abnormal",
			},
			"timestamp": int64(1709899044852),
		},
		{
			"metrics": map[string]float64{
				"load5": 2,
			},
			"target": "127.0.0.1",
			"dimension": map[string]string{
				"module":     "db",
				"location":   "shenzhen",
				"event_type": "abnormal",
			},
			"timestamp": int64(1709899044853),
		},
	}

	assert.Len(t, events, 2)
	for i, event := range events {
		assert.Equal(t, excepted[i], event.Data())
	}
}

func BenchmarkConvertProxyMetricsData(b *testing.B) {
	pd := &define.ProxyData{
		DataId:      1001,
		AccessToken: "1000_accesstoken",
		Version:     "v2",
		Type:        define.ProxyMetricType,
		Data:        unmarshalProxyData(proxyMetricData),
	}

	var conv proxyConverter
	for i := 0; i < b.N; i++ {
		conv.Convert(&define.Record{
			RecordType: define.RecordProxy,
			Data:       pd,
		}, func(evts ...define.Event) {})
	}
}

func TestConvertMarshalFailed(t *testing.T) {
	var conv proxyConverter
	t.Run("EventType", func(t *testing.T) {
		pd := &define.ProxyData{
			DataId:      1001,
			AccessToken: "1000_accesstoken",
			Type:        define.ProxyEventType,
			Data:        "{-}event",
		}

		var seen bool
		conv.Convert(&define.Record{
			RecordType: define.RecordProxy,
			Data:       pd,
		}, func(evts ...define.Event) {
			for i := 0; i < len(evts); i++ {
				seen = true
			}
		})
		assert.False(t, seen)
	})

	t.Run("MetricType", func(t *testing.T) {
		pd := &define.ProxyData{
			DataId:      1002,
			AccessToken: "1000_accesstoken",
			Type:        define.ProxyMetricType,
			Data:        "{-}metric",
		}

		var seen bool
		conv.Convert(&define.Record{
			RecordType: define.RecordProxy,
			Data:       pd,
		}, func(evts ...define.Event) {
			for i := 0; i < len(evts); i++ {
				seen = true
			}
		})
		assert.False(t, seen)
	})
}
