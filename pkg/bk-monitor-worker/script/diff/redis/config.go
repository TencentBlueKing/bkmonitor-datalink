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
	"fmt"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"

	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

const (
	SrcRedisModePath                   = "diffRedis.srcRedis.mod"
	SrcRedisSentinelMasterNamePath     = "diffRedis.srcRedis.sentinel.masterName"
	SrcRedisSentinelMasterAddressPath  = "diffRedis.srcRedis.sentinel.address"
	SrcRedisSentinelMasterPasswordPath = "diffRedis.srcRedis.sentinel.password"
	SrcRedisStandaloneHostPath         = "diffRedis.srcRedis.standalone.host"
	SrcRedisStandalonePortPath         = "diffRedis.srcRedis.standalone.port"
	SrcRedisStandalonePasswordPath     = "diffRedis.srcRedis.standalone.port"
	SrcRedisDbPath                     = "diffRedis.srcRedis.db"
	SrcRedisDialTimeoutPath            = "diffRedis.srcRedis.dialTimeout"
	SrcRedisReadTimeoutPath            = "diffRedis.srcRedis.readTimeout"

	BypassRedisModePath                   = "diffRedis.bypassRedis.mod"
	BypassRedisSentinelMasterNamePath     = "diffRedis.bypassRedis.sentinel.masterName"
	BypassRedisSentinelMasterAddressPath  = "diffRedis.bypassRedis.sentinel.address"
	BypassRedisSentinelMasterPasswordPath = "diffRedis.bypassRedis.sentinel.password"
	BypassRedisStandaloneHostPath         = "diffRedis.bypassRedis.standalone.host"
	BypassRedisStandalonePortPath         = "diffRedis.bypassRedis.standalone.port"
	BypassRedisStandalonePasswordPath     = "diffRedis.bypassRedis.standalone.port"
	BypassRedisDbPath                     = "diffRedis.bypassRedis.db"
	BypassRedisDialTimeoutPath            = "diffRedis.bypassRedis.dialTimeout"
	BypassRedisReadTimeoutPath            = "diffRedis.bypassRedis.readTimeout"
)

func InitRedisDiffConfig() {
	// 配置默认值
	viper.SetDefault(SrcRedisModePath, "standalone")
	viper.SetDefault(SrcRedisSentinelMasterNamePath, "standalone")
	viper.SetDefault(SrcRedisSentinelMasterAddressPath, []string{"127.0.0.1"})
	viper.SetDefault(SrcRedisSentinelMasterPasswordPath, "")
	viper.SetDefault(SrcRedisStandaloneHostPath, "127.0.0.1")
	viper.SetDefault(SrcRedisStandalonePortPath, 6379)
	viper.SetDefault(SrcRedisStandalonePasswordPath, "")
	viper.SetDefault(SrcRedisDbPath, 0)
	viper.SetDefault(SrcRedisDialTimeoutPath, 10*time.Second)
	viper.SetDefault(SrcRedisReadTimeoutPath, 10*time.Second)

	viper.SetDefault(BypassRedisModePath, "standalone")
	viper.SetDefault(BypassRedisSentinelMasterNamePath, "standalone")
	viper.SetDefault(BypassRedisSentinelMasterAddressPath, []string{"127.0.0.1"})
	viper.SetDefault(BypassRedisSentinelMasterPasswordPath, "")
	viper.SetDefault(BypassRedisStandaloneHostPath, "127.0.0.1")
	viper.SetDefault(BypassRedisStandalonePortPath, 6379)
	viper.SetDefault(BypassRedisStandalonePasswordPath, "")
	viper.SetDefault(BypassRedisDbPath, 0)
	viper.SetDefault(BypassRedisDialTimeoutPath, 10*time.Second)
	viper.SetDefault(BypassRedisReadTimeoutPath, 10*time.Second)

	// 初始化配置
	DiffConfig.SrcConfig = GetRedisClientOpt("srcRedis")
	DiffConfig.BypassConfig = GetRedisClientOpt("bypassRedis")
}

var DiffConfig Config

type Config struct {
	KeyType      string
	SrcKey       string
	BypassKey    string
	SrcConfig    *redisUtils.Option
	BypassConfig *redisUtils.Option
}

func GetRDSClient(cfg *redisUtils.Option) (goRedis.UniversalClient, error) {
	client, err := redisUtils.NewRedisClient(context.TODO(), cfg)
	if err != nil {
		return nil, err
	}
	return client, err
}

func GetRedisClientOpt(redisName string) *redisUtils.Option {
	return &redisUtils.Option{
		Mode:             viper.GetString(fmt.Sprintf("diffRedis.%s.mod", redisName)),
		Host:             viper.GetString(fmt.Sprintf("diffRedis.%s.standalone.host", redisName)),
		Port:             viper.GetInt(fmt.Sprintf("diffRedis.%s.standalone.port", redisName)),
		SentinelAddress:  viper.GetStringSlice(fmt.Sprintf("diffRedis.%s.sentinel.address", redisName)),
		MasterName:       viper.GetString(fmt.Sprintf("diffRedis.%s.sentinel.masterName", redisName)),
		SentinelPassword: viper.GetString(fmt.Sprintf("diffRedis.%s.sentinel.password", redisName)),
		Password:         viper.GetString(fmt.Sprintf("diffRedis.%s.standalone.password", redisName)),
		Db:               viper.GetInt(fmt.Sprintf("diffRedis.%s.db", redisName)),
		DialTimeout:      viper.GetDuration(fmt.Sprintf("diffRedis.%s.dialTimeout", redisName)),
		ReadTimeout:      viper.GetDuration(fmt.Sprintf("diffRedis.%s.readTimeout", redisName)),
	}
}
