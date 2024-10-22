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
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
)

func SetQueryReference(ctx context.Context, reference QueryReference) {
	md.set(ctx, QueryReferenceKey, reference)
	return
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
func (q *Query) UUID(prefix string) string {
	str := fmt.Sprintf("%s%s%s%s%s%s%s%s%s%s",
		prefix, q.SourceType, q.ClusterID, q.ClusterName, q.TagsKey,
		q.RetentionPolicy, q.DB, q.Measurement, q.Field, q.Condition,
	)
	return str
}

// MetricLabels 获取真实指标名称
func (q *Query) MetricLabels(ctx context.Context) *prompb.Label {
	if GetQueryParams(ctx).IsReference {
		return nil
	}

	var (
		metrics    []string
		encodeFunc = GetPromDataFormat(ctx).EncodeFunc()
	)
	if q.DataSource != "" {
		metrics = append(metrics, q.DataSource)
	}
	for _, n := range strings.Split(q.TableID, ".") {
		metrics = append(metrics, n)
	}
	if q.Field != "" {
		field := q.Field
		if encodeFunc != nil {
			field = encodeFunc(field)
		}
		metrics = append(metrics, field)
	}
	return &prompb.Label{
		Name:  labels.MetricName,
		Value: strings.Join(metrics, ":"),
	}
}
