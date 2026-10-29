// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisstate

import (
	"context"
	"fmt"
	"strconv"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
)

// DependencyIndexRef 把现有等级配置摘要作为执行索引维度，不改变持久化的策略版本身份。
type DependencyIndexRef struct {
	domain.PolicyVersion
	ConfigDigest string
}

// ErrDependencySnapshotStale 要求保留当前事件，等待依赖刷新后补齐已保存 Alert 的索引。
var ErrDependencySnapshotStale error = dependencySnapshotStaleError{}

type dependencySnapshotStaleError struct{}

func (dependencySnapshotStaleError) Error() string {
	return "dependency source snapshot is older than indexed qualification"
}

func (dependencySnapshotStaleError) PolicyPreparationPending() bool { return true }

func (dependencySnapshotStaleError) CanRetry() bool { return true }

// DependencyMembership 保存本次已求值规则的主资格，不保存 Alert 正文或目标副本。
type DependencyMembership struct {
	Policy     domain.PolicyVersion
	Matched    bool
	ObservedAt time.Time
}

// DependencyCandidateUpdate 用 Alert 自身 revision 防止后台回填复活较旧主状态。
// Clear 用于失去主资格或终态；相同 revision 可补齐不同策略版本的后台回填。
type DependencyCandidateUpdate struct {
	TenantID     string
	ConfigDigest string
	AlertID      string
	Revision     int64
	BeginAt      time.Time
	Clear        bool
	Memberships  []DependencyMembership
}

func (s *Store) dependencyCandidateKey(tenant string, p DependencyIndexRef) string {
	return s.dependencyKey(tenant, p.PolicyVersion) + ":admitted:" + p.ConfigDigest
}

func (s *Store) dependencyCandidateOwner(tenant, id, configDigest string) string {
	return s.base(tenant) + ":dependency-admitted-owner-ns:" + configDigest + ":" + id
}

func (s *Store) dependencyCandidateAllOwners(tenant, id string) string {
	return s.base(tenant) + ":dependency-admitted-owner:" + id
}

// 倒转固定长度 UTC 时间文本，使所有 score=0 的成员按 BeginAt 倒序、AlertID 升序排列。
// 不把纳秒时间转成 Redis float score，避免同毫秒或浮点精度损失改变选主顺序。
func dependencyCandidateMember(at time.Time, id string) string {
	raw := []byte(at.UTC().Format("20060102150405.000000000"))
	for i, value := range raw {
		if value >= '0' && value <= '9' {
			raw[i] = '9' - (value - '0')
		}
	}
	return string(raw) + ":" + id
}

func (s *Store) dependencyCandidateArgs(u DependencyCandidateUpdate) ([]string, []any, error) {
	if err := domain.ValidateIdentityPart("tenant", u.TenantID, 64); err != nil {
		return nil, nil, err
	}
	if u.AlertID == "" || len(u.AlertID) > domain.EntityIDMaxBytes || u.Revision < 1 || (!u.Clear && u.BeginAt.IsZero()) {
		return nil, nil, fmt.Errorf("invalid dependency candidate update")
	}
	owner := s.dependencyCandidateOwner(u.TenantID, u.AlertID, u.ConfigDigest)
	keys := []string{owner, s.dependencyCandidateAllOwners(u.TenantID, u.AlertID), owner + ":times"}
	args := []any{u.Revision, u.Clear, dependencyCandidateMember(u.BeginAt, u.AlertID)}
	for _, m := range u.Memberships {
		if err := m.Policy.Validate(); err != nil {
			return nil, nil, err
		}
		keys = append(keys, s.dependencyCandidateKey(u.TenantID, DependencyIndexRef{PolicyVersion: m.Policy, ConfigDigest: u.ConfigDigest}))
		stamp := "!"
		if !m.ObservedAt.IsZero() {
			stamp = m.ObservedAt.UTC().Format("20060102150405.000000000")
		} else if m.Matched {
			stamp = "00000000000000.000000000"
		}
		args = append(args, m.Matched, stamp)
	}
	return keys, args, nil
}

// ApplyDependencyCandidates 原子更新一个 Alert 的主候选成员及反向索引。
func (s *Store) ApplyDependencyCandidates(ctx context.Context, u DependencyCandidateUpdate) error {
	return s.ApplyDependencyCandidateBatch(ctx, []DependencyCandidateUpdate{u})
}

// ApplyDependencyCandidateBatch 供后台按页合并 Redis 往返；每个 Alert 仍独立按 revision 排序。
func (s *Store) ApplyDependencyCandidateBatch(ctx context.Context, updates []DependencyCandidateUpdate) error {
	pipe := s.client.Pipeline()
	commands := make([]*redis.Cmd, 0, len(updates))
	script := redis.NewScript(applyDependencyCandidateScript)
	// 同一 pipeline 首项加载脚本，后续项复用 Redis 缓存，避免每个成员重复传脚本正文。
	for _, u := range updates {
		keys, args, err := s.dependencyCandidateArgs(u)
		if err != nil {
			return err
		}
		if len(commands) == 0 {
			commands = append(commands, script.Eval(ctx, pipe, keys, args...))
		} else {
			commands = append(commands, script.EvalSha(ctx, pipe, keys, args...))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	for _, command := range commands {
		code, err := command.Int64()
		if err != nil {
			return err
		}
		if code == 2 {
			return ErrDependencySnapshotStale
		}
	}
	return nil
}

const applyDependencyCandidateScript = `
local revision=tonumber(ARGV[1]);local clear=ARGV[2]=='1';local member=ARGV[3]
local state=redis.call('HMGET',KEYS[2],'_revision','_cleared')
local global=tonumber(state[1] or '0');local cleared=tonumber(state[2] or '0')
if clear then
 if global>revision then return 0 end
 local owners=redis.call('HKEYS',KEYS[2])
 for _,owner in ipairs(owners) do
  if string.sub(owner,1,1)~='_' then
   local old=redis.call('HGETALL',owner)
   for i=1,#old,2 do if string.sub(old[i],1,1)~='_' then redis.call('ZREM',old[i],old[i+1]) end end
   redis.call('DEL',owner,owner..':times');redis.call('HDEL',KEYS[2],owner)
  end
 end
 redis.call('HSET',KEYS[2],'_revision',ARGV[1],'_cleared',ARGV[1]);redis.call('EXPIRE',KEYS[2],2592000)
 return 1
end
if cleared>=revision then return 0 end
local previous=tonumber(redis.call('HGET',KEYS[1],'_revision') or '0')
if previous>revision then return 0 end
-- 在清任何旧成员之前检查依赖时间；CAS 较新不代表其 Worker 读取的目标事实也较新。
for i=4,#KEYS do
 local stamp=ARGV[2*i-3]
 if stamp~='!' then
  local oldstamp=redis.call('HGET',KEYS[3],KEYS[i])
  if oldstamp and oldstamp~='!' and stamp<oldstamp then return 2 end
 end
end
local advanced=revision>previous
if advanced then
 local old=redis.call('HGETALL',KEYS[1])
 for i=1,#old,2 do
  if string.sub(old[i],1,1)~='_' then redis.call('ZREM',old[i],old[i+1]) end
 end
 -- 当前成员和纯字段标记随 revision 整体替换；源时间 floor 位于独立的 times key，必须保留。
 if #old>2 then redis.call('DEL',KEYS[1]) end
end
redis.call('HSET',KEYS[1],'_revision',ARGV[1])
redis.call('HSET',KEYS[2],KEYS[1],'1','_revision',math.max(global,revision))
-- owner 从不设置 TTL；全局 owner 只在清理后重新获得资格时撤销清理 TTL。
if cleared>0 and global==cleared then redis.call('PERSIST',KEYS[2]) end
for i=4,#KEYS do
 local stamp=ARGV[2*i-3];local localkey='_local:'..KEYS[i]
 -- 纯字段 false 在本 Alert revision 内不受外部快照替换，已见的源时间仍留作后续版本的 floor。
 if advanced or stamp=='!' or redis.call('HEXISTS',KEYS[1],localkey)==0 then
  local old=false
  if not advanced then old=redis.call('HGET',KEYS[1],KEYS[i]) end
  if old then redis.call('ZREM',KEYS[i],old) end
  if stamp=='!' then redis.call('HSET',KEYS[1],localkey,'1') else redis.call('HSET',KEYS[3],KEYS[i],stamp) end
  if ARGV[2*i-4]=='1' then
   redis.call('ZADD',KEYS[i],0,member);redis.call('HSET',KEYS[1],KEYS[i],member)
  elseif old then redis.call('HDEL',KEYS[1],KEYS[i]) end
 end
end
return 1`

// RetireDependencyNamespace 停用旧等级快照；保留其成员供受控重建，不允许旧 Worker 重新开放。
func (s *Store) RetireDependencyNamespace(ctx context.Context, tenant, digest string) error {
	return s.client.Set(ctx, s.base(tenant)+":dependency-admitted-retired:"+digest, "1", 0).Err()
}

// ActivateDependencyNamespace 仅控制面在当前配置完整重建成功后调用，支持回退到曾使用过的摘要。
func (s *Store) ActivateDependencyNamespace(ctx context.Context, tenant, digest string) error {
	return s.client.Del(ctx, s.base(tenant)+":dependency-admitted-retired:"+digest).Err()
}

// MarkDependencyIndexReady 仅在完整后台读取成功后开放该冻结版本，不将半页发布为可用索引。
func (s *Store) MarkDependencyIndexReady(ctx context.Context, tenant string, p DependencyIndexRef) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if err := domain.ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return err
	}
	return s.client.Set(ctx, s.dependencyCandidateKey(tenant, p)+":ready", "1", 0).Err()
}

// DependencyCandidate 是共享有序索引的一条引用；Revision 用于安全清理过期候选。
type DependencyCandidate struct {
	AlertID    string
	Revision   int64
	Member     string
	ObservedAt time.Time
}

// TopDependencyCandidate 区分尚未回填与确实无主；Lua 只读取首项与其 Alert revision。
func (s *Store) TopDependencyCandidate(ctx context.Context, tenant string, p DependencyIndexRef) (DependencyCandidate, bool, error) {
	if err := domain.ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return DependencyCandidate{}, false, err
	}
	if err := p.Validate(); err != nil {
		return DependencyCandidate{}, false, err
	}
	key := s.dependencyCandidateKey(tenant, p)
	result, err := dependencyCandidateTopScript.Run(ctx, s.client, []string{key, key + ":ready", s.base(tenant) + ":dependency-admitted-retired:" + p.ConfigDigest}, s.base(tenant)+":dependency-admitted-owner-ns:"+p.ConfigDigest+":").Slice()
	if err != nil {
		return DependencyCandidate{}, false, err
	}
	if len(result) == 1 {
		return DependencyCandidate{}, result[0] == int64(1), nil
	}
	if len(result) != 4 {
		return DependencyCandidate{}, false, ErrState
	}
	member, ok := result[1].(string)
	if !ok || len(member) <= len("20060102150405.000000000:") {
		return DependencyCandidate{}, false, ErrState
	}
	revision, ok := result[2].(string)
	if !ok {
		return DependencyCandidate{}, false, ErrState
	}
	n, parseErr := strconv.ParseInt(revision, 10, 64)
	if parseErr != nil || n < 1 {
		return DependencyCandidate{}, false, ErrState
	}
	stamp, ok := result[3].(string)
	if !ok {
		return DependencyCandidate{}, false, ErrState
	}
	var observed time.Time
	if stamp != "00000000000000.000000000" {
		observed, err = time.Parse("20060102150405.000000000", stamp)
		if err != nil {
			return DependencyCandidate{}, false, ErrState
		}
	}
	return DependencyCandidate{AlertID: member[len("20060102150405.000000000:"):], Revision: n, Member: member, ObservedAt: observed}, true, nil
}

var dependencyCandidateTopScript = redis.NewScript(`
if redis.call('EXISTS',KEYS[3])==1 or redis.call('EXISTS',KEYS[2])==0 then return {0} end
local first=redis.call('ZRANGE',KEYS[1],0,0);if #first==0 then return {1} end
local id=string.sub(first[1],26)
local revision=redis.call('HGET',ARGV[1]..id,'_revision')
if not revision then return redis.error_reply('dependency candidate owner missing') end
local stamp=redis.call('HGET',ARGV[1]..id..':times',KEYS[1]) or '00000000000000.000000000'
return {1,first[1],revision,stamp}`)

// RemoveDependencyCandidate 不删除更新后的同一 Alert 成员，后台旧读取不能覆盖实时状态。
func (s *Store) RemoveDependencyCandidate(ctx context.Context, tenant string, p DependencyIndexRef, c DependencyCandidate) error {
	return removeDependencyCandidateScript.Run(ctx, s.client, []string{s.dependencyCandidateKey(tenant, p), s.dependencyCandidateOwner(tenant, c.AlertID, p.ConfigDigest)}, c.Revision, c.Member).Err()
}

var removeDependencyCandidateScript = redis.NewScript(`
if tonumber(redis.call('HGET',KEYS[2],'_revision') or '0')~=tonumber(ARGV[1]) then return 0 end
if redis.call('HGET',KEYS[2],KEYS[1])~=ARGV[2] then return 0 end
redis.call('ZREM',KEYS[1],ARGV[2]);redis.call('HDEL',KEYS[2],KEYS[1]);return 1`)
