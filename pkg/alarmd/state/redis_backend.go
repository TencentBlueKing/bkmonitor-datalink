// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

type RedisBackendOptions struct {
	Address      string
	Username     string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
}

type redisClient interface {
	MGet(context.Context, ...string) *redis.SliceCmd
	Pipelined(context.Context, func(redis.Pipeliner) error) ([]redis.Cmder, error)
	Ping(context.Context) *redis.StatusCmd
	Close() error
}

// RedisBackend implements the minimum phase-one Redis String command set. The
// go-redis client owns pooling and reconnect; dependency errors are returned to
// M7 without being converted into NORMAL/RECOVERY state.
type RedisBackend struct {
	address string
	client  redisClient
}

func NewRedisBackend(options RedisBackendOptions) (*RedisBackend, error) {
	if options.Address == "" || options.DB < 0 || options.DialTimeout <= 0 || options.ReadTimeout <= 0 ||
		options.WriteTimeout <= 0 || options.PoolSize <= 0 {
		return nil, fmt.Errorf("state: invalid Redis backend options")
	}
	client := redis.NewClient(&redis.Options{
		Addr: options.Address, Username: options.Username, Password: options.Password, DB: options.DB,
		DialTimeout: options.DialTimeout, ReadTimeout: options.ReadTimeout, WriteTimeout: options.WriteTimeout,
		PoolSize: options.PoolSize,
	})
	return &RedisBackend{address: options.Address, client: client}, nil
}

func (backend *RedisBackend) Address() string {
	if backend == nil {
		return ""
	}
	return backend.address
}

func (backend *RedisBackend) Ping(ctx context.Context) error {
	if backend == nil || backend.client == nil {
		return fmt.Errorf("state: Redis backend is required")
	}
	return backend.client.Ping(ctx).Err()
}

func (backend *RedisBackend) MGet(ctx context.Context, keys []string) ([][]byte, error) {
	if backend == nil || backend.client == nil {
		return nil, fmt.Errorf("state: Redis backend is required")
	}
	if len(keys) == 0 {
		return [][]byte{}, nil
	}
	values, err := backend.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	if len(values) != len(keys) {
		return nil, fmt.Errorf("state: Redis MGET returned %d values for %d keys", len(values), len(keys))
	}
	result := make([][]byte, len(values))
	for index, value := range values {
		switch typed := value.(type) {
		case nil:
		case string:
			result[index] = []byte(typed)
		case []byte:
			result[index] = append([]byte(nil), typed...)
		default:
			return nil, fmt.Errorf("state: Redis MGET value %d has unsupported type %T", index, value)
		}
	}
	return result, nil
}

func (backend *RedisBackend) SetMany(ctx context.Context, writes []BackendWrite) error {
	if backend == nil || backend.client == nil {
		return fmt.Errorf("state: Redis backend is required")
	}
	if len(writes) == 0 {
		return nil
	}
	for index, write := range writes {
		if write.Key == "" || write.TTL <= 0 {
			return fmt.Errorf("state: invalid Redis write %d", index)
		}
	}
	_, err := backend.client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
		for _, write := range writes {
			pipeline.Set(ctx, write.Key, write.Value, write.TTL)
		}
		return nil
	})
	return err
}

func (backend *RedisBackend) Close() error {
	if backend == nil || backend.client == nil {
		return nil
	}
	return backend.client.Close()
}

type FixedRouter struct {
	target StorageTarget
}

func NewFixedRouter(name string, backend Backend) (*FixedRouter, error) {
	if name == "" || backend == nil {
		return nil, fmt.Errorf("state: fixed storage target name and backend are required")
	}
	return &FixedRouter{target: StorageTarget{Name: name, Backend: backend}}, nil
}

func (router *FixedRouter) Route(_, _ string) (StorageTarget, error) {
	if router == nil || router.target.Name == "" || router.target.Backend == nil {
		return StorageTarget{}, fmt.Errorf("state: fixed storage router is not configured")
	}
	return router.target, nil
}
