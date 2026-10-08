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
	"errors"
	"fmt"
	"slices"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/policy"
)

// MergeRequest 的首次时间来自冻结 PolicyContext；身份是 Alert，Event 仅用于固定首次裁决。
type MergeRequest struct {
	TenantID   string
	Policy     domain.PolicyVersion
	GroupKey   string
	Member     domain.DependencyMain
	Groups     []int
	GroupCount int
	At         time.Time
	Duration   time.Duration
	Cyclic     bool
}

// MergeMember 只在真实 Alert CAS 后 committed；注册本身不能证明业务对象存在。
type MergeMember struct {
	Main          domain.DependencyMain `json:"main"`
	Groups        []int                 `json:"groups"`
	FirstAtMillis int64                 `json:"first_at_ms"`
	Committed     bool                  `json:"committed"`
}

// MergeWindow 是有界 Redis 快照；Frozen 非空后不能再向同一窗口加成员。
type MergeWindow struct {
	ID              string               `json:"id"`
	TenantID        string               `json:"tenant_id"`
	Policy          domain.PolicyVersion `json:"policy"`
	GroupKey        string               `json:"group_key"`
	StartedAtMillis int64                `json:"started_at_ms"`
	DeadlineMillis  int64                `json:"deadline_ms"`
	GroupCount      int                  `json:"group_count"`
	Cyclic          bool                 `json:"cyclic"`
	Revision        int64                `json:"revision"`
	Members         []MergeMember        `json:"members,omitempty"`
	Frozen          *MergeVerdict        `json:"frozen,omitempty"`
}

// MergeVerdict 固定本次成功成员及操作身份；后续必须持久化业务裁决，不能只靠该缓存完成父子关系。
type MergeVerdict struct {
	Outcome     string   `json:"outcome"`
	OperationID string   `json:"operation_id"`
	AtMillis    int64    `json:"at_ms"`
	MemberIDs   []string `json:"member_ids"`
}

func mergeHash(v string) bool { raw, err := hex.DecodeString(v); return err == nil && len(raw) == 32 }

func (r MergeRequest) validate() error {
	if domain.ValidateIdentityPart("tenant", r.TenantID, 64) != nil || r.Policy.Validate() != nil || !mergeHash(r.GroupKey) || r.Member.Validate() != nil {
		return policy.ErrInvalid
	}
	if r.At.IsZero() || r.At.UnixMilli() < 0 || r.At.UnixMilli() > 1<<46 || r.Duration < time.Second || r.Duration > 24*time.Hour || r.Duration%time.Second != 0 || r.GroupCount < 1 || r.GroupCount > 32 || len(r.Groups) < 1 || len(r.Groups) > r.GroupCount {
		return policy.ErrInvalid
	}
	for i, g := range r.Groups {
		if g < 0 || g >= r.GroupCount || (i > 0 && r.Groups[i-1] >= g) {
			return policy.ErrInvalid
		}
	}
	return nil
}

func (s *Store) mergeWindowKey(tenant, id string) string {
	return s.base(tenant) + ":merge-window:" + id
}

func (s *Store) mergeDueKey(tenant string) string { return s.base(tenant) + ":merge-due" }

func (s *Store) mergeGroupKey(r MergeRequest) string {
	return s.base(r.TenantID) + ":merge-group:" + digest(r.Policy.ID, fmt.Sprint(r.Policy.Version), r.Policy.Digest, r.GroupKey)
}

func mergeWindowID(r MergeRequest) string {
	return digest("merge-window", r.TenantID, r.Policy.ID, fmt.Sprint(r.Policy.Version), r.Policy.Digest, r.GroupKey, r.Member.EventID)
}

func mergeMemberField(id string) string { return "m:" + digest("member", id) }

// JoinMergeWindow 原子创建固定半开窗口 [start, deadline) 并按 Alert 去重；相同 Event 重试保留首次结果。
// 每窗口最多 256 成员、租户最多 4096 待检查窗口；超限整次跳过，不截断后宣称成功。
func (s *Store) JoinMergeWindow(ctx context.Context, r MergeRequest) (domain.MergeWait, error) {
	if err := r.validate(); err != nil {
		return domain.MergeWait{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	proposed := MergeWindow{ID: mergeWindowID(r), TenantID: r.TenantID, Policy: r.Policy, GroupKey: r.GroupKey, StartedAtMillis: r.At.UnixMilli(), DeadlineMillis: r.At.Add(r.Duration).UnixMilli(), GroupCount: r.GroupCount, Cyclic: r.Cyclic, Revision: 1}
	member := MergeMember{Main: r.Member, Groups: slices.Clone(r.Groups), FirstAtMillis: r.At.UnixMilli()}
	meta, _ := json.Marshal(proposed)
	raw, _ := json.Marshal(member)
	decisionKey := s.base(r.TenantID) + ":merge-event:" + digest(r.Policy.ID, fmt.Sprint(r.Policy.Version), r.Policy.Digest, r.GroupKey, r.Member.EventID, r.Member.AlertID)
	signature := digest(string(raw), fmt.Sprint(r.GroupCount), fmt.Sprint(r.Duration), fmt.Sprint(r.Cyclic))
	out, err := joinMergeScript.Run(ctx, s.client, []string{s.mergeGroupKey(r), s.mergeDueKey(r.TenantID), decisionKey}, s.base(r.TenantID)+":merge-window:", string(meta), string(raw), mergeMemberField(r.Member.AlertID), r.At.UnixMilli(), signature).Slice()
	if err != nil {
		return domain.MergeWait{}, err
	}
	if len(out) != 3 && len(out) != 4 {
		return domain.MergeWait{}, ErrState
	}
	code, _ := out[0].(string)
	switch code {
	case "budget":
		return domain.MergeWait{}, ErrBudget
	case "conflict":
		return domain.MergeWait{}, policy.ErrConflict
	case "late":
		return domain.MergeWait{}, policy.ErrUnavailable
	case "joined":
	default:
		return domain.MergeWait{}, ErrState
	}
	metaText, ok := out[1].(string)
	if !ok {
		return domain.MergeWait{}, ErrState
	}
	memberText, ok := out[2].(string)
	if !ok {
		return domain.MergeWait{}, ErrState
	}
	var w MergeWindow
	var m MergeMember
	if json.Unmarshal([]byte(metaText), &w) != nil || json.Unmarshal([]byte(memberText), &m) != nil || w.TenantID != r.TenantID || w.Policy != r.Policy || w.GroupKey != r.GroupKey || m.Main.AlertID != r.Member.AlertID || m.Main.EventSourceID != r.Member.EventSourceID || m.Main.Fingerprint != r.Member.Fingerprint {
		return domain.MergeWait{}, ErrState
	}
	wait := domain.MergeWait{WindowID: w.ID, Policy: w.Policy, GroupKey: w.GroupKey, MemberEventID: m.Main.EventID, Severity: m.Main.Severity, Groups: slices.Clone(m.Groups), StartedAt: time.UnixMilli(w.StartedAtMillis).UTC(), Deadline: time.UnixMilli(w.DeadlineMillis).UTC()}
	if err := wait.Validate(); err != nil {
		return domain.MergeWait{}, ErrState
	}
	s.observeInit(ctx, "merge", out)
	return wait, nil
}

var joinMergeScript = redis.NewScript(`
local types={'string','zset','string'}
for i=1,3 do local t=redis.call('TYPE',KEYS[i]).ok;if t~='none' and t~=types[i] then return {'state','',''} end end
local prior=redis.call('GET',KEYS[3])
if prior then
 if string.len(prior)>8192 then return {'state','',''} end
 local ok,p=pcall(cjson.decode,prior);if not ok or type(p)~='table' then return {'state','',''} end
 if p.signature~=ARGV[6] then return {'conflict','',''} end
 return {'joined',p.meta,p.member}
end
local proposal=cjson.decode(ARGV[2]);local member=cjson.decode(ARGV[3]);local at=tonumber(ARGV[5])
local id=redis.call('GET',KEYS[1]);local key=nil;local meta=nil
if id then
 if string.len(id)~=64 or string.find(id,'[^a-f0-9]') then return {'state','',''} end
 key=ARGV[1]..id;local t=redis.call('TYPE',key).ok
 if t~='none' and t~='hash' then return {'state','',''} end
 local raw=redis.call('HGET',key,'meta')
 if raw then
  if string.len(raw)>262144 then return {'state','',''} end
  local ok,v=pcall(cjson.decode,raw);if not ok or type(v)~='table' or type(v.started_at_ms)~='number' or type(v.deadline_ms)~='number' or type(v.revision)~='number' then return {'state','',''} end
  if at<v.started_at_ms then return {'late','',''} end
  if not v.frozen and at<v.deadline_ms then meta=v end
 end
end
local initialization=''
if not meta then
 initialization='created'
 if redis.call('ZCARD',KEYS[2])>=4096 then return {'budget','',''} end
 meta=proposal;key=ARGV[1]..meta.id
 if redis.call('EXISTS',key)==1 then return {'conflict','',''} end
else
 if meta.group_count~=proposal.group_count or meta.cyclic~=proposal.cyclic then return {'conflict','',''} end
end
local existing=redis.call('HGET',key,ARGV[4])
if existing then
 if string.len(existing)>4096 then return {'state','',''} end
 local ok,v=pcall(cjson.decode,existing);if not ok or type(v)~='table' or type(v.main)~='table' then return {'state','',''} end
 if v.main.alert_id~=member.main.alert_id or v.main.event_source_id~=member.main.event_source_id or v.main.fingerprint~=member.main.fingerprint then return {'conflict','',''} end
 member=v
else
 if redis.call('HLEN',key)>=257 then return {'budget','',''} end
 meta.revision=meta.revision+1
end
local metaText=cjson.encode(meta);local memberText=cjson.encode(member)
redis.call('HSET',key,'meta',metaText,ARGV[4],memberText)
redis.call('PEXPIRE',key,172800000)
redis.call('SET',KEYS[1],meta.id,'PX',172800000)
redis.call('ZADD',KEYS[2],'NX',meta.deadline_ms,meta.id);redis.call('PEXPIRE',KEYS[2],172800000)
redis.call('SET',KEYS[3],cjson.encode({signature=ARGV[6],meta=metaText,member=memberText}),'PX',172800000)
return {'joined',metaText,memberText,initialization}
`)

// CommitMergeMember 在真实 Alert 已持久化其等待引用后确认成员，不凭预期 AlertID 参与裁决。
// 只核对首次成员身份；后续 Event 的同 Alert 加入仍复用该首次引用。
func (s *Store) CommitMergeMember(ctx context.Context, tenant, alertID string, w domain.MergeWait) (bool, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alertID == "" || len(alertID) > domain.EntityIDMaxBytes || w.Validate() != nil {
		return false, policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := commitMergeScript.Run(ctx, s.client, []string{s.mergeWindowKey(tenant, w.WindowID), s.mergeDueKey(tenant)}, mergeMemberField(alertID), alertID, w.MemberEventID, w.Severity, w.WindowID).Int()
	if err != nil {
		return false, err
	}
	if n < 0 {
		return false, ErrState
	}
	return n == 1, nil
}

var commitMergeScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return 0 end;if t~='hash' then return -1 end
local raw=redis.call('HGET',KEYS[1],'meta');local m=redis.call('HGET',KEYS[1],ARGV[1]);if not raw or not m then return 0 end;if string.len(raw)>262144 or string.len(m)>4096 then return -1 end
local ok,v=pcall(cjson.decode,raw);local mok,member=pcall(cjson.decode,m)
if not ok or not mok or type(v)~='table' or type(member)~='table' or type(member.main)~='table' or type(v.revision)~='number' then return -1 end
if member.main.alert_id~=ARGV[2] or member.main.event_id~=ARGV[3] or member.main.severity~=ARGV[4] then return -1 end
if member.committed then return 1 end;if v.frozen then return 0 end
local dt=redis.call('TYPE',KEYS[2]).ok;if dt~='none' and dt~='zset' then return -1 end
member.committed=true;v.revision=v.revision+1
redis.call('HSET',KEYS[1],'meta',cjson.encode(v),ARGV[1],cjson.encode(member))
if not v.cyclic then redis.call('ZADD',KEYS[2],v.started_at_ms,ARGV[5]) end
return 1
`)

// ReadMergeWindow 原子读取完整有界快照；允许尚未确认成员，但裁决必须另行核对真实 Alert。
func (s *Store) ReadMergeWindow(ctx context.Context, tenant, id string) (MergeWindow, bool, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !mergeHash(id) {
		return MergeWindow{}, false, policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := readMergeScript.Run(ctx, s.client, []string{s.mergeWindowKey(tenant, id)}).StringSlice()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return MergeWindow{}, false, nil
		}
		return MergeWindow{}, false, err
	}
	if len(out) == 0 {
		return MergeWindow{}, false, nil
	}
	if len(out) < 2 || out[0] != "ok" || len(out) > 258 {
		return MergeWindow{}, false, ErrState
	}
	var w MergeWindow
	if json.Unmarshal([]byte(out[1]), &w) != nil || w.ID != id || w.TenantID != tenant || w.Policy.Validate() != nil || !mergeHash(w.GroupKey) || w.Revision < 1 || len(w.Members) > 0 || w.GroupCount < 1 || w.GroupCount > 32 || w.DeadlineMillis <= w.StartedAtMillis || w.DeadlineMillis-w.StartedAtMillis > 86400000 {
		return MergeWindow{}, false, ErrState
	}
	if w.Frozen != nil {
		f := w.Frozen
		if !mergeHash(f.OperationID) || (f.Outcome != "succeeded" && f.Outcome != "failed") || f.AtMillis < w.StartedAtMillis || len(f.MemberIDs) > 256 || (f.Outcome == "succeeded" && len(f.MemberIDs) < 2) {
			return MergeWindow{}, false, ErrState
		}
		for i, id := range f.MemberIDs {
			if id == "" || len(id) > domain.EntityIDMaxBytes || (i > 0 && f.MemberIDs[i-1] >= id) {
				return MergeWindow{}, false, ErrState
			}
		}
	}
	seen := map[string]bool{}
	committed := map[string]bool{}
	for _, raw := range out[2:] {
		var m MergeMember
		if len(raw) > 4096 || json.Unmarshal([]byte(raw), &m) != nil || m.Main.Validate() != nil || seen[m.Main.AlertID] || len(m.Groups) == 0 || len(m.Groups) > w.GroupCount || m.FirstAtMillis < w.StartedAtMillis || m.FirstAtMillis >= w.DeadlineMillis {
			return MergeWindow{}, false, ErrState
		}
		for i, g := range m.Groups {
			if g < 0 || g >= w.GroupCount || (i > 0 && m.Groups[i-1] >= g) {
				return MergeWindow{}, false, ErrState
			}
		}
		seen[m.Main.AlertID] = true
		committed[m.Main.AlertID] = m.Committed
		w.Members = append(w.Members, m)
	}
	if w.Frozen != nil {
		for _, id := range w.Frozen.MemberIDs {
			if !committed[id] {
				return MergeWindow{}, false, ErrState
			}
		}
	}
	slices.SortFunc(w.Members, func(a, b MergeMember) int {
		if a.Main.AlertID < b.Main.AlertID {
			return -1
		}
		if a.Main.AlertID > b.Main.AlertID {
			return 1
		}
		return 0
	})
	return w, true, nil
}

var readMergeScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return {} end;if t~='hash' then return {'state'} end
if redis.call('HLEN',KEYS[1])>257 then return {'state'} end
local meta=redis.call('HGET',KEYS[1],'meta');if not meta or string.len(meta)>262144 then return {'state'} end
local out={'ok',meta};local entries=redis.call('HGETALL',KEYS[1])
for i=1,#entries,2 do if entries[i]~='meta' then if string.sub(entries[i],1,2)~='m:' or string.len(entries[i+1])>4096 then return {'state'} end;table.insert(out,entries[i+1]) end end
return out
`)

// MergeCandidate 是控制面重读成员 Alert 后得到的当前有效条件组；不能直接相信旧成员快照。
type MergeCandidate struct {
	AlertID string `json:"alert_id"`
	Groups  []int  `json:"groups"`
}

// FreezeMergeWindow 用完整快照 revision 防止选成员期间又有加入/确认；重试返回已冻结结果。
// 非周期满足全部组且至少两个唯一 Alert 时提前成功，周期只能在精确截止点之后裁决。
func (s *Store) FreezeMergeWindow(ctx context.Context, tenant, id string, revision int64, at time.Time, candidates []MergeCandidate) (MergeWindow, bool, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !mergeHash(id) || revision < 1 || at.IsZero() || at.UnixMilli() < 0 || at.UnixMilli() > 1<<46 || len(candidates) > 256 {
		return MergeWindow{}, false, policy.ErrInvalid
	}
	seen := map[string]bool{}
	selected := append([]MergeCandidate{}, candidates...)
	for _, c := range selected {
		if c.AlertID == "" || len(c.AlertID) > domain.EntityIDMaxBytes || seen[c.AlertID] || len(c.Groups) < 1 || len(c.Groups) > 32 {
			return MergeWindow{}, false, policy.ErrInvalid
		}
		seen[c.AlertID] = true
		for i, g := range c.Groups {
			if g < 0 || g >= 32 || (i > 0 && c.Groups[i-1] >= g) {
				return MergeWindow{}, false, policy.ErrInvalid
			}
		}
	}
	slices.SortFunc(selected, func(a, b MergeCandidate) int {
		if a.AlertID < b.AlertID {
			return -1
		}
		if a.AlertID > b.AlertID {
			return 1
		}
		return 0
	})
	fields := make([]string, 0, len(selected))
	for _, c := range selected {
		fields = append(fields, mergeMemberField(c.AlertID))
	}
	// 窗口身份已经包含策略版本/分组；不能把当前检查时间放进本次操作身份。
	operation, err := domain.MergeDecisionID(tenant, id)
	if err != nil {
		return MergeWindow{}, false, err
	}
	raw, _ := json.Marshal(selected)
	rawFields, _ := json.Marshal(fields)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := freezeMergeScript.Run(ctx, s.client, []string{s.mergeWindowKey(tenant, id)}, revision, at.UnixMilli(), string(raw), string(rawFields), operation).Int()
	if err != nil {
		return MergeWindow{}, false, err
	}
	if n < 0 {
		return MergeWindow{}, false, ErrState
	}
	if n == 0 {
		return MergeWindow{}, false, nil
	}
	w, found, err := s.ReadMergeWindow(ctx, tenant, id)
	if err != nil {
		return MergeWindow{}, false, err
	}
	if !found {
		return MergeWindow{}, false, nil
	}
	return w, w.Frozen != nil, nil
}

var freezeMergeScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;if t=='none' then return 0 end;if t~='hash' then return -1 end
local raw=redis.call('HGET',KEYS[1],'meta');if not raw or string.len(raw)>262144 then return -1 end
local ok,v=pcall(cjson.decode,raw);if not ok or type(v)~='table' or type(v.revision)~='number' or type(v.deadline_ms)~='number' or type(v.group_count)~='number' then return -1 end
if v.frozen then return 1 end;if v.revision~=tonumber(ARGV[1]) then return 0 end
local at=tonumber(ARGV[2]);if at<v.started_at_ms or (v.cyclic and at<v.deadline_ms) then return 0 end
local candidates=cjson.decode(ARGV[3]);local fields=cjson.decode(ARGV[4]);local groups={};local ids={}
for i,c in ipairs(candidates) do
 local raw=redis.call('HGET',KEYS[1],fields[i]);if not raw or string.len(raw)>4096 then return 0 end
 local ok,m=pcall(cjson.decode,raw);if not ok or type(m)~='table' or type(m.main)~='table' then return -1 end
 if not m.committed or m.main.alert_id~=c.alert_id then return 0 end
 for _,g in ipairs(c.groups) do if g<0 or g>=v.group_count then return -1 end;groups[tostring(g)]=true end
 table.insert(ids,c.alert_id)
end
local success=#ids>=2
for g=0,v.group_count-1 do if not groups[tostring(g)] then success=false end end
if not success and at<v.deadline_ms then return 0 end
v.revision=v.revision+1;v.frozen={outcome=success and 'succeeded' or 'failed',operation_id=ARGV[5],at_ms=at,member_ids=candidates[1] and ids or cjson.empty_array}
-- Redis Lua 不同版本对空数组支持不同；无成员时省略该字段，Go 解码为 nil。
if #ids==0 then v.frozen.member_ids=nil end
redis.call('HSET',KEYS[1],'meta',cjson.encode(v));return 1
`)

// ListMergeDue 返回当前租户最多 32 个到期提示；消费者仍须核对实际窗口/成员，不信任索引即成功。
func (s *Store) ListMergeDue(ctx context.Context, tenant string, at time.Time, limit int) ([]string, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || at.IsZero() || limit < 1 || limit > 32 {
		return nil, policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ids, err := s.client.ZRangeArgs(ctx, redis.ZRangeArgs{Key: s.mergeDueKey(tenant), Start: "-inf", Stop: fmt.Sprint(at.UnixMilli()), ByScore: true, Count: int64(limit)}).Result()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !mergeHash(id) {
			return nil, ErrState
		}
	}
	return ids, nil
}

// FinishMergeWindow 只删除已完成冻结窗口的提示，不删除结果或更新分组指针；迟到确认不影响新窗口。
func (s *Store) FinishMergeWindow(ctx context.Context, tenant, id string) error {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !mergeHash(id) {
		return policy.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := finishMergeScript.Run(ctx, s.client, []string{s.mergeWindowKey(tenant, id), s.mergeDueKey(tenant)}, id).Int()
	if err != nil {
		return err
	}
	if n < 0 {
		return ErrState
	}
	return nil
}

var finishMergeScript = redis.NewScript(`
local t=redis.call('TYPE',KEYS[1]).ok;local d=redis.call('TYPE',KEYS[2]).ok
if (t~='none' and t~='hash') or (d~='none' and d~='zset') then return -1 end
if t=='hash' then local raw=redis.call('HGET',KEYS[1],'meta');if not raw or string.len(raw)>262144 then return -1 end;local ok,v=pcall(cjson.decode,raw);if not ok or type(v)~='table' or not v.frozen then return -1 end end
redis.call('ZREM',KEYS[2],ARGV[1]);return 1
`)
