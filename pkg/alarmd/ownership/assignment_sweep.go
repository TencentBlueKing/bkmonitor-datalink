// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// AssignmentSweep is what one sweep of the Assignment records found and did.
type AssignmentSweep struct {
	// Scanned is how many Assignment records the key space held.
	Scanned int
	// Retired is how many of them named a Query Group the leader no longer
	// runs; Reclaimed how many of those were deleted, HeldByLease how many
	// were left because a worker still holds a live lease on the Query
	// Group, and Changed how many were left because the record moved under
	// the sweep.
	Retired, Reclaimed, HeldByLease, Changed int
	Duration                                 time.Duration
}

// reclaimAssignmentScript deletes one retired Query Group's Assignment
// record and ownership hash under the leader fence, and only while nobody
// holds a live lease on it: a worker that still runs the Query Group off an
// assigned set it read before the retirement keeps its record until its
// lease lapses, and the next sweep takes it. The record is re-read inside
// the script and left alone if it no longer names the Query Group the sweep
// read it under. Expiry is judged on Redis's clock, like every fence.
//
// KEYS[1] leader ownership hash, KEYS[2] the Assignment record, KEYS[3] the
// Query Group's ownership hash. ARGV[1..3] the leader fence, ARGV[4] the
// Query Group the record was read under.
var reclaimAssignmentScript = redis.NewScript(FenceLua + `
if fence_refusal('', KEYS[1], '0', ARGV[1], ARGV[2], ARGV[3], '', redis_now_ms()) then return 'STALE' end
local named = redis.call('HGET', KEYS[2], 'query_group')
if not named then return 'GONE' end
if named ~= ARGV[4] then return 'CHANGED' end
local holder = redis.call('HGET', KEYS[3], 'owner_id')
local deadline = tonumber(redis.call('HGET', KEYS[3], 'deadline_ms') or '0')
if holder and holder ~= '' and deadline > redis_now_ms() then return 'HELD' end
redis.call('DEL', KEYS[2], KEYS[3])
return 'RECLAIMED'
`)

// assignmentSweepScan bounds one SCAN page and one pipelined read.
const assignmentSweepScan = 500

// SweepAssignments walks every Assignment record in the store and reclaims
// the ones that name a Query Group outside keep -- the set the leader runs
// this round, active and draining -- whose lease has lapsed. Records and
// ownership hashes carry no expiry, so a Query Group that leaves the active
// set leaves both behind for good: six such records, all naming workers of
// long-retired replicas, were found on a deployment, and a coverage reading
// over the records never reaches its whole because of them.
//
// The sweep is the leader's, under its fence, and it decides nothing about
// live work: a record whose Query Group is still in keep is not touched
// however stale its desired worker, that is the reconcile round's to
// re-place; a retired record with a live lease is left for the lease to
// lapse. A leader that loses its fence mid-sweep stops with ErrStaleFence.
// The walk is a SCAN over the store's own prefix, which is fine on the
// standalone store this runs against and is not written for a cluster.
func (store *RedisStore) SweepAssignments(
	ctx context.Context,
	authority PublicationAuthority,
	keep map[execution.QueryGroupIdentity]struct{},
) (AssignmentSweep, error) {
	sweep := AssignmentSweep{}
	if store == nil || store.client == nil {
		return sweep, errors.New("alarmd ownership: initialized store is required")
	}
	if authority.Fence.QueryGroup != ControlLeaderIdentity || validateFence(authority.Fence) != nil {
		return sweep, errors.New("alarmd ownership: assignment sweep needs the control leader authority")
	}
	started := time.Now()
	defer func() { sweep.Duration = time.Since(started) }()
	pattern := store.prefix + ":{*}:assignment"
	leaderKey := store.ownershipKey(ControlLeaderIdentity)
	var cursor uint64
	for {
		keys, next, err := store.client.Scan(ctx, cursor, pattern, assignmentSweepScan).Result()
		if err != nil {
			return sweep, fmt.Errorf("alarmd ownership: assignment sweep scan: %w", err)
		}
		if len(keys) > 0 {
			replies := make([]*redis.StringCmd, len(keys))
			if _, err := store.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
				for index, key := range keys {
					replies[index] = pipe.HGet(ctx, key, "query_group")
				}
				return nil
			}); err != nil && !errors.Is(err, redis.Nil) {
				return sweep, fmt.Errorf("alarmd ownership: assignment sweep read: %w", err)
			}
			for index, key := range keys {
				named, err := replies[index].Result()
				if errors.Is(err, redis.Nil) || named == "" {
					continue
				}
				if err != nil {
					return sweep, fmt.Errorf("alarmd ownership: assignment sweep read: %w", err)
				}
				sweep.Scanned++
				queryGroup := execution.QueryGroupIdentity(named)
				if _, kept := keep[queryGroup]; kept {
					continue
				}
				if store.assignmentKey(queryGroup) != key {
					// The record's own name does not hash to its key: not a
					// record this store wrote. Left alone, counted with the
					// ones that changed under the sweep.
					sweep.Retired++
					sweep.Changed++
					continue
				}
				sweep.Retired++
				result, err := reclaimAssignmentScript.Run(ctx, store.client, []string{leaderKey, key, store.ownershipKey(queryGroup)},
					authority.Fence.OwnerID, authority.Fence.OwnerEpoch, authority.Fence.LeaseToken, named).Text()
				if err != nil {
					return sweep, fmt.Errorf("alarmd ownership: assignment sweep reclaim %s: %w", queryGroup, err)
				}
				switch result {
				case "RECLAIMED":
					sweep.Reclaimed++
				case "HELD":
					sweep.HeldByLease++
				case "STALE":
					return sweep, ErrStaleFence
				default:
					sweep.Changed++
				}
			}
		}
		if next == 0 {
			return sweep, nil
		}
		cursor = next
	}
}
