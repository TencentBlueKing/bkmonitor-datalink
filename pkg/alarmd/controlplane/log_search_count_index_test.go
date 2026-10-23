// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"fmt"
	"testing"
)

// A log search query without a field counts documents whatever method the
// strategy names, as the platform's LogSearchTimeSeriesDataSource does (and
// the log source inherits): COUNT on _index. The method is kept once a field
// is named. Both data types go through the same rule.
func TestALogSearchQueryWithoutAFieldCountsDocuments(t *testing.T) {
	planner, err := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, dataType := range []string{"log", "time_series"} {
		for _, test := range []struct {
			name, field, wantField, wantWindow, wantFunction string
		}{
			// The strategy says AVG; averaging _index is refused by the log
			// store ("Field [_index] of type [_index] is not supported for
			// aggregation [avg]", or SELECT AVG(*) on a SQL store).
			{name: "no field", field: "", wantField: "_index", wantWindow: "count_over_time", wantFunction: "sum"},
			{name: "a field", field: "bytes", wantField: "bytes", wantWindow: "avg_over_time", wantFunction: "avg"},
		} {
			t.Run(dataType+"/"+test.name, func(t *testing.T) {
				config := fmt.Sprintf(`{"data_source_label":"bk_log_search","data_type_label":%q,"index_set_id":7,"metric_field":%q,"agg_method":"AVG","alias":"a","agg_interval":60,"agg_dimension":["host"],"query_string":"*"}`, dataType, test.field)
				facts, err := planner.CompilePrimaryQuery(context.Background(), pollingTestSource(config))
				if err != nil {
					t.Fatal(err)
				}
				q := facts.QueryList[0]
				if q.FieldName != test.wantField || q.TimeAggregation.Method != test.wantWindow {
					t.Fatalf("field %q window %q, want %q %q", q.FieldName, q.TimeAggregation.Method, test.wantField, test.wantWindow)
				}
				if len(q.Functions) == 0 || q.Functions[0].Method != test.wantFunction {
					t.Fatalf("functions %+v, want %q first", q.Functions, test.wantFunction)
				}
			})
		}
	}
}
