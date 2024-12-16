// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"fmt"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/patrickmn/go-cache"

	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

type CacheType string

var (
	// CacheTypeRedis cache resource: redis
	CacheTypeRedis CacheType = "redis"
	// CacheTypeMemory cache resource: memory
	CacheTypeMemory CacheType = "memory"
)

// CacheKey cache key format class
type CacheKey struct {
	Format func(bkBizId, appName, traceId string) string
	Ttl    time.Duration
}

var (
	// CacheTraceInfoKey origin trace info key instance
	CacheTraceInfoKey = CacheKey{
		Format: func(bkBizId, appName, traceId string) string {
			return fmt.Sprintf("traceInfo:%s:%s:%s", bkBizId, appName, traceId)
		},
		Ttl: 5 * time.Minute,
	}
)

// CacheStorageData storage request of cache
type CacheStorageData struct {
	DataId string
	Key    string
	Value  []byte
	Ttl    time.Duration
}

// CacheOperator cache interface
type CacheOperator interface {
	// Save saves cache request
	Save(CacheStorageData) error
	// SaveBatch batch save cache request
	SaveBatch([]CacheStorageData) error
	// Query query cache
	Query(string) ([]byte, error)
	// Close resource
	Close()
}

// RedisCacheOptions resource: cache config
type RedisCacheOptions struct {
	Mode             string
	Host             string
	Port             int
	SentinelAddress  []string
	MasterName       string
	SentinelPassword string
	Password         string
	Db               int
	DialTimeout      time.Duration
	ReadTimeout      time.Duration
}

type RedisCache struct {
	ctx context.Context

	client goRedis.UniversalClient
}

func (r *RedisCache) Save(data CacheStorageData) error {
	_, err := r.client.Set(r.ctx, data.Key, data.Value, data.Ttl).Result()
	return err
}

func (r *RedisCache) SaveBatch(items []CacheStorageData) error {
	p := r.client.Pipeline()
	for _, data := range items {
		p.Set(r.ctx, data.Key, data.Value, data.Ttl)
	}
	_, err := p.Exec(r.ctx)
	return err
}

func (r *RedisCache) Query(key string) ([]byte, error) {
	ex := r.client.Exists(r.ctx, key)
	n, err := ex.Result()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}

	res := r.client.Get(r.ctx, key)
	return res.Bytes()
}

func (r *RedisCache) Close() {
	r.client.Close()
}

func newRedisCache(ctx context.Context, options RedisCacheOptions) (*RedisCache, error) {
	client, err := redisUtils.NewRedisClient(
		context.Background(),
		&redisUtils.Option{
			Mode:             options.Mode,
			Host:             options.Host,
			Port:             options.Port,
			SentinelAddress:  options.SentinelAddress,
			MasterName:       options.MasterName,
			SentinelPassword: options.SentinelPassword,
			Password:         options.Password,
			Db:               options.Db,
			DialTimeout:      options.DialTimeout,
			ReadTimeout:      options.ReadTimeout,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis client. %+v. error: %s", options, err)
	}

	monitorLogger.Infof("create Redis Client successfully")
	return &RedisCache{client: client, ctx: ctx}, nil
}

type MemoryCache struct {
	c *cache.Cache
}

func (m *MemoryCache) Save(data CacheStorageData) error {
	m.c.Set(data.Key, data.Value, data.Ttl)
	return nil
}

func (m *MemoryCache) SaveBatch(items []CacheStorageData) error {
	for _, data := range items {
		m.c.Set(data.Key, data.Value, data.Ttl)
	}
	return nil
}

func (m *MemoryCache) Query(key string) ([]byte, error) {
	r, exist := m.c.Get(key)
	if exist {
		return r.([]byte), nil
	}
	return nil, nil
}

func (m *MemoryCache) Close() {
	m.c.Flush()
}

func newMemoryCache() (*MemoryCache, error) {
	return &MemoryCache{}, nil
}
