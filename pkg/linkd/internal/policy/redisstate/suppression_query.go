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
	"fmt"
	"strconv"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/policy"
)

// 查询登记按租户/方式分别限制为 65536 项；所有清理都在策略写路径完成，查询没有修复副作用。
// 期限只是物理保留依据，不参与业务窗口身份，也不替代 Event 的首次裁决。
const suppressionIndexLua = `
local function checkIndex(key,id)
 local t=redis.call('TYPE',key).ok
 if t~='none' and t~='zset' then return 'state' end
 if redis.call('ZCARD',key)>65536 then return 'state' end
 local now=redis.call('TIME');local clock=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000)
 local expired=redis.call('ZRANGEBYSCORE',key,'-inf',clock,'LIMIT',0,256)
 if #expired>0 then redis.call('ZREM',key,unpack(expired)) end
 if redis.call('ZCARD',key)>=65536 and not redis.call('ZSCORE',key,id) then return 'budget' end
 return nil
end
local function touchIndex(key,id,ttl)
 local now=redis.call('TIME');local clock=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000)
 redis.call('ZADD',key,clock+ttl,id)
 if redis.call('PTTL',key)<ttl then redis.call('PEXPIRE',key,ttl) end
end
`

type suppressionDescriptor struct {
	Policy          domain.PolicyVersion `json:"policy"`
	SourceID        string               `json:"event_source_id,omitempty"`
	Fingerprint     string               `json:"fingerprint,omitempty"`
	GroupKey        string               `json:"group_key,omitempty"`
	DurationSeconds int64                `json:"duration_seconds"`
	Threshold       int                  `json:"threshold,omitempty"`
}

func (r ClipRequest) descriptor() string {
	raw, _ := json.Marshal(suppressionDescriptor{Policy: domain.PolicyVersion{ID: r.PolicyID, Version: r.Version, Digest: r.Digest}, SourceID: r.SourceID, Fingerprint: r.Fingerprint, DurationSeconds: int64(r.Duration.Seconds()), Threshold: r.Threshold})
	return string(raw)
}

func (r AggregationRequest) descriptor() string {
	raw, _ := json.Marshal(suppressionDescriptor{Policy: domain.PolicyVersion{ID: r.PolicyID, Version: r.Version, Digest: r.Digest}, GroupKey: r.GroupKey, DurationSeconds: int64(r.Duration.Seconds())})
	return string(raw)
}

// SuppressionMember 是 Redis 当前仍保留的 Event 身份；不包含 Event 载荷或完整分组值。
type SuppressionMember struct {
	EventID     string `json:"event_id"`
	SourceID    string `json:"event_source_id"`
	Fingerprint string `json:"fingerprint"`
	AtMillis    int64  `json:"at_ms"`
}

func (r AggregationRequest) member() string {
	raw, _ := json.Marshal(SuppressionMember{EventID: r.EventID, SourceID: r.SourceID, Fingerprint: r.Fingerprint, AtMillis: r.At.UnixMilli()})
	return string(raw)
}

func (s *Store) suppressionIndex(tenant, kind string) string {
	return s.base(tenant) + ":suppression-index:" + kind
}

func clipRuntimeID(r ClipRequest, id string) string {
	return digest("identity", r.SourceID, r.Fingerprint) + ":" + id
}

func validSuppressionID(kind, id string) bool {
	if kind == "aggregation" {
		return mergeHash(id)
	}
	parts := strings.Split(id, ":")
	return kind == "clip" && len(parts) == 2 && mergeHash(parts[0]) && mergeHash(parts[1])
}

func (d suppressionDescriptor) validate(kind, tenant, id string) error {
	if d.Policy.Validate() != nil || d.DurationSeconds < 1 || d.DurationSeconds > 30*24*3600 {
		return ErrState
	}
	if kind == "clip" {
		if (Identity{TenantID: tenant, SourceID: d.SourceID, Fingerprint: d.Fingerprint}).Validate() != nil || d.Threshold < 1 || d.Threshold > 10000 || d.GroupKey != "" {
			return ErrState
		}
		subject := digest("identity", d.SourceID, d.Fingerprint)
		if id != subject+":"+digest("clip", d.Policy.ID, fmt.Sprint(d.Policy.Version), d.Policy.Digest, subject) {
			return ErrState
		}
	} else if !mergeHash(d.GroupKey) || d.SourceID != "" || d.Fingerprint != "" || d.Threshold != 0 || id != digest("aggregation", d.Policy.ID, fmt.Sprint(d.Policy.Version), d.Policy.Digest, d.GroupKey) {
		return ErrState
	}
	return nil
}

// SuppressionWindow 描述一次只读观察；Redis 占位状态不证明主 Alert 此刻仍活动或已完成处置。
// Clip 的 ID 包含身份哈希和 CounterID，聚合 ID 则为跨来源窗口身份；两者都不暴露 Redis 键。
type SuppressionWindow struct {
	ID                    string               `json:"id"`
	TenantID              string               `json:"bk_tenant_id"`
	Kind                  string               `json:"kind"`
	Policy                domain.PolicyVersion `json:"policy"`
	SourceID              string               `json:"event_source_id,omitempty"`
	Fingerprint           string               `json:"fingerprint,omitempty"`
	GroupKey              string               `json:"group_key,omitempty"`
	Epoch                 string               `json:"epoch"`
	OwnerAlertID          string               `json:"owner_alert_id,omitempty"`
	OwnerEventID          string               `json:"owner_event_id,omitempty"`
	OwnerSourceID         string               `json:"owner_source_id,omitempty"`
	OwnerFingerprint      string               `json:"owner_fingerprint,omitempty"`
	State                 string               `json:"state"`
	ObservedAtMillis      int64                `json:"observed_at_ms"`
	RetentionMillis       int64                `json:"retention_ms"`
	DurationSeconds       int64                `json:"duration_seconds"`
	Threshold             int                  `json:"threshold,omitempty"`
	Count                 *int                 `json:"observed_count,omitempty"`
	MemberCount           int                  `json:"member_count"`
	LastEvaluatedAtMillis int64                `json:"last_evaluated_at_ms,omitempty"`
	StartedAtMillis       int64                `json:"started_at_ms,omitempty"`
	ExpiresAtMillis       int64                `json:"expires_at_ms,omitempty"`
	PendingUntilMillis    int64                `json:"pending_until_ms,omitempty"`
}

// Validate 校验查询快照与其稳定身份、作用域和计数预算一致，不证明当前 Alert 处置资格。
func (v SuppressionWindow) Validate() error {
	d := suppressionDescriptor{Policy: v.Policy, SourceID: v.SourceID, Fingerprint: v.Fingerprint, GroupKey: v.GroupKey, DurationSeconds: v.DurationSeconds, Threshold: v.Threshold}
	if domain.ValidateIdentityPart("tenant", v.TenantID, 64) != nil || !validSuppressionID(v.Kind, v.ID) || d.validate(v.Kind, v.TenantID, v.ID) != nil {
		return ErrState
	}
	if v.Epoch == "" || len(v.Epoch) > domain.EntityIDMaxBytes || len(v.OwnerAlertID) > domain.EntityIDMaxBytes || v.ObservedAtMillis < 0 || v.ObservedAtMillis > 1<<46 || v.RetentionMillis < 0 || v.RetentionMillis > (30*24*time.Hour+time.Minute).Milliseconds() || v.MemberCount < 0 || v.MemberCount > 10000 {
		return ErrState
	}
	if v.Kind == "clip" {
		if v.State != "retained" || v.Count == nil || *v.Count < 0 || *v.Count > v.MemberCount || v.LastEvaluatedAtMillis < 0 || v.LastEvaluatedAtMillis > 1<<46 || v.OwnerSourceID != "" || v.OwnerFingerprint != "" || v.OwnerEventID != "" || v.StartedAtMillis != 0 || v.ExpiresAtMillis != 0 || v.PendingUntilMillis != 0 {
			return ErrState
		}
	} else {
		decision := AggregationDecision{Role: "owner", WindowID: v.ID, Epoch: v.Epoch, OwnerAlertID: v.OwnerAlertID, OwnerEventID: v.OwnerEventID, OwnerSourceID: v.OwnerSourceID, OwnerFingerprint: v.OwnerFingerprint, StartedAtMillis: v.StartedAtMillis, ExpiresAtMillis: v.ExpiresAtMillis}
		if decision.validate() != nil || v.Count != nil || v.LastEvaluatedAtMillis != 0 || (v.State != "pending" && v.State != "admitted") || v.ExpiresAtMillis-v.StartedAtMillis != d.DurationSeconds*1000 || (v.State == "pending" && (v.PendingUntilMillis < 1 || v.PendingUntilMillis > 1<<46)) || (v.State == "admitted" && v.PendingUntilMillis != 0) {
			return ErrState
		}
	}
	return nil
}

// SuppressionWindowPage 按当前登记顺序返回一页；过滤/过期可产生空页，Next 非空时须继续。
type SuppressionWindowPage struct {
	Items []SuppressionWindow
	Next  string
}

// ListSuppressionWindows 仅分页已有登记，单页最多 16 个身份，不扫描全局键，也不补写丢失索引。
// 游标身份消失后返回冲突，调用方从首页重读；跨页不是一致性快照，新状态须通过刷新发现。
func (s *Store) ListSuppressionWindows(ctx context.Context, tenant, kind, after string, limit int) (SuppressionWindowPage, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || (kind != "clip" && kind != "aggregation") || (after != "" && !validSuppressionID(kind, after)) || limit < 1 || limit > 16 {
		return SuppressionWindowPage{}, policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := listSuppressionScript.Run(ctx, s.client, []string{s.suppressionIndex(tenant, kind)}, after, limit).StringSlice()
	if err != nil {
		return SuppressionWindowPage{}, err
	}
	if len(raw) > 0 && raw[0] == "changed" {
		return SuppressionWindowPage{}, policy.ErrConflict
	}
	if len(raw) < 2 || raw[0] != "ok" || len(raw) > limit+2 || (raw[1] != "" && !validSuppressionID(kind, raw[1])) {
		return SuppressionWindowPage{}, ErrState
	}
	page := SuppressionWindowPage{Items: []SuppressionWindow{}, Next: raw[1]}
	if page.Next != "" && (len(raw) < 3 || page.Next != raw[len(raw)-1]) {
		return SuppressionWindowPage{}, ErrState
	}
	seen := map[string]bool{}
	for _, id := range raw[2:] {
		if !validSuppressionID(kind, id) || seen[id] {
			return SuppressionWindowPage{}, ErrState
		}
		seen[id] = true
		v, found, err := s.ReadSuppressionWindow(ctx, tenant, kind, id)
		if err != nil {
			return SuppressionWindowPage{}, err
		}
		if found {
			page.Items = append(page.Items, v)
		}
	}
	return page, nil
}

var listSuppressionScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok
if t~='none' and t~='zset' then return {'state'} end
if redis.call('ZCARD',KEYS[1])>65536 then return {'state'} end
local offset=0
if ARGV[1]~='' then local rank=redis.call('ZRANK',KEYS[1],ARGV[1]);if not rank then return {'changed'} end;offset=rank+1 end
local limit=tonumber(ARGV[2]);local ids=redis.call('ZRANGE',KEYS[1],offset,offset+limit)
for _,id in ipairs(ids) do if string.len(id)>129 then return {'state'} end end
local next='';if #ids>limit then next=ids[limit];table.remove(ids) end
local out={'ok',next};for _,id in ipairs(ids) do table.insert(out,id) end;return out
`)

func (s *Store) suppressionKeys(tenant, kind, id string) []string {
	if kind == "clip" {
		key := s.base(tenant) + ":clip:" + id
		return []string{key, key + ":meta"}
	}
	return []string{s.base(tenant) + ":aggregation:" + id, s.base(tenant) + ":aggregation-members:" + id}
}

// ReadSuppressionWindow 原子读取一个窗口，计数使用 Redis 观察时间；不裁剪过期成员、不续期、不重新裁决。
// 缺失只表示当前缓存没有该对象，不能替代 Event 已保存的历史结果。
func (s *Store) ReadSuppressionWindow(ctx context.Context, tenant, kind, id string) (SuppressionWindow, bool, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !validSuppressionID(kind, id) {
		return SuppressionWindow{}, false, policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := readSuppressionScript.Run(ctx, s.client, s.suppressionKeys(tenant, kind, id), kind, id).StringSlice()
	if err != nil {
		return SuppressionWindow{}, false, err
	}
	if len(raw) == 1 && raw[0] == "missing" {
		return SuppressionWindow{}, false, nil
	}
	if len(raw) != 2 || raw[0] != "ok" || len(raw[1]) > 8192 {
		return SuppressionWindow{}, false, ErrState
	}
	var wire struct {
		Descriptor suppressionDescriptor `json:"descriptor"`
		SuppressionWindow
	}
	if json.Unmarshal([]byte(raw[1]), &wire) != nil || wire.Descriptor.validate(kind, tenant, id) != nil {
		return SuppressionWindow{}, false, ErrState
	}
	v, d := wire.SuppressionWindow, wire.Descriptor
	v.ID, v.TenantID, v.Kind, v.Policy = id, tenant, kind, d.Policy
	v.SourceID, v.Fingerprint, v.GroupKey = d.SourceID, d.Fingerprint, d.GroupKey
	v.DurationSeconds, v.Threshold = d.DurationSeconds, d.Threshold
	if err := v.Validate(); err != nil {
		return SuppressionWindow{}, false, err
	}
	return v, true, nil
}

var readSuppressionScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;local mt=redis.call('TYPE',KEYS[2]).ok
local now=redis.call('TIME');local clock=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000)
if ARGV[1]=='clip' then
 if t=='none' and mt=='none' then return {'missing'} end
 if t~='zset' or mt~='hash' or redis.call('HLEN',KEYS[2])>8 or redis.call('ZCARD',KEYS[1])>10000 then return {'state'} end
 local raw=redis.call('HGET',KEYS[2],'descriptor');if not raw or string.len(raw)>2048 then return {'state'} end
 local ok,d=pcall(cjson.decode,raw);if not ok or type(d)~='table' or type(d.duration_seconds)~='number' or d.duration_seconds<1 or d.duration_seconds>2592000 then return {'state'} end
 local epoch=redis.call('HGET',KEYS[2],'epoch');local owner=redis.call('HGET',KEYS[2],'owner');local last=tonumber(redis.call('HGET',KEYS[2],'last_at'))
 if not epoch or string.len(epoch)>256 or not owner or string.len(owner)>256 or not last or redis.call('HGET',KEYS[2],'scope')~=string.sub(ARGV[2],1,64) then return {'state'} end
 local ttl=math.min(redis.call('PTTL',KEYS[1]),redis.call('PTTL',KEYS[2]))
 local count=redis.call('ZCOUNT',KEYS[1],clock-d.duration_seconds*1000,clock)
 return {'ok',cjson.encode({descriptor=d,epoch=epoch,owner_alert_id=owner,state='retained',observed_at_ms=clock,retention_ms=ttl,observed_count=count,member_count=redis.call('ZCARD',KEYS[1]),last_evaluated_at_ms=last})}
end
if t=='none' then return {'missing'} end
if t~='string' or (mt~='none' and mt~='zset') or redis.call('STRLEN',KEYS[1])>4096 or redis.call('ZCARD',KEYS[2])>10000 then return {'state'} end
local ok,v=pcall(cjson.decode,redis.call('GET',KEYS[1]));if not ok or type(v)~='table' or v.window_id~=ARGV[2] then return {'state'} end
v.observed_at_ms=clock;v.retention_ms=redis.call('PTTL',KEYS[1]);v.member_count=redis.call('ZCARD',KEYS[2]);v.pending_until_ms=v.pending_until;return {'ok',cjson.encode(v)}
`)

// SuppressionMemberPage 只包含当前仍保留的成员；Epoch 绑定当前代次，Next 是该代次中的有界页偏移。
type SuppressionMemberPage struct {
	Epoch            string
	Items            []SuppressionMember
	Next             string
	ObservedAtMillis int64
}

// ListSuppressionMembers 拒绝在换代后沿用旧游标，读取不增补缺失成员。分页期间可有新成员加入，因此不是历史快照。
func (s *Store) ListSuppressionMembers(ctx context.Context, tenant, kind, id, epoch, after string, limit int) (SuppressionMemberPage, error) {
	if epoch == "" || len(epoch) > domain.EntityIDMaxBytes || limit < 1 || limit > 16 {
		return SuppressionMemberPage{}, policy.ErrInvalid
	}
	offset := 0
	if after != "" {
		n, err := strconv.Atoi(after)
		if err != nil || n < 1 || n > 10000 || strconv.Itoa(n) != after {
			return SuppressionMemberPage{}, policy.ErrInvalid
		}
		offset = n
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	v, found, err := s.ReadSuppressionWindow(ctx, tenant, kind, id)
	if err != nil {
		return SuppressionMemberPage{}, err
	}
	if !found {
		return SuppressionMemberPage{}, policy.ErrNotFound
	}
	if v.Epoch != epoch {
		return SuppressionMemberPage{}, policy.ErrConflict
	}
	raw, err := readSuppressionMembersScript.Run(ctx, s.client, s.suppressionKeys(tenant, kind, id), kind, id, epoch, offset, limit).StringSlice()
	if err != nil {
		return SuppressionMemberPage{}, err
	}
	if len(raw) > 0 && raw[0] == "changed" {
		return SuppressionMemberPage{}, policy.ErrConflict
	}
	if len(raw) < 3 || raw[0] != "ok" || len(raw) > 3+limit {
		return SuppressionMemberPage{}, ErrState
	}
	now, err := strconv.ParseInt(raw[1], 10, 64)
	if err != nil || now < 0 || now > 1<<46 {
		return SuppressionMemberPage{}, ErrState
	}
	page := SuppressionMemberPage{Epoch: epoch, Items: []SuppressionMember{}, Next: raw[2], ObservedAtMillis: now}
	if page.Next != "" && page.Next != strconv.Itoa(offset+limit) {
		return SuppressionMemberPage{}, ErrState
	}
	seen := map[string]bool{}
	for _, entry := range raw[3:] {
		var member SuppressionMember
		if len(entry) > 2048 || json.Unmarshal([]byte(entry), &member) != nil {
			return SuppressionMemberPage{}, ErrState
		}
		if kind == "clip" {
			member.SourceID, member.Fingerprint = v.SourceID, v.Fingerprint
		}
		if member.EventID == "" || len(member.EventID) > domain.EntityIDMaxBytes || seen[member.EventID] || (Identity{TenantID: tenant, SourceID: member.SourceID, Fingerprint: member.Fingerprint}).Validate() != nil || member.AtMillis < 0 || member.AtMillis > 1<<46 {
			return SuppressionMemberPage{}, ErrState
		}
		seen[member.EventID] = true
		page.Items = append(page.Items, member)
	}
	return page, nil
}

var readSuppressionMembersScript = redis.NewScript(`
local key=KEYS[2];local epoch=''
if ARGV[1]=='clip' then
 if redis.call('TYPE',KEYS[2]).ok~='hash' then return {'changed'} end
 epoch=redis.call('HGET',KEYS[2],'epoch');key=KEYS[1]
else
 if redis.call('TYPE',KEYS[1]).ok~='string' then return {'changed'} end
 if redis.call('STRLEN',KEYS[1])>4096 then return {'state'} end
 local ok,v=pcall(cjson.decode,redis.call('GET',KEYS[1]));if not ok or type(v)~='table' or v.window_id~=ARGV[2] then return {'state'} end;epoch=v.epoch
end
if epoch~=ARGV[3] then return {'changed'} end
local t=redis.call('TYPE',key).ok;if t~='none' and t~='zset' then return {'state'} end
local count=redis.call('ZCARD',key);if count>10000 then return {'state'} end
local offset=tonumber(ARGV[4]);local limit=tonumber(ARGV[5]);if offset>count then return {'changed'} end
local now=redis.call('TIME');local clock=tonumber(now[1])*1000+math.floor(tonumber(now[2])/1000)
local next='';if offset+limit<count then next=tostring(offset+limit) end
local out={'ok',tostring(clock),next};local rows=redis.call('ZRANGE',key,offset,offset+limit-1,'WITHSCORES')
for i=1,#rows,2 do
 if string.len(rows[i])>2048 then return {'state'} end
 local v=rows[i]
 if ARGV[1]=='clip' then v=cjson.encode({event_id=v,at_ms=tonumber(rows[i+1])})
 else local ok,m=pcall(cjson.decode,v);if not ok or type(m)~='table' or m.at_ms~=tonumber(rows[i+1]) then return {'state'} end end
 table.insert(out,v)
end
return out
`)
