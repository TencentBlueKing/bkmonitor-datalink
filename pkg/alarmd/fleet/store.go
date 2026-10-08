// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

// Publishing a snapshot per replica keeps the read side free of fan-out: any
// replica answers for the whole deployment by reading what the others wrote,
// so the API needs no topology awareness and no peer discovery.
//
// Both bounds below are enforced rather than advisory. An unenforced cap is
// worse than none: it reads as a guarantee in configuration and is discovered
// to be absent only when the data has already grown.
const (
	// DefaultTTL keeps a snapshot readable for a few publishing intervals, so a
	// single slow round does not make a live replica look missing, while a
	// stopped replica disappears rather than lingering as stale truth.
	DefaultTTL = 2 * time.Minute
	// DefaultMaxAnomalyBytes bounds the encoded anomaly list rather than its
	// length, because length does not bound what actually gets written: one
	// anomaly carries up to maxStrategiesPerQueryGroup strategy references, so
	// records differ in size by about four times.
	//
	// The budget is set above what a replica can realistically produce and below
	// what the tracker's own bound allows. A replica owning every object of a
	// deployment this size reports at most a few hundred anomalies at roughly
	// 500 bytes each, and under two megabytes even if every one of them carried
	// a full strategy list; the tracker permits far more objects than that, and
	// that case is what this stops.
	//
	// The previous bound was a flat 200 records, which sat below the population
	// a healthy deployment reports during an incident -- so it truncated during
	// normal operation, which is when the list is worth reading. A bound that
	// fires routinely is not a safety valve.
	//
	// The full count travels alongside the list either way, so reaching the
	// bound is visible as truncation instead of silently shortening it.
	DefaultMaxAnomalyBytes = 2 << 20
)

// RedisStore publishes and reads replica snapshots on the control plane.
type RedisStore struct {
	client          redis.Cmdable
	prefix          string
	ttl             time.Duration
	maxAnomalyBytes int
	meter           StoreMeter
	// admitLoad asks the observation memory line for what a load of
	// snapshots will hold decoded (AdmitLoads); nil admits every load.
	// holdLoad holds it instead, for a page that releases it when its answer
	// is written (HoldLoads).
	admitLoad func(bytes uint64) bool
	holdLoad  func(bytes uint64) (release func(), held bool)
}

// ErrSnapshotsDeferred is a load of snapshots the observation memory line
// had no room for: nothing was read.
var ErrSnapshotsDeferred = errors.New("alarmd fleet: snapshots deferred: no room under the observation memory line")

// snapshotDecodedCharge is how many times its length a snapshot holds once
// decoded, which a load is admitted as: measured 2.80 on a replica of half
// a megabyte.
const snapshotDecodedCharge = 3

// AdmitLoads has every load of snapshots a reader asks for (Load: the
// objects route, the diagnosis, the strategy standing) ask admit for what
// the snapshots will hold decoded -- their lengths, read first, times
// snapshotDecodedCharge -- before any is read, and read none when it says
// no (ErrSnapshotsDeferred). A view is read whole or not at all. The
// verdict scrape's read does not ask (loadUnadmitted).
func (store *RedisStore) AdmitLoads(admit func(bytes uint64) bool) {
	if store != nil {
		store.admitLoad = admit
	}
}

// HoldLoads has a load a page reads under its collector of holds
// (pageHolds) hold what the snapshots and the view built of them come to --
// their lengths times pageReadCharge -- instead of being admitted, the hold
// kept on the collector and released with it once the page's answer is
// written. A load under no collector is admitted as AdmitLoads says; a view
// kept past its page is admitted as it is kept (admitKept).
func (store *RedisStore) HoldLoads(hold func(bytes uint64) (release func(), held bool)) {
	if store != nil {
		store.holdLoad = hold
	}
}

// admitKept asks the memory line for a view kept past the page that read
// it, as AdmitLoads's admit; nil admits it.
func (store *RedisStore) admitKept(bytes uint64) bool {
	if store == nil || store.admitLoad == nil {
		return true
	}
	return store.admitLoad(bytes)
}

// StoreMeter is what the store reports its own Redis traffic to. A fleet
// view is one MGET over every replica's snapshot and it runs on every page
// load and every OB channel invocation, but it rides the state store's
// connection: on a running deployment the per-connection counters could not
// tell two hundred views a minute from the baseline's own drift. These are
// written by this store alone, so a difference over a window is the views'.
type StoreMeter interface {
	// SnapshotPublished is the size of the snapshot just written.
	SnapshotPublished(bytes int)
	// SnapshotsLoaded is one view's read: how many snapshots came back and
	// how many bytes they were.
	SnapshotsLoaded(loaded int, bytes int)
	// SummaryPublished is the size of the summary just written beside the
	// snapshot.
	SummaryPublished(bytes int)
	// SummariesLoaded and OwnedLoaded are one summarized read: the summaries,
	// and the owned lists read when the digests disagreed.
	SummariesLoaded(loaded int, bytes int)
	OwnedLoaded(loaded int, bytes int)
}

// Meter attaches a meter to the store. Nil leaves it unmetered, which is
// what a test or a tool that is not a running replica gets.
func (store *RedisStore) Meter(meter StoreMeter) {
	if store != nil {
		store.meter = meter
	}
}

// NewRedisStore builds a store. A non-positive ttl or cap falls back to the
// package default rather than meaning "unbounded".
func NewRedisStore(client redis.Cmdable, prefix string, ttl time.Duration, maxAnomalyBytes int) (*RedisStore, error) {
	if client == nil {
		return nil, errors.New("alarmd fleet: Redis client is required")
	}
	if prefix == "" {
		return nil, errors.New("alarmd fleet: key prefix is required")
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if maxAnomalyBytes <= 0 {
		maxAnomalyBytes = DefaultMaxAnomalyBytes
	}
	return &RedisStore{client: client, prefix: prefix, ttl: ttl, maxAnomalyBytes: maxAnomalyBytes}, nil
}

// TTL reports how long a published snapshot stays readable. Callers use it to
// keep their freshness budget shorter, so a replica that stops publishing is
// seen as stale before it is seen as absent.
func (store *RedisStore) TTL() time.Duration { return store.ttl }

func (store *RedisStore) snapshotKey(replica string) string {
	return store.replicaKey("fleet-snapshot", replica)
}

// summaryKey and ownedKey are the keys a replica's summary and its owned
// list are written to, beside its snapshot.
func (store *RedisStore) summaryKey(replica string) string {
	return store.replicaKey("fleet-summary", replica)
}

func (store *RedisStore) ownedKey(replica string) string {
	return store.replicaKey("fleet-owned", replica)
}

func (store *RedisStore) replicaKey(kind, replica string) string {
	digest := sha256.Sum256([]byte(replica))
	return store.prefix + ":" + kind + ":" + hex.EncodeToString(digest[:])
}

// withinAnomalyBudget keeps the longest prefix of the list that fits the budget.
// The caller has already sorted by age, so the prefix is the oldest objects --
// the ones that have been wrong longest -- rather than an arbitrary subset that
// changes on every tick.
func withinAnomalyBudget(anomalies []Anomaly, budget int) []Anomaly {
	if budget <= 0 {
		return anomalies
	}
	spent := 0
	for index, anomaly := range anomalies {
		encoded, err := json.Marshal(anomaly)
		if err != nil {
			// Cut here rather than publish a list whose size cannot be accounted
			// for. The count beside it still reports the untruncated total, so
			// the gap stays visible.
			return anomalies[:index]
		}
		// The separator that joins this record to the previous one counts too;
		// a budget that ignores it is not the size of what gets written.
		spent += len(encoded) + 1
		if spent > budget {
			return anomalies[:index]
		}
	}
	return anomalies
}

// withinObjectBudget keeps the longest prefix of the identifier list that fits.
// The list is sorted, so the prefix is stable between ticks rather than an
// arbitrary subset that changes on every publish.
func withinObjectBudget(objects []string, budget int) []string {
	if budget <= 0 {
		return objects
	}
	spent := 0
	for index, object := range objects {
		spent += len(object) + 3 // quotes and separator
		if spent > budget {
			return objects[:index]
		}
	}
	return objects
}

// Publish writes this replica's snapshot, truncating the anomaly list to the
// configured byte budget. TotalAnomalies always carries the untruncated count so
// the reader can tell a short list from a complete one.
func (store *RedisStore) Publish(ctx context.Context, snapshot Snapshot) error {
	snapshot, err := store.written(snapshot)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("alarmd fleet: encode snapshot: %w", err)
	}
	if err := store.client.Set(ctx, store.snapshotKey(snapshot.Replica), payload, store.ttl).Err(); err != nil {
		return fmt.Errorf("alarmd fleet: publish snapshot: %w", err)
	}
	if store.meter != nil {
		store.meter.SnapshotPublished(len(payload))
	}
	return nil
}

// PublishSummarized writes this replica's snapshot as Publish does, and
// beside it its summary and its whole owned list, in one MULTI/EXEC, and
// returns the summary it wrote: a
// reader finds the three from one publish, or the three from the one before
// until they expire -- never a summary of one snapshot beside another. The
// summary is of the snapshot as written, its rows decided at stallAfter; its
// owned digest and list are the whole set, before the snapshot's list is cut.
func (store *RedisStore) PublishSummarized(ctx context.Context, snapshot Snapshot, stallAfter time.Duration) (ReplicaSummary, error) {
	owned := snapshot.OwnedObjects
	snapshot, err := store.written(snapshot)
	if err != nil {
		return ReplicaSummary{}, err
	}
	summary := SummaryOf(snapshot, owned, stallAfter)
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return ReplicaSummary{}, fmt.Errorf("alarmd fleet: encode snapshot: %w", err)
	}
	summaryPayload, err := json.Marshal(summary)
	if err != nil {
		return ReplicaSummary{}, fmt.Errorf("alarmd fleet: encode summary: %w", err)
	}
	if owned == nil {
		owned = []string{}
	}
	ownedPayload, err := json.Marshal(owned)
	if err != nil {
		return ReplicaSummary{}, fmt.Errorf("alarmd fleet: encode owned objects: %w", err)
	}
	if _, err := store.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, store.snapshotKey(snapshot.Replica), payload, store.ttl)
		pipe.Set(ctx, store.summaryKey(snapshot.Replica), summaryPayload, store.ttl)
		pipe.Set(ctx, store.ownedKey(snapshot.Replica), ownedPayload, store.ttl)
		return nil
	}); err != nil {
		return ReplicaSummary{}, fmt.Errorf("alarmd fleet: publish snapshot and summary: %w", err)
	}
	if store.meter != nil {
		store.meter.SnapshotPublished(len(payload))
		store.meter.SummaryPublished(len(summaryPayload))
	}
	return summary, nil
}

// written is the snapshot as it is written: checked, its totals at least
// its lists, and its lists cut to the budget.
func (store *RedisStore) written(snapshot Snapshot) (Snapshot, error) {
	if snapshot.Replica == "" {
		return snapshot, errors.New("alarmd fleet: snapshot requires a replica identity")
	}
	if snapshot.TakenAt.IsZero() {
		return snapshot, errors.New("alarmd fleet: snapshot requires a capture time")
	}
	if snapshot.TotalAnomalies < len(snapshot.Anomalies) {
		snapshot.TotalAnomalies = len(snapshot.Anomalies)
	}
	if snapshot.TotalDemoted < len(snapshot.Demoted) {
		snapshot.TotalDemoted = len(snapshot.Demoted)
	}
	if snapshot.TotalUndecidable < len(snapshot.Undecidable) {
		snapshot.TotalUndecidable = len(snapshot.Undecidable)
	}
	if snapshot.TotalByDesign < len(snapshot.ByDesign) {
		snapshot.TotalByDesign = len(snapshot.ByDesign)
	}
	// Each column gets the budget, rather than the two sharing one. Sharing would
	// let a long pool shorten the anomaly list, which is the reading this package
	// exists to prevent -- and it would do it during exactly the backend outage
	// that fills the pool.
	// The owned set gets the same budget and the same treatment: Owned keeps the
	// true count, so a replica past the budget publishes a short set beside a
	// full count. The aggregate notices the shortfall and refuses to compare,
	// rather than reporting set arithmetic done on half the objects.
	snapshot.OwnedObjects = withinObjectBudget(snapshot.OwnedObjects, store.maxAnomalyBytes)
	held := withinReadHoldBudget(snapshot.ReadHolds, store.maxAnomalyBytes)
	snapshot.ReadHoldsCut = snapshot.ReadHoldsCut || len(held) < len(snapshot.ReadHolds)
	snapshot.ReadHolds = held
	snapshot.Anomalies = withinAnomalyBudget(snapshot.Anomalies, store.maxAnomalyBytes)
	snapshot.Demoted = withinAnomalyBudget(snapshot.Demoted, store.maxAnomalyBytes)
	snapshot.Undecidable = withinAnomalyBudget(snapshot.Undecidable, store.maxAnomalyBytes)
	snapshot.ByDesign = withinAnomalyBudget(snapshot.ByDesign, store.maxAnomalyBytes)
	return snapshot, nil
}

// Load reads the named replicas' snapshots. A replica with no readable snapshot
// is simply absent from the result: the caller turns that into a gap, because
// only the caller knows which replicas were expected.
//
// A decode failure is reported rather than skipped. Silently dropping a corrupt
// snapshot would shorten the anomaly list, which is the exact reading this
// package exists to prevent.
func (store *RedisStore) Load(ctx context.Context, replicas []string) ([]Snapshot, error) {
	return store.load(ctx, replicas, store.admitLoad != nil || store.holdLoad != nil)
}

// loadUnadmitted is Load without asking the memory line: the reads a
// verdict is decided from (the scrape, and the health route's reads of
// replicas that published no summary). The verdict must not turn unknown because observation is
// short of memory -- which is when detection is busiest -- so it reads
// whatever the line says, until it reads replicas' summaries instead.
func (store *RedisStore) loadUnadmitted(ctx context.Context, replicas []string) ([]Snapshot, error) {
	return store.load(ctx, replicas, false)
}

func (store *RedisStore) load(ctx context.Context, replicas []string, admitted bool) ([]Snapshot, error) {
	snapshots := make([]Snapshot, 0, len(replicas))
	if err := store.loadEach(ctx, replicas, admitted, func(snapshot Snapshot) { snapshots = append(snapshots, snapshot) }); err != nil {
		return nil, err
	}
	return snapshots, nil
}

// summarizeUnadmitted reads the named replicas' snapshots as the verdict
// reads them, without asking the memory line, and keeps of each only its
// summary (summaryFromSnapshot): a snapshot is let go once it is
// summarized, where reading them all first held every one decoded at once.
// A replica with no readable snapshot is absent, as from Load.
func (store *RedisStore) summarizeUnadmitted(ctx context.Context, replicas []string, stallAfter time.Duration) ([]ReplicaSummary, error) {
	summaries := make([]ReplicaSummary, 0, len(replicas))
	if err := store.loadEach(ctx, replicas, false, func(snapshot Snapshot) {
		summaries = append(summaries, summaryFromSnapshot(snapshot, stallAfter))
	}); err != nil {
		return nil, err
	}
	return summaries, nil
}

// loadEach is load handing keep each snapshot as it is decoded, in order.
// On an error keep may have been handed some of them; the caller drops
// what it kept, as load does.
func (store *RedisStore) loadEach(ctx context.Context, replicas []string, admitted bool, keep func(Snapshot)) error {
	loaded := 0
	var decodeErr error
	var admit func(uint64) bool
	switch holds := pageHoldsOf(ctx); {
	case admitted && holds != nil && store.holdLoad != nil:
		admit = func(total uint64) bool {
			release, held := store.holdLoad(total * pageReadChargeNum / pageReadChargeDen)
			if held {
				holds.add(release, total)
			}
			return held
		}
	case admitted && store.admitLoad != nil:
		// No page reads without a collector: every one reads through the
		// shared read, which brings its own. A reader that does not is
		// admitted until the next collection, rather than read unasked.
		admit = func(total uint64) bool { return store.admitLoad(total * snapshotDecodedCharge) }
	}
	bytes, err := store.read(ctx, replicas, store.snapshotKey, admit, func(index int, text string) error {
		var snapshot Snapshot
		if err := json.Unmarshal([]byte(text), &snapshot); err != nil {
			decodeErr = fmt.Errorf("alarmd fleet: decode snapshot for %s: %w", replicas[index], err)
			return decodeErr
		}
		if snapshot.Replica != replicas[index] {
			decodeErr = fmt.Errorf("alarmd fleet: snapshot for %s reports replica %q", replicas[index], snapshot.Replica)
			return decodeErr
		}
		keep(snapshot)
		loaded++
		return nil
	})
	if errors.Is(err, ErrSnapshotsDeferred) {
		return err
	}
	if err != nil && decodeErr == nil {
		return fmt.Errorf("alarmd fleet: read snapshots: %w", err)
	}
	// Counted whether or not what came back decoded: a read that decodes
	// badly still cost the round trips and the bytes.
	if store.meter != nil && len(replicas) > 0 {
		store.meter.SnapshotsLoaded(loaded, bytes)
	}
	return decodeErr
}

// LoadSummaries reads the named replicas' summaries. A replica with none
// readable -- one that has not published since this build, or whose summary
// expired -- is absent from the result, and the reader summarizes its
// snapshot instead. A decode failure is reported, as Load reports one.
func (store *RedisStore) LoadSummaries(ctx context.Context, replicas []string) ([]ReplicaSummary, error) {
	summaries := make([]ReplicaSummary, 0, len(replicas))
	var decodeErr error
	bytes, err := store.read(ctx, replicas, store.summaryKey, nil, func(index int, text string) error {
		var summary ReplicaSummary
		if err := json.Unmarshal([]byte(text), &summary); err != nil {
			decodeErr = fmt.Errorf("alarmd fleet: decode summary for %s: %w", replicas[index], err)
			return decodeErr
		}
		if summary.Head.Replica != replicas[index] {
			decodeErr = fmt.Errorf("alarmd fleet: summary for %s reports replica %q", replicas[index], summary.Head.Replica)
			return decodeErr
		}
		summaries = append(summaries, summary)
		return nil
	})
	if err != nil && decodeErr == nil {
		return nil, fmt.Errorf("alarmd fleet: read summaries: %w", err)
	}
	if store.meter != nil && len(replicas) > 0 {
		store.meter.SummariesLoaded(len(summaries), bytes)
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	return summaries, nil
}

// LoadOwned reads the named replicas' whole owned lists, by replica. A
// replica with none readable is absent from the result.
func (store *RedisStore) LoadOwned(ctx context.Context, replicas []string) (map[string][]string, error) {
	owned := make(map[string][]string, len(replicas))
	var decodeErr error
	bytes, err := store.read(ctx, replicas, store.ownedKey, nil, func(index int, text string) error {
		var list []string
		if err := json.Unmarshal([]byte(text), &list); err != nil {
			decodeErr = fmt.Errorf("alarmd fleet: decode owned objects for %s: %w", replicas[index], err)
			return decodeErr
		}
		owned[replicas[index]] = list
		return nil
	})
	if err != nil && decodeErr == nil {
		return nil, fmt.Errorf("alarmd fleet: read owned objects: %w", err)
	}
	if store.meter != nil && len(replicas) > 0 {
		store.meter.OwnedLoaded(len(owned), bytes)
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	return owned, nil
}

// readChunkBytes is the most one MGET of a read asks for: the budget one of
// a snapshot's lists is cut to, so a larger list budget reads in larger
// MGETs with it.
func (store *RedisStore) readChunkBytes() int64 { return int64(store.maxAnomalyBytes) }

// read reads the named replicas' keys of one kind and hands visit each
// value that came back, in order, with the replica's position; it says how
// many bytes came back. The values' lengths are read first, in one pipeline,
// and the values in MGETs of at most maxAnomalyBytes each -- the budget one
// of a snapshot's lists is cut to -- or of one value when that alone is more:
// each MGET's replies are decoded and let go before the next is read, where
// one MGET of every replica held every reply, and a copy of each, beside
// everything decoded from them. A replica with nothing readable is skipped.
//
// admit, when there is one, is asked for the lengths' total before anything
// is read, and a refusal reads nothing (ErrSnapshotsDeferred).
func (store *RedisStore) read(ctx context.Context, replicas []string, key func(string) string, admit func(total uint64) bool,
	visit func(index int, text string) error) (int, error) {
	if len(replicas) == 0 {
		return 0, nil
	}
	keys := make([]string, 0, len(replicas))
	for _, replica := range replicas {
		keys = append(keys, key(replica))
	}
	lengths := make([]*redis.IntCmd, len(keys))
	if _, err := store.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index, name := range keys {
			lengths[index] = pipe.StrLen(ctx, name)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	if admit != nil {
		total := uint64(0)
		for _, length := range lengths {
			total += uint64(length.Val())
		}
		if !admit(total) {
			return 0, ErrSnapshotsDeferred
		}
	}
	bytes, bound := 0, store.readChunkBytes()
	for start := 0; start < len(keys); {
		end, spent := start, int64(0)
		for end < len(keys) && (end == start || spent+lengths[end].Val() <= bound) {
			spent += lengths[end].Val()
			end++
		}
		// This MGET's replies are referred to in this pass alone: what visit
		// keeps it decodes from them, and they go with the pass.
		values, err := store.client.MGet(ctx, keys[start:end]...).Result()
		if err != nil {
			return bytes, err
		}
		for offset, value := range values {
			if value == nil {
				continue
			}
			text, ok := value.(string)
			if !ok {
				return bytes, fmt.Errorf("value for %s has an unexpected type", replicas[start+offset])
			}
			bytes += len(text)
			if err := visit(start+offset, text); err != nil {
				return bytes, err
			}
		}
		start = end
	}
	return bytes, nil
}
