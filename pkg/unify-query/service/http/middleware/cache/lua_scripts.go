package cache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/spf13/viper"
)

const (
	cacheWriteWithLimitScript = `
-- 变量定义
local data_key = KEYS[1]
local index_key = KEYS[2]
local limit_config_key = KEYS[3]
local value = ARGV[1]
local ttl = tonumber(ARGV[2])
local timestamp = tonumber(ARGV[3])
local default_limit = tonumber(ARGV[4])

-- 1. 动态获取容量限制
local limit = tonumber(redis.call('GET', limit_config_key)) or default_limit

-- 2. 维护 LRU 索引
redis.call('ZADD', index_key, timestamp, data_key)
redis.call('SET', data_key, value, 'EX', ttl)

-- 3. 水位检查和清理
local count = redis.call('ZCARD', index_key)
if count > limit then
    local eviction_count = count - limit
    local candidates = redis.call('ZRANGE', index_key, 0, eviction_count - 1)

    if #candidates > 0 then
        redis.call('DEL', unpack(candidates))
        redis.call('ZREM', index_key, unpack(candidates))
    end
end

return 1
`

	// Redis Lua 脚本：获取当前缓存统计信息
	cacheStatsScript = `
local index_key = KEYS[1]
local count = redis.call('ZCARD', index_key)
return count
`
)

func (d *Service) writeRemoteCache(ctx context.Context, dataKey string, data []byte) error {
	// 1. 生成正确的Redis键名
	redisDataKey := cacheKeyMap(dataKeyType, dataKey)
	indexKey := cacheKeyMap(indexKeyType, "")
	limitConfigKey := cacheKeyMap(limitKeyType, "")

	timestamp := time.Now().UnixNano()
	defaultLimit := viper.GetInt64(http.QueryCacheDefaultLimitConfigPath)

	script := goRedis.NewScript(cacheWriteWithLimitScript)
	// 2. 修复参数列表，使用正确的键名
	_, err := redis.ExecLua(ctx, script, []string{redisDataKey, indexKey, limitConfigKey},
		string(data), int(d.conf.payloadTTL.Seconds()), timestamp, defaultLimit)

	if err != nil {
		log.Errorf(ctx, "failed to execute cache write lua script for key %s: %v", dataKey, err)
		return err
	}

	log.Debugf(ctx, "successfully wrote cache with limit control for key: %s", dataKey)
	return nil
}

func (d *Service) GetCacheStats(ctx context.Context) (int64, error) {
	// 1. 使用CacheKey生成系统索引键
	indexKey := cacheKeyMap(indexKeyType, "")

	script := goRedis.NewScript(cacheStatsScript)
	result, err := redis.ExecLua(ctx, script, []string{indexKey})
	if err != nil {
		return 0, err
	}

	count, ok := result.(int64)
	if !ok {
		return 0, fmt.Errorf("unexpected result type from cache stats script: %T", result)
	}

	return count, nil
}

func (d *Service) SetCacheLimit(ctx context.Context, limit int64) error {
	// 1. 使用CacheKey生成限制配置键
	limitKey := cacheKeyMap(limitKeyType, "")

	_, err := redis.Set(ctx, limitKey, fmt.Sprintf("%d", limit), 0) // 永久有效
	if err != nil {
		log.Errorf(ctx, "failed to set cache limit to %d: %v", limit, err)
		return err
	}

	log.Infof(ctx, "successfully set cache limit to %d", limit)
	return nil
}

func (d *Service) GetCacheLimit(ctx context.Context) (int64, error) {
	// 1. 使用CacheKey生成限制配置键
	limitKey := cacheKeyMap(limitKeyType, "")

	result, err := redis.Get(ctx, limitKey)
	if err != nil {
		return viper.GetInt64(http.QueryCacheDefaultLimitConfigPath), nil
	}

	limit, err := strconv.ParseInt(result, 10, 64)
	if err != nil {
		log.Warnf(ctx, "failed to parse cache limit from Redis, using default: %v", err)
		return viper.GetInt64(http.QueryCacheDefaultLimitConfigPath), nil
	}

	return limit, nil
}
