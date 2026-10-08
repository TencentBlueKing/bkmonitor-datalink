// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package redisstate 原子维护允许故障丢失的策略计数和窗口；不持久化 Event/Alert 业务事实。
package redisstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/suppressioncleanup"
)

var (
	// ErrBudget 表示本次策略协调超过硬上限，调用方记录跳过而不是截断成功。
	ErrBudget = errors.New("policy Redis budget exceeded")
	// ErrState 表示协调状态类型/作用域异常，不能假装返回正常计数。
	ErrState = errors.New("invalid policy Redis state")
)

// Store 使用已经启用 ContextTimeoutEnabled 的有界 Redis 客户端；连接生命周期由装配管理。
type Store struct {
	statisticsSlots chan struct{}
	observer        StateObserver
	client          *redis.Client
	namespace       string
}

// New 不连接或清空 Redis；部署名字只参与稳定哈希，不直接成为业务身份兜底。
func New(client *redis.Client, deployment string) (*Store, error) {
	if client == nil || !client.Options().ContextTimeoutEnabled || strings.TrimSpace(deployment) == "" || len(deployment) > 128 {
		return nil, fmt.Errorf("policy Redis requires deployment and context-aware client")
	}
	return &Store{statisticsSlots: make(chan struct{}, 4), client: client, namespace: "linkd:policies:" + digest("deployment", deployment)}, nil
}

// StateObserver 只接收固定的初始化分类，不接收租户或业务身份。
type StateObserver interface {
	ObservePolicyStateInit(context.Context, string, string)
}

// SetObserver 仅允许装配时调用，必须在并发使用Store前完成。
func (s *Store) SetObserver(observer StateObserver) { s.observer = observer }

func (s *Store) observeInit(ctx context.Context, scheme string, result []any) {
	if s.observer == nil || len(result) < 3 {
		return
	}
	kind, _ := result[len(result)-1].(string)
	if kind == "created" || kind == "repaired" {
		s.observer.ObservePolicyStateInit(ctx, scheme, kind)
	}
}

func digest(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Identity 保持防抖按租户/来源/fingerprint 隔离；跨来源聚合不使用该身份作分组。
type Identity struct {
	TenantID    string
	SourceID    string
	Fingerprint string
}

func (i Identity) Validate() error {
	if err := domain.ValidateIdentityPart("tenant", i.TenantID, 64); err != nil {
		return err
	}
	if err := domain.ValidateIdentityPart("source", i.SourceID, 32); err != nil {
		return err
	}
	if i.Fingerprint == "" || len(i.Fingerprint) > 128 {
		return fmt.Errorf("invalid fingerprint")
	}
	return nil
}

// ClipRequest 的 At 必须来自已经持久化的 PolicyContext，EventID 在多等级和重试间相同。
type ClipRequest struct {
	Identity
	PolicyID  string
	Version   int64
	Digest    string
	EventID   string
	At        time.Time
	Duration  time.Duration
	Threshold int
}

// ClipDecision 保留首次结果与计数代次；Epoch 是该代次的首条 EventID，不是随机 ID 或本地时间。
type ClipDecision struct {
	Allowed           bool   `json:"allowed"`
	Count             int    `json:"count"`
	Threshold         int    `json:"threshold"`
	EvaluatedAtMillis int64  `json:"evaluated_at_ms"`
	Epoch             string `json:"epoch"`
	CounterID         string `json:"counter_id"`
	Replayed          bool   `json:"-"`
}

func (r ClipRequest) validate() error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := domain.ValidateIdentityPart("policy id", r.PolicyID, 80); err != nil {
		return err
	}
	bytes, err := hex.DecodeString(r.Digest)
	if err != nil || len(bytes) != 32 || r.Version < 1 || r.Version >= 1<<53 {
		return fmt.Errorf("invalid policy release")
	}
	if r.EventID == "" || len(r.EventID) > domain.EntityIDMaxBytes || r.At.IsZero() || r.At.UnixMilli() < 0 || r.At.UnixMilli() > 1<<46 || r.Duration < time.Second || r.Duration > 30*24*time.Hour || r.Duration%time.Second != 0 || r.Threshold < 1 || r.Threshold > 10000 {
		return fmt.Errorf("invalid clip request or budget")
	}
	return nil
}

func (s *Store) base(tenant string) string {
	return s.namespace + ":{" + digest("tenant", tenant) + "}"
}

func (s *Store) clipKeys(r ClipRequest) ([]string, string) {
	subject := digest("identity", r.SourceID, r.Fingerprint)
	counterID := digest("clip", r.PolicyID, fmt.Sprint(r.Version), r.Digest, subject)
	base := s.base(r.TenantID)
	counter := base + ":clip:" + subject + ":" + counterID
	return []string{counter, counter + ":meta", base + ":clip-index:" + subject, base + ":clip-op:" + digest(counterID, r.EventID), s.suppressionIndex(r.TenantID, "clip")}, counterID
}

// Clip 原子完成去重、闭区间滑动统计及首次结果缓存。即使 Redis 部分丢失，也不暂停全局处理。
// 返回 error 时调用方不能写出正常抑制结果；Redis 脚本的类型检查先于任何修改。
func (s *Store) Clip(ctx context.Context, r ClipRequest) (ClipDecision, error) {
	if err := r.validate(); err != nil {
		return ClipDecision{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	keys, counterID := s.clipKeys(r)
	signature := digest(counterID, fmt.Sprint(r.Duration.Milliseconds()), fmt.Sprint(r.Threshold))
	result, err := clipScript.Run(ctx, s.client, keys, r.EventID, r.At.UnixMilli(), r.Duration.Milliseconds(), r.Threshold, counterID, digest("identity", r.SourceID, r.Fingerprint), signature, r.descriptor(), clipRuntimeID(r, counterID)).Slice()
	if err != nil {
		return ClipDecision{}, err
	}
	if len(result) != 2 && len(result) != 3 {
		return ClipDecision{}, ErrState
	}
	tag, ok := result[0].(string)
	if !ok {
		return ClipDecision{}, ErrState
	}
	switch tag {
	case "budget":
		return ClipDecision{}, ErrBudget
	case "conflict":
		return ClipDecision{}, policy.ErrConflict
	case "state":
		return ClipDecision{}, ErrState
	case "new", "replay":
	default:
		return ClipDecision{}, ErrState
	}
	raw, ok := result[1].(string)
	if !ok {
		return ClipDecision{}, ErrState
	}
	var decision ClipDecision
	if err := json.Unmarshal([]byte(raw), &decision); err != nil {
		return ClipDecision{}, ErrState
	}
	if decision.CounterID != counterID || decision.Epoch == "" || decision.Count < 1 || decision.Count > 10000 || decision.Threshold != r.Threshold || decision.Allowed != (decision.Count >= decision.Threshold) {
		return ClipDecision{}, ErrState
	}
	decision.Replayed = tag == "replay"
	if !decision.Replayed {
		s.observeInit(ctx, "clip", result)
	}
	return decision, nil
}

var clipScript = redis.NewScript(suppressionIndexLua + `
local types={'zset','hash','set','string'}
for i=1,4 do local t=redis.call('TYPE',KEYS[i]).ok;if t~='none' and t~=types[i] then return {'state','type'} end end
local cached=redis.call('GET',KEYS[4])
if cached then
 local ok,envelope=pcall(cjson.decode,cached)
 if not ok or type(envelope)~='table' or envelope.signature~=ARGV[7] then return {'conflict','operation'} end
 return {'replay',cjson.encode(envelope.decision)}
end
local indexError=checkIndex(KEYS[5],ARGV[9]);if indexError then return {indexError,'runtime_index'} end
local countExists=redis.call('EXISTS',KEYS[1]);local metaExists=redis.call('EXISTS',KEYS[2]);local initialization='';if countExists==0 and metaExists==0 then initialization='created' end
local savedScope=redis.call('HGET',KEYS[2],'scope')
if savedScope and savedScope~=ARGV[6] then return {'state','scope'} end
if redis.call('SCARD',KEYS[3])>=512 and redis.call('SISMEMBER',KEYS[3],KEYS[1])==0 then
 local prefix=string.sub(KEYS[1],1,string.len(KEYS[1])-64)
 local members=redis.call('SMEMBERS',KEYS[3])
 for _,key in ipairs(members) do
  if string.sub(key,1,string.len(prefix))~=prefix then return {'state','reverse_scope'} end
  if redis.call('EXISTS',key)==0 and redis.call('EXISTS',key..':meta')==0 then redis.call('SREM',KEYS[3],key) end
 end
 if redis.call('SCARD',KEYS[3])>=512 then return {'budget','reverse_index'} end
end
-- 任一计数组成部分丢失时重开代次，不将残缺计数与旧 owner 拼成一份有效状态。
if countExists~=metaExists or (metaExists==1 and (not savedScope or not redis.call('HGET',KEYS[2],'epoch') or redis.call('HGET',KEYS[2],'epoch')=='' or not redis.call('HGET',KEYS[2],'owner'))) then redis.call('DEL',KEYS[1],KEYS[2]);metaExists=0;initialization='repaired' end
local epoch=redis.call('HGET',KEYS[2],'epoch')
if not epoch then epoch=ARGV[1];redis.call('HSET',KEYS[2],'epoch',epoch,'scope',ARGV[6],'owner','') end
local at=tonumber(redis.call('ZSCORE',KEYS[1],ARGV[1]) or ARGV[2]);local duration=tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf','('..tostring(at-duration))
if redis.call('ZCARD',KEYS[1])>=10000 and not redis.call('ZSCORE',KEYS[1],ARGV[1]) then return {'budget','members'} end
redis.call('ZADD',KEYS[1],'NX',at,ARGV[1])
redis.call('HSET',KEYS[2],'descriptor',ARGV[8],'last_at',at)
local count=redis.call('ZCOUNT',KEYS[1],at-duration,at)
local decision={allowed=count>=tonumber(ARGV[4]),count=count,threshold=tonumber(ARGV[4]),evaluated_at_ms=at,epoch=epoch,counter_id=ARGV[5]}
local ttl=duration+60000;local retention=ttl
redis.call('SADD',KEYS[3],KEYS[1]);redis.call('PEXPIRE',KEYS[1],ttl);redis.call('PEXPIRE',KEYS[2],ttl)
touchIndex(KEYS[5],ARGV[9],ttl)
-- 反向索引可能包含更长的其他策略窗口，不能由短窗口缩短已有 TTL。
local oldttl=redis.call('PTTL',KEYS[3]);if oldttl<retention then redis.call('PEXPIRE',KEYS[3],retention) end
redis.call('SET',KEYS[4],cjson.encode({signature=ARGV[7],decision=decision}),'PX',retention)
return {'new',cjson.encode(decision),initialization}
`)

// BindClipOwner 在真实 Alert 创建后绑定计数代次，旧代次重试不能占有新窗口。
func (s *Store) BindClipOwner(ctx context.Context, r ClipRequest, decision ClipDecision, alertID string) error {
	if err := r.validate(); err != nil {
		return err
	}
	keys, id := s.clipKeys(r)
	if !decision.Allowed || decision.CounterID != id || decision.Epoch == "" || alertID == "" || len(alertID) > domain.EntityIDMaxBytes {
		return fmt.Errorf("invalid clip owner")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := bindClipScript.Run(ctx, s.client, keys[:2], decision.Epoch, alertID, digest("identity", r.SourceID, r.Fingerprint)).Int()
	if err != nil {
		return err
	}
	if result == 2 {
		return ErrState
	}
	return nil
}

var bindClipScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[2]).ok;if t=='none' then return 0 end;if t~='hash' then return 2 end
if redis.call('HGET',KEYS[2],'scope')~=ARGV[3] then return 2 end
if redis.call('HGET',KEYS[2],'epoch')~=ARGV[1] or redis.call('EXISTS',KEYS[1])==0 then return 0 end
local owner=redis.call('HGET',KEYS[2],'owner');if owner and owner~='' and owner~=ARGV[2] then return 2 end
redis.call('HSET',KEYS[2],'owner',ARGV[2]);return 1
`)

// ClearClipIdentity 按反向索引清理终态所属计数；owner 为空只清理尚无 Alert 的统计。
// 旧 Alert 关闭只能删除仍属于它的代次，不能清掉新 Alert 的计数；不使用 KEYS 扫描。
func (s *Store) ClearClipIdentity(ctx context.Context, identity Identity, owner string) ([]suppressioncleanup.Window, error) {
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	if len(owner) > domain.EntityIDMaxBytes {
		return nil, fmt.Errorf("invalid owner")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	base := s.base(identity.TenantID)
	subject := digest("identity", identity.SourceID, identity.Fingerprint)
	raw, err := clearClipScript.Run(ctx, s.client, []string{base + ":clip-index:" + subject, s.suppressionIndex(identity.TenantID, "clip")}, base+":clip:"+subject+":", subject, owner).Slice()
	return cleanupWindows(raw, err, "clip", subject)
}

var clearClipScript = redis.NewScript(`
local rt=redis.call('TYPE',KEYS[2]).ok;if rt~='none' and rt~='zset' then return {'state'} end
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return {'ok'} end;if t~='set' then return {'state'} end
if redis.call('SCARD',KEYS[1])>512 then return {'state'} end
local members=redis.call('SMEMBERS',KEYS[1]);local removed={'ok'}
-- 先验证整份反向页；损坏引用不能变成跨租户或跨身份的 DEL。
for _,key in ipairs(members) do
 if string.sub(key,1,string.len(ARGV[1]))~=ARGV[1] or string.len(key)~=string.len(ARGV[1])+64 or string.find(string.sub(key,-64),'[^0-9a-f]') then return {'state'} end
 local t=redis.call('TYPE',key..':meta').ok
 if t~='none' and (t~='hash' or redis.call('HGET',key..':meta','scope')~=ARGV[2]) then return {'state'} end
 if t=='hash' then local epoch=redis.call('HGET',key..':meta','epoch');if not epoch or string.len(epoch)==0 or string.len(epoch)>160 or not redis.call('HGET',key..':meta','owner') then return {'state'} end end
end
for _,key in ipairs(members) do
 local owner=redis.call('HGET',key..':meta','owner')
 if not owner or owner==ARGV[3] then
  local id=ARGV[2]..':'..string.sub(key,-64);local epoch=redis.call('HGET',key..':meta','epoch')
  table.insert(removed,id);table.insert(removed,epoch or '');table.insert(removed,epoch and 0 or 1)
  redis.call('DEL',key,key..':meta');redis.call('SREM',KEYS[1],key);redis.call('ZREM',KEYS[2],id)
 end
end
if redis.call('SCARD',KEYS[1])==0 then redis.call('DEL',KEYS[1]) end
return removed
`)
