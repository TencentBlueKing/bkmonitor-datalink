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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/policy"
)

// DependencyRequest 只按租户与策略版本协调，来源身份放在候选内，不进入跨来源选主键。
type DependencyRequest struct {
	TenantID  string
	Policy    domain.PolicyVersion
	Candidate domain.DependencyMain
}

// DependencyReservation 区分未提交候选与真实存在的主；registered 不意味着处置已放行。
type DependencyReservation struct {
	Role string
	Main domain.DependencyMain
}

func (r DependencyRequest) validate() error {
	if err := domain.ValidateIdentityPart("tenant", r.TenantID, 64); err != nil {
		return err
	}
	if err := r.Policy.Validate(); err != nil {
		return err
	}
	return r.Candidate.Validate()
}

func (s *Store) dependencyKey(tenant string, p domain.PolicyVersion) string {
	return s.base(tenant) + ":dependency:" + digest(p.ID, fmt.Sprint(p.Version), p.Digest)
}

func (s *Store) dependencyOwner(tenant, owner string) string {
	return s.base(tenant) + ":dependency-owner:" + digest(owner)
}

// ClaimDependencyMain 对完整候选原子抢占；未提交候选只有 30 秒租期，不可直接作为关联主使用。
func (s *Store) ClaimDependencyMain(ctx context.Context, r DependencyRequest) (DependencyReservation, error) {
	if err := r.validate(); err != nil {
		return DependencyReservation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, _ := json.Marshal(r.Candidate)
	out, err := claimDependencyScript.Run(ctx, s.client, []string{s.dependencyKey(r.TenantID, r.Policy), s.dependencyOwner(r.TenantID, r.Candidate.AlertID)}, string(raw), r.Candidate.AlertID, r.Candidate.EventID, dependencySignature(r.Candidate)).Slice()
	if err != nil {
		return DependencyReservation{}, err
	}
	if len(out) != 2 {
		return DependencyReservation{}, ErrState
	}
	role, ok := out[0].(string)
	if !ok {
		return DependencyReservation{}, ErrState
	}
	if role == "conflict" {
		return DependencyReservation{}, policy.ErrConflict
	}
	if role == "budget" {
		return DependencyReservation{}, ErrBudget
	}
	if role != "candidate" && role != "pending" && role != "registered" {
		return DependencyReservation{}, ErrState
	}
	body, ok := out[1].(string)
	if !ok {
		return DependencyReservation{}, ErrState
	}
	var main domain.DependencyMain
	if err := json.Unmarshal([]byte(body), &main); err != nil || main.Validate() != nil {
		return DependencyReservation{}, ErrState
	}
	return DependencyReservation{Role: role, Main: main}, nil
}

var claimDependencyScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;local it=redis.call('TYPE',KEYS[2]).ok
if (t~='none' and t~='string') or (it~='none' and it~='set') then return {'state','type'} end
local raw=redis.call('GET',KEYS[1]);local current=nil
local stamp=redis.call('TIME');local now=tonumber(stamp[1])*1000+math.floor(tonumber(stamp[2])/1000)
if raw then
 if string.len(raw)>4096 then return {'state','record'} end
 local ok,v=pcall(cjson.decode,raw)
 if not ok or type(v)~='table' or type(v.main)~='table' or (v.state~='pending' and v.state~='registered') then return {'state','record'} end
 if v.state=='pending' and (type(v.until_ms)~='number' or now>v.until_ms) then current=nil else current=v end
end
if current then
 if current.main.alert_id==ARGV[2] and current.main.event_id==ARGV[3] then if current.signature~=ARGV[4] then return {'conflict','candidate'} end;return {'candidate',cjson.encode(current.main)} end
 return {current.state,cjson.encode(current.main)}
end
if redis.call('SCARD',KEYS[2])>=512 and redis.call('SISMEMBER',KEYS[2],KEYS[1])==0 then return {'budget','owners'} end
local ok,main=pcall(cjson.decode,ARGV[1]);if not ok then return {'state','input'} end
redis.call('SET',KEYS[1],cjson.encode({state='pending',main=main,signature=ARGV[4],until_ms=now+30000}),'PX',30000)
redis.call('SADD',KEYS[2],KEYS[1]);if redis.call('PTTL',KEYS[2])<2592000000 then redis.call('PEXPIRE',KEYS[2],2592000000) end
return {'candidate',ARGV[1]}
`)

// CommitDependencyMain 仅在对应 Alert CAS 成功后登记；被后续策略屏蔽/等待的主仍属于待处理主。
// 30 天仅用于协调缓存回收，不是 ShieldBinding TTL；丢失后允许正常重新登记。
func (s *Store) CommitDependencyMain(ctx context.Context, r DependencyRequest) (bool, error) {
	if err := r.validate(); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := commitDependencyScript.Run(ctx, s.client, []string{s.dependencyKey(r.TenantID, r.Policy)}, r.Candidate.AlertID, r.Candidate.EventID, dependencySignature(r.Candidate)).Int()
	if err != nil {
		return false, err
	}
	if n < 0 {
		return false, ErrState
	}
	return n == 1, nil
}

var commitDependencyScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return 0 end;if t~='string' then return -1 end
local raw=redis.call('GET',KEYS[1]);if string.len(raw)>4096 then return -1 end;local ok,v=pcall(cjson.decode,raw);if not ok or type(v)~='table' or type(v.main)~='table' then return -1 end
if v.main.alert_id~=ARGV[1] or v.main.event_id~=ARGV[2] then return 0 end
if v.signature~=ARGV[3] then return -1 end
if v.state=='registered' then return 1 end
local stamp=redis.call('TIME');local now=tonumber(stamp[1])*1000+math.floor(tonumber(stamp[2])/1000)
if v.state~='pending' or type(v.until_ms)~='number' then return -1 end;if now>v.until_ms then return 0 end
v.state='registered';v.until_ms=nil;redis.call('SET',KEYS[1],cjson.encode(v),'PX',2592000000);return 1
`)

// ReleaseDependencyMain 只移除仍由所观察 Event/Alert 占有的代次，旧清理不影响新主。
func (s *Store) ReleaseDependencyMain(ctx context.Context, r DependencyRequest) (bool, error) {
	if err := r.validate(); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := releaseDependencyScript.Run(ctx, s.client, []string{s.dependencyKey(r.TenantID, r.Policy), s.dependencyOwner(r.TenantID, r.Candidate.AlertID)}, r.Candidate.AlertID, r.Candidate.EventID).Int()
	if err != nil {
		return false, err
	}
	if n < 0 {
		return false, ErrState
	}
	return n == 1, nil
}

var releaseDependencyScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;local it=redis.call('TYPE',KEYS[2]).ok
if (t~='none' and t~='string') or (it~='none' and it~='set') then return -1 end
if t=='none' then redis.call('SREM',KEYS[2],KEYS[1]);return 0 end
local raw=redis.call('GET',KEYS[1]);if string.len(raw)>4096 then return -1 end;local ok,v=pcall(cjson.decode,raw);if not ok or type(v)~='table' or type(v.main)~='table' then return -1 end
if v.main.alert_id~=ARGV[1] or v.main.event_id~=ARGV[2] then return 0 end
redis.call('DEL',KEYS[1]);redis.call('SREM',KEYS[2],KEYS[1]);return 1
`)

// ClearDependencyOwner 使用有界反向索引清理终态主，不遍历全租户策略键。
func (s *Store) ClearDependencyOwner(ctx context.Context, tenant, owner string) (int, error) {
	if err := domain.ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return 0, err
	}
	if owner == "" || len(owner) > domain.EntityIDMaxBytes {
		return 0, ErrState
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := clearDependencyScript.Run(ctx, s.client, []string{s.dependencyOwner(tenant, owner)}, s.base(tenant)+":dependency:", owner).Int()
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, ErrState
	}
	return n, nil
}

var clearDependencyScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return 0 end;if t~='set' or redis.call('SCARD',KEYS[1])>512 then return -1 end
local keys=redis.call('SMEMBERS',KEYS[1]);local owned={}
for _,key in ipairs(keys) do
 if string.sub(key,1,string.len(ARGV[1]))~=ARGV[1] then return -1 end
 local t=redis.call('TYPE',key).ok;if t~='none' then
  if t~='string' then return -1 end
  local raw=redis.call('GET',key);if string.len(raw)>4096 then return -1 end;local ok,v=pcall(cjson.decode,raw);if not ok or type(v)~='table' or type(v.main)~='table' then return -1 end
  if v.main.alert_id==ARGV[2] then table.insert(owned,key) end
 end
end
for _,key in ipairs(owned) do redis.call('DEL',key) end
redis.call('DEL',KEYS[1]);return #owned
`)

func dependencySignature(m domain.DependencyMain) string {
	return digest(m.AlertID, m.EventID, m.EventSourceID, m.Fingerprint, m.Severity)
}

// GetDependencyMain 只读已登记/仍持有租期的候选；普通子告警不能抢占一个虚假的主位置。
func (s *Store) GetDependencyMain(ctx context.Context, tenant string, p domain.PolicyVersion) (DependencyReservation, bool, error) {
	if err := domain.ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return DependencyReservation{}, false, err
	}
	if err := p.Validate(); err != nil {
		return DependencyReservation{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := s.client.Get(ctx, s.dependencyKey(tenant, p)).Bytes()
	if errors.Is(err, redis.Nil) {
		return DependencyReservation{}, false, nil
	}
	if err != nil {
		return DependencyReservation{}, false, err
	}
	var v struct {
		State     string                `json:"state"`
		Main      domain.DependencyMain `json:"main"`
		Signature string                `json:"signature"`
	}
	if len(raw) > 4096 || json.Unmarshal(raw, &v) != nil || v.Main.Validate() != nil || (v.State != "pending" && v.State != "registered") || v.Signature != dependencySignature(v.Main) {
		return DependencyReservation{}, false, ErrState
	}
	return DependencyReservation{Role: v.State, Main: v.Main}, true, nil
}
