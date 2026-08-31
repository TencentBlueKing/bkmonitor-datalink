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
	Eval(context.Context, string, []string, ...interface{}) *redis.Cmd
	Close() error
}

const compareAndSetScript = `
local current = redis.call('GET', KEYS[1])
if ARGV[1] == '1' then
  if current then return 0 end
else
  if not current or current ~= ARGV[2] then return 0 end
end
if tonumber(ARGV[4]) == 0 then
  redis.call('SET', KEYS[1], ARGV[3])
else
  redis.call('PSETEX', KEYS[1], ARGV[4], ARGV[3])
end
return 1
`

// RedisBackend implements the minimum phase-one Redis String command set. The
// go-redis client owns pooling and reconnect; dependency errors are returned to
// M7 without being converted into NORMAL/RECOVERY state.
type RedisBackend struct {
	address    string
	client     redisClient
	ownsClient bool
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
	return &RedisBackend{address: options.Address, client: client, ownsClient: true}, nil
}

// NewRedisBackendWithClient binds the state backend to a runtime-owned
// universal Redis client. The caller retains client lifecycle ownership.
func NewRedisBackendWithClient(address string, client redis.UniversalClient) (*RedisBackend, error) {
	if address == "" || client == nil {
		return nil, fmt.Errorf("state: Redis backend address and client are required")
	}
	return &RedisBackend{address: address, client: client}, nil
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

func (backend *RedisBackend) CompareAndSet(
	ctx context.Context, key string, expected []byte, expectedMissing bool, value []byte, ttl time.Duration,
) (bool, error) {
	if backend == nil || backend.client == nil || key == "" || len(value) == 0 || ttl < 0 ||
		(expectedMissing && len(expected) != 0) {
		return false, fmt.Errorf("state: invalid Redis compare-and-set")
	}
	missing := "0"
	if expectedMissing {
		missing = "1"
	}
	result, err := backend.client.Eval(ctx, compareAndSetScript, []string{key}, missing, expected, value, ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (backend *RedisBackend) Close() error {
	if backend == nil || backend.client == nil || !backend.ownsClient {
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
