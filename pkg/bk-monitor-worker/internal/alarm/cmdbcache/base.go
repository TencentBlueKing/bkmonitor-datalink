// MIT License

// Copyright (c) 2021~2024 腾讯蓝鲸

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

package cmdbcache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

const (
	cmdbApiPageSize = 500
)

var (
	cmdbApiClient     *cmdb.Client
	cmdbApiClientOnce sync.Once
)

// getCmdbApi Get cmdb api client instance with lock
func getCmdbApi() *cmdb.Client {
	cmdbApiClientOnce.Do(func() {
		config := bkapi.ClientConfig{
			Endpoint:            fmt.Sprintf("%s/api/c/compapi/v2/cc/", cfg.BkApiUrl),
			AuthorizationParams: map[string]string{"bk_username": "admin", "bk_supplier_account": "0"},
			AppCode:             cfg.BkApiAppCode,
			AppSecret:           cfg.BkApiAppSecret,
			JsonMarshaler:       jsonx.Marshal,
		}

		var err error
		cmdbApiClient, err = cmdb.New(
			config,
			bkapi.OptJsonBodyProvider(),
			OptRateLimitResultProvider(cfg.CmdbApiRateLimitQPS, cfg.CmdbApiRateLimitBurst, cfg.CmdbApiRateLimitTimeout),
		)
		if err != nil {
			panic(err)
		}
	})
	return cmdbApiClient
}

// Manager 缓存管理器接口
type Manager interface {
	// Type 缓存类型
	Type() string
	// GetCacheKey 获取缓存key
	GetCacheKey(key string) string
	// RefreshByBiz 按业务刷新缓存
	RefreshByBiz(ctx context.Context, bizID int) error
	// RefreshByBizIds 按业务列表刷新缓存，并清理指定的缓存
	RefreshByBizIds(ctx context.Context, bizIds []int, concurrentLimit int) error
	// RefreshGlobal 刷新全局缓存
	RefreshGlobal(ctx context.Context) error
	// CleanGlobal 清理全局缓存
	CleanGlobal(ctx context.Context) error
	// CleanPartial 清理部分缓存
	CleanPartial(ctx context.Context, cacheKey string, cleanFields []string) error
	// Reset 重置
	Reset()

	// UseBiz 是否按业务执行
	useBiz() bool
	// GetConcurrentLimit 并发限制
	GetConcurrentLimit() int
}

// BaseCacheManager 基础缓存管理器
type BaseCacheManager struct {
	Prefix          string
	RedisClient     redis.UniversalClient
	Expire          time.Duration
	ConcurrentLimit int

	updatedFieldSet  map[string]map[string]struct{}
	updateFieldLocks map[string]*sync.Mutex
}

// NewBaseCacheManager 创建缓存管理器
func NewBaseCacheManager(prefix string, opt *redis.Options, concurrentLimit int) (*BaseCacheManager, error) {
	client, err := redis.GetClient(opt)
	if err != nil {
		return nil, err
	}
	return &BaseCacheManager{
		Prefix:           prefix,
		RedisClient:      client,
		Expire:           time.Hour * 24 * 7,
		updatedFieldSet:  make(map[string]map[string]struct{}),
		updateFieldLocks: make(map[string]*sync.Mutex),
		ConcurrentLimit:  concurrentLimit,
	}, nil
}

// Type 缓存类型
func (c *BaseCacheManager) Type() string {
	return "base"
}

// Reset 重置
func (c *BaseCacheManager) Reset() {
	for cacheKey := range c.updatedFieldSet {
		c.updateFieldLocks[cacheKey].Lock()
		c.updatedFieldSet[cacheKey] = make(map[string]struct{})
		c.updateFieldLocks[cacheKey].Unlock()
	}
}

// initUpdatedFieldSet 初始化更新字段集合，确保后续不存在并发问题
func (c *BaseCacheManager) initUpdatedFieldSet(keys ...string) {
	for _, key := range keys {
		cacheKey := c.GetCacheKey(key)
		c.updatedFieldSet[cacheKey] = make(map[string]struct{})
		c.updateFieldLocks[cacheKey] = &sync.Mutex{}
	}
}

// GetConcurrentLimit 并发限制
func (c *BaseCacheManager) GetConcurrentLimit() int {
	return c.ConcurrentLimit
}

// GetCacheKey 获取缓存key
func (c *BaseCacheManager) GetCacheKey(key string) string {
	return fmt.Sprintf("%s.%s", c.Prefix, key)
}

// UpdateHashMapCache 更新hashmap类型缓存
func (c *BaseCacheManager) UpdateHashMapCache(ctx context.Context, key string, data map[string]string) error {
	client := c.RedisClient
	cacheKey := c.GetCacheKey(key)

	// 初始化更新字段集合
	updatedFieldSet, ok := c.updatedFieldSet[cacheKey]
	if !ok {
		return errors.Errorf("key %s not found in updatedFieldSet", key)
	}
	lock, _ := c.updateFieldLocks[cacheKey]

	// 执行更新
	pipeline := client.Pipeline()
	lock.Lock()
	for field, value := range data {
		pipeline.HSet(ctx, cacheKey, field, value)
		updatedFieldSet[field] = struct{}{}

		if pipeline.Len() > 500 {
			lock.Unlock()
			if _, err := pipeline.Exec(ctx); err != nil {
				return errors.Wrap(err, "update hashmap failed")
			}
			lock.Lock()
		}
	}
	lock.Unlock()

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
	cacheKey := c.GetCacheKey(key)

	// 获取已更新的字段，如果不存在则删除
	updatedFieldSet, ok := c.updatedFieldSet[cacheKey]
	if !ok || len(updatedFieldSet) == 0 {
		client.Del(ctx, cacheKey)
		return nil
	}

	// 获取已存在的字段
	existsFields, err := client.HKeys(ctx, cacheKey).Result()
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
	client.HDel(ctx, cacheKey, needDeleteFields...)

	return nil
}

// UpdateExpire 更新缓存过期时间
func (c *BaseCacheManager) UpdateExpire(ctx context.Context, key string) error {
	client := c.RedisClient
	result := client.Expire(ctx, c.GetCacheKey(key), c.Expire*time.Second)
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

// CleanGlobal 清理全局缓存
func (c *BaseCacheManager) CleanGlobal(ctx context.Context) error {
	return nil
}

// CleanPartial 清理部分缓存
func (c *BaseCacheManager) CleanPartial(ctx context.Context, key string, cleanFields []string) error {
	cacheKey := c.GetCacheKey(key)
	needCleanFields := make([]string, 0)
	for _, field := range cleanFields {
		if _, ok := c.updatedFieldSet[cacheKey][field]; ok {
			needCleanFields = append(needCleanFields, field)
		}
	}

	if len(needCleanFields) == 0 {
		c.RedisClient.HDel(ctx, cacheKey, cleanFields...)
	}
	return nil
}

// UseBiz 是否按业务执行
func (c *BaseCacheManager) useBiz() bool {
	return true
}

// NewCacheManagerByType 创建缓存管理器
func NewCacheManagerByType(opt *redis.Options, prefix string, cacheType string, concurrentLimit int) (Manager, error) {
	var cacheManager Manager
	var err error
	switch cacheType {
	case "host_topo":
		cacheManager, err = NewHostAndTopoCacheManager(prefix, opt, concurrentLimit)
	case "business":
		cacheManager, err = NewBusinessCacheManager(prefix, opt, concurrentLimit)
	case "module":
		cacheManager, err = NewModuleCacheManager(prefix, opt, concurrentLimit)
	case "set":
		cacheManager, err = NewSetCacheManager(prefix, opt, concurrentLimit)
	case "service_instance":
		cacheManager, err = NewServiceInstanceCacheManager(prefix, opt, concurrentLimit)
	case "dynamic_group":
		cacheManager, err = NewDynamicGroupCacheManager(prefix, opt, concurrentLimit)
	default:
		err = errors.Errorf("unsupported cache type: %s", cacheType)
	}
	return cacheManager, err
}

// RefreshByBizIds 按业务列表刷新缓存，并清理指定的缓存
func (c *BaseCacheManager) RefreshByBizIds(ctx context.Context, bizIds []int, concurrentLimit int) error {
	// 并发控制
	wg := sync.WaitGroup{}
	limitChan := make(chan struct{}, concurrentLimit)

	// 按业务刷新缓存
	errChan := make(chan error, len(bizIds))
	for _, bizId := range bizIds {
		limitChan <- struct{}{}
		wg.Add(1)
		go func(bizId int) {
			defer func() {
				wg.Done()
				<-limitChan
			}()
			err := c.RefreshByBiz(ctx, bizId)
			if err != nil {
				errChan <- errors.Wrapf(err, "refresh %s cache by biz failed, biz: %d", c.Type(), bizId)
			}
		}(bizId)
	}

	// 等待所有任务完成
	wg.Wait()
	close(errChan)
	for err := range errChan {
		return err
	}

	return nil
}
