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

	"github.com/prometheus/prometheus/model/labels"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

// SetQueries 写入查询扩展，因为有多个查询所以除了常量前缀之外，还需要指定 k（该值一般为指标名，用于对应）
func SetQueries(ctx context.Context, queries *Queries) error {
	queries.ctx = ctx
	md.set(ctx, QueriesKey, queries)
	return nil
}

// GetQueries 如果查询异常，则直接报错
func GetQueries(ctx context.Context) *Queries {
	r, ok := md.get(ctx, QueriesKey)
	if ok {
		if v, ok := r.(*Queries); ok {
			return v
		}
	}
	return nil
}

// String 输出字符串
func (q *Queries) String() string {
	res, _ := json.Marshal(q)
	return string(res)
}

// DirectlyClusterID 获取直查的集群ID
func (q *Queries) DirectlyClusterID() string {
	return q.directlyClusterID
}

// DirectlyMetricName 获取直查的 metricName
func (q *Queries) DirectlyMetricName() map[string]string {
	return q.directlyMetricName
}

func (q *Queries) DirectlyResultTable() map[string][]string {
	return q.directlyResultTable
}

// DirectlyLabelsMatcher 获取额外的查询条件
func (q *Queries) DirectlyLabelsMatcher() map[string][]*labels.Matcher {
	return q.directlyLabelsMatcher
}
