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
	"errors"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResourceBudgetSharesAccountingAcrossGoroutines(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	budget := NewResourceBudget(ResourceBudgetLimits{
		MaxSeries: 100,
		MaxPoints: 100,
		MaxBytes:  1000,
	}, cancel)
	ctx = WithResourceBudget(ctx, budget)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, GetResourceBudget(ctx).Reserve(1, 2, 3))
		}()
	}
	wg.Wait()

	snapshot := budget.Snapshot()
	require.Equal(t, int64(10), snapshot.Usage.Series)
	require.Equal(t, int64(20), snapshot.Usage.Points)
	require.Equal(t, int64(30), snapshot.Usage.Bytes)
	require.Nil(t, snapshot.Rejected)
}

func TestResourceBudgetRejectsWithoutCommittingFailedIncrementAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	budget := NewResourceBudget(ResourceBudgetLimits{MaxPoints: 2}, cancel)

	require.NoError(t, budget.Reserve(0, 2, 0))
	err := budget.Reserve(0, 1, 0)
	var limitErr *ResourceBudgetError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, ResourcePoints, limitErr.Resource)
	require.Equal(t, int64(2), limitErr.Limit)
	require.Equal(t, int64(3), limitErr.Attempted)
	require.True(t, IsResourceBudgetError(err))
	require.Equal(t, int64(2), budget.Snapshot().Usage.Points)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestResourceBudgetEvalCapacitySaturatesOnOverflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	budget := NewResourceBudget(ResourceBudgetLimits{MaxEvalCapacityBytes: math.MaxInt64 - 1}, cancel)

	err := budget.ReserveEvalCapacity(math.MaxInt64, math.MaxInt64, PromQLPointBytes)
	var limitErr *ResourceBudgetError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, ResourceEvalCapacityBytes, limitErr.Resource)
	require.Equal(t, int64(math.MaxInt64), limitErr.Attempted)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestResourceBudgetCloseReturnsUsageAndReleasesCounters(t *testing.T) {
	budget := NewResourceBudget(ResourceBudgetLimits{
		MaxResponseBytes:     100,
		MaxEvalCapacityBytes: 1000,
	}, nil)
	require.NoError(t, budget.ReserveResponseBytes(25))
	require.NoError(t, budget.ReserveEvalCapacity(2, 10, PromQLPointBytes))

	snapshot := budget.Close()
	require.Equal(t, int64(25), snapshot.Usage.ResponseBytes)
	require.Equal(t, int64(480), snapshot.Usage.EvalCapacityBytes)
	require.Equal(t, int64(2), snapshot.Usage.EvalSeries)
	require.Equal(t, int64(10), snapshot.Usage.MaxEvalSteps)
	require.Equal(t, ResourceBudgetUsage{}, budget.Snapshot().Usage)
}

func TestResourceBudgetErrorSurvivesWrapping(t *testing.T) {
	source := &ResourceBudgetError{Resource: ResourceSeries, Limit: 2, Attempted: 3}
	wrapped := errors.Join(errors.New("query failed"), source)
	require.True(t, IsResourceBudgetError(wrapped))
}
