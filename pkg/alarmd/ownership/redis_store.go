// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type RedisStoreOptions struct {
	Address      string
	Username     string
	Password     string
	DB           int
	Prefix       string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
}

type RedisStore struct {
	prefix     string
	client     redis.UniversalClient
	ownsClient bool
}

func NewRedisStore(options RedisStoreOptions) (*RedisStore, error) {
	if options.Address == "" || options.Prefix == "" || options.DB < 0 || options.DialTimeout <= 0 ||
		options.ReadTimeout <= 0 || options.WriteTimeout <= 0 || options.PoolSize <= 0 {
		return nil, errors.New("alarmd ownership: invalid Redis store options")
	}
	if strings.ContainsAny(options.Prefix, "{} \t\r\n") {
		return nil, errors.New("alarmd ownership: Redis prefix must be canonical text")
	}
	return &RedisStore{
		prefix: options.Prefix,
		client: redis.NewClient(&redis.Options{
			Addr: options.Address, Username: options.Username, Password: options.Password, DB: options.DB,
			DialTimeout: options.DialTimeout, ReadTimeout: options.ReadTimeout, WriteTimeout: options.WriteTimeout,
			PoolSize: options.PoolSize,
		}), ownsClient: true,
	}, nil
}

// NewRedisStoreWithClient binds ownership facts to a runtime-owned universal
// Redis client. The caller retains client lifecycle ownership.
func NewRedisStoreWithClient(client redis.UniversalClient, prefix string) (*RedisStore, error) {
	if client == nil || prefix == "" || strings.ContainsAny(prefix, "{} \t\r\n") {
		return nil, errors.New("alarmd ownership: Redis client and canonical prefix are required")
	}
	return &RedisStore{prefix: prefix, client: client}, nil
}

// Ping is the store's readiness: the server answers, and it runs the owner
// fence. The second is not implied by the first -- a server that answers
// PING and refuses a write after TIME would pass here and fail the first
// lease -- so the fence itself is run once, on a key nothing else uses.
func (store *RedisStore) Ping(ctx context.Context) error {
	if store == nil || store.client == nil {
		return errors.New("alarmd ownership: Redis store is required")
	}
	if err := store.client.Ping(ctx).Err(); err != nil {
		return err
	}
	_, err := ProbeFenceClock(ctx, store.client, store.prefix)
	return err
}

func (store *RedisStore) Close() error {
	if store == nil || store.client == nil || !store.ownsClient {
		return nil
	}
	return store.client.Close()
}

func (store *RedisStore) RegisterWorker(ctx context.Context, worker WorkerRegistration) error {
	if err := worker.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(worker)
	if err != nil {
		return fmt.Errorf("alarmd ownership: encode worker registration: %w", err)
	}
	ttl := time.Until(worker.ExpiresAt)
	if ttl <= 0 {
		return errors.New("alarmd ownership: worker registration is already expired")
	}
	_, err = store.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, store.workerKey(worker.WorkerID), payload, ttl)
		pipe.ZAdd(ctx, store.workerRegistryKey(), &redis.Z{Score: float64(worker.ExpiresAt.UnixMilli()), Member: worker.WorkerID})
		return nil
	})
	return err
}

// ReadWorker reads one worker's registration: the registration and true,
// or false when none is stored. It reads the key alone, not the registry
// index, so an expired registration whose key is still there is returned
// with its ExpiresAt for the caller to judge.
func (store *RedisStore) ReadWorker(ctx context.Context, workerID string) (WorkerRegistration, bool, error) {
	if store == nil || store.client == nil || workerID == "" {
		return WorkerRegistration{}, false, errors.New("alarmd ownership: worker read needs a store and a worker")
	}
	payload, err := store.client.Get(ctx, store.workerKey(workerID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return WorkerRegistration{}, false, nil
	}
	if err != nil {
		return WorkerRegistration{}, false, err
	}
	var worker WorkerRegistration
	if err := json.Unmarshal(payload, &worker); err != nil {
		return WorkerRegistration{}, false, fmt.Errorf("alarmd ownership: decode worker registration: %w", err)
	}
	if err := worker.Validate(); err != nil {
		return WorkerRegistration{}, false, err
	}
	return worker, true, nil
}

// ReadControlLeader reads who holds the control leader lease and under
// which term, from the lease hash's owner_id and owner_epoch alone -- the
// token stays where it is. False when nobody holds it. Whether the lease
// is live is not judged here: a Worker that finds a dead Leader learns so
// from the connection, and a live one is renewed on Redis's clock, which
// this caller does not have.
func (store *RedisStore) ReadControlLeader(ctx context.Context) (ControlLeader, bool, error) {
	if store == nil || store.client == nil {
		return ControlLeader{}, false, errors.New("alarmd ownership: initialized store is required")
	}
	values, err := store.client.HMGet(ctx, store.ownershipKey(ControlLeaderIdentity), "owner_id", "owner_epoch").Result()
	if err != nil {
		return ControlLeader{}, false, err
	}
	if len(values) != 2 || values[0] == nil {
		return ControlLeader{}, false, nil
	}
	owner, _ := values[0].(string)
	if owner == "" {
		return ControlLeader{}, false, nil
	}
	leader := ControlLeader{OwnerID: owner}
	if text, ok := values[1].(string); ok && text != "" {
		epoch, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return ControlLeader{}, false, fmt.Errorf("alarmd ownership: control leader epoch %q: %w", text, err)
		}
		leader.OwnerEpoch = epoch
	}
	return leader, true, nil
}

// workerReadBatch bounds one registration pipeline, for the reason
// assignmentReadBatch bounds the other one.
const workerReadBatch = 512

// ListReadyWorkers reads the registry index and then every registration it
// names, the registrations in bounded pipelined batches.
//
// Every error here returns an error. That is not defensive style: the one
// thing this function must never do is answer a failed read with a short
// list. Its caller uses the returned set as "the workers that exist", and a
// truncated set does not read as a failure anywhere downstream -- it reads as
// workers having left, which is exactly the input that moves their Query
// Groups somewhere else. A registry that could not be read has to stop the
// round, and the only way to say so is the error.
//
// A registration key that is simply gone is a different fact, and it keeps
// its old handling: the worker is dropped from the index rather than failing
// the round, because the index outliving one registration is ordinary.
func (store *RedisStore) ListReadyWorkers(
	ctx context.Context,
	at time.Time,
) ([]WorkerRegistration, ControlReadStats, error) {
	stats := ControlReadStats{}
	if at.IsZero() {
		return nil, stats, errors.New("alarmd ownership: worker listing time is required")
	}
	started := time.Now()
	defer func() { stats.Duration = time.Since(started) }()
	stats.RoundTrips++
	if err := store.client.ZRemRangeByScore(ctx, store.workerRegistryKey(), "-inf", strconv.FormatInt(at.UnixMilli(), 10)).Err(); err != nil {
		return nil, stats, err
	}
	stats.RoundTrips++
	workerIDs, err := store.client.ZRangeByScore(ctx, store.workerRegistryKey(), &redis.ZRangeBy{
		Min: "(" + strconv.FormatInt(at.UnixMilli(), 10), Max: "+inf",
	}).Result()
	if err != nil {
		return nil, stats, err
	}
	stats.Keys = len(workerIDs)
	workers := make([]WorkerRegistration, 0, len(workerIDs))
	missing := make([]interface{}, 0)
	for start := 0; start < len(workerIDs); start += workerReadBatch {
		end := start + workerReadBatch
		if end > len(workerIDs) {
			end = len(workerIDs)
		}
		replies := make([]*redis.StringCmd, end-start)
		stats.RoundTrips++
		if _, pipeErr := store.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for offset, workerID := range workerIDs[start:end] {
				replies[offset] = pipe.Get(ctx, store.workerKey(workerID))
			}
			return nil
		}); pipeErr != nil && !errors.Is(pipeErr, redis.Nil) {
			return nil, stats, pipeErr
		}
		for offset, reply := range replies {
			workerID := workerIDs[start+offset]
			payload, getErr := reply.Bytes()
			if errors.Is(getErr, redis.Nil) {
				missing = append(missing, workerID)
				continue
			}
			if getErr != nil {
				return nil, stats, getErr
			}
			var worker WorkerRegistration
			if decodeErr := json.Unmarshal(payload, &worker); decodeErr != nil {
				return nil, stats, fmt.Errorf("alarmd ownership: decode worker registration: %w", decodeErr)
			}
			if validateErr := worker.Validate(); validateErr != nil {
				return nil, stats, validateErr
			}
			if worker.AssignmentReadiness == WorkerReady && worker.ExpiresAt.After(at) {
				workers = append(workers, worker)
			}
		}
	}
	if len(missing) > 0 {
		stats.RoundTrips++
		if err := store.client.ZRem(ctx, store.workerRegistryKey(), missing...).Err(); err != nil {
			return nil, stats, err
		}
	}
	sort.Slice(workers, func(left, right int) bool { return workers[left].WorkerID < workers[right].WorkerID })
	return workers, stats, nil
}

func (store *RedisStore) AcquireControlLeader(
	ctx context.Context,
	leaderID string,
	at time.Time,
	ttl time.Duration,
) (PublicationAuthority, error) {
	lease, err := store.acquire(ctx, ControlLeaderIdentity, leaderID, at, ttl, false)
	if err != nil {
		return PublicationAuthority{}, err
	}
	return PublicationAuthority{Fence: lease.Fence, Deadline: lease.Deadline}, nil
}

func (store *RedisStore) RenewControlLeader(
	ctx context.Context,
	authority PublicationAuthority,
	at time.Time,
	ttl time.Duration,
) (PublicationAuthority, error) {
	if authority.Fence.QueryGroup != ControlLeaderIdentity {
		return PublicationAuthority{}, ErrStaleFence
	}
	lease, err := store.renew(ctx, authority.Fence, at, ttl, false)
	if err != nil {
		return PublicationAuthority{}, err
	}
	return PublicationAuthority{Fence: lease.Fence, Deadline: lease.Deadline}, nil
}

func (store *RedisStore) PublishAssignment(
	ctx context.Context,
	authority PublicationAuthority,
	decision AssignmentDecision,
) (AssignmentRecord, error) {
	if authority.Fence.QueryGroup != ControlLeaderIdentity {
		return AssignmentRecord{}, errors.New("alarmd ownership: invalid Assignment publication")
	}
	if err := decision.Validate(); err != nil {
		return AssignmentRecord{}, err
	}
	result, err := publishAssignmentScript.Run(ctx, store.client, []string{
		store.ownershipKey(ControlLeaderIdentity), store.assignmentKey(decision.QueryGroup),
		store.ownershipKey(decision.QueryGroup),
	}, authority.Fence.OwnerID, authority.Fence.OwnerEpoch, authority.Fence.LeaseToken,
		decision.ExpectedRecordRevision, string(decision.QueryGroup), decision.DesiredWorkerID,
		string(decision.PlacementReason), decision.DecidedAt.UnixMilli(),
		decision.ContentScope, ContentSwitchMargin.Milliseconds(), boolText(decision.WithdrawContentScope)).Result()
	if err != nil {
		return AssignmentRecord{}, err
	}
	values, err := scriptValues(result, 10)
	if err != nil {
		return AssignmentRecord{}, err
	}
	if scriptText(values[0]) == "STALE" {
		return AssignmentRecord{}, ErrStaleFence
	}
	if scriptText(values[0]) == "CONFLICT" {
		return AssignmentRecord{}, ErrAssignmentConflict
	}
	return assignmentFromReply(decision.QueryGroup, values[0:6], values[7:10])
}

// assignmentFromReply assembles a record from the field order the scripts
// reply in: the six classic fields, then content scope, pending scope and
// effective_at_ms. It validates exactly as assignmentFromHash does.
func assignmentFromReply(
	queryGroup execution.QueryGroupIdentity,
	classic []interface{},
	content []interface{},
) (AssignmentRecord, error) {
	record := AssignmentRecord{
		QueryGroup: queryGroup, DesiredWorkerID: scriptText(classic[0]),
		AssignmentGeneration: uint64(scriptInt(classic[1])), RecordRevision: uint64(scriptInt(classic[2])),
		ControlEpoch: uint64(scriptInt(classic[3])), PlacementReason: PlacementReason(scriptText(classic[4])),
		AssignedAt:   time.UnixMilli(scriptInt(classic[5])),
		ContentScope: scriptText(content[0]), PendingContentScope: scriptText(content[1]),
	}
	if effective := scriptInt(content[2]); effective > 0 {
		record.EffectiveAt = time.UnixMilli(effective)
	}
	if err := record.Validate(); err != nil {
		return AssignmentRecord{}, err
	}
	return record, nil
}

func (store *RedisStore) ReadAssignment(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
) (AssignmentRecord, error) {
	if queryGroup == "" {
		return AssignmentRecord{}, errors.New("alarmd ownership: query group is required")
	}
	values, err := store.client.HGetAll(ctx, store.assignmentKey(queryGroup)).Result()
	if err != nil {
		return AssignmentRecord{}, err
	}
	return assignmentFromHash(queryGroup, values)
}

// assignmentFromHash is the one decoder both the single and the batched read
// use. Two decoders for one record shape would be two things that must agree
// about the same hash, and the batched path exists precisely to be used
// instead of the other one.
func assignmentFromHash(
	queryGroup execution.QueryGroupIdentity,
	values map[string]string,
) (AssignmentRecord, error) {
	if len(values) == 0 {
		return AssignmentRecord{}, ErrAssignmentAbsent
	}
	record := AssignmentRecord{
		QueryGroup: queryGroup, DesiredWorkerID: values["desired_worker_id"],
		AssignmentGeneration: parseUint(values["assignment_generation"]), RecordRevision: parseUint(values["record_revision"]),
		ControlEpoch: parseUint(values["control_epoch"]), PlacementReason: PlacementReason(values["placement_reason"]),
		AssignedAt:   time.UnixMilli(parseInt(values["assigned_at_ms"])),
		ContentScope: values["content_scope"], PendingContentScope: values["pending_content_scope"],
	}
	if effective := parseInt(values["effective_at_ms"]); effective > 0 {
		record.EffectiveAt = time.UnixMilli(effective)
	}
	if err := record.Validate(); err != nil {
		return AssignmentRecord{}, err
	}
	return record, nil
}

// ControlReadStats is what one control-plane read actually did on the wire.
//
// It is filled in by the code that issues the calls, not derived afterwards
// from a client-side label: the question the round has to answer is "how many
// round trips did this round spend", and a per-client counter cannot say which
// round trips belonged to which round, nor separate the assignment reads from
// everything else the same client is doing.
type ControlReadStats struct {
	// Keys is how many records were asked for.
	Keys int
	// RoundTrips is how many times the caller waited for Redis. One per
	// pipelined batch plus each unpipelined command the read needs.
	RoundTrips int
	// Duration is the wall time those round trips took.
	Duration time.Duration
}

// Add folds another read's stats into these, for a round that does more than
// one of them.
func (stats ControlReadStats) Add(other ControlReadStats) ControlReadStats {
	return ControlReadStats{
		Keys:       stats.Keys + other.Keys,
		RoundTrips: stats.RoundTrips + other.RoundTrips,
		Duration:   stats.Duration + other.Duration,
	}
}

// assignmentReadBatch bounds one pipeline. A round over every Query Group of
// the population would otherwise put the whole population in one buffer.
const assignmentReadBatch = 512

// ReadAssignments reads many Assignment records in bounded pipelined batches.
//
// The records are the same ones ReadAssignment returns, decoded by the same
// function and validated the same way. What changes is the waiting: a round
// that settles Q Query Groups spent Q round trips here and now spends
// ceil(Q/512).
//
// A Query Group with no record is absent from the returned map, which is the
// batched form of ErrAssignmentAbsent -- not an error, and not an empty
// record either. A read that fails is an error and the map is nil. The
// distinction is the whole point: absent means "never assigned, place it",
// and a failed read means "we do not know", and a round that turned the
// second into the first would republish every Assignment in the population
// against a ready set it could not verify.
func (store *RedisStore) ReadAssignments(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]AssignmentRecord, ControlReadStats, error) {
	if store == nil || store.client == nil {
		return nil, ControlReadStats{}, errors.New("alarmd ownership: initialized store is required")
	}
	stats := ControlReadStats{Keys: len(queryGroups)}
	records := make(map[execution.QueryGroupIdentity]AssignmentRecord, len(queryGroups))
	if len(queryGroups) == 0 {
		return records, stats, nil
	}
	started := time.Now()
	defer func() { stats.Duration = time.Since(started) }()
	for start := 0; start < len(queryGroups); start += assignmentReadBatch {
		end := start + assignmentReadBatch
		if end > len(queryGroups) {
			end = len(queryGroups)
		}
		replies := make([]*redis.StringStringMapCmd, end-start)
		stats.RoundTrips++
		if _, err := store.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for offset, queryGroup := range queryGroups[start:end] {
				if queryGroup == "" {
					return errors.New("alarmd ownership: query group is required")
				}
				replies[offset] = pipe.HGetAll(ctx, store.assignmentKey(queryGroup))
			}
			return nil
		}); err != nil && !errors.Is(err, redis.Nil) {
			return nil, stats, err
		}
		for offset, reply := range replies {
			queryGroup := queryGroups[start+offset]
			values, err := reply.Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return nil, stats, err
			}
			record, decodeErr := assignmentFromHash(queryGroup, values)
			if errors.Is(decodeErr, ErrAssignmentAbsent) {
				continue
			}
			if decodeErr != nil {
				return nil, stats, decodeErr
			}
			records[queryGroup] = record
		}
	}
	return records, stats, nil
}

func (store *RedisStore) Acquire(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	workerID string,
	at time.Time,
	ttl time.Duration,
) (Lease, error) {
	return store.acquire(ctx, queryGroup, workerID, at, ttl, true)
}

func (store *RedisStore) acquire(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	ownerID string,
	at time.Time,
	ttl time.Duration,
	requireAssignment bool,
) (Lease, error) {
	if queryGroup == "" || ownerID == "" || at.IsZero() || ttl <= 0 {
		return Lease{}, errors.New("alarmd ownership: invalid lease acquisition")
	}
	token, err := leaseToken()
	if err != nil {
		return Lease{}, err
	}
	result, err := acquireScript.Run(ctx, store.client, []string{
		store.assignmentKey(queryGroup), store.ownershipKey(queryGroup),
	}, boolText(requireAssignment), ownerID, ttl.Milliseconds(), token).Result()
	if err != nil {
		return Lease{}, err
	}
	values, err := scriptValues(result, 5)
	if err != nil {
		return Lease{}, err
	}
	switch scriptText(values[0]) {
	case "NOT_DESIRED":
		return Lease{}, ErrNotDesired
	case "BUSY":
		// values[2] is the deadline of the lease in the way, on the server's
		// clock; nothing reads it yet. It is in the reply for a caller that
		// wants to wait exactly that long instead of retrying blind.
		return Lease{}, ErrLeaseBusy
	case "PAUSED":
		return Lease{}, ErrStaleFence
	case "OWNED":
		return Lease{
			Fence: execution.OwnerFence{
				QueryGroup: queryGroup, OwnerID: ownerID, OwnerEpoch: uint64(scriptInt(values[1])), LeaseToken: token,
			},
			Deadline:     onCallerClock(at, scriptInt(values[4]), scriptInt(values[2])),
			ContentScope: scriptText(values[3]),
		}, nil
	default:
		return Lease{}, errors.New("alarmd ownership: invalid lease acquisition response")
	}
}

// onCallerClock carries a server instant over to the caller's clock: the
// duration still to run on the server, added to the instant the caller
// passed in when it asked. That instant is from before the round trip, so
// the result is earlier than the server's instant by at least the request's
// way in, and never later. A server instant already reached returns the
// anchor itself, so a comparison with the caller's clock reads it as passed.
//
// The one thing this rests on is that the anchor was read before the server
// read TIME, and that the caller later compares the result against the same
// clock the anchor came from. time.Now() carries a monotonic reading and
// Add keeps it, so the comparison is monotonic and a wall clock stepped
// back does not make a lapsed lease look live. An anchor without one -- a
// time.Unix/UnixMilli round trip, an injected clock that strips it -- is a
// wall-clock anchor, and a step back after it would lengthen the lease in
// the holder's eyes by the size of the step. Every production caller today
// passes time.Now() through; this is the precondition written down, not a
// defect found.
func onCallerClock(anchor time.Time, serverNowMillis, serverInstantMillis int64) time.Time {
	remaining := serverInstantMillis - serverNowMillis
	if remaining <= 0 {
		return anchor
	}
	return anchor.Add(time.Duration(remaining) * time.Millisecond)
}

func (store *RedisStore) Renew(
	ctx context.Context,
	fence execution.OwnerFence,
	at time.Time,
	ttl time.Duration,
) (Lease, error) {
	return store.renew(ctx, fence, at, ttl, true)
}

func (store *RedisStore) renew(
	ctx context.Context,
	fence execution.OwnerFence,
	at time.Time,
	ttl time.Duration,
	requireAssignment bool,
) (Lease, error) {
	if err := validateFence(fence); err != nil || at.IsZero() || ttl <= 0 {
		return Lease{}, ErrStaleFence
	}
	result, err := renewScript.Run(ctx, store.client, []string{
		store.assignmentKey(fence.QueryGroup), store.ownershipKey(fence.QueryGroup),
	}, boolText(requireAssignment), fence.OwnerID, fence.OwnerEpoch, fence.LeaseToken, ttl.Milliseconds()).Result()
	if err != nil {
		return Lease{}, err
	}
	values, err := scriptValues(result, 6)
	if err != nil {
		return Lease{}, err
	}
	switch scriptText(values[0]) {
	case "NOT_DESIRED":
		return Lease{}, ErrNotDesired
	case "RENEWED":
		// The deadline the store wrote, not the one asked for: under a
		// pending content change the store caps it at the change's
		// effective time, and the holder must plan by the capped value.
		// Both come back as server instants and are carried over to the
		// caller's clock from the same anchor, so under the cap they are
		// the same instant here as they are on the server.
		serverNow := scriptInt(values[5])
		lease := Lease{
			Fence: fence, Deadline: onCallerClock(at, serverNow, scriptInt(values[1])),
			ContentScope: scriptText(values[2]), PendingContentScope: scriptText(values[3]),
		}
		if effective := scriptInt(values[4]); effective > 0 {
			lease.EffectiveAt = onCallerClock(at, serverNow, effective)
		}
		return lease, nil
	default:
		return Lease{}, ErrStaleFence
	}
}

func (store *RedisStore) CheckFence(ctx context.Context, fence execution.OwnerFence) error {
	_, err := store.checkFence(ctx, fence, "")
	return err
}

// CheckFenceWithAssignment validates the fence and returns the Assignment
// record that names its owner, both decided by the same script run.
//
// It exists because the scheduler's idle path used to ask for the two facts
// separately -- one script run for the fence, one HGETALL for the record --
// with nothing but a local time comparison in between. Two round trips for two
// facts that belong to the same instant is both the slower and the weaker
// answer, since an Assignment published between them would be read against a
// fence checked before it.
//
// The record is assembled and validated exactly as ReadAssignment assembles it
// from HGETALL, so a caller cannot tell the two apart by what it gets back.
// A fence the store rejects returns the rejection and no record: there is no
// state in which a worker should act on an Assignment its fence does not cover.
func (store *RedisStore) CheckFenceWithAssignment(
	ctx context.Context,
	fence execution.OwnerFence,
) (AssignmentRecord, error) {
	if fence.QueryGroup == ControlLeaderIdentity {
		return AssignmentRecord{}, errors.New("alarmd ownership: control leader identity has no Assignment record")
	}
	values, err := store.checkFence(ctx, fence, "")
	if err != nil {
		return AssignmentRecord{}, err
	}
	return assignmentFromReply(fence.QueryGroup, values[1:7], values[7:10])
}

// CheckFenceForContentScope is CheckFence for a writer that declares the
// executable view it is acting on: the fence then also refuses a record that
// names another content scope, with ErrContentScopeMoved. An empty scope is
// exactly CheckFence.
func (store *RedisStore) CheckFenceForContentScope(
	ctx context.Context,
	fence execution.OwnerFence,
	contentScope string,
) error {
	_, err := store.checkFence(ctx, fence, contentScope)
	return err
}

// checkFence returns the whole script reply so the exported entry points
// share one decision. Only a VALID fence yields values; every rejection is
// mapped to the same error the caller has always seen. The caller names no
// instant: the lease deadline is compared with Redis's clock in the script.
func (store *RedisStore) checkFence(
	ctx context.Context,
	fence execution.OwnerFence,
	contentScope string,
) ([]interface{}, error) {
	requireAssignment := fence.QueryGroup != ControlLeaderIdentity
	if err := validateFence(fence); err != nil {
		return nil, ErrStaleFence
	}
	result, err := checkFenceScript.Run(ctx, store.client, []string{
		store.assignmentKey(fence.QueryGroup), store.ownershipKey(fence.QueryGroup),
	}, boolText(requireAssignment), fence.OwnerID, fence.OwnerEpoch, fence.LeaseToken, contentScope).Result()
	if err != nil {
		return nil, err
	}
	values, err := scriptValues(result, 10)
	if err != nil {
		return nil, err
	}
	switch scriptText(values[0]) {
	case "VALID":
		return values, nil
	case "NOT_DESIRED":
		return nil, ErrNotDesired
	case "CONTENT_MOVED":
		return nil, ErrContentScopeMoved
	default:
		return nil, ErrStaleFence
	}
}

func (store *RedisStore) Release(ctx context.Context, fence execution.OwnerFence) error {
	if err := validateFence(fence); err != nil {
		return ErrStaleFence
	}
	result, err := releaseScript.Run(ctx, store.client, []string{store.ownershipKey(fence.QueryGroup)},
		fence.OwnerID, fence.OwnerEpoch, fence.LeaseToken).Text()
	if err != nil {
		return err
	}
	if result != "RELEASED" {
		return ErrStaleFence
	}
	return nil
}

func (store *RedisStore) FencedCompareAndSet(
	ctx context.Context,
	request FencedCASRequest,
) (FencedCASStatus, error) {
	if err := validateFence(request.Fence); err != nil || request.Namespace == "" ||
		strings.ContainsAny(request.Namespace, "{} \t\r\n") || len(request.Value) == 0 || request.TTL < 0 ||
		(request.ExpectedMissing && len(request.Expected) != 0) {
		return "", errors.New("alarmd ownership: invalid fenced CAS request")
	}
	requireAssignment := request.Fence.QueryGroup != ControlLeaderIdentity
	result, err := fencedCASScript.Run(ctx, store.client, []string{
		store.assignmentKey(request.Fence.QueryGroup), store.ownershipKey(request.Fence.QueryGroup),
		store.controlKey(request.Fence.QueryGroup, request.Namespace),
	}, boolText(requireAssignment), request.Fence.OwnerID, request.Fence.OwnerEpoch, request.Fence.LeaseToken,
		boolText(request.ExpectedMissing), request.Expected, request.Value, request.TTL.Milliseconds(),
		request.ContentScope).Text()
	if err != nil {
		return "", err
	}
	switch result {
	case string(FencedCASApplied):
		return FencedCASApplied, nil
	case string(FencedCASConflict):
		return FencedCASConflict, nil
	case string(FencedCASStaleOwner):
		return FencedCASStaleOwner, ErrStaleFence
	case string(FencedCASContentMoved):
		return FencedCASContentMoved, ErrContentScopeMoved
	default:
		return "", errors.New("alarmd ownership: invalid fenced CAS response")
	}
}

func (store *RedisStore) ReadControl(
	ctx context.Context,
	queryGroup execution.QueryGroupIdentity,
	namespace string,
) ([]byte, bool, error) {
	if store == nil || store.client == nil || queryGroup == "" || namespace == "" ||
		strings.ContainsAny(namespace, "{} \t\r\n") {
		return nil, false, errors.New("alarmd ownership: invalid control read")
	}
	value, err := store.client.Get(ctx, store.controlKey(queryGroup, namespace)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]byte(nil), value...), false, nil
}

// ControlRead is one entry of a batched control read.
type ControlRead struct {
	Raw     []byte
	Missing bool
}

const controlReadBatch = 512

// ReadControlBatch reads one control namespace for many Query Groups in
// pipelined batches: the same bytes ReadControl returns, one round trip per
// batch instead of one per Query Group. Keys carry per-Query-Group hash
// tags, so the reads are pipelined rather than sent as one MGET, which a
// cluster would refuse across slots.
func (store *RedisStore) ReadControlBatch(
	ctx context.Context,
	queryGroups []execution.QueryGroupIdentity,
	namespace string,
) ([]ControlRead, error) {
	if store == nil || store.client == nil || namespace == "" || strings.ContainsAny(namespace, "{} \t\r\n") {
		return nil, errors.New("alarmd ownership: invalid control read")
	}
	reads := make([]ControlRead, len(queryGroups))
	for start := 0; start < len(queryGroups); start += controlReadBatch {
		end := start + controlReadBatch
		if end > len(queryGroups) {
			end = len(queryGroups)
		}
		replies := make([]*redis.StringCmd, end-start)
		if _, err := store.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for offset, queryGroup := range queryGroups[start:end] {
				if queryGroup == "" {
					return errors.New("alarmd ownership: invalid control read")
				}
				replies[offset] = pipe.Get(ctx, store.controlKey(queryGroup, namespace))
			}
			return nil
		}); err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for offset, reply := range replies {
			value, err := reply.Bytes()
			switch {
			case errors.Is(err, redis.Nil):
				reads[start+offset] = ControlRead{Missing: true}
			case err != nil:
				return nil, err
			default:
				reads[start+offset] = ControlRead{Raw: append([]byte(nil), value...)}
			}
		}
	}
	return reads, nil
}

func (store *RedisStore) workerRegistryKey() string {
	return store.prefix + ":worker-registry"
}

func (store *RedisStore) workerKey(workerID string) string {
	digest := sha256.Sum256([]byte(workerID))
	return store.prefix + ":worker:" + hex.EncodeToString(digest[:])
}

// FenceKeys is a read-only descriptor of the Redis facts one fenced write must
// consult before it mutates anything: the assignment HASH that names the
// desired worker and the ownership HASH that holds the live lease. It exposes
// no mutation; other stores use it to verify the same fence rule as
// CheckFence and FencedCompareAndSet inside their own scripts.
type FenceKeys struct {
	AssignmentKey     string
	OwnershipKey      string
	RequireAssignment bool
}

// FenceKeys locates the ownership facts of one query group. The control
// leader identity has no assignment record, exactly as in CheckFence.
func (store *RedisStore) FenceKeys(queryGroup execution.QueryGroupIdentity) FenceKeys {
	if store == nil {
		return FenceKeys{}
	}
	return FenceKeys{
		AssignmentKey:     store.assignmentKey(queryGroup),
		OwnershipKey:      store.ownershipKey(queryGroup),
		RequireAssignment: queryGroup != ControlLeaderIdentity,
	}
}

func (store *RedisStore) assignmentKey(queryGroup execution.QueryGroupIdentity) string {
	return store.controlKey(queryGroup, "assignment")
}

func (store *RedisStore) ownershipKey(queryGroup execution.QueryGroupIdentity) string {
	return store.controlKey(queryGroup, "ownership")
}

func (store *RedisStore) controlKey(queryGroup execution.QueryGroupIdentity, namespace string) string {
	digest := sha256.Sum256([]byte(queryGroup))
	return store.prefix + ":{" + hex.EncodeToString(digest[:]) + "}:" + namespace
}

func validateFence(fence execution.OwnerFence) error {
	if fence.QueryGroup == "" || fence.OwnerID == "" || fence.OwnerEpoch == 0 || fence.LeaseToken == "" {
		return ErrStaleFence
	}
	return nil
}

func leaseToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("alarmd ownership: generate lease token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func boolText(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func scriptValues(value interface{}, minimum int) ([]interface{}, error) {
	values, ok := value.([]interface{})
	if !ok || len(values) < minimum {
		return nil, fmt.Errorf("alarmd ownership: invalid Redis script response %T", value)
	}
	return values, nil
}

func scriptText(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(value)
	}
}

func scriptInt(value interface{}) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	case []byte:
		parsed, _ := strconv.ParseInt(string(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func parseUint(value string) uint64 {
	parsed, _ := strconv.ParseUint(value, 10, 64)
	return parsed
}

func parseInt(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

// acquireScript hands out a lease. The deadline is minted here, on the
// server's clock, from the lifetime the caller asked for (ARGV[3], in
// milliseconds); the caller never names an instant. A new lease starts on
// the content scope the record names now: a pending change whose time has
// come is promoted here, so the holder that follows a lapsed lease is on the
// new content from its first Slot. The reply carries the scope so the holder
// knows what it was admitted to execute, and the server instant so it can
// keep the remaining duration on its own clock.
//
// Every branch returns five elements: status, epoch, deadline_ms, scope,
// now_ms. BUSY carries the deadline of the lease that is in the way.
var acquireScript = redis.NewScript(FenceLua + `
local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local ttl_ms = tonumber(ARGV[3])
local token = ARGV[4]
local now_ms = redis_now_ms()
local deadline_ms = now_ms + ttl_ms
local scope = ''
if require_assignment == '1' then
  local desired = redis.call('HGET', KEYS[1], 'desired_worker_id')
  if not desired or desired ~= owner_id then return {'NOT_DESIRED', 0, 0, '', now_ms} end
  scope = current_content_scope(KEYS[1], now_ms)
end
local disposition = redis.call('HGET', KEYS[2], 'execution_disposition')
if disposition and disposition ~= 'ACTIVE' then return {'PAUSED', 0, 0, '', now_ms} end
local current_owner = redis.call('HGET', KEYS[2], 'owner_id')
local current_deadline = tonumber(redis.call('HGET', KEYS[2], 'deadline_ms') or '0')
if current_owner and current_owner ~= '' and current_deadline > now_ms then return {'BUSY', 0, current_deadline, '', now_ms} end
local epoch = tonumber(redis.call('HGET', KEYS[2], 'owner_epoch') or '0') + 1
redis.call('HSET', KEYS[2], 'owner_id', owner_id, 'owner_epoch', epoch, 'lease_token', token,
  'deadline_ms', deadline_ms, 'execution_disposition', 'ACTIVE')
return {'OWNED', epoch, deadline_ms, scope, now_ms}
`)

// renewScript extends a lease, and is where a holder learns about a content
// change. The reply carries the scope the record names now and, when a
// change is pending, the scope it will switch to and when. The new deadline
// is capped at that time: a lease is never extended past the moment its
// content stops being authorized, so a holder that reads nothing else still
// runs out of lease exactly there and comes back through acquire, onto the
// new content. A pending change whose time has already come is promoted
// first and the renewal proceeds uncapped on the new scope.
//
// The new deadline is minted on the server's clock from the lifetime asked
// for (ARGV[5], milliseconds), like acquire's. Every branch returns six
// elements: status, deadline_ms, scope, pending scope, effective_at_ms,
// now_ms; rejections carry empty tails.
var renewScript = redis.NewScript(FenceLua + `
local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local ttl_ms = tonumber(ARGV[5])
local now_ms = redis_now_ms()
local deadline_ms = now_ms + ttl_ms
local refusal = fence_refusal(KEYS[1], KEYS[2], require_assignment, owner_id, epoch, token, '', now_ms)
if refusal then return {refusal, 0, '', '', 0, now_ms} end
local scope, pending, effective = '', '', 0
if require_assignment == '1' then
  scope = current_content_scope(KEYS[1], now_ms)
  local change = redis.call('HMGET', KEYS[1], 'pending_content_scope', 'effective_at_ms')
  if change[1] and change[1] ~= '' then
    pending = change[1]
    effective = tonumber(change[2] or '0')
    if effective > 0 and deadline_ms > effective then deadline_ms = effective end
  end
end
redis.call('HSET', KEYS[2], 'deadline_ms', deadline_ms)
return {'RENEWED', deadline_ms, scope, pending, effective, now_ms}
`)

// checkFenceScript answers both questions a fenced worker asks before it acts:
// is this lease still the live one, and which Assignment record names its
// owner. It had to read desired_worker_id to answer the first anyway, so the
// second costs it one HMGET where it used to do one HGET -- and saves the
// caller the separate HGETALL that ReadAssignment would have sent. The two
// facts then describe one instant, which two round trips cannot promise: the
// Control Leader can publish a new Assignment between them.
//
// Every branch returns a seven element array so one reply shape covers every
// outcome. Absent hash fields are returned as empty strings rather than left
// out: a Lua table stops converting at its first nil and a missing HMGET
// element is nil, so an unguarded record would silently shorten the reply for
// the control leader identity, which carries no Assignment record at all.
//
// ARGV[5], when present and non-empty, is the content scope the caller is
// executing; the fence then also refuses a record that names another. The
// reply's elements 8 to 10 carry the record's content scope, pending scope
// and effective time, empty for a record that has none.
var checkFenceScript = redis.NewScript(FenceLua + `
local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local content_scope = ARGV[5] or ''
local now_ms = redis_now_ms()
local empty = {'', '', '', '', '', '', '', '', ''}
local refusal = fence_refusal(KEYS[1], KEYS[2], require_assignment, owner_id, epoch, token, content_scope, now_ms)
if refusal then return {refusal, empty[1], empty[2], empty[3], empty[4], empty[5], empty[6], empty[7], empty[8], empty[9]} end
local record = {'', '', '', '', '', '', '', '', ''}
if require_assignment == '1' then
  local fields = redis.call('HMGET', KEYS[1], 'desired_worker_id', 'assignment_generation',
    'record_revision', 'control_epoch', 'placement_reason', 'assigned_at_ms',
    'content_scope', 'pending_content_scope', 'effective_at_ms')
  for index = 1, 9 do
    if fields[index] then record[index] = fields[index] end
  end
end
return {'VALID', record[1], record[2], record[3], record[4], record[5], record[6], record[7], record[8], record[9]}
`)

var releaseScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'owner_id') ~= ARGV[1] or
   redis.call('HGET', KEYS[1], 'owner_epoch') ~= ARGV[2] or
   redis.call('HGET', KEYS[1], 'lease_token') ~= ARGV[3] then return 'STALE' end
redis.call('HDEL', KEYS[1], 'owner_id', 'lease_token', 'deadline_ms')
return 'RELEASED'
`)

// publishAssignmentScript writes a leader's decision under the leader fence
// and the record's revision CAS. ARGV is the leader fence (1 to 3), the
// expected record revision (4), the query group, desired worker, placement
// reason and decision time (5 to 8), then the content scope the decision
// authorizes (ARGV[9], empty leaves the record's scope as it is) and the
// margin a pending change waits after the current lease deadline
// (ARGV[10]); KEYS[3] is the Query Group's ownership hash, read for that
// deadline. Whether that lease is live, and whether a pending change has
// fallen due, are judged on the server's clock, the one the deadline and
// effective_at_ms were minted on; the decision time is what the leader says
// about itself and is only stored.
//
// A content change for a desired worker that stays and holds a live lease
// is written as pending: the record keeps authorizing the old scope until
// effective_at_ms = lease deadline + margin, and renewal will not
// extend the lease past that. A change with no live holder, or one that
// arrives together with a change of desired worker, is written directly --
// there is nobody to protect from it, or the old holder is already refused
// by desired_worker_id. So is the first scope a record ever gets: a record
// that names nothing authorized nothing in particular, its fence admitted
// every content, and there is no old content whose holder a deadline would
// protect. Written as pending it would cap every lease in the fleet once on
// the round the contract starts, and each Query Group would lose its lease
// and hold its output for the last batch bound of it; written directly it
// binds from now, which the holder -- on the content the record names, or
// it would not be the current publication -- passes. A pending change written twice with the same scope is
// left alone; a different scope replaces it and never moves effective_at_ms
// earlier. Every scope the leader decides here bumps record_revision, so a
// CAS reader sees the decision; assignment_generation counts changes of
// desired worker only. The promotion of a pending scope when its time comes
// (current_content_scope, run by whichever script reads the record next)
// does not bump it: that is the record settling a decision already
// revisioned, not a new one, and bumping there would fail the leader's own
// CAS against a revision it read moments ago.
//
// ARGV[11] is the withdrawal flag: '1' clears the record's scope and any
// pending change directly, bumping record_revision when there was anything
// to clear. Withdrawing the comparison refuses nobody, so it waits for no
// deadline; it is exclusive with a named scope and checked before it.
//
// Replies are ten elements: desired worker, generation, revision, control
// epoch, reason, assigned_at, query group, content scope, pending scope,
// effective_at_ms.
var publishAssignmentScript = redis.NewScript(FenceLua + `
local leader_id = ARGV[1]
local leader_epoch = ARGV[2]
local leader_token = ARGV[3]
local now_ms = redis_now_ms()
if fence_refusal('', KEYS[1], '0', leader_id, leader_epoch, leader_token, '', now_ms) then
  return {'STALE', 0, 0, 0, '', 0, '', '', '', 0}
end
local expected_revision = tonumber(ARGV[4])
local current_revision = tonumber(redis.call('HGET', KEYS[2], 'record_revision') or '0')
if current_revision ~= expected_revision then
  return {'CONFLICT', 0, current_revision, 0, '', 0, '', '', '', 0}
end
local query_group = ARGV[5]
local desired = ARGV[6]
local reason = ARGV[7]
local assigned_at = ARGV[8]
local wanted_scope = ARGV[9] or ''
local margin_ms = tonumber(ARGV[10] or '0')
local withdraw = ARGV[11] == '1'
local function reply()
  local f = redis.call('HMGET', KEYS[2], 'desired_worker_id', 'assignment_generation', 'record_revision',
    'control_epoch', 'placement_reason', 'assigned_at_ms', 'content_scope', 'pending_content_scope', 'effective_at_ms')
  return {f[1], f[2], f[3], f[4], f[5], f[6], query_group, f[7] or '', f[8] or '', tonumber(f[9] or '0')}
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
`)

// fencedCASScript writes one control value under the owner fence. ARGV is
// the fence (1 to 4), expected-missing flag, expected value, new value and
// TTL in milliseconds (5 to 8), then ARGV[9], which when present and
// non-empty is the content scope the caller is executing. A fence refused
// for a moved scope answers CONTENT_MOVED rather than STALE_OWNER: the lease
// is fine, the view is not, and the two send a worker down different paths.
var fencedCASScript = redis.NewScript(FenceLua + `
local require_assignment = ARGV[1]
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
`)
