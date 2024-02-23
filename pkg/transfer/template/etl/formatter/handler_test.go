// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/formatter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

type handlerCase struct {
	pass   int
	err    interface{}
	record define.ETLRecord
}

// HandlerSuite :
type HandlerSuite struct {
	testsuite.StoreSuite
}

// callback 作为一个验证函数 传入runHandler
func (s *HandlerSuite) runHandler(handler define.ETLRecordChainingHandler, callback func(record *define.ETLRecord), cases []handlerCase) {
	// 对每个case 用callback验证
	for _, c := range cases {
		pass := 0
		err := handler(&c.record, func(r *define.ETLRecord) error {
			if callback != nil {
				callback(r) // 如果未通过 callback会退出,pass 值不变
			}
			pass++
			return nil
		})
		s.Equal(c.pass, pass)
		switch e := c.err.(type) {
		case string:
			s.Error(err, e)
		default:
			s.Equal(c.err, err)
		}
	}
}

func (s *HandlerSuite) makeTime() *int64 {
	ts := time.Now().Unix()
	return &ts
}

// TestCheckRecordHandler :
func (s *HandlerSuite) TestCheckRecordHandler() {
	s.runHandler(CheckRecordHandler(false), nil, []handlerCase{
		{0, "record time is empty", define.ETLRecord{
			Metrics:    map[string]interface{}{"v": 0},
			Dimensions: map[string]interface{}{"k": "v"},
		}},
		{0, "record metrics is empty", define.ETLRecord{
			Time:       s.makeTime(),
			Dimensions: map[string]interface{}{"k": "v"},
		}},
		{0, "record dimensions is empty", define.ETLRecord{
			Time:    s.makeTime(),
			Metrics: map[string]interface{}{"v": 0},
		}},
		{1, nil, define.ETLRecord{
			Time:       s.makeTime(),
			Metrics:    map[string]interface{}{"v": 0},
			Dimensions: map[string]interface{}{"k": "v"},
		}},
	})
}

// TestFormatDimensionsHandler :
func (s *HandlerSuite) TestFormatDimensionsHandler() {
	s.runHandler(FormatDimensionsHandler, func(record *define.ETLRecord) {
		for key, value := range record.Dimensions {
			switch value.(type) {
			case string:
				break
			default:
				s.Failf("%s is not string but %T", key, value)
			}
		}
	}, []handlerCase{
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"a": "b",
			},
		}},
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"a": "b",
				"c": 1,
				"d": 2.0,
			},
		}},
	})
}

// TestFillCmdbHandlerCreatorWithDetail
func (s *HandlerSuite) TestFillCmdbHandlerCreatorWithDetail() {
	hostInfo := models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 1,
		CCTopoBaseModelInfo: &models.CCTopoBaseModelInfo{
			BizID: []int{2},
			Topo: []map[string]string{{
				"Anduin":                "1",
				"Guldan":                "2",
				"bk_biz_id":             "2",
				define.RecordBkModuleID: "31",
			}},
		},
	}
	s.StoreHost(&hostInfo).AnyTimes()
	s.Store.EXPECT().Get(gomock.Any()).Return(nil, define.ErrItemNotFound).AnyTimes()

	cases := []struct {
		name      string
		enable    bool
		bizID     string
		cmdbLevel interface{}
		record    define.ETLRecord
	}{
		{
			"有bizId上报,无其他",
			true, "2", nil, define.ETLRecord{
				Dimensions: map[string]interface{}{define.RecordBizIDFieldName: "2"},
			},
		},
		{
			"啥都有 transfer 不管上传数据是否合理 均不补充 考虑新老采集器",
			true, "2", []byte(`[{"bk_biz_id":2,"bk_biz_name":"蓝鲸","bk_module_id":31,"bk_module_name":"","bk_service_status":"1","bk_set_env":"3","bk_set_id":8,"bk_set_name":"配置平台"}]`), define.ETLRecord{
				Dimensions: map[string]interface{}{define.RecordBizIDFieldName: "2", define.RecordCMDBLevelFieldName: []byte(`[{"bk_biz_id":2,"bk_biz_name":"蓝鲸","bk_module_id":31,"bk_module_name":"","bk_service_status":"1","bk_set_env":"3","bk_set_id":8,"bk_set_name":"配置平台"}]`)},
			},
		},
		{
			"没cmdb 补上相关业务",
			true, "2", "[{\"Anduin\":\"1\",\"Guldan\":\"2\",\"bk_biz_id\":\"2\",\"bk_module_id\":\"31\"}]",
			define.ETLRecord{
				Dimensions: map[string]interface{}{define.RecordBizIDFieldName: "2", define.RecordCloudIDFieldName: 1, define.RecordIPFieldName: "127.0.0.1"},
			},
		},
		{
			"没topo 且biz无法正确被补充，无法处理，说明数据有问题,这里是空数组",
			true, "", "[{}]",
			define.ETLRecord{
				Dimensions: map[string]interface{}{define.RecordBizIDFieldName: "", define.RecordCloudIDFieldName: 1, define.RecordIPFieldName: "127.0.0.1"},
			},
		},
		{
			"有biz_id 无ip ==> cmdb_level 无法补全  此时if(metadata 已经配置)应该丢弃(由下一个节点丢弃) 这里是nil",
			true, "2", nil,
			define.ETLRecord{
				Dimensions: map[string]interface{}{define.RecordCloudIDFieldName: 1, define.RecordBizIDFieldName: "2"},
			},
		},
	}
	cmdbInfo := make([]interface{}, 0)
	for _, value := range cases {
		s.T().Run(value.name, func(t *testing.T) {
			record := value.record
			_ = FillCmdbLevelHandlerCreator(cmdbInfo, s.Store, value.enable)(&record, func(record *define.ETLRecord) error {
				return nil
			})
			s.Equalf(value.cmdbLevel, record.Dimensions[define.RecordCMDBLevelFieldName], "%s", value.name)
		})

	}
}

// TestFillBizIDHandlerCreator :
func (s *HandlerSuite) TestFillBizIDHandlerCreator() {
	hostInfo := models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 1,
		CCTopoBaseModelInfo: &models.CCTopoBaseModelInfo{
			BizID: []int{2},
			Topo: []map[string]string{
				{
					define.RecordBkModuleID: "19",
					"Anduin":                "1",
				}, {
					define.RecordBkModuleID: "18",
					"Guldan":                "2",
				},
			},
		},
	}
	s.StoreHost(&hostInfo).AnyTimes()
	s.Store.EXPECT().Get(gomock.Any()).Return(nil, define.ErrItemNotFound).AnyTimes()

	s.runHandler(FillBizIDHandlerCreator(s.Store, s.ResultTableConfig), func(record *define.ETLRecord) {
		_, bizOk := record.Dimensions[define.RecordBizIDFieldName]
		s.True(bizOk)
	}, []handlerCase{
		// 有biz id 无ip cloud
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				define.RecordBizIDFieldName: 3,
			},
		}}, {
			1, nil, define.ETLRecord{
				Dimensions: map[string]interface{}{
					"ip":          "127.0.0.1",
					"bk_cloud_id": 1,
				},
			},
		},
	})
}

func (s *HandlerSuite) TestCutterByDbmMetaV0() {
	hostInfo := models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 1,
		CCTopoBaseModelInfo: &models.CCTopoBaseModelInfo{
			BizID: []int{2},
			Topo:  []map[string]string{},
		},
		DbmMeta: `[{"role":"master","cluster":"ssd.nvmessd.dba.db"},{"role":"slave","cluster":"ssd.abcd.dba.db"}]`,
	}
	s.StoreHost(&hostInfo).AnyTimes()
	s.Store.EXPECT().Get(gomock.Any()).Return(nil, define.ErrItemNotFound).AnyTimes()

	s.runHandler(TransferRecordCutterByDbmMetaCreator(s.Store, true), func(record *define.ETLRecord) {
		dims := record.Dimensions
		s.NotNil(dims["role"])
		s.NotNil(dims["cluster"])
		s.T().Logf("dbm-meta/v0 record: %+v", record)
	}, []handlerCase{
		// 有biz id 无ip cloud
		{
			2, nil, define.ETLRecord{
				Dimensions: map[string]interface{}{
					define.RecordBizIDFieldName:   3,
					define.RecordIPFieldName:      "127.0.0.1",
					define.RecordCloudIDFieldName: "1",
				},
			},
		},
	})
}

func (s *HandlerSuite) TestCutterByDbmMetaV1() {
	hostInfo := models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 1,
		CCTopoBaseModelInfo: &models.CCTopoBaseModelInfo{
			BizID: []int{2},
			Topo:  []map[string]string{},
		},
		DbmMeta: `{"version":"v1","common":{"region":"gz","status":"prod"},"custom":[{"role":"master","cluster":"ssd.nvmessd.dba.db"},{"role":"slave","cluster":"ssd.abcd.dba.db"}]}`,
	}
	s.StoreHost(&hostInfo).AnyTimes()
	s.Store.EXPECT().Get(gomock.Any()).Return(nil, define.ErrItemNotFound).AnyTimes()

	s.runHandler(TransferRecordCutterByDbmMetaCreator(s.Store, true), func(record *define.ETLRecord) {
		dims := record.Dimensions
		s.NotNil(dims["role"])
		s.NotNil(dims["cluster"])
		s.NotNil(dims["region"])
		s.NotNil(dims["status"])
		s.T().Logf("dbm-meta/v1 record: %+v", record)
	}, []handlerCase{
		{
			2, nil, define.ETLRecord{
				Dimensions: map[string]interface{}{
					define.RecordBizIDFieldName:   3,
					define.RecordIPFieldName:      "127.0.0.1",
					define.RecordCloudIDFieldName: "1",
				},
			},
		},
	})
}

// TestFillBizIDHandlerCreator :
func (s *HandlerSuite) TestFillBizIDHandlerCreatorWithInstanceId() {
	instanceInfo := &models.CCInstanceInfo{
		InstanceID: "2",
		CCTopoBaseModelInfo: &models.CCTopoBaseModelInfo{
			BizID: []int{2},
			Topo: []map[string]string{
				{
					define.RecordBkModuleID: "19",
					"Anduin":                "1",
				}, {
					define.RecordBkModuleID: "18",
					"Guldan":                "2",
				},
			},
		},
	}
	s.StoreInstance(instanceInfo).AnyTimes()
	s.Store.EXPECT().Get(gomock.Any()).Return(nil, define.ErrItemNotFound).AnyTimes()
	s.runHandler(FillCmdbLevelHandlerCreator([]interface{}{}, s.Store, true), func(record *define.ETLRecord) {
	}, []handlerCase{
		// 所有信息齐全
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				define.RecordBkTargetServiceInstanceID: "2",
				define.RecordCMDBLevelFieldName:        nil,
			},
		}},
	})
}

// TestFillSupplierIDHandler :
func (s *HandlerSuite) TestFillSupplierIDHandler() {
	s.Store.EXPECT().Get(gomock.Any()).Return(nil, define.ErrItemNotFound).AnyTimes()

	s.runHandler(FillSupplierIDHandler, func(record *define.ETLRecord) {
		id, ok := record.Dimensions[define.RecordSupplierIDFieldName]
		s.True(ok)
		s.Equal(record.Metrics[define.RecordSupplierIDFieldName], id)
	}, []handlerCase{
		{1, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				define.RecordSupplierIDFieldName: 0,
			},
			Dimensions: map[string]interface{}{},
		}},
		{1, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				define.RecordSupplierIDFieldName: 1,
			},
			Dimensions: map[string]interface{}{
				define.RecordSupplierIDFieldName: 1,
			},
		}},
	})
}

// TestFillDefaultValueCreator
func (s *HandlerSuite) TestFillDefaultValueCreator() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(s.CTX, s.PipelineConfig, `{"etl_config":"bk_flat_batch","result_table_list":[{"schema_type":"fixed","shipper_list":[{"cluster_config":{"domain_name":"influxdb.service.consul","port":5260},"storage_config":{"real_table_name":"heartbeat","database":"uptimecheck"},"cluster_type":"influxdb"}],"result_table":"uptimecheck.heartbeat","field_list":[{"default_value":"12","type":"int","is_config_by_user":true,"tag":"dimension","field_name":"bk_biz_id"},{"default_value":null,"type":"int","is_config_by_user":true,"tag":"dimension","field_name":"test"},{"default_value":null,"type":"timestamp","is_config_by_user":true,"tag":"","field_name":"time"}]}],"mq_config":{"cluster_config":{"domain_name":"kafka.service.consul","port":9092},"storage_config":{"topic":"0bkmonitor_10080","partition":1},"cluster_type":"kafka"},"data_id":1008}`)
	rt := config.ResultTableConfigFromContext(s.CTX)
	s.runHandler(FillDefaultValueCreator(true, rt), func(record *define.ETLRecord) {
		s.Equal("12", record.Dimensions["bk_biz_id"])
		s.Equal("", record.Dimensions["test"])
	}, []handlerCase{
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"bk_biz_id": "12",
				"test":      "",
			},
		}},
	})
}

// TestRoundingTimeHandlerCreator :
func (s *HandlerSuite) TestRoundingTimeHandlerCreator() {
	s.Nil(RoundingTimeHandlerCreator(""))
	s.runHandler(RoundingTimeHandlerCreator("1m"), func(record *define.ETLRecord) {
		s.Equal(int64(0), *record.Time%60)
	}, []handlerCase{
		{0, "get record time error", define.ETLRecord{}},
		{1, nil, define.ETLRecord{
			Time: s.makeTime(),
		}},
	})
}

// TestTransformMetricsHandlerCreator :
func (s *HandlerSuite) TestTransformMetricsHandlerCreator() {
	metrics := map[string]etl.TransformFn{
		"int":    etl.TransformNilInt64,
		"float":  etl.TransformNilFloat64,
		"string": etl.TransformNilString,
	}
	s.runHandler(TransformMetricsHandlerCreator(metrics, false), func(record *define.ETLRecord) {
		s.Equal(len(metrics), len(record.Metrics))
		for key, value := range record.Metrics {
			switch value.(type) {
			case int64:
				s.Equal("int", key)
			case float64:
				s.Equal("float", key)
			case string:
				s.Equal("string", key)
			default:
				s.Failf("unknown type", "%s:%T", key, value)
			}
		}
	}, []handlerCase{
		{0, "transform field int error cannot convert \"x\" (type string) to int", define.ETLRecord{
			Metrics: map[string]interface{}{
				"int": "x",
			},
		}},
		{0, "metrics has nothing", define.ETLRecord{
			Metrics: map[string]interface{}{
				"x": "x",
			},
		}},
		{0, "field string not found", define.ETLRecord{
			Metrics: map[string]interface{}{
				"int":   "0",
				"float": "1",
			},
		}},
		{1, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				"int":    "0",
				"float":  1,
				"string": 2,
			},
		}},
		{1, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				"int":    "0",
				"float":  1,
				"string": 2,
				"bool":   false,
			},
		}},
	})
}

// TestTransformMetricsHandlerCreatorMissing :
func (s *HandlerSuite) TestTransformMetricsHandlerCreatorMissing() {
	metrics := map[string]etl.TransformFn{
		"int":    etl.TransformNilInt64,
		"float":  etl.TransformNilFloat64,
		"string": etl.TransformNilString,
	}
	s.runHandler(TransformMetricsHandlerCreator(metrics, true), func(record *define.ETLRecord) {
		s.True(len(metrics) >= len(record.Metrics))
	}, []handlerCase{
		{1, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				"int":   "0",
				"float": "1",
			},
		}},
		{1, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				"int":    "0",
				"float":  1,
				"string": 2,
			},
		}},
		{1, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				"int":    "0",
				"float":  1,
				"string": 2,
				"bool":   false,
			},
		}},
	})
}

// TestTransformMetricsAsFloat64Creator :
func (s *HandlerSuite) TestTransformMetricsAsFloat64Creator() {
	s.runHandler(MetricsAsFloat64Creator(true), func(record *define.ETLRecord) {
		testMetrics := map[string]interface{}{
			"string":      0.0,
			"int":         0.0,
			"float":       1.1,
			"bool":        0.0,
			"nil":         nil,
			"stringSpace": "",
		}
		for key, value := range record.Metrics {
			s.Equal(value, testMetrics[key])
		}
	}, []handlerCase{
		{1, nil, define.ETLRecord{ // pass : 输入到chan中的条数, record 测试record
			Metrics: map[string]interface{}{
				"string":       "0",
				"int":          0,
				"float":        1.1,
				"bool":         false,
				"normalString": "test",
				"stringSpace":  "",
			},
		}},
	})
}

// TestTransformDimensionsHandlerCreator :
func (s *HandlerSuite) TestTransformDimensionsHandlerCreator() {
	dimensions := map[string]etl.TransformFn{
		"x": etl.TransformNilString,
		"y": etl.TransformNilString,
	}
	s.runHandler(TransformDimensionsHandlerCreator(dimensions, false), func(record *define.ETLRecord) {
		s.True(len(dimensions) == len(record.Dimensions))
		for key, value := range record.Dimensions {
			switch value.(type) {
			case string:
				break
			default:
				s.Failf("unknown type", "%s:%T", key, value)
			}
		}
	}, []handlerCase{
		{0, "field y not found", define.ETLRecord{
			Dimensions: map[string]interface{}{
				"x": 0,
			},
		}},
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"x": 0,
				"y": 1,
			},
		}},
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"x": 0,
				"y": 1,
				"z": 2,
			},
		}},
	})
}

// TestTransformDimensionsHandlerCreatorMissing :
func (s *HandlerSuite) TestTransformDimensionsHandlerCreatorMissing() {
	dimensions := map[string]etl.TransformFn{
		"x": etl.TransformNilString,
		"y": etl.TransformNilString,
	}
	s.runHandler(TransformDimensionsHandlerCreator(dimensions, true), func(record *define.ETLRecord) {
		s.True(len(dimensions) >= len(record.Dimensions))
	}, []handlerCase{
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"x": 0,
			},
		}},
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"x": 0,
				"y": 1,
			},
		}},
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"x": 0,
				"y": 1,
				"z": 2,
			},
		}},
	})
}

// TestMetricsCutterHandler :
func (s *HandlerSuite) TestRecordCutterByCmdbLevelHandler() {
	s.runHandler(TransferRecordCutterByCmdbLevelCreator([]interface{}{define.RecordBkModuleID, "Guldan"}, true), func(record *define.ETLRecord) {
	}, []handlerCase{
		{3, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				"a": 0,
			},
			Dimensions: map[string]interface{}{
				"x":             0,
				"bk_biz_id":     2,
				"bk_cmdb_level": "[{\"bk_biz_id\":2,\"bk_module_id\":31,\"bk_set_id\":8,\"Guldan\":2,\"Anduin\":2},{\"bk_biz_id\":2,\"bk_module_id\":311,\"bk_set_id\":18,\"Sylvanas\":1}]",
			},
		}},
	})
}

// TestMetricsCutterHandler :
func (s *HandlerSuite) TestMetricsCutterHandler() {
	dimensions := []string{"x", "y"}
	s.runHandler(MetricsCutterHandler, func(record *define.ETLRecord) {
		var ok bool
		_, ok = record.Dimensions[define.MetricKeyFieldName]
		s.True(ok)
		for _, key := range dimensions {
			_, ok := record.Dimensions[key]
			s.True(ok)
		}

		s.Equal(1, len(record.Metrics))
		_, ok = record.Metrics[define.MetricValueFieldName]
		s.True(ok)
	}, []handlerCase{
		{3, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				"a": 0,
				"b": 1.2,
				"c": "3",
			},
			Dimensions: map[string]interface{}{
				"x": 0,
				"y": 1,
			},
		}},
		{1, nil, define.ETLRecord{
			Metrics: map[string]interface{}{
				"a": 0,
			},
			Dimensions: map[string]interface{}{
				"x": 0,
				"y": 1,
			},
		}},
	})
}

// TestLocalTimeInjectHandler :
func (s *HandlerSuite) TestLocalTimeInjectHandler() {
	s.Nil(LocalTimeInjectHandlerCreator(define.LocalTimeFieldName, false))

	s.runHandler(LocalTimeInjectHandlerCreator(define.LocalTimeFieldName, true), func(record *define.ETLRecord) {
		localTime, ok := record.Metrics[define.LocalTimeFieldName]
		s.True(ok)
		s.IsType(int64(0), localTime)
	}, []handlerCase{
		{1, nil, define.ETLRecord{
			Metrics:    map[string]interface{}{},
			Dimensions: map[string]interface{}{},
		}},
	})
}

// TestTransformAliasNameHandlerCreator :
func (s *HandlerSuite) TestTransformAliasNameHandlerCreator() {
	consul := `
{
  "schema_type": "dynamic",
  "shipper_list": [
    
  ],
  "result_table": "system.cpu_detail",
  "field_list": [
    {
      "type": "int",
      "is_config_by_user": true,
      "tag": "dimension",
      "field_name": "bk_biz_id",
      "alias_name": "alias_bk_biz_id"
    },
    {
      "type": "int",
      "is_config_by_user": true,
      "tag": "dimension",
      "field_name": "normalName",
      "alias_name": "aliasName"
    },
    {
      "type": "int",
      "is_config_by_user": true,
      "tag": "dimension",
      "field_name": "custom_field",
      "alias_name": "alias_custom_field"
    },
    {
      "type": "int",
      "is_config_by_user": true,
      "tag": "metric",
      "field_name": "metric_field",
      "alias_name": "alias_metric_field"
    },
    {
      "type": "timestamp",
      "is_config_by_user": true,
      "tag": "dimension",
      "field_name": "testNil",
      "alias_name": ""
    },
    {
      "type": "timestamp",
      "is_config_by_user": true,
      "tag": "",
      "field_name": "time",
      "alias_name": "alias_time"
    }
  ]
}
`

	var (
		ctx  = context.Background()
		conf config.MetaResultTableConfig
	)
	s.NoError(json.Unmarshal([]byte(consul), &conf))
	ctx = config.ResultTableConfigIntoContext(ctx, &conf)
	rt := config.ResultTableConfigFromContext(ctx)
	s.runHandler(TransformAliasNameHandlerCreator(rt, true), func(record *define.ETLRecord) {
		testDimensions := map[string]interface{}{
			"alias_bk_biz_id":    "Alias",
			"aliasName":          "Alias",
			"alias_custom_field": "Alias",
			"bk_biz_id":          "Alias",
			"normalName":         "Alias",
			"field_name":         "NoAlias",
			"custom_field":       "Alias",
		}
		for key, value := range record.Dimensions {
			s.Equal(value, testDimensions[key])
		}
		testMetrics := map[string]interface{}{
			"testNil":            nil,
			"alias_metric_field": 12,
			"metric_field":       12,
		}
		s.Equal(len(record.Metrics), len(testMetrics))
		for key, value := range record.Metrics {
			s.Equal(value, testMetrics[key])
		}
	}, []handlerCase{
		{1, nil, define.ETLRecord{
			Dimensions: map[string]interface{}{
				"bk_biz_id":    "Alias",
				"normalName":   "Alias",
				"field_name":   "NoAlias",
				"custom_field": "Alias",
			},
			Metrics: map[string]interface{}{
				"metric_field": 12,
				"testNil":      nil,
			},
		}},
	})
}

// TestHandlerSuite :
func TestHandlerSuite(t *testing.T) {
	suite.Run(t, new(HandlerSuite))
}
