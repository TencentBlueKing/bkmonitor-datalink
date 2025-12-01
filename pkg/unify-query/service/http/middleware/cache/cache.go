package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/memcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/dgraph-io/ristretto"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

const (
	locked  = "1"
	doneMsg = "done"
)

func initConf() Config {
	writeTimeout := viper.GetDuration("http.write_timeout")
	skipPaths := viper.GetStringSlice("http.query_cache.skip_paths")

	return Config{
		executeTTL:   writeTimeout,
		payloadTTL:   writeTimeout,
		lockTTL:      writeTimeout * 2,
		freshLock:    writeTimeout / 2,
		skipMethods:  viper.GetStringSlice("http.query_cache.skip_methods"),
		skipPaths:    skipPaths,
		bucketLimit:  viper.GetInt64("http.query_cache.default_limit"),
		cacheEnabled: viper.GetBool("http.query_cache.enabled"),
	}
}

func NewInstance(ctx context.Context) (*Service, error) {
	service := &Service{}
	err := service.initialize(ctx, initConf())
	if err != nil {
		return nil, err
	}
	log.Infof(ctx, "cache middleware initialized successfully")
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

func (d *Service) doDistributed(ctx context.Context, key string, doQuery func() (interface{}, string, error)) (result interface{}, hit bool, err error) {
	var acquired bool

	ctx, span := trace.NewSpan(ctx, "cache-middleware-do-distributed")
	defer span.End(&err)

	lockKey := cacheKeyMap(lockKeyType, key)

	span.Set("lock-key", lockKey)

	result, hit, err = d.getFromDistributedCache(ctx, key)
	if err != nil {
		return result, hit, err
	}
	if hit {
		d.setCacheToLocal(key, result)
		return result, hit, err
	}

	// 1. 尝试从Redis获取分布式锁
	acquired, err = redis.SetNX(ctx, lockKey, locked, d.conf.lockTTL)
	if err != nil {
		return result, hit, err
	}

	span.Set("distributed-lock-acquired", acquired)

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
		result, err = d.runAndNotify(ctx, key, doQuery)
		return result, hit, err
	} else {
		// 2.2 锁获取失败，成为 Cluster Waiter
		// 2.2.1 进入等待循环
		result, err = d.waiterLoop(ctx, key)
		return result, hit, err
	}
}

func (d *Service) runAndNotify(ctx context.Context, key string, doQuery func() (interface{}, string, error)) (result interface{}, err error) {
	ctx, span := trace.NewSpan(ctx, "cache-middleware-run-and-notify")
	defer span.End(&err)

	dataKey := cacheKeyMap(dataKeyType, key)
	channelKey := cacheKeyMap(channelKeyType, key)

	span.Set("data-key", dataKey)
	span.Set("channel-key", channelKey)

	// 1. 执行函数获取结果
	result, _, err = doQuery()
	if err != nil {
		return result, err
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

	span.Set("cache-written", true)

	// 3. 通知等待者
	_, err = redis.Publish(ctx, channelKey, doneMsg)
	if err != nil {
		log.Warnf(ctx, "failed to publish completion for key %s: %v", dataKey, err)
		return nil, err
	}

	span.Set("cache-notify-published", true)

	return result, nil
}

func (d *Service) waiterLoop(ctx context.Context, key string) (result interface{}, err error) {
	ctx, span := trace.NewSpan(ctx, "cache-middleware-waiter-loop")
	defer span.End(&err)

	dataKey := cacheKeyMap(dataKeyType, key)

	span.Set("data-key", dataKey)

	// 1.阻塞进入等待
	err = d.waitForNotify(ctx, dataKey)
	if err != nil {
		return result, err
	}
	// 3. 通知到达，读取缓存并返回
	result, _, err = d.getFromDistributedCache(ctx, dataKey)

	return result, err
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

func (d *Service) getFromDistributedCache(ctx context.Context, key string) (result interface{}, hit bool, err error) {
	ctx, span := trace.NewSpan(ctx, "cache-middleware-get-distributed-cache")
	defer span.End(&err)
	cacheKey := cacheKeyMap(dataKeyType, key)

	span.Set("data-key", cacheKey)

	valStr, err := redis.Get(ctx, cacheKey)
	if err != nil {
		// 如果是缓存未命中，则不返回错误
		missing := redis.IsNil(err)
		err = nil

		if missing {
			err = nil
		}

		return result, hit, err
	}
	if redis.IsNil(err) {
		span.Set("hit-distributed", false)

		// 缓存未命中
		err = nil
		return result, hit, err
	}

	if err != nil {
		return result, hit, err
	}

	span.Set("hit-distributed", true)
	err = json.Unmarshal([]byte(valStr), &result)

	hit = true
	return result, hit, err
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

func (d *Service) do(ctx context.Context, key string, doQuery func() (interface{}, string, error)) (result interface{}, crossReason string, err error) {
	ctx, span := trace.NewSpan(ctx, "cache-middleware-do")
	defer span.End(&err)

	var (
		hitLocal       bool
		hitDistributed bool
	)

	// 1. 尝试从本地缓存获取
	result, hitLocal = d.getCacheFromLocal(key)

	span.Set("key", key)
	span.Set("hit-local", hitLocal)

	if hitLocal {
		crossReason = crossByL1CacheHit
		return result, crossReason, err
	}

	// 2. 尝试从分布式缓存获取
	result, hitDistributed, err = d.doDistributed(d.ctx, key, doQuery)
	if err != nil {
		return result, crossReason, err
	}

	if hitDistributed {
		crossReason = crossByL2CacheHit
	}

	return result, crossReason, err
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

func skipPath(path string, skipPaths []string) bool {
	for _, p := range skipPaths {
		if path == p {
			return true
		}

		if strings.Contains(p, "*") {
			if matchWildcard(path, p) {
				return true
			}
		}
	}
	return false
}

func matchWildcard(path, pattern string) bool {
	regexPattern := "^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), "\\*", ".*") + "$"
	matched, _ := regexp.MatchString(regexPattern, path)
	return matched
}

func (d *Service) isSkipPath(path string) bool {
	return skipPath(path, d.conf.skipPaths)
}

func (d *Service) isSkipMethod(method string) bool {
	for _, skipMethod := range d.conf.skipMethods {
		if method == skipMethod {
			return true
		}
	}
	return false
}

const (
	crossByNotEnabled  = "cache-cross-by-not-enabled"
	crossBySkipPath    = "cache-cross-by-skip-path"
	crossBySkipMethod  = "cache-cross-by-skip-method"
	crossByL1CacheHit  = "cache-cross-by-cache-l1-hit"
	crossByL2CacheHit  = "cache-cross-by-cache-l2-hit"
	crossByClientError = "cache-cross-by-client-error"
	crossByServerError = "cache-cross-by-server-error"
	crossBySuccess     = "cache-cross-by-success"
)

// CacheMiddleware 返回缓存中间件
func (d *Service) CacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var (
			result      interface{}
			err         error
			crossReason string
		)

		ctx, span := trace.NewSpan(c.Request.Context(), "cache-middleware")
		defer span.End(&err)

		defer func() {
			span.Set("cache-middleware-cross-reason", crossReason)
		}()

		span.Set("cache-enabled", d.conf.cacheEnabled)
		span.Set("cache-skip-path", d.conf.skipPaths)
		span.Set("cache-skip-methods", d.conf.skipMethods)

		if !d.conf.cacheEnabled {
			crossReason = crossByNotEnabled
			c.Next()
			return
		}

		if d.isSkipPath(c.Request.URL.Path) {
			crossReason = crossBySkipPath
			c.Next()
			return
		}

		if d.isSkipMethod(c.Request.Method) {
			crossReason = crossBySkipMethod
			c.Next()
			return
		}

		doQuery := func(key string, c *gin.Context) (result interface{}, crossReason string, err error) {
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
				result = CachedResponse{
					CacheKey:   key,
					StatusCode: c.Writer.Status(),
					Headers:    c.Writer.Header(),
					Body:       writer.buffer.Bytes(),
				}
				crossReason = crossBySuccess
				return result, crossReason, err
			} else {
				if c.Writer.Status() >= 400 && c.Writer.Status() < 500 {
					crossReason = crossByClientError
				}
				if c.Writer.Status() >= 500 {
					crossReason = crossByServerError
				}

				return result, crossReason, err
			}
		}

		cacheKey, err := generateCacheKey(c)
		if err != nil {
			log.Warnf(ctx, "failed to generate cache key: %v", err)
			c.AbortWithError(400, fmt.Errorf("failed to generate cache key: %v", err))
			return
		}

		span.Set("cache-key", cacheKey)

		result, crossReason, err = d.do(c.Request.Context(), cacheKey, func() (interface{}, string, error) {
			return doQuery(cacheKey, c)
		})
		if err != nil {
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

func (d *Service) serveCachedResponse(c *gin.Context, cachedResp *CachedResponse) {
	for key, values := range cachedResp.Headers {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Status(cachedResp.StatusCode)
	c.Writer.Write(cachedResp.Body)
}
