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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestQueryRawWithInstanceDirect(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	start := "1723594000"
	end := "1723595000"

	mock.Es.Set(map[string]any{
		// basic query direct test
		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10}`: `{"took":301,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c726c895a380ba1a9df04ba4a977b29b","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"f6950fef394e813999d7316cdbf0de4d","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}}]}}`,

		`{"from":0,"query":{"bool":{"filter":[{"wildcard":{"message":{"value":"test"}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}]}},"size":2}`: `{"took":301,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":1.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c726c895a380ba1a9df04ba4a977b29b","_score":1.0,"_source":{"dtEventTimeStamp":"1723594161000","message":"this is a test message","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"f6950fef394e813999d7316cdbf0de4d","_score":1.0,"_source":{"dtEventTimeStamp":"1723594161000","message":"another test log entry","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}}]}}`,

		`{"from":0,"query":{"bool":{"filter":[{"bool":{"must":[{"wildcard":{"message":{"value":"error"}}},{"wildcard":{"__ext.io_kubernetes_pod":{"value":"bk-datalink-unify-query-6df8bcc4c9-rk4sc"}}},{"wildcard":{"__ext.container_name":{"value":"unify-query"}}}]}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}},{"term":{"container_name":"unify-query"}}]}},"size":2}`: `{"took":301,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":2.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c726c895a380ba1a9df04ba4a977b29b","_score":2.0,"_source":{"dtEventTimeStamp":"1723594161000","message":"database connection error occurred","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","container_name":"unify-query","io_kubernetes_pod":"bk-datalink-unify-query-6df8bcc4c9-rk4sc"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"f6950fef394e813999d7316cdbf0de4d","_score":2.0,"_source":{"dtEventTimeStamp":"1723594161000","message":"fatal error in processing request","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","container_name":"unify-query","io_kubernetes_pod":"bk-datalink-unify-query-6df8bcc4c9-rk4sc"}}}]}}`,
	})

	mock.BkSQL.Set(map[string]any{
		"SHOW CREATE TABLE `2_bklog_bkunify_query_doris`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":18,"external_api_call_time_mills":{"bkbase_auth_api":43,"bkbase_meta_api":0,"bkbase_apigw_api":33},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":0,"query_db":5,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":45,"match_query_routing_rule":0,"check_permission":43,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":11776,"timetaken":0.096,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,

		// doris basic query
		"SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1723594000000 AND `dtEventTimeStamp` <= 1723595000000 AND `dtEventTime` >= '2024-08-14 08:06:40' AND `dtEventTime` <= '2024-08-14 08:23:21' AND `thedate` = '20240814' LIMIT 10": `{"result":true,"message":"成功","code":"00","data":{"cluster":"codev_doris2","totalRecords":3,"external_api_call_time_mills":{"bkbase_meta_api":10},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"dtEventTime":1723594100000,"__shard_key__":29077703950,"dtEventTimeStamp":1723594105000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:06:40","iterationIndex":15,"__ext":"{\"container_id\":\"abc123def456\",\"container_name\":\"app-server\",\"io_kubernetes_pod\":\"app-server-pod-123\"}","cloudId":0,"gseIndex":2450128,"path":"/var/log/application.log","time":"1723594105","log":"2024-08-14T08:06:40.500Z INFO application started","message":"application started successfully","serverip":"10.0.0.1","trace_id":"info001"},{"dtEventTime":1723594150000,"__shard_key__":29077703951,"dtEventTimeStamp":1723594158000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:06:41","iterationIndex":17,"__ext":"{\"container_id\":\"def789ghi012\",\"container_name\":\"web-server\",\"io_kubernetes_pod\":\"web-server-pod-456\"}","cloudId":0,"gseIndex":2450129,"path":"/var/log/nginx.log","time":"1723594158","log":"2024-08-14T08:06:41.800Z INFO nginx server running","message":"nginx server is running","serverip":"10.0.0.2","trace_id":"info002"},{"dtEventTime":1723594250000,"__shard_key__":29077703952,"dtEventTimeStamp":1723594252000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:06:43","iterationIndex":18,"__ext":"{\"container_id\":\"ghi345jkl678\",\"container_name\":\"database\",\"io_kubernetes_pod\":\"database-pod-789\"}","cloudId":0,"gseIndex":2450130,"path":"/var/log/mysql.log","time":"1723594252","log":"2024-08-14T08:06:43.200Z INFO database connected","message":"database connection established","serverip":"10.0.0.3","trace_id":"info003"}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":2000,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":10,"connect_db":50,"match_query_routing_rule":0,"check_permission":10,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["dtEventTime","__shard_key__","dtEventTimeStamp","thedate","localtime","iterationIndex","__ext","cloudId","gseIndex","path","time","log","message","serverip","trace_id"],"total_record_size":245000,"result_schema":[{"field_type":"long","field_name":"__c0","field_alias":"dtEventTime","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"__shard_key__","field_index":1},{"field_type":"long","field_name":"__c2","field_alias":"dtEventTimeStamp","field_index":2},{"field_type":"int","field_name":"__c3","field_alias":"thedate","field_index":3},{"field_type":"string","field_name":"__c4","field_alias":"localtime","field_index":4},{"field_type":"long","field_name":"__c5","field_alias":"iterationIndex","field_index":5},{"field_type":"string","field_name":"__c6","field_alias":"__ext","field_index":6},{"field_type":"long","field_name":"__c7","field_alias":"cloudId","field_index":7},{"field_type":"long","field_name":"__c8","field_alias":"gseIndex","field_index":8},{"field_type":"string","field_name":"__c9","field_alias":"path","field_index":9},{"field_type":"long","field_name":"__c10","field_alias":"time","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"log","field_index":11},{"field_type":"string","field_name":"__c12","field_alias":"message","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"serverip","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"trace_id","field_index":14}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["100915_bklog_pub_svrlog_pangusvr_other_other_analysis"]},"errors":null,"trace_id":"0816890bc718ec5786d469e9a79110d2","span_id":"6d04c0ddf758c603"}`,

		// doris complex conditions query
		"SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1723594000000 AND `dtEventTimeStamp` <= 1723595000000 AND `dtEventTime` >= '2024-08-14 08:06:40' AND `dtEventTime` <= '2024-08-14 08:23:21' AND `thedate` = '20240814' AND (`message` MATCH_PHRASE 'warning' OR `message` MATCH_PHRASE 'critical') AND `serverip` = '10.0.0.1' LIMIT 50": `{"result":true,"message":"成功","code":"00","data":{"cluster":"codev_doris2","totalRecords":2,"external_api_call_time_mills":{"bkbase_meta_api":10},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"dtEventTime":1723594300000,"__shard_key__":29077703955,"dtEventTimeStamp":1723594305000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:06:44","iterationIndex":21,"__ext":"{\"container_id\":\"warning123\",\"container_name\":\"monitor\",\"io_kubernetes_pod\":\"monitor-pod-111\"}","cloudId":0,"gseIndex":2.450133e+06,"path":"/var/log/monitor.log","time":"1723594305","log":"2024-08-14T08:06:44.500Z WARNING high memory usage detected","message":"high memory usage detected on server","serverip":"10.0.0.1","trace_id":"warn001"},{"dtEventTime":1723594350000,"__shard_key__":29077703956,"dtEventTimeStamp":1723594358000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:06:46","iterationIndex":22,"__ext":"{\"container_id\":\"critical456\",\"container_name\":\"alert-system\",\"io_kubernetes_pod\":\"alert-pod-222\"}","cloudId":0,"gseIndex":2.450134e+06,"path":"/var/log/alert.log","time":"1723594358","log":"2024-08-14T08:06:46.800Z CRITICAL service unavailable","message":"critical service failure detected","serverip":"10.0.0.1","trace_id":"crit001"}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":2500,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":12,"connect_db":55,"match_query_routing_rule":0,"check_permission":12,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["dtEventTime","__shard_key__","dtEventTimeStamp","thedate","localtime","iterationIndex","__ext","cloudId","gseIndex","path","time","log","message","serverip","trace_id"],"total_record_size":265000,"result_schema":[{"field_type":"long","field_name":"__c0","field_alias":"dtEventTime","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"__shard_key__","field_index":1},{"field_type":"long","field_name":"__c2","field_alias":"dtEventTimeStamp","field_index":2},{"field_type":"int","field_name":"__c3","field_alias":"thedate","field_index":3},{"field_type":"string","field_name":"__c4","field_alias":"localtime","field_index":4},{"field_type":"long","field_name":"__c5","field_alias":"iterationIndex","field_index":5},{"field_type":"string","field_name":"__c6","field_alias":"__ext","field_index":6},{"field_type":"long","field_name":"__c7","field_alias":"cloudId","field_index":7},{"field_type":"long","field_name":"__c8","field_alias":"gseIndex","field_index":8},{"field_type":"string","field_name":"__c9","field_alias":"path","field_index":9},{"field_type":"long","field_name":"__c10","field_alias":"time","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"log","field_index":11},{"field_type":"string","field_name":"__c12","field_alias":"message","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"serverip","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"trace_id","field_index":14}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["100915_bklog_pub_svrlog_pangusvr_other_other_analysis"]},"errors":null,"trace_id":"0816890bc718ec5786d469e9a79110d2","span_id":"6d04c0ddf758c603"}`,

		// doris highlight test query
		"SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1723594000000 AND `dtEventTimeStamp` <= 1723595000000 AND `dtEventTime` >= '2024-08-14 08:06:40' AND `dtEventTime` <= '2024-08-14 08:23:21' AND `thedate` = '20240814' AND `message` MATCH_PHRASE 'error' AND `log` MATCH_PHRASE 'error' LIMIT 100": `{"result":true,"message":"成功","code":"00","data":{"cluster":"codev_doris2","totalRecords":2,"external_api_call_time_mills":{"bkbase_meta_api":10},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"dtEventTime":1723594160000,"__shard_key__":29077703953,"dtEventTimeStamp":1723594161000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:06:41","iterationIndex":19,"__ext":"{\"container_id\":\"375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2\",\"container_name\":\"unify-query\",\"io_kubernetes_pod\":\"bk-datalink-unify-query-6df8bcc4c9-rk4sc\"}","cloudId":0,"gseIndex":2450131,"path":"/var/log/app.log","time":"1723594161","log":"2024-08-14T08:06:41.000Z error database connection failed","message":"database connection error occurred","serverip":"127.0.0.1","trace_id":"abc123"},{"dtEventTime":1723594200000,"__shard_key__":29077703954,"dtEventTimeStamp":1723594201000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:06:42","iterationIndex":20,"__ext":"{\"container_id\":\"375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2\",\"container_name\":\"unify-query\",\"io_kubernetes_pod\":\"bk-datalink-unify-query-6df8bcc4c9-rk4sc\"}","cloudId":0,"gseIndex":2450132,"path":"/var/log/app.log","time":"1723594202","log":"2024-08-14T08:06:42.000Z error processing request timeout","message":"fatal error in processing request","serverip":"127.0.0.1","trace_id":"def456"}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":3049,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":11,"connect_db":66,"match_query_routing_rule":0,"check_permission":11,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["dtEventTime","__shard_key__","dtEventTimeStamp","thedate","localtime","iterationIndex","__ext","cloudId","gseIndex","path","time","log","message","serverip","trace_id"],"total_record_size":327384,"result_schema":[{"field_type":"long","field_name":"__c0","field_alias":"dtEventTime","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"__shard_key__","field_index":1},{"field_type":"long","field_name":"__c2","field_alias":"dtEventTimeStamp","field_index":2},{"field_type":"int","field_name":"__c3","field_alias":"thedate","field_index":3},{"field_type":"string","field_name":"__c4","field_alias":"localtime","field_index":4},{"field_type":"long","field_name":"__c5","field_alias":"iterationIndex","field_index":5},{"field_type":"string","field_name":"__c6","field_alias":"__ext","field_index":6},{"field_type":"long","field_name":"__c7","field_alias":"cloudId","field_index":7},{"field_type":"long","field_name":"__c8","field_alias":"gseIndex","field_index":8},{"field_type":"string","field_name":"__c9","field_alias":"path","field_index":9},{"field_type":"long","field_name":"__c10","field_alias":"time","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"log","field_index":11},{"field_type":"string","field_name":"__c12","field_alias":"message","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"serverip","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"trace_id","field_index":14}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["100915_bklog_pub_svrlog_pangusvr_other_other_analysis"]},"errors":null,"trace_id":"0816890bc718ec5786d469e9a79110d2","span_id":"6d04c0ddf758c603"}`,

		// doris time range specific query
		"SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1723594500000 AND `dtEventTimeStamp` <= 1723594800000 AND `dtEventTime` >= '2024-08-14 08:08:00' AND `dtEventTime` <= '2024-08-14 08:08:30' AND `thedate` = '20240814' LIMIT 5": `{"result":true,"message":"成功","code":"00","data":{"cluster":"codev_doris2","totalRecords":1,"external_api_call_time_mills":{"bkbase_meta_api":10},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"dtEventTime":1723594680000,"__shard_key__":29077703957,"dtEventTimeStamp":1723594685000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:08:05","iterationIndex":23,"__ext":"{\"container_id\":\"late789\",\"container_name\":\"batch-job\",\"io_kubernetes_pod\":\"batch-job-pod-333\"}","cloudId":0,"gseIndex":2450135,"path":"/var/log/batch.log","time":"1723594685","log":"2024-08-14T08:08:05.500Z INFO batch processing completed","message":"batch job completed successfully","serverip":"10.0.0.5","trace_id":"batch001"}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":1800,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":9,"connect_db":45,"match_query_routing_rule":0,"check_permission":9,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["dtEventTime","__shard_key__","dtEventTimeStamp","thedate","localtime","iterationIndex","__ext","cloudId","gseIndex","path","time","log","message","serverip","trace_id"],"total_record_size":185000,"result_schema":[{"field_type":"long","field_name":"__c0","field_alias":"dtEventTime","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"__shard_key__","field_index":1},{"field_type":"long","field_name":"__c2","field_alias":"dtEventTimeStamp","field_index":2},{"field_type":"int","field_name":"__c3","field_alias":"thedate","field_index":3},{"field_type":"string","field_name":"__c4","field_alias":"localtime","field_index":4},{"field_type":"long","field_name":"__c5","field_alias":"iterationIndex","field_index":5},{"field_type":"string","field_name":"__c6","field_alias":"__ext","field_index":6},{"field_type":"long","field_name":"__c7","field_alias":"cloudId","field_index":7},{"field_type":"long","field_name":"__c8","field_alias":"gseIndex","field_index":8},{"field_type":"string","field_name":"__c9","field_alias":"path","field_index":9},{"field_type":"long","field_name":"__c10","field_alias":"time","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"log","field_index":11},{"field_type":"string","field_name":"__c12","field_alias":"message","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"serverip","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"trace_id","field_index":14}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["100915_bklog_pub_svrlog_pangusvr_other_other_analysis"]},"errors":null,"trace_id":"0816890bc718ec5786d469e9a79110d2","span_id":"6d04c0ddf758c603"}`,

		// doris time range specific query (actual query generated by test)
		"SELECT *, `dtEventTimeStamp` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1723594500000 AND `dtEventTimeStamp` <= 1723594800000 AND `dtEventTime` >= '2024-08-14 08:15:00' AND `dtEventTime` <= '2024-08-14 08:20:01' AND `thedate` = '20240814' LIMIT 5": `{"result":true,"message":"成功","code":"00","data":{"cluster":"codev_doris2","totalRecords":1,"external_api_call_time_mills":{"bkbase_meta_api":10},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"dtEventTime":1723594680000,"__shard_key__":29077703957,"dtEventTimeStamp":1723594685000,"thedate":2.0240814e+07,"localtime":"2024-08-14 08:08:05","iterationIndex":23,"__ext":"{\"container_id\":\"late789\",\"container_name\":\"batch-job\",\"io_kubernetes_pod\":\"batch-job-pod-333\"}","cloudId":0,"gseIndex":2450135,"path":"/var/log/batch.log","time":"1723594685","log":"2024-08-14T08:08:05.500Z INFO batch processing completed","message":"batch job completed successfully","serverip":"10.0.0.5","trace_id":"batch001"}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":1800,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":9,"connect_db":45,"match_query_routing_rule":0,"check_permission":9,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["dtEventTime","__shard_key__","dtEventTimeStamp","thedate","localtime","iterationIndex","__ext","cloudId","gseIndex","path","time","log","message","serverip","trace_id"],"total_record_size":185000,"result_schema":[{"field_type":"long","field_name":"__c0","field_alias":"dtEventTime","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"__shard_key__","field_index":1},{"field_type":"long","field_name":"__c2","field_alias":"dtEventTimeStamp","field_index":2},{"field_type":"int","field_name":"__c3","field_alias":"thedate","field_index":3},{"field_type":"string","field_name":"__c4","field_alias":"localtime","field_index":4},{"field_type":"long","field_name":"__c5","field_alias":"iterationIndex","field_index":5},{"field_type":"string","field_name":"__c6","field_alias":"__ext","field_index":6},{"field_type":"long","field_name":"__c7","field_alias":"cloudId","field_index":7},{"field_type":"long","field_name":"__c8","field_alias":"gseIndex","field_index":8},{"field_type":"string","field_name":"__c9","field_alias":"path","field_index":9},{"field_type":"long","field_name":"__c10","field_alias":"time","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"log","field_index":11},{"field_type":"string","field_name":"__c12","field_alias":"message","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"serverip","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"trace_id","field_index":14}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["100915_bklog_pub_svrlog_pangusvr_other_other_analysis"]},"errors":null,"trace_id":"0816890bc718ec5786d469e9a79110d2","span_id":"6d04c0ddf758c603"}`,
	})

	tests := map[string]struct {
		queryDirect *structured.QueryDirect
		total       int64
		expected    string
		options     string
	}{
		"basic query direct test": {
			queryDirect: &structured.QueryDirect{
				SpaceUid: spaceUid,
				References: metadata.QueryReference{
					"a": []*metadata.QueryMetric{
						{
							ReferenceName: "a",
							MetricName:    "dtEventTimeStamp",
							QueryList: []*metadata.Query{
								{
									DataSource:  "bkdata",
									TableID:     "result_table.bk_base_es",
									StorageType: "elasticsearch",
									StorageID:   "0",
									SourceType:  structured.BkData,
									ClusterName: "",
									DB:          "es_index",
									Measurement: "",
									Field:       "dtEventTimeStamp",
								},
							},
						},
					},
				},
				Start: start,
				End:   end,
				Limit: 10,
				From:  0,
			},
			total:    10000,
			expected: `[{"__data_label":"","__doc_id":"c726c895a380ba1a9df04ba4a977b29b","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"","__doc_id":"f6950fef394e813999d7316cdbf0de4d","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"}]`,
			options:  `{"result_table.bk_base_es|0":{"from":0}}`,
		},
		"query with highlight enabled": {
			queryDirect: &structured.QueryDirect{
				SpaceUid: spaceUid,
				References: metadata.QueryReference{
					"a": []*metadata.QueryMetric{
						{
							ReferenceName: "a",
							MetricName:    "dtEventTimeStamp",
							QueryList: []*metadata.Query{
								{
									DataSource:  "bkdata",
									TableID:     "result_table.bk_base_es",
									StorageType: "elasticsearch",
									StorageID:   "0",
									SourceType:  structured.BkData,
									ClusterName: "",
									DB:          "es_index",
									Measurement: "",
									Field:       "dtEventTimeStamp",
									AllConditions: metadata.AllConditions{
										{
											{
												DimensionName: "message",
												Value:         []string{"test"},
												Operator:      metadata.ConditionContains,
											},
										},
									},
								},
							},
						},
					},
				},
				Start: start,
				End:   end,
				Limit: 2,
				HighLight: &metadata.HighLight{
					Enable:            true,
					MaxAnalyzedOffset: 1000,
				},
			},
			total:    10000,
			expected: `[{"__data_label":"","__doc_id":"c726c895a380ba1a9df04ba4a977b29b","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__highlight":{"message":["this is a <mark>test</mark> message"]},"__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000","message":"this is a test message"},{"__data_label":"","__doc_id":"f6950fef394e813999d7316cdbf0de4d","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__highlight":{"message":["another <mark>test</mark> log entry"]},"__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000","message":"another test log entry"}]`,
			options:  `{"result_table.bk_base_es|0":{"from":0}}`,
		},
		"nested query with query string and highlight enabled": {
			queryDirect: &structured.QueryDirect{
				SpaceUid: spaceUid,
				References: metadata.QueryReference{
					"a": []*metadata.QueryMetric{
						{
							ReferenceName: "a",
							MetricName:    "dtEventTimeStamp",
							QueryList: []*metadata.Query{
								{
									DataSource:  "bkdata",
									TableID:     "result_table.bk_base_es",
									StorageType: "elasticsearch",
									StorageID:   "0",
									SourceType:  structured.BkData,
									ClusterName: "",
									DB:          "es_index",
									Measurement: "",
									Field:       "dtEventTimeStamp",
									QueryString: "container_name:unify-query",
									AllConditions: metadata.AllConditions{
										{
											{
												DimensionName: "message",
												Value:         []string{"error"},
												Operator:      metadata.ConditionContains,
											},
											{
												DimensionName: "__ext.io_kubernetes_pod",
												Value:         []string{"bk-datalink-unify-query-6df8bcc4c9-rk4sc"},
												Operator:      metadata.ConditionContains,
											},
											{
												DimensionName: "__ext.container_name",
												Value:         []string{"unify-query"},
												Operator:      metadata.ConditionContains,
											},
										},
									},
								},
							},
						},
					},
				},
				Start: start,
				End:   end,
				Limit: 2,
				HighLight: &metadata.HighLight{
					Enable:            true,
					MaxAnalyzedOffset: 1000,
				},
			},
			total:    10000,
			expected: `[{"__data_label":"","__doc_id":"c726c895a380ba1a9df04ba4a977b29b","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bk-datalink-unify-query-6df8bcc4c9-rk4sc","__highlight":{"__ext.container_name":["<mark>unify-query</mark>"],"__ext.io_kubernetes_pod":["<mark>bk-datalink-unify-query-6df8bcc4c9-rk4sc</mark>"],"message":["database connection <mark>error</mark> occurred"]},"__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000","message":"database connection error occurred"},{"__data_label":"","__doc_id":"f6950fef394e813999d7316cdbf0de4d","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bk-datalink-unify-query-6df8bcc4c9-rk4sc","__highlight":{"__ext.container_name":["<mark>unify-query</mark>"],"__ext.io_kubernetes_pod":["<mark>bk-datalink-unify-query-6df8bcc4c9-rk4sc</mark>"],"message":["fatal <mark>error</mark> in processing request"]},"__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000","message":"fatal error in processing request"}]`,
			options:  `{"result_table.bk_base_es|0":{"from":0}}`,
		},
		"doris basic query": {
			queryDirect: &structured.QueryDirect{
				SpaceUid: spaceUid,
				References: metadata.QueryReference{
					"a": []*metadata.QueryMetric{
						{
							ReferenceName: "a",
							MetricName:    "dtEventTimeStamp",
							QueryList: []*metadata.Query{
								{
									DataSource:  structured.BkLog,
									TableID:     "result_table.doris",
									StorageType: "bk_sql",
									StorageID:   "4",
									SourceType:  structured.BkLog,
									ClusterName: "",
									DB:          "2_bklog_bkunify_query_doris",
									Measurement: "doris",
									Field:       "dtEventTimeStamp",
								},
							},
						},
					},
				},
				Start: start,
				End:   end,
				Limit: 10,
			},
			total:    3,
			expected: `[{"__data_label":"","__ext.container_id":"abc123def456","__ext.container_name":"app-server","__ext.io_kubernetes_pod":"app-server-pod-123","__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.907770395e+10,"cloudId":0,"dtEventTime":1.7235941e+12,"dtEventTimeStamp":1.723594105e+12,"gseIndex":2.450128e+06,"iterationIndex":15,"localtime":"2024-08-14 08:06:40","log":"2024-08-14T08:06:40.500Z INFO application started","message":"application started successfully","path":"/var/log/application.log","serverip":"10.0.0.1","thedate":2.0240814e+07,"time":"1723594105","trace_id":"info001"},{"__data_label":"","__ext.container_id":"def789ghi012","__ext.container_name":"web-server","__ext.io_kubernetes_pod":"web-server-pod-456","__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.9077703951e+10,"cloudId":0,"dtEventTime":1.72359415e+12,"dtEventTimeStamp":1.723594158e+12,"gseIndex":2.450129e+06,"iterationIndex":17,"localtime":"2024-08-14 08:06:41","log":"2024-08-14T08:06:41.800Z INFO nginx server running","message":"nginx server is running","path":"/var/log/nginx.log","serverip":"10.0.0.2","thedate":2.0240814e+07,"time":"1723594158","trace_id":"info002"},{"__data_label":"","__ext.container_id":"ghi345jkl678","__ext.container_name":"database","__ext.io_kubernetes_pod":"database-pod-789","__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.9077703952e+10,"cloudId":0,"dtEventTime":1.72359425e+12,"dtEventTimeStamp":1.723594252e+12,"gseIndex":2.45013e+06,"iterationIndex":18,"localtime":"2024-08-14 08:06:43","log":"2024-08-14T08:06:43.200Z INFO database connected","message":"database connection established","path":"/var/log/mysql.log","serverip":"10.0.0.3","thedate":2.0240814e+07,"time":"1723594252","trace_id":"info003"}]`,
			options:  `{"result_table.doris|4":{"result_schema":[{"field_alias":"dtEventTime","field_index":0,"field_name":"__c0","field_type":"long"},{"field_alias":"__shard_key__","field_index":1,"field_name":"__c1","field_type":"long"},{"field_alias":"dtEventTimeStamp","field_index":2,"field_name":"__c2","field_type":"long"},{"field_alias":"thedate","field_index":3,"field_name":"__c3","field_type":"int"},{"field_alias":"localtime","field_index":4,"field_name":"__c4","field_type":"string"},{"field_alias":"iterationIndex","field_index":5,"field_name":"__c5","field_type":"long"},{"field_alias":"__ext","field_index":6,"field_name":"__c6","field_type":"string"},{"field_alias":"cloudId","field_index":7,"field_name":"__c7","field_type":"long"},{"field_alias":"gseIndex","field_index":8,"field_name":"__c8","field_type":"long"},{"field_alias":"path","field_index":9,"field_name":"__c9","field_type":"string"},{"field_alias":"time","field_index":10,"field_name":"__c10","field_type":"long"},{"field_alias":"log","field_index":11,"field_name":"__c11","field_type":"string"},{"field_alias":"message","field_index":12,"field_name":"__c12","field_type":"string"},{"field_alias":"serverip","field_index":13,"field_name":"__c13","field_type":"string"},{"field_alias":"trace_id","field_index":14,"field_name":"__c14","field_type":"string"}]}}`,
		},
		"doris complex conditions query": {
			queryDirect: &structured.QueryDirect{
				SpaceUid: spaceUid,
				References: metadata.QueryReference{
					"a": []*metadata.QueryMetric{
						{
							ReferenceName: "a",
							MetricName:    "dtEventTimeStamp",
							QueryList: []*metadata.Query{
								{
									DataSource:  structured.BkLog,
									TableID:     "result_table.doris",
									StorageType: "bk_sql",
									StorageID:   "4",
									SourceType:  structured.BkLog,
									ClusterName: "",
									DB:          "2_bklog_bkunify_query_doris",
									Measurement: "doris",
									Field:       "dtEventTimeStamp",
									AllConditions: metadata.AllConditions{
										{
											{
												DimensionName: "message",
												Value:         []string{"warning", "critical"},
												Operator:      metadata.ConditionContains,
											},
											{
												DimensionName: "serverip",
												Value:         []string{"10.0.0.1"},
												Operator:      metadata.ConditionEqual,
											},
										},
									},
								},
							},
						},
					},
				},
				Start: start,
				End:   end,
				Limit: 50,
			},
			total:    2,
			expected: `[{"__data_label":"","__ext.container_id":"warning123","__ext.container_name":"monitor","__ext.io_kubernetes_pod":"monitor-pod-111","__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.9077703955e+10,"cloudId":0,"dtEventTime":1.7235943e+12,"dtEventTimeStamp":1.723594305e+12,"gseIndex":2.450133e+06,"iterationIndex":21,"localtime":"2024-08-14 08:06:44","log":"2024-08-14T08:06:44.500Z WARNING high memory usage detected","message":"high memory usage detected on server","path":"/var/log/monitor.log","serverip":"10.0.0.1","thedate":2.0240814e+07,"time":"1723594305","trace_id":"warn001"},{"__data_label":"","__ext.container_id":"critical456","__ext.container_name":"alert-system","__ext.io_kubernetes_pod":"alert-pod-222","__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.9077703956e+10,"cloudId":0,"dtEventTime":1.72359435e+12,"dtEventTimeStamp":1.723594358e+12,"gseIndex":2.450134e+06,"iterationIndex":22,"localtime":"2024-08-14 08:06:46","log":"2024-08-14T08:06:46.800Z CRITICAL service unavailable","message":"critical service failure detected","path":"/var/log/alert.log","serverip":"10.0.0.1","thedate":2.0240814e+07,"time":"1723594358","trace_id":"crit001"}]`,
			options:  `{"result_table.doris|4":{"result_schema":[{"field_alias":"dtEventTime","field_index":0,"field_name":"__c0","field_type":"long"},{"field_alias":"__shard_key__","field_index":1,"field_name":"__c1","field_type":"long"},{"field_alias":"dtEventTimeStamp","field_index":2,"field_name":"__c2","field_type":"long"},{"field_alias":"thedate","field_index":3,"field_name":"__c3","field_type":"int"},{"field_alias":"localtime","field_index":4,"field_name":"__c4","field_type":"string"},{"field_alias":"iterationIndex","field_index":5,"field_name":"__c5","field_type":"long"},{"field_alias":"__ext","field_index":6,"field_name":"__c6","field_type":"string"},{"field_alias":"cloudId","field_index":7,"field_name":"__c7","field_type":"long"},{"field_alias":"gseIndex","field_index":8,"field_name":"__c8","field_type":"long"},{"field_alias":"path","field_index":9,"field_name":"__c9","field_type":"string"},{"field_alias":"time","field_index":10,"field_name":"__c10","field_type":"long"},{"field_alias":"log","field_index":11,"field_name":"__c11","field_type":"string"},{"field_alias":"message","field_index":12,"field_name":"__c12","field_type":"string"},{"field_alias":"serverip","field_index":13,"field_name":"__c13","field_type":"string"},{"field_alias":"trace_id","field_index":14,"field_name":"__c14","field_type":"string"}]}}`,
		},
		"doris time range specific query": {
			queryDirect: &structured.QueryDirect{
				SpaceUid: spaceUid,
				References: metadata.QueryReference{
					"a": []*metadata.QueryMetric{
						{
							ReferenceName: "a",
							MetricName:    "dtEventTimeStamp",
							QueryList: []*metadata.Query{
								{
									DataSource:  structured.BkLog,
									TableID:     "result_table.doris",
									StorageType: "bk_sql",
									StorageID:   "4",
									SourceType:  structured.BkLog,
									ClusterName: "",
									DB:          "2_bklog_bkunify_query_doris",
									Measurement: "doris",
									Field:       "dtEventTimeStamp",
								},
							},
						},
					},
				},
				Start: "1723594500",
				End:   "1723594800",
				Limit: 5,
			},
			total:    1,
			expected: `[{"__data_label":"","__ext.container_id":"late789","__ext.container_name":"batch-job","__ext.io_kubernetes_pod":"batch-job-pod-333","__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.9077703957e+10,"cloudId":0,"dtEventTime":1.72359468e+12,"dtEventTimeStamp":1.723594685e+12,"gseIndex":2.450135e+06,"iterationIndex":23,"localtime":"2024-08-14 08:08:05","log":"2024-08-14T08:08:05.500Z INFO batch processing completed","message":"batch job completed successfully","path":"/var/log/batch.log","serverip":"10.0.0.5","thedate":2.0240814e+07,"time":"1723594685","trace_id":"batch001"}]`,
			options:  `{"result_table.doris|4":{"result_schema":[{"field_alias":"dtEventTime","field_index":0,"field_name":"__c0","field_type":"long"},{"field_alias":"__shard_key__","field_index":1,"field_name":"__c1","field_type":"long"},{"field_alias":"dtEventTimeStamp","field_index":2,"field_name":"__c2","field_type":"long"},{"field_alias":"thedate","field_index":3,"field_name":"__c3","field_type":"int"},{"field_alias":"localtime","field_index":4,"field_name":"__c4","field_type":"string"},{"field_alias":"iterationIndex","field_index":5,"field_name":"__c5","field_type":"long"},{"field_alias":"__ext","field_index":6,"field_name":"__c6","field_type":"string"},{"field_alias":"cloudId","field_index":7,"field_name":"__c7","field_type":"long"},{"field_alias":"gseIndex","field_index":8,"field_name":"__c8","field_type":"long"},{"field_alias":"path","field_index":9,"field_name":"__c9","field_type":"string"},{"field_alias":"time","field_index":10,"field_name":"__c10","field_type":"long"},{"field_alias":"log","field_index":11,"field_name":"__c11","field_type":"string"},{"field_alias":"message","field_index":12,"field_name":"__c12","field_type":"string"},{"field_alias":"serverip","field_index":13,"field_name":"__c13","field_type":"string"},{"field_alias":"trace_id","field_index":14,"field_name":"__c14","field_type":"string"}]}}`,
		},
		"doris query with highlight enabled": {
			queryDirect: &structured.QueryDirect{
				SpaceUid: spaceUid,
				References: metadata.QueryReference{
					"a": []*metadata.QueryMetric{
						{
							ReferenceName: "a",
							MetricName:    "dtEventTimeStamp",
							QueryList: []*metadata.Query{
								{
									DataSource:  structured.BkLog,
									TableID:     "result_table.doris",
									StorageType: "bk_sql",
									StorageID:   "4",
									SourceType:  structured.BkLog,
									ClusterName: "",
									DB:          "2_bklog_bkunify_query_doris",
									Measurement: "doris",
									Field:       "dtEventTimeStamp",
									AllConditions: metadata.AllConditions{
										{
											{
												DimensionName: "message",
												Value:         []string{"error"},
												Operator:      metadata.ConditionContains,
											},
											{
												DimensionName: "log",
												Value:         []string{"error"},
												Operator:      metadata.ConditionContains,
											},
										},
									},
								},
							},
						},
					},
				},
				Start: start,
				End:   end,
				Limit: 100,
				HighLight: &metadata.HighLight{
					Enable:            true,
					MaxAnalyzedOffset: 1000,
				},
			},
			total:    2,
			expected: `[{"__data_label":"","__ext.container_id":"375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bk-datalink-unify-query-6df8bcc4c9-rk4sc","__highlight":{"log":["2024-08-14T08:06:41.000Z <mark>error</mark> database connection failed"],"message":["database connection <mark>error</mark> occurred"]},"__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.9077703953e+10,"cloudId":0,"dtEventTime":1.72359416e+12,"dtEventTimeStamp":1.723594161e+12,"gseIndex":2.450131e+06,"iterationIndex":19,"localtime":"2024-08-14 08:06:41","log":"2024-08-14T08:06:41.000Z error database connection failed","message":"database connection error occurred","path":"/var/log/app.log","serverip":"127.0.0.1","thedate":2.0240814e+07,"time":"1723594161","trace_id":"abc123"},{"__data_label":"","__ext.container_id":"375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bk-datalink-unify-query-6df8bcc4c9-rk4sc","__highlight":{"log":["2024-08-14T08:06:42.000Z <mark>error</mark> processing request timeout"],"message":["fatal <mark>error</mark> in processing request"]},"__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.9077703954e+10,"cloudId":0,"dtEventTime":1.7235942e+12,"dtEventTimeStamp":1.723594201e+12,"gseIndex":2.450132e+06,"iterationIndex":20,"localtime":"2024-08-14 08:06:42","log":"2024-08-14T08:06:42.000Z error processing request timeout","message":"fatal error in processing request","path":"/var/log/app.log","serverip":"127.0.0.1","thedate":2.0240814e+07,"time":"1723594202","trace_id":"def456"}]`,
			options:  `{"result_table.doris|4":{"result_schema":[{"field_alias":"dtEventTime","field_index":0,"field_name":"__c0","field_type":"long"},{"field_alias":"__shard_key__","field_index":1,"field_name":"__c1","field_type":"long"},{"field_alias":"dtEventTimeStamp","field_index":2,"field_name":"__c2","field_type":"long"},{"field_alias":"thedate","field_index":3,"field_name":"__c3","field_type":"int"},{"field_alias":"localtime","field_index":4,"field_name":"__c4","field_type":"string"},{"field_alias":"iterationIndex","field_index":5,"field_name":"__c5","field_type":"long"},{"field_alias":"__ext","field_index":6,"field_name":"__c6","field_type":"string"},{"field_alias":"cloudId","field_index":7,"field_name":"__c7","field_type":"long"},{"field_alias":"gseIndex","field_index":8,"field_name":"__c8","field_type":"long"},{"field_alias":"path","field_index":9,"field_name":"__c9","field_type":"string"},{"field_alias":"time","field_index":10,"field_name":"__c10","field_type":"long"},{"field_alias":"log","field_index":11,"field_name":"__c11","field_type":"string"},{"field_alias":"message","field_index":12,"field_name":"__c12","field_type":"string"},{"field_alias":"serverip","field_index":13,"field_name":"__c13","field_type":"string"},{"field_alias":"trace_id","field_index":14,"field_name":"__c14","field_type":"string"}]}}`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			total, list, options, err := queryRawWithInstanceDirect(ctx, tt.queryDirect)
			assert.Nil(t, err)
			if err != nil {
				return
			}
			assert.Equal(t, tt.total, total)
			actual := json.MarshalListMap(list)
			assert.Equal(t, tt.expected, actual)
			if len(options) > 0 || tt.options != "" {
				optActual, _ := json.Marshal(options)
				assert.JSONEq(t, tt.options, string(optActual))
			}
		})
	}
}
