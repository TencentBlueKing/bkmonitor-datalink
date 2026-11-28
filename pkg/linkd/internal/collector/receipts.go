// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"linkd/internal/pluginruntime"
)

func (r *RedisReceipts) resultKey(request pluginruntime.Request) string {
	return r.key(request.TenantID, request.SourceID) + ":result:" + url.QueryEscape(request.RequestID)
}

func (r *RedisReceipts) requestKey(request pluginruntime.Request) string {
	return r.key(request.TenantID, request.SourceID) + ":request:" + url.QueryEscape(request.RequestID)
}

func (r *RedisReceipts) pendingKey(tenant, source string) string {
	return r.key(tenant, source) + ":pending"
}

// BeginAttempt 在调用 Python 前持久预留一次尝试，总计最多三次。
// 预留后崩溃也消耗此次机会，不能假设未收到回复就意味着插件没有执行。
func (r *RedisReceipts) BeginAttempt(ctx context.Context, request pluginruntime.Request) (bool, error) {
	count, err := r.reserveAttempt(ctx, request, "attempts")
	return count > 0, err
}

// BeginPublication 预留批次发布次数，与 Python 计算次数独立；零表示三次预算已耗尽。
func (r *RedisReceipts) BeginPublication(ctx context.Context, request pluginruntime.Request) (int, error) {
	return r.reserveAttempt(ctx, request, "publications")
}

func (r *RedisReceipts) reserveAttempt(ctx context.Context, request pluginruntime.Request, stage string) (int, error) {
	key := r.key(request.TenantID, request.SourceID) + ":" + stage + ":" + url.QueryEscape(request.RequestID)
	count, err := r.eval(ctx, `
local count = tonumber(redis.call('GET', KEYS[1]) or '0')
if count >= 3 then return 0 end
redis.call('INCR', KEYS[1])
return count + 1
`, []string{key}).Int()
	return count, err
}

// 首次输入与待处理索引必须一起写入，否则崩溃后无法发现尚未执行的请求。
// 已存在的请求不重新入索引，避免已完成请求重放后再次进入待处理集合。
const saveRequestScript = `
if redis.call('EXISTS', KEYS[1]) == 0 then
    redis.call('SET', KEYS[1], ARGV[1])
    redis.call('ZADD', KEYS[2], ARGV[2], ARGV[3])
end
return redis.call('GET', KEYS[1])
`

// SaveRequest 固定推送或定时执行的首次输入；再次提交同一身份返回持久快照。
// 来源发布、凭据、输入游标和时间一并保留，恢复调用方必须据该发布装配投递目标。
func (r *RedisReceipts) SaveRequest(ctx context.Context, request pluginruntime.Request) (pluginruntime.Request, error) {
	key := r.requestKey(request)
	raw, err := json.Marshal(request)
	if err != nil {
		return request, err
	}
	received, err := time.Parse(time.RFC3339Nano, request.ReceivedAt)
	if err != nil {
		return request, err
	}
	stored, err := r.eval(ctx, saveRequestScript, []string{key, r.pendingKey(request.TenantID, request.SourceID)}, raw, received.UnixMilli(), request.RequestID).Text()
	if err != nil {
		return request, err
	}
	var snapshot pluginruntime.Request
	err = json.Unmarshal([]byte(stored), &snapshot)
	return snapshot, err
}

// PendingIDs 按首次接收/调度时间分批列出未完成身份，不读取业务正文。
// 本方法不取得执行所有权；调用方须在来源授权下串行恢复，完成后再读取下一批。
func (r *RedisReceipts) PendingIDs(ctx context.Context, tenant, source string, limit int64) ([]string, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("positive pending request batch size required")
	}
	return r.client.ZRange(ctx, r.pendingKey(tenant, source), 0, limit-1).Result()
}

// LoadRequest 按固定调用身份恢复首次执行输入，包含发布版本及输入游标。
// 不存在时返回 redis.Nil，不从当前配置或进度拼接替代请求。
func (r *RedisReceipts) LoadRequest(ctx context.Context, tenant, source, id string) (pluginruntime.Request, error) {
	key := r.requestKey(pluginruntime.Request{TenantID: tenant, SourceID: source, RequestID: id})
	raw, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		// 推送可能在接收事务后、执行快照前崩溃；Stream 本身已保存完整接收事实。
		if _, parseErr := restoreReceipt(pluginruntime.Request{}, id); parseErr == nil {
			return r.Load(ctx, tenant, source, id)
		}
	}
	if err != nil {
		return pluginruntime.Request{}, err
	}
	var stored pluginruntime.Request
	err = json.Unmarshal(raw, &stored)
	return stored, err
}

// LoadResult 读取发布前固定的计算结果；不存在与存储失败明确区分。
func (r *RedisReceipts) LoadResult(ctx context.Context, request pluginruntime.Request) (*StoredResult, error) {
	raw, err := r.client.Get(ctx, r.resultKey(request)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result StoredResult
	err = json.Unmarshal(raw, &result)
	return &result, err
}

// SaveResult 只保存首个完整计算结果，不进行 payload hash 比较；重复调用返回既有结果。
func (r *RedisReceipts) SaveResult(ctx context.Context, request pluginruntime.Request, result pluginruntime.Result) (pluginruntime.Result, error) {
	raw, err := json.Marshal(StoredResult{Result: result})
	if err != nil {
		return result, err
	}
	if err = r.eval(ctx, `redis.call('SETNX', KEYS[1], ARGV[1]); return 1`, []string{r.resultKey(request)}, raw).Err(); err != nil {
		return result, err
	}
	stored, err := r.LoadResult(ctx, request)
	if err != nil {
		return result, err
	}
	if stored == nil {
		return result, fmt.Errorf("saved source computation is missing")
	}
	return stored.Result, nil
}

// Complete 原子保存成功或明确插件失败的终结结果；失败结果不携带候选进度。
// Redis 事务同时提交进度、完成结果并移除待处理索引；连接断开时结果可能未知。
// 配置领导权后，授权检查与所有写入在同一 Lua 中完成；不撤销已经发出的 Kafka 请求。
func (r *RedisReceipts) Complete(ctx context.Context, request pluginruntime.Request, result pluginruntime.Result) error {
	raw, err := json.Marshal(StoredResult{Result: result, Completed: true})
	if err != nil {
		return err
	}
	input, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode completed source input: %w", err)
	}
	var state []byte
	if result.ProposedState != nil {
		state, err = json.Marshal(result.ProposedState)
		if err != nil {
			return fmt.Errorf("encode source progress: %w", err)
		}
	}
	progress := NewRedisProgress(r.client, r.prefix)
	_, err = r.eval(ctx, completeRequestScript, []string{
		r.resultKey(request), r.requestKey(request), r.pendingKey(request.TenantID, request.SourceID),
		progress.key(request.TenantID, request.SourceID),
		r.key(request.TenantID, request.SourceID) + ":attempts:" + url.QueryEscape(request.RequestID),
		r.key(request.TenantID, request.SourceID) + ":publications:" + url.QueryEscape(request.RequestID),
		r.key(request.TenantID, request.SourceID), r.pushPendingKey(),
	}, raw, input, string(state), r.retention.Milliseconds(), request.RequestID, request.Mode, request.TenantID, request.SourceID).Result()
	return err
}

const completeRequestScript = `
if ARGV[3] ~= '' then redis.call('SET', KEYS[4], ARGV[3]) end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[4])
redis.call('SET', KEYS[2], ARGV[2], 'NX', 'PX', ARGV[4])
redis.call('PEXPIRE', KEYS[2], ARGV[4])
redis.call('PEXPIRE', KEYS[5], ARGV[4])
redis.call('PEXPIRE', KEYS[6], ARGV[4])
if ARGV[6] == 'push' then
 redis.call('XDEL', KEYS[7], ARGV[5])
 redis.call('ZREM', KEYS[8], cjson.encode({ARGV[7], ARGV[8], ARGV[5]}))
end
redis.call('ZREM', KEYS[3], ARGV[5])
return 1
`

// RedisReceipts 保存来源接收事实；Redis 不是 Python 调用通道，执行仍走同步 HTTP。
// 未完成请求没有 TTL；终结时释放 Stream 正文并为请求快照、结果和计数设置保留期。
type RedisReceipts struct {
	client                       redis.UniversalClient
	prefix                       string
	retention                    time.Duration
	authorityKey, authorityToken string
	writer                       AuthorizedWriter
}

// NewRedisReceipts 复用宿主 Redis 连接和部署命名空间；retention 为零时终结记录保留七天。
func NewRedisReceipts(client redis.UniversalClient, prefix string, retention time.Duration) *RedisReceipts {
	if retention == 0 {
		retention = 7 * 24 * time.Hour
	}
	return &RedisReceipts{client: client, prefix: prefix, retention: retention}
}

// Accept 使用 Redis 持久 Stream ID 固定身份和接收时间。
// 客户端重复 HTTP 请求是独立接收，不承诺跨 HTTP 重试去重；重放同一记录应沿用其 Stream ID。
func (r *RedisReceipts) Accept(ctx context.Context, request pluginruntime.Request) (pluginruntime.Request, error) {
	request.RequestID = ""
	request.ReceivedAt = ""
	raw, err := json.Marshal(request)
	if err != nil {
		return request, fmt.Errorf("encode source receipt: %w", err)
	}
	key := r.key(request.TenantID, request.SourceID)
	// 接收与登记待处理身份一次完成，关闭接收成功后尚未进入 Service 的发现窗口。
	id, err := r.eval(ctx, `
local id = redis.call('XADD', KEYS[1], '*', 'request', ARGV[1])
local milliseconds = string.match(id, '^(%d+)-')
redis.call('ZADD', KEYS[2], milliseconds, id)
redis.call('ZADD', KEYS[3], milliseconds, cjson.encode({ARGV[2], ARGV[3], id}))
return id
`, []string{key, r.pendingKey(request.TenantID, request.SourceID), r.pushPendingKey()}, raw, request.TenantID, request.SourceID).Text()
	if err != nil {
		return request, fmt.Errorf("persist source receipt: %w", err)
	}
	return restoreReceipt(request, id)
}

func (r *RedisReceipts) key(tenant, source string) string {
	return r.prefix + ":collector:receipts:" + url.QueryEscape(tenant) + ":" + url.QueryEscape(source)
}

// Load 读取指定租户来源的原接收记录，恢复相同执行身份与接收时间。
// 记录不存在返回 redis.Nil；配置和原始正文取自记录，不读取当前来源发布。
func (r *RedisReceipts) Load(ctx context.Context, tenant, source, id string) (pluginruntime.Request, error) {
	messages, err := r.client.XRangeN(ctx, r.key(tenant, source), id, id, 1).Result()
	if err != nil {
		return pluginruntime.Request{}, fmt.Errorf("load source receipt: %w", err)
	}
	if len(messages) == 0 {
		// 不调用 LoadRequest，避免快照过期时互相回退；过期后返回 redis.Nil。
		raw, err := r.client.Get(ctx, r.requestKey(pluginruntime.Request{TenantID: tenant, SourceID: source, RequestID: id})).Bytes()
		if err != nil {
			return pluginruntime.Request{}, err
		}
		var request pluginruntime.Request
		err = json.Unmarshal(raw, &request)
		return request, err
	}
	raw, ok := messages[0].Values["request"].(string)
	if !ok {
		return pluginruntime.Request{}, fmt.Errorf("source receipt has invalid request encoding")
	}
	var request pluginruntime.Request
	if err = json.Unmarshal([]byte(raw), &request); err != nil {
		return request, fmt.Errorf("decode source receipt: %w", err)
	}
	return restoreReceipt(request, messages[0].ID)
}

func restoreReceipt(request pluginruntime.Request, id string) (pluginruntime.Request, error) {
	milliseconds, _, ok := strings.Cut(id, "-")
	if !ok {
		return request, fmt.Errorf("invalid Redis receipt ID")
	}
	value, err := strconv.ParseInt(milliseconds, 10, 64)
	if err != nil {
		return request, fmt.Errorf("parse receipt timestamp: %w", err)
	}
	request.RequestID = id
	request.ReceivedAt = time.UnixMilli(value).UTC().Format(time.RFC3339Nano)
	return request, nil
}
