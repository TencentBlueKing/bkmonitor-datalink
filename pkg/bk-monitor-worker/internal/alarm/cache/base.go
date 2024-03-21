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

package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/pkg/errors"

	redis2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
)

const (
	CmdbApiPageSize = 500
)

// Manager 缓存管理器接口
type Manager interface {
	// Type 缓存类型
	Type() string
	// RefreshByBiz 按业务刷新缓存
	RefreshByBiz(ctx context.Context, bizID int) error
	// RefreshGlobal 刷新全局缓存
	RefreshGlobal(ctx context.Context) error
	// CleanByBiz 按业务清理缓存
	CleanByBiz(ctx context.Context, bizID int) error
	// CleanGlobal 清理全局缓存
	CleanGlobal(ctx context.Context) error

	// UseBiz 是否按业务执行
	UseBiz() bool

	// CleanByEvents 根据事件清理缓存
	CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error
	// UpdateByEvents 根据事件更新缓存
	UpdateByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error
}

// BaseCacheManager 基础缓存管理器
type BaseCacheManager struct {
	Prefix      string
	RedisClient redis.UniversalClient
	Expire      int

	updatedFieldSet map[string]map[string]struct{}
}

// NewBaseCacheManager 创建缓存管理器
func NewBaseCacheManager(prefix string, opt *redis2.RedisOptions) (*BaseCacheManager, error) {
	client, err := redis2.GetRedisClient(opt)
	if err != nil {
		return nil, err
	}
	return &BaseCacheManager{
		Prefix:          prefix,
		RedisClient:     client,
		Expire:          86400,
		updatedFieldSet: make(map[string]map[string]struct{}),
	}, nil
}

// initUpdatedFieldSet 初始化更新字段集合，确保后续不存在并发问题
func (c *BaseCacheManager) initUpdatedFieldSet(keys ...string) {
	for _, key := range keys {
		c.updatedFieldSet[c.GetCacheKey(key)] = make(map[string]struct{})
	}
}

// GetCacheKey 获取缓存key
func (c *BaseCacheManager) GetCacheKey(key string) string {
	return fmt.Sprintf("%s.%s", c.Prefix, key)
}

// UpdateHashMapCache 更新hashmap类型缓存
func (c *BaseCacheManager) UpdateHashMapCache(ctx context.Context, key string, data map[string]string) error {
	client := c.RedisClient

	// 初始化更新字段集合
	updatedFieldSet, ok := c.updatedFieldSet[key]
	if !ok {
		return errors.Errorf("key %s not found in updatedFieldSet", key)
	}

	// 执行更新
	pipeline := client.Pipeline()
	for field, value := range data {
		pipeline.HSet(ctx, key, field, value)
		updatedFieldSet[field] = struct{}{}

		if pipeline.Len() > 500 {
			if _, err := pipeline.Exec(ctx); err != nil {
				return errors.Wrap(err, "update hashmap failed")
			}
		}
	}
	if pipeline.Len() > 0 {
		if _, err := pipeline.Exec(ctx); err != nil {
			return errors.Wrap(err, "update hashmap failed")
		}
	}
	return nil
}

// DeleteMissingHashMapFields 删除hashmap类型缓存中不存在的字段
func (c *BaseCacheManager) DeleteMissingHashMapFields(ctx context.Context, key string) error {
	client := c.RedisClient

	// 获取已更新的字段，如果不存在则删除
	updatedFieldSet, ok := c.updatedFieldSet[key]
	if !ok || len(updatedFieldSet) == 0 {
		client.Del(ctx, key)
		return nil
	}

	// 获取已存在的字段
	existsFields, err := client.HKeys(ctx, key).Result()
	if err != nil {
		return err
	}
	existsFieldSet := make(map[string]struct{})
	for _, field := range existsFields {
		existsFieldSet[field] = struct{}{}
	}

	// 计算需要删除的字段
	needDeleteFields := make([]string, 0)
	for field := range existsFieldSet {
		if _, ok := updatedFieldSet[field]; !ok {
			needDeleteFields = append(needDeleteFields, field)
		}
	}

	// 执行删除
	client.HDel(ctx, key, needDeleteFields...)

	return nil
}

// UpdateExpire 更新缓存过期时间
func (c *BaseCacheManager) UpdateExpire(ctx context.Context, key string) error {
	client := c.RedisClient
	result := client.Expire(ctx, key, time.Duration(c.Expire)*time.Second)
	if err := result.Err(); err != nil {
		return errors.Wrap(err, "expire hashmap failed")
	}
	return nil
}

// RefreshByBiz 刷新业务缓存
func (c *BaseCacheManager) RefreshByBiz(ctx context.Context, bizID int) error {
	return nil
}

// RefreshGlobal 刷新全局缓存
func (c *BaseCacheManager) RefreshGlobal(ctx context.Context) error {
	return nil
}

// CleanByBiz 清理业务缓存
func (c *BaseCacheManager) CleanByBiz(ctx context.Context, bizID int) error {
	return nil
}

// CleanGlobal 清理全局缓存
func (c *BaseCacheManager) CleanGlobal(ctx context.Context) error {
	return nil
}

// UseBiz 是否按业务执行
func (c *BaseCacheManager) UseBiz() bool {
	return true
}
