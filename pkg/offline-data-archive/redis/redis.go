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
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	utilRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

func setDefault() {
	viper.SetDefault(ModeConfigPath, "standalone")
	viper.SetDefault(HostConfigPath, "127.0.0.1")
	viper.SetDefault(PortConfigPath, 6379)
	viper.SetDefault(PasswordConfigPath, "")
	viper.SetDefault(DataBaseConfigPath, 0)

	viper.SetDefault(MasterNameConfigPath, "")
	viper.SetDefault(SentinelAddressConfigPath, []string{})
	viper.SetDefault(SentinelPasswordConfigPath, "")

	viper.SetDefault(DialTimeoutConfigPath, time.Second)
	viper.SetDefault(ReadTimeoutConfigPath, time.Second*30)
	viper.SetDefault(ServiceNameConfigPath, "bkmonitorv3:archive")
}

func NewRedis(ctx context.Context) (redis.UniversalClient, error) {
	setDefault()

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

	return utilRedis.NewRedisClient(
		ctx, &utilRedis.Option{
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
}
