// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestFormatFactory_RangeQueryAndAggregates(t *testing.T) {
	var start int64 = 1721024820
	var end int64 = 1721046420

	for name, c := range map[string]struct {
		timeField  metadata.TimeField
		aggregates metadata.Aggregates
		expected   string
	}{
		"second date field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: "date",
				Unit: Second,
			},
			expected: ``,
		},
		"second time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: Second,
			},
			expected: `{"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":false,"to":1721046420}}}}`,
		},
		"int time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeInt,
				Unit: Second,
			},
			expected: `{"query":{"range":{"time":{"from":1721024820,"include_lower":true,"include_upper":false,"to":1721046420}}}}`,
		},
		"aggregate second time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/ShangHai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","fixed_interval":"1m","min_doc_count":0,"time_zone":"Asia/ShangHai"}}},"terms":{"field":"gseIndex","size":0}}},"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":false,"to":1721046420}}}}`,
		},
		"aggregate second int field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeInt,
				Unit: Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/ShangHai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","fixed_interval":"1m","min_doc_count":0}}},"terms":{"field":"gseIndex","size":0}}},"query":{"range":{"time":{"from":1721024820,"include_lower":true,"include_upper":false,"to":1721046420}}}}`,
		},
		"aggregate millisecond int field": {
			timeField: metadata.TimeField{
				Name: "dtEventTime",
				Type: TimeFieldTypeInt,
				Unit: Millisecond,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/ShangHai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"dtEventTime":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420000,"min":1721024820000},"field":"dtEventTime","fixed_interval":"1m","min_doc_count":0}}},"terms":{"field":"gseIndex","size":0}}},"query":{"range":{"dtEventTime":{"from":1721024820000,"include_lower":true,"include_upper":false,"to":1721046420000}}}}`,
		},
		"aggregate millisecond time field": {
			timeField: metadata.TimeField{
				Name: "dtEventTime",
				Type: TimeFieldTypeTime,
				Unit: Millisecond,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/ShangHai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"dtEventTime":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420000,"min":1721024820000},"field":"dtEventTime","fixed_interval":"1m","min_doc_count":0,"time_zone":"Asia/ShangHai"}}},"terms":{"field":"gseIndex","size":0}}},"query":{"range":{"dtEventTime":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":false,"to":1721046420}}}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).
				WithQuery("value", c.timeField, start, end, 0, 0).
				WithTransform(structured.QueryRawFormat(ctx), structured.PromQueryFormat(ctx))

			ss := elastic.NewSearchSource()
			rangeQuery, err := fact.RangeQuery()
			assert.Nil(t, err)
			if err == nil {
				ss.Query(rangeQuery)
				if len(c.aggregates) > 0 {
					aggName, agg, aggErr := fact.EsAgg(c.aggregates)
					assert.Nil(t, aggErr)
					if aggErr == nil {
						ss.Aggregation(aggName, agg)
					}
				}
			}

			body, _ := ss.Source()
			bodyJson, _ := json.Marshal(body)
			bodyString := string(bodyJson)
			assert.Equal(t, c.expected, bodyString)
		})
	}
}
