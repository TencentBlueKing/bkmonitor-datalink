// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tars

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TarsCloud/TarsGo/tars/protocol/res/propertyf"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/statf"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, func() {
		Ready()
	})
}

func TestPropertyImpl(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		var r *define.Record
		svc := PropertyImpl{
			receiver.Publisher{Func: func(record *define.Record) {
				r = record
			}},
			pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			}},
		}
		props := map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody{
			{
				ModuleName:   "TestApp.HelloGo",
				Ip:           "127.0.0.1",
				PropertyName: "Add",
				IPropertyVer: 2,
			}: {VInfo: []propertyf.StatPropInfo{
				{Value: "440", Policy: "Sum"},
				{Value: "73.333", Policy: "Avg"},
				{Value: "94", Policy: "Max"},
				{Value: "33", Policy: "Min"},
				{Value: "6", Policy: "Count"},
				{Value: "0|0,50|1,100|5", Policy: "Distr"},
			}},
		}

		_, err := svc.ReportPropMsg(context.Background(), props)
		assert.NoErrorf(t, err, "failed to invoke ReportPropMsg, err=%v", err)

		data := r.Data.(*define.TarsData)
		assert.Equal(t, r.RecordType, define.RecordTars)
		assert.Equal(t, r.RequestType, define.RequestTars)
		assert.Equal(t, data.Type, define.TarsPropertyType)

		pd := data.Data.(*define.TarsPropertyData)
		assert.Len(t, pd.Props, 1)
		for head, body := range pd.Props {
			b, err := json.Marshal(head)
			assert.NoErrorf(t, err, "failed to Marshal prop head, err=%v", err)
			expected := `{"moduleName":"TestApp.HelloGo","ip":"127.0.0.1","propertyName":"Add","setName":"","setArea":"","setID":"","sContainer":"","iPropertyVer":2}`
			assert.JSONEq(t, expected, string(b))

			b, err = json.Marshal(body)
			assert.NoErrorf(t, err, "failed to Marshal prop body, err=%v", err)
			expected = `{"vInfo":[{"policy":"Sum","value":"440"},{"policy":"Avg","value":"73.333"},{"policy":"Max","value":"94"},{"policy":"Min","value":"33"},{"policy":"Count","value":"6"},{"policy":"Distr","value":"0|0,50|1,100|5"}]}`
			assert.JSONEq(t, expected, string(b))
		}
	})
}

func TestStatImpl(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		var r *define.Record
		svc := StatImpl{
			receiver.Publisher{Func: func(record *define.Record) {
				r = record
			}},
			pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
				return define.StatusCodeOK, "", nil
			}},
		}
		stats := map[statf.StatMicMsgHead]statf.StatMicMsgBody{
			{
				MasterName:    "stat_from_server",
				SlaveName:     "TestApp.HelloGo",
				InterfaceName: "Add",
				MasterIp:      "127.0.0.1",
				SlaveIp:       "127.0.0.1",
				TarsVersion:   "1.4.5",
			}: {
				Count:         6,
				TimeoutCount:  0,
				ExecCount:     0,
				IntervalCount: map[int32]int32{100: 0, 200: 2, 500: 4},
				TotalRspTime:  1343,
				MaxRspTime:    284,
				MinRspTime:    159,
			},
		}

		_, err := svc.ReportMicMsg(context.Background(), stats, false)
		assert.NoErrorf(t, err, "failed to invoke ReportPropMsg, err=%v", err)

		data := r.Data.(*define.TarsData)
		assert.Equal(t, r.RecordType, define.RecordTars)
		assert.Equal(t, r.RequestType, define.RequestTars)
		assert.Equal(t, data.Type, define.TarsStatType)

		pd := data.Data.(*define.TarsStatData)
		assert.Len(t, pd.Stats, 1)

		for head, body := range pd.Stats {
			b, err := json.Marshal(head)
			assert.NoErrorf(t, err, "failed to Marshal stat head, err=%v", err)
			expected := `{"masterName":"stat_from_server","slaveName":"TestApp.HelloGo","interfaceName":"Add","masterIp":"127.0.0.1","slaveIp":"127.0.0.1","slavePort":0,"returnValue":0,"slaveSetName":"","slaveSetArea":"","slaveSetID":"","tarsVersion":"1.4.5"}`
			assert.JSONEq(t, expected, string(b))

			b, err = json.Marshal(body)
			assert.NoErrorf(t, err, "failed to Marshal stat body, err=%v", err)
			expected = `{"count":6,"timeoutCount":0,"execCount":0,"intervalCount":{"100":0,"200":2,"500":4},"totalRspTime":1343,"maxRspTime":284,"minRspTime":159}`
			assert.JSONEq(t, expected, string(b))
		}
	})
}
