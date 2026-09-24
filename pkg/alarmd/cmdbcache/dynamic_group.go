// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// The dynamic group cache is the fork's own: one String per group under
// "<redis_key_prefix>dynamic_group:<id>", JSON, written by the dynamic group
// module on its own cadence with a seven-day expiry. alarmd reads it from
// configured target group connection (legacy prefix-only configurations use
// the host cache connection), and only for the groups active Plans reference:
// tens to hundreds of GETs a minute, never a scan of the inverse hash that lists every
// instance of a model.

// GroupClient is the one command the reader issues: MGET over the
// referenced ids, one round trip. Narrow so a test can stand in for Redis
// and fail the transport on purpose.
type GroupClient interface {
	MGet(ctx context.Context, keys ...string) *redis.SliceCmd
}

// GroupReader reads group documents by id.
type GroupReader struct {
	client GroupClient
	prefix string
}

// NewGroupReader builds a reader over the fork's group cache. The prefix is
// the fork's own key prefix, spelled exactly as the writer spells it (it
// carries its own separator, ":" by default); it is a deployment coordinate
// rendered from the same source as the writer's, never derived here.
func NewGroupReader(client GroupClient, prefix string) (*GroupReader, error) {
	if client == nil {
		return nil, errors.New("alarmd cmdbcache: a redis client is required")
	}
	if strings.TrimSpace(prefix) == "" {
		return nil, errors.New("alarmd cmdbcache: the dynamic group key prefix is required")
	}
	return &GroupReader{client: client, prefix: prefix}, nil
}

func (reader *GroupReader) key(id string) string {
	return reader.prefix + "dynamic_group:" + id
}

// GroupRead is one id's raw read: the payload, or that the key was absent.
type GroupRead struct {
	Payload []byte
	Missing bool
}

// Read fetches every id in one MGET. The error is the transport's - the
// round trip failed - and a caller keeps what it held; a missing key is not
// an error, it is an answer.
func (reader *GroupReader) Read(ctx context.Context, ids []string) (map[string]GroupRead, error) {
	reads := make(map[string]GroupRead, len(ids))
	if len(ids) == 0 {
		return reads, nil
	}
	keys := make([]string, len(ids))
	for index, id := range ids {
		keys[index] = reader.key(id)
	}
	values, err := reader.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("alarmd cmdbcache: read dynamic groups: %w", err)
	}
	if len(values) != len(ids) {
		return nil, fmt.Errorf("alarmd cmdbcache: read dynamic groups: %d values for %d keys", len(values), len(ids))
	}
	for index, id := range ids {
		switch value := values[index].(type) {
		case nil:
			reads[id] = GroupRead{Missing: true}
		case string:
			reads[id] = GroupRead{Payload: []byte(value)}
		case []byte:
			reads[id] = GroupRead{Payload: value}
		default:
			return nil, fmt.Errorf("alarmd cmdbcache: read dynamic group %s: unexpected value %T", id, value)
		}
	}
	return reads, nil
}

// GroupMember is one member as the cache carries it, after the checks that
// do not depend on a plan: it names the group's model and an instance, and
// the group's summary lists it.
type GroupMember struct {
	ModelID     string
	ModelInstID string
	// HostID is the host member's bk_host_id as text; empty when the writer
	// did not put one on the member, which drops it under the host rule.
	HostID string
}

// GroupSnapshot is one group as last read: unavailable by name, or a model
// with its members. It is immutable once published; per-plan key sets are
// memoised on it, so a large group is walked once per plan identity per
// snapshot rather than once per Slot.
type GroupSnapshot struct {
	ID string
	// ReadAt is when the read that produced this snapshot succeeded.
	ReadAt time.Time
	// Unavailable is the closed reason the group cannot be used at all:
	// key_missing, json_invalid, structure_invalid. Empty when it can.
	Unavailable string
	ModelID     string
	Members     []GroupMember
	// Dropped counts the members refused by the plan-independent checks.
	Dropped int

	mu   sync.Mutex
	keys map[string]*groupKeys
}

type groupKeys struct {
	members map[string]struct{}
	dropped int
}

// Keys returns the group's member keys under a plan's identity and rule,
// and how many members the rule dropped on top of the plan-independent
// drops. The set is shared; callers read it.
func (snapshot *GroupSnapshot) Keys(plan *contract.TargetPlanV1) (map[string]struct{}, int) {
	if snapshot == nil || plan == nil {
		return nil, 0
	}
	signature := string(plan.Rule) + "\x00" + plan.ModelID + "\x00" + strings.Join(plan.Identity.Dimensions, ",") + "\x00" + plan.Identity.ModelDimension + "\x00" + plan.Identity.ModelValue
	if plan.Identity.HostIdentity {
		signature += "\x00host"
	}
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if cached, found := snapshot.keys[signature]; found {
		return cached.members, cached.dropped
	}
	built := &groupKeys{members: make(map[string]struct{}, len(snapshot.Members))}
	for _, member := range snapshot.Members {
		if member.ModelID != plan.ModelID {
			built.dropped++
			continue
		}
		switch {
		case plan.Rule == contract.TargetPlanRuleHostID || plan.Identity.HostIdentity:
			// Held under the host id: the host_id rule's key, and the key of
			// a model_inst_id plan read by host identity. A member the
			// writer put no host id on cannot be placed and is dropped.
			if member.HostID == "" {
				built.dropped++
				continue
			}
			built.members[plan.Identity.HostKey(member.HostID)] = struct{}{}
		default:
			built.members[plan.Identity.MemberKey(member.ModelID, member.ModelInstID)] = struct{}{}
		}
	}
	if snapshot.keys == nil {
		snapshot.keys = make(map[string]*groupKeys, 2)
	}
	snapshot.keys[signature] = built
	return built.members, built.dropped
}

// decodeGroup reads one group document per the protocol: an object with
// model_id, member_list and the model_inst_ids summary. A member is kept
// when it names the group's model, names an instance, and the summary lists
// it; the rest are dropped and counted. member_list absent is a structure
// the reader does not accept - it cannot tell "empty" from "not written" -
// and member_list present and empty is the writer saying empty.
func decodeGroup(id string, payload []byte, readAt time.Time) *GroupSnapshot {
	snapshot := &GroupSnapshot{ID: id, ReadAt: readAt}
	var document struct {
		ModelID      string            `json:"model_id"`
		ModelInstIDs []json.RawMessage `json:"model_inst_ids"`
		MemberList   *[]struct {
			ModelID     string          `json:"model_id"`
			ModelInstID json.RawMessage `json:"model_inst_id"`
			HostID      json.RawMessage `json:"bk_host_id"`
		} `json:"member_list"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		snapshot.Unavailable = targetplan.ReasonJSONInvalid
		return snapshot
	}
	if strings.TrimSpace(document.ModelID) == "" || document.MemberList == nil {
		snapshot.Unavailable = targetplan.ReasonStructureInvalid
		return snapshot
	}
	snapshot.ModelID = document.ModelID
	var listed map[string]struct{}
	if document.ModelInstIDs != nil {
		listed = make(map[string]struct{}, len(document.ModelInstIDs))
		for _, raw := range document.ModelInstIDs {
			if text := rawScalarText(raw); text != "" {
				listed[text] = struct{}{}
			}
		}
	}
	for _, wire := range *document.MemberList {
		instance := rawScalarText(wire.ModelInstID)
		if wire.ModelID != document.ModelID || instance == "" {
			snapshot.Dropped++
			continue
		}
		if listed != nil {
			if _, found := listed[instance]; !found {
				snapshot.Dropped++
				continue
			}
		}
		host := rawScalarText(wire.HostID)
		if host == "0" {
			host = ""
		}
		snapshot.Members = append(snapshot.Members, GroupMember{ModelID: wire.ModelID, ModelInstID: instance, HostID: host})
	}
	return snapshot
}

// GroupLookup is what the store hands a resolver for one id: the snapshot,
// how old it is, and whether a refresh after it failed at the transport,
// which is the one condition under which an old snapshot is served on
// purpose and has to be said.
type GroupLookup struct {
	Snapshot      *GroupSnapshot
	Age           time.Duration
	RefreshFailed bool
	// ReadErr is set when the id was not held and the synchronous first
	// read failed; the snapshot is then nil.
	ReadErr error
}

// GroupStore holds the referenced groups' snapshots and refreshes them on
// the host index's cadence. An id is referenced the first time a resolver
// asks for it; that first ask reads it synchronously, one GET on the
// connection every Slot already writes State and Progress to, so a Plan's
// first Slot after a restart does not run a minute with its group members
// outside the target.
type GroupStore struct {
	reader   *GroupReader
	interval time.Duration
	maxAge   time.Duration
	now      func() time.Time

	mu        sync.RWMutex
	snapshots map[string]*GroupSnapshot
	// referenced is, per id, when it was last asked for and the longest
	// evaluation interval among the Plans that asked. A reference ages out
	// when nobody has asked for it within max(the staleness bound, twice
	// that longest interval): a Plan asks every Slot, so an id nobody asks
	// for that long is one no active Plan references any more, and keeping
	// it would read it every refresh and count it in the health as a
	// standing failure once the writer withdraws it. The bound keeps every
	// Plan on a period up to the staleness bound at zero Slot-path reads,
	// and the registered interval keeps the longer ones there too; no
	// horizon runs close to a Slot's own cadence.
	referenced    map[string]groupReference
	lastFailureAt time.Time
	syncReads     uint64
	lastError     error
	failures      uint64
	refreshes     uint64
}

// GroupStoreOptions mirror StoreOptions: the same cadence and the same
// staleness bound as the host index, on purpose.
type GroupStoreOptions struct {
	RefreshInterval time.Duration
	MaxAge          time.Duration
	Now             func() time.Time
}

func NewGroupStore(reader *GroupReader, options GroupStoreOptions) (*GroupStore, error) {
	if reader == nil {
		return nil, errors.New("alarmd cmdbcache: a group reader is required")
	}
	if options.RefreshInterval <= 0 {
		return nil, errors.New("alarmd cmdbcache: a positive refresh interval is required")
	}
	if options.MaxAge <= options.RefreshInterval {
		return nil, errors.New("alarmd cmdbcache: the staleness bound must exceed the refresh interval")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &GroupStore{reader: reader, interval: options.RefreshInterval, maxAge: options.MaxAge, now: now,
		snapshots: make(map[string]*GroupSnapshot), referenced: make(map[string]groupReference)}, nil
}

// MaxAge is the bound past which a held snapshot is not served.
func (store *GroupStore) MaxAge() time.Duration {
	if store == nil {
		return 0
	}
	return store.maxAge
}

// groupReference is one id's registration: when it was last asked for and
// the longest evaluation interval among the Plans that asked.
type groupReference struct {
	askedAt  time.Time
	interval time.Duration
}

// Group answers one id for a Plan evaluated every interval, reading it
// first when it is not held.
func (store *GroupStore) Group(ctx context.Context, id string, interval time.Duration) GroupLookup {
	if store == nil {
		return GroupLookup{ReadErr: errors.New("alarmd cmdbcache: no group store")}
	}
	now := store.now()
	store.mu.Lock()
	reference := store.referenced[id]
	reference.askedAt = now
	if interval > reference.interval {
		reference.interval = interval
	}
	store.referenced[id] = reference
	snapshot, held := store.snapshots[id]
	failedAt := store.lastFailureAt
	if !held {
		store.syncReads++
	}
	store.mu.Unlock()
	if !held {
		reads, err := store.reader.Read(ctx, []string{id})
		if err != nil {
			return GroupLookup{ReadErr: err}
		}
		snapshot = store.publish(id, reads[id], now)
		failedAt = time.Time{}
	}
	return GroupLookup{Snapshot: snapshot, Age: now.Sub(snapshot.ReadAt), RefreshFailed: failedAt.After(snapshot.ReadAt)}
}

func (store *GroupStore) publish(id string, read GroupRead, at time.Time) *GroupSnapshot {
	var snapshot *GroupSnapshot
	if read.Missing {
		snapshot = &GroupSnapshot{ID: id, ReadAt: at, Unavailable: targetplan.ReasonKeyMissing}
	} else {
		snapshot = decodeGroup(id, read.Payload, at)
	}
	store.mu.Lock()
	store.snapshots[id] = snapshot
	store.mu.Unlock()
	return snapshot
}

// Refresh re-reads every referenced group. A transport failure keeps every
// snapshot and is recorded, so the next lookups say they are served past a
// failed refresh; a missing key replaces the snapshot with an unavailable
// one - the writer withdrew or never wrote it, and that is an answer. A
// reference nobody has asked for within its horizon is forgotten first,
// snapshot and all.
func (store *GroupStore) Refresh(ctx context.Context) error {
	now := store.now()
	store.mu.Lock()
	ids := make([]string, 0, len(store.referenced))
	for id, reference := range store.referenced {
		horizon := store.maxAge
		if 2*reference.interval > horizon {
			horizon = 2 * reference.interval
		}
		if now.Sub(reference.askedAt) > horizon {
			delete(store.referenced, id)
			delete(store.snapshots, id)
			continue
		}
		ids = append(ids, id)
	}
	store.mu.Unlock()
	sort.Strings(ids)
	reads, err := store.reader.Read(ctx, ids)
	if err != nil {
		store.mu.Lock()
		store.lastFailureAt, store.lastError = store.now(), err
		store.failures++
		store.mu.Unlock()
		return err
	}
	at := store.now()
	for _, id := range ids {
		store.publish(id, reads[id], at)
	}
	store.mu.Lock()
	store.lastError = nil
	store.refreshes++
	store.mu.Unlock()
	return nil
}

// Run keeps the referenced groups fresh until the context ends.
func (store *GroupStore) Run(ctx context.Context) {
	if store == nil {
		return
	}
	ticker := time.NewTicker(store.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = store.Refresh(ctx)
		}
	}
}

// GroupHealth is what the store is currently serving.
type GroupHealth struct {
	Referenced  int
	Loaded      int
	Unavailable int
	// RefreshFailed says the last refresh failed at the transport; the
	// snapshots served are older than the cadence promises.
	RefreshFailed     bool
	ConsecutiveErrors uint64
	Refreshes         uint64
	// SyncReads counts the reads made on a Slot path for an id not held:
	// one per first reference, and none after, is the reading.
	SyncReads uint64
}

func (store *GroupStore) Health() GroupHealth {
	if store == nil {
		return GroupHealth{}
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	health := GroupHealth{Referenced: len(store.referenced), RefreshFailed: store.lastError != nil,
		ConsecutiveErrors: store.failures, Refreshes: store.refreshes, SyncReads: store.syncReads}
	for _, snapshot := range store.snapshots {
		if snapshot.Unavailable != "" {
			health.Unavailable++
			continue
		}
		health.Loaded++
	}
	return health
}
