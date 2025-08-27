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
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/jarcoal/httpmock"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func TestQueryTsWithDoris(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	//viper.Set(bkapi.BkAPIAddressConfigPath, mock.BkSQLUrlDomain)

	spaceUid := influxdb.SpaceUid
	tableID := influxdb.ResultTableDoris

	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)

	defaultStart := time.UnixMilli(1744662513046)
	defaultEnd := time.UnixMilli(1744684113046)

	mock.BkSQL.Set(map[string]any{
		"SHOW CREATE TABLE `2_bklog_bkunify_query_doris`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":18,"external_api_call_time_mills":{"bkbase_auth_api":43,"bkbase_meta_api":0,"bkbase_apigw_api":33},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":0,"query_db":5,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":45,"match_query_routing_rule":0,"check_permission":43,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":11776,"timetaken":0.096,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,

		// 查询 1 条原始数据，按照字段正向排序
		"SELECT *, `gseIndex` AS `_value_`, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1744662180000 AND `dtEventTimeStamp` <= 1744684113000 AND `thedate` = '20250415' ORDER BY `_timestamp_` ASC, `_value_` ASC LIMIT 1": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"2_bklog_bkunify_query_doris":{"start":"2025041500","end":"2025041523"}},"cluster":"doris-test","totalRecords":1,"external_api_call_time_mills":{"bkbase_auth_api":12,"bkbase_meta_api":0,"bkbase_apigw_api":0},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"thedate":20250415,"dteventtimestamp":1744662180000,"dteventtime":null,"localtime":null,"__shard_key__":29077703953,"__ext":"{\"container_id\":\"375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2\",\"container_image\":\"sha256:3a0506f06f1467e93c3a582203aac1a7501e77091572ec9612ddeee4a4dbbdb8\",\"container_name\":\"unify-query\",\"io_kubernetes_pod\":\"bk-datalink-unify-query-6df8bcc4c9-rk4sc\",\"io_kubernetes_pod_ip\":\"127.0.0.1\",\"io_kubernetes_pod_namespace\":\"blueking\",\"io_kubernetes_pod_uid\":\"558c5b17-b221-47e1-aa66-036cc9b43e2a\",\"io_kubernetes_workload_name\":\"bk-datalink-unify-query-6df8bcc4c9\",\"io_kubernetes_workload_type\":\"ReplicaSet\"}","cloudid":0.0,"file":"http/handler.go:320","gseindex":2450131.0,"iterationindex":19.0,"level":"info","log":"2025-04-14T20:22:59.982Z\tinfo\thttp/handler.go:320\t[5108397435e997364f8dc1251533e65e] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[strategy:9155] Connection:[keep-alive] Content-Length:[863] Content-Type:[application/json] Traceparent:[00-5108397435e997364f8dc1251533e65e-ca18e72c0f0eafd4-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__2]], body: {\"space_uid\":\"bkcc__2\",\"query_list\":[{\"field_name\":\"bscp_config_consume_total_file_change_count\",\"is_regexp\":false,\"function\":[{\"method\":\"mean\",\"without\":false,\"dimensions\":[\"app\",\"biz\",\"clientType\"]}],\"time_aggregation\":{\"function\":\"increase\",\"window\":\"1m\"},\"is_dom_sampled\":false,\"reference_name\":\"a\",\"dimensions\":[\"app\",\"biz\",\"clientType\"],\"conditions\":{\"field_list\":[{\"field_name\":\"releaseChangeStatus\",\"value\":[\"Failed\"],\"op\":\"contains\"},{\"field_name\":\"bcs_cluster_id\",\"value\":[\"BCS-K8S-00000\"],\"op\":\"contains\"}],\"condition_list\":[\"and\"]},\"keep_columns\":[\"_time\",\"a\",\"app\",\"biz\",\"clientType\"],\"query_string\":\"\"}],\"metric_merge\":\"a\",\"start_time\":\"1744660260\",\"end_time\":\"1744662120\",\"step\":\"60s\",\"timezone\":\"Asia/Shanghai\",\"instant\":false}","message":" header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[strategy:9155] Connection:[keep-alive] Content-Length:[863] Content-Type:[application/json] Traceparent:[00-5108397435e997364f8dc1251533e65e-ca18e72c0f0eafd4-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__2]], body: {\"space_uid\":\"bkcc__2\",\"query_list\":[{\"field_name\":\"bscp_config_consume_total_file_change_count\",\"is_regexp\":false,\"function\":[{\"method\":\"mean\",\"without\":false,\"dimensions\":[\"app\",\"biz\",\"clientType\"]}],\"time_aggregation\":{\"function\":\"increase\",\"window\":\"1m\"},\"is_dom_sampled\":false,\"reference_name\":\"a\",\"dimensions\":[\"app\",\"biz\",\"clientType\"],\"conditions\":{\"field_list\":[{\"field_name\":\"releaseChangeStatus\",\"value\":[\"Failed\"],\"op\":\"contains\"},{\"field_name\":\"bcs_cluster_id\",\"value\":[\"BCS-K8S-00000\"],\"op\":\"contains\"}],\"condition_list\":[\"and\"]},\"keep_columns\":[\"_time\",\"a\",\"app\",\"biz\",\"clientType\"],\"query_string\":\"\"}],\"metric_merge\":\"a\",\"start_time\":\"1744660260\",\"end_time\":\"1744662120\",\"step\":\"60s\",\"timezone\":\"Asia/Shanghai\",\"instant\":false}","path":"/var/host/data/bcs/lib/docker/containers/375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2/375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2-json.log","report_time":"2025-04-14T20:22:59.982Z","serverip":"127.0.0.1","time":"1744662180000","trace_id":"5108397435e997364f8dc1251533e65e","_value_":2450131.0,"_timestamp_":1744662180000}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":182,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":56,"match_query_routing_rule":0,"check_permission":13,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["thedate","dteventtimestamp","dteventtime","localtime","__shard_key__","__ext","cloudid","file","gseindex","iterationindex","level","log","message","path","report_time","serverip","time","trace_id","_value_","_timestamp_"],"total_record_size":8856,"timetaken":0.255,"result_schema":[{"field_type":"int","field_name":"__c0","field_alias":"thedate","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"dteventtimestamp","field_index":1},{"field_type":"string","field_name":"__c2","field_alias":"dteventtime","field_index":2},{"field_type":"string","field_name":"__c3","field_alias":"localtime","field_index":3},{"field_type":"long","field_name":"__c4","field_alias":"__shard_key__","field_index":4},{"field_type":"string","field_name":"__c5","field_alias":"__ext","field_index":5},{"field_type":"double","field_name":"__c6","field_alias":"cloudid","field_index":6},{"field_type":"string","field_name":"__c7","field_alias":"file","field_index":7},{"field_type":"double","field_name":"__c8","field_alias":"gseindex","field_index":8},{"field_type":"double","field_name":"__c9","field_alias":"iterationindex","field_index":9},{"field_type":"string","field_name":"__c10","field_alias":"level","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"log","field_index":11},{"field_type":"string","field_name":"__c12","field_alias":"message","field_index":12},{"field_type":"string","field_name":"__c13","field_alias":"path","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"report_time","field_index":14},{"field_type":"string","field_name":"__c15","field_alias":"serverip","field_index":15},{"field_type":"string","field_name":"__c16","field_alias":"time","field_index":16},{"field_type":"string","field_name":"__c17","field_alias":"trace_id","field_index":17},{"field_type":"double","field_name":"__c18","field_alias":"_value_","field_index":18},{"field_type":"long","field_name":"__c19","field_alias":"_timestamp_","field_index":19}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,

		// 根据维度 __ext.container_name 进行 count 聚合，同时用值正向排序
		"SELECT CAST(__ext['container_name'] AS STRING) AS `__ext__bk_46__container_name`, COUNT(`gseIndex`) AS `_value_`, CAST(FLOOR(dtEventTimeStamp / 30000) AS INT) * 30000  AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1744662509999 AND `dtEventTimeStamp` <= 1744684142999 AND `thedate` = '20250415' GROUP BY __ext__bk_46__container_name, _timestamp_ ORDER BY `_timestamp_` ASC, `_value_` ASC LIMIT 2000005": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"2_bklog_bkunify_query_doris":{"start":"2025041500","end":"2025041523"}},"cluster":"doris-test","totalRecords":722,"external_api_call_time_mills":{"bkbase_auth_api":72,"bkbase_meta_api":6,"bkbase_apigw_api":28},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"__ext__bk_46__container_name":"unify-query","_value_":3684,"_timestamp_":1744662510000},{"__ext__bk_46__container_name":"unify-query","_value_":4012,"_timestamp_":1744662540000},{"__ext__bk_46__container_name":"unify-query","_value_":3671,"_timestamp_":1744662570000},{"__ext__bk_46__container_name":"unify-query","_value_":17092,"_timestamp_":1744662600000},{"__ext__bk_46__container_name":"unify-query","_value_":12881,"_timestamp_":1744662630000},{"__ext__bk_46__container_name":"unify-query","_value_":5902,"_timestamp_":1744662660000},{"__ext__bk_46__container_name":"unify-query","_value_":10443,"_timestamp_":1744662690000},{"__ext__bk_46__container_name":"unify-query","_value_":4388,"_timestamp_":1744662720000},{"__ext__bk_46__container_name":"unify-query","_value_":3357,"_timestamp_":1744662750000},{"__ext__bk_46__container_name":"unify-query","_value_":4381,"_timestamp_":1744662780000},{"__ext__bk_46__container_name":"unify-query","_value_":3683,"_timestamp_":1744662810000},{"__ext__bk_46__container_name":"unify-query","_value_":4353,"_timestamp_":1744662840000},{"__ext__bk_46__container_name":"unify-query","_value_":3441,"_timestamp_":1744662870000},{"__ext__bk_46__container_name":"unify-query","_value_":4251,"_timestamp_":1744662900000},{"__ext__bk_46__container_name":"unify-query","_value_":3476,"_timestamp_":1744662930000},{"__ext__bk_46__container_name":"unify-query","_value_":4036,"_timestamp_":1744662960000},{"__ext__bk_46__container_name":"unify-query","_value_":3549,"_timestamp_":1744662990000},{"__ext__bk_46__container_name":"unify-query","_value_":4351,"_timestamp_":1744663020000},{"__ext__bk_46__container_name":"unify-query","_value_":3651,"_timestamp_":1744663050000},{"__ext__bk_46__container_name":"unify-query","_value_":4096,"_timestamp_":1744663080000},{"__ext__bk_46__container_name":"unify-query","_value_":3618,"_timestamp_":1744663110000},{"__ext__bk_46__container_name":"unify-query","_value_":4100,"_timestamp_":1744663140000},{"__ext__bk_46__container_name":"unify-query","_value_":3622,"_timestamp_":1744663170000},{"__ext__bk_46__container_name":"unify-query","_value_":6044,"_timestamp_":1744663200000},{"__ext__bk_46__container_name":"unify-query","_value_":3766,"_timestamp_":1744663230000},{"__ext__bk_46__container_name":"unify-query","_value_":4461,"_timestamp_":1744663260000},{"__ext__bk_46__container_name":"unify-query","_value_":3783,"_timestamp_":1744663290000},{"__ext__bk_46__container_name":"unify-query","_value_":4559,"_timestamp_":1744663320000},{"__ext__bk_46__container_name":"unify-query","_value_":3634,"_timestamp_":1744663350000},{"__ext__bk_46__container_name":"unify-query","_value_":3869,"_timestamp_":1744663380000},{"__ext__bk_46__container_name":"unify-query","_value_":3249,"_timestamp_":1744663410000},{"__ext__bk_46__container_name":"unify-query","_value_":4473,"_timestamp_":1744663440000},{"__ext__bk_46__container_name":"unify-query","_value_":3514,"_timestamp_":1744663470000},{"__ext__bk_46__container_name":"unify-query","_value_":4923,"_timestamp_":1744663500000},{"__ext__bk_46__container_name":"unify-query","_value_":3379,"_timestamp_":1744663530000},{"__ext__bk_46__container_name":"unify-query","_value_":4489,"_timestamp_":1744663560000},{"__ext__bk_46__container_name":"unify-query","_value_":3411,"_timestamp_":1744663590000},{"__ext__bk_46__container_name":"unify-query","_value_":4374,"_timestamp_":1744663620000},{"__ext__bk_46__container_name":"unify-query","_value_":3370,"_timestamp_":1744663650000},{"__ext__bk_46__container_name":"unify-query","_value_":4310,"_timestamp_":1744663680000},{"__ext__bk_46__container_name":"unify-query","_value_":3609,"_timestamp_":1744663710000},{"__ext__bk_46__container_name":"unify-query","_value_":4318,"_timestamp_":1744663740000},{"__ext__bk_46__container_name":"unify-query","_value_":3570,"_timestamp_":1744663770000},{"__ext__bk_46__container_name":"unify-query","_value_":4334,"_timestamp_":1744663800000},{"__ext__bk_46__container_name":"unify-query","_value_":3767,"_timestamp_":1744663830000},{"__ext__bk_46__container_name":"unify-query","_value_":4455,"_timestamp_":1744663860000},{"__ext__bk_46__container_name":"unify-query","_value_":3703,"_timestamp_":1744663890000},{"__ext__bk_46__container_name":"unify-query","_value_":4511,"_timestamp_":1744663920000},{"__ext__bk_46__container_name":"unify-query","_value_":3667,"_timestamp_":1744663950000},{"__ext__bk_46__container_name":"unify-query","_value_":3998,"_timestamp_":1744663980000},{"__ext__bk_46__container_name":"unify-query","_value_":3579,"_timestamp_":1744664010000},{"__ext__bk_46__container_name":"unify-query","_value_":4156,"_timestamp_":1744664040000},{"__ext__bk_46__container_name":"unify-query","_value_":3340,"_timestamp_":1744664070000},{"__ext__bk_46__container_name":"unify-query","_value_":4344,"_timestamp_":1744664100000},{"__ext__bk_46__container_name":"unify-query","_value_":3590,"_timestamp_":1744664130000},{"__ext__bk_46__container_name":"unify-query","_value_":4161,"_timestamp_":1744664160000},{"__ext__bk_46__container_name":"unify-query","_value_":3484,"_timestamp_":1744664190000},{"__ext__bk_46__container_name":"unify-query","_value_":4273,"_timestamp_":1744664220000},{"__ext__bk_46__container_name":"unify-query","_value_":3494,"_timestamp_":1744664250000},{"__ext__bk_46__container_name":"unify-query","_value_":4230,"_timestamp_":1744664280000},{"__ext__bk_46__container_name":"unify-query","_value_":3619,"_timestamp_":1744664310000},{"__ext__bk_46__container_name":"unify-query","_value_":4013,"_timestamp_":1744664340000},{"__ext__bk_46__container_name":"unify-query","_value_":3565,"_timestamp_":1744664370000},{"__ext__bk_46__container_name":"unify-query","_value_":18144,"_timestamp_":1744664400000},{"__ext__bk_46__container_name":"unify-query","_value_":13615,"_timestamp_":1744664430000},{"__ext__bk_46__container_name":"unify-query","_value_":3178,"_timestamp_":1744664460000},{"__ext__bk_46__container_name":"unify-query","_value_":13044,"_timestamp_":1744664490000},{"__ext__bk_46__container_name":"unify-query","_value_":4767,"_timestamp_":1744664520000},{"__ext__bk_46__container_name":"unify-query","_value_":3528,"_timestamp_":1744664550000},{"__ext__bk_46__container_name":"unify-query","_value_":4316,"_timestamp_":1744664580000},{"__ext__bk_46__container_name":"unify-query","_value_":3317,"_timestamp_":1744664610000},{"__ext__bk_46__container_name":"unify-query","_value_":4395,"_timestamp_":1744664640000},{"__ext__bk_46__container_name":"unify-query","_value_":3599,"_timestamp_":1744664670000},{"__ext__bk_46__container_name":"unify-query","_value_":4149,"_timestamp_":1744664700000},{"__ext__bk_46__container_name":"unify-query","_value_":3474,"_timestamp_":1744664730000},{"__ext__bk_46__container_name":"unify-query","_value_":4201,"_timestamp_":1744664760000},{"__ext__bk_46__container_name":"unify-query","_value_":3384,"_timestamp_":1744664790000},{"__ext__bk_46__container_name":"unify-query","_value_":4442,"_timestamp_":1744664820000},{"__ext__bk_46__container_name":"unify-query","_value_":3559,"_timestamp_":1744664850000},{"__ext__bk_46__container_name":"unify-query","_value_":4166,"_timestamp_":1744664880000},{"__ext__bk_46__container_name":"unify-query","_value_":3438,"_timestamp_":1744664910000},{"__ext__bk_46__container_name":"unify-query","_value_":4244,"_timestamp_":1744664940000},{"__ext__bk_46__container_name":"unify-query","_value_":3640,"_timestamp_":1744664970000},{"__ext__bk_46__container_name":"unify-query","_value_":4305,"_timestamp_":1744665000000},{"__ext__bk_46__container_name":"unify-query","_value_":3771,"_timestamp_":1744665030000},{"__ext__bk_46__container_name":"unify-query","_value_":4485,"_timestamp_":1744665060000},{"__ext__bk_46__container_name":"unify-query","_value_":3842,"_timestamp_":1744665090000},{"__ext__bk_46__container_name":"unify-query","_value_":4423,"_timestamp_":1744665120000},{"__ext__bk_46__container_name":"unify-query","_value_":3610,"_timestamp_":1744665150000},{"__ext__bk_46__container_name":"unify-query","_value_":4125,"_timestamp_":1744665180000},{"__ext__bk_46__container_name":"unify-query","_value_":3500,"_timestamp_":1744665210000},{"__ext__bk_46__container_name":"unify-query","_value_":4252,"_timestamp_":1744665240000},{"__ext__bk_46__container_name":"unify-query","_value_":3427,"_timestamp_":1744665270000},{"__ext__bk_46__container_name":"unify-query","_value_":5089,"_timestamp_":1744665300000},{"__ext__bk_46__container_name":"unify-query","_value_":3450,"_timestamp_":1744665330000},{"__ext__bk_46__container_name":"unify-query","_value_":4349,"_timestamp_":1744665360000},{"__ext__bk_46__container_name":"unify-query","_value_":3188,"_timestamp_":1744665390000},{"__ext__bk_46__container_name":"unify-query","_value_":4556,"_timestamp_":1744665420000},{"__ext__bk_46__container_name":"unify-query","_value_":3372,"_timestamp_":1744665450000},{"__ext__bk_46__container_name":"unify-query","_value_":4408,"_timestamp_":1744665480000},{"__ext__bk_46__container_name":"unify-query","_value_":3445,"_timestamp_":1744665510000},{"__ext__bk_46__container_name":"unify-query","_value_":4213,"_timestamp_":1744665540000},{"__ext__bk_46__container_name":"unify-query","_value_":3408,"_timestamp_":1744665570000},{"__ext__bk_46__container_name":"unify-query","_value_":6235,"_timestamp_":1744665600000},{"__ext__bk_46__container_name":"unify-query","_value_":3641,"_timestamp_":1744665630000},{"__ext__bk_46__container_name":"unify-query","_value_":4577,"_timestamp_":1744665660000},{"__ext__bk_46__container_name":"unify-query","_value_":3719,"_timestamp_":1744665690000},{"__ext__bk_46__container_name":"unify-query","_value_":4548,"_timestamp_":1744665720000},{"__ext__bk_46__container_name":"unify-query","_value_":3420,"_timestamp_":1744665750000},{"__ext__bk_46__container_name":"unify-query","_value_":4246,"_timestamp_":1744665780000},{"__ext__bk_46__container_name":"unify-query","_value_":3359,"_timestamp_":1744665810000},{"__ext__bk_46__container_name":"unify-query","_value_":4332,"_timestamp_":1744665840000},{"__ext__bk_46__container_name":"unify-query","_value_":3422,"_timestamp_":1744665870000},{"__ext__bk_46__container_name":"unify-query","_value_":4229,"_timestamp_":1744665900000},{"__ext__bk_46__container_name":"unify-query","_value_":3610,"_timestamp_":1744665930000},{"__ext__bk_46__container_name":"unify-query","_value_":4119,"_timestamp_":1744665960000},{"__ext__bk_46__container_name":"unify-query","_value_":3570,"_timestamp_":1744665990000},{"__ext__bk_46__container_name":"unify-query","_value_":4144,"_timestamp_":1744666020000},{"__ext__bk_46__container_name":"unify-query","_value_":3302,"_timestamp_":1744666050000},{"__ext__bk_46__container_name":"unify-query","_value_":4398,"_timestamp_":1744666080000},{"__ext__bk_46__container_name":"unify-query","_value_":3559,"_timestamp_":1744666110000},{"__ext__bk_46__container_name":"unify-query","_value_":4097,"_timestamp_":1744666140000},{"__ext__bk_46__container_name":"unify-query","_value_":3315,"_timestamp_":1744666170000},{"__ext__bk_46__container_name":"unify-query","_value_":16721,"_timestamp_":1744666200000},{"__ext__bk_46__container_name":"unify-query","_value_":13631,"_timestamp_":1744666230000},{"__ext__bk_46__container_name":"unify-query","_value_":2982,"_timestamp_":1744666260000},{"__ext__bk_46__container_name":"unify-query","_value_":11858,"_timestamp_":1744666290000},{"__ext__bk_46__container_name":"unify-query","_value_":5515,"_timestamp_":1744666320000},{"__ext__bk_46__container_name":"unify-query","_value_":2869,"_timestamp_":1744666350000},{"__ext__bk_46__container_name":"unify-query","_value_":4795,"_timestamp_":1744666380000},{"__ext__bk_46__container_name":"unify-query","_value_":3603,"_timestamp_":1744666410000},{"__ext__bk_46__container_name":"unify-query","_value_":4204,"_timestamp_":1744666440000},{"__ext__bk_46__container_name":"unify-query","_value_":3264,"_timestamp_":1744666470000},{"__ext__bk_46__container_name":"unify-query","_value_":4377,"_timestamp_":1744666500000},{"__ext__bk_46__container_name":"unify-query","_value_":3443,"_timestamp_":1744666530000},{"__ext__bk_46__container_name":"unify-query","_value_":4307,"_timestamp_":1744666560000},{"__ext__bk_46__container_name":"unify-query","_value_":3459,"_timestamp_":1744666590000},{"__ext__bk_46__container_name":"unify-query","_value_":4342,"_timestamp_":1744666620000},{"__ext__bk_46__container_name":"unify-query","_value_":3598,"_timestamp_":1744666650000},{"__ext__bk_46__container_name":"unify-query","_value_":4052,"_timestamp_":1744666680000},{"__ext__bk_46__container_name":"unify-query","_value_":3577,"_timestamp_":1744666710000},{"__ext__bk_46__container_name":"unify-query","_value_":4128,"_timestamp_":1744666740000},{"__ext__bk_46__container_name":"unify-query","_value_":3499,"_timestamp_":1744666770000},{"__ext__bk_46__container_name":"unify-query","_value_":6209,"_timestamp_":1744666800000},{"__ext__bk_46__container_name":"unify-query","_value_":3575,"_timestamp_":1744666830000},{"__ext__bk_46__container_name":"unify-query","_value_":4543,"_timestamp_":1744666860000},{"__ext__bk_46__container_name":"unify-query","_value_":3604,"_timestamp_":1744666890000},{"__ext__bk_46__container_name":"unify-query","_value_":4579,"_timestamp_":1744666920000},{"__ext__bk_46__container_name":"unify-query","_value_":3531,"_timestamp_":1744666950000},{"__ext__bk_46__container_name":"unify-query","_value_":4314,"_timestamp_":1744666980000},{"__ext__bk_46__container_name":"unify-query","_value_":3416,"_timestamp_":1744667010000},{"__ext__bk_46__container_name":"unify-query","_value_":4320,"_timestamp_":1744667040000},{"__ext__bk_46__container_name":"unify-query","_value_":3488,"_timestamp_":1744667070000},{"__ext__bk_46__container_name":"unify-query","_value_":5054,"_timestamp_":1744667100000},{"__ext__bk_46__container_name":"unify-query","_value_":3525,"_timestamp_":1744667130000},{"__ext__bk_46__container_name":"unify-query","_value_":4313,"_timestamp_":1744667160000},{"__ext__bk_46__container_name":"unify-query","_value_":3607,"_timestamp_":1744667190000},{"__ext__bk_46__container_name":"unify-query","_value_":4118,"_timestamp_":1744667220000},{"__ext__bk_46__container_name":"unify-query","_value_":3350,"_timestamp_":1744667250000},{"__ext__bk_46__container_name":"unify-query","_value_":4280,"_timestamp_":1744667280000},{"__ext__bk_46__container_name":"unify-query","_value_":3634,"_timestamp_":1744667310000},{"__ext__bk_46__container_name":"unify-query","_value_":4174,"_timestamp_":1744667340000},{"__ext__bk_46__container_name":"unify-query","_value_":3807,"_timestamp_":1744667370000},{"__ext__bk_46__container_name":"unify-query","_value_":4358,"_timestamp_":1744667400000},{"__ext__bk_46__container_name":"unify-query","_value_":3595,"_timestamp_":1744667430000},{"__ext__bk_46__container_name":"unify-query","_value_":4630,"_timestamp_":1744667460000},{"__ext__bk_46__container_name":"unify-query","_value_":3845,"_timestamp_":1744667490000},{"__ext__bk_46__container_name":"unify-query","_value_":4361,"_timestamp_":1744667520000},{"__ext__bk_46__container_name":"unify-query","_value_":3572,"_timestamp_":1744667550000},{"__ext__bk_46__container_name":"unify-query","_value_":4095,"_timestamp_":1744667580000},{"__ext__bk_46__container_name":"unify-query","_value_":3535,"_timestamp_":1744667610000},{"__ext__bk_46__container_name":"unify-query","_value_":4200,"_timestamp_":1744667640000},{"__ext__bk_46__container_name":"unify-query","_value_":3390,"_timestamp_":1744667670000},{"__ext__bk_46__container_name":"unify-query","_value_":4262,"_timestamp_":1744667700000},{"__ext__bk_46__container_name":"unify-query","_value_":3398,"_timestamp_":1744667730000},{"__ext__bk_46__container_name":"unify-query","_value_":4320,"_timestamp_":1744667760000},{"__ext__bk_46__container_name":"unify-query","_value_":3429,"_timestamp_":1744667790000},{"__ext__bk_46__container_name":"unify-query","_value_":4288,"_timestamp_":1744667820000},{"__ext__bk_46__container_name":"unify-query","_value_":3482,"_timestamp_":1744667850000},{"__ext__bk_46__container_name":"unify-query","_value_":4166,"_timestamp_":1744667880000},{"__ext__bk_46__container_name":"unify-query","_value_":3612,"_timestamp_":1744667910000},{"__ext__bk_46__container_name":"unify-query","_value_":4194,"_timestamp_":1744667940000},{"__ext__bk_46__container_name":"unify-query","_value_":3423,"_timestamp_":1744667970000},{"__ext__bk_46__container_name":"unify-query","_value_":18203,"_timestamp_":1744668000000},{"__ext__bk_46__container_name":"unify-query","_value_":13685,"_timestamp_":1744668030000},{"__ext__bk_46__container_name":"unify-query","_value_":3281,"_timestamp_":1744668060000},{"__ext__bk_46__container_name":"unify-query","_value_":12556,"_timestamp_":1744668090000},{"__ext__bk_46__container_name":"unify-query","_value_":4893,"_timestamp_":1744668120000},{"__ext__bk_46__container_name":"unify-query","_value_":3607,"_timestamp_":1744668150000},{"__ext__bk_46__container_name":"unify-query","_value_":4336,"_timestamp_":1744668180000},{"__ext__bk_46__container_name":"unify-query","_value_":3609,"_timestamp_":1744668210000},{"__ext__bk_46__container_name":"unify-query","_value_":4097,"_timestamp_":1744668240000},{"__ext__bk_46__container_name":"unify-query","_value_":3669,"_timestamp_":1744668270000},{"__ext__bk_46__container_name":"unify-query","_value_":3997,"_timestamp_":1744668300000},{"__ext__bk_46__container_name":"unify-query","_value_":3494,"_timestamp_":1744668330000},{"__ext__bk_46__container_name":"unify-query","_value_":4172,"_timestamp_":1744668360000},{"__ext__bk_46__container_name":"unify-query","_value_":3523,"_timestamp_":1744668390000},{"__ext__bk_46__container_name":"unify-query","_value_":3877,"_timestamp_":1744668420000},{"__ext__bk_46__container_name":"unify-query","_value_":3565,"_timestamp_":1744668450000},{"__ext__bk_46__container_name":"unify-query","_value_":4230,"_timestamp_":1744668480000},{"__ext__bk_46__container_name":"unify-query","_value_":3469,"_timestamp_":1744668510000},{"__ext__bk_46__container_name":"unify-query","_value_":4243,"_timestamp_":1744668540000},{"__ext__bk_46__container_name":"unify-query","_value_":3304,"_timestamp_":1744668570000},{"__ext__bk_46__container_name":"unify-query","_value_":4690,"_timestamp_":1744668600000},{"__ext__bk_46__container_name":"unify-query","_value_":3717,"_timestamp_":1744668630000},{"__ext__bk_46__container_name":"unify-query","_value_":4618,"_timestamp_":1744668660000},{"__ext__bk_46__container_name":"unify-query","_value_":3732,"_timestamp_":1744668690000},{"__ext__bk_46__container_name":"unify-query","_value_":4477,"_timestamp_":1744668720000},{"__ext__bk_46__container_name":"unify-query","_value_":3615,"_timestamp_":1744668750000},{"__ext__bk_46__container_name":"unify-query","_value_":4154,"_timestamp_":1744668780000},{"__ext__bk_46__container_name":"unify-query","_value_":3367,"_timestamp_":1744668810000},{"__ext__bk_46__container_name":"unify-query","_value_":4193,"_timestamp_":1744668840000},{"__ext__bk_46__container_name":"unify-query","_value_":3592,"_timestamp_":1744668870000},{"__ext__bk_46__container_name":"unify-query","_value_":4971,"_timestamp_":1744668900000},{"__ext__bk_46__container_name":"unify-query","_value_":3359,"_timestamp_":1744668930000},{"__ext__bk_46__container_name":"unify-query","_value_":4540,"_timestamp_":1744668960000},{"__ext__bk_46__container_name":"unify-query","_value_":3406,"_timestamp_":1744668990000},{"__ext__bk_46__container_name":"unify-query","_value_":4375,"_timestamp_":1744669020000},{"__ext__bk_46__container_name":"unify-query","_value_":3386,"_timestamp_":1744669050000},{"__ext__bk_46__container_name":"unify-query","_value_":4281,"_timestamp_":1744669080000},{"__ext__bk_46__container_name":"unify-query","_value_":3410,"_timestamp_":1744669110000},{"__ext__bk_46__container_name":"unify-query","_value_":4545,"_timestamp_":1744669140000},{"__ext__bk_46__container_name":"unify-query","_value_":3724,"_timestamp_":1744669170000},{"__ext__bk_46__container_name":"unify-query","_value_":5903,"_timestamp_":1744669200000},{"__ext__bk_46__container_name":"unify-query","_value_":3672,"_timestamp_":1744669230000},{"__ext__bk_46__container_name":"unify-query","_value_":4413,"_timestamp_":1744669260000},{"__ext__bk_46__container_name":"unify-query","_value_":3792,"_timestamp_":1744669290000},{"__ext__bk_46__container_name":"unify-query","_value_":4422,"_timestamp_":1744669320000},{"__ext__bk_46__container_name":"unify-query","_value_":3718,"_timestamp_":1744669350000},{"__ext__bk_46__container_name":"unify-query","_value_":4213,"_timestamp_":1744669380000},{"__ext__bk_46__container_name":"unify-query","_value_":3622,"_timestamp_":1744669410000},{"__ext__bk_46__container_name":"unify-query","_value_":4043,"_timestamp_":1744669440000},{"__ext__bk_46__container_name":"unify-query","_value_":3542,"_timestamp_":1744669470000},{"__ext__bk_46__container_name":"unify-query","_value_":4179,"_timestamp_":1744669500000},{"__ext__bk_46__container_name":"unify-query","_value_":3368,"_timestamp_":1744669530000},{"__ext__bk_46__container_name":"unify-query","_value_":4354,"_timestamp_":1744669560000},{"__ext__bk_46__container_name":"unify-query","_value_":3368,"_timestamp_":1744669590000},{"__ext__bk_46__container_name":"unify-query","_value_":4229,"_timestamp_":1744669620000},{"__ext__bk_46__container_name":"unify-query","_value_":3458,"_timestamp_":1744669650000},{"__ext__bk_46__container_name":"unify-query","_value_":4310,"_timestamp_":1744669680000},{"__ext__bk_46__container_name":"unify-query","_value_":3512,"_timestamp_":1744669710000},{"__ext__bk_46__container_name":"unify-query","_value_":4188,"_timestamp_":1744669740000},{"__ext__bk_46__container_name":"unify-query","_value_":3436,"_timestamp_":1744669770000},{"__ext__bk_46__container_name":"unify-query","_value_":12171,"_timestamp_":1744669800000},{"__ext__bk_46__container_name":"unify-query","_value_":18129,"_timestamp_":1744669830000},{"__ext__bk_46__container_name":"unify-query","_value_":7142,"_timestamp_":1744669860000},{"__ext__bk_46__container_name":"unify-query","_value_":9153,"_timestamp_":1744669890000},{"__ext__bk_46__container_name":"unify-query","_value_":4566,"_timestamp_":1744669920000},{"__ext__bk_46__container_name":"unify-query","_value_":3225,"_timestamp_":1744669950000},{"__ext__bk_46__container_name":"unify-query","_value_":4378,"_timestamp_":1744669980000},{"__ext__bk_46__container_name":"unify-query","_value_":3623,"_timestamp_":1744670010000},{"__ext__bk_46__container_name":"unify-query","_value_":4266,"_timestamp_":1744670040000},{"__ext__bk_46__container_name":"unify-query","_value_":3645,"_timestamp_":1744670070000},{"__ext__bk_46__container_name":"unify-query","_value_":4043,"_timestamp_":1744670100000},{"__ext__bk_46__container_name":"unify-query","_value_":3350,"_timestamp_":1744670130000},{"__ext__bk_46__container_name":"unify-query","_value_":4333,"_timestamp_":1744670160000},{"__ext__bk_46__container_name":"unify-query","_value_":3489,"_timestamp_":1744670190000},{"__ext__bk_46__container_name":"unify-query","_value_":4303,"_timestamp_":1744670220000},{"__ext__bk_46__container_name":"unify-query","_value_":3560,"_timestamp_":1744670250000},{"__ext__bk_46__container_name":"unify-query","_value_":4121,"_timestamp_":1744670280000},{"__ext__bk_46__container_name":"unify-query","_value_":3374,"_timestamp_":1744670310000},{"__ext__bk_46__container_name":"unify-query","_value_":4362,"_timestamp_":1744670340000},{"__ext__bk_46__container_name":"unify-query","_value_":3242,"_timestamp_":1744670370000},{"__ext__bk_46__container_name":"unify-query","_value_":6416,"_timestamp_":1744670400000},{"__ext__bk_46__container_name":"unify-query","_value_":3697,"_timestamp_":1744670430000},{"__ext__bk_46__container_name":"unify-query","_value_":4506,"_timestamp_":1744670460000},{"__ext__bk_46__container_name":"unify-query","_value_":3749,"_timestamp_":1744670490000},{"__ext__bk_46__container_name":"unify-query","_value_":4587,"_timestamp_":1744670520000},{"__ext__bk_46__container_name":"unify-query","_value_":3538,"_timestamp_":1744670550000},{"__ext__bk_46__container_name":"unify-query","_value_":4221,"_timestamp_":1744670580000},{"__ext__bk_46__container_name":"unify-query","_value_":3476,"_timestamp_":1744670610000},{"__ext__bk_46__container_name":"unify-query","_value_":4227,"_timestamp_":1744670640000},{"__ext__bk_46__container_name":"unify-query","_value_":3587,"_timestamp_":1744670670000},{"__ext__bk_46__container_name":"unify-query","_value_":4848,"_timestamp_":1744670700000},{"__ext__bk_46__container_name":"unify-query","_value_":3551,"_timestamp_":1744670730000},{"__ext__bk_46__container_name":"unify-query","_value_":4068,"_timestamp_":1744670760000},{"__ext__bk_46__container_name":"unify-query","_value_":3387,"_timestamp_":1744670790000},{"__ext__bk_46__container_name":"unify-query","_value_":4366,"_timestamp_":1744670820000},{"__ext__bk_46__container_name":"unify-query","_value_":3635,"_timestamp_":1744670850000},{"__ext__bk_46__container_name":"unify-query","_value_":4256,"_timestamp_":1744670880000},{"__ext__bk_46__container_name":"unify-query","_value_":3690,"_timestamp_":1744670910000},{"__ext__bk_46__container_name":"unify-query","_value_":4155,"_timestamp_":1744670940000},{"__ext__bk_46__container_name":"unify-query","_value_":3318,"_timestamp_":1744670970000},{"__ext__bk_46__container_name":"unify-query","_value_":4661,"_timestamp_":1744671000000},{"__ext__bk_46__container_name":"unify-query","_value_":3494,"_timestamp_":1744671030000},{"__ext__bk_46__container_name":"unify-query","_value_":4442,"_timestamp_":1744671060000},{"__ext__bk_46__container_name":"unify-query","_value_":3643,"_timestamp_":1744671090000},{"__ext__bk_46__container_name":"unify-query","_value_":4755,"_timestamp_":1744671120000},{"__ext__bk_46__container_name":"unify-query","_value_":3607,"_timestamp_":1744671150000},{"__ext__bk_46__container_name":"unify-query","_value_":4284,"_timestamp_":1744671180000},{"__ext__bk_46__container_name":"unify-query","_value_":3258,"_timestamp_":1744671210000},{"__ext__bk_46__container_name":"unify-query","_value_":4453,"_timestamp_":1744671240000},{"__ext__bk_46__container_name":"unify-query","_value_":3431,"_timestamp_":1744671270000},{"__ext__bk_46__container_name":"unify-query","_value_":4231,"_timestamp_":1744671300000},{"__ext__bk_46__container_name":"unify-query","_value_":3623,"_timestamp_":1744671330000},{"__ext__bk_46__container_name":"unify-query","_value_":3907,"_timestamp_":1744671360000},{"__ext__bk_46__container_name":"unify-query","_value_":3524,"_timestamp_":1744671390000},{"__ext__bk_46__container_name":"unify-query","_value_":4438,"_timestamp_":1744671420000},{"__ext__bk_46__container_name":"unify-query","_value_":3547,"_timestamp_":1744671450000},{"__ext__bk_46__container_name":"unify-query","_value_":4033,"_timestamp_":1744671480000},{"__ext__bk_46__container_name":"unify-query","_value_":3632,"_timestamp_":1744671510000},{"__ext__bk_46__container_name":"unify-query","_value_":4162,"_timestamp_":1744671540000},{"__ext__bk_46__container_name":"unify-query","_value_":3588,"_timestamp_":1744671570000},{"__ext__bk_46__container_name":"unify-query","_value_":16444,"_timestamp_":1744671600000},{"__ext__bk_46__container_name":"unify-query","_value_":15396,"_timestamp_":1744671630000},{"__ext__bk_46__container_name":"unify-query","_value_":3024,"_timestamp_":1744671660000},{"__ext__bk_46__container_name":"unify-query","_value_":12656,"_timestamp_":1744671690000},{"__ext__bk_46__container_name":"unify-query","_value_":4733,"_timestamp_":1744671720000},{"__ext__bk_46__container_name":"unify-query","_value_":3766,"_timestamp_":1744671750000},{"__ext__bk_46__container_name":"unify-query","_value_":4388,"_timestamp_":1744671780000},{"__ext__bk_46__container_name":"unify-query","_value_":3340,"_timestamp_":1744671810000},{"__ext__bk_46__container_name":"unify-query","_value_":4487,"_timestamp_":1744671840000},{"__ext__bk_46__container_name":"unify-query","_value_":3549,"_timestamp_":1744671870000},{"__ext__bk_46__container_name":"unify-query","_value_":4154,"_timestamp_":1744671900000},{"__ext__bk_46__container_name":"unify-query","_value_":3406,"_timestamp_":1744671930000},{"__ext__bk_46__container_name":"unify-query","_value_":4314,"_timestamp_":1744671960000},{"__ext__bk_46__container_name":"unify-query","_value_":3472,"_timestamp_":1744671990000},{"__ext__bk_46__container_name":"unify-query","_value_":4309,"_timestamp_":1744672020000},{"__ext__bk_46__container_name":"unify-query","_value_":3458,"_timestamp_":1744672050000},{"__ext__bk_46__container_name":"unify-query","_value_":4191,"_timestamp_":1744672080000},{"__ext__bk_46__container_name":"unify-query","_value_":3475,"_timestamp_":1744672110000},{"__ext__bk_46__container_name":"unify-query","_value_":4194,"_timestamp_":1744672140000},{"__ext__bk_46__container_name":"unify-query","_value_":3525,"_timestamp_":1744672170000},{"__ext__bk_46__container_name":"unify-query","_value_":4445,"_timestamp_":1744672200000},{"__ext__bk_46__container_name":"unify-query","_value_":3822,"_timestamp_":1744672230000},{"__ext__bk_46__container_name":"unify-query","_value_":4346,"_timestamp_":1744672260000},{"__ext__bk_46__container_name":"unify-query","_value_":3700,"_timestamp_":1744672290000},{"__ext__bk_46__container_name":"unify-query","_value_":4615,"_timestamp_":1744672320000},{"__ext__bk_46__container_name":"unify-query","_value_":3591,"_timestamp_":1744672350000},{"__ext__bk_46__container_name":"unify-query","_value_":4056,"_timestamp_":1744672380000},{"__ext__bk_46__container_name":"unify-query","_value_":3544,"_timestamp_":1744672410000},{"__ext__bk_46__container_name":"unify-query","_value_":4188,"_timestamp_":1744672440000},{"__ext__bk_46__container_name":"unify-query","_value_":3647,"_timestamp_":1744672470000},{"__ext__bk_46__container_name":"unify-query","_value_":4887,"_timestamp_":1744672500000},{"__ext__bk_46__container_name":"unify-query","_value_":3450,"_timestamp_":1744672530000},{"__ext__bk_46__container_name":"unify-query","_value_":4302,"_timestamp_":1744672560000},{"__ext__bk_46__container_name":"unify-query","_value_":3425,"_timestamp_":1744672590000},{"__ext__bk_46__container_name":"unify-query","_value_":4320,"_timestamp_":1744672620000},{"__ext__bk_46__container_name":"unify-query","_value_":3532,"_timestamp_":1744672650000},{"__ext__bk_46__container_name":"unify-query","_value_":4282,"_timestamp_":1744672680000},{"__ext__bk_46__container_name":"unify-query","_value_":3571,"_timestamp_":1744672710000},{"__ext__bk_46__container_name":"unify-query","_value_":4182,"_timestamp_":1744672740000},{"__ext__bk_46__container_name":"unify-query","_value_":3210,"_timestamp_":1744672770000},{"__ext__bk_46__container_name":"unify-query","_value_":6383,"_timestamp_":1744672800000},{"__ext__bk_46__container_name":"unify-query","_value_":3622,"_timestamp_":1744672830000},{"__ext__bk_46__container_name":"unify-query","_value_":4408,"_timestamp_":1744672860000},{"__ext__bk_46__container_name":"unify-query","_value_":3611,"_timestamp_":1744672890000},{"__ext__bk_46__container_name":"unify-query","_value_":4795,"_timestamp_":1744672920000},{"__ext__bk_46__container_name":"unify-query","_value_":3632,"_timestamp_":1744672950000},{"__ext__bk_46__container_name":"unify-query","_value_":4102,"_timestamp_":1744672980000},{"__ext__bk_46__container_name":"unify-query","_value_":3534,"_timestamp_":1744673010000},{"__ext__bk_46__container_name":"unify-query","_value_":4212,"_timestamp_":1744673040000},{"__ext__bk_46__container_name":"unify-query","_value_":3380,"_timestamp_":1744673070000},{"__ext__bk_46__container_name":"unify-query","_value_":4289,"_timestamp_":1744673100000},{"__ext__bk_46__container_name":"unify-query","_value_":3565,"_timestamp_":1744673130000},{"__ext__bk_46__container_name":"unify-query","_value_":4120,"_timestamp_":1744673160000},{"__ext__bk_46__container_name":"unify-query","_value_":3526,"_timestamp_":1744673190000},{"__ext__bk_46__container_name":"unify-query","_value_":4200,"_timestamp_":1744673220000},{"__ext__bk_46__container_name":"unify-query","_value_":3302,"_timestamp_":1744673250000},{"__ext__bk_46__container_name":"unify-query","_value_":4370,"_timestamp_":1744673280000},{"__ext__bk_46__container_name":"unify-query","_value_":3462,"_timestamp_":1744673310000},{"__ext__bk_46__container_name":"unify-query","_value_":4223,"_timestamp_":1744673340000},{"__ext__bk_46__container_name":"unify-query","_value_":3564,"_timestamp_":1744673370000},{"__ext__bk_46__container_name":"unify-query","_value_":12072,"_timestamp_":1744673400000},{"__ext__bk_46__container_name":"unify-query","_value_":17986,"_timestamp_":1744673430000},{"__ext__bk_46__container_name":"unify-query","_value_":4089,"_timestamp_":1744673460000},{"__ext__bk_46__container_name":"unify-query","_value_":12000,"_timestamp_":1744673490000},{"__ext__bk_46__container_name":"unify-query","_value_":4790,"_timestamp_":1744673520000},{"__ext__bk_46__container_name":"unify-query","_value_":3637,"_timestamp_":1744673550000},{"__ext__bk_46__container_name":"unify-query","_value_":4177,"_timestamp_":1744673580000},{"__ext__bk_46__container_name":"unify-query","_value_":3438,"_timestamp_":1744673610000},{"__ext__bk_46__container_name":"unify-query","_value_":4465,"_timestamp_":1744673640000},{"__ext__bk_46__container_name":"unify-query","_value_":3627,"_timestamp_":1744673670000},{"__ext__bk_46__container_name":"unify-query","_value_":4131,"_timestamp_":1744673700000},{"__ext__bk_46__container_name":"unify-query","_value_":3396,"_timestamp_":1744673730000},{"__ext__bk_46__container_name":"unify-query","_value_":4395,"_timestamp_":1744673760000},{"__ext__bk_46__container_name":"unify-query","_value_":3638,"_timestamp_":1744673790000},{"__ext__bk_46__container_name":"unify-query","_value_":4093,"_timestamp_":1744673820000},{"__ext__bk_46__container_name":"unify-query","_value_":3584,"_timestamp_":1744673850000},{"__ext__bk_46__container_name":"unify-query","_value_":4082,"_timestamp_":1744673880000},{"__ext__bk_46__container_name":"unify-query","_value_":3475,"_timestamp_":1744673910000},{"__ext__bk_46__container_name":"unify-query","_value_":4051,"_timestamp_":1744673940000},{"__ext__bk_46__container_name":"unify-query","_value_":3354,"_timestamp_":1744673970000},{"__ext__bk_46__container_name":"unify-query","_value_":6296,"_timestamp_":1744674000000},{"__ext__bk_46__container_name":"unify-query","_value_":3473,"_timestamp_":1744674030000},{"__ext__bk_46__container_name":"unify-query","_value_":4412,"_timestamp_":1744674060000},{"__ext__bk_46__container_name":"unify-query","_value_":3793,"_timestamp_":1744674090000},{"__ext__bk_46__container_name":"unify-query","_value_":4391,"_timestamp_":1744674120000},{"__ext__bk_46__container_name":"unify-query","_value_":3836,"_timestamp_":1744674150000},{"__ext__bk_46__container_name":"unify-query","_value_":4190,"_timestamp_":1744674180000},{"__ext__bk_46__container_name":"unify-query","_value_":3478,"_timestamp_":1744674210000},{"__ext__bk_46__container_name":"unify-query","_value_":4230,"_timestamp_":1744674240000},{"__ext__bk_46__container_name":"unify-query","_value_":3488,"_timestamp_":1744674270000},{"__ext__bk_46__container_name":"unify-query","_value_":4964,"_timestamp_":1744674300000},{"__ext__bk_46__container_name":"unify-query","_value_":3455,"_timestamp_":1744674330000},{"__ext__bk_46__container_name":"unify-query","_value_":4116,"_timestamp_":1744674360000},{"__ext__bk_46__container_name":"unify-query","_value_":3250,"_timestamp_":1744674390000},{"__ext__bk_46__container_name":"unify-query","_value_":4494,"_timestamp_":1744674420000},{"__ext__bk_46__container_name":"unify-query","_value_":3326,"_timestamp_":1744674450000},{"__ext__bk_46__container_name":"unify-query","_value_":4590,"_timestamp_":1744674480000},{"__ext__bk_46__container_name":"unify-query","_value_":3580,"_timestamp_":1744674510000},{"__ext__bk_46__container_name":"unify-query","_value_":4368,"_timestamp_":1744674540000},{"__ext__bk_46__container_name":"unify-query","_value_":3685,"_timestamp_":1744674570000},{"__ext__bk_46__container_name":"unify-query","_value_":4381,"_timestamp_":1744674600000},{"__ext__bk_46__container_name":"unify-query","_value_":3699,"_timestamp_":1744674630000},{"__ext__bk_46__container_name":"unify-query","_value_":4513,"_timestamp_":1744674660000},{"__ext__bk_46__container_name":"unify-query","_value_":3729,"_timestamp_":1744674690000},{"__ext__bk_46__container_name":"unify-query","_value_":4500,"_timestamp_":1744674720000},{"__ext__bk_46__container_name":"unify-query","_value_":3639,"_timestamp_":1744674750000},{"__ext__bk_46__container_name":"unify-query","_value_":4018,"_timestamp_":1744674780000},{"__ext__bk_46__container_name":"unify-query","_value_":3587,"_timestamp_":1744674810000},{"__ext__bk_46__container_name":"unify-query","_value_":4168,"_timestamp_":1744674840000},{"__ext__bk_46__container_name":"unify-query","_value_":3389,"_timestamp_":1744674870000},{"__ext__bk_46__container_name":"unify-query","_value_":4289,"_timestamp_":1744674900000},{"__ext__bk_46__container_name":"unify-query","_value_":3540,"_timestamp_":1744674930000},{"__ext__bk_46__container_name":"unify-query","_value_":4106,"_timestamp_":1744674960000},{"__ext__bk_46__container_name":"unify-query","_value_":3478,"_timestamp_":1744674990000},{"__ext__bk_46__container_name":"unify-query","_value_":4268,"_timestamp_":1744675020000},{"__ext__bk_46__container_name":"unify-query","_value_":3577,"_timestamp_":1744675050000},{"__ext__bk_46__container_name":"unify-query","_value_":4087,"_timestamp_":1744675080000},{"__ext__bk_46__container_name":"unify-query","_value_":3511,"_timestamp_":1744675110000},{"__ext__bk_46__container_name":"unify-query","_value_":4174,"_timestamp_":1744675140000},{"__ext__bk_46__container_name":"unify-query","_value_":3573,"_timestamp_":1744675170000},{"__ext__bk_46__container_name":"unify-query","_value_":17095,"_timestamp_":1744675200000},{"__ext__bk_46__container_name":"unify-query","_value_":14907,"_timestamp_":1744675230000},{"__ext__bk_46__container_name":"unify-query","_value_":6455,"_timestamp_":1744675260000},{"__ext__bk_46__container_name":"unify-query","_value_":9818,"_timestamp_":1744675290000},{"__ext__bk_46__container_name":"unify-query","_value_":5253,"_timestamp_":1744675320000},{"__ext__bk_46__container_name":"unify-query","_value_":3567,"_timestamp_":1744675350000},{"__ext__bk_46__container_name":"unify-query","_value_":4047,"_timestamp_":1744675380000},{"__ext__bk_46__container_name":"unify-query","_value_":3342,"_timestamp_":1744675410000},{"__ext__bk_46__container_name":"unify-query","_value_":4605,"_timestamp_":1744675440000},{"__ext__bk_46__container_name":"unify-query","_value_":3394,"_timestamp_":1744675470000},{"__ext__bk_46__container_name":"unify-query","_value_":4260,"_timestamp_":1744675500000},{"__ext__bk_46__container_name":"unify-query","_value_":3373,"_timestamp_":1744675530000},{"__ext__bk_46__container_name":"unify-query","_value_":4341,"_timestamp_":1744675560000},{"__ext__bk_46__container_name":"unify-query","_value_":3559,"_timestamp_":1744675590000},{"__ext__bk_46__container_name":"unify-query","_value_":4188,"_timestamp_":1744675620000},{"__ext__bk_46__container_name":"unify-query","_value_":3519,"_timestamp_":1744675650000},{"__ext__bk_46__container_name":"unify-query","_value_":4143,"_timestamp_":1744675680000},{"__ext__bk_46__container_name":"unify-query","_value_":3630,"_timestamp_":1744675710000},{"__ext__bk_46__container_name":"unify-query","_value_":4042,"_timestamp_":1744675740000},{"__ext__bk_46__container_name":"unify-query","_value_":3653,"_timestamp_":1744675770000},{"__ext__bk_46__container_name":"unify-query","_value_":4358,"_timestamp_":1744675800000},{"__ext__bk_46__container_name":"unify-query","_value_":3688,"_timestamp_":1744675830000},{"__ext__bk_46__container_name":"unify-query","_value_":4450,"_timestamp_":1744675860000},{"__ext__bk_46__container_name":"unify-query","_value_":3387,"_timestamp_":1744675890000},{"__ext__bk_46__container_name":"unify-query","_value_":4864,"_timestamp_":1744675920000},{"__ext__bk_46__container_name":"unify-query","_value_":3629,"_timestamp_":1744675950000},{"__ext__bk_46__container_name":"unify-query","_value_":4127,"_timestamp_":1744675980000},{"__ext__bk_46__container_name":"unify-query","_value_":3424,"_timestamp_":1744676010000},{"__ext__bk_46__container_name":"unify-query","_value_":4267,"_timestamp_":1744676040000},{"__ext__bk_46__container_name":"unify-query","_value_":3328,"_timestamp_":1744676070000},{"__ext__bk_46__container_name":"unify-query","_value_":5128,"_timestamp_":1744676100000},{"__ext__bk_46__container_name":"unify-query","_value_":3657,"_timestamp_":1744676130000},{"__ext__bk_46__container_name":"unify-query","_value_":4185,"_timestamp_":1744676160000},{"__ext__bk_46__container_name":"unify-query","_value_":3336,"_timestamp_":1744676190000},{"__ext__bk_46__container_name":"unify-query","_value_":4532,"_timestamp_":1744676220000},{"__ext__bk_46__container_name":"unify-query","_value_":3700,"_timestamp_":1744676250000},{"__ext__bk_46__container_name":"unify-query","_value_":4174,"_timestamp_":1744676280000},{"__ext__bk_46__container_name":"unify-query","_value_":3318,"_timestamp_":1744676310000},{"__ext__bk_46__container_name":"unify-query","_value_":4463,"_timestamp_":1744676340000},{"__ext__bk_46__container_name":"unify-query","_value_":3502,"_timestamp_":1744676370000},{"__ext__bk_46__container_name":"unify-query","_value_":6064,"_timestamp_":1744676400000},{"__ext__bk_46__container_name":"unify-query","_value_":3292,"_timestamp_":1744676430000},{"__ext__bk_46__container_name":"unify-query","_value_":4858,"_timestamp_":1744676460000},{"__ext__bk_46__container_name":"unify-query","_value_":3543,"_timestamp_":1744676490000},{"__ext__bk_46__container_name":"unify-query","_value_":4620,"_timestamp_":1744676520000},{"__ext__bk_46__container_name":"unify-query","_value_":3750,"_timestamp_":1744676550000},{"__ext__bk_46__container_name":"unify-query","_value_":4043,"_timestamp_":1744676580000},{"__ext__bk_46__container_name":"unify-query","_value_":3595,"_timestamp_":1744676610000},{"__ext__bk_46__container_name":"unify-query","_value_":4152,"_timestamp_":1744676640000},{"__ext__bk_46__container_name":"unify-query","_value_":3550,"_timestamp_":1744676670000},{"__ext__bk_46__container_name":"unify-query","_value_":4011,"_timestamp_":1744676700000},{"__ext__bk_46__container_name":"unify-query","_value_":3502,"_timestamp_":1744676730000},{"__ext__bk_46__container_name":"unify-query","_value_":4050,"_timestamp_":1744676760000},{"__ext__bk_46__container_name":"unify-query","_value_":3118,"_timestamp_":1744676790000},{"__ext__bk_46__container_name":"unify-query","_value_":4628,"_timestamp_":1744676820000},{"__ext__bk_46__container_name":"unify-query","_value_":3441,"_timestamp_":1744676850000},{"__ext__bk_46__container_name":"unify-query","_value_":4366,"_timestamp_":1744676880000},{"__ext__bk_46__container_name":"unify-query","_value_":3500,"_timestamp_":1744676910000},{"__ext__bk_46__container_name":"unify-query","_value_":4160,"_timestamp_":1744676940000},{"__ext__bk_46__container_name":"unify-query","_value_":3662,"_timestamp_":1744676970000},{"__ext__bk_46__container_name":"unify-query","_value_":11392,"_timestamp_":1744677000000},{"__ext__bk_46__container_name":"unify-query","_value_":18649,"_timestamp_":1744677030000},{"__ext__bk_46__container_name":"unify-query","_value_":7107,"_timestamp_":1744677060000},{"__ext__bk_46__container_name":"unify-query","_value_":9213,"_timestamp_":1744677090000},{"__ext__bk_46__container_name":"unify-query","_value_":4235,"_timestamp_":1744677120000},{"__ext__bk_46__container_name":"unify-query","_value_":3623,"_timestamp_":1744677150000},{"__ext__bk_46__container_name":"unify-query","_value_":4412,"_timestamp_":1744677180000},{"__ext__bk_46__container_name":"unify-query","_value_":3436,"_timestamp_":1744677210000},{"__ext__bk_46__container_name":"unify-query","_value_":4233,"_timestamp_":1744677240000},{"__ext__bk_46__container_name":"unify-query","_value_":3440,"_timestamp_":1744677270000},{"__ext__bk_46__container_name":"unify-query","_value_":4383,"_timestamp_":1744677300000},{"__ext__bk_46__container_name":"unify-query","_value_":3507,"_timestamp_":1744677330000},{"__ext__bk_46__container_name":"unify-query","_value_":4288,"_timestamp_":1744677360000},{"__ext__bk_46__container_name":"unify-query","_value_":3197,"_timestamp_":1744677390000},{"__ext__bk_46__container_name":"unify-query","_value_":4605,"_timestamp_":1744677420000},{"__ext__bk_46__container_name":"unify-query","_value_":3249,"_timestamp_":1744677450000},{"__ext__bk_46__container_name":"unify-query","_value_":4421,"_timestamp_":1744677480000},{"__ext__bk_46__container_name":"unify-query","_value_":2998,"_timestamp_":1744677510000},{"__ext__bk_46__container_name":"unify-query","_value_":4700,"_timestamp_":1744677540000},{"__ext__bk_46__container_name":"unify-query","_value_":3598,"_timestamp_":1744677570000},{"__ext__bk_46__container_name":"unify-query","_value_":5781,"_timestamp_":1744677600000},{"__ext__bk_46__container_name":"unify-query","_value_":3734,"_timestamp_":1744677630000},{"__ext__bk_46__container_name":"unify-query","_value_":4510,"_timestamp_":1744677660000},{"__ext__bk_46__container_name":"unify-query","_value_":3752,"_timestamp_":1744677690000},{"__ext__bk_46__container_name":"unify-query","_value_":4447,"_timestamp_":1744677720000},{"__ext__bk_46__container_name":"unify-query","_value_":3523,"_timestamp_":1744677750000},{"__ext__bk_46__container_name":"unify-query","_value_":4187,"_timestamp_":1744677780000},{"__ext__bk_46__container_name":"unify-query","_value_":3640,"_timestamp_":1744677810000},{"__ext__bk_46__container_name":"unify-query","_value_":3900,"_timestamp_":1744677840000},{"__ext__bk_46__container_name":"unify-query","_value_":3514,"_timestamp_":1744677870000},{"__ext__bk_46__container_name":"unify-query","_value_":4863,"_timestamp_":1744677900000},{"__ext__bk_46__container_name":"unify-query","_value_":3565,"_timestamp_":1744677930000},{"__ext__bk_46__container_name":"unify-query","_value_":4335,"_timestamp_":1744677960000},{"__ext__bk_46__container_name":"unify-query","_value_":3533,"_timestamp_":1744677990000},{"__ext__bk_46__container_name":"unify-query","_value_":4307,"_timestamp_":1744678020000},{"__ext__bk_46__container_name":"unify-query","_value_":3556,"_timestamp_":1744678050000},{"__ext__bk_46__container_name":"unify-query","_value_":4179,"_timestamp_":1744678080000},{"__ext__bk_46__container_name":"unify-query","_value_":3664,"_timestamp_":1744678110000},{"__ext__bk_46__container_name":"unify-query","_value_":4362,"_timestamp_":1744678140000},{"__ext__bk_46__container_name":"unify-query","_value_":3222,"_timestamp_":1744678170000},{"__ext__bk_46__container_name":"unify-query","_value_":4750,"_timestamp_":1744678200000},{"__ext__bk_46__container_name":"unify-query","_value_":3546,"_timestamp_":1744678230000},{"__ext__bk_46__container_name":"unify-query","_value_":4601,"_timestamp_":1744678260000},{"__ext__bk_46__container_name":"unify-query","_value_":3702,"_timestamp_":1744678290000},{"__ext__bk_46__container_name":"unify-query","_value_":4564,"_timestamp_":1744678320000},{"__ext__bk_46__container_name":"unify-query","_value_":3610,"_timestamp_":1744678350000},{"__ext__bk_46__container_name":"unify-query","_value_":4130,"_timestamp_":1744678380000},{"__ext__bk_46__container_name":"unify-query","_value_":3412,"_timestamp_":1744678410000},{"__ext__bk_46__container_name":"unify-query","_value_":4614,"_timestamp_":1744678440000},{"__ext__bk_46__container_name":"unify-query","_value_":3522,"_timestamp_":1744678470000},{"__ext__bk_46__container_name":"unify-query","_value_":4148,"_timestamp_":1744678500000},{"__ext__bk_46__container_name":"unify-query","_value_":3408,"_timestamp_":1744678530000},{"__ext__bk_46__container_name":"unify-query","_value_":4261,"_timestamp_":1744678560000},{"__ext__bk_46__container_name":"unify-query","_value_":3607,"_timestamp_":1744678590000},{"__ext__bk_46__container_name":"unify-query","_value_":4172,"_timestamp_":1744678620000},{"__ext__bk_46__container_name":"unify-query","_value_":3529,"_timestamp_":1744678650000},{"__ext__bk_46__container_name":"unify-query","_value_":4227,"_timestamp_":1744678680000},{"__ext__bk_46__container_name":"unify-query","_value_":3487,"_timestamp_":1744678710000},{"__ext__bk_46__container_name":"unify-query","_value_":4298,"_timestamp_":1744678740000},{"__ext__bk_46__container_name":"unify-query","_value_":3609,"_timestamp_":1744678770000},{"__ext__bk_46__container_name":"unify-query","_value_":7230,"_timestamp_":1744678800000},{"__ext__bk_46__container_name":"unify-query","_value_":3818,"_timestamp_":1744678830000},{"__ext__bk_46__container_name":"unify-query","_value_":11924,"_timestamp_":1744678860000},{"__ext__bk_46__container_name":"unify-query","_value_":27269,"_timestamp_":1744678890000},{"__ext__bk_46__container_name":"unify-query","_value_":5073,"_timestamp_":1744678920000},{"__ext__bk_46__container_name":"unify-query","_value_":3474,"_timestamp_":1744678950000},{"__ext__bk_46__container_name":"unify-query","_value_":4474,"_timestamp_":1744678980000},{"__ext__bk_46__container_name":"unify-query","_value_":3536,"_timestamp_":1744679010000},{"__ext__bk_46__container_name":"unify-query","_value_":4525,"_timestamp_":1744679040000},{"__ext__bk_46__container_name":"unify-query","_value_":3503,"_timestamp_":1744679070000},{"__ext__bk_46__container_name":"unify-query","_value_":4194,"_timestamp_":1744679100000},{"__ext__bk_46__container_name":"unify-query","_value_":3557,"_timestamp_":1744679130000},{"__ext__bk_46__container_name":"unify-query","_value_":4259,"_timestamp_":1744679160000},{"__ext__bk_46__container_name":"unify-query","_value_":3611,"_timestamp_":1744679190000},{"__ext__bk_46__container_name":"unify-query","_value_":4218,"_timestamp_":1744679220000},{"__ext__bk_46__container_name":"unify-query","_value_":3622,"_timestamp_":1744679250000},{"__ext__bk_46__container_name":"unify-query","_value_":4417,"_timestamp_":1744679280000},{"__ext__bk_46__container_name":"unify-query","_value_":3730,"_timestamp_":1744679310000},{"__ext__bk_46__container_name":"unify-query","_value_":4204,"_timestamp_":1744679340000},{"__ext__bk_46__container_name":"unify-query","_value_":3641,"_timestamp_":1744679370000},{"__ext__bk_46__container_name":"unify-query","_value_":4849,"_timestamp_":1744679400000},{"__ext__bk_46__container_name":"unify-query","_value_":3803,"_timestamp_":1744679430000},{"__ext__bk_46__container_name":"unify-query","_value_":4398,"_timestamp_":1744679460000},{"__ext__bk_46__container_name":"unify-query","_value_":3674,"_timestamp_":1744679490000},{"__ext__bk_46__container_name":"unify-query","_value_":4727,"_timestamp_":1744679520000},{"__ext__bk_46__container_name":"unify-query","_value_":3926,"_timestamp_":1744679550000},{"__ext__bk_46__container_name":"unify-query","_value_":4173,"_timestamp_":1744679580000},{"__ext__bk_46__container_name":"unify-query","_value_":3531,"_timestamp_":1744679610000},{"__ext__bk_46__container_name":"unify-query","_value_":4968,"_timestamp_":1744679640000},{"__ext__bk_46__container_name":"unify-query","_value_":3432,"_timestamp_":1744679670000},{"__ext__bk_46__container_name":"unify-query","_value_":5059,"_timestamp_":1744679700000},{"__ext__bk_46__container_name":"unify-query","_value_":3560,"_timestamp_":1744679730000},{"__ext__bk_46__container_name":"unify-query","_value_":4087,"_timestamp_":1744679760000},{"__ext__bk_46__container_name":"unify-query","_value_":3590,"_timestamp_":1744679790000},{"__ext__bk_46__container_name":"unify-query","_value_":4436,"_timestamp_":1744679820000},{"__ext__bk_46__container_name":"unify-query","_value_":5299,"_timestamp_":1744679850000},{"__ext__bk_46__container_name":"unify-query","_value_":4320,"_timestamp_":1744679880000},{"__ext__bk_46__container_name":"unify-query","_value_":3861,"_timestamp_":1744679910000},{"__ext__bk_46__container_name":"unify-query","_value_":4511,"_timestamp_":1744679940000},{"__ext__bk_46__container_name":"unify-query","_value_":3711,"_timestamp_":1744679970000},{"__ext__bk_46__container_name":"unify-query","_value_":6021,"_timestamp_":1744680000000},{"__ext__bk_46__container_name":"unify-query","_value_":3942,"_timestamp_":1744680030000},{"__ext__bk_46__container_name":"unify-query","_value_":4800,"_timestamp_":1744680060000},{"__ext__bk_46__container_name":"unify-query","_value_":3681,"_timestamp_":1744680090000},{"__ext__bk_46__container_name":"unify-query","_value_":4592,"_timestamp_":1744680120000},{"__ext__bk_46__container_name":"unify-query","_value_":3560,"_timestamp_":1744680150000},{"__ext__bk_46__container_name":"unify-query","_value_":4194,"_timestamp_":1744680180000},{"__ext__bk_46__container_name":"unify-query","_value_":3490,"_timestamp_":1744680210000},{"__ext__bk_46__container_name":"unify-query","_value_":4971,"_timestamp_":1744680240000},{"__ext__bk_46__container_name":"unify-query","_value_":4009,"_timestamp_":1744680270000},{"__ext__bk_46__container_name":"unify-query","_value_":4837,"_timestamp_":1744680300000},{"__ext__bk_46__container_name":"unify-query","_value_":3227,"_timestamp_":1744680330000},{"__ext__bk_46__container_name":"unify-query","_value_":4531,"_timestamp_":1744680360000},{"__ext__bk_46__container_name":"unify-query","_value_":2888,"_timestamp_":1744680390000},{"__ext__bk_46__container_name":"unify-query","_value_":5083,"_timestamp_":1744680420000},{"__ext__bk_46__container_name":"unify-query","_value_":3557,"_timestamp_":1744680450000},{"__ext__bk_46__container_name":"unify-query","_value_":4207,"_timestamp_":1744680480000},{"__ext__bk_46__container_name":"unify-query","_value_":3373,"_timestamp_":1744680510000},{"__ext__bk_46__container_name":"unify-query","_value_":4482,"_timestamp_":1744680540000},{"__ext__bk_46__container_name":"unify-query","_value_":3110,"_timestamp_":1744680570000},{"__ext__bk_46__container_name":"unify-query","_value_":13551,"_timestamp_":1744680600000},{"__ext__bk_46__container_name":"unify-query","_value_":17159,"_timestamp_":1744680630000},{"__ext__bk_46__container_name":"unify-query","_value_":6284,"_timestamp_":1744680660000},{"__ext__bk_46__container_name":"unify-query","_value_":9924,"_timestamp_":1744680690000},{"__ext__bk_46__container_name":"unify-query","_value_":4547,"_timestamp_":1744680720000},{"__ext__bk_46__container_name":"unify-query","_value_":3474,"_timestamp_":1744680750000},{"__ext__bk_46__container_name":"unify-query","_value_":4312,"_timestamp_":1744680780000},{"__ext__bk_46__container_name":"unify-query","_value_":3689,"_timestamp_":1744680810000},{"__ext__bk_46__container_name":"unify-query","_value_":4680,"_timestamp_":1744680840000},{"__ext__bk_46__container_name":"unify-query","_value_":3609,"_timestamp_":1744680870000},{"__ext__bk_46__container_name":"unify-query","_value_":4886,"_timestamp_":1744680900000},{"__ext__bk_46__container_name":"unify-query","_value_":3842,"_timestamp_":1744680930000},{"__ext__bk_46__container_name":"unify-query","_value_":4810,"_timestamp_":1744680960000},{"__ext__bk_46__container_name":"unify-query","_value_":4102,"_timestamp_":1744680990000},{"__ext__bk_46__container_name":"unify-query","_value_":4594,"_timestamp_":1744681020000},{"__ext__bk_46__container_name":"unify-query","_value_":4168,"_timestamp_":1744681050000},{"__ext__bk_46__container_name":"unify-query","_value_":4562,"_timestamp_":1744681080000},{"__ext__bk_46__container_name":"unify-query","_value_":4506,"_timestamp_":1744681110000},{"__ext__bk_46__container_name":"unify-query","_value_":5243,"_timestamp_":1744681140000},{"__ext__bk_46__container_name":"unify-query","_value_":5135,"_timestamp_":1744681170000},{"__ext__bk_46__container_name":"unify-query","_value_":6671,"_timestamp_":1744681200000},{"__ext__bk_46__container_name":"unify-query","_value_":3806,"_timestamp_":1744681230000},{"__ext__bk_46__container_name":"unify-query","_value_":4535,"_timestamp_":1744681260000},{"__ext__bk_46__container_name":"unify-query","_value_":3721,"_timestamp_":1744681290000},{"__ext__bk_46__container_name":"unify-query","_value_":4799,"_timestamp_":1744681320000},{"__ext__bk_46__container_name":"unify-query","_value_":3909,"_timestamp_":1744681350000},{"__ext__bk_46__container_name":"unify-query","_value_":4261,"_timestamp_":1744681380000},{"__ext__bk_46__container_name":"unify-query","_value_":3671,"_timestamp_":1744681410000},{"__ext__bk_46__container_name":"unify-query","_value_":4359,"_timestamp_":1744681440000},{"__ext__bk_46__container_name":"unify-query","_value_":4063,"_timestamp_":1744681470000},{"__ext__bk_46__container_name":"unify-query","_value_":5231,"_timestamp_":1744681500000},{"__ext__bk_46__container_name":"unify-query","_value_":3778,"_timestamp_":1744681530000},{"__ext__bk_46__container_name":"unify-query","_value_":4684,"_timestamp_":1744681560000},{"__ext__bk_46__container_name":"unify-query","_value_":4072,"_timestamp_":1744681590000},{"__ext__bk_46__container_name":"unify-query","_value_":5029,"_timestamp_":1744681620000},{"__ext__bk_46__container_name":"unify-query","_value_":3700,"_timestamp_":1744681650000},{"__ext__bk_46__container_name":"unify-query","_value_":4670,"_timestamp_":1744681680000},{"__ext__bk_46__container_name":"unify-query","_value_":3557,"_timestamp_":1744681710000},{"__ext__bk_46__container_name":"unify-query","_value_":4590,"_timestamp_":1744681740000},{"__ext__bk_46__container_name":"unify-query","_value_":3041,"_timestamp_":1744681770000},{"__ext__bk_46__container_name":"unify-query","_value_":5043,"_timestamp_":1744681800000},{"__ext__bk_46__container_name":"unify-query","_value_":3530,"_timestamp_":1744681830000},{"__ext__bk_46__container_name":"unify-query","_value_":6807,"_timestamp_":1744681860000},{"__ext__bk_46__container_name":"unify-query","_value_":4455,"_timestamp_":1744681890000},{"__ext__bk_46__container_name":"unify-query","_value_":6841,"_timestamp_":1744681920000},{"__ext__bk_46__container_name":"unify-query","_value_":4519,"_timestamp_":1744681950000},{"__ext__bk_46__container_name":"unify-query","_value_":6617,"_timestamp_":1744681980000},{"__ext__bk_46__container_name":"unify-query","_value_":4633,"_timestamp_":1744682010000},{"__ext__bk_46__container_name":"unify-query","_value_":5997,"_timestamp_":1744682040000},{"__ext__bk_46__container_name":"unify-query","_value_":4446,"_timestamp_":1744682070000},{"__ext__bk_46__container_name":"unify-query","_value_":5569,"_timestamp_":1744682100000},{"__ext__bk_46__container_name":"unify-query","_value_":4324,"_timestamp_":1744682130000},{"__ext__bk_46__container_name":"unify-query","_value_":5354,"_timestamp_":1744682160000},{"__ext__bk_46__container_name":"unify-query","_value_":7245,"_timestamp_":1744682190000},{"__ext__bk_46__container_name":"unify-query","_value_":5258,"_timestamp_":1744682220000},{"__ext__bk_46__container_name":"unify-query","_value_":4296,"_timestamp_":1744682250000},{"__ext__bk_46__container_name":"unify-query","_value_":5349,"_timestamp_":1744682280000},{"__ext__bk_46__container_name":"unify-query","_value_":4479,"_timestamp_":1744682310000},{"__ext__bk_46__container_name":"unify-query","_value_":5127,"_timestamp_":1744682340000},{"__ext__bk_46__container_name":"unify-query","_value_":4006,"_timestamp_":1744682370000},{"__ext__bk_46__container_name":"unify-query","_value_":19058,"_timestamp_":1744682400000},{"__ext__bk_46__container_name":"unify-query","_value_":14501,"_timestamp_":1744682430000},{"__ext__bk_46__container_name":"unify-query","_value_":3810,"_timestamp_":1744682460000},{"__ext__bk_46__container_name":"unify-query","_value_":12368,"_timestamp_":1744682490000},{"__ext__bk_46__container_name":"unify-query","_value_":6976,"_timestamp_":1744682520000},{"__ext__bk_46__container_name":"unify-query","_value_":4399,"_timestamp_":1744682550000},{"__ext__bk_46__container_name":"unify-query","_value_":5482,"_timestamp_":1744682580000},{"__ext__bk_46__container_name":"unify-query","_value_":4524,"_timestamp_":1744682610000},{"__ext__bk_46__container_name":"unify-query","_value_":5478,"_timestamp_":1744682640000},{"__ext__bk_46__container_name":"unify-query","_value_":4920,"_timestamp_":1744682670000},{"__ext__bk_46__container_name":"unify-query","_value_":5347,"_timestamp_":1744682700000},{"__ext__bk_46__container_name":"unify-query","_value_":4427,"_timestamp_":1744682730000},{"__ext__bk_46__container_name":"unify-query","_value_":5102,"_timestamp_":1744682760000},{"__ext__bk_46__container_name":"unify-query","_value_":4441,"_timestamp_":1744682790000},{"__ext__bk_46__container_name":"unify-query","_value_":5596,"_timestamp_":1744682820000},{"__ext__bk_46__container_name":"unify-query","_value_":4888,"_timestamp_":1744682850000},{"__ext__bk_46__container_name":"unify-query","_value_":5306,"_timestamp_":1744682880000},{"__ext__bk_46__container_name":"unify-query","_value_":4825,"_timestamp_":1744682910000},{"__ext__bk_46__container_name":"unify-query","_value_":5897,"_timestamp_":1744682940000},{"__ext__bk_46__container_name":"unify-query","_value_":4481,"_timestamp_":1744682970000},{"__ext__bk_46__container_name":"unify-query","_value_":6086,"_timestamp_":1744683000000},{"__ext__bk_46__container_name":"unify-query","_value_":4910,"_timestamp_":1744683030000},{"__ext__bk_46__container_name":"unify-query","_value_":5676,"_timestamp_":1744683060000},{"__ext__bk_46__container_name":"unify-query","_value_":3626,"_timestamp_":1744683090000},{"__ext__bk_46__container_name":"unify-query","_value_":6929,"_timestamp_":1744683120000},{"__ext__bk_46__container_name":"unify-query","_value_":4601,"_timestamp_":1744683150000},{"__ext__bk_46__container_name":"unify-query","_value_":5525,"_timestamp_":1744683180000},{"__ext__bk_46__container_name":"unify-query","_value_":4500,"_timestamp_":1744683210000},{"__ext__bk_46__container_name":"unify-query","_value_":5617,"_timestamp_":1744683240000},{"__ext__bk_46__container_name":"unify-query","_value_":4503,"_timestamp_":1744683270000},{"__ext__bk_46__container_name":"unify-query","_value_":6328,"_timestamp_":1744683300000},{"__ext__bk_46__container_name":"unify-query","_value_":4557,"_timestamp_":1744683330000},{"__ext__bk_46__container_name":"unify-query","_value_":5356,"_timestamp_":1744683360000},{"__ext__bk_46__container_name":"unify-query","_value_":4413,"_timestamp_":1744683390000},{"__ext__bk_46__container_name":"unify-query","_value_":5335,"_timestamp_":1744683420000},{"__ext__bk_46__container_name":"unify-query","_value_":4640,"_timestamp_":1744683450000},{"__ext__bk_46__container_name":"unify-query","_value_":5399,"_timestamp_":1744683480000},{"__ext__bk_46__container_name":"unify-query","_value_":4298,"_timestamp_":1744683510000},{"__ext__bk_46__container_name":"unify-query","_value_":5415,"_timestamp_":1744683540000},{"__ext__bk_46__container_name":"unify-query","_value_":4540,"_timestamp_":1744683570000},{"__ext__bk_46__container_name":"unify-query","_value_":6949,"_timestamp_":1744683600000},{"__ext__bk_46__container_name":"unify-query","_value_":4574,"_timestamp_":1744683630000},{"__ext__bk_46__container_name":"unify-query","_value_":5757,"_timestamp_":1744683660000},{"__ext__bk_46__container_name":"unify-query","_value_":4669,"_timestamp_":1744683690000},{"__ext__bk_46__container_name":"unify-query","_value_":5706,"_timestamp_":1744683720000},{"__ext__bk_46__container_name":"unify-query","_value_":4472,"_timestamp_":1744683750000},{"__ext__bk_46__container_name":"unify-query","_value_":5386,"_timestamp_":1744683780000},{"__ext__bk_46__container_name":"unify-query","_value_":4490,"_timestamp_":1744683810000},{"__ext__bk_46__container_name":"unify-query","_value_":5104,"_timestamp_":1744683840000},{"__ext__bk_46__container_name":"unify-query","_value_":4201,"_timestamp_":1744683870000},{"__ext__bk_46__container_name":"unify-query","_value_":5979,"_timestamp_":1744683900000},{"__ext__bk_46__container_name":"unify-query","_value_":4853,"_timestamp_":1744683930000},{"__ext__bk_46__container_name":"unify-query","_value_":6691,"_timestamp_":1744683960000},{"__ext__bk_46__container_name":"unify-query","_value_":4572,"_timestamp_":1744683990000},{"__ext__bk_46__container_name":"unify-query","_value_":5554,"_timestamp_":1744684020000},{"__ext__bk_46__container_name":"unify-query","_value_":5244,"_timestamp_":1744684050000},{"__ext__bk_46__container_name":"unify-query","_value_":5392,"_timestamp_":1744684080000},{"__ext__bk_46__container_name":"unify-query","_value_":4550,"_timestamp_":1744684110000},{"__ext__bk_46__container_name":"unify-query","_value_":520,"_timestamp_":1744684140000}],"stage_elapsed_time_mills":{"check_query_syntax":2,"query_db":52,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":8,"connect_db":55,"match_query_routing_rule":0,"check_permission":73,"check_query_semantic":0,"pick_valid_storage":1},"total_record_size":269248,"timetaken":0.191,"result_schema":[{"field_type":"string","field_name":"__c0","field_alias":"__ext__bk_46__container_name","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"_value_","field_index":1},{"field_type":"long","field_name":"__c2","field_alias":"_timestamp_","field_index":2}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	for i, c := range map[string]struct {
		queryTs *structured.QueryTs
		result  string
	}{
		"查询 1 条原始数据，按照字段正向排序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         1,
						From:          0,
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
			},
			result: `{
  "is_partial":false,
  "series" : [ {
    "name" : "_result0",
    "metric_name" : "",
    "columns" : [ "_time", "_value" ],
    "types" : [ "float", "float" ],
    "group_keys" : [ "__ext.container_id", "__ext.container_image", "__ext.container_name", "__ext.io_kubernetes_pod", "__ext.io_kubernetes_pod_ip", "__ext.io_kubernetes_pod_namespace", "__ext.io_kubernetes_pod_uid", "__ext.io_kubernetes_workload_name", "__ext.io_kubernetes_workload_type", "__name__", "cloudid", "file", "gseindex", "iterationindex", "level", "log", "message", "path", "report_time", "serverip", "time", "trace_id" ],
    "group_values" : [ "375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2", "sha256:3a0506f06f1467e93c3a582203aac1a7501e77091572ec9612ddeee4a4dbbdb8", "unify-query", "bk-datalink-unify-query-6df8bcc4c9-rk4sc", "127.0.0.1", "blueking", "558c5b17-b221-47e1-aa66-036cc9b43e2a", "bk-datalink-unify-query-6df8bcc4c9", "ReplicaSet", "bklog:result_table:doris:gseIndex", "0", "http/handler.go:320", "2450131", "19", "info", "2025-04-14T20:22:59.982Z\tinfo\thttp/handler.go:320\t[5108397435e997364f8dc1251533e65e] header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[strategy:9155] Connection:[keep-alive] Content-Length:[863] Content-Type:[application/json] Traceparent:[00-5108397435e997364f8dc1251533e65e-ca18e72c0f0eafd4-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__2]], body: {\"space_uid\":\"bkcc__2\",\"query_list\":[{\"field_name\":\"bscp_config_consume_total_file_change_count\",\"is_regexp\":false,\"function\":[{\"method\":\"mean\",\"without\":false,\"dimensions\":[\"app\",\"biz\",\"clientType\"]}],\"time_aggregation\":{\"function\":\"increase\",\"window\":\"1m\"},\"is_dom_sampled\":false,\"reference_name\":\"a\",\"dimensions\":[\"app\",\"biz\",\"clientType\"],\"conditions\":{\"field_list\":[{\"field_name\":\"releaseChangeStatus\",\"value\":[\"Failed\"],\"op\":\"contains\"},{\"field_name\":\"bcs_cluster_id\",\"value\":[\"BCS-K8S-00000\"],\"op\":\"contains\"}],\"condition_list\":[\"and\"]},\"keep_columns\":[\"_time\",\"a\",\"app\",\"biz\",\"clientType\"],\"query_string\":\"\"}],\"metric_merge\":\"a\",\"start_time\":\"1744660260\",\"end_time\":\"1744662120\",\"step\":\"60s\",\"timezone\":\"Asia/Shanghai\",\"instant\":false}", " header: map[Accept:[*/*] Accept-Encoding:[gzip, deflate] Bk-Query-Source:[strategy:9155] Connection:[keep-alive] Content-Length:[863] Content-Type:[application/json] Traceparent:[00-5108397435e997364f8dc1251533e65e-ca18e72c0f0eafd4-00] User-Agent:[python-requests/2.31.0] X-Bk-Scope-Space-Uid:[bkcc__2]], body: {\"space_uid\":\"bkcc__2\",\"query_list\":[{\"field_name\":\"bscp_config_consume_total_file_change_count\",\"is_regexp\":false,\"function\":[{\"method\":\"mean\",\"without\":false,\"dimensions\":[\"app\",\"biz\",\"clientType\"]}],\"time_aggregation\":{\"function\":\"increase\",\"window\":\"1m\"},\"is_dom_sampled\":false,\"reference_name\":\"a\",\"dimensions\":[\"app\",\"biz\",\"clientType\"],\"conditions\":{\"field_list\":[{\"field_name\":\"releaseChangeStatus\",\"value\":[\"Failed\"],\"op\":\"contains\"},{\"field_name\":\"bcs_cluster_id\",\"value\":[\"BCS-K8S-00000\"],\"op\":\"contains\"}],\"condition_list\":[\"and\"]},\"keep_columns\":[\"_time\",\"a\",\"app\",\"biz\",\"clientType\"],\"query_string\":\"\"}],\"metric_merge\":\"a\",\"start_time\":\"1744660260\",\"end_time\":\"1744662120\",\"step\":\"60s\",\"timezone\":\"Asia/Shanghai\",\"instant\":false}", "/var/host/data/bcs/lib/docker/containers/375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2/375597ee636fd5d53cb7b0958823d9ba6534bd24cd698e485c41ca2f01b78ed2-json.log", "2025-04-14T20:22:59.982Z", "127.0.0.1", "1744662180000", "5108397435e997364f8dc1251533e65e" ],
    "values" : [ [ 1744662480000, 2450131 ] ]
  } ]
}`,
		},
		"根据维度 __ext.container_name 进行 count 聚合，同时用值正向排序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function: "count_over_time",
							Window:   "30s",
						},
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"__ext.container_name"},
							},
							{
								Method: "topk",
								VArgsList: []interface{}{
									5,
								},
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
				Step:        "30s",
			},
			result: `{
  "is_partial":false,
  "series" : [ {
    "name" : "_result0",
    "metric_name" : "",
    "columns" : [ "_time", "_value" ],
    "types" : [ "float", "float" ],
    "group_keys" : [ "__ext.container_name" ],
    "group_values" : [ "unify-query" ],
    "values" : [ [ 1744662510000, 3684 ], [ 1744662540000, 4012 ], [ 1744662570000, 3671 ], [ 1744662600000, 17092 ], [ 1744662630000, 12881 ], [ 1744662660000, 5902 ], [ 1744662690000, 10443 ], [ 1744662720000, 4388 ], [ 1744662750000, 3357 ], [ 1744662780000, 4381 ], [ 1744662810000, 3683 ], [ 1744662840000, 4353 ], [ 1744662870000, 3441 ], [ 1744662900000, 4251 ], [ 1744662930000, 3476 ], [ 1744662960000, 4036 ], [ 1744662990000, 3549 ], [ 1744663020000, 4351 ], [ 1744663050000, 3651 ], [ 1744663080000, 4096 ], [ 1744663110000, 3618 ], [ 1744663140000, 4100 ], [ 1744663170000, 3622 ], [ 1744663200000, 6044 ], [ 1744663230000, 3766 ], [ 1744663260000, 4461 ], [ 1744663290000, 3783 ], [ 1744663320000, 4559 ], [ 1744663350000, 3634 ], [ 1744663380000, 3869 ], [ 1744663410000, 3249 ], [ 1744663440000, 4473 ], [ 1744663470000, 3514 ], [ 1744663500000, 4923 ], [ 1744663530000, 3379 ], [ 1744663560000, 4489 ], [ 1744663590000, 3411 ], [ 1744663620000, 4374 ], [ 1744663650000, 3370 ], [ 1744663680000, 4310 ], [ 1744663710000, 3609 ], [ 1744663740000, 4318 ], [ 1744663770000, 3570 ], [ 1744663800000, 4334 ], [ 1744663830000, 3767 ], [ 1744663860000, 4455 ], [ 1744663890000, 3703 ], [ 1744663920000, 4511 ], [ 1744663950000, 3667 ], [ 1744663980000, 3998 ], [ 1744664010000, 3579 ], [ 1744664040000, 4156 ], [ 1744664070000, 3340 ], [ 1744664100000, 4344 ], [ 1744664130000, 3590 ], [ 1744664160000, 4161 ], [ 1744664190000, 3484 ], [ 1744664220000, 4273 ], [ 1744664250000, 3494 ], [ 1744664280000, 4230 ], [ 1744664310000, 3619 ], [ 1744664340000, 4013 ], [ 1744664370000, 3565 ], [ 1744664400000, 18144 ], [ 1744664430000, 13615 ], [ 1744664460000, 3178 ], [ 1744664490000, 13044 ], [ 1744664520000, 4767 ], [ 1744664550000, 3528 ], [ 1744664580000, 4316 ], [ 1744664610000, 3317 ], [ 1744664640000, 4395 ], [ 1744664670000, 3599 ], [ 1744664700000, 4149 ], [ 1744664730000, 3474 ], [ 1744664760000, 4201 ], [ 1744664790000, 3384 ], [ 1744664820000, 4442 ], [ 1744664850000, 3559 ], [ 1744664880000, 4166 ], [ 1744664910000, 3438 ], [ 1744664940000, 4244 ], [ 1744664970000, 3640 ], [ 1744665000000, 4305 ], [ 1744665030000, 3771 ], [ 1744665060000, 4485 ], [ 1744665090000, 3842 ], [ 1744665120000, 4423 ], [ 1744665150000, 3610 ], [ 1744665180000, 4125 ], [ 1744665210000, 3500 ], [ 1744665240000, 4252 ], [ 1744665270000, 3427 ], [ 1744665300000, 5089 ], [ 1744665330000, 3450 ], [ 1744665360000, 4349 ], [ 1744665390000, 3188 ], [ 1744665420000, 4556 ], [ 1744665450000, 3372 ], [ 1744665480000, 4408 ], [ 1744665510000, 3445 ], [ 1744665540000, 4213 ], [ 1744665570000, 3408 ], [ 1744665600000, 6235 ], [ 1744665630000, 3641 ], [ 1744665660000, 4577 ], [ 1744665690000, 3719 ], [ 1744665720000, 4548 ], [ 1744665750000, 3420 ], [ 1744665780000, 4246 ], [ 1744665810000, 3359 ], [ 1744665840000, 4332 ], [ 1744665870000, 3422 ], [ 1744665900000, 4229 ], [ 1744665930000, 3610 ], [ 1744665960000, 4119 ], [ 1744665990000, 3570 ], [ 1744666020000, 4144 ], [ 1744666050000, 3302 ], [ 1744666080000, 4398 ], [ 1744666110000, 3559 ], [ 1744666140000, 4097 ], [ 1744666170000, 3315 ], [ 1744666200000, 16721 ], [ 1744666230000, 13631 ], [ 1744666260000, 2982 ], [ 1744666290000, 11858 ], [ 1744666320000, 5515 ], [ 1744666350000, 2869 ], [ 1744666380000, 4795 ], [ 1744666410000, 3603 ], [ 1744666440000, 4204 ], [ 1744666470000, 3264 ], [ 1744666500000, 4377 ], [ 1744666530000, 3443 ], [ 1744666560000, 4307 ], [ 1744666590000, 3459 ], [ 1744666620000, 4342 ], [ 1744666650000, 3598 ], [ 1744666680000, 4052 ], [ 1744666710000, 3577 ], [ 1744666740000, 4128 ], [ 1744666770000, 3499 ], [ 1744666800000, 6209 ], [ 1744666830000, 3575 ], [ 1744666860000, 4543 ], [ 1744666890000, 3604 ], [ 1744666920000, 4579 ], [ 1744666950000, 3531 ], [ 1744666980000, 4314 ], [ 1744667010000, 3416 ], [ 1744667040000, 4320 ], [ 1744667070000, 3488 ], [ 1744667100000, 5054 ], [ 1744667130000, 3525 ], [ 1744667160000, 4313 ], [ 1744667190000, 3607 ], [ 1744667220000, 4118 ], [ 1744667250000, 3350 ], [ 1744667280000, 4280 ], [ 1744667310000, 3634 ], [ 1744667340000, 4174 ], [ 1744667370000, 3807 ], [ 1744667400000, 4358 ], [ 1744667430000, 3595 ], [ 1744667460000, 4630 ], [ 1744667490000, 3845 ], [ 1744667520000, 4361 ], [ 1744667550000, 3572 ], [ 1744667580000, 4095 ], [ 1744667610000, 3535 ], [ 1744667640000, 4200 ], [ 1744667670000, 3390 ], [ 1744667700000, 4262 ], [ 1744667730000, 3398 ], [ 1744667760000, 4320 ], [ 1744667790000, 3429 ], [ 1744667820000, 4288 ], [ 1744667850000, 3482 ], [ 1744667880000, 4166 ], [ 1744667910000, 3612 ], [ 1744667940000, 4194 ], [ 1744667970000, 3423 ], [ 1744668000000, 18203 ], [ 1744668030000, 13685 ], [ 1744668060000, 3281 ], [ 1744668090000, 12556 ], [ 1744668120000, 4893 ], [ 1744668150000, 3607 ], [ 1744668180000, 4336 ], [ 1744668210000, 3609 ], [ 1744668240000, 4097 ], [ 1744668270000, 3669 ], [ 1744668300000, 3997 ], [ 1744668330000, 3494 ], [ 1744668360000, 4172 ], [ 1744668390000, 3523 ], [ 1744668420000, 3877 ], [ 1744668450000, 3565 ], [ 1744668480000, 4230 ], [ 1744668510000, 3469 ], [ 1744668540000, 4243 ], [ 1744668570000, 3304 ], [ 1744668600000, 4690 ], [ 1744668630000, 3717 ], [ 1744668660000, 4618 ], [ 1744668690000, 3732 ], [ 1744668720000, 4477 ], [ 1744668750000, 3615 ], [ 1744668780000, 4154 ], [ 1744668810000, 3367 ], [ 1744668840000, 4193 ], [ 1744668870000, 3592 ], [ 1744668900000, 4971 ], [ 1744668930000, 3359 ], [ 1744668960000, 4540 ], [ 1744668990000, 3406 ], [ 1744669020000, 4375 ], [ 1744669050000, 3386 ], [ 1744669080000, 4281 ], [ 1744669110000, 3410 ], [ 1744669140000, 4545 ], [ 1744669170000, 3724 ], [ 1744669200000, 5903 ], [ 1744669230000, 3672 ], [ 1744669260000, 4413 ], [ 1744669290000, 3792 ], [ 1744669320000, 4422 ], [ 1744669350000, 3718 ], [ 1744669380000, 4213 ], [ 1744669410000, 3622 ], [ 1744669440000, 4043 ], [ 1744669470000, 3542 ], [ 1744669500000, 4179 ], [ 1744669530000, 3368 ], [ 1744669560000, 4354 ], [ 1744669590000, 3368 ], [ 1744669620000, 4229 ], [ 1744669650000, 3458 ], [ 1744669680000, 4310 ], [ 1744669710000, 3512 ], [ 1744669740000, 4188 ], [ 1744669770000, 3436 ], [ 1744669800000, 12171 ], [ 1744669830000, 18129 ], [ 1744669860000, 7142 ], [ 1744669890000, 9153 ], [ 1744669920000, 4566 ], [ 1744669950000, 3225 ], [ 1744669980000, 4378 ], [ 1744670010000, 3623 ], [ 1744670040000, 4266 ], [ 1744670070000, 3645 ], [ 1744670100000, 4043 ], [ 1744670130000, 3350 ], [ 1744670160000, 4333 ], [ 1744670190000, 3489 ], [ 1744670220000, 4303 ], [ 1744670250000, 3560 ], [ 1744670280000, 4121 ], [ 1744670310000, 3374 ], [ 1744670340000, 4362 ], [ 1744670370000, 3242 ], [ 1744670400000, 6416 ], [ 1744670430000, 3697 ], [ 1744670460000, 4506 ], [ 1744670490000, 3749 ], [ 1744670520000, 4587 ], [ 1744670550000, 3538 ], [ 1744670580000, 4221 ], [ 1744670610000, 3476 ], [ 1744670640000, 4227 ], [ 1744670670000, 3587 ], [ 1744670700000, 4848 ], [ 1744670730000, 3551 ], [ 1744670760000, 4068 ], [ 1744670790000, 3387 ], [ 1744670820000, 4366 ], [ 1744670850000, 3635 ], [ 1744670880000, 4256 ], [ 1744670910000, 3690 ], [ 1744670940000, 4155 ], [ 1744670970000, 3318 ], [ 1744671000000, 4661 ], [ 1744671030000, 3494 ], [ 1744671060000, 4442 ], [ 1744671090000, 3643 ], [ 1744671120000, 4755 ], [ 1744671150000, 3607 ], [ 1744671180000, 4284 ], [ 1744671210000, 3258 ], [ 1744671240000, 4453 ], [ 1744671270000, 3431 ], [ 1744671300000, 4231 ], [ 1744671330000, 3623 ], [ 1744671360000, 3907 ], [ 1744671390000, 3524 ], [ 1744671420000, 4438 ], [ 1744671450000, 3547 ], [ 1744671480000, 4033 ], [ 1744671510000, 3632 ], [ 1744671540000, 4162 ], [ 1744671570000, 3588 ], [ 1744671600000, 16444 ], [ 1744671630000, 15396 ], [ 1744671660000, 3024 ], [ 1744671690000, 12656 ], [ 1744671720000, 4733 ], [ 1744671750000, 3766 ], [ 1744671780000, 4388 ], [ 1744671810000, 3340 ], [ 1744671840000, 4487 ], [ 1744671870000, 3549 ], [ 1744671900000, 4154 ], [ 1744671930000, 3406 ], [ 1744671960000, 4314 ], [ 1744671990000, 3472 ], [ 1744672020000, 4309 ], [ 1744672050000, 3458 ], [ 1744672080000, 4191 ], [ 1744672110000, 3475 ], [ 1744672140000, 4194 ], [ 1744672170000, 3525 ], [ 1744672200000, 4445 ], [ 1744672230000, 3822 ], [ 1744672260000, 4346 ], [ 1744672290000, 3700 ], [ 1744672320000, 4615 ], [ 1744672350000, 3591 ], [ 1744672380000, 4056 ], [ 1744672410000, 3544 ], [ 1744672440000, 4188 ], [ 1744672470000, 3647 ], [ 1744672500000, 4887 ], [ 1744672530000, 3450 ], [ 1744672560000, 4302 ], [ 1744672590000, 3425 ], [ 1744672620000, 4320 ], [ 1744672650000, 3532 ], [ 1744672680000, 4282 ], [ 1744672710000, 3571 ], [ 1744672740000, 4182 ], [ 1744672770000, 3210 ], [ 1744672800000, 6383 ], [ 1744672830000, 3622 ], [ 1744672860000, 4408 ], [ 1744672890000, 3611 ], [ 1744672920000, 4795 ], [ 1744672950000, 3632 ], [ 1744672980000, 4102 ], [ 1744673010000, 3534 ], [ 1744673040000, 4212 ], [ 1744673070000, 3380 ], [ 1744673100000, 4289 ], [ 1744673130000, 3565 ], [ 1744673160000, 4120 ], [ 1744673190000, 3526 ], [ 1744673220000, 4200 ], [ 1744673250000, 3302 ], [ 1744673280000, 4370 ], [ 1744673310000, 3462 ], [ 1744673340000, 4223 ], [ 1744673370000, 3564 ], [ 1744673400000, 12072 ], [ 1744673430000, 17986 ], [ 1744673460000, 4089 ], [ 1744673490000, 12000 ], [ 1744673520000, 4790 ], [ 1744673550000, 3637 ], [ 1744673580000, 4177 ], [ 1744673610000, 3438 ], [ 1744673640000, 4465 ], [ 1744673670000, 3627 ], [ 1744673700000, 4131 ], [ 1744673730000, 3396 ], [ 1744673760000, 4395 ], [ 1744673790000, 3638 ], [ 1744673820000, 4093 ], [ 1744673850000, 3584 ], [ 1744673880000, 4082 ], [ 1744673910000, 3475 ], [ 1744673940000, 4051 ], [ 1744673970000, 3354 ], [ 1744674000000, 6296 ], [ 1744674030000, 3473 ], [ 1744674060000, 4412 ], [ 1744674090000, 3793 ], [ 1744674120000, 4391 ], [ 1744674150000, 3836 ], [ 1744674180000, 4190 ], [ 1744674210000, 3478 ], [ 1744674240000, 4230 ], [ 1744674270000, 3488 ], [ 1744674300000, 4964 ], [ 1744674330000, 3455 ], [ 1744674360000, 4116 ], [ 1744674390000, 3250 ], [ 1744674420000, 4494 ], [ 1744674450000, 3326 ], [ 1744674480000, 4590 ], [ 1744674510000, 3580 ], [ 1744674540000, 4368 ], [ 1744674570000, 3685 ], [ 1744674600000, 4381 ], [ 1744674630000, 3699 ], [ 1744674660000, 4513 ], [ 1744674690000, 3729 ], [ 1744674720000, 4500 ], [ 1744674750000, 3639 ], [ 1744674780000, 4018 ], [ 1744674810000, 3587 ], [ 1744674840000, 4168 ], [ 1744674870000, 3389 ], [ 1744674900000, 4289 ], [ 1744674930000, 3540 ], [ 1744674960000, 4106 ], [ 1744674990000, 3478 ], [ 1744675020000, 4268 ], [ 1744675050000, 3577 ], [ 1744675080000, 4087 ], [ 1744675110000, 3511 ], [ 1744675140000, 4174 ], [ 1744675170000, 3573 ], [ 1744675200000, 17095 ], [ 1744675230000, 14907 ], [ 1744675260000, 6455 ], [ 1744675290000, 9818 ], [ 1744675320000, 5253 ], [ 1744675350000, 3567 ], [ 1744675380000, 4047 ], [ 1744675410000, 3342 ], [ 1744675440000, 4605 ], [ 1744675470000, 3394 ], [ 1744675500000, 4260 ], [ 1744675530000, 3373 ], [ 1744675560000, 4341 ], [ 1744675590000, 3559 ], [ 1744675620000, 4188 ], [ 1744675650000, 3519 ], [ 1744675680000, 4143 ], [ 1744675710000, 3630 ], [ 1744675740000, 4042 ], [ 1744675770000, 3653 ], [ 1744675800000, 4358 ], [ 1744675830000, 3688 ], [ 1744675860000, 4450 ], [ 1744675890000, 3387 ], [ 1744675920000, 4864 ], [ 1744675950000, 3629 ], [ 1744675980000, 4127 ], [ 1744676010000, 3424 ], [ 1744676040000, 4267 ], [ 1744676070000, 3328 ], [ 1744676100000, 5128 ], [ 1744676130000, 3657 ], [ 1744676160000, 4185 ], [ 1744676190000, 3336 ], [ 1744676220000, 4532 ], [ 1744676250000, 3700 ], [ 1744676280000, 4174 ], [ 1744676310000, 3318 ], [ 1744676340000, 4463 ], [ 1744676370000, 3502 ], [ 1744676400000, 6064 ], [ 1744676430000, 3292 ], [ 1744676460000, 4858 ], [ 1744676490000, 3543 ], [ 1744676520000, 4620 ], [ 1744676550000, 3750 ], [ 1744676580000, 4043 ], [ 1744676610000, 3595 ], [ 1744676640000, 4152 ], [ 1744676670000, 3550 ], [ 1744676700000, 4011 ], [ 1744676730000, 3502 ], [ 1744676760000, 4050 ], [ 1744676790000, 3118 ], [ 1744676820000, 4628 ], [ 1744676850000, 3441 ], [ 1744676880000, 4366 ], [ 1744676910000, 3500 ], [ 1744676940000, 4160 ], [ 1744676970000, 3662 ], [ 1744677000000, 11392 ], [ 1744677030000, 18649 ], [ 1744677060000, 7107 ], [ 1744677090000, 9213 ], [ 1744677120000, 4235 ], [ 1744677150000, 3623 ], [ 1744677180000, 4412 ], [ 1744677210000, 3436 ], [ 1744677240000, 4233 ], [ 1744677270000, 3440 ], [ 1744677300000, 4383 ], [ 1744677330000, 3507 ], [ 1744677360000, 4288 ], [ 1744677390000, 3197 ], [ 1744677420000, 4605 ], [ 1744677450000, 3249 ], [ 1744677480000, 4421 ], [ 1744677510000, 2998 ], [ 1744677540000, 4700 ], [ 1744677570000, 3598 ], [ 1744677600000, 5781 ], [ 1744677630000, 3734 ], [ 1744677660000, 4510 ], [ 1744677690000, 3752 ], [ 1744677720000, 4447 ], [ 1744677750000, 3523 ], [ 1744677780000, 4187 ], [ 1744677810000, 3640 ], [ 1744677840000, 3900 ], [ 1744677870000, 3514 ], [ 1744677900000, 4863 ], [ 1744677930000, 3565 ], [ 1744677960000, 4335 ], [ 1744677990000, 3533 ], [ 1744678020000, 4307 ], [ 1744678050000, 3556 ], [ 1744678080000, 4179 ], [ 1744678110000, 3664 ], [ 1744678140000, 4362 ], [ 1744678170000, 3222 ], [ 1744678200000, 4750 ], [ 1744678230000, 3546 ], [ 1744678260000, 4601 ], [ 1744678290000, 3702 ], [ 1744678320000, 4564 ], [ 1744678350000, 3610 ], [ 1744678380000, 4130 ], [ 1744678410000, 3412 ], [ 1744678440000, 4614 ], [ 1744678470000, 3522 ], [ 1744678500000, 4148 ], [ 1744678530000, 3408 ], [ 1744678560000, 4261 ], [ 1744678590000, 3607 ], [ 1744678620000, 4172 ], [ 1744678650000, 3529 ], [ 1744678680000, 4227 ], [ 1744678710000, 3487 ], [ 1744678740000, 4298 ], [ 1744678770000, 3609 ], [ 1744678800000, 7230 ], [ 1744678830000, 3818 ], [ 1744678860000, 11924 ], [ 1744678890000, 27269 ], [ 1744678920000, 5073 ], [ 1744678950000, 3474 ], [ 1744678980000, 4474 ], [ 1744679010000, 3536 ], [ 1744679040000, 4525 ], [ 1744679070000, 3503 ], [ 1744679100000, 4194 ], [ 1744679130000, 3557 ], [ 1744679160000, 4259 ], [ 1744679190000, 3611 ], [ 1744679220000, 4218 ], [ 1744679250000, 3622 ], [ 1744679280000, 4417 ], [ 1744679310000, 3730 ], [ 1744679340000, 4204 ], [ 1744679370000, 3641 ], [ 1744679400000, 4849 ], [ 1744679430000, 3803 ], [ 1744679460000, 4398 ], [ 1744679490000, 3674 ], [ 1744679520000, 4727 ], [ 1744679550000, 3926 ], [ 1744679580000, 4173 ], [ 1744679610000, 3531 ], [ 1744679640000, 4968 ], [ 1744679670000, 3432 ], [ 1744679700000, 5059 ], [ 1744679730000, 3560 ], [ 1744679760000, 4087 ], [ 1744679790000, 3590 ], [ 1744679820000, 4436 ], [ 1744679850000, 5299 ], [ 1744679880000, 4320 ], [ 1744679910000, 3861 ], [ 1744679940000, 4511 ], [ 1744679970000, 3711 ], [ 1744680000000, 6021 ], [ 1744680030000, 3942 ], [ 1744680060000, 4800 ], [ 1744680090000, 3681 ], [ 1744680120000, 4592 ], [ 1744680150000, 3560 ], [ 1744680180000, 4194 ], [ 1744680210000, 3490 ], [ 1744680240000, 4971 ], [ 1744680270000, 4009 ], [ 1744680300000, 4837 ], [ 1744680330000, 3227 ], [ 1744680360000, 4531 ], [ 1744680390000, 2888 ], [ 1744680420000, 5083 ], [ 1744680450000, 3557 ], [ 1744680480000, 4207 ], [ 1744680510000, 3373 ], [ 1744680540000, 4482 ], [ 1744680570000, 3110 ], [ 1744680600000, 13551 ], [ 1744680630000, 17159 ], [ 1744680660000, 6284 ], [ 1744680690000, 9924 ], [ 1744680720000, 4547 ], [ 1744680750000, 3474 ], [ 1744680780000, 4312 ], [ 1744680810000, 3689 ], [ 1744680840000, 4680 ], [ 1744680870000, 3609 ], [ 1744680900000, 4886 ], [ 1744680930000, 3842 ], [ 1744680960000, 4810 ], [ 1744680990000, 4102 ], [ 1744681020000, 4594 ], [ 1744681050000, 4168 ], [ 1744681080000, 4562 ], [ 1744681110000, 4506 ], [ 1744681140000, 5243 ], [ 1744681170000, 5135 ], [ 1744681200000, 6671 ], [ 1744681230000, 3806 ], [ 1744681260000, 4535 ], [ 1744681290000, 3721 ], [ 1744681320000, 4799 ], [ 1744681350000, 3909 ], [ 1744681380000, 4261 ], [ 1744681410000, 3671 ], [ 1744681440000, 4359 ], [ 1744681470000, 4063 ], [ 1744681500000, 5231 ], [ 1744681530000, 3778 ], [ 1744681560000, 4684 ], [ 1744681590000, 4072 ], [ 1744681620000, 5029 ], [ 1744681650000, 3700 ], [ 1744681680000, 4670 ], [ 1744681710000, 3557 ], [ 1744681740000, 4590 ], [ 1744681770000, 3041 ], [ 1744681800000, 5043 ], [ 1744681830000, 3530 ], [ 1744681860000, 6807 ], [ 1744681890000, 4455 ], [ 1744681920000, 6841 ], [ 1744681950000, 4519 ], [ 1744681980000, 6617 ], [ 1744682010000, 4633 ], [ 1744682040000, 5997 ], [ 1744682070000, 4446 ], [ 1744682100000, 5569 ], [ 1744682130000, 4324 ], [ 1744682160000, 5354 ], [ 1744682190000, 7245 ], [ 1744682220000, 5258 ], [ 1744682250000, 4296 ], [ 1744682280000, 5349 ], [ 1744682310000, 4479 ], [ 1744682340000, 5127 ], [ 1744682370000, 4006 ], [ 1744682400000, 19058 ], [ 1744682430000, 14501 ], [ 1744682460000, 3810 ], [ 1744682490000, 12368 ], [ 1744682520000, 6976 ], [ 1744682550000, 4399 ], [ 1744682580000, 5482 ], [ 1744682610000, 4524 ], [ 1744682640000, 5478 ], [ 1744682670000, 4920 ], [ 1744682700000, 5347 ], [ 1744682730000, 4427 ], [ 1744682760000, 5102 ], [ 1744682790000, 4441 ], [ 1744682820000, 5596 ], [ 1744682850000, 4888 ], [ 1744682880000, 5306 ], [ 1744682910000, 4825 ], [ 1744682940000, 5897 ], [ 1744682970000, 4481 ], [ 1744683000000, 6086 ], [ 1744683030000, 4910 ], [ 1744683060000, 5676 ], [ 1744683090000, 3626 ], [ 1744683120000, 6929 ], [ 1744683150000, 4601 ], [ 1744683180000, 5525 ], [ 1744683210000, 4500 ], [ 1744683240000, 5617 ], [ 1744683270000, 4503 ], [ 1744683300000, 6328 ], [ 1744683330000, 4557 ], [ 1744683360000, 5356 ], [ 1744683390000, 4413 ], [ 1744683420000, 5335 ], [ 1744683450000, 4640 ], [ 1744683480000, 5399 ], [ 1744683510000, 4298 ], [ 1744683540000, 5415 ], [ 1744683570000, 4540 ], [ 1744683600000, 6949 ], [ 1744683630000, 4574 ], [ 1744683660000, 5757 ], [ 1744683690000, 4669 ], [ 1744683720000, 5706 ], [ 1744683750000, 4472 ], [ 1744683780000, 5386 ], [ 1744683810000, 4490 ], [ 1744683840000, 5104 ], [ 1744683870000, 4201 ], [ 1744683900000, 5979 ], [ 1744683930000, 4853 ], [ 1744683960000, 6691 ], [ 1744683990000, 4572 ], [ 1744684020000, 5554 ], [ 1744684050000, 5244 ], [ 1744684080000, 5392 ], [ 1744684110000, 4550 ] ]
  } ]
}`,
		},
	} {
		t.Run(fmt.Sprintf("%s", i), func(t *testing.T) {
			metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: spaceUid, SkipSpace: "true"})

			res, err := queryTsWithPromEngine(ctx, c.queryTs)
			assert.Nil(t, err)
			excepted, err := json.Marshal(res)
			assert.Nil(t, err)
			assert.JSONEq(t, c.result, string(excepted))
		})
	}
}

func TestQueryTsWithEs(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	viper.Set(bkapi.BkAPIAddressConfigPath, mock.EsUrlDomain)

	spaceUid := influxdb.SpaceUid
	tableID := influxdb.ResultTableEs

	mock.Init()
	promql.MockEngine()

	defaultStart := time.UnixMilli(1717027200000)
	defaultEnd := time.UnixMilli(1717027500000)

	for i, c := range map[string]struct {
		queryTs *structured.QueryTs
		result  string
	}{
		"查询 10 条原始数据，按照字段正向排序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         10,
						From:          0,
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
			},
		},
		"根据维度 __ext.container_name 进行 count 聚合，同时用值正向排序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         5,
						From:          0,
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function: "count_over_time",
							Window:   "30s",
						},
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"__ext.container_name"},
							},
							{
								Method: "topk",
								VArgsList: []interface{}{
									5,
								},
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"gseIndex",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
				Step:        "30s",
			},
		},
	} {
		t.Run(fmt.Sprintf("%s", i), func(t *testing.T) {
			metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: spaceUid, SkipSpace: "true"})

			res, err := queryTsWithPromEngine(ctx, c.queryTs)
			if err != nil {
				log.Errorf(ctx, err.Error())
				return
			}
			data := res.(*PromData)
			if data.Status != nil && data.Status.Code != "" {
				fmt.Println("code: ", data.Status.Code)
				fmt.Println("message: ", data.Status.Message)
				return
			}

			log.Infof(ctx, fmt.Sprintf("%+v", data.Tables))
		})
	}
}

func TestQueryReferenceWithEs(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	spaceUid := influxdb.SpaceUid
	tableID := influxdb.ResultTableEs

	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)

	defaultStart := time.UnixMilli(1741154079123) // 2025-03-05 13:54:39
	defaultEnd := time.UnixMilli(1741155879987)   // 2025-03-05 14:24:39

	mock.Es.Set(map[string]any{
		`{"aggregations":{"_value":{"value_count":{"field":"gseIndex"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1741154079123,"include_lower":true,"include_upper":true,"to":1741155879987}}}}},"size":0}`: `{"took":626,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182355}}}`,
		`{"aggregations":{"_value":{"value_count":{"field":"gseIndex"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:       `{"took":171,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182486}}}`,

		`{"aggregations":{"__ext.container_name":{"aggregations":{"_value":{"value_count":{"field":"gseIndex"}}},"terms":{"field":"__ext.container_name","missing":" ","order":[{"_value":"asc"}],"size":5}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1741154079123,"include_lower":true,"include_upper":true,"to":1741155879987}}}}},"size":0}`: `{"took":860,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"__ext.container_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"unify-query","doc_count":182355,"_value":{"value":182355}},{"key":" ","doc_count":182355,"_value":{"value":4325521}}]}}}`,

		`{"aggregations":{"__ext.container_name":{"aggregations":{"_value":{"value_count":{"field":"gseIndex"}}},"terms":{"field":"__ext.container_name","missing":" ","order":[{"_value":"desc"}],"size":5}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`: `{"took":885,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"__ext.container_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"unify-query","doc_count":182486,"_value":{"value":182486}}]}}}`,

		`{"aggregations":{"_value":{"value_count":{"field":"__ext.container_name"}}},"query":{"bool":{"filter":[{"bool":{"must":[{"exists":{"field":"__ext.io_kubernetes_pod"}},{"exists":{"field":"__ext.container_name"}}]}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}]}},"size":0}`: `{"took":283,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182486}}}`,
		`{"aggregations":{"_value":{"value_count":{"field":"__ext.container_name"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                                                                                                                  `{"took":283,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182486}}}`,
		`{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                                                                                                               `{"took":167,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182486}}}`,

		`{"aggregations":{"_value":{"cardinality":{"field":"__ext.io_kubernetes_pod"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                                                                                                                                                                                                              `{"took":1595,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":4}}}`,
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod"}}},"date_histogram":{"extended_bounds":{"max":1741155879000,"min":1741154079000},"field":"dtEventTimeStamp","interval":"1m","min_doc_count":0}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                            `{"took":529,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"buckets":[{"key_as_string":"1741154040000","key":1741154040000,"doc_count":3408,"_value":{"value":3408}},{"key_as_string":"1741154100000","key":1741154100000,"doc_count":4444,"_value":{"value":4444}},{"key_as_string":"1741154160000","key":1741154160000,"doc_count":4577,"_value":{"value":4577}},{"key_as_string":"1741154220000","key":1741154220000,"doc_count":4668,"_value":{"value":4668}},{"key_as_string":"1741154280000","key":1741154280000,"doc_count":5642,"_value":{"value":5642}},{"key_as_string":"1741154340000","key":1741154340000,"doc_count":4860,"_value":{"value":4860}},{"key_as_string":"1741154400000","key":1741154400000,"doc_count":35988,"_value":{"value":35988}},{"key_as_string":"1741154460000","key":1741154460000,"doc_count":7098,"_value":{"value":7098}},{"key_as_string":"1741154520000","key":1741154520000,"doc_count":5287,"_value":{"value":5287}},{"key_as_string":"1741154580000","key":1741154580000,"doc_count":5422,"_value":{"value":5422}},{"key_as_string":"1741154640000","key":1741154640000,"doc_count":4906,"_value":{"value":4906}},{"key_as_string":"1741154700000","key":1741154700000,"doc_count":4447,"_value":{"value":4447}},{"key_as_string":"1741154760000","key":1741154760000,"doc_count":4713,"_value":{"value":4713}},{"key_as_string":"1741154820000","key":1741154820000,"doc_count":4621,"_value":{"value":4621}},{"key_as_string":"1741154880000","key":1741154880000,"doc_count":4417,"_value":{"value":4417}},{"key_as_string":"1741154940000","key":1741154940000,"doc_count":5092,"_value":{"value":5092}},{"key_as_string":"1741155000000","key":1741155000000,"doc_count":4805,"_value":{"value":4805}},{"key_as_string":"1741155060000","key":1741155060000,"doc_count":5545,"_value":{"value":5545}},{"key_as_string":"1741155120000","key":1741155120000,"doc_count":4614,"_value":{"value":4614}},{"key_as_string":"1741155180000","key":1741155180000,"doc_count":5121,"_value":{"value":5121}},{"key_as_string":"1741155240000","key":1741155240000,"doc_count":4854,"_value":{"value":4854}},{"key_as_string":"1741155300000","key":1741155300000,"doc_count":5343,"_value":{"value":5343}},{"key_as_string":"1741155360000","key":1741155360000,"doc_count":4789,"_value":{"value":4789}},{"key_as_string":"1741155420000","key":1741155420000,"doc_count":4755,"_value":{"value":4755}},{"key_as_string":"1741155480000","key":1741155480000,"doc_count":5115,"_value":{"value":5115}},{"key_as_string":"1741155540000","key":1741155540000,"doc_count":4588,"_value":{"value":4588}},{"key_as_string":"1741155600000","key":1741155600000,"doc_count":6474,"_value":{"value":6474}},{"key_as_string":"1741155660000","key":1741155660000,"doc_count":5416,"_value":{"value":5416}},{"key_as_string":"1741155720000","key":1741155720000,"doc_count":5128,"_value":{"value":5128}},{"key_as_string":"1741155780000","key":1741155780000,"doc_count":5050,"_value":{"value":5050}},{"key_as_string":"1741155840000","key":1741155840000,"doc_count":1299,"_value":{"value":1299}}]}}}`,
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod"}}},"date_histogram":{"extended_bounds":{"max":1741155879987,"min":1741154079123},"field":"dtEventTimeStamp","interval":"1m","min_doc_count":0}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1741154079123,"include_lower":true,"include_upper":true,"to":1741155879987}}}}},"size":0}`:                      `{"took":759,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"buckets":[{"key_as_string":"1741154040000","key":1741154040000,"doc_count":3277,"_value":{"value":3277}},{"key_as_string":"1741154100000","key":1741154100000,"doc_count":4444,"_value":{"value":4444}},{"key_as_string":"1741154160000","key":1741154160000,"doc_count":4577,"_value":{"value":4577}},{"key_as_string":"1741154220000","key":1741154220000,"doc_count":4668,"_value":{"value":4668}},{"key_as_string":"1741154280000","key":1741154280000,"doc_count":5642,"_value":{"value":5642}},{"key_as_string":"1741154340000","key":1741154340000,"doc_count":4860,"_value":{"value":4860}},{"key_as_string":"1741154400000","key":1741154400000,"doc_count":35988,"_value":{"value":35988}},{"key_as_string":"1741154460000","key":1741154460000,"doc_count":7098,"_value":{"value":7098}},{"key_as_string":"1741154520000","key":1741154520000,"doc_count":5287,"_value":{"value":5287}},{"key_as_string":"1741154580000","key":1741154580000,"doc_count":5422,"_value":{"value":5422}},{"key_as_string":"1741154640000","key":1741154640000,"doc_count":4906,"_value":{"value":4906}},{"key_as_string":"1741154700000","key":1741154700000,"doc_count":4447,"_value":{"value":4447}},{"key_as_string":"1741154760000","key":1741154760000,"doc_count":4713,"_value":{"value":4713}},{"key_as_string":"1741154820000","key":1741154820000,"doc_count":4621,"_value":{"value":4621}},{"key_as_string":"1741154880000","key":1741154880000,"doc_count":4417,"_value":{"value":4417}},{"key_as_string":"1741154940000","key":1741154940000,"doc_count":5092,"_value":{"value":5092}},{"key_as_string":"1741155000000","key":1741155000000,"doc_count":4805,"_value":{"value":4805}},{"key_as_string":"1741155060000","key":1741155060000,"doc_count":5545,"_value":{"value":5545}},{"key_as_string":"1741155120000","key":1741155120000,"doc_count":4614,"_value":{"value":4614}},{"key_as_string":"1741155180000","key":1741155180000,"doc_count":5121,"_value":{"value":5121}},{"key_as_string":"1741155240000","key":1741155240000,"doc_count":4854,"_value":{"value":4854}},{"key_as_string":"1741155300000","key":1741155300000,"doc_count":5343,"_value":{"value":5343}},{"key_as_string":"1741155360000","key":1741155360000,"doc_count":4789,"_value":{"value":4789}},{"key_as_string":"1741155420000","key":1741155420000,"doc_count":4755,"_value":{"value":4755}},{"key_as_string":"1741155480000","key":1741155480000,"doc_count":5115,"_value":{"value":5115}},{"key_as_string":"1741155540000","key":1741155540000,"doc_count":4588,"_value":{"value":4588}},{"key_as_string":"1741155600000","key":1741155600000,"doc_count":6474,"_value":{"value":6474}},{"key_as_string":"1741155660000","key":1741155660000,"doc_count":5416,"_value":{"value":5416}},{"key_as_string":"1741155720000","key":1741155720000,"doc_count":5128,"_value":{"value":5128}},{"key_as_string":"1741155780000","key":1741155780000,"doc_count":5050,"_value":{"value":5050}},{"key_as_string":"1741155840000","key":1741155840000,"doc_count":1299,"_value":{"value":1299}}]}}}`,
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"date_histogram":{"extended_bounds":{"max":1741341600000,"min":1741320000000},"field":"dtEventTimeStamp","interval":"1d","min_doc_count":0,"time_zone":"Asia/Shanghai"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1741320000000,"include_lower":true,"include_upper":true,"to":1741341600000}}}}},"size":0}`: `{"took":5,"timed_out":false,"_shards":{"total":68,"successful":68,"skipped":0,"failed":0},"hits":{"total":{"value":2367,"relation":"eq"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"buckets":[{"key_as_string":"1741276800000","key":1741276800000,"doc_count":2367,"_value":{"value":2367}}]}}}`,

		`{"aggregations":{"span_name":{"aggregations":{"end_time":{"aggregations":{"_value":{"value_count":{"field":"span_name"}}},"date_histogram":{"extended_bounds":{"max":1748245394000000,"min":1747641987000000},"field":"end_time","interval":"5600h","min_doc_count":0}}},"terms":{"field":"span_name","missing":" ","size":10000}}},"query":{"bool":{"filter":{"range":{"end_time":{"from":1747641987000000,"include_lower":true,"include_upper":true,"to":1748245394000000}}}}},"size":0}`: `{"took":408,"timed_out":false,"_shards":{"total":6,"successful":6,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"span_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"SELECT","doc_count":1657598,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":83221,"_value":{"value":83221}},{"key":1747650240000000,"doc_count":48270,"_value":{"value":48270}},{"key":1747670400000000,"doc_count":11389,"_value":{"value":11389}},{"key":1747690560000000,"doc_count":42125,"_value":{"value":42125}},{"key":1747710720000000,"doc_count":127370,"_value":{"value":127370}},{"key":1747730880000000,"doc_count":140077,"_value":{"value":140077}},{"key":1747751040000000,"doc_count":19016,"_value":{"value":19016}},{"key":1747771200000000,"doc_count":21385,"_value":{"value":21385}},{"key":1747791360000000,"doc_count":165395,"_value":{"value":165395}},{"key":1747811520000000,"doc_count":209679,"_value":{"value":209679}},{"key":1747831680000000,"doc_count":49772,"_value":{"value":49772}},{"key":1747851840000000,"doc_count":19983,"_value":{"value":19983}},{"key":1747872000000000,"doc_count":143679,"_value":{"value":143679}},{"key":1747892160000000,"doc_count":178952,"_value":{"value":178952}},{"key":1747912320000000,"doc_count":60992,"_value":{"value":60992}},{"key":1747932480000000,"doc_count":44126,"_value":{"value":44126}},{"key":1747952640000000,"doc_count":63272,"_value":{"value":63272}},{"key":1747972800000000,"doc_count":79260,"_value":{"value":79260}},{"key":1747992960000000,"doc_count":22578,"_value":{"value":22578}},{"key":1748013120000000,"doc_count":5817,"_value":{"value":5817}},{"key":1748033280000000,"doc_count":5874,"_value":{"value":5874}},{"key":1748053440000000,"doc_count":4371,"_value":{"value":4371}},{"key":1748073600000000,"doc_count":1128,"_value":{"value":1128}},{"key":1748093760000000,"doc_count":1106,"_value":{"value":1106}},{"key":1748113920000000,"doc_count":1099,"_value":{"value":1099}},{"key":1748134080000000,"doc_count":1130,"_value":{"value":1130}},{"key":1748154240000000,"doc_count":1084,"_value":{"value":1084}},{"key":1748174400000000,"doc_count":1073,"_value":{"value":1073}},{"key":1748194560000000,"doc_count":1093,"_value":{"value":1093}},{"key":1748214720000000,"doc_count":36526,"_value":{"value":36526}},{"key":1748234880000000,"doc_count":66756,"_value":{"value":66756}}]}},{"key":"/trpc.example.greeter.Greeter/SayHello","doc_count":883345,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":12124,"_value":{"value":12124}},{"key":1747650240000000,"doc_count":29654,"_value":{"value":29654}},{"key":1747670400000000,"doc_count":29482,"_value":{"value":29482}},{"key":1747690560000000,"doc_count":29672,"_value":{"value":29672}},{"key":1747710720000000,"doc_count":29660,"_value":{"value":29660}},{"key":1747730880000000,"doc_count":29457,"_value":{"value":29457}},{"key":1747751040000000,"doc_count":29506,"_value":{"value":29506}},{"key":1747771200000000,"doc_count":29475,"_value":{"value":29475}},{"key":1747791360000000,"doc_count":29642,"_value":{"value":29642}},{"key":1747811520000000,"doc_count":29639,"_value":{"value":29639}},{"key":1747831680000000,"doc_count":29629,"_value":{"value":29629}},{"key":1747851840000000,"doc_count":29498,"_value":{"value":29498}},{"key":1747872000000000,"doc_count":29492,"_value":{"value":29492}},{"key":1747892160000000,"doc_count":29346,"_value":{"value":29346}},{"key":1747912320000000,"doc_count":29055,"_value":{"value":29055}},{"key":1747932480000000,"doc_count":29116,"_value":{"value":29116}},{"key":1747952640000000,"doc_count":29132,"_value":{"value":29132}},{"key":1747972800000000,"doc_count":29109,"_value":{"value":29109}},{"key":1747992960000000,"doc_count":29576,"_value":{"value":29576}},{"key":1748013120000000,"doc_count":29656,"_value":{"value":29656}},{"key":1748033280000000,"doc_count":29664,"_value":{"value":29664}},{"key":1748053440000000,"doc_count":29467,"_value":{"value":29467}},{"key":1748073600000000,"doc_count":29676,"_value":{"value":29676}},{"key":1748093760000000,"doc_count":29654,"_value":{"value":29654}},{"key":1748113920000000,"doc_count":29494,"_value":{"value":29494}},{"key":1748134080000000,"doc_count":29668,"_value":{"value":29668}},{"key":1748154240000000,"doc_count":29508,"_value":{"value":29508}},{"key":1748174400000000,"doc_count":29668,"_value":{"value":29668}},{"key":1748194560000000,"doc_count":29672,"_value":{"value":29672}},{"key":1748214720000000,"doc_count":29666,"_value":{"value":29666}},{"key":1748234880000000,"doc_count":15288,"_value":{"value":15288}}]}},{"key":"/trpc.example.greeter.Greeter/SayHi","doc_count":865553,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":11860,"_value":{"value":11860}},{"key":1747650240000000,"doc_count":29057,"_value":{"value":29057}},{"key":1747670400000000,"doc_count":28868,"_value":{"value":28868}},{"key":1747690560000000,"doc_count":29050,"_value":{"value":29050}},{"key":1747710720000000,"doc_count":29068,"_value":{"value":29068}},{"key":1747730880000000,"doc_count":28883,"_value":{"value":28883}},{"key":1747751040000000,"doc_count":28914,"_value":{"value":28914}},{"key":1747771200000000,"doc_count":28934,"_value":{"value":28934}},{"key":1747791360000000,"doc_count":29011,"_value":{"value":29011}},{"key":1747811520000000,"doc_count":29063,"_value":{"value":29063}},{"key":1747831680000000,"doc_count":28963,"_value":{"value":28963}},{"key":1747851840000000,"doc_count":28896,"_value":{"value":28896}},{"key":1747872000000000,"doc_count":28934,"_value":{"value":28934}},{"key":1747892160000000,"doc_count":28790,"_value":{"value":28790}},{"key":1747912320000000,"doc_count":28426,"_value":{"value":28426}},{"key":1747932480000000,"doc_count":28512,"_value":{"value":28512}},{"key":1747952640000000,"doc_count":28490,"_value":{"value":28490}},{"key":1747972800000000,"doc_count":28560,"_value":{"value":28560}},{"key":1747992960000000,"doc_count":28992,"_value":{"value":28992}},{"key":1748013120000000,"doc_count":29080,"_value":{"value":29080}},{"key":1748033280000000,"doc_count":29072,"_value":{"value":29072}},{"key":1748053440000000,"doc_count":28908,"_value":{"value":28908}},{"key":1748073600000000,"doc_count":29052,"_value":{"value":29052}},{"key":1748093760000000,"doc_count":29054,"_value":{"value":29054}},{"key":1748113920000000,"doc_count":28890,"_value":{"value":28890}},{"key":1748134080000000,"doc_count":29076,"_value":{"value":29076}},{"key":1748154240000000,"doc_count":28930,"_value":{"value":28930}},{"key":1748174400000000,"doc_count":29058,"_value":{"value":29058}},{"key":1748194560000000,"doc_count":29084,"_value":{"value":29084}},{"key":1748214720000000,"doc_count":29070,"_value":{"value":29070}},{"key":1748234880000000,"doc_count":15008,"_value":{"value":15008}}]}},{"key":"internalSpanDoSomething","doc_count":441681,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":6061,"_value":{"value":6061}},{"key":1747650240000000,"doc_count":14829,"_value":{"value":14829}},{"key":1747670400000000,"doc_count":14741,"_value":{"value":14741}},{"key":1747690560000000,"doc_count":14836,"_value":{"value":14836}},{"key":1747710720000000,"doc_count":14830,"_value":{"value":14830}},{"key":1747730880000000,"doc_count":14725,"_value":{"value":14725}},{"key":1747751040000000,"doc_count":14753,"_value":{"value":14753}},{"key":1747771200000000,"doc_count":14739,"_value":{"value":14739}},{"key":1747791360000000,"doc_count":14817,"_value":{"value":14817}},{"key":1747811520000000,"doc_count":14822,"_value":{"value":14822}},{"key":1747831680000000,"doc_count":14816,"_value":{"value":14816}},{"key":1747851840000000,"doc_count":14748,"_value":{"value":14748}},{"key":1747872000000000,"doc_count":14746,"_value":{"value":14746}},{"key":1747892160000000,"doc_count":14673,"_value":{"value":14673}},{"key":1747912320000000,"doc_count":14533,"_value":{"value":14533}},{"key":1747932480000000,"doc_count":14557,"_value":{"value":14557}},{"key":1747952640000000,"doc_count":14566,"_value":{"value":14566}},{"key":1747972800000000,"doc_count":14556,"_value":{"value":14556}},{"key":1747992960000000,"doc_count":14788,"_value":{"value":14788}},{"key":1748013120000000,"doc_count":14829,"_value":{"value":14829}},{"key":1748033280000000,"doc_count":14832,"_value":{"value":14832}},{"key":1748053440000000,"doc_count":14738,"_value":{"value":14738}},{"key":1748073600000000,"doc_count":14837,"_value":{"value":14837}},{"key":1748093760000000,"doc_count":14827,"_value":{"value":14827}},{"key":1748113920000000,"doc_count":14747,"_value":{"value":14747}},{"key":1748134080000000,"doc_count":14834,"_value":{"value":14834}},{"key":1748154240000000,"doc_count":14754,"_value":{"value":14754}},{"key":1748174400000000,"doc_count":14834,"_value":{"value":14834}},{"key":1748194560000000,"doc_count":14836,"_value":{"value":14836}},{"key":1748214720000000,"doc_count":14833,"_value":{"value":14833}},{"key":1748234880000000,"doc_count":7644,"_value":{"value":7644}}]}},{"key":"test.example.greeter.SayHello/sleep","doc_count":432779,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":5930,"_value":{"value":5930}},{"key":1747650240000000,"doc_count":14529,"_value":{"value":14529}},{"key":1747670400000000,"doc_count":14434,"_value":{"value":14434}},{"key":1747690560000000,"doc_count":14525,"_value":{"value":14525}},{"key":1747710720000000,"doc_count":14534,"_value":{"value":14534}},{"key":1747730880000000,"doc_count":14444,"_value":{"value":14444}},{"key":1747751040000000,"doc_count":14461,"_value":{"value":14461}},{"key":1747771200000000,"doc_count":14466,"_value":{"value":14466}},{"key":1747791360000000,"doc_count":14501,"_value":{"value":14501}},{"key":1747811520000000,"doc_count":14533,"_value":{"value":14533}},{"key":1747831680000000,"doc_count":14482,"_value":{"value":14482}},{"key":1747851840000000,"doc_count":14448,"_value":{"value":14448}},{"key":1747872000000000,"doc_count":14467,"_value":{"value":14467}},{"key":1747892160000000,"doc_count":14395,"_value":{"value":14395}},{"key":1747912320000000,"doc_count":14213,"_value":{"value":14213}},{"key":1747932480000000,"doc_count":14255,"_value":{"value":14255}},{"key":1747952640000000,"doc_count":14245,"_value":{"value":14245}},{"key":1747972800000000,"doc_count":14281,"_value":{"value":14281}},{"key":1747992960000000,"doc_count":14496,"_value":{"value":14496}},{"key":1748013120000000,"doc_count":14539,"_value":{"value":14539}},{"key":1748033280000000,"doc_count":14536,"_value":{"value":14536}},{"key":1748053440000000,"doc_count":14454,"_value":{"value":14454}},{"key":1748073600000000,"doc_count":14526,"_value":{"value":14526}},{"key":1748093760000000,"doc_count":14527,"_value":{"value":14527}},{"key":1748113920000000,"doc_count":14445,"_value":{"value":14445}},{"key":1748134080000000,"doc_count":14538,"_value":{"value":14538}},{"key":1748154240000000,"doc_count":14465,"_value":{"value":14465}},{"key":1748174400000000,"doc_count":14529,"_value":{"value":14529}},{"key":1748194560000000,"doc_count":14542,"_value":{"value":14542}},{"key":1748214720000000,"doc_count":14535,"_value":{"value":14535}},{"key":1748234880000000,"doc_count":7504,"_value":{"value":7504}}]}}]}}}`,

		`{"aggregations":{"span_name":{"aggregations":{"end_time":{"aggregations":{"_value":{"value_count":{"field":"span_name"}}},"date_histogram":{"extended_bounds":{"max":1748245394000000,"min":1747641987000000},"field":"end_time","interval":"5600h","min_doc_count":0}}},"terms":{"field":"span_name","missing":" ","size":10000}}},"query":{"bool":{"filter":[{"bool":{"should":[{"match_phrase":{"span_name":{"query":"SELECT"}}},{"match_phrase":{"span_name":{"query":"/trpc.example.greeter.Greeter/SayHello"}}},{"match_phrase":{"span_name":{"query":"/trpc.example.greeter.Greeter/SayHi"}}},{"match_phrase":{"span_name":{"query":"internalSpanDoSomething"}}},{"match_phrase":{"span_name":{"query":"test.example.greeter.SayHello/sleep"}}}]}},{"range":{"end_time":{"from":1747641987000000,"include_lower":true,"include_upper":true,"to":1748245394000000}}}]}},"size":0}`: `{"took":408,"timed_out":false,"_shards":{"total":6,"successful":6,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"span_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"SELECT","doc_count":1657598,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":83221,"_value":{"value":83221}},{"key":1747650240000000,"doc_count":48270,"_value":{"value":48270}},{"key":1747670400000000,"doc_count":11389,"_value":{"value":11389}},{"key":1747690560000000,"doc_count":42125,"_value":{"value":42125}},{"key":1747710720000000,"doc_count":127370,"_value":{"value":127370}},{"key":1747730880000000,"doc_count":140077,"_value":{"value":140077}},{"key":1747751040000000,"doc_count":19016,"_value":{"value":19016}},{"key":1747771200000000,"doc_count":21385,"_value":{"value":21385}},{"key":1747791360000000,"doc_count":165395,"_value":{"value":165395}},{"key":1747811520000000,"doc_count":209679,"_value":{"value":209679}},{"key":1747831680000000,"doc_count":49772,"_value":{"value":49772}},{"key":1747851840000000,"doc_count":19983,"_value":{"value":19983}},{"key":1747872000000000,"doc_count":143679,"_value":{"value":143679}},{"key":1747892160000000,"doc_count":178952,"_value":{"value":178952}},{"key":1747912320000000,"doc_count":60992,"_value":{"value":60992}},{"key":1747932480000000,"doc_count":44126,"_value":{"value":44126}},{"key":1747952640000000,"doc_count":63272,"_value":{"value":63272}},{"key":1747972800000000,"doc_count":79260,"_value":{"value":79260}},{"key":1747992960000000,"doc_count":22578,"_value":{"value":22578}},{"key":1748013120000000,"doc_count":5817,"_value":{"value":5817}},{"key":1748033280000000,"doc_count":5874,"_value":{"value":5874}},{"key":1748053440000000,"doc_count":4371,"_value":{"value":4371}},{"key":1748073600000000,"doc_count":1128,"_value":{"value":1128}},{"key":1748093760000000,"doc_count":1106,"_value":{"value":1106}},{"key":1748113920000000,"doc_count":1099,"_value":{"value":1099}},{"key":1748134080000000,"doc_count":1130,"_value":{"value":1130}},{"key":1748154240000000,"doc_count":1084,"_value":{"value":1084}},{"key":1748174400000000,"doc_count":1073,"_value":{"value":1073}},{"key":1748194560000000,"doc_count":1093,"_value":{"value":1093}},{"key":1748214720000000,"doc_count":36526,"_value":{"value":36526}},{"key":1748234880000000,"doc_count":66756,"_value":{"value":66756}}]}},{"key":"/trpc.example.greeter.Greeter/SayHello","doc_count":883345,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":12124,"_value":{"value":12124}},{"key":1747650240000000,"doc_count":29654,"_value":{"value":29654}},{"key":1747670400000000,"doc_count":29482,"_value":{"value":29482}},{"key":1747690560000000,"doc_count":29672,"_value":{"value":29672}},{"key":1747710720000000,"doc_count":29660,"_value":{"value":29660}},{"key":1747730880000000,"doc_count":29457,"_value":{"value":29457}},{"key":1747751040000000,"doc_count":29506,"_value":{"value":29506}},{"key":1747771200000000,"doc_count":29475,"_value":{"value":29475}},{"key":1747791360000000,"doc_count":29642,"_value":{"value":29642}},{"key":1747811520000000,"doc_count":29639,"_value":{"value":29639}},{"key":1747831680000000,"doc_count":29629,"_value":{"value":29629}},{"key":1747851840000000,"doc_count":29498,"_value":{"value":29498}},{"key":1747872000000000,"doc_count":29492,"_value":{"value":29492}},{"key":1747892160000000,"doc_count":29346,"_value":{"value":29346}},{"key":1747912320000000,"doc_count":29055,"_value":{"value":29055}},{"key":1747932480000000,"doc_count":29116,"_value":{"value":29116}},{"key":1747952640000000,"doc_count":29132,"_value":{"value":29132}},{"key":1747972800000000,"doc_count":29109,"_value":{"value":29109}},{"key":1747992960000000,"doc_count":29576,"_value":{"value":29576}},{"key":1748013120000000,"doc_count":29656,"_value":{"value":29656}},{"key":1748033280000000,"doc_count":29664,"_value":{"value":29664}},{"key":1748053440000000,"doc_count":29467,"_value":{"value":29467}},{"key":1748073600000000,"doc_count":29676,"_value":{"value":29676}},{"key":1748093760000000,"doc_count":29654,"_value":{"value":29654}},{"key":1748113920000000,"doc_count":29494,"_value":{"value":29494}},{"key":1748134080000000,"doc_count":29668,"_value":{"value":29668}},{"key":1748154240000000,"doc_count":29508,"_value":{"value":29508}},{"key":1748174400000000,"doc_count":29668,"_value":{"value":29668}},{"key":1748194560000000,"doc_count":29672,"_value":{"value":29672}},{"key":1748214720000000,"doc_count":29666,"_value":{"value":29666}},{"key":1748234880000000,"doc_count":15288,"_value":{"value":15288}}]}},{"key":"/trpc.example.greeter.Greeter/SayHi","doc_count":865553,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":11860,"_value":{"value":11860}},{"key":1747650240000000,"doc_count":29057,"_value":{"value":29057}},{"key":1747670400000000,"doc_count":28868,"_value":{"value":28868}},{"key":1747690560000000,"doc_count":29050,"_value":{"value":29050}},{"key":1747710720000000,"doc_count":29068,"_value":{"value":29068}},{"key":1747730880000000,"doc_count":28883,"_value":{"value":28883}},{"key":1747751040000000,"doc_count":28914,"_value":{"value":28914}},{"key":1747771200000000,"doc_count":28934,"_value":{"value":28934}},{"key":1747791360000000,"doc_count":29011,"_value":{"value":29011}},{"key":1747811520000000,"doc_count":29063,"_value":{"value":29063}},{"key":1747831680000000,"doc_count":28963,"_value":{"value":28963}},{"key":1747851840000000,"doc_count":28896,"_value":{"value":28896}},{"key":1747872000000000,"doc_count":28934,"_value":{"value":28934}},{"key":1747892160000000,"doc_count":28790,"_value":{"value":28790}},{"key":1747912320000000,"doc_count":28426,"_value":{"value":28426}},{"key":1747932480000000,"doc_count":28512,"_value":{"value":28512}},{"key":1747952640000000,"doc_count":28490,"_value":{"value":28490}},{"key":1747972800000000,"doc_count":28560,"_value":{"value":28560}},{"key":1747992960000000,"doc_count":28992,"_value":{"value":28992}},{"key":1748013120000000,"doc_count":29080,"_value":{"value":29080}},{"key":1748033280000000,"doc_count":29072,"_value":{"value":29072}},{"key":1748053440000000,"doc_count":28908,"_value":{"value":28908}},{"key":1748073600000000,"doc_count":29052,"_value":{"value":29052}},{"key":1748093760000000,"doc_count":29054,"_value":{"value":29054}},{"key":1748113920000000,"doc_count":28890,"_value":{"value":28890}},{"key":1748134080000000,"doc_count":29076,"_value":{"value":29076}},{"key":1748154240000000,"doc_count":28930,"_value":{"value":28930}},{"key":1748174400000000,"doc_count":29058,"_value":{"value":29058}},{"key":1748194560000000,"doc_count":29084,"_value":{"value":29084}},{"key":1748214720000000,"doc_count":29070,"_value":{"value":29070}},{"key":1748234880000000,"doc_count":15008,"_value":{"value":15008}}]}},{"key":"internalSpanDoSomething","doc_count":441681,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":6061,"_value":{"value":6061}},{"key":1747650240000000,"doc_count":14829,"_value":{"value":14829}},{"key":1747670400000000,"doc_count":14741,"_value":{"value":14741}},{"key":1747690560000000,"doc_count":14836,"_value":{"value":14836}},{"key":1747710720000000,"doc_count":14830,"_value":{"value":14830}},{"key":1747730880000000,"doc_count":14725,"_value":{"value":14725}},{"key":1747751040000000,"doc_count":14753,"_value":{"value":14753}},{"key":1747771200000000,"doc_count":14739,"_value":{"value":14739}},{"key":1747791360000000,"doc_count":14817,"_value":{"value":14817}},{"key":1747811520000000,"doc_count":14822,"_value":{"value":14822}},{"key":1747831680000000,"doc_count":14816,"_value":{"value":14816}},{"key":1747851840000000,"doc_count":14748,"_value":{"value":14748}},{"key":1747872000000000,"doc_count":14746,"_value":{"value":14746}},{"key":1747892160000000,"doc_count":14673,"_value":{"value":14673}},{"key":1747912320000000,"doc_count":14533,"_value":{"value":14533}},{"key":1747932480000000,"doc_count":14557,"_value":{"value":14557}},{"key":1747952640000000,"doc_count":14566,"_value":{"value":14566}},{"key":1747972800000000,"doc_count":14556,"_value":{"value":14556}},{"key":1747992960000000,"doc_count":14788,"_value":{"value":14788}},{"key":1748013120000000,"doc_count":14829,"_value":{"value":14829}},{"key":1748033280000000,"doc_count":14832,"_value":{"value":14832}},{"key":1748053440000000,"doc_count":14738,"_value":{"value":14738}},{"key":1748073600000000,"doc_count":14837,"_value":{"value":14837}},{"key":1748093760000000,"doc_count":14827,"_value":{"value":14827}},{"key":1748113920000000,"doc_count":14747,"_value":{"value":14747}},{"key":1748134080000000,"doc_count":14834,"_value":{"value":14834}},{"key":1748154240000000,"doc_count":14754,"_value":{"value":14754}},{"key":1748174400000000,"doc_count":14834,"_value":{"value":14834}},{"key":1748194560000000,"doc_count":14836,"_value":{"value":14836}},{"key":1748214720000000,"doc_count":14833,"_value":{"value":14833}},{"key":1748234880000000,"doc_count":7644,"_value":{"value":7644}}]}},{"key":"test.example.greeter.SayHello/sleep","doc_count":432779,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":5930,"_value":{"value":5930}},{"key":1747650240000000,"doc_count":14529,"_value":{"value":14529}},{"key":1747670400000000,"doc_count":14434,"_value":{"value":14434}},{"key":1747690560000000,"doc_count":14525,"_value":{"value":14525}},{"key":1747710720000000,"doc_count":14534,"_value":{"value":14534}},{"key":1747730880000000,"doc_count":14444,"_value":{"value":14444}},{"key":1747751040000000,"doc_count":14461,"_value":{"value":14461}},{"key":1747771200000000,"doc_count":14466,"_value":{"value":14466}},{"key":1747791360000000,"doc_count":14501,"_value":{"value":14501}},{"key":1747811520000000,"doc_count":14533,"_value":{"value":14533}},{"key":1747831680000000,"doc_count":14482,"_value":{"value":14482}},{"key":1747851840000000,"doc_count":14448,"_value":{"value":14448}},{"key":1747872000000000,"doc_count":14467,"_value":{"value":14467}},{"key":1747892160000000,"doc_count":14395,"_value":{"value":14395}},{"key":1747912320000000,"doc_count":14213,"_value":{"value":14213}},{"key":1747932480000000,"doc_count":14255,"_value":{"value":14255}},{"key":1747952640000000,"doc_count":14245,"_value":{"value":14245}},{"key":1747972800000000,"doc_count":14281,"_value":{"value":14281}},{"key":1747992960000000,"doc_count":14496,"_value":{"value":14496}},{"key":1748013120000000,"doc_count":14539,"_value":{"value":14539}},{"key":1748033280000000,"doc_count":14536,"_value":{"value":14536}},{"key":1748053440000000,"doc_count":14454,"_value":{"value":14454}},{"key":1748073600000000,"doc_count":14526,"_value":{"value":14526}},{"key":1748093760000000,"doc_count":14527,"_value":{"value":14527}},{"key":1748113920000000,"doc_count":14445,"_value":{"value":14445}},{"key":1748134080000000,"doc_count":14538,"_value":{"value":14538}},{"key":1748154240000000,"doc_count":14465,"_value":{"value":14465}},{"key":1748174400000000,"doc_count":14529,"_value":{"value":14529}},{"key":1748194560000000,"doc_count":14542,"_value":{"value":14542}},{"key":1748214720000000,"doc_count":14535,"_value":{"value":14535}},{"key":1748234880000000,"doc_count":7504,"_value":{"value":7504}}]}}]}}}`,

		`{"aggregations":{"span_name":{"aggregations":{"end_time":{"aggregations":{"_value":{"value_count":{"field":"span_name"}}},"date_histogram":{"extended_bounds":{"max":1748245394000000,"min":1747641987000000},"field":"end_time","interval":"5600h","min_doc_count":0}}},"terms":{"field":"span_name","missing":" ","size":10000}}},"query":{"bool":{"filter":[{"exists":{"field":"span_name"}},{"range":{"end_time":{"from":1747641987000000,"include_lower":true,"include_upper":true,"to":1748245394000000}}}]}},"size":0}`: `{"took":408,"timed_out":false,"_shards":{"total":6,"successful":6,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"span_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"SELECT","doc_count":1657598,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":83221,"_value":{"value":83221}},{"key":1747650240000000,"doc_count":48270,"_value":{"value":48270}},{"key":1747670400000000,"doc_count":11389,"_value":{"value":11389}},{"key":1747690560000000,"doc_count":42125,"_value":{"value":42125}},{"key":1747710720000000,"doc_count":127370,"_value":{"value":127370}},{"key":1747730880000000,"doc_count":140077,"_value":{"value":140077}},{"key":1747751040000000,"doc_count":19016,"_value":{"value":19016}},{"key":1747771200000000,"doc_count":21385,"_value":{"value":21385}},{"key":1747791360000000,"doc_count":165395,"_value":{"value":165395}},{"key":1747811520000000,"doc_count":209679,"_value":{"value":209679}},{"key":1747831680000000,"doc_count":49772,"_value":{"value":49772}},{"key":1747851840000000,"doc_count":19983,"_value":{"value":19983}},{"key":1747872000000000,"doc_count":143679,"_value":{"value":143679}},{"key":1747892160000000,"doc_count":178952,"_value":{"value":178952}},{"key":1747912320000000,"doc_count":60992,"_value":{"value":60992}},{"key":1747932480000000,"doc_count":44126,"_value":{"value":44126}},{"key":1747952640000000,"doc_count":63272,"_value":{"value":63272}},{"key":1747972800000000,"doc_count":79260,"_value":{"value":79260}},{"key":1747992960000000,"doc_count":22578,"_value":{"value":22578}},{"key":1748013120000000,"doc_count":5817,"_value":{"value":5817}},{"key":1748033280000000,"doc_count":5874,"_value":{"value":5874}},{"key":1748053440000000,"doc_count":4371,"_value":{"value":4371}},{"key":1748073600000000,"doc_count":1128,"_value":{"value":1128}},{"key":1748093760000000,"doc_count":1106,"_value":{"value":1106}},{"key":1748113920000000,"doc_count":1099,"_value":{"value":1099}},{"key":1748134080000000,"doc_count":1130,"_value":{"value":1130}},{"key":1748154240000000,"doc_count":1084,"_value":{"value":1084}},{"key":1748174400000000,"doc_count":1073,"_value":{"value":1073}},{"key":1748194560000000,"doc_count":1093,"_value":{"value":1093}},{"key":1748214720000000,"doc_count":36526,"_value":{"value":36526}},{"key":1748234880000000,"doc_count":66756,"_value":{"value":66756}}]}},{"key":"/trpc.example.greeter.Greeter/SayHello","doc_count":883345,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":12124,"_value":{"value":12124}},{"key":1747650240000000,"doc_count":29654,"_value":{"value":29654}},{"key":1747670400000000,"doc_count":29482,"_value":{"value":29482}},{"key":1747690560000000,"doc_count":29672,"_value":{"value":29672}},{"key":1747710720000000,"doc_count":29660,"_value":{"value":29660}},{"key":1747730880000000,"doc_count":29457,"_value":{"value":29457}},{"key":1747751040000000,"doc_count":29506,"_value":{"value":29506}},{"key":1747771200000000,"doc_count":29475,"_value":{"value":29475}},{"key":1747791360000000,"doc_count":29642,"_value":{"value":29642}},{"key":1747811520000000,"doc_count":29639,"_value":{"value":29639}},{"key":1747831680000000,"doc_count":29629,"_value":{"value":29629}},{"key":1747851840000000,"doc_count":29498,"_value":{"value":29498}},{"key":1747872000000000,"doc_count":29492,"_value":{"value":29492}},{"key":1747892160000000,"doc_count":29346,"_value":{"value":29346}},{"key":1747912320000000,"doc_count":29055,"_value":{"value":29055}},{"key":1747932480000000,"doc_count":29116,"_value":{"value":29116}},{"key":1747952640000000,"doc_count":29132,"_value":{"value":29132}},{"key":1747972800000000,"doc_count":29109,"_value":{"value":29109}},{"key":1747992960000000,"doc_count":29576,"_value":{"value":29576}},{"key":1748013120000000,"doc_count":29656,"_value":{"value":29656}},{"key":1748033280000000,"doc_count":29664,"_value":{"value":29664}},{"key":1748053440000000,"doc_count":29467,"_value":{"value":29467}},{"key":1748073600000000,"doc_count":29676,"_value":{"value":29676}},{"key":1748093760000000,"doc_count":29654,"_value":{"value":29654}},{"key":1748113920000000,"doc_count":29494,"_value":{"value":29494}},{"key":1748134080000000,"doc_count":29668,"_value":{"value":29668}},{"key":1748154240000000,"doc_count":29508,"_value":{"value":29508}},{"key":1748174400000000,"doc_count":29668,"_value":{"value":29668}},{"key":1748194560000000,"doc_count":29672,"_value":{"value":29672}},{"key":1748214720000000,"doc_count":29666,"_value":{"value":29666}},{"key":1748234880000000,"doc_count":15288,"_value":{"value":15288}}]}},{"key":"/trpc.example.greeter.Greeter/SayHi","doc_count":865553,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":11860,"_value":{"value":11860}},{"key":1747650240000000,"doc_count":29057,"_value":{"value":29057}},{"key":1747670400000000,"doc_count":28868,"_value":{"value":28868}},{"key":1747690560000000,"doc_count":29050,"_value":{"value":29050}},{"key":1747710720000000,"doc_count":29068,"_value":{"value":29068}},{"key":1747730880000000,"doc_count":28883,"_value":{"value":28883}},{"key":1747751040000000,"doc_count":28914,"_value":{"value":28914}},{"key":1747771200000000,"doc_count":28934,"_value":{"value":28934}},{"key":1747791360000000,"doc_count":29011,"_value":{"value":29011}},{"key":1747811520000000,"doc_count":29063,"_value":{"value":29063}},{"key":1747831680000000,"doc_count":28963,"_value":{"value":28963}},{"key":1747851840000000,"doc_count":28896,"_value":{"value":28896}},{"key":1747872000000000,"doc_count":28934,"_value":{"value":28934}},{"key":1747892160000000,"doc_count":28790,"_value":{"value":28790}},{"key":1747912320000000,"doc_count":28426,"_value":{"value":28426}},{"key":1747932480000000,"doc_count":28512,"_value":{"value":28512}},{"key":1747952640000000,"doc_count":28490,"_value":{"value":28490}},{"key":1747972800000000,"doc_count":28560,"_value":{"value":28560}},{"key":1747992960000000,"doc_count":28992,"_value":{"value":28992}},{"key":1748013120000000,"doc_count":29080,"_value":{"value":29080}},{"key":1748033280000000,"doc_count":29072,"_value":{"value":29072}},{"key":1748053440000000,"doc_count":28908,"_value":{"value":28908}},{"key":1748073600000000,"doc_count":29052,"_value":{"value":29052}},{"key":1748093760000000,"doc_count":29054,"_value":{"value":29054}},{"key":1748113920000000,"doc_count":28890,"_value":{"value":28890}},{"key":1748134080000000,"doc_count":29076,"_value":{"value":29076}},{"key":1748154240000000,"doc_count":28930,"_value":{"value":28930}},{"key":1748174400000000,"doc_count":29058,"_value":{"value":29058}},{"key":1748194560000000,"doc_count":29084,"_value":{"value":29084}},{"key":1748214720000000,"doc_count":29070,"_value":{"value":29070}},{"key":1748234880000000,"doc_count":15008,"_value":{"value":15008}}]}},{"key":"internalSpanDoSomething","doc_count":441681,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":6061,"_value":{"value":6061}},{"key":1747650240000000,"doc_count":14829,"_value":{"value":14829}},{"key":1747670400000000,"doc_count":14741,"_value":{"value":14741}},{"key":1747690560000000,"doc_count":14836,"_value":{"value":14836}},{"key":1747710720000000,"doc_count":14830,"_value":{"value":14830}},{"key":1747730880000000,"doc_count":14725,"_value":{"value":14725}},{"key":1747751040000000,"doc_count":14753,"_value":{"value":14753}},{"key":1747771200000000,"doc_count":14739,"_value":{"value":14739}},{"key":1747791360000000,"doc_count":14817,"_value":{"value":14817}},{"key":1747811520000000,"doc_count":14822,"_value":{"value":14822}},{"key":1747831680000000,"doc_count":14816,"_value":{"value":14816}},{"key":1747851840000000,"doc_count":14748,"_value":{"value":14748}},{"key":1747872000000000,"doc_count":14746,"_value":{"value":14746}},{"key":1747892160000000,"doc_count":14673,"_value":{"value":14673}},{"key":1747912320000000,"doc_count":14533,"_value":{"value":14533}},{"key":1747932480000000,"doc_count":14557,"_value":{"value":14557}},{"key":1747952640000000,"doc_count":14566,"_value":{"value":14566}},{"key":1747972800000000,"doc_count":14556,"_value":{"value":14556}},{"key":1747992960000000,"doc_count":14788,"_value":{"value":14788}},{"key":1748013120000000,"doc_count":14829,"_value":{"value":14829}},{"key":1748033280000000,"doc_count":14832,"_value":{"value":14832}},{"key":1748053440000000,"doc_count":14738,"_value":{"value":14738}},{"key":1748073600000000,"doc_count":14837,"_value":{"value":14837}},{"key":1748093760000000,"doc_count":14827,"_value":{"value":14827}},{"key":1748113920000000,"doc_count":14747,"_value":{"value":14747}},{"key":1748134080000000,"doc_count":14834,"_value":{"value":14834}},{"key":1748154240000000,"doc_count":14754,"_value":{"value":14754}},{"key":1748174400000000,"doc_count":14834,"_value":{"value":14834}},{"key":1748194560000000,"doc_count":14836,"_value":{"value":14836}},{"key":1748214720000000,"doc_count":14833,"_value":{"value":14833}},{"key":1748234880000000,"doc_count":7644,"_value":{"value":7644}}]}},{"key":"test.example.greeter.SayHello/sleep","doc_count":432779,"end_time":{"buckets":[{"key":1747630080000000,"doc_count":5930,"_value":{"value":5930}},{"key":1747650240000000,"doc_count":14529,"_value":{"value":14529}},{"key":1747670400000000,"doc_count":14434,"_value":{"value":14434}},{"key":1747690560000000,"doc_count":14525,"_value":{"value":14525}},{"key":1747710720000000,"doc_count":14534,"_value":{"value":14534}},{"key":1747730880000000,"doc_count":14444,"_value":{"value":14444}},{"key":1747751040000000,"doc_count":14461,"_value":{"value":14461}},{"key":1747771200000000,"doc_count":14466,"_value":{"value":14466}},{"key":1747791360000000,"doc_count":14501,"_value":{"value":14501}},{"key":1747811520000000,"doc_count":14533,"_value":{"value":14533}},{"key":1747831680000000,"doc_count":14482,"_value":{"value":14482}},{"key":1747851840000000,"doc_count":14448,"_value":{"value":14448}},{"key":1747872000000000,"doc_count":14467,"_value":{"value":14467}},{"key":1747892160000000,"doc_count":14395,"_value":{"value":14395}},{"key":1747912320000000,"doc_count":14213,"_value":{"value":14213}},{"key":1747932480000000,"doc_count":14255,"_value":{"value":14255}},{"key":1747952640000000,"doc_count":14245,"_value":{"value":14245}},{"key":1747972800000000,"doc_count":14281,"_value":{"value":14281}},{"key":1747992960000000,"doc_count":14496,"_value":{"value":14496}},{"key":1748013120000000,"doc_count":14539,"_value":{"value":14539}},{"key":1748033280000000,"doc_count":14536,"_value":{"value":14536}},{"key":1748053440000000,"doc_count":14454,"_value":{"value":14454}},{"key":1748073600000000,"doc_count":14526,"_value":{"value":14526}},{"key":1748093760000000,"doc_count":14527,"_value":{"value":14527}},{"key":1748113920000000,"doc_count":14445,"_value":{"value":14445}},{"key":1748134080000000,"doc_count":14538,"_value":{"value":14538}},{"key":1748154240000000,"doc_count":14465,"_value":{"value":14465}},{"key":1748174400000000,"doc_count":14529,"_value":{"value":14529}},{"key":1748194560000000,"doc_count":14542,"_value":{"value":14542}},{"key":1748214720000000,"doc_count":14535,"_value":{"value":14535}},{"key":1748234880000000,"doc_count":7504,"_value":{"value":7504}}]}}]}}}`,

		// test for not nested and time group
		`{"aggregations":{"span_name":{"aggregations":{"end_time":{"aggregations":{"_value":{"value_count":{"field":"span_name"}}},"date_histogram":{"extended_bounds":{"max":1748940259000000,"min":1748936649000000},"field":"end_time","interval":"2000m","min_doc_count":0}}},"terms":{"field":"span_name","include":["SELECT","build-metadata-query","query-ts-to-query-metric","check-must-query-feature-flag","HTTP POST"],"missing":" ","size":10000}}},"query":{"bool":{"filter":[{"bool":{"should":[{"match_phrase":{"span_name":{"query":"SELECT"}}},{"match_phrase":{"span_name":{"query":"build-metadata-query"}}},{"match_phrase":{"span_name":{"query":"query-ts-to-query-metric"}}},{"match_phrase":{"span_name":{"query":"check-must-query-feature-flag"}}},{"match_phrase":{"span_name":{"query":"HTTP POST"}}}]}},{"range":{"end_time":{"from":1748936649000000,"include_lower":true,"include_upper":true,"to":1748940259000000}}}]}},"size":0,"sort":[{"time":{"order":"desc"}}]}`: `{"took":664,"timed_out":false,"_shards":{"total":18,"successful":18,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"span_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"SELECT","doc_count":348008,"end_time":{"buckets":[{"key":1748936640000000,"doc_count":6708,"_value":{"value":6708}},{"key":1748936760000000,"doc_count":10553,"_value":{"value":10553}},{"key":1748936880000000,"doc_count":12858,"_value":{"value":12858}},{"key":1748937000000000,"doc_count":11162,"_value":{"value":11162}},{"key":1748937120000000,"doc_count":9563,"_value":{"value":9563}},{"key":1748937240000000,"doc_count":10434,"_value":{"value":10434}},{"key":1748937360000000,"doc_count":10171,"_value":{"value":10171}},{"key":1748937480000000,"doc_count":11727,"_value":{"value":11727}},{"key":1748937600000000,"doc_count":10559,"_value":{"value":10559}},{"key":1748937720000000,"doc_count":14119,"_value":{"value":14119}},{"key":1748937840000000,"doc_count":12566,"_value":{"value":12566}},{"key":1748937960000000,"doc_count":11628,"_value":{"value":11628}},{"key":1748938080000000,"doc_count":12620,"_value":{"value":12620}},{"key":1748938200000000,"doc_count":12409,"_value":{"value":12409}},{"key":1748938320000000,"doc_count":11037,"_value":{"value":11037}},{"key":1748938440000000,"doc_count":14292,"_value":{"value":14292}},{"key":1748938560000000,"doc_count":13506,"_value":{"value":13506}},{"key":1748938680000000,"doc_count":10988,"_value":{"value":10988}},{"key":1748938800000000,"doc_count":10391,"_value":{"value":10391}},{"key":1748938920000000,"doc_count":12972,"_value":{"value":12972}},{"key":1748939040000000,"doc_count":12814,"_value":{"value":12814}},{"key":1748939160000000,"doc_count":12226,"_value":{"value":12226}},{"key":1748939280000000,"doc_count":13951,"_value":{"value":13951}},{"key":1748939400000000,"doc_count":11659,"_value":{"value":11659}},{"key":1748939520000000,"doc_count":12034,"_value":{"value":12034}},{"key":1748939640000000,"doc_count":12894,"_value":{"value":12894}},{"key":1748939760000000,"doc_count":9558,"_value":{"value":9558}},{"key":1748939880000000,"doc_count":9821,"_value":{"value":9821}},{"key":1748940000000000,"doc_count":10974,"_value":{"value":10974}},{"key":1748940120000000,"doc_count":10108,"_value":{"value":10108}},{"key":1748940240000000,"doc_count":1706,"_value":{"value":1706}}]}},{"key":"build-metadata-query","doc_count":244308,"end_time":{"buckets":[{"key":1748936640000000,"doc_count":6556,"_value":{"value":6556}},{"key":1748936760000000,"doc_count":7669,"_value":{"value":7669}},{"key":1748936880000000,"doc_count":8004,"_value":{"value":8004}},{"key":1748937000000000,"doc_count":8018,"_value":{"value":8018}},{"key":1748937120000000,"doc_count":7863,"_value":{"value":7863}},{"key":1748937240000000,"doc_count":7875,"_value":{"value":7875}},{"key":1748937360000000,"doc_count":7682,"_value":{"value":7682}},{"key":1748937480000000,"doc_count":8105,"_value":{"value":8105}},{"key":1748937600000000,"doc_count":9063,"_value":{"value":9063}},{"key":1748937720000000,"doc_count":8108,"_value":{"value":8108}},{"key":1748937840000000,"doc_count":8105,"_value":{"value":8105}},{"key":1748937960000000,"doc_count":7870,"_value":{"value":7870}},{"key":1748938080000000,"doc_count":8123,"_value":{"value":8123}},{"key":1748938200000000,"doc_count":8641,"_value":{"value":8641}},{"key":1748938320000000,"doc_count":8427,"_value":{"value":8427}},{"key":1748938440000000,"doc_count":9207,"_value":{"value":9207}},{"key":1748938560000000,"doc_count":8305,"_value":{"value":8305}},{"key":1748938680000000,"doc_count":7807,"_value":{"value":7807}},{"key":1748938800000000,"doc_count":8275,"_value":{"value":8275}},{"key":1748938920000000,"doc_count":8159,"_value":{"value":8159}},{"key":1748939040000000,"doc_count":7912,"_value":{"value":7912}},{"key":1748939160000000,"doc_count":8390,"_value":{"value":8390}},{"key":1748939280000000,"doc_count":8040,"_value":{"value":8040}},{"key":1748939400000000,"doc_count":8795,"_value":{"value":8795}},{"key":1748939520000000,"doc_count":8190,"_value":{"value":8190}},{"key":1748939640000000,"doc_count":8110,"_value":{"value":8110}},{"key":1748939760000000,"doc_count":7794,"_value":{"value":7794}},{"key":1748939880000000,"doc_count":8249,"_value":{"value":8249}},{"key":1748940000000000,"doc_count":8477,"_value":{"value":8477}},{"key":1748940120000000,"doc_count":8015,"_value":{"value":8015}},{"key":1748940240000000,"doc_count":474,"_value":{"value":474}}]}},{"key":"query-ts-to-query-metric","doc_count":244246,"end_time":{"buckets":[{"key":1748936640000000,"doc_count":6518,"_value":{"value":6518}},{"key":1748936760000000,"doc_count":7755,"_value":{"value":7755}},{"key":1748936880000000,"doc_count":8031,"_value":{"value":8031}},{"key":1748937000000000,"doc_count":8098,"_value":{"value":8098}},{"key":1748937120000000,"doc_count":7935,"_value":{"value":7935}},{"key":1748937240000000,"doc_count":7823,"_value":{"value":7823}},{"key":1748937360000000,"doc_count":7721,"_value":{"value":7721}},{"key":1748937480000000,"doc_count":7932,"_value":{"value":7932}},{"key":1748937600000000,"doc_count":9653,"_value":{"value":9653}},{"key":1748937720000000,"doc_count":8050,"_value":{"value":8050}},{"key":1748937840000000,"doc_count":7874,"_value":{"value":7874}},{"key":1748937960000000,"doc_count":7721,"_value":{"value":7721}},{"key":1748938080000000,"doc_count":7947,"_value":{"value":7947}},{"key":1748938200000000,"doc_count":8591,"_value":{"value":8591}},{"key":1748938320000000,"doc_count":8146,"_value":{"value":8146}},{"key":1748938440000000,"doc_count":8742,"_value":{"value":8742}},{"key":1748938560000000,"doc_count":8157,"_value":{"value":8157}},{"key":1748938680000000,"doc_count":7746,"_value":{"value":7746}},{"key":1748938800000000,"doc_count":8919,"_value":{"value":8919}},{"key":1748938920000000,"doc_count":8179,"_value":{"value":8179}},{"key":1748939040000000,"doc_count":7846,"_value":{"value":7846}},{"key":1748939160000000,"doc_count":8345,"_value":{"value":8345}},{"key":1748939280000000,"doc_count":8003,"_value":{"value":8003}},{"key":1748939400000000,"doc_count":8806,"_value":{"value":8806}},{"key":1748939520000000,"doc_count":8182,"_value":{"value":8182}},{"key":1748939640000000,"doc_count":8055,"_value":{"value":8055}},{"key":1748939760000000,"doc_count":7827,"_value":{"value":7827}},{"key":1748939880000000,"doc_count":8064,"_value":{"value":8064}},{"key":1748940000000000,"doc_count":8980,"_value":{"value":8980}},{"key":1748940120000000,"doc_count":8018,"_value":{"value":8018}},{"key":1748940240000000,"doc_count":582,"_value":{"value":582}}]}},{"key":"check-must-query-feature-flag","doc_count":242879,"end_time":{"buckets":[{"key":1748936640000000,"doc_count":6522,"_value":{"value":6522}},{"key":1748936760000000,"doc_count":7609,"_value":{"value":7609}},{"key":1748936880000000,"doc_count":7887,"_value":{"value":7887}},{"key":1748937000000000,"doc_count":7960,"_value":{"value":7960}},{"key":1748937120000000,"doc_count":7821,"_value":{"value":7821}},{"key":1748937240000000,"doc_count":7811,"_value":{"value":7811}},{"key":1748937360000000,"doc_count":7667,"_value":{"value":7667}},{"key":1748937480000000,"doc_count":8084,"_value":{"value":8084}},{"key":1748937600000000,"doc_count":9048,"_value":{"value":9048}},{"key":1748937720000000,"doc_count":8102,"_value":{"value":8102}},{"key":1748937840000000,"doc_count":8105,"_value":{"value":8105}},{"key":1748937960000000,"doc_count":7856,"_value":{"value":7856}},{"key":1748938080000000,"doc_count":8100,"_value":{"value":8100}},{"key":1748938200000000,"doc_count":8587,"_value":{"value":8587}},{"key":1748938320000000,"doc_count":8418,"_value":{"value":8418}},{"key":1748938440000000,"doc_count":9132,"_value":{"value":9132}},{"key":1748938560000000,"doc_count":8260,"_value":{"value":8260}},{"key":1748938680000000,"doc_count":7798,"_value":{"value":7798}},{"key":1748938800000000,"doc_count":8231,"_value":{"value":8231}},{"key":1748938920000000,"doc_count":8132,"_value":{"value":8132}},{"key":1748939040000000,"doc_count":7876,"_value":{"value":7876}},{"key":1748939160000000,"doc_count":7995,"_value":{"value":7995}},{"key":1748939280000000,"doc_count":7933,"_value":{"value":7933}},{"key":1748939400000000,"doc_count":8785,"_value":{"value":8785}},{"key":1748939520000000,"doc_count":8160,"_value":{"value":8160}},{"key":1748939640000000,"doc_count":8096,"_value":{"value":8096}},{"key":1748939760000000,"doc_count":7783,"_value":{"value":7783}},{"key":1748939880000000,"doc_count":8243,"_value":{"value":8243}},{"key":1748940000000000,"doc_count":8443,"_value":{"value":8443}},{"key":1748940120000000,"doc_count":7974,"_value":{"value":7974}},{"key":1748940240000000,"doc_count":461,"_value":{"value":461}}]}},{"key":"HTTP POST","doc_count":196251,"end_time":{"buckets":[{"key":1748936640000000,"doc_count":4030,"_value":{"value":4030}},{"key":1748936760000000,"doc_count":6054,"_value":{"value":6054}},{"key":1748936880000000,"doc_count":6267,"_value":{"value":6267}},{"key":1748937000000000,"doc_count":6852,"_value":{"value":6852}},{"key":1748937120000000,"doc_count":6175,"_value":{"value":6175}},{"key":1748937240000000,"doc_count":6228,"_value":{"value":6228}},{"key":1748937360000000,"doc_count":5939,"_value":{"value":5939}},{"key":1748937480000000,"doc_count":6460,"_value":{"value":6460}},{"key":1748937600000000,"doc_count":7292,"_value":{"value":7292}},{"key":1748937720000000,"doc_count":6527,"_value":{"value":6527}},{"key":1748937840000000,"doc_count":6510,"_value":{"value":6510}},{"key":1748937960000000,"doc_count":6298,"_value":{"value":6298}},{"key":1748938080000000,"doc_count":6561,"_value":{"value":6561}},{"key":1748938200000000,"doc_count":6995,"_value":{"value":6995}},{"key":1748938320000000,"doc_count":7051,"_value":{"value":7051}},{"key":1748938440000000,"doc_count":6930,"_value":{"value":6930}},{"key":1748938560000000,"doc_count":6774,"_value":{"value":6774}},{"key":1748938680000000,"doc_count":6264,"_value":{"value":6264}},{"key":1748938800000000,"doc_count":6646,"_value":{"value":6646}},{"key":1748938920000000,"doc_count":6862,"_value":{"value":6862}},{"key":1748939040000000,"doc_count":6341,"_value":{"value":6341}},{"key":1748939160000000,"doc_count":6457,"_value":{"value":6457}},{"key":1748939280000000,"doc_count":6502,"_value":{"value":6502}},{"key":1748939400000000,"doc_count":7303,"_value":{"value":7303}},{"key":1748939520000000,"doc_count":6932,"_value":{"value":6932}},{"key":1748939640000000,"doc_count":6820,"_value":{"value":6820}},{"key":1748939760000000,"doc_count":6156,"_value":{"value":6156}},{"key":1748939880000000,"doc_count":7334,"_value":{"value":7334}},{"key":1748940000000000,"doc_count":6940,"_value":{"value":6940}},{"key":1748940120000000,"doc_count":6187,"_value":{"value":6187}},{"key":1748940240000000,"doc_count":564,"_value":{"value":564}}]}}]}}}`,

		// test for nested and not nested time group
		`{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"end_time":{"aggregations":{"events":{"aggregations":{"_value":{"value_count":{"field":"events.name"}}},"nested":{"path":"events"}}},"date_histogram":{"extended_bounds":{"max":1748940259000000,"min":1748936649000000},"field":"end_time","interval":"2000m","min_doc_count":0}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" ","size":5}}},"nested":{"path":"events"}}},"query":{"bool":{"filter":{"range":{"end_time":{"from":1748936649000000,"include_lower":true,"include_upper":true,"to":1748940259000000}}}}},"size":0,"sort":[{"time":{"order":"desc"}}]}`: `{"took":111,"timed_out":false,"_shards":{"total":18,"successful":18,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"events":{"doc_count":711,"events.name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"exception","doc_count":711,"reverse_nested":{"doc_count":711,"end_time":{"buckets":[{"key":1748936640000000,"doc_count":12,"events":{"doc_count":12,"_value":{"value":12}}},{"key":1748936760000000,"doc_count":42,"events":{"doc_count":42,"_value":{"value":42}}},{"key":1748936880000000,"doc_count":18,"events":{"doc_count":18,"_value":{"value":18}}},{"key":1748937000000000,"doc_count":15,"events":{"doc_count":15,"_value":{"value":15}}},{"key":1748937120000000,"doc_count":19,"events":{"doc_count":19,"_value":{"value":19}}},{"key":1748937240000000,"doc_count":19,"events":{"doc_count":19,"_value":{"value":19}}},{"key":1748937360000000,"doc_count":18,"events":{"doc_count":18,"_value":{"value":18}}},{"key":1748937480000000,"doc_count":15,"events":{"doc_count":15,"_value":{"value":15}}},{"key":1748937600000000,"doc_count":34,"events":{"doc_count":34,"_value":{"value":34}}},{"key":1748937720000000,"doc_count":16,"events":{"doc_count":16,"_value":{"value":16}}},{"key":1748937840000000,"doc_count":16,"events":{"doc_count":16,"_value":{"value":16}}},{"key":1748937960000000,"doc_count":32,"events":{"doc_count":32,"_value":{"value":32}}},{"key":1748938080000000,"doc_count":97,"events":{"doc_count":97,"_value":{"value":97}}},{"key":1748938200000000,"doc_count":14,"events":{"doc_count":14,"_value":{"value":14}}},{"key":1748938320000000,"doc_count":18,"events":{"doc_count":18,"_value":{"value":18}}},{"key":1748938440000000,"doc_count":15,"events":{"doc_count":15,"_value":{"value":15}}},{"key":1748938560000000,"doc_count":20,"events":{"doc_count":20,"_value":{"value":20}}},{"key":1748938680000000,"doc_count":14,"events":{"doc_count":14,"_value":{"value":14}}},{"key":1748938800000000,"doc_count":18,"events":{"doc_count":18,"_value":{"value":18}}},{"key":1748938920000000,"doc_count":16,"events":{"doc_count":16,"_value":{"value":16}}},{"key":1748939040000000,"doc_count":26,"events":{"doc_count":26,"_value":{"value":26}}},{"key":1748939160000000,"doc_count":16,"events":{"doc_count":16,"_value":{"value":16}}},{"key":1748939280000000,"doc_count":32,"events":{"doc_count":32,"_value":{"value":32}}},{"key":1748939400000000,"doc_count":31,"events":{"doc_count":31,"_value":{"value":31}}},{"key":1748939520000000,"doc_count":48,"events":{"doc_count":48,"_value":{"value":48}}},{"key":1748939640000000,"doc_count":20,"events":{"doc_count":20,"_value":{"value":20}}},{"key":1748939760000000,"doc_count":16,"events":{"doc_count":16,"_value":{"value":16}}},{"key":1748939880000000,"doc_count":18,"events":{"doc_count":18,"_value":{"value":18}}},{"key":1748940000000000,"doc_count":14,"events":{"doc_count":14,"_value":{"value":14}}},{"key":1748940120000000,"doc_count":16,"events":{"doc_count":16,"_value":{"value":16}}},{"key":1748940240000000,"doc_count":6,"events":{"doc_count":6,"_value":{"value":6}}}]}}}]}}}}`,

		// test for nested term and not nested time and field value
		`{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"end_time":{"aggregations":{"_value":{"value_count":{"field":"span_name"}}},"date_histogram":{"extended_bounds":{"max":1748940259000000,"min":1748936649000000},"field":"end_time","interval":"2000m","min_doc_count":0}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" ","size":5}}},"nested":{"path":"events"}}},"query":{"bool":{"filter":{"range":{"end_time":{"from":1748936649000000,"include_lower":true,"include_upper":true,"to":1748940259000000}}}}},"size":0,"sort":[{"time":{"order":"desc"}}]}`: `{"took":83,"timed_out":false,"_shards":{"total":18,"successful":18,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"events":{"doc_count":711,"events.name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"exception","doc_count":711,"reverse_nested":{"doc_count":711,"end_time":{"buckets":[{"key":1748936640000000,"doc_count":12,"_value":{"value":12}},{"key":1748936760000000,"doc_count":42,"_value":{"value":42}},{"key":1748936880000000,"doc_count":18,"_value":{"value":18}},{"key":1748937000000000,"doc_count":15,"_value":{"value":15}},{"key":1748937120000000,"doc_count":19,"_value":{"value":19}},{"key":1748937240000000,"doc_count":19,"_value":{"value":19}},{"key":1748937360000000,"doc_count":18,"_value":{"value":18}},{"key":1748937480000000,"doc_count":15,"_value":{"value":15}},{"key":1748937600000000,"doc_count":34,"_value":{"value":34}},{"key":1748937720000000,"doc_count":16,"_value":{"value":16}},{"key":1748937840000000,"doc_count":16,"_value":{"value":16}},{"key":1748937960000000,"doc_count":32,"_value":{"value":32}},{"key":1748938080000000,"doc_count":97,"_value":{"value":97}},{"key":1748938200000000,"doc_count":14,"_value":{"value":14}},{"key":1748938320000000,"doc_count":18,"_value":{"value":18}},{"key":1748938440000000,"doc_count":15,"_value":{"value":15}},{"key":1748938560000000,"doc_count":20,"_value":{"value":20}},{"key":1748938680000000,"doc_count":14,"_value":{"value":14}},{"key":1748938800000000,"doc_count":18,"_value":{"value":18}},{"key":1748938920000000,"doc_count":16,"_value":{"value":16}},{"key":1748939040000000,"doc_count":26,"_value":{"value":26}},{"key":1748939160000000,"doc_count":16,"_value":{"value":16}},{"key":1748939280000000,"doc_count":32,"_value":{"value":32}},{"key":1748939400000000,"doc_count":31,"_value":{"value":31}},{"key":1748939520000000,"doc_count":48,"_value":{"value":48}},{"key":1748939640000000,"doc_count":20,"_value":{"value":20}},{"key":1748939760000000,"doc_count":16,"_value":{"value":16}},{"key":1748939880000000,"doc_count":18,"_value":{"value":18}},{"key":1748940000000000,"doc_count":14,"_value":{"value":14}},{"key":1748940120000000,"doc_count":16,"_value":{"value":16}},{"key":1748940240000000,"doc_count":6,"_value":{"value":6}}]}}}]}}}}`,

		// test for nested and not nested
		`{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"_value":{"value_count":{"field":"span_name"}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" ","size":10000}}},"nested":{"path":"events"}}},"query":{"bool":{"filter":[{"bool":{"should":[{"match_phrase":{"span_name":{"query":"SELECT"}}},{"match_phrase":{"span_name":{"query":"build-metadata-query"}}},{"match_phrase":{"span_name":{"query":"query-ts-to-query-metric"}}},{"match_phrase":{"span_name":{"query":"check-must-query-feature-flag"}}},{"match_phrase":{"span_name":{"query":"HTTP POST"}}}]}},{"range":{"end_time":{"from":1748936649000000,"include_lower":true,"include_upper":true,"to":1748940259000000}}}]}},"size":0,"sort":[{"time":{"order":"desc"}}]}`: `{"took":59,"timed_out":false,"_shards":{"total":18,"successful":18,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"events":{"doc_count":1,"events.name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"exception","doc_count":1,"reverse_nested":{"doc_count":1,"_value":{"value":1}}}]}}}}`,
	})

	for i, c := range map[string]struct {
		queryTsString string
		queryTs       *structured.QueryTs
		result        string
	}{
		"test for apm trace_tilapia empty": {
			queryTsString: `{"space_uid":"bkcc__2","query_list":[{"data_source":"bklog","table_id":"result_table.es_with_time_filed","field_name":"span_name","function":[{"method":"count","dimensions":["span_name"],"window":"20160s"}],"reference_name":"a","dimensions":["span_name"],"conditions":{"field_list":[{"field_name":"span_name","value":[""],"op":"ne"}]}}],"metric_merge":"a","start_time":"1747641987","end_time":"1748245394","step":"20160s","timezone":"Asia/Shanghai","look_back_delta":"1m","instant":false}`,
			result:        `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["/trpc.example.greeter.Greeter/SayHello"],"values":[[1747630080000,12124],[1747650240000,29654],[1747670400000,29482],[1747690560000,29672],[1747710720000,29660],[1747730880000,29457],[1747751040000,29506],[1747771200000,29475],[1747791360000,29642],[1747811520000,29639],[1747831680000,29629],[1747851840000,29498],[1747872000000,29492],[1747892160000,29346],[1747912320000,29055],[1747932480000,29116],[1747952640000,29132],[1747972800000,29109],[1747992960000,29576],[1748013120000,29656],[1748033280000,29664],[1748053440000,29467],[1748073600000,29676],[1748093760000,29654],[1748113920000,29494],[1748134080000,29668],[1748154240000,29508],[1748174400000,29668],[1748194560000,29672],[1748214720000,29666],[1748234880000,15288]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["/trpc.example.greeter.Greeter/SayHi"],"values":[[1747630080000,11860],[1747650240000,29057],[1747670400000,28868],[1747690560000,29050],[1747710720000,29068],[1747730880000,28883],[1747751040000,28914],[1747771200000,28934],[1747791360000,29011],[1747811520000,29063],[1747831680000,28963],[1747851840000,28896],[1747872000000,28934],[1747892160000,28790],[1747912320000,28426],[1747932480000,28512],[1747952640000,28490],[1747972800000,28560],[1747992960000,28992],[1748013120000,29080],[1748033280000,29072],[1748053440000,28908],[1748073600000,29052],[1748093760000,29054],[1748113920000,28890],[1748134080000,29076],[1748154240000,28930],[1748174400000,29058],[1748194560000,29084],[1748214720000,29070],[1748234880000,15008]]},{"name":"_result2","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["SELECT"],"values":[[1747630080000,83221],[1747650240000,48270],[1747670400000,11389],[1747690560000,42125],[1747710720000,127370],[1747730880000,140077],[1747751040000,19016],[1747771200000,21385],[1747791360000,165395],[1747811520000,209679],[1747831680000,49772],[1747851840000,19983],[1747872000000,143679],[1747892160000,178952],[1747912320000,60992],[1747932480000,44126],[1747952640000,63272],[1747972800000,79260],[1747992960000,22578],[1748013120000,5817],[1748033280000,5874],[1748053440000,4371],[1748073600000,1128],[1748093760000,1106],[1748113920000,1099],[1748134080000,1130],[1748154240000,1084],[1748174400000,1073],[1748194560000,1093],[1748214720000,36526],[1748234880000,66756]]},{"name":"_result3","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["internalSpanDoSomething"],"values":[[1747630080000,6061],[1747650240000,14829],[1747670400000,14741],[1747690560000,14836],[1747710720000,14830],[1747730880000,14725],[1747751040000,14753],[1747771200000,14739],[1747791360000,14817],[1747811520000,14822],[1747831680000,14816],[1747851840000,14748],[1747872000000,14746],[1747892160000,14673],[1747912320000,14533],[1747932480000,14557],[1747952640000,14566],[1747972800000,14556],[1747992960000,14788],[1748013120000,14829],[1748033280000,14832],[1748053440000,14738],[1748073600000,14837],[1748093760000,14827],[1748113920000,14747],[1748134080000,14834],[1748154240000,14754],[1748174400000,14834],[1748194560000,14836],[1748214720000,14833],[1748234880000,7644]]},{"name":"_result4","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["test.example.greeter.SayHello/sleep"],"values":[[1747630080000,5930],[1747650240000,14529],[1747670400000,14434],[1747690560000,14525],[1747710720000,14534],[1747730880000,14444],[1747751040000,14461],[1747771200000,14466],[1747791360000,14501],[1747811520000,14533],[1747831680000,14482],[1747851840000,14448],[1747872000000,14467],[1747892160000,14395],[1747912320000,14213],[1747932480000,14255],[1747952640000,14245],[1747972800000,14281],[1747992960000,14496],[1748013120000,14539],[1748033280000,14536],[1748053440000,14454],[1748073600000,14526],[1748093760000,14527],[1748113920000,14445],[1748134080000,14538],[1748154240000,14465],[1748174400000,14529],[1748194560000,14542],[1748214720000,14535],[1748234880000,7504]]}]`,
		},
		"test for not nested and time group": {
			queryTsString: `{"space_uid":"bkcc__2","query_list":[{"data_source":"bklog","table_id":"result_table.es_with_time_filed","field_name":"span_name","is_regexp":false,"function":[{"method":"count","dimensions":["span_name"],"window":"120s"}],"time_aggregation":{},"is_dom_sampled":false,"reference_name":"a","dimensions":["span_name"],"limit":200000,"conditions":{"field_list":[{"field_name":"span_name","value":["SELECT","build-metadata-query","query-ts-to-query-metric","check-must-query-feature-flag","HTTP POST"],"op":"eq"}]},"query_string":"*","is_prefix":false}],"metric_merge":"a","order_by":["-time"],"start_time":"1748936649","end_time":"1748940259","step":"120s","timezone":"Asia/Shanghai","look_back_delta":"1m","instant":false}`,
			result:        `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["HTTP POST"],"values":[[1748936640000,4030],[1748936760000,6054],[1748936880000,6267],[1748937000000,6852],[1748937120000,6175],[1748937240000,6228],[1748937360000,5939],[1748937480000,6460],[1748937600000,7292],[1748937720000,6527],[1748937840000,6510],[1748937960000,6298],[1748938080000,6561],[1748938200000,6995],[1748938320000,7051],[1748938440000,6930],[1748938560000,6774],[1748938680000,6264],[1748938800000,6646],[1748938920000,6862],[1748939040000,6341],[1748939160000,6457],[1748939280000,6502],[1748939400000,7303],[1748939520000,6932],[1748939640000,6820],[1748939760000,6156],[1748939880000,7334],[1748940000000,6940],[1748940120000,6187],[1748940240000,564]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["SELECT"],"values":[[1748936640000,6708],[1748936760000,10553],[1748936880000,12858],[1748937000000,11162],[1748937120000,9563],[1748937240000,10434],[1748937360000,10171],[1748937480000,11727],[1748937600000,10559],[1748937720000,14119],[1748937840000,12566],[1748937960000,11628],[1748938080000,12620],[1748938200000,12409],[1748938320000,11037],[1748938440000,14292],[1748938560000,13506],[1748938680000,10988],[1748938800000,10391],[1748938920000,12972],[1748939040000,12814],[1748939160000,12226],[1748939280000,13951],[1748939400000,11659],[1748939520000,12034],[1748939640000,12894],[1748939760000,9558],[1748939880000,9821],[1748940000000,10974],[1748940120000,10108],[1748940240000,1706]]},{"name":"_result2","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["build-metadata-query"],"values":[[1748936640000,6556],[1748936760000,7669],[1748936880000,8004],[1748937000000,8018],[1748937120000,7863],[1748937240000,7875],[1748937360000,7682],[1748937480000,8105],[1748937600000,9063],[1748937720000,8108],[1748937840000,8105],[1748937960000,7870],[1748938080000,8123],[1748938200000,8641],[1748938320000,8427],[1748938440000,9207],[1748938560000,8305],[1748938680000,7807],[1748938800000,8275],[1748938920000,8159],[1748939040000,7912],[1748939160000,8390],[1748939280000,8040],[1748939400000,8795],[1748939520000,8190],[1748939640000,8110],[1748939760000,7794],[1748939880000,8249],[1748940000000,8477],[1748940120000,8015],[1748940240000,474]]},{"name":"_result3","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["check-must-query-feature-flag"],"values":[[1748936640000,6522],[1748936760000,7609],[1748936880000,7887],[1748937000000,7960],[1748937120000,7821],[1748937240000,7811],[1748937360000,7667],[1748937480000,8084],[1748937600000,9048],[1748937720000,8102],[1748937840000,8105],[1748937960000,7856],[1748938080000,8100],[1748938200000,8587],[1748938320000,8418],[1748938440000,9132],[1748938560000,8260],[1748938680000,7798],[1748938800000,8231],[1748938920000,8132],[1748939040000,7876],[1748939160000,7995],[1748939280000,7933],[1748939400000,8785],[1748939520000,8160],[1748939640000,8096],[1748939760000,7783],[1748939880000,8243],[1748940000000,8443],[1748940120000,7974],[1748940240000,461]]},{"name":"_result4","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["query-ts-to-query-metric"],"values":[[1748936640000,6518],[1748936760000,7755],[1748936880000,8031],[1748937000000,8098],[1748937120000,7935],[1748937240000,7823],[1748937360000,7721],[1748937480000,7932],[1748937600000,9653],[1748937720000,8050],[1748937840000,7874],[1748937960000,7721],[1748938080000,7947],[1748938200000,8591],[1748938320000,8146],[1748938440000,8742],[1748938560000,8157],[1748938680000,7746],[1748938800000,8919],[1748938920000,8179],[1748939040000,7846],[1748939160000,8345],[1748939280000,8003],[1748939400000,8806],[1748939520000,8182],[1748939640000,8055],[1748939760000,7827],[1748939880000,8064],[1748940000000,8980],[1748940120000,8018],[1748940240000,582]]}]`,
		},
		"test for nested and not nested time group": {
			queryTsString: `{"space_uid":"bkcc__2","query_list":[{"data_source":"bklog","table_id":"result_table.es_with_time_filed","field_name":"events.name","is_regexp":false,"function":[{"method":"count","dimensions":["events.name"],"window":"120s"}],"time_aggregation":{},"is_dom_sampled":false,"reference_name":"a","dimensions":["span_name"],"limit":5}],"metric_merge":"a","order_by":["-time"],"start_time":"1748936649","end_time":"1748940259","step":"120s","timezone":"Asia/Shanghai","look_back_delta":"1m","instant":false}`,
			result:        `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["events.name"],"group_values":["exception"],"values":[[1748936640000,12],[1748936760000,42],[1748936880000,18],[1748937000000,15],[1748937120000,19],[1748937240000,19],[1748937360000,18],[1748937480000,15],[1748937600000,34],[1748937720000,16],[1748937840000,16],[1748937960000,32],[1748938080000,97],[1748938200000,14],[1748938320000,18],[1748938440000,15],[1748938560000,20],[1748938680000,14],[1748938800000,18],[1748938920000,16],[1748939040000,26],[1748939160000,16],[1748939280000,32],[1748939400000,31],[1748939520000,48],[1748939640000,20],[1748939760000,16],[1748939880000,18],[1748940000000,14],[1748940120000,16],[1748940240000,6]]}]`,
		},
		"test for nested term and not nested time and field value": {
			queryTsString: `{"space_uid":"bkcc__2","query_list":[{"data_source":"bklog","table_id":"result_table.es_with_time_filed","field_name":"span_name","is_regexp":false,"function":[{"method":"count","dimensions":["events.name"],"window":"120s"}],"time_aggregation":{},"is_dom_sampled":false,"reference_name":"a","dimensions":["span_name"],"limit":5}],"metric_merge":"a","order_by":["-time"],"start_time":"1748936649","end_time":"1748940259","step":"120s","timezone":"Asia/Shanghai","look_back_delta":"1m","instant":false}`,
			result:        `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["events.name"],"group_values":["exception"],"values":[[1748936640000,12],[1748936760000,42],[1748936880000,18],[1748937000000,15],[1748937120000,19],[1748937240000,19],[1748937360000,18],[1748937480000,15],[1748937600000,34],[1748937720000,16],[1748937840000,16],[1748937960000,32],[1748938080000,97],[1748938200000,14],[1748938320000,18],[1748938440000,15],[1748938560000,20],[1748938680000,14],[1748938800000,18],[1748938920000,16],[1748939040000,26],[1748939160000,16],[1748939280000,32],[1748939400000,31],[1748939520000,48],[1748939640000,20],[1748939760000,16],[1748939880000,18],[1748940000000,14],[1748940120000,16],[1748940240000,6]]}]`,
		},
		"test for nested and not nested": {
			queryTsString: `{"space_uid":"bkcc__2","query_list":[{"data_source":"bklog","table_id":"result_table.es_with_time_filed","field_name":"span_name","is_regexp":false,"function":[{"method":"count","dimensions":["events.name"]}],"time_aggregation":{},"is_dom_sampled":false,"reference_name":"a","dimensions":["span_name"],"limit":200000,"conditions":{"field_list":[{"field_name":"span_name","value":["SELECT","build-metadata-query","query-ts-to-query-metric","check-must-query-feature-flag","HTTP POST"],"op":"eq"}]},"query_string":"*","is_prefix":false}],"metric_merge":"a","order_by":["-time"],"start_time":"1748936649","end_time":"1748940259","step":"120s","timezone":"Asia/Shanghai","look_back_delta":"1m","instant":false}`,
			result:        `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["events.name"],"group_values":["exception"],"values":[[1748936649000,1]]}]`,
		},
		"test for apm trace_tilapia duibi": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableEsWithTimeFiled),
						FieldName:     "span_name",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "count",
								Dimensions: []string{"span_name"},
								Window:     "20160s",
							},
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "span_name",
									Value:         []string{""},
									Operator:      "ne",
									IsPrefix:      false,
									IsSuffix:      false,
									IsWildcard:    false,
								},
							},
						},
					},
				},
				MetricMerge:   "a",
				Start:         "1747641987",
				End:           "1748245394",
				Step:          "20160s",
				LookBackDelta: "1m",
				Timezone:      "Asia/Shanghai",
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["/trpc.example.greeter.Greeter/SayHello"],"values":[[1747630080000,12124],[1747650240000,29654],[1747670400000,29482],[1747690560000,29672],[1747710720000,29660],[1747730880000,29457],[1747751040000,29506],[1747771200000,29475],[1747791360000,29642],[1747811520000,29639],[1747831680000,29629],[1747851840000,29498],[1747872000000,29492],[1747892160000,29346],[1747912320000,29055],[1747932480000,29116],[1747952640000,29132],[1747972800000,29109],[1747992960000,29576],[1748013120000,29656],[1748033280000,29664],[1748053440000,29467],[1748073600000,29676],[1748093760000,29654],[1748113920000,29494],[1748134080000,29668],[1748154240000,29508],[1748174400000,29668],[1748194560000,29672],[1748214720000,29666],[1748234880000,15288]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["/trpc.example.greeter.Greeter/SayHi"],"values":[[1747630080000,11860],[1747650240000,29057],[1747670400000,28868],[1747690560000,29050],[1747710720000,29068],[1747730880000,28883],[1747751040000,28914],[1747771200000,28934],[1747791360000,29011],[1747811520000,29063],[1747831680000,28963],[1747851840000,28896],[1747872000000,28934],[1747892160000,28790],[1747912320000,28426],[1747932480000,28512],[1747952640000,28490],[1747972800000,28560],[1747992960000,28992],[1748013120000,29080],[1748033280000,29072],[1748053440000,28908],[1748073600000,29052],[1748093760000,29054],[1748113920000,28890],[1748134080000,29076],[1748154240000,28930],[1748174400000,29058],[1748194560000,29084],[1748214720000,29070],[1748234880000,15008]]},{"name":"_result2","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["SELECT"],"values":[[1747630080000,83221],[1747650240000,48270],[1747670400000,11389],[1747690560000,42125],[1747710720000,127370],[1747730880000,140077],[1747751040000,19016],[1747771200000,21385],[1747791360000,165395],[1747811520000,209679],[1747831680000,49772],[1747851840000,19983],[1747872000000,143679],[1747892160000,178952],[1747912320000,60992],[1747932480000,44126],[1747952640000,63272],[1747972800000,79260],[1747992960000,22578],[1748013120000,5817],[1748033280000,5874],[1748053440000,4371],[1748073600000,1128],[1748093760000,1106],[1748113920000,1099],[1748134080000,1130],[1748154240000,1084],[1748174400000,1073],[1748194560000,1093],[1748214720000,36526],[1748234880000,66756]]},{"name":"_result3","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["internalSpanDoSomething"],"values":[[1747630080000,6061],[1747650240000,14829],[1747670400000,14741],[1747690560000,14836],[1747710720000,14830],[1747730880000,14725],[1747751040000,14753],[1747771200000,14739],[1747791360000,14817],[1747811520000,14822],[1747831680000,14816],[1747851840000,14748],[1747872000000,14746],[1747892160000,14673],[1747912320000,14533],[1747932480000,14557],[1747952640000,14566],[1747972800000,14556],[1747992960000,14788],[1748013120000,14829],[1748033280000,14832],[1748053440000,14738],[1748073600000,14837],[1748093760000,14827],[1748113920000,14747],[1748134080000,14834],[1748154240000,14754],[1748174400000,14834],[1748194560000,14836],[1748214720000,14833],[1748234880000,7644]]},{"name":"_result4","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["span_name"],"group_values":["test.example.greeter.SayHello/sleep"],"values":[[1747630080000,5930],[1747650240000,14529],[1747670400000,14434],[1747690560000,14525],[1747710720000,14534],[1747730880000,14444],[1747751040000,14461],[1747771200000,14466],[1747791360000,14501],[1747811520000,14533],[1747831680000,14482],[1747851840000,14448],[1747872000000,14467],[1747892160000,14395],[1747912320000,14213],[1747932480000,14255],[1747952640000,14245],[1747972800000,14281],[1747992960000,14496],[1748013120000,14539],[1748033280000,14536],[1748053440000,14454],[1748073600000,14526],[1748093760000,14527],[1748113920000,14445],[1748134080000,14538],[1748154240000,14465],[1748174400000,14529],[1748194560000,14542],[1748214720000,14535],[1748234880000,7504]]}]`,
		},
		"统计数量，毫秒查询": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.UnixMilli(), 10),
				End:         strconv.FormatInt(defaultEnd.UnixMilli(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079123,182355]]}]`,
		},
		"统计数量": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,182486]]}]`,
		},
		"根据维度 __ext.container_name 进行 sum 聚合，同时用值正向排序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         5,
						From:          0,
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "count",
								Dimensions: []string{"__ext.container_name"},
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.UnixMilli(), 10),
				End:         strconv.FormatInt(defaultEnd.UnixMilli(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__ext.container_name"],"group_values":["unify-query"],"values":[[1741154079123,182355]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__ext.container_name"],"group_values":[""],"values":[[1741154079123,4325521]]}]`,
		},
		"根据维度 __ext.container_name 进行 count 聚合，同时用值倒序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         5,
						From:          0,
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "count",
								Dimensions: []string{"__ext.container_name"},
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"-_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__ext.container_name"],"group_values":["unify-query"],"values":[[1741154079000,182486]]}]`,
		},
		"统计 __ext.container_name 和 __ext.io_kubernetes_pod 不为空的文档数量": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.container_name",
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "__ext.io_kubernetes_pod",
									Operator:      "ncontains",
									Value:         []string{""},
								},
								{
									DimensionName: "__ext.container_name",
									Operator:      "ncontains",
									Value:         []string{""},
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,182486]]}]`,
		},
		"a + b": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "b",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
				},
				MetricMerge: "a + b",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,364972]]}]`,
		},
		"__ext.io_kubernetes_pod 统计去重数量": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "cardinality",
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,4]]}]`,
		},
		"__ext.io_kubernetes_pod 统计数量": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "b",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
							{
								Method: "date_histogram",
								Window: "1m",
							},
						},
					},
				},
				MetricMerge: "b",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154040000,3408],[1741154100000,4444],[1741154160000,4577],[1741154220000,4668],[1741154280000,5642],[1741154340000,4860],[1741154400000,35988],[1741154460000,7098],[1741154520000,5287],[1741154580000,5422],[1741154640000,4906],[1741154700000,4447],[1741154760000,4713],[1741154820000,4621],[1741154880000,4417],[1741154940000,5092],[1741155000000,4805],[1741155060000,5545],[1741155120000,4614],[1741155180000,5121],[1741155240000,4854],[1741155300000,5343],[1741155360000,4789],[1741155420000,4755],[1741155480000,5115],[1741155540000,4588],[1741155600000,6474],[1741155660000,5416],[1741155720000,5128],[1741155780000,5050],[1741155840000,1299]]}]`,
		},
		"__ext.io_kubernetes_pod 统计数量，毫秒": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "b",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
							{
								Method: "date_histogram",
								Window: "1m",
							},
						},
					},
				},
				MetricMerge: "b",
				Start:       strconv.FormatInt(defaultStart.UnixMilli(), 10),
				End:         strconv.FormatInt(defaultEnd.UnixMilli(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154040000,3277],[1741154100000,4444],[1741154160000,4577],[1741154220000,4668],[1741154280000,5642],[1741154340000,4860],[1741154400000,35988],[1741154460000,7098],[1741154520000,5287],[1741154580000,5422],[1741154640000,4906],[1741154700000,4447],[1741154760000,4713],[1741154820000,4621],[1741154880000,4417],[1741154940000,5092],[1741155000000,4805],[1741155060000,5545],[1741155120000,4614],[1741155180000,5121],[1741155240000,4854],[1741155300000,5343],[1741155360000,4789],[1741155420000,4755],[1741155480000,5115],[1741155540000,4588],[1741155600000,6474],[1741155660000,5416],[1741155720000,5128],[1741155780000,5050],[1741155840000,1299]]}]`,
		},
		"测试聚合周期大于查询周期": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "dtEventTimeStamp",
						ReferenceName: "b",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
							{
								Method: "date_histogram",
								Window: "1d",
							},
						},
					},
				},
				MetricMerge: "b",
				Start:       "1741320000000",
				End:         "1741341600000",
				Instant:     false,
				SpaceUid:    spaceUid,
				Timezone:    "Asia/Shanghai",
				Step:        "1d",
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741276800000,2367]]}]`,
		},
	} {
		t.Run(fmt.Sprintf("%s", i), func(t *testing.T) {
			metadata.SetUser(ctx, &metadata.User{Key: "username:test", SpaceUID: spaceUid, SkipSpace: "true"})
			var ts *structured.QueryTs
			if c.queryTsString == "" {
				ts = c.queryTs
			} else {
				_ = json.Unmarshal([]byte(c.queryTsString), &ts)
				//nts, _ := json.Marshal(c.queryTs)
				//assert.JSONEq(t, c.queryTsString, string(nts))
			}

			data, err := queryReferenceWithPromEngine(ctx, ts)
			assert.Nil(t, err)

			if err != nil {
				return
			}

			if data.Status != nil && data.Status.Code != "" {
				fmt.Println("code: ", data.Status.Code)
				fmt.Println("message: ", data.Status.Message)
				return
			}

			actual, _ := json.Marshal(data.Tables)
			assert.Equal(t, c.result, string(actual))
		})
	}
}

func TestQueryTs(t *testing.T) {

	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	mock.InfluxDB.Set(map[string]any{
		`SELECT mean("usage") AS _value, "time" AS _time FROM cpu_summary WHERE time > 1677081599999000000 and time < 1677085659999000000 AND (bk_biz_id='2') GROUP BY time(1m0s) LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677081600000000000, 30,
								},
								{
									1677081660000000000, 21,
								},
								{
									1677081720000000000, 1,
								},
								{
									1677081780000000000, 7,
								},
								{
									1677081840000000000, 4,
								},
								{
									1677081900000000000, 2,
								},
								{
									1677081960000000000, 100,
								},
								{
									1677082020000000000, 94,
								},
								{
									1677082080000000000, 34,
								},
							},
						},
					},
				},
			},
		},
		`SELECT "usage" AS _value, *::tag, "time" AS _time FROM cpu_summary WHERE time > 1677081359999000000 and time < 1677085659999000000 AND ((notice_way='weixin' and status='failed') and bk_biz_id='2') LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{},
							Columns: []string{
								influxdb.ResultColumnName,
								"job",
								"notice_way",
								"status",
								influxdb.TimeColumnName,
							},
							Values: [][]any{
								{
									30,
									"SLI",
									"weixin",
									"failed",
									1677081600000000000,
								},
								{
									21,
									"SLI",
									"weixin",
									"failed",
									1677081660000000000,
								},
								{
									1,
									"SLI",
									"weixin",
									"failed",
									1677081720000000000,
								},
								{
									7,
									"SLI",
									"weixin",
									"failed",
									1677081780000000000,
								},
								{
									4,
									"SLI",
									"weixin",
									"failed",
									1677081840000000000,
								},
								{
									2,
									"SLI",
									"weixin",
									"failed",
									1677081900000000000,
								},
								{
									100,
									"SLI",
									"weixin",
									"failed",
									1677081960000000000,
								},
								{
									94,
									"SLI",
									"weixin",
									"failed",
									1677082020000000000,
								},
								{
									34,
									"SLI",
									"weixin",
									"failed",
									1677082080000000000,
								},
							},
						},
					},
				},
			},
		},
		`SELECT count("usage") AS _value, "time" AS _time FROM cpu_summary WHERE time > 1677081599999000000 and time < 1677085659999000000 AND (bk_biz_id='2') GROUP BY "status", time(1m0s) LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{
								"status": "failed",
							},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677081600000000000, 30,
								},
								{
									1677081660000000000, 21,
								},
								{
									1677081720000000000, 1,
								},
								{
									1677081780000000000, 7,
								},
								{
									1677081840000000000, 4,
								},
								{
									1677081900000000000, 2,
								},
								{
									1677081960000000000, 100,
								},
								{
									1677082020000000000, 94,
								},
								{
									1677082080000000000, 34,
								},
							},
						},
					},
				},
			},
		},
		// test query  __name__ with raw 多指标单表
		`SELECT "usage" AS _value, *::tag, "time" AS _time FROM cpu_summary WHERE time > 1677081300000000000 and time < 1677085600000000000 AND (bk_biz_id='2') LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{
								"status": "failed",
							},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677082080000000000, 34,
								},
							},
						},
					},
				},
			},
		},
		`SELECT "free" AS _value, *::tag, "time" AS _time FROM cpu_summary WHERE time > 1677081300000000000 and time < 1677085600000000000 AND (bk_biz_id='2') LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{
								"status": "failed",
							},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677082080000000000, 68,
								},
							},
						},
					},
				},
			},
		},
		`SELECT "value" AS _value, *::tag, "time" AS _time FROM merltrics_rest_request_status_200_count WHERE time > 1677081300000000000 and time < 1677085600000000000 LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{
								"namespace": "lolstage",
								"container": "message-history",
							},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677082080000000000, 68,
								},
							},
						},
					},
				},
			},
		},

		// test query  __name__ with raw 多指标单表 exporter
		`SELECT "metric_value" AS _value, *::tag, "time" AS _time FROM exporter WHERE time > 1677081300000000000 and time < 1677085600000000000 AND (metric_name =~ /.*/) LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{
								"metric_name": "usage",
								"name":        "buzzy",
							},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677082080000000000, 68,
								},
							},
						},
						{
							Name: "",
							Tags: map[string]string{
								"metric_name": "free",
								"name":        "buzzy",
							},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677082080000000000, 70,
								},
							},
						},
					},
				},
			},
		},
		// test query  __name__ with raw 多指标单表 standard_v2_time_series
		`SELECT "usage" AS _value, *::tag, "time" AS _time FROM standard_v2_time_series WHERE time > 1677081300000000000 and time < 1677085600000000000 LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{
								"name": "buzzy",
							},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677082080000000000, 68,
								},
							},
						},
					},
				},
			},
		},
	})

	testCases := map[string]struct {
		query  string
		result string
	}{
		"test query": {
			query:  `{"query_list":[{"data_source":"","table_id":"system.cpu_summary","field_name":"usage","field_list":null,"function":[{"method":"mean","without":false,"dimensions":[],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"avg_over_time","window":"60s","position":0,"vargs_list":null},"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1677081600000,30],[1677081660000,21],[1677081720000,1],[1677081780000,7],[1677081840000,4],[1677081900000,2],[1677081960000,100],[1677082020000,94],[1677082080000,34]]}],"is_partial":false}`,
		},
		"test lost sample in increase": {
			query:  `{"query_list":[{"data_source":"bkmonitor","table_id":"system.cpu_summary","field_name":"usage","field_list":null,"function":null,"time_aggregation":{"function":"increase","window":"5m0s","position":0,"vargs_list":null},"reference_name":"a","dimensions":null,"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"notice_way","value":["weixin"],"op":"eq"},{"field_name":"status","value":["failed"],"op":"eq"}],"condition_list":["and"]},"keep_columns":null}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["job","notice_way","status"],"group_values":["SLI","weixin","failed"],"values":[[1677081660000,52.499649999999995],[1677081720000,38.49981666666667],[1677081780000,46.66666666666667],[1677081840000,40],[1677081900000,16.25],[1677081960000,137.5],[1677082020000,247.5],[1677082080000,285],[1677082140000,263.6679222222223],[1677082200000,160.00106666666667],[1677082260000,51.00056666666667]]}],"is_partial":false}`,
		},
		"test query support fuzzy __name__ with count": {
			query:  `{"query_list":[{"data_source":"","table_id":"system.cpu_summary","field_name":".*","is_regexp":true,"field_list":null,"function":[{"method":"sum","without":false,"dimensions":["status"],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0,"vargs_list":null},"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["status"],"group_values":["failed"],"values":[[1677081600000,30],[1677081660000,21],[1677081720000,1],[1677081780000,7],[1677081840000,4],[1677081900000,2],[1677081960000,100],[1677082020000,94],[1677082080000,34]]}],"is_partial":false}`,
		},
		"test query  __name__ with raw 多指标单表": {
			query:  `{"query_list":[{"data_source":"","table_id":"system.cpu_summary","field_name":".*","is_regexp":true,"field_list":null,"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"10m"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__name__","status"],"group_values":["bkmonitor:system:cpu_summary:free","failed"],"values":[[1677082200000,68]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__name__","status"],"group_values":["bkmonitor:system:cpu_summary:usage","failed"],"values":[[1677082200000,34]]}],"is_partial":false}`,
		},

		"test query  __name__ with raw 多指标单表 exporter": {
			query:  `{"query_list":[{"data_source":"","table_id":"bk.exporter","field_name":".*","is_regexp":true,"field_list":null,"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"10m"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__name__","name"],"group_values":["bkmonitor:bk:exporter:free","buzzy"],"values":[[1677082200000,70]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__name__","name"],"group_values":["bkmonitor:bk:exporter:usage","buzzy"],"values":[[1677082200000,68]]}],"is_partial":false}`,
		},
		"test query  __name__ with raw 多指标单表 standard_v2_time_series": {
			query:  `{"query_list":[{"data_source":"","table_id":"bk.standard_v2_time_series","field_name":".*","is_regexp":true,"field_list":null,"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"10m"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__name__","name"],"group_values":["bkmonitor:bk:standard_v2_time_series:usage","buzzy"],"values":[[1677082200000,68]]}],"is_partial":false}`,
		},
		"test regx with __name__ 单指标单表": {
			query:  `{"query_list":[{"data_source":"","field_name":"merltrics_rest_request_status_.+_count","is_regexp":true,"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__name__","container","namespace"],"group_values":["merltrics_rest_request_status_200_count","message-history","lolstage"],"values":[[1677082080000,68],[1677082140000,68],[1677082200000,68],[1677082260000,68],[1677082320000,68],[1677082380000,68]]}],"is_partial":false}`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})

			body := []byte(c.query)
			query := &structured.QueryTs{}
			err := json.Unmarshal(body, query)
			assert.Nil(t, err)

			res, err := queryTsWithPromEngine(ctx, query)
			assert.Nil(t, err)
			out, err := json.Marshal(res)
			assert.Nil(t, err)
			actual := string(out)
			fmt.Printf("ActualResult: %v\n", actual)
			assert.Equal(t, c.result, actual)
		})
	}
}

func TestQueryRawWithInstance(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	start := "1723594000"
	end := "1723595000"

	mock.BkSQL.Set(map[string]any{
		"SHOW CREATE TABLE `2_bklog_bkunify_query_doris`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":18,"external_api_call_time_mills":{"bkbase_auth_api":43,"bkbase_meta_api":0,"bkbase_apigw_api":33},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":0,"query_db":5,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":45,"match_query_routing_rule":0,"check_permission":43,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":11776,"timetaken":0.096,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
		// query raw by doris
		"SELECT *, `dtEventTimeStamp` AS `_timestamp_` FROM `2_bklog_bkunify_query_doris`.doris WHERE `dtEventTimeStamp` >= 1723594000000 AND `dtEventTimeStamp` <= 1723595000000 AND `thedate` = '20240814' LIMIT 2000005": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{"100968_bklog_proz_ds_analysis":{"start":"2025072100","end":"2025072123"}},"cluster":"proz_doris","totalRecords":1,"external_api_call_time_mills":{"bkbase_meta_api":8},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"thedate":20250721,"dteventtimestamp":1753085020000,"dteventtime":"2025-07-21 16:03:40","localtime":"2025-07-21 16:03:45","__shard_key__":29218083010,"_starttime_":"2025-07-21 16:03:40","_endtime_":"2025-07-21 16:03:40","bk_host_id":1234,"cloudid":0,"path":"/proz/logds/Player/gggggggggggg.log","gseindex":44444,"iterationindex":8,"time":1753085020,"_value_":1753085020000,"_timestamp_":1753085020000}],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":148,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":16,"connect_db":20,"match_query_routing_rule":0,"check_permission":8,"check_query_semantic":0,"pick_valid_storage":0},"select_fields_order":["thedate","dteventtimestamp","dteventtime","localtime","__shard_key__","_starttime_","_endtime_","bk_host_id","__ext","cloudid","serverip","path","gseindex","iterationindex","log","time","_value_","_timestamp_"],"total_record_size":5136,"trino_cluster_host":"","timetaken":0.193,"result_schema":[{"field_type":"int","field_name":"__c0","field_alias":"thedate","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"dteventtimestamp","field_index":1},{"field_type":"string","field_name":"__c2","field_alias":"dteventtime","field_index":2},{"field_type":"string","field_name":"__c3","field_alias":"localtime","field_index":3},{"field_type":"long","field_name":"__c4","field_alias":"__shard_key__","field_index":4},{"field_type":"string","field_name":"__c5","field_alias":"_starttime_","field_index":5},{"field_type":"string","field_name":"__c6","field_alias":"_endtime_","field_index":6},{"field_type":"int","field_name":"__c7","field_alias":"bk_host_id","field_index":7},{"field_type":"string","field_name":"__c8","field_alias":"__ext","field_index":8},{"field_type":"int","field_name":"__c9","field_alias":"cloudid","field_index":9},{"field_type":"string","field_name":"__c10","field_alias":"serverip","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"path","field_index":11},{"field_type":"long","field_name":"__c12","field_alias":"gseindex","field_index":12},{"field_type":"int","field_name":"__c13","field_alias":"iterationindex","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"log","field_index":14},{"field_type":"long","field_name":"__c15","field_alias":"time","field_index":15},{"field_type":"long","field_name":"__c16","field_alias":"_value_","field_index":16},{"field_type":"long","field_name":"__c17","field_alias":"_timestamp_","field_index":17}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["100968_bklog_proz_ds_analysis"]},"errors":null,"trace_id":"d4bcaac4032cfab745cca440c7d8e534","span_id":"bab680b2e2c6340d"}`,
	})

	mock.Es.Set(map[string]any{
		`{"_source":{"includes":["__ext.container_id","dtEventTimeStamp"]},"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":20,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":301,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c726c895a380ba1a9df04ba4a977b29b","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"fa209967d4a8c5d21b3e4f67d2cd579e","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"dc888e9a3789976aa11483626fc61a4f","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c2dae031f095fa4b9deccf81964c7837","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8a916e558c71d4226f1d7f3279cf0fdd","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"f6950fef394e813999d7316cdbf0de4d","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"328d487e284703b1d0bb8017dba46124","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"cb790ecb36bbaf02f6f0eb80ac2fd65c","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"bd8a8ef60e94ade63c55c8773170d458","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c8401bb4ec021b038cb374593b8adce3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}}]}}`,

		// nested query + query string 测试 + highlight
		`{"_source":{"includes":["group","user.first","user.last"]},"from":0,"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"match_phrase":{"user.first":{"query":"John"}}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}},{"match_phrase":{"group":{"query":"fans"}}}]}},"size":5}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"aS3KjpEBbwEm76LbcH1G","_score":0.0,"_source":{"group":"fans","user.first":"John","user.last":"Smith"}}]}}`,

		`{"_source":{"includes":["status","message"]},"from":0,"query":{"bool":{"filter":[{"match_phrase":{"status":{"query":"error"}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}]}},"size":5}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"bT4KjpEBbwEm76LbdH2H","_score":0.0,"_source":{"status":"error","message":"Something went wrong"}}]}}`,

		`{"from":0,"query":{"bool":{"filter":[{"match_phrase":{"resource.k8s.bcs.cluster.id":{"query":"BCS-K8S-00000"}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}]}},"size":0}`: `{"took":15,"timed_out":false,"_shards":{"total":6,"successful":6,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[{"_index":"v2_2_bkapm_trace_bk_monitor_20250604_0","_type":"_doc","_id":"14712105480911733430","_score":null,"_source":{"links":[],"trace_state":"","elapsed_time":38027,"status":{"message":"","code":0},"resource":{"k8s.pod.ip":"192.168.1.100","bk.instance.id":":unify-query::192.168.1.100:","service.name":"unify-query","net.host.ip":"192.168.1.100","k8s.bcs.cluster.id":"BCS-K8S-00000","k8s.pod.name":"bk-monitor-unify-query-5c685b56f-n4b6d","k8s.namespace.name":"blueking"},"span_name":"http-curl","attributes":{"apdex_type":"satisfied","req-http-method":"POST","req-http-path":"https://bkapi.paas3-dev.bktencent.com/api/bk-base/prod/v3/queryengine/query_sync"},"end_time":1749006597019296,"parent_span_id":"6f15efc54fedfebe","events":[],"span_id":"4a5f6170ae000a3f","trace_id":"5c999893cdbc41390c5ff8f3be5f62a9","kind":1,"start_time":1749006596981268,"time":"1749006604000"}}]}}`,

		// array highlight test
		`{"_source":{"includes":["tags","user.first","user.last"]},"from":0,"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"match_phrase":{"user.first":{"query":"John"}}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}},{"match_phrase":{"tags":{"query":"important"}}}]}},"size":5}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"cT5KjpEBbwEm76LbeH3I","_score":0.0,"_source":{"tags":["important","urgent","critical"],"user.first":"John","user.last":"Smith"}}]}}`,

		// array highlight with match all
		`{"_source":{"includes":["tags","user.first","user.last"]},"from":0,"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"match_phrase":{"user.first":{"query":"John"}}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"Smi\""}}]}},"size":5}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"cT5KjpEBbwEm76LbeH3I","_score":0.0,"_source":{"tags":["important","urgent","critical"],"user.first":"John","user.last":"Smith"}}]}}`,

		// array highlight with wildcard all
		`{"_source":{"includes":["tags","user.first","user.last"]},"from":0,"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"match_phrase":{"user.first":{"query":"John"}}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*Smi*"}}]}},"size":5}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"cT5KjpEBbwEm76LbeH3I","_score":0.0,"_source":{"tags":["important","urgent","critical"],"user.first":"John","user.last":"Smith"}}]}}`,

		// include basic test with contains operator
		`{"_source":{"includes":["level","message"]},"from":0,"query":{"bool":{"filter":[{"bool":{"should":[{"wildcard":{"level":{"value":"error"}}},{"wildcard":{"level":{"value":"warn"}}},{"wildcard":{"level":{"value":"info"}}}]}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}]}},"size":10}`: `{"took":5,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"test1","_score":0.0,"_source":{"level":"error","message":"Error occurred"},"highlight":{"level":["<mark>error</mark>"]}},{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"test2","_score":0.0,"_source":{"level":"warn","message":"Warning message"},"highlight":{"level":["<mark>warn</mark>"]}},{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"test3","_score":0.0,"_source":{"level":"info","message":"Info message"},"highlight":{"level":["<mark>info</mark>"]}}]}}`,

		// include with nested field and contains
		`{"_source":{"includes":["user.role","user.department"]},"from":0,"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"bool":{"should":[{"wildcard":{"user.role":{"value":"admin"}}},{"wildcard":{"user.role":{"value":"user"}}},{"wildcard":{"user.role":{"value":"guest"}}}]}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}]}},"size":5}`: `{"took":3,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":2,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"nested1","_score":0.0,"_source":{"user.role":"admin","user.department":"IT"},"highlight":{"user.role":["<mark>admin</mark>"]}},{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"nested2","_score":0.0,"_source":{"user.role":"user","user.department":"Sales"},"highlight":{"user.role":["<mark>user</mark>"]}}]}}`,

		// include with contains and exclude pattern
		`{"_source":{"includes":["application","log_level","message"]},"from":0,"query":{"bool":{"filter":[{"bool":{"must":[{"bool":{"should":[{"wildcard":{"application":{"value":"web-app"}}},{"wildcard":{"application":{"value":"mobile-app"}}},{"wildcard":{"application":{"value":"desktop-app"}}}]}},{"bool":{"must_not":[{"wildcard":{"log_level":{"value":"debug"}}},{"wildcard":{"log_level":{"value":"trace"}}}]}}]}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}]}},"size":3}`: `{"took":4,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"app1","_score":0.0,"_source":{"application":"web-app","log_level":"info","message":"User login successful"},"highlight":{"application":["<mark>web-app</mark>"]}},{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"app2","_score":0.0,"_source":{"application":"mobile-app","log_level":"warn","message":"Low battery warning"},"highlight":{"application":["<mark>mobile-app</mark>"]}},{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"app3","_score":0.0,"_source":{"application":"desktop-app","log_level":"error","message":"File not found"},"highlight":{"application":["<mark>desktop-app</mark>"]}}]}}`,

		`{"_source":{"includes":["__ext.io_kubernetes_pod","dtEventTimeStamp"]},"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":20,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":468,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"e058129ae18bff87c95e83f24584e654","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c124dae69af9b86a7128ee4281820158","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c7f73abf7e865a4b4d7fc608387d01cf","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"39c3ec662881e44bf26d2a6bfc0e35c3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"58e03ce0b9754bf0657d49a5513adcb5","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"43a36f412886bf30b0746562513638d3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"218ceafd04f89b39cda7954e51f4a48a","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8d9abe9b782fe3a1272c93f0af6b39e1","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"0826407be7f04f19086774ed68eac8dd","_score":0.0,"_source":{"dtEventTimeStamp":"1723594224000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"d56b4120194eb37f53410780da777d43","_score":0.0,"_source":{"dtEventTimeStamp":"1723594224000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94"}}}]}}`,
		`{"_source":{"includes":["__ext.container_id","dtEventTimeStamp"]},"from":1,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":1}`:                                                      `{"took":17,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"4f3a5e9c167097c9658e88b2f32364b2","_score":0.0,"_source":{"dtEventTimeStamp":"1723594209000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}}]}}`,
		`{"_source":{"includes":["__ext.container_id","dtEventTimeStamp"]},"from":1,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1723594000123,"include_lower":true,"include_upper":true,"to":1723595000234}}}}},"size":10}`:                                               `{"took":468,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"e058129ae18bff87c95e83f24584e654","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c124dae69af9b86a7128ee4281820158","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c7f73abf7e865a4b4d7fc608387d01cf","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"39c3ec662881e44bf26d2a6bfc0e35c3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"58e03ce0b9754bf0657d49a5513adcb5","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"43a36f412886bf30b0746562513638d3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"218ceafd04f89b39cda7954e51f4a48a","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8d9abe9b782fe3a1272c93f0af6b39e1","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"0826407be7f04f19086774ed68eac8dd","_score":0.0,"_source":{"dtEventTimeStamp":"1723594224000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"d56b4120194eb37f53410780da777d43","_score":0.0,"_source":{"dtEventTimeStamp":"1723594224000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94"}}}]}}`,

		// merge rt test mock data
		`{"_source":{"includes":["a","b"]},"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":5,"sort":[{"a":{"order":"asc"}},{"b":{"order":"asc"}}]}`:  `{"hits":{"total":{"value":123},"hits":[{"_index":"result_table_index","_id":"1","_source":{"a":"1","b":"1"},"sort":["1","1"]},{"_index":"result_table_index","_id":"2","_source":{"a":"1","b":"2"},"sort":["1","2"]},{"_index":"result_table_index","_id":"3","_source":{"a":"1","b":"3"},"sort":["1","3"]},{"_index":"result_table_index","_id":"4","_source":{"a":"1","b":"4"},"sort":["1","4"]},{"_index":"result_table_index","_id":"5","_source":{"a":"1","b":"5"},"sort":["1","5"]}]}}`,
		`{"_source":{"includes":["a","b"]},"from":5,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":5,"sort":[{"a":{"order":"asc"}},{"b":{"order":"asc"}}]}`:  `{"hits":{"total":{"value":123},"hits":[{"_index":"result_table_index","_id":"6","_source":{"a":"2","b":"1"},"sort":["2","1"]},{"_index":"result_table_index","_id":"7","_source":{"a":"2","b":"2"},"sort":["2","2"]},{"_index":"result_table_index","_id":"8","_source":{"a":"2","b":"3"},"sort":["2","3"]},{"_index":"result_table_index","_id":"9","_source":{"a":"2","b":"4"},"sort":["2","4"]},{"_index":"result_table_index","_id":"10","_source":{"a":"2","b":"5"},"sort":["2","5"]}]}}`,
		`{"_source":{"includes":["a","b"]},"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"sort":[{"a":{"order":"asc"}},{"b":{"order":"asc"}}]}`: `{"hits":{"total":{"value":123},"hits":[{"_index":"result_table_index","_id":"1","_source":{"a":"1","b":"1"},"sort":["1","1"]},{"_index":"result_table_index","_id":"2","_source":{"a":"1","b":"2"},"sort":["1","2"]},{"_index":"result_table_index","_id":"3","_source":{"a":"1","b":"3"},"sort":["1","3"]},{"_index":"result_table_index","_id":"4","_source":{"a":"1","b":"4"},"sort":["1","4"]},{"_index":"result_table_index","_id":"5","_source":{"a":"1","b":"5"},"sort":["1","5"]},{"_index":"result_table_index","_id":"6","_source":{"a":"2","b":"1"},"sort":["2","1"]},{"_index":"result_table_index","_id":"7","_source":{"a":"2","b":"2"},"sort":["2","2"]},{"_index":"result_table_index","_id":"8","_source":{"a":"2","b":"3"},"sort":["2","3"]},{"_index":"result_table_index","_id":"9","_source":{"a":"2","b":"4"},"sort":["2","4"]},{"_index":"result_table_index","_id":"10","_source":{"a":"2","b":"5"},"sort":["2","5"]}]}}`,

		// scroll with 5m
		`{"_source":{"includes":["a","b"]},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":5,"sort":[{"a":{"order":"asc"}},{"b":{"order":"asc"}}]}`: `{"_scroll_id":"one","hits":{"total":{"value":123},"hits":[{"_index":"result_table_index","_id":"1","_source":{"a":"1","b":"1"}},{"_index":"result_table_index","_id":"2","_source":{"a":"1","b":"2"}},{"_index":"result_table_index","_id":"3","_source":{"a":"1","b":"3"}},{"_index":"result_table_index","_id":"4","_source":{"a":"1","b":"4"}},{"_index":"result_table_index","_id":"5","_source":{"a":"1","b":"5"}}]}}`,

		// scroll id
		`{"scroll":"5m","scroll_id":"one"}`: `{"_scroll_id":"two","hits":{"total":{"value":123},"hits":[{"_index":"result_table_index","_id":"6","_source":{"a":"2","b":"1"}},{"_index":"result_table_index","_id":"7","_source":{"a":"2","b":"2"}},{"_index":"result_table_index","_id":"8","_source":{"a":"2","b":"3"}},{"_index":"result_table_index","_id":"9","_source":{"a":"2","b":"4"}},{"_index":"result_table_index","_id":"10","_source":{"a":"2","b":"5"}}]}}`,

		// query collections.attributes.db.statement
		`{"from":0,"query":{"bool":{"filter":[{"bool":{"must":[{"exists":{"field":"collections.attributes.db.statement"}},{"match_phrase":{"app_name":{"query":"bkop"}}},{"match_phrase":{"biz_id":{"query":"2"}}}]}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1749456412,"include_lower":true,"include_upper":true,"to":1749460022}}}]}},"size":1,"sort":[{"time":{"order":"desc"}}]}`: `{"took":77,"timed_out":false,"_shards":{"total":9,"successful":9,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[{"_index":"v2_apm_global_precalculate_storage_new_1_20250609_0","_type":"_doc","_id":"bf6477880e7abd183119c150ea1447db","_score":null,"_source":{"biz_id":"2","biz_name":"蓝鲸","app_id":"3","app_name":"bkop","trace_id":"bf6477880e7abd183119c150ea1447db","hierarchy_count":9,"service_count":1,"span_count":1746,"span_max_duration":316009,"span_min_duration":0,"root_service":"unify-query","root_service_span_id":"0db6f409a4b7b759","root_service_span_name":"/query/ts/promql","root_service_status_code":200,"root_service_category":"http","root_service_kind":2,"root_span_id":"0db6f409a4b7b759","root_span_name":"/query/ts/promql","root_span_service":"unify-query","root_span_kind":2,"error":false,"error_count":0,"category_statistics":{"db":0,"messaging":0,"async_backend":0,"other":0,"http":120,"rpc":0},"kind_statistics":{"unspecified":0,"interval":1626,"sync":120,"async":0},"collections":{"attributes.http.scheme":["http"],"attributes.http.method":["POST"],"resource.k8s.pod.name":["bk-datalink-unify-query-6595d74cbf-mzl4x","bk-datalink-unify-query-6595d74cbf-g2q2p","bk-datalink-unify-query-6595d74cbf-zbg52","bk-datalink-unify-query-6595d74cbf-klqkz"],"attributes.net.peer.port":["44024","46284","46272","44026","44020","44048","46290","37374","37442","46360","44088","44114","37490","44152","37504","46278","44034","46288","37424","46358","44072","46366","44100","44138","46408","37526","37542","52822","52854","37616","52878","46286","37410","46356","46364","44098","37498","44136","44158","46432","46448","52850","37612","52872","52898","44056","37386","37446","46362","44094","44120","46404","44154","46414","37536","52846","37608","37620","52866","52892","46442","52834","52862","37606","37618","52882","43598","43938","43354","43816","44934","45806","45906","43642","44884","44974","45858","43788","44894","45020","45888","44948","45808","45950","48902","48944","48994","49058","49068","48922","48990","49012","49062","48918","48970","49008","49060","49070","48942","48992","49014","49064"]}},"sort":[1749460179462463]}]}}`,
		`{"_source":{"includes":["tags","user.first","user.last"]},"aggregations":{"user":{"aggregations":{"user.last":{"aggregations":{"user.first":{"aggregations":{"reverse_nested":{"aggregations":{"dtEventTimeStamp":{"aggregations":{"user":{"aggregations":{"_value":{"max":{"field":"user.first"}}},"nested":{"path":"user"}}},"date_histogram":{"extended_bounds":{"max":1723595000000,"min":1723594000000},"field":"dtEventTimeStamp","interval":"1m","min_doc_count":0}}},"reverse_nested":{}}},"terms":{"field":"user.first","include":["John"],"missing":" ","size":5}}},"terms":{"field":"user.last","missing":" ","size":5}}},"nested":{"path":"user"}}},"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"match_phrase":{"user.first":{"query":"John"}}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*Smi*"}}]}},"size":0}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"user":{"doc_count":1,"user.last":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":" ","doc_count":1,"user.first":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"John","doc_count":1,"reverse_nested":{"doc_count":1,"dtEventTimeStamp":{"buckets":[{"key_as_string":"2024-08-14T01:00:00.000Z","key":1723594000000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:01:00.000Z","key":1723594060000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:02:00.000Z","key":1723594120000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:03:00.000Z","key":1723594180000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:04:00.000Z","key":1723594240000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:05:00.000Z","key":1723594300000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:06:00.000Z","key":1723594360000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:07:00.000Z","key":1723594420000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:08:00.000Z","key":1723594480000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:09:00.000Z","key":1723594540000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:10:00.000Z","key":1723594600000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:11:00.000Z","key":1723594660000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:12:00.000Z","key":1723594720000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:13:00.000Z","key":1723594780000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:14:00.000Z","key":1723594840000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:15:00.000Z","key":1723594900000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}},{"key_as_string":"2024-08-14T01:16:00.000Z","key":1723594960000,"doc_count":0,"user":{"doc_count":0,"_value":{"value":null}}}]}}}]}}]}}}}`,

		// highlight with int field
		`{"_source":{"includes":["application","log_level","message"]},"from":0,"query":{"bool":{"filter":[{"wildcard":{"gseIndex":{"value":"12345"}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"2\""}}]}},"size":3}`: `{"hits":{"total":{"value":3},"hits":[{"_index":"result_table_index","_id":"6","_source":{"a":"2","b":"1","gseIndex":12345}},{"_index":"result_table_index","_id":"7","_source":{"a":"2","b":"2","gseIndex":12345}},{"_index":"result_table_index","_id":"9","_source":{"a":"2","b":"4","gseIndex":12345}}]}}`,

		// highlight with gseIndex 8019256.12
		`{"from":0,"query":{"bool":{"filter":[{"wildcard":{"gseIndex":{"value":"8019256.12"}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}]}},"size":1}`: `{"took":1043,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":16,"relation":"eq"},"max_score":null,"hits":[{"_index":"v2_2_bklog_bkunify_query_20250710_0","_type":"_doc","_id":"3440427488472403621","_score":null,"_source":{"report_time":"2025-07-10T02:46:19.443Z","trace_id":"af754e7bbf629abaee3499638974dda9","level":"info","iterationIndex":15,"cloudId":0,"gseIndex":8019256.12,"time":"1752115579000","file":"victoriaMetrics/instance.go:397","dtEventTimeStamp":"1752115579000"}}]}}`,

		// highlight with gseIndex 8019256
		`{"from":0,"query":{"bool":{"filter":[{"wildcard":{"gseIndex":{"value":"8019256"}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}]}},"size":1}`: `{"took":1043,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":16,"relation":"eq"},"max_score":null,"hits":[{"_index":"v2_2_bklog_bkunify_query_20250710_0","_type":"_doc","_id":"3440427488472403621","_score":null,"_source":{"report_time":"2025-07-10T02:46:19.443Z","trace_id":"af754e7bbf629abaee3499638974dda9","level":"info","iterationIndex":15,"cloudId":0,"gseIndex":8019256,"time":"1752115579000","file":"victoriaMetrics/instance.go:397","dtEventTimeStamp":"1752115579000"}}]}}`,

		// query raw multi query from + size over size
		`{"from":0,"query":{"bool":{"filter":{"range":{"end_time":{"from":1723595000000000,"include_lower":true,"include_upper":true,"to":1723595000000000}}}}},"size":100,"sort":[{"time":{"order":"desc"}}]}`:                     `{"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10,"relation":"eq"},"hits":[{"_type":"_doc","_id":"00001","_source":{"time":"00001"}},{"_type":"_doc","_id":"10001","_source":{"time":"10001"}},{"_type":"_doc","_id":"20001","_source":{"time":"20001"}}]}}`,
		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723595000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":100,"sort":[{"time":{"order":"desc"}}]}`: `{"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10,"relation":"eq"},"hits":[{"_type":"_doc","_id":"00002","_source":{"time":"00002"}},{"_type":"_doc","_id":"10002","_source":{"time":"10002"}},{"_type":"_doc","_id":"20002","_source":{"time":"20002"}}]}}`,

		// query raw multi query from + size
		`{"from":0,"query":{"bool":{"filter":{"range":{"end_time":{"from":1723595000000000,"include_lower":true,"include_upper":true,"to":1723595000000000}}}}},"size":4,"sort":[{"time":{"order":"desc"}}]}`:                     `{"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10,"relation":"eq"},"hits":[{"_type":"_doc","_id":"00001","_source":{"time":"00001"}},{"_type":"_doc","_id":"10001","_source":{"time":"10001"}},{"_type":"_doc","_id":"20001","_source":{"time":"20001"}}]}}`,
		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723595000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":4,"sort":[{"time":{"order":"desc"}}]}`: `{"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10,"relation":"eq"},"hits":[{"_type":"_doc","_id":"00002","_source":{"time":"00002"}},{"_type":"_doc","_id":"10002","_source":{"time":"10002"}},{"_type":"_doc","_id":"20002","_source":{"time":"20002"}}]}}`,

		// query raw multi query from + size 数量刚好结束
		`{"from":0,"query":{"bool":{"filter":{"range":{"end_time":{"from":1723595000000000,"include_lower":true,"include_upper":true,"to":1723595000000000}}}}},"size":12,"sort":[{"time":{"order":"desc"}}]}`:                     `{"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10,"relation":"eq"},"hits":[{"_type":"_doc","_id":"00001","_source":{"time":"00001"}},{"_type":"_doc","_id":"10001","_source":{"time":"10001"}},{"_type":"_doc","_id":"20001","_source":{"time":"20001"}}]}}`,
		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723595000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":12,"sort":[{"time":{"order":"desc"}}]}`: `{"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10,"relation":"eq"},"hits":[{"_type":"_doc","_id":"00002","_source":{"time":"00002"}},{"_type":"_doc","_id":"10002","_source":{"time":"10002"}},{"_type":"_doc","_id":"20002","_source":{"time":"20002"}}]}}`,
	})

	mock.BkSQL.Set(map[string]any{
		// query object field is null
		"SELECT * FROM `2_bklog_bkunify_query_doris`.doris WHERE (`dtEventTimeStamp` >= 1723595000000 AND `dtEventTimeStamp` <= 1723595000000 AND `thedate` = '20240814') ORDER BY `dtEventTimeStamp` DESC, `gseIndex` DESC, `iterationIndex` DESC LIMIT 100 OFFSET 0": `{"result":true,"message":"成功","code":"00","data":{"cluster":"codev_doris2","totalRecords":100,"external_api_call_time_mills":{"bkbase_meta_api":10},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"dtEventTime":1756175592000,"__shard_key__":5853918203,"__unique_key__":"12395782465323060901","dtEventTimeStamp":1756175592000,"thedate":20250826,"localTime":"2025-08-26 10:33:12","iterationIndex":7,"__ext":null,"cloudId":0,"gseIndex":11483283,"path":"/data/home/user00/pangusvr/bin/log/httpgatesvr.log","time":1756175592,"log":"[20250826 10:33:11:328884][INFO    ][httpgatesvr][(httpgatesvr/cos_file_ci_callback.lua:167) (Lua)] [on_update_cos_ci_info] success uid: 2199033031264, path_name: highlight/1/2199033031264/649651764779846687_c133e9abb8b600235143a96366a6ac03.jpg","content":" [on_update_cos_ci_info] success uid: 2199033031264, path_name: highlight/1/2199033031264/649651764779846687_c133e9abb8b600235143a96366a6ac03.jpg","func":"Lua","level":"INFO","log_file":"httpgatesvr/cos_file_ci_callback.lua:167","log_time":"20250826 10:33:11:328884","svr":"httpgatesvr"}],"bk_biz_ids":[],"stage_elapsed_time_mills":{"check_query_syntax":1,"query_db":3049,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":11,"connect_db":66,"match_query_routing_rule":0,"check_permission":11,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["dtEventTime","__shard_key__","__unique_key__","dtEventTimeStamp","thedate","localTime","iterationIndex","__ext","bk_host_id","cloudId","gseIndex","path","serverIp","time","log","content","func","level","log_file","log_time","svr"],"total_record_size":327384,"trino_cluster_host":"","timetaken":3.139,"result_schema":[{"field_type":"long","field_name":"__c0","field_alias":"dtEventTime","field_index":0},{"field_type":"long","field_name":"__c1","field_alias":"__shard_key__","field_index":1},{"field_type":"string","field_name":"__c2","field_alias":"__unique_key__","field_index":2},{"field_type":"long","field_name":"__c3","field_alias":"dtEventTimeStamp","field_index":3},{"field_type":"int","field_name":"__c4","field_alias":"thedate","field_index":4},{"field_type":"string","field_name":"__c5","field_alias":"localTime","field_index":5},{"field_type":"long","field_name":"__c6","field_alias":"iterationIndex","field_index":6},{"field_type":"string","field_name":"__c7","field_alias":"__ext","field_index":7},{"field_type":"long","field_name":"__c8","field_alias":"bk_host_id","field_index":8},{"field_type":"long","field_name":"__c9","field_alias":"cloudId","field_index":9},{"field_type":"long","field_name":"__c10","field_alias":"gseIndex","field_index":10},{"field_type":"string","field_name":"__c11","field_alias":"path","field_index":11},{"field_type":"string","field_name":"__c12","field_alias":"serverIp","field_index":12},{"field_type":"long","field_name":"__c13","field_alias":"time","field_index":13},{"field_type":"string","field_name":"__c14","field_alias":"log","field_index":14},{"field_type":"string","field_name":"__c15","field_alias":"content","field_index":15},{"field_type":"string","field_name":"__c16","field_alias":"func","field_index":16},{"field_type":"string","field_name":"__c17","field_alias":"level","field_index":17},{"field_type":"string","field_name":"__c18","field_alias":"log_file","field_index":18},{"field_type":"string","field_name":"__c19","field_alias":"log_time","field_index":19},{"field_type":"string","field_name":"__c20","field_alias":"svr","field_index":20}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["100915_bklog_pub_svrlog_pangusvr_other_other_analysis"]},"errors":null,"trace_id":"0816890bc718ec5786d469e9a79110d2","span_id":"6d04c0ddf758c603"}`,
	})

	tcs := map[string]struct {
		queryTs  *structured.QueryTs
		total    int64
		expected string
		options  string
	}{
		"query collections.attributes.db.statement": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkApm,
						TableID:    structured.TableID(influxdb.ResultTableEs),
						FieldName:  "collections.attributes.db.statement",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "count",
								Dimensions: []string{"collections.attributes.db.statement"},
								Window:     "120s",
							},
						},
						QueryString:   "*",
						ReferenceName: "a",
						Dimensions:    []string{"collections.attributes.db.statement"},
						Limit:         1,
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "collections.attributes.db.statement",
									Value:         []string{""},
									Operator:      "ne",
								},
								{
									DimensionName: "app_name",
									Value:         []string{"bkop"},
									Operator:      "eq",
								},
								{
									DimensionName: "biz_id",
									Value:         []string{"2"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{"and", "and"},
						},
					},
				},
				MetricMerge:   "a",
				OrderBy:       []string{"-time"},
				Timezone:      "Asia/Shanghai",
				LookBackDelta: "1m",
				Start:         "1749456412",
				End:           "1749460022",
				HighLight: &metadata.HighLight{
					Enable: true,
				},
			},
			total:    1e4,
			expected: `[{"__data_label":"es","__doc_id":"bf6477880e7abd183119c150ea1447db","__highlight":{"app_name":["<mark>bkop</mark>"],"biz_id":["<mark>2</mark>"]},"__index":"v2_apm_global_precalculate_storage_new_1_20250609_0","__result_table":"result_table.es","app_id":"3","app_name":"bkop","biz_id":"2","biz_name":"蓝鲸","category_statistics.async_backend":0,"category_statistics.db":0,"category_statistics.http":120,"category_statistics.messaging":0,"category_statistics.other":0,"category_statistics.rpc":0,"collections.attributes.http.method":["POST"],"collections.attributes.http.scheme":["http"],"collections.attributes.net.peer.port":["44024","46284","46272","44026","44020","44048","46290","37374","37442","46360","44088","44114","37490","44152","37504","46278","44034","46288","37424","46358","44072","46366","44100","44138","46408","37526","37542","52822","52854","37616","52878","46286","37410","46356","46364","44098","37498","44136","44158","46432","46448","52850","37612","52872","52898","44056","37386","37446","46362","44094","44120","46404","44154","46414","37536","52846","37608","37620","52866","52892","46442","52834","52862","37606","37618","52882","43598","43938","43354","43816","44934","45806","45906","43642","44884","44974","45858","43788","44894","45020","45888","44948","45808","45950","48902","48944","48994","49058","49068","48922","48990","49012","49062","48918","48970","49008","49060","49070","48942","48992","49014","49064"],"collections.resource.k8s.pod.name":["bk-datalink-unify-query-6595d74cbf-mzl4x","bk-datalink-unify-query-6595d74cbf-g2q2p","bk-datalink-unify-query-6595d74cbf-zbg52","bk-datalink-unify-query-6595d74cbf-klqkz"],"error":false,"error_count":0,"hierarchy_count":9,"kind_statistics.async":0,"kind_statistics.interval":1626,"kind_statistics.sync":120,"kind_statistics.unspecified":0,"root_service":"unify-query","root_service_category":"http","root_service_kind":2,"root_service_span_id":"0db6f409a4b7b759","root_service_span_name":"/query/ts/promql","root_service_status_code":200,"root_span_id":"0db6f409a4b7b759","root_span_kind":2,"root_span_name":"/query/ts/promql","root_span_service":"unify-query","service_count":1,"span_count":1746,"span_max_duration":316009,"span_min_duration":0,"trace_id":"bf6477880e7abd183119c150ea1447db"}]`,
			options:  `{"result_table.es|3":{"from":0,"search_after":[1749460179462463]}}`,
		},
		"query with EpochMillis": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableBkBaseEs),
						KeepColumns: []string{"__ext.container_id", "dtEventTimeStamp"},
					},
				},
				From:  1,
				Limit: 10,
				Start: "1723594000123",
				End:   "1723595000234",
			},
			total:    1e4,
			expected: `[{"__data_label":"bkbase_es","__doc_id":"e058129ae18bff87c95e83f24584e654","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"c124dae69af9b86a7128ee4281820158","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"c7f73abf7e865a4b4d7fc608387d01cf","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"39c3ec662881e44bf26d2a6bfc0e35c3","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"58e03ce0b9754bf0657d49a5513adcb5","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"43a36f412886bf30b0746562513638d3","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"218ceafd04f89b39cda7954e51f4a48a","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"8d9abe9b782fe3a1272c93f0af6b39e1","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"0826407be7f04f19086774ed68eac8dd","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594224000","dtEventTimeStamp":"1723594224000"},{"__data_label":"bkbase_es","__doc_id":"d56b4120194eb37f53410780da777d43","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594224000","dtEventTimeStamp":"1723594224000"}]`,
			options:  `{"result_table.bk_base_es|0":{"from":1}}`,
		},
		"query es with multi rt and multi from 0 - 5": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableBkBaseEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"a",
					"b",
				},
				Limit:       5,
				MetricMerge: "a",
				Start:       start,
				End:         end,
				IsMultiFrom: true,
				ResultTableOptions: map[string]*metadata.ResultTableOption{
					"result_table.es|3": {
						From: function.IntPoint(0),
					},
					"result_table.bk_base_es|0": {
						From: function.IntPoint(5),
					},
				},
			},
			total:    246,
			expected: `[{"__data_label":"es","__doc_id":"1","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"1"},{"__data_label":"es","__doc_id":"2","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"2"},{"__data_label":"es","__doc_id":"3","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"3"},{"__data_label":"es","__doc_id":"4","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"4"},{"__data_label":"es","__doc_id":"5","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"5"}]`,
			options:  `{"result_table.bk_base_es|0":{"from":5,"search_after":["2","5"]},"result_table.es|3":{"from":5,"search_after":["1","5"]}}`,
		},
		"query es with multi rt and multi from 5 - 10": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableBkBaseEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"a",
					"b",
					metadata.KeyTableID,
				},
				Limit:       5,
				MetricMerge: "a",
				Start:       start,
				End:         end,
				IsMultiFrom: true,
				ResultTableOptions: map[string]*metadata.ResultTableOption{
					"result_table.es|3": {
						From: function.IntPoint(5),
					},
					"result_table.bk_base_es|0": {
						From: function.IntPoint(5),
					},
				},
			},
			total:    246,
			expected: `[{"__data_label":"bkbase_es","__doc_id":"6","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"2","b":"1"},{"__data_label":"es","__doc_id":"6","__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"1"},{"__data_label":"bkbase_es","__doc_id":"7","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"2","b":"2"},{"__data_label":"es","__doc_id":"7","__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"2"},{"__data_label":"bkbase_es","__doc_id":"8","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"2","b":"3"}]`,
			options:  `{"result_table.bk_base_es|0":{"from":8,"search_after":["2","5"]},"result_table.es|3":{"from":7,"search_after":["2","5"]}}`,
		},
		"query es with multi rt and one from 0 - 5": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableBkBaseEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"a",
					"b",
					metadata.KeyTableID,
				},
				From:        0,
				Limit:       5,
				MetricMerge: "a",
				Start:       start,
				End:         end,
			},
			total:    246,
			expected: `[{"__data_label":"bkbase_es","__doc_id":"1","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"1"},{"__data_label":"es","__doc_id":"1","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"1"},{"__data_label":"bkbase_es","__doc_id":"2","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"2"},{"__data_label":"es","__doc_id":"2","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"2"},{"__data_label":"bkbase_es","__doc_id":"3","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"3"}]`,
			options:  `{"result_table.bk_base_es|0":{"from":0,"search_after":["1","5"]},"result_table.es|3":{"from":0,"search_after":["1","5"]}}`,
		},
		"query es with multi rt and one from 5 - 10": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableBkBaseEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"a",
					"b",
					metadata.KeyTableID,
				},
				From:        5,
				Limit:       5,
				MetricMerge: "a",
				Start:       start,
				End:         end,
			},
			total:    246,
			expected: `[{"__data_label":"es","__doc_id":"3","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"3"},{"__data_label":"bkbase_es","__doc_id":"4","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"4"},{"__data_label":"es","__doc_id":"4","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"4"},{"__data_label":"bkbase_es","__doc_id":"5","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"5"},{"__data_label":"es","__doc_id":"5","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"5"}]`,
			options:  `{"result_table.bk_base_es|0":{"from":0,"search_after":["2","5"]},"result_table.es|3":{"from":0,"search_after":["2","5"]}}`,
		},
		"query_bk_base_es_1 to 1": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						From:        1,
						Limit:       1,
						KeepColumns: []string{"__ext.container_id", "dtEventTimeStamp"},
					},
				},
				Start: start,
				End:   end,
			},
			total:    1e4,
			expected: `[{"__data_label":"es","__doc_id":"4f3a5e9c167097c9658e88b2f32364b2","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.es","_time":"1723594209000","dtEventTimeStamp":"1723594209000"}]`,
			options:  `{"result_table.es|3":{"from":1}}`,
		},
		"query with scroll - 1": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableBkBaseEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"a",
					"b",
					metadata.KeyTableID,
				},
				From:        0,
				Limit:       5,
				MetricMerge: "a",
				Start:       start,
				End:         end,
				Scroll:      "5m",
			},
			expected: `[{"__data_label":"bkbase_es","__doc_id":"1","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"1"},{"__data_label":"es","__doc_id":"1","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"1"},{"__data_label":"bkbase_es","__doc_id":"2","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"2"},{"__data_label":"es","__doc_id":"2","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"2"},{"__data_label":"bkbase_es","__doc_id":"3","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"3"},{"__data_label":"es","__doc_id":"3","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"3"},{"__data_label":"bkbase_es","__doc_id":"4","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"4"},{"__data_label":"es","__doc_id":"4","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"4"},{"__data_label":"bkbase_es","__doc_id":"5","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"1","b":"5"},{"__data_label":"es","__doc_id":"5","__index":"result_table_index","__result_table":"result_table.es","a":"1","b":"5"}]`,
			total:    246,
			options:  `{"result_table.bk_base_es|0":{"from":0,"scroll_id":"one"},"result_table.es|3":{"from":0,"scroll_id":"one"}}`,
		},
		"query with scroll - 2": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(influxdb.ResultTableBkBaseEs),
						KeepColumns:   []string{"a", "b"},
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"a",
					"b",
					metadata.KeyTableID,
				},
				From:        0,
				Limit:       5,
				MetricMerge: "a",
				Start:       start,
				End:         end,
				ResultTableOptions: map[string]*metadata.ResultTableOption{
					"result_table.es|3": {
						ScrollID: "one",
					},
					"result_table.bk_base_es|0": {
						ScrollID: "one",
					},
				},
				Scroll: "5m",
			},
			expected: `[{"__data_label":"bkbase_es","__doc_id":"6","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"2","b":"1"},{"__data_label":"es","__doc_id":"6","__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"1"},{"__data_label":"bkbase_es","__doc_id":"7","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"2","b":"2"},{"__data_label":"es","__doc_id":"7","__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"2"},{"__data_label":"bkbase_es","__doc_id":"8","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"2","b":"3"},{"__data_label":"es","__doc_id":"8","__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"3"},{"__data_label":"bkbase_es","__doc_id":"9","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"2","b":"4"},{"__data_label":"es","__doc_id":"9","__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"4"},{"__data_label":"bkbase_es","__doc_id":"10","__index":"result_table_index","__result_table":"result_table.bk_base_es","a":"2","b":"5"},{"__data_label":"es","__doc_id":"10","__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"5"}]`,
			total:    246,
			options:  `{"result_table.bk_base_es|0":{"from":0,"scroll_id":"two"},"result_table.es|3":{"from":0,"scroll_id":"two"}}`,
		},
		"nested query + query string 测试 + highlight": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						KeepColumns: []string{"group", "user.first", "user.last"},
						QueryString: "group: fans",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "user.first",
									Value:         []string{"John"},
									Operator:      structured.ConditionEqual,
								},
							},
						},
					},
				},
				Limit: 5,
				Start: start,
				End:   end,
				HighLight: &metadata.HighLight{
					Enable: true,
				},
			},
			total:    1,
			expected: `[{"__data_label":"es","__doc_id":"aS3KjpEBbwEm76LbcH1G","__highlight":{"group":["<mark>fans</mark>"],"user.first":["<mark>John</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","group":"fans","user.first":"John","user.last":"Smith"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"high light from condition": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						KeepColumns: []string{"status", "message"},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "status",
									Value:         []string{"error"},
									Operator:      structured.ConditionEqual,
								},
							},
						},
					},
				},
				Limit: 5,
				Start: start,
				End:   end,
				HighLight: &metadata.HighLight{
					Enable: true,
				},
			},
			total:    1,
			expected: `[{"__data_label":"es","__doc_id":"bT4KjpEBbwEm76LbdH2H","__highlight":{"status":["<mark>error</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","message":"Something went wrong","status":"error"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"debug highlight": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    structured.TableID(influxdb.ResultTableEs),
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "resource.k8s.bcs.cluster.id",
									Value:         []string{"BCS-K8S-00000"},
									Operator:      structured.ConditionEqual,
								},
							},
						},
					},
				},
				Limit: 0,
				Start: start,
				End:   end,
				HighLight: &metadata.HighLight{
					Enable: true,
				},
			},
			total:    1e4,
			expected: `[{"__data_label":"es","__doc_id":"14712105480911733430","__highlight":{"resource.k8s.bcs.cluster.id":["<mark>BCS-K8S-00000</mark>"]},"__index":"v2_2_bkapm_trace_bk_monitor_20250604_0","__result_table":"result_table.es","attributes.apdex_type":"satisfied","attributes.req-http-method":"POST","attributes.req-http-path":"https://bkapi.paas3-dev.bktencent.com/api/bk-base/prod/v3/queryengine/query_sync","elapsed_time":38027,"end_time":1.749006597019296e+15,"events":[],"kind":1,"links":[],"parent_span_id":"6f15efc54fedfebe","resource.bk.instance.id":":unify-query::192.168.1.100:","resource.k8s.bcs.cluster.id":"BCS-K8S-00000","resource.k8s.namespace.name":"blueking","resource.k8s.pod.ip":"192.168.1.100","resource.k8s.pod.name":"bk-monitor-unify-query-5c685b56f-n4b6d","resource.net.host.ip":"192.168.1.100","resource.service.name":"unify-query","span_id":"4a5f6170ae000a3f","span_name":"http-curl","start_time":1.749006596981268e+15,"status.code":0,"status.message":"","time":"1749006604000","trace_id":"5c999893cdbc41390c5ff8f3be5f62a9","trace_state":""}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"array highlight test": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						KeepColumns: []string{"tags", "user.first", "user.last"},
						QueryString: "tags: important",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "user.first",
									Value:         []string{"John"},
									Operator:      structured.ConditionEqual,
								},
							},
						},
					},
				},
				Limit: 5,
				Start: start,
				End:   end,
				HighLight: &metadata.HighLight{
					Enable: true,
				},
			},
			total:    1,
			expected: `[{"__data_label":"es","__doc_id":"cT5KjpEBbwEm76LbeH3I","__highlight":{"user.first":["<mark>John</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","tags":["important","urgent","critical"],"user.first":"John","user.last":"Smith"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"array highlight with match all": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						KeepColumns: []string{"tags", "user.first", "user.last"},
						QueryString: "Smi",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "user.first",
									Value:         []string{"John"},
									Operator:      structured.ConditionEqual,
								},
							},
						},
					},
				},
				Limit: 5,
				Start: start,
				End:   end,
				HighLight: &metadata.HighLight{
					Enable: true,
				},
			},
			total:    1,
			expected: `[{"__data_label":"es","__doc_id":"cT5KjpEBbwEm76LbeH3I","__highlight":{"user.first":["<mark>John</mark>"],"user.last":["<mark>Smi</mark>th"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","tags":["important","urgent","critical"],"user.first":"John","user.last":"Smith"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"array highlight with wildcard all": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						KeepColumns: []string{"tags", "user.first", "user.last"},
						QueryString: "*Smi*",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "user.first",
									Value:         []string{"John"},
									Operator:      structured.ConditionEqual,
								},
							},
						},
					},
				},
				Limit: 5,
				Start: start,
				End:   end,
				HighLight: &metadata.HighLight{
					Enable: true,
				},
			},
			total:    1,
			expected: `[{"__data_label":"es","__doc_id":"cT5KjpEBbwEm76LbeH3I","__highlight":{"user.first":["<mark>John</mark>"],"user.last":["<mark>Smi</mark>th"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","tags":["important","urgent","critical"],"user.first":"John","user.last":"Smith"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"include basic test with contains operator": {
			queryTs: &structured.QueryTs{
				HighLight: &metadata.HighLight{
					Enable: true,
				},
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						FieldName:   "level",
						KeepColumns: []string{"level", "message"},
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "count",
								Dimensions: []string{"level"},
							},
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "level",
									Value:         []string{"error", "warn", "info"},
									Operator:      structured.Contains,
								},
							},
						},
					},
				},
				Limit: 10,
				Start: start,
				End:   end,
			},
			total:    3,
			expected: `[{"__data_label":"es","__doc_id":"test1","__highlight":{"level":["<mark>error</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","level":"error","message":"Error occurred"},{"__data_label":"es","__doc_id":"test2","__highlight":{"level":["<mark>warn</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","level":"warn","message":"Warning message"},{"__data_label":"es","__doc_id":"test3","__highlight":{"level":["<mark>info</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","level":"info","message":"Info message"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},

		"include with nested field and contains": {
			queryTs: &structured.QueryTs{
				HighLight: &metadata.HighLight{
					Enable: true,
				},
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						KeepColumns: []string{"user.role", "user.department"},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "user.role",
									Value:         []string{"admin", "user", "guest"},
									Operator:      structured.Contains,
								},
							},
						},
					},
				},
				Limit: 5,
				Start: start,
				End:   end,
			},
			total:    2,
			expected: `[{"__data_label":"es","__doc_id":"nested1","__highlight":{"user.role":["<mark>admin</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","user.department":"IT","user.role":"admin"},{"__data_label":"es","__doc_id":"nested2","__highlight":{"user.role":["<mark>user</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","user.department":"Sales","user.role":"user"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},

		"include with contains and exclude pattern": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						KeepColumns: []string{"application", "log_level", "message"},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "application",
									Value:         []string{"web-app", "mobile-app", "desktop-app"},
									Operator:      structured.Contains,
								},
								{
									DimensionName: "log_level",
									Value:         []string{"debug", "trace"},
									Operator:      structured.Ncontains,
								},
							},
							ConditionList: []string{"and"},
						},
					},
				},
				HighLight: &metadata.HighLight{
					Enable: true,
				},
				Limit: 3,
				Start: start,
				End:   end,
			},
			total:    3,
			expected: `[{"__data_label":"es","__doc_id":"app1","__highlight":{"application":["<mark>web-app</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","application":"web-app","log_level":"info","message":"User login successful"},{"__data_label":"es","__doc_id":"app2","__highlight":{"application":["<mark>mobile-app</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","application":"mobile-app","log_level":"warn","message":"Low battery warning"},{"__data_label":"es","__doc_id":"app3","__highlight":{"application":["<mark>desktop-app</mark>"]},"__index":"bk_unify_query_demo_2","__result_table":"result_table.es","application":"desktop-app","log_level":"error","message":"File not found"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},

		"highlight with int field": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(influxdb.ResultTableEs),
						KeepColumns: []string{"application", "log_level", "message"},
						QueryString: "2",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "gseIndex",
									Value:         []string{"12345"},
									Operator:      structured.Contains,
								},
							},
							ConditionList: []string{},
						},
					},
				},
				HighLight: &metadata.HighLight{
					Enable: true,
				},
				Limit: 3,
				Start: start,
				End:   end,
			},
			total:    3,
			expected: `[{"__data_label":"es","__doc_id":"6","__highlight":{"a":["<mark>2</mark>"],"gseIndex":["<mark>12345</mark>"]},"__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"1","gseIndex":12345},{"__data_label":"es","__doc_id":"7","__highlight":{"a":["<mark>2</mark>"],"b":["<mark>2</mark>"],"gseIndex":["<mark>12345</mark>"]},"__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"2","gseIndex":12345},{"__data_label":"es","__doc_id":"9","__highlight":{"a":["<mark>2</mark>"],"gseIndex":["<mark>12345</mark>"]},"__index":"result_table_index","__result_table":"result_table.es","a":"2","b":"4","gseIndex":12345}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"highlight with gseIndex 8019256.12": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    structured.TableID(influxdb.ResultTableEs),
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "gseIndex",
									Value:         []string{"8019256.12"},
									Operator:      structured.Contains,
								},
							},
							ConditionList: []string{},
						},
					},
				},
				HighLight: &metadata.HighLight{
					Enable: true,
				},
				Limit: 1,
				Start: start,
				End:   end,
			},
			total:    16,
			expected: `[{"__data_label":"es","__doc_id":"3440427488472403621","__highlight":{"gseIndex":["<mark>8019256.12</mark>"]},"__index":"v2_2_bklog_bkunify_query_20250710_0","__result_table":"result_table.es","_time":"1752115579000","cloudId":0,"dtEventTimeStamp":"1752115579000","file":"victoriaMetrics/instance.go:397","gseIndex":8.01925612e+06,"iterationIndex":15,"level":"info","report_time":"2025-07-10T02:46:19.443Z","time":"1752115579000","trace_id":"af754e7bbf629abaee3499638974dda9"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"highlight with gseIndex 8019256": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    structured.TableID(influxdb.ResultTableEs),
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "gseIndex",
									Value:         []string{"8019256"},
									Operator:      structured.Contains,
								},
							},
							ConditionList: []string{},
						},
					},
				},
				HighLight: &metadata.HighLight{
					Enable: true,
				},
				Limit: 1,
				Start: start,
				End:   end,
			},
			total:    16,
			expected: `[{"__data_label":"es","__doc_id":"3440427488472403621","__highlight":{"gseIndex":["<mark>8019256</mark>"]},"__index":"v2_2_bklog_bkunify_query_20250710_0","__result_table":"result_table.es","_time":"1752115579000","cloudId":0,"dtEventTimeStamp":"1752115579000","file":"victoriaMetrics/instance.go:397","gseIndex":8.019256e+06,"iterationIndex":15,"level":"info","report_time":"2025-07-10T02:46:19.443Z","time":"1752115579000","trace_id":"af754e7bbf629abaee3499638974dda9"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"query string ": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    structured.TableID(influxdb.ResultTableEs),
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "gseIndex",
									Value:         []string{"8019256"},
									Operator:      structured.Contains,
								},
							},
							ConditionList: []string{},
						},
					},
				},
				HighLight: &metadata.HighLight{
					Enable: true,
				},
				Limit: 1,
				Start: start,
				End:   end,
			},
			total:    16,
			expected: `[{"__data_label":"es","__doc_id":"3440427488472403621","__highlight":{"gseIndex":["<mark>8019256</mark>"]},"__index":"v2_2_bklog_bkunify_query_20250710_0","__result_table":"result_table.es","_time":"1752115579000","cloudId":0,"dtEventTimeStamp":"1752115579000","file":"victoriaMetrics/instance.go:397","gseIndex":8.019256e+06,"iterationIndex":15,"level":"info","report_time":"2025-07-10T02:46:19.443Z","time":"1752115579000","trace_id":"af754e7bbf629abaee3499638974dda9"}]`,
			options:  `{"result_table.es|3":{"from":0}}`,
		},
		"query raw by doris": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    influxdb.ResultTableDoris,
					},
				},
				Start:  start,
				End:    end,
				DryRun: false,
			},
			total:    5136,
			options:  `{"result_table.doris|4":{"result_schema":[{"field_alias":"thedate","field_index":0,"field_name":"__c0","field_type":"int"},{"field_alias":"dteventtimestamp","field_index":1,"field_name":"__c1","field_type":"long"},{"field_alias":"dteventtime","field_index":2,"field_name":"__c2","field_type":"string"},{"field_alias":"localtime","field_index":3,"field_name":"__c3","field_type":"string"},{"field_alias":"__shard_key__","field_index":4,"field_name":"__c4","field_type":"long"},{"field_alias":"_starttime_","field_index":5,"field_name":"__c5","field_type":"string"},{"field_alias":"_endtime_","field_index":6,"field_name":"__c6","field_type":"string"},{"field_alias":"bk_host_id","field_index":7,"field_name":"__c7","field_type":"int"},{"field_alias":"__ext","field_index":8,"field_name":"__c8","field_type":"string"},{"field_alias":"cloudid","field_index":9,"field_name":"__c9","field_type":"int"},{"field_alias":"serverip","field_index":10,"field_name":"__c10","field_type":"string"},{"field_alias":"path","field_index":11,"field_name":"__c11","field_type":"string"},{"field_alias":"gseindex","field_index":12,"field_name":"__c12","field_type":"long"},{"field_alias":"iterationindex","field_index":13,"field_name":"__c13","field_type":"int"},{"field_alias":"log","field_index":14,"field_name":"__c14","field_type":"string"},{"field_alias":"time","field_index":15,"field_name":"__c15","field_type":"long"},{"field_alias":"_value_","field_index":16,"field_name":"__c16","field_type":"long"},{"field_alias":"_timestamp_","field_index":17,"field_name":"__c17","field_type":"long"}]}}`,
			expected: `[{"__data_label":"bksql","__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":2.921808301e+10,"_endtime_":"2025-07-21 16:03:40","_starttime_":"2025-07-21 16:03:40","_timestamp_":1.75308502e+12,"_value_":1.75308502e+12,"bk_host_id":1234,"cloudid":0,"dteventtime":"2025-07-21 16:03:40","dteventtimestamp":1.75308502e+12,"gseindex":44444,"iterationindex":8,"localtime":"2025-07-21 16:03:45","path":"/proz/logds/Player/gggggggggggg.log","thedate":2.0250721e+07,"time":1.75308502e+09}]`,
		},
		"query raw multi query from + size over size": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    "multi_es",
					},
				},
				From:  50,
				Limit: 50,
				Step:  start,
				End:   end,
				OrderBy: structured.OrderBy{
					"-time",
					"__result_table",
				},
			},
			total:    20,
			expected: `[]`,
			options:  `{"result_table.es_with_time_filed|3":{"from":0},"result_table.es|3":{"from":0}}`,
		},
		"query raw multi query from + size": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    "multi_es",
					},
				},
				From:  2,
				Limit: 2,
				Step:  start,
				End:   end,
				OrderBy: structured.OrderBy{
					"-time",
					"__result_table",
				},
			},
			total:    20,
			expected: `[{"__data_label":"es","__doc_id":"10002","__index":"","__result_table":"result_table.es","time":"10002"},{"__data_label":"es","__doc_id":"10001","__index":"","__result_table":"result_table.es_with_time_filed","time":"10001"}]`,
			options:  `{"result_table.es_with_time_filed|3":{"from":0},"result_table.es|3":{"from":0}}`,
		},
		"query raw multi query from + size 数量刚好结束": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    "multi_es",
					},
				},
				From:  6,
				Limit: 6,
				Step:  start,
				End:   end,
				OrderBy: structured.OrderBy{
					"-time",
					"__result_table",
				},
			},
			total:    20,
			expected: `[]`,
			options:  `{"result_table.es_with_time_filed|3":{"from":0},"result_table.es|3":{"from":0}}`,
		},
		"query raw multi query from + size 数量剩余 1 个": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    "multi_es",
					},
				},
				From:  5,
				Limit: 7,
				Step:  start,
				End:   end,
				OrderBy: structured.OrderBy{
					"-time",
					"__result_table",
				},
			},
			total:    20,
			expected: `[{"__data_label":"es","__doc_id":"00001","__index":"","__result_table":"result_table.es_with_time_filed","time":"00001"}]`,
			options:  `{"result_table.es_with_time_filed|3":{"from":0},"result_table.es|3":{"from":0}}`,
		},
		"query object field is null": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkLog,
						TableID:    influxdb.ResultTableDoris,
						SQL:        "SELECT *  ORDER BY dtEventTimeStamp DESC, gseIndex DESC, iterationIndex DESC LIMIT 100 OFFSET 0",
					},
				},
				Step: start,
				End:  end,
			},
			total:    327384,
			expected: `[{"__data_label":"bksql","__ext":<nil>,"__index":"2_bklog_bkunify_query_doris","__result_table":"result_table.doris","__shard_key__":5.853918203e+09,"__unique_key__":"12395782465323060901","cloudId":0,"content":" [on_update_cos_ci_info] success uid: 2199033031264, path_name: highlight/1/2199033031264/649651764779846687_c133e9abb8b600235143a96366a6ac03.jpg","dtEventTime":1.756175592e+12,"dtEventTimeStamp":1.756175592e+12,"func":"Lua","gseIndex":1.1483283e+07,"iterationIndex":7,"level":"INFO","localTime":"2025-08-26 10:33:12","log":"[20250826 10:33:11:328884][INFO    ][httpgatesvr][(httpgatesvr/cos_file_ci_callback.lua:167) (Lua)] [on_update_cos_ci_info] success uid: 2199033031264, path_name: highlight/1/2199033031264/649651764779846687_c133e9abb8b600235143a96366a6ac03.jpg","log_file":"httpgatesvr/cos_file_ci_callback.lua:167","log_time":"20250826 10:33:11:328884","path":"/data/home/user00/pangusvr/bin/log/httpgatesvr.log","svr":"httpgatesvr","thedate":2.0250826e+07,"time":1.756175592e+09}]`,
			options:  `{"result_table.doris|4":{"result_schema":[{"field_alias":"dtEventTime","field_index":0,"field_name":"__c0","field_type":"long"},{"field_alias":"__shard_key__","field_index":1,"field_name":"__c1","field_type":"long"},{"field_alias":"__unique_key__","field_index":2,"field_name":"__c2","field_type":"string"},{"field_alias":"dtEventTimeStamp","field_index":3,"field_name":"__c3","field_type":"long"},{"field_alias":"thedate","field_index":4,"field_name":"__c4","field_type":"int"},{"field_alias":"localTime","field_index":5,"field_name":"__c5","field_type":"string"},{"field_alias":"iterationIndex","field_index":6,"field_name":"__c6","field_type":"long"},{"field_alias":"__ext","field_index":7,"field_name":"__c7","field_type":"string"},{"field_alias":"bk_host_id","field_index":8,"field_name":"__c8","field_type":"long"},{"field_alias":"cloudId","field_index":9,"field_name":"__c9","field_type":"long"},{"field_alias":"gseIndex","field_index":10,"field_name":"__c10","field_type":"long"},{"field_alias":"path","field_index":11,"field_name":"__c11","field_type":"string"},{"field_alias":"serverIp","field_index":12,"field_name":"__c12","field_type":"string"},{"field_alias":"time","field_index":13,"field_name":"__c13","field_type":"long"},{"field_alias":"log","field_index":14,"field_name":"__c14","field_type":"string"},{"field_alias":"content","field_index":15,"field_name":"__c15","field_type":"string"},{"field_alias":"func","field_index":16,"field_name":"__c16","field_type":"string"},{"field_alias":"level","field_index":17,"field_name":"__c17","field_type":"string"},{"field_alias":"log_file","field_index":18,"field_name":"__c18","field_type":"string"},{"field_alias":"log_time","field_index":19,"field_name":"__c19","field_type":"string"},{"field_alias":"svr","field_index":20,"field_name":"__c20","field_type":"string"}]}}`,
		},
	}

	for name, c := range tcs {
		t.Run(name, func(t *testing.T) {
			total, list, options, err := queryRawWithInstance(ctx, c.queryTs)
			assert.Nil(t, err)
			if err != nil {
				return
			}

			assert.Equal(t, c.total, total)

			actual := json.MarshalListMap(list)

			assert.Equal(t, c.expected, actual)

			if len(options) > 0 || c.options != "" {
				optActual, _ := json.Marshal(options)
				assert.JSONEq(t, c.options, string(optActual))
			}
		})
	}
}

// TestQueryExemplar comment lint rebel
func TestQueryExemplar(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)

	body := []byte(`{"query_list":[{"data_source":"","table_id":"system.cpu_summary","field_name":"usage","field_list":["bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"function":null,"time_aggregation":{"function":"","window":"","position":0,"vargs_list":null},"reference_name":"","dimensions":null,"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"bk_obj_id","value":["module"],"op":"contains"},{"field_name":"ip","value":["127.0.0.2"],"op":"contains"},{"field_name":"bk_inst_id","value":["14261"],"op":"contains"},{"field_name":"bk_biz_id","value":["7"],"op":"contains"}],"condition_list":["and","and","and"]},"keep_columns":null}],"metric_merge":"","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"","down_sample_range":"1m"}`)

	query := &structured.QueryTs{}
	err := json.Unmarshal(body, query)
	assert.Nil(t, err)

	metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})

	mock.InfluxDB.Set(map[string]any{
		`select usage as _value, time as _time, bk_trace_id, bk_span_id, bk_trace_value, bk_trace_timestamp from cpu_summary where time > 1677081600000000000 and time < 1677085600000000000 and (bk_obj_id='module' and (ip='127.0.0.2' and (bk_inst_id='14261' and bk_biz_id='7'))) and bk_biz_id='2' and (bk_span_id != '' or bk_trace_id != '')  limit 100000005 slimit 100005`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{},
							Columns: []string{
								influxdb.ResultColumnName,
								influxdb.TimeColumnName,
								"bk_trace_id",
								"bk_span_id",
								"bk_trace_value",
								"bk_trace_timestamp",
							},
							Values: [][]any{
								{
									30,
									1677081600000000000,
									"b9cc0e45d58a70b61e8db6fffb5e3376",
									"3d2a373cbeefa1f8",
									1,
									1680157900669,
								},
								{
									21,
									1677081660000000000,
									"fe45f0eccdce3e643a77504f6e6bd87a",
									"c72dcc8fac9bcead",
									1,
									1682121442937,
								},
								{
									1,
									1677081720000000000,
									"771073eb573336a6d3365022a512d6d8",
									"fca46f1c065452e8",
									1,
									1682150008969,
								},
							},
						},
					},
				},
			},
		},
	})

	res, err := queryExemplar(ctx, query)
	assert.Nil(t, err)
	out, err := json.Marshal(res)
	assert.Nil(t, err)
	actual := string(out)
	assert.Equal(t, `{"series":[{"name":"_result0","metric_name":"usage","columns":["_value","_time","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"types":["float","float","string","string","float","float"],"group_keys":[],"group_values":[],"values":[[30,1677081600000000000,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],[21,1677081660000000000,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],[1,1677081720000000000,"771073eb573336a6d3365022a512d6d8","fca46f1c065452e8",1,1682150008969]]}],"is_partial":false}`, actual)
}

func TestVmQueryParams(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	promql.MockEngine()

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
			query:    `{"query_list":[{"field_name":"bk_split_measurement","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"increase","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["and", "and"]}},{"field_name":"bk_split_measurement","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"delta","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (increase(a[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (delta(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["victoria_metrics"],"metric_filter_condition":{"a":"filter=\"bk_split_measurement\", bcs_cluster_id=~\"cls-2\", bcs_cluster_id=~\"cls-2\", bk_biz_id=\"100801\", result_table_id=\"victoria_metrics\", __name__=\"bk_split_measurement_value\"","b":"filter=\"bk_split_measurement\", result_table_id=\"victoria_metrics\", __name__=\"bk_split_measurement_value\""}}`,
		},
		{
			username: "vm-query-or",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"container_cpu_usage_seconds_total","field_list":null,"function":[{"method":"sum","without":false,"dimensions":[],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0,"vargs_list":null},"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"bk_biz_id","value":["7"],"op":"contains"},{"field_name":"ip","value":["127.0.0.1","127.0.0.2"],"op":"contains"},{"field_name":"ip","value":["[a-z]","[A-Z]"],"op":"req"},{"field_name":"api","value":["/metrics"],"op":"ncontains"},{"field_name":"bk_biz_id","value":["7"],"op":"contains"},{"field_name":"api","value":["/metrics"],"op":"contains"}],"condition_list":["and","and","and","or","and"]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1697458200","end_time":"1697461800","step":"60s","down_sample_range":"3s","timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum(count_over_time(a[1m] offset -59s999ms))","start":1697458200,"end":1697461800,"step":60},"result_table_list":["100147_bcs_prom_computation_result_table_25428","100147_bcs_prom_computation_result_table_25429"],"metric_filter_condition":{"a":"bcs_cluster_id=\"BCS-K8S-25428\", bk_biz_id=\"7\", ip=~\"^(127\\\\.0\\\\.0\\\\.1|127\\\\.0\\\\.0\\\\.2)$\", ip=~\"[a-z]|[A-Z]\", api!=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25428\", bk_biz_id=\"7\", api=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", bk_biz_id=\"7\", ip=~\"^(127\\\\.0\\\\.0\\\\.1|127\\\\.0\\\\.0\\\\.2)$\", ip=~\"[a-z]|[A-Z]\", api!=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", bk_biz_id=\"7\", api=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25429\", bk_biz_id=\"7\", ip=~\"^(127\\\\.0\\\\.0\\\\.1|127\\\\.0\\\\.0\\\\.2)$\", ip=~\"[a-z]|[A-Z]\", api!=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25429\", bk_biz_id=\"7\", api=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=\"container_cpu_usage_seconds_total_value\""}}`,
		},
		{
			username: "vm-query-or-for-internal",
			spaceUid: "vm-query",
			promql:   `{"promql":"sum by(job, metric_name) (delta(label_replace({__name__=~\"container_cpu_.+_total\", __name__ !~ \".+_size_count\", __name__ !~ \".+_process_time_count\", job=\"metric-social-friends-forever\"}, \"metric_name\", \"$1\", \"__name__\", \"ffs_rest_(.*)_count\")[2m:]))","start":"1698147600","end":"1698151200","step":"60s","bk_biz_ids":null,"timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (job, metric_name) (delta(label_replace({__name__=~\"a\"} offset -59s999ms, \"metric_name\", \"$1\", \"__name__\", \"ffs_rest_(.*)_count_value\")[2m:]))","start":1698147600,"end":1698151200,"step":60},"result_table_list":["100147_bcs_prom_computation_result_table_25428","100147_bcs_prom_computation_result_table_25429"],"metric_filter_condition":{"a":"bcs_cluster_id=\"BCS-K8S-25428\", __name__!~\".+_size_count_value\", __name__!~\".+_process_time_count_value\", job=\"metric-social-friends-forever\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=~\"container_cpu_.+_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", __name__!~\".+_size_count_value\", __name__!~\".+_process_time_count_value\", job=\"metric-social-friends-forever\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=~\"container_cpu_.+_total_value\" or bcs_cluster_id=\"BCS-K8S-25429\", __name__!~\".+_size_count_value\", __name__!~\".+_process_time_count_value\", job=\"metric-social-friends-forever\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=~\"container_cpu_.+_total_value\""}}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"container_cpu_usage_seconds_total","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["or", "and"]}},{"field_name":"container_cpu_usage_seconds_total","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(a[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["100147_bcs_prom_computation_result_table_25428","100147_bcs_prom_computation_result_table_25429"],"metric_filter_condition":{"b":"bcs_cluster_id=\"BCS-K8S-25429\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25428\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\""}}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["and","and"]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(a[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["vm_rt"],"metric_filter_condition":{"a":"bcs_cluster_id=\"cls\", bcs_cluster_id=~\"cls-2\", bcs_cluster_id=~\"cls-2\", bk_biz_id=\"100801\", result_table_id=\"vm_rt\", __name__=\"metric_value\"","b":"bcs_cluster_id=\"cls\", result_table_id=\"vm_rt\", __name__=\"metric_value\""}}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"namespace","value":["ns"],"op":"contains"}],"condition_list":[]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(a[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["vm_rt"],"metric_filter_condition":{"a":"bcs_cluster_id=\"cls\", namespace=\"ns\", result_table_id=\"vm_rt\", __name__=\"metric_value\"","b":"bcs_cluster_id=\"cls\", result_table_id=\"vm_rt\", __name__=\"metric_value\""}}`,
		},
		{
			username: "vm-query-fuzzy-name",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"me.*","is_regexp":true,"function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"namespace","value":["ns"],"op":"contains"}],"condition_list":[]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time({__name__=~\"a\"}[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["vm_rt"],"metric_filter_condition":{"a":"bcs_cluster_id=\"cls\", namespace=\"ns\", result_table_id=\"vm_rt\", __name__=~\"me.*_value\"","b":"bcs_cluster_id=\"cls\", result_table_id=\"vm_rt\", __name__=\"metric_value\""}}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			promql:   `{"promql":"max_over_time((increase(container_cpu_usage_seconds_total{}[10m]) \u003e 0)[1h:])","start":"1720765200","end":"1720786800","step":"10m","bk_biz_ids":null,"timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"max_over_time((increase(a[10m] offset -9m59s999ms) \u003e 0)[1h:])","start":1720765200,"end":1720786800,"step":600},"result_table_list":["100147_bcs_prom_computation_result_table_25428","100147_bcs_prom_computation_result_table_25429"],"metric_filter_condition":{"a":"bcs_cluster_id=\"BCS-K8S-25428\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25429\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=\"container_cpu_usage_seconds_total_value\""}}`,
		},
	}

	for i, c := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			var (
				query *structured.QueryTs
				err   error
			)
			ctx := metadata.InitHashID(ctx)
			metadata.SetUser(ctx, &metadata.User{Key: fmt.Sprintf("username:%s", c.username), SpaceUID: c.spaceUid})

			if c.promql != "" {
				var queryPromQL *structured.QueryPromQL
				err = json.Unmarshal([]byte(c.promql), &queryPromQL)
				assert.Nil(t, err)
				query, err = promQLToStruct(ctx, queryPromQL)
			} else {
				err = json.Unmarshal([]byte(c.query), &query)
			}

			query.SpaceUid = c.spaceUid
			assert.Nil(t, err)
			_, err = queryTsWithPromEngine(ctx, query)
			if c.error != nil {
				assert.Contains(t, err.Error(), c.error.Error())
			} else {
				var vmParams map[string]string
				if vmParams != nil {
					assert.Equal(t, c.params, vmParams["sql"])
				}
			}
		})
	}
}

func TestStructAndPromQLConvert(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	promql.MockEngine()

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
		"promql to struct with 1m": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `count_over_time(bkmonitor:metric[1m] @ start() offset -29s999ms)`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `30s`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						AlignInfluxdbResult: true,
						DataSource:          `bkmonitor`,
						FieldName:           `metric`,
						StartOrEnd:          parser.START,
						//Offset:              "59s999ms",
						OffsetForward: true,
						TimeAggregation: structured.TimeAggregation{
							Function:  "count_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: `a`,
						Step:          `30s`,
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
				PromQL: `sum by (job, metric_name) (delta(label_replace({__name__=~"bkmonitor:container_cpu_.+_total",job="metric-social-friends-forever"} @ start() offset -29s999ms, "metric_name", "$1", "__name__", "ffs_rest_(.*)_count")[2m:]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `30s`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:          `bkmonitor`,
						FieldName:           `container_cpu_.+_total`,
						IsRegexp:            true,
						StartOrEnd:          parser.START,
						AlignInfluxdbResult: true,
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
		"promql to struct with topk": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `topk(1, bkmonitor:metric)`,
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
		"promql to struct with delta(metric[1m])`": {
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
		"promql to struct with condition contains`": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `bkmonitor:metric{dim_contains=~"^(val-1|val-2|val-3)$",dim_req=~"val-1|val-2|val-3"} @ end()`,
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
									DimensionName: "dim_contains",
									Value: []string{
										"val-1",
										"val-2",
										"val-3",
									},
									Operator: "contains",
								},
								{
									DimensionName: "dim_req",
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
		"quantile and quantile_over_time": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `quantile(0.9, quantile_over_time(0.9, bkmonitor:metric[1m]))`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:  "quantile_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
							VargsList: []interface{}{
								0.9,
							},
							Position: 1,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "quantile",
								VArgsList: []interface{}{
									0.9,
								},
							},
						},
					},
				},
				MetricMerge: "a",
			},
		},
		"nodeIndex 3 with sum": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `increase(sum by (deployment_environment, result_table_id) (bkmonitor:5000575_bkapm_metric_tgf_server_gs_cn_idctest:__default__:trace_additional_duration_count{deployment_environment="g-5"})[2m:])`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						TableID:    "5000575_bkapm_metric_tgf_server_gs_cn_idctest.__default__",
						FieldName:  "trace_additional_duration_count",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "deployment_environment",
									Value:         []string{"g-5"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{},
						},
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:   "increase",
							Window:     "2m0s",
							NodeIndex:  3,
							IsSubQuery: true,
							Step:       "0s",
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"deployment_environment", "result_table_id",
								},
							},
						},
						Offset: "0s",
					},
				},
				MetricMerge: "a",
			},
		},
		"nodeIndex 2 with sum": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `sum by (deployment_environment, result_table_id) (increase(bkmonitor:5000575_bkapm_metric_tgf_server_gs_cn_idctest:__default__:trace_additional_duration_count{deployment_environment="g-5"}[2m]))`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						TableID:    "5000575_bkapm_metric_tgf_server_gs_cn_idctest.__default__",
						FieldName:  "trace_additional_duration_count",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "deployment_environment",
									Value:         []string{"g-5"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{},
						},
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:  "increase",
							Window:    "2m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"deployment_environment", "result_table_id",
								},
							},
						},
					},
				},
				MetricMerge: "a",
			},
		},
		"predict_linear": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `predict_linear(bkmonitor:metric[1h], 4*3600)`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    "bkmonitor",
						TableID:       "",
						FieldName:     "metric",
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:  "predict_linear",
							Window:    "1h0m0s",
							NodeIndex: 2,
							VargsList: []interface{}{4 * 3600},
						},
					},
				},
				MetricMerge: "a",
			},
		},
		"promql to struct with many time aggregate": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `min_over_time(increase(bkmonitor:metric[1m])[2m:])`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    "bkmonitor",
						TableID:       "",
						FieldName:     "metric",
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:  "increase",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "min_over_time",
								Window:     "2m0s",
								IsSubQuery: true,
								Step:       "0s",
							},
						},
						Offset: "0s",
					},
				},
				MetricMerge: "a",
			},
		},
		"promql to struct with many time aggregate and funciton": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `topk(5, floor(sum by (dim) (last_over_time(min_over_time(increase(label_replace(bkmonitor:metric, "name", "$0", "__name__", ".+")[1m:])[2m:])[3m:15s]))))`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    "bkmonitor",
						TableID:       "",
						FieldName:     "metric",
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:   "increase",
							Window:     "1m0s",
							NodeIndex:  3,
							IsSubQuery: true,
							Step:       "0s",
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "label_replace",
								VArgsList: []interface{}{
									"name",
									"$0",
									"__name__",
									".+",
								},
							},
							{
								Method:     "min_over_time",
								Window:     "2m0s",
								IsSubQuery: true,
								Step:       "0s",
							},
							{
								Method:     "last_over_time",
								Window:     "3m0s",
								IsSubQuery: true,
								Step:       "15s",
							},
							{
								Method:     "sum",
								Dimensions: []string{"dim"},
							},
							{
								Method: "floor",
							},
							{
								Method: "topk",
								VArgsList: []interface{}{
									5,
								},
							},
						},
						Offset: "0s",
					},
				},
				MetricMerge: "a",
			},
		},
		"promql with match - 1": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `sum by (pod_name, bcs_cluster_id, namespace,instance) (rate(container_cpu_usage_seconds_total{namespace="ns-1"}[2m])) / on(bcs_cluster_id, namespace, pod_name) group_left() sum (sum_over_time(kube_pod_container_resource_limits_cpu_cores{namespace="ns-1"}[1m])) by (pod_name, bcs_cluster_id,namespace)`,
				Match:  `{pod_name="pod", bcs_cluster_id!="cls-1", namespace="ns-1", instance="ins-1"}`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "container_cpu_usage_seconds_total",
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "2m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name", "bcs_cluster_id", "namespace", "instance"},
							},
						},
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "instance",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ins-1"},
								},
							},
							ConditionList: []string{"and", "and", "and", "and"},
						},
					},
					{
						DataSource: "bkmonitor",
						FieldName:  "kube_pod_container_resource_limits_cpu_cores",
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name", "bcs_cluster_id", "namespace"},
							},
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "instance",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ins-1"},
								},
							},
							ConditionList: []string{"and", "and", "and", "and"},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: `a / on(bcs_cluster_id, namespace, pod_name) group_left() b`,
			},
		},
		"promql with match and verify - 1": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL:             `sum by (pod_name, bcs_cluster_id, namespace,instance) (rate(container_cpu_usage_seconds_total{namespace="ns-1"}[2m])) / on(bcs_cluster_id, namespace, pod_name) group_left() sum (sum_over_time(kube_pod_container_resource_limits_cpu_cores{namespace="ns-1"}[1m])) by (pod_name, bcs_cluster_id,namespace)`,
				Match:              `{pod_name="pod", bcs_cluster_id!="cls-1", namespace="ns-1", instance="ins-1"}`,
				IsVerifyDimensions: true,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "container_cpu_usage_seconds_total",
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "2m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name", "bcs_cluster_id", "namespace", "instance"},
							},
						},
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "instance",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ins-1"},
								},
							},
							ConditionList: []string{"and", "and", "and", "and"},
						},
					},
					{
						DataSource: "bkmonitor",
						FieldName:  "kube_pod_container_resource_limits_cpu_cores",
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name", "bcs_cluster_id", "namespace"},
							},
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
							},
							ConditionList: []string{"and", "and", "and"},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: `a / on(bcs_cluster_id, namespace, pod_name) group_left() b`,
			},
		},
		"promql with match and verify - 2": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL:             `sum by (pod_name) (rate(container_cpu_usage_seconds_total{namespace="ns-1"}[2m])) / on(bcs_cluster_id, namespace, pod_name) group_left() kube_pod_container_resource_limits_cpu_cores{namespace="ns-1"} or sum by (bcs_cluster_id, namespace, pod_name, instance) (rate(container_cpu_usage_seconds_total{namespace="ns-1"}[1m]))`,
				Match:              `{pod_name="pod", bcs_cluster_id!="cls-1", namespace="ns-1", instance="ins-1"}`,
				IsVerifyDimensions: true,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "container_cpu_usage_seconds_total",
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "2m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name"},
							},
						},
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
							},
							ConditionList: []string{"and"},
						},
					},
					{
						DataSource: "bkmonitor",
						FieldName:  "kube_pod_container_resource_limits_cpu_cores",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
							},
						},
						ReferenceName: "b",
					},
					{
						DataSource: "bkmonitor",
						FieldName:  "container_cpu_usage_seconds_total",
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"bcs_cluster_id", "namespace", "pod_name", "instance"},
							},
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "instance",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ins-1"},
								},
							},
							ConditionList: []string{"and", "and", "and", "and"},
						},
						ReferenceName: "c",
					},
				},
				MetricMerge: `a / on(bcs_cluster_id, namespace, pod_name) group_left() b or c`,
			},
		},
		"promql with 特殊字符": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `sum by (http__bk_46____bk_45____bk_37__1) (rate({__name__=~"bkapm:apm__bk_45__10001:test:http__bk_46__status",test__bk_46____bk_45____bk_94__1="test.-^1"}[1m]))`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkapm",
						TableID:    "apm-10001.test",
						IsRegexp:   true,
						FieldName:  "http.status",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"http.-%1"},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "test.-^1",
									Operator:      "eq",
									Value:         []string{"test.-^1"},
								},
							},
						},
					},
				},
				MetricMerge: "a",
			},
		},
		"promql 多层时间聚合聚合函数": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `quantile_over_time(0.95, sum(sum_over_time(bkmonitor:metric{tag!="abc"}[1m]))[1h:1m])`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						TableID:    "",
						FieldName:  "metric",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "sum",
							},
							{
								Method:     "quantile_over_time",
								VArgsList:  []interface{}{0.95},
								Position:   1,
								Window:     "1h0m0s",
								IsSubQuery: true,
								Step:       "1m0s",
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Offset:        "0s",
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "tag",
									Operator:      "ne",
									Value:         []string{"abc"},
								},
							},
						},
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

func TestQueryTs_ToQueryReference(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})
	jsonData := `{"query_list":[{"data_source":"","table_id":"","field_name":"container_cpu_usage_seconds_total","is_regexp":false,"field_list":null,"function":[{"method":"sum","without":false,"dimensions":["namespace"],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"rate","window":"5m","node_index":0,"position":0,"vargs_list":[],"is_sub_query":false,"step":""},"reference_name":"a","dimensions":["namespace"],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-blueking-gse-data-common"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-blueking-gse"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["flux-cd-deploy"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["kube-system"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bkmonitor-operator-bkop"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bkmonitor-operator"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-blueking-gse-data-jk"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["kyverno"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-bscp-prod"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-bkce-bcs-k8s-40980"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-costops-grey"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-bscp-test"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-system"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bkop-system"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bk-system"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25186"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25451"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25326"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25182"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25037"],"op":"contains"}],"condition_list":["and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and"]},"keep_columns":["_time","a","namespace"],"step":""}],"metric_merge":"a","result_columns":null,"start_time":"1702266900","end_time":"1702871700","step":"150s","down_sample_range":"5m","timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`
	var query *structured.QueryTs
	err := json.Unmarshal([]byte(jsonData), &query)
	assert.Nil(t, err)

	queryReference, err := query.ToQueryReference(ctx)
	assert.Nil(t, err)

	vmExpand := queryReference.ToVmExpand(ctx)
	expectData := `job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-blueking-gse-data-common", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-blueking-gse", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="flux-cd-deploy", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="kube-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bkmonitor-operator-bkop", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bkmonitor-operator", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-blueking-gse-data-jk", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="kyverno", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-bscp-prod", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-bkce-bcs-k8s-40980", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-costops-grey", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-bscp-test", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bkop-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bk-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25186", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25451", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25326", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25182", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25037", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value"`
	assert.Equal(t, expectData, vmExpand.MetricFilterCondition["a"])
	assert.Nil(t, err)
	assert.True(t, metadata.GetQueryParams(ctx).IsDirectQuery())
}

func TestQueryTsClusterMetrics(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)

	var (
		key string
		err error
	)

	key = fmt.Sprintf("%s:%s", ClusterMetricQueryPrefix, redis.ClusterMetricMetaKey)
	_, err = redisUtil.HSet(ctx, key, "influxdb_shard_write_points_ok", `{"metric_name":"influxdb_shard_write_points_ok","tags":["bkm_cluster","database","engine","hostname","id","index_type","path","retention_policy","wal_path"]}`)
	if err != nil {
		return
	}

	key = fmt.Sprintf("%s:%s", ClusterMetricQueryPrefix, redis.ClusterMetricKey)
	_, err = redisUtil.HSet(ctx, key, "influxdb_shard_write_points_ok|bkm_cluster=default", `[{"bkm_cluster":"default","database":"_internal","engine":"tsm1","hostname":"influxdb-0","id":"43","index_type":"inmem","path":"/var/lib/influxdb/data/_internal/monitor/43","retention_policy":"monitor","wal_path":"/var/lib/influxdb/wal/_internal/monitor/43","time":1700903220,"value":1498687},{"bkm_cluster":"default","database":"_internal","engine":"tsm1","hostname":"influxdb-0","id":"44","index_type":"inmem","path":"/var/lib/influxdb/data/_internal/monitor/44","retention_policy":"monitor","wal_path":"/var/lib/influxdb/wal/_internal/monitor/44","time":1700903340,"value":1499039.5}]`)
	if err != nil {
		return
	}

	testCases := map[string]struct {
		query  string
		result string
	}{
		"rangeCase": {
			query: `
                {
                    "space_uid": "influxdb",
                    "query_list": [
                        {
                            "data_source": "",
                            "table_id": "",
                            "field_name": "influxdb_shard_write_points_ok",
                            "field_list": null,
                            "function": [
                                {
                                    "method": "sum",
                                    "without": false,
                                    "dimensions": ["bkm_cluster"],
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
                                "field_list": [{"field_name": "bkm_cluster", "value": ["default"], "op": "eq"}],
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
                    "start_time": "1700901370",
                    "end_time": "1700905370",
                    "step": "60s",
					"instant": false
                }
			`,
			result: `{"is_partial":false,"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bkm_cluster"],"group_values":["default"],"values":[[1700903220000,1498687],[1700903340000,1499039.5]]}]}`,
		},
		"instanceCase": {
			query: `
                {
                    "space_uid": "influxdb",
                    "query_list": [
                        {
                            "data_source": "",
                            "table_id": "",
                            "field_name": "influxdb_shard_write_points_ok",
                            "field_list": null,
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
                                "field_list": [
									{"field_name": "bkm_cluster", "value": ["default"], "op": "eq"},
									{"field_name": "id", "value": ["43"], "op": "eq"},
									{"field_name": "database", "value": ["_internal"], "op": "eq"},
									{"field_name": "bkm_cluster", "value": ["default"], "op": "eq"},
									{"field_name": "id", "value": ["44"], "op": "eq"}
								],
                                "condition_list": ["and", "or", "and", "and"]
                            },
                            "keep_columns": [
                                "_time",
                                "a"
                            ]
                        }
                    ],
                    "metric_merge": "a",
                    "result_columns": null,
                    "end_time": "1700905370",
					"instant": true
                }
			`,
			result: `{"is_partial":false,"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bkm_cluster","database","engine","hostname","id","index_type","path","retention_policy","wal_path"],"group_values":["default","_internal","tsm1","influxdb-0","43","inmem","/var/lib/influxdb/data/_internal/monitor/43","monitor","/var/lib/influxdb/wal/_internal/monitor/43"],"values":[[1700903220000,1498687]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bkm_cluster","database","engine","hostname","id","index_type","path","retention_policy","wal_path"],"group_values":["default","_internal","tsm1","influxdb-0","44","inmem","/var/lib/influxdb/data/_internal/monitor/44","monitor","/var/lib/influxdb/wal/_internal/monitor/44"],"values":[[1700903340000,1499039.5]]}]}`,
		},
	}
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			body := []byte(c.query)
			query := &structured.QueryTs{}
			err := json.Unmarshal(body, query)
			assert.Nil(t, err)

			res, err := QueryTsClusterMetrics(ctx, query)
			t.Logf("QueryTsClusterMetrics error: %+v", err)
			assert.Nil(t, err)
			out, err := json.Marshal(res)
			actual := string(out)
			assert.Nil(t, err)
			fmt.Printf("ActualResult: %v\n", actual)
			assert.JSONEq(t, c.result, actual)
		})
	}
}

func TestQueryTsToInstanceAndStmt(t *testing.T) {

	ctx := metadata.InitHashID(context.Background())

	spaceUid := influxdb.SpaceUid

	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)

	testCases := map[string]struct {
		query        *structured.QueryTs
		promql       string
		stmt         string
		instanceType string
	}{
		"test_matcher_with_vm": {
			promql:       `datasource:result_table:vm:container_cpu_usage_seconds_total{}`,
			stmt:         `a`,
			instanceType: consul.VictoriaMetricsStorageType,
		},
		"test_matcher_with_influxdb": {
			promql:       `datasource:result_table:influxdb:cpu_summary{}`,
			stmt:         `a`,
			instanceType: consul.PrometheusStorageType,
		},
		"test_group_with_vm": {
			promql:       `sum(count_over_time(datasource:result_table:vm:container_cpu_usage_seconds_total{}[1m]))`,
			stmt:         `sum(count_over_time(a[1m] offset -59s999ms))`,
			instanceType: consul.VictoriaMetricsStorageType,
		},
		"test_group_with_influxdb": {
			promql:       `sum(count_over_time(datasource:result_table:influxdb:cpu_summary{}[1m]))`,
			stmt:         `sum(last_over_time(a[1m] offset -59s999ms))`,
			instanceType: consul.PrometheusStorageType,
		},
	}

	err := featureFlag.MockFeatureFlag(ctx, `{
	  	"must-vm-query": {
	  		"variations": {
	  			"true": true,
	  			"false": false
	  		},
	  		"targeting": [{
	  			"query": "tableID in [\"result_table.vm\"]",
	  			"percentage": {
	  				"true": 100,
	  				"false":0 
	  			}
	  		}],
	  		"defaultRule": {
	  			"variation": "false"
	  		}
	  	}
	  }`)
	if err != nil {
		log.Fatalf(ctx, err.Error())
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			if c.promql != "" {
				query, err := promQLToStruct(ctx, &structured.QueryPromQL{PromQL: c.promql})
				if err != nil {
					log.Fatalf(ctx, err.Error())
				}
				c.query = query
			}
			c.query.SpaceUid = spaceUid

			instance, stmt, err := queryTsToInstanceAndStmt(metadata.InitHashID(ctx), c.query)
			if err != nil {
				log.Fatalf(ctx, err.Error())
			}

			assert.Equal(t, c.stmt, stmt)
			if instance != nil {
				assert.Equal(t, c.instanceType, instance.InstanceType())
			}
		})
	}
}

func TestMultiRouteQuerySortingIssues(t *testing.T) {
	mock.Init()
	ctx := context.Background()

	influxdb.MockSpaceRouter(ctx)

	router, err := influxdb.GetSpaceTsDbRouter()
	assert.NoError(t, err)

	err = router.Add(ctx, ir.DataLabelToResultTableKey, "multi_route_test", &ir.ResultTableList{
		"route1_table", "route2_table",
	})
	assert.NoError(t, err)

	err = router.Add(ctx, ir.ResultTableDetailKey, "route1_table", &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     "route1_table",
		DB:          "route1",
		StorageType: "elasticsearch",
		DataLabel:   "route1",
	})
	assert.NoError(t, err)

	err = router.Add(ctx, ir.ResultTableDetailKey, "route2_table", &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     "route2_table",
		DB:          "route2",
		StorageType: "elasticsearch",
		DataLabel:   "route2",
	})
	assert.NoError(t, err)

	space := router.GetSpace(ctx, "bkcc__2")
	if space == nil {
		space = make(ir.Space)
	}
	space["route1_table"] = &ir.SpaceResultTable{
		TableId: "route1_table",
	}
	space["route2_table"] = &ir.SpaceResultTable{
		TableId: "route2_table",
	}
	space["multi_route_test"] = &ir.SpaceResultTable{
		TableId: "multi_route_test", // multi_route_test -> route1_table, route2_table
	}
	err = router.Add(ctx, ir.SpaceToResultTableKey, "bkcc__2", &space)
	assert.NoError(t, err)

	const EsUrlDomain = "http://127.0.0.1:93002"

	route1Mappings := `{"route1":{"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"gseIndex":{"type":"long"},"iterationIndex":{"type":"long"},"__data_label":{"type":"keyword"},"log":{"type":"text"}}}}}`
	httpmock.RegisterResponder(http.MethodGet, EsUrlDomain+"/route1/_mapping/", httpmock.NewStringResponder(http.StatusOK, route1Mappings))

	route2Mappings := `{"route2":{"mappings":{"properties":{"dtEventTimeStamp":{"type":"date"},"gseIndex":{"type":"long"},"iterationIndex":{"type":"long"},"__data_label":{"type":"keyword"},"log":{"type":"text"}}}}}`
	httpmock.RegisterResponder(http.MethodGet, EsUrlDomain+"/route2/_mapping/", httpmock.NewStringResponder(http.StatusOK, route2Mappings))

	route1SearchResponse := `{
		"took": 5,
		"timed_out": false,
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 1000, "relation": "eq"},
			"max_score": null,
			"hits": [
				{"_index": "route1", "_id": "id1", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 1, "iterationIndex": 3, "__data_label": "route1", "log": "route1 message 1"}},
				{"_index": "route1", "_id": "id2", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 1, "iterationIndex": 2, "__data_label": "route1", "log": "route1 message 2"}},
				{"_index": "route1", "_id": "id3", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 1, "iterationIndex": 1, "__data_label": "route1", "log": "route1 message 3"}},
				{"_index": "route1", "_id": "id4", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 1, "iterationIndex": 0, "__data_label": "route1", "log": "route1 message 4"}},
				{"_index": "route1", "_id": "id5", "_score": null, "_source": {"dtEventTimeStamp": "1752141700000", "gseIndex": 5, "iterationIndex": 2, "__data_label": "route1", "log": "route1 message 5"}},
				{"_index": "route1", "_id": "id6", "_score": null, "_source": {"dtEventTimeStamp": "1752141700000", "gseIndex": 5, "iterationIndex": 1, "__data_label": "route1", "log": "route1 message 6"}},
				{"_index": "route1", "_id": "id7", "_score": null, "_source": {"dtEventTimeStamp": "1752141700000", "gseIndex": 5, "iterationIndex": 0, "__data_label": "route1", "log": "route1 message 7"}},
				{"_index": "route1", "_id": "id8", "_score": null, "_source": {"dtEventTimeStamp": "1752141600000", "gseIndex": 8, "iterationIndex": 1, "__data_label": "route1", "log": "route1 message 8"}},
				{"_index": "route1", "_id": "id9", "_score": null, "_source": {"dtEventTimeStamp": "1752141600000", "gseIndex": 8, "iterationIndex": 0, "__data_label": "route1", "log": "route1 message 9"}},
				{"_index": "route1", "_id": "id10", "_score": null, "_source": {"dtEventTimeStamp": "1752141500000", "gseIndex": 10, "iterationIndex": 0, "__data_label": "route1", "log": "route1 message 10"}}
			]
		}
	}`
	httpmock.RegisterResponder(http.MethodPost, EsUrlDomain+"/route1/_search", httpmock.NewStringResponder(http.StatusOK, route1SearchResponse))

	route2SearchResponse := `{
		"took": 5,
		"timed_out": false,
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 2000, "relation": "eq"},
			"max_score": null,
			"hits": [
				{"_index": "route2", "_id": "id1", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 3, "iterationIndex": 2, "__data_label": "route2", "log": "route2 message 1"}},
				{"_index": "route2", "_id": "id2", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 3, "iterationIndex": 1, "__data_label": "route2", "log": "route2 message 2"}},
				{"_index": "route2", "_id": "id3", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 3, "iterationIndex": 0, "__data_label": "route2", "log": "route2 message 3"}},
				{"_index": "route2", "_id": "id4", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 2, "iterationIndex": 3, "__data_label": "route2", "log": "route2 message 4"}},
				{"_index": "route2", "_id": "id5", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 2, "iterationIndex": 2, "__data_label": "route2", "log": "route2 message 5"}},
				{"_index": "route2", "_id": "id6", "_score": null, "_source": {"dtEventTimeStamp": "1752141800000", "gseIndex": 2, "iterationIndex": 1, "__data_label": "route2", "log": "route2 message 6"}},
				{"_index": "route2", "_id": "id7", "_score": null, "_source": {"dtEventTimeStamp": "1752141700000", "gseIndex": 7, "iterationIndex": 2, "__data_label": "route2", "log": "route2 message 7"}},
				{"_index": "route2", "_id": "id8", "_score": null, "_source": {"dtEventTimeStamp": "1752141700000", "gseIndex": 7, "iterationIndex": 1, "__data_label": "route2", "log": "route2 message 8"}},
				{"_index": "route2", "_id": "id9", "_score": null, "_source": {"dtEventTimeStamp": "1752141700000", "gseIndex": 6, "iterationIndex": 0, "__data_label": "route2", "log": "route2 message 9"}},
				{"_index": "route2", "_id": "id10", "_score": null, "_source": {"dtEventTimeStamp": "1752141600000", "gseIndex": 9, "iterationIndex": 0, "__data_label": "route2", "log": "route2 message 10"}}
			]
		}
	}`
	httpmock.RegisterResponder(http.MethodPost, EsUrlDomain+"/route2/_search", httpmock.NewStringResponder(http.StatusOK, route2SearchResponse))

	// -dtEventTimeStamp, -gseIndex, -iterationIndex
	queryTs := &structured.QueryTs{
		SpaceUid: "bkcc__2",
		QueryList: []*structured.Query{
			{
				TableID: "multi_route_test",
				FieldList: []string{
					"dtEventTimeStamp",
					"gseIndex",
					"iterationIndex",
					"__data_label",
					"log",
				},
			},
		},
		Start: "1752141400000",
		End:   "1752141900000",
		OrderBy: structured.OrderBy{
			"-dtEventTimeStamp",
			"-gseIndex",
			"-iterationIndex",
		},
		Limit: 50,
	}

	_, list, _, err := queryRawWithInstance(ctx, queryTs)
	assert.NoError(t, err)

	for i, item := range list {
		dtEventTimeStamp := getIntValue(item["dtEventTimeStamp"])
		gseIndex := getIntValue(item["gseIndex"])
		iterationIndex := getIntValue(item["iterationIndex"])
		dataLabel := item["__data_label"]

		t.Logf("第%d条: dtEventTimeStamp=%d, gseIndex=%d, iterationIndex=%d, 来源=%s",
			i+1, dtEventTimeStamp, gseIndex, iterationIndex, dataLabel)
	}

	sortingErrors := []string{}
	for i := 0; i < len(list)-1; i++ {
		current := list[i]
		next := list[i+1]

		currentTime := getIntValue(current["dtEventTimeStamp"])
		nextTime := getIntValue(next["dtEventTimeStamp"])

		currentGse := getIntValue(current["gseIndex"])
		nextGse := getIntValue(next["gseIndex"])

		currentIter := getIntValue(current["iterationIndex"])
		nextIter := getIntValue(next["iterationIndex"])

		//  -dtEventTimeStamp, -gseIndex, -iterationIndex
		if currentTime > nextTime {
			continue
		} else if currentTime == nextTime {
			// gseIndex
			if currentGse > nextGse {
				continue
			} else if currentGse == nextGse {
				// iterationIndex
				if currentIter >= nextIter {
					continue
				} else {
					sortingErrors = append(sortingErrors, fmt.Sprintf("位置%d和%d: iterationIndex排序错误 %d < %d", i, i+1, currentIter, nextIter))
				}
			} else {
				sortingErrors = append(sortingErrors, fmt.Sprintf("位置%d和%d: gseIndex排序错误 %d < %d", i, i+1, currentGse, nextGse))
			}
		} else {
			sortingErrors = append(sortingErrors, fmt.Sprintf("位置%d和%d: dtEventTimeStamp排序错误 %d < %d", i, i+1, currentTime, nextTime))
		}
	}

	route1Count := 0
	route2Count := 0
	for _, item := range list {
		if dataLabel, ok := item["__data_label"]; ok {
			if dataLabel == "route1" {
				route1Count++
			} else if dataLabel == "route2" {
				route2Count++
			}
		}
	}

	if len(sortingErrors) > 0 {
		t.Logf("errors:")
		for _, err := range sortingErrors {
			t.Logf(" %s", err)
		}
	}

	if route1Count == 0 || route2Count == 0 {
		t.Errorf("merge data error: expected both routes data, got route1=%d, route2=%d", route1Count, route2Count)
	} else {
		t.Logf("merge data success: route1=%d, route2=%d", route1Count, route2Count)
	}

	expectedOrder := []map[string]interface{}{
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(3), "iterationIndex": int64(2), "__data_label": "route2"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(3), "iterationIndex": int64(1), "__data_label": "route2"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(3), "iterationIndex": int64(0), "__data_label": "route2"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(2), "iterationIndex": int64(3), "__data_label": "route2"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(2), "iterationIndex": int64(2), "__data_label": "route2"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(2), "iterationIndex": int64(1), "__data_label": "route2"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(1), "iterationIndex": int64(3), "__data_label": "route1"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(1), "iterationIndex": int64(2), "__data_label": "route1"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(1), "iterationIndex": int64(1), "__data_label": "route1"},
		{"dtEventTimeStamp": int64(1752141800000), "gseIndex": int64(1), "iterationIndex": int64(0), "__data_label": "route1"},
	}

	for i, expected := range expectedOrder {
		if i >= len(list) {
			break
		}
		actual := list[i]

		if getIntValue(actual["dtEventTimeStamp"]) != expected["dtEventTimeStamp"].(int64) ||
			getIntValue(actual["gseIndex"]) != expected["gseIndex"].(int64) ||
			getIntValue(actual["iterationIndex"]) != expected["iterationIndex"].(int64) ||
			actual["__data_label"] != expected["__data_label"] {
			t.Errorf("number %d mismatch: expected %v, got %v", i+1, expected, actual)
		}
	}
}

func getIntValue(value interface{}) int64 {
	switch v := value.(type) {
	case string:
		var result int64
		if _, err := fmt.Sscanf(v, "%d", &result); err == nil {
			return result
		}
		return 0
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func TestQueryRawWithScroll_ESFlow(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.es"
	spaceUid := influxdb.SpaceUid
	testDataLabel := influxdb.ResultTableEs
	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")
	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     testTableId,
		DB:          "es_index",
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   "es",
	})
	assert.NoError(t, err)

	resultTableList := ir.ResultTableList{testTableId}
	err = router.Add(ctx, ir.DataLabelToResultTableKey, "es", &resultTableList)
	assert.NoError(t, err)

	err = router.Add(ctx, ir.DataLabelToResultTableKey, testTableId, &resultTableList)
	assert.NoError(t, err)

	route01 := "route1_table"
	err = router.Add(ctx, ir.ResultTableDetailKey, route01, &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     route01,
		DB:          route01,
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   route01,
	})
	assert.NoError(t, err)

	err = router.Add(ctx, ir.ResultTableDetailKey, "route2_table", &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     "route2_table",
		DB:          "route2",
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   "route2",
	})
	assert.NoError(t, err)

	type expectResult struct {
		desc     string
		total    int64
		done     bool
		hasData  bool
		mockData map[string]any
	}

	require.NoError(t, err, "Failed to add space mapping")

	s, err := miniredis.Run()
	require.NoError(t, err, "Failed to start miniredis")
	defer s.Close()

	options := &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
		DB:    0,
	}

	err = redisUtil.SetInstance(ctx, "test-scroll", options)
	require.NoError(t, err, "Failed to set unify-query redis instance")

	initEsMockData := map[string]any{
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"es_test1"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":1,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_1","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"es_test2"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":2,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_2","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"es_test3"}}]}}`,
	}
	secondRoundEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0"}`: `{"_scroll_id":"scroll_id_0_next","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"4","_source":{"dtEventTimeStamp":"1723594004000","data":"es_test4"}}]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_1"}`: `{"_scroll_id":"scroll_id_1_next","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"5","_source":{"dtEventTimeStamp":"1723594005000","data":"es_test5"}}]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_2"}`: `{"_scroll_id":"scroll_id_2_next","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"es_test6"}}]}}`,
	}

	thirdRoundEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0_next"}`: `{"_scroll_id":"","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_1_next"}`: `{"_scroll_id":"scroll_id_1_final","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"7","_source":{"dtEventTimeStamp":"1723594007000","data":"es_test7"}}]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_2_next"}`: `{"_scroll_id":"scroll_id_2_final","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"8","_source":{"dtEventTimeStamp":"1723594008000","data":"es_test8"}}]}}`,
	}

	allDoneMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_1_final"}`: `{"_scroll_id":"","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_2_final"}`: `{"_scroll_id":"","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
	}

	start := "1723594000"
	end := "1723595000"
	type testCase struct {
		queryTs  *structured.QueryTs
		expected []expectResult
	}

	tCase := testCase{
		queryTs: &structured.QueryTs{
			SpaceUid: spaceUid,
			QueryList: []*structured.Query{
				{
					TableID: structured.TableID(testDataLabel),
				},
			},
			Scroll:   "9m",
			Timezone: "Asia/Shanghai",
			Limit:    10,
			Start:    start,
			End:      end,
		},
		expected: []expectResult{
			{
				desc:     "First scroll request",
				total:    3,
				done:     false,
				hasData:  true,
				mockData: initEsMockData,
			},
			{
				desc:     "Second scroll request",
				total:    3,
				done:     false,
				hasData:  true,
				mockData: secondRoundEsMockData,
			},
			{
				desc:     "Third scroll request - slice 0 ends, others continue",
				total:    2,
				done:     false,
				hasData:  true,
				mockData: thirdRoundEsMockData,
			},
			{
				desc:     "Fourth scroll request - should be done",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: allDoneMockData,
			},
		},
	}
	user := &metadata.User{
		Key:       "username:test_scroll_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}

	queryTsBytes, err := json.Marshal(tCase.queryTs)
	require.NoError(t, err, "Failed to marshal queryTs")
	queryTsStr := string(queryTsBytes)

	for i, c := range tCase.expected {
		t.Logf("Running step %d: %s", i+1, c.desc)

		ctx = metadata.InitHashID(context.Background())

		metadata.SetUser(ctx, user)

		mock.Es.Set(c.mockData)

		session, err := redisUtil.GetOrCreateScrollSession(ctx, queryTsStr, ScrollWindowTimeout, ScrollMaxSlice, 10)
		require.NoError(t, err, "Failed to get scroll session")
		err = session.AcquireLock(ctx)
		require.NoErrorf(t, err, "Failed to acquire lock for scroll session in step %d", i+1)

		var queryTsCopy structured.QueryTs
		err = json.Unmarshal([]byte(queryTsStr), &queryTsCopy)
		require.NoError(t, err, "Failed to unmarshal queryTs")

		t.Logf("Starting queryRawWithScroll with session: %+v", session)
		total, list, _, err := queryRawWithScroll(ctx, &queryTsCopy, session)
		done := session.Done()
		t.Logf("queryRawWithScroll returned: total=%d, len(list)=%d, done=%v, err=%v", total, len(list), done, err)
		hasData := len(list) > 0
		assert.NoError(t, err, "QueryRawWithScroll should not return error for step %d", i+1)
		assert.Equal(t, c.total, total, "Total should match expected value for step %d", i+1)
		assert.Equal(t, c.done, session.Done(), "Done should match expected value for step %d", i+1)
		assert.Equal(t, c.hasData, hasData, "HasData should match expected value for step %d", i+1)

		if c.hasData {
			assert.Greater(t, len(list), 0, "Should have data when hasData is true for step %d", i+1)
		} else {
			assert.Equal(t, 0, len(list), "Should have no data when hasData is false for step %d", i+1)
		}
		err = session.ReleaseLock(ctx)
		require.NoError(t, err, "Failed to release lock for scroll session in step %d", i+1)
		t.Logf("Session: %+v", session)
	}
}

func TestQueryRawWithScroll_DorisFlow(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.doris"

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")

	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
		StorageId:   4,
		TableId:     testTableId,
		DB:          "doris_db",
		StorageType: consul.BkSqlStorageType,
		DataLabel:   "doris_test",
	})
	assert.NoError(t, err)

	type expectResult struct {
		desc     string
		total    int64
		done     bool
		hasData  bool
		mockData map[string]any
	}

	require.NoError(t, err, "Failed to add space mapping")

	s, err := miniredis.Run()
	require.NoError(t, err, "Failed to start miniredis")
	defer s.Close()

	options := &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
		DB:    0,
	}

	err = redisUtil.SetInstance(ctx, "test", options)
	require.NoError(t, err, "Failed to set unify-query redis instance")

	initDorisMockData := map[string]any{
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10`:           `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594001000","data":"doris_test1"},{"dtEventTimeStamp":"1723594002000","data":"doris_test2"}]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 10`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594003000","data":"doris_test3"},{"dtEventTimeStamp":"1723594004000","data":"doris_test4"}]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 20`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594005000","data":"doris_test5"},{"dtEventTimeStamp":"1723594006000","data":"doris_test6"}]}}`,
	}

	inProgressDorisMockData := map[string]any{
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 30`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594007000","data":"doris_test7"},{"dtEventTimeStamp":"1723594008000","data":"doris_test8"}]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 40`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594009000","data":"doris_test9"},{"dtEventTimeStamp":"1723594010000","data":"doris_test10"}]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 50`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594011000","data":"doris_test11"},{"dtEventTimeStamp":"1723594012000","data":"doris_test12"}]}}`,
	}

	thirdRoundDorisMockData := map[string]any{
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 60`: `{"result":true,"code":"00","message":"","data":{"totalRecords":0,"total_record_size":0,"list":[]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 70`: `{"result":true,"code":"00","message":"","data":{"totalRecords":0,"total_record_size":0,"list":[]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 80`: `{"result":true,"code":"00","message":"","data":{"totalRecords":0,"total_record_size":0,"list":[]}}`,
	}

	start := "1723594000"
	end := "1723595000"
	type testCase struct {
		queryTs  *structured.QueryTs
		expected []expectResult
	}

	tCase := testCase{
		queryTs: &structured.QueryTs{
			SpaceUid: spaceUid,
			QueryList: []*structured.Query{
				{
					TableID: structured.TableID(testTableId),
				},
			},
			Timezone: "Asia/Shanghai",
			Scroll:   "9m",
			Limit:    10,
			Start:    start,
			End:      end,
		},
		expected: []expectResult{
			{
				desc:     "First scroll request - slice 0,1,2 with OFFSET 0,10,20",
				total:    6,
				done:     false,
				hasData:  true,
				mockData: initDorisMockData,
			},
			{
				desc:     "Second scroll request - slice 0,1,2 with OFFSET 30,40,50",
				total:    6,
				done:     false,
				hasData:  true,
				mockData: inProgressDorisMockData,
			},
			{
				desc:     "Third scroll request - slice 0,1,2 with OFFSET 60,70,80 - should be done",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: thirdRoundDorisMockData,
			},
			{
				desc:     "Fourth scroll request - should still be done",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: thirdRoundDorisMockData,
			},
		},
	}
	queryTsBytes, err := json.Marshal(tCase.queryTs)
	require.NoError(t, err, "Failed to marshal queryTs")
	queryTsStr := string(queryTsBytes)

	user := &metadata.User{
		Key:       "username:test_doris_scroll_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}
	session, err := redisUtil.GetOrCreateScrollSession(t.Context(), queryTsStr, ScrollWindowTimeout, ScrollMaxSlice, 10)
	require.NoError(t, err, "Failed to get scroll session")

	for i, c := range tCase.expected {
		t.Logf("Running step %d: %s", i+1, c.desc)

		ctx = metadata.InitHashID(context.Background())
		metadata.SetUser(ctx, user)

		mock.BkSQL.Set(c.mockData)

		require.NoError(t, err, "Failed to make slices")

		var queryTsCopy structured.QueryTs
		err = json.Unmarshal([]byte(queryTsStr), &queryTsCopy)
		require.NoError(t, err, "Failed to unmarshal queryTs")
		err = session.AcquireLock(ctx)
		require.NoErrorf(t, err, "Failed to acquire lock for scroll session in step %d", i+1)
		total, list, _, err := queryRawWithScroll(ctx, &queryTsCopy, session)
		done := session.Done()
		t.Logf("queryRawWithScroll returned: total=%d, len(list)=%d, done=%v, err=%v", total, len(list), done, err)
		err = session.ReleaseLock(ctx)
		require.NoError(t, err, "Failed to release lock for scroll session in step %d", i+1)
		hasData := len(list) > 0
		t.Logf("Session: %+v", session)
		assert.NoError(t, err, "QueryRawWithScroll should not return error for step %d", i+1)
		assert.Equal(t, c.total, total, "Total should match expected value for step %d", i+1)
		assert.Equal(t, c.done, session.Done(), "Done should match expected value for step %d", i+1)
		assert.Equal(t, c.hasData, hasData, "HasData should match expected value for step %d", i+1)
	}
}
