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
	"sync"
	"time"

	"github.com/avast/retry-go"
	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

type Instance struct {
	Ctx    context.Context
	Client goRedis.UniversalClient
}

var (
	redisInstance *Instance
	redisOnce     sync.Once
)

// GetInstance 获取存储类型的 redis
func GetInstance() *Instance {
	if redisInstance != nil {
		return redisInstance
	}
	redisConfig := config.Config.Store.Redis
	redisOnce.Do(func() {
		opt := redisUtils.Option{
			Mode:             redisConfig.Mode,
			Host:             redisConfig.Host,
			Port:             redisConfig.Port,
			SentinelAddress:  redisConfig.Address,
			MasterName:       redisConfig.MasterName,
			SentinelPassword: redisConfig.SentinelPassword,
			Password:         redisConfig.Password,
			Db:               redisConfig.Database,
			DialTimeout:      redisConfig.DialTimeout,
			ReadTimeout:      redisConfig.ReadTimeout,
		}
		redisInstance = NewClient(&opt)
	})
	return redisInstance
}

// NewClient get a redis instance
func NewClient(opt *redisUtils.Option) *Instance {
	ctx := context.TODO()
	var client goRedis.UniversalClient
	var err error

	// 添加重试
	err = retry.Do(
		func() error {
			client, err = redisUtils.NewRedisClient(ctx, opt)
			if err != nil {
				logger.Errorf("new redis client, error: %s, opt: %#v", err, opt)
				return err
			}
			return nil
		},
		retry.Attempts(3),
		retry.Delay(2*time.Second),
	)
	if err != nil {
		logger.Fatalf("failed to create redis storage client, error: %s", err)
	}

	return &Instance{Ctx: ctx, Client: client}
}

// Open new a instance
func (r *Instance) Open() error {
	return nil
}

// Put put a key-val
func (r *Instance) Put(key, val string, expiration time.Duration) error {
	if err := r.Client.Set(r.Ctx, key, val, expiration).Err(); err != nil {
		logger.Debugf("put redis error, key: %s, val: %s, err: %v", key, val, err)
		return err
	}
	return nil
}

// Get get a val from key
func (r *Instance) Get(key string) ([]byte, error) {
	data, err := r.Client.Get(r.Ctx, key).Bytes()
	if err != nil {
		logger.Debugf("get redis key: %s error, %v", key, err)
		return nil, err
	}
	return data, nil
}

// Delete delete a key
func (r *Instance) Delete(key string) error {
	exist, err := r.Client.Exists(r.Ctx, key).Result()
	if err != nil {
		logger.Debugf("check redis key: %s exist error, %v", key, err)
		return err
	}
	if exist == 0 {
		logger.Debugf("key: %s not exist from redis", key)
		return nil
	}
	if err := r.Client.Del(r.Ctx, key).Err(); err != nil {
		logger.Debugf("delete key: %s error, %v", key, err)
		return err
	}
	return nil
}

// Close close connection
func (r *Instance) Close() error {
	if r.Client != nil {
		return r.Client.Close()
	}
	return nil
}

// Publish message
func (r *Instance) Publish(channelName string, msg interface{}) error {
	if err := r.Client.Publish(r.Ctx, channelName, msg).Err(); err != nil {
		return err
	}
	return nil
}

// Subscribe subscribe channel from redis
func (r *Instance) Subscribe(channelNames ...string) *goRedis.PubSub {
	return r.Client.Subscribe(r.Ctx, channelNames...)
}
