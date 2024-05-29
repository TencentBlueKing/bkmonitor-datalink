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

package cmdbcache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

const (
	cmdbApiPageSize = 500
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
	// Reset 重置
	Reset()

	// UseBiz 是否按业务执行
	useBiz() bool
	// GetConcurrentLimit 并发限制
	GetConcurrentLimit() int

	// CleanByEvents 根据事件清理缓存
	CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error
	// UpdateByEvents 根据事件更新缓存
	UpdateByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error
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

// Reset 重置
func (c *BaseCacheManager) Reset() {
	for key := range c.updatedFieldSet {
		c.updateFieldLocks[key].Lock()
		c.updatedFieldSet[key] = make(map[string]struct{})
		c.updateFieldLocks[key].Unlock()
	}
}

// initUpdatedFieldSet 初始化更新字段集合，确保后续不存在并发问题
func (c *BaseCacheManager) initUpdatedFieldSet(keys ...string) {
	for _, key := range keys {
		c.updatedFieldSet[c.GetCacheKey(key)] = make(map[string]struct{})
		c.updateFieldLocks[c.GetCacheKey(key)] = &sync.Mutex{}
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

	// 初始化更新字段集合
	updatedFieldSet, ok := c.updatedFieldSet[key]
	if !ok {
		return errors.Errorf("key %s not found in updatedFieldSet", key)
	}
	lock, _ := c.updateFieldLocks[key]

	// 执行更新
	pipeline := client.Pipeline()
	lock.Lock()
	for field, value := range data {
		pipeline.HSet(ctx, key, field, value)
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
	default:
		err = errors.Errorf("unsupported cache type: %s", cacheType)
	}
	return cacheManager, err
}

// RefreshAll 执行缓存管理器
func RefreshAll(ctx context.Context, cacheManager Manager, concurrentLimit int) error {
	// 判断是否启用业务缓存刷新
	if cacheManager.useBiz() {
		// 获取业务列表
		cmdbApi, err := api.GetCmdbApi()
		if err != nil {
			return errors.Wrap(err, "get cmdb api client failed")
		}
		var result cmdb.SearchBusinessResp
		_, err = cmdbApi.SearchBusiness().SetResult(&result).Request()
		if err = api.HandleApiResultError(result.ApiCommonRespMeta, err, "search business failed"); err != nil {
			return err
		}

		// 并发控制
		wg := sync.WaitGroup{}
		limitChan := make(chan struct{}, concurrentLimit)

		// 按业务刷新缓存
		errChan := make(chan error, len(result.Data.Info))
		for _, biz := range result.Data.Info {
			limitChan <- struct{}{}
			wg.Add(1)
			go func(bizId int) {
				defer func() {
					wg.Done()
					<-limitChan
				}()
				err := cacheManager.RefreshByBiz(ctx, bizId)
				if err != nil {
					errChan <- errors.Wrapf(err, "refresh host and topo cache by biz failed, biz: %d", bizId)
				}
			}(biz.BkBizId)
		}

		// 等待所有任务完成
		wg.Wait()
		close(errChan)
		for err := range errChan {
			return err
		}

		// 按业务清理缓存
		errChan = make(chan error, len(result.Data.Info))
		for _, biz := range result.Data.Info {
			limitChan <- struct{}{}
			wg.Add(1)
			go func(bizId int) {
				defer func() {
					wg.Done()
					<-limitChan
				}()
				err := cacheManager.CleanByBiz(ctx, bizId)
				if err != nil {
					errChan <- errors.Wrapf(err, "clean host and topo cache by biz failed, biz: %d", bizId)
				}
			}(biz.BkBizId)
		}

		// 等待所有任务完成
		wg.Wait()
		close(errChan)
		for err := range errChan {
			return err
		}
	}

	// 刷新全局缓存
	err := cacheManager.RefreshGlobal(ctx)
	if err != nil {
		return errors.Wrap(err, "refresh global host and topo cache failed")
	}

	// 清理全局缓存
	err = cacheManager.CleanGlobal(ctx)
	if err != nil {
		return errors.Wrap(err, "clean global host and topo cache failed")
	}

	return nil
}
