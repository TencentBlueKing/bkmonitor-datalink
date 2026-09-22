// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activeindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	redis "github.com/redis/go-redis/v9"
)

// MaxPending 限制每个目标中待刷新的不同策略数，溢出由周期发现恢复。
const MaxPending = 10000

// HintScript 只标记待刷新策略，不触碰正式集合。NX 保留最早执行时间，避免热门策略饥饿。
// token 是一次提示的并发身份，不是业务版本；仅在待处理期间保留。
const HintScript = `
if not redis.call('ZSCORE',KEYS[1],ARGV[1]) and (redis.call('ZCARD',KEYS[1]) >= 10000 or redis.call('HLEN',KEYS[2]) >= 10000) then return redis.error_reply('queue full') end
local t = redis.call('TIME')
local due = t[1]*1000 + math.floor(t[2]/1000) + tonumber(ARGV[3])
redis.call('HSET',KEYS[2],ARGV[1],ARGV[2])
redis.call('ZADD',KEYS[1],'NX',due,ARGV[1])
return 1
`

// HintKeys 返回独立于正式集合的合并队列键。
func HintKeys(prefix string) []string {
	root := MetadataPrefix(prefix)
	return []string{root + ":pending", root + ":tokens"}
}

// Status 保存最近完整发布与失败信息；不包含凭据或告警正文。
type Status struct {
	LastSuccess string `json:"last_success,omitempty"`
	LastAttempt string `json:"last_attempt"`
	Error       string `json:"error,omitempty"`
	Members     int    `json:"members"`
}

// Pending 是领取时的提示身份；提交仅确认相同身份，不吞掉扫描期间的新提示。
type Pending struct {
	Scope Scope
	Token string
}

// Cache 隔离 Redis 原子发布、租约和可合并提示；实现不得以部分读取覆盖集合。
type Cache interface {
	ReportDiscovery(context.Context, bool) error
	Discover(context.Context) ([]Scope, error)
	Enqueue(context.Context, Scope) error
	Pending(context.Context, int) ([]Pending, error)
	Acquire(context.Context, Scope, time.Duration) (string, error)
	Release(context.Context, Scope, string) error
	Publish(context.Context, Pending, string, []string) error
	Failed(context.Context, Pending, string, string) error
}

// RedisCache 持有目标客户端，连接由控制面装配层关闭。
type RedisCache struct {
	client            *redis.Client
	prefix            string
	maxRows, maxBytes int
}

// NewRedisCache 设置集合完整读取及构建的硬资源边界。
func NewRedisCache(client *redis.Client, prefix string, maxRows, maxBytes int) *RedisCache {
	return &RedisCache{client: client, prefix: prefix, maxRows: maxRows, maxBytes: maxBytes}
}

// Enqueue 合并提示；重复提示不延后最早刷新时间。
func (r *RedisCache) Enqueue(ctx context.Context, s Scope) error {
	if err := s.Validate(); err != nil {
		return err
	}
	return r.client.Eval(ctx, HintScript, HintKeys(r.prefix), s.Key(r.prefix), uuid.NewString(), 0).Err()
}

// Pending 有界读取到期策略及其提示身份，使用 Redis 时间避免控制面时钟偏差。
func (r *RedisCache) Pending(ctx context.Context, limit int) ([]Pending, error) {
	values, err := r.client.Eval(ctx, `local t=redis.call('TIME'); local keys=redis.call('ZRANGEBYSCORE',KEYS[1],'-inf',t[1]*1000+math.floor(t[2]/1000),'LIMIT',0,ARGV[1]); local out={}; for _,k in ipairs(keys) do table.insert(out,k); table.insert(out,redis.call('HGET',KEYS[2],k) or '') end; return out`, HintKeys(r.prefix), limit).StringSlice()
	if err != nil {
		return nil, err
	}
	result := make([]Pending, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		s, err := ParseKey(r.prefix, values[i])
		if err != nil {
			return nil, err
		}
		result = append(result, Pending{s, values[i+1]})
	}
	return result, nil
}

func (r *RedisCache) scopeKey(s Scope, suffix string) string {
	return MetadataPrefix(r.prefix) + ":" + suffix + ":" + s.BKTenantID + ":" + s.StrategyID
}

// Acquire 为一个策略取得有期限的发布权；查询必须发生在取得租约之后。
func (r *RedisCache) Acquire(ctx context.Context, s Scope, ttl time.Duration) (string, error) {
	token := uuid.NewString()
	ok, err := r.client.SetNX(ctx, r.scopeKey(s, "lease"), token, ttl).Result()
	if err != nil || !ok {
		return "", err
	}
	return token, nil
}

// Release 只释放自身租约，过期任务不能删除继任者的所有权。
func (r *RedisCache) Release(ctx context.Context, s Scope, token string) error {
	return r.client.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end; return 0`, []string{r.scopeKey(s, "lease")}, token).Err()
}

// Discover 扫描正式集合，包括数据库已无 active 的残留策略；SCAN 不是快照，因此每轮重复校准。
func (r *RedisCache) Discover(ctx context.Context) ([]Scope, error) {
	pattern := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`).Replace(r.prefix) + ":*"
	seen := map[Scope]bool{}
	var cursor uint64
	bytes := 0
	for range 1000 {
		keys, next, err := r.client.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			bytes += len(key)
			if bytes > r.maxBytes {
				return nil, fmt.Errorf("strategy discovery exceeds bytes limit")
			}
			if s, err := ParseKey(r.prefix, key); err == nil {
				seen[s] = true
			}
			if len(seen) > MaxPending {
				return nil, fmt.Errorf("strategy discovery exceeds count limit")
			}
		}
		cursor = next
		if cursor == 0 {
			result := make([]Scope, 0, len(seen))
			for s := range seen {
				result = append(result, s)
			}
			return result, nil
		}
	}
	return nil, fmt.Errorf("strategy discovery exceeds scan limit")
}

func (r *RedisCache) members(ctx context.Context, key string) ([]string, error) {
	count, err := r.client.SCard(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if count > int64(r.maxRows) {
		return nil, fmt.Errorf("cached members exceed limit")
	}
	seen := map[string]bool{}
	bytes := 0
	var cursor uint64
	for range 10000 {
		values, next, err := r.client.SScan(ctx, key, cursor, "", 256).Result()
		if err != nil {
			return nil, err
		}
		for _, v := range values {
			bytes += len(v)
			seen[v] = true
			if bytes > r.maxBytes || len(seen) > r.maxRows {
				return nil, fmt.Errorf("cached members exceed limit")
			}
		}
		cursor = next
		if cursor == 0 {
			out := make([]string, 0, len(seen))
			for v := range seen {
				out = append(out, v)
			}
			slices.Sort(out)
			return out, nil
		}
	}
	return nil, fmt.Errorf("cached members exceed scan limit")
}

// 发布不先删除正式集合，否则 RENAME 被 ACL 拒绝会把局部错误变成缓存丢失。
// RENAME 覆盖旧集合的释放成本受完整读取预算限制，大策略仍需单独做延迟压测。
// 脚本执行错误不等于回滚；通知使用 pcall，失败写入状态，集合成功不撤销。
const publishScript = `
if redis.call('GET',KEYS[1]) ~= ARGV[1] then return -1 end
for i=4,6 do local kind=redis.call('TYPE',KEYS[i]).ok; local expected='hash'; if i==4 then expected='zset' end; if kind~='none' and kind~=expected then return redis.error_reply('invalid metadata type') end end
if ARGV[3]=='1' and tonumber(ARGV[4])>0 and redis.call('SCARD',KEYS[3])~=tonumber(ARGV[4]) then return redis.error_reply('incomplete staging') end
if ARGV[3]=='1' then
 if tonumber(ARGV[4])>0 then
  redis.call('PERSIST',KEYS[3])
  local moved=redis.pcall('RENAME',KEYS[3],KEYS[2])
  if type(moved)=='table' and moved.err then redis.call('PEXPIRE',KEYS[3],120000); return redis.error_reply('snapshot rename failed') end
 else redis.call('UNLINK',KEYS[2]) end
end
redis.call('HSET',KEYS[6],'last_success',ARGV[5],'last_attempt',ARGV[5],'members',ARGV[4],'error','')
if tonumber(ARGV[4])>0 then redis.call('PERSIST',KEYS[6]) else redis.call('EXPIRE',KEYS[6],86400) end
if redis.call('HGET',KEYS[5],KEYS[2])==ARGV[2] then redis.call('ZREM',KEYS[4],KEYS[2]); redis.call('HDEL',KEYS[5],KEYS[2]) end
if ARGV[3]=='1' then
 local reply=redis.pcall('PUBLISH',ARGV[6],ARGV[7])
 if type(reply)=='table' and reply.err then redis.call('HSET',KEYS[6],'error','notification_failed'); return 2 end
end
return 1
`

// ReportDiscovery 保存目标级校准健康状态，失败不覆盖上次完整发现时间。
func (r *RedisCache) ReportDiscovery(ctx context.Context, success bool) error {
	key := MetadataPrefix(r.prefix) + ":health"
	values := map[string]any{"last_attempt": time.Now().UTC().Format(time.RFC3339Nano), "error": "discovery_failed"}
	if success {
		values["last_success"] = values["last_attempt"]
		values["error"] = ""
	}
	return r.client.HSet(ctx, key, values).Err()
}

// Publish 对比完整成员并构建带 TTL 的临时集合；只有仍持有租约才原子发布。
// 返回错误时调用方保留提示并重读事实，不能从响应丢失推断写入未发生。
func (r *RedisCache) Publish(ctx context.Context, p Pending, lease string, members []string) error {
	key := p.Scope.Key(r.prefix)
	old, err := r.members(ctx, key)
	if err != nil {
		return err
	}
	slices.Sort(members)
	members = slices.Compact(members)
	if len(members) > r.maxRows {
		return fmt.Errorf("members exceed limit")
	}
	bytes := 0
	for _, m := range members {
		bytes += len(m)
	}
	if bytes > r.maxBytes {
		return fmt.Errorf("members exceed byte limit")
	}
	changed := !slices.Equal(old, members)
	temp := r.scopeKey(p.Scope, "staging") + ":" + lease
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = r.client.Unlink(cleanup, temp).Err()
	}()
	if changed {
		for start := 0; start < len(members); start += 256 {
			args := []any{lease}
			for _, v := range members[start:min(start+256, len(members))] {
				args = append(args, v)
			}
			n, err := r.client.Eval(ctx, `if redis.call('GET',KEYS[1])~=ARGV[1] then return -1 end; redis.call('SADD',KEYS[2],unpack(ARGV,2)); redis.call('PEXPIRE',KEYS[2],120000); return 1`, []string{r.scopeKey(p.Scope, "lease"), temp}, args...).Int()
			if err != nil {
				return err
			}
			if n != 1 {
				return fmt.Errorf("projection lease lost")
			}
		}
	}
	notice, _ := json.Marshal(p.Scope)
	flag := "0"
	if changed {
		flag = "1"
	}
	hint := HintKeys(r.prefix)
	n, err := r.client.Eval(ctx, publishScript, []string{r.scopeKey(p.Scope, "lease"), key, temp, hint[0], hint[1], r.scopeKey(p.Scope, "status")}, lease, p.Token, flag, len(members), time.Now().UTC().Format(time.RFC3339Nano), r.prefix+":changes", string(notice)).Int()
	if err != nil {
		return err
	}
	if n < 0 {
		return fmt.Errorf("projection lease lost")
	}
	if n == 2 {
		return fmt.Errorf("notification failed after publication")
	}
	return nil
}

// Failed 保存脱敏原因，并仅延后本次提示；新的提示不会被旧失败覆盖。
func (r *RedisCache) Failed(ctx context.Context, p Pending, lease, reason string) error {
	hint := HintKeys(r.prefix)
	return r.client.Eval(ctx, `if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end; redis.call('HSET',KEYS[2],'last_attempt',ARGV[2],'error',ARGV[3]); if redis.call('EXISTS',ARGV[4])==1 then redis.call('PERSIST',KEYS[2]) else redis.call('EXPIRE',KEYS[2],86400) end; if redis.call('HGET',KEYS[4],ARGV[4])==ARGV[5] then local t=redis.call('TIME'); redis.call('ZADD',KEYS[3],'XX',t[1]*1000+math.floor(t[2]/1000)+5000,ARGV[4]) end; return 1`, []string{r.scopeKey(p.Scope, "lease"), r.scopeKey(p.Scope, "status"), hint[0], hint[1]}, lease, time.Now().UTC().Format(time.RFC3339Nano), reason, p.Scope.Key(r.prefix), p.Token).Err()
}

// ReadStatus 读取最近发布状态，缺失表示尚未校准或状态元数据已过期。
func (r *RedisCache) ReadStatus(ctx context.Context, s Scope) (Status, error) {
	v, err := r.client.HGetAll(ctx, r.scopeKey(s, "status")).Result()
	if err != nil {
		return Status{}, err
	}
	if len(v) == 0 {
		return Status{}, redis.Nil
	}
	n, err := strconv.Atoi(v["members"])
	if err != nil && !errors.Is(err, strconv.ErrSyntax) {
		return Status{}, err
	}
	return Status{LastSuccess: v["last_success"], LastAttempt: v["last_attempt"], Error: v["error"], Members: n}, nil
}
