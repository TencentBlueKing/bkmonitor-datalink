// MIT License

// Copyright (c) 2021~2022 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package redis

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
	"github.com/pkg/errors"
)

// RedisOptions Redis参数
type RedisOptions struct {
	Mode string `json:"mode" mapstructure:"mode"`

	Addrs    []string `json:"addrs" mapstructure:"addrs"`
	Username string   `json:"username" mapstructure:"username"`
	Password string   `json:"password" mapstructure:"password"`

	SentinelUsername string `json:"sentinel_username" mapstructure:"sentinel_username"`
	SentinelPassword string `json:"sentinel_password" mapstructure:"sentinel_password"`
	MasterName       string `json:"master_name" mapstructure:"master_name"`

	DB int `json:"db" mapstructure:"db"`
}

func GetRedisClient(opt *RedisOptions) (redis.UniversalClient, error) {
	var client redis.UniversalClient
	if opt.Mode == "standalone" {
		client = redis.NewUniversalClient(&redis.UniversalOptions{
			Addrs:    opt.Addrs,
			Username: opt.Username,
			Password: opt.Password,
			DB:       opt.DB,
		})
	} else if opt.Mode == "sentinel" {
		client = redis.NewUniversalClient(&redis.UniversalOptions{
			Addrs:            opt.Addrs,
			SentinelUsername: opt.SentinelUsername,
			SentinelPassword: opt.SentinelPassword,
			MasterName:       opt.MasterName,
			Username:         opt.Username,
			Password:         opt.Password,
			DB:               opt.DB,
		})
	} else if opt.Mode == "cluster" {
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    opt.Addrs,
			Username: opt.Username,
			Password: opt.Password,
		})
	} else {
		return nil, errors.New(fmt.Sprintf("invalid redis mode: %s", opt.Mode))
	}

	ctx := context.Background()
	_, err := client.Ping(ctx).Result()
	if err != nil {
		return nil, errors.Wrap(err, "ping redis failed")
	}

	return client, nil
}
