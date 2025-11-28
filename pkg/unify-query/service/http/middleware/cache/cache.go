package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/dgraph-io/ristretto"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

const (
	locked  = "1"
	doneMsg = "done"
)

func initConf() Config {
	slowQueryThreshold := viper.GetDuration("http.slow_query_threshold")
	readTimeout := viper.GetDuration("http.read_timeout")
	return Config{
		executeTTL:   slowQueryThreshold,
		payloadTTL:   readTimeout,
		lockTTL:      slowQueryThreshold * 2,
		freshLock:    slowQueryThreshold / 2,
		skipMethods:  viper.GetStringSlice("http.query_cache.skip_methods"),
		skipPaths:    viper.GetStringSlice("http.query_cache.skip_paths"),
		bucketLimit:  viper.GetInt64("cache.bucket_limit"),
		cacheEnabled: viper.GetBool("cache.enabled"),
	}
}

func NewInstance(ctx context.Context) (*Service, error) {
	service := &Service{}
	err := service.initialize(ctx, initConf())
	if err != nil {
		return nil, err
	}
	return service, nil
}

type Service struct {
	ctx  context.Context
	conf Config

	localCache *ristretto.Cache

	winnerMap  map[string]string
	winnerLock sync.RWMutex
	waiterMap  map[string]*WaitGroupValue
	waiterLock sync.RWMutex
}

type Config struct {
	// executeTTL 是执行函数的最大允许时间
	executeTTL time.Duration
	// payloadTTL 是缓存数据的 TTL
	payloadTTL time.Duration
	// lockTTL 是分布式锁的 TTL
	lockTTL time.Duration
	// freshLock 是作为配置的锁的续期时间. 真实的刷新频率会是该值的一半
	freshLock    time.Duration
	skipMethods  []string
	skipPaths    []string
	bucketLimit  int64
	cacheEnabled bool
}

type WaitGroupValue struct {
	mu       sync.Mutex
	channels []chan struct{}
}

func (d *Service) getCacheFromLocal(key string) (interface{}, bool) {
	return d.localCache.Get(key)
}

func (d *Service) setCacheToLocal(key string, value interface{}) {
	d.localCache.SetWithTTL(key, value, 1, d.conf.payloadTTL)
}

func (d *Service) doDistributed(ctx context.Context, key string, doQuery func() (interface{}, error)) (interface{}, error) {
	lockKey := cacheKeyMap(lockKeyType, key)

	l2Result, l2Err := d.getFromDistributedCache(ctx, key)
	if l2Err != nil {
		return nil, l2Err
	}
	d.setCacheToLocal(key, l2Result)

	// 1. 尝试从Redis获取分布式锁
	acquired, lockErr := redis.SetNX(ctx, lockKey, locked, d.conf.lockTTL)
	if lockErr != nil {
		log.Warnf(ctx, "failed to acquire distributed lock for key %s: %v", lockKey, lockErr)
		return nil, lockErr
	}

	if acquired {
		// 2.1 锁获取成功，成为 Cluster Winner
		d.winnerLock.Lock()
		d.winnerMap[key] = key
		d.winnerLock.Unlock()

		defer func() {
			d.winnerLock.Lock()
			delete(d.winnerMap, key)
			d.winnerLock.Unlock()
		}()

		// 2.1.1 执行函数并通知等待者
		result, dbErr := d.runAndNotify(ctx, key, doQuery)

		return result, dbErr
	} else {
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
	err = d.writeLimitedDistributedCache(ctx, dataKey, bts)
	if err != nil {
		log.Warnf(ctx, "failed to write cache with limit control: %v", err)
		return nil, err
	}

	// 3. 通知等待者
	_, err = redis.Publish(ctx, channelKey, doneMsg)
	if err != nil {
		log.Warnf(ctx, "failed to publish completion for key %s: %v", dataKey, err)
		return nil, err
	}

	return result, nil
}

func (d *Service) waiterLoop(ctx context.Context, key string) (interface{}, error) {
	dataKey := cacheKeyMap(dataKeyType, key)
	// 1.阻塞进入等待
	err := d.waitForNotify(ctx, dataKey)
	if err != nil {
		log.Warnf(ctx, "cache wait timeout for key %s,with error: %v", dataKey, err)
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
			d.winnerLock.RLock()
			for keyStr := range d.winnerMap {
				lockKeys = append(lockKeys, cacheKeyMap(lockKeyType, keyStr))
			}

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
			d.winnerLock.RUnlock()
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

func (d *Service) initialize(ctx context.Context, conf Config) error {
	localCache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters:        viper.GetInt64(memcache.RistrettoNumCountersPath),
		MaxCost:            viper.GetInt64(memcache.RistrettoMaxCostPath),
		BufferItems:        viper.GetInt64(memcache.RistrettoBufferItemsPath),
		IgnoreInternalCost: viper.GetBool(memcache.RistrettoIgnoreInternalCostPath),
	})
	if err != nil {
		return err
	}

	d.conf = conf
	// freshLock < executeTTL < lockTTL

	d.ctx = ctx
	d.localCache = localCache
	d.winnerMap = make(map[string]string)
	d.waiterMap = make(map[string]*WaitGroupValue)
	go d.ttlKeeper(ctx)
	go d.subLoop(ctx)
	return nil
}

func (d *Service) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	return d.do(key, fn)
}

func (d *Service) do(key string, doQuery func() (interface{}, error)) (interface{}, error) {
	// 1. 尝试从本地缓存获取
	if val, found := d.getCacheFromLocal(key); found {
		return val, nil
	}
	// 2. 尝试从分布式缓存获取
	result, err := d.doDistributed(d.ctx, key, doQuery)
	return result, err
}

type CachedResponse struct {
	CacheKey   string              `json:"cache_key"`
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"body"`
}

type Payload struct {
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

func (d *Service) isSkipPath(path string) bool {
	for _, skipPath := range d.conf.skipPaths {
		if path == skipPath {
			return true
		}
	}
	return false
}

func (d *Service) isSkipMethod(method string) bool {
	for _, skipMethod := range d.conf.skipMethods {
		if method == skipMethod {
			return true
		}
	}
	return false
}

// CacheMiddleware 返回缓存中间件
func (d *Service) CacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !d.conf.cacheEnabled {
			c.Next()
			return
		}

		if d.isSkipPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		if d.isSkipMethod(c.Request.Method) {
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

		cacheKey, err := generateCacheKey(c)
		if err != nil {
			log.Warnf(c.Request.Context(), "failed to generate cache key: %v", err)
			c.AbortWithError(400, fmt.Errorf("failed to generate cache key: %v", err))
			return
		}
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
			return cachedResp
		}
	}

	ctx := c.Request.Context()
	result, err := d.getFromDistributedCache(ctx, dataKey)

	if err != nil {
		// 缓存未命中
		return nil
	}

	cachedResp, ok := result.(*CachedResponse)
	if !ok {
		return nil
	}

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

	err = d.writeLimitedDistributedCache(ctx, cacheKey, bts)
	if err != nil {
		return fmt.Errorf("failed to write cache with limit control: %w", err)
	}

	_, err = redis.Publish(ctx, channelKey, doneMsg)
	if err != nil {
		log.Warnf(ctx, "failed to publish completion for key %s: %v", cacheKey, err)
	}

	return nil
}
