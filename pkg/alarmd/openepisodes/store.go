// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package openepisodes is alarmd's own record of the alert episodes it
// opened: per strategy, the fingerprints whose ABNORMAL the broker took and
// whose RECOVERY it has not, each with the strategy version it opened at
// and the series' target values. It is written by the workers after the
// broker's ACK and read by the closes before a filter close, as the
// expectation a dry run is checked against: a count that does not come
// from the alert link (filterclose).
//
// One hash per (tenant, strategy) in the runtime Redis, under the
// deployment's state prefix: field the fingerprint, value a small JSON
// entry. The hash is shared, so a restart or a new control leader reads
// what the last one left; only its expiry (TTL) or the loss of the Redis
// takes a record away, and then the closes decide alert by alert.
//
// Each hash carries its start, field SinceField, written once when the hash
// is created (HSETNX): the record holds every episode opened from then on,
// and none it can vouch for before. A hash created at the rollout, after a
// Redis loss or after its expiry lacks the episodes opened earlier and
// still open - exactly the ones that send nothing new - so the closes
// check against the record only the alerts the link opened from the start
// on, and decide the older ones alert by alert.
//
// The record over-counts, never under-counts, in normal operation: an
// episode the link ended on its own (a close by filter, a user's close)
// stays in it until a close of its decision finds nothing at the link and
// prunes the entries that decision covered. An over-count only widens the
// margin a dry run is allowed.
package openepisodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// TTL is the hash's life, set again on every write to it and at least
// once per RefreshEvery while the strategy still sends: seven days, past
// the longest a decision waits after a strategy's last event (an inactive
// period over a long weekend, an absent strategy's grace of minutes), so
// only the record of a strategy that stopped sending a week ago expires.
const TTL = 7 * 24 * time.Hour

// RefreshEvery is how often a strategy that sends only repeated ABNORMALs
// - no new episode, nothing written - has its hash's life set again.
const RefreshEvery = time.Hour

// MaxLoad bounds the entries one read takes; past it the record is
// reported truncated and the closes take it as missing.
const MaxLoad = 1 << 20

// scanCount is HSCAN's page hint.
const scanCount = 1000

// SinceField is the hash's start: the unix second it was created at. No
// fingerprint spells it (fingerprints are hex).
const SinceField = "_since"

// Key names one strategy's record.
type Key struct {
	TenantID   string
	StrategyID string
}

// Entry is one open episode.
type Entry struct {
	Fingerprint string `json:"-"`
	// Version is the strategy revision the episode opened at, as the link
	// files it (labels.strategy_version).
	Version string `json:"version"`
	// Target is the series' values of its Plan's target identity
	// dimensions, as the alert carries them; empty where the Plan names no
	// target plan or the series does not carry every dimension.
	Target   map[string]string `json:"target,omitempty"`
	OpenedAt int64             `json:"opened_at"`
	// Unreadable is an entry whose value does not decode: it counts toward
	// every expectation of the strategy and is pruned only with the whole
	// strategy.
	Unreadable bool `json:"-"`
}

// Record is one strategy's record as read.
type Record struct {
	// Exists is a hash with at least one field, its start included: a
	// record. A strategy whose hash expired or was lost has none; one whose
	// every episode recovered keeps its start and no entry.
	Exists    bool
	Truncated bool
	Entries   []Entry
	// Since is the hash's start (SinceField); zero when it has none.
	Since time.Time
}

// Store is the record in the runtime Redis.
type Store struct {
	client redis.UniversalClient
	prefix string
}

func NewStore(client redis.UniversalClient, prefix string) (*Store, error) {
	if client == nil || strings.TrimSpace(prefix) == "" || strings.ContainsAny(prefix, "{} \t\r\n") {
		return nil, errors.New("alarmd openepisodes: the record's client and prefix are required")
	}
	return &Store{client: client, prefix: prefix}, nil
}

func (store *Store) key(key Key) string {
	return store.prefix + ":openepisodes:v1:" + url.PathEscape(key.TenantID) + ":" + url.PathEscape(key.StrategyID)
}

// Change is one write an acknowledged envelope makes: an ABNORMAL's entry,
// or a RECOVERY's removal.
type Change struct {
	Key         Key
	Fingerprint string
	Open        bool
	Entry       Entry
}

// ChangeOf is the write an acknowledged envelope makes, false for one that
// opens and ends nothing at the link.
func ChangeOf(event contract.TriggerEventV1, now time.Time) (Change, bool) {
	if event.DedupeMD5 == "" || event.StrategyRef == nil || event.LegacyOutput != nil || event.TenantID == "" ||
		event.PlanRef.StrategyID == "" {
		return Change{}, false
	}
	change := Change{Key: Key{TenantID: event.TenantID, StrategyID: event.PlanRef.StrategyID}, Fingerprint: event.DedupeMD5}
	switch event.EventKind {
	case contract.TriggerEventAbnormal:
		change.Open = true
		change.Entry = Entry{Version: strconv.FormatInt(event.StrategyRef.Revision, 10),
			Target: TargetOf(event.TargetDimensions, event.RecordRef.Dimensions), OpenedAt: now.Unix()}
	case contract.TriggerEventRecovery:
	default:
		return Change{}, false
	}
	return change, true
}

// TargetOf is a series' values of the target identity dimensions, read
// from its dimensions as the link indexes them (keywords: a string's text,
// an integer's decimal text); nil when there are none or one is missing.
func TargetOf(names []string, dimensions map[string]json.RawMessage) map[string]string {
	if len(names) == 0 {
		return nil
	}
	target := make(map[string]string, len(names))
	for _, name := range names {
		value, ok := Keyword(dimensions[name])
		if !ok {
			return nil
		}
		target[name] = value
	}
	return target
}

// Keyword is a JSON string's text or a JSON integer's decimal text.
func Keyword(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, text != ""
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&number) != nil {
		return "", false
	}
	if _, err := number.Int64(); err != nil {
		return "", false
	}
	return number.String(), true
}

// Apply writes the changes and sets the life of their hashes and of the
// refresh ones, in one pipeline.
func (store *Store) Apply(ctx context.Context, changes []Change, refresh []Key) error {
	if len(changes) == 0 && len(refresh) == 0 {
		return nil
	}
	pipe := store.client.Pipeline()
	touched := map[Key]struct{}{}
	for _, change := range changes {
		key := store.key(change.Key)
		if change.Open {
			value, err := json.Marshal(change.Entry)
			if err != nil {
				return err
			}
			// The start goes in first, once: a hash this write creates
			// starts at the write.
			pipe.HSetNX(ctx, key, SinceField, strconv.FormatInt(change.Entry.OpenedAt, 10))
			// An entry already there is the episode's own: the link labels
			// an alert only when it creates it (Linkd ec55128e7
			// internal/lifecycle/plan.go:417), so a repeat after a restart
			// - the copy empty, the version moved on - keeps the version
			// and opening the episode was written with. A RECOVERY removes
			// the entry first, so a new episode is written anew.
			pipe.HSetNX(ctx, key, change.Fingerprint, value)
		} else {
			pipe.HDel(ctx, key, change.Fingerprint)
		}
		touched[change.Key] = struct{}{}
	}
	for _, key := range refresh {
		touched[key] = struct{}{}
	}
	for key := range touched {
		pipe.Expire(ctx, store.key(key), TTL)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// Load reads one strategy's record, page by page.
func (store *Store) Load(ctx context.Context, key Key) (Record, error) {
	var record Record
	var cursor uint64
	for {
		fields, next, err := store.client.HScan(ctx, store.key(key), cursor, "*", scanCount).Result()
		if err != nil {
			return Record{}, err
		}
		for i := 0; i+1 < len(fields); i += 2 {
			if fields[i] == SinceField {
				if at, err := strconv.ParseInt(fields[i+1], 10, 64); err == nil && at > 0 {
					record.Since = time.Unix(at, 0)
				}
				record.Exists = true
				continue
			}
			entry := Entry{Fingerprint: fields[i]}
			if json.Unmarshal([]byte(fields[i+1]), &entry) != nil || entry.Version == "" {
				entry = Entry{Fingerprint: fields[i], Unreadable: true}
			}
			record.Exists = true
			if len(record.Entries) >= MaxLoad {
				record.Truncated = true
				return record, nil
			}
			record.Entries = append(record.Entries, entry)
		}
		if next == 0 {
			return record, nil
		}
		cursor = next
	}
}

// Since is the record's start alone (one HGET): zero, false when the
// strategy has no record or a record without a start.
func (store *Store) Since(ctx context.Context, key Key) (time.Time, bool, error) {
	raw, err := store.client.HGet(ctx, store.key(key), SinceField).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	at, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || at <= 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(at, 0), true, nil
}

// Prune removes the entries of the given fingerprints: the episodes of a
// decision the link holds none of any more.
func (store *Store) Prune(ctx context.Context, key Key, fingerprints []string) error {
	for start := 0; start < len(fingerprints); start += scanCount {
		batch := fingerprints[start:min(len(fingerprints), start+scanCount)]
		if err := store.client.HDel(ctx, store.key(key), batch...).Err(); err != nil {
			return err
		}
	}
	return nil
}
