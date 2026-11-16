// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.
package ownership

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisscript"
)

// A worker session is one lease per worker process (design 012 third batch,
// A2): the worker renews it, and the Control Leader reads it to tell whether
// a Query Group's holder is still alive. Its record is one hash per worker
// id -- worker_id, session_epoch, session_token, deadline_ms -- under a key
// of its own, so no Query Group script reads it: a fence that read the
// session would need the session and the Query Group in one atomic domain,
// which a Redis Cluster cannot give.
//
// Three facts make a session safe to read from outside any atomic script:
//
//   - A session is granted only when the worker id has no live one, so the
//     epoch it gets is issued after the previous session lapsed.
//   - A session that lapsed is never renewed again; the worker has to
//     acquire a new one, under a new epoch.
//   - The epoch only grows: a new one is max(previous + 1, the server's
//     clock in milliseconds), so a record that expired or a failover that
//     lost an increment cannot hand out an epoch a Query Group record
//     already names.
//
// So "every session of this worker up to epoch E has lapsed", once read, stays
// true.

// workerSessionRetention is how long a session record outlives its last
// renewal. The deadline says whether a session is alive; the record is kept
// so a worker id's epoch is still there to grow from, as owner_epoch is kept
// for a Query Group.
const workerSessionRetention = 7 * 24 * time.Hour

const workerSessionIdentityPrefix = "alarmd-worker-session:"

// WorkerSessionIdentity is the identity a worker's session record is keyed
// and traced by.
func WorkerSessionIdentity(workerID string) execution.QueryGroupIdentity {
	return execution.QueryGroupIdentity(workerSessionIdentityPrefix + workerID)
}

// IsWorkerSessionIdentity reports whether identity is a worker session's.
func IsWorkerSessionIdentity(identity execution.QueryGroupIdentity) bool {
	return strings.HasPrefix(string(identity), workerSessionIdentityPrefix)
}

// WorkerSession is the session this process holds. Deadline is on the
// caller's clock (onCallerClock), anchored before the round trip.
type WorkerSession struct {
	WorkerID string
	Epoch    uint64
	Token    string
	Deadline time.Time
}

// WorkerSessionFact is a session record as the Control Leader reads it, on
// the server's clock.
type WorkerSessionFact struct {
	WorkerID        string
	Present         bool
	Epoch           uint64
	DeadlineMillis  int64
	ServerNowMillis int64
}

// LapsedThrough is the largest epoch of this worker whose session is known
// to have lapsed, and whether there is one. A lapsed record proves every
// session up to its epoch; a live one proves every earlier session, because
// its own was granted only after they lapsed. A record not read proves
// nothing: its holder is taken as alive.
func (fact WorkerSessionFact) LapsedThrough() (uint64, bool) {
	if !fact.Present || fact.Epoch == 0 {
		return 0, false
	}
	if fact.DeadlineMillis <= fact.ServerNowMillis {
		return fact.Epoch, true
	}
	if fact.Epoch > 1 {
		return fact.Epoch - 1, true
	}
	return 0, false
}

var workerSessionAcquireScript = redisscript.New("ownership_worker_session_acquire", FenceLua+`
local now_ms = redis_now_ms()
local current = redis.call('HMGET', KEYS[1], 'session_epoch', 'deadline_ms')
local deadline = tonumber(current[2] or '0')
if deadline > now_ms then return {'BUSY', 0, deadline, now_ms} end
local epoch = math.max(tonumber(current[1] or '0') + 1, now_ms)
local deadline_ms = now_ms + tonumber(ARGV[3])
redis.call('HSET', KEYS[1], 'worker_id', ARGV[1], 'session_epoch', string.format('%d', epoch),
  'session_token', ARGV[2], 'deadline_ms', string.format('%d', deadline_ms))
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return {'ACQUIRED', epoch, deadline_ms, now_ms}
`)

var workerSessionRenewScript = redisscript.New("ownership_worker_session_renew", FenceLua+`
local now_ms = redis_now_ms()
local current = redis.call('HMGET', KEYS[1], 'session_epoch', 'session_token', 'deadline_ms')
if current[1] ~= ARGV[1] or current[2] ~= ARGV[2] or tonumber(current[3] or '0') <= now_ms then
  return {'STALE', 0, now_ms}
end
local deadline_ms = now_ms + tonumber(ARGV[3])
redis.call('HSET', KEYS[1], 'deadline_ms', string.format('%d', deadline_ms))
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return {'RENEWED', deadline_ms, now_ms}
`)

var workerSessionReleaseScript = redisscript.New("ownership_worker_session_release", `
local current = redis.call('HMGET', KEYS[1], 'session_epoch', 'session_token')
if current[1] ~= ARGV[1] or current[2] ~= ARGV[2] then return 'STALE' end
redis.call('HSET', KEYS[1], 'deadline_ms', '0')
return 'RELEASED'
`)

var workerSessionReadScript = redisscript.New("ownership_worker_session_read", FenceLua+`
local now_ms = redis_now_ms()
local current = redis.call('HMGET', KEYS[1], 'session_epoch', 'deadline_ms')
return {current[1] or '', current[2] or '', now_ms}
`)

func (store *RedisStore) workerSessionKey(workerID string) string {
	return store.controlKey(WorkerSessionIdentity(workerID), "session")
}

// AcquireWorkerSession grants this process the worker id's session, or
// ErrLeaseBusy while another session of the id is alive.
func (store *RedisStore) AcquireWorkerSession(ctx context.Context, workerID string, at time.Time, ttl time.Duration) (WorkerSession, error) {
	if store == nil || workerID == "" || at.IsZero() || ttl <= 0 {
		return WorkerSession{}, errors.New("alarmd ownership: invalid worker session acquisition")
	}
	token, err := leaseToken()
	if err != nil {
		return WorkerSession{}, err
	}
	result, err := workerSessionAcquireScript.Run(ctx, store.client, []string{store.workerSessionKey(workerID)},
		workerID, token, ttl.Milliseconds(), workerSessionRetention.Milliseconds()).Result()
	if err != nil {
		return WorkerSession{}, err
	}
	values, err := scriptValues(result, 4)
	if err != nil {
		return WorkerSession{}, err
	}
	switch scriptText(values[0]) {
	case "BUSY":
		return WorkerSession{}, ErrLeaseBusy
	case "ACQUIRED":
		return WorkerSession{WorkerID: workerID, Epoch: uint64(scriptInt(values[1])), Token: token,
			Deadline: onCallerClock(at, scriptInt(values[3]), scriptInt(values[2]))}, nil
	}
	return WorkerSession{}, errors.New("alarmd ownership: invalid worker session acquisition response")
}

// RenewWorkerSession extends a live session, or ErrStaleFence for one that
// lapsed or is no longer this process's: a lapsed session is never renewed.
func (store *RedisStore) RenewWorkerSession(ctx context.Context, session WorkerSession, at time.Time, ttl time.Duration) (WorkerSession, error) {
	if store == nil || session.WorkerID == "" || session.Epoch == 0 || session.Token == "" || at.IsZero() || ttl <= 0 {
		return WorkerSession{}, ErrStaleFence
	}
	result, err := workerSessionRenewScript.Run(ctx, store.client, []string{store.workerSessionKey(session.WorkerID)},
		strconv.FormatUint(session.Epoch, 10), session.Token, ttl.Milliseconds(), workerSessionRetention.Milliseconds()).Result()
	if err != nil {
		return WorkerSession{}, err
	}
	values, err := scriptValues(result, 3)
	if err != nil {
		return WorkerSession{}, err
	}
	switch scriptText(values[0]) {
	case "STALE":
		return WorkerSession{}, ErrStaleFence
	case "RENEWED":
		session.Deadline = onCallerClock(at, scriptInt(values[2]), scriptInt(values[1]))
		return session, nil
	}
	return WorkerSession{}, errors.New("alarmd ownership: invalid worker session renewal response")
}

// ReleaseWorkerSession ends this process's session at once, so the Control
// Leader reads its holder as gone without waiting out the TTL. The epoch is
// kept.
func (store *RedisStore) ReleaseWorkerSession(ctx context.Context, session WorkerSession) error {
	if store == nil || session.WorkerID == "" || session.Epoch == 0 || session.Token == "" {
		return ErrStaleFence
	}
	result, err := workerSessionReleaseScript.Run(ctx, store.client, []string{store.workerSessionKey(session.WorkerID)},
		strconv.FormatUint(session.Epoch, 10), session.Token).Text()
	if err != nil {
		return err
	}
	if result != "RELEASED" {
		return ErrStaleFence
	}
	return nil
}

// ReadWorkerSessions reads the session records of workerIDs, each judged on
// its own server's clock. The Control Leader reads only the holders of the
// Query Groups it is about to move, so a round reads a few records, one
// script run each.
func (store *RedisStore) ReadWorkerSessions(ctx context.Context, workerIDs []string) (map[string]WorkerSessionFact, error) {
	facts := make(map[string]WorkerSessionFact, len(workerIDs))
	if store == nil {
		return facts, nil
	}
	for _, workerID := range workerIDs {
		result, err := workerSessionReadScript.Run(ctx, store.client, []string{store.workerSessionKey(workerID)}).Result()
		if err != nil {
			return nil, err
		}
		values, err := scriptValues(result, 3)
		if err != nil {
			return nil, err
		}
		fact := WorkerSessionFact{WorkerID: workerID, ServerNowMillis: scriptInt(values[2])}
		if epoch := scriptText(values[0]); epoch != "" {
			parsed, parseErr := strconv.ParseUint(epoch, 10, 64)
			if parseErr != nil {
				return nil, errors.New("alarmd ownership: invalid worker session epoch")
			}
			fact.Present, fact.Epoch = true, parsed
			fact.DeadlineMillis, _ = strconv.ParseInt(scriptText(values[1]), 10, 64)
		}
		facts[workerID] = fact
	}
	return facts, nil
}
