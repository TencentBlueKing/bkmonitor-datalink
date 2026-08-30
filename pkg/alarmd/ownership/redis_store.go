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
	prefix string
	client *redis.Client
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
		}),
	}, nil
}

func (store *RedisStore) Ping(ctx context.Context) error {
	if store == nil || store.client == nil {
		return errors.New("alarmd ownership: Redis store is required")
	}
	return store.client.Ping(ctx).Err()
}

func (store *RedisStore) Close() error {
	if store == nil || store.client == nil {
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

func (store *RedisStore) ListReadyWorkers(ctx context.Context, at time.Time) ([]WorkerRegistration, error) {
	if at.IsZero() {
		return nil, errors.New("alarmd ownership: worker listing time is required")
	}
	if err := store.client.ZRemRangeByScore(ctx, store.workerRegistryKey(), "-inf", strconv.FormatInt(at.UnixMilli(), 10)).Err(); err != nil {
		return nil, err
	}
	workerIDs, err := store.client.ZRangeByScore(ctx, store.workerRegistryKey(), &redis.ZRangeBy{
		Min: "(" + strconv.FormatInt(at.UnixMilli(), 10), Max: "+inf",
	}).Result()
	if err != nil {
		return nil, err
	}
	workers := make([]WorkerRegistration, 0, len(workerIDs))
	missing := make([]interface{}, 0)
	for _, workerID := range workerIDs {
		payload, getErr := store.client.Get(ctx, store.workerKey(workerID)).Bytes()
		if errors.Is(getErr, redis.Nil) {
			missing = append(missing, workerID)
			continue
		}
		if getErr != nil {
			return nil, getErr
		}
		var worker WorkerRegistration
		if err := json.Unmarshal(payload, &worker); err != nil {
			return nil, fmt.Errorf("alarmd ownership: decode worker registration: %w", err)
		}
		if err := worker.Validate(); err != nil {
			return nil, err
		}
		if worker.AssignmentReadiness == WorkerReady && worker.ExpiresAt.After(at) {
			workers = append(workers, worker)
		}
	}
	if len(missing) > 0 {
		if err := store.client.ZRem(ctx, store.workerRegistryKey(), missing...).Err(); err != nil {
			return nil, err
		}
	}
	sort.Slice(workers, func(left, right int) bool { return workers[left].WorkerID < workers[right].WorkerID })
	return workers, nil
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
	}, authority.Fence.OwnerID, authority.Fence.OwnerEpoch, authority.Fence.LeaseToken, decision.DecidedAt.UnixMilli(),
		decision.ExpectedRecordRevision, string(decision.QueryGroup), decision.DesiredWorkerID,
		string(decision.PlacementReason), decision.DecidedAt.UnixMilli()).Result()
	if err != nil {
		return AssignmentRecord{}, err
	}
	values, err := scriptValues(result, 7)
	if err != nil {
		return AssignmentRecord{}, err
	}
	if scriptText(values[0]) == "STALE" {
		return AssignmentRecord{}, ErrStaleFence
	}
	if scriptText(values[0]) == "CONFLICT" {
		return AssignmentRecord{}, ErrAssignmentConflict
	}
	record := AssignmentRecord{
		QueryGroup: decision.QueryGroup, DesiredWorkerID: scriptText(values[0]),
		AssignmentGeneration: uint64(scriptInt(values[1])), RecordRevision: uint64(scriptInt(values[2])),
		ControlEpoch: uint64(scriptInt(values[3])), PlacementReason: PlacementReason(scriptText(values[4])),
		AssignedAt: time.UnixMilli(scriptInt(values[5])),
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
	if len(values) == 0 {
		return AssignmentRecord{}, ErrAssignmentAbsent
	}
	record := AssignmentRecord{
		QueryGroup: queryGroup, DesiredWorkerID: values["desired_worker_id"],
		AssignmentGeneration: parseUint(values["assignment_generation"]), RecordRevision: parseUint(values["record_revision"]),
		ControlEpoch: parseUint(values["control_epoch"]), PlacementReason: PlacementReason(values["placement_reason"]),
		AssignedAt: time.UnixMilli(parseInt(values["assigned_at_ms"])),
	}
	if err := record.Validate(); err != nil {
		return AssignmentRecord{}, err
	}
	return record, nil
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
	deadline := at.Add(ttl)
	result, err := acquireScript.Run(ctx, store.client, []string{
		store.assignmentKey(queryGroup), store.ownershipKey(queryGroup),
	}, boolText(requireAssignment), ownerID, at.UnixMilli(), deadline.UnixMilli(), token).Result()
	if err != nil {
		return Lease{}, err
	}
	values, err := scriptValues(result, 3)
	if err != nil {
		return Lease{}, err
	}
	switch scriptText(values[0]) {
	case "NOT_DESIRED":
		return Lease{}, ErrNotDesired
	case "BUSY":
		return Lease{}, ErrLeaseBusy
	case "PAUSED":
		return Lease{}, ErrStaleFence
	case "OWNED":
		return Lease{
			Fence: execution.OwnerFence{
				QueryGroup: queryGroup, OwnerID: ownerID, OwnerEpoch: uint64(scriptInt(values[1])), LeaseToken: token,
			},
			Deadline: time.UnixMilli(scriptInt(values[2])),
		}, nil
	default:
		return Lease{}, errors.New("alarmd ownership: invalid lease acquisition response")
	}
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
	deadline := at.Add(ttl)
	result, err := renewScript.Run(ctx, store.client, []string{
		store.assignmentKey(fence.QueryGroup), store.ownershipKey(fence.QueryGroup),
	}, boolText(requireAssignment), fence.OwnerID, fence.OwnerEpoch, fence.LeaseToken, at.UnixMilli(), deadline.UnixMilli()).Text()
	if err != nil {
		return Lease{}, err
	}
	switch result {
	case "NOT_DESIRED":
		return Lease{}, ErrNotDesired
	case "RENEWED":
		return Lease{Fence: fence, Deadline: deadline}, nil
	default:
		return Lease{}, ErrStaleFence
	}
}

func (store *RedisStore) CheckFence(ctx context.Context, fence execution.OwnerFence, at time.Time) error {
	requireAssignment := fence.QueryGroup != ControlLeaderIdentity
	if err := validateFence(fence); err != nil || at.IsZero() {
		return ErrStaleFence
	}
	result, err := checkFenceScript.Run(ctx, store.client, []string{
		store.assignmentKey(fence.QueryGroup), store.ownershipKey(fence.QueryGroup),
	}, boolText(requireAssignment), fence.OwnerID, fence.OwnerEpoch, fence.LeaseToken, at.UnixMilli()).Text()
	if err != nil {
		return err
	}
	if result == "VALID" {
		return nil
	}
	if result == "NOT_DESIRED" {
		return ErrNotDesired
	}
	return ErrStaleFence
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
	if err := validateFence(request.Fence); err != nil || request.At.IsZero() || request.Namespace == "" ||
		strings.ContainsAny(request.Namespace, "{} \t\r\n") || len(request.Value) == 0 || request.TTL < 0 ||
		(request.ExpectedMissing && len(request.Expected) != 0) {
		return "", errors.New("alarmd ownership: invalid fenced CAS request")
	}
	requireAssignment := request.Fence.QueryGroup != ControlLeaderIdentity
	result, err := fencedCASScript.Run(ctx, store.client, []string{
		store.assignmentKey(request.Fence.QueryGroup), store.ownershipKey(request.Fence.QueryGroup),
		store.controlKey(request.Fence.QueryGroup, request.Namespace),
	}, boolText(requireAssignment), request.Fence.OwnerID, request.Fence.OwnerEpoch, request.Fence.LeaseToken,
		request.At.UnixMilli(), boolText(request.ExpectedMissing), request.Expected, request.Value, request.TTL.Milliseconds()).Text()
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
	default:
		return "", errors.New("alarmd ownership: invalid fenced CAS response")
	}
}

func (store *RedisStore) workerRegistryKey() string {
	return store.prefix + ":worker-registry"
}

func (store *RedisStore) workerKey(workerID string) string {
	digest := sha256.Sum256([]byte(workerID))
	return store.prefix + ":worker:" + hex.EncodeToString(digest[:])
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

var acquireScript = redis.NewScript(`
local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local now_ms = tonumber(ARGV[3])
local deadline_ms = tonumber(ARGV[4])
local token = ARGV[5]
if require_assignment == '1' then
  local desired = redis.call('HGET', KEYS[1], 'desired_worker_id')
  if not desired or desired ~= owner_id then return {'NOT_DESIRED', 0, 0} end
end
local disposition = redis.call('HGET', KEYS[2], 'execution_disposition')
if disposition and disposition ~= 'ACTIVE' then return {'PAUSED', 0, 0} end
local current_owner = redis.call('HGET', KEYS[2], 'owner_id')
local current_deadline = tonumber(redis.call('HGET', KEYS[2], 'deadline_ms') or '0')
if current_owner and current_owner ~= '' and current_deadline > now_ms then return {'BUSY', 0, current_deadline} end
local epoch = tonumber(redis.call('HGET', KEYS[2], 'owner_epoch') or '0') + 1
redis.call('HSET', KEYS[2], 'owner_id', owner_id, 'owner_epoch', epoch, 'lease_token', token,
  'deadline_ms', deadline_ms, 'execution_disposition', 'ACTIVE')
return {'OWNED', epoch, deadline_ms}
`)

var renewScript = redis.NewScript(`
local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local now_ms = tonumber(ARGV[5])
local deadline_ms = tonumber(ARGV[6])
if require_assignment == '1' then
  local desired = redis.call('HGET', KEYS[1], 'desired_worker_id')
  if not desired or desired ~= owner_id then return 'NOT_DESIRED' end
end
if redis.call('HGET', KEYS[2], 'execution_disposition') ~= 'ACTIVE' then return 'STALE' end
if redis.call('HGET', KEYS[2], 'owner_id') ~= owner_id or
   redis.call('HGET', KEYS[2], 'owner_epoch') ~= epoch or
   redis.call('HGET', KEYS[2], 'lease_token') ~= token or
   tonumber(redis.call('HGET', KEYS[2], 'deadline_ms') or '0') <= now_ms then return 'STALE' end
redis.call('HSET', KEYS[2], 'deadline_ms', deadline_ms)
return 'RENEWED'
`)

var checkFenceScript = redis.NewScript(`
local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local now_ms = tonumber(ARGV[5])
if require_assignment == '1' then
  local desired = redis.call('HGET', KEYS[1], 'desired_worker_id')
  if not desired or desired ~= owner_id then return 'NOT_DESIRED' end
end
if redis.call('HGET', KEYS[2], 'execution_disposition') ~= 'ACTIVE' then return 'STALE' end
if redis.call('HGET', KEYS[2], 'owner_id') ~= owner_id or
   redis.call('HGET', KEYS[2], 'owner_epoch') ~= epoch or
   redis.call('HGET', KEYS[2], 'lease_token') ~= token or
   tonumber(redis.call('HGET', KEYS[2], 'deadline_ms') or '0') <= now_ms then return 'STALE' end
return 'VALID'
`)

var releaseScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'owner_id') ~= ARGV[1] or
   redis.call('HGET', KEYS[1], 'owner_epoch') ~= ARGV[2] or
   redis.call('HGET', KEYS[1], 'lease_token') ~= ARGV[3] then return 'STALE' end
redis.call('HDEL', KEYS[1], 'owner_id', 'lease_token', 'deadline_ms')
return 'RELEASED'
`)

var publishAssignmentScript = redis.NewScript(`
local leader_id = ARGV[1]
local leader_epoch = ARGV[2]
local leader_token = ARGV[3]
local now_ms = tonumber(ARGV[4])
if redis.call('HGET', KEYS[1], 'execution_disposition') ~= 'ACTIVE' or
   redis.call('HGET', KEYS[1], 'owner_id') ~= leader_id or
   redis.call('HGET', KEYS[1], 'owner_epoch') ~= leader_epoch or
   redis.call('HGET', KEYS[1], 'lease_token') ~= leader_token or
   tonumber(redis.call('HGET', KEYS[1], 'deadline_ms') or '0') <= now_ms then
  return {'STALE', 0, 0, 0, '', 0, ''}
end
local expected_revision = tonumber(ARGV[5])
local current_revision = tonumber(redis.call('HGET', KEYS[2], 'record_revision') or '0')
if current_revision ~= expected_revision then
  return {'CONFLICT', 0, current_revision, 0, '', 0, ''}
end
local query_group = ARGV[6]
local desired = ARGV[7]
local reason = ARGV[8]
local assigned_at = ARGV[9]
local current_desired = redis.call('HGET', KEYS[2], 'desired_worker_id')
if current_desired and current_desired == desired then
  return {current_desired, redis.call('HGET', KEYS[2], 'assignment_generation'),
    redis.call('HGET', KEYS[2], 'record_revision'), redis.call('HGET', KEYS[2], 'control_epoch'),
    redis.call('HGET', KEYS[2], 'placement_reason'), redis.call('HGET', KEYS[2], 'assigned_at_ms'), query_group}
end
local generation = tonumber(redis.call('HGET', KEYS[2], 'assignment_generation') or '0') + 1
local revision = current_revision + 1
redis.call('HSET', KEYS[2], 'query_group', query_group, 'desired_worker_id', desired,
  'assignment_generation', generation, 'record_revision', revision, 'control_epoch', leader_epoch,
  'placement_reason', reason, 'assigned_at_ms', assigned_at)
return {desired, generation, revision, leader_epoch, reason, assigned_at, query_group}
`)

var fencedCASScript = redis.NewScript(`
local require_assignment = ARGV[1]
local owner_id = ARGV[2]
local epoch = ARGV[3]
local token = ARGV[4]
local now_ms = tonumber(ARGV[5])
if require_assignment == '1' then
  local desired = redis.call('HGET', KEYS[1], 'desired_worker_id')
  if not desired or desired ~= owner_id then return 'STALE_OWNER' end
end
if redis.call('HGET', KEYS[2], 'execution_disposition') ~= 'ACTIVE' or
   redis.call('HGET', KEYS[2], 'owner_id') ~= owner_id or
   redis.call('HGET', KEYS[2], 'owner_epoch') ~= epoch or
   redis.call('HGET', KEYS[2], 'lease_token') ~= token or
   tonumber(redis.call('HGET', KEYS[2], 'deadline_ms') or '0') <= now_ms then return 'STALE_OWNER' end
local current = redis.call('GET', KEYS[3])
if ARGV[6] == '1' then
  if current then return 'CONFLICT' end
elseif not current or current ~= ARGV[7] then
  return 'CONFLICT'
end
local ttl_ms = tonumber(ARGV[9])
if ttl_ms > 0 then redis.call('SET', KEYS[3], ARGV[8], 'PX', ttl_ms)
else redis.call('SET', KEYS[3], ARGV[8]) end
return 'APPLIED'
`)
