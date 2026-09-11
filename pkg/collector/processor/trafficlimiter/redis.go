// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trafficlimiter

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	redisModeStandalone = "standalone"

	// 建连超时。只影响第一次从连接池拿出空闲连接去握手的那次 Dial，
	// 不计入下面的脚本 ctx。Redis 暂时不可达时，这次 Allow 失败并降级本地，不阻塞 Collector 启动。
	defaultRedisDialTimeout = 500 * time.Millisecond

	// 单次读写超时（go-redis ReadTimeout / WriteTimeout）。Lua 是一次 EVAL：先写脚本再读返回值，
	// 所以一次 Allow 可能连续撞上这两个超时。
	defaultRedisIOTimeout = 100 * time.Millisecond

	// 连接池借不到连接时最多等这么久。超时后本次 Allow 报错并降级本地，避免 PreCheck 把请求 goroutine 卡死。
	defaultRedisPoolTimeout = 100 * time.Millisecond

	// 整次 Lua 调用（含排队、EVAL、等返回）的 ctx 上限。这是用户请求回包前同步等待 Redis 的硬顶。
	// 它大于单次 IO 超时，是为了覆盖「借连接 + 写 + 读」整段，而不是把 Dial 500ms 也叠进去。
	defaultRedisOperationTime = 200 * time.Millisecond

	// 每进程 Redis 连接上限。额度打开后每个启用限流的请求最多占用一条连接直到 Lua 返回；
	// 池满后新请求走 PoolTimeout，再失败则本地 GCRA。64 与 Collector 常见并发同量级，避免无限建连打满 Redis。
	defaultRedisPoolSize = 64

	// Redis 故障 warn 的全进程限频间隔。降级路径可能每个超限请求都走到，不能按请求打日志。
	redisFailureLogInterval = time.Minute
)

// redisGCRAScript 使用 Redis TIME 统一各 Collector Pod 的时间源。
// Key 只在成功扣减时写入，并在欠账完全恢复后自动过期；拒绝不会延长状态生命周期。
const redisGCRAScript = `
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
if rate == nil or burst == nil or cost == nil or rate <= 0 or burst <= 0 or cost < 0 then
  return redis.error_reply("invalid gcra arguments")
end
if cost > burst then
  return 0
end

local now_parts = redis.call("TIME")
local now = tonumber(now_parts[1]) * 1000000 + tonumber(now_parts[2])
local tat = tonumber(redis.call("GET", KEYS[1]))
if tat == nil or tat < now then
  tat = now
end

local increment = math.ceil(cost * 1000000 / rate)
local burst_interval = math.ceil(burst * 1000000 / rate)
if tat - now > burst_interval - increment then
  return 0
end

local new_tat = tat + increment
local ttl = math.ceil((new_tat - now) / 1000)
if ttl < 1 then
  ttl = 1
end
redis.call("SET", KEYS[1], new_tat, "PX", ttl)
return 1
`

type RedisConfig struct {
	Mode     string   `config:"mode" mapstructure:"mode"`
	DB       int      `config:"db" mapstructure:"db"`
	Key      string   `config:"key" mapstructure:"key"`
	Addrs    []string `config:"addrs" mapstructure:"addrs"`
	Password string   `config:"password" mapstructure:"password"`
}

func (c RedisConfig) normalized() RedisConfig {
	c.Mode = strings.TrimSpace(c.Mode)
	c.Key = strings.TrimSuffix(strings.TrimSpace(c.Key), ":")
	for i := range c.Addrs {
		c.Addrs[i] = strings.TrimSpace(c.Addrs[i])
	}
	return c
}

// empty 表示主配置里完全没有出现 redis 段，此时整个处理器空转，等同于额度为 0。
// 与「配错了」区分开：只要填了任意一个字段就仍然走 Validate，避免拼错字段名被当成未启用。
func (c RedisConfig) empty() bool {
	return c.Mode == "" && c.Key == "" && c.DB == 0 && c.Password == "" && len(c.Addrs) == 0
}

func (c RedisConfig) Validate() error {
	c = c.normalized()
	if c.Mode != redisModeStandalone {
		return errors.Errorf("traffic limiter redis mode must be %q: %q", redisModeStandalone, c.Mode)
	}
	if c.DB < 0 {
		return errors.Errorf("traffic limiter redis db must not be negative: %d", c.DB)
	}
	if c.Key == "" {
		return errors.New("traffic limiter redis key must not be empty")
	}
	if len(c.Addrs) != 1 || c.Addrs[0] == "" {
		return errors.New("traffic limiter standalone redis requires exactly one non-empty address")
	}
	return nil
}

// distributedGCRA 的 cost 是经过权重折算后的计费字节，不是原始逻辑字节。
type distributedGCRA interface {
	Allow(token, service string, cost int64, config Config) (bool, error)
	Close() error
}

type redisGCRA struct {
	client redis.UniversalClient
	script *redis.Script
	key    string
}

func newRedisGCRA(config RedisConfig) distributedGCRA {
	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:    append([]string(nil), config.Addrs...),
		DB:       config.DB,
		Password: config.Password,
		// -1 在 go-redis v8 里表示关闭自动重试（0 会被改写成默认 3 次）。
		// Lua 已在 Redis 侧执行成功但客户端超时的情况下，重试会把同一笔流量再扣一次。
		MaxRetries:   -1,
		DialTimeout:  defaultRedisDialTimeout,
		ReadTimeout:  defaultRedisIOTimeout,
		WriteTimeout: defaultRedisIOTimeout,
		PoolTimeout:  defaultRedisPoolTimeout,
		PoolSize:     defaultRedisPoolSize,
	})
	return &redisGCRA{
		client: client,
		script: redis.NewScript(redisGCRAScript),
		key:    config.Key,
	}
}

func (r *redisGCRA) Allow(token, service string, cost int64, config Config) (bool, error) {
	if cost > config.BurstBytes {
		return false, nil
	} // 通过gcra语义快速判断，不走lua脚本
	ctx, cancel := context.WithTimeout(context.Background(), defaultRedisOperationTime)
	defer cancel()

	result, err := r.script.Run(
		ctx,
		r.client,
		[]string{r.stateKey(token, service, config)},
		config.BytesPerSecond,
		config.BurstBytes,
		cost,
	).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (r *redisGCRA) Close() error {
	return r.client.Close()
}

// stateKey 对 Token、服务名和额度做摘要，避免在 Redis 中暴露 Token 或服务名。
// 额度参与摘要后，配置变化会自动切换到一个新的满桶，旧 Key 等待动态 TTL 回收。
func (r *redisGCRA) stateKey(token, service string, config Config) string {
	hash := sha256.New()
	writeHashString(hash, token)
	writeHashString(hash, service)
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], uint64(config.BytesPerSecond))
	_, _ = hash.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(config.BurstBytes))
	_, _ = hash.Write(number[:])
	return r.key + ":" + hex.EncodeToString(hash.Sum(nil))
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashString(hash hashWriter, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(value))
}

// fallbackDecision 把降级原因分成几类可直接对应处置动作的桶，用 errors.Is / As 判断而不做字符串匹配：
//
//	no_client → 查配置（redis 段非法，或实例刚 Clean 尚未重建）
//	closed    → Reload 窗口内用了旧快照的客户端，应极少且短暂，通常无需处理
//	timeout   → 查 Redis 负载与网络
//	error     → 看日志，Redis < 5 的脚本报错落在这里
//
// 已知缺口：连接池等待超时（PoolTimeout）也会落进 error。go-redis v8 把 ErrPoolTimeout
// 放在 internal/pool 中，外部包引用不到，只导出了 ErrClosed。池压力需要另行暴露 PoolStats。
func fallbackDecision(err error) prometheus.Counter {
	switch {
	case errors.Is(err, errNoRedisClient):
		return decisionLocalNoClient
	case errors.Is(err, redis.ErrClosed):
		return decisionLocalClosed
	case errors.Is(err, context.DeadlineExceeded):
		return decisionLocalTimeout
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return decisionLocalTimeout
	}
	return decisionLocalError
}

var lastRedisFailureLog atomic.Int64

func warnRedisFailure(err error) {
	now := time.Now().UnixNano()
	previous := lastRedisFailureLog.Load()
	if now-previous < redisFailureLogInterval.Nanoseconds() ||
		!lastRedisFailureLog.CompareAndSwap(previous, now) {
		return
	}
	logger.Warnf("traffic limiter redis unavailable, fallback to local GCRA: %v", err)
}
