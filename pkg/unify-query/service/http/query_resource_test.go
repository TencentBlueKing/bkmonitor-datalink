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
	"testing"
	"time"

	promPromql "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestEvaluationStepPlanCountsEachASTSelectorBranch(t *testing.T) {
	start := time.Unix(0, 0)
	end := start.Add(30 * 24 * time.Hour)
	plan, err := evaluationStepPlan(
		"sum(count_over_time(a[1d])) + sum(count_over_time(a[5m])) + b",
		start,
		end,
		time.Minute,
		false,
	)

	require.NoError(t, err)
	const rangeSteps int64 = 30*24*60 + 1
	require.Equal(t, 2*rangeSteps, plan["a"])
	require.Equal(t, rangeSteps, plan["b"])
}

func TestEvaluationStepPlanUsesSubqueryStep(t *testing.T) {
	at := time.Unix(0, 0)
	plan, err := evaluationStepPlan("a[1d:1m]", at, at, 5*time.Minute, true)

	require.NoError(t, err)
	require.Equal(t, int64(24*60+1), plan["a"])
}

type ownedQueryInstance struct {
	tsdb.DefaultInstance
	closed int
}

func (i *ownedQueryInstance) DirectQueryRangeWithClose(
	context.Context,
	string,
	time.Time,
	time.Time,
	time.Duration,
) (promPromql.Matrix, bool, func(), error) {
	return promPromql.Matrix{}, false, func() { i.closed++ }, nil
}

func TestExecuteQueryWithResourceBudgetTransfersCloseOwnership(t *testing.T) {
	instance := &ownedQueryInstance{}
	start := time.Unix(0, 0)
	_, _, release, err := executeQueryWithResourceBudget(
		context.Background(),
		nil,
		instance,
		"a",
		start,
		start.Add(time.Minute),
		time.Minute,
		false,
	)

	require.NoError(t, err)
	require.Zero(t, instance.closed)
	release()
	release()
	require.Equal(t, 1, instance.closed)
}
