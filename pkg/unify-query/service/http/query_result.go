// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"sync"
	"time"

	promPromql "github.com/prometheus/prometheus/promql"
	"go.opentelemetry.io/otel/attribute"
	otelTrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

type ownedQueryResult struct {
	value   any
	release func()
}

func executeQueryWithClose(
	ctx context.Context,
	instance tsdb.Instance,
	statement string,
	start, end time.Time,
	step time.Duration,
	instant bool,
) (result any, partial bool, release func(), err error) {
	var closeResult func()
	if instant {
		if owned, ok := instance.(tsdb.InstantQueryWithClose); ok {
			var vector promPromql.Vector
			vector, partial, closeResult, err = owned.DirectQueryWithClose(ctx, statement, end)
			result = vector
		} else if statusAware, ok := instance.(tsdb.InstantQueryWithPartial); ok {
			var vector promPromql.Vector
			vector, partial, err = statusAware.DirectQueryWithPartial(ctx, statement, end)
			result = vector
		} else {
			result, err = instance.DirectQuery(ctx, statement, end)
		}
	} else if owned, ok := instance.(tsdb.RangeQueryWithClose); ok {
		var matrix promPromql.Matrix
		matrix, partial, closeResult, err = owned.DirectQueryRangeWithClose(ctx, statement, start, end, step)
		result = matrix
	} else {
		result, partial, err = instance.DirectQueryRange(ctx, statement, start, end, step)
	}

	var once sync.Once
	release = func() {
		once.Do(func() {
			if closeResult != nil {
				closeResult()
			}
		})
	}
	if err != nil {
		release()
		return nil, partial, release, err
	}
	observePromQLResultCapacity(ctx, result, instant)
	return result, partial, release, nil
}

func observePromQLResultCapacity(ctx context.Context, result any, instant bool) {
	queryType, points, capacity, ok := promQLResultPointUsage(result)
	if !ok {
		return
	}
	if instant {
		queryType = "instant"
	}
	span := otelTrace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int("query-cost.result-points", points),
		attribute.Int("query-cost.result-point-capacity", capacity),
	)
	metric.QueryResultCapacityObserve(ctx, queryType, points, capacity)
}

func promQLResultPointUsage(result any) (queryType string, points, capacity int, ok bool) {
	queryType = "range"
	switch value := result.(type) {
	case promPromql.Matrix:
		for _, series := range value {
			points += len(series.Points)
			capacity += cap(series.Points)
		}
	case promPromql.Vector:
		queryType = "instant"
		points = len(value)
		capacity = cap(value)
	default:
		return "", 0, 0, false
	}
	return queryType, points, capacity, true
}
