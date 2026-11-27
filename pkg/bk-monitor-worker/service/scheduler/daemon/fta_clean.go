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
	"slices"

	redis "github.com/go-redis/redis/v8"

	rdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/broker/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/ftaclean"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

func EnsureFTACleanTasks(ctx context.Context, queues []string) error {
	if !slices.Contains(queues, ftaclean.Queue) || !config.FTAClean.Enabled {
		return nil
	}
	if err := ftaclean.ValidateSettings(); err != nil {
		return err
	}
	return registerFTAShards(ctx, rdb.GetRDB().Client(), config.FTAClean.Shards)
}

func registerFTAShards(ctx context.Context, client redis.UniversalClient, count int) error {
	var members []any
	for shard := 0; shard < count; shard++ {
		payload, err := json.Marshal(ftaclean.Shard{Index: shard})
		if err != nil {
			return err
		}
		member, err := json.Marshal(task.SerializerTask{Kind: ftaclean.Kind, Payload: payload, Options: task.Options{Queue: ftaclean.Queue}})
		if err != nil {
			return err
		}
		members = append(members, string(member))
	}
	key := common.DaemonTaskKey()
	existing, err := client.SMembers(ctx, key).Result()
	if err != nil {
		return err
	}
	pipe := client.TxPipeline()
	for _, member := range existing {
		var previous task.SerializerTask
		if json.Unmarshal([]byte(member), &previous) != nil || previous.Kind != ftaclean.Kind {
			continue
		}
		var shard ftaclean.Shard
		if json.Unmarshal(previous.Payload, &shard) == nil && shard.Index >= count {
			pipe.SRem(ctx, key, member)
		}
	}
	pipe.SAdd(ctx, key, members...)
	_, err = pipe.Exec(ctx)
	return err
}
