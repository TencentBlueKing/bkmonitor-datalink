// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// QueryGroupOwner describes an active lease observed on Redis's clock. It
// identifies a logical Worker, not a process incarnation or execution receipt.
// The lease token is deliberately never read or returned.
type QueryGroupOwner struct {
	OwnerID    string    `json:"owner_id"`
	OwnerEpoch uint64    `json:"owner_epoch"`
	Deadline   time.Time `json:"deadline"`
	ObservedAt time.Time `json:"observed_at"`
}

// ReadActiveControlLeader is the control leader's active lease on Redis's clock.
// Unlike ReadControlLeader, it rejects a retained hash whose lease has expired.
// It does not expose the lease token or alter the older discovery API's meaning.
func (store *RedisStore) ReadActiveControlLeader(ctx context.Context) (QueryGroupOwner, bool, error) {
	return store.ReadQueryGroupOwner(ctx, ControlLeaderIdentity)
}

// ReadQueryGroupOwner reads one active lease atomically with Redis TIME. Desired
// placement is not ownership; absent, expired and paused leases return false.
//
// A lease held under a worker session (A2) has no deadline of its own: it is
// active while its holder's session is not proven gone, and its deadline is
// the session's. That is read in a second step, so the two readings are a
// moment apart; this is for routing, and a moment's difference only routes
// one request to a holder that just let go.
func (store *RedisStore) ReadQueryGroupOwner(ctx context.Context, identity execution.QueryGroupIdentity) (QueryGroupOwner, bool, error) {
	if store == nil || store.client == nil || identity == "" {
		return QueryGroupOwner{}, false, errors.New("alarmd ownership: owner read needs a store and a Query Group")
	}
	values, err := store.client.Eval(ctx, readQueryGroupOwnerScript, []string{store.ownershipKey(identity)}).Slice()
	if err != nil {
		return QueryGroupOwner{}, false, err
	}
	if len(values) == 0 {
		return QueryGroupOwner{}, false, nil
	}
	if len(values) == 5 {
		return store.sessionHeldOwner(ctx, values)
	}
	if len(values) != 4 {
		return QueryGroupOwner{}, false, errors.New("alarmd ownership: invalid owner read response")
	}
	owner, _ := values[0].(string)
	epochText, _ := values[1].(string)
	deadlineText, _ := values[2].(string)
	observed, observedOK := values[3].(int64)
	epoch, epochErr := strconv.ParseUint(epochText, 10, 64)
	deadline, deadlineErr := strconv.ParseInt(deadlineText, 10, 64)
	if owner == "" || epochErr != nil || epoch == 0 || deadlineErr != nil || !observedOK || deadline <= observed {
		return QueryGroupOwner{}, false, errors.New("alarmd ownership: invalid active owner facts")
	}
	return QueryGroupOwner{OwnerID: owner, OwnerEpoch: epoch, Deadline: time.UnixMilli(deadline).UTC(), ObservedAt: time.UnixMilli(observed).UTC()}, true, nil
}

const readQueryGroupOwnerScript = `
local clock = redis.call('TIME')
local now_ms = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
local facts = redis.call('HMGET', KEYS[1], 'owner_id', 'owner_epoch', 'deadline_ms', 'execution_disposition', 'owner_session_epoch')
if not facts[1] or facts[1] == '' or facts[4] ~= 'ACTIVE' then return {} end
if facts[5] and facts[5] ~= '' then return {facts[1], facts[2], facts[5], now_ms, 'session'} end
local deadline = tonumber(facts[3])
if not deadline or deadline <= now_ms then return {} end
return {facts[1], facts[2], facts[3], now_ms}
`

// sessionHeldOwner finishes the read of a lease held under a worker session:
// active while the holder's session is not proven gone, until its deadline.
func (store *RedisStore) sessionHeldOwner(ctx context.Context, values []interface{}) (QueryGroupOwner, bool, error) {
	owner, _ := values[0].(string)
	epochText, _ := values[1].(string)
	sessionText, _ := values[2].(string)
	epoch, epochErr := strconv.ParseUint(epochText, 10, 64)
	sessionEpoch, sessionErr := strconv.ParseUint(sessionText, 10, 64)
	if owner == "" || epochErr != nil || epoch == 0 || sessionErr != nil {
		return QueryGroupOwner{}, false, errors.New("alarmd ownership: invalid active owner facts")
	}
	facts, err := store.ReadWorkerSessions(ctx, []string{owner})
	if err != nil {
		return QueryGroupOwner{}, false, err
	}
	fact := facts[owner]
	if through, lapsed := fact.LapsedThrough(); (lapsed && sessionEpoch <= through) || !fact.Present || fact.DeadlineMillis <= fact.ServerNowMillis {
		return QueryGroupOwner{}, false, nil
	}
	return QueryGroupOwner{OwnerID: owner, OwnerEpoch: epoch, Deadline: time.UnixMilli(fact.DeadlineMillis).UTC(),
		ObservedAt: time.UnixMilli(fact.ServerNowMillis).UTC()}, true, nil
}
