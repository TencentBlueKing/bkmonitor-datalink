// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package rollout drives an image change on a Deployment to its end: it
// watches the rollout, reverts an image that does not become ready, and keeps
// the receipt in Redis so the watch survives the process that started it -
// including when that process is one of the Pods being replaced.
package rollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
)

// The states of a receipt. The first three are in progress; the rest are
// final, and a receipt in one of them is no longer watched.
const (
	StatePending    = "pending"
	StateRollingOut = "rolling_out"
	StateReverting  = "reverting"

	StateUnchanged     = "unchanged"
	StateNotApplied    = "not_applied"
	StateComplete      = "complete"
	StateReverted      = "reverted"
	StateRevertFailed  = "revert_failed"
	StateSuperseded    = "superseded"
	StateTargetChanged = "target_changed"
)

// FinalStates is the closed set of final states, the label values of the
// count of receipts that ended.
var FinalStates = []string{StateUnchanged, StateNotApplied, StateComplete, StateReverted, StateRevertFailed, StateSuperseded, StateTargetChanged}

// Final is whether a state ends a receipt.
func Final(state string) bool {
	switch state {
	case StatePending, StateRollingOut, StateReverting:
		return false
	}
	return true
}

// Record is one receipt: the image change asked for, what the image was
// before, and where its rollout stands. Times are Unix milliseconds.
type Record struct {
	ID        string   `json:"receipt_id"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	UID       string   `json:"uid"`
	Container string   `json:"container"`
	Previous  string   `json:"previous_image"`
	Image     string   `json:"image"`
	State     string   `json:"state"`
	Warnings  []string `json:"warnings,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
	StartedAt int64    `json:"started_at_ms"`
	EndedAt   int64    `json:"ended_at_ms,omitempty"`
	// Generation is the Deployment generation the change (or, once
	// reverting, the revert) was written at; the rollout is judged at it.
	Generation int64 `json:"generation,omitempty"`
	// RevertSent is a revert that was written; the rollout watched from then
	// on is the revert's.
	RevertSent bool `json:"revert_sent,omitempty"`
	// LastProgressAt is when the rollout last moved: an updated or available
	// replica more, or the Progressing condition updated. The fallback
	// failure bound counts from it, and a takeover keeps it.
	LastProgressAt int64 `json:"last_progress_at_ms,omitempty"`
	// Progress is the rollout as last read, to tell a move from none.
	Progress Progress `json:"progress"`
	// Paused is the Deployment paused as last read; the record is written
	// when that changes, not on every poll of a pause.
	Paused bool `json:"paused,omitempty"`
	// Detail says why a final state was reached, in a few words.
	Detail string `json:"detail,omitempty"`
}

// Progress is what a rollout is judged moving by.
type Progress struct {
	Updated     int32  `json:"updated"`
	Available   int32  `json:"available"`
	Progressing string `json:"progressing_updated,omitempty"`
}

// Entry is a receipt as stored, with who drives it and until when. Now is
// Redis's clock at the read, which the lease is measured by.
type Entry struct {
	Record     Record
	DrivenBy   string
	LeaseUntil time.Time
	Now        time.Time
}

// LeaseExpired is a receipt nobody holds.
func (e Entry) LeaseExpired() bool { return !e.LeaseUntil.After(e.Now) }

// ErrNotFound is a receipt that does not exist, or has expired.
var ErrNotFound = errors.New("rollout receipt not found")

// ErrLeaseLost is a write refused because the writer no longer holds the
// receipt: another process took it over.
var ErrLeaseLost = errors.New("rollout receipt held by another process")

// The store's bounds: how long a receipt is kept, how long a lease lasts
// without renewal, and how many receipts per Deployment the latest-receipt
// index keeps.
const (
	RecordTTL     = 7 * 24 * time.Hour
	LeaseDuration = 15 * time.Second
	indexKeep     = 32
)

// Store keeps receipts in Redis: one hash per receipt (the record, its
// driver, its lease), a set of the receipts still in progress, and a sorted
// set per Deployment for its latest receipt. Every key shares the prefix's
// hash tag, so a script touching several runs on one cluster slot. Leases
// are measured by Redis's clock, not a Pod's.
type Store struct {
	client redis.UniversalClient
	prefix string
	lease  time.Duration
}

// NewStore keeps receipts under prefix, which should carry a hash tag.
func NewStore(client redis.UniversalClient, prefix string) *Store {
	return &Store{client: client, prefix: prefix, lease: LeaseDuration}
}

func (s *Store) recordKey(id string) string { return s.prefix + "rollout:" + id }
func (s *Store) activeKey() string          { return s.prefix + "rollouts:active" }
func (s *Store) indexKey(namespace, name string) string {
	return s.prefix + "rollouts:" + namespace + "/" + name
}

// clock is Redis's time in milliseconds; replicate_commands lets a script
// that read it still write on Redis 5.
const clock = `redis.replicate_commands()
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
`

// create: KEYS record, active, index; ARGV doc, owner, lease ms, ttl ms,
// receipt id, index keep. Refuses an existing receipt.
var createScript = redis.NewScript(clock + `
if redis.call('EXISTS', KEYS[1]) == 1 then return 0 end
redis.call('HSET', KEYS[1], 'doc', ARGV[1], 'owner', ARGV[2], 'lease', now + tonumber(ARGV[3]))
redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[4]))
redis.call('SADD', KEYS[2], ARGV[5])
redis.call('ZADD', KEYS[3], now, ARGV[5])
redis.call('ZREMRANGEBYSCORE', KEYS[3], '-inf', now - tonumber(ARGV[4]))
redis.call('ZREMRANGEBYRANK', KEYS[3], 0, -1 - tonumber(ARGV[6]))
redis.call('PEXPIRE', KEYS[3], tonumber(ARGV[4]))
return 1
`)

// put: KEYS record, active; ARGV doc (empty to renew only), owner, lease
// ms, final ("1" ends the receipt), receipt id. Writes only for the holder
// of a live lease.
var putScript = redis.NewScript(clock + `
local owner = redis.call('HGET', KEYS[1], 'owner')
local lease = tonumber(redis.call('HGET', KEYS[1], 'lease') or '0')
if owner ~= ARGV[2] or lease <= now then return 0 end
if ARGV[1] ~= '' then redis.call('HSET', KEYS[1], 'doc', ARGV[1]) end
if ARGV[4] == '1' then
  redis.call('HSET', KEYS[1], 'lease', 0)
  redis.call('SREM', KEYS[2], ARGV[5])
else
  redis.call('HSET', KEYS[1], 'lease', now + tonumber(ARGV[3]))
end
return 1
`)

// take: KEYS record, active; ARGV owner, lease ms, receipt id. Takes a
// receipt whose lease has run out; one that no longer exists leaves the
// in-progress set.
var takeScript = redis.NewScript(clock + `
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('SREM', KEYS[2], ARGV[3])
  return false
end
local lease = tonumber(redis.call('HGET', KEYS[1], 'lease') or '0')
if lease > now then return false end
-- A final receipt is never driven again, whatever left it in the set.
local doc = redis.call('HGET', KEYS[1], 'doc')
local state = cjson.decode(doc)['state']
if state ~= 'pending' and state ~= 'rolling_out' and state ~= 'reverting' then
  redis.call('SREM', KEYS[2], ARGV[3])
  return false
end
redis.call('HSET', KEYS[1], 'owner', ARGV[1], 'lease', now + tonumber(ARGV[2]))
return doc
`)

// release: KEYS record; ARGV owner. Ends the holder's lease so a successor
// need not wait it out.
var releaseScript = redis.NewScript(clock + `
if redis.call('HGET', KEYS[1], 'owner') ~= ARGV[1] then return 0 end
redis.call('HSET', KEYS[1], 'lease', 0)
return 1
`)

// read: KEYS record; returns doc, owner, lease, now.
var readScript = redis.NewScript(clock + `
local fields = redis.call('HMGET', KEYS[1], 'doc', 'owner', 'lease')
if not fields[1] then return false end
return {fields[1], fields[2] or '', fields[3] or '0', tostring(now)}
`)

// Create stores a new receipt held by owner.
func (s *Store) Create(ctx context.Context, record Record, owner string) error {
	doc, err := json.Marshal(record)
	if err != nil {
		return err
	}
	created, err := createScript.Run(ctx, s.client, []string{s.recordKey(record.ID), s.activeKey(), s.indexKey(record.Namespace, record.Name)},
		string(doc), owner, s.lease.Milliseconds(), RecordTTL.Milliseconds(), record.ID, indexKeep).Int()
	if err != nil {
		return err
	}
	if created != 1 {
		return fmt.Errorf("rollout receipt %s already exists", record.ID)
	}
	return nil
}

// Put writes the record and renews owner's lease, or ends the receipt when
// its state is final. ErrLeaseLost when owner no longer holds it.
func (s *Store) Put(ctx context.Context, record Record, owner string) error {
	doc, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.put(ctx, record.ID, string(doc), owner, Final(record.State))
}

// Renew extends owner's lease without changing the record.
func (s *Store) Renew(ctx context.Context, id, owner string) error {
	return s.put(ctx, id, "", owner, false)
}

func (s *Store) put(ctx context.Context, id, doc, owner string, final bool) error {
	flag := "0"
	if final {
		flag = "1"
	}
	written, err := putScript.Run(ctx, s.client, []string{s.recordKey(id), s.activeKey()}, doc, owner, s.lease.Milliseconds(), flag, id).Int()
	if err != nil {
		return err
	}
	if written != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Take gives owner a receipt whose lease has run out. false when another
// process holds it or it no longer exists.
func (s *Store) Take(ctx context.Context, id, owner string) (Record, bool, error) {
	doc, err := takeScript.Run(ctx, s.client, []string{s.recordKey(id), s.activeKey()}, owner, s.lease.Milliseconds(), id).Text()
	if errors.Is(err, redis.Nil) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var record Record
	if err := json.Unmarshal([]byte(doc), &record); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
}

// Release ends owner's lease on a receipt, for a successor to take it at
// once.
func (s *Store) Release(ctx context.Context, id, owner string) error {
	return releaseScript.Run(ctx, s.client, []string{s.recordKey(id)}, owner).Err()
}

// Get reads a receipt.
func (s *Store) Get(ctx context.Context, id string) (Entry, error) {
	values, err := readScript.Run(ctx, s.client, []string{s.recordKey(id)}).StringSlice()
	if errors.Is(err, redis.Nil) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	if len(values) != 4 {
		return Entry{}, fmt.Errorf("rollout receipt %s: unexpected read", id)
	}
	var entry Entry
	if err := json.Unmarshal([]byte(values[0]), &entry.Record); err != nil {
		return Entry{}, err
	}
	lease, _ := strconv.ParseInt(values[2], 10, 64)
	now, _ := strconv.ParseInt(values[3], 10, 64)
	entry.DrivenBy, entry.LeaseUntil, entry.Now = values[1], time.UnixMilli(lease), time.UnixMilli(now)
	return entry, nil
}

// Latest reads a Deployment's latest receipt.
func (s *Store) Latest(ctx context.Context, namespace, name string) (Entry, error) {
	ids, err := s.client.ZRevRange(ctx, s.indexKey(namespace, name), 0, indexKeep-1).Result()
	if err != nil {
		return Entry{}, err
	}
	for _, id := range ids {
		entry, err := s.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return entry, err
	}
	return Entry{}, ErrNotFound
}

// Active lists the receipts still in progress.
func (s *Store) Active(ctx context.Context) ([]string, error) {
	return s.client.SMembers(ctx, s.activeKey()).Result()
}
