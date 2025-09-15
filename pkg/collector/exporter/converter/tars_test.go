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
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/TarsCloud/TarsGo/tars/protocol/res/propertyf"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/statf"
	"github.com/spf13/cast"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

var statMicMsgHead = statf.StatMicMsgHead{
	MasterName:    "TestApp.HelloGo@1.1",
	SlaveName:     "OtherTestApp.HiGo",
	InterfaceName: "Add",
	MasterIp:      "",
	SlaveIp:       "0.0.0.0",
	ReturnValue:   0,
	SlaveSetName:  "name",
	SlaveSetArea:  "area",
	SlaveSetID:    "1",
	TarsVersion:   "1.4.5",
}

var rpcClientMetricDims = map[string]string{
	resourceTagsScopeName:      "client_metrics",
	resourceTagsRPCSystem:      "tars",
	resourceTagsServiceName:    "TestApp.HelloGo",
	resourceTagsInstance:       "127.0.0.1",
	resourceTagsVersion:        "1.1",
	rpcMetricTagsCallerServer:  "TestApp.HelloGo",
	rpcMetricTagsCallerService: "TestApp.HelloGo",
	rpcMetricTagsCallerIp:      "127.0.0.1",
	rpcMetricTagsCalleeServer:  "OtherTestApp.HiGo",
	rpcMetricTagsCalleeService: "OtherTestApp.HiGo",
	rpcMetricTagsCalleeIp:      "0.0.0.0",
	rpcMetricTagsCalleeMethod:  "Add",
	rpcMetricTagsCodeType:      rpcMetricTagsCodeTypeSuccess,
	rpcMetricTagsCode:          "0",
}

var rpcServerMetricDims = map[string]string{
	resourceTagsScopeName:      "server_metrics",
	resourceTagsRPCSystem:      "tars",
	resourceTagsServiceName:    "OtherTestApp.HiGo",
	resourceTagsInstance:       "0.0.0.0",
	resourceTagsVersion:        "1.1",
	rpcMetricTagsCallerServer:  "TestApp.HelloGo",
	rpcMetricTagsCallerService: "TestApp.HelloGo",
	rpcMetricTagsCallerIp:      "127.0.0.1",
	rpcMetricTagsCalleeServer:  "OtherTestApp.HiGo",
	rpcMetricTagsCalleeService: "OtherTestApp.HiGo",
	rpcMetricTagsCalleeIp:      "0.0.0.0",
	rpcMetricTagsCalleeMethod:  "Add",
	rpcMetricTagsCodeType:      rpcMetricTagsCodeTypeSuccess,
	rpcMetricTagsCode:          "0",
}

func TestSplitAtLastOnce(t *testing.T) {
	tests := []struct {
		input       string
		separator   string
		expectLeft  string
		expectRight string
	}{
		{input: "", separator: "@", expectLeft: "", expectRight: ""},
		{input: "@", separator: "@", expectLeft: "", expectRight: ""},
		{input: "left@", separator: "@", expectLeft: "left", expectRight: ""},
		{input: "left@right", separator: "@", expectLeft: "left", expectRight: "right"},
		{input: "left1@left2@right", separator: "@", expectLeft: "left1@left2", expectRight: "right"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			actualLeft, actualRight := splitAtLastOnce(tt.input, tt.separator)
			assert.Equal(t, tt.expectLeft, actualLeft)
			assert.Equal(t, tt.expectRight, actualRight)
		})
	}
}

func TestPropNameToNormalizeMetricName(t *testing.T) {
	tests := []struct {
		propertyName string
		expect       string
		policy       string
	}{
		{
			propertyName: "ErrLog:",
			expect:       "ErrLog_count",
			policy:       "Count",
		},
		{
			propertyName: "Exception-Log",
			expect:       "Exception_Log_count",
			policy:       "Count",
		},
		{
			propertyName: "TestApp.HelloGo.HelloGoObjAdapter.connectRate",
			expect:       "TestApp_HelloGo_HelloGoObjAdapter_connectRate_count",
			policy:       "Count",
		},
		{
			propertyName: "TestApp.HelloGo.exception_single_log_more_than_3M",
			expect:       "TestApp_HelloGo_exception_single_log_more_than_3M_count",
			policy:       "Count",
		},
	}

	for _, tt := range tests {
		t.Run(tt.propertyName, func(t *testing.T) {
			assert.Equal(t, propNameToNormalizeMetricName(tt.propertyName, tt.policy), tt.expect)
		})
	}
}

func TestTarsProperty(t *testing.T) {
	data := &define.TarsData{
		Type:      define.TarsPropertyType,
		Timestamp: 1719417736,
		Data: &define.TarsPropertyData{
			Props: map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody{
				{
					ModuleName:   "TestApp.HelloGo",
					Ip:           "127.0.0.1",
					PropertyName: "TestApp.HelloGo.TestPropertyName",
					SetName:      "name",
					SetArea:      "area",
					SetID:        "1",
					SContainer:   "container1",
					IPropertyVer: 2,
				}: {VInfo: []propertyf.StatPropInfo{
					{Value: "440", Policy: "Sum"},
					{Value: "73.333", Policy: "Avg"},
					{Value: "94", Policy: "Max"},
					{Value: "33", Policy: "Min"},
					{Value: "6", Policy: "Count"},
					{Value: "0|0,50|1,100|5", Policy: "Distr"},
				}},
			},
		},
	}
	record := &define.Record{
		RecordType:    define.RecordTars,
		RequestType:   define.RequestTars,
		RequestClient: define.RequestClient{IP: "127.0.0.1"},
		Token:         define.Token{Original: "xxx", MetricsDataId: 123},
		Data:          data,
	}

	conv := newTarsConverter(&TarsConfig{DisableAggregate: true})
	conv.Convert(record, func(events ...define.Event) {
		assert.Len(t, events, 10)

		commonDims := map[string]string{
			resourceTagsScopeName:        "tars_property",
			resourceTagsRPCSystem:        "tars",
			resourceTagsServiceName:      "TestApp.HelloGo",
			resourceTagsInstance:         "127.0.0.1",
			resourceTagsContainerName:    "container1",
			tarsPropertyTagsIPropertyVer: "2",
			tarsPropertyTagsPropertyName: "TestApp.HelloGo.TestPropertyName",
		}
		expects := []common.MapStr{
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Sum"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_sum": float64(440)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Avg"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_avg": 73.333},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Max"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_max": float64(94)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Min"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_min": float64(33)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Count"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_count": float64(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "0"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_bucket": int32(0)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "50"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_bucket": int32(1)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "100"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_bucket": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "+Inf"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_bucket": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_count": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
		}

		for idx, event := range events {
			assert.Equal(t, expects[idx], event.Data())
		}
	})
}

func TestTarsStat(t *testing.T) {
	data := &define.TarsData{
		Type:      define.TarsStatType,
		Timestamp: 1719417736,
		Data: &define.TarsStatData{
			FromClient: true,
			Stats: map[statf.StatMicMsgHead]statf.StatMicMsgBody{
				statMicMsgHead: {
					Count:         6,
					TimeoutCount:  0,
					ExecCount:     0,
					IntervalCount: map[int32]int32{100: 0, 200: 2, 500: 4, 1000: 0},
					TotalRspTime:  1343,
				},
			},
		},
	}
	record := &define.Record{
		RecordType:    define.RecordTars,
		RequestType:   define.RequestTars,
		RequestClient: define.RequestClient{IP: "127.0.0.1"},
		Token:         define.Token{Original: "xxx", MetricsDataId: 123},
		Data:          data,
	}

	conv := newTarsConverter(&TarsConfig{DisableAggregate: true})
	conv.Convert(record, func(events ...define.Event) {
		expects := []common.MapStr{
			{
				"dimension": rpcClientMetricDims,
				"metrics":   common.MapStr{"origin_rpc_client_handled_total": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcClientMetricDims, map[string]string{"le": "0.1"}),
				"metrics":   common.MapStr{"origin_rpc_client_handled_seconds_bucket": int32(0)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcClientMetricDims, map[string]string{"le": "0.2"}),
				"metrics":   common.MapStr{"origin_rpc_client_handled_seconds_bucket": int32(2)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcClientMetricDims, map[string]string{"le": "0.5"}),
				"metrics":   common.MapStr{"origin_rpc_client_handled_seconds_bucket": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcClientMetricDims, map[string]string{"le": "1"}),
				"metrics":   common.MapStr{"origin_rpc_client_handled_seconds_bucket": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcClientMetricDims, map[string]string{"le": "+Inf"}),
				"metrics":   common.MapStr{"origin_rpc_client_handled_seconds_bucket": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": rpcClientMetricDims,
				"metrics":   common.MapStr{"origin_rpc_client_handled_seconds_count": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": rpcClientMetricDims,
				"metrics":   common.MapStr{"origin_rpc_client_handled_seconds_sum": 1.343},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": rpcServerMetricDims,
				"metrics":   common.MapStr{"origin_rpc_server_handled_total": int32(6)},
				"target":    "0.0.0.0",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcServerMetricDims, map[string]string{"le": "0.1"}),
				"metrics":   common.MapStr{"origin_rpc_server_handled_seconds_bucket": int32(0)},
				"target":    "0.0.0.0",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcServerMetricDims, map[string]string{"le": "0.2"}),
				"metrics":   common.MapStr{"origin_rpc_server_handled_seconds_bucket": int32(2)},
				"target":    "0.0.0.0",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcServerMetricDims, map[string]string{"le": "0.5"}),
				"metrics":   common.MapStr{"origin_rpc_server_handled_seconds_bucket": int32(6)},
				"target":    "0.0.0.0",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcServerMetricDims, map[string]string{"le": "1"}),
				"metrics":   common.MapStr{"origin_rpc_server_handled_seconds_bucket": int32(6)},
				"target":    "0.0.0.0",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcServerMetricDims, map[string]string{"le": "+Inf"}),
				"metrics":   common.MapStr{"origin_rpc_server_handled_seconds_bucket": int32(6)},
				"target":    "0.0.0.0",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": rpcServerMetricDims,
				"metrics":   common.MapStr{"origin_rpc_server_handled_seconds_count": int32(6)},
				"target":    "0.0.0.0",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": rpcServerMetricDims,
				"metrics":   common.MapStr{"origin_rpc_server_handled_seconds_sum": 1.343},
				"target":    "0.0.0.0",
				"timestamp": int64(1719417736),
			},
		}

		assert.Len(t, events, 16)
		for idx, event := range events {
			assert.Equal(t, expects[idx], event.Data())
		}
	})
}

func TestTarsStatAggregate(t *testing.T) {
	var totalEvents []define.Event
	gatherFunc := func(events ...define.Event) {
		totalEvents = append(totalEvents, events...)
	}

	conv := newTarsConverter(&TarsConfig{IsDropOriginal: true})
	defer conv.Clean()

	for i := 0; i < 100000; i++ {
		data := &define.TarsData{
			Type:      define.TarsStatType,
			Timestamp: 1719417736,
			Data: &define.TarsStatData{
				FromClient: true,
				Stats: map[statf.StatMicMsgHead]statf.StatMicMsgBody{
					statMicMsgHead: {
						Count:         4,
						TimeoutCount:  0,
						ExecCount:     0,
						IntervalCount: map[int32]int32{100: 1, 200: 1, 500: 1, 1000: 1},
						TotalRspTime:  1,
					},
				},
			},
		}
		record := &define.Record{
			RecordType:    define.RecordTars,
			RequestType:   define.RequestTars,
			RequestClient: define.RequestClient{IP: "127.0.0.1"},
			Token:         define.Token{Original: "xxx", MetricsDataId: 123},
			Data:          data,
		}
		conv.Convert(record, gatherFunc)
	}

	// 等待一段时间，确保所有事件都被处理
	time.Sleep(500 * time.Millisecond)

	metricAggregateSumMap := make(map[string]float64)
	for _, event := range totalEvents {
		for metric, value := range event.Data()["metrics"].(common.MapStr) {
			metricAggregateSumMap[metric] += cast.ToFloat64(value)
		}
	}

	for metric, value := range metricAggregateSumMap {
		// 浮点数求和可能会有精度问题，这里使用 math.Trunc 来对浮点数进行截断，去掉不可能达到的精度。
		shift := math.Pow10(10)
		metricAggregateSumMap[metric] = cast.ToFloat64(fmt.Sprintf("%.9f", math.Trunc(value*shift)/shift))
	}

	expected := map[string]float64{
		"rpc_client_handled_seconds_sum":    100,
		"rpc_client_handled_seconds_count":  400000,
		"rpc_client_handled_seconds_bucket": 1400000,
		"rpc_client_handled_total":          400000,
		"rpc_server_handled_seconds_sum":    100,
		"rpc_server_handled_seconds_count":  400000,
		"rpc_server_handled_seconds_bucket": 1400000,
		"rpc_server_handled_total":          400000,
	}
	assert.Equal(t, expected, metricAggregateSumMap)
}

// BenchmarkTarsStat 基准测试 TarsStat 转换性能
func BenchmarkTarsStat(b *testing.B) {
	data := &define.TarsData{
		Type:      define.TarsStatType,
		Timestamp: 1719417736,
		Data: &define.TarsStatData{
			FromClient: true,
			Stats: map[statf.StatMicMsgHead]statf.StatMicMsgBody{
				statMicMsgHead: {
					Count:        6,
					TimeoutCount: 0,
					ExecCount:    0,
					IntervalCount: map[int32]int32{
						100:  0,
						200:  1,
						500:  1,
						1000: 0,
						2000: 0,
						3000: 10,
						4000: 0,
					},
					TotalRspTime: 1343,
				},
			},
		},
	}
	record := &define.Record{
		RecordType:    define.RecordTars,
		RequestType:   define.RequestTars,
		RequestClient: define.RequestClient{IP: "127.0.0.1"},
		Token:         define.Token{Original: "xxx", MetricsDataId: 123},
		Data:          data,
	}

	conv := newTarsConverter(nil)
	defer conv.Clean()

	for i := 0; i < b.N; i++ {
		conv.Convert(record, func(events ...define.Event) {})
	}
}

// BenchmarkTarsProperty 基准测试 TarsProperty 转换性能
func BenchmarkTarsProperty(b *testing.B) {
	data := &define.TarsData{
		Type:      define.TarsPropertyType,
		Timestamp: 1719417736,
		Data: &define.TarsPropertyData{
			Props: map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody{
				{
					ModuleName:   "TestApp.HelloGo",
					Ip:           "127.0.0.1",
					PropertyName: "TestApp.HelloGo.TestPropertyName",
					SetName:      "name",
					SetArea:      "area",
					SetID:        "1",
					SContainer:   "container1",
					IPropertyVer: 2,
				}: {VInfo: []propertyf.StatPropInfo{
					{Value: "440", Policy: "Sum"},
					{Value: "73.333", Policy: "Avg"},
					{Value: "94", Policy: "Max"},
					{Value: "33", Policy: "Min"},
					{Value: "6", Policy: "Count"},
					{Value: "0|0,50|1,100|5", Policy: "Distr"},
				}},
			},
		},
	}
	record := &define.Record{
		RecordType:    define.RecordTars,
		RequestType:   define.RequestTars,
		RequestClient: define.RequestClient{IP: "127.0.0.1"},
		Token:         define.Token{Original: "xxx", MetricsDataId: 123},
		Data:          data,
	}

	conv := newTarsConverter(&TarsConfig{DisableAggregate: true})
	for i := 0; i < b.N; i++ {
		conv.Convert(record, func(events ...define.Event) {})
	}
}
