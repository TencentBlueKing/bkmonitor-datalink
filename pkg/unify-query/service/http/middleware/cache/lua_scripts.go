package cache

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	goRedis "github.com/go-redis/redis/v8"
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
)

func (d *Service) writeLimitedDistributedCache(ctx context.Context, dataKey string, data []byte) (err error) {
	ctx, span := trace.NewSpan(ctx, "write-limited-distributed-cache")
	defer span.End(&err)

	// 1. 生成正确的Redis键名
	redisDataKey := cacheKeyMap(dataKeyType, dataKey)
	indexKey := cacheKeyMap(indexKeyType, "")
	limitConfigKey := cacheKeyMap(limitKeyType, "")
	timestamp := time.Now().UnixNano()

	span.Set("data-key", redisDataKey)
	span.Set("index-key", indexKey)
	span.Set("limit-config-key", limitConfigKey)
	span.Set("timestamp", timestamp)

	script := goRedis.NewScript(cacheWriteWithLimitScript)
	// 2. 修复参数列表，使用正确的键名
	_, err = redis.ExecLua(ctx, script, []string{redisDataKey, indexKey, limitConfigKey},
		string(data), int(d.conf.payloadTTL.Seconds()), timestamp, d.conf.bucketLimit)

	if err != nil {
		return
	}

	return
}
