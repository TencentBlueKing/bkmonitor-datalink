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

type closeAwareQueryInstance struct {
	tsdb.DefaultInstance
	closed int
}

func (i *closeAwareQueryInstance) DirectQueryRangeWithClose(
	context.Context,
	string,
	time.Time,
	time.Time,
	time.Duration,
) (promPromql.Matrix, bool, func(), error) {
	return promPromql.Matrix{{Points: []promPromql.Point{{T: 1, V: 2}}}}, false, func() {
		i.closed++
	}, nil
}

func TestExecuteQueryWithCloseTransfersOwnershipOnce(t *testing.T) {
	instance := &closeAwareQueryInstance{}
	start := time.Unix(0, 0)

	result, partial, release, err := executeQueryWithClose(
		context.Background(),
		instance,
		"a",
		start,
		start.Add(time.Minute),
		time.Minute,
		false,
	)

	require.NoError(t, err)
	require.False(t, partial)
	require.Equal(t, int64(1), result.(promPromql.Matrix)[0].Points[0].T)
	require.Zero(t, instance.closed)

	release()
	release()
	require.Equal(t, 1, instance.closed)
}

func TestPromQLResultPointUsageDistinguishesLengthAndCapacity(t *testing.T) {
	matrixPoints := make([]promPromql.Point, 1, 8)
	queryType, points, capacity, ok := promQLResultPointUsage(promPromql.Matrix{{Points: matrixPoints}})
	require.True(t, ok)
	require.Equal(t, "range", queryType)
	require.Equal(t, 1, points)
	require.Equal(t, 8, capacity)

	vector := make(promPromql.Vector, 1, 4)
	queryType, points, capacity, ok = promQLResultPointUsage(vector)
	require.True(t, ok)
	require.Equal(t, "instant", queryType)
	require.Equal(t, 1, points)
	require.Equal(t, 4, capacity)

	_, _, _, ok = promQLResultPointUsage(struct{}{})
	require.False(t, ok)
}
