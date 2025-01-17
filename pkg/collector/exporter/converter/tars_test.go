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

	"github.com/TarsCloud/TarsGo/tars/protocol/res/propertyf"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/statf"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

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
		t.Run("input -> "+tt.input, func(t *testing.T) {
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
		{propertyName: "ErrLog:", expect: "ErrLog_count", policy: "Count"},
		{propertyName: "Exception-Log", expect: "Exception_Log_count", policy: "Count"},
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
		t.Run("input -> "+tt.propertyName, func(t *testing.T) {
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

	TarsConverter.Convert(record, func(events ...define.Event) {
		assert.Len(t, events, 20)

		commonDims := map[string]string{
			tarsPropertyTagsIPropertyVer: "2",
			tarsPropertyTagsIp:           "127.0.0.1",
			tarsPropertyTagsModuleName:   "TestApp.HelloGo",
			tarsPropertyTagsPropertyName: "TestApp.HelloGo.TestPropertyName",
			tarsPropertyTagsSContainer:   "container1",
			tarsPropertyTagsSetName:      "name",
			tarsPropertyTagsSetArea:      "area",
			tarsPropertyTagsSetId:        "1",
		}
		customMetricDims := map[string]string{
			resourceTagsScopeName:        "tars_property",
			resourceTagsRPCSystem:        "tars",
			resourceTagsServiceName:      "TestApp.HelloGo",
			resourceTagsInstance:         "127.0.0.1",
			resourceTagsContainerName:    "container1",
			resourceTagsConSetid:         "name.area.1",
			tarsPropertyTagsIPropertyVer: "2",
			tarsPropertyTagsPropertyName: "TestApp.HelloGo.TestPropertyName",
		}
		expects := []common.MapStr{
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Sum"}),
				"metrics":   common.MapStr{"tars_property_sum": float64(440)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Sum"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_sum": float64(440)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Avg"}),
				"metrics":   common.MapStr{"tars_property_avg": 73.333},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Avg"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_avg": 73.333},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Max"}),
				"metrics":   common.MapStr{"tars_property_max": float64(94)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Max"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_max": float64(94)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Min"}),
				"metrics":   common.MapStr{"tars_property_min": float64(33)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Min"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_min": float64(33)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Count"}),
				"metrics":   common.MapStr{"tars_property_count": float64(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Count"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_count": float64(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "0"}),
				"metrics":   common.MapStr{"tars_property_distr_bucket": 0},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "50"}),
				"metrics":   common.MapStr{"tars_property_distr_bucket": 1},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "100"}),
				"metrics":   common.MapStr{"tars_property_distr_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "+Inf"}),
				"metrics":   common.MapStr{"tars_property_distr_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr"}),
				"metrics":   common.MapStr{"tars_property_distr_count": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "0"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_bucket": 0},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "50"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_bucket": 1},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "100"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr", "le": "+Inf"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(customMetricDims, map[string]string{tarsPropertyTagsPropertyPolicy: "Distr"}),
				"metrics":   common.MapStr{"TestApp_HelloGo_TestPropertyName_distr_count": 6},
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
				{
					MasterName:    "TestApp.HelloGo@1.1",
					SlaveName:     "OtherTestApp.HiGo",
					InterfaceName: "Add",
					MasterIp:      "",
					SlaveIp:       "127.0.0.1",
					SlavePort:     0,
					ReturnValue:   0,
					SlaveSetName:  "name",
					SlaveSetArea:  "area",
					SlaveSetID:    "1",
					TarsVersion:   "1.4.5",
				}: {
					Count:        6,
					TimeoutCount: 0,
					ExecCount:    0,
					IntervalCount: map[int32]int32{
						100:  0,
						200:  2,
						500:  4,
						1000: 0,
					},
					TotalRspTime: 1343,
					MaxRspTime:   284,
					MinRspTime:   159,
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

	TarsConverter.Convert(record, func(events ...define.Event) {
		commonDims := map[string]string{
			tarsStatTagsRole:          tarsStatTagsRoleClient,
			tarsStatTagsInterfaceName: "Add",
			tarsStatTagsMasterIp:      "127.0.0.1",
			tarsStatTagsMasterName:    "TestApp.HelloGo@1.1",
			tarsStatTagsReturnValue:   "0",
			tarsStatTagsSlaveIp:       "127.0.0.1",
			tarsStatTagsSlaveName:     "OtherTestApp.HiGo",
			tarsStatTagsSlavePort:     "0",
			tarsStatTagsSlaveSetArea:  "area",
			tarsStatTagsSlaveSetId:    "1",
			tarsStatTagsSlaveSetName:  "name",
			tarsStatTagsTarsVersion:   "1.4.5",
		}
		rpcMetricDims := map[string]string{
			resourceTagsScopeName:       "client_metrics",
			resourceTagsRPCSystem:       "tars",
			resourceTagsServiceName:     "TestApp.HelloGo",
			resourceTagsInstance:        "127.0.0.1",
			resourceTagsVersion:         "1.1",
			rpcMetricTagsCallerServer:   "TestApp.HelloGo",
			rpcMetricTagsCallerIp:       "127.0.0.1",
			rpcMetricTagsCalleeServer:   "OtherTestApp.HiGo",
			rpcMetricTagsCalleeIp:       "127.0.0.1",
			rpcMetricTagsUserExt1:       "0",
			rpcMetricTagsCalleeMethod:   "Add",
			rpcMetricTagsCalleeConSetid: "name.area.1",
			rpcMetricTagsCode:           "0",
		}

		expects := []common.MapStr{
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"le": "0.1"}),
				"metrics":   common.MapStr{"tars_request_duration_seconds_bucket": 0},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"le": "0.2"}),
				"metrics":   common.MapStr{"tars_request_duration_seconds_bucket": 2},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"le": "0.5"}),
				"metrics":   common.MapStr{"tars_request_duration_seconds_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"le": "1"}),
				"metrics":   common.MapStr{"tars_request_duration_seconds_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"le": "+Inf"}),
				"metrics":   common.MapStr{"tars_request_duration_seconds_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, nil),
				"metrics":   common.MapStr{"tars_request_duration_seconds_count": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, nil),
				"metrics": common.MapStr{
					"tars_timeout_total":                int32(0),
					"tars_requests_total":               int32(6),
					"tars_exceptions_total":             int32(0),
					"tars_request_duration_seconds_max": 0.284,
					"tars_request_duration_seconds_min": 0.159,
					"tars_request_duration_seconds_sum": 1.343,
				},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{rpcMetricTagsCodeType: rpcMetricTagsCodeTypeSuccess}),
				"metrics":   common.MapStr{"rpc_client_handled_total": int32(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{rpcMetricTagsCodeType: rpcMetricTagsCodeTypeException}),
				"metrics":   common.MapStr{"rpc_client_handled_total": int32(0)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{rpcMetricTagsCodeType: rpcMetricTagsCodeTypeTimeout}),
				"metrics":   common.MapStr{"rpc_client_handled_total": int32(0)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{"le": "0.1", rpcMetricTagsCodeType: rpcMetricTagsCodeTypeSuccess}),
				"metrics":   common.MapStr{"rpc_client_handled_seconds_bucket": 0},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{"le": "0.2", rpcMetricTagsCodeType: rpcMetricTagsCodeTypeSuccess}),
				"metrics":   common.MapStr{"rpc_client_handled_seconds_bucket": 2},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{"le": "0.5", rpcMetricTagsCodeType: rpcMetricTagsCodeTypeSuccess}),
				"metrics":   common.MapStr{"rpc_client_handled_seconds_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{"le": "1", rpcMetricTagsCodeType: rpcMetricTagsCodeTypeSuccess}),
				"metrics":   common.MapStr{"rpc_client_handled_seconds_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{"le": "+Inf", rpcMetricTagsCodeType: rpcMetricTagsCodeTypeSuccess}),
				"metrics":   common.MapStr{"rpc_client_handled_seconds_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{rpcMetricTagsCodeType: rpcMetricTagsCodeTypeSuccess}),
				"metrics":   common.MapStr{"rpc_client_handled_seconds_count": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(rpcMetricDims, map[string]string{rpcMetricTagsCodeType: rpcMetricTagsCodeTypeSuccess}),
				"metrics":   common.MapStr{"rpc_client_handled_seconds_sum": 1.343},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
		}

		for idx, event := range events {
			assert.Equal(t, expects[idx], event.Data())
		}
	})
}
