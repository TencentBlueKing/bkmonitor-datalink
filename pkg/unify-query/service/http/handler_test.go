// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	tsdbInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

// mockData comment lint rebel
func mockData(ctx context.Context, path, bucket string) *curl.TestCurl {
	featureFlag.MockFeatureFlag(ctx, `{
    	"vm-query-or": {
    		"variations": {
    			"true": true,
    			"false": false
    		},
    		"targeting": [{
    			"query": "name in [\"vm-query-or\"]",
    			"percentage": {
    				"true": 100,
    				"false": 0
    			}
    		}],
    		"defaultRule": {
    			"percentage": {
    				"true": 0,
    				"false": 100
    			}
    		}
    	}
    }`)

	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	metadata.GetQueryRouter().MockSpaceUid("vm-query", consul.VictoriaMetricsStorageType)

	victoriaMetricsStorageId := int64(1)
	influxdbStorageId := int64(2)
	mock.SetSpaceTsDbMockData(ctx, path, bucket,
		ir.SpaceInfo{
			consul.VictoriaMetricsStorageType: ir.Space{
				"a.b_2": &ir.SpaceResultTable{
					TableId: "a.b_2",
					Filters: []map[string]string{
						{
							redis.BkSplitMeasurement: redis.BkSplitMeasurement,
						},
					},
				},
				"a.b_1": &ir.SpaceResultTable{
					TableId: "a.b_1",
					Filters: []map[string]string{
						{
							redis.BkSplitMeasurement: redis.BkSplitMeasurement,
						},
					},
				},
			},
		},
		ir.ResultTableDetailInfo{
			"a.b_2": &ir.ResultTableDetail{
				Fields:          []string{redis.BkSplitMeasurement},
				MeasurementType: redis.BkSplitMeasurement,
				StorageId:       victoriaMetricsStorageId,
				DB:              redis.BkSplitMeasurement,
				VmRt:            consul.VictoriaMetricsStorageType,
			},
			"a.b_1": &ir.ResultTableDetail{
				Fields:          []string{redis.BkSplitMeasurement},
				MeasurementType: redis.BkSplitMeasurement,
				StorageId:       victoriaMetricsStorageId,
				DB:              redis.BkSplitMeasurement,
				VmRt:            consul.VictoriaMetricsStorageType,
			},
		},
		nil, nil,
	)

	mock.SetSpaceTsDbMockData(ctx, path, bucket,
		ir.SpaceInfo{
			consul.InfluxDBStorageType: ir.Space{
				"a.b": &ir.SpaceResultTable{
					TableId: "a.b",
					Filters: []map[string]string{
						{
							redis.BkSplitMeasurement: redis.BkSplitMeasurement,
						},
					},
				},
				"system.cpu_summary": &ir.SpaceResultTable{
					TableId: "system.cpu_summary",
				},
			},
		},
		ir.ResultTableDetailInfo{
			"a.b": &ir.ResultTableDetail{
				Fields:          []string{redis.BkSplitMeasurement},
				MeasurementType: redis.BkSplitMeasurement,
				StorageId:       influxdbStorageId,
				DB:              redis.BkSplitMeasurement,
			},
			"system.cpu_summary": &ir.ResultTableDetail{
				Fields:          []string{"usage", "rate"},
				MeasurementType: redis.BKTraditionalMeasurement,
				StorageId:       influxdbStorageId,
				DB:              "system",
				Measurement:     "cpu_summary",
			},
		},
		nil, nil,
	)
	mock.SetSpaceTsDbMockData(ctx, path, bucket,
		ir.SpaceInfo{
			"vm-query": ir.Space{
				"table_id.__default__": &ir.SpaceResultTable{
					TableId: "table_id.__default__",
					Filters: []map[string]string{
						{
							"bcs_cluster_id": "cls",
							"namespace":      "",
						},
					},
				},
				"100147_bcs_prom_computation_result_table_25428.__default__": &ir.SpaceResultTable{
					TableId: "100147_bcs_prom_computation_result_table_25428.__default__",
					Filters: []map[string]string{
						{
							"bcs_cluster_id": "BCS-K8S-25428",
							"namespace":      "",
						},
						{
							"bcs_cluster_id": "BCS-K8S-25430",
							"namespace":      "",
						},
					},
				},
				"100147_bcs_prom_computation_result_table_25429.__default__": &ir.SpaceResultTable{
					TableId: "100147_bcs_prom_computation_result_table_25429.__default__",
					Filters: []map[string]string{
						{
							"bcs_cluster_id": "BCS-K8S-25429",
							"namespace":      "",
						},
					},
				},
			},
		},
		ir.ResultTableDetailInfo{
			"table_id.__default__": &ir.ResultTableDetail{
				Fields:          []string{"metric"},
				MeasurementType: redis.BkSplitMeasurement,
				Measurement:     "__default__",
				StorageId:       victoriaMetricsStorageId,
				DB:              "table_id",
				VmRt:            "vm_rt",
			},
			"100147_bcs_prom_computation_result_table_25428.__default__": &ir.ResultTableDetail{
				Fields:          []string{"container_cpu_usage_seconds_total"},
				MeasurementType: redis.BkSplitMeasurement,
				StorageId:       victoriaMetricsStorageId,
				DB:              "100147_bcs_prom_computation_result_table_25428",
				Measurement:     "__default__",
				VmRt:            "100147_bcs_prom_computation_result_table_25428",
				DataLabel:       "100147_bcs_prom_computation_result_table_25428",
			},
			"100147_bcs_prom_computation_result_table_25429.__default__": &ir.ResultTableDetail{
				Fields:          []string{"container_cpu_usage_seconds_total"},
				MeasurementType: redis.BkSplitMeasurement,
				StorageId:       victoriaMetricsStorageId,
				DB:              "100147_bcs_prom_computation_result_table_25429",
				Measurement:     "__default__",
				VmRt:            "100147_bcs_prom_computation_result_table_25429",
				DataLabel:       "100147_bcs_prom_computation_result_table_25429",
			},
		},
		nil, nil,
	)
	mock.SetSpaceTsDbMockData(ctx, path, bucket,
		ir.SpaceInfo{
			"bkcc__100147": ir.Space{"custom_report_aggate.base": &ir.SpaceResultTable{
				TableId: "custom_report_aggate.base",
			}}},
		ir.ResultTableDetailInfo{"custom_report_aggate.base": &ir.ResultTableDetail{
			Fields:          []string{"bkmonitor_action_notice_api_call_count_total"},
			MeasurementType: redis.BkSplitMeasurement,
			StorageId:       influxdbStorageId,
			DB:              "custom_report_aggate",
			Measurement:     "base",
		}},
		nil, nil,
	)
	mockCurl := curl.NewMockCurl(map[string]string{
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=pushgateway_bkmonitor_unify_query&q=select+metric_value+as+_value%2C+time+as+_time%2C+bk_trace_id%2C+bk_span_id%2C+bk_trace_value%2C+bk_trace_timestamp+from+group2_cmdb_level+where+time+%3E+1682149980000000000+and+time+%3C+1682154605000000000+and+%28bk_obj_id%3D%27module%27+and+%28ip%3D%27127.0.0.2%27+and+%28bk_inst_id%3D%2714261%27+and+bk_biz_id%3D%277%27%29%29%29+and+metric_name+%3D+%27unify_query_request_count_total%27+and+%28bk_span_id+%21%3D+%27%27+or+bk_trace_id+%21%3D+%27%27%29++limit+100000000+slimit+100000000`: `{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T07:53:28Z",114938716,"d8952469b9014ed6b36c19d396b15c61","0a97123ee5ad7fd8",1,1682150008967],["2023-04-22T07:53:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:53:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:53:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:53:28Z",114939201,"771073eb573336a6d3365022a512d6d8","fca46f1c065452e8",1,1682150008969],["2023-04-22T07:54:28Z",114949368,"5b4931bbeb7bf497ff46d9cd9579ab60","0b0713e4e0106e55",1,1682150068965],["2023-04-22T07:54:28Z",114949853,"7c3c66f8763071d315fe8136bf8ff35c","159d9534754dc66d",1,1682150068965],["2023-04-22T07:54:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:54:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:54:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T07:55:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:55:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:55:28Z",114959046,"c9659d0b28bdb0c8afbd21aedd6bacd3","94bc25ffa3cc44e5",1,1682150128965],["2023-04-22T07:55:28Z",114959529,"c9659d0b28bdb0c8afbd21aedd6bacd3","94bc25ffa3cc44e5",1,1682150128962],["2023-04-22T07:55:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:56:28Z",114968813,"5def2a6568efe57199022da6f7cfcf3f","1d761b6cc2aabe5e",1,1682150188964],["2023-04-22T07:56:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:56:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:56:28Z",114969300,"ca18ec94669be35fee1b4ae4a2e3df2a","c113aa392812404a",1,1682150188968],["2023-04-22T07:56:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T07:57:28Z",114986134,"ab5b57bea973133a5cb1f89ee93ffd5a","191d6c2087b110de",1,1682150248965],["2023-04-22T07:57:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:57:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:57:28Z",114986622,"032fa9e22eaa486349f3a9d8ac1a0c76","7832acea944dc180",1,1682150248967],["2023-04-22T07:57:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:58:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:58:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:58:28Z",114997601,"0bd0b98c89250d1ab3e4fc77cdc9d619","4d3a86e07ac11e04",1,1682150308961],["2023-04-22T07:58:28Z",114998085,"09aeca94699ae82eec038c85573b68c4","687b908edb32b09f",1,1682150308967],["2023-04-22T07:58:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T07:59:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:59:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:59:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:59:28Z",115009204,"595fb8f3671365cc1ad920d30a7c9c6e","d1f36e8cbdb25ce8",1,1682150368961],["2023-04-22T07:59:28Z",115008721,"595fb8f3671365cc1ad920d30a7c9c6e","d1f36e8cbdb25ce8",1,1682150368964],["2023-04-22T08:00:28Z",115021473,"5a6dbfa27835ac9b22d8a795477e3155","1954ae8cddc3a6fc",1,1682150428955],["2023-04-22T08:00:28Z",115020990,"5a6dbfa27835ac9b22d8a795477e3155","1954ae8cddc3a6fc",1,1682150428959],["2023-04-22T08:00:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:00:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:00:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:01:28Z",115031442,"0ae45ad8874d96426b65416a67c82531","1c7968f7293a0af2",1,1682150488966],["2023-04-22T08:01:28Z",115031925,"0ae45ad8874d96426b65416a67c82531","1c7968f7293a0af2",1,1682150488960],["2023-04-22T08:01:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:01:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:01:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:02:28Z",115042237,"dac723082fea9d0863af70e751ef565e","34eb4eb32a60309e",1,1682150548960],["2023-04-22T08:02:28Z",115042720,"dac723082fea9d0863af70e751ef565e","34eb4eb32a60309e",1,1682150548958],["2023-04-22T08:02:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:02:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:02:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:03:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:03:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:03:28Z",115054635,"2f5205ecfff84e47bd6331e54e4d3e40","6fc8350f2318a7ca",1,1682150608962],["2023-04-22T08:03:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:03:28Z",115054151,"c4ed02cb38548d62bfae43dcd42a4b55","dfb56f2c0a0deb30",1,1682150608965],["2023-04-22T08:04:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:04:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:04:28Z",115063742,"2e572a096c8cf7e5a41e399c638b0e04","5b4471f3c41cb3b8",1,1682150668968],["2023-04-22T08:04:28Z",115063258,"32d7b9135f692c1628c9e3ba337d3a84","6a445c21f73bc40a",1,1682150668957],["2023-04-22T08:04:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:05:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:05:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:05:28Z",115079908,"bbf470769dbed5ce874134f989116bf5","6a24c9711646a433",1,1682150728966],["2023-04-22T08:05:28Z",115079419,"a6bc1c857d21aeba6951cd290ede7d6d","a1192477fcc2c445",1,1682150728957],["2023-04-22T08:05:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:06:28Z",115089527,"c583995aac74569636719ebc2dcf414a","10d77bd592504036",1,1682150788967],["2023-04-22T08:06:28Z",115090010,"c583995aac74569636719ebc2dcf414a","10d77bd592504036",1,1682150788964],["2023-04-22T08:06:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:06:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:06:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:07:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:07:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:07:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:07:28Z",115104143,"3944a09230dcf8b027c332b6b760a4f6","eb7bc4cd762e225c",1,1682150848966],["2023-04-22T08:07:28Z",115104626,"3944a09230dcf8b027c332b6b760a4f6","eb7bc4cd762e225c",1,1682150848963],["2023-04-22T08:08:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:08:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:08:28Z",115117650,"392ba52dda51ed66211c49fea3adfff0","44d3761ef5df2a60",1,1682150908968],["2023-04-22T08:08:28Z",115117162,"689739026da9b228529a3ebb63fb3756","6636e804a8c7f830",1,1682150908968],["2023-04-22T08:08:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:09:28Z",115126002,"f2d171da57dae50fb9c7af0e04655937","2515b8271d896683",1,1682150968878],["2023-04-22T08:09:28Z",115125519,"c084597a13d5a368329adf5791a9bd5b","2d7a037191f6825b",1,1682150968884],["2023-04-22T08:09:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:09:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:09:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:10:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:10:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:10:28Z",115136231,"aef23b75f180c0f6b80b052e1c5749ad","41f16325f7ec7207",1,1682151028962],["2023-04-22T08:10:28Z",115135748,"2a9a2c8c5f6f7b4c7bc64db82cc25ece","5c36f565160affe7",1,1682151028969],["2023-04-22T08:10:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:11:28Z",115149569,"1586056eee1d191386d8784d7b3f3f34","0ac1f67fb5dc05c7",1,1682151088970],["2023-04-22T08:11:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:11:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:11:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:11:28Z",115149085,"fb5227061604428741a1a45303321abd","e1b026cffa189a0d",1,1682151088967],["2023-04-22T08:12:28Z",115163411,"aceec42255f7f4e5e157530531dceec7","1c217159f11008b5",1,1682151148964],["2023-04-22T08:12:28Z",115162928,"aceec42255f7f4e5e157530531dceec7","1c217159f11008b5",1,1682151148967],["2023-04-22T08:12:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:12:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:12:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:13:28Z",115173673,"1acd32ff4227a91bcbe2e8a4f2d1e0cf","188222897a849cbf",1,1682151208175],["2023-04-22T08:13:28Z",115174156,"1acd32ff4227a91bcbe2e8a4f2d1e0cf","188222897a849cbf",1,1682151208171],["2023-04-22T08:13:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:13:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:13:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:14:28Z",115181241,"8ab09a1fba0a83675f5213f28ecf7ab0","048a63198da516d7",1,1682151268964],["2023-04-22T08:14:28Z",115181724,"8ab09a1fba0a83675f5213f28ecf7ab0","048a63198da516d7",1,1682151268961],["2023-04-22T08:14:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:14:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:14:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:15:28Z",115198050,"b183c974d457eceedba600a7ae7293bb","16375e22aa7686d8",1,1682151328948],["2023-04-22T08:15:28Z",115197567,"b183c974d457eceedba600a7ae7293bb","16375e22aa7686d8",1,1682151328951],["2023-04-22T08:15:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:15:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:15:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:16:28Z",115210906,"dc9d05c9878bc4e2dcbe17b9d9c92bc6","006c18da99f83a21",1,1682151388967],["2023-04-22T08:16:28Z",115211389,"76a0c8887915c5f374382c88b787392e","15a9434f4310b900",1,1682151388962],["2023-04-22T08:16:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:16:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:16:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:17:28Z",115222732,"5650177cd1f79ae792761118f91d71b4","2291ee695308c91a",1,1682151448859],["2023-04-22T08:17:28Z",115223215,"5650177cd1f79ae792761118f91d71b4","2291ee695308c91a",1,1682151448855],["2023-04-22T08:17:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:17:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:17:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:18:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:18:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:18:28Z",115231442,"0b523dd2f43ead213b42cd8f67fc27fb","8dd7adf4186e2bd4",1,1682151508966],["2023-04-22T08:18:28Z",115231926,"24ccf66130d5a1b858600c1a6aa916f3","8f30bfca7b4bb53e",1,1682151508968],["2023-04-22T08:18:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:19:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:19:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:19:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:19:28Z",115242028,"de8f959bdfeb8a5aca2bb123b3fa235d","d5b4ab7dad84b1ac",1,1682151568967],["2023-04-22T08:19:28Z",115242511,"de8f959bdfeb8a5aca2bb123b3fa235d","d5b4ab7dad84b1ac",1,1682151568965],["2023-04-22T08:20:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:20:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:20:28Z",115250857,"eabaf0a4308f2cb4e4f4ba28e758e026","5f917b5184d48ac9",1,1682151628962],["2023-04-22T08:20:28Z",115250374,"eabaf0a4308f2cb4e4f4ba28e758e026","5f917b5184d48ac9",1,1682151628967],["2023-04-22T08:20:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:21:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:21:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:21:28Z",115262828,"41f32db3a5dbae3d8131704786d3b9f5","a2fe2bd779d50031",1,1682151688962],["2023-04-22T08:21:28Z",115263311,"41f32db3a5dbae3d8131704786d3b9f5","a2fe2bd779d50031",1,1682151688954],["2023-04-22T08:21:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:22:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:22:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:22:28Z",115272127,"a444dd5f641e2fbf8f4f0fad31a43755","b97c208449487be7",1,1682151748967],["2023-04-22T08:22:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:22:28Z",115272611,"e0aa20bb79008be034cb5fb51a55e7be","fba717982f19aee1",1,1682151748965]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:23:28Z",115282567,"4fcfab1357beb0f1ff3968795564e8ba","20d7bb6662da56fc",1,1682151808957],["2023-04-22T08:23:28Z",115283050,"4fcfab1357beb0f1ff3968795564e8ba","20d7bb6662da56fc",1,1682151808954],["2023-04-22T08:23:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:23:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:23:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:24:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:24:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:24:28Z",115294443,"c48444259faf9c8bbba4236729e14979","8efc0b4772479d87",1,1682151868961],["2023-04-22T08:24:28Z",115293959,"d821b5094ccfc3afda6defa6741c0b1e","ab31b554831614a4",1,1682151868933],["2023-04-22T08:24:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:25:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:25:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:25:28Z",115305763,"634cdecba4540a9b15aa8a96474755d1","9bbdf0bf4daba865",1,1682151928969],["2023-04-22T08:25:28Z",115306246,"634cdecba4540a9b15aa8a96474755d1","9bbdf0bf4daba865",1,1682151928965],["2023-04-22T08:25:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:26:28Z",115313747,"40587b86461c7112efcb4097b82cf219","25787181e25dfdf3",1,1682151988957],["2023-04-22T08:26:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:26:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:26:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:26:28Z",115314231,"997d4d5d73426d66bf3c511624501753","fa422b5124625654",1,1682151988969]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:27:28Z",115324910,"ae21a9fcae09af13debaf56ac257003a","3c5e0082e4051153",1,1682152048965],["2023-04-22T08:27:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:27:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:27:28Z",115324425,"b2b216720a0439bc2287b49b19177ebe","7c42dddc736039ee",1,1682152048968],["2023-04-22T08:27:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:28:28Z",115337517,"74c5ecc1c2292513a37f239a853e3fe7","24f42a3ad6a3ab43",1,1682152108965],["2023-04-22T08:28:28Z",115338000,"74c5ecc1c2292513a37f239a853e3fe7","24f42a3ad6a3ab43",1,1682152108961],["2023-04-22T08:28:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:28:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:28:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:29:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:29:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:29:28Z",115349761,"8ba4ae67dc9775191fd7bf4b4f8cce94","61351ca322215086",1,1682152168967],["2023-04-22T08:29:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:29:28Z",115349276,"c9ad084b420b818c5d1ac3d4c853f18c","cf67a7588e41f316",1,1682152168970],["2023-04-22T08:30:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:30:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:30:28Z",115361417,"7d2f78ce24e3ade2e29d5514c0736143","566f3685f55e610f",1,1682152228963],["2023-04-22T08:30:28Z",115360934,"7d2f78ce24e3ade2e29d5514c0736143","566f3685f55e610f",1,1682152228966],["2023-04-22T08:30:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:31:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:31:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:31:28Z",115371502,"c189c35c14ae1e886e9d317652546af7","3ec0c24ee8ade2cd",1,1682152288959],["2023-04-22T08:31:28Z",115371986,"ce6dfccc1a569043e7a64f59d6ae210e","b289ea384ddd9e3e",1,1682152288966],["2023-04-22T08:31:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:32:28Z",115383620,"5482659f94a769ef70dc8243771c3ad1","26a60035b3bdbda2",1,1682152348949],["2023-04-22T08:32:28Z",115383137,"5482659f94a769ef70dc8243771c3ad1","26a60035b3bdbda2",1,1682152348952],["2023-04-22T08:32:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:32:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:32:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:33:28Z",115398302,"8076c97c8553ae4c484f4cbd56ca14c3","3810f43be4e95c84",1,1682152408964],["2023-04-22T08:33:28Z",115397819,"8076c97c8553ae4c484f4cbd56ca14c3","3810f43be4e95c84",1,1682152408967],["2023-04-22T08:33:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:33:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:33:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:34:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:34:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:34:28Z",115409417,"249d5d3fc819d18145b12f7202168e79","44f7eb14cdc234f7",1,1682152468963],["2023-04-22T08:34:28Z",115408934,"249d5d3fc819d18145b12f7202168e79","44f7eb14cdc234f7",1,1682152468967],["2023-04-22T08:34:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:35:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:35:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:35:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:35:28Z",115417148,"c3bd264a05effac853a5170173516af8","e2e97ddc9c392638",1,1682152528963],["2023-04-22T08:35:28Z",115416665,"c3bd264a05effac853a5170173516af8","e2e97ddc9c392638",1,1682152528966],["2023-04-22T08:36:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:36:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:36:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:36:28Z",115431074,"2d3ecc1056447120048c227b048ce0ae","dd7f8dc416754bf8",1,1682152588969],["2023-04-22T08:36:28Z",115431557,"2d3ecc1056447120048c227b048ce0ae","dd7f8dc416754bf8",1,1682152588965]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:37:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:37:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:37:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:37:28Z",115443915,"1d9e62409af4ab52b33e1bd1699bc780","eb9b452580af0ad1",1,1682152648961],["2023-04-22T08:37:28Z",115444398,"1d9e62409af4ab52b33e1bd1699bc780","eb9b452580af0ad1",1,1682152648954],["2023-04-22T08:38:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:38:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:38:28Z",115458081,"60cf809ac0ae93262b8ac2fe5db95065","a42e57f60c06eda2",1,1682152708969],["2023-04-22T08:38:28Z",115457596,"94f464096a013e4f61b3ed7dd04c93a3","a8709d02b9847220",1,1682152708967],["2023-04-22T08:38:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:39:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:39:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:39:28Z",115471863,"9b223f1cd429c0ab8f6157a31fb29630","47c34acfd33c1716",1,1682152768966],["2023-04-22T08:39:28Z",115472346,"9b223f1cd429c0ab8f6157a31fb29630","47c34acfd33c1716",1,1682152768962],["2023-04-22T08:39:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:40:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:40:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:40:28Z",115484160,"b20722843abac3a54b457c188dbaea15","a746bea06ff1baf9",1,1682152828960],["2023-04-22T08:40:28Z",115483677,"b20722843abac3a54b457c188dbaea15","a746bea06ff1baf9",1,1682152828964],["2023-04-22T08:40:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:41:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:41:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:41:28Z",115494091,"e3cb92488f83fc5a24122113b5c453fd","af36db2f540f133a",1,1682152888966],["2023-04-22T08:41:28Z",115494574,"e3cb92488f83fc5a24122113b5c453fd","af36db2f540f133a",1,1682152888963],["2023-04-22T08:41:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:42:28Z",115505010,"188a66107ecba773063eea56f9107eb6","073e73fa7ca1c993",1,1682152948966],["2023-04-22T08:42:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:42:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:42:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:42:28Z",115504526,"193b160f5c8fafb283569391d4e2ba90","ff69aa31aea248c8",1,1682152948952]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:43:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:43:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:43:28Z",115515895,"3d099ac99237f61536dc7d2d27d99bb8","3fb14916f9e591f7",1,1682153008967],["2023-04-22T08:43:28Z",115516380,"01691333a4f11f215883c05c51e36fc1","62017c0603dff189",1,1682153008965],["2023-04-22T08:43:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:44:28Z",115528870,"73e5b3d7b91aa2c89db08d39eef6b4f5","28d1bf05bd220383",1,1682153068969],["2023-04-22T08:44:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:44:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:44:28Z",115528384,"f558ca47fb57af24481b18dfd62dd9b7","c595af435ccff1ea",1,1682153068968],["2023-04-22T08:44:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:45:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:45:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:45:28Z",115540553,"cb23ec363097810291c106e5e2f1a8eb","99bd004b666ec085",1,1682153128960],["2023-04-22T08:45:28Z",115541036,"cb23ec363097810291c106e5e2f1a8eb","99bd004b666ec085",1,1682153128956],["2023-04-22T08:45:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:46:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:46:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:46:28Z",115552374,"e5cd69540f3e8bcde571db176b9a36d0","40296b35be3eeeb2",1,1682153188892],["2023-04-22T08:46:28Z",115551891,"e5cd69540f3e8bcde571db176b9a36d0","40296b35be3eeeb2",1,1682153188895],["2023-04-22T08:46:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:47:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:47:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:47:28Z",115566396,"a42a93b3c057f391eee6f26b7c5aa7ee","8a037eb6d56afa12",1,1682153248968],["2023-04-22T08:47:28Z",115565912,"70278d96624f975c43c5a4b15ac77f37","b5015c7dc52f7e78",1,1682153248970],["2023-04-22T08:47:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:48:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:48:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:48:28Z",115578955,"c8a8313fc6922dda66ce0ab7b186b282","7ce523c25706efd8",1,1682153308966],["2023-04-22T08:48:28Z",115579439,"1fff8976d9a43612c0c22372923b7321","8afb144b049ea2df",1,1682153308967],["2023-04-22T08:48:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:49:28Z",115589237,"310be0dc38fbd8360719f73a0b3b6bfe","0e9e91d5b70a9ec0",1,1682153368966],["2023-04-22T08:49:28Z",115589721,"9d9eb773f59f447fa56f47a09443a029","3211971d3d050cf9",1,1682153368965],["2023-04-22T08:49:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:49:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:49:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:50:28Z",115601705,"d3f670b536b0b4ab38c638517c41e303","31b1f14e2b7de574",1,1682153428964],["2023-04-22T08:50:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:50:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:50:28Z",115602191,"e038bea53a99e80d2e9eab7fb3234306","bc21eb298f0c8bdc",1,1682153428962],["2023-04-22T08:50:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:51:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:51:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:51:28Z",115617161,"59a5d61a87444fd828449900a3aaf0bb","a784d38118cf457b",1,1682153488963],["2023-04-22T08:51:28Z",115616678,"59a5d61a87444fd828449900a3aaf0bb","a784d38118cf457b",1,1682153488966],["2023-04-22T08:51:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:52:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:52:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:52:28Z",115633071,"af4bcf779a03b632e775c7bb99de737a","3fb6496458bbc3fa",1,1682153548969],["2023-04-22T08:52:28Z",115633558,"a89c63ede093668fe429a1b4259678e6","a1b477cab5351b9f",1,1682153548970],["2023-04-22T08:52:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:53:28Z",115651501,"383fea37a13434177c7e1b620570f5c7","21ba17a4811e2ab2",1,1682153608971],["2023-04-22T08:53:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:53:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:53:28Z",115651993,"2fa9126187165a5c7bc49087ae801ba4","7a6bc40c354978dc",1,1682153608974],["2023-04-22T08:53:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:54:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:54:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:54:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:54:28Z",115664362,"9b38375f27cecc654c8049c0f085a92b","c8df4f8d5ae463d7",1,1682153668959],["2023-04-22T08:54:28Z",115663879,"9b38375f27cecc654c8049c0f085a92b","c8df4f8d5ae463d7",1,1682153668962]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:55:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:55:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:55:28Z",115677626,"fc984e3758b50bc89cfbb3b558ddc311","75ca3eec04a5d020",1,1682153728969],["2023-04-22T08:55:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:55:28Z",115678110,"f72610be63b673974cfb83eda9e6d9c7","e589bb099c5f7119",1,1682153728969],["2023-04-22T08:56:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:56:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:56:28Z",115688066,"123ca2b3129d38e29c0d5992ecbed2eb","a04fc73588f3ffe1",1,1682153788962],["2023-04-22T08:56:28Z",115688550,"efdbac437ab63d0056db7944ac0c9b65","a9b1181bc0c78598",1,1682153788966],["2023-04-22T08:56:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:57:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:57:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:57:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:57:28Z",115699105,"567dafb493027f8a669adcc010d32110","e3be133197c6aa47",1,1682153848969],["2023-04-22T08:57:28Z",115699588,"567dafb493027f8a669adcc010d32110","e3be133197c6aa47",1,1682153848965],["2023-04-22T08:58:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:58:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:58:28Z",115711291,"21d243655ebc908285e52dba2c3580ff","51a3a5b744505fc1",1,1682153908960],["2023-04-22T08:58:28Z",115710808,"21d243655ebc908285e52dba2c3580ff","51a3a5b744505fc1",1,1682153908963],["2023-04-22T08:58:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T08:59:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:59:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:59:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:59:28Z",115721920,"fc71c7a93f323ebdff1f049c135664ec","d2167f87865c3aea",1,1682153968957],["2023-04-22T08:59:28Z",115722403,"fc71c7a93f323ebdff1f049c135664ec","d2167f87865c3aea",1,1682153968954],["2023-04-22T09:00:28Z",115733923,"e7f4dbf09d7b4c4f2c7e26f9049184c0","0e8b85f9ffe197a5",1,1682154028963],["2023-04-22T09:00:28Z",115734406,"e7f4dbf09d7b4c4f2c7e26f9049184c0","0e8b85f9ffe197a5",1,1682154028960],["2023-04-22T09:00:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:00:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:00:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T09:01:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:01:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:01:28Z",115747364,"275548d0061bd450a575613c59196723","950473ea00c51d0f",1,1682154088967],["2023-04-22T09:01:28Z",115747847,"275548d0061bd450a575613c59196723","950473ea00c51d0f",1,1682154088965],["2023-04-22T09:01:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:02:28Z",115761811,"daf31f17f49d490c83f13c353410698b","06200c4175726158",1,1682154148960],["2023-04-22T09:02:28Z",115761328,"daf31f17f49d490c83f13c353410698b","06200c4175726158",1,1682154148963],["2023-04-22T09:02:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:02:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:02:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T09:03:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:03:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:03:28Z",115772865,"4d398f1781b6849e18b4a49805d6d416","b30781b0e212ab49",1,1682154208965],["2023-04-22T09:03:28Z",115773350,"a529ad6df25d44cbc591c5f1814d0cc1","c6b23fb20021e49c",1,1682154208967],["2023-04-22T09:03:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:04:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:04:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:04:28Z",115788825,"31b151d76e41ca1db6f3269953fda85e","70740b7f8c392c25",1,1682154268967],["2023-04-22T09:04:28Z",115789312,"43cf1c738170373ef48170f359066959","bd97feaff7779eae",1,1682154268968],["2023-04-22T09:04:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T09:05:28Z",115802781,"8b0441d87cbcc474d664c99535ad669d","2a6c432dd8e4c3fd",1,1682154328967],["2023-04-22T09:05:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:05:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:05:28Z",115802297,"580d9d98d6b76d8d8444463a1a21627b","bbf61af646e16978",1,1682154328956],["2023-04-22T09:05:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:06:28Z",115813616,"4aa471d0f00740011ce8d66acc52281a","216cc953663aac68",1,1682154388948],["2023-04-22T09:06:28Z",115814099,"4aa471d0f00740011ce8d66acc52281a","216cc953663aac68",1,1682154388946],["2023-04-22T09:06:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:06:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:06:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T09:07:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:07:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:07:28Z",115825085,"efd0e34e9126172d47ded9fdddec5a7b","8ddb9404521f177b",1,1682154448967],["2023-04-22T09:07:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:07:28Z",115824600,"b0705f41034b923baad5b10e376a58de","e1f7bb8fd52d0a7b",1,1682154448962],["2023-04-22T09:08:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:08:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:08:28Z",115835645,"42bb8976800e050326fa14c25b281924","600873b41a6ff1b2",1,1682154508966],["2023-04-22T09:08:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:08:28Z",115836130,"984e5e6ca13c09397100769e6d336039","db4129d930407fc8",1,1682154508964]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"group2_cmdb_level","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"values":[["2023-04-22T09:09:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:09:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:09:28Z",115849354,"ff3d29f3ec4a8b2364d2a5ba7b0751be","66103693e7484b95",1,1682154568967],["2023-04-22T09:09:28Z",115849837,"ff3d29f3ec4a8b2364d2a5ba7b0751be","66103693e7484b95",1,1682154568963],["2023-04-22T09:09:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]]}]}]}
`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=system&q=select+mean%28%22usage%22%29+as+_value%2C+time+as+_time+from+cpu_summary+where+time+%3E+1677081599999000000+and+time+%3C+1677085659999000000++group+by+time%281m0s%29+limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T15:59:00Z",null],["2023-02-22T16:00:00Z",25.124152312094484],["2023-02-22T16:01:00Z",20.724334166696504],["2023-02-22T16:02:00Z",20.426171484280808],["2023-02-22T16:03:00Z",20.327529103992745]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:04:00Z",20.468538578157883],["2023-02-22T16:05:00Z",20.25296970605787],["2023-02-22T16:06:00Z",19.9283445874921],["2023-02-22T16:07:00Z",19.612237758778733],["2023-02-22T16:08:00Z",20.187296617920314]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:09:00Z",20.916380134086413],["2023-02-22T16:10:00Z",22.554908120339377],["2023-02-22T16:11:00Z",20.253084390783837],["2023-02-22T16:12:00Z",20.48536897192481],["2023-02-22T16:13:00Z",20.090785116663426]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:14:00Z",20.25654085898734],["2023-02-22T16:15:00Z",21.041731249213385],["2023-02-22T16:16:00Z",20.43003902957978],["2023-02-22T16:17:00Z",20.038367095325594],["2023-02-22T16:18:00Z",20.202399021312875]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:19:00Z",21.37467097743847],["2023-02-22T16:20:00Z",22.651718347719402],["2023-02-22T16:21:00Z",20.301023323252785],["2023-02-22T16:22:00Z",20.451627781431707],["2023-02-22T16:23:00Z",19.891683255113772]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:24:00Z",20.644901190626083],["2023-02-22T16:25:00Z",20.37609141634239],["2023-02-22T16:26:00Z",20.454340379883195],["2023-02-22T16:27:00Z",19.570824461410087],["2023-02-22T16:28:00Z",20.31326038669719]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:29:00Z",21.53592697328099],["2023-02-22T16:30:00Z",24.04803475336384],["2023-02-22T16:31:00Z",20.730816789762073],["2023-02-22T16:32:00Z",20.371870403348336],["2023-02-22T16:33:00Z",19.82545696862311]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:34:00Z",20.320976389889655],["2023-02-22T16:35:00Z",20.567491437854738],["2023-02-22T16:36:00Z",20.934958308411666],["2023-02-22T16:37:00Z",19.90507015314242],["2023-02-22T16:38:00Z",20.37676404541998]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:39:00Z",20.668093391150975],["2023-02-22T16:40:00Z",21.99879925023976],["2023-02-22T16:41:00Z",20.23986108096937],["2023-02-22T16:42:00Z",21.025451068689662],["2023-02-22T16:43:00Z",24.664738068080318]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:44:00Z",20.50489535135916],["2023-02-22T16:45:00Z",21.43855141688965],["2023-02-22T16:46:00Z",25.547292511592147],["2023-02-22T16:47:00Z",20.22969132310118],["2023-02-22T16:48:00Z",20.263914410308956]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:49:00Z",21.046247079107264],["2023-02-22T16:50:00Z",23.639822963990397],["2023-02-22T16:51:00Z",21.84574206076609],["2023-02-22T16:52:00Z",20.25510660626945],["2023-02-22T16:53:00Z",20.17809699916729]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:54:00Z",19.875349111182004],["2023-02-22T16:55:00Z",20.215643757678873],["2023-02-22T16:56:00Z",19.968096510353472],["2023-02-22T16:57:00Z",19.8493275944543],["2023-02-22T16:58:00Z",20.31881482976456]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:59:00Z",21.344305007289915],["2023-02-22T17:00:00Z",25.937044373952602],["2023-02-22T17:01:00Z",20.421952975501853],["2023-02-22T17:02:00Z",20.121773311320066],["2023-02-22T17:03:00Z",19.74360429634455]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T17:04:00Z",19.90800208328392],["2023-02-22T17:05:00Z",20.48559522490759],["2023-02-22T17:06:00Z",20.0645267193599],["2023-02-22T17:07:00Z",26.77071273642816]]}]}]}
`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=system&q=select+mean%28%22rate%22%29+as+_value%2C+time+as+_time+from+cpu_summary+where+time+%3E+1677081599999000000+and+time+%3C+1677085659999000000++group+by+time%281m0s%29+limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T15:59:00Z",null],["2023-02-22T16:00:00Z",25.124152312094484],["2023-02-22T16:01:00Z",20.724334166696504],["2023-02-22T16:02:00Z",20.426171484280808],["2023-02-22T16:03:00Z",20.327529103992745]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:04:00Z",20.468538578157883],["2023-02-22T16:05:00Z",20.25296970605787],["2023-02-22T16:06:00Z",19.9283445874921],["2023-02-22T16:07:00Z",19.612237758778733],["2023-02-22T16:08:00Z",20.187296617920314]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:09:00Z",20.916380134086413],["2023-02-22T16:10:00Z",22.554908120339377],["2023-02-22T16:11:00Z",20.253084390783837],["2023-02-22T16:12:00Z",20.48536897192481],["2023-02-22T16:13:00Z",20.090785116663426]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:14:00Z",20.25654085898734],["2023-02-22T16:15:00Z",21.041731249213385],["2023-02-22T16:16:00Z",20.43003902957978],["2023-02-22T16:17:00Z",20.038367095325594],["2023-02-22T16:18:00Z",20.202399021312875]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:19:00Z",21.37467097743847],["2023-02-22T16:20:00Z",22.651718347719402],["2023-02-22T16:21:00Z",20.301023323252785],["2023-02-22T16:22:00Z",20.451627781431707],["2023-02-22T16:23:00Z",19.891683255113772]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:24:00Z",20.644901190626083],["2023-02-22T16:25:00Z",20.37609141634239],["2023-02-22T16:26:00Z",20.454340379883195],["2023-02-22T16:27:00Z",19.570824461410087],["2023-02-22T16:28:00Z",20.31326038669719]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:29:00Z",21.53592697328099],["2023-02-22T16:30:00Z",24.04803475336384],["2023-02-22T16:31:00Z",20.730816789762073],["2023-02-22T16:32:00Z",20.371870403348336],["2023-02-22T16:33:00Z",19.82545696862311]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:34:00Z",20.320976389889655],["2023-02-22T16:35:00Z",20.567491437854738],["2023-02-22T16:36:00Z",20.934958308411666],["2023-02-22T16:37:00Z",19.90507015314242],["2023-02-22T16:38:00Z",20.37676404541998]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:39:00Z",20.668093391150975],["2023-02-22T16:40:00Z",21.99879925023976],["2023-02-22T16:41:00Z",20.23986108096937],["2023-02-22T16:42:00Z",21.025451068689662],["2023-02-22T16:43:00Z",24.664738068080318]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:44:00Z",20.50489535135916],["2023-02-22T16:45:00Z",21.43855141688965],["2023-02-22T16:46:00Z",25.547292511592147],["2023-02-22T16:47:00Z",20.22969132310118],["2023-02-22T16:48:00Z",20.263914410308956]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:49:00Z",21.046247079107264],["2023-02-22T16:50:00Z",23.639822963990397],["2023-02-22T16:51:00Z",21.84574206076609],["2023-02-22T16:52:00Z",20.25510660626945],["2023-02-22T16:53:00Z",20.17809699916729]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:54:00Z",19.875349111182004],["2023-02-22T16:55:00Z",20.215643757678873],["2023-02-22T16:56:00Z",19.968096510353472],["2023-02-22T16:57:00Z",19.8493275944543],["2023-02-22T16:58:00Z",20.31881482976456]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T16:59:00Z",21.344305007289915],["2023-02-22T17:00:00Z",25.937044373952602],["2023-02-22T17:01:00Z",20.421952975501853],["2023-02-22T17:02:00Z",20.121773311320066],["2023-02-22T17:03:00Z",19.74360429634455]],"partial":true}],"partial":true}]}
{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T17:04:00Z",19.90800208328392],["2023-02-22T17:05:00Z",20.48559522490759],["2023-02-22T17:06:00Z",20.0645267193599],["2023-02-22T17:07:00Z",26.77071273642816]]}]}]}
`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=custom_report_aggate&q=select+%22value%22+as+_value%2C+time+as+_time%2C%2A%3A%3Atag+from+bkmonitor_action_notice_api_call_count_total+where+time+%3E+1692584759999000000+and+time+%3C+1692585659999000000+and+%28notice_way%3D%27weixin%27+and+status%3D%27failed%27%29++limit+100000000+slimit+100000000`: `{"results":[{"statement_id":0,"series":[{"name":"bkmonitor_action_notice_api_call_count_total","columns":["_time","_value","job","notice_way","status","target"],"values":[["2023-08-21T02:26:34.603Z",14568,"SLI","weixin","failed","unknown"],["2023-08-21T02:27:33.198Z",14568,"SLI","weixin","failed","unknown"],["2023-08-21T02:28:32.629Z",14568,"SLI","weixin","failed","unknown"],["2023-08-21T02:29:36.848Z",14568,"SLI","weixin","failed","unknown"],["2023-08-21T02:32:35.819Z",14570,"SLI","weixin","failed","unknown"],["2023-08-21T02:32:55.496Z",14569,"SLI","weixin","failed","unknown"],["2023-08-21T02:33:39.496Z",14570,"SLI","weixin","failed","unknown"],["2023-08-21T02:34:43.517Z",14570,"SLI","weixin","failed","unknown"],["2023-08-21T02:37:35.203Z",14570,"SLI","weixin","failed","unknown"],["2023-08-21T02:38:32.111Z",14570,"SLI","weixin","failed","unknown"],["2023-08-21T02:39:32.135Z",14570,"SLI","weixin","failed","unknown"],["2023-08-21T02:40:40.788Z",14570,"SLI","weixin","failed","unknown"]]}]}]}
`,
		`victoria_metric/api`: `{"result": true, "code":"00", "data":{}}`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=system&q=select+count%28%22rate%22%29+as+_value%2C+time+as+_time+from+cpu_summary+where+time+%3E+1677081599999000000+and+time+%3C+1677085659999000000++group+by+time%281m0s%29+limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T15:59:00Z",null],["2023-02-22T16:00:00Z",25.124152312094484],["2023-02-22T16:01:00Z",20.724334166696504],["2023-02-22T16:02:00Z",20.426171484280808],["2023-02-22T16:03:00Z",20.327529103992745]],"partial":true}],"partial":true}]}
`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=system&q=select+count%28%22usage%22%29+as+_value%2C+time+as+_time+from+cpu_summary+where+time+%3E+1677081599999000000+and+time+%3C+1677085659999000000++group+by+time%281m0s%29+limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"statement_id":0,"series":[{"name":"cpu_summary","columns":["_time","_value"],"values":[["2023-02-22T15:59:00Z",null],["2023-02-22T16:00:00Z",25.124152312094484],["2023-02-22T16:01:00Z",20.724334166696504],["2023-02-22T16:02:00Z",20.426171484280808],["2023-02-22T16:03:00Z",20.327529103992745]],"partial":true}],"partial":true}]}
`,
	}, log.OtLogger)

	tsdb.SetStorage(strconv.FormatInt(victoriaMetricsStorageId, 10), &tsdb.Storage{
		Type: consul.VictoriaMetricsStorageType,
		Instance: &victoriaMetrics.Instance{
			Ctx:                  ctx,
			Address:              "victoria_metric",
			UriPath:              "api",
			Curl:                 mockCurl,
			InfluxCompatible:     true,
			UseNativeOr:          true,
			AuthenticationMethod: "token",
		},
	})
	tsdb.SetStorage(strconv.FormatInt(influxdbStorageId, 10), &tsdb.Storage{
		Type: consul.InfluxDBStorageType,
		Instance: tsdbInfluxdb.NewInstance(
			context.TODO(),
			tsdbInfluxdb.Options{
				Host:      "127.0.0.1",
				Port:      80,
				Curl:      mockCurl,
				ChunkSize: 10,
				MaxSlimit: 1e8,
				MaxLimit:  1e8,
				Timeout:   time.Hour,
			},
		),
	})

	mock.SetRedisClient(context.TODO(), "test")
	return mockCurl
}

func TestQueryTs(t *testing.T) {
	ctx := context.Background()
	log.InitTestLogger()
	mockData(ctx, "handler_test", "handler_test")

	testCases := map[string]struct {
		query  string
		result string
	}{
		"test query": {
			query:  `{"space_uid":"influxdb","query_list":[{"data_source":"","table_id":"system.cpu_summary","field_name":"usage","field_list":null,"function":[{"method":"mean","without":false,"dimensions":[],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"avg_over_time","window":"60s","position":0,"vargs_list":null},"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1677081600000,25.124152312094484],[1677081660000,20.724334166696504],[1677081720000,20.426171484280808],[1677081780000,20.327529103992745],[1677081840000,20.468538578157883],[1677081900000,20.25296970605787],[1677081960000,19.9283445874921],[1677082020000,19.612237758778733],[1677082080000,20.187296617920314],[1677082140000,20.916380134086413],[1677082200000,22.554908120339377],[1677082260000,20.253084390783837],[1677082320000,20.48536897192481],[1677082380000,20.090785116663426],[1677082440000,20.25654085898734],[1677082500000,21.041731249213385],[1677082560000,20.43003902957978],[1677082620000,20.038367095325594],[1677082680000,20.202399021312875],[1677082740000,21.37467097743847],[1677082800000,22.651718347719402],[1677082860000,20.301023323252785],[1677082920000,20.451627781431707],[1677082980000,19.891683255113772],[1677083040000,20.644901190626083],[1677083100000,20.37609141634239],[1677083160000,20.454340379883195],[1677083220000,19.570824461410087],[1677083280000,20.31326038669719],[1677083340000,21.53592697328099],[1677083400000,24.04803475336384],[1677083460000,20.730816789762073],[1677083520000,20.371870403348336],[1677083580000,19.82545696862311],[1677083640000,20.320976389889655],[1677083700000,20.567491437854738],[1677083760000,20.934958308411666],[1677083820000,19.90507015314242],[1677083880000,20.37676404541998],[1677083940000,20.668093391150975],[1677084000000,21.99879925023976],[1677084060000,20.23986108096937],[1677084120000,21.025451068689662],[1677084180000,24.664738068080318],[1677084240000,20.50489535135916],[1677084300000,21.43855141688965],[1677084360000,25.547292511592147],[1677084420000,20.22969132310118],[1677084480000,20.263914410308956],[1677084540000,21.046247079107264],[1677084600000,23.639822963990397],[1677084660000,21.84574206076609],[1677084720000,20.25510660626945],[1677084780000,20.17809699916729],[1677084840000,19.875349111182004],[1677084900000,20.215643757678873],[1677084960000,19.968096510353472],[1677085020000,19.8493275944543],[1677085080000,20.31881482976456],[1677085140000,21.344305007289915],[1677085200000,25.937044373952602],[1677085260000,20.421952975501853],[1677085320000,20.121773311320066],[1677085380000,19.74360429634455],[1677085440000,19.90800208328392],[1677085500000,20.48559522490759],[1677085560000,20.0645267193599]]}]}`,
		},
		"test query by different metric dims": {
			query: `{
	"space_uid": "a_1068",
	"query_list": [{
		"data_source": "",
		"table_id": "",
		"field_name": "container_cpu_usage_seconds_total",
		"field_list": null,
		"function": [{
			"method": "max",
			"without": false,
			"dimensions": ["bcs_cluster_id", "node"],
			"dimensions": ["bcs_cluster_id", "node", "pod_name"],
			"position": 0,
			"args_list": null,
			"vargs_list": null
		}],
		"time_aggregation": {
			"function": "",
			"window": "",
			"position": 0,
			"vargs_list": null
		},
		"reference_name": "a",
		"dimensions": null,
		"limit": 0,
		"timestamp": null,
		"start_or_end": 0,
		"vector_offset": 0,
		"offset": "",
		"offset_forward": false,
		"slimit": 0,
		"soffset": 0,
		"conditions": {
			"field_list": [{
				"field_name": "pod_name",
				"value": ["kube-apiserver"],
				"op": "req"
			}, {
				"field_name": "bcs_cluster_id",
				"value": ["test"],
				"op": "eq"
			}],
			"condition_list": ["and", "and"]
		},
		"keep_columns": null
	}, {
		"data_source": "",
		"table_id": "",
		"field_name": "kube_node_status_capacity_cpu_cores",
		"field_list": null,
		"function": [{
			"method": "mean",
			"without": false,
			"dimensions": ["bcs_cluster_id", "node"],
			"position": 0,
			"args_list": null,
			"vargs_list": null
		}],
		"time_aggregation": {
			"function": "",
			"window": "",
			"position": 0,
			"vargs_list": null
		},
		"reference_name": "b",
		"dimensions": null,
		"limit": 0,
		"timestamp": null,
		"start_or_end": 0,
		"vector_offset": 0,
		"offset": "",
		"offset_forward": false,
		"slimit": 0,
		"soffset": 0,
		"conditions": {
			"field_list": [{
				"field_name": "bcs_cluster_id",
				"value": ["test"],
				"op": "eq"
			}],
			"condition_list": ["and"]
		},
		"keep_columns": null
	}],
	"metric_merge": "a/b",
	"result_columns": null,
	"start_time": "1682585834",
	"end_time": "1682587634",
	"step": "60s"
}`,
			result: ``,
		},
		"test lost sample in increase": {
			query:  `{"space_uid":"a_100147","query_list":[{"data_source":"bkmonitor","table_id":"custom_report_aggate.base","field_name":"bkmonitor_action_notice_api_call_count_total","field_list":null,"function":null,"time_aggregation":{"function":"increase","window":"5m0s","position":0,"vargs_list":null},"reference_name":"a","dimensions":null,"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"notice_way","value":["weixin"],"op":"eq"},{"field_name":"status","value":["failed"],"op":"eq"}],"condition_list":["and"]},"keep_columns":null}],"metric_merge":"a","result_columns":null,"start_time":"1692585000","end_time":"1692585600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["job","notice_way","status","target"],"group_values":["SLI","weixin","failed","unknown"],"values":[[1692585000000,0],[1692585060000,0],[1692585120000,16629.322052596937],[1692585180000,18016.221027991163],[1692585240000,18878.885417156103],[1692585300000,19426.666666666664],[1692585360000,21085.29945653025],[1692585420000,0],[1692585480000,0],[1692585540000,0],[1692585600000,0]]}]}`,
		},
		"test query support fuzzy __name__": {
			query: `{
    "space_uid": "influxdb",
    "query_list": [
        {
            "data_source": "",
            "table_id": "system.cpu_summary",
            "field_name": ".*",
			"is_regexp": true,
            "field_list": null,
            "function": [
                {
                    "method": "mean",
                    "without": false,
                    "dimensions": [],
                    "position": 0,
                    "args_list": null,
                    "vargs_list": null
                }
            ],
            "time_aggregation": {
                "function": "avg_over_time",
                "window": "60s",
                "position": 0,
                "vargs_list": null
            },
            "reference_name": "a",
            "dimensions": [],
            "limit": 0,
            "timestamp": null,
            "start_or_end": 0,
            "vector_offset": 0,
            "offset": "",
            "offset_forward": false,
            "slimit": 0,
            "soffset": 0,
            "conditions": {
                "field_list": [],
                "condition_list": []
            },
            "keep_columns": [
                "_time",
                "a"
            ]
        }
    ],
    "metric_merge": "a",
    "result_columns": null,
    "start_time": "1677081600",
    "end_time": "1677085600",
    "step": "60s"
}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1677081600000,25.124152312094484],[1677081660000,20.724334166696504],[1677081720000,20.426171484280808],[1677081780000,20.327529103992745],[1677081840000,20.468538578157883],[1677081900000,20.25296970605787],[1677081960000,19.9283445874921],[1677082020000,19.612237758778733],[1677082080000,20.187296617920314],[1677082140000,20.916380134086413],[1677082200000,22.554908120339377],[1677082260000,20.253084390783837],[1677082320000,20.48536897192481],[1677082380000,20.090785116663426],[1677082440000,20.25654085898734],[1677082500000,21.041731249213385],[1677082560000,20.43003902957978],[1677082620000,20.038367095325594],[1677082680000,20.202399021312875],[1677082740000,21.37467097743847],[1677082800000,22.651718347719402],[1677082860000,20.301023323252785],[1677082920000,20.451627781431707],[1677082980000,19.891683255113772],[1677083040000,20.644901190626083],[1677083100000,20.37609141634239],[1677083160000,20.454340379883195],[1677083220000,19.570824461410087],[1677083280000,20.31326038669719],[1677083340000,21.53592697328099],[1677083400000,24.04803475336384],[1677083460000,20.730816789762073],[1677083520000,20.371870403348336],[1677083580000,19.82545696862311],[1677083640000,20.320976389889655],[1677083700000,20.567491437854738],[1677083760000,20.934958308411666],[1677083820000,19.90507015314242],[1677083880000,20.37676404541998],[1677083940000,20.668093391150975],[1677084000000,21.99879925023976],[1677084060000,20.23986108096937],[1677084120000,21.025451068689662],[1677084180000,24.664738068080318],[1677084240000,20.50489535135916],[1677084300000,21.43855141688965],[1677084360000,25.547292511592147],[1677084420000,20.22969132310118],[1677084480000,20.263914410308956],[1677084540000,21.046247079107264],[1677084600000,23.639822963990397],[1677084660000,21.84574206076609],[1677084720000,20.25510660626945],[1677084780000,20.17809699916729],[1677084840000,19.875349111182004],[1677084900000,20.215643757678873],[1677084960000,19.968096510353472],[1677085020000,19.8493275944543],[1677085080000,20.31881482976456],[1677085140000,21.344305007289915],[1677085200000,25.937044373952602],[1677085260000,20.421952975501853],[1677085320000,20.121773311320066],[1677085380000,19.74360429634455],[1677085440000,19.90800208328392],[1677085500000,20.48559522490759],[1677085560000,20.0645267193599]]}]}`,
		},
		"test query support fuzzy __name__ with count": {
			query: `{
    "space_uid": "influxdb",
    "query_list": [
        {
            "data_source": "",
            "table_id": "system.cpu_summary",
            "field_name": ".*",
			"is_regexp": true,
            "field_list": null,
            "function": [
                {
                    "method": "sum",
                    "without": false,
                    "dimensions": [],
                    "position": 0,
                    "args_list": null,
                    "vargs_list": null
                }
            ],
            "time_aggregation": {
                "function": "count_over_time",
                "window": "60s",
                "position": 0,
                "vargs_list": null
            },
            "reference_name": "a",
            "dimensions": [],
            "limit": 0,
            "timestamp": null,
            "start_or_end": 0,
            "vector_offset": 0,
            "offset": "",
            "offset_forward": false,
            "slimit": 0,
            "soffset": 0,
            "conditions": {
                "field_list": [],
                "condition_list": []
            },
            "keep_columns": [
                "_time",
                "a"
            ]
        }
    ],
    "metric_merge": "a",
    "result_columns": null,
    "start_time": "1677081600",
    "end_time": "1677085600",
    "step": "60s"
}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1677081600000,25],[1677081660000,20],[1677081720000,20],[1677081780000,20]]}]}`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			body := []byte(c.query)
			query := &structured.QueryTs{}
			err := json.Unmarshal(body, query)
			assert.Nil(t, err)

			res, err := queryTs(ctx, query)
			assert.Nil(t, err)
			out, err := json.Marshal(res)
			assert.Nil(t, err)
			actual := string(out)
			fmt.Printf("ActualResult: %v\n", actual)
			assert.Equal(t, c.result, actual)
		})
	}
}

// TestQueryExemplar comment lint rebel
func TestQueryExemplar(t *testing.T) {
	ctx := context.Background()
	log.InitTestLogger()
	mockData(ctx, "handler_test", "handler_test")

	body := []byte(`{"space_uid":"a_7","query_list":[{"data_source":"","table_id":"pushgateway_bkmonitor_unify_query.group2_cmdb_level","field_name":"unify_query_request_count_total","field_list":["bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"function":null,"time_aggregation":{"function":"","window":"","position":0,"vargs_list":null},"reference_name":"","dimensions":null,"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"bk_obj_id","value":["module"],"op":"contains"},{"field_name":"ip","value":["127.0.0.2"],"op":"contains"},{"field_name":"bk_inst_id","value":["14261"],"op":"contains"},{"field_name":"bk_biz_id","value":["7"],"op":"contains"}],"condition_list":["and","and","and"]},"keep_columns":null}],"metric_merge":"","result_columns":null,"start_time":"1682149980","end_time":"1682154605","step":"","down_sample_range":"1m"}`)

	query := &structured.QueryTs{}
	err := json.Unmarshal(body, query)
	assert.Nil(t, err)

	res, err := queryExemplar(ctx, query)
	assert.Nil(t, err)
	out, err := json.Marshal(res)
	assert.Nil(t, err)
	actual := string(out)
	assert.Equal(t, `{"series":[{"name":"_result0","metric_name":"metric_value","columns":["_time","_value","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"types":["string","float","string","string","float","float"],"group_keys":[],"group_values":[],"values":[["2023-04-22T07:53:28Z",114938716,"d8952469b9014ed6b36c19d396b15c61","0a97123ee5ad7fd8",1,1682150008967],["2023-04-22T07:53:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:53:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:53:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:53:28Z",114939201,"771073eb573336a6d3365022a512d6d8","fca46f1c065452e8",1,1682150008969],["2023-04-22T07:54:28Z",114949368,"5b4931bbeb7bf497ff46d9cd9579ab60","0b0713e4e0106e55",1,1682150068965],["2023-04-22T07:54:28Z",114949853,"7c3c66f8763071d315fe8136bf8ff35c","159d9534754dc66d",1,1682150068965],["2023-04-22T07:54:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:54:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:54:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:55:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:55:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:55:28Z",114959046,"c9659d0b28bdb0c8afbd21aedd6bacd3","94bc25ffa3cc44e5",1,1682150128965],["2023-04-22T07:55:28Z",114959529,"c9659d0b28bdb0c8afbd21aedd6bacd3","94bc25ffa3cc44e5",1,1682150128962],["2023-04-22T07:55:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:56:28Z",114968813,"5def2a6568efe57199022da6f7cfcf3f","1d761b6cc2aabe5e",1,1682150188964],["2023-04-22T07:56:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:56:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:56:28Z",114969300,"ca18ec94669be35fee1b4ae4a2e3df2a","c113aa392812404a",1,1682150188968],["2023-04-22T07:56:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:57:28Z",114986134,"ab5b57bea973133a5cb1f89ee93ffd5a","191d6c2087b110de",1,1682150248965],["2023-04-22T07:57:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:57:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:57:28Z",114986622,"032fa9e22eaa486349f3a9d8ac1a0c76","7832acea944dc180",1,1682150248967],["2023-04-22T07:57:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:58:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:58:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:58:28Z",114997601,"0bd0b98c89250d1ab3e4fc77cdc9d619","4d3a86e07ac11e04",1,1682150308961],["2023-04-22T07:58:28Z",114998085,"09aeca94699ae82eec038c85573b68c4","687b908edb32b09f",1,1682150308967],["2023-04-22T07:58:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:59:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T07:59:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T07:59:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T07:59:28Z",115009204,"595fb8f3671365cc1ad920d30a7c9c6e","d1f36e8cbdb25ce8",1,1682150368961],["2023-04-22T07:59:28Z",115008721,"595fb8f3671365cc1ad920d30a7c9c6e","d1f36e8cbdb25ce8",1,1682150368964],["2023-04-22T08:00:28Z",115021473,"5a6dbfa27835ac9b22d8a795477e3155","1954ae8cddc3a6fc",1,1682150428955],["2023-04-22T08:00:28Z",115020990,"5a6dbfa27835ac9b22d8a795477e3155","1954ae8cddc3a6fc",1,1682150428959],["2023-04-22T08:00:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:00:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:00:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:01:28Z",115031442,"0ae45ad8874d96426b65416a67c82531","1c7968f7293a0af2",1,1682150488966],["2023-04-22T08:01:28Z",115031925,"0ae45ad8874d96426b65416a67c82531","1c7968f7293a0af2",1,1682150488960],["2023-04-22T08:01:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:01:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:01:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:02:28Z",115042237,"dac723082fea9d0863af70e751ef565e","34eb4eb32a60309e",1,1682150548960],["2023-04-22T08:02:28Z",115042720,"dac723082fea9d0863af70e751ef565e","34eb4eb32a60309e",1,1682150548958],["2023-04-22T08:02:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:02:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:02:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:03:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:03:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:03:28Z",115054635,"2f5205ecfff84e47bd6331e54e4d3e40","6fc8350f2318a7ca",1,1682150608962],["2023-04-22T08:03:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:03:28Z",115054151,"c4ed02cb38548d62bfae43dcd42a4b55","dfb56f2c0a0deb30",1,1682150608965],["2023-04-22T08:04:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:04:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:04:28Z",115063742,"2e572a096c8cf7e5a41e399c638b0e04","5b4471f3c41cb3b8",1,1682150668968],["2023-04-22T08:04:28Z",115063258,"32d7b9135f692c1628c9e3ba337d3a84","6a445c21f73bc40a",1,1682150668957],["2023-04-22T08:04:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:05:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:05:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:05:28Z",115079908,"bbf470769dbed5ce874134f989116bf5","6a24c9711646a433",1,1682150728966],["2023-04-22T08:05:28Z",115079419,"a6bc1c857d21aeba6951cd290ede7d6d","a1192477fcc2c445",1,1682150728957],["2023-04-22T08:05:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:06:28Z",115089527,"c583995aac74569636719ebc2dcf414a","10d77bd592504036",1,1682150788967],["2023-04-22T08:06:28Z",115090010,"c583995aac74569636719ebc2dcf414a","10d77bd592504036",1,1682150788964],["2023-04-22T08:06:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:06:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:06:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:07:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:07:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:07:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:07:28Z",115104143,"3944a09230dcf8b027c332b6b760a4f6","eb7bc4cd762e225c",1,1682150848966],["2023-04-22T08:07:28Z",115104626,"3944a09230dcf8b027c332b6b760a4f6","eb7bc4cd762e225c",1,1682150848963],["2023-04-22T08:08:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:08:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:08:28Z",115117650,"392ba52dda51ed66211c49fea3adfff0","44d3761ef5df2a60",1,1682150908968],["2023-04-22T08:08:28Z",115117162,"689739026da9b228529a3ebb63fb3756","6636e804a8c7f830",1,1682150908968],["2023-04-22T08:08:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:09:28Z",115126002,"f2d171da57dae50fb9c7af0e04655937","2515b8271d896683",1,1682150968878],["2023-04-22T08:09:28Z",115125519,"c084597a13d5a368329adf5791a9bd5b","2d7a037191f6825b",1,1682150968884],["2023-04-22T08:09:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:09:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:09:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:10:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:10:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:10:28Z",115136231,"aef23b75f180c0f6b80b052e1c5749ad","41f16325f7ec7207",1,1682151028962],["2023-04-22T08:10:28Z",115135748,"2a9a2c8c5f6f7b4c7bc64db82cc25ece","5c36f565160affe7",1,1682151028969],["2023-04-22T08:10:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:11:28Z",115149569,"1586056eee1d191386d8784d7b3f3f34","0ac1f67fb5dc05c7",1,1682151088970],["2023-04-22T08:11:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:11:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:11:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:11:28Z",115149085,"fb5227061604428741a1a45303321abd","e1b026cffa189a0d",1,1682151088967],["2023-04-22T08:12:28Z",115163411,"aceec42255f7f4e5e157530531dceec7","1c217159f11008b5",1,1682151148964],["2023-04-22T08:12:28Z",115162928,"aceec42255f7f4e5e157530531dceec7","1c217159f11008b5",1,1682151148967],["2023-04-22T08:12:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:12:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:12:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:13:28Z",115173673,"1acd32ff4227a91bcbe2e8a4f2d1e0cf","188222897a849cbf",1,1682151208175],["2023-04-22T08:13:28Z",115174156,"1acd32ff4227a91bcbe2e8a4f2d1e0cf","188222897a849cbf",1,1682151208171],["2023-04-22T08:13:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:13:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:13:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:14:28Z",115181241,"8ab09a1fba0a83675f5213f28ecf7ab0","048a63198da516d7",1,1682151268964],["2023-04-22T08:14:28Z",115181724,"8ab09a1fba0a83675f5213f28ecf7ab0","048a63198da516d7",1,1682151268961],["2023-04-22T08:14:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:14:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:14:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:15:28Z",115198050,"b183c974d457eceedba600a7ae7293bb","16375e22aa7686d8",1,1682151328948],["2023-04-22T08:15:28Z",115197567,"b183c974d457eceedba600a7ae7293bb","16375e22aa7686d8",1,1682151328951],["2023-04-22T08:15:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:15:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:15:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:16:28Z",115210906,"dc9d05c9878bc4e2dcbe17b9d9c92bc6","006c18da99f83a21",1,1682151388967],["2023-04-22T08:16:28Z",115211389,"76a0c8887915c5f374382c88b787392e","15a9434f4310b900",1,1682151388962],["2023-04-22T08:16:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:16:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:16:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:17:28Z",115222732,"5650177cd1f79ae792761118f91d71b4","2291ee695308c91a",1,1682151448859],["2023-04-22T08:17:28Z",115223215,"5650177cd1f79ae792761118f91d71b4","2291ee695308c91a",1,1682151448855],["2023-04-22T08:17:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:17:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:17:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:18:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:18:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:18:28Z",115231442,"0b523dd2f43ead213b42cd8f67fc27fb","8dd7adf4186e2bd4",1,1682151508966],["2023-04-22T08:18:28Z",115231926,"24ccf66130d5a1b858600c1a6aa916f3","8f30bfca7b4bb53e",1,1682151508968],["2023-04-22T08:18:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:19:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:19:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:19:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:19:28Z",115242028,"de8f959bdfeb8a5aca2bb123b3fa235d","d5b4ab7dad84b1ac",1,1682151568967],["2023-04-22T08:19:28Z",115242511,"de8f959bdfeb8a5aca2bb123b3fa235d","d5b4ab7dad84b1ac",1,1682151568965],["2023-04-22T08:20:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:20:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:20:28Z",115250857,"eabaf0a4308f2cb4e4f4ba28e758e026","5f917b5184d48ac9",1,1682151628962],["2023-04-22T08:20:28Z",115250374,"eabaf0a4308f2cb4e4f4ba28e758e026","5f917b5184d48ac9",1,1682151628967],["2023-04-22T08:20:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:21:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:21:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:21:28Z",115262828,"41f32db3a5dbae3d8131704786d3b9f5","a2fe2bd779d50031",1,1682151688962],["2023-04-22T08:21:28Z",115263311,"41f32db3a5dbae3d8131704786d3b9f5","a2fe2bd779d50031",1,1682151688954],["2023-04-22T08:21:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:22:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:22:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:22:28Z",115272127,"a444dd5f641e2fbf8f4f0fad31a43755","b97c208449487be7",1,1682151748967],["2023-04-22T08:22:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:22:28Z",115272611,"e0aa20bb79008be034cb5fb51a55e7be","fba717982f19aee1",1,1682151748965],["2023-04-22T08:23:28Z",115282567,"4fcfab1357beb0f1ff3968795564e8ba","20d7bb6662da56fc",1,1682151808957],["2023-04-22T08:23:28Z",115283050,"4fcfab1357beb0f1ff3968795564e8ba","20d7bb6662da56fc",1,1682151808954],["2023-04-22T08:23:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:23:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:23:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:24:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:24:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:24:28Z",115294443,"c48444259faf9c8bbba4236729e14979","8efc0b4772479d87",1,1682151868961],["2023-04-22T08:24:28Z",115293959,"d821b5094ccfc3afda6defa6741c0b1e","ab31b554831614a4",1,1682151868933],["2023-04-22T08:24:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:25:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:25:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:25:28Z",115305763,"634cdecba4540a9b15aa8a96474755d1","9bbdf0bf4daba865",1,1682151928969],["2023-04-22T08:25:28Z",115306246,"634cdecba4540a9b15aa8a96474755d1","9bbdf0bf4daba865",1,1682151928965],["2023-04-22T08:25:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:26:28Z",115313747,"40587b86461c7112efcb4097b82cf219","25787181e25dfdf3",1,1682151988957],["2023-04-22T08:26:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:26:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:26:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:26:28Z",115314231,"997d4d5d73426d66bf3c511624501753","fa422b5124625654",1,1682151988969],["2023-04-22T08:27:28Z",115324910,"ae21a9fcae09af13debaf56ac257003a","3c5e0082e4051153",1,1682152048965],["2023-04-22T08:27:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:27:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:27:28Z",115324425,"b2b216720a0439bc2287b49b19177ebe","7c42dddc736039ee",1,1682152048968],["2023-04-22T08:27:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:28:28Z",115337517,"74c5ecc1c2292513a37f239a853e3fe7","24f42a3ad6a3ab43",1,1682152108965],["2023-04-22T08:28:28Z",115338000,"74c5ecc1c2292513a37f239a853e3fe7","24f42a3ad6a3ab43",1,1682152108961],["2023-04-22T08:28:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:28:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:28:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:29:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:29:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:29:28Z",115349761,"8ba4ae67dc9775191fd7bf4b4f8cce94","61351ca322215086",1,1682152168967],["2023-04-22T08:29:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:29:28Z",115349276,"c9ad084b420b818c5d1ac3d4c853f18c","cf67a7588e41f316",1,1682152168970],["2023-04-22T08:30:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:30:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:30:28Z",115361417,"7d2f78ce24e3ade2e29d5514c0736143","566f3685f55e610f",1,1682152228963],["2023-04-22T08:30:28Z",115360934,"7d2f78ce24e3ade2e29d5514c0736143","566f3685f55e610f",1,1682152228966],["2023-04-22T08:30:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:31:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:31:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:31:28Z",115371502,"c189c35c14ae1e886e9d317652546af7","3ec0c24ee8ade2cd",1,1682152288959],["2023-04-22T08:31:28Z",115371986,"ce6dfccc1a569043e7a64f59d6ae210e","b289ea384ddd9e3e",1,1682152288966],["2023-04-22T08:31:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:32:28Z",115383620,"5482659f94a769ef70dc8243771c3ad1","26a60035b3bdbda2",1,1682152348949],["2023-04-22T08:32:28Z",115383137,"5482659f94a769ef70dc8243771c3ad1","26a60035b3bdbda2",1,1682152348952],["2023-04-22T08:32:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:32:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:32:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:33:28Z",115398302,"8076c97c8553ae4c484f4cbd56ca14c3","3810f43be4e95c84",1,1682152408964],["2023-04-22T08:33:28Z",115397819,"8076c97c8553ae4c484f4cbd56ca14c3","3810f43be4e95c84",1,1682152408967],["2023-04-22T08:33:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:33:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:33:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:34:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:34:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:34:28Z",115409417,"249d5d3fc819d18145b12f7202168e79","44f7eb14cdc234f7",1,1682152468963],["2023-04-22T08:34:28Z",115408934,"249d5d3fc819d18145b12f7202168e79","44f7eb14cdc234f7",1,1682152468967],["2023-04-22T08:34:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:35:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:35:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:35:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:35:28Z",115417148,"c3bd264a05effac853a5170173516af8","e2e97ddc9c392638",1,1682152528963],["2023-04-22T08:35:28Z",115416665,"c3bd264a05effac853a5170173516af8","e2e97ddc9c392638",1,1682152528966],["2023-04-22T08:36:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:36:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:36:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:36:28Z",115431074,"2d3ecc1056447120048c227b048ce0ae","dd7f8dc416754bf8",1,1682152588969],["2023-04-22T08:36:28Z",115431557,"2d3ecc1056447120048c227b048ce0ae","dd7f8dc416754bf8",1,1682152588965],["2023-04-22T08:37:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:37:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:37:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:37:28Z",115443915,"1d9e62409af4ab52b33e1bd1699bc780","eb9b452580af0ad1",1,1682152648961],["2023-04-22T08:37:28Z",115444398,"1d9e62409af4ab52b33e1bd1699bc780","eb9b452580af0ad1",1,1682152648954],["2023-04-22T08:38:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:38:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:38:28Z",115458081,"60cf809ac0ae93262b8ac2fe5db95065","a42e57f60c06eda2",1,1682152708969],["2023-04-22T08:38:28Z",115457596,"94f464096a013e4f61b3ed7dd04c93a3","a8709d02b9847220",1,1682152708967],["2023-04-22T08:38:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:39:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:39:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:39:28Z",115471863,"9b223f1cd429c0ab8f6157a31fb29630","47c34acfd33c1716",1,1682152768966],["2023-04-22T08:39:28Z",115472346,"9b223f1cd429c0ab8f6157a31fb29630","47c34acfd33c1716",1,1682152768962],["2023-04-22T08:39:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:40:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:40:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:40:28Z",115484160,"b20722843abac3a54b457c188dbaea15","a746bea06ff1baf9",1,1682152828960],["2023-04-22T08:40:28Z",115483677,"b20722843abac3a54b457c188dbaea15","a746bea06ff1baf9",1,1682152828964],["2023-04-22T08:40:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:41:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:41:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:41:28Z",115494091,"e3cb92488f83fc5a24122113b5c453fd","af36db2f540f133a",1,1682152888966],["2023-04-22T08:41:28Z",115494574,"e3cb92488f83fc5a24122113b5c453fd","af36db2f540f133a",1,1682152888963],["2023-04-22T08:41:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:42:28Z",115505010,"188a66107ecba773063eea56f9107eb6","073e73fa7ca1c993",1,1682152948966],["2023-04-22T08:42:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:42:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:42:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:42:28Z",115504526,"193b160f5c8fafb283569391d4e2ba90","ff69aa31aea248c8",1,1682152948952],["2023-04-22T08:43:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:43:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:43:28Z",115515895,"3d099ac99237f61536dc7d2d27d99bb8","3fb14916f9e591f7",1,1682153008967],["2023-04-22T08:43:28Z",115516380,"01691333a4f11f215883c05c51e36fc1","62017c0603dff189",1,1682153008965],["2023-04-22T08:43:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:44:28Z",115528870,"73e5b3d7b91aa2c89db08d39eef6b4f5","28d1bf05bd220383",1,1682153068969],["2023-04-22T08:44:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:44:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:44:28Z",115528384,"f558ca47fb57af24481b18dfd62dd9b7","c595af435ccff1ea",1,1682153068968],["2023-04-22T08:44:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:45:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:45:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:45:28Z",115540553,"cb23ec363097810291c106e5e2f1a8eb","99bd004b666ec085",1,1682153128960],["2023-04-22T08:45:28Z",115541036,"cb23ec363097810291c106e5e2f1a8eb","99bd004b666ec085",1,1682153128956],["2023-04-22T08:45:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:46:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:46:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:46:28Z",115552374,"e5cd69540f3e8bcde571db176b9a36d0","40296b35be3eeeb2",1,1682153188892],["2023-04-22T08:46:28Z",115551891,"e5cd69540f3e8bcde571db176b9a36d0","40296b35be3eeeb2",1,1682153188895],["2023-04-22T08:46:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:47:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:47:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:47:28Z",115566396,"a42a93b3c057f391eee6f26b7c5aa7ee","8a037eb6d56afa12",1,1682153248968],["2023-04-22T08:47:28Z",115565912,"70278d96624f975c43c5a4b15ac77f37","b5015c7dc52f7e78",1,1682153248970],["2023-04-22T08:47:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:48:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:48:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:48:28Z",115578955,"c8a8313fc6922dda66ce0ab7b186b282","7ce523c25706efd8",1,1682153308966],["2023-04-22T08:48:28Z",115579439,"1fff8976d9a43612c0c22372923b7321","8afb144b049ea2df",1,1682153308967],["2023-04-22T08:48:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:49:28Z",115589237,"310be0dc38fbd8360719f73a0b3b6bfe","0e9e91d5b70a9ec0",1,1682153368966],["2023-04-22T08:49:28Z",115589721,"9d9eb773f59f447fa56f47a09443a029","3211971d3d050cf9",1,1682153368965],["2023-04-22T08:49:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:49:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:49:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:50:28Z",115601705,"d3f670b536b0b4ab38c638517c41e303","31b1f14e2b7de574",1,1682153428964],["2023-04-22T08:50:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:50:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:50:28Z",115602191,"e038bea53a99e80d2e9eab7fb3234306","bc21eb298f0c8bdc",1,1682153428962],["2023-04-22T08:50:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:51:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:51:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:51:28Z",115617161,"59a5d61a87444fd828449900a3aaf0bb","a784d38118cf457b",1,1682153488963],["2023-04-22T08:51:28Z",115616678,"59a5d61a87444fd828449900a3aaf0bb","a784d38118cf457b",1,1682153488966],["2023-04-22T08:51:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:52:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:52:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:52:28Z",115633071,"af4bcf779a03b632e775c7bb99de737a","3fb6496458bbc3fa",1,1682153548969],["2023-04-22T08:52:28Z",115633558,"a89c63ede093668fe429a1b4259678e6","a1b477cab5351b9f",1,1682153548970],["2023-04-22T08:52:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:53:28Z",115651501,"383fea37a13434177c7e1b620570f5c7","21ba17a4811e2ab2",1,1682153608971],["2023-04-22T08:53:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:53:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:53:28Z",115651993,"2fa9126187165a5c7bc49087ae801ba4","7a6bc40c354978dc",1,1682153608974],["2023-04-22T08:53:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:54:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:54:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:54:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:54:28Z",115664362,"9b38375f27cecc654c8049c0f085a92b","c8df4f8d5ae463d7",1,1682153668959],["2023-04-22T08:54:28Z",115663879,"9b38375f27cecc654c8049c0f085a92b","c8df4f8d5ae463d7",1,1682153668962],["2023-04-22T08:55:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:55:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:55:28Z",115677626,"fc984e3758b50bc89cfbb3b558ddc311","75ca3eec04a5d020",1,1682153728969],["2023-04-22T08:55:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:55:28Z",115678110,"f72610be63b673974cfb83eda9e6d9c7","e589bb099c5f7119",1,1682153728969],["2023-04-22T08:56:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:56:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:56:28Z",115688066,"123ca2b3129d38e29c0d5992ecbed2eb","a04fc73588f3ffe1",1,1682153788962],["2023-04-22T08:56:28Z",115688550,"efdbac437ab63d0056db7944ac0c9b65","a9b1181bc0c78598",1,1682153788966],["2023-04-22T08:56:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:57:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:57:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:57:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:57:28Z",115699105,"567dafb493027f8a669adcc010d32110","e3be133197c6aa47",1,1682153848969],["2023-04-22T08:57:28Z",115699588,"567dafb493027f8a669adcc010d32110","e3be133197c6aa47",1,1682153848965],["2023-04-22T08:58:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:58:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:58:28Z",115711291,"21d243655ebc908285e52dba2c3580ff","51a3a5b744505fc1",1,1682153908960],["2023-04-22T08:58:28Z",115710808,"21d243655ebc908285e52dba2c3580ff","51a3a5b744505fc1",1,1682153908963],["2023-04-22T08:58:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:59:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T08:59:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T08:59:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T08:59:28Z",115721920,"fc71c7a93f323ebdff1f049c135664ec","d2167f87865c3aea",1,1682153968957],["2023-04-22T08:59:28Z",115722403,"fc71c7a93f323ebdff1f049c135664ec","d2167f87865c3aea",1,1682153968954],["2023-04-22T09:00:28Z",115733923,"e7f4dbf09d7b4c4f2c7e26f9049184c0","0e8b85f9ffe197a5",1,1682154028963],["2023-04-22T09:00:28Z",115734406,"e7f4dbf09d7b4c4f2c7e26f9049184c0","0e8b85f9ffe197a5",1,1682154028960],["2023-04-22T09:00:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:00:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:00:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:01:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:01:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:01:28Z",115747364,"275548d0061bd450a575613c59196723","950473ea00c51d0f",1,1682154088967],["2023-04-22T09:01:28Z",115747847,"275548d0061bd450a575613c59196723","950473ea00c51d0f",1,1682154088965],["2023-04-22T09:01:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:02:28Z",115761811,"daf31f17f49d490c83f13c353410698b","06200c4175726158",1,1682154148960],["2023-04-22T09:02:28Z",115761328,"daf31f17f49d490c83f13c353410698b","06200c4175726158",1,1682154148963],["2023-04-22T09:02:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:02:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:02:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:03:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:03:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:03:28Z",115772865,"4d398f1781b6849e18b4a49805d6d416","b30781b0e212ab49",1,1682154208965],["2023-04-22T09:03:28Z",115773350,"a529ad6df25d44cbc591c5f1814d0cc1","c6b23fb20021e49c",1,1682154208967],["2023-04-22T09:03:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:04:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:04:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:04:28Z",115788825,"31b151d76e41ca1db6f3269953fda85e","70740b7f8c392c25",1,1682154268967],["2023-04-22T09:04:28Z",115789312,"43cf1c738170373ef48170f359066959","bd97feaff7779eae",1,1682154268968],["2023-04-22T09:04:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:05:28Z",115802781,"8b0441d87cbcc474d664c99535ad669d","2a6c432dd8e4c3fd",1,1682154328967],["2023-04-22T09:05:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:05:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:05:28Z",115802297,"580d9d98d6b76d8d8444463a1a21627b","bbf61af646e16978",1,1682154328956],["2023-04-22T09:05:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:06:28Z",115813616,"4aa471d0f00740011ce8d66acc52281a","216cc953663aac68",1,1682154388948],["2023-04-22T09:06:28Z",115814099,"4aa471d0f00740011ce8d66acc52281a","216cc953663aac68",1,1682154388946],["2023-04-22T09:06:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:06:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:06:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:07:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:07:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:07:28Z",115825085,"efd0e34e9126172d47ded9fdddec5a7b","8ddb9404521f177b",1,1682154448967],["2023-04-22T09:07:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:07:28Z",115824600,"b0705f41034b923baad5b10e376a58de","e1f7bb8fd52d0a7b",1,1682154448962],["2023-04-22T09:08:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:08:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:08:28Z",115835645,"42bb8976800e050326fa14c25b281924","600873b41a6ff1b2",1,1682154508966],["2023-04-22T09:08:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],["2023-04-22T09:08:28Z",115836130,"984e5e6ca13c09397100769e6d336039","db4129d930407fc8",1,1682154508964],["2023-04-22T09:09:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],["2023-04-22T09:09:28Z",5,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900736],["2023-04-22T09:09:28Z",115849354,"ff3d29f3ec4a8b2364d2a5ba7b0751be","66103693e7484b95",1,1682154568967],["2023-04-22T09:09:28Z",115849837,"ff3d29f3ec4a8b2364d2a5ba7b0751be","66103693e7484b95",1,1682154568963],["2023-04-22T09:09:28Z",483,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937]]}]}`, actual)
}

func TestVmQueryParams(t *testing.T) {
	ctx := context.Background()
	mockCurl := mockData(ctx, "handler_test", "handler_test")

	testCases := []struct {
		username string
		spaceUid string
		query    string
		promql   string
		start    string
		end      string
		step     string
		params   string
		error    error
	}{
		{
			username: "vm-query",
			spaceUid: consul.VictoriaMetricsStorageType,
			query:    `{"query_list":[{"field_name":"bk_split_measurement","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["and", "and"]}},{"field_name":"bk_split_measurement","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":false,"api_type":"query_range","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(bk_split_measurement_value{bcs_cluster_id=~\"cls-2\",bcs_cluster_id=~\"cls-2\",bk_biz_id=\"100801\",bk_split_measurement=\"bk_split_measurement\"}[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(bk_split_measurement_value{bk_split_measurement=\"bk_split_measurement\"}[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_group":{"bk_split_measurement_value":["victoria_metrics"]},"metric_filter_condition":null,"metric_alias_mapping":null}`,
		},
		{
			username: "vm-query-or",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"container_cpu_usage_seconds_total","field_list":null,"function":[{"method":"sum","without":false,"dimensions":[],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0,"vargs_list":null},"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"bk_biz_id","value":["7"],"op":"contains"},{"field_name":"ip","value":["127.0.0.1","127.0.0.2"],"op":"contains"},{"field_name":"ip","value":["[a-z]","[A-Z]"],"op":"req"},{"field_name":"api","value":["/metrics"],"op":"ncontains"},{"field_name":"bk_biz_id","value":["7"],"op":"contains"},{"field_name":"api","value":["/metrics"],"op":"contains"}],"condition_list":["and","and","and","or","and"]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1697458200","end_time":"1697461800","step":"60s","down_sample_range":"3s","timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","api_params":{"query":"sum(count_over_time(a[1m] offset -59s999ms))","start":1697458200,"end":1697461800,"step":60},"result_table_group":{"a":["1_prom_computation_result"]},"metric_filter_condition":{"a":"result_table_id=\"1_prom_computation_result\", __name__=\"container_cpu_usage_seconds_total_value\", bcs_cluster_id=\"cls-2\", bk_biz_id=\"7\", ip=~\"198\\\\.0\\\\.0\\\\.1|198\\\\.0\\\\.0\\\\.2\", ip=~\"[a-z]|[A-Z]\", api!=\"/metrics\" or result_table_id=\"1_prom_computation_result\", __name__=\"container_cpu_usage_seconds_total_value\", bcs_cluster_id=\"cls-2\", bk_biz_id=\"7\", api=\"/metrics\""},"metric_alias_mapping":null}`,
		},
		{
			username: "vm-query-or-for-interval",
			spaceUid: "vm-query",
			promql:   `{"promql":"sum by(job, metric_name) (delta(label_replace({__name__=~\"container_cpu_.+_total\", __name__ !~ \".+_size_count\", __name__ !~ \".+_process_time_count\", job=\"metric-social-friends-forever\"}, \"metric_name\", \"$1\", \"__name__\", \"ffs_rest_(.*)_count\")[2m:]))","start":"1698147600","end":"1698151200","step":"60s","bk_biz_ids":null,"timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","api_params":{"query":"sum by (job, metric_name) (label_replace(delta(a[2m:] offset 1ms), \"metric_name\", \"$1\", \"__name__\", \"ffs_rest_(.*)_count\"))","start":1698147600,"end":1698151200,"step":60},"result_table_group":{"a":["1_prom_computation_result"]},"metric_filter_condition":{"a":"result_table_id=\"1_prom_computation_result\", __name__=~\"container_cpu_.+_total_value\", bcs_cluster_id=\"cls-2\", job=\"metric-social-friends-forever\""},"metric_alias_mapping":null}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"container_cpu_usage_seconds_total","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["or", "and"]}},{"field_name":"container_cpu_usage_seconds_total","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":false,"api_type":"query_range","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(container_cpu_usage_seconds_total_value{bcs_cluster_id=\"cls-2\"}[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(container_cpu_usage_seconds_total_value{bcs_cluster_id=\"cls-2\"}[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_group":{"container_cpu_usage_seconds_total_value":["1_prom_computation_result"]},"metric_filter_condition":null,"metric_alias_mapping":null}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["and","and"]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":false,"api_type":"query_range","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(metric_value{bcs_cluster_id=\"cls\",bcs_cluster_id=~\"cls-2\",bcs_cluster_id=~\"cls-2\",bk_biz_id=\"100801\"}[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(metric_value{bcs_cluster_id=\"cls\"}[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_group":{"metric_value":["table_id"]},"metric_filter_condition":null,"metric_alias_mapping":null}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"namespace","value":["ns"],"op":"contains"}],"condition_list":[]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":false,"api_type":"query_range","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(metric_value{bcs_cluster_id=\"cls\",namespace=\"ns\"}[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(metric_value{bcs_cluster_id=\"cls\"}[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_group":{"metric_value":["table_id"]},"metric_filter_condition":null,"metric_alias_mapping":null}`,
		},
		{
			username: "vm-query-fuzzy-name",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"me.*","is_regexp":true,"function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"namespace","value":["ns"],"op":"contains"}],"condition_list":[]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":false,"api_type":"query_range","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(me.*_value{bcs_cluster_id=\"cls\",namespace=\"ns\"}[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(metric_value{bcs_cluster_id=\"cls\"}[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_group":{"me.*_value":["table_id"],"metric_value":["table_id"]},"metric_filter_condition":null,"metric_alias_mapping":null}`,
		},
	}

	for i, c := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			var (
				query *structured.QueryTs
				err   error
			)
			ctx, _ = context.WithCancel(ctx)
			metadata.SetUser(ctx, fmt.Sprintf("username:%s", c.username), c.spaceUid)
			if c.promql != "" {
				var queryPromQL *structured.QueryPromQL
				err = json.Unmarshal([]byte(c.promql), &queryPromQL)
				assert.Nil(t, err)
				if err == nil {
					query, err = promQLToStruct(ctx, queryPromQL)
				}
			} else {
				err = json.Unmarshal([]byte(c.query), &query)
			}

			query.SpaceUid = c.spaceUid
			assert.Nil(t, err)
			if err == nil {
				_, err = queryTs(ctx, query)
				if c.error != nil {
					assert.Contains(t, err.Error(), c.error.Error())
				} else {
					if len(mockCurl.Params) == 0 {
						assert.Nil(t, err)
					}
					var vmParams *victoriaMetrics.Params
					if mockCurl.Params != nil {
						err = json.Unmarshal(mockCurl.Params, &vmParams)
						assert.Nil(t, err)
					}
					if vmParams != nil {
						assert.Equal(t, c.params, vmParams.SQL)
					}
				}
			}
		})
	}
}

func TestStructAndPromQLConvert(t *testing.T) {
	ctx := context.Background()
	mock.SetRedisClient(ctx, "test-struct-promql")

	testCase := map[string]struct {
		queryStruct bool
		query       *structured.QueryTs
		promql      *structured.QueryPromQL
		err         error
	}{
		"query struct with or": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "custom",
						TableID:    "dataLabel",
						FieldName:  "metric",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function: "sum_over_time",
							Window:   "1m0s",
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
							},
							ConditionList: []string{
								"or",
							},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
				Start:       "1691132705",
				End:         "1691136305",
				Step:        "1m",
			},
			err: fmt.Errorf("or 过滤条件无法直接转换为 promql 语句，请使用结构化查询"),
		},
		"query struct with and": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "custom",
						TableID:    "dataLabel",
						FieldName:  "metric",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time(custom:dataLabel:metric{bcs_cluster_id=~"cls-2",bcs_cluster_id=~"cls-2"}[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"promql struct with and": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "custom",
						TableID:    "dataLabel",
						FieldName:  "metric",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time(custom:dataLabel:metric{bcs_cluster_id=~"cls-2",bcs_cluster_id=~"cls-2"}[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"promql struct 1": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkMonitor,
						FieldName:  "container_cpu_usage_seconds_total",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2|cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bk_biz_id",
									Value: []string{
										"2",
									},
									Operator: "eq",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
					{
						DataSource: structured.BkMonitor,
						FieldName:  "container_cpu_usage_seconds_total",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "count_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: "a / on (bcs_cluster_id) group_left () b",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time(bkmonitor:container_cpu_usage_seconds_total{bcs_cluster_id=~"cls-2|cls-2",bk_biz_id="2"}[1m])) / on (bcs_cluster_id) group_left () sum by (bcs_cluster_id, result_table_id) (count_over_time(bkmonitor:container_cpu_usage_seconds_total[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"query struct 1": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkMonitor,
						FieldName:  "container_cpu_usage_seconds_total",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2|cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bk_biz_id",
									Value: []string{
										"2",
									},
									Operator: "eq",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
					{
						DataSource: structured.BkMonitor,
						FieldName:  "container_cpu_usage_seconds_total",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "count_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: "a / on (bcs_cluster_id) group_left () b",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time(bkmonitor:container_cpu_usage_seconds_total{bcs_cluster_id=~"cls-2|cls-2",bk_biz_id="2"}[1m])) / on (bcs_cluster_id) group_left () sum by (bcs_cluster_id, result_table_id) (count_over_time(bkmonitor:container_cpu_usage_seconds_total[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"query struct with __name__ ": {
			queryStruct: false,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkMonitor,
						TableID:    "table_id",
						FieldName:  ".*",
						IsRegexp:   true,
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						ReferenceName: "a",
						Dimensions:    nil,
						Limit:         0,
						Timestamp:     nil,
						StartOrEnd:    0,
						VectorOffset:  0,
						Offset:        "",
						OffsetForward: false,
						Slimit:        0,
						Soffset:       0,
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2|cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bk_biz_id",
									Value: []string{
										"2",
									},
									Operator: "eq",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						KeepColumns:         nil,
						AlignInfluxdbResult: false,
						Start:               "",
						End:                 "",
						Step:                "",
						Timezone:            "",
					},
					{
						DataSource: structured.BkMonitor,
						TableID:    "table_id",
						FieldName:  ".*",
						IsRegexp:   true,
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "count_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: "a / on (bcs_cluster_id) group_left () b",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time({__name__=~"bkmonitor:table_id:.*",bcs_cluster_id=~"cls-2|cls-2",bk_biz_id="2"}[1m])) / on (bcs_cluster_id) group_left () sum by (bcs_cluster_id, result_table_id) (count_over_time({__name__=~"bkmonitor:table_id:.*"}[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"promql struct with __name__ ": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkMonitor,
						TableID:    "table_id",
						FieldName:  ".*",
						IsRegexp:   true,
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						ReferenceName: "a",
						Dimensions:    nil,
						Limit:         0,
						Timestamp:     nil,
						StartOrEnd:    0,
						VectorOffset:  0,
						Offset:        "",
						OffsetForward: false,
						Slimit:        0,
						Soffset:       0,
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2|cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bk_biz_id",
									Value: []string{
										"2",
									},
									Operator: "eq",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						KeepColumns:         nil,
						AlignInfluxdbResult: false,
						Start:               "",
						End:                 "",
						Step:                "",
						Timezone:            "",
					},
					{
						DataSource: structured.BkMonitor,
						TableID:    "table_id",
						FieldName:  ".*",
						IsRegexp:   true,
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function: "count_over_time",
							Window:   "1m0s",
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: "a / on (bcs_cluster_id) group_left () b",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time({__name__=~"bkmonitor:table_id:.*",bcs_cluster_id=~"cls-2|cls-2",bk_biz_id="2"}[1m])) / on (bcs_cluster_id) group_left () sum by (bcs_cluster_id, result_table_id) (count_over_time({__name__=~"bkmonitor:table_id:.*"}[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"promql to struct with 1m:2m": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `count_over_time(bkmonitor:metric[1m:2m])`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `30s`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: `bkmonitor`,
						FieldName:  `metric`,
						TimeAggregation: structured.TimeAggregation{
							Function:   "count_over_time",
							Window:     "1m0s",
							IsSubQuery: true,
							Step:       "2m0s",
							NodeIndex:  2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: `a`,
						Offset:        "0s",
					},
				},
				MetricMerge: "a",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `30s`,
			},
		},
		"promql to struct with delta label_replace 1m:2m": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `sum by (job, metric_name) (delta(label_replace({__name__=~"bkmonitor:container_cpu_.+_total",job="metric-social-friends-forever"}, "metric_name", "$1", "__name__", "ffs_rest_(.*)_count")[2m:]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `30s`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: `bkmonitor`,
						FieldName:  `container_cpu_.+_total`,
						IsRegexp:   true,
						TimeAggregation: structured.TimeAggregation{
							Function:   "delta",
							Window:     "2m0s",
							NodeIndex:  3,
							IsSubQuery: true,
							Step:       "0s",
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "job",
									Operator:      "eq",
									Value: []string{
										"metric-social-friends-forever",
									},
								},
								//{
								//	DimensionName: "__name__",
								//	Operator:      "nreq",
								//	Value: []string{
								//		".+_size_count",
								//	},
								//},
								//{
								//	DimensionName: "__name__",
								//	Operator:      "nreq",
								//	Value: []string{
								//		".+_process_time_count",
								//	},
								//},
							},
							ConditionList: []string{},
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "label_replace",
								VArgsList: []interface{}{
									"metric_name",
									"$1",
									"__name__",
									"ffs_rest_(.*)_count",
								},
							},
							{
								Method: "sum",
								Dimensions: []string{
									"job",
									"metric_name",
								},
							},
						},
						ReferenceName: `a`,
						Offset:        "0s",
					},
				},
				MetricMerge: "a",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `30s`,
			},
		},
		"promq to struct with topk": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `topk($1, bkmonitor:metric)`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "topk",
								VArgsList: []interface{}{
									1,
								},
							},
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
		},
		"promq to struct with delta(metric[1m])`": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `delta(bkmonitor:metric[1m])`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						TimeAggregation: structured.TimeAggregation{
							Function:  "delta",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
		},
		"promq to struct with metric @end()`": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `bkmonitor:metric @ end()`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						StartOrEnd: parser.END,
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
		},
		"promq to struct with condition contains`": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `bkmonitor:metric{dim-contains=~"^(val-1|val-2|val-3)$",dim-req=~"val-1|val-2|val-3"} @ end()`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						StartOrEnd: parser.END,
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "dim-contains",
									Value: []string{
										"val-1",
										"val-2",
										"val-3",
									},
									Operator: "contains",
								},
								{
									DimensionName: "dim-req",
									Value: []string{
										"val-1",
										"val-2",
										"val-3",
									},
									Operator: "req",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
		},
	}

	for n, c := range testCase {
		t.Run(n, func(t *testing.T) {
			ctx, _ = context.WithCancel(ctx)
			if c.queryStruct {
				promql, err := structToPromQL(ctx, c.query)
				if c.err != nil {
					assert.Equal(t, c.err, err)
				} else {
					assert.Nil(t, err)
					if err == nil {
						equalWithJson(t, c.promql, promql)
					}
				}
			} else {
				query, err := promQLToStruct(ctx, c.promql)
				if c.err != nil {
					assert.Equal(t, c.err, err)
				} else {
					assert.Nil(t, err)
					if err == nil {
						equalWithJson(t, c.query, query)
					}
				}
			}
		})
	}
}

func equalWithJson(t *testing.T, a, b interface{}) {
	a1, a1Err := json.Marshal(a)
	assert.Nil(t, a1Err)

	b1, b1Err := json.Marshal(b)
	assert.Nil(t, b1Err)
	if a1Err == nil && b1Err == nil {
		assert.Equal(t, string(a1), string(b1))
	}
}
