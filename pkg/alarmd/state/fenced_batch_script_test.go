// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// serverCommandCalls is how many times the server has run each command
// since its stats were last reset, from INFO commandstats.
func serverCommandCalls(t *testing.T, fixture *redisBatchFixture) map[string]int {
	t.Helper()
	info, err := fixture.client.Info(context.Background(), "commandstats").Result()
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	for _, line := range strings.Split(info, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.HasPrefix(name, "cmdstat_") {
			continue
		}
		for _, pair := range strings.Split(value, ",") {
			if key, number, found := strings.Cut(pair, "="); found && key == "calls" {
				calls[strings.TrimPrefix(name, "cmdstat_")], _ = strconv.Atoi(number)
			}
		}
	}
	return calls
}

func fencedWritesForTest(prefix string, count, size int) []FencedWrite {
	writes := make([]FencedWrite, count)
	for index := range writes {
		writes[index] = FencedWrite{Key: fmt.Sprintf("%s:%d", prefix, index), ExpectedMissing: true,
			Value: []byte(strings.Repeat("v", size)), TTL: time.Minute}
	}
	return writes
}

// A batch of fenced writes verifies the owner fence once per script, not
// once per write: the server runs one TIME and the fence's HGETs for the
// script, and a GET and a PSETEX for each write. The fence used to be most
// of what a state write cost the server.
func TestAFencedBatchVerifiesTheFenceOncePerScript(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	guard := &FenceGuard{Keys: fixture.owners.FenceKeys(frozenRef().Slot.QueryGroup), OwnerID: fixture.fence.OwnerID,
		OwnerEpoch: fixture.fence.OwnerEpoch, LeaseToken: fixture.fence.LeaseToken}
	const writes = fencedScriptItems
	if err := fixture.client.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	outcomes, err := fixture.backend.CompareAndSetManyByDigest(ctx, guard, fencedWritesForTest("once", writes, 64))
	if err != nil {
		t.Fatal(err)
	}
	for index, outcome := range outcomes {
		if outcome.Err != nil || outcome.Status != FencedWriteApplied {
			t.Fatalf("write %d: %+v", index, outcome)
		}
	}
	calls := serverCommandCalls(t, fixture)
	total := 0
	for name, count := range calls {
		if name != "script|load" && name != "config|resetstat" && name != "info" {
			total += count
		}
	}
	t.Logf("%d writes: server commands %v, %.2f per write", writes, calls, float64(total)/float64(writes))
	if calls["evalsha"] != 1 || calls["time"] != 1 || calls["hget"] > 6 || calls["get"] != writes || calls["psetex"] != writes {
		t.Fatalf("server commands %v, want one script, one TIME, at most six HGET and one GET and PSETEX per write", calls)
	}
}

// A lease that has lapsed refuses every write of the batch, and none is
// made: the fence is decided once, before any write, for all of them.
func TestALapsedLeaseRefusesTheWholeBatchAndWritesNothing(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	guard := &FenceGuard{Keys: fixture.owners.FenceKeys(frozenRef().Slot.QueryGroup), OwnerID: fixture.fence.OwnerID,
		OwnerEpoch: fixture.fence.OwnerEpoch, LeaseToken: fixture.fence.LeaseToken}
	fixture.lapseLease(t)
	writes := fencedWritesForTest("lapsed", 200, 64)
	outcomes, err := fixture.backend.CompareAndSetManyByDigest(ctx, guard, writes)
	if err != nil || len(outcomes) != len(writes) {
		t.Fatalf("CompareAndSetManyByDigest() = (%d outcomes, %v)", len(outcomes), err)
	}
	for index, outcome := range outcomes {
		if outcome.Err != nil || outcome.Status != FencedWriteStaleOwner {
			t.Fatalf("write %d: %+v, want STALE_OWNER", index, outcome)
		}
	}
	keys := make([]string, len(writes))
	for index, write := range writes {
		keys[index] = write.Key
	}
	if exists := fixture.client.Exists(ctx, keys...).Val(); exists != 0 {
		t.Fatalf("%d keys written under a lapsed lease", exists)
	}
}

// A call larger than one script is cut into several, in order, and every
// write still gets its own answer in its own place.
func TestAFencedBatchLargerThanOneScriptKeepsEveryAnswerInPlace(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	guard := &FenceGuard{Keys: fixture.owners.FenceKeys(frozenRef().Slot.QueryGroup), OwnerID: fixture.fence.OwnerID,
		OwnerEpoch: fixture.fence.OwnerEpoch, LeaseToken: fixture.fence.LeaseToken}
	writes := fencedWritesForTest("split", fencedScriptItems+44, 32)
	// One write in each script expects a value that is not there.
	for _, index := range []int{3, fencedScriptItems + 7} {
		writes[index].ExpectedMissing, writes[index].ExpectedDigest = false, ExpectedValueDigest([]byte("absent"))
	}
	if err := fixture.client.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	outcomes, err := fixture.backend.CompareAndSetManyByDigest(ctx, guard, writes)
	if err != nil || len(outcomes) != len(writes) {
		t.Fatalf("CompareAndSetManyByDigest() = (%d outcomes, %v)", len(outcomes), err)
	}
	for index, outcome := range outcomes {
		want := FencedWriteApplied
		if index == 3 || index == fencedScriptItems+7 {
			want = FencedWriteConflictMissing
		}
		if outcome.Err != nil || outcome.Status != want {
			t.Fatalf("write %d: %+v, want %s", index, outcome, want)
		}
	}
	if calls := serverCommandCalls(t, fixture); calls["evalsha"] != 2 || calls["time"] != 2 {
		t.Fatalf("server commands %v, want two scripts", calls)
	}
}

// A call over the byte bound but under the key bound is cut by its bytes:
// 256 writes of 1 KiB are two scripts, each fenced once, every answer in its
// place.
func TestAFencedBatchOverTheByteBoundIsCutByItsBytes(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	guard := &FenceGuard{Keys: fixture.owners.FenceKeys(frozenRef().Slot.QueryGroup), OwnerID: fixture.fence.OwnerID,
		OwnerEpoch: fixture.fence.OwnerEpoch, LeaseToken: fixture.fence.LeaseToken}
	writes := fencedWritesForTest("bytes", fencedScriptItems, 1<<10)
	if len(writes)<<10 <= fencedScriptBytes || len(writes)<<10 > 2*fencedScriptBytes {
		t.Fatalf("%d KiB is not between one and two byte bounds", len(writes))
	}
	// One write in each script expects a value that is not there.
	first := fencedScriptBytes >> 10
	for _, index := range []int{first - 1, first} {
		writes[index].ExpectedMissing, writes[index].ExpectedDigest = false, ExpectedValueDigest([]byte("absent"))
	}
	if err := fixture.client.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	outcomes, err := fixture.backend.CompareAndSetManyByDigest(ctx, guard, writes)
	if err != nil || len(outcomes) != len(writes) {
		t.Fatalf("CompareAndSetManyByDigest() = (%d outcomes, %v)", len(outcomes), err)
	}
	for index, outcome := range outcomes {
		want := FencedWriteApplied
		if index == first-1 || index == first {
			want = FencedWriteConflictMissing
		}
		if outcome.Err != nil || outcome.Status != want {
			t.Fatalf("write %d: %+v, want %s", index, outcome, want)
		}
		if value := fixture.client.Get(ctx, writes[index].Key).Val(); (want == FencedWriteApplied) != (value == string(writes[index].Value)) {
			t.Fatalf("write %d answered %s, key holds %d bytes", index, want, len(value))
		}
	}
	if calls := serverCommandCalls(t, fixture); calls["evalsha"] != 2 || calls["time"] != 2 {
		t.Fatalf("server commands %v, want two scripts", calls)
	}
}

// The bounds of one script, on both sides of each.
func TestFencedScriptChunksCutAtBothBounds(t *testing.T) {
	sizes := func(chunks []fencedChunk) []int {
		out := make([]int, len(chunks))
		for index, chunk := range chunks {
			out[index] = chunk.end - chunk.start
		}
		return out
	}
	for _, test := range []struct {
		name   string
		writes []FencedWrite
		want   []int
	}{
		{"one write", fencedWritesForTest("c", 1, 10), []int{1}},
		{"exactly the key bound", fencedWritesForTest("c", fencedScriptItems, 10), []int{fencedScriptItems}},
		{"one past the key bound", fencedWritesForTest("c", fencedScriptItems+1, 10), []int{fencedScriptItems, 1}},
		{"exactly the byte bound", fencedWritesForTest("c", 4, fencedScriptBytes/4), []int{4}},
		{"one byte past the byte bound", append(fencedWritesForTest("c", 4, fencedScriptBytes/4), fencedWritesForTest("d", 1, 1)...), []int{4, 1}},
		{"a write larger than the byte bound goes alone", append(append(fencedWritesForTest("c", 1, 10), fencedWritesForTest("d", 1, fencedScriptBytes+1)...), fencedWritesForTest("e", 1, 10)...), []int{1, 1, 1}},
	} {
		if got := sizes(fencedScriptChunks(test.writes)); fmt.Sprint(got) != fmt.Sprint(test.want) {
			t.Errorf("%s: scripts of %v, want %v", test.name, got, test.want)
		}
	}
}

// TestMeasureFencedBatchScriptTime is how long one batch script holds the
// server at its bounds: every write overwrites a value it proves by digest,
// so each one reads, hashes and writes. A measurement, run with
// ALARMD_MEASURE_FENCED_SCRIPT=1; the server's SLOWLOG gives the script's
// own time.
func TestMeasureFencedBatchScriptTime(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_FENCED_SCRIPT") == "" {
		t.Skip("a measurement; set ALARMD_MEASURE_FENCED_SCRIPT=1 to run it")
	}
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	guard := &FenceGuard{Keys: fixture.owners.FenceKeys(frozenRef().Slot.QueryGroup), OwnerID: fixture.fence.OwnerID,
		OwnerEpoch: fixture.fence.OwnerEpoch, LeaseToken: fixture.fence.LeaseToken}
	for _, setting := range [][2]string{{"slowlog-log-slower-than", "0"}, {"slowlog-max-len", "10000"}} {
		if err := fixture.client.ConfigSet(ctx, setting[0], setting[1]).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, shape := range []struct{ writes, size int }{
		{256, 64}, {256, 800}, {256, 4096}, {29, 7168}, {1, 200 << 10}, {1, 1 << 20},
	} {
		var samples []time.Duration
		for run := 0; run < 50; run++ {
			writes := fencedWritesForTest(fmt.Sprintf("measure:%d:%d", shape.size, run), shape.writes, shape.size)
			if _, err := fixture.backend.CompareAndSetManyByDigest(ctx, guard, writes); err != nil {
				t.Fatal(err)
			}
			// The measured call overwrites each value, proving the one it read.
			for index := range writes {
				writes[index].ExpectedMissing = false
				writes[index].ExpectedDigest = ExpectedValueDigest(writes[index].Value)
				writes[index].Value = []byte(strings.Repeat("w", shape.size))
			}
			fixture.client.Do(ctx, "SLOWLOG", "RESET")
			outcomes, err := fixture.backend.CompareAndSetManyByDigest(ctx, guard, writes)
			if err != nil || outcomes[0].Status != FencedWriteApplied {
				t.Fatalf("overwrite: %+v, %v", outcomes[0], err)
			}
			entries, _ := fixture.client.UniversalClient.(*redis.Client).SlowLogGet(ctx, -1).Result()
			for _, entry := range entries {
				if len(entry.Args) > 0 && strings.EqualFold(entry.Args[0], "evalsha") {
					samples = append(samples, entry.Duration)
				}
			}
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		at := func(q float64) time.Duration { return samples[int(q*float64(len(samples)-1)+0.5)] }
		t.Logf("%d writes x %d bytes: %d scripts, script time p50=%v p99=%v max=%v", shape.writes, shape.size, len(samples)/50,
			at(0.5), at(0.99), at(1))
	}
}

// scriptlessPipeliner stands in for a server that dropped the script between
// the pipeline caching it and the calls using it: the caching command is
// replaced by one that caches nothing, keeping its place in the replies.
type scriptlessPipeliner struct{ redis.Pipeliner }

func (pipe scriptlessPipeliner) ScriptLoad(ctx context.Context, _ string) *redis.StringCmd {
	return pipe.Pipeliner.Echo(ctx, "not cached")
}

type scriptlessClient struct{ *countingRedisClient }

func (client scriptlessClient) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	return client.countingRedisClient.Pipelined(ctx, func(pipe redis.Pipeliner) error { return fn(scriptlessPipeliner{pipe}) })
}

// A script the server does not hold is answered NOSCRIPT and never ran, so
// its writes are sent again with the script's text and applied once -- every
// script of the call, each answer in its place.
func TestAFencedBatchTheServerHadNoScriptForIsSentAgainWithItsText(t *testing.T) {
	fixture := newRedisBatchFixture(t)
	ctx := context.Background()
	if err := fixture.client.ScriptFlush(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	backend := &RedisBackend{address: fixture.address, client: scriptlessClient{fixture.client}}
	guard := &FenceGuard{Keys: fixture.owners.FenceKeys(frozenRef().Slot.QueryGroup), OwnerID: fixture.fence.OwnerID,
		OwnerEpoch: fixture.fence.OwnerEpoch, LeaseToken: fixture.fence.LeaseToken}
	writes := fencedWritesForTest("noscript", fencedScriptItems+3, 16)
	if err := fixture.client.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	outcomes, err := backend.CompareAndSetManyByDigest(ctx, guard, writes)
	if err != nil || len(outcomes) != len(writes) {
		t.Fatalf("CompareAndSetManyByDigest() = (%d outcomes, %v)", len(outcomes), err)
	}
	for index, outcome := range outcomes {
		if outcome.Err != nil || outcome.Status != FencedWriteApplied {
			t.Fatalf("write %d: %+v", index, outcome)
		}
	}
	calls := serverCommandCalls(t, fixture)
	if calls["eval"] != 2 || calls["psetex"] != len(writes) {
		t.Fatalf("server commands %v, want both scripts sent again as EVAL and every write made once", calls)
	}
}
