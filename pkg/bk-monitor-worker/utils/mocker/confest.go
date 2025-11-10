// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mocker

import (
	gomonkey "github.com/agiledragon/gomonkey/v2"
	mapset "github.com/deckarep/golang-set/v2"
	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	dependentredis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
)

func InitTestDBConfig(filePath string) {
	config.FilePath = filePath
	config.InitConfig()
}

func RedisMocker() (*RedisClientMocker, *gomonkey.Patches) {
	redisClient := &RedisClientMocker{
		ZRangeByScoreWithScoresValue: []goRedis.Z{},
		HMGetValue:                   []any{},
		SetMap:                       map[string]mapset.Set[string]{},
		HKeysValue:                   []string{},
	}
	patch := gomonkey.ApplyFunc(redis.GetInstance, func() *redis.Instance {
		return &redis.Instance{
			Client: redisClient,
		}
	})
	return redisClient, patch
}

func DependenceRedisMocker() (*RedisClientMocker, *gomonkey.Patches) {
	redisClient := &RedisClientMocker{
		ZRangeByScoreWithScoresValue: []goRedis.Z{},
		HMGetValue:                   []any{},
		SetMap:                       map[string]mapset.Set[string]{},
		HKeysValue:                   []string{},
	}
	patch := gomonkey.ApplyFunc(dependentredis.GetCacheRedisInstance, func() *dependentredis.Instance {
		return &dependentredis.Instance{
			Client: redisClient,
		}
	})
	return redisClient, patch
}
