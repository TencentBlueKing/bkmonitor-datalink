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
	"fmt"
	"math/rand/v2"
	"net"
	"os"
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

	// 读写与连接池等待超时。只有补货泵访问 Redis，与单轮租约的上限保持一致即可；
	// 等待补货的请求有自己更短的等待上限，不受它影响。
	defaultRedisIOTimeout = defaultSyncTimeout

	// 每进程 Redis 连接上限。同一时刻只有一个补货泵调用在途，
	// 多留一条给 Reload 换客户端时新旧补货泵短暂重叠。
	defaultRedisPoolSize = 2

	// Redis 故障 warn 的全进程限频间隔。
	redisFailureLogInterval = time.Minute
)

// redisLeaseScript 为一批服务处理租约，使用 Redis TIME 统一各 Collector Pod 的时间源。
//
// 每个服务两个 Key：KEYS[2i-1] 是共享 GCRA 状态（TAT，微秒），KEYS[2i] 是需求表
// （Hash，字段为实例 ID，值为「需求速率,上报时间」）。ARGV[1..4] 是实例 ID、同步周期、需求失联时长、
// 预取窗口（后三者为微秒），其后每服务 4 个参数：速率、容量（见 Config.leaseCapacity）、
// 申请量（负数表示退还）、本实例需求速率。
//
// 对每个服务：
//  1. 用本实例的需求覆盖写需求表，剔除失联实例，得到全部存活实例的需求与总需求 D；
//  2. D ≤ 速率时不紧张，授予 min(申请量, 桶中可用量)，公平份额为「需求 + 剩余额度均分」；
//  3. D > 速率时按 max-min 算出本实例的公平速率 f，授予量另受封顶：
//     max(f × 距本实例上次租约的时长, 桶中可用量 − 其他实例公平份额的预取量)。
//     前者保证先同步的实例只能拿走自己那份；后者在桶里还有富余时允许多拿，
//     只要给其他实例留足各自的预取。D 来自各实例最近的上报，需求刚下降的实例要一会儿才会更新，
//     这段时间 D 偏高、看起来像争抢，若只按前者封顶，限额之内突增的实例会被白白卡住。
//  4. 退还时把 TAT 回拨，不早于当前时间。
//
// 返回每个服务的 {授予量, 存活实例数, 公平份额}。时间戳与 TAT 用 %.0f 写入，
// 避免 Lua number 转字符串时按 %.14g 截断微秒精度。
const redisLeaseScript = `
local id = ARGV[1]
local period = tonumber(ARGV[2])
local stale = tonumber(ARGV[3])
local prefetch = tonumber(ARGV[4])
local count = #KEYS / 2
if #KEYS % 2 ~= 0 or period == nil or stale == nil or prefetch == nil or #ARGV ~= 4 + count * 4 then
  return redis.error_reply("invalid lease arguments")
end
local now_parts = redis.call("TIME")
local now = tonumber(now_parts[1]) * 1000000 + tonumber(now_parts[2])
local result = {}
for i = 1, count do
  local tat_key = KEYS[i * 2 - 1]
  local demand_key = KEYS[i * 2]
  local base = 4 + (i - 1) * 4
  local rate = tonumber(ARGV[base + 1])
  local capacity = tonumber(ARGV[base + 2])
  local want = tonumber(ARGV[base + 3])
  local demand = tonumber(ARGV[base + 4])
  if rate == nil or capacity == nil or want == nil or demand == nil or rate <= 0 or capacity <= 0 or demand < 0 then
    return redis.error_reply("invalid lease arguments")
  end
  if demand > rate then
    demand = rate
  end

  local demands = {demand}
  local total = demand
  local last = nil
  local entries = redis.call("HGETALL", demand_key)
  for j = 1, #entries, 2 do
    local field = entries[j]
    local value = entries[j + 1]
    local sep = string.find(value, ",", 1, true)
    local d = nil
    local seen = nil
    if sep ~= nil then
      d = tonumber(string.sub(value, 1, sep - 1))
      seen = tonumber(string.sub(value, sep + 1))
    end
    if field == id then
      last = seen
    elseif d == nil or seen == nil or now - seen > stale then
      redis.call("HDEL", demand_key, field)
    else
      demands[#demands + 1] = d
      total = total + d
    end
  end
  local peers = #demands

  local share = demand + (rate - total) / peers
  if total > rate then
    table.sort(demands)
    local remaining = rate
    local left = peers
    local level = rate
    for _, d in ipairs(demands) do
      level = remaining / left
      if d > level then
        break
      end
      remaining = remaining - d
      left = left - 1
    end
    share = math.min(demand, level)
  end

  local tat = tonumber(redis.call("GET", tat_key))
  if tat == nil or tat < now then
    tat = now
  end
  local grant = 0
  if want < 0 then
    tat = math.max(now, tat - math.floor(-want * 1000000 / rate))
    grant = want
  elseif want > 0 then
    local available = math.floor((now + math.ceil(capacity * 1000000 / rate) - tat) * rate / 1000000)
    local limit = want
    if total > rate then
      local elapsed = period * 2
      if last ~= nil and now - last < elapsed then
        elapsed = now - last
      end
      limit = math.floor(share * elapsed / 1000000)
      local spare = available - math.ceil((rate - share) * prefetch / 1000000)
      if spare > limit then
        limit = spare
      end
    end
    grant = math.min(want, available, limit)
    if grant < 0 then
      grant = 0
    end
    tat = tat + math.ceil(grant * 1000000 / rate)
  end
  if grant ~= 0 then
    if tat > now then
      redis.call("SET", tat_key, string.format("%.0f", tat), "PX", math.ceil((tat - now) / 1000))
    else
      redis.call("DEL", tat_key)
    end
  end

  if demand > 0 or want ~= 0 then
    redis.call("HSET", demand_key, id, string.format("%.0f,%.0f", demand, now))
  else
    redis.call("HDEL", demand_key, id)
  end
  redis.call("PEXPIRE", demand_key, math.ceil(stale / 1000) * 5)

  result[#result + 1] = grant
  result[#result + 1] = peers
  result[#result + 1] = math.floor(share)
end
return result
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

// instanceID 标识本进程，只用在 Redis 需求表里区分各个 Collector。
// 只需要在存活实例之间唯一，不需要跨重启稳定：重启后旧 ID 在 defaultDemandStaleAfter 后过期。
// 必须唯一：两个实例共用一个 ID 会互相覆盖需求，各自按一个实例的份额放行，总量随之超额。
// 同一进程内 Reload 前后的客户端共用 ID，避免新旧客户端短暂重叠时被当成两个实例。
var instanceID = newInstanceID()

func newInstanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%08x", host, rand.Uint32())
}

type redisLeaser struct {
	client redis.UniversalClient
	script *redis.Script
	key    string
	id     string
}

func newRedisLeaser(config RedisConfig) quotaLeaser {
	return newRedisLeaserWithID(config, instanceID)
}

func newRedisLeaserWithID(config RedisConfig, id string) quotaLeaser {
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
		id:     id,
	}
}

// Lease 用一次脚本调用处理整批服务。批次为空时只执行一次 TIME，用作健康探测。
func (r *redisLeaser) Lease(ctx context.Context, requests []leaseRequest) ([]leaseResult, error) {
	keys := make([]string, 0, len(requests)*2)
	args := make([]any, 0, 4+len(requests)*4)
	args = append(args, r.id, defaultSyncInterval.Microseconds(), defaultDemandStaleAfter.Microseconds(),
		defaultPrefetchWindow.Microseconds())
	for _, request := range requests {
		key := r.stateKey(request.key.token, request.key.service, request.config)
		keys = append(keys, key, key+":demand")
		args = append(args, request.config.BytesPerSecond, request.config.leaseCapacity(), request.want, request.demand)
	}
	values, err := r.script.Run(ctx, r.client, keys, args...).Int64Slice()
	if err != nil {
		return nil, err
	}
	if len(values) != len(requests)*3 {
		return nil, errors.Errorf("traffic limiter lease script returned %d values for %d requests", len(values), len(requests))
	}
	results := make([]leaseResult, len(requests))
	for i := range results {
		results[i] = leaseResult{grant: values[i*3], peers: values[i*3+1], share: values[i*3+2]}
	}
	return results, nil
}

func (r *redisLeaser) Close() error {
	return r.client.Close()
}

// stateKey 对 Token、服务名和额度做摘要，避免在 Redis 中暴露 Token 或服务名。
// 额度参与摘要后，配置变化会自动切换到一个新的满桶与新的需求表，旧 Key 等待 TTL 回收。
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
