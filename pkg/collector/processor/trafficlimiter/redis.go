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

	// 建连超时。Redis 暂时不可达时，这次同步失败，不阻塞 Collector 启动，也不阻塞任何业务请求。
	defaultRedisDialTimeout = 500 * time.Millisecond

	// 读写与连接池等待超时。只有后台同步访问 Redis，与整次同步的上限保持一致即可。
	defaultRedisIOTimeout = defaultSyncTimeout

	// 每进程 Redis 连接上限。同一时刻只有一个同步 goroutine 在访问 Redis，
	// 多留一条给 Reload 换客户端时新旧同步短暂重叠。
	defaultRedisPoolSize = 2

	// Redis 故障 warn 的全进程限频间隔。
	redisFailureLogInterval = time.Minute
)

// redisLeaseScript 按 GCRA 为一批服务授予额度，使用 Redis TIME 统一各 Collector Pod 的时间源。
// KEYS[i] 是各服务的状态 Key，ARGV 每 3 个一组：速率、容量（见 Config.leaseCapacity）、申请量。
// 每个服务授予 min(申请量, 桶中当前可用字节)，桶空时授予 0；返回各服务的授予量。
// Key 只在授予大于 0 时写入，并在欠账完全恢复后自动过期。
const redisLeaseScript = `
if #ARGV ~= #KEYS * 3 then
  return redis.error_reply("invalid lease arguments")
end
local now_parts = redis.call("TIME")
local now = tonumber(now_parts[1]) * 1000000 + tonumber(now_parts[2])
local grants = {}
for i = 1, #KEYS do
  local base = (i - 1) * 3
  local rate = tonumber(ARGV[base + 1])
  local burst = tonumber(ARGV[base + 2])
  local want = tonumber(ARGV[base + 3])
  if rate == nil or burst == nil or want == nil or rate <= 0 or burst <= 0 or want < 0 then
    return redis.error_reply("invalid lease arguments")
  end

  local tat = tonumber(redis.call("GET", KEYS[i]))
  if tat == nil or tat < now then
    tat = now
  end
  local burst_interval = math.ceil(burst * 1000000 / rate)
  local available = math.floor((now + burst_interval - tat) * rate / 1000000)
  local grant = math.min(want, available)
  if grant > 0 then
    local new_tat = tat + math.ceil(grant * 1000000 / rate)
    local ttl = math.ceil((new_tat - now) / 1000)
    if ttl < 1 then
      ttl = 1
    end
    redis.call("SET", KEYS[i], new_tat, "PX", ttl)
  else
    grant = 0
  end
  grants[i] = grant
end
return grants
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

type redisLeaser struct {
	client redis.UniversalClient
	script *redis.Script
	key    string
}

func newRedisLeaser(config RedisConfig) quotaLeaser {
	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:    append([]string(nil), config.Addrs...),
		DB:       config.DB,
		Password: config.Password,
		// -1 在 go-redis v8 里表示关闭自动重试（0 会被改写成默认 3 次）。
		// Lua 已在 Redis 侧执行成功但客户端超时的情况下，重试会把同一批额度再领一次。
		MaxRetries:   -1,
		DialTimeout:  defaultRedisDialTimeout,
		ReadTimeout:  defaultRedisIOTimeout,
		WriteTimeout: defaultRedisIOTimeout,
		PoolTimeout:  defaultRedisIOTimeout,
		PoolSize:     defaultRedisPoolSize,
	})
	return &redisLeaser{
		client: client,
		script: redis.NewScript(redisLeaseScript),
		key:    config.Key,
	}
}

// Lease 用一次脚本调用为整批服务申请额度。
func (r *redisLeaser) Lease(ctx context.Context, requests []leaseRequest) ([]int64, error) {
	keys := make([]string, len(requests))
	args := make([]any, 0, len(requests)*3)
	for i, request := range requests {
		keys[i] = r.stateKey(request.key.token, request.key.service, request.config)
		args = append(args, request.config.BytesPerSecond, request.config.leaseCapacity(), request.want)
	}
	return r.script.Run(ctx, r.client, keys, args...).Int64Slice()
}

func (r *redisLeaser) Close() error {
	return r.client.Close()
}

// stateKey 对 Token、服务名和额度做摘要，避免在 Redis 中暴露 Token 或服务名。
// 额度参与摘要后，配置变化会自动切换到一个新的满桶，旧 Key 等待动态 TTL 回收。
func (r *redisLeaser) stateKey(token, service string, config Config) string {
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

// fallbackDecision 把同步失败的原因分成几类可直接对应处置动作的桶，用 errors.Is / As 判断而不做字符串匹配：
//
//	closed  → Reload 换客户端的窗口内旧同步被关闭，应极少且短暂，通常无需处理
//	timeout → 查 Redis 负载与网络
//	error   → 看日志，Redis < 5 的脚本报错落在这里
//
// 未配置可用 Redis 时没有同步，请求路径直接记 no_client，不经过这里。
func fallbackDecision(err error) prometheus.Counter {
	switch {
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
	logger.Warnf("traffic limiter failed to sync quota with redis: %v", err)
}
