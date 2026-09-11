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
	digest := sha256.Sum256([]byte(replica))
	return store.prefix + ":fleet-snapshot:" + hex.EncodeToString(digest[:])
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

// Publish writes this replica's snapshot, truncating the anomaly list to the
// configured byte budget. TotalAnomalies always carries the untruncated count so
// the reader can tell a short list from a complete one.
func (store *RedisStore) Publish(ctx context.Context, snapshot Snapshot) error {
	if snapshot.Replica == "" {
		return errors.New("alarmd fleet: snapshot requires a replica identity")
	}
	if snapshot.TakenAt.IsZero() {
		return errors.New("alarmd fleet: snapshot requires a capture time")
	}
	if snapshot.TotalAnomalies < len(snapshot.Anomalies) {
		snapshot.TotalAnomalies = len(snapshot.Anomalies)
	}
	if snapshot.TotalDemoted < len(snapshot.Demoted) {
		snapshot.TotalDemoted = len(snapshot.Demoted)
	}
	// Each column gets the budget, rather than the two sharing one. Sharing would
	// let a long pool shorten the anomaly list, which is the reading this package
	// exists to prevent -- and it would do it during exactly the backend outage
	// that fills the pool.
	snapshot.Anomalies = withinAnomalyBudget(snapshot.Anomalies, store.maxAnomalyBytes)
	snapshot.Demoted = withinAnomalyBudget(snapshot.Demoted, store.maxAnomalyBytes)
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("alarmd fleet: encode snapshot: %w", err)
	}
	if err := store.client.Set(ctx, store.snapshotKey(snapshot.Replica), payload, store.ttl).Err(); err != nil {
		return fmt.Errorf("alarmd fleet: publish snapshot: %w", err)
	}
	return nil
}

// Load reads the named replicas' snapshots. A replica with no readable snapshot
// is simply absent from the result: the caller turns that into a gap, because
// only the caller knows which replicas were expected.
//
// A decode failure is reported rather than skipped. Silently dropping a corrupt
// snapshot would shorten the anomaly list, which is the exact reading this
// package exists to prevent.
func (store *RedisStore) Load(ctx context.Context, replicas []string) ([]Snapshot, error) {
	if len(replicas) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(replicas))
	for _, replica := range replicas {
		keys = append(keys, store.snapshotKey(replica))
	}
	values, err := store.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("alarmd fleet: read snapshots: %w", err)
	}
	snapshots := make([]Snapshot, 0, len(values))
	for index, value := range values {
		if value == nil {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("alarmd fleet: snapshot for %s has an unexpected type", replicas[index])
		}
		var snapshot Snapshot
		if err := json.Unmarshal([]byte(text), &snapshot); err != nil {
			return nil, fmt.Errorf("alarmd fleet: decode snapshot for %s: %w", replicas[index], err)
		}
		if snapshot.Replica != replicas[index] {
			return nil, fmt.Errorf("alarmd fleet: snapshot for %s reports replica %q", replicas[index], snapshot.Replica)
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}
