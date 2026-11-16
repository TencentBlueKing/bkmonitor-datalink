// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// stagingHookBackend is a real backend with a step run before each segment
// script and before the swap: what expiry, another attempt or a lost field
// does between the scripts of one staged write.
type stagingHookBackend struct {
	*RedisBackend
	beforeSegment func(index int, segment HashStageSegment)
	beforeSwap    func(swap HashStageSwap)
	segments      int
	stageKeys     []string
}

func (backend *stagingHookBackend) StageHashSegment(
	ctx context.Context, segment HashStageSegment,
) (HashDeltaOutcome, error) {
	if backend.beforeSegment != nil {
		backend.beforeSegment(backend.segments, segment)
	}
	backend.segments++
	backend.stageKeys = append(backend.stageKeys, segment.Key)
	return backend.RedisBackend.StageHashSegment(ctx, segment)
}

func (backend *stagingHookBackend) SwapStagedHash(ctx context.Context, swap HashStageSwap) (HashDeltaOutcome, error) {
	if backend.beforeSwap != nil {
		backend.beforeSwap(swap)
	}
	return backend.RedisBackend.SwapStagedHash(ctx, swap)
}

// stagedMemory is n absent groups, more than one segment's worth when n is.
func stagedMemory(n int) []execution.NoDataGroupMemory {
	memory := make([]execution.NoDataGroupMemory, n)
	for index := range memory {
		memory[index] = absentGroup(fmt.Sprintf("group-%05d", index))
	}
	return memory
}

// storeNoDataAs sends one mutation as the given attempt.
func storeNoDataAs(
	t *testing.T, store *ExecutionStore, mutation execution.PlanNoDataMutation, attempt execution.NoDataWriteAttempt,
) execution.NoDataApplyItemResult {
	t.Helper()
	applied, err := store.ApplyNoData(context.Background(), execution.NoDataApplyRequest{
		Retention: execution.GenerationRetention{Unknown: true}, Contract: frozenRef(),
		Items: []execution.PlanNoDataMutation{mutation}, Attempt: attempt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return applied.Items[0]
}

func stagedWriteRetried(t *testing.T, got execution.NoDataApplyItemResult) {
	t.Helper()
	if got.Status != execution.NoDataRetryable || got.ReasonCode != execution.ReasonCode(contract.ReasonStateWriteRetryable) {
		t.Fatalf("write = %+v, want RETRYABLE with %s", got, contract.ReasonStateWriteRetryable)
	}
}

// The staging key is named apart from the formal key and from every other
// attempt's: its kind is not one a reader of a memory builds or a glob over
// the formal kind matches, and the fence epoch and attempt number are in it.
func TestTheNoDataStagingKeyIsNoReadersAndEachAttemptsOwn(t *testing.T) {
	unsplit := noDataIdentityV2()
	piece := unsplit
	piece.Shard = execution.ShardRef{Dimension: "bk_target_ip", Index: 0, Count: 2, MatcherDigest: string(seriesDigest("matcher-0"))}
	for _, identity := range []execution.PlanNoDataIdentity{unsplit, piece} {
		formal, err := PlanNoDataHashKeyV2("alarmd", identity)
		if err != nil {
			t.Fatal(err)
		}
		first, err := PlanNoDataStageKeyV1("alarmd", identity, execution.NoDataWriteAttempt{OwnerEpoch: 7, AttemptNo: 1})
		if err != nil {
			t.Fatal(err)
		}
		retry, _ := PlanNoDataStageKeyV1("alarmd", identity, execution.NoDataWriteAttempt{OwnerEpoch: 7, AttemptNo: 2})
		owner, _ := PlanNoDataStageKeyV1("alarmd", identity, execution.NoDataWriteAttempt{OwnerEpoch: 8, AttemptNo: 1})
		if strings.HasPrefix(first, "alarmd:nodata-hash") || !strings.HasPrefix(first, "alarmd:nodata-stage") ||
			!strings.Contains(first, ":v1:") || !strings.HasSuffix(first, ":7:1") {
			t.Fatalf("the staging key of %s is %q; want the nodata-stage kind at v1, ending in the epoch and attempt", formal, first)
		}
		if first == formal || first == retry || first == owner || retry == owner {
			t.Fatalf("staging keys %q, %q, %q against the formal %q; want every one its own", first, retry, owner, formal)
		}
	}
}

// A staged first write lands the whole memory, across segments, and a reader
// of the formal key before the swap finds no record at all - not the
// segments written so far. After the swap the staging key is gone and the
// formal key lives for the Plan's lifetime, not the staging key's.
func TestAStagedFirstWriteIsSeenWholeOrNotAtAllOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	hooked := &stagingHookBackend{RedisBackend: server.backend}
	store := noDataStoreOver(t, hooked)
	reader := server.store(t)
	ctx := context.Background()
	groups := 2*execution.NoDataFirstWriteSegmentGroups + 500
	memory := stagedMemory(groups)
	var stageKey string
	hooked.beforeSwap = func(swap HashStageSwap) {
		stageKey = swap.StageKey
		if got := loadOneNoDataMemory(t, reader); got.Status != execution.NoDataMemoryMissing {
			t.Errorf("a reader before the swap found %+v, want no record", got)
		}
		if exists := server.client.Exists(ctx, noDataHashKey(t)).Val(); exists != 0 {
			t.Error("the formal key exists before the swap")
		}
		if fields := server.client.HLen(ctx, swap.StageKey).Val(); fields != int64(groups+1) {
			t.Errorf("the staging key holds %d fields before the swap, want %d groups and the marker", fields, groups)
		}
		if life := server.client.PTTL(ctx, swap.StageKey).Val(); life <= 0 || life > noDataStageTTLCeiling {
			t.Errorf("the staging key lives %v, want a short life of at most %v", life, noDataStageTTLCeiling)
		}
	}
	if got := storeNoDataAs(t, store, firstNoDataWrite(t, 0, memory...),
		execution.NoDataWriteAttempt{OwnerEpoch: 3, AttemptNo: 1}); got.Status != execution.NoDataApplied {
		t.Fatalf("staged first write = %+v, want it applied", got)
	}
	if hooked.segments != 3 {
		t.Fatalf("%d groups were staged in %d scripts, want 3", groups, hooked.segments)
	}
	loaded := loadOneNoDataMemory(t, reader)
	if loaded.Status != execution.NoDataMemoryFound || len(loaded.Groups) != groups {
		t.Fatalf("after the swap the record is %s with %d groups, want it found with %d", loaded.Status, len(loaded.Groups), groups)
	}
	for index, group := range loaded.Groups {
		if group.GroupKey != memory[index].GroupKey {
			t.Fatalf("group %d is %q, want %q", index, group.GroupKey, memory[index].GroupKey)
		}
	}
	if fields := server.record(t); fields[noDataStageMarkerField] != "" {
		t.Fatal("the swapped record kept the staging marker; a decoder reads it as corrupt")
	}
	if exists := server.client.Exists(ctx, stageKey).Val(); exists != 0 {
		t.Fatal("the staging key is still there after the swap")
	}
	want := max(30*24*time.Hour, GenerationScopedFloor)
	if life := server.client.PTTL(ctx, noDataHashKey(t)).Val(); life <= want-time.Minute || life > want {
		t.Fatalf("the formal key lives %v after the swap, want the Plan's %v and not the staging key's", life, want)
	}
}

// A swap over a record that exists replaces it whole: before the swap the
// formal key holds the old record and nothing staged, after it the new one
// and nothing of the old, at the lifetime the swap gave it.
func TestASwapReplacesAnExistingRecordWholeOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	backend := server.backend
	ctx := context.Background()
	const formal, stage = "record", "record-stage"
	if outcome, err := backend.ApplyHashDelta(ctx, HashDeltaWrite{
		Key: formal, HeaderField: noDataHeaderField, ExpectedMissing: true, Header: []byte("header-1"),
		Set: []HashField{{Name: "g:old-1", Value: []byte("p")}, {Name: "g:old-2", Value: []byte("p")}}, TTL: time.Minute,
	}); err != nil || outcome.Status != HashDeltaApplied {
		t.Fatalf("the old record = (%+v, %v)", outcome, err)
	}
	old := server.client.HGetAll(ctx, formal).Val()
	marker := []byte("1:1:nonce")
	for index, set := range [][]HashField{{{Name: "g:new-1", Value: []byte("p")}}, {{Name: "g:new-2", Value: []byte("p")}}} {
		if outcome, err := backend.StageHashSegment(ctx, HashStageSegment{Key: stage, MarkerField: noDataStageMarkerField,
			Marker: marker, First: index == 0, Set: set, TTL: time.Minute}); err != nil || outcome.Status != HashDeltaApplied {
			t.Fatalf("segment %d = (%+v, %v)", index, outcome, err)
		}
	}
	if during := server.client.HGetAll(ctx, formal).Val(); !reflect.DeepEqual(during, old) {
		t.Fatalf("the formal key holds %v while staged, want the old record %v", during, old)
	}
	outcome, err := backend.SwapStagedHash(ctx, HashStageSwap{Key: formal, StageKey: stage, HeaderField: noDataHeaderField,
		ExpectedDigest: HeaderDigest([]byte("header-1")), Header: []byte("header-2"),
		MarkerField: noDataStageMarkerField, Marker: marker, Fields: 3, TTL: time.Hour})
	if err != nil || outcome.Status != HashDeltaApplied {
		t.Fatalf("swap = (%+v, %v)", outcome, err)
	}
	want := map[string]string{"g:new-1": "p", "g:new-2": "p", noDataHeaderField: "header-2"}
	if after := server.client.HGetAll(ctx, formal).Val(); !reflect.DeepEqual(after, want) {
		t.Fatalf("after the swap the formal key holds %v, want %v", after, want)
	}
	if life := server.client.PTTL(ctx, formal).Val(); life <= 59*time.Minute || life > time.Hour {
		t.Fatalf("the swapped record lives %v, want the hour the swap gave it", life)
	}
	if exists := server.client.Exists(ctx, stage).Val(); exists != 0 {
		t.Fatal("the staging key outlived its swap")
	}
}

// Two attempts never mix their fields. Under keys of their own - another
// attempt number - the later one's first segment deletes only its own key,
// the earlier one's swap lands exactly its groups, and the later one then
// meets the record as a conflict and takes its staging key with it. Under one
// name - a write that holds no fence - the later first segment starts the key
// over, and the earlier write's next segment finds it is not its own.
func TestTwoAttemptsNeverMixTheirStagedFieldsOnARealServer(t *testing.T) {
	ctx := context.Background()
	identity := noDataIdentityV2()
	formal := noDataHashKey(t)
	stage := func(t *testing.T, backend *RedisBackend, key string, marker []byte, first bool, names ...string) HashDeltaStatus {
		t.Helper()
		set := make([]HashField, 0, len(names))
		for _, name := range names {
			set = append(set, HashField{Name: noDataGroupPrefix + name, Value: []byte(noDataPresentValue)})
		}
		outcome, err := backend.StageHashSegment(ctx, HashStageSegment{Key: key, MarkerField: noDataStageMarkerField,
			Marker: marker, First: first, Set: set, TTL: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		return outcome.Status
	}
	swap := func(t *testing.T, backend *RedisBackend, key string, marker []byte, groups int) HashDeltaStatus {
		t.Helper()
		outcome, err := backend.SwapStagedHash(ctx, HashStageSwap{Key: formal, StageKey: key, HeaderField: noDataHeaderField,
			ExpectedMissing: true, Header: []byte("header"), MarkerField: noDataStageMarkerField, Marker: marker,
			Fields: groups + 1, TTL: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		return outcome.Status
	}

	t.Run("under keys of their own", func(t *testing.T) {
		server := startNoDataServer(t)
		earlierKey, _ := PlanNoDataStageKeyV1("alarmd", identity, execution.NoDataWriteAttempt{OwnerEpoch: 1, AttemptNo: 1})
		laterKey, _ := PlanNoDataStageKeyV1("alarmd", identity, execution.NoDataWriteAttempt{OwnerEpoch: 1, AttemptNo: 2})
		earlier, later := []byte("1:1:a"), []byte("1:2:b")
		if got := stage(t, server.backend, earlierKey, earlier, true, "a-1"); got != HashDeltaApplied {
			t.Fatalf("earlier first segment = %s", got)
		}
		if got := stage(t, server.backend, laterKey, later, true, "b-1"); got != HashDeltaApplied {
			t.Fatalf("later first segment = %s", got)
		}
		if got := stage(t, server.backend, earlierKey, earlier, false, "a-2"); got != HashDeltaApplied {
			t.Fatalf("earlier second segment = %s; the later attempt's start reached the earlier attempt's key", got)
		}
		if got := swap(t, server.backend, earlierKey, earlier, 2); got != HashDeltaApplied {
			t.Fatalf("earlier swap = %s", got)
		}
		want := map[string]string{noDataGroupPrefix + "a-1": "p", noDataGroupPrefix + "a-2": "p", noDataHeaderField: "header"}
		if record := server.record(t); !reflect.DeepEqual(record, want) {
			t.Fatalf("the record is %v, want exactly the earlier attempt's %v", record, want)
		}
		if got := swap(t, server.backend, laterKey, later, 1); got != HashDeltaConflict {
			t.Fatalf("later swap = %s, want a conflict with the record the earlier one put in place", got)
		}
		if exists := server.client.Exists(ctx, laterKey).Val(); exists != 0 {
			t.Fatal("the refused swap left its own staging key behind")
		}
	})

	t.Run("under one name", func(t *testing.T) {
		server := startNoDataServer(t)
		key, _ := PlanNoDataStageKeyV1("alarmd", identity, execution.NoDataWriteAttempt{})
		earlier, later := []byte("0:0:a"), []byte("0:0:b")
		if got := stage(t, server.backend, key, earlier, true, "a-1"); got != HashDeltaApplied {
			t.Fatalf("earlier first segment = %s", got)
		}
		if got := stage(t, server.backend, key, later, true, "b-1"); got != HashDeltaApplied {
			t.Fatalf("later first segment = %s", got)
		}
		if got := stage(t, server.backend, key, earlier, false, "a-2"); got != HashStageLost {
			t.Fatalf("earlier second segment = %s, want STAGE_LOST: the key was started over by another write", got)
		}
		want := map[string]string{noDataGroupPrefix + "b-1": "p", noDataStageMarkerField: string(later)}
		if staged := server.client.HGetAll(ctx, key).Val(); !reflect.DeepEqual(staged, want) {
			t.Fatalf("the staging key holds %v, want only the later write's %v", staged, want)
		}
		if got := swap(t, server.backend, key, earlier, 2); got != HashStageLost {
			t.Fatalf("earlier swap = %s, want STAGE_LOST", got)
		}
		if exists := server.client.Exists(ctx, formal).Val(); exists != 0 {
			t.Fatal("a write whose staging was lost put a record in place")
		}
		// 2.8: a refused swap deletes the staging key only when the marker,
		// nonce and all, is its own. The earlier write's refusals - its
		// staging lost, or the formal header not what it expected - leave the
		// later write's key under the same name as it was.
		if staged := server.client.HGetAll(ctx, key).Val(); !reflect.DeepEqual(staged, want) {
			t.Fatalf("after the earlier write's refused swap the staging key holds %v, want the later write's %v", staged, want)
		}
		server.client.HSet(ctx, formal, noDataHeaderField, "another writer")
		if got := swap(t, server.backend, key, earlier, 1); got != HashDeltaConflict {
			t.Fatalf("earlier swap over a record in place = %s, want a conflict", got)
		}
		if staged := server.client.HGetAll(ctx, key).Val(); !reflect.DeepEqual(staged, want) {
			t.Fatalf("after the earlier write's conflict the staging key holds %v, want the later write's %v", staged, want)
		}
	})
}

// Two whole writes of one Plan under one attempt's name - a caller with no
// fence writes as attempt 0 of epoch 0 - each carry a marker of their own,
// made by the store: the later write starts the staging key over, and the
// earlier one's next segment, sent between the later one's last segment and
// its swap, is STAGE_LOST rather than adding its groups to the later write's
// key. The later write then puts exactly its own memory in place.
func TestTwoWritesUnderOneNameEachStageUnderAMarkerOfTheirOwnOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	ctx := context.Background()
	unfenced := execution.NoDataWriteAttempt{}
	earlierMemory := make([]execution.NoDataGroupMemory, execution.NoDataFirstWriteSegmentGroups+10)
	for index := range earlierMemory {
		earlierMemory[index] = absentGroup(fmt.Sprintf("earlier-%05d", index))
	}
	laterMemory := stagedMemory(execution.NoDataFirstWriteSegmentGroups + 10)

	later := &stagingHookBackend{RedisBackend: server.backend}
	laterStore := noDataStoreOver(t, later)
	earlier := &stagingHookBackend{RedisBackend: server.backend}
	earlierStore := noDataStoreOver(t, earlier)
	var interleaved HashDeltaStatus
	ran := false
	earlier.beforeSegment = func(index int, segment HashStageSegment) {
		if index != 1 || ran {
			return
		}
		ran = true
		// The later write runs whole here; just before its swap the earlier
		// write's second segment, as the store built it, is sent.
		later.beforeSwap = func(HashStageSwap) {
			outcome, err := server.backend.StageHashSegment(ctx, segment)
			if err != nil {
				t.Fatal(err)
			}
			interleaved = outcome.Status
		}
		if got := storeNoDataAs(t, laterStore, firstNoDataWrite(t, 0, laterMemory...), unfenced); got.Status != execution.NoDataApplied {
			t.Fatalf("the later write = %+v, want it applied", got)
		}
	}
	stagedWriteRetried(t, storeNoDataAs(t, earlierStore, firstNoDataWrite(t, 0, earlierMemory...), unfenced))
	if !ran {
		t.Fatal("fixture: the earlier write never reached its second segment")
	}
	if interleaved != HashStageLost {
		t.Fatalf("the earlier write's segment on the later write's key = %s, want STAGE_LOST: "+
			"the two markers are the same", interleaved)
	}
	record := server.record(t)
	if len(record) != len(laterMemory)+1 {
		t.Fatalf("the record holds %d fields, want the later write's %d groups and its header", len(record), len(laterMemory))
	}
	for name := range record {
		if strings.Contains(name, "earlier-") {
			t.Fatalf("the record holds the earlier write's group %q", name)
		}
	}
}

// K1: a staging key that expires between two segments ends the write as
// RETRYABLE and puts nothing in place - the next segment does not recreate
// the key with only the later segments and no lifetime. Expiry is the key's
// short life running out; a deletion is the same absence reached directly.
func TestAStagingKeyLostBetweenSegmentsIsRetriedAndPutsNothingInPlaceOnARealServer(t *testing.T) {
	for _, lose := range []struct {
		name string
		do   func(t *testing.T, server *noDataServer, key string)
	}{
		{name: "it expired", do: func(t *testing.T, server *noDataServer, key string) {
			ctx := context.Background()
			server.client.PExpire(ctx, key, time.Millisecond)
			testwait.Until(t, "the staging key to expire", func() bool { return server.client.Exists(ctx, key).Val() == 0 })
		}},
		{name: "it was deleted", do: func(t *testing.T, server *noDataServer, key string) {
			server.client.Del(context.Background(), key)
		}},
	} {
		t.Run(lose.name, func(t *testing.T) {
			server := startNoDataServer(t)
			hooked := &stagingHookBackend{RedisBackend: server.backend}
			store := noDataStoreOver(t, hooked)
			hooked.beforeSegment = func(index int, segment HashStageSegment) {
				if index == 1 {
					lose.do(t, server, segment.Key)
				}
			}
			swapped := false
			hooked.beforeSwap = func(HashStageSwap) { swapped = true }
			stagedWriteRetried(t, storeNoDataAs(t, store,
				firstNoDataWrite(t, 0, stagedMemory(execution.NoDataFirstWriteSegmentGroups+10)...),
				execution.NoDataWriteAttempt{OwnerEpoch: 2, AttemptNo: 1}))
			if swapped {
				t.Fatal("a write whose staging key was lost went on to the swap")
			}
			ctx := context.Background()
			if exists := server.client.Exists(ctx, noDataHashKey(t)).Val(); exists != 0 {
				t.Fatal("a write whose staging key was lost put a record in place")
			}
			if exists := server.client.Exists(ctx, hooked.stageKeys[1]).Val(); exists != 0 {
				t.Fatal("the segment after the loss recreated the staging key")
			}
		})
	}
}

// A staging key one group short of the memory is not renamed: the write is
// RETRYABLE, the formal key is not created, and the attempt's staging key is
// deleted rather than left to its life.
func TestAStagingKeyShortOfAGroupIsNotSwappedOnARealServer(t *testing.T) {
	server := startNoDataServer(t)
	hooked := &stagingHookBackend{RedisBackend: server.backend}
	store := noDataStoreOver(t, hooked)
	ctx := context.Background()
	memory := stagedMemory(execution.NoDataFirstWriteSegmentGroups + 10)
	hooked.beforeSwap = func(swap HashStageSwap) {
		server.client.HDel(ctx, swap.StageKey, noDataGroupPrefix+memory[len(memory)-1].GroupKey)
	}
	stagedWriteRetried(t, storeNoDataAs(t, store, firstNoDataWrite(t, 0, memory...),
		execution.NoDataWriteAttempt{OwnerEpoch: 4, AttemptNo: 1}))
	if exists := server.client.Exists(ctx, noDataHashKey(t)).Val(); exists != 0 {
		t.Fatal("a staging key short of a group was put in place")
	}
	if exists := server.client.Exists(ctx, hooked.stageKeys[0]).Val(); exists != 0 {
		t.Fatal("the refused swap left the attempt's staging key behind")
	}
	if got := loadOneNoDataMemory(t, server.store(t)); got.Status != execution.NoDataMemoryMissing {
		t.Fatalf("a reader after the refused swap found %+v, want no record", got)
	}
}
