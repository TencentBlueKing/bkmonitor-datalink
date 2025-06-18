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

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/bkapi"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

const (
	cmdbApiPageSize = 500
)

var (
	cmdbApiClients     map[string]*cmdb.Client
	cmdbApiClientMutex sync.RWMutex
)

func init() {
	cmdbApiClients = make(map[string]*cmdb.Client)
}

// getCmdbApi Get cmdb api client instance with lock
func getCmdbApi(tenantId string) *cmdb.Client {
	// 首先尝试读锁获取已存在的客户端
	cmdbApiClientMutex.RLock()
	if client, exists := cmdbApiClients[tenantId]; exists {
		cmdbApiClientMutex.RUnlock()
		return client
	}
	cmdbApiClientMutex.RUnlock()

	// 如果不存在，获取写锁创建新客户端
	cmdbApiClientMutex.Lock()
	defer cmdbApiClientMutex.Unlock()

	// 双重检查，防止在等待写锁期间其他goroutine已经创建了客户端
	if client, exists := cmdbApiClients[tenantId]; exists {
		return client
	}

	// 判断是否使用网关
	var endpoint string
	if cfg.BkApiCmdbApiGatewayUrl != "" {
		endpoint = cfg.BkApiCmdbApiGatewayUrl
	} else {
		endpoint = fmt.Sprintf("%s/api/c/compapi/v2/cc/", cfg.BkApiUrl)
	}

	config := bkapi.ClientConfig{
		Endpoint:            endpoint,
		AuthorizationParams: map[string]string{"bk_username": "admin", "bk_supplier_account": "0"},
		AppCode:             cfg.BkApiAppCode,
		AppSecret:           cfg.BkApiAppSecret,
		JsonMarshaler:       jsonx.Marshal,
	}

	client, err := cmdb.New(
		config,
		bkapi.OptJsonBodyProvider(),
		OptRateLimitResultProvider(cfg.CmdbApiRateLimitQPS, cfg.CmdbApiRateLimitBurst, cfg.CmdbApiRateLimitTimeout),
		api.NewHeaderProvider(map[string]string{"X-Bk-Tenant-Id": tenantId}),
	)
	if err != nil {
		panic(err)
	}

	cmdbApiClients[tenantId] = client
	return client
}

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
	// GetBkTenantId 获取bk_tenant_id
	GetBkTenantId() string
	// GetConcurrentLimit 并发限制
	GetConcurrentLimit() int

	// CleanByEvents 根据事件清理缓存
	CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error
	// UpdateByEvents 根据事件更新缓存
	UpdateByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error
}

// BaseCacheManager 基础缓存管理器
type BaseCacheManager struct {
	bkTenantId      string
	Prefix          string
	RedisClient     redis.UniversalClient
	Expire          time.Duration
	ConcurrentLimit int

	updatedFieldSet  map[string]map[string]struct{}
	updateFieldLocks map[string]*sync.Mutex
}

// NewBaseCacheManager 创建缓存管理器
func NewBaseCacheManager(bkTenantId string, prefix string, opt *redis.Options, concurrentLimit int) (*BaseCacheManager, error) {
	client, err := redis.GetClient(opt)
	if err != nil {
		return nil, err
	}
	return &BaseCacheManager{
		bkTenantId:       bkTenantId,
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
	// 如果租户id为默认租户，则不添加租户id，为了兼容旧的缓存key
	if c.bkTenantId == tenant.DefaultTenantId {
		return fmt.Sprintf("%s.%s", c.Prefix, key)
	}
	return fmt.Sprintf("%s.%s.%s", c.bkTenantId, c.Prefix, key)
}

// UpdateHashMapCache 更新hashmap类型缓存
func (c *BaseCacheManager) UpdateHashMapCache(ctx context.Context, key string, data map[string]string) error {
	client := c.RedisClient

	// 初始化更新字段集合
	updatedFieldSet := c.updatedFieldSet[key]
	lock := c.updateFieldLocks[key]

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
	updatedFieldSet := c.updatedFieldSet[key]
	if len(updatedFieldSet) == 0 {
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

// GetBkTenantId 获取bk_tenant_id
func (c *BaseCacheManager) GetBkTenantId() string {
	return c.bkTenantId
}

// NewCacheManagerByType 创建缓存管理器
func NewCacheManagerByType(bkTenantId string, opt *redis.Options, prefix string, cacheType string, concurrentLimit int) (Manager, error) {
	var cacheManager Manager
	var err error
	switch cacheType {
	case "host_topo":
		cacheManager, err = NewHostAndTopoCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	case "business":
		cacheManager, err = NewBusinessCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	case "module":
		cacheManager, err = NewModuleCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	case "set":
		cacheManager, err = NewSetCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	case "service_instance":
		cacheManager, err = NewServiceInstanceCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	case "dynamic_group":
		cacheManager, err = NewDynamicGroupCacheManager(bkTenantId, prefix, opt, concurrentLimit)
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
		cmdbApi := getCmdbApi(cacheManager.GetBkTenantId())
		var result cmdb.SearchBusinessResp
		_, err := cmdbApi.SearchBusiness().SetResult(&result).Request()
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
					errChan <- errors.Wrapf(err, "refresh %s cache by biz failed, biz: %d", cacheManager.Type(), bizId)
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
					errChan <- errors.Wrapf(err, "clean %s cache by biz failed, biz: %d", cacheManager.Type(), bizId)
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
		return errors.Wrapf(err, "refresh global %s cache failed", cacheManager.Type())
	}

	// 清理全局缓存
	err = cacheManager.CleanGlobal(ctx)
	if err != nil {
		return errors.Wrapf(err, "clean global %s cache failed", cacheManager.Type())
	}

	return nil
}
