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
	// DefaultMaxAnomalies bounds one replica's published list. The full count
	// travels alongside it, so exceeding the bound is visible as truncation
	// instead of silently shortening the list.
	DefaultMaxAnomalies = 200
)

// RedisStore publishes and reads replica snapshots on the control plane.
type RedisStore struct {
	client       redis.Cmdable
	prefix       string
	ttl          time.Duration
	maxAnomalies int
}

// NewRedisStore builds a store. A non-positive ttl or cap falls back to the
// package default rather than meaning "unbounded".
func NewRedisStore(client redis.Cmdable, prefix string, ttl time.Duration, maxAnomalies int) (*RedisStore, error) {
	if client == nil {
		return nil, errors.New("alarmd fleet: Redis client is required")
	}
	if prefix == "" {
		return nil, errors.New("alarmd fleet: key prefix is required")
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if maxAnomalies <= 0 {
		maxAnomalies = DefaultMaxAnomalies
	}
	return &RedisStore{client: client, prefix: prefix, ttl: ttl, maxAnomalies: maxAnomalies}, nil
}

// TTL reports how long a published snapshot stays readable. Callers use it to
// keep their freshness budget shorter, so a replica that stops publishing is
// seen as stale before it is seen as absent.
func (store *RedisStore) TTL() time.Duration { return store.ttl }

func (store *RedisStore) snapshotKey(replica string) string {
	digest := sha256.Sum256([]byte(replica))
	return store.prefix + ":fleet-snapshot:" + hex.EncodeToString(digest[:])
}

// Publish writes this replica's snapshot, truncating the anomaly list to the
// configured bound. TotalAnomalies always carries the untruncated count so the
// reader can tell a short list from a complete one.
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
	if len(snapshot.Anomalies) > store.maxAnomalies {
		snapshot.Anomalies = snapshot.Anomalies[:store.maxAnomalies]
	}
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
