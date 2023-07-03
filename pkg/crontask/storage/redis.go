// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/crontask/utils/redis"
)

const (
	redisTypePath        = "redis.type"
	redisMasterNamePath  = "redis.master_name"
	redisAddressPath     = "redis.address"
	redisUsernamePath    = "redis.username"
	redisPasswordPath    = "redis.password"
	redisDatabasePath    = "redis.database"
	redisDialTimeoutPath = "redis.dial_timeout"
	redisReadTimeoutPath = "redis.read_timeout"
)

func init() {
	viper.SetDefault(redisMasterNamePath, "")
	viper.SetDefault(redisAddressPath, []string{"127.0.0.1:6379"})
	viper.SetDefault(redisUsernamePath, "root")
	viper.SetDefault(redisPasswordPath, "")
	viper.SetDefault(redisDatabasePath, 0)
	viper.SetDefault(redisDialTimeoutPath, time.Second*10)
	viper.SetDefault(redisReadTimeoutPath, time.Second*10)
}

// RedisSession
type RedisSession struct {
	Client redis.UniversalClient
}

// Open new a redis universal client
func (r *RedisSession) Open() error {
	r.Client = redisUtils.NewClient(
		viper.GetString(redisMasterNamePath),
		viper.GetStringSlice(redisAddressPath),
		viper.GetString(redisUsernamePath),
		viper.GetString(redisPasswordPath),
		viper.GetInt(redisDatabasePath),
		viper.GetDuration(redisDialTimeoutPath),
		viper.GetDuration(redisReadTimeoutPath),
	)
	return nil
}

// Close close connection
func (r *RedisSession) Close() error {
	return r.Client.Close()
}
