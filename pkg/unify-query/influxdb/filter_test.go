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
	"testing"

	"github.com/hashicorp/consul/api"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestFilter
func TestFilter(t *testing.T) {
	log.InitTestLogger()
	_ = consul.SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", nil, "",
	)
	consul.MetadataPath = "test/metadata/v1/default/data_id"
	consul.BCSInfoPath = "test/metadata/v1/default/project_id"
	consul.MetricRouterPath = "test/metadata/influxdb_metrics"

	data := map[string]api.KVPairs{
		consul.MetadataPath: {
			{
				Key:   consul.MetadataPath + "/1500009",
				Value: []byte(`{"bk_data_id":1500009,"data_id":1500009,"mq_config":{"storage_config":{"topic":"0bkmonitor_15000090","partition":1},"cluster_config":{"domain_name":"kafka.service.consul","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","result_table_list":[{"bk_biz_id":2,"result_table":"process.port","shipper_list":[{"storage_config":{"real_table_name":"port","database":"process","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[{"field_name":"alive","type":"float","tag":"metric","default_value":"0","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_biz_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_collect_config_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_cloud_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_ip","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_service_category_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_topo_id","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"bk_target_topo_level","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"listen_address","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"listen_port","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"pid","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"process_name","type":"string","tag":"dimension","default_value":"","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"target","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"time","type":"timestamp","tag":"timestamp","default_value":"","is_config_by_user":true,"description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","unit":"","alias_name":"","option":{}}],"schema_type":"free","option":{}}],"option":{"inject_local_time":true,"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bk_bkmonitorv3_enterprise_production/metadata/influxdb_metrics/1500009/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"custom","token":"4774c8313d74430ca68c204aa6491eee","transfer_cluster_id":"default"}`),
			},
			{
				Key:   consul.MetadataPath + "/1500015",
				Value: []byte(`{"bk_data_id":1500015,"data_id":1500015,"mq_config":{"storage_config":{"topic":"0bkmonitor_15000150","partition":1},"cluster_config":{"domain_name":"kafka.service.consul","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_event","result_table_list":[{"bk_biz_id":2,"result_table":"2_bkmonitor_event_public_1500015","shipper_list":[{"storage_config":{"index_datetime_format":"write_20060102","index_datetime_timezone":0,"date_format":"%Y%m%d","slice_size":500,"slice_gap":1440,"retention":30,"warm_phase_days":0,"warm_phase_settings":{},"base_index":"2_bkmonitor_event_public_1500015","index_settings":{"number_of_shards":4,"number_of_replicas":1},"mapping_settings":{"dynamic_templates":[{"discover_dimension":{"path_match":"dimensions.*","mapping":{"type":"keyword"}}}]}},"cluster_config":{"domain_name":"es7.service.consul","port":9200,"schema":null,"is_ssl_verify":false,"cluster_id":3,"cluster_name":"es7_cluster","version":"7.2","custom_option":"","registered_system":"_default","creator":"system","create_time":1624001652,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"elasticsearch","auth_info":{"password":"5gYTZqvd7Z7s","username":"elastic"}}],"field_list":[{"field_name":"dimensions","type":"object","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"object","es_dynamic":true}},{"field_name":"event","type":"object","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"object","es_properties":{"content":{"type":"text"},"count":{"type":"integer"}}}},{"field_name":"event_name","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword"}},{"field_name":"target","type":"string","tag":"dimension","default_value":null,"is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{"es_type":"keyword"}},{"field_name":"time","type":"timestamp","tag":"timestamp","default_value":"","is_config_by_user":true,"description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","unit":"","alias_name":"","option":{"es_type":"date_nanos","es_format":"epoch_millis"}}],"schema_type":"free","option":{"es_unique_field_list":["event","target","dimensions","event_name","time"]}}],"option":{"inject_local_time":true,"timestamp_precision":"ms","flat_batch_key":"data"},"type_label":"log","source_label":"bk_monitor","token":"d6dc05057e384f6db70e3542e3f8a2ce","transfer_cluster_id":"default"}`),
			},
			{
				Key:   "test/metadata/v1/bkte-k8s-gz-1/data_id/535007",
				Value: []byte(`{"bk_data_id":535007,"data_id":535007,"mq_config":{"storage_config":{"topic":"0bkmonitor_5350070","partition":1},"cluster_config":{"domain_name":"bk-kafka-monitor-01","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","option":{"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bkmonitorv3_ieod_production/metadata/influxdb_metrics/535007/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"bk_monitor","token":"e200cafcef9b44228b77ae005eb3ddc7","transfer_cluster_id":"bkte-k8s-gz-1","data_name":"bcs_BCS-K8S-25595_k8s_metric","result_table_list":[{"bk_biz_id":100867,"result_table":"100867_bkmonitor_time_series_535007.__default__","shipper_list":[{"storage_config":{"real_table_name":"__default__","database":"100867_bkmonitor_time_series_535007","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[],"schema_type":"free","option":{"is_split_measurement":true}}]}`),
			},
			{
				Key:   "test/metadata/v1/bkte-k8s-gz-1/data_id/535008",
				Value: []byte(`{"bk_data_id":535008,"data_id":535008,"mq_config":{"storage_config":{"topic":"0bkmonitor_5350080","partition":1},"cluster_config":{"domain_name":"bk-kafka-monitor-01","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","option":{"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bkmonitorv3_ieod_production/metadata/influxdb_metrics/535008/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"bk_monitor","token":"b1cd63c950dd481eb493e7c0384185b1","transfer_cluster_id":"bkte-k8s-gz-1","data_name":"bcs_BCS-K8S-25595_custom_metric","result_table_list":[{"bk_biz_id":100867,"result_table":"100867_bkmonitor_time_series_535008.__default__","shipper_list":[{"storage_config":{"real_table_name":"__default__","database":"100867_bkmonitor_time_series_535008","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[],"schema_type":"free","option":{"is_split_measurement":true}}]}`),
			},
		},
		consul.BCSInfoPath: {
			{
				Key:   consul.BCSInfoPath + "/488d11df92d64673b763505ec9c8d3bf/cluster_id/BCS-K8S-25595",
				Value: []byte(`[535008,535007]`),
			},
			{
				Key:   consul.BCSInfoPath + "/aaaaa/cluster_id/bcs-k8s",
				Value: []byte(`[1500009, 1500011]`),
			},
			{
				Key:   consul.BCSInfoPath + "/aaaaa/cluster_id/bcs-k9s",
				Value: []byte(`[1500013]`),
			},
			{
				Key:   consul.BCSInfoPath + "/bbbbb/cluster_id/bcs-k10s",
				Value: []byte(`[1500033]`),
			},
		},
		// metric info
		consul.MetricRouterPath: {
			{
				Key:   consul.MetricRouterPath + `/1573195/time_series_metric/tunnel_respond_cost_milliseconds`,
				Value: []byte(`["module","bk_monitor_type","bk_container","bk_biz_id","bk_instance","bk_service","target","bk_pod","transmit","node","bcs_cluster_id","bk_endpoint","bk_job","bk_monitor_name","bk_namespace"]`),
			},
			{
				Key:   consul.MetricRouterPath + `/1573195/time_series_metric/tunnel_respond_fail_num`,
				Value: []byte(`["target","module","bk_instance","bk_monitor_type","bk_job","bk_pod","bk_monitor_name","node","bk_biz_id","bk_endpoint","transmit","bk_namespace","bcs_cluster_id","bk_container","bk_service"]`),
			},
			{
				Key:   consul.MetricRouterPath + `/1573195/time_series_metric/tunnel_respond_total`,
				Value: []byte(`["bk_container","bk_namespace","transmit","bk_biz_id","module","bk_endpoint","bcs_cluster_id","bk_monitor_type","bk_instance","bk_pod","node","bk_job","target","bk_service","bk_monitor_name"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/1500009/time_series_metric",
				Value: []byte(`["alive"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/535007/time_series_metric",
				Value: []byte(`["kube_daemonset_status_desired_number_scheduled"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/535008/time_series_metric",
				Value: []byte(`["kube_daemonset_status_desired_number_scheduled"]`),
			},
		},
	}

	stubs := gostub.Stub(&consul.GetDataWithPrefix, func(prefix string) (api.KVPairs, error) {
		return data[prefix], nil
	})
	stubs = gostub.Stub(&consul.GetPathDataIDPath, func(metadataPath, version string) ([]string, error) {
		return []string{metadataPath}, nil
	})
	defer stubs.Reset()

	_ = consul.ReloadBCSInfo()
	reloadData, err := consul.ReloadRouterInfo()
	assert.Nil(t, err)
	ReloadTableInfos(reloadData)
	metricData, err := consul.ReloadMetricInfo()
	assert.Nil(t, err)
	ReloadMetricRouter(metricData)

	type testProcess struct {
		ids    interface{} // []int, []string
		idType string
	}

	var testDataIDFilter *DataIDFilter
	testCases := map[string]struct {
		metric string
		items  []testProcess
		expect []consul.DataID
	}{
		// d.FilterBiz().FilterProject().FilterCluster()
		"alive": {
			metric: "alive",
			items: []testProcess{
				{
					ids:    []int{0, 2},
					idType: "biz",
				},
				{
					ids:    []string{"aaaaa"},
					idType: "project",
				},
				{
					ids:    []string{"bcs-k8s"},
					idType: "cluster",
				},
			},
			expect: []consul.DataID{1500009},
		},
		"kube_daemonset_status_desired_number_scheduled": {
			metric: "kube_daemonset_status_desired_number_scheduled",
			items: []testProcess{
				{
					ids:    []int{100867},
					idType: "biz",
				},
				{
					ids:    []string{"BCS-K8S-25595"},
					idType: "cluster",
				},
			},
			expect: []consul.DataID{535008, 535007},
		},
		// d.FilterMetric
		"filter_metric": {
			metric: "alive",
			items: []testProcess{
				{
					ids:    []int{0, 2},
					idType: "biz",
				},
			},
			expect: []consul.DataID{1500009},
		},
		"asdfas": {
			metric: "asdfas",
			items: []testProcess{
				{
					ids:    []int{0, 2},
					idType: "biz",
				},
			},
			expect: nil,
		},
		"empty": {
			metric: "",
			items: []testProcess{
				{
					ids:    []string{"bcs-k8s"},
					idType: "cluster",
				},
			},
			expect: []consul.DataID{1500009, 1500011},
		},
	}

	for _, testCase := range testCases {
		testDataIDFilter = NewDataIDFilter(testCase.metric)
		for _, item := range testCase.items {
			switch item.idType {
			case "biz":
				testDataIDFilter.FilterByBizIDs(item.ids.([]int)...)
			case "project":
				testDataIDFilter.FilterByProjectIDs(item.ids.([]string)...)
			case "cluster":
				testDataIDFilter.FilterByClusterIDs(item.ids.([]string)...)
			}
		}

		if len(testCase.expect) > 1 {
			// 如果有多个dataid，其在列表中的顺序可能不同，故用Contains方式进行断言
			for _, dataid := range testDataIDFilter.Values() {
				assert.Contains(t, testCase.expect, dataid)
			}
		} else {
			assert.Equal(t, testCase.expect, testDataIDFilter.Values())
		}

	}
}
