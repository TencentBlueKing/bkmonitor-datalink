// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

// The owner fence and the five scripts built on it as they were before a
// move gave the outgoing holder a grace (releasing_worker_id with
// effective_at_ms). A rollout runs both generations against the same
// records, so the handover tests run these texts beside the current ones to
// read what each side does with what the other wrote. Frozen copies: they
// are the old binary, and nothing should edit them.

const legacyFenceLua = `redis.replicate_commands()
local function redis_now_ms()
  local now = redis.call('TIME')
  return tonumber(now[1]) * 1000 + math.floor(tonumber(now[2]) / 1000)
end
local function current_content_scope(assignment_key, now_ms)
  local fields = redis.call('HMGET', assignment_key, 'content_scope', 'pending_content_scope', 'effective_at_ms')
  local scope = fields[1] or ''
  local pending = fields[2]
  local effective = tonumber(fields[3] or '0')
  if pending and pending ~= '' and effective > 0 and now_ms >= effective then
    redis.call('HSET', assignment_key, 'content_scope', pending)
    redis.call('HDEL', assignment_key, 'pending_content_scope', 'effective_at_ms')
    scope = pending
  end
  return scope
end
local function fence_refusal(assignment_key, ownership_key, require_assignment, owner_id, epoch, token, content_scope, now_ms)
  if require_assignment == '1' then
    local desired = redis.call('HGET', assignment_key, 'desired_worker_id')
    if not desired or desired ~= owner_id then return 'NOT_DESIRED' end
  end
  if redis.call('HGET', ownership_key, 'execution_disposition') ~= 'ACTIVE' then return 'STALE' end
  if redis.call('HGET', ownership_key, 'owner_id') ~= owner_id or
     redis.call('HGET', ownership_key, 'owner_epoch') ~= epoch or
     redis.call('HGET', ownership_key, 'lease_token') ~= token or
     tonumber(redis.call('HGET', ownership_key, 'deadline_ms') or '0') <= now_ms then return 'STALE' end
  if require_assignment == '1' and content_scope and content_scope ~= '' then
    local named = current_content_scope(assignment_key, now_ms)
    if named ~= '' and named ~= content_scope then
      local change = redis.call('HMGET', assignment_key, 'pending_content_scope', 'effective_at_ms')
      local pending = change[1]
      local effective = tonumber(change[2] or '0')
      if not pending or pending ~= content_scope or effective <= 0 then return 'CONTENT_MOVED' end
    end
  end
  return nil
end
`

const legacyAcquireLua = `local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local ttl_ms = tonumber(ARGV[3])
local token = ARGV[4]
local now_ms = redis_now_ms()
local deadline_ms = now_ms + ttl_ms
local scope, timeline = '', 0
if require_assignment == '1' then
  local desired = redis.call('HGET', KEYS[1], 'desired_worker_id')
  if not desired or desired ~= owner_id then return {'NOT_DESIRED', 0, 0, '', now_ms, 0} end
  scope = current_content_scope(KEYS[1], now_ms)
  timeline = tonumber(redis.call('HGET', KEYS[1], 'timeline_record_revision') or '0')
end
local disposition = redis.call('HGET', KEYS[2], 'execution_disposition')
if disposition and disposition ~= 'ACTIVE' then return {'PAUSED', 0, 0, '', now_ms, 0} end
local current_owner = redis.call('HGET', KEYS[2], 'owner_id')
local current_deadline = tonumber(redis.call('HGET', KEYS[2], 'deadline_ms') or '0')
if current_owner and current_owner ~= '' and current_deadline > now_ms then return {'BUSY', 0, current_deadline, '', now_ms, 0} end
local epoch = tonumber(redis.call('HGET', KEYS[2], 'owner_epoch') or '0') + 1
redis.call('HSET', KEYS[2], 'owner_id', owner_id, 'owner_epoch', epoch, 'lease_token', token,
  'deadline_ms', deadline_ms, 'execution_disposition', 'ACTIVE')
return {'OWNED', epoch, deadline_ms, scope, now_ms, timeline}
`

const legacyRenewLua = `local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local ttl_ms = tonumber(ARGV[5])
local now_ms = redis_now_ms()
local deadline_ms = now_ms + ttl_ms
local refusal = fence_refusal(KEYS[1], KEYS[2], require_assignment, owner_id, epoch, token, '', now_ms)
if refusal then return {refusal, 0, '', '', 0, now_ms, 0} end
local scope, pending, effective, timeline = '', '', 0, 0
if require_assignment == '1' then
  scope = current_content_scope(KEYS[1], now_ms)
  local change = redis.call('HMGET', KEYS[1], 'pending_content_scope', 'effective_at_ms', 'timeline_record_revision')
  if change[1] and change[1] ~= '' then
    pending = change[1]
    effective = tonumber(change[2] or '0')
    if effective > 0 and deadline_ms > effective then deadline_ms = effective end
  end
  timeline = tonumber(change[3] or '0')
end
redis.call('HSET', KEYS[2], 'deadline_ms', deadline_ms)
return {'RENEWED', deadline_ms, scope, pending, effective, now_ms, timeline}
`

const legacyCheckFenceLua = `local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local content_scope = ARGV[5] or ''
local now_ms = redis_now_ms()
local empty = {'', '', '', '', '', '', '', '', '', ''}
local refusal = fence_refusal(KEYS[1], KEYS[2], require_assignment, owner_id, epoch, token, content_scope, now_ms)
if refusal then return {refusal, empty[1], empty[2], empty[3], empty[4], empty[5], empty[6], empty[7], empty[8], empty[9], empty[10]} end
local record = {'', '', '', '', '', '', '', '', '', ''}
if require_assignment == '1' then
  local fields = redis.call('HMGET', KEYS[1], 'desired_worker_id', 'assignment_generation',
    'record_revision', 'control_epoch', 'placement_reason', 'assigned_at_ms',
    'content_scope', 'pending_content_scope', 'effective_at_ms', 'timeline_record_revision')
  for index = 1, 10 do
    if fields[index] then record[index] = fields[index] end
  end
end
return {'VALID', record[1], record[2], record[3], record[4], record[5], record[6], record[7], record[8], record[9], record[10]}
`

const legacyPublishLua = `local leader_id = ARGV[1]
local leader_epoch = ARGV[2]
local leader_token = ARGV[3]
local now_ms = redis_now_ms()
if fence_refusal('', KEYS[1], '0', leader_id, leader_epoch, leader_token, '', now_ms) then
  return {'STALE', 0, 0, 0, '', 0, '', '', '', 0, 0}
end
local expected_revision = tonumber(ARGV[4])
local current_revision = tonumber(redis.call('HGET', KEYS[2], 'record_revision') or '0')
if current_revision ~= expected_revision then
  return {'CONFLICT', 0, current_revision, 0, '', 0, '', '', '', 0, 0}
end
local query_group = ARGV[5]
local desired = ARGV[6]
local reason = ARGV[7]
local assigned_at = ARGV[8]
local wanted_scope = ARGV[9] or ''
local margin_ms = tonumber(ARGV[10] or '0')
local withdraw = ARGV[11] == '1'
local timeline = tonumber(ARGV[12] or '0')
local function reply()
  local f = redis.call('HMGET', KEYS[2], 'desired_worker_id', 'assignment_generation', 'record_revision',
    'control_epoch', 'placement_reason', 'assigned_at_ms', 'content_scope', 'pending_content_scope', 'effective_at_ms',
    'timeline_record_revision')
  return {f[1], f[2], f[3], f[4], f[5], f[6], query_group, f[7] or '', f[8] or '', tonumber(f[9] or '0'), tonumber(f[10] or '0')}
end
-- The timeline revision is the Query Group's, not the decision's: written
-- when a decision names one newer than the record holds and left alone
-- otherwise, and it does not move record_revision - the cutover that writes
-- it beside the timeline itself does not either, and a reader compares it
-- to the view, not to the record's revision. Newer only, because the
-- decision's number is a copy read from the catalog a moment ago while the
-- cutover writes the number it just wrote to the timeline: a copy that lost
-- that race would put the record behind the timeline, and nothing rewrites
-- a timeline whose content did not change.
if timeline > 0 then
  local have = tonumber(redis.call('HGET', KEYS[2], 'timeline_record_revision') or '0')
  if timeline > have then redis.call('HSET', KEYS[2], 'timeline_record_revision', timeline) end
end
local current_desired = redis.call('HGET', KEYS[2], 'desired_worker_id')
if current_desired and current_desired == desired then
  if withdraw then
    local named = redis.call('HMGET', KEYS[2], 'content_scope', 'pending_content_scope')
    if (named[1] and named[1] ~= '') or (named[2] and named[2] ~= '') then
      redis.call('HDEL', KEYS[2], 'content_scope', 'pending_content_scope', 'effective_at_ms')
      redis.call('HSET', KEYS[2], 'record_revision', current_revision + 1)
    end
    return reply()
  end
  if wanted_scope == '' then return reply() end
  local scope = current_content_scope(KEYS[2], now_ms)
  if scope == wanted_scope then
    if redis.call('HGET', KEYS[2], 'pending_content_scope') then
      redis.call('HDEL', KEYS[2], 'pending_content_scope', 'effective_at_ms')
      redis.call('HSET', KEYS[2], 'record_revision', current_revision + 1)
    end
    return reply()
  end
  local lease_deadline = tonumber(redis.call('HGET', KEYS[3], 'deadline_ms') or '0')
  local holder = redis.call('HGET', KEYS[3], 'owner_id')
  if scope ~= '' and holder and holder ~= '' and lease_deadline > now_ms then
    local pending = redis.call('HGET', KEYS[2], 'pending_content_scope')
    if pending == wanted_scope then return reply() end
    local effective = lease_deadline + margin_ms
    local existing = tonumber(redis.call('HGET', KEYS[2], 'effective_at_ms') or '0')
    if existing > effective then effective = existing end
    redis.call('HSET', KEYS[2], 'pending_content_scope', wanted_scope, 'effective_at_ms', effective,
      'record_revision', current_revision + 1)
    return reply()
  end
  redis.call('HSET', KEYS[2], 'content_scope', wanted_scope, 'record_revision', current_revision + 1)
  redis.call('HDEL', KEYS[2], 'pending_content_scope', 'effective_at_ms')
  return reply()
end
local generation = tonumber(redis.call('HGET', KEYS[2], 'assignment_generation') or '0') + 1
local revision = current_revision + 1
redis.call('HSET', KEYS[2], 'query_group', query_group, 'desired_worker_id', desired,
  'assignment_generation', generation, 'record_revision', revision, 'control_epoch', leader_epoch,
  'placement_reason', reason, 'assigned_at_ms', assigned_at)
if wanted_scope ~= '' then redis.call('HSET', KEYS[2], 'content_scope', wanted_scope) end
if withdraw then redis.call('HDEL', KEYS[2], 'content_scope') end
redis.call('HDEL', KEYS[2], 'pending_content_scope', 'effective_at_ms')
return reply()
`

const legacyFencedCASLua = `local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local content_scope = ARGV[9] or ''
local refusal = fence_refusal(KEYS[1], KEYS[2], require_assignment, owner_id, epoch, token, content_scope, redis_now_ms())
if refusal == 'CONTENT_MOVED' then return 'CONTENT_MOVED' end
if refusal then return 'STALE_OWNER' end
local current = redis.call('GET', KEYS[3])
if ARGV[5] == '1' then
  if current then return 'CONFLICT' end
elseif not current or current ~= ARGV[6] then
  return 'CONFLICT'
end
local ttl_ms = tonumber(ARGV[8])
if ttl_ms > 0 then redis.call('SET', KEYS[3], ARGV[7], 'PX', ttl_ms)
else redis.call('SET', KEYS[3], ARGV[7]) end
return 'APPLIED'
`
