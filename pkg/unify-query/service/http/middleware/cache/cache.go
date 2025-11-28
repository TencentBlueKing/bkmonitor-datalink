package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http"
	"github.com/dgraph-io/ristretto"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

const (
	locked  = "1"
	doneMsg = "done"
)

func NewInstance(ctx context.Context) (*Service, error) {
	service := &Service{}
	err := service.initialize(ctx)
	if err != nil {
		return nil, err
	}
	return service, nil
}

type Service struct {
	ctx  context.Context
	conf Config

	localCache *ristretto.Cache
	metrics    *Metrics
	resilience *ResilienceManager
	winnerMap  sync.Map
	waiterMap  sync.Map
}

type Config struct {
	// executeTTL 是执行函数的最大允许时间
	executeTTL time.Duration
	// payloadTTL 是缓存数据的 TTL
	payloadTTL time.Duration
	// lockTTL 是分布式锁的 TTL
	lockTTL time.Duration
	// freshLock 是作为配置的锁的续期时间. 真实的刷新频率会是该值的一半
	freshLock time.Duration
}

func (d *Service) getCacheFromLocal(key string) (interface{}, bool) {
	return d.localCache.Get(key)
}

func (d *Service) setCacheToLocal(key string, value interface{}) {
	d.localCache.SetWithTTL(key, value, 1, d.conf.payloadTTL)
}

func (d *Service) doDistributed(ctx context.Context, key string, doQuery func() (interface{}, error)) (interface{}, error) {
	lockKey := cacheKeyMap(lockKeyType, key)
	metrics := d.metrics

	var l2Result interface{}
	var l2Err error

	err := d.resilience.ExecuteWithProtection(ctx, func() error {
		l2Start := time.Now()
		l2Result, l2Err = d.getFromDistributedCache(ctx, key)
		metrics.recordCacheDuration("l2_read", time.Since(l2Start))
		return l2Err
	})

	if err == nil && l2Err == nil {
		metrics.recordCacheRequest("l2", "hit")
		d.setCacheToLocal(key, l2Result)
		return l2Result, nil
	}

	if !d.resilience.IsRedisAvailable() {
		metrics.recordCacheRequest("l2", "circuit_open")
		metrics.recordDBRequest("redis_downgrade")
		return doQuery()
	}

	metrics.recordCacheRequest("l2", "miss")

	var acquired bool
	// 1. 尝试从Redis获取分布式锁
	err = d.resilience.ExecuteWithProtection(ctx, func() error {
		lockStart := time.Now()
		acq, lockErr := redis.SetNX(ctx, lockKey, locked, d.conf.lockTTL)
		acquired = acq
		metrics.recordCacheDuration("lock_acquire", time.Since(lockStart))
		return lockErr
	})

	if err != nil {
		metrics.recordCacheError("redis_setnx", "lock_error")
		return nil, err
	}

	if acquired {
		// 2.1 锁获取成功，成为 Cluster Winner
		d.winnerMap.Store(key, key)
		defer func() {
			d.winnerMap.Delete(key)
		}()
		metrics.recordSingleflightDedup("cluster_winner")
		metrics.recordDBRequest("cache_miss")

		dbStart := time.Now()
		// 2.1.1 执行函数并通知等待者
		result, dbErr := d.runAndNotify(ctx, key, doQuery)
		metrics.recordCacheDuration("db_query", time.Since(dbStart))

		return result, dbErr
	} else {
		metrics.recordSingleflightDedup("cluster_waiter")
		// 2.2 锁获取失败，成为 Cluster Waiter
		// 2.2.1 进入等待循环
		return d.waiterLoop(ctx, key)
	}
}

func (d *Service) runAndNotify(ctx context.Context, key string, doQuery func() (interface{}, error)) (interface{}, error) {
	dataKey := cacheKeyMap(dataKeyType, key)
	channelKey := cacheKeyMap(channelKeyType, key)

	// 1. 执行函数获取结果
	result, err := doQuery()
	if err != nil {
		return nil, err
	}

	bts, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	// 2. 回写缓存
	err = d.writeDistributedCache(ctx, dataKey, bts)
	if err != nil {
		log.Warnf(ctx, "failed to write cache with limit control: %v", err)
		return nil, err
	}

	// 3. 通知等待者
	_, err = redis.Publish(ctx, channelKey, doneMsg)
	if err != nil {
		log.Warnf(ctx, "failed to publish completion for key %s: %v", dataKey, err)
	}

	return result, nil
}

func (d *Service) waiterLoop(ctx context.Context, key string) (interface{}, error) {
	dataKey := cacheKeyMap(dataKeyType, key)
	// 1.阻塞进入等待
	err := d.waitForNotify(ctx, dataKey)
	if err != nil {
		log.Warnf(ctx, "run wait timeout for key %s,with error: %v", dataKey, err)
		// 2. 如果遇到报错，直接返回错误
		return nil, err
	}
	// 3. 通知到达，读取缓存并返回
	return d.getFromDistributedCache(ctx, dataKey)
}

func (d *Service) ttlKeeper(ctx context.Context) {
	refreshInterval := d.conf.freshLock
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			var lockKeys []string
			d.winnerMap.Range(func(key, value interface{}) bool {
				if keyStr, ok := key.(string); ok {
					lockKeys = append(lockKeys, cacheKeyMap(lockKeyType, keyStr))
				}
				return true
			})

			if len(lockKeys) > 0 {
				client := redis.Client()
				pipe := client.Pipeline()

				for _, lockKey := range lockKeys {
					pipe.Expire(ctx, lockKey, d.conf.lockTTL)
				}

				_, err := pipe.Exec(ctx)
				if err != nil {
					log.Warnf(ctx, "failed to batch refresh lock TTL for %d keys: %v", len(lockKeys), err)
				} else {
					log.Debugf(ctx, "successfully refreshed TTL for %d lock keys", len(lockKeys))
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

func (d *Service) getFromDistributedCache(ctx context.Context, key string) (interface{}, error) {
	cacheKey := cacheKeyMap(dataKeyType, key)
	valStr, err := redis.Get(ctx, cacheKey)
	if redis.IsNil(err) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	var res interface{}
	err = json.Unmarshal([]byte(valStr), &res)
	return res, err
}

func (d *Service) initialize(ctx context.Context) error {
	localCache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters:        viper.GetInt64(memcache.RistrettoNumCountersPath),
		MaxCost:            viper.GetInt64(memcache.RistrettoMaxCostPath),
		BufferItems:        viper.GetInt64(memcache.RistrettoBufferItemsPath),
		IgnoreInternalCost: viper.GetBool(memcache.RistrettoIgnoreInternalCostPath),
	})
	if err != nil {
		return err
	}

	slowQueryThreshold := viper.GetDuration(http.SlowQueryThresholdConfigPath)
	readTimeout := viper.GetDuration(http.ReadTimeOutConfigPath)

	d.conf = Config{
		executeTTL: slowQueryThreshold,
		payloadTTL: readTimeout,
		lockTTL:    slowQueryThreshold * 2,
		freshLock:  slowQueryThreshold / 2,
	}
	// freshLock < executeTTL < lockTTL

	d.metrics = NewMetrics()

	maxInflight := viper.GetInt(http.QueryCacheMaxInflightConfigPath)
	maxFailures := viper.GetInt(http.QueryCacheMaxFailuresConfigPath)
	resetTimeout := viper.GetDuration(http.QueryCacheResetTimeoutConfigPath)
	d.resilience = NewResilienceManager(maxInflight, maxFailures, resetTimeout, d.metrics)

	d.ctx = ctx
	d.localCache = localCache
	go d.ttlKeeper(ctx)
	go d.subLoop(ctx)
	return nil
}

func (d *Service) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	return d.do(key, fn)
}

func (d *Service) do(key string, doQuery func() (interface{}, error)) (interface{}, error) {
	startTime := time.Now()
	metrics := d.metrics

	l1Start := time.Now()
	// 1. 尝试从本地缓存获取
	if val, found := d.getCacheFromLocal(key); found {
		metrics.recordCacheRequest("l1", "hit")
		metrics.recordCacheDuration("l1_read", time.Since(l1Start))
		return val, nil
	}
	metrics.recordCacheRequest("l1", "miss")
	// 2. 尝试从分布式缓存获取
	result, err := d.doDistributed(d.ctx, key, doQuery)
	metrics.recordCacheDuration("total_wait", time.Since(startTime))

	return result, err
}

type CachedResponse struct {
	CacheKey   string              `json:"cache_key"`
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
}

type CachePayload struct {
	req     interface{}
	spaceID string
	path    string
}

type responseWriter struct {
	gin.ResponseWriter
	buffer *bytes.Buffer
}

func (w *responseWriter) Write(data []byte) (int, error) {
	w.buffer.Write(data)
	return w.ResponseWriter.Write(data)
}

func isCacheEnabled() bool {
	return viper.GetBool(http.QueryCacheEnabledConfigPath)
}

func isSkipMethod(method string) bool {
	skipMethods := viper.GetStringSlice(http.QueryCacheSkipMethodsConfigPath)
	method = strings.ToUpper(method)
	for _, m := range skipMethods {
		if method == strings.ToUpper(m) {
			return true
		}
	}
	return false
}

func isSkipPath(path string) bool {
	skipPaths := viper.GetStringSlice(http.QueryCacheSkipPathsConfigPath)
	for _, skipPath := range skipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}
	return false
}

// CacheMiddleware 返回缓存中间件
func (d *Service) CacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isCacheEnabled() {
			c.Next()
			return
		}

		if isSkipPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		if isSkipMethod(c.Request.Method) {
			c.Next()
			return
		}

		doQuery := func(key string, c *gin.Context) (interface{}, error) {
			writer := &responseWriter{
				ResponseWriter: c.Writer,
				buffer:         bytes.NewBuffer(nil),
			}
			originWriter := c.Writer
			c.Writer = writer
			c.Next()
			c.Writer = originWriter
			isSuccess := c.Writer.Status() >= 200 && c.Writer.Status() < 300
			if isSuccess {
				return CachedResponse{
					CacheKey:   key,
					StatusCode: c.Writer.Status(),
					Headers:    c.Writer.Header(),
					Body:       writer.buffer.Bytes(),
				}, nil
			} else {
				return nil, fmt.Errorf("non-success status code: %d", c.Writer.Status())
			}
		}

		cacheKey := generateCacheKey(c)
		result, err := d.do(cacheKey, func() (interface{}, error) {
			return doQuery(cacheKey, c)
		})

		if err != nil {
			log.Warnf(c.Request.Context(), "cache error: %v", err)
			c.Next()
			return
		}

		if cachedResp, ok := result.(*CachedResponse); ok {
			d.serveCachedResponse(c, cachedResp)
			return
		}

		c.Next()
	}
}

// getCachedResponse 获取缓存的响应
func (d *Service) getCachedResponse(c *gin.Context, key string) *CachedResponse {
	dataKey := cacheKeyMap(dataKeyType, key)
	// 直接从 L1 缓存查询
	if val, found := d.getCacheFromLocal(dataKey); found {
		if cachedResp, ok := val.(*CachedResponse); ok {
			d.metrics.recordCacheRequest("l1", "hit")
			return cachedResp
		}
		d.metrics.recordCacheRequest("l1", "error")
	}

	d.metrics.recordCacheRequest("l1", "miss")

	ctx := c.Request.Context()
	l2Start := time.Now()
	result, err := d.getFromDistributedCache(ctx, dataKey)
	d.metrics.recordCacheDuration("l2_read", time.Since(l2Start))

	if err != nil {
		// 缓存未命中
		d.metrics.recordCacheRequest("l2", "miss")
		return nil
	}

	cachedResp, ok := result.(*CachedResponse)
	if !ok {
		d.metrics.recordCacheRequest("l2", "error")
		return nil
	}

	d.metrics.recordCacheRequest("l2", "hit")

	// 回填 L1 缓存
	d.setCacheToLocal(dataKey, cachedResp)

	return cachedResp
}

func (d *Service) serveCachedResponse(c *gin.Context, cachedResp *CachedResponse) {
	for key, values := range cachedResp.Headers {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Status(cachedResp.StatusCode)
	c.Writer.Write(cachedResp.Body)
	log.Debugf(c.Request.Context(), "cache hit for key: %s", cachedResp.CacheKey)
}

func (d *Service) writeRemote(ctx context.Context, key string, cachedResp *CachedResponse) error {
	cacheKey := cacheKeyMap(dataKeyType, key)
	channelKey := cacheKeyMap(channelKeyType, key)
	bts, err := json.Marshal(cachedResp)
	if err != nil {
		return err
	}

	err = d.writeDistributedCache(ctx, cacheKey, bts)
	if err != nil {
		return fmt.Errorf("failed to write cache with limit control: %w", err)
	}

	_, err = redis.Publish(ctx, channelKey, doneMsg)
	if err != nil {
		log.Warnf(ctx, "failed to publish completion for key %s: %v", cacheKey, err)
	}

	return nil
}
