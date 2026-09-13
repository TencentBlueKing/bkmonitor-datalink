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
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// Only Set and MGet are exercised; any other call would panic and thereby prove
// the store reached a command it should not use.
type fakeRedis struct {
	redis.Cmdable
	values  map[string]string
	lastTTL time.Duration
	setErr  error
}

func newFakeRedis() *fakeRedis { return &fakeRedis{values: map[string]string{}} }

func (client *fakeRedis) Set(_ context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
	client.lastTTL = ttl
	if client.setErr != nil {
		return redis.NewStatusResult("", client.setErr)
	}
	client.values[key] = string(value.([]byte))
	return redis.NewStatusResult("OK", nil)
}

func (client *fakeRedis) MGet(_ context.Context, keys ...string) *redis.SliceCmd {
	result := make([]interface{}, 0, len(keys))
	for _, key := range keys {
		if value, ok := client.values[key]; ok {
			result = append(result, value)
			continue
		}
		result = append(result, nil)
	}
	return redis.NewSliceResult(result, nil)
}

func mustStore(t *testing.T, client redis.Cmdable, ttl time.Duration, budget int) *RedisStore {
	t.Helper()
	store, err := NewRedisStore(client, "alarmd:test", ttl, budget)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func snapshotWith(anomalies int) Snapshot {
	snapshot := Snapshot{Replica: "pod-a", TakenAt: now, Owned: 500}
	for index := 0; index < anomalies; index++ {
		snapshot.Anomalies = append(snapshot.Anomalies, anomaly(string(rune('a'+index%26))))
	}
	return snapshot
}

// A cap that is announced but not applied is worse than no cap: it reads as a
// guarantee and is discovered to be absent only once the data has grown.
func TestPublishEnforcesTheAnomalyBudgetAndReportsTruncation(t *testing.T) {
	client := newFakeRedis()
	// Sized from the records themselves rather than guessed, so the test states
	// how many fit instead of depending on the encoding staying byte-identical.
	full := snapshotWith(10)
	encoded, err := json.Marshal(full.Anomalies[0])
	if err != nil {
		t.Fatal(err)
	}
	budget := (len(encoded) + 1) * 3
	store := mustStore(t, client, time.Minute, budget)
	if err := store.Publish(context.Background(), full); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), []string{"pod-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d snapshots, want 1", len(loaded))
	}
	if got := len(loaded[0].Anomalies); got != 3 {
		t.Fatalf("published anomalies = %d, want the 3 that fit the budget", got)
	}
	if loaded[0].TotalAnomalies != 10 {
		t.Fatalf("total anomalies = %d, want the untruncated 10", loaded[0].TotalAnomalies)
	}
	if !loaded[0].Truncated() {
		t.Fatal("snapshot does not report itself as truncated")
	}
}

// The bound exists to stop a pathological list, not to fire during an incident.
// A flat count of 200 sat below what a replica reports when a few hundred
// objects are degraded at once -- which is exactly when the list is read.
func TestTheDefaultBudgetHoldsEveryObjectOfADeploymentThisSize(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 0)
	full := snapshotWith(943)
	if err := store.Publish(context.Background(), full); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), []string{"pod-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(loaded[0].Anomalies); got != 943 {
		t.Fatalf("published anomalies = %d, want all 943 kept under the default budget", got)
	}
	if loaded[0].Truncated() {
		t.Fatal("a population this deployment produces normally was reported as truncated")
	}
}

// Records differ in size by about four times with the strategy list, so a list
// that fits by count can still exceed what gets written. Budgeting by bytes is
// the point of the change; budgeting by count would pass this with a payload
// several times larger.
func TestTheBudgetCountsBytesRatherThanRecords(t *testing.T) {
	client := newFakeRedis()
	heavy := Snapshot{Replica: "pod-a", TakenAt: now, Owned: 500}
	for index := 0; index < 10; index++ {
		one := anomaly(string(rune('a' + index%26)))
		for strategy := 0; strategy < 32; strategy++ {
			one.Strategies = append(one.Strategies, StrategyRef{
				StrategyID: fmt.Sprintf("strategy-%d-%d", index, strategy),
				BusinessID: fmt.Sprintf("business-%d", strategy),
			})
		}
		heavy.Anomalies = append(heavy.Anomalies, one)
	}
	light, err := json.Marshal(anomaly("a"))
	if err != nil {
		t.Fatal(err)
	}
	// A budget that would hold ten of the small records holds far fewer of these.
	store := mustStore(t, client, time.Minute, (len(light)+1)*10)
	if err := store.Publish(context.Background(), heavy); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), []string{"pod-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(loaded[0].Anomalies); got >= 10 {
		t.Fatalf("published %d heavy anomalies under a ten-small-record budget", got)
	}
	if loaded[0].TotalAnomalies != 10 {
		t.Fatalf("total anomalies = %d, want the untruncated 10", loaded[0].TotalAnomalies)
	}
}

func TestPublishAppliesTheTTLSoAStoppedReplicaDisappears(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, 90*time.Second, 10)
	if err := store.Publish(context.Background(), snapshotWith(1)); err != nil {
		t.Fatal(err)
	}
	if client.lastTTL != 90*time.Second {
		t.Fatalf("TTL = %v, want 90s", client.lastTTL)
	}
}

// Zero must mean "use the default", not "keep forever" and not "publish
// everything": an unbounded snapshot is how the control plane grows without
// anyone deciding that it should.
func TestNonPositiveBoundsFallBackToDefaultsRatherThanUnbounded(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, 0, 0)
	if store.ttl != DefaultTTL || store.maxAnomalyBytes != DefaultMaxAnomalyBytes {
		t.Fatalf("bounds = (%v, %d), want the defaults (%v, %d)",
			store.ttl, store.maxAnomalyBytes, DefaultTTL, DefaultMaxAnomalyBytes)
	}
	// Enough records that the default budget must cut them, so "zero falls back
	// to the default" is proved by the bound acting rather than by reading it.
	encoded, err := json.Marshal(anomaly("a"))
	if err != nil {
		t.Fatal(err)
	}
	fits := DefaultMaxAnomalyBytes / (len(encoded) + 1)
	if err := store.Publish(context.Background(), snapshotWith(fits+5)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), []string{"pod-a"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(loaded[0].Anomalies); got >= fits+5 {
		t.Fatalf("published anomalies = %d, want the default budget to have cut them", got)
	}
	if !loaded[0].Truncated() {
		t.Fatal("the default budget cut the list without reporting truncation")
	}
}

func TestLoadOmitsReplicasWithoutASnapshot(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 10)
	if err := store.Publish(context.Background(), snapshotWith(0)); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), []string{"pod-a", "pod-gone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Replica != "pod-a" {
		t.Fatalf("loaded = %+v, want only the replica that published", loaded)
	}
}

// Dropping an unreadable snapshot would shorten the anomaly list, which is the
// reading this package exists to prevent, so it is an error instead.
func TestLoadReportsCorruptSnapshotsInsteadOfSkippingThem(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 10)
	client.values[store.snapshotKey("pod-a")] = "{not json"
	if _, err := store.Load(context.Background(), []string{"pod-a"}); err == nil {
		t.Fatal("corrupt snapshot was accepted")
	}
}

func TestLoadRejectsASnapshotStoredUnderAnotherReplicasKey(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 10)
	client.values[store.snapshotKey("pod-a")] = `{"replica":"pod-b","taken_at":"2026-09-09T12:00:00Z","owned":1}`
	_, err := store.Load(context.Background(), []string{"pod-a"})
	if err == nil || !strings.Contains(err.Error(), "reports replica") {
		t.Fatalf("load = %v, want a mismatch between key and payload", err)
	}
}

func TestPublishRequiresIdentityAndCaptureTime(t *testing.T) {
	store := mustStore(t, newFakeRedis(), time.Minute, 10)
	if err := store.Publish(context.Background(), Snapshot{TakenAt: now}); err == nil {
		t.Fatal("snapshot without a replica identity was accepted")
	}
	if err := store.Publish(context.Background(), Snapshot{Replica: "pod-a"}); err == nil {
		t.Fatal("snapshot without a capture time was accepted")
	}
}
