// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package esquery

import "github.com/olivere/elastic/v7"

type rangeQuery struct {
	query *elastic.RangeQuery
}

// Range 保留客户端的边界构造方式，输出 ES 5.4 至 ES 9 均支持的比较符。
func Range(query *elastic.RangeQuery) elastic.Query {
	return &rangeQuery{query: query}
}

func (q *rangeQuery) Source() (interface{}, error) {
	source, err := q.query.Source()
	if err != nil {
		return nil, err
	}

	// RangeQuery.Source 每次生成独立的 map；只转换当前字段，保留 _name 等元数据。
	// 直接搬运边界值，不通过 JSON 解码，以保留 int64 纳秒时间的精度。
	ranges := source.(map[string]interface{})["range"].(map[string]interface{})
	for _, value := range ranges {
		bounds, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		if lower := bounds["from"]; lower != nil {
			operator := "gt"
			if bounds["include_lower"] == true {
				operator = "gte"
			}
			bounds[operator] = lower
		}
		if upper := bounds["to"]; upper != nil {
			operator := "lt"
			if bounds["include_upper"] == true {
				operator = "lte"
			}
			bounds[operator] = upper
		}
		delete(bounds, "from")
		delete(bounds, "to")
		delete(bounds, "include_lower")
		delete(bounds, "include_upper")
	}
	return source, nil
}
