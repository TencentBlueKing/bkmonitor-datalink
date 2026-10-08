// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
)

func TestRecoverExpiredOnlyRequeuesExpiredActiveTasks(t *testing.T) {
	server, err := miniredis.Run()
	require.NoError(t, err)
	defer server.Close()

	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	broker := &RDB{client: client}
	ctx := context.Background()
	queue := "default"
	now := time.Now()

	for _, item := range []struct {
		id    string
		state string
		lease time.Time
	}{
		{id: "expired", state: "active", lease: now.Add(-time.Hour)},
		{id: "running", state: "active", lease: now.Add(time.Minute)},
		{id: "stale", state: "completed", lease: now.Add(-time.Hour)},
	} {
		require.NoError(t, client.HSet(ctx, common.TaskKey(queue, item.id), "state", item.state).Err())
		require.NoError(t, client.ZAdd(ctx, common.LeaseKey(queue), &redis.Z{
			Score: float64(item.lease.Unix()), Member: item.id,
		}).Err())
	}
	require.NoError(t, client.LPush(ctx, common.ActiveKey(queue), "expired", "running").Err())

	count, err := broker.RecoverExpired(now, queue)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, []string{"expired"}, client.LRange(ctx, common.PendingKey(queue), 0, -1).Val())
	require.Equal(t, []string{"running"}, client.LRange(ctx, common.ActiveKey(queue), 0, -1).Val())
	require.Equal(t, "pending", client.HGet(ctx, common.TaskKey(queue, "expired"), "state").Val())
	require.Equal(t, "active", client.HGet(ctx, common.TaskKey(queue, "running"), "state").Val())
	require.Equal(t, "completed", client.HGet(ctx, common.TaskKey(queue, "stale"), "state").Val())
	require.ErrorIs(t, client.ZScore(ctx, common.LeaseKey(queue), "expired").Err(), redis.Nil)

	count, err = broker.RecoverExpired(now, queue)
	require.NoError(t, err)
	require.Zero(t, count)
}
