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
)

// QueryParams 查询信息
type QueryParams struct {
	ctx context.Context

	Start int64
	End   int64

	DataSource  map[string]struct{}
	IsReference bool
	IsSkipK8s   bool
}

func (q *QueryParams) SetIsSkipK8s(isSkipK8s bool) *QueryParams {
	q.IsSkipK8s = isSkipK8s
	q.set()
	return q
}

func (q *QueryParams) SetIsReference(isReference bool) *QueryParams {
	q.IsReference = isReference
	q.set()
	return q
}

func (q *QueryParams) SetDataSource(ds string) *QueryParams {
	q.DataSource[ds] = struct{}{}
	q.set()
	return q
}

func (q *QueryParams) SetTime(start, end int64) *QueryParams {
	q.Start = start
	q.End = end
	q.set()
	return q
}

func (q *QueryParams) set() {
	if md != nil {
		md.set(q.ctx, QueryParamsKey, q)
	}
}

// GetQueryParams 读取
func GetQueryParams(ctx context.Context) *QueryParams {
	if md != nil {
		r, ok := md.get(ctx, QueryParamsKey)
		if ok {
			if v, ok := r.(*QueryParams); ok {
				return v
			}
		}
	}
	return &QueryParams{
		ctx:        ctx,
		DataSource: make(map[string]struct{}),
	}
}
