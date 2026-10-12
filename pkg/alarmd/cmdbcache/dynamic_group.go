// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisbatch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// The dynamic group cache is the fork's own: one String per group under
// "<redis_key_prefix>dynamic_group:<id>", JSON, written by the dynamic group
// module on its own cadence with a seven-day expiry. alarmd reads it from
// configured target group connection (legacy prefix-only configurations use
// the host cache connection), and only for the groups active Plans reference:
// tens to hundreds of STRLENs and GETs a minute, never a scan of the inverse
// hash that lists every instance of a model.

// GroupClient is the one call the reader makes: a pipeline, of the
// documents' lengths or of the documents (redisbatch.Windows). Narrow so a test can stand in for Redis and fail the
// transport on purpose.
type GroupClient = redisbatch.Client

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

// decode reads one of the fork's group documents (decodeGroup).
func (reader *GroupReader) decode(id string, payload []byte, readAt time.Time) *GroupSnapshot {
	return decodeGroup(id, payload, readAt)
}

// groupSource is where a store reads its groups and how it reads one: the
// fork's per-group documents (GroupReader) or the platform's one hash per
// tenant (PlatformGroupReader). Read hands each id's read to visit, a missing
// group as an answer and a failed read as the error; decode makes a read
// payload a snapshot.
type groupSource interface {
	Read(ctx context.Context, ids []string, bound int, visit func(id string, read GroupRead)) error
	decode(id string, payload []byte, readAt time.Time) *GroupSnapshot
}

// GroupRead is one id's raw read: the payload, or that the key was absent.
// The payload is the reply's own bytes, valid only while visit runs.
type GroupRead struct {
	Payload []byte
	Missing bool
}

// Read reads every id a window at a time (redisbatch.Windows) and hands each
// id's read to visit, in order, before the next window is read. A window is
// one pipeline of the documents after the last whose lengths add up to at
// most bound bytes, or the one document larger than it; each document's
// length is read once, in pipelines of redisbatch.Batch ahead of the
// windows. A group document is a whole member list,
// from a few hundred bytes to megabytes: a caller that decodes in visit
// holds one window of documents at a time, not every referenced group's,
// and one reply is never more than the bound.
//
// A missing key is not an error, it is an answer. A key Redis answers with
// an error - LOADING or BUSY, which hold for the whole instance while it
// restarts or runs a script, or a key of another type - is not: it says
// nothing about its group, and is never handed over as one. Read stops
// there and returns the error, wrapping the redisbatch.UnansweredError that
// says so, as it does for a round trip that failed; a caller keeps what it
// held for every group, and lets go of what it made of the windows visited
// before it.
func (reader *GroupReader) Read(ctx context.Context, ids []string, bound int, visit func(id string, read GroupRead)) error {
	keys := make([]string, len(ids))
	for index, id := range ids {
		keys[index] = reader.key(id)
	}
	windows := redisbatch.NewWindows(reader.client, keys, bound)
	for read := 0; read < len(keys); {
		start, values, err := windows.Next(ctx)
		if err != nil {
			return fmt.Errorf("alarmd cmdbcache: read dynamic groups: %w", err)
		}
		if start != read || len(values) == 0 {
			return fmt.Errorf("alarmd cmdbcache: read dynamic groups: %d values at %d, want some at %d", len(values), start, read)
		}
		for offset, value := range values {
			visit(ids[start+offset], GroupRead{Payload: value.Raw, Missing: value.Missing})
		}
		read += len(values)
	}
	return nil
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
	// TenantID is the tenant the writer says the group is, empty when it
	// says none. An ip_cloud plan reads a group only of its own tenant.
	TenantID string
	Members  []GroupMember
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
		case plan.Rule == contract.TargetPlanRuleHostID || plan.Identity.HostIdentity || plan.Rule == contract.TargetPlanRuleIPCloud:
			// Held under the host id: the host_id rule's key, the key of a
			// model_inst_id plan read by host identity, and what an ip_cloud
			// resolution maps to an address once per Slot. A member the
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
// it; the rest are dropped and counted. An instance the summary lists and no
// member carries is a member the list lost, counted dropped too: the two
// disagree, and the list is not the whole group (decision-017 sections 3.2
// and 4: a normal empty group has a summary that does not contradict it).
// member_list absent is a structure the reader does not accept - it cannot
// tell "empty" from "not written" - and member_list present and empty, with
// nothing in the summary, is the writer saying empty.
func decodeGroup(id string, payload []byte, readAt time.Time) *GroupSnapshot {
	snapshot := &GroupSnapshot{ID: id, ReadAt: readAt}
	var document struct {
		ModelID      string            `json:"model_id"`
		TenantID     string            `json:"bk_tenant_id"`
		ModelInstIDs []json.RawMessage `json:"model_inst_ids"`
		MemberList   *[]struct {
			ModelID     string          `json:"model_id"`
			ModelInstID json.RawMessage `json:"model_inst_id"`
			HostID      json.RawMessage `json:"bk_host_id"`
		} `json:"member_list"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
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
	snapshot.TenantID = strings.TrimSpace(document.TenantID)
	var listed map[string]struct{}
	if document.ModelInstIDs != nil {
		listed = make(map[string]struct{}, len(document.ModelInstIDs))
		for _, raw := range document.ModelInstIDs {
			if text := rawScalarText(raw); text != "" {
				listed[text] = struct{}{}
			}
		}
	}
	carried := make(map[string]struct{}, len(*document.MemberList))
	for _, wire := range *document.MemberList {
		instance := rawScalarText(wire.ModelInstID)
		if instance != "" {
			carried[instance] = struct{}{}
		}
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
	for instance := range listed {
		if _, found := carried[instance]; !found {
			snapshot.Dropped++
		}
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
	reader    groupSource
	interval  time.Duration
	maxAge    time.Duration
	readBound int
	now       func() time.Time

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
	refreshes     uint64
	// failingSince is the first refresh that failed since the last one that
	// read the groups, zero while they read; failureReason why the latest
	// failed, in closed words (groupFailureReason). A refresh reads every
	// group or none, so the run is the store's, not a group's.
	failingSince  time.Time
	failureReason string
}

// GroupStoreOptions mirror StoreOptions: the same cadence and the same
// staleness bound as the host index, on purpose.
type GroupStoreOptions struct {
	RefreshInterval time.Duration
	MaxAge          time.Duration
	// ReadBound is the most bytes of group documents one read holds at once
	// (GroupReader.Read), derived from the container by the caller.
	ReadBound int
	Now       func() time.Time
}

func NewGroupStore(reader *GroupReader, options GroupStoreOptions) (*GroupStore, error) {
	if reader == nil {
		return nil, errors.New("alarmd cmdbcache: a group reader is required")
	}
	return newGroupStore(reader, options)
}

func newGroupStore(reader groupSource, options GroupStoreOptions) (*GroupStore, error) {
	if options.RefreshInterval <= 0 {
		return nil, errors.New("alarmd cmdbcache: a positive refresh interval is required")
	}
	if options.MaxAge <= options.RefreshInterval {
		return nil, errors.New("alarmd cmdbcache: the staleness bound must exceed the refresh interval")
	}
	if options.ReadBound <= 0 {
		return nil, errors.New("alarmd cmdbcache: a positive read bound is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &GroupStore{reader: reader, interval: options.RefreshInterval, maxAge: options.MaxAge, readBound: options.ReadBound, now: now,
		snapshots: make(map[string]*GroupSnapshot), referenced: make(map[string]groupReference)}, nil
}

// MaxAge is the bound past which a held snapshot is not served.
func (store *GroupStore) MaxAge() time.Duration {
	if store == nil {
		return 0
	}
	return store.maxAge
}

// groupFailureReason is why a refresh could not read the groups, in closed
// words: the code Redis answered its key with (LOADING, BUSY, WRONGTYPE;
// redisbatch.AnsweredCode), timed_out for a round trip that ran past its
// deadline or was cancelled, transport for any other failure. Never the
// error's text: a Redis client's names the endpoint it could not reach, and
// the reason is published with the replica's dependencies.
func groupFailureReason(err error) string {
	if code, answered := redisbatch.AnsweredCode(err); answered {
		return code
	}
	var timeout interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return "timed_out"
	}
	return "transport"
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
		// A first read that fails, Redis answering the key with an error
		// included, publishes nothing: the next ask reads it again.
		var first *GroupSnapshot
		if err := store.reader.Read(ctx, []string{id}, store.readBound, func(_ string, read GroupRead) {
			first = snapshotOf(store.reader, id, read, now)
		}); err != nil {
			return GroupLookup{ReadErr: err}
		}
		store.mu.Lock()
		store.snapshots[id] = first
		store.mu.Unlock()
		snapshot, failedAt = first, time.Time{}
	}
	return GroupLookup{Snapshot: snapshot, Age: now.Sub(snapshot.ReadAt), RefreshFailed: failedAt.After(snapshot.ReadAt)}
}

// snapshotOf is one id's read as a snapshot. It keeps nothing of the read:
// the payload is the reply's, and is let go with its window.
func snapshotOf(source groupSource, id string, read GroupRead, at time.Time) *GroupSnapshot {
	if read.Missing {
		return &GroupSnapshot{ID: id, ReadAt: at, Unavailable: targetplan.ReasonKeyMissing}
	}
	return source.decode(id, read.Payload, at)
}

// Refresh re-reads every referenced group. A transport failure keeps every
// snapshot and is recorded, so the next lookups say they are served past a
// failed refresh; a missing key replaces the snapshot with an unavailable
// one - the writer withdrew or never wrote it, and that is an answer. A
// reference nobody has asked for within its horizon is forgotten first,
// snapshot and all.
//
// A group read with an explicit empty member list is the writer saying the
// group is empty, and it is read as that: a normal empty answer, with no
// members (decision-017 section 4, the E ruling: a key that is there, whose
// structure and root model check out and that states member_list: [] with no
// contradicting model_inst_ids, is a normal empty group). Nothing is held
// back for a writer cycle and no share of groups emptying together is
// judged the writer's fault: the user ruled out such share-of-set gates on
// 10-08, and a writer failure is a key missing, a document that does not
// decode, or a read that fails, each of which keeps or names its own state
// above. A restart reads what the writer has, as every refresh does.
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
	// Each window of documents is decoded as it is read, before the next is
	// read: the refresh holds one window of documents at a time beside the
	// snapshots it makes, not every referenced group's document.
	next := make(map[string]*GroupSnapshot, len(ids))
	readErr := store.reader.Read(ctx, ids, store.readBound, func(id string, read GroupRead) {
		next[id] = snapshotOf(store.reader, id, read, time.Time{})
	})
	// A read that failed - at the round trip, or Redis answering a key with
	// an error - keeps every group's snapshot, served as past a failed
	// refresh, and the run of failures says since when and why.
	if readErr != nil {
		store.mu.Lock()
		store.lastFailureAt, store.lastError = store.now(), readErr
		if store.failingSince.IsZero() {
			store.failingSince = store.lastFailureAt
		}
		store.failureReason = groupFailureReason(readErr)
		store.mu.Unlock()
		return readErr
	}
	// The snapshots are dated when the read succeeded, which is when the
	// last window was.
	at := store.now()
	for _, snapshot := range next {
		snapshot.ReadAt = at
	}
	store.mu.Lock()
	for _, id := range ids {
		if current, read := next[id]; read {
			store.snapshots[id] = current
		}
	}
	store.lastError, store.failingSince, store.failureReason = nil, time.Time{}, ""
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
	// RefreshFailed says the last refresh failed - at the round trip, or
	// Redis answering a group's key with an error - and the snapshots served
	// are older than the cadence promises.
	RefreshFailed bool
	Refreshes     uint64
	// SyncReads counts the reads made on a Slot path for an id not held:
	// one per first reference, and none after, is the reading.
	SyncReads uint64
	// FailingSince is the first refresh that failed since the groups were
	// last read, and FailureReason why the latest failed, in closed words
	// (groupFailureReason): every group held is served past them. Zero and
	// empty while the refreshes read.
	FailingSince  time.Time
	FailureReason string
}

func (store *GroupStore) Health() GroupHealth {
	if store == nil {
		return GroupHealth{}
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	health := GroupHealth{Referenced: len(store.referenced), RefreshFailed: store.lastError != nil,
		Refreshes: store.refreshes, SyncReads: store.syncReads,
		FailingSince: store.failingSince, FailureReason: store.failureReason}
	for _, snapshot := range store.snapshots {
		if snapshot.Unavailable != "" {
			health.Unavailable++
			continue
		}
		health.Loaded++
	}
	return health
}
