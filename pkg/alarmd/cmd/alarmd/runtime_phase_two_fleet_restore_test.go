// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
)

func TestFleetRestoreRetriesWithinBudgetAndReacquires(t *testing.T) {
	at := time.Now()
	owned := []execution.QueryGroupIdentity{"a", "b"}
	reads := map[execution.QueryGroupIdentity]int{}
	publisher := fleetPublisher{
		tracker:       fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned:         func() []execution.QueryGroupIdentity { return owned },
		now:           func() time.Time { return at },
		restoreBudget: 1, staleAfter: time.Minute,
		restore: oneByOne(func(_ context.Context, qg execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			reads[qg]++
			if qg == "a" && reads[qg] == 1 {
				return fleet.RestoredState{}, errors.New("temporary read failure")
			}
			return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, nil
		}),
	}
	publisher.tracker.Observe(context.Background(), observability.Observation{
		RunOutcome: "source_not_due", Trace: observability.TraceFields{QueryGroupKey: "a"},
	})
	for i, want := range []int{0, 1, 2} {
		snapshot := publisher.snapshot(context.Background())
		if snapshot.Determined != want || reads["a"]+reads["b"] != i+1 {
			t.Fatalf("publish %d: determined=%d reads=%v", i, snapshot.Determined, reads)
		}
	}
	owned = []execution.QueryGroupIdentity{"b"}
	publisher.snapshot(context.Background())
	if _, kept := publisher.restoreAttempts["a"]; kept || publisher.tracker.HasConclusion("a") {
		t.Fatal("lost ownership retained restore state")
	}
	owned = []execution.QueryGroupIdentity{"a", "b"}
	if got := publisher.snapshot(context.Background()).Determined; got != 2 || reads["a"] != 3 {
		t.Fatalf("reacquisition did not restore: determined=%d reads=%v", got, reads)
	}
}

func TestFleetRestoreStopsFailedReadsAndAdvances(t *testing.T) {
	at := time.Now()
	reads := map[execution.QueryGroupIdentity]int{}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"broken", "missing", "stale", "healthy"}
		},
		now:           func() time.Time { return at },
		restoreBudget: 1, staleAfter: time.Minute,
		restore: oneByOne(func(_ context.Context, qg execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			reads[qg]++
			switch qg {
			case "broken":
				return fleet.RestoredState{}, errors.New("read failure")
			case "missing":
				return fleet.RestoredState{}, nil
			case "stale":
				return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at.Add(-time.Hour)}, nil
			default:
				return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, nil
			}
		}),
	}
	for i := 0; i < 10; i++ {
		publisher.snapshot(context.Background())
	}
	if reads["broken"] != fleetRestoreMaxAttempts || reads["missing"] != 1 || reads["stale"] != 1 || reads["healthy"] != 1 {
		t.Fatalf("unbounded retries or starved objects: %v", reads)
	}
	if publisher.tracker.Determined() != 1 {
		t.Fatal("missing, stale or unreadable history became determined")
	}
}

// An object this process determined with an empty round before its record
// was read still has the record read, once: the record dates the run of
// empty rounds from before the restart. An object seen with records has no
// use for it and is not read.
func TestFleetRestoreReadsTheRecordOfAnObjectDeterminedWithoutRecords(t *testing.T) {
	at := time.Now()
	since := at.Add(-70 * time.Minute)
	tracker := fleet.NewTracker(nil, "pod", func() time.Time { return at })
	reads := map[execution.QueryGroupIdentity]int{}
	publisher := fleetPublisher{
		tracker: tracker, restoreBudget: 8, staleAfter: time.Minute,
		owned: func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"empty", "data"} },
		now:   func() time.Time { return at },
		restore: oneByOne(func(_ context.Context, qg execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			reads[qg]++
			return fleet.RestoredState{LastCompletion: "FULL_EMPTY_COMPLETED", NextSlot: at, EmptyRunSince: since,
				LastRound: &fleet.RestoredRound{Slot: at.Add(-2 * time.Minute), Kind: "FULL_EMPTY_COMPLETED"}}, nil
		}),
	}
	round := func(qg, kind string) {
		tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: kind,
			Trace: observability.TraceFields{QueryGroupKey: qg, StrategyID: "4101", BusinessID: "2", EvaluationTime: at.Add(-2 * time.Minute).Unix()}})
	}
	round("empty", "FULL_EMPTY_COMPLETED")
	round("data", "FULL_COMPLETED")
	var snapshot fleet.Snapshot
	for i := 0; i < 3; i++ {
		snapshot = publisher.snapshot(context.Background())
	}
	if reads["empty"] != 1 || reads["data"] != 0 {
		t.Fatalf("reads = %v, want the empty object's record read once and the other's not at all", reads)
	}
	for _, row := range snapshot.NoData {
		if row.QueryGroup == "empty" && row.Kind == fleet.KindEmptyEveryRound && row.Since.Equal(since.Truncate(time.Second)) {
			return
		}
	}
	t.Fatalf("no data rows = %+v, want the empty object listed from its record's start %s", snapshot.NoData, since)
}

func TestFleetRestoreDoesNotOverwriteConclusionDuringRead(t *testing.T) {
	at := time.Now()
	tracker := fleet.NewTracker(nil, "pod", func() time.Time { return at })
	publisher := fleetPublisher{
		tracker: tracker, restoreBudget: 1, staleAfter: time.Minute,
		restore: oneByOne(func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			for i := 0; i < fleet.DefaultBlockedRounds; i++ {
				tracker.Observe(context.Background(), observability.Observation{
					RunOutcome: "source_error", Trace: observability.TraceFields{QueryGroupKey: "qg"},
				})
			}
			return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, nil
		}),
	}
	publisher.restoreOwned(context.Background(), []execution.QueryGroupIdentity{"qg"}, at)
	if got := tracker.Anomalies(); len(got) != 1 || got[0].Kind != fleet.KindBlockedRun {
		t.Fatalf("history overwrote a newer failure: %+v", got)
	}
}

func TestFleetRestoreFiltersRetiredRunnerDuringRead(t *testing.T) {
	at := time.Now()
	tracker := fleet.NewTracker(nil, "pod", func() time.Time { return at })
	publisher := fleetPublisher{
		tracker: tracker, restoreBudget: 1, staleAfter: time.Minute,
		owned: func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"current"} },
		now:   func() time.Time { return at },
		restore: oneByOne(func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			for i := 0; i < fleet.DefaultBlockedRounds; i++ {
				tracker.Observe(context.Background(), observability.Observation{
					RunOutcome: "source_error", Trace: observability.TraceFields{QueryGroupKey: "retired"},
				})
			}
			return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, nil
		}),
	}
	snapshot := publisher.snapshot(context.Background())
	if snapshot.Determined != 1 || len(snapshot.Anomalies) != 0 || tracker.HasConclusion("retired") {
		t.Fatalf("retired runner contaminated snapshot: %+v", snapshot)
	}
}

// The commit's summary of the last round reaches the tracker with the keys
// the contract fixed: the Slot that completed, the commit's clock, how it
// ended and why, and the revisions it ran under. A record from before the
// field existed has no round, and a clock that does not parse is dropped
// rather than invented.
func TestRestoredRoundIsMappedFromTheCommittedSummary(t *testing.T) {
	summary := &execution.LastCompletionSummary{
		Slot: 1_700_000_060, CompletedAt: "2026-09-16T08:00:05Z", Kind: execution.CompletionPartialGap, ReasonCode: "QUERY_TIMEOUT",
		Contract: execution.FrozenExecutionContractRef{SnapshotRevision: "s1", QueryRevision: "q1", ScheduleRevision: "r1"},
	}
	round := restoredRoundOf(summary)
	if round == nil || !round.Slot.Equal(time.Unix(1_700_000_060, 0)) || !round.CompletedAt.Equal(time.Date(2026, 9, 16, 8, 0, 5, 0, time.UTC)) ||
		round.Kind != string(execution.CompletionPartialGap) || round.ReasonCode != "QUERY_TIMEOUT" ||
		round.SnapshotRevision != "s1" || round.QueryRevision != "q1" || round.ScheduleRevision != "r1" {
		t.Fatalf("restored round = %+v, want every field of the summary mapped", round)
	}
	if restoredRoundOf(nil) != nil {
		t.Fatal("a record without a summary produced a round")
	}
	summary.CompletedAt = "not a clock"
	if round := restoredRoundOf(summary); round == nil || !round.CompletedAt.IsZero() || round.ReasonCode != "QUERY_TIMEOUT" {
		t.Fatalf("restored round with an unparsable clock = %+v, want the clock dropped and the reason kept", round)
	}
}

// The two facts about records come off the record as Slot times, and a zero
// -- a record from before the fields, or one a build without them wrote back
// during a mixed-version roll -- comes off as no time at all, not as the
// epoch: an object restored with "empty since 1970" would be listed on the
// spot with an age of decades.
func TestTheTwoFactsAboutRecordsAreMappedAndZeroIsNotTheEpoch(t *testing.T) {
	record := execution.ScheduleProgress{NextSlot: 1_700_000_120, LastFullSlot: 1_700_000_060,
		LastCompletionKind: execution.CompletionFullEmpty, LastDataSlot: 1_699_990_000, EmptyRunSinceSlot: 1_699_996_400}
	restored := restoredStateOf(record)
	if !restored.LastDataSlot.Equal(time.Unix(1_699_990_000, 0)) || !restored.EmptyRunSince.Equal(time.Unix(1_699_996_400, 0)) ||
		restored.LastCompletion != "FULL_EMPTY_COMPLETED" || !restored.LastFullSlot.Equal(time.Unix(1_700_000_060, 0)) {
		t.Fatalf("restored = %+v, want both facts as the Slots the record names", restored)
	}
	record.LastDataSlot, record.EmptyRunSinceSlot = 0, 0
	restored = restoredStateOf(record)
	if !restored.LastDataSlot.IsZero() || !restored.EmptyRunSince.IsZero() {
		t.Fatalf("restored from a record that names neither = %+v, want both zero, not the epoch", restored)
	}
}

// oneByOne reads a batch the way the tests' restore functions read one
// object: each in turn, a state or an error in its place.
func oneByOne(read func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, error)) func(context.Context,
	[]execution.QueryGroupIdentity) ([]fleet.RestoredState, []error) {
	return func(ctx context.Context, queryGroups []execution.QueryGroupIdentity) ([]fleet.RestoredState, []error) {
		states, errs := make([]fleet.RestoredState, len(queryGroups)), make([]error, len(queryGroups))
		for index, queryGroup := range queryGroups {
			states[index], errs[index] = read(ctx, queryGroup)
		}
		return states, errs
	}
}

// A publish reads every object it wants, up to its budget, in one batch:
// the publish waits on one read, not a round trip per object. The rest are
// the next publish's.
func TestAPublishRestoresItsObjectsInOneRead(t *testing.T) {
	at := time.Now()
	owned := []execution.QueryGroupIdentity{"a", "b", "c", "d", "e"}
	var batches [][]execution.QueryGroupIdentity
	publisher := fleetPublisher{
		tracker:       fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned:         func() []execution.QueryGroupIdentity { return owned },
		now:           func() time.Time { return at },
		restoreBudget: 3, staleAfter: time.Minute,
		restore: func(_ context.Context, queryGroups []execution.QueryGroupIdentity) ([]fleet.RestoredState, []error) {
			batches = append(batches, append([]execution.QueryGroupIdentity(nil), queryGroups...))
			states, errs := make([]fleet.RestoredState, len(queryGroups)), make([]error, len(queryGroups))
			for index, queryGroup := range queryGroups {
				if queryGroup == "b" {
					errs[index] = errors.New("this record could not be read")
					continue
				}
				states[index] = fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}
			}
			return states, errs
		},
	}
	publisher.snapshot(context.Background())
	if len(batches) != 1 || len(batches[0]) != 3 || publisher.tracker.Determined() != 2 {
		t.Fatalf("batches %v determined %d, want one read of three and the two read cleanly restored", batches,
			publisher.tracker.Determined())
	}
	publisher.snapshot(context.Background())
	// The unreadable one is asked again beside the two left over.
	if len(batches) != 2 || len(batches[1]) != 3 || batches[1][0] != "b" || publisher.tracker.Determined() != 4 {
		t.Fatalf("batches %v determined %d, want the one that failed read again with the rest", batches,
			publisher.tracker.Determined())
	}
}

// batchLoader answers a batched Progress read from fixed results.
type batchLoader struct {
	results map[execution.QueryGroupIdentity]execution.ProgressLoadResult
	errs    map[execution.QueryGroupIdentity]error
}

func (loader batchLoader) LoadProgressWithin(_ context.Context, identities []execution.ProgressIdentity, admit func(uint64) bool) (
	[]execution.ProgressLoadResult, []error, int) {
	results, errs := make([]execution.ProgressLoadResult, 0, len(identities)), make([]error, 0, len(identities))
	for _, identity := range identities {
		if !admit(1024) {
			break
		}
		results, errs = append(results, loader.results[identity.QueryGroup]), append(errs, loader.errs[identity.QueryGroup])
	}
	return results, errs, len(results)
}

// The restore source maps one batched read back in order: a record to its
// state, a missing record to nothing restored and no error, and a record
// that could not be read to its own error, the others standing.
func TestTheRestoreSourceReadsABatchInPlace(t *testing.T) {
	record := execution.ScheduleProgress{LastCompletionKind: "FULL_COMPLETED", NextSlot: 1790720040, LastFullSlot: 1790719980}
	everything := func(uint64) bool { return true }
	source := progressRestoreSource(batchLoader{
		results: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{"read": {Status: execution.ProgressFound, Progress: &record},
			"missing": {Status: execution.ProgressMissing}, "invalid": {Status: execution.ProgressFound, Progress: &record}},
		// A record that decoded and did not validate comes with its error.
		errs: map[execution.QueryGroupIdentity]error{"broken": &progress.DeterministicInvalidError{Err: errors.New("undecodable")},
			"invalid": errors.New("does not validate")},
	}, everything)
	states, errs := source(context.Background(), []execution.QueryGroupIdentity{"missing", "broken", "read", "invalid"})
	if len(states) != 4 || len(errs) != 4 || errs[0] != nil || errs[1] == nil || errs[2] != nil || errs[3] == nil {
		t.Fatalf("states %+v errs %v, want an error only in the broken and invalid records' places", states, errs)
	}
	if states[3] != (fleet.RestoredState{}) {
		t.Fatalf("an invalid record restored %+v", states[3])
	}
	if states[0] != (fleet.RestoredState{}) || states[2].LastCompletion != "FULL_COMPLETED" ||
		!states[2].LastFullSlot.Equal(time.Unix(1790719980, 0)) {
		t.Fatalf("states %+v, want nothing for the missing record and the read one mapped", states)
	}
	if progressRestoreSource(nil, everything) != nil {
		t.Fatal("a replica with no Progress store restores")
	}
}

// loadingReply is Redis answering a key with an error while it loads its
// dataset.
type loadingReply string

func (reply loadingReply) Error() string { return string(reply) }
func (loadingReply) RedisError()         {}

// Only a record read and found unusable is a fact about it. Redis answering
// its key with an error, or the read not reaching Redis, is unread: the
// publisher spends no attempt on it.
func TestTheRestoreSourceTellsARecordUnreadFromOneUnusable(t *testing.T) {
	record := execution.ScheduleProgress{LastCompletionKind: "FULL_COMPLETED", NextSlot: 1790720040}
	source := progressRestoreSource(batchLoader{
		results: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{"invalid": {Status: execution.ProgressFound, Progress: &record}},
		errs: map[execution.QueryGroupIdentity]error{
			"loading":   loadingReply("LOADING Redis is loading the dataset in memory"),
			"transport": errors.New("read tcp: connection reset by peer"),
			"undecoded": &progress.DeterministicInvalidError{Err: errors.New("undecodable")},
			"invalid":   errors.New("does not validate"),
		},
	}, func(uint64) bool { return true })
	_, errs := source(context.Background(), []execution.QueryGroupIdentity{"loading", "transport", "undecoded", "invalid"})
	var unread *errRestoreUnread
	for index, want := range []bool{true, true, false, false} {
		if errs[index] == nil || errors.As(errs[index], &unread) != want {
			t.Errorf("error %d %v: unread %v, want %v", index, errs[index], !want, want)
		}
	}
}

// A Redis loading its dataset for three publishes in a row, then back,
// spends none of the objects' attempts: each is restored once it answers.
func TestARestoreThroughARedisStillLoadingRestoresOnceItAnswers(t *testing.T) {
	at := time.Now()
	record := execution.ScheduleProgress{LastCompletionKind: "FULL_COMPLETED", NextSlot: execution.EvaluationTime(at.Unix())}
	loading := true
	reads := 0
	loader := loaderFunc(func(identities []execution.ProgressIdentity) ([]execution.ProgressLoadResult, []error, int) {
		reads++
		results, errs := make([]execution.ProgressLoadResult, len(identities)), make([]error, len(identities))
		for index, identity := range identities {
			if loading {
				errs[index] = loadingReply("LOADING Redis is loading the dataset in memory")
				continue
			}
			found := record
			found.Identity = identity
			results[index] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &found}
		}
		return results, errs, len(identities)
	})
	publisher := fleetPublisher{
		tracker:       fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned:         func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"a", "b"} },
		now:           func() time.Time { return at },
		restoreBudget: 2, staleAfter: time.Minute,
		restore: progressRestoreSource(loader, func(uint64) bool { return true }),
	}
	for publish := 0; publish < fleetRestoreMaxAttempts; publish++ {
		publisher.snapshot(context.Background())
	}
	if publisher.tracker.Determined() != 0 || reads != fleetRestoreMaxAttempts {
		t.Fatalf("determined %d after %d reads while Redis loaded, want nothing restored and a read each publish",
			publisher.tracker.Determined(), reads)
	}
	loading = false
	if got := publisher.snapshot(context.Background()).Determined; got != 2 || reads != fleetRestoreMaxAttempts+1 {
		t.Fatalf("determined %d after %d reads once Redis answered, want both restored", got, reads)
	}
}

// loaderFunc is a batched Progress read answered by a function.
type loaderFunc func([]execution.ProgressIdentity) ([]execution.ProgressLoadResult, []error, int)

func (loader loaderFunc) LoadProgressWithin(_ context.Context, identities []execution.ProgressIdentity, _ func(uint64) bool) (
	[]execution.ProgressLoadResult, []error, int) {
	return loader(identities)
}

// A read that answers for fewer objects than it was asked about restores
// the ones it answered for and leaves the others to be asked again, rather
// than taking the publish down with it.
func TestARestoreReadAnsweringForFewerObjectsLeavesTheRestForLater(t *testing.T) {
	at := time.Now()
	asked := 0
	publisher := fleetPublisher{
		tracker:       fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned:         func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"a", "b"} },
		now:           func() time.Time { return at },
		restoreBudget: 2, staleAfter: time.Minute,
		restore: func(_ context.Context, queryGroups []execution.QueryGroupIdentity) ([]fleet.RestoredState, []error) {
			asked++
			return []fleet.RestoredState{{LastCompletion: "FULL_COMPLETED", NextSlot: at}}, []error{nil}
		},
	}
	publisher.snapshot(context.Background())
	// Not read is no attempt: the object is the next publish's, as often as
	// the memory line leaves it for later.
	if publisher.tracker.Determined() != 1 || !publisher.tracker.WantsRestore("b") || publisher.restoreAttempts["b"] != 0 {
		t.Fatalf("determined %d, attempts on b %d; want the answered object restored and the other still wanted, no attempt spent",
			publisher.tracker.Determined(), publisher.restoreAttempts["b"])
	}
}

// A publish asks for a whole pipeline of objects when that many want
// restoring: the production budget, one pipeline of a batched control read.
func TestAPublishAsksForAWholePipelineOfObjects(t *testing.T) {
	at := time.Now()
	owned := make([]execution.QueryGroupIdentity, 0, 600)
	for index := 0; index < 600; index++ {
		owned = append(owned, execution.QueryGroupIdentity(fmt.Sprintf("qg-%03d", index)))
	}
	var asked []int
	publisher := fleetPublisher{
		tracker:       fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned:         func() []execution.QueryGroupIdentity { return owned },
		now:           func() time.Time { return at },
		restoreBudget: fleetRestoreBudgetPerPublish, staleAfter: time.Minute,
		restore: func(_ context.Context, queryGroups []execution.QueryGroupIdentity) ([]fleet.RestoredState, []error) {
			asked = append(asked, len(queryGroups))
			return make([]fleet.RestoredState, len(queryGroups)), make([]error, len(queryGroups))
		},
	}
	publisher.snapshot(context.Background())
	publisher.snapshot(context.Background())
	if len(asked) != 2 || asked[0] != ownership.ControlReadBatch || asked[1] != 600-ownership.ControlReadBatch {
		t.Fatalf("asked %v, want a whole pipeline and then the rest", asked)
	}
}

// deferringLoader reads every record it is asked for until admit refuses
// one, then stops, as a read under the memory line does.
type deferringLoader struct{ record execution.ScheduleProgress }

func (loader deferringLoader) LoadProgressWithin(_ context.Context, identities []execution.ProgressIdentity, admit func(uint64) bool) (
	[]execution.ProgressLoadResult, []error, int) {
	results, errs := []execution.ProgressLoadResult{}, []error{}
	for _, identity := range identities {
		if !admit(1024) {
			break
		}
		record := loader.record
		record.Identity = identity
		results, errs = append(results, execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &record}), append(errs, nil)
	}
	return results, errs, len(results)
}

// A diagnosis page reads its objects' progress as far as the memory line
// has room, and the objects after the first it refused are deferred, not
// missing and not failed.
func TestADiagnosisDefersTheProgressTheMemoryLineHasNoRoomFor(t *testing.T) {
	room := 3
	read := diagnosisProgress(deferringLoader{record: execution.ScheduleProgress{NextSlot: 120, LastFullSlot: 60}}, func(uint64) bool {
		room--
		return room >= 0
	})
	found, unread, err := read(context.Background(), []string{"a", "b", "c", "d", "e"})
	if err != nil || len(found) != 3 || len(unread) != 2 || unread["d"] != fleet.ProgressDeferred || unread["e"] != fleet.ProgressDeferred {
		t.Fatalf("found %v unread %v error %v, want three read and two deferred", found, unread, err)
	}
}
