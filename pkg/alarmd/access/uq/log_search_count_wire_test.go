// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import "testing"

// A keyword log strategy that names no field and says AVG asks the provider
// to count documents, not to average _index: an Elasticsearch-backed index
// set refuses an avg over _index and a SQL-backed one refuses AVG(*). The two
// index sets stand for one of each store; the request alarmd sends is the
// same shape for both, and the provider picks the store by the index set.
func TestAKeywordLogStrategyWithoutAFieldAsksForADocumentCount(t *testing.T) {
	for _, test := range []struct{ name, config, wire string }{
		{name: "search store",
			config: `{"data_source_label":"bk_log_search","data_type_label":"log","index_set_id":7,"metric_field":"","agg_method":"AVG","agg_interval":60,"agg_dimension":[],"query_string":"*","time_field":"dtEventTimeStamp","result_table_id":"","alias":"a"}`,
			wire:   `{"query_list":[{"data_source":"bklog","table_id":"bklog_index_set_7","field_name":"_index","driver":"influxdb","time_field":"dtEventTimeStamp","is_regexp":false,"reference_name":"a","function":[{"method":"sum","position":0}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0},"conditions":{},"query_string":"*"}],"metric_merge":"a","start_time":"1700123000","end_time":"1700124000","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"Asia/Shanghai","not_time_align":false}`},
		{name: "sql store",
			config: `{"data_source_label":"bk_log_search","data_type_label":"log","index_set_id":8,"metric_field":"","agg_method":"AVG","agg_interval":60,"agg_dimension":[],"query_string":"192.0.2.1","time_field":"dtEventTimeStamp","result_table_id":"","alias":"a"}`,
			wire:   `{"query_list":[{"data_source":"bklog","table_id":"bklog_index_set_8","field_name":"_index","driver":"influxdb","time_field":"dtEventTimeStamp","is_regexp":false,"reference_name":"a","function":[{"method":"sum","position":0}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0},"conditions":{},"query_string":"*192.0.2.1*"}],"metric_merge":"a","start_time":"1700123000","end_time":"1700124000","step":"60s","space_uid":"bkcc__2","down_sample_range":"","timezone":"Asia/Shanghai","not_time_align":false}`},
	} {
		path, wire, err := buildWireRequest(compiledSpec(t, test.config))
		if err != nil {
			t.Fatal(err)
		}
		if path != "/query/ts" || string(wire) != test.wire {
			t.Errorf("%s: path %s\nwire %s", test.name, path, wire)
		}
	}
}
