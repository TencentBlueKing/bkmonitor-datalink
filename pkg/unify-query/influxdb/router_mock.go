// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"fmt"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

const (
	BkAppCode                  = "default_app_code"
	SpaceUid                   = "bkcc__2"
	ResultTableVM              = "result_table.vm"
	ResultTableInfluxDB        = "result_table.influxdb"
	ResultTableEs              = "result_table.es"
	ResultTableEsWithTimeFiled = "result_table.es_with_time_filed"
	ResultTableBkBaseEs        = "result_table.bk_base_es"
	ResultTableBkSQL           = "result_table.bk_sql"
	ResultTableDoris           = "result_table.doris"
)

var (
	mockRedisOnce       sync.Once
	mockSpaceRouterOnce sync.Once
)

func MockSpaceRouter(ctx context.Context) {
	mockSpaceRouterOnce.Do(func() {
		_ = featureFlag.MockFeatureFlag(ctx, `{
		"bk-data-table-id-auth": {
	  		"variations": {
	  			"true": true,
	  			"false": false
	  		},
	  		"targeting": [
				{
					"query": "spaceUID in [\"bkdata\"]",
					"percentage": {
					   "false": 100
                	}
            	}
			],
			"defaultRule": {
	  			"variation": "true"
	  		}
		},
		"jwt-auth": {
	  		"variations": {
	  			"true": true,
	  			"false": false
	  		},
	  		"targeting": [
			],
			"defaultRule": {
	  			"variation": "true"
	  		}
		},
	  	"must-vm-query": {
	  		"variations": {
	  			"true": true,
	  			"false": false
	  		},
	  		"targeting": [
                {
	  			    "query": "tableID in [\"result_table.vm\", \"result_table.k8s\"]",
	  			    "percentage": {
	  			    	"true": 100,
	  				    "false":0 
	  			    }
	  		    },
               {
	  			    "query": "tableID in [\"system.cpu_detail\",\"system.disk\"]",
	  			    "percentage": {
	  			    	"true": 100,
	  				    "false":0 
	  			    }
	  		   }
            ],
	  		"defaultRule": {
	  			"variation": "false"
	  		}
	  	}
	  }`)

		vmFiedls := []string{
			"container_cpu_usage_seconds_total",
			"kube_pod_info",
			"node_with_pod_relation",
			"node_with_system_relation",
			"deployment_with_replicaset_relation",
			"pod_with_replicaset_relation",
			"apm_service_instance_with_pod_relation",
			"apm_service_instance_with_system_relation",
			"container_info_relation",
			"host_info_relation",
			"kubelet_info",
		}
		influxdbFields := []string{
			"kube_pod_info",
			"kube_node_info",
			"kube_node_status_condition",
			"kubelet_cluster_request_total",
			"merltrics_rest_request_status_200_count",
			"merltrics_rest_request_status_500_count",
		}

		tsdb.SetStorage(
			consul.VictoriaMetricsStorageType,
			&tsdb.Storage{Type: consul.VictoriaMetricsStorageType},
		)
		tsdb.SetStorage("2", &tsdb.Storage{Type: consul.InfluxDBStorageType})
		tsdb.SetStorage("3", &tsdb.Storage{Type: consul.ElasticsearchStorageType, Address: mock.EsUrl})
		tsdb.SetStorage("4", &tsdb.Storage{Type: consul.BkSqlStorageType, Address: mock.BkBaseUrl})

		r := GetInfluxDBRouter()
		r.clusterInfo = ir.ClusterInfo{
			"default": &ir.Cluster{
				HostList: []string{"default"},
			},
		}
		r.hostInfo = ir.HostInfo{
			"default": &ir.Host{
				DomainName: "127.0.0.1",
				Port:       12302,
				Protocol:   "http",
			},
		}

		setSpaceTsDbMockData(ctx,
			ir.BkAppSpace{
				BkAppCode: {
					"*",
				},
				"my_code": {
					"my_space_uid",
				},
			},
			ir.SpaceInfo{
				SpaceUid: ir.Space{
					"system.disk": &ir.SpaceResultTable{
						TableId: "system.disk",
						Filters: []map[string]string{
							{"bk_biz_id": "2"},
						},
					},
					"system.cpu_detail": &ir.SpaceResultTable{
						TableId: "system.cpu_detail",
						Filters: []map[string]string{
							{"bk_biz_id": "2"},
						},
					},
					"system.cpu_summary": &ir.SpaceResultTable{
						TableId: "system.cpu_summary",
						Filters: []map[string]string{
							{"bk_biz_id": "2"},
						},
					},
					"bk.exporter": &ir.SpaceResultTable{
						TableId: "bk.exporter",
					},
					"bk.standard_v2_time_series": &ir.SpaceResultTable{
						TableId: "bk.standard_v2_time_series",
					},
					ResultTableVM: &ir.SpaceResultTable{
						TableId: ResultTableVM,
					},
					ResultTableInfluxDB: &ir.SpaceResultTable{
						TableId: ResultTableInfluxDB,
					},
					"result_table.unify_query": &ir.SpaceResultTable{TableId: "result_table.unify_query"},
					ResultTableEs: &ir.SpaceResultTable{
						TableId: ResultTableEs,
					},
					"alias_es_1": &ir.SpaceResultTable{
						TableId: "alias_es_1",
					},
					ResultTableEsWithTimeFiled: &ir.SpaceResultTable{
						TableId: ResultTableEsWithTimeFiled,
					},
					ResultTableBkSQL: &ir.SpaceResultTable{
						TableId: ResultTableBkSQL,
					},
					ResultTableBkBaseEs: &ir.SpaceResultTable{
						TableId: ResultTableBkBaseEs,
					},
					ResultTableDoris: &ir.SpaceResultTable{
						TableId: ResultTableDoris,
					},
				},
			},
			ir.ResultTableDetailInfo{
				"result_table.kubelet_info": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "result_table.kubelet_info",
					VmRt:            "2_bcs_prom_computation_result_table",
					Fields:          vmFiedls,
					DB:              "other",
					Measurement:     "kubelet_info",
					BcsClusterID:    "BCS-K8S-00000",
					MeasurementType: redis.BkSplitMeasurement,
					StorageType:     consul.VictoriaMetricsStorageType,
					DataLabel:       "kubelet_info",
				},
				"bk.exporter": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "bk.exporter",
					DB:              "bk",
					Measurement:     "exporter",
					ClusterName:     "default",
					Fields:          []string{"usage", "free"},
					MeasurementType: redis.BkExporter,
					StorageType:     consul.InfluxDBStorageType,
				},
				"bk.standard_v2_time_series": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "bk.standard_v2_time_series",
					DB:              "bk",
					Measurement:     "standard_v2_time_series",
					ClusterName:     "default",
					Fields:          []string{"usage", "free"},
					MeasurementType: redis.BkStandardV2TimeSeries,
					StorageType:     consul.InfluxDBStorageType,
				},
				"system.cpu_summary": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "system.cpu_summary",
					DB:              "system",
					Measurement:     "cpu_summary",
					ClusterName:     "default",
					VmRt:            "",
					Fields:          []string{"usage", "free"},
					MeasurementType: redis.BKTraditionalMeasurement,
					StorageType:     consul.InfluxDBStorageType,
					DataLabel:       "cpu_summary",
				},
				"system.cpu_detail": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "system.cpu_detail",
					VmRt:            "100147_ieod_system_cpu_detail_raw",
					Fields:          []string{"usage", "free"},
					MeasurementType: redis.BKTraditionalMeasurement,
					StorageType:     consul.InfluxDBStorageType,
					DataLabel:       "cpu_detail",
				},
				"system.disk": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "system.disk",
					VmRt:            "100147_ieod_system_disk_raw",
					CmdbLevelVmRt:   "rt_by_cmdb_level",
					Fields:          []string{"usage", "free"},
					MeasurementType: redis.BKTraditionalMeasurement,
					StorageType:     consul.InfluxDBStorageType,
					DataLabel:       "disk",
				},
				ResultTableVM: &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         ResultTableVM,
					VmRt:            "2_bcs_prom_computation_result_table",
					Fields:          vmFiedls,
					BcsClusterID:    "BCS-K8S-00000",
					MeasurementType: redis.BkSplitMeasurement,
					StorageType:     consul.VictoriaMetricsStorageType,
					DataLabel:       "vm",
				},
				ResultTableInfluxDB: &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         ResultTableInfluxDB,
					Fields:          influxdbFields,
					BcsClusterID:    "BCS-K8S-00000",
					DB:              "result_table",
					Measurement:     "influxdb",
					MeasurementType: redis.BkSplitMeasurement,
					ClusterName:     "default",
					DataLabel:       "influxdb",
					StorageType:     consul.InfluxDBStorageType,
				},
				"result_table.unify_query": &ir.ResultTableDetail{
					StorageId:             3,
					TableId:               "result_table.unify_query",
					DB:                    "unify_query",
					SourceType:            "",
					StorageType:           consul.ElasticsearchStorageType,
					StorageClusterRecords: []ir.Record{},
					DataLabel:             "es",
					FieldAlias: map[string]string{
						"alias_ns": "__ext.host.bk_set_name",
					},
				},
				ResultTableEs: &ir.ResultTableDetail{
					StorageId:   3,
					TableId:     ResultTableEs,
					DB:          "es_index",
					SourceType:  "",
					StorageType: consul.ElasticsearchStorageType,
					StorageClusterRecords: []ir.Record{
						{
							StorageID: 3,
							// 2019-12-02 08:00:00
							EnableTime: 1575244800,
						},
						{
							StorageID: 4,
							// 2019-11-02 08:00:00
							EnableTime: 1572652800,
						},
					},
					DataLabel: "es",
					FieldAlias: map[string]string{
						"alias_ns": "__ext.host.bk_set_name",
					},
				},
				"alias_es_1": &ir.ResultTableDetail{
					StorageId:   3,
					TableId:     ResultTableEs,
					DB:          "es_index",
					SourceType:  "",
					StorageType: consul.ElasticsearchStorageType,
					StorageClusterRecords: []ir.Record{
						{
							StorageID: 3,
							// 2019-12-02 08:00:00
							EnableTime: 1575244800,
						},
						{
							StorageID: 4,
							// 2019-11-02 08:00:00
							EnableTime: 1572652800,
						},
					},
					DataLabel: "es",
					FieldAlias: map[string]string{
						"alias_ns": "__ext.namespace",
					},
				},
				ResultTableEsWithTimeFiled: &ir.ResultTableDetail{
					StorageId:   3,
					TableId:     ResultTableEsWithTimeFiled,
					DB:          "es_index",
					SourceType:  "",
					StorageType: consul.ElasticsearchStorageType,
					StorageClusterRecords: []ir.Record{
						{
							StorageID: 3,
							// 2019-12-02 08:00:00
							EnableTime: 1575244800,
						},
						{
							StorageID: 4,
							// 2019-11-02 08:00:00
							EnableTime: 1572652800,
						},
					},
					DataLabel: "es",
					Options: struct {
						TimeField   ir.TimeField `json:"time_field"`
						NeedAddTime bool         `json:"need_add_time"`
					}{
						TimeField: ir.TimeField{
							Name: "end_time",
							Type: "long",
							Unit: "microsecond",
						}, NeedAddTime: false,
					},
				},
				ResultTableBkSQL: &ir.ResultTableDetail{
					StorageId:   4,
					TableId:     ResultTableBkSQL,
					DataLabel:   "bksql",
					DB:          "2_bklog_bkunify_query_doris",
					StorageType: consul.BkSqlStorageType,
				},
				ResultTableDoris: &ir.ResultTableDetail{
					StorageId:   4,
					TableId:     ResultTableDoris,
					DB:          "2_bklog_bkunify_query_doris",
					Measurement: "doris",
					DataLabel:   "bksql",
					StorageType: consul.BkSqlStorageType,
				},
				ResultTableBkBaseEs: &ir.ResultTableDetail{
					SourceType:  "bkdata",
					DB:          "es_index",
					DataLabel:   "bkbase_es",
					StorageType: consul.ElasticsearchStorageType,
				},
			}, nil,
			ir.DataLabelToResultTable{
				"alias_es": ir.ResultTableList{
					ResultTableEs,
					"alias_es_1",
				},
				"influxdb": ir.ResultTableList{
					"result_table.influxdb",
					"result_table.vm",
				},
				"multi_es": ir.ResultTableList{
					ResultTableEs,
					ResultTableEsWithTimeFiled,
				},
				"es_and_doris": ir.ResultTableList{
					ResultTableEs,
					ResultTableDoris,
				},
			},
		)
	})
}

func setSpaceTsDbMockData(ctx context.Context, bkAppSpace ir.BkAppSpace, spaceInfo ir.SpaceInfo, rtInfo ir.ResultTableDetailInfo, fieldInfo ir.FieldToResultTable, dataLabelInfo ir.DataLabelToResultTable) {
	mockRedisOnce.Do(func() {
		setRedisClient(ctx)
	})

	mockPath := "mock" + time.Now().String()
	sr, err := SetSpaceTsDbRouter(ctx, mockPath, mockPath, "", 5, false)
	if err != nil {
		panic(err)
	}
	sr.cache.Clear()

	for bkApp, spaceUidList := range bkAppSpace {
		err = sr.Add(ctx, ir.BkAppToSpaceKey, bkApp, spaceUidList)
		if err != nil {
			panic(err)
		}
	}
	for sid, space := range spaceInfo {
		err = sr.Add(ctx, ir.SpaceToResultTableKey, sid, &space)
		if err != nil {
			panic(err)
		}
	}
	for rid, rt := range rtInfo {
		err = sr.Add(ctx, ir.ResultTableDetailKey, rid, rt)
		if err != nil {
			panic(err)
		}
	}

	for dataLabel, rts := range dataLabelInfo {
		err = sr.Add(ctx, ir.DataLabelToResultTableKey, dataLabel, &rts)
		if err != nil {
			panic(err)
		}
	}
}

func setRedisClient(ctx context.Context) {
	host := viper.GetString("redis.host")
	port := viper.GetInt("redis.port")
	pwd := viper.GetString("redis.password")
	options := &goRedis.UniversalOptions{
		DB:       0,
		Addrs:    []string{fmt.Sprintf("%s:%d", host, port)},
		Password: pwd,
	}
	redis.SetInstance(ctx, "mock", options)
}

func MockRouterWithHostInfo(hostInfo ir.HostInfo) *Router {
	i := GetInfluxDBRouter()
	i.hostInfo = hostInfo
	i.hostStatusInfo = make(ir.HostStatusInfo, len(hostInfo))
	// 将hostInfo 里面的信息初始化到 hostStatusInfo 并且初始化 Read 状态为 true
	for _, v := range hostInfo {
		i.hostStatusInfo[v.DomainName] = &ir.HostStatus{Read: true}
	}
	return i
}
