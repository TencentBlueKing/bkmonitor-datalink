// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/prometheus/storage"
)

const (
	MIN   = "min"
	MAX   = "max"
	SUM   = "sum"
	COUNT = "count"
	LAST  = "last"
	MEAN  = "mean"
	AVG   = "avg"

	MinOT   = "min_over_time"
	MaxOT   = "max_over_time"
	SumOT   = "sum_over_time"
	CountOT = "count_over_time"
	LastOT  = "last_over_time"
	AvgOT   = "avg_over_time"
)

var domSampledFunc = map[string]string{
	MIN + MinOT:   MIN,
	MAX + MaxOT:   MAX,
	SUM + SumOT:   SUM,
	AVG + AvgOT:   MEAN,
	MEAN + AvgOT:  MEAN,
	SUM + CountOT: COUNT,
}

func SetQueryReference(ctx context.Context, reference QueryReference) error {
	md.set(ctx, QueryReferenceKey, reference)
	return nil
}

func GetQueryReference(ctx context.Context) QueryReference {
	r, ok := md.get(ctx, QueryReferenceKey)
	if ok {
		if v, ok := r.(QueryReference); ok {
			return v
		}
	}
	return nil
}

// UUID 获取唯一性
func (q Query) UUID(prefix string) string {
	str := fmt.Sprintf("%s%s%s%s%s%s%s%s%s%s",
		prefix, q.SourceType, q.ClusterID, q.ClusterName, q.TagsKey,
		q.RetentionPolicy, q.DB, q.Measurement, q.Field, q.Condition,
	)
	return str
}

func (q Query) GetDownSampleFunc(hints *storage.SelectHints) (string, time.Duration, []string) {
	var (
		dims   []string
		window = time.Duration(hints.Range * 1e6)
		step   = time.Duration(hints.Step * 1e6)

		grouping time.Duration
	)

	// 为了保持数据的精度，如果 step 小于 window 则使用 step 的聚合，否则使用 window
	if step < window {
		grouping = step
	} else {
		grouping = window
	}

	if len(q.AggregateMethodList) > 0 {
		method := q.AggregateMethodList[0]
		if method.Without {
			return "", grouping, dims
		}

		if name, ok := domSampledFunc[method.Name+hints.Func]; ok {
			return name, grouping, method.Dimensions
		}
	}

	return "", grouping, dims
}
