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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
)

// QueryParams 查询信息
type QueryParams struct {
	ctx context.Context

	AlignStart time.Time
	Start      time.Time
	End        time.Time
	Step       time.Duration
	TimeUnit   string
	Timezone   string

	StorageType *set.Set[string]

	IsReference bool
	IsSkipK8s   bool
}

func (q *QueryParams) SetIsSkipK8s(isSkipK8s bool) *QueryParams {
	q.IsSkipK8s = isSkipK8s
	return q
}

func (q *QueryParams) SetIsReference(isReference bool) *QueryParams {
	q.IsReference = isReference
	return q
}

func (q *QueryParams) IsDirectQuery() bool {
	return q.StorageType.Existed(VictoriaMetricsStorageType)
}

func (q *QueryParams) SetStorageType(ds string) *QueryParams {
	q.StorageType.Add(ds)
	return q
}

func (q *QueryParams) SetTime(alignStart, start, end time.Time, step time.Duration, unit, timezone string) *QueryParams {
	q.AlignStart = alignStart
	q.Start = start
	q.End = end
	q.Step = step
	q.TimeUnit = unit
	q.Timezone = timezone
	return q
}

func (q *QueryParams) set() *QueryParams {
	if md != nil {
		md.set(q.ctx, QueryParamsKey, q)
	}
	return q
}

// GetQueryParams 读取
func GetQueryParams(ctx context.Context) *QueryParams {
	if md != nil {
		r, ok := md.get(ctx, QueryParamsKey)
		if ok {
			if qp, ok := r.(*QueryParams); ok {
				return qp
			}
		}
	}

	return (&QueryParams{
		ctx:         ctx,
		StorageType: set.New[string](),
	}).set()
}
