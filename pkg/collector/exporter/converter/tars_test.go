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

func TestTarsProperty(t *testing.T) {
	data := &define.TarsData{
		Type:      define.TarsPropertyType,
		Timestamp: 1719417736,
		Data: &define.TarsPropertyData{
			Props: map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody{
				{
					ModuleName:   "TestApp.HelloGo",
					Ip:           "127.0.0.1",
					PropertyName: "Add",
					SetName:      "",
					SetArea:      "",
					SetID:        "",
					SContainer:   "",
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
		assert.Len(t, events, 10)

		commonDims := map[string]string{
			"i_property_ver": "2",
			"ip":             "127.0.0.1",
			"module_name":    "TestApp.HelloGo",
			"property_name":  "Add",
			"s_container":    "",
			"set_area":       "",
			"set_name":       "",
		}
		expects := []common.MapStr{
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Sum"}),
				"metrics":   common.MapStr{"tars_property_sum": float64(440)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Avg"}),
				"metrics":   common.MapStr{"tars_property_avg": 73.333},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Max"}),
				"metrics":   common.MapStr{"tars_property_max": float64(94)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Min"}),
				"metrics":   common.MapStr{"tars_property_min": float64(33)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Count"}),
				"metrics":   common.MapStr{"tars_property_count": float64(6)},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Distr", "le": "0"}),
				"metrics":   common.MapStr{"tars_property_distr_bucket": 0},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Distr", "le": "50"}),
				"metrics":   common.MapStr{"tars_property_distr_bucket": 1},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Distr", "le": "100"}),
				"metrics":   common.MapStr{"tars_property_distr_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Distr", "le": "+Inf"}),
				"metrics":   common.MapStr{"tars_property_distr_bucket": 6},
				"target":    "127.0.0.1",
				"timestamp": int64(1719417736),
			},
			{
				"dimension": utils.MergeMaps(commonDims, map[string]string{"property_policy": "Distr"}),
				"metrics":   common.MapStr{"tars_property_distr_count": 6},
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
			FromClient: false,
			Stats: map[statf.StatMicMsgHead]statf.StatMicMsgBody{
				{
					MasterName:    "stat_from_server",
					SlaveName:     "TestApp.HelloGo",
					InterfaceName: "Add",
					MasterIp:      "127.0.0.1",
					SlaveIp:       "127.0.0.1",
					SlavePort:     0,
					ReturnValue:   0,
					SlaveSetName:  "",
					SlaveSetArea:  "",
					SlaveSetID:    "",
					TarsVersion:   "1.4.5",
				}: {
					Count:        6,
					TimeoutCount: 0,
					ExecCount:    0,
					IntervalCount: map[int32]int32{
						5:    0,
						10:   0,
						50:   0,
						100:  0,
						200:  2,
						500:  4,
						1000: 0,
						2000: 0,
						3000: 0,
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
		assert.Len(t, events, 12)
		expect := common.MapStr{
			"dimension": map[string]string{
				"role":           "server",
				"interface_name": "Add",
				"master_ip":      "127.0.0.1",
				"master_name":    "stat_from_server",
				"return_value":   "0",
				"slave_ip":       "127.0.0.1",
				"slave_name":     "TestApp.HelloGo",
				"slave_port":     "0",
				"slave_set_area": "",
				"slave_set_id":   "",
				"slave_set_name": "",
				"tars_version":   "1.4.5",
			},
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
		}
		assert.Equal(t, expect, events[11].Data())
	})
}
