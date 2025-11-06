// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

func TestInstance_ShowCreateTable(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	ins := createTestInstance(ctx)

	mock.BkSQL.Set(map[string]any{
		"SHOW CREATE TABLE `bklog_pure_v4_log_doris_for_unify_query_2`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":28,"external_api_call_time_mills":{"bkbase_auth_api":44,"bkbase_meta_api":13,"bkbase_apigw_api":0},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Analyzed":"false","Type":"int","Null":"No","Key":"true","Default":null,"Extra":""},{"Field":"__shard_key__","Analyzed":"false","Type":"bigint","Null":"No","Key":"true","Default":null,"Extra":""},{"Field":"__unique_key__","Analyzed":"false","Type":"varchar(512)","Null":"Yes","Key":"true","Default":null,"Extra":""},{"Field":"dtEventTimeStamp","Analyzed":"false","Type":"bigint","Null":"No","Key":"false","Default":null,"Extra":"NONE"},{"Field":"dtEventTime","Analyzed":"false","Type":"varchar(32)","Null":"No","Key":"false","Default":null,"Extra":"NONE"},{"Field":"localTime","Analyzed":"false","Type":"varchar(32)","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext","Analyzed":"false","Type":"variant","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"cloudId","Analyzed":"false","Type":"double","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"file","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"gseIndex","Analyzed":"false","Type":"double","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"iterationIndex","Analyzed":"false","Type":"double","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"level","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"log","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"message","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"path","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"report_time","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"serverIp","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"time","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"trace_id","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.container_id","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.container_image","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.container_name","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.io_kubernetes_pod","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.io_kubernetes_pod_ip","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.io_kubernetes_pod_namespace","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.io_kubernetes_pod_uid","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.io_kubernetes_workload_name","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"},{"Field":"__ext.io_kubernetes_workload_type","Analyzed":"false","Type":"text","Null":"Yes","Key":"false","Default":null,"Extra":"NONE"}],"bk_biz_ids":[],"stage_elapsed_time_mills":{"check_query_syntax":0,"query_db":16,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":12,"connect_db":48,"match_query_routing_rule":0,"check_permission":45,"check_query_semantic":0,"pick_valid_storage":0},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"DESC mapleleaf_2.bklog_pure_v4_log_doris_for_unify_query_2","total_record_size":19880,"trino_cluster_host":"","timetaken":0.121,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_pure_v4_log_doris_for_unify_query"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	end := time.UnixMilli(1730118889181)
	start := time.UnixMilli(1730118589181)

	datasource := "bklog"
	db := "bklog_pure_v4_log_doris_for_unify_query_2"
	measurement := "doris"

	for name, c := range map[string]struct {
		query    *metadata.Query
		expected string
	}{
		"field map": {
			query: &metadata.Query{
				DataSource:  datasource,
				DB:          db,
				Measurement: measurement,
				FieldAlias: map[string]string{
					"container_name": "__ext.container_name",
				},
			},
			expected: `{"__ext":{"alias_name":"","field_name":"__ext","field_type":"variant","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.container_id":{"alias_name":"","field_name":"__ext.container_id","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.container_image":{"alias_name":"","field_name":"__ext.container_image","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.container_name":{"alias_name":"container_name","field_name":"__ext.container_name","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_pod":{"alias_name":"","field_name":"__ext.io_kubernetes_pod","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_pod_ip":{"alias_name":"","field_name":"__ext.io_kubernetes_pod_ip","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_pod_namespace":{"alias_name":"","field_name":"__ext.io_kubernetes_pod_namespace","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_pod_uid":{"alias_name":"","field_name":"__ext.io_kubernetes_pod_uid","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_workload_name":{"alias_name":"","field_name":"__ext.io_kubernetes_workload_name","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__ext.io_kubernetes_workload_type":{"alias_name":"","field_name":"__ext.io_kubernetes_workload_type","field_type":"text","origin_field":"__ext","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__shard_key__":{"alias_name":"","field_name":"__shard_key__","field_type":"bigint","origin_field":"__shard_key__","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"__unique_key__":{"alias_name":"","field_name":"__unique_key__","field_type":"varchar(512)","origin_field":"__unique_key__","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"cloudId":{"alias_name":"","field_name":"cloudId","field_type":"double","origin_field":"cloudId","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"dtEventTime":{"alias_name":"","field_name":"dtEventTime","field_type":"varchar(32)","origin_field":"dtEventTime","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"dtEventTimeStamp":{"alias_name":"","field_name":"dtEventTimeStamp","field_type":"bigint","origin_field":"dtEventTimeStamp","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"file":{"alias_name":"","field_name":"file","field_type":"text","origin_field":"file","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"gseIndex":{"alias_name":"","field_name":"gseIndex","field_type":"double","origin_field":"gseIndex","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"iterationIndex":{"alias_name":"","field_name":"iterationIndex","field_type":"double","origin_field":"iterationIndex","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"level":{"alias_name":"","field_name":"level","field_type":"text","origin_field":"level","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"localTime":{"alias_name":"","field_name":"localTime","field_type":"varchar(32)","origin_field":"localTime","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"log":{"alias_name":"","field_name":"log","field_type":"text","origin_field":"log","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"message":{"alias_name":"","field_name":"message","field_type":"text","origin_field":"message","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"path":{"alias_name":"","field_name":"path","field_type":"text","origin_field":"path","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"report_time":{"alias_name":"","field_name":"report_time","field_type":"text","origin_field":"report_time","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"serverIp":{"alias_name":"","field_name":"serverIp","field_type":"text","origin_field":"serverIp","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"thedate":{"alias_name":"","field_name":"thedate","field_type":"int","origin_field":"thedate","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"time":{"alias_name":"","field_name":"time","field_type":"text","origin_field":"time","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"trace_id":{"alias_name":"","field_name":"trace_id","field_type":"text","origin_field":"trace_id","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			fact, err := ins.InitQueryFactory(ctx, c.query, start, end)
			assert.Nil(t, err)

			actual, _ := json.Marshal(fact.FieldMap())
			assert.Equal(t, c.expected, string(actual))
		})
	}
}

func TestInstance_QuerySeriesSet(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	ins := createTestInstance(ctx)

	mock.BkSQL.Set(map[string]any{
		// doris
		"SHOW CREATE TABLE `2_bklog_bkunify_query_doris`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":18,"external_api_call_time_mills":{"bkbase_auth_api":43,"bkbase_meta_api":0,"bkbase_apigw_api":33},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":0,"query_db":5,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":45,"match_query_routing_rule":0,"check_permission":43,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":11776,"timetaken":0.096,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
		// count by cloudId with doris
		"SELECT `cloudId`, COUNT(`cloudId`) AS `_value_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` <= 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' GROUP BY `cloudId` LIMIT 10005": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"2_bklog_bkunify_query_doris":{"start":"2025041100","end":"2025041123"}},"cluster":"doris-test","totalRecords":1,"external_api_call_time_mills":{"bkbase_auth_api":32,"bkbase_meta_api":0,"bkbase_apigw_api":0},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"cloudId":0.0,"_value_":6}],"stage_elapsed_time_mills":{"check_query_syntax":2,"query_db":22,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":44,"match_query_routing_rule":0,"check_permission":32,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["cloudId","_value_"],"total_record_size":456,"timetaken":0.103,"result_schema":[{"field_type":"double","field_name":"__c0","field_alias":"cloudId","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"_value_","field_index":1}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
		"SHOW CREATE TABLE `5000140_bklog_container_log_demo_analysis`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":20,"external_api_call_time_mills":{"bkbase_auth_api":64,"bkbase_meta_api":6,"bkbase_apigw_api":25},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"NO","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE","Analyzed":"true"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"c1","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"c2","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":2,"query_db":27,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":9,"connect_db":56,"match_query_routing_rule":0,"check_permission":66,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":13168,"timetaken":0.161,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,

		// count_by_namespace_with_mysql
		"SELECT `namespace`, COUNT(`login_rate`) AS `_value_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' GROUP BY `namespace` LIMIT 10005": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":11,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"namespace\":\"bgp2\",\"_value_\":5},{\"namespace\":\"cq100\",\"_value_\":5},{\"namespace\":\"gz100\",\"_value_\":5},{\"namespace\":\"hn0-new\",\"_value_\":5},{\"namespace\":\"hn1\",\"_value_\":5},{\"namespace\":\"hn10\",\"_value_\":5},{\"namespace\":\"nj100\",\"_value_\":5},{\"namespace\":\"njloadtest\",\"_value_\":5},{\"namespace\":\"pbe\",\"_value_\":5},{\"namespace\":\"tj100\",\"_value_\":5},{\"namespace\":\"tj101\",\"_value_\":5}],\"select_fields_order\":[\"namespace\",\"_value_\"],\"sql\":\"SELECT `namespace`, COUNT(`login_rate`) AS `_value_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE (`dtEventTimeStamp` >= 1730118589181) AND (`dtEventTimeStamp` < 1730118889181) GROUP BY `namespace` LIMIT 10005\",\"total_record_size\":3216,\"timetaken\":0.24,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"5c70526f101a00531ef8fbaadc783693\",\"span_id\":\"2a31369ceb208970\"}",

		// count by 1m with mysql
		"SELECT COUNT(`login_rate`) AS `_value_`, MAX(FLOOR((dtEventTimeStamp + 0) / 60000) * 60000 - 0) AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' GROUP BY (FLOOR((dtEventTimeStamp + 0) / 60000) * 60000 - 0) ORDER BY `_timestamp_` ASC LIMIT 10005": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":5,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"_value_\":11,\"_timestamp_\":1730118600000},{\"_value_\":11,\"_timestamp_\":1730118660000},{\"_value_\":11,\"_timestamp_\":1730118720000},{\"_value_\":11,\"_timestamp_\":1730118780000},{\"_value_\":11,\"_timestamp_\":1730118840000}],\"select_fields_order\":[\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT COUNT(`login_rate`) AS `_value_`, MAX(`dtEventTimeStamp` - ((`dtEventTimeStamp` - 0) % 60000 - 0)) AS `_timestamp_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE (`dtEventTimeStamp` >= 1730118589181) AND (`dtEventTimeStamp` < 1730118889181) GROUP BY `dtEventTimeStamp` - (`dtEventTimeStamp` % 60000) ORDER BY `_timestamp_` LIMIT 10005\",\"total_record_size\":1424,\"timetaken\":0.231,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"127866cb51f85a4a7f620eb0e66588b1\",\"span_id\":\"578f26767bbb78c8\"}",

		// count by 1m with doris 没有login_rate
		"SELECT COUNT(`login_rate`) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` <= 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' GROUP BY _timestamp_ ORDER BY `_timestamp_` ASC LIMIT 10005": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":5,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"_value_\":2,\"_timestamp_\":1730118720000}],\"select_fields_order\":[\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT COUNT(`login_rate`) AS `_value_`, MAX(`dtEventTimeStamp` - ((`dtEventTimeStamp` - 0) % 60000 - 0)) AS `_timestamp_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE (`dtEventTimeStamp` >= 1730118589181) AND (`dtEventTimeStamp` < 1730118889181) GROUP BY `dtEventTimeStamp` - (`dtEventTimeStamp` % 60000) ORDER BY `_timestamp_` LIMIT 10005\",\"total_record_size\":1424,\"timetaken\":0.231,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"127866cb51f85a4a7f620eb0e66588b1\",\"span_id\":\"578f26767bbb78c8\"}",
	})
	end := time.UnixMilli(1730118889181)
	start := time.UnixMilli(1730118589181)

	datasource := "bkdata"
	db := "132_lol_new_login_queue_login_1min"
	field := "login_rate"
	tableID := db + ".default"

	for name, c := range map[string]struct {
		query    *metadata.Query
		expected string
	}{
		"count by cloudId with doris": {
			query: &metadata.Query{
				DataSource:  datasource,
				TableID:     tableID,
				DB:          "2_bklog_bkunify_query_doris",
				Measurement: "doris",
				Field:       "cloudId",
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"cloudId"},
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:cloudId"},{"name":"cloudId","value":"0"}],"samples":[{"value":6,"timestamp":1730118589181}],"exemplars":null,"histograms":null}]`,
		},
		"count by namespace with mysql": {
			query: &metadata.Query{
				DataSource: datasource,
				TableID:    tableID,
				DB:         db,
				Field:      field,
				DataLabel:  db,
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"namespace"},
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"bgp2"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"cq100"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"gz100"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"hn0-new"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"hn1"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"hn10"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"nj100"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"njloadtest"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"pbe"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"tj100"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bkdata:132_lol_new_login_queue_login_1min:default:login_rate"},{"name":"namespace","value":"tj101"}],"samples":[{"value":5,"timestamp":1730118589181}],"exemplars":null,"histograms":null}]`,
		},
		"count with 1m with mysql": {
			query: &metadata.Query{
				DataSource: datasource,
				TableID:    tableID,
				DB:         db,
				Field:      field,
				DataLabel:  db,
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute,
					},
				},
			},
			expected: `[ {
  "labels" : [ {
    "name" : "__name__",
    "value" : "bkdata:132_lol_new_login_queue_login_1min:default:login_rate"
  } ],
  "samples" : [{
    "value" : 11,
    "timestamp" : 1730118600000
  }, {
    "value" : 11,
    "timestamp" : 1730118660000
  }, {
    "value" : 11,
    "timestamp" : 1730118720000
  }, {
    "value" : 11,
    "timestamp" : 1730118780000
  }, {
    "value" : 11,
    "timestamp" : 1730118840000
  } ],
  "exemplars" : null,
  "histograms" : null
} ]`,
		},
		"count with 1m with doris": {
			query: &metadata.Query{
				DataSource:  datasource,
				TableID:     tableID,
				DB:          "2_bklog_bkunify_query_doris",
				Measurement: sql_expr.Doris,
				Field:       field,
				DataLabel:   db,
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute,
					},
				},
			},
			expected: `[ {
  "labels" : [ {
    "name" : "__name__",
    "value" : "bkdata:132_lol_new_login_queue_login_1min:default:login_rate"
  } ],
  "samples" : [ {
    "timestamp" : 1730118540000
  }, {
    "timestamp" : 1730118600000
  }, {
    "timestamp" : 1730118660000
  }, {
    "value" : 2,
    "timestamp" : 1730118720000
  }, {
    "timestamp" : 1730118780000
  }, {
    "timestamp" : 1730118840000
  } ],
  "exemplars" : null,
  "histograms" : null
} ]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			if c.query.DB == "" {
				c.query.DB = db
			}
			if c.query.Field == "" {
				c.query.Field = field
			}

			set := ins.QuerySeriesSet(ctx, c.query, start, end)
			ts, err := mock.SeriesSetToTimeSeries(set)
			assert.Nil(t, err)

			actual, err := json.Marshal(ts)
			assert.Nil(t, err)

			assert.JSONEq(t, c.expected, string(actual))
		})
	}
}

func TestInstance_QueryRaw(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	ins := createTestInstance(ctx)

	mock.BkSQL.Set(map[string]any{
		// query raw by doris use condition
		"SHOW CREATE TABLE `2_bklog_pure_v4_log_doris_for_unify_query`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":18,"external_api_call_time_mills":{"bkbase_auth_api":69,"bkbase_meta_api":9,"bkbase_apigw_api":25},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"__shard_key__","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"cloudId","Type":"decimalv3(38, 6)","Null":"YES","Key":"YES","Default":null,"Extra":""},{"Field":"serverIp","Type":"varchar(512)","Null":"YES","Key":"YES","Default":null,"Extra":""},{"Field":"path","Type":"varchar(512)","Null":"YES","Key":"YES","Default":null,"Extra":""},{"Field":"gseIndex","Type":"decimalv3(38, 6)","Null":"YES","Key":"YES","Default":null,"Extra":""},{"Field":"iterationIndex","Type":"decimalv3(38, 6)","Null":"YES","Key":"YES","Default":null,"Extra":""},{"Field":"dtEventTimeStamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dtEventTime","Type":"varchar(32)","Null":"NO","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localTime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE","Analyzed":"true"},{"Field":"message","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":6,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":6,"connect_db":66,"match_query_routing_rule":0,"check_permission":69,"check_query_semantic":0,"pick_valid_storage":0},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_pure_v4_log_doris_for_unify_query_2","total_record_size":11808,"timetaken":0.148,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_pure_v4_log_doris_for_unify_query"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
		// query raw by doris use condition
		"SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_pure_v4_log_doris_for_unify_query`.doris WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` <= 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND (`message` MATCH_PHRASE 'Bk-Query-Source' OR (`level` MATCH_PHRASE 'error' OR `level` MATCH_PHRASE 'info')) LIMIT 5": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"2_bklog_pure_v4_log_doris_for_unify_query":{"start":"2025042100","end":"2025042123"}},"cluster":"doris-test","totalRecords":5,"external_api_call_time_mills":{"bkbase_auth_api":38,"bkbase_meta_api":0,"bkbase_apigw_api":0},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"thedate":20250421,"__shard_key__":29087245464,"cloudId":0.0,"path":"/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log","gseIndex":4281730.0,"iterationIndex":0.0,"dtEventTimeStamp":1745234704000,"dtEventTime":"2025-04-21 19:25:04","localTime":"2025-04-21 19:25:04","file":"http/handler.go:361","level":"info","log":"2025-04-21T11:25:00.643Z\tinfo\thttp/handler.go:361\t[9a8222f1a3407f97f351207752953cb5] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[204] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-ed105a4a519bddd9-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__2]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:2_bkapm_metric_mandotest.__default__:trpc.*\\\"})\",\"start\":\"1745234900\",\"end\":\"1745235100\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}","message":" header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[204] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-ed105a4a519bddd9-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__2]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:2_bkapm_metric_mandotest.__default__:trpc.*\\\"})\",\"start\":\"1745234900\",\"end\":\"1745235100\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}","report_time":"2025-04-21T11:25:00.643Z","time":"1745234704","trace_id":"9a8222f1a3407f97f351207752953cb5","_value_":1745234704000,"_timestamp_":1745234704000},{"thedate":20250421,"__shard_key__":29087245464,"cloudId":0.0,"path":"/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log","gseIndex":4281730.0,"iterationIndex":2.0,"dtEventTimeStamp":1745234704000,"dtEventTime":"2025-04-21 19:25:04","localTime":"2025-04-21 19:25:04","file":"http/handler.go:361","level":"info","log":"2025-04-21T11:25:00.790Z\tinfo\thttp/handler.go:361\t[9a8222f1a3407f97f351207752953cb5] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[202] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-1b008865dfe232ab-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__7]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:7_bkapm_metric_bk_itsm.__default__:trpc.*\\\"})\",\"start\":\"1745234700\",\"end\":\"1745234900\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}","message":" header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[202] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-1b008865dfe232ab-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__7]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:7_bkapm_metric_bk_itsm.__default__:trpc.*\\\"})\",\"start\":\"1745234700\",\"end\":\"1745234900\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}","report_time":"2025-04-21T11:25:00.790Z","time":"1745234704","trace_id":"9a8222f1a3407f97f351207752953cb5","_value_":1745234704000,"_timestamp_":1745234704000},{"thedate":20250421,"__shard_key__":29087245464,"cloudId":0.0,"path":"/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log","gseIndex":4281730.0,"iterationIndex":4.0,"dtEventTimeStamp":1745234704000,"dtEventTime":"2025-04-21 19:25:04","localTime":"2025-04-21 19:25:04","file":"http/handler.go:361","level":"info","log":"2025-04-21T11:25:00.855Z\tinfo\thttp/handler.go:361\t[9a8222f1a3407f97f351207752953cb5] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[202] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-6c1e5853f8d7c5ea-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__7]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:7_bkapm_metric_bk_itsm.__default__:trpc.*\\\"})\",\"start\":\"1745235100\",\"end\":\"1745235300\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}","message":" header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[202] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-6c1e5853f8d7c5ea-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__7]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:7_bkapm_metric_bk_itsm.__default__:trpc.*\\\"})\",\"start\":\"1745235100\",\"end\":\"1745235300\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}","report_time":"2025-04-21T11:25:00.855Z","time":"1745234704","trace_id":"9a8222f1a3407f97f351207752953cb5","_value_":1745234704000,"_timestamp_":1745234704000},{"thedate":20250421,"__shard_key__":29087245464,"cloudId":0.0,"path":"/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log","gseIndex":4281730.0,"iterationIndex":6.0,"dtEventTimeStamp":1745234704000,"dtEventTime":"2025-04-21 19:25:04","localTime":"2025-04-21 19:25:04","file":"http/handler.go:361","level":"info","log":"2025-04-21T11:25:01.030Z\tinfo\thttp/handler.go:361\t[9a8222f1a3407f97f351207752953cb5] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[220] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-d3159692d865fe24-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bksaas__capp512]], body: {\"promql\":\"count by (service_name) ({__name__=~\\\"custom:bkapm_-59_metric_bkapp_capp512_stag_21.__default__:.*\\\"})\",\"start\":\"1745234900\",\"end\":\"1745235100\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}","message":" header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[220] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-d3159692d865fe24-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bksaas__capp512]], body: {\"promql\":\"count by (service_name) ({__name__=~\\\"custom:bkapm_-59_metric_bkapp_capp512_stag_21.__default__:.*\\\"})\",\"start\":\"1745234900\",\"end\":\"1745235100\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}","report_time":"2025-04-21T11:25:01.030Z","time":"1745234704","trace_id":"9a8222f1a3407f97f351207752953cb5","_value_":1745234704000,"_timestamp_":1745234704000},{"thedate":20250421,"__shard_key__":29087245464,"cloudId":0.0,"path":"/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log","gseIndex":4281730.0,"iterationIndex":8.0,"dtEventTimeStamp":1745234704000,"dtEventTime":"2025-04-21 19:25:04","localTime":"2025-04-21 19:25:04","file":"http/handler.go:305","level":"info","log":"2025-04-21T11:25:01.199Z\tinfo\thttp/handler.go:305\t[adb84ecc380008245cdb800b6fd54d7f] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[username:admin] Connection:[keep-alive] Content-Length:[686] Content-Type:[application/json] Traceparent:[00-adb84ecc380008245cdb800b6fd54d7f-ddc4638680c14719-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__11]], body: {\"space_uid\":\"bkcc__11\",\"query_list\":[{\"table_id\":\"system.cpu_summary\",\"field_name\":\"usage\",\"is_regexp\":false,\"function\":[{\"method\":\"mean\",\"without\":false,\"dimensions\":[\"bk_target_ip\",\"bk_target_cloud_id\"]}],\"time_aggregation\":{\"function\":\"avg_over_time\",\"window\":\"60s\"},\"is_dom_sampled\":false,\"reference_name\":\"a\",\"dimensions\":[\"bk_target_ip\",\"bk_target_cloud_id\"],\"conditions\":{},\"keep_columns\":[\"_time\",\"a\",\"bk_target_ip\",\"bk_target_cloud_id\"],\"query_string\":\"\"}],\"metric_merge\":\"a\",\"start_time\":\"1745234520\",\"end_time\":\"1745234700\",\"step\":\"60s\",\"timezone\":\"Asia/Shanghai\",\"instant\":false}","message":" header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[username:admin] Connection:[keep-alive] Content-Length:[686] Content-Type:[application/json] Traceparent:[00-adb84ecc380008245cdb800b6fd54d7f-ddc4638680c14719-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__11]], body: {\"space_uid\":\"bkcc__11\",\"query_list\":[{\"table_id\":\"system.cpu_summary\",\"field_name\":\"usage\",\"is_regexp\":false,\"function\":[{\"method\":\"mean\",\"without\":false,\"dimensions\":[\"bk_target_ip\",\"bk_target_cloud_id\"]}],\"time_aggregation\":{\"function\":\"avg_over_time\",\"window\":\"60s\"},\"is_dom_sampled\":false,\"reference_name\":\"a\",\"dimensions\":[\"bk_target_ip\",\"bk_target_cloud_id\"],\"conditions\":{},\"keep_columns\":[\"_time\",\"a\",\"bk_target_ip\",\"bk_target_cloud_id\"],\"query_string\":\"\"}],\"metric_merge\":\"a\",\"start_time\":\"1745234520\",\"end_time\":\"1745234700\",\"step\":\"60s\",\"timezone\":\"Asia/Shanghai\",\"instant\":false}","report_time":"2025-04-21T11:25:01.199Z","time":"1745234704","trace_id":"adb84ecc380008245cdb800b6fd54d7f","_value_":1745234704000,"_timestamp_":1745234704000}],"stage_elapsed_time_mills":{"check_query_syntax":2,"query_db":19,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":41,"match_query_routing_rule":0,"check_permission":39,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["thedate","__shard_key__","cloudId","serverIp","path","gseIndex","iterationIndex","dtEventTimeStamp","dtEventTime","localTime","__ext","file","level","log","message","report_time","time","trace_id","_value_","_timestamp_"],"total_record_size":30880,"timetaken":0.104,"result_schema":[{"field_type":"int","field_name":"__c0","field_alias":"thedate","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"__shard_key__","field_index":1},{"field_type":"double","field_name":"__c2","field_alias":"cloudId","field_index":2},{"field_type":"string","field_name":"__c3","field_alias":"serverIp","field_index":3},{"field_type":"string","field_name":"__c4","field_alias":"path","field_index":4},{"field_type":"double","field_name":"__c5","field_alias":"gseIndex","field_index":5},{"field_type":"double","field_name":"__c6","field_alias":"iterationIndex","field_index":6},{"field_type":"long","field_name":"__c7","field_alias":"dtEventTimeStamp","field_index":7},{"field_type":"string","field_name":"__c8","field_alias":"dtEventTime","field_index":8},{"field_type":"string","field_name":"__c9","field_alias":"localTime","field_index":9},{"field_type":"string","field_name":"__c10","field_alias":"__ext","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"file","field_index":11},{"field_type":"string","field_name":"__c12","field_alias":"level","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"log","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"message","field_index":14},{"field_type":"string","field_name":"__c15","field_alias":"report_time","field_index":15},{"field_type":"string","field_name":"__c16","field_alias":"time","field_index":16},{"field_type":"string","field_name":"__c17","field_alias":"trace_id","field_index":17},{"field_type":"long","field_name":"__c18","field_alias":"_value_","field_index":18},{"field_type":"long","field_name":"__c19","field_alias":"_timestamp_","field_index":19}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_pure_v4_log_doris_for_unify_query"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,

		"SHOW CREATE TABLE `5000140_bklog_container_log_demo_analysis`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":20,"external_api_call_time_mills":{"bkbase_auth_api":64,"bkbase_meta_api":6,"bkbase_apigw_api":25},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"NO","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE","Analyzed":"true"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"c1","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"c2","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":2,"query_db":27,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":9,"connect_db":56,"match_query_routing_rule":0,"check_permission":66,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":13168,"timetaken":0.161,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,

		// query with in
		"SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` < 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND `namespace` IN ('gz100', 'bgp2-new') LIMIT 10005": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"132_lol_new_login_queue_login_1min\":{}},\"cluster\":\"default2\",\"totalRecords\":5,\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:31:00\",\"dtEventTimeStamp\":1730118660000,\"localTime\":\"2024-10-28 20:32:03\",\"_startTime_\":\"2024-10-28 20:31:00\",\"_endTime_\":\"2024-10-28 20:32:00\",\"namespace\":\"gz100\",\"login_rate\":269.0,\"_value_\":269.0,\"_timestamp_\":1730118660000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:28:00\",\"dtEventTimeStamp\":1730118480000,\"localTime\":\"2024-10-28 20:29:03\",\"_startTime_\":\"2024-10-28 20:28:00\",\"_endTime_\":\"2024-10-28 20:29:00\",\"namespace\":\"gz100\",\"login_rate\":271.0,\"_value_\":271.0,\"_timestamp_\":1730118480000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:29:00\",\"dtEventTimeStamp\":1730118540000,\"localTime\":\"2024-10-28 20:30:02\",\"_startTime_\":\"2024-10-28 20:29:00\",\"_endTime_\":\"2024-10-28 20:30:00\",\"namespace\":\"gz100\",\"login_rate\":267.0,\"_value_\":267.0,\"_timestamp_\":1730118540000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:30:00\",\"dtEventTimeStamp\":1730118600000,\"localTime\":\"2024-10-28 20:31:04\",\"_startTime_\":\"2024-10-28 20:30:00\",\"_endTime_\":\"2024-10-28 20:31:00\",\"namespace\":\"gz100\",\"login_rate\":274.0,\"_value_\":274.0,\"_timestamp_\":1730118600000},{\"thedate\":20241028,\"dtEventTime\":\"2024-10-28 20:27:00\",\"dtEventTimeStamp\":1730118420000,\"localTime\":\"2024-10-28 20:28:03\",\"_startTime_\":\"2024-10-28 20:27:00\",\"_endTime_\":\"2024-10-28 20:28:00\",\"namespace\":\"gz100\",\"login_rate\":279.0,\"_value_\":279.0,\"_timestamp_\":1730118420000}],\"select_fields_order\":[\"thedate\",\"dtEventTime\",\"dtEventTimeStamp\",\"localTime\",\"_startTime_\",\"_endTime_\",\"namespace\",\"login_rate\",\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM mapleleaf_132.lol_new_login_queue_login_1min_132 WHERE ((`dtEventTimeStamp` >= 1730118415782) AND (`dtEventTimeStamp` < 1730118715782)) AND `namespace` IN ('gz100', 'bgp2-new') LIMIT 10005\",\"total_record_size\":5832,\"timetaken\":0.251,\"bksql_call_elapsed_time\":0,\"device\":\"tspider\",\"result_table_ids\":[\"132_lol_new_login_queue_login_1min\"]},\"errors\":null,\"trace_id\":\"c083ca92cee435138f9076e1c1f6faeb\",\"span_id\":\"735f314a259a981a\"}",

		// query raw by doris
		"SELECT *, NULL AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` <= 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' LIMIT 2": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"5000140_bklog_container_log_demo_analysis\":{\"start\":\"2025032100\",\"end\":\"2025032123\"}},\"cluster\":\"doris_bklog\",\"totalRecords\":2,\"external_api_call_time_mills\":{\"bkbase_meta_api\":0},\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"thedate\":20250321,\"dteventtimestamp\":1742540043000,\"dteventtime\":\"2025-03-21 14:54:03\",\"localtime\":\"2025-03-21 14:54:12\",\"__shard_key__\":29042334000,\"_starttime_\":\"2025-03-21 14:54:03\",\"_endtime_\":\"2025-03-21 14:54:03\",\"bk_host_id\":267382,\"__ext\":\"{\\\"container_id\\\":\\\"436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e\\\",\\\"container_image\\\":\\\"sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf\\\",\\\"container_name\\\":\\\"bkmonitorbeat\\\",\\\"io_kubernetes_pod\\\":\\\"bkm-daemonset-worker-9tckj\\\",\\\"io_kubernetes_pod_namespace\\\":\\\"bkmonitor-operator\\\",\\\"io_kubernetes_pod_uid\\\":\\\"0d310b8f-aca1-48ab-b02c-92f5c221eac3\\\",\\\"io_kubernetes_workload_name\\\":\\\"bkm-daemonset-worker\\\",\\\"io_kubernetes_workload_type\\\":\\\"DaemonSet\\\",\\\"labels\\\":{\\\"app_kubernetes_io_component\\\":\\\"bkmonitorbeat\\\",\\\"controller_revision_hash\\\":\\\"6b87cb95fc\\\",\\\"pod_template_generation\\\":\\\"14\\\"}}\",\"cloudid\":0,\"path\":\"/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log\",\"gseindex\":7451424,\"iterationindex\":2,\"log\":\"2025-03-21 06:54:03.766\\tINFO\\t[metricbeat] bkm_metricbeat_scrape_line{} 274; kvs=[uri=(http://:10251/metrics)]\",\"logtime\":\"2025-03-21 06:54:03.766\",\"level\":\"INFO\",\"cid\":\"436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e\",\"time\":1742540045,\"_value_\":267382,\"_timestamp_\":1742540043000},{\"thedate\":20250321,\"dteventtimestamp\":1742540043000,\"dteventtime\":\"2025-03-21 14:54:03\",\"localtime\":\"2025-03-21 14:54:12\",\"__shard_key__\":29042334000,\"_starttime_\":\"2025-03-21 14:54:03\",\"_endtime_\":\"2025-03-21 14:54:03\",\"bk_host_id\":267382,\"__ext\":\"{\\\"container_id\\\":\\\"436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e\\\",\\\"container_image\\\":\\\"sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf\\\",\\\"container_name\\\":\\\"bkmonitorbeat\\\",\\\"io_kubernetes_pod\\\":\\\"bkm-daemonset-worker-9tckj\\\",\\\"io_kubernetes_pod_namespace\\\":\\\"bkmonitor-operator\\\",\\\"io_kubernetes_pod_uid\\\":\\\"0d310b8f-aca1-48ab-b02c-92f5c221eac3\\\",\\\"io_kubernetes_workload_name\\\":\\\"bkm-daemonset-worker\\\",\\\"io_kubernetes_workload_type\\\":\\\"DaemonSet\\\",\\\"labels\\\":{\\\"app_kubernetes_io_component\\\":\\\"bkmonitorbeat\\\",\\\"controller_revision_hash\\\":\\\"6b87cb95fc\\\",\\\"pod_template_generation\\\":\\\"14\\\"}}\",\"cloudid\":0,\"path\":\"/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log\",\"gseindex\":7451424,\"iterationindex\":1,\"log\":\"2025-03-21 06:54:03.766\\tINFO\\t[metricbeat] bkm_metricbeat_scrape_duration_seconds{} 0.002395; kvs=[uri=(http://:10251/metrics)]\",\"logtime\":\"2025-03-21 06:54:03.766\",\"level\":\"INFO\",\"cid\":\"436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e\",\"time\":1742540045,\"_value_\":267382,\"_timestamp_\":1742540043000}],\"stage_elapsed_time_mills\":{\"check_query_syntax\":1,\"query_db\":21,\"get_query_driver\":0,\"match_query_forbidden_config\":0,\"convert_query_statement\":1,\"connect_db\":45,\"match_query_routing_rule\":0,\"check_permission\":0,\"check_query_semantic\":0,\"pick_valid_storage\":1},\"select_fields_order\":[\"thedate\",\"dteventtimestamp\",\"dteventtime\",\"localtime\",\"__shard_key__\",\"_starttime_\",\"_endtime_\",\"bk_host_id\",\"__ext\",\"cloudid\",\"serverip\",\"path\",\"gseindex\",\"iterationindex\",\"log\",\"logtime\",\"level\",\"cid\",\"time\",\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT *, `bk_host_id` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM mapleleaf_5000140.bklog_container_log_demo_analysis_5000140__2 WHERE `thedate` = '20250321' LIMIT 2\",\"total_record_size\":9304,\"timetaken\":0.069,\"result_schema\":[{\"field_type\":\"int\",\"field_name\":\"__c0\",\"field_alias\":\"thedate\",\"field_index\":0},{\"field_type\":\"long\",\"field_name\":\"__c1\",\"field_alias\":\"dteventtimestamp\",\"field_index\":1},{\"field_type\":\"string\",\"field_name\":\"__c2\",\"field_alias\":\"dteventtime\",\"field_index\":2},{\"field_type\":\"string\",\"field_name\":\"__c3\",\"field_alias\":\"localtime\",\"field_index\":3},{\"field_type\":\"long\",\"field_name\":\"__c4\",\"field_alias\":\"__shard_key__\",\"field_index\":4},{\"field_type\":\"string\",\"field_name\":\"__c5\",\"field_alias\":\"_starttime_\",\"field_index\":5},{\"field_type\":\"string\",\"field_name\":\"__c6\",\"field_alias\":\"_endtime_\",\"field_index\":6},{\"field_type\":\"int\",\"field_name\":\"__c7\",\"field_alias\":\"bk_host_id\",\"field_index\":7},{\"field_type\":\"string\",\"field_name\":\"__c8\",\"field_alias\":\"__ext\",\"field_index\":8},{\"field_type\":\"int\",\"field_name\":\"__c9\",\"field_alias\":\"cloudid\",\"field_index\":9},{\"field_type\":\"string\",\"field_name\":\"__c10\",\"field_alias\":\"serverip\",\"field_index\":10},{\"field_type\":\"string\",\"field_name\":\"__c11\",\"field_alias\":\"path\",\"field_index\":11},{\"field_type\":\"long\",\"field_name\":\"__c12\",\"field_alias\":\"gseindex\",\"field_index\":12},{\"field_type\":\"int\",\"field_name\":\"__c13\",\"field_alias\":\"iterationindex\",\"field_index\":13},{\"field_type\":\"string\",\"field_name\":\"__c14\",\"field_alias\":\"log\",\"field_index\":14},{\"field_type\":\"string\",\"field_name\":\"__c15\",\"field_alias\":\"logtime\",\"field_index\":15},{\"field_type\":\"string\",\"field_name\":\"__c16\",\"field_alias\":\"level\",\"field_index\":16},{\"field_type\":\"string\",\"field_name\":\"__c17\",\"field_alias\":\"cid\",\"field_index\":17},{\"field_type\":\"long\",\"field_name\":\"__c18\",\"field_alias\":\"time\",\"field_index\":18},{\"field_type\":\"int\",\"field_name\":\"__c19\",\"field_alias\":\"_value_\",\"field_index\":19},{\"field_type\":\"long\",\"field_name\":\"__c20\",\"field_alias\":\"_timestamp_\",\"field_index\":20}],\"bksql_call_elapsed_time\":0,\"device\":\"doris\",\"result_table_ids\":[\"5000140_bklog_container_log_demo_analysis\"]},\"errors\":null,\"trace_id\":\"1d6580ef7e6d7e7c040801a72645fdf2\",\"span_id\":\"ab5485e1dd6595bc\"}",

		// query raw by doris
		"SELECT *, NULL AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1730118589181 AND `dtEventTimeStamp` <= 1730118889181 AND `dtEventTime` >= '2024-10-28 20:29:49' AND `dtEventTime` <= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND (`log` MATCH_PHRASE 'metricbeat_scrape' OR `log` MATCH_PHRASE 'metricbeat') LIMIT 2": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"5000140_bklog_container_log_demo_analysis\":{\"start\":\"2025032100\",\"end\":\"2025032123\"}},\"cluster\":\"doris_bklog\",\"totalRecords\":2,\"external_api_call_time_mills\":{\"bkbase_meta_api\":0},\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"thedate\":20250321,\"dteventtimestamp\":1742540043000,\"dteventtime\":\"2025-03-21 14:54:03\",\"localtime\":\"2025-03-21 14:54:12\",\"__shard_key__\":29042334000,\"_starttime_\":\"2025-03-21 14:54:03\",\"_endtime_\":\"2025-03-21 14:54:03\",\"bk_host_id\":267382,\"__ext\":\"{\\\"container_id\\\":\\\"436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e\\\",\\\"container_image\\\":\\\"sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf\\\",\\\"container_name\\\":\\\"bkmonitorbeat\\\",\\\"io_kubernetes_pod\\\":\\\"bkm-daemonset-worker-9tckj\\\",\\\"io_kubernetes_pod_namespace\\\":\\\"bkmonitor-operator\\\",\\\"io_kubernetes_pod_uid\\\":\\\"0d310b8f-aca1-48ab-b02c-92f5c221eac3\\\",\\\"io_kubernetes_workload_name\\\":\\\"bkm-daemonset-worker\\\",\\\"io_kubernetes_workload_type\\\":\\\"DaemonSet\\\",\\\"labels\\\":{\\\"app_kubernetes_io_component\\\":\\\"bkmonitorbeat\\\",\\\"controller_revision_hash\\\":\\\"6b87cb95fc\\\",\\\"pod_template_generation\\\":\\\"14\\\"}}\",\"cloudid\":0,\"path\":\"/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log\",\"gseindex\":7451424,\"iterationindex\":2,\"log\":\"2025-03-21 06:54:03.766\\tINFO\\t[metricbeat] bkm_metricbeat_scrape_line{} 274; kvs=[uri=(http://:10251/metrics)]\",\"logtime\":\"2025-03-21 06:54:03.766\",\"level\":\"INFO\",\"cid\":\"436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e\",\"time\":1742540045,\"_value_\":267382,\"_timestamp_\":1742540043000},{\"thedate\":20250321,\"dteventtimestamp\":1742540043000,\"dteventtime\":\"2025-03-21 14:54:03\",\"localtime\":\"2025-03-21 14:54:12\",\"__shard_key__\":29042334000,\"_starttime_\":\"2025-03-21 14:54:03\",\"_endtime_\":\"2025-03-21 14:54:03\",\"bk_host_id\":267382,\"__ext\":\"{\\\"container_id\\\":\\\"436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e\\\",\\\"container_image\\\":\\\"sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf\\\",\\\"container_name\\\":\\\"bkmonitorbeat\\\",\\\"io_kubernetes_pod\\\":\\\"bkm-daemonset-worker-9tckj\\\",\\\"io_kubernetes_pod_namespace\\\":\\\"bkmonitor-operator\\\",\\\"io_kubernetes_pod_uid\\\":\\\"0d310b8f-aca1-48ab-b02c-92f5c221eac3\\\",\\\"io_kubernetes_workload_name\\\":\\\"bkm-daemonset-worker\\\",\\\"io_kubernetes_workload_type\\\":\\\"DaemonSet\\\",\\\"labels\\\":{\\\"app_kubernetes_io_component\\\":\\\"bkmonitorbeat\\\",\\\"controller_revision_hash\\\":\\\"6b87cb95fc\\\",\\\"pod_template_generation\\\":\\\"14\\\"}}\",\"cloudid\":0,\"path\":\"/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log\",\"gseindex\":7451424,\"iterationindex\":1,\"log\":\"2025-03-21 06:54:03.766\\tINFO\\t[metricbeat] bkm_metricbeat_scrape_duration_seconds{} 0.002395; kvs=[uri=(http://:10251/metrics)]\",\"logtime\":\"2025-03-21 06:54:03.766\",\"level\":\"INFO\",\"cid\":\"436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e\",\"time\":1742540045,\"_value_\":267382,\"_timestamp_\":1742540043000}],\"stage_elapsed_time_mills\":{\"check_query_syntax\":1,\"query_db\":21,\"get_query_driver\":0,\"match_query_forbidden_config\":0,\"convert_query_statement\":1,\"connect_db\":45,\"match_query_routing_rule\":0,\"check_permission\":0,\"check_query_semantic\":0,\"pick_valid_storage\":1},\"select_fields_order\":[\"thedate\",\"dteventtimestamp\",\"dteventtime\",\"localtime\",\"__shard_key__\",\"_starttime_\",\"_endtime_\",\"bk_host_id\",\"__ext\",\"cloudid\",\"serverip\",\"path\",\"gseindex\",\"iterationindex\",\"log\",\"logtime\",\"level\",\"cid\",\"time\",\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT *, `bk_host_id` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM mapleleaf_5000140.bklog_container_log_demo_analysis_5000140__2 WHERE `thedate` = '20250321' LIMIT 2\",\"total_record_size\":9304,\"timetaken\":0.069,\"result_schema\":[{\"field_type\":\"int\",\"field_name\":\"__c0\",\"field_alias\":\"thedate\",\"field_index\":0},{\"field_type\":\"long\",\"field_name\":\"__c1\",\"field_alias\":\"dteventtimestamp\",\"field_index\":1},{\"field_type\":\"string\",\"field_name\":\"__c2\",\"field_alias\":\"dteventtime\",\"field_index\":2},{\"field_type\":\"string\",\"field_name\":\"__c3\",\"field_alias\":\"localtime\",\"field_index\":3},{\"field_type\":\"long\",\"field_name\":\"__c4\",\"field_alias\":\"__shard_key__\",\"field_index\":4},{\"field_type\":\"string\",\"field_name\":\"__c5\",\"field_alias\":\"_starttime_\",\"field_index\":5},{\"field_type\":\"string\",\"field_name\":\"__c6\",\"field_alias\":\"_endtime_\",\"field_index\":6},{\"field_type\":\"int\",\"field_name\":\"__c7\",\"field_alias\":\"bk_host_id\",\"field_index\":7},{\"field_type\":\"string\",\"field_name\":\"__c8\",\"field_alias\":\"__ext\",\"field_index\":8},{\"field_type\":\"int\",\"field_name\":\"__c9\",\"field_alias\":\"cloudid\",\"field_index\":9},{\"field_type\":\"string\",\"field_name\":\"__c10\",\"field_alias\":\"serverip\",\"field_index\":10},{\"field_type\":\"string\",\"field_name\":\"__c11\",\"field_alias\":\"path\",\"field_index\":11},{\"field_type\":\"long\",\"field_name\":\"__c12\",\"field_alias\":\"gseindex\",\"field_index\":12},{\"field_type\":\"int\",\"field_name\":\"__c13\",\"field_alias\":\"iterationindex\",\"field_index\":13},{\"field_type\":\"string\",\"field_name\":\"__c14\",\"field_alias\":\"log\",\"field_index\":14},{\"field_type\":\"string\",\"field_name\":\"__c15\",\"field_alias\":\"logtime\",\"field_index\":15},{\"field_type\":\"string\",\"field_name\":\"__c16\",\"field_alias\":\"level\",\"field_index\":16},{\"field_type\":\"string\",\"field_name\":\"__c17\",\"field_alias\":\"cid\",\"field_index\":17},{\"field_type\":\"long\",\"field_name\":\"__c18\",\"field_alias\":\"time\",\"field_index\":18},{\"field_type\":\"int\",\"field_name\":\"__c19\",\"field_alias\":\"_value_\",\"field_index\":19},{\"field_type\":\"long\",\"field_name\":\"__c20\",\"field_alias\":\"_timestamp_\",\"field_index\":20}],\"bksql_call_elapsed_time\":0,\"device\":\"doris\",\"result_table_ids\":[\"5000140_bklog_container_log_demo_analysis\"]},\"errors\":null,\"trace_id\":\"1d6580ef7e6d7e7c040801a72645fdf2\",\"span_id\":\"ab5485e1dd6595bc\"}",
	})

	end := time.UnixMilli(1730118889181)
	start := time.UnixMilli(1730118589181)

	datasource := "bkdata"
	db := "132_lol_new_login_queue_login_1min"
	field := "login_rate"
	tableID := db + ".default"

	for name, c := range map[string]struct {
		query    *metadata.Query
		options  string
		expected string
	}{
		"query with in": {
			query: &metadata.Query{
				DataSource: datasource,
				TableID:    tableID,
				DB:         db,
				DataLabel:  db,
				Field:      field,
				OffsetInfo: metadata.OffSetInfo{Limit: 10},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"gz100", "bgp2-new"},
						},
					},
				},
			},
			expected: `[ {
  "__data_label" : "132_lol_new_login_queue_login_1min",
  "__index" : "132_lol_new_login_queue_login_1min",
  "__result_table" : "132_lol_new_login_queue_login_1min.default",
  "__table_uuid" : "132_lol_new_login_queue_login_1min.default",
  "_endTime_" : "2024-10-28 20:32:00",
  "_startTime_" : "2024-10-28 20:31:00",
  "_timestamp_" : 1730118660000,
  "_value_" : 269,
  "dtEventTime" : "2024-10-28 20:31:00",
  "dtEventTimeStamp" : 1730118660000,
  "localTime" : "2024-10-28 20:32:03",
  "login_rate" : 269,
  "namespace" : "gz100",
  "thedate" : 20241028
}, {
  "__data_label" : "132_lol_new_login_queue_login_1min",
  "__index" : "132_lol_new_login_queue_login_1min",
  "__result_table" : "132_lol_new_login_queue_login_1min.default",
  "__table_uuid" : "132_lol_new_login_queue_login_1min.default",
  "_endTime_" : "2024-10-28 20:29:00",
  "_startTime_" : "2024-10-28 20:28:00",
  "_timestamp_" : 1730118480000,
  "_value_" : 271,
  "dtEventTime" : "2024-10-28 20:28:00",
  "dtEventTimeStamp" : 1730118480000,
  "localTime" : "2024-10-28 20:29:03",
  "login_rate" : 271,
  "namespace" : "gz100",
  "thedate" : 20241028
}, {
  "__data_label" : "132_lol_new_login_queue_login_1min",
  "__index" : "132_lol_new_login_queue_login_1min",
  "__result_table" : "132_lol_new_login_queue_login_1min.default",
  "__table_uuid" : "132_lol_new_login_queue_login_1min.default",
  "_endTime_" : "2024-10-28 20:30:00",
  "_startTime_" : "2024-10-28 20:29:00",
  "_timestamp_" : 1730118540000,
  "_value_" : 267,
  "dtEventTime" : "2024-10-28 20:29:00",
  "dtEventTimeStamp" : 1730118540000,
  "localTime" : "2024-10-28 20:30:02",
  "login_rate" : 267,
  "namespace" : "gz100",
  "thedate" : 20241028
}, {
  "__data_label" : "132_lol_new_login_queue_login_1min",
  "__index" : "132_lol_new_login_queue_login_1min",
  "__result_table" : "132_lol_new_login_queue_login_1min.default",
  "__table_uuid" : "132_lol_new_login_queue_login_1min.default",
  "_endTime_" : "2024-10-28 20:31:00",
  "_startTime_" : "2024-10-28 20:30:00",
  "_timestamp_" : 1730118600000,
  "_value_" : 274,
  "dtEventTime" : "2024-10-28 20:30:00",
  "dtEventTimeStamp" : 1730118600000,
  "localTime" : "2024-10-28 20:31:04",
  "login_rate" : 274,
  "namespace" : "gz100",
  "thedate" : 20241028
}, {
  "__data_label" : "132_lol_new_login_queue_login_1min",
  "__index" : "132_lol_new_login_queue_login_1min",
  "__result_table" : "132_lol_new_login_queue_login_1min.default",
  "__table_uuid" : "132_lol_new_login_queue_login_1min.default",
  "_endTime_" : "2024-10-28 20:28:00",
  "_startTime_" : "2024-10-28 20:27:00",
  "_timestamp_" : 1730118420000,
  "_value_" : 279,
  "dtEventTime" : "2024-10-28 20:27:00",
  "dtEventTimeStamp" : 1730118420000,
  "localTime" : "2024-10-28 20:28:03",
  "login_rate" : 279,
  "namespace" : "gz100",
  "thedate" : 20241028
} ]`,
		},
		"query raw by doris": {
			query: &metadata.Query{
				TableID:     "5000140_bklog_container_log_demo_analysis.doris",
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "bk_host_id",
				DataLabel:   "5000140_bklog_container_log_demo_analysis",
				Size:        2,
			},
			expected: `[ {
  "__data_label" : "5000140_bklog_container_log_demo_analysis",
  "__ext.container_id" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "__ext.container_image" : "sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf",
  "__ext.container_name" : "bkmonitorbeat",
  "__ext.io_kubernetes_pod" : "bkm-daemonset-worker-9tckj",
  "__ext.io_kubernetes_pod_namespace" : "bkmonitor-operator",
  "__ext.io_kubernetes_pod_uid" : "0d310b8f-aca1-48ab-b02c-92f5c221eac3",
  "__ext.io_kubernetes_workload_name" : "bkm-daemonset-worker",
  "__ext.io_kubernetes_workload_type" : "DaemonSet",
  "__ext.labels.app_kubernetes_io_component" : "bkmonitorbeat",
  "__ext.labels.controller_revision_hash" : "6b87cb95fc",
  "__ext.labels.pod_template_generation" : "14",
  "__index" : "5000140_bklog_container_log_demo_analysis",
  "__result_table" : "5000140_bklog_container_log_demo_analysis.doris",
  "__shard_key__" : 29042334000,
  "__table_uuid" : "5000140_bklog_container_log_demo_analysis.doris",
  "_endtime_" : "2025-03-21 14:54:03",
  "_starttime_" : "2025-03-21 14:54:03",
  "_timestamp_" : 1742540043000,
  "_value_" : 267382,
  "bk_host_id" : 267382,
  "cid" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "cloudid" : 0,
  "dteventtime" : "2025-03-21 14:54:03",
  "dteventtimestamp" : 1742540043000,
  "gseindex" : 7451424,
  "iterationindex" : 2,
  "level" : "INFO",
  "localtime" : "2025-03-21 14:54:12",
  "log" : "2025-03-21 06:54:03.766\tINFO\t[metricbeat] bkm_metricbeat_scrape_line{} 274; kvs=[uri=(http://:10251/metrics)]",
  "logtime" : "2025-03-21 06:54:03.766",
  "path" : "/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log",
  "thedate" : 20250321,
  "time" : 1742540045
}, {
  "__data_label" : "5000140_bklog_container_log_demo_analysis",
  "__ext.container_id" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "__ext.container_image" : "sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf",
  "__ext.container_name" : "bkmonitorbeat",
  "__ext.io_kubernetes_pod" : "bkm-daemonset-worker-9tckj",
  "__ext.io_kubernetes_pod_namespace" : "bkmonitor-operator",
  "__ext.io_kubernetes_pod_uid" : "0d310b8f-aca1-48ab-b02c-92f5c221eac3",
  "__ext.io_kubernetes_workload_name" : "bkm-daemonset-worker",
  "__ext.io_kubernetes_workload_type" : "DaemonSet",
  "__ext.labels.app_kubernetes_io_component" : "bkmonitorbeat",
  "__ext.labels.controller_revision_hash" : "6b87cb95fc",
  "__ext.labels.pod_template_generation" : "14",
  "__index" : "5000140_bklog_container_log_demo_analysis",
  "__result_table" : "5000140_bklog_container_log_demo_analysis.doris",
  "__shard_key__" : 29042334000,
  "__table_uuid" : "5000140_bklog_container_log_demo_analysis.doris",
  "_endtime_" : "2025-03-21 14:54:03",
  "_starttime_" : "2025-03-21 14:54:03",
  "_timestamp_" : 1742540043000,
  "_value_" : 267382,
  "bk_host_id" : 267382,
  "cid" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "cloudid" : 0,
  "dteventtime" : "2025-03-21 14:54:03",
  "dteventtimestamp" : 1742540043000,
  "gseindex" : 7451424,
  "iterationindex" : 1,
  "level" : "INFO",
  "localtime" : "2025-03-21 14:54:12",
  "log" : "2025-03-21 06:54:03.766\tINFO\t[metricbeat] bkm_metricbeat_scrape_duration_seconds{} 0.002395; kvs=[uri=(http://:10251/metrics)]",
  "logtime" : "2025-03-21 06:54:03.766",
  "path" : "/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log",
  "thedate" : 20250321,
  "time" : 1742540045
} ]`,
		},
		"query raw by doris use querystring in": {
			query: &metadata.Query{
				TableID:     "5000140_bklog_container_log_demo_analysis.doris",
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "bk_host_id",
				DataLabel:   "5000140_bklog_container_log_demo_analysis",
				QueryString: "metricbeat_scrape metricbeat",
				Size:        2,
			},
			expected: `[ {
  "__data_label" : "5000140_bklog_container_log_demo_analysis",
  "__ext.container_id" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "__ext.container_image" : "sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf",
  "__ext.container_name" : "bkmonitorbeat",
  "__ext.io_kubernetes_pod" : "bkm-daemonset-worker-9tckj",
  "__ext.io_kubernetes_pod_namespace" : "bkmonitor-operator",
  "__ext.io_kubernetes_pod_uid" : "0d310b8f-aca1-48ab-b02c-92f5c221eac3",
  "__ext.io_kubernetes_workload_name" : "bkm-daemonset-worker",
  "__ext.io_kubernetes_workload_type" : "DaemonSet",
  "__ext.labels.app_kubernetes_io_component" : "bkmonitorbeat",
  "__ext.labels.controller_revision_hash" : "6b87cb95fc",
  "__ext.labels.pod_template_generation" : "14",
  "__index" : "5000140_bklog_container_log_demo_analysis",
  "__result_table" : "5000140_bklog_container_log_demo_analysis.doris",
  "__shard_key__" : 29042334000,
  "__table_uuid" : "5000140_bklog_container_log_demo_analysis.doris",
  "_endtime_" : "2025-03-21 14:54:03",
  "_starttime_" : "2025-03-21 14:54:03",
  "_timestamp_" : 1742540043000,
  "_value_" : 267382,
  "bk_host_id" : 267382,
  "cid" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "cloudid" : 0,
  "dteventtime" : "2025-03-21 14:54:03",
  "dteventtimestamp" : 1742540043000,
  "gseindex" : 7451424,
  "iterationindex" : 2,
  "level" : "INFO",
  "localtime" : "2025-03-21 14:54:12",
  "log" : "2025-03-21 06:54:03.766\tINFO\t[metricbeat] bkm_metricbeat_scrape_line{} 274; kvs=[uri=(http://:10251/metrics)]",
  "logtime" : "2025-03-21 06:54:03.766",
  "path" : "/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log",
  "thedate" : 20250321,
  "time" : 1742540045
}, {
  "__data_label" : "5000140_bklog_container_log_demo_analysis",
  "__ext.container_id" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "__ext.container_image" : "sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf",
  "__ext.container_name" : "bkmonitorbeat",
  "__ext.io_kubernetes_pod" : "bkm-daemonset-worker-9tckj",
  "__ext.io_kubernetes_pod_namespace" : "bkmonitor-operator",
  "__ext.io_kubernetes_pod_uid" : "0d310b8f-aca1-48ab-b02c-92f5c221eac3",
  "__ext.io_kubernetes_workload_name" : "bkm-daemonset-worker",
  "__ext.io_kubernetes_workload_type" : "DaemonSet",
  "__ext.labels.app_kubernetes_io_component" : "bkmonitorbeat",
  "__ext.labels.controller_revision_hash" : "6b87cb95fc",
  "__ext.labels.pod_template_generation" : "14",
  "__index" : "5000140_bklog_container_log_demo_analysis",
  "__result_table" : "5000140_bklog_container_log_demo_analysis.doris",
  "__shard_key__" : 29042334000,
  "__table_uuid" : "5000140_bklog_container_log_demo_analysis.doris",
  "_endtime_" : "2025-03-21 14:54:03",
  "_starttime_" : "2025-03-21 14:54:03",
  "_timestamp_" : 1742540043000,
  "_value_" : 267382,
  "bk_host_id" : 267382,
  "cid" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "cloudid" : 0,
  "dteventtime" : "2025-03-21 14:54:03",
  "dteventtimestamp" : 1742540043000,
  "gseindex" : 7451424,
  "iterationindex" : 1,
  "level" : "INFO",
  "localtime" : "2025-03-21 14:54:12",
  "log" : "2025-03-21 06:54:03.766\tINFO\t[metricbeat] bkm_metricbeat_scrape_duration_seconds{} 0.002395; kvs=[uri=(http://:10251/metrics)]",
  "logtime" : "2025-03-21 06:54:03.766",
  "path" : "/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log",
  "thedate" : 20250321,
  "time" : 1742540045
} ]`,
		},
		"query raw by doris use querystring  and max analyzed offset is 40": {
			query: &metadata.Query{
				TableID:     "5000140_bklog_container_log_demo_analysis.doris",
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Field:       "bk_host_id",
				DataLabel:   "5000140_bklog_container_log_demo_analysis",
				QueryString: "metricbeat_scrape metricbeat",
				Size:        2,
			},
			expected: `[ {
  "__data_label" : "5000140_bklog_container_log_demo_analysis",
  "__ext.container_id" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "__ext.container_image" : "sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf",
  "__ext.container_name" : "bkmonitorbeat",
  "__ext.io_kubernetes_pod" : "bkm-daemonset-worker-9tckj",
  "__ext.io_kubernetes_pod_namespace" : "bkmonitor-operator",
  "__ext.io_kubernetes_pod_uid" : "0d310b8f-aca1-48ab-b02c-92f5c221eac3",
  "__ext.io_kubernetes_workload_name" : "bkm-daemonset-worker",
  "__ext.io_kubernetes_workload_type" : "DaemonSet",
  "__ext.labels.app_kubernetes_io_component" : "bkmonitorbeat",
  "__ext.labels.controller_revision_hash" : "6b87cb95fc",
  "__ext.labels.pod_template_generation" : "14",
  "__index" : "5000140_bklog_container_log_demo_analysis",
  "__result_table" : "5000140_bklog_container_log_demo_analysis.doris",
  "__shard_key__" : 29042334000,
  "__table_uuid" : "5000140_bklog_container_log_demo_analysis.doris",
  "_endtime_" : "2025-03-21 14:54:03",
  "_starttime_" : "2025-03-21 14:54:03",
  "_timestamp_" : 1742540043000,
  "_value_" : 267382,
  "bk_host_id" : 267382,
  "cid" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "cloudid" : 0,
  "dteventtime" : "2025-03-21 14:54:03",
  "dteventtimestamp" : 1742540043000,
  "gseindex" : 7451424,
  "iterationindex" : 2,
  "level" : "INFO",
  "localtime" : "2025-03-21 14:54:12",
  "log" : "2025-03-21 06:54:03.766\tINFO\t[metricbeat] bkm_metricbeat_scrape_line{} 274; kvs=[uri=(http://:10251/metrics)]",
  "logtime" : "2025-03-21 06:54:03.766",
  "path" : "/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log",
  "thedate" : 20250321,
  "time" : 1742540045
}, {
  "__data_label" : "5000140_bklog_container_log_demo_analysis",
  "__ext.container_id" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "__ext.container_image" : "sha256:3aec083a12d24544c15f55559e80b571cb3e66e291c5f67f4366b0f9c75674bf",
  "__ext.container_name" : "bkmonitorbeat",
  "__ext.io_kubernetes_pod" : "bkm-daemonset-worker-9tckj",
  "__ext.io_kubernetes_pod_namespace" : "bkmonitor-operator",
  "__ext.io_kubernetes_pod_uid" : "0d310b8f-aca1-48ab-b02c-92f5c221eac3",
  "__ext.io_kubernetes_workload_name" : "bkm-daemonset-worker",
  "__ext.io_kubernetes_workload_type" : "DaemonSet",
  "__ext.labels.app_kubernetes_io_component" : "bkmonitorbeat",
  "__ext.labels.controller_revision_hash" : "6b87cb95fc",
  "__ext.labels.pod_template_generation" : "14",
  "__index" : "5000140_bklog_container_log_demo_analysis",
  "__result_table" : "5000140_bklog_container_log_demo_analysis.doris",
  "__shard_key__" : 29042334000,
  "__table_uuid" : "5000140_bklog_container_log_demo_analysis.doris",
  "_endtime_" : "2025-03-21 14:54:03",
  "_starttime_" : "2025-03-21 14:54:03",
  "_timestamp_" : 1742540043000,
  "_value_" : 267382,
  "bk_host_id" : 267382,
  "cid" : "436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e",
  "cloudid" : 0,
  "dteventtime" : "2025-03-21 14:54:03",
  "dteventtimestamp" : 1742540043000,
  "gseindex" : 7451424,
  "iterationindex" : 1,
  "level" : "INFO",
  "localtime" : "2025-03-21 14:54:12",
  "log" : "2025-03-21 06:54:03.766\tINFO\t[metricbeat] bkm_metricbeat_scrape_duration_seconds{} 0.002395; kvs=[uri=(http://:10251/metrics)]",
  "logtime" : "2025-03-21 06:54:03.766",
  "path" : "/data/bcs/service/docker/containers/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e/436cf7ea65b29fb0280c89a774056ac8420ee67d2ba485cfb2932e15c15d9e4e-json.log",
  "thedate" : 20250321,
  "time" : 1742540045
} ]`,
		},
		"query raw by doris use condition and result schema": {
			query: &metadata.Query{
				TableID:     "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
				DB:          "2_bklog_pure_v4_log_doris_for_unify_query",
				Measurement: "doris",
				Field:       "dtEventTimeStamp",
				DataLabel:   "log_index_set_1183",
				AllConditions: metadata.AllConditions{
					{
						{DimensionName: "message", Value: []string{"Bk-Query-Source"}, Operator: "contains"},
					},
					{
						{DimensionName: "level", Value: []string{"error", "info"}, Operator: "contains"},
					},
				},
				Size: 5,
			},
			options: `{"2_bklog.bklog_pure_v4_log_doris_for_unify_query":{"result_schema":[{"field_alias":"thedate","field_index":0,"field_name":"__c0","field_type":"int"},{"field_alias":"__shard_key__","field_index":1,"field_name":"__c1","field_type":"long"},{"field_alias":"cloudId","field_index":2,"field_name":"__c2","field_type":"double"},{"field_alias":"serverIp","field_index":3,"field_name":"__c3","field_type":"string"},{"field_alias":"path","field_index":4,"field_name":"__c4","field_type":"string"},{"field_alias":"gseIndex","field_index":5,"field_name":"__c5","field_type":"double"},{"field_alias":"iterationIndex","field_index":6,"field_name":"__c6","field_type":"double"},{"field_alias":"dtEventTimeStamp","field_index":7,"field_name":"__c7","field_type":"long"},{"field_alias":"dtEventTime","field_index":8,"field_name":"__c8","field_type":"string"},{"field_alias":"localTime","field_index":9,"field_name":"__c9","field_type":"string"},{"field_alias":"__ext","field_index":10,"field_name":"__c10","field_type":"string"},{"field_alias":"file","field_index":11,"field_name":"__c11","field_type":"string"},{"field_alias":"level","field_index":12,"field_name":"__c12","field_type":"string"},{"field_alias":"log","field_index":13,"field_name":"__c13","field_type":"string"},{"field_alias":"message","field_index":14,"field_name":"__c14","field_type":"string"},{"field_alias":"report_time","field_index":15,"field_name":"__c15","field_type":"string"},{"field_alias":"time","field_index":16,"field_name":"__c16","field_type":"string"},{"field_alias":"trace_id","field_index":17,"field_name":"__c17","field_type":"string"},{"field_alias":"_value_","field_index":18,"field_name":"__c18","field_type":"long"},{"field_alias":"_timestamp_","field_index":19,"field_name":"__c19","field_type":"long"}]}}`,
			expected: `[ {
  "__data_label" : "log_index_set_1183",
  "__index" : "2_bklog_pure_v4_log_doris_for_unify_query",
  "__result_table" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "__shard_key__" : 29087245464,
  "__table_uuid" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "_timestamp_" : 1745234704000,
  "_value_" : 1745234704000,
  "cloudId" : 0,
  "dtEventTime" : "2025-04-21 19:25:04",
  "dtEventTimeStamp" : 1745234704000,
  "file" : "http/handler.go:361",
  "gseIndex" : 4281730,
  "iterationIndex" : 0,
  "level" : "info",
  "localTime" : "2025-04-21 19:25:04",
  "log" : "2025-04-21T11:25:00.643Z\tinfo\thttp/handler.go:361\t[9a8222f1a3407f97f351207752953cb5] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[204] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-ed105a4a519bddd9-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__2]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:2_bkapm_metric_mandotest.__default__:trpc.*\\\"})\",\"start\":\"1745234900\",\"end\":\"1745235100\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}",
  "message" : " header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[204] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-ed105a4a519bddd9-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__2]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:2_bkapm_metric_mandotest.__default__:trpc.*\\\"})\",\"start\":\"1745234900\",\"end\":\"1745235100\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}",
  "path" : "/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log",
  "report_time" : "2025-04-21T11:25:00.643Z",
  "thedate" : 20250421,
  "time" : "1745234704",
  "trace_id" : "9a8222f1a3407f97f351207752953cb5"
}, {
  "__data_label" : "log_index_set_1183",
  "__index" : "2_bklog_pure_v4_log_doris_for_unify_query",
  "__result_table" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "__shard_key__" : 29087245464,
  "__table_uuid" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "_timestamp_" : 1745234704000,
  "_value_" : 1745234704000,
  "cloudId" : 0,
  "dtEventTime" : "2025-04-21 19:25:04",
  "dtEventTimeStamp" : 1745234704000,
  "file" : "http/handler.go:361",
  "gseIndex" : 4281730,
  "iterationIndex" : 2,
  "level" : "info",
  "localTime" : "2025-04-21 19:25:04",
  "log" : "2025-04-21T11:25:00.790Z\tinfo\thttp/handler.go:361\t[9a8222f1a3407f97f351207752953cb5] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[202] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-1b008865dfe232ab-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__7]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:7_bkapm_metric_bk_itsm.__default__:trpc.*\\\"})\",\"start\":\"1745234700\",\"end\":\"1745234900\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}",
  "message" : " header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[202] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-1b008865dfe232ab-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__7]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:7_bkapm_metric_bk_itsm.__default__:trpc.*\\\"})\",\"start\":\"1745234700\",\"end\":\"1745234900\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}",
  "path" : "/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log",
  "report_time" : "2025-04-21T11:25:00.790Z",
  "thedate" : 20250421,
  "time" : "1745234704",
  "trace_id" : "9a8222f1a3407f97f351207752953cb5"
}, {
  "__data_label" : "log_index_set_1183",
  "__index" : "2_bklog_pure_v4_log_doris_for_unify_query",
  "__result_table" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "__shard_key__" : 29087245464,
  "__table_uuid" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "_timestamp_" : 1745234704000,
  "_value_" : 1745234704000,
  "cloudId" : 0,
  "dtEventTime" : "2025-04-21 19:25:04",
  "dtEventTimeStamp" : 1745234704000,
  "file" : "http/handler.go:361",
  "gseIndex" : 4281730,
  "iterationIndex" : 4,
  "level" : "info",
  "localTime" : "2025-04-21 19:25:04",
  "log" : "2025-04-21T11:25:00.855Z\tinfo\thttp/handler.go:361\t[9a8222f1a3407f97f351207752953cb5] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[202] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-6c1e5853f8d7c5ea-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__7]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:7_bkapm_metric_bk_itsm.__default__:trpc.*\\\"})\",\"start\":\"1745235100\",\"end\":\"1745235300\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}",
  "message" : " header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[202] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-6c1e5853f8d7c5ea-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__7]], body: {\"promql\":\"count by (target) ({__name__=~\\\"custom:7_bkapm_metric_bk_itsm.__default__:trpc.*\\\"})\",\"start\":\"1745235100\",\"end\":\"1745235300\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}",
  "path" : "/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log",
  "report_time" : "2025-04-21T11:25:00.855Z",
  "thedate" : 20250421,
  "time" : "1745234704",
  "trace_id" : "9a8222f1a3407f97f351207752953cb5"
}, {
  "__data_label" : "log_index_set_1183",
  "__index" : "2_bklog_pure_v4_log_doris_for_unify_query",
  "__result_table" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "__shard_key__" : 29087245464,
  "__table_uuid" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "_timestamp_" : 1745234704000,
  "_value_" : 1745234704000,
  "cloudId" : 0,
  "dtEventTime" : "2025-04-21 19:25:04",
  "dtEventTimeStamp" : 1745234704000,
  "file" : "http/handler.go:361",
  "gseIndex" : 4281730,
  "iterationIndex" : 6,
  "level" : "info",
  "localTime" : "2025-04-21 19:25:04",
  "log" : "2025-04-21T11:25:01.030Z\tinfo\thttp/handler.go:361\t[9a8222f1a3407f97f351207752953cb5] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[220] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-d3159692d865fe24-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bksaas__capp512]], body: {\"promql\":\"count by (service_name) ({__name__=~\\\"custom:bkapm_-59_metric_bkapp_capp512_stag_21.__default__:.*\\\"})\",\"start\":\"1745234900\",\"end\":\"1745235100\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}",
  "message" : " header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[backend] Connection:[keep-alive] Content-Length:[220] Content-Type:[application/json] Traceparent:[00-9a8222f1a3407f97f351207752953cb5-d3159692d865fe24-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bksaas__capp512]], body: {\"promql\":\"count by (service_name) ({__name__=~\\\"custom:bkapm_-59_metric_bkapp_capp512_stag_21.__default__:.*\\\"})\",\"start\":\"1745234900\",\"end\":\"1745235100\",\"step\":\"200s\",\"bk_biz_ids\":null,\"look_back_delta\":\"\",\"instant\":false}",
  "path" : "/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log",
  "report_time" : "2025-04-21T11:25:01.030Z",
  "thedate" : 20250421,
  "time" : "1745234704",
  "trace_id" : "9a8222f1a3407f97f351207752953cb5"
}, {
  "__data_label" : "log_index_set_1183",
  "__index" : "2_bklog_pure_v4_log_doris_for_unify_query",
  "__result_table" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "__shard_key__" : 29087245464,
  "__table_uuid" : "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
  "_timestamp_" : 1745234704000,
  "_value_" : 1745234704000,
  "cloudId" : 0,
  "dtEventTime" : "2025-04-21 19:25:04",
  "dtEventTimeStamp" : 1745234704000,
  "file" : "http/handler.go:305",
  "gseIndex" : 4281730,
  "iterationIndex" : 8,
  "level" : "info",
  "localTime" : "2025-04-21 19:25:04",
  "log" : "2025-04-21T11:25:01.199Z\tinfo\thttp/handler.go:305\t[adb84ecc380008245cdb800b6fd54d7f] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[username:admin] Connection:[keep-alive] Content-Length:[686] Content-Type:[application/json] Traceparent:[00-adb84ecc380008245cdb800b6fd54d7f-ddc4638680c14719-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__11]], body: {\"space_uid\":\"bkcc__11\",\"query_list\":[{\"table_id\":\"system.cpu_summary\",\"field_name\":\"usage\",\"is_regexp\":false,\"function\":[{\"method\":\"mean\",\"without\":false,\"dimensions\":[\"bk_target_ip\",\"bk_target_cloud_id\"]}],\"time_aggregation\":{\"function\":\"avg_over_time\",\"window\":\"60s\"},\"is_dom_sampled\":false,\"reference_name\":\"a\",\"dimensions\":[\"bk_target_ip\",\"bk_target_cloud_id\"],\"conditions\":{},\"keep_columns\":[\"_time\",\"a\",\"bk_target_ip\",\"bk_target_cloud_id\"],\"query_string\":\"\"}],\"metric_merge\":\"a\",\"start_time\":\"1745234520\",\"end_time\":\"1745234700\",\"step\":\"60s\",\"timezone\":\"Asia/Shanghai\",\"instant\":false}",
  "message" : " header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[username:admin] Connection:[keep-alive] Content-Length:[686] Content-Type:[application/json] Traceparent:[00-adb84ecc380008245cdb800b6fd54d7f-ddc4638680c14719-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__11]], body: {\"space_uid\":\"bkcc__11\",\"query_list\":[{\"table_id\":\"system.cpu_summary\",\"field_name\":\"usage\",\"is_regexp\":false,\"function\":[{\"method\":\"mean\",\"without\":false,\"dimensions\":[\"bk_target_ip\",\"bk_target_cloud_id\"]}],\"time_aggregation\":{\"function\":\"avg_over_time\",\"window\":\"60s\"},\"is_dom_sampled\":false,\"reference_name\":\"a\",\"dimensions\":[\"bk_target_ip\",\"bk_target_cloud_id\"],\"conditions\":{},\"keep_columns\":[\"_time\",\"a\",\"bk_target_ip\",\"bk_target_cloud_id\"],\"query_string\":\"\"}],\"metric_merge\":\"a\",\"start_time\":\"1745234520\",\"end_time\":\"1745234700\",\"step\":\"60s\",\"timezone\":\"Asia/Shanghai\",\"instant\":false}",
  "path" : "/var/host/data/bcs/lib/docker/containers/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93/5d0cff8ae531973edec39aa9989439c6371357491c3c7791d920e6f5569d5c93-json.log",
  "report_time" : "2025-04-21T11:25:01.199Z",
  "thedate" : 20250421,
  "time" : "1745234704",
  "trace_id" : "adb84ecc380008245cdb800b6fd54d7f"
} ]`,
		},
		"query raw by doris dry run": {
			query: &metadata.Query{
				TableID:     "2_bklog.bklog_pure_v4_log_doris_for_unify_query",
				DB:          "2_bklog_pure_v4_log_doris_for_unify_query",
				Measurement: "doris",
				Field:       "dtEventTimeStamp",
				DataLabel:   "log_index_set_1183",
				AllConditions: metadata.AllConditions{
					{
						{DimensionName: "message", Value: []string{"Bk-Query-Source"}, Operator: "contains"},
					},
					{
						{DimensionName: "level", Value: []string{"error", "info"}, Operator: "contains"},
					},
				},
				DryRun: true,
				Size:   5,
			},
			options:  "{\"2_bklog.bklog_pure_v4_log_doris_for_unify_query\":{\"sql\":\"SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_pure_v4_log_doris_for_unify_query`.doris WHERE `dtEventTimeStamp` \\u003e= 1730118589181 AND `dtEventTimeStamp` \\u003c= 1730118889181 AND `dtEventTime` \\u003e= '2024-10-28 20:29:49' AND `dtEventTime` \\u003c= '2024-10-28 20:34:50' AND `thedate` = '20241028' AND (`message` MATCH_PHRASE 'Bk-Query-Source' OR (`level` MATCH_PHRASE 'error' OR `level` MATCH_PHRASE 'info')) LIMIT 5\"}}",
			expected: `[]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			if c.query.DB == "" {
				c.query.DB = db
			}
			if c.query.Field == "" {
				c.query.Field = field
			}

			dataCh := make(chan map[string]any)

			var (
				option *metadata.ResultTableOption
				err    error
			)
			go func() {
				defer func() {
					close(dataCh)
				}()
				_, _, option, err = ins.QueryRawData(ctx, c.query, start, end, dataCh)
				assert.Nil(t, err)
			}()

			list := make([]map[string]any, 0)
			for d := range dataCh {
				list = append(list, d)
			}

			actual, err := json.Marshal(list)
			assert.Nil(t, err)

			assert.JSONEq(t, c.expected, string(actual))

			if c.options != "" {
				options := make(metadata.ResultTableOptions)
				options.SetOption(c.query.TableUUID(), option)

				optString, _ := json.Marshal(options)
				assert.Equal(t, c.options, string(optString))
			}
		})
	}
}

func TestInstance_bkSql(t *testing.T) {
	mock.Init()

	start := time.UnixMilli(1718189940000)
	end := time.UnixMilli(1718193555000)

	testCases := []struct {
		name  string
		start time.Time
		end   time.Time
		query *metadata.Query

		expected string
	}{
		{
			name: "namespace in and aggregate count",
			query: &metadata.Query{
				DB:    "132_lol_new_login_queue_login_1min",
				Field: "login_rate",
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"namespace"},
						Window:     time.Second * 15,
					},
				},
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"bgp2-new", "gz100"},
						},
					},
				},
			},
			expected: "SELECT `namespace`, COUNT(`login_rate`) AS `_value_`, MAX(FLOOR((dtEventTimeStamp + 0) / 15000) * 15000 - 0) AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' AND `namespace` IN ('bgp2-new', 'gz100') GROUP BY `namespace`, (FLOOR((dtEventTimeStamp + 0) / 15000) * 15000 - 0)",
		},
		{
			name: "conditions with or",
			query: &metadata.Query{
				DB:    "132_lol_new_login_queue_login_1min",
				Field: "login_rate",
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
						},
					},
				},
			},
			expected: "SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' AND (`namespace` NOT LIKE '%test%' AND `namespace` NOT LIKE '%test2%') AND `namespace` NOT IN ('test', 'test2') AND `namespace` NOT LIKE '%test%' AND `namespace` != 'test'",
		},
		{
			name: "conditions with or and",
			query: &metadata.Query{
				DB:          "132_lol_new_login_queue_login_1min",
				Measurement: sql_expr.Doris,
				Field:       "login_rate",
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"*test*", "*test2*"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test", "test2"},
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"*test*"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"test"},
						},
						{
							DimensionName: "text",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"*test*", "*test2*"},
							IsWildcard:    true,
						},
						{
							DimensionName: "text",
							Operator:      metadata.ConditionNotContains,
							Value:         []string{"test", "test2"},
						},
						{
							DimensionName: "text",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"*test*"},
							IsWildcard:    true,
						},
						{
							DimensionName: "text",
							Operator:      metadata.ConditionNotContains,
							Value:         []string{"test"},
						},
					},
				},
			},
			expected: "SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` <= 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' AND (`namespace` NOT LIKE '%test%' AND `namespace` NOT LIKE '%test2%') AND `namespace` NOT IN ('test', 'test2') AND `namespace` NOT LIKE '%test%' AND `namespace` != 'test' AND (`text` NOT LIKE '%test%' AND `text` NOT LIKE '%test2%') AND (`text` NOT MATCH_PHRASE 'test' AND `text` NOT MATCH_PHRASE 'test2') AND `text` NOT LIKE '%test%' AND `text` NOT MATCH_PHRASE 'test'",
		},
		{
			name: "conditions with or and like",
			query: &metadata.Query{
				DB:    "132_lol_new_login_queue_login_1min",
				Field: "login_rate",
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"test", "test2"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"test", "test2"},
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"test"},
							IsWildcard:    true,
						},
						{
							DimensionName: "namespace",
							Operator:      metadata.ConditionContains,
							Value:         []string{"test"},
						},
					},
				},
			},
			expected: "SELECT *, `login_rate` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `132_lol_new_login_queue_login_1min` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' AND (`namespace` LIKE '%test%' OR `namespace` LIKE '%test2%') AND `namespace` IN ('test', 'test2') AND `namespace` LIKE '%test%' AND `namespace` = 'test'",
		},
		{
			name: "aggregate sum",
			query: &metadata.Query{
				DB:    "132_hander_opmon_avg",
				Field: "value",
				Aggregates: metadata.Aggregates{
					{
						Name: "sum",
					},
				},
			},

			expected: "SELECT SUM(`value`) AS `_value_` FROM `132_hander_opmon_avg` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612'",
		},
		{
			name: "aggregate cardinality with mysql",
			query: &metadata.Query{
				DB:          "2_bklog_bkunify_query_doris",
				Measurement: "",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "cardinality",
					},
				},
				Orders: metadata.Orders{
					{
						Name: "dtEventTimeStamp",
						Ast:  false,
					},
					{
						Name: "gseIndex",
						Ast:  false,
					},
					{
						Name: "iterationIndex",
						Ast:  false,
					},
				},
			},

			expected: "SELECT COUNT(DISTINCT `gseIndex`) AS `_value_` FROM `2_bklog_bkunify_query_doris` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612'",
		},
		{
			name: "aggregate date_histogram with mysql",
			query: &metadata.Query{
				DB:          "2_bklog_bkunify_query_doris",
				Measurement: "",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
					},
					{
						Name:   "date_histogram",
						Window: time.Minute * 5,
					},
				},
				Orders: metadata.Orders{
					{
						Name: "dtEventTimeStamp",
						Ast:  false,
					},
					{
						Name: "gseIndex",
						Ast:  false,
					},
					{
						Name: "iterationIndex",
						Ast:  false,
					},
				},
			},

			expected: "SELECT COUNT(`gseIndex`) AS `_value_`, MAX(FLOOR((dtEventTimeStamp + 0) / 300000) * 300000 - 0) AS `_timestamp_` FROM `2_bklog_bkunify_query_doris` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' GROUP BY (FLOOR((dtEventTimeStamp + 0) / 300000) * 300000 - 0)",
		},
		{
			name: "aggregate cardinality with doris",
			query: &metadata.Query{
				DB:          "2_bklog_bkunify_query_doris",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "cardinality",
					},
				},
				Orders: metadata.Orders{
					{
						Name: "dtEventTimeStamp",
						Ast:  false,
					},
					{
						Name: "gseIndex",
						Ast:  false,
					},
					{
						Name: "iterationIndex",
						Ast:  false,
					},
				},
			},

			expected: "SELECT COUNT(DISTINCT `gseIndex`) AS `_value_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` <= 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612'",
		},
		{
			name: "aggregate date_histogram with doris",
			query: &metadata.Query{
				DB:          "2_bklog_bkunify_query_doris",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
					},
					{
						Name:   "date_histogram",
						Window: time.Minute * 5,
					},
				},
				Orders: metadata.Orders{
					{
						Name: "dtEventTimeStamp",
						Ast:  false,
					},
					{
						Name: "gseIndex",
						Ast:  false,
					},
					{
						Name: "iterationIndex",
						Ast:  false,
					},
				},
			},

			expected: "SELECT COUNT(`gseIndex`) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 5 AS INT) * 5 - 0) * 60 * 1000) AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` <= 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' GROUP BY _timestamp_",
		},
		//{
		//	name: "aggregate multi function",
		//	query: &metadata.Query{
		//		DB:    "132_hander_opmon_avg",
		//		Field: "value",
		//		Aggregates: metadata.Aggregates{
		//			{
		//				Name:   "sum",
		//				Field:  "value",
		//				Window: time.Second * 15,
		//			},
		//			{
		//				Name:   "count",
		//				Field:  "other",
		//				Window: time.Hour,
		//			},
		//		},
		//	},
		//
		//	// TODO 适配多字段查询特性
		//	expected: "",
		//},
		{
			name: "query raw order ",
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "value",
				Size:        5,
				Orders: metadata.Orders{
					{
						Name: "dtEventTimeStamp",
						Ast:  false,
					},
					{
						Name: "gseIndex",
						Ast:  false,
					},
					{
						Name: "iterationIndex",
						Ast:  false,
					},
				},
			},
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` <= 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' ORDER BY `dtEventTimeStamp` DESC, `gseIndex` DESC, `iterationIndex` DESC LIMIT 5",
		},
		{
			name: "query raw",
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "value",
				Size:        5,
			},
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` <= 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' LIMIT 5",
		},
		{
			name: "query raw with order desc",
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "value",
				Orders: metadata.Orders{
					{
						Name: "_time",
						Ast:  false,
					},
				},
			},
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` <= 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' ORDER BY `_timestamp_` DESC",
		},
		{
			name: "query aggregate count and dimensions",
			query: &metadata.Query{
				DB:          "100133_ieod_logsearch4_errorlog_p",
				Measurement: "doris",
				Field:       "gseIndex",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
						Dimensions: []string{
							"ip",
						},
					},
				},
				Size: 5,
			},

			expected: "SELECT `ip`, COUNT(`gseIndex`) AS `_value_` FROM `100133_ieod_logsearch4_errorlog_p`.doris WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` <= 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' GROUP BY `ip` LIMIT 5",
		},
		{
			name:  "query aggregate count",
			start: time.Unix(1733756400, 0),
			end:   time.Unix(1733846399, 0),
			query: &metadata.Query{
				DB:    "101068_MatchFullLinkTimeConsumptionFlow_CostTime",
				Field: "matchstep_start_to_fail_0_100",
				Aggregates: metadata.Aggregates{
					{
						Name: "count",
					},
				},
			},

			expected: "SELECT COUNT(`matchstep_start_to_fail_0_100`) AS `_value_` FROM `101068_MatchFullLinkTimeConsumptionFlow_CostTime` WHERE `dtEventTimeStamp` >= 1733756400000 AND `dtEventTimeStamp` < 1733846399000 AND `dtEventTime` >= '2024-12-09 23:00:00' AND `dtEventTime` <= '2024-12-11 00:00:00' AND `thedate` >= '20241209' AND `thedate` <= '20241210'",
		},
		{
			name:  "query aggregate count with window hour",
			start: time.Unix(1733756400, 0),
			end:   time.Unix(1733846399, 0),
			query: &metadata.Query{
				DB:    "101068_MatchFullLinkTimeConsumptionFlow_CostTime",
				Field: "matchstep_start_to_fail_0_100",
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Hour,
					},
				},
			},

			expected: "SELECT COUNT(`matchstep_start_to_fail_0_100`) AS `_value_`, MAX(FLOOR((dtEventTimeStamp + 0) / 3600000) * 3600000 - 0) AS `_timestamp_` FROM `101068_MatchFullLinkTimeConsumptionFlow_CostTime` WHERE `dtEventTimeStamp` >= 1733756400000 AND `dtEventTimeStamp` < 1733846399000 AND `dtEventTime` >= '2024-12-09 23:00:00' AND `dtEventTime` <= '2024-12-11 00:00:00' AND `thedate` >= '20241209' AND `thedate` <= '20241210' GROUP BY (FLOOR((dtEventTimeStamp + 0) / 3600000) * 3600000 - 0)",
		},
		{
			name:  "query aggregate count with window hour and alias field",
			start: time.Unix(1733756400, 0),
			end:   time.Unix(1733846399, 0),
			query: &metadata.Query{
				DB:          "101068_MatchFullLinkTimeConsumptionFlow_CostTime",
				Measurement: sql_expr.Doris,
				Field:       "alias_field",
				FieldAlias: map[string]string{
					"alias_field": "origin_field",
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Window:     time.Hour,
						Dimensions: []string{"alias_field"},
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "alias_field",
							Operator:      "eq",
							Value:         []string{"123"},
						},
					},
				},
			},

			expected: "SELECT `origin_field` AS `alias_field`, COUNT(`origin_field`) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 60 AS INT) * 60 - 0) * 60 * 1000) AS `_timestamp_` FROM `101068_MatchFullLinkTimeConsumptionFlow_CostTime`.doris WHERE `dtEventTimeStamp` >= 1733756400000 AND `dtEventTimeStamp` <= 1733846399000 AND `dtEventTime` >= '2024-12-09 23:00:00' AND `dtEventTime` <= '2024-12-11 00:00:00' AND `thedate` >= '20241209' AND `thedate` <= '20241210' AND `origin_field` = '123' GROUP BY alias_field, _timestamp_",
		},
		{
			name:  "query with regexp_extract and AS alias",
			start: time.Unix(1755069858, 0),
			end:   time.Unix(1757661858, 0),
			query: &metadata.Query{
				DB:          "bklog_index_set_21692_analysis",
				Measurement: "doris",
				DataLabel:   "100915_bklog_pub_svrlog_pangusvr_lobby_analysis",
				QueryString: "buy weekly card success",
				SQL:         `SELECT DISTINCT (regexp_extract(log, 'openid:(\\d+)', 1)) AS id LIMIT 100000`,
			},
			expected: "SELECT DISTINCT regexp_extract(`log`, 'openid:(\\\\d+)', 1) AS id FROM `bklog_index_set_21692_analysis`.doris WHERE (`dtEventTimeStamp` >= 1755069858000 AND `dtEventTimeStamp` <= 1757661858000 AND `dtEventTime` >= '2025-08-13 15:24:18' AND `dtEventTime` <= '2025-09-12 15:24:19' AND `thedate` >= '20250813' AND `thedate` <= '20250912' AND (`log` = 'buy' OR `log` = 'weekly' OR `log` = 'card' OR `log` = 'success')) LIMIT 100000",
		},
		{
			name: "object field eq and aggregate with sql and union table",
			query: &metadata.Query{
				DB: "100915_bklog_pub_svrlog_pangusvr_lobby_analysis",
				DBs: []string{
					"100915_bklog_pub_svrlog_pangusvr_other_9_analysis",
					"100915_bklog_pub_svrlog_pangusvr_lobby_analysis",
				},
				Measurement: sql_expr.Doris,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "thedate",
							Operator:      metadata.ConditionExisted,
						},
					},
				},
				QueryString: "",
				SQL: `SELECT 
    path, 
    COUNT(*) AS total_count WHERE thedate>='20250923' AND thedate<='20250923' GROUP BY path LIMIT 100`,
			},
			start:    time.UnixMilli(1758607200000),
			end:      time.UnixMilli(1758610800000),
			expected: "SELECT `path`, COUNT(*) AS total_count FROM (SELECT * FROM `100915_bklog_pub_svrlog_pangusvr_lobby_analysis`.doris WHERE (`dtEventTimeStamp` >= 1758607200000 AND `dtEventTimeStamp` <= 1758610800000 AND `dtEventTime` >= '2025-09-23 14:00:00' AND `dtEventTime` <= '2025-09-23 15:00:01' AND `thedate` = '20250923' AND `thedate` IS NOT NULL) UNION ALL SELECT * FROM `100915_bklog_pub_svrlog_pangusvr_other_9_analysis`.doris WHERE (`dtEventTimeStamp` >= 1758607200000 AND `dtEventTimeStamp` <= 1758610800000 AND `dtEventTime` >= '2025-09-23 14:00:00' AND `dtEventTime` <= '2025-09-23 15:00:01' AND `thedate` = '20250923' AND `thedate` IS NOT NULL)) AS combined_data GROUP BY `path` LIMIT 100",
		},
		{
			name: "object field eq and aggregate with sql and union table",
			query: &metadata.Query{
				DB: "100915_bklog_pub_svrlog_pangusvr_lobby_analysis",
				DBs: []string{
					"100915_bklog_pub_svrlog_pangusvr_other_9_analysis",
					"100915_bklog_pub_svrlog_pangusvr_lobby_analysis",
				},
				Measurement: sql_expr.Doris,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "thedate",
							Operator:      metadata.ConditionExisted,
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"path"},
					},
				},
				Size: 100,
			},
			start:    time.UnixMilli(1758607200000),
			end:      time.UnixMilli(1758610800000),
			expected: "SELECT `path`, COUNT(*) AS `_value_` FROM (SELECT * FROM `100915_bklog_pub_svrlog_pangusvr_lobby_analysis`.doris WHERE `dtEventTimeStamp` >= 1758607200000 AND `dtEventTimeStamp` <= 1758610800000 AND `dtEventTime` >= '2025-09-23 14:00:00' AND `dtEventTime` <= '2025-09-23 15:00:01' AND `thedate` = '20250923' AND `thedate` IS NOT NULL UNION ALL SELECT * FROM `100915_bklog_pub_svrlog_pangusvr_other_9_analysis`.doris WHERE `dtEventTimeStamp` >= 1758607200000 AND `dtEventTimeStamp` <= 1758610800000 AND `dtEventTime` >= '2025-09-23 14:00:00' AND `dtEventTime` <= '2025-09-23 15:00:01' AND `thedate` = '20250923' AND `thedate` IS NOT NULL) AS combined_data GROUP BY `path` LIMIT 100",
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			if c.start.Unix() <= 0 {
				c.start = start
			}
			if c.end.Unix() <= 0 {
				c.end = end
			}

			fieldsMap := metadata.FieldsMap{
				"text": {
					FieldType: sql_expr.DorisTypeText,
				},
				"origin_field": {
					AliasName: "alias_field",
					FieldType: sql_expr.DorisTypeText,
				},
				"login_rate": {
					FieldType: sql_expr.DorisTypeDouble,
				},
				"gseIndex": {
					FieldType: sql_expr.DorisTypeInt,
				},
				"value": {
					FieldType: sql_expr.DorisTypeDouble,
				},
				"dtEventTimeStamp": {
					FieldType: sql_expr.DorisTypeBigInt,
				},
				"ip": {
					FieldType: sql_expr.DorisTypeText,
				},
				"log": {
					FieldType: sql_expr.DorisTypeText,
				},
				"path": {
					FieldType: sql_expr.DorisTypeText,
				},
				"namespace": {
					FieldType: sql_expr.DorisTypeString,
				},
				"thedate": {
					FieldType: sql_expr.DorisTypeText,
				},
				"iterationIndex": {
					FieldType: sql_expr.DorisTypeInt,
				},
				"_timestamp_": {
					FieldType: sql_expr.DorisTypeBigInt,
				},
				"bk_host_id": {
					FieldType: sql_expr.DorisTypeInt,
				},
			}

			fact := bksql.NewQueryFactory(ctx, c.query).
				WithFieldsMap(fieldsMap).
				WithRangeTime(c.start, c.end)
			sql, err := fact.SQL()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, sql)
		})
	}
}

func TestInstance_bkSql_EdgeCases(t *testing.T) {
	mock.Init()

	// 基础时间范围
	baseStart := time.UnixMilli(1718189940000)
	baseEnd := time.UnixMilli(1718193555000)

	// 跨天时间范围
	crossDayStart := time.Unix(1733756400, 0) // 2024-12-09 00:00:00
	crossDayEnd := time.Unix(1733846399, 0)   // 2024-12-09 23:59:59

	testCases := []struct {
		name     string
		start    time.Time
		end      time.Time
		query    *metadata.Query
		expected string
		err      error
	}{
		// 测试用例1: 无聚合函数的原始查询
		{
			name: "mysql raw query without aggregation",
			query: &metadata.Query{
				DB:    "test_db",
				Field: "value",
			},
			expected: "SELECT *, `value` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `test_db` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612'",
		},

		// 测试用例2: 多聚合函数组合
		{
			name: "mysql multiple aggregates",
			query: &metadata.Query{
				DB:    "metrics_db",
				Field: "temperature",
				Aggregates: metadata.Aggregates{
					{Name: "max"},
					{Name: "min"},
				},
			},
			expected: "SELECT MAX(`temperature`) AS `_value_`, MIN(`temperature`) AS `_value_` FROM `metrics_db` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612'",
		},

		// 测试用例3: 复杂条件组合
		{
			name: "mysql complex conditions",
			query: &metadata.Query{
				DB:    "security_logs",
				Field: "duration",
				AllConditions: metadata.AllConditions{
					[]metadata.ConditionField{
						{
							DimensionName: "severity",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"high", "critical"},
						},
						{
							DimensionName: "source_ip",
							Operator:      metadata.ConditionNotContains,
							Value:         []string{"127.0.0.1"},
						},
					},
				},
			},
			expected: "SELECT *, `duration` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `security_logs` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' AND `severity` IN ('high', 'critical') AND `source_ip` != '127.0.0.1'",
		},

		// 测试用例4: 多字段排序
		{
			name: "mysql multiple order fields",
			query: &metadata.Query{
				DB:    "transaction_logs",
				Field: "amount",
				Orders: metadata.Orders{
					{
						Name: "timestamp",
						Ast:  true,
					},
					{
						Name: "account_id",
					},
				},
			},
			expected: "SELECT *, `amount` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `transaction_logs` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612' ORDER BY `account_id` DESC, `timestamp` ASC",
		},

		// 测试用例5: 特殊字符转义
		{
			name: "mysql special characters in fields",
			query: &metadata.Query{
				DB:          "special_metrics",
				Measurement: "select", // 保留字作为measurement
				Field:       "*",
				Aggregates: metadata.Aggregates{
					{Name: "sum"},
				},
			},
			expected: "SELECT SUM(`*`) AS `_value_` FROM `special_metrics`.select WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612'",
		},

		// 测试用例6: 零窗口时间
		{
			name: "mysql zero window size",
			query: &metadata.Query{
				DB:    "time_series_data",
				Field: "value",
				Aggregates: metadata.Aggregates{
					{
						Name:   "avg",
						Window: 0,
					},
				},
			},
			expected: "SELECT AVG(`value`) AS `_value_` FROM `time_series_data` WHERE `dtEventTimeStamp` >= 1718189940000 AND `dtEventTimeStamp` < 1718193555000 AND `dtEventTime` >= '2024-06-12 18:59:00' AND `dtEventTime` <= '2024-06-12 19:59:16' AND `thedate` = '20240612'",
		},

		// 测试用例7: 跨多天的时间范围
		{
			name:  "mysql multi-day time range",
			start: crossDayStart,
			end:   crossDayEnd,
			query: &metadata.Query{
				DB:    "daily_metrics",
				Field: "active_users",
				Aggregates: metadata.Aggregates{
					{Name: "count"},
				},
			},
			expected: "SELECT COUNT(`active_users`) AS `_value_` FROM `daily_metrics` WHERE `dtEventTimeStamp` >= 1733756400000 AND `dtEventTimeStamp` < 1733846399000 AND `dtEventTime` >= '2024-12-09 23:00:00' AND `dtEventTime` <= '2024-12-11 00:00:00' AND `thedate` >= '20241209' AND `thedate` <= '20241210'",
		},

		// 测试用例8: 默认处理 object 字段
		{
			name: "mysql default multiple order fields",
			query: &metadata.Query{
				DB:    "transaction_logs",
				Field: "__ext.container_id",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.container_id",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"1234567890"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.container_id", "test"},
					},
				},
				Orders: metadata.Orders{
					{
						Name: "timestamp",
						Ast:  true,
					},
					{
						Name: "__ext.container_id",
					},
				},
			},
			err: fmt.Errorf("query is not support object with __ext.container_id"),
		},

		// 测试用例9: doris 处理 object 字段
		{
			name: "doris default multiple order fields",
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: sql_expr.Doris,
				Field:       "__ext.container_id",
				Size:        3,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_workload_name",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"bkm-daemonset-worker"},
						},
						{
							DimensionName: "bk_host_id",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"267730"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.io_kubernetes_workload_name", "__ext.io_kubernetes_workload_type"},
					},
				},
				Orders: metadata.Orders{
					{
						Name: "__ext.io_kubernetes_workload_name",
					},
				},
			},
			start:    time.Unix(1741334700, 0),
			end:      time.Unix(1741335000, 0),
			expected: "SELECT CAST(__ext['io_kubernetes_workload_name'] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_name`, CAST(__ext['io_kubernetes_workload_type'] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_type`, COUNT(CAST(__ext['container_id'] AS STRING)) AS `_value_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741334700000 AND `dtEventTimeStamp` <= 1741335000000 AND `dtEventTime` >= '2025-03-07 16:05:00' AND `dtEventTime` <= '2025-03-07 16:10:01' AND `thedate` = '20250307' AND CAST(__ext['io_kubernetes_workload_name'] AS STRING) = 'bkm-daemonset-worker' AND `bk_host_id` = '267730' GROUP BY __ext__bk_46__io_kubernetes_workload_name, __ext__bk_46__io_kubernetes_workload_type ORDER BY CAST(__ext['io_kubernetes_workload_name'] AS STRING) DESC LIMIT 3",
		},
		// 测试用例10: doris 处理 object 字段 + 时间聚合
		{
			name: "doris default multiple order fields and time aggregate",
			query: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: sql_expr.Doris,
				Field:       "__ext.container_id",
				Size:        3,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_workload_name",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"bkm-daemonset-worker"},
						},
						{
							DimensionName: "bk_host_id",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"267730"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.io_kubernetes_workload_name", "__ext.io_kubernetes_workload_type"},
						Window:     time.Minute,
					},
				},
				Orders: metadata.Orders{
					{
						Name: "__ext.io_kubernetes_workload_name",
					},
				},
			},
			start:    time.Unix(1741334700, 0),
			end:      time.Unix(1741335000, 0),
			expected: "SELECT CAST(__ext['io_kubernetes_workload_name'] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_name`, CAST(__ext['io_kubernetes_workload_type'] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_type`, COUNT(CAST(__ext['container_id'] AS STRING)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741334700000 AND `dtEventTimeStamp` <= 1741335000000 AND `dtEventTime` >= '2025-03-07 16:05:00' AND `dtEventTime` <= '2025-03-07 16:10:01' AND `thedate` = '20250307' AND CAST(__ext['io_kubernetes_workload_name'] AS STRING) = 'bkm-daemonset-worker' AND `bk_host_id` = '267730' GROUP BY __ext__bk_46__io_kubernetes_workload_name, __ext__bk_46__io_kubernetes_workload_type, _timestamp_ ORDER BY CAST(__ext['io_kubernetes_workload_name'] AS STRING) DESC LIMIT 3",
		},
		// 测试用例11: doris 处理 object 字段 + 时间聚合 5m
		{
			name: "doris default multiple order fields and time aggregate 5m",
			query: &metadata.Query{
				DB:            "5000140_bklog_container_log_demo_analysis",
				Measurement:   sql_expr.Doris,
				Field:         "__ext.container_id",
				Size:          3,
				AllConditions: metadata.AllConditions{},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.io_kubernetes_workload_name"},
						Window:     time.Minute * 5,
					},
				},
				Orders: metadata.Orders{
					{
						Name: "__ext.io_kubernetes_workload_name",
					},
				},
			},
			start:    time.Unix(1741334700, 0),
			end:      time.Unix(1741335000, 0),
			expected: "SELECT CAST(__ext['io_kubernetes_workload_name'] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_name`, COUNT(CAST(__ext['container_id'] AS STRING)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 5 AS INT) * 5 - 0) * 60 * 1000) AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741334700000 AND `dtEventTimeStamp` <= 1741335000000 AND `dtEventTime` >= '2025-03-07 16:05:00' AND `dtEventTime` <= '2025-03-07 16:10:01' AND `thedate` = '20250307' GROUP BY __ext__bk_46__io_kubernetes_workload_name, _timestamp_ ORDER BY CAST(__ext['io_kubernetes_workload_name'] AS STRING) DESC LIMIT 3",
		},
		// 测试用例12: doris 处理 object 字段 + 时间聚合 15s
		{
			name: "doris default multiple order fields and time aggregate 15s",
			query: &metadata.Query{
				DB:            "5000140_bklog_container_log_demo_analysis",
				Measurement:   sql_expr.Doris,
				Field:         "__ext.container_id",
				Size:          3,
				AllConditions: metadata.AllConditions{},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"__ext.io_kubernetes_workload_name"},
						Window:     time.Second * 15,
					},
				},
				Orders: metadata.Orders{
					{
						Name: "__ext.io_kubernetes_workload_name",
					},
				},
			},
			start:    time.Unix(1741334700, 0),
			end:      time.Unix(1741335000, 0),
			expected: "SELECT CAST(__ext['io_kubernetes_workload_name'] AS STRING) AS `__ext__bk_46__io_kubernetes_workload_name`, COUNT(CAST(__ext['container_id'] AS STRING)) AS `_value_`, (CAST((FLOOR(dtEventTimeStamp + 0) / 15000) AS INT) * 15000 - 0) AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1741334700000 AND `dtEventTimeStamp` <= 1741335000000 AND `dtEventTime` >= '2025-03-07 16:05:00' AND `dtEventTime` <= '2025-03-07 16:10:01' AND `thedate` = '20250307' GROUP BY __ext__bk_46__io_kubernetes_workload_name, _timestamp_ ORDER BY CAST(__ext['io_kubernetes_workload_name'] AS STRING) DESC LIMIT 3",
		},
		// 测试用例13: doris 处理多层级 object 字段
		{
			name: "doris default multiple order fields and time aggregate 1m",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "attributes.http.host",
				Size:        1,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "attributes.http.host",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{""},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"attributes.http.host"},
						Window:     time.Minute,
					},
				},
				Orders: metadata.Orders{
					{
						Name: "attributes.http.host",
					},
				},
			},
			start:    time.UnixMilli(1744880448784),
			end:      time.UnixMilli(1744884048785),
			expected: "SELECT CAST(attributes['http.host'] AS STRING) AS `attributes__bk_46__http__bk_46__host`, COUNT(CAST(attributes['http.host'] AS STRING)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1744880448784 AND `dtEventTimeStamp` <= 1744884048785 AND `dtEventTime` >= '2025-04-17 17:00:48' AND `dtEventTime` <= '2025-04-17 18:00:49' AND `thedate` = '20250417' AND CAST(attributes['http.host'] AS STRING) IS NOT NULL GROUP BY attributes__bk_46__http__bk_46__host, _timestamp_ ORDER BY CAST(attributes['http.host'] AS STRING) DESC LIMIT 1",
		},
		{
			name: "nested field eq",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "events.attributes.exception.type",
				Size:        1,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "events.attributes.exception.type",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"*errors.withMessage"},
						},
						{
							DimensionName: "events",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"{}"},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT *, CAST(events['attributes']['exception.type'] AS TEXT ARRAY) AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND ARRAY_CONTAINS(CAST(events['attributes']['exception.type'] AS TEXT ARRAY), '*errors.withMessage') == 1 AND `events` != '{}' LIMIT 1",
		},
		{
			name: "nested field existed",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "events.attributes.exception.type",
				Size:        1,
				Source:      []string{"events"},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "events.attributes.exception.type",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{""},
						},
						{
							DimensionName: "events",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"{}"},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT *, CAST(events['attributes']['exception.type'] AS TEXT ARRAY) AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND CAST(events['attributes']['exception.type'] AS TEXT ARRAY) IS NOT NULL AND `events` != '{}' LIMIT 1",
		},
		{
			name: "nested field like",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "events.attributes.exception.type",
				Size:        1,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "events.attributes.exception.type",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"*errors*"},
							IsWildcard:    true,
						},
						{
							DimensionName: "events",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"{}"},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT *, CAST(events['attributes']['exception.type'] AS TEXT ARRAY) AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND ARRAY_MATCH_ANY(x -> x LIKE '%errors%', CAST(events['attributes']['exception.type'] AS TEXT ARRAY)) AND `events` != '{}' LIMIT 1",
		},
		{
			name: "nested field regex",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "events.attributes.exception.type",
				Size:        1,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "events.attributes.exception.type",
							Operator:      metadata.ConditionRegEqual,
							Value:         []string{".*errors.*"},
							IsWildcard:    true,
						},
						{
							DimensionName: "events",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"{}"},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT *, CAST(events['attributes']['exception.type'] AS TEXT ARRAY) AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND ARRAY_MATCH_ANY(x -> x REGEXP '.*errors.*', CAST(events['attributes']['exception.type'] AS TEXT ARRAY)) AND `events` != '{}' LIMIT 1",
		},
		{
			name: "nested field gt",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "events.timestamp",
				Size:        1,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "events.timestamp",
							Operator:      metadata.ConditionGt,
							Value:         []string{"0"},
						},
						{
							DimensionName: "events",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"{}"},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT *, CAST(events['timestamp'] AS BIGINT ARRAY) AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND ARRAY_MATCH_ANY(x -> x > 0, CAST(events['timestamp'] AS BIGINT ARRAY)) AND `events` != '{}' LIMIT 1",
		},
		{
			name: "nested field eq multi values and aggregate",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "events.attributes.exception.type",
				Size:        1,
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute,
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "events.attributes.exception.type",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"*errors.withMessage", "info"},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT COUNT(CAST(events['attributes']['exception.type'] AS TEXT ARRAY)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND (ARRAY_CONTAINS(CAST(events['attributes']['exception.type'] AS TEXT ARRAY), '*errors.withMessage') == 1 OR ARRAY_CONTAINS(CAST(events['attributes']['exception.type'] AS TEXT ARRAY), 'info') == 1) GROUP BY _timestamp_ LIMIT 1",
		},
		{
			name: "nested field eq and aggregate",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "events.attributes.exception.type",
				Size:        1,
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute,
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "events.attributes.exception.type",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"*errors.withMessage"},
						},
						{
							DimensionName: "events",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{"{}"},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT COUNT(CAST(events['attributes']['exception.type'] AS TEXT ARRAY)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND ARRAY_CONTAINS(CAST(events['attributes']['exception.type'] AS TEXT ARRAY), '*errors.withMessage') == 1 AND `events` != '{}' GROUP BY _timestamp_ LIMIT 1",
		},
		{
			name: "nested field eq and aggregate",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "resource.bk.instance.id",
				Size:        1,
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute,
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "resource.bk.instance.id",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{""},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT COUNT(CAST(resource['bk.instance.id'] AS STRING)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND CAST(resource['bk.instance.id'] AS STRING) IS NOT NULL GROUP BY _timestamp_ LIMIT 1",
		},
		{
			name: "object field eq and aggregate",
			query: &metadata.Query{
				DB:          "2_bkapm_trace_bkop_doris",
				Measurement: sql_expr.Doris,
				Field:       "extra.queueDuration",
				Size:        1,
				Aggregates: metadata.Aggregates{
					{
						Name:   "count",
						Window: time.Minute,
					},
				},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "extra.queueDuration",
							Operator:      metadata.ConditionNotEqual,
							Value:         []string{""},
						},
					},
				},
			},
			start:    time.UnixMilli(1750953836000),
			end:      time.UnixMilli(1750953838000),
			expected: "SELECT COUNT(CAST(extra['queueDuration'] AS INT)) AS `_value_`, ((CAST((FLOOR(__shard_key__ / 1000) + 0) / 1 AS INT) * 1 - 0) * 60 * 1000) AS `_timestamp_` FROM `2_bkapm_trace_bkop_doris`.doris WHERE `dtEventTimeStamp` >= 1750953836000 AND `dtEventTimeStamp` <= 1750953838000 AND `dtEventTime` >= '2025-06-27 00:03:56' AND `dtEventTime` <= '2025-06-27 00:03:59' AND `thedate` = '20250627' AND CAST(extra['queueDuration'] AS INT) IS NOT NULL GROUP BY _timestamp_ LIMIT 1",
		},
		{
			name: "object field eq and aggregate with sql - 1",
			query: &metadata.Query{
				DB:          "100968_bklog_proz_ds_analysis",
				Measurement: sql_expr.Doris,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "log",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"MetricsOnRPCSendBunch a big bunch happen"},
						},
					},
				},
				FieldAlias: map[string]string{
					"pod_namespace": "__ext.pod.namespace",
				},
				QueryString: "test",
				SQL: `SELECT
  pod_namespace as ns,
  split_part (log, '|', 3) as ct,
  count(*)
WHERE
 log MATCH_ALL 'Reliable RPC called out of limit'
group by
  ns,
  ct
LIMIT
  1000`,
			},
			start:    time.UnixMilli(1751958582292),
			end:      time.UnixMilli(1752563382292),
			expected: "SELECT CAST(__ext['pod']['namespace'] AS STRING) AS ns, split_part(`log`, '|', 3) AS ct, count(*) FROM `100968_bklog_proz_ds_analysis`.doris WHERE `log` MATCH_ALL 'Reliable RPC called out of limit' AND (`dtEventTimeStamp` >= 1751958582292 AND `dtEventTimeStamp` <= 1752563382292 AND `dtEventTime` >= '2025-07-08 15:09:42' AND `dtEventTime` <= '2025-07-15 15:09:43' AND `thedate` >= '20250708' AND `thedate` <= '20250715' AND (`log` = 'test') AND `log` = 'MetricsOnRPCSendBunch a big bunch happen') GROUP BY ns, ct LIMIT 1000",
		},
		{
			name: "object field eq and aggregate with sql - 2",
			query: &metadata.Query{
				DB:          "100968_bklog_proz_ds_analysis",
				Measurement: sql_expr.Doris,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "log",
							Operator:      metadata.ConditionEqual,
							Value:         []string{"MetricsOnRPCSendBunch a big bunch happen"},
						},
					},
				},
				FieldAlias: map[string]string{
					"pod_namespace": "__ext.pod.namespace",
				},
				QueryString: "test",
				SQL: `SELECT
  serverIp,
  events.attributes.exception.type AS et,
  COUNT(*) AS log_count
WHERE
  log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal'
GROUP BY
  serverIp, et
LIMIT
  1000`,
			},
			start:    time.UnixMilli(1751958582292),
			end:      time.UnixMilli(1752563382292),
			expected: "SELECT `serverIp`, CAST(events['attributes']['exception.type'] AS TEXT ARRAY) AS et, COUNT(*) AS log_count FROM `100968_bklog_proz_ds_analysis`.doris WHERE `log` MATCH_PHRASE 'Error' OR `log` MATCH_PHRASE 'Fatal' AND (`dtEventTimeStamp` >= 1751958582292 AND `dtEventTimeStamp` <= 1752563382292 AND `dtEventTime` >= '2025-07-08 15:09:42' AND `dtEventTime` <= '2025-07-15 15:09:43' AND `thedate` >= '20250708' AND `thedate` <= '20250715' AND (`log` = 'test') AND `log` = 'MetricsOnRPCSendBunch a big bunch happen') GROUP BY `serverIp`, et LIMIT 1000",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())

			// 设置默认时间范围
			start := tc.start
			if start.IsZero() {
				start = baseStart
			}
			end := tc.end
			if end.IsZero() {
				end = baseEnd
			}

			// SQL生成验证
			fact := bksql.NewQueryFactory(ctx, tc.query).WithFieldsMap(metadata.FieldsMap{
				"log":                               {FieldType: sql_expr.DorisTypeText},
				"text":                              {FieldType: sql_expr.DorisTypeText},
				"events.attributes.exception.type":  {FieldType: fmt.Sprintf(sql_expr.DorisTypeArray, sql_expr.DorisTypeText)},
				"events.timestamp":                  {FieldType: fmt.Sprintf(sql_expr.DorisTypeArray, sql_expr.DorisTypeBigInt)},
				"extra.queueDuration":               {FieldType: sql_expr.DorisTypeInt},
				"__ext.io_kubernetes_workload_name": {FieldType: sql_expr.DorisTypeString},
				"__ext.io_kubernetes_workload_type": {FieldType: sql_expr.DorisTypeString},
				"__ext.container_id":                {FieldType: sql_expr.DorisTypeString},
				"__ext.pod.namespace":               {FieldType: sql_expr.DorisTypeString, AliasName: "pod_namespace"},
				"resource.bk.instance.id":           {FieldType: sql_expr.DorisTypeString},
				"bk_host_id":                        {FieldType: sql_expr.DorisTypeString},
				"http.host":                         {FieldType: sql_expr.DorisTypeString},
				"attributes.http.host":              {FieldType: sql_expr.DorisTypeString},
				"attributes.exception.type":         {FieldType: fmt.Sprintf(sql_expr.DorisTypeArray, sql_expr.DorisTypeText)},
				"events":                            {FieldType: sql_expr.DorisTypeVariant},
				"serverIp":                          {FieldType: sql_expr.DorisTypeString},
			}).WithRangeTime(start, end)
			generatedSQL, err := fact.SQL()

			if tc.err != nil {
				assert.Equal(t, tc.err, err)
			} else {
				assert.Nil(t, err)
				if err == nil {
					assert.Equal(t, tc.expected, generatedSQL)

					// 验证时间条件
					if tc.start.IsZero() && tc.end.IsZero() {
						assert.Contains(t, generatedSQL, fmt.Sprintf("`dtEventTimeStamp` >= %d", baseStart.UnixMilli()))

						if tc.query.Measurement == sql_expr.Doris {
							assert.Contains(t, generatedSQL, fmt.Sprintf("`dtEventTimeStamp` <= %d", baseEnd.UnixMilli()))
						} else {
							assert.Contains(t, generatedSQL, fmt.Sprintf("`dtEventTimeStamp` < %d", baseEnd.UnixMilli()))
						}
					}
				}
			}
		})
	}
}

// 测试正常标签名查询
func TestInstance_QueryLabelNames_Normal(t *testing.T) {
	// 初始化测试实例
	ctx := metadata.InitHashID(context.Background())
	instance := createTestInstance(ctx)

	end := time.Unix(1740553771, 0)
	start := time.Unix(1740551971, 0)

	// mock 查询数据
	mock.BkSQL.Set(map[string]any{
		"SHOW CREATE TABLE `5000140_bklog_container_log_demo_analysis`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":20,"external_api_call_time_mills":{"bkbase_auth_api":64,"bkbase_meta_api":6,"bkbase_apigw_api":25},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"NO","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE","Analyzed":"true"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"c1","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"c2","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":2,"query_db":27,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":9,"connect_db":56,"match_query_routing_rule":0,"check_permission":66,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":13168,"timetaken":0.161,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
		"SELECT *, `dtEventTimeStamp` AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1740551971000 AND `dtEventTimeStamp` <= 1740553771000 AND `dtEventTime` >= '2025-02-26 14:39:31' AND `dtEventTime` <= '2025-02-26 15:09:32' AND `thedate` = '20250226' LIMIT 1": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"5000140_bklog_container_log_demo_analysis":{"start":"2025022600","end":"2025022623"}},"cluster":"doris_bklog","totalRecords":1,"external_api_call_time_mills":{"bkbase_meta_api":8},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"thedate":20250226,"dteventtimestamp":1740552000000,"dteventtime":"2025-02-26 14:40:00","localtime":"2025-02-26 14:45:01","_starttime_":"2025-02-26 14:40:00","_endtime_":"2025-02-26 14:40:00","bk_host_id":5279498,"__ext":"{\"container_id\":\"101e58e9940c78a374e4ca3fe28d2360a8dd38b5b93937f7996902c203ac7812\",\"container_name\":\"ds\",\"bk_bcs_cluster_id\":\"BCS-K8S-26678\",\"io_kubernetes_pod\":\"ds-pro-z-instance-season-p-qvq6l-8fbrq\",\"container_image\":\"proz-tcr.tencentcloudcr.com/a1_proz/proz-ds@sha256:0ccc969d0614c41e9418ab81f444a26db743e82d3a2a2cc2d12e549391c5768f\",\"io_kubernetes_pod_namespace\":\"ds9204\",\"io_kubernetes_workload_type\":\"GameServer\",\"io_kubernetes_pod_uid\":\"78e5a0cf-fdec-43aa-9c64-5e58c35c949d\",\"io_kubernetes_workload_name\":\"ds-pro-z-instance-season-p-qvq6l-8fbrq\",\"labels\":{\"agones_dev_gameserver\":\"ds-pro-z-instance-season-p-qvq6l-8fbrq\",\"agones_dev_role\":\"gameserver\",\"agones_dev_safe_to_evict\":\"false\",\"component\":\"ds\",\"part_of\":\"projectz\"}}","cloudid":0,"path":"/proz/LinuxServer/ProjectZ/Saved/Logs/Stats/ObjectStat_ds-pro-z-instance-season-p-qvq6l-8fbrq-0_2025.02.26-04.25.48.368.log","gseindex":1399399185,"iterationindex":185,"log":"[2025.02.26-14.40.00:711][937]                       BTT_SetLocationWarpTarget_C 35","time":1740552000,"_timestamp_":1740552000000}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":54,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":9,"connect_db":34,"match_query_routing_rule":0,"check_permission":9,"check_query_semantic":0,"pick_valid_storage":0},"select_fields_order":["thedate","dteventtimestamp","dteventtime","localtime","_starttime_","_endtime_","bk_host_id","__ext","cloudid","path","gseindex","iterationindex","log","time","_timestamp_"],"total_record_size":4512,"timetaken":0.107,"result_schema":[{"field_type":"int","field_name":"__c0","field_alias":"thedate","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"dteventtimestamp","field_index":1},{"field_type":"string","field_name":"__c2","field_alias":"dteventtime","field_index":2},{"field_type":"string","field_name":"__c3","field_alias":"localtime","field_index":3},{"field_type":"string","field_name":"__c4","field_alias":"_starttime_","field_index":4},{"field_type":"string","field_name":"__c5","field_alias":"_endtime_","field_index":5},{"field_type":"int","field_name":"__c6","field_alias":"bk_host_id","field_index":6},{"field_type":"string","field_name":"__c7","field_alias":"__ext","field_index":7},{"field_type":"int","field_name":"__c8","field_alias":"cloudid","field_index":8},{"field_type":"string","field_name":"__c10","field_alias":"path","field_index":10},{"field_type":"long","field_name":"__c11","field_alias":"gseindex","field_index":11},{"field_type":"int","field_name":"__c12","field_alias":"iterationindex","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"log","field_index":13},{"field_type":"long","field_name":"__c14","field_alias":"time","field_index":14},{"field_type":"long","field_name":"__c15","field_alias":"_timestamp_","field_index":15}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["5000140_bklog_container_log_demo_analysis"]},"errors":null,"trace_id":"3465b590d66a21d3aae7841d36aaec3d","span_id":"34296e9388f3258a"}`,
	})

	// 测试用例
	tests := []struct {
		name string
		qry  *metadata.Query

		expectedNames []string
		expectError   bool
	}{
		{
			name: "normal-case",
			qry: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
			},
			expectedNames: []string{
				"bk_host_id", "__ext", "cloudid", "path", "gseindex", "iterationindex", "log", "time",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 执行测试
			ctx = metadata.InitHashID(ctx)
			names, err := instance.QueryLabelNames(ctx, tt.qry, start, end)

			// 验证结果
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.expectedNames, names)
			}
		})
	}
}

// 测试正常标签名查询
func TestInstance_QueryLabelValues_Normal(t *testing.T) {
	// 初始化测试实例
	ctx := metadata.InitHashID(context.Background())
	instance := createTestInstance(ctx)

	end := time.Unix(1740553771, 0)
	start := time.Unix(1740551971, 0)

	// mock 查询数据
	mock.BkSQL.Set(map[string]any{
		"SHOW CREATE TABLE `5000140_bklog_container_log_demo_analysis`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":20,"external_api_call_time_mills":{"bkbase_auth_api":64,"bkbase_meta_api":6,"bkbase_apigw_api":25},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"dimensions.pipelineName","Type":"STRING","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"bk_host_id","Type":"varchar(32)","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"NO","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE","Analyzed":"true"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"c1","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"c2","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"varchar(65533)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":2,"query_db":27,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":9,"connect_db":56,"match_query_routing_rule":0,"check_permission":66,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":13168,"timetaken":0.161,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,

		// normal-case
		"SELECT DISTINCT `bk_host_id`, `dtEventTimeStamp` AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1740551971000 AND `dtEventTimeStamp` <= 1740553771000 AND `dtEventTime` >= '2025-02-26 14:39:31' AND `dtEventTime` <= '2025-02-26 15:09:32' AND `thedate` = '20250226' LIMIT 2": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"5000140_bklog_container_log_demo_analysis":{"start":"2025022600","end":"2025022623"}},"cluster":"doris_bklog","totalRecords":2,"external_api_call_time_mills":{"bkbase_meta_api":6},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"bk_host_id":5843771,"_timestamp_":1740551971000},{"bk_host_id":4580470,"_timestamp_":1740551971001}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":204,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":6,"connect_db":39,"match_query_routing_rule":0,"check_permission":6,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["bk_host_id","_timestamp_"],"total_record_size":6952,"timetaken":0.257,"result_schema":[{"field_type":"int","field_name":"bk_host_id","field_alias":"bk_host_id","field_index":0},{"field_type":"long","field_name":"_timestamp_","field_alias":"_timestamp_","field_index":1}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["5000140_bklog_container_log_demo_analysis"]},"errors":null,"trace_id":"3592ea81c52ab826aba587d91e5054b6","span_id":"f21eca23481c778d"}`,

		// object field
		"SELECT DISTINCT CAST(dimensions['pipelineName'] AS STRING) AS `dimensions__bk_46__pipelineName`, `dtEventTimeStamp` AS `_timestamp_` FROM `5000140_bklog_container_log_demo_analysis`.doris WHERE `dtEventTimeStamp` >= 1740551971000 AND `dtEventTimeStamp` <= 1740553771000 AND `dtEventTime` >= '2025-02-26 14:39:31' AND `dtEventTime` <= '2025-02-26 15:09:32' AND `thedate` = '20250226' LIMIT 3": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"100147_apm_devops_event":{"start":"2025082900","end":"2025082923"}},"cluster":"monitor-log1","totalRecords":5,"external_api_call_time_mills":{"bkbase_meta_api":0},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"dimensions__bk_46__pipelineName":"Arashi 配置热刷","_timestamp_":1740551971000},{"dimensions__bk_46__pipelineName":"PAC-free-bird-mr-V2","_timestamp_":1740551971001},{"dimensions__bk_46__pipelineName":"[子流水线]free-bird-unittest","_timestamp_":1740551971002},{"dimensions__bk_46__pipelineName":"free-bird-unittest@MERGE_REQUEST","_timestamp_":1740551971003},{"dimensions__bk_46__pipelineName":"【Server】变更TcaplusDB","_timestamp_":1740551971004}],"bk_biz_ids":[],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":37,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":35,"match_query_routing_rule":0,"check_permission":1,"check_query_semantic":0,"pick_valid_storage":0},"select_fields_order":["dimensions__bk_46__pipelineName","_timestamp_"],"total_record_size":1856,"trino_cluster_host":"","timetaken":0.076,"result_schema":[{"field_type":"string","field_name":"dimensions__bk_46__pipelineName","field_alias":"dimensions__bk_46__pipelineName","field_index":0},{"field_type":"long","field_name":"_timestamp_","field_alias":"_timestamp_","field_index":1}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["100147_apm_devops_event"]},"errors":null,"trace_id":"606f1e90be4b2ceb050ee8e176bf1164","span_id":"b181deac3ae9807c"}`,
	})

	// 测试用例
	tests := []struct {
		name string
		qry  *metadata.Query
		key  string

		expectedNames []string
		expectError   bool
	}{
		{
			name: "normal-case",
			qry: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Size:        2,
			},
			key: "bk_host_id",
			expectedNames: []string{
				"5843771", "4580470",
			},
		},
		{
			name: "object field",
			qry: &metadata.Query{
				DB:          "5000140_bklog_container_log_demo_analysis",
				Measurement: "doris",
				Size:        3,
			},
			key: "dimensions.pipelineName",
			expectedNames: []string{
				"Arashi 配置热刷",
				"PAC-free-bird-mr-V2",
				"[子流水线]free-bird-unittest",
				"free-bird-unittest@MERGE_REQUEST",
				"【Server】变更TcaplusDB",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 执行测试
			ctx = metadata.InitHashID(ctx)
			names, err := instance.QueryLabelValues(ctx, tt.qry, tt.key, start, end)

			// 验证结果
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.ElementsMatch(t, tt.expectedNames, names)
			}
		})
	}
}

// 创建测试用Instance
func createTestInstance(ctx context.Context) *bksql.Instance {
	mock.Init()

	ins, err := bksql.NewInstance(ctx, &bksql.Options{
		Address:   mock.BkBaseUrl,
		Timeout:   time.Minute,
		MaxLimit:  1e4,
		Tolerance: 5,
		Curl:      &curl.HttpCurl{},
	})
	if err != nil {
		log.Fatalf(ctx, err.Error())
		return nil
	}
	return ins
}
