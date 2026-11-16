// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esquery

import (
	"encoding/json"
	"testing"

	"github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"
)

func TestRange(t *testing.T) {
	for _, tc := range []struct {
		name     string
		query    *elastic.RangeQuery
		expected map[string]interface{}
	}{
		{
			name:     "两端包含",
			query:    elastic.NewRangeQuery("value").Gte(10).Lte(20),
			expected: map[string]interface{}{"gte": 10, "lte": 20},
		},
		{
			name:     "两端排他",
			query:    elastic.NewRangeQuery("value").Gt(10).Lt(20),
			expected: map[string]interface{}{"gt": 10, "lt": 20},
		},
		{
			name:     "起点包含终点排他",
			query:    elastic.NewRangeQuery("value").From(10).To(20).IncludeUpper(false),
			expected: map[string]interface{}{"gte": 10, "lt": 20},
		},
		{
			name:     "起点排他终点包含",
			query:    elastic.NewRangeQuery("value").From(10).To(20).IncludeLower(false),
			expected: map[string]interface{}{"gt": 10, "lte": 20},
		},
		{
			name:     "零下界保留且无上界",
			query:    elastic.NewRangeQuery("value").Gte(0),
			expected: map[string]interface{}{"gte": 0},
		},
		{
			name:     "零上界保留且无下界",
			query:    elastic.NewRangeQuery("value").Lte(0),
			expected: map[string]interface{}{"lte": 0},
		},
		{
			name:     "仅有排他下界",
			query:    elastic.NewRangeQuery("value").Gt(-10),
			expected: map[string]interface{}{"gt": -10},
		},
		{
			name:     "仅有排他上界",
			query:    elastic.NewRangeQuery("value").Lt(-10),
			expected: map[string]interface{}{"lt": -10},
		},
		{
			name:     "两端无界不输出null",
			query:    elastic.NewRangeQuery("value"),
			expected: map[string]interface{}{},
		},
		{
			// 这两个相邻整数超过 float64 的整数精度，JSON 解码转换会使边界合并。
			name:     "纳秒整数保留原类型和数值",
			query:    elastic.NewRangeQuery("value").Gte(int64(1790812800000000001)).Lt(int64(1790812800000000002)),
			expected: map[string]interface{}{"gte": int64(1790812800000000001), "lt": int64(1790812800000000002)},
		},
		{
			name:     "小数边界",
			query:    elastic.NewRangeQuery("value").Gte(10.1).Lt(20.9),
			expected: map[string]interface{}{"gte": 10.1, "lt": 20.9},
		},
		{
			name: "保留日期格式时区和查询选项",
			query: elastic.NewRangeQuery("value").Gte("2026-11-13").Lt("now").
				Format("yyyy-MM-dd").TimeZone("Asia/Shanghai").Boost(0).Relation("within"),
			expected: map[string]interface{}{
				"gte": "2026-11-13", "lt": "now", "format": "yyyy-MM-dd",
				"time_zone": "Asia/Shanghai", "boost": float64(0), "relation": "within",
			},
		},
		{
			name:     "保留查询名称",
			query:    elastic.NewRangeQuery("value").Gte(10).QueryName("named-range"),
			expected: map[string]interface{}{"gte": 10},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, err := tc.query.Source()
			require.NoError(t, err)
			query := Range(tc.query)
			source, err := query.Source()
			require.NoError(t, err)
			ranges := source.(map[string]interface{})["range"].(map[string]interface{})
			require.Equal(t, tc.expected, ranges["value"])
			require.Equal(t, original.(map[string]interface{})["range"].(map[string]interface{})["_name"], ranges["_name"])

			// 验证最终 JSON，确保纳秒整数不会在发送时丢失精度。
			encoded, err := json.Marshal(ranges["value"])
			require.NoError(t, err)
			wantJSON, err := json.Marshal(tc.expected)
			require.NoError(t, err)
			require.Equal(t, string(wantJSON), string(encoded))

			repeated, err := query.Source()
			require.NoError(t, err)
			require.Equal(t, source, repeated)
			unchanged, err := tc.query.Source()
			require.NoError(t, err)
			require.Equal(t, original, unchanged)

			// 分页 from 与 range 无关，组合到 SearchSource 后仍需原样保留。
			search, err := elastic.NewSearchSource().From(7).Size(3).Query(query).Source()
			require.NoError(t, err)
			body := search.(map[string]interface{})
			require.Equal(t, 7, body["from"])
			require.Equal(t, 3, body["size"])
			require.Equal(t, source, body["query"])
		})
	}
}
