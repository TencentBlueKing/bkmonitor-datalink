// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/suppressioncleanup"
)

// AggregationRequest 按租户、策略版本和字段分组；Identity 仅描述候选，不隐式加入分组。
// CandidateAlertID 来自正常 AlertIDGenerator，必须在所有重试中保持不变。
type AggregationRequest struct {
	Identity
	PolicyID         string
	Version          int64
	Digest           string
	GroupKey         string
	EventID          string
	CandidateAlertID string
	At               time.Time
	Duration         time.Duration
}

// AggregationDecision 是跨来源窗口的占位或首次抑制结果。只有 owner 才可复核后确认抑制。
// pending 不能关联为主告警；candidate 必须在真实 Alert 获准处置后显式 Commit。
type AggregationDecision struct {
	Role             string `json:"role"`
	WindowID         string `json:"window_id"`
	Epoch            string `json:"epoch"`
	OwnerAlertID     string `json:"owner_alert_id"`
	OwnerEventID     string `json:"owner_event_id"`
	OwnerSourceID    string `json:"owner_source_id"`
	OwnerFingerprint string `json:"owner_fingerprint"`
	StartedAtMillis  int64  `json:"started_at_ms"`
	ExpiresAtMillis  int64  `json:"expires_at_ms"`
	Replayed         bool   `json:"-"`
}

func (r AggregationRequest) validate() error {
	if err := (ClipRequest{Identity: r.Identity, PolicyID: r.PolicyID, Version: r.Version, Digest: r.Digest, EventID: r.EventID, At: r.At, Duration: r.Duration, Threshold: 1}).validate(); err != nil {
		return err
	}
	if !hash64(r.GroupKey) || r.CandidateAlertID == "" || len(r.CandidateAlertID) > domain.EntityIDMaxBytes {
		return fmt.Errorf("invalid aggregation group/candidate")
	}
	return nil
}

func hash64(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}

func (s *Store) aggregationKeys(r AggregationRequest) ([]string, string, string) {
	id := digest("aggregation", r.PolicyID, fmt.Sprint(r.Version), r.Digest, r.GroupKey)
	base := s.base(r.TenantID)
	signature := digest(id, r.EventID, r.SourceID, r.Fingerprint, r.CandidateAlertID, fmt.Sprint(r.Duration.Milliseconds()))
	return []string{base + ":aggregation:" + id, base + ":aggregation-owner:" + digest(r.CandidateAlertID), base + ":aggregation-op:" + digest(id, r.EventID), s.suppressionIndex(r.TenantID, "aggregation"), base + ":aggregation-members:" + id}, id, signature
}

// ClaimAggregation 原子读取或抢占空窗；占位最长 30 秒，过期后其他候选可以重新竞争。
// 已冻结的抑制结果优先返回；正常后续成员不延长固定窗口或 owner 租期。
func (s *Store) ClaimAggregation(ctx context.Context, r AggregationRequest) (AggregationDecision, error) {
	if err := r.validate(); err != nil {
		return AggregationDecision{}, err
	}
	keys, id, signature := s.aggregationKeys(r)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := claimAggregationScript.Run(ctx, s.client, keys, id, r.EventID, r.CandidateAlertID, r.SourceID, r.Fingerprint, r.At.UnixMilli(), r.Duration.Milliseconds(), signature, r.descriptor(), r.member()).Slice()
	decision, failure := aggregationResult(raw, err, id)
	if failure == nil {
		s.observeInit(ctx, "aggregation", raw)
	}
	return decision, failure
}

func aggregationResult(raw []any, err error, id string) (AggregationDecision, error) {
	if err != nil {
		return AggregationDecision{}, err
	}
	if len(raw) != 2 && len(raw) != 3 {
		return AggregationDecision{}, ErrState
	}
	tag, ok := raw[0].(string)
	if !ok {
		return AggregationDecision{}, ErrState
	}
	switch tag {
	case "budget":
		return AggregationDecision{}, ErrBudget
	case "state":
		return AggregationDecision{}, ErrState
	case "conflict":
		return AggregationDecision{}, policy.ErrConflict
	case "new", "replay":
	default:
		return AggregationDecision{}, ErrState
	}
	body, ok := raw[1].(string)
	if !ok {
		return AggregationDecision{}, ErrState
	}
	var d AggregationDecision
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return AggregationDecision{}, ErrState
	}
	if err := d.validate(); err != nil || d.WindowID != id {
		return AggregationDecision{}, ErrState
	}
	d.Replayed = tag == "replay"
	return d, nil
}

func (d AggregationDecision) validate() error {
	if d.Role != "candidate" && d.Role != "pending" && d.Role != "owner" && d.Role != "suppressed" {
		return ErrState
	}
	if !hash64(d.WindowID) || d.Epoch == "" || len(d.Epoch) > domain.EntityIDMaxBytes || d.Epoch != d.OwnerEventID || d.OwnerAlertID == "" || len(d.OwnerAlertID) > domain.EntityIDMaxBytes || d.StartedAtMillis < 0 || d.StartedAtMillis > 1<<46 || d.ExpiresAtMillis <= d.StartedAtMillis || d.ExpiresAtMillis-d.StartedAtMillis > (30*24*time.Hour).Milliseconds() {
		return ErrState
	}
	return (Identity{TenantID: "validation", SourceID: d.OwnerSourceID, Fingerprint: d.OwnerFingerprint}).Validate()
}

// Lua 使用 Redis TIME 约束短期占位租期，业务窗口仍使用已冻结的处理时间。
// 时间不参与身份：Epoch 固定为首条候选 EventID，清理和提交必须同时比较 owner。
var claimAggregationScript = redis.NewScript(suppressionIndexLua + `
local expected={'string','set','string'}
for i=1,3 do local t=redis.call('TYPE',KEYS[i]).ok;if t~='none' and t~=expected[i] then return {'state','type'} end end
local cached=redis.call('GET',KEYS[3])
if cached then
 local ok,op=pcall(cjson.decode,cached)
 if not ok or type(op)~='table' or op.signature~=ARGV[8] then return {'conflict','operation'} end
 return {'replay',cjson.encode(op.decision)}
end
local now=redis.call('TIME');local clock=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000)
local raw=redis.call('GET',KEYS[1]);local current=nil
if raw then
 local ok,value=pcall(cjson.decode,raw)
 if not ok or type(value)~='table' or value.window_id~=ARGV[1] or type(value.expires_at_ms)~='number' or type(value.started_at_ms)~='number' or (value.state~='pending' and value.state~='admitted') or (value.state=='pending' and type(value.pending_until)~='number') then return {'state','record'} end
 current=value
 if tonumber(ARGV[6])<current.started_at_ms then return {'state','clock_order'} end
 if tonumber(ARGV[6])>current.expires_at_ms or (current.state=='pending' and clock>current.pending_until) then current=nil end
end
if current then
 if current.owner_event_id==ARGV[2] and current.owner_alert_id==ARGV[3] then current.role='candidate'
 elseif current.state=='pending' then current.role='pending'
 else current.role='owner' end
 return {'new',cjson.encode(current)}
end
local indexError=checkIndex(KEYS[4],ARGV[1]);if indexError then return {indexError,'runtime_index'} end
local mt=redis.call('TYPE',KEYS[5]).ok;if mt~='none' and mt~='zset' then return {'state','members'} end
-- owner 索引有固定大小。只清除不存在的窗口引用，不触及其他 Alert 的新代次。
if redis.call('SCARD',KEYS[2])>=512 and redis.call('SISMEMBER',KEYS[2],ARGV[1])==0 then
 local prefix=string.sub(KEYS[1],1,string.len(KEYS[1])-64)
 for _,id in ipairs(redis.call('SMEMBERS',KEYS[2])) do
  if string.len(id)~=64 or string.find(id,'[^0-9a-f]') then return {'state','reverse_scope'} end
  local key=prefix..id;local t=redis.call('TYPE',key).ok
  if t=='none' then redis.call('SREM',KEYS[2],id)
  elseif t~='string' then return {'state','reverse_type'}
  else local ok,value=pcall(cjson.decode,redis.call('GET',key));if not ok or type(value)~='table' then return {'state','reverse_record'} end;if value.owner_alert_id~=ARGV[3] then redis.call('SREM',KEYS[2],id) end end
 end
 if redis.call('SCARD',KEYS[2])>=512 then return {'budget','owner_windows'} end
end
current={window_id=ARGV[1],epoch=ARGV[2],owner_event_id=ARGV[2],owner_alert_id=ARGV[3],owner_source_id=ARGV[4],owner_fingerprint=ARGV[5],started_at_ms=tonumber(ARGV[6]),expires_at_ms=tonumber(ARGV[6])+tonumber(ARGV[7]),state='pending',pending_until=clock+30000}
current.descriptor=cjson.decode(ARGV[9])
redis.call('SET',KEYS[1],cjson.encode(current),'PX',90000)
redis.call('DEL',KEYS[5]);redis.call('ZADD',KEYS[5],ARGV[6],ARGV[10]);redis.call('PEXPIRE',KEYS[5],90000)
touchIndex(KEYS[4],ARGV[1],90000)
redis.call('SADD',KEYS[2],ARGV[1]);if redis.call('PTTL',KEYS[2])<90000 then redis.call('PEXPIRE',KEYS[2],90000) end
current.role='candidate';return {'new',cjson.encode(current),'created'}
`)

// CommitAggregationOwner 只能在真实主 Alert 活跃且已放行后调用；返回 false 表示占位已被取代/丢失。
// 缓存丢失不重建窗口，避免旧成功请求越过新代次；调用方记录诊断后正常运行。
func (s *Store) CommitAggregationOwner(ctx context.Context, r AggregationRequest, d AggregationDecision) (bool, error) {
	if err := r.validate(); err != nil {
		return false, err
	}
	keys, id, _ := s.aggregationKeys(r)
	if err := d.validate(); err != nil || d.WindowID != id || d.OwnerAlertID != r.CandidateAlertID || d.OwnerEventID != r.EventID || d.OwnerSourceID != r.SourceID || d.OwnerFingerprint != r.Fingerprint || d.StartedAtMillis != r.At.UnixMilli() || d.Role != "candidate" {
		return false, ErrState
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := commitAggregationScript.Run(ctx, s.client, []string{keys[0], keys[1], keys[3], keys[4]}, id, d.Epoch, d.OwnerAlertID, r.Duration.Milliseconds()+60000).Int()
	if err != nil {
		return false, err
	}
	if n < 0 {
		return false, ErrState
	}
	return n == 1, nil
}

var commitAggregationScript = redis.NewScript(suppressionIndexLua + `
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return 0 end;if t~='string' then return -1 end
local it=redis.call('TYPE',KEYS[2]).ok;if it~='none' and it~='set' then return -1 end
local ok,v=pcall(cjson.decode,redis.call('GET',KEYS[1]));if not ok or type(v)~='table' or v.window_id~=ARGV[1] then return -1 end
if v.epoch~=ARGV[2] or v.owner_alert_id~=ARGV[3] then return 0 end
if v.state~='pending' and v.state~='admitted' then return -1 end
local ttl=tonumber(ARGV[4])
-- 重试不得延长既有固定窗口的物理保留时间。
if v.state=='admitted' then return 1 end
local now=redis.call('TIME');local clock=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000)
if type(v.pending_until)~='number' or clock>v.pending_until then return 0 end
local indexError=checkIndex(KEYS[3],ARGV[1]);if indexError then return -1 end
local mt=redis.call('TYPE',KEYS[4]).ok;if mt~='none' and mt~='zset' then return -1 end
if redis.call('SCARD',KEYS[2])>=512 and redis.call('SISMEMBER',KEYS[2],ARGV[1])==0 then return -1 end
v.state='admitted';v.pending_until=nil
redis.call('SET',KEYS[1],cjson.encode(v),'PX',ttl);redis.call('SADD',KEYS[2],ARGV[1]);if redis.call('PTTL',KEYS[2])<ttl then redis.call('PEXPIRE',KEYS[2],ttl) end
redis.call('PEXPIRE',KEYS[4],ttl);touchIndex(KEYS[3],ARGV[1],ttl)
return 1
`)

// ConfirmAggregationSuppression 在调用方复核真实主 Alert 后原子确认 owner/代次并冻结 Event 的首次抑制结果。
// 返回 false 表示复核期间窗口已改变，应有界重试；不能关联到未经确认的候选。
func (s *Store) ConfirmAggregationSuppression(ctx context.Context, r AggregationRequest, d AggregationDecision) (AggregationDecision, bool, error) {
	if err := r.validate(); err != nil {
		return AggregationDecision{}, false, err
	}
	keys, id, signature := s.aggregationKeys(r)
	if err := d.validate(); err != nil || d.WindowID != id || (d.Role != "owner" && d.Role != "suppressed") || d.OwnerEventID == r.EventID {
		return AggregationDecision{}, false, ErrState
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := confirmAggregationScript.Run(ctx, s.client, []string{keys[0], keys[2], keys[4]}, id, d.Epoch, d.OwnerAlertID, r.At.UnixMilli(), signature, r.Duration.Milliseconds()+60000, r.member()).Slice()
	if err == nil && len(raw) == 2 && raw[0] == "changed" {
		return AggregationDecision{}, false, nil
	}
	result, err := aggregationResult(raw, err, id)
	return result, err == nil, err
}

var confirmAggregationScript = redis.NewScript(`
for i=1,2 do local t=redis.call('TYPE',KEYS[i]).ok;if t~='none' and t~='string' then return {'state','type'} end end
local cached=redis.call('GET',KEYS[2])
if cached then local ok,op=pcall(cjson.decode,cached);if not ok or type(op)~='table' or op.signature~=ARGV[5] then return {'conflict','operation'} end;return {'replay',cjson.encode(op.decision)} end
local raw=redis.call('GET',KEYS[1]);if not raw then return {'changed','missing'} end
local ok,v=pcall(cjson.decode,raw);if not ok or type(v)~='table' or v.window_id~=ARGV[1] then return {'state','record'} end
if v.epoch~=ARGV[2] or v.owner_alert_id~=ARGV[3] or v.state~='admitted' then return {'changed','owner'} end
if type(v.started_at_ms)~='number' or type(v.expires_at_ms)~='number' then return {'state','clock'} end
if tonumber(ARGV[4])<v.started_at_ms or tonumber(ARGV[4])>v.expires_at_ms then return {'changed','expired'} end
local mt=redis.call('TYPE',KEYS[3]).ok;if mt~='none' and mt~='zset' then return {'state','members'} end
if redis.call('ZCARD',KEYS[3])>=10000 and not redis.call('ZSCORE',KEYS[3],ARGV[7]) then return {'budget','members'} end
redis.call('ZADD',KEYS[3],'NX',ARGV[4],ARGV[7])
-- 成员保留与固定窗口一致，后续事件不能续期整个窗口或其成员集合。
redis.call('PEXPIRE',KEYS[3],redis.call('PTTL',KEYS[1]))
v.role='suppressed'
redis.call('SET',KEYS[2],cjson.encode({signature=ARGV[5],decision=v}),'PX',ARGV[6])
return {'new',cjson.encode(v)}
`)

// ReleaseAggregation 取消未被选中/放行的候选，或移除已核实终结的主告警登记。
// 必须提供完整观察到的 owner 和代次；旧结果不能删除当前不同 owner 的窗口。
func (s *Store) ReleaseAggregation(ctx context.Context, tenant string, d AggregationDecision) (bool, error) {
	if err := domain.ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return false, err
	}
	if err := d.validate(); err != nil {
		return false, err
	}
	base := s.base(tenant)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := releaseAggregationScript.Run(ctx, s.client, []string{base + ":aggregation:" + d.WindowID, base + ":aggregation-owner:" + digest(d.OwnerAlertID), base + ":aggregation-members:" + d.WindowID, s.suppressionIndex(tenant, "aggregation")}, d.WindowID, d.Epoch, d.OwnerAlertID).Int()
	if err != nil {
		return false, err
	}
	if n < 0 {
		return false, ErrState
	}
	return n == 1, nil
}

var releaseAggregationScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return 0 end;if t~='string' then return -1 end
local it=redis.call('TYPE',KEYS[2]).ok;if it~='none' and it~='set' then return -1 end
local ok,v=pcall(cjson.decode,redis.call('GET',KEYS[1]));if not ok or type(v)~='table' or v.window_id~=ARGV[1] then return -1 end
if v.epoch~=ARGV[2] or v.owner_alert_id~=ARGV[3] then return 0 end
local rt=redis.call('TYPE',KEYS[4]).ok;if rt~='none' and rt~='zset' then return -1 end
local mt=redis.call('TYPE',KEYS[3]).ok;if mt~='none' and mt~='zset' then return -1 end
redis.call('DEL',KEYS[3]);redis.call('ZREM',KEYS[4],ARGV[1])
redis.call('DEL',KEYS[1]);redis.call('SREM',KEYS[2],ARGV[1]);if redis.call('SCARD',KEYS[2])==0 then redis.call('DEL',KEYS[2]) end
return 1
`)

// ClearAggregationOwner 终态按反向索引清理所有策略/来源下仍属于此 Alert 的登记，不扫描全局键。
func (s *Store) ClearAggregationOwner(ctx context.Context, tenant, owner string) ([]suppressioncleanup.Window, error) {
	if err := domain.ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return nil, err
	}
	if owner == "" || len(owner) > domain.EntityIDMaxBytes {
		return nil, fmt.Errorf("invalid aggregation owner")
	}
	base := s.base(tenant)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := clearAggregationScript.Run(ctx, s.client, []string{base + ":aggregation-owner:" + digest(owner), s.suppressionIndex(tenant, "aggregation")}, base+":aggregation:", owner, base+":aggregation-members:").Slice()
	return cleanupWindows(raw, err, "aggregation", "")
}

var clearAggregationScript = redis.NewScript(`
local rt=redis.call('TYPE',KEYS[2]).ok;if rt~='none' and rt~='zset' then return {'state'} end
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return {'ok'} end;if t~='set' or redis.call('SCARD',KEYS[1])>512 then return {'state'} end
local ids=redis.call('SMEMBERS',KEYS[1]);local owned={};local removed={'ok'}
for _,id in ipairs(ids) do
 if string.len(id)~=64 or string.find(id,'[^0-9a-f]') then return {'state'} end
 local key=ARGV[1]..id;local t=redis.call('TYPE',key).ok
 if t~='none' then
  if t~='string' then return {'state'} end
  local ok,v=pcall(cjson.decode,redis.call('GET',key));if not ok or type(v)~='table' or v.window_id~=id then return {'state'} end
  if v.owner_alert_id==ARGV[2] then local mt=redis.call('TYPE',ARGV[3]..id).ok;if mt~='none' and mt~='zset' then return {'state'} end;if type(v.epoch)~='string' or string.len(v.epoch)==0 or string.len(v.epoch)>160 then return {'state'} end;table.insert(owned,key);table.insert(removed,id);table.insert(removed,v.epoch);table.insert(removed,0) end
 end
end
for _,key in ipairs(owned) do local id=string.sub(key,-64);redis.call('DEL',key,ARGV[3]..id);redis.call('ZREM',KEYS[2],id) end
redis.call('DEL',KEYS[1]);return removed
`)
