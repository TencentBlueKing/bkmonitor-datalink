// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package alarm

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
