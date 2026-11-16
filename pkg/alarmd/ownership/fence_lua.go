// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ContentSwitchMargin is how long after the current lease deadline a pending
// content scope takes effect. It covers the clock spread between the leader
// that wrote it, the worker that reads it and the Redis that compares it -
// the one bound alarmd assumes between its clocks, execution.MaxClockSkew -
// within which the last batch a worker admitted before it read the change
// also lands. It is a system constant, not a setting.
const ContentSwitchMargin = execution.MaxClockSkew

// FenceLua is the one Lua definition of the owner fence, prepended to every
// script that decides whether a writer may act. Six scripts used to carry
// their own copy of the same five comparisons; a rule copied six times is a
// rule that will be changed in five places, and the one left behind is the
// one that then admits a stale owner. decision-016 adds a sixth comparison
// -- the content scope -- and it is added here once.
//
// It defines three functions and nothing else:
//
//	redis_now_ms()
//	    The instant the script is running, in milliseconds, read from the
//	    server's own clock (TIME). Every deadline a fenced script mints and
//	    every expiry it judges uses this instant and no other: a lease is
//	    deadline_ms = redis_now_ms() + ttl when it is granted or renewed, and
//	    it has lapsed when deadline_ms <= redis_now_ms() at the write. The
//	    callers' clocks -- a leader's, a worker's -- never reach a comparison,
//	    so a skewed or stepped clock on either cannot lengthen a lease, and
//	    two writers racing on one record are judged by the one clock they
//	    share. The caller is told the server instant in every reply that
//	    carries a deadline, and keeps only the remaining duration, anchored
//	    to its own clock from before the round trip.
//
//	current_content_scope(assignment_key, now_ms)
//	    The content scope the assignment record names now. A pending scope
//	    whose effective time has passed is promoted on the way out -- copied
//	    into content_scope and cleared -- so every script that reads the scope
//	    also settles a change that fell due. The scope is an empty string
//	    until a leader has ever written one.
//
//	fence_refusal(assignment_key, ownership_key, require_assignment,
//	              owner_id, epoch, token, content_scope, now_ms, record_fields)
//	    nil when the fence holds, otherwise why it does not, decided in this
//	    order: NOT_DESIRED when the assignment names another worker and is
//	    not draining the caller (below); STALE for a lease that is paused,
//	    belongs to someone else, carries another epoch or token, or has
//	    passed its deadline (a lease its holder renews itself; one held under
//	    a worker session, A2, has none: its holder stops acting when its
//	    session or its last confirmation runs out, and the Control Leader
//	    hands the Query Group over only once the session is proven gone);
//	    CONTENT_MOVED, last, when the lease holds but
//	    the caller declared a content scope that is neither the scope the
//	    record names now nor the one it has pending.
//
//	    It reads each record once, with one HMGET each: the assignment's
//	    desired worker, grace pair and scopes, and the lease's disposition,
//	    owner, epoch, token and deadline. A script that also returns
//	    assignment fields names them in record_fields, and they come back in
//	    the same read, as the second result: a table from field name to the
//	    value (false when absent), with a promotion this run made already
//	    applied. The comparisons run on what was read, in the order above, so
//	    reading everything first changes no answer, only how many commands
//	    the server runs: TIME and two HMGETs where it used to be TIME and six
//	    HGETs, with the caller's own read of the record on top.
//
//	    A record moved to another worker while the caller held a live lease
//	    names the caller in releasing_worker_id until effective_at_ms (the
//	    lease deadline at the move plus ContentSwitchMargin). Until then the
//	    caller is not refused for the move: it is the outgoing holder,
//	    finishing the commit boundary it is in, and the old lease is the only
//	    valid owner until it lets go (design 02 §6.2). The grace skips the
//	    desired-worker comparison and nothing else. The owner, epoch, token
//	    and deadline comparisons still run on this clock, so a holder whose
//	    lease lapsed is STALE inside the grace, and once the new owner is
//	    granted the lease the old triple no longer matches. There is never an
//	    instant with two writers. Renewal caps the holder at the grace's end,
//	    and the grant to the new owner clears the pair.
//
//	    The pending scope is admitted on purpose. A pending change protects
//	    the holder of the old content until the moment it was promised, and
//	    nothing more: the new content is the leader's decision from the
//	    moment it was written, so a writer that already executes it -- a
//	    worker whose Segment cut over before the change fell due -- is ahead
//	    of the record, not behind it. Refusing it would stall every Query
//	    Group for one lease lifetime on every publication. A pending scope
//	    counts only with its effective time, the same pair the promotion and
//	    ContentChangePending read: a writer that one day wrote the scope
//	    without the time would otherwise have written a scope the fence
//	    admits and nothing ever promotes.
//
//	    The order is the meaning. CONTENT_MOVED is read by every caller as
//	    "the lease is good, the view is behind, re-read" -- it is kept out of
//	    IsLeaseDecision for exactly that -- so it may only be said once the
//	    lease has actually been found good. A worker whose lease lapsed
//	    while the leader, seeing no live holder, wrote a new scope directly
//	    would otherwise come back with CONTENT_MOVED in hand, keep the Query
//	    Group and re-read, and the lost lease would never be reported.
//
// The content scope comparison is optional on both sides on purpose, and
// both empty shapes are live, not only left over from builds before the
// field: a caller that passes an empty scope -- the Control Leader's own
// fence check and any fenced write that executes no content -- gets the five
// comparisons it always had; a record that names no scope -- one the Control
// Leader wrote with no scope to state, or withdrew the scope from --
// authorizes whatever the caller declares. The comparison binds only once
// the Control Leader has written a scope and a writer declares one.
//
// TIME is a non-deterministic command. Redis before 5 refused a write after
// one unless the script had opted into effects replication, and Redis from 5
// on replicates effects by default; redis.replicate_commands() is that opt-in
// on the old servers and a no-op that returns true on the new ones, so it is
// called once at the top and the same text runs on both. ProbeFenceClock
// verifies at startup that the server this store was given accepts it.
const FenceLua = `
redis.replicate_commands()
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
local function fence_refusal(assignment_key, ownership_key, require_assignment, owner_id, epoch, token, content_scope, now_ms, record_fields)
  local facts = {}
  if require_assignment == '1' then
    local names = {'desired_worker_id', 'releasing_worker_id', 'effective_at_ms', 'content_scope', 'pending_content_scope'}
    local named = {}
    for _, name in ipairs(names) do named[name] = true end
    for _, name in ipairs(record_fields or {}) do
      if not named[name] then
        named[name] = true
        names[#names + 1] = name
      end
    end
    local values = redis.call('HMGET', assignment_key, unpack(names))
    for index, name in ipairs(names) do facts[name] = values[index] end
    local desired = facts['desired_worker_id']
    if not desired then return 'NOT_DESIRED', facts end
    local draining = facts['releasing_worker_id'] == owner_id and tonumber(facts['effective_at_ms'] or '0') > now_ms
    if desired ~= owner_id and not draining then return 'NOT_DESIRED', facts end
  end
  local held = redis.call('HMGET', ownership_key, 'execution_disposition', 'owner_id', 'owner_epoch', 'lease_token', 'deadline_ms',
    'owner_session_epoch')
  if held[1] ~= 'ACTIVE' then return 'STALE', facts end
  local self_renewed = not held[6] or held[6] == ''
  if held[2] ~= owner_id or held[3] ~= epoch or held[4] ~= token or
     (self_renewed and tonumber(held[5] or '0') <= now_ms) then return 'STALE', facts end
  if require_assignment == '1' and content_scope and content_scope ~= '' then
    local scope = facts['content_scope'] or ''
    local pending = facts['pending_content_scope']
    local effective = tonumber(facts['effective_at_ms'] or '0')
    if pending and pending ~= '' and effective > 0 and now_ms >= effective then
      redis.call('HSET', assignment_key, 'content_scope', pending)
      redis.call('HDEL', assignment_key, 'pending_content_scope', 'effective_at_ms')
      scope = pending
      facts['content_scope'], facts['pending_content_scope'], facts['effective_at_ms'] = pending, false, false
      pending, effective = false, 0
    end
    if scope ~= '' and scope ~= content_scope then
      if not pending or pending ~= content_scope or effective <= 0 then return 'CONTENT_MOVED', facts end
    end
  end
  return nil, facts
end
`

// fenceClockProbeLua does the two things every fenced script does, in the
// order that matters: read TIME, then issue a write command. PEXPIRE on a
// key that does not exist changes nothing and is still a write to the
// script engine, so a server that refuses the sequence refuses this without
// a key being touched. It runs once, at readiness, so it is sent as text.
const fenceClockProbeLua = FenceLua + `
redis.call('PEXPIRE', KEYS[1], 1)
return redis_now_ms()
`

// FenceClockProber is the one command the probe needs; both Redis-backed
// stores' clients have it.
type FenceClockProber interface {
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd
}

// ProbeFenceClock runs one fence-shaped script on the client and returns
// the server's clock reading, or a named error when the server will not run
// the fence at all. It belongs to readiness: a store whose fence cannot run
// on the Redis it was given is not a store, and the first lease is the wrong
// place to learn that. The reading is a fact about the server, not a number
// anything should be corrected by; the fence needs no offset because no
// caller clock takes part in it.
func ProbeFenceClock(ctx context.Context, client FenceClockProber, prefix string) (time.Time, error) {
	if client == nil || prefix == "" {
		return time.Time{}, fmt.Errorf("alarmd ownership: Redis client and prefix are required")
	}
	millis, err := client.Eval(ctx, fenceClockProbeLua, []string{prefix + ":fence-clock-probe"}).Int64()
	if err != nil {
		return time.Time{}, fmt.Errorf("alarmd ownership: the owner fence cannot run on this Redis, "+
			"it reads TIME and then writes (Redis 5 or later, or 3.2 or later with script effects replication): %w", err)
	}
	return time.UnixMilli(millis), nil
}
