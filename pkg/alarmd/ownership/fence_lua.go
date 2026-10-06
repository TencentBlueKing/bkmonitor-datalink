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
)

// ContentSwitchMargin is how long after the current lease deadline a pending
// content scope takes effect. It covers the last batch a worker may have
// admitted before it read the pending change, and the clock spread between
// the leader that wrote it, the worker that reads it and the Redis that
// compares it. It is a system constant, not a setting: nothing an operator
// knows would make a better number than the batch bound the worker enforces.
const ContentSwitchMargin = 5 * time.Second

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
//	              owner_id, epoch, token, content_scope, now_ms)
//	    nil when the fence holds, otherwise why it does not, decided in this
//	    order: NOT_DESIRED when the assignment names another worker; STALE
//	    for a lease that is paused, belongs to someone else, carries another
//	    epoch or token, or has passed its deadline; CONTENT_MOVED, last, when
//	    the lease holds but the caller declared a content scope that is
//	    neither the scope the record names now nor the one it has pending.
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
// The content scope comparison is optional on both sides on purpose. A
// caller that passes an empty scope -- every binary built before this field
// existed -- gets the five comparisons it always had; a record that names no
// scope -- every record written by a leader from before it -- authorizes
// whatever the caller declares, as it always did. The comparison binds only
// once a leader has written a scope and a writer declares one, so the two
// argument shapes and the two record shapes coexist through a rolling
// restart in either order.
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
