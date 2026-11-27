// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/ftaclean"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

func TestFTAShardRegistration(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	ctx := context.Background()
	key := common.DaemonTaskKey()
	require.NoError(t, client.SAdd(ctx, key, `{"kind":"unrelated"}`).Err())
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := registerFTAShards(ctx, client, 4); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	require.EqualValues(t, 5, client.SCard(ctx, key).Val())
	require.NoError(t, registerFTAShards(ctx, client, 2))
	members, err := client.SMembers(ctx, key).Result()
	require.NoError(t, err)
	require.Len(t, members, 3)
	var shards []int
	for _, m := range members {
		var task task.SerializerTask
		require.NoError(t, json.Unmarshal([]byte(m), &task))
		if task.Kind == ftaclean.Kind {
			var shard ftaclean.Shard
			require.NoError(t, json.Unmarshal(task.Payload, &shard))
			shards = append(shards, shard.Index)
			require.Equal(t, ftaclean.Queue, task.Options.Queue)
		}
	}
	require.ElementsMatch(t, []int{0, 1}, shards)
}
