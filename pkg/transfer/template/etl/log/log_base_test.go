// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cstockton/go-conv"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// LogProcessorUsageTest
type LogProcessorUsageTest struct {
	testsuite.ETLSuite
}

// TestUsage :
func (s *LogProcessorUsageTest) TestUsage() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig, `
{
    "result_table_list":[
        {
            "option":{
                "es_unique_field_list":[
                    "ip",
                    "path",
                    "gseIndex",
                    "_iteration_idx"
                ]
            },
            "schema_type":"free",
            "result_table":"2_log.log",
            "field_list":[
                {
                    "default_value":null,
                    "alias_name":"bk_biz_id",
                    "tag":"metric",
                    "description":"\u4e1a\u52a1ID\uff08\u4e34\u65f6\uff09\uff0c\u5f85\u7cfb\u7edf\u5185\u7f6e\u5b57\u6bb5\u5b8c\u5584\u914d\u7f6e",
                    "type":"float",
                    "is_config_by_user":true,
                    "field_name":"_bizid_",
                    "unit":"",
                    "option":{
                        "es_include_in_all":true,
                        "es_type":"keyword",
                        "es_doc_values":false,
                        "es_index":true
                    }
                },
                {
                    "default_value":null,
                    "alias_name":"dtEventTimeStamp",
                    "tag":"metric",
                    "description":"\u65f6\u95f4\u6233",
                    "type":"timestamp",
                    "is_config_by_user":true,
                    "field_name":"_utctime_",
                    "unit":"",
                    "option":{
                        "time_format":"datetime",
                        "es_format":"epoch_millis",
                        "es_type":"date",
                        "es_doc_values":false,
                        "es_include_in_all":true,
                        "time_zone":"0",
                        "es_index":true
                    }
                },
                {
                    "default_value":null,
                    "alias_name":"gseIndex",
                    "tag":"metric",
                    "description":"gse\u7d22\u5f15",
                    "type":"float",
                    "is_config_by_user":true,
                    "field_name":"_gseindex_",
                    "unit":"",
                    "option":{
                        "es_include_in_all":false,
                        "es_type":"long",
                        "es_doc_values":true,
                        "es_index":true
                    }
                },
                {
                    "default_value":null,
                    "alias_name":"log",
                    "tag":"metric",
                    "description":"\u65e5\u5fd7\u5185\u5bb9",
                    "type":"string",
                    "is_config_by_user":true,
                    "field_name":"log",
                    "unit":"",
                    "option":{
                        "es_include_in_all":true,
                        "es_type":"text",
                        "es_doc_values":false,
                        "es_index":true
                    }
                },
                {
                    "default_value":null,
                    "alias_name":"path",
                    "tag":"dimension",
                    "description":"\u65e5\u5fd7\u8def\u5f84",
                    "type":"string",
                    "is_config_by_user":true,
                    "field_name":"_path_",
                    "unit":"",
                    "option":{
                        "es_include_in_all":true,
                        "es_type":"keyword",
                        "es_doc_values":true,
                        "es_index":true
                    }
                }
            ]
        }
    ],
    "source_label":"bk_monitor",
    "type_label":"log",
    "data_id":1200151,
    "etl_config":"bk_log_text",
    "option":{
        "group_info_alias":"_private_",
        "encoding":"UTF-8"
    }
}`,
	)

	processor, err := log.NewLogProcessor(s.CTX, "test", nil)
	s.NoError(err)

	cases := []map[string]interface{}{
		{
			"dimensions": map[string]interface{}{
				"path": "/tmp/health_check.log",
			},
			"metrics": map[string]interface{}{
				"log":              "1",
				"bk_biz_id":        0.0,
				"gseIndex":         1.0,
				"dtEventTimeStamp": 1570527709.0,
				"_iteration_idx":   0.0,
			},
			"group_info": []map[string]string{
				{"bk_app_code": "bk_log_search"},
			},
		},
		{
			"dimensions": map[string]interface{}{
				"path": "/tmp/health_check.log",
			},
			"metrics": map[string]interface{}{
				"log":              "2",
				"bk_biz_id":        0.0,
				"gseIndex":         1.0,
				"dtEventTimeStamp": 1570527709.0,
				"_iteration_idx":   1.0,
			},
			"group_info": []map[string]string{
				{"bk_app_code": "bk_log_search"},
			},
		},
	}

	s.RunN(2, `{"_bizid_":0,"_cloudid_":0,"_dstdataid_":1200124,"_errorcode_":0,"_gseindex_":1,"_path_":"/tmp/health_check.log","_private_":[{"bk_app_code":"bk_log_search"}],"_server_":"127.0.0.1","_srcdataid_":1200124,"_time_":"2019-10-08 17:41:49","_type_":0,"_utctime_":"2019-10-08 09:41:49","_value_":["1", "2"],"_worldid_":-1}`,
		processor,
		func(result map[string]interface{}) {
			metrics := result["metrics"].(map[string]interface{})
			idx := conv.Int(metrics["_iteration_idx"])
			excepts := cases[idx]
			excepts["time"] = result["time"].(float64)
			s.EqualRecord(result, excepts)
		},
	)
}

// LogProcessorDbmTest
type LogProcessorDbmTest struct {
	testsuite.ETLSuite
}

func (s *LogProcessorDbmTest) TestDbmParse() {
	s.CTX = testsuite.PipelineConfigStringInfoContext(
		s.CTX, s.PipelineConfig,
		`{
    "result_table_list":[
        {
            "option":{
                "es_unique_field_list":[
                    "ip",
                    "path",
                    "gseIndex",
                    "_iteration_idx"
                ]
            },
            "schema_type":"free",
            "result_table":"2_log.log",
            "field_list":[
                {
                    "default_value":null,
                    "alias_name":"bk_biz_id",
                    "tag":"metric",
                    "description":"\u4e1a\u52a1ID\uff08\u4e34\u65f6\uff09\uff0c\u5f85\u7cfb\u7edf\u5185\u7f6e\u5b57\u6bb5\u5b8c\u5584\u914d\u7f6e",
                    "type":"float",
                    "is_config_by_user":true,
                    "field_name":"_bizid_",
                    "unit":"",
                    "option":{
                        "es_include_in_all":true,
                        "es_type":"keyword",
                        "es_doc_values":false,
                        "es_index":true
                    }
                },
                {
                    "default_value":null,
                    "alias_name":"dtEventTimeStamp",
                    "tag":"metric",
                    "description":"\u65f6\u95f4\u6233",
                    "type":"timestamp",
                    "is_config_by_user":true,
                    "field_name":"_utctime_",
                    "unit":"",
                    "option":{
                        "time_format":"datetime",
                        "es_format":"epoch_millis",
                        "es_type":"date",
                        "es_doc_values":false,
                        "es_include_in_all":true,
                        "time_zone":"0",
                        "es_index":true
                    }
                },
                {
                    "default_value":null,
                    "alias_name":"gseIndex",
                    "tag":"metric",
                    "description":"gse\u7d22\u5f15",
                    "type":"float",
                    "is_config_by_user":true,
                    "field_name":"_gseindex_",
                    "unit":"",
                    "option":{
                        "es_include_in_all":false,
                        "es_type":"long",
                        "es_doc_values":true,
                        "es_index":true
                    }
                },
                {
                    "default_value":null,
                    "alias_name":"log",
                    "tag":"metric",
                    "description":"\u65e5\u5fd7\u5185\u5bb9",
                    "type":"string",
                    "is_config_by_user":true,
                    "field_name":"log",
                    "unit":"",
                    "option":{
                        "es_include_in_all":true,
                        "es_type":"text",
                        "es_doc_values":false,
                        "es_index":true,
						"dbm_enabled": true,
                        "dbm_url": "http://localhost:48088/parse",
                        "dbm_field": "parsed_sql"
                    }
                },
                {
                    "default_value":null,
                    "alias_name":"path",
                    "tag":"dimension",
                    "description":"\u65e5\u5fd7\u8def\u5f84",
                    "type":"string",
                    "is_config_by_user":true,
                    "field_name":"_path_",
                    "unit":"",
                    "option":{
                        "es_include_in_all":true,
                        "es_type":"keyword",
                        "es_doc_values":true,
                        "es_index":true
                    }
                }
            ]
        }
    ],
    "source_label":"bk_monitor",
    "type_label":"log",
    "data_id":1200151,
    "etl_config":"bk_log_text",
    "option":{
        "group_info_alias":"_private_",
        "encoding":"UTF-8"
    }
}`,
	)

	processor, err := log.NewLogProcessor(s.CTX, "test", nil)
	s.NoError(err)

	cases := []map[string]interface{}{
		{
			"dimensions": map[string]interface{}{
				"path": "/tmp/health_check.log",
			},
			"metrics": map[string]interface{}{
				"log": "select table1 from db1;",
				"parsed_sql": map[string]interface{}{
					"command":           "select",
					"query_string":      "select table1 from db1",
					"query_digest_text": "select table1 from db1",
					"query_digest_md5":  "2399dfde29d825527043a78a73a6666",
					"db_name":           "db1",
					"table_name":        "table1",
					"query_length":      float64(20),
				},
				"bk_biz_id":        0.0,
				"gseIndex":         1.0,
				"dtEventTimeStamp": 1570527709.0,
				"_iteration_idx":   0.0,
			},
			"group_info": []map[string]string{
				{"bk_app_code": "bk_log_search"},
			},
		},
	}

	input := `
{
    "_bizid_":0,
    "_cloudid_":0,
    "_dstdataid_":1200124,
    "_errorcode_":0,
    "_gseindex_":1,
    "_path_":"/tmp/health_check.log",
    "_private_":[
        {
            "bk_app_code":"bk_log_search"
        }
    ],
    "_server_":"127.0.0.1",
    "_srcdataid_":1200124,
    "_time_":"2019-10-08 17:41:49",
    "_type_":0,
    "_utctime_":"2019-10-08 09:41:49",
    "_value_":[
        "select table1 from db1;"
    ],
    "_worldid_":-1
}
`

	srv := &http.Server{Addr: "localhost:48088"}
	http.HandleFunc("/parse", func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"command":           "select",
			"query_string":      "select table1 from db1",
			"query_digest_text": "select table1 from db1",
			"query_digest_md5":  "2399dfde29d825527043a78a73a6666",
			"db_name":           "db1",
			"table_name":        "table1",
			"query_length":      20,
		}
		b, _ := json.Marshal(response)
		fmt.Fprint(w, string(b))
	})

	go func() {
		srv.ListenAndServe()
	}()
	time.Sleep(time.Millisecond * 100)

	s.RunN(1, input,
		processor,
		func(result map[string]interface{}) {
			metrics := result["metrics"].(map[string]interface{})
			idx := conv.Int(metrics["_iteration_idx"])
			excepts := cases[idx]
			excepts["time"] = result["time"].(float64)
			s.EqualRecord(result, excepts)
		},
	)
	srv.Close()
}

// TestLogProcessorUsageTest :
func TestLogProcessorUsageTest(t *testing.T) {
	suite.Run(t, new(LogProcessorUsageTest))
}

// TestLogProcessorDbmTest :
func TestLogProcessorDbmTest(t *testing.T) {
	suite.Run(t, new(LogProcessorDbmTest))
}
