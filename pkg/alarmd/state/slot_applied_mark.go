// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// SlotAppliedBackend records and reads which Plans of one Slot had their state
// written by an attempt that then failed to write down that it had.
//
// It is its own capability for the reason the others are: a backend without it
// must say so rather than have every write report the store as down. Nothing
// here is load-bearing for a Slot -- a deployment whose backend cannot do it
// keeps exactly today's behaviour, which is to record such a Slot as a gap.
type SlotAppliedBackend interface {
	Backend
	// AddSlotApplied adds members to a set and sets the whole key's lifetime.
	// The two together, because a set written without one outlives the Slot it
	// describes and is then read by a completion it has nothing to do with.
	AddSlotApplied(ctx context.Context, key string, members []string, ttl time.Duration) error
	// ReadSlotApplied returns the members, empty for a key that is not there.
	ReadSlotApplied(ctx context.Context, key string) ([]string, error)
}

// SlotAppliedMarkStore writes and reads the one fact a query-free completion
// cannot otherwise have: whether an earlier attempt at this Slot got as far as
// writing state.
//
// The mark is written on the failure path only. An attempt that finishes writes
// its Progress, and the Progress is then the fact; an attempt that wrote state
// and could not write Progress is the one case where the fact exists nowhere.
// So the steady state costs nothing, and the cost appears exactly when
// something has gone wrong -- which is also when there is spare write budget,
// because the thing that failed was a write.
type SlotAppliedMarkStore struct {
	prefix string
	router StorageRouter
}

func NewSlotAppliedMarkStore(prefix string, router StorageRouter) (*SlotAppliedMarkStore, error) {
	if prefix == "" || router == nil {
		return nil, fmt.Errorf("state: slot-applied mark store needs a prefix and a router")
	}
	if err := probeBackendCapabilities("slot-applied mark store", router, slotAppliedMarkCapabilities); err != nil {
		return nil, err
	}
	return &SlotAppliedMarkStore{prefix: prefix, router: router}, nil
}

// SlotAppliedKey names the mark of one Slot.
//
// One key per Slot rather than per Plan: the reader wants "which of this Slot's
// Plans got there", which is one question and should cost one round trip, and
// the writer is on a path that has already failed once and should not turn one
// failure into a Plan-sized batch of writes.
func SlotAppliedKey(prefix string, slot execution.SlotIdentity) (string, error) {
	if prefix == "" || slot.QueryGroup == "" || slot.EvaluationTime <= 0 {
		return "", identityError("valid slot-applied mark identity is required")
	}
	return prefix + ":slot-applied:v1:" + string(slot.QueryGroup) + ":" +
		strconv.FormatInt(int64(slot.EvaluationTime), 10), nil
}

// SlotAppliedMember is how one Plan is named inside the set.
//
// The identity spelled out rather than digested. The set is read back and
// intersected with the frozen due Plans, so both sides have to derive the same
// text from the same three fields; a digest would add a derivation that has to
// agree across two call sites and says nothing more.
func SlotAppliedMember(plan execution.PlanIdentity) string {
	return plan.TenantID + "|" + plan.BusinessID + "|" + plan.StrategyID
}

// routeFor picks the target a Slot's mark lives on.
//
// The first due Plan in canonical order, so the writer and the reader land on
// the same place without either of them storing where it was: the due set is
// frozen for the Slot, and both sides sort it the same way.
func (store *SlotAppliedMarkStore) routeFor(plans []execution.PlanIdentity) (StorageTarget, error) {
	if len(plans) == 0 {
		return StorageTarget{}, fmt.Errorf("state: a slot-applied mark needs at least one Plan to route by")
	}
	sorted := append([]execution.PlanIdentity(nil), plans...)
	sort.Slice(sorted, func(left, right int) bool {
		return SlotAppliedMember(sorted[left]) < SlotAppliedMember(sorted[right])
	})
	return store.router.Route(sorted[0].TenantID, sorted[0].StrategyID)
}

// Record writes that these Plans of this Slot had their state applied, with a
// lifetime ending at the Slot's keep-until.
//
// The reader is the query-free finalization, and the rule that decides it
// (ResolveFinalization) sends a Slot down that path once the replay window has
// closed: at recovery-until, or later if the owner that should have run it
// was being taken over or drained. The Slot's keep-until is that latest
// moment as the scheduler derives it -- recovery-until plus the deployment's
// own post-recovery terminal delay -- and after it nothing about the Slot is
// read, so keeping the mark longer would be keeping a key nobody asks about.
//
// The lifetime is derived from the Slot rather than configured: a second knob
// would let the two drift, and the only right answer is the one the
// finalization rule already uses. It used to end at recovery-until, which is
// where the reader begins, not where it ends: a mark written at any point
// before the boundary was gone by the time the finalization arrived, and a
// Slot that had evaluated and alerted was recorded as a gap.
func (store *SlotAppliedMarkStore) Record(
	ctx context.Context, slot execution.SlotIdentity, duePlans []execution.PlanIdentity,
	applied []execution.PlanIdentity, keepUntil time.Time, now time.Time,
) error {
	if len(applied) == 0 {
		return nil
	}
	key, err := SlotAppliedKey(store.prefix, slot)
	if err != nil {
		return err
	}
	ttl := keepUntil.Sub(now)
	if ttl <= 0 {
		// The Slot can no longer be finalized, so nothing will ever read this.
		return nil
	}
	target, err := store.routeFor(duePlans)
	if err != nil {
		return err
	}
	backend, ok := target.Backend.(SlotAppliedBackend)
	if !ok {
		return ErrSlotAppliedUnsupported
	}
	members := make([]string, 0, len(applied))
	for _, plan := range applied {
		members = append(members, SlotAppliedMember(plan))
	}
	sort.Strings(members)
	return backend.AddSlotApplied(ctx, key, members, ttl)
}

// Read answers how many of this Slot's due Plans an earlier attempt applied.
//
// Members that are not in the due set are ignored rather than counted. The
// frozen due set is this Slot's source of truth for what it was going to
// evaluate; a mark naming something outside it was written against a different
// view of the Slot and cannot make this one more complete than it is.
func (store *SlotAppliedMarkStore) Read(
	ctx context.Context, slot execution.SlotIdentity, duePlans []execution.PlanIdentity,
) (execution.ExecutionEvidence, error) {
	evidence := execution.ExecutionEvidence{
		Kind: execution.EvidenceNoneFound, PlansTotal: len(duePlans),
	}
	key, err := SlotAppliedKey(store.prefix, slot)
	if err != nil {
		return evidence, err
	}
	target, err := store.routeFor(duePlans)
	if err != nil {
		return evidence, err
	}
	backend, ok := target.Backend.(SlotAppliedBackend)
	if !ok {
		return evidence, ErrSlotAppliedUnsupported
	}
	members, err := backend.ReadSlotApplied(ctx, key)
	if err != nil {
		return evidence, err
	}
	if len(members) == 0 {
		return evidence, nil
	}
	marked := make(map[string]struct{}, len(members))
	for _, member := range members {
		marked[member] = struct{}{}
	}
	applied := 0
	for _, plan := range duePlans {
		if _, found := marked[SlotAppliedMember(plan)]; found {
			applied++
		}
	}
	if applied == 0 {
		return evidence, nil
	}
	evidence.Kind, evidence.PlansApplied = execution.EvidenceStateApplied, applied
	return evidence, nil
}

// AddSlotApplied and ReadSlotApplied on the Redis backend.
//
// The add and the lifetime go in one pipeline rather than two round trips: a
// set written whose PEXPIRE then failed is a key with no lifetime on a store
// where nothing else will ever touch it again, which is the one outcome worth
// spending a pipeline to avoid.
func (backend *RedisBackend) AddSlotApplied(
	ctx context.Context, key string, members []string, ttl time.Duration,
) error {
	if backend == nil || backend.client == nil {
		return fmt.Errorf("state: redis backend is not configured")
	}
	if len(members) == 0 || ttl <= 0 {
		return nil
	}
	values := make([]interface{}, 0, len(members))
	for _, member := range members {
		values = append(values, member)
	}
	_, err := backend.client.Pipelined(ctx, func(pipeline redis.Pipeliner) error {
		pipeline.SAdd(ctx, key, values...)
		pipeline.PExpire(ctx, key, ttl)
		return nil
	})
	return err
}

func (backend *RedisBackend) ReadSlotApplied(ctx context.Context, key string) ([]string, error) {
	if backend == nil || backend.client == nil {
		return nil, fmt.Errorf("state: redis backend is not configured")
	}
	members, err := backend.client.SMembers(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return members, err
}

var _ SlotAppliedBackend = (*RedisBackend)(nil)

// setClient is the part of the Redis client this file needs.
type setClient interface {
	SAdd(context.Context, string, ...interface{}) *redis.IntCmd
	SMembers(context.Context, string) *redis.StringSliceCmd
	PExpire(context.Context, string, time.Duration) *redis.BoolCmd
}
