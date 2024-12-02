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
	BkAppCode           = "default_app_code"
	SpaceUid            = "space_default"
	ResultTableVM       = "result_table.vm"
	ResultTableInfluxDB = "result_table.influxdb"
	ResultTableEs       = "result_table.es"
	ResultTableBkBaseEs = "result_table.bk_base_es"
	ResultTableBkSQL    = "result_table.bk_sql"
)

var (
	mockRedisOnce       sync.Once
	mockSpaceRouterOnce sync.Once
)

func MockSpaceRouter(ctx context.Context) {
	mockSpaceRouterOnce.Do(func() {
		_ = featureFlag.MockFeatureFlag(ctx, `{
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
		}
		influxdbFields := []string{
			"kube_pod_info",
			"kube_node_info",
			"kube_node_status_condition",
		}

		tsdb.SetStorage(
			consul.VictoriaMetricsStorageType,
			&tsdb.Storage{Type: consul.VictoriaMetricsStorageType},
		)
		tsdb.SetStorage("2", &tsdb.Storage{Type: consul.InfluxDBStorageType})
		tsdb.SetStorage("3", &tsdb.Storage{Type: consul.ElasticsearchStorageType, Address: mock.EsUrl})
		tsdb.SetStorage("4", &tsdb.Storage{Type: consul.BkSqlStorageType})

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
					ResultTableVM: &ir.SpaceResultTable{
						TableId: ResultTableVM,
					},
					ResultTableInfluxDB: &ir.SpaceResultTable{
						TableId: ResultTableInfluxDB,
					},
					ResultTableEs: &ir.SpaceResultTable{
						TableId: ResultTableEs,
					},
					ResultTableBkSQL: &ir.SpaceResultTable{
						TableId: ResultTableBkSQL,
					},
					ResultTableBkBaseEs: &ir.SpaceResultTable{
						TableId: ResultTableBkBaseEs,
					},
				},
			},
			ir.ResultTableDetailInfo{
				"system.cpu_summary": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "system.cpu_summary",
					DB:              "system",
					Measurement:     "cpu_summary",
					ClusterName:     "default",
					VmRt:            "",
					Fields:          []string{"usage", "free"},
					MeasurementType: redis.BKTraditionalMeasurement,
				},
				"system.cpu_detail": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "system.cpu_detail",
					VmRt:            "100147_ieod_system_cpu_detail_raw",
					Fields:          []string{"usage", "free"},
					MeasurementType: redis.BKTraditionalMeasurement,
				},
				"system.disk": &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         "system.disk",
					VmRt:            "100147_ieod_system_disk_raw",
					Fields:          []string{"usage", "free"},
					MeasurementType: redis.BKTraditionalMeasurement,
				},
				ResultTableVM: &ir.ResultTableDetail{
					StorageId:       2,
					TableId:         ResultTableVM,
					VmRt:            "2_bcs_prom_computation_result_table",
					Fields:          vmFiedls,
					BcsClusterID:    "BCS-K8S-00000",
					MeasurementType: redis.BkSplitMeasurement,
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
				},
				ResultTableEs: &ir.ResultTableDetail{
					StorageId: 3,
					TableId:   ResultTableEs,
				},
				ResultTableBkSQL: &ir.ResultTableDetail{
					StorageId: 4,
					TableId:   ResultTableBkSQL,
				},
				ResultTableBkBaseEs: &ir.ResultTableDetail{
					SourceType: "bkdata",
					DB:         "es_index",
				},
			}, nil,
			ir.DataLabelToResultTable{
				"influxdb": ir.ResultTableList{
					"result_table.influxdb",
					"result_table.vm",
				},
			},
		)
	})
}

func setSpaceTsDbMockData(ctx context.Context, bkAppSpace ir.BkAppSpace, spaceInfo ir.SpaceInfo, rtInfo ir.ResultTableDetailInfo, fieldInfo ir.FieldToResultTable, dataLabelInfo ir.DataLabelToResultTable) {
	mockRedisOnce.Do(func() {
		setRedisClient(ctx)
	})

	sr, err := SetSpaceTsDbRouter(ctx, "mock", "mock", "", 100)
	if err != nil {
		panic(err)
	}
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
	for field, rts := range fieldInfo {
		err = sr.Add(ctx, ir.FieldToResultTableKey, field, &rts)
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
