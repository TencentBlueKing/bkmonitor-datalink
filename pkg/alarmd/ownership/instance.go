// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package ownership

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisatomic"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// InstanceRegistration describes a running process's roles and internal
// endpoint. It is separate from WorkerRegistration: control and channel
// instances neither become execution candidates nor enlarge worker counts.
type InstanceRegistration struct {
	InstanceID    string                `json:"instance_id"`
	Roles         roles.Set             `json:"roles"`
	Endpoint      string                `json:"endpoint,omitempty"`
	Incarnation   string                `json:"incarnation"`
	InternalToken string                `json:"internal_token,omitempty"`
	ExpiresAt     time.Time             `json:"expires_at"`
	RoleStatus    map[roles.Role]string `json:"role_status,omitempty"`
	// ControlCandidacy is the class this process stands for the Control
	// Leader lease in: ControlCandidacyPreferred for a process with the
	// control role, ControlCandidacyFallback for a Worker without it that
	// can still run the core control rounds when nobody preferred is there.
	// Empty means the process is no candidate, and it is also what every
	// record written before candidacy existed carries: such a record is read
	// by its roles, as it always was.
	ControlCandidacy string `json:"control_candidacy,omitempty"`
	// ControlCandidacyReady says the process has finished starting and is
	// not stopping, so it may take the lease. A candidate that is not ready
	// does not contend, and a preferred one that is not ready does not hold
	// a fallback off.
	ControlCandidacyReady bool `json:"control_candidacy_ready,omitempty"`
	// ControlCandidacyReadySince is when this incarnation's record first
	// said ready, kept while it stays ready and dropped the first time it
	// does not. A candidate is stable once it has been ready for a whole
	// candidacy window (CandidacyStable). A process that keeps restarting
	// never is: every incarnation starts again from its own first ready
	// write. Absent in records from before it existed, which read as not
	// stable.
	ControlCandidacyReadySince *time.Time `json:"control_candidacy_ready_since,omitempty"`
}

// The two candidacy classes. Derived from the roles, never configured: the
// control role makes a process preferred, and a Worker without it is the
// fallback that keeps the deployment led when no preferred process is.
const (
	ControlCandidacyPreferred = "preferred"
	ControlCandidacyFallback  = "fallback"
)

func (instance InstanceRegistration) Validate() error {
	if instance.InstanceID == "" || instance.Incarnation == "" || instance.ExpiresAt.IsZero() || len(instance.Roles) == 0 {
		return errors.New("alarmd ownership: incomplete instance registration")
	}
	if _, err := roles.Resolve(instance.Roles); err != nil {
		return fmt.Errorf("alarmd ownership: instance roles: %w", err)
	}
	for role, status := range instance.RoleStatus {
		if !instance.Roles.Has(role) || status == "" {
			return errors.New("alarmd ownership: status requires a declared role and nonempty value")
		}
	}
	if instance.ControlCandidacyReadySince != nil && !instance.ControlCandidacyReady {
		return errors.New("alarmd ownership: a candidacy ready-since time requires a ready candidacy")
	}
	switch instance.ControlCandidacy {
	case "":
		if instance.ControlCandidacyReady {
			return errors.New("alarmd ownership: control candidacy readiness requires a candidacy class")
		}
	case ControlCandidacyPreferred:
		if !instance.Roles.Has(roles.Control) {
			return errors.New("alarmd ownership: a preferred control candidate declares the control role")
		}
	case ControlCandidacyFallback:
		if instance.Roles.Has(roles.Control) || !instance.Roles.Has(roles.Worker) {
			return errors.New("alarmd ownership: a fallback control candidate is a Worker without the control role")
		}
	default:
		return fmt.Errorf("alarmd ownership: unknown control candidacy %q", instance.ControlCandidacy)
	}
	return nil
}

// ControlCapable says the process this record describes can be the Control
// Leader: it declares the control role, or it stands as a fallback
// candidate. A reader that finds the lease held by a process uses this,
// not the role alone, to accept that process as the Leader it talks to.
func (instance InstanceRegistration) ControlCapable() bool {
	return instance.Roles.Has(roles.Control) || instance.ControlCandidacy == ControlCandidacyFallback
}

// CandidacyStable says the record is live at the instant, says ready, and
// has said so since at least window before it. A record without a ready-since
// time is not stable, whatever else it says.
func (instance InstanceRegistration) CandidacyStable(at time.Time, window time.Duration) bool {
	return instance.ControlCandidacyReady && instance.ExpiresAt.After(at) && instance.ControlCandidacyReadySince != nil &&
		!at.Before(instance.ControlCandidacyReadySince.Add(window))
}

// CandidacyClass is the class the record stands in, reading a record from
// before candidacy existed by its roles: a control process was always the
// only kind that took the lease, so it reads as preferred.
func (instance InstanceRegistration) CandidacyClass() string {
	if instance.ControlCandidacy == "" && instance.Roles.Has(roles.Control) {
		return ControlCandidacyPreferred
	}
	return instance.ControlCandidacy
}

func (store *RedisStore) instanceKey(instanceID string) string {
	digest := sha256.Sum256([]byte(instanceID))
	return store.prefix + ":instance:" + hex.EncodeToString(digest[:])
}

// RegisterInstance renews only the process's independent registration. The
// Worker registry, QG identities and fencing leases keep their existing keys.
func (store *RedisStore) RegisterInstance(ctx context.Context, instance InstanceRegistration) error {
	if store == nil || store.client == nil {
		return errors.New("alarmd ownership: initialized instance store is required")
	}
	if err := instance.Validate(); err != nil {
		return err
	}
	ttl := time.Until(instance.ExpiresAt)
	if ttl <= 0 {
		return errors.New("alarmd ownership: instance registration is already expired")
	}
	payload, err := json.Marshal(instance)
	if err != nil {
		return fmt.Errorf("alarmd ownership: encode instance registration: %w", err)
	}
	index := store.candidatesKey(instance.ControlCandidacy)
	if index == "" {
		return store.client.Set(ctx, store.instanceKey(instance.InstanceID), payload, ttl).Err()
	}
	// A candidate is also named in its class's index, in the same round
	// trip. The score is the record's own expiry, so the index never says a
	// record is live longer than the record is.
	//
	// A preferred record and its entry are one atomic write: a record written
	// without its entry would be a live preferred process a fallback cannot
	// see and takes the lease in front of. A fallback's are only pipelined:
	// a fallback missing from its index makes a preferred process that is
	// not yet stable contend at once, which is the behaviour without
	// fallbacks and the safe side, and a Worker's renewal then costs one
	// command more rather than three.
	if instance.ControlCandidacy == ControlCandidacyPreferred {
		// Each index is pruned on read, and in the steady state nothing reads
		// it: a fallback lists only while the lease is free, a preferred
		// process only while it is not yet stable. A Worker or control pod
		// that rolls leaves its old name behind, so the preferred renewal,
		// a handful of processes every few seconds, also drops the expired
		// entries of both - two commands inside the one script it already
		// runs, none added to a Worker's renewal.
		at := strconv.FormatInt(instance.ExpiresAt.Add(-ttl).UnixMilli(), 10)
		return registerPreferredInstanceWrite.Run(ctx, store.client,
			[]string{store.instanceKey(instance.InstanceID), store.preferredCandidatesKey(), store.fallbackCandidatesKey()},
			payload, registrationTTLMillis(ttl), instance.ExpiresAt.UnixMilli(), instance.InstanceID, at)
	}
	_, err = store.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, store.instanceKey(instance.InstanceID), payload, ttl)
		pipe.ZAdd(ctx, index, &redis.Z{
			Score: float64(instance.ExpiresAt.UnixMilli()), Member: instance.InstanceID,
		})
		return nil
	})
	return err
}

// registerPreferredInstanceWrite stores a preferred candidate's record with
// its life, names it in the preferred index scored by its expiry, and drops
// the entries of both indexes that expired by the record's write time, as
// one script rather than a MULTI/EXEC (see redisatomic for why). The prune
// counts are not read, as they were not before. The record and the two
// indexes share no hash tag: like the activation scripts
// (controlplane/activation_rebuild.go) this runs on a standalone or Sentinel
// server, and the configuration refuses Cluster (config/redis.go).
var registerPreferredInstanceWrite = redisatomic.New(`
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('ZADD', KEYS[2], ARGV[3], ARGV[4])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', ARGV[5])
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', ARGV[5])
return 1
`)

// preferredCandidatesKey indexes the processes that stand as preferred
// Control Leader candidates, scored by their record's expiry. Only the
// preferred are indexed: the one question a fallback asks is whether any of
// them is there, and the index then stays as small as the control
// processes rather than growing with the Workers.
func (store *RedisStore) preferredCandidatesKey() string {
	return store.prefix + ":control-preferred-candidates"
}

// fallbackCandidatesKey indexes the Workers that stand as fallback
// candidates, the same way. It is read only by a preferred process that finds
// the lease free before it has itself been ready for a whole window, to ask
// whether a ready fallback is there to take the lease meanwhile.
func (store *RedisStore) fallbackCandidatesKey() string {
	return store.prefix + ":control-fallback-candidates"
}

func (store *RedisStore) candidatesKey(class string) string {
	switch class {
	case ControlCandidacyPreferred:
		return store.preferredCandidatesKey()
	case ControlCandidacyFallback:
		return store.fallbackCandidatesKey()
	}
	return ""
}

// ListPreferredControlCandidates reads every preferred candidate whose record
// is live at the given instant, ready or not; the caller judges readiness.
// One round trip prunes the index and reads it, a second reads the records.
//
// Every failure is an error rather than a short list. The caller is a
// fallback deciding whether it may take the lease, or a fallback Leader
// deciding whether to hand it back, and a truncated list reads to the first
// as "no preferred process is there" -- the one answer that lets it contend
// against a preferred process that is.
func (store *RedisStore) ListPreferredControlCandidates(ctx context.Context, at time.Time) ([]InstanceRegistration, error) {
	return store.listControlCandidates(ctx, at, ControlCandidacyPreferred)
}

// ListFallbackControlCandidates reads every fallback candidate whose record
// is live at the instant, ready or not, under the same rules: the caller is a
// preferred process deciding whether to wait for one, and a short list would
// read as "no fallback is there".
func (store *RedisStore) ListFallbackControlCandidates(ctx context.Context, at time.Time) ([]InstanceRegistration, error) {
	return store.listControlCandidates(ctx, at, ControlCandidacyFallback)
}

func (store *RedisStore) listControlCandidates(ctx context.Context, at time.Time, class string) ([]InstanceRegistration, error) {
	if store == nil || store.client == nil || at.IsZero() {
		return nil, errors.New("alarmd ownership: candidate listing needs a store and an instant")
	}
	key := store.candidatesKey(class)
	cutoff := strconv.FormatInt(at.UnixMilli(), 10)
	var listed *redis.StringSliceCmd
	if _, err := store.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.ZRemRangeByScore(ctx, key, "-inf", cutoff)
		listed = pipe.ZRangeByScore(ctx, key, &redis.ZRangeBy{Min: "(" + cutoff, Max: "+inf"})
		return nil
	}); err != nil {
		return nil, err
	}
	ids := listed.Val()
	if len(ids) == 0 {
		return nil, nil
	}
	replies := make([]*redis.StringCmd, len(ids))
	if _, err := store.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index, id := range ids {
			replies[index] = pipe.Get(ctx, store.instanceKey(id))
		}
		return nil
	}); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	candidates := make([]InstanceRegistration, 0, len(ids))
	for index, reply := range replies {
		payload, err := reply.Bytes()
		if errors.Is(err, redis.Nil) {
			// The record went before the index noticed: a process that
			// stopped registering. Its score lapses on its own.
			continue
		}
		if err != nil {
			return nil, err
		}
		var instance InstanceRegistration
		if err := json.Unmarshal(payload, &instance); err != nil {
			return nil, fmt.Errorf("alarmd ownership: decode instance registration: %w", err)
		}
		if err := instance.Validate(); err != nil {
			return nil, err
		}
		if instance.InstanceID != ids[index] || instance.CandidacyClass() != class || !instance.ExpiresAt.After(at) {
			continue
		}
		candidates = append(candidates, instance)
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].InstanceID < candidates[right].InstanceID })
	return candidates, nil
}

// ReadInstance distinguishes absence from an invalid stored record. Expiry is
// returned as a fact for the caller to judge, as it is in ReadWorker.
func (store *RedisStore) ReadInstance(ctx context.Context, instanceID string) (InstanceRegistration, bool, error) {
	if store == nil || store.client == nil || instanceID == "" {
		return InstanceRegistration{}, false, errors.New("alarmd ownership: instance read needs a store and an identity")
	}
	payload, err := store.client.Get(ctx, store.instanceKey(instanceID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return InstanceRegistration{}, false, nil
	}
	if err != nil {
		return InstanceRegistration{}, false, err
	}
	var instance InstanceRegistration
	if err := json.Unmarshal(payload, &instance); err != nil {
		return InstanceRegistration{}, false, fmt.Errorf("alarmd ownership: decode instance registration: %w", err)
	}
	if err := instance.Validate(); err != nil {
		return InstanceRegistration{}, false, err
	}
	if instance.InstanceID != instanceID {
		return InstanceRegistration{}, false, errors.New("alarmd ownership: stored instance identity differs from requested identity")
	}
	return instance, true, nil
}
