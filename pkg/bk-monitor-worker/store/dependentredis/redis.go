// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dependentredis

import (
	"context"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

type Instance struct {
	ctx    context.Context
	Client goRedis.UniversalClient
}

var instance *Instance

func NewInstance(ctx context.Context) (*Instance, error) {
	client, err := redisUtils.NewRedisClient(
		ctx,
		&redisUtils.Option{
			Mode:             config.StorageDependentRedisMode,
			Host:             config.StorageDependentRedisStandaloneHost,
			Port:             config.StorageDependentRedisStandalonePort,
			SentinelAddress:  config.StorageDependentRedisSentinelAddress,
			MasterName:       config.StorageDependentRedisSentinelMasterName,
			SentinelPassword: config.StorageDependentRedisSentinelPassword,
			Password:         config.StorageDependentRedisStandalonePassword,
			Db:               config.StorageDependentRedisDatabase,
			DialTimeout:      time.Duration(config.StorageDependentRedisDialTimeout) * time.Second,
			ReadTimeout:      time.Duration(config.StorageDependentRedisReadTimeout) * time.Second,
		},
	)
	if err != nil {
		return nil, err
	}
	return &Instance{ctx: ctx, Client: client}, nil
}

// GetInstance get a redis instance
func GetInstance(ctx context.Context) (*Instance, error) {
	if instance != nil {
		return instance, nil
	}
	return NewInstance(ctx)
}

// Open new a instance
func (r *Instance) Open() error {
	return nil
}

// Put put a key-val
func (r *Instance) Put(key, val string, expiration time.Duration) error {
	if err := r.Client.Set(r.ctx, key, val, expiration).Err(); err != nil {
		logger.Errorf("put redis error, key: %s, val: %s, err: %v", key, val, err)
		return err
	}
	return nil
}

// Get get a val from key
func (r *Instance) Get(key string) ([]byte, error) {
	data, err := r.Client.Get(r.ctx, key).Bytes()
	if err != nil {
		logger.Errorf("get redis key: %s error, %v", key, err)
		return nil, err
	}
	return data, nil
}

// Delete delete a key
func (r *Instance) Delete(key string) error {
	exist, err := r.Client.Exists(r.ctx, key).Result()
	if err != nil {
		logger.Errorf("check redis key: %s exist error, %v", key, err)
		return err
	}
	if exist == 0 {
		logger.Warnf("key: %s not exist from redis", key)
		return nil
	}
	if err := r.Client.Del(r.ctx, key).Err(); err != nil {
		logger.Errorf("delete key: %s error, %v", key, err)
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

func (r *Instance) HSet(key, field, value string) error {
	err := r.Client.HSet(r.ctx, key, field, value).Err()
	if err != nil {
		logger.Errorf("hset field error, key: %s, field: %s, value: %s", key, field, value)
		return err
	}
	return nil
}

func (r *Instance) HGet(key, field string) string {
	val := r.Client.HGet(r.ctx, key, field).Val()
	if val == "" {
		logger.Warnf("hset field error, key: %s, field: %s, value: %s", key, field, val)
	}
	return val
}

func (r *Instance) HGetAll(key string) map[string]string {
	val := r.Client.HGetAll(r.ctx, key).Val()
	if len(val) == 0 {
		logger.Warnf("hset field error, key: %s, value is empty", key)
	}
	return val
}

// Publish message pusher
func (r *Instance) Publish(channelName string, msg interface{}) error {
	if err := r.Client.Publish(r.ctx, channelName, msg).Err(); err != nil {
		return err
	}
	return nil
}

// Subscribe subscribe channel from redis
func (r *Instance) Subscribe(channelNames ...string) <-chan *goRedis.Message {
	p := r.Client.Subscribe(r.ctx, channelNames...)
	return p.Channel()
}

func (r *Instance) ZCount(key, min, max string) (int64, error) {
	zcount := r.Client.ZCount(r.ctx, key, min, max)
	return zcount.Result()
}

func (r *Instance) ZRangeByScoreWithScores(key string, opt *goRedis.ZRangeBy) ([]goRedis.Z, error) {
	return r.Client.ZRangeByScoreWithScores(r.ctx, key, opt).Result()
}

func (r *Instance) HMGet(key string, fields ...string) ([]interface{}, error) {
	return r.Client.HMGet(r.ctx, key, fields...).Result()
}
