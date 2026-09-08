// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

func TestReserveQueryResultEvalCapacityUsesActualSeriesAndEvaluationSteps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	budget := metadata.NewResourceBudget(metadata.ResourceBudgetLimits{
		MaxEvalCapacityBytes: 3 * 10 * metadata.PromQLPointBytes,
	}, cancel)
	budget.SetEvaluationSteps(10)
	ctx = metadata.WithResourceBudget(ctx, budget)

	require.NoError(t, reserveQueryResultEvalCapacity(ctx, 3))
	err := reserveQueryResultEvalCapacity(ctx, 1)
	var limitErr *metadata.ResourceBudgetError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, metadata.ResourceEvalCapacityBytes, limitErr.Resource)
	require.Equal(t, int64(4*10)*metadata.PromQLPointBytes, limitErr.Attempted)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestSparseSeriesCapacityModelUsesRangeStepsWithoutAllocatingPointSlices(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		series int64
		days   int64
	}{
		{name: "1k-series-1d", series: 1000, days: 1},
		{name: "1k-series-30d", series: 1000, days: 30},
		{name: "10k-series-1d", series: 10000, days: 1},
		{name: "10k-series-30d", series: 10000, days: 30},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			steps := testCase.days*24*60 + 1
			budget := metadata.NewResourceBudget(metadata.ResourceBudgetLimits{}, nil)
			budget.SetEvaluationSteps(steps)
			ctx := metadata.WithResourceBudget(context.Background(), budget)

			require.NoError(t, reserveQueryResultEvalCapacity(ctx, int(testCase.series)))
			snapshot := budget.Snapshot()
			require.Equal(t, testCase.series*steps*metadata.PromQLPointBytes, snapshot.Usage.EvalCapacityBytes)
			require.Equal(t, steps, snapshot.Usage.MaxEvalSteps)
		})
	}
}

func BenchmarkFormatSparseSeries(b *testing.B) {
	metadata.InitMetadata()
	for _, seriesCount := range []int{1000, 10000} {
		for _, dynamicLabels := range []bool{false, true} {
			name := fmt.Sprintf("series=%d/dynamic_labels=%t", seriesCount, dynamicLabels)
			rows := sparseSeriesRows(seriesCount, dynamicLabels)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					ctx := metadata.InitHashID(context.Background())
					budget := metadata.NewResourceBudget(metadata.ResourceBudgetLimits{}, nil)
					ctx = metadata.WithResourceBudget(ctx, budget)
					factory := NewQueryFactory(ctx, &metadata.Query{Field: "metric_value2"}).
						WithRangeTime(time.Unix(0, 0), time.Unix(int64(seriesCount*60), 0))
					result, err := factory.FormatDataToQueryResult(ctx, rows)
					if err != nil {
						b.Fatal(err)
					}
					expectedSeries := 1
					if dynamicLabels {
						expectedSeries = seriesCount
					}
					if len(result.Timeseries) != expectedSeries {
						b.Fatalf("unexpected series: got=%d want=%d", len(result.Timeseries), expectedSeries)
					}
				}
			})
		}
	}
}

func sparseSeriesRows(count int, dynamicLabels bool) []map[string]any {
	rows := make([]map[string]any, 0, count)
	for i := 0; i < count; i++ {
		dataTime := "stable"
		if dynamicLabels {
			dataTime = fmt.Sprintf("minute-%d", i)
		}
		rows = append(rows, map[string]any{
			sql_expr.TimeStamp: int64(i * 60 * 1000),
			sql_expr.Value:     float64(i),
			"url":              "example",
			"data_time":        dataTime,
		})
	}
	return rows
}
