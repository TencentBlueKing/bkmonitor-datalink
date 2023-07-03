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

	"github.com/go-redis/redis/v8"
)

const (
	StandAlone = "standalone"
	Sentinel   = "sentinel"
)

type Option struct {
	Mode             string
	Host             string
	Port             int
	Password         string
	MasterName       string
	SentinelAddress  []string
	SentinelPassword string
	Db               int
	DialTimeout      time.Duration
	ReadTimeout      time.Duration
}

// NewRedisClient 初始化 redis 缓存实例
func NewRedisClient(
	ctx context.Context, opt *Option,
) (redis.UniversalClient, error) {
	option := &redis.UniversalOptions{
		MasterName:       opt.MasterName,
		DB:               opt.Db,
		Password:         opt.Password,
		SentinelPassword: opt.SentinelPassword,
		DialTimeout:      opt.DialTimeout,
		ReadTimeout:      opt.ReadTimeout,
	}
	if opt.Mode == Sentinel {
		option.Addrs = opt.SentinelAddress
	} else {
		option.Addrs = []string{fmt.Sprintf("%s:%d", opt.Host, opt.Port)}
		option.MasterName = ""
	}

	cli := redis.NewUniversalClient(option)
	_, err := cli.Ping(ctx).Result()
	if err != nil {
		return nil, err
	}
	return cli, nil
}
