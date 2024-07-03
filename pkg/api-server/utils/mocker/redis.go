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
	"context"
	"strconv"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/alicebob/miniredis/v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/store/redis"
	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

func RedisMocker() (*redis.Instance, *gomonkey.Patches) {
	// 启动 server
	s, _ := miniredis.Run()

	ctx := context.TODO()

	// 构建 client
	port, _ := strconv.Atoi(s.Port())
	RedisClient, _ := redisUtils.NewRedisClient(
		ctx,
		&redisUtils.Option{
			Mode: redisUtils.StandAlone,
			Host: s.Host(),
			Port: port,
			Db:   0,
		},
	)
	// 组装 rs
	rs := &redis.Instance{
		Client: RedisClient,
		Ctx:    ctx,
	}
	patch := gomonkey.ApplyFunc(redis.GetInstance, func() *redis.Instance {
		return &redis.Instance{
			Ctx:    ctx,
			Client: RedisClient,
		}
	})
	return rs, patch
}
