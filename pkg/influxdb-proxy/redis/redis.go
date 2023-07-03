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

	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

var (
	flowLog    *logging.Entry
	moduleName = "redis"
	redisCache goRedis.UniversalClient
)

// ServiceRegister 注册缓存服务
var ServiceRegister = func() error {
	if c := GetRedis(); c != nil {
		c.Close()
	}
	return loadRedis()
}

// 初始化缓存实例
func loadRedis() error {
	var err error
	mode := viper.GetString(ModeConfigPath)
	host := viper.GetString(HostConfigPath)
	port := viper.GetInt(PortConfigPath)
	password := viper.GetString(PasswordConfigPath)

	masterName := viper.GetString(MasterNameConfigPath)
	sentinelAddress := viper.GetStringSlice(SentinelAddressConfigPath)
	sentinelPassword := viper.GetString(SentinelPasswordConfigPath)
	db := viper.GetInt(DataBaseConfigPath)

	dialTimeout := viper.GetDuration(DialTimeoutConfigPath)
	readTimeout := viper.GetDuration(ReadTimeoutConfigPath)

	ctx := context.Background()
	redisCache, err = redis.NewRedisClient(
		ctx, &redis.Option{
			Mode:             mode,
			Host:             host,
			Port:             port,
			Password:         password,
			MasterName:       masterName,
			SentinelAddress:  sentinelAddress,
			SentinelPassword: sentinelPassword,
			Db:               db,
			DialTimeout:      dialTimeout,
			ReadTimeout:      readTimeout,
		},
	)
	return err
}

func GetRedis() goRedis.UniversalClient {
	return redisCache
}

// 初始化日志配置
func init() {
	flowLog = logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
}
