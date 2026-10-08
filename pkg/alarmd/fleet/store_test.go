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
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	// mgets is how many keys each MGET asked for, and mgetFailsAt the one,
	// counted from 1, whose transport fails.
	mgets       []int
	mgetFailsAt int
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

// Pipelined runs the batch against a pipe that answers the one command
// the store pipelines when it reads, the values' lengths.
func (client *fakeRedis) Pipelined(_ context.Context, batch func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	return nil, batch(&fakePipe{client: client})
}

type fakePipe struct {
	redis.Pipeliner
	client *fakeRedis
}

func (pipe *fakePipe) StrLen(_ context.Context, key string) *redis.IntCmd {
	return redis.NewIntResult(int64(len(pipe.client.values[key])), nil)
}

func (client *fakeRedis) MGet(_ context.Context, keys ...string) *redis.SliceCmd {
	if len(keys) == 0 {
		// As Redis answers an MGET of no keys.
		return redis.NewSliceResult(nil, errors.New("ERR wrong number of arguments for 'mget' command"))
	}
	client.mgets = append(client.mgets, len(keys))
	if client.mgetFailsAt > 0 && len(client.mgets) == client.mgetFailsAt {
		return redis.NewSliceResult(nil, errors.New("connection reset"))
	}
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

// storeMeterRecord is what the store told its meter.
type storeMeterRecord struct {
	published   []int
	loads       int
	loadedCount int
	loadedBytes int
	summaries   []int
	// summaryLoads and ownedLoads are the loaded count and bytes of each
	// summarized read.
	summaryLoads [][2]int
	ownedLoads   [][2]int
}

func (m *storeMeterRecord) SnapshotPublished(bytes int) { m.published = append(m.published, bytes) }
func (m *storeMeterRecord) SnapshotsLoaded(loaded int, bytes int) {
	m.loads, m.loadedCount, m.loadedBytes = m.loads+1, m.loadedCount+loaded, m.loadedBytes+bytes
}
func (m *storeMeterRecord) SummaryPublished(bytes int) { m.summaries = append(m.summaries, bytes) }
func (m *storeMeterRecord) SummariesLoaded(loaded int, bytes int) {
	m.summaryLoads = append(m.summaryLoads, [2]int{loaded, bytes})
}
func (m *storeMeterRecord) OwnedLoaded(loaded int, bytes int) {
	m.ownedLoads = append(m.ownedLoads, [2]int{loaded, bytes})
}

// The store reports its own traffic. A fleet view is one read of every
// replica's snapshot and it happens on every page load and every OB channel
// invocation, but it rides a connection the rest of the runtime is using:
// on a running deployment two hundred views a minute could not be told from
// that connection's own drift. These are written by the store alone, so a
// difference over a window is the views' -- which is the whole point of
// counting them here rather than reading a shared connection's counters.
//
// A read that finds nothing still counts as a read: a view was built, and a
// zero-byte one is a fact about the fleet, not a read that did not happen.
func TestTheStoreReportsWhatItPublishedAndWhatEachViewRead(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 10)
	meter := &storeMeterRecord{}
	store.Meter(meter)
	if err := store.Publish(context.Background(), snapshotWith(0)); err != nil {
		t.Fatal(err)
	}
	if len(meter.published) != 1 || meter.published[0] != len(client.values[store.snapshotKey("pod-a")]) {
		t.Fatalf("published = %v, want the bytes actually written", meter.published)
	}
	if _, err := store.Load(context.Background(), []string{"pod-a", "pod-gone"}); err != nil {
		t.Fatal(err)
	}
	if meter.loads != 1 || meter.loadedCount != 1 || meter.loadedBytes != meter.published[0] {
		t.Fatalf("meter = %+v, want one read of one snapshot of %d bytes", meter, meter.published[0])
	}
	if _, err := store.Load(context.Background(), []string{"pod-gone"}); err != nil {
		t.Fatal(err)
	}
	if meter.loads != 2 || meter.loadedCount != 1 || meter.loadedBytes != meter.published[0] {
		t.Fatalf("meter = %+v, want a second read that found nothing and still counted", meter)
	}
	// An unmetered store is the ordinary case for a tool or a test.
	plain := mustStore(t, newFakeRedis(), time.Minute, 10)
	if err := plain.Publish(context.Background(), snapshotWith(0)); err != nil {
		t.Fatal(err)
	}
}

// A load reads the replicas' snapshots in MGETs of at most the store's list
// budget each -- one value alone when it is more -- and decodes each before
// the next is read, every snapshot coming back in the replicas' order.
func TestALoadReadsSnapshotsInMGetsOfTheListBudget(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 1000)
	replicas := []string{"pod-a", "pod-b", "pod-c", "pod-d"}
	for index, replica := range replicas {
		owned := make([]string, 0)
		// pod-c's alone is past the budget.
		for n := 0; n < []int{10, 10, 80, 10}[index]; n++ {
			owned = append(owned, fmt.Sprintf("%s-object-%02d", replica, n))
		}
		encoded, err := json.Marshal(Snapshot{Replica: replica, TakenAt: now, Owned: len(owned), OwnedObjects: owned})
		if err != nil {
			t.Fatal(err)
		}
		client.values[store.snapshotKey(replica)] = string(encoded)
	}
	snapshots, err := store.Load(context.Background(), replicas)
	if err != nil || len(snapshots) != len(replicas) {
		t.Fatalf("loaded %d snapshots, error %v", len(snapshots), err)
	}
	for index, snapshot := range snapshots {
		if snapshot.Replica != replicas[index] {
			t.Fatalf("snapshot %d is %s, want %s", index, snapshot.Replica, replicas[index])
		}
	}
	for index, keys := range client.mgets {
		if keys < 1 {
			t.Fatalf("MGET %d asked for no keys: %v", index, client.mgets)
		}
	}
	// pod-a and pod-b together within the budget, pod-c alone past it, and
	// pod-d, which does not fit beside it.
	if len(client.mgets) != 3 || client.mgets[0] != 2 || client.mgets[1] != 1 || client.mgets[2] != 1 {
		t.Fatalf("MGETs of %v keys, want [2 1 1]: two within the budget together, pod-c's past it alone, then pod-d", client.mgets)
	}
	total := 0
	for _, value := range client.values {
		total += len(value)
	}
	if total <= 1000 {
		t.Fatalf("the fixture's snapshots total %d bytes, within one MGET's budget", total)
	}
}

// A load whose transport fails part way through is an error and no read:
// the snapshots decoded before it are not returned, and the meter does not
// count bytes of a view that was never built.
func TestALoadWhoseTransportFailsPartWayIsNotCounted(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 1000)
	meter := &storeMeterRecord{}
	store.Meter(meter)
	replicas := []string{"pod-a", "pod-b", "pod-c"}
	for _, replica := range replicas {
		owned := make([]string, 0, 40)
		for n := 0; n < 40; n++ {
			owned = append(owned, fmt.Sprintf("%s-object-%02d", replica, n))
		}
		encoded, _ := json.Marshal(Snapshot{Replica: replica, TakenAt: now, Owned: len(owned), OwnedObjects: owned})
		client.values[store.snapshotKey(replica)] = string(encoded)
	}
	client.mgetFailsAt = 2
	if snapshots, err := store.Load(context.Background(), replicas); err == nil || snapshots != nil {
		t.Fatalf("loaded %d snapshots, error %v; want the failed read refused whole", len(snapshots), err)
	}
	if len(client.mgets) != 2 || meter.loads != 0 {
		t.Fatalf("MGETs %v, meter loads %d; want the read stopped at the failure and nothing counted", client.mgets, meter.loads)
	}
}

// A load asks the memory line for what its snapshots decode to -- their
// lengths times snapshotDecodedCharge -- before reading any, and refused it
// reads none and counts no read; admitted, it reads them all.
func TestALoadAsksTheMemoryLineForWhatItsSnapshotsDecodeTo(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 0)
	meter := &storeMeterRecord{}
	store.Meter(meter)
	total := 0
	for _, replica := range replicas() {
		encoded, _ := json.Marshal(Snapshot{Replica: replica, TakenAt: now, Owned: 1})
		client.values[store.snapshotKey(replica)] = string(encoded)
		total += len(encoded)
	}
	var asked []uint64
	store.AdmitLoads(func(bytes uint64) bool {
		asked = append(asked, bytes)
		return false
	})
	if snapshots, err := store.Load(context.Background(), replicas()); !errors.Is(err, ErrSnapshotsDeferred) || snapshots != nil {
		t.Fatalf("loaded %v with error %v, want the load deferred", snapshots, err)
	}
	if len(asked) != 1 || asked[0] != uint64(total*snapshotDecodedCharge) || len(client.mgets) != 0 || meter.loads != 0 {
		t.Fatalf("asked %v for %d bytes, MGETs %v, loads %d; want one ask for the decoded size and nothing read or counted",
			asked, total, client.mgets, meter.loads)
	}
	store.AdmitLoads(func(uint64) bool { return true })
	if snapshots, err := store.Load(context.Background(), replicas()); err != nil || len(snapshots) != 2 || meter.loads != 1 {
		t.Fatalf("loaded %d snapshots with error %v and %d loads, want both read and counted once", len(snapshots), err, meter.loads)
	}
}

// A page's view whose load the memory line refused says so in its own gap,
// not as replicas missing, a read that failed or a shortfall of ownership;
// nothing is counted and the verdict is unknown. The health route decides
// the verdict and records it, and reads a replica that published no summary
// without asking: the line refusing, it reads the verdict the data says.
func TestAViewTheMemoryLineRefusedIsOneGapAndNothingElse(t *testing.T) {
	client := newFakeRedis()
	store := mustStore(t, client, time.Minute, 0)
	for _, snapshot := range snapshotsWithAnomalies(2) {
		snapshot.TakenAt = now
		encoded, _ := json.Marshal(snapshot)
		client.values[store.snapshotKey(snapshot.Replica)] = string(encoded)
	}
	store.AdmitLoads(func(uint64) bool { return false })
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}}, stubRegistry{replicas: replicas()}, store)
	view := service.View(context.Background())
	if len(view.Gaps) != 1 || view.Gaps[0].Kind != GapSnapshotsDeferred || view.Health != HealthUnknown || view.AnomaliesTotal != 0 ||
		view.Covered != 0 {
		t.Errorf("page view: gaps %+v health %s anomalies %d covered %d, want the deferred gap alone and nothing counted",
			view.Gaps, view.Health, view.AnomaliesTotal, view.Covered)
	}
	summarized, _ := service.Summarized(context.Background(), time.Minute)
	if len(summarized.Gaps) != 0 || summarized.AnomaliesTotal != 2 || summarized.Health != HealthDegraded {
		t.Errorf("health route: gaps %+v health %s anomalies %d, want the data's verdict, the line not asked",
			summarized.Gaps, summarized.Health, summarized.AnomaliesTotal)
	}
	if MetricGapKind(GapSnapshotsDeferred) != string(GapSnapshotsDeferred) {
		t.Fatalf("the deferred gap folds to %q on the metric label", MetricGapKind(GapSnapshotsDeferred))
	}
}

// A diagnosis over a view the memory line deferred reads every Plan as not
// read, and says it was deferred rather than unreadable.
func TestADiagnosisOverADeferredViewSaysItWasDeferred(t *testing.T) {
	facts := diagnosisFacts()["4101"]
	for kind, detail := range map[GapKind]string{
		GapSnapshotsDeferred:   "fleet snapshots deferred: no room under the observation memory line",
		GapSnapshotsUnreadable: "fleet snapshots unreadable",
	} {
		ctx := newDiagnosisContext(&View{Gaps: []Gap{{Kind: kind}}}, "pod-a", now)
		row := diagnoseStrategy("4101", facts, ctx)
		if len(row.UnknownParts) == 0 || row.UnknownParts[0].Reason != UnknownReplicaUnreadable || row.UnknownParts[0].Detail != detail {
			t.Errorf("%s: parts %+v, want every Plan unread with %q", kind, row.UnknownParts, detail)
		}
	}
}

// Pages that ask for a view together share one read of the snapshots, and
// each builds its own view from it: deciding one does not decide the other.
func TestPagesThatAskTogetherShareOneReadOfTheSnapshots(t *testing.T) {
	reader := &blockingSnapshots{release: make(chan struct{}), snapshots: externalRows(2)}
	reader.snapshots[1].Anomalies[0].FailingSince = now.Add(-time.Hour)
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, reader)
	views := make(chan View, 2)
	read := func(stallAfter time.Duration) {
		view := service.View(context.Background())
		Decide(&view, now, stallAfter)
		views <- view
	}
	go read(time.Minute)
	for deadline := time.Now().Add(2 * time.Second); reader.count() == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	go read(0)
	time.Sleep(100 * time.Millisecond)
	close(reader.release)
	first, second := <-views, <-views
	if reader.count() != 1 {
		t.Fatalf("%d reads for two pages that asked together, want one", reader.count())
	}
	stalled := OursCount(first.Anomalies) + OursCount(second.Anomalies)
	if stalled != 1 || reader.snapshots[1].Anomalies[0].Stalled {
		t.Fatalf("ours across the two views %d, snapshot row stalled %v: want one view deciding the row stalled and the shared snapshot untouched",
			stalled, reader.snapshots[1].Anomalies[0].Stalled)
	}
}

// A replica that published no summary and whose snapshot could not be read
// is that replica unread; the other replicas' summaries stand.
func TestAReplicaWhoseSnapshotCouldNotBeReadLeavesTheOthersSummaries(t *testing.T) {
	summary := SummaryOf(snapshotsWithAnomalies(2)[1], nil, time.Minute)
	summary.Head.TakenAt = now
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, failingSnapshotsWithSummaries{summaries: []ReplicaSummary{summary}})
	view, part := service.Summarized(context.Background(), time.Minute)
	if !hasGap(view, GapSnapshotsUnreadable) || part.Attribution.Ours != 2 || len(view.PerReplica) != 1 {
		t.Fatalf("gaps %+v ours %d replicas %d, want pod-a unread and pod-b's summary counted", view.Gaps, part.Attribution.Ours,
			len(view.PerReplica))
	}
	// Part read is not nothing read: the objects the read replica does not
	// hold are still a shortfall.
	if !hasGap(view, GapOwnershipShortfall) {
		t.Fatalf("gaps %+v, want the ownership shortfall kept beside the replica unread", view.Gaps)
	}
	for _, gap := range view.Gaps {
		if gap.Kind == GapSnapshotsUnreadable && gap.Replica != "pod-a" {
			t.Fatalf("unread gap %+v, want it on pod-a alone", gap)
		}
	}
}

// failingSnapshotsWithSummaries has summaries for some replicas and cannot
// read any snapshot.
type failingSnapshotsWithSummaries struct{ summaries []ReplicaSummary }

func (store failingSnapshotsWithSummaries) Load(context.Context, []string) ([]Snapshot, error) {
	return nil, errors.New("snapshots unreadable")
}
func (store failingSnapshotsWithSummaries) LoadSummaries(context.Context, []string) ([]ReplicaSummary, error) {
	return store.summaries, nil
}
func (store failingSnapshotsWithSummaries) LoadOwned(context.Context, []string) (map[string][]string, error) {
	return map[string][]string{}, nil
}

// The reads past the memory line are reached by the verdict alone: the
// snapshot reads for replicas whose summaries carry no counts -- one
// snapshot at a time from Redis, all at once from any other reader -- from
// the summaries' read, and that from the health route and the verdict
// scrape. Each call is placed by the function it is written in, one entry a
// call, so a new caller of any of them fails here.
func TestTheReadsPastTheMemoryLineAreReachedByTheVerdictAlone(t *testing.T) {
	want := map[string][]string{
		"loadUnadmitted":         {"fleet/service.go:loadForVerdict"},
		"loadForVerdict":         {"fleet/service.go:summarizeFromSnapshots"},
		"summarizeUnadmitted":    {"fleet/service.go:summarizeFromSnapshots"},
		"summarizeFromSnapshots": {"fleet/service.go:summarize"},
		// the /api/health route, its one call there, and the scrape
		"Summarized": {"cmd/alarmd/runtime_phase_two_fleet.go:fleetVerdictSource", "fleet/handler.go:NewHandler"},
	}
	got := map[string][]string{}
	err := filepath.WalkDir("..", func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		relative := filepath.ToSlash(strings.TrimPrefix(path, ".."+string(filepath.Separator)))
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					if _, watched := want[selector.Sel.Name]; watched {
						got[selector.Sel.Name] = append(got[selector.Sel.Name], relative+":"+function.Name.Name)
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, callers := range want {
		sort.Strings(got[name])
		if fmt.Sprint(got[name]) != fmt.Sprint(callers) {
			t.Errorf("%s called from %v, want %v", name, got[name], callers)
		}
	}
}

// namedSnapshots holds its reads until released and answers each with a
// snapshot for every replica asked about, counting the reads.
type namedSnapshots struct {
	mu      sync.Mutex
	loads   int
	release chan struct{}
}

func (reader *namedSnapshots) Load(_ context.Context, replicas []string) ([]Snapshot, error) {
	reader.mu.Lock()
	reader.loads++
	reader.mu.Unlock()
	<-reader.release
	snapshots := make([]Snapshot, 0, len(replicas))
	for _, replica := range replicas {
		snapshots = append(snapshots, Snapshot{Replica: replica, TakenAt: now})
	}
	return snapshots, nil
}

// A read in progress is shared only with callers asking for the same
// replicas: one asking for others reads them itself.
func TestASharedReadIsSharedOnlyForTheSameReplicas(t *testing.T) {
	reader := &namedSnapshots{release: make(chan struct{})}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 2, Known: true}}, stubRegistry{replicas: replicas()}, reader)
	type answer struct{ snapshots []Snapshot }
	answers := make(chan answer, 2)
	go func() {
		snapshots, _ := service.loadShared(context.Background(), []string{"pod-a"})
		answers <- answer{snapshots}
	}()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		reader.mu.Lock()
		started := reader.loads
		reader.mu.Unlock()
		if started == 1 {
			break
		}
	}
	go func() {
		snapshots, _ := service.loadShared(context.Background(), []string{"pod-a", "pod-b"})
		answers <- answer{snapshots}
	}()
	time.Sleep(100 * time.Millisecond)
	close(reader.release)
	first, second := <-answers, <-answers
	if len(first.snapshots)+len(second.snapshots) != 3 || reader.loads != 2 {
		t.Fatalf("answers of %d and %d snapshots from %d reads, want each its own replicas from reads of their own",
			len(first.snapshots), len(second.snapshots), reader.loads)
	}
}

// The same replicas asked for in another order are the same read.
func TestASharedReadIsSharedWhateverTheReplicasOrder(t *testing.T) {
	reader := &namedSnapshots{release: make(chan struct{})}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 2, Known: true}}, stubRegistry{replicas: replicas()}, reader)
	answers := make(chan int, 2)
	go func() {
		snapshots, _ := service.loadShared(context.Background(), []string{"pod-a", "pod-b"})
		answers <- len(snapshots)
	}()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		reader.mu.Lock()
		started := reader.loads
		reader.mu.Unlock()
		if started == 1 {
			break
		}
	}
	go func() {
		snapshots, _ := service.loadShared(context.Background(), []string{"pod-b", "pod-a"})
		answers <- len(snapshots)
	}()
	time.Sleep(100 * time.Millisecond)
	close(reader.release)
	if first, second := <-answers, <-answers; first != 2 || second != 2 || reader.loads != 1 {
		t.Fatalf("answers of %d and %d snapshots from %d reads, want one read of both replicas shared", first, second, reader.loads)
	}
}

// waitingSnapshots holds its reads until released or until the read's own
// context ends, and says how each read ended. A stopped read returns once
// lingering is closed, when there is one.
type waitingSnapshots struct {
	mu        sync.Mutex
	loads     int
	stopped   int
	release   chan struct{}
	lingering chan struct{}
}

func (reader *waitingSnapshots) Load(ctx context.Context, replicas []string) ([]Snapshot, error) {
	reader.mu.Lock()
	reader.loads++
	reader.mu.Unlock()
	select {
	case <-reader.release:
	case <-ctx.Done():
		reader.mu.Lock()
		reader.stopped++
		reader.mu.Unlock()
		if reader.lingering != nil {
			<-reader.lingering
		}
		return nil, ctx.Err()
	}
	snapshots := make([]Snapshot, 0, len(replicas))
	for _, replica := range replicas {
		snapshots = append(snapshots, Snapshot{Replica: replica, TakenAt: now})
	}
	return snapshots, nil
}

func (reader *waitingSnapshots) counts() (int, int) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.loads, reader.stopped
}

func (reader *waitingSnapshots) waitLoads(t *testing.T, want int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if loads, _ := reader.counts(); loads >= want {
			return
		}
	}
	t.Fatalf("reads never reached %d", want)
}

// The caller that started a shared read going away does not fail the
// callers waiting for it: they get the snapshots. Nor does a waiter wait
// past its own context.
func TestASharedReadOutlivesTheCallerThatStartedIt(t *testing.T) {
	reader := &waitingSnapshots{release: make(chan struct{})}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 2, Known: true}}, stubRegistry{replicas: replicas()}, reader)
	type answer struct {
		snapshots []Snapshot
		err       error
	}
	first, second, third := make(chan answer, 1), make(chan answer, 1), make(chan answer, 1)
	leading, leave := context.WithCancel(context.Background())
	go func() {
		snapshots, err := service.loadShared(leading, replicas())
		first <- answer{snapshots, err}
	}()
	reader.waitLoads(t, 1)
	go func() {
		snapshots, err := service.loadShared(context.Background(), replicas())
		second <- answer{snapshots, err}
	}()
	impatient, giveUp := context.WithCancel(context.Background())
	go func() {
		snapshots, err := service.loadShared(impatient, replicas())
		third <- answer{snapshots, err}
	}()
	time.Sleep(50 * time.Millisecond)
	leave()
	if got := <-first; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the caller that left got %v, want its own cancellation", got.err)
	}
	giveUp()
	if got := <-third; !errors.Is(got.err, context.Canceled) {
		t.Fatalf("the waiter that gave up got %v, want its own cancellation while the read still runs", got.err)
	}
	close(reader.release)
	got := <-second
	if loads, stopped := reader.counts(); got.err != nil || len(got.snapshots) != 2 || loads != 1 || stopped != 0 {
		t.Fatalf("the waiter got %d snapshots, err %v, from %d reads with %d stopped; want the one read's snapshots",
			len(got.snapshots), got.err, loads, stopped)
	}
}

// A shared read everyone stopped waiting for is stopped, and a caller that
// comes after reads again rather than joining the stopped read, even while
// the stopped read has not yet returned.
func TestASharedReadNobodyWaitsForIsStopped(t *testing.T) {
	reader := &waitingSnapshots{release: make(chan struct{}), lingering: make(chan struct{})}
	defer close(reader.lingering)
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 2, Known: true}}, stubRegistry{replicas: replicas()}, reader)
	leading, leave := context.WithCancel(context.Background())
	left := make(chan error, 1)
	go func() {
		_, err := service.loadShared(leading, replicas())
		left <- err
	}()
	reader.waitLoads(t, 1)
	leave()
	<-left
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if _, stopped := reader.counts(); stopped == 1 {
			break
		}
	}
	if _, stopped := reader.counts(); stopped != 1 {
		t.Fatal("the read nobody waited for was not stopped")
	}
	close(reader.release)
	bounded, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshots, err := service.loadShared(bounded, replicas())
	if loads, _ := reader.counts(); err != nil || len(snapshots) != 2 || loads != 2 {
		t.Fatalf("the caller after got %d snapshots, err %v, after %d reads; want a read of its own", len(snapshots), err, loads)
	}
}

// The health route's shared read outlives the caller that started it too.
func TestASummarizedReadOutlivesTheCallerThatStartedIt(t *testing.T) {
	reader := &waitingSnapshots{release: make(chan struct{})}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 2, Known: true}}, stubRegistry{replicas: replicas()}, reader)
	leading, leave := context.WithCancel(context.Background())
	first, second := make(chan View, 1), make(chan View, 1)
	go func() {
		view, _ := service.Summarized(leading, time.Minute)
		first <- view
	}()
	reader.waitLoads(t, 1)
	go func() {
		view, _ := service.Summarized(context.Background(), time.Minute)
		second <- view
	}()
	time.Sleep(50 * time.Millisecond)
	leave()
	if view := <-first; !hasGap(view, GapSnapshotsUnreadable) || view.Health != HealthUnknown {
		t.Fatalf("the caller that left read %s with gaps %+v, want unknown and unread", view.Health, view.Gaps)
	}
	close(reader.release)
	if view := <-second; hasGap(view, GapSnapshotsUnreadable) || len(view.PerReplica) != 2 {
		t.Fatalf("the waiter read gaps %+v over %d replicas, want both replicas read", view.Gaps, len(view.PerReplica))
	}
}

// A diagnosis over a view the memory line deferred, or whose snapshots or
// registry could not be read, is answered and not kept; one over a view
// read whole is kept for the pages after it.
func TestADiagnosisOverADeferredViewIsNotKept(t *testing.T) {
	for name, gap := range map[string]GapKind{"deferred": GapSnapshotsDeferred, "unreadable": GapSnapshotsUnreadable,
		"registry": GapRegistryUnavailable, "read": ""} {
		deferred := gap != ""
		cache := &diagnosisCache{entries: map[string]*diagnosisEntry{}}
		view := &View{}
		if deferred {
			view.Gaps = []Gap{{Kind: gap}}
		}
		entry, _ := cache.get(context.Background(), "", now, func(context.Context) *diagnosisEntry {
			return &diagnosisEntry{view: view, readAt: now, expires: now.Add(DiagnosisCacheTTL)}
		})
		if _, kept := cache.entries[entry.id]; kept == deferred {
			t.Errorf("%s: kept %v, want a deferred view answered and not kept", name, kept)
		}
	}
}

// One replica's snapshot unread is that replica's Plans unread; the other
// replicas' Plans are observed.
func TestOneReplicaUnreadLeavesTheOthersObserved(t *testing.T) {
	ctx := newDiagnosisContext(&View{Gaps: []Gap{{Kind: GapSnapshotsUnreadable, Replica: "pod-a"}}}, "pod-a", now)
	if !ctx.unread["pod-a"] || ctx.unread[""] || ctx.unread["pod-b"] {
		t.Fatalf("unread %v, want pod-a alone", ctx.unread)
	}
}
