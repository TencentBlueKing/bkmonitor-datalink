// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build redis
// +build redis

package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// Redis :
type Redis = *redis.Client

// RedisStore :
type RedisStore struct {
	define.BaseStore
	redis         Redis
	scanBatchSize int64
}

// Exists :
func (s *RedisStore) Exists(key string) (bool, error) {
	value, err := s.redis.Exists(key).Result()
	if err != nil {
		return false, err
	}
	if value == 1 {
		return true, err
	}
	return false, nil
}

// Set :
func (s *RedisStore) Set(key string, data []byte, expires time.Duration) error {
	_, err := s.redis.Set(key, data, expires).Result()
	return err
}

// Get :
func (s *RedisStore) Get(key string) ([]byte, error) {
	result, err := s.redis.Get(key).Bytes()
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Delete :
func (s *RedisStore) Delete(key string) error {
	_, err := s.redis.Del(key).Result()
	return err
}

// Commit :
func (s *RedisStore) Commit() error {
	_, err := s.redis.Command().Result()
	return err
}

// Scan :
func (s *RedisStore) Scan(prefix string, callback define.StoreScanCallback, withAll ...bool) error {
	var (
		cursor uint64 = 0
		match         = fmt.Sprintf("%s*", prefix)
	)

loop:
	for {
		keys, cursor, err := s.redis.Scan(cursor, match, s.scanBatchSize).Result()
		if err != nil {
			return err
		} else if cursor == 0 {
			break
		}

		pipe := s.redis.Pipeline()
		for _, key := range keys {
			pipe.Get(key)
		}

		cmds, err := pipe.Exec()
		if err != nil {
			return err
		}

		for index, c := range cmds {
			cmd := c.(*redis.StringCmd)
			result, err := cmd.Bytes()
			if err != nil {
				return err
			}

			if !callback(keys[index], result) {
				break loop
			}
		}
	}
	return nil
}

// Close :
func (s *RedisStore) Close() error {
	return s.redis.Close()
}

// PutCache : NotImplemented
func (s *RedisStore) PutCache(key string, data []byte, expires time.Duration) error {
	s.Set(key, data, expires)
}

// Batch :
func (s *RedisStore) Batch() error {
	return nil
}

// NewRedisStore :
func NewRedisStore(redis Redis) *RedisStore {
	return &RedisStore{
		redis: redis,
	}
}

const (
	ConfRedisStorageHost     = "redis.host"
	ConfRedisStoragePort     = "redis.port"
	ConfRedisStoragePassword = "redis.password"
	ConfRedisStorageDatabase = "storage.redis.database"
)

func initRedisConfiguration(c define.Configuration) {
	c.SetDefault(ConfRedisStorageHost, "localhost")
	c.SetDefault(ConfRedisStoragePassword, "")
	c.SetDefault(ConfRedisStoragePort, 6379)
	c.SetDefault(ConfRedisStorageDatabase, 0)
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initRedisConfiguration))
	define.RegisterStore("redis", func(ctx context.Context, name string) (define.Store, error) {
		conf := config.FromContext(ctx)
		redisDB := redis.NewClient(&redis.Options{
			Addr:     fmt.Sprintf("%s:%d", conf.GetString(ConfRedisStorageHost), conf.GetInt(ConfRedisStoragePort)),
			Password: conf.GetString(ConfRedisStoragePassword),
			DB:       conf.GetInt(ConfRedisStorageDatabase),
		})

		_, err := redisDB.Ping().Result()
		if err != nil {
			return nil, err
		}

		return NewRedisStore(redisDB), nil
	})
}
