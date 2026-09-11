// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// pruneRetention is a production-shaped retention: a Slot of a 60 s Plan at
// evaluation time T has its query deadline at T+60 s less the 5 s reserve,
// may be replayed for ten minutes after that, and is read for a further
// thirty seconds, so its Segment is dead at T+685 s.
var pruneRetention = execution.SlotRetention{QueryReserve: 5 * time.Second, MaxReplayAge: 10 * time.Minute, TerminalDelay: 30 * time.Second}

type cutoverObserver struct {
	mu    sync.Mutex
	facts []observability.ScheduleCutoverFacts
}

func (observer *cutoverObserver) Observe(_ context.Context, observation observability.Observation) {
	if observation.ScheduleCutover == nil {
		return
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.facts = append(observer.facts, *observation.ScheduleCutover)
}

func (observer *cutoverObserver) last(t *testing.T) observability.ScheduleCutoverFacts {
	t.Helper()
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.facts) == 0 {
		t.Fatal("no schedule cutover was observed")
	}
	return observer.facts[len(observer.facts)-1]
}

type pruneHarness struct {
	ctx        context.Context
	client     *redis.Client
	prefix     string
	repository *controlplane.RedisCatalogRepository
	progress   *activationProgressReader
	reconciler *controlplane.ScheduleActivationReconciler
	observer   *cutoverObserver
	at         time.Time
}

func newPruneHarness(t *testing.T, retention *execution.SlotRetention) *pruneHarness {
	t.Helper()
	harness := &pruneHarness{ctx: context.Background(), client: newControlplaneRedis(t), prefix: "alarmd:control:timeline-prune", observer: &cutoverObserver{}}
	repository, err := controlplane.NewRedisCatalogRepository(harness.client, harness.prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repository.ConfigureObserver(harness.observer)
	if retention != nil {
		if err := repository.ConfigureSegmentRetention(*retention); err != nil {
			t.Fatal(err)
		}
	}
	harness.repository = repository
	harness.progress = &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}, errorsByGroup: map[execution.QueryGroupIdentity]error{}}
	compiler, semantics := runtimePlanCompiler(t)
	harness.at = time.Unix(60, 0)
	harness.reconciler, err = controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, harness.progress, func() time.Time { return harness.at })
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

// publishAndActivate publishes one Threshold strategy with the given
// threshold and activates it at boundary, so every call opens one Segment.
func (harness *pruneHarness) publishAndActivate(t *testing.T, threshold int, boundary int64) execution.QueryGroupIdentity {
	t.Helper()
	catalog := validCatalog(t, threshold)
	snapshot, _, err := harness.repository.PublishCatalog(harness.ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	harness.at = time.Unix(boundary, 0)
	if _, err := harness.reconciler.Ensure(harness.ctx, snapshot.Publication); err != nil {
		t.Fatalf("activation at %d: %v", boundary, err)
	}
	return catalog.QueryGroups[0].Identity
}

func (harness *pruneHarness) setProgress(queryGroup execution.QueryGroupIdentity, nextSlot execution.EvaluationTime) {
	harness.progress.byGroup[queryGroup] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: queryGroup}, NextSlot: nextSlot, LastFullSlot: nextSlot - 60,
		LastCompletionKind: execution.CompletionFull,
	}}
}

type timelineShape struct {
	bytes                 int64
	segments              []execution.ScheduleSegmentFact
	firstReactivatedAfter *execution.EvaluationTime
	lastReactivatedAfter  *execution.EvaluationTime
}

func (harness *pruneHarness) timeline(t *testing.T, queryGroup execution.QueryGroupIdentity) timelineShape {
	t.Helper()
	key := harness.prefix + ":schedule_timeline:" + string(queryGroup)
	raw, err := harness.client.Get(harness.ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Segments []struct {
			Schedule struct {
				Segment execution.ScheduleSegmentFact `json:"Segment"`
			} `json:"schedule"`
			ReactivatedAfter *execution.EvaluationTime `json:"reactivated_after"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	shape := timelineShape{bytes: int64(len(raw))}
	for index, segment := range persisted.Segments {
		shape.segments = append(shape.segments, segment.Schedule.Segment)
		if index == 0 {
			shape.firstReactivatedAfter = segment.ReactivatedAfter
		}
		if index == len(persisted.Segments)-1 {
			shape.lastReactivatedAfter = segment.ReactivatedAfter
		}
	}
	return shape
}

// TestPublicationCutoverBoundsTimelineToSegmentsStillRead is the growth
// bound: with the Worker keeping pace, the timeline holds only the Segments
// whose Slots the scheduler may still read, however many publications have
// happened. Without pruning every publication adds a Segment for good, so
// the assertion on the last publications fails on the previous behaviour.
func TestPublicationCutoverBoundsTimelineToSegmentsStillRead(t *testing.T) {
	harness := newPruneHarness(t, &pruneRetention)
	var queryGroup execution.QueryGroupIdentity
	var sizes []int64
	var counts []int
	for index := 0; index < 30; index++ {
		boundary := int64(60 + 60*index)
		if queryGroup != "" {
			// The Worker runs one Slot behind the boundary.
			harness.setProgress(queryGroup, execution.EvaluationTime(boundary-60))
		}
		queryGroup = harness.publishAndActivate(t, 80+index, boundary)
		if index == 0 {
			harness.setProgress(queryGroup, 60)
		}
		shape := harness.timeline(t, queryGroup)
		sizes, counts = append(sizes, shape.bytes), append(counts, len(shape.segments))
	}
	// A Segment [T, T+60) dies at T+685 s, so at boundary B every Segment
	// ending at or before B-625 is dead; with the Worker at B-60 the floor
	// never binds. That leaves eleven closed Segments plus the open one.
	for index := 14; index < len(counts); index++ {
		if counts[index] != 12 {
			t.Fatalf("publication %d kept %d Segments, want 12 (sizes=%v counts=%v)", index+1, counts[index], sizes, counts)
		}
		if sizes[index] > sizes[13]+64 {
			t.Fatalf("timeline keeps growing after the bound is reached: sizes=%v", sizes)
		}
	}
	shape := harness.timeline(t, queryGroup)
	if first := shape.segments[0]; first.Start != 60*(30-11) {
		t.Fatalf("first retained Segment starts at %d, want %d: %+v", first.Start, 60*(30-11), shape.segments)
	}
	facts := harness.observer.last(t)
	if facts.Result != "success" || facts.SegmentsPruned != 1 || facts.Timelines != 1 || facts.MaxTimelineBytes != int(shape.bytes) || facts.PayloadBytes <= facts.MaxTimelineBytes {
		t.Fatalf("cutover facts=%+v, timeline bytes=%d", facts, shape.bytes)
	}
}

// TestPublicationCutoverKeepsSegmentsTheWorkerWillStillRead is the
// non-destructiveness proof, on Slots rather than on ages: a Segment dead by
// age but holding the Worker's next Slot stays; a Segment whose Slot is still
// inside the replay window stays; and when Progress cannot be read nothing
// is pruned at all.
func TestPublicationCutoverKeepsSegmentsTheWorkerWillStillRead(t *testing.T) {
	t.Run("progress floor keeps a dead segment the worker still reads", func(t *testing.T) {
		harness := newPruneHarness(t, &pruneRetention)
		queryGroup := harness.publishAndActivate(t, 80, 60)
		// The Worker never got past the first Slot.
		harness.setProgress(queryGroup, 60)
		for index := 1; index < 20; index++ {
			harness.publishAndActivate(t, 80+index, int64(60+60*index))
		}
		shape := harness.timeline(t, queryGroup)
		if len(shape.segments) != 20 || shape.segments[0].Start != 60 {
			t.Fatalf("segments the worker still reads were pruned: %+v", shape.segments)
		}
		facts := harness.observer.last(t)
		if facts.SegmentsPruned != 0 || len(facts.PrunesSkipped) != 0 {
			t.Fatalf("cutover facts=%+v", facts)
		}
		// The Worker moves on to Slot 600; every Segment ending at or before
		// it is dropped, the one holding Slot 600 is not.
		harness.setProgress(queryGroup, 600)
		harness.publishAndActivate(t, 100, 1260)
		shape = harness.timeline(t, queryGroup)
		if shape.segments[0].Start != 600 {
			t.Fatalf("first retained Segment starts at %d, want 600: %+v", shape.segments[0].Start, shape.segments)
		}
		if facts := harness.observer.last(t); facts.SegmentsPruned != 9 {
			t.Fatalf("cutover facts=%+v", facts)
		}
	})
	t.Run("a slot inside the replay window keeps its segment whatever progress says", func(t *testing.T) {
		harness := newPruneHarness(t, &pruneRetention)
		queryGroup := harness.publishAndActivate(t, 80, 60)
		harness.setProgress(queryGroup, 60)
		for index := 1; index < 12; index++ {
			harness.setProgress(queryGroup, execution.EvaluationTime(60+60*index))
			harness.publishAndActivate(t, 80+index, int64(60+60*index))
		}
		// Progress claims the Worker is far ahead, so the floor binds nothing.
		// At 780 s only the Slot at 60 s is past its keep-until (745 s); the
		// Slot at 120 s may still be replayed until 805 s, and every later
		// one longer. The scheduler would still run them, so their Segments
		// stay whatever Progress says.
		harness.setProgress(queryGroup, 100000)
		harness.publishAndActivate(t, 200, 780)
		shape := harness.timeline(t, queryGroup)
		if len(shape.segments) != 12 || shape.segments[0].Start != 120 {
			t.Fatalf("Segments with replayable Slots were pruned, or the dead one was kept: %+v", shape.segments)
		}
		if facts := harness.observer.last(t); facts.SegmentsPruned != 1 {
			t.Fatalf("cutover facts=%+v", facts)
		}
	})
	t.Run("unreadable progress prunes nothing this cutover", func(t *testing.T) {
		harness := newPruneHarness(t, &pruneRetention)
		queryGroup := harness.publishAndActivate(t, 80, 60)
		harness.setProgress(queryGroup, 60)
		for index := 1; index < 20; index++ {
			harness.setProgress(queryGroup, execution.EvaluationTime(60+60*index))
			harness.publishAndActivate(t, 80+index, int64(60+60*index))
		}
		before := harness.timeline(t, queryGroup)
		harness.progress.errorsByGroup[queryGroup] = errors.New("progress store unreachable")
		harness.publishAndActivate(t, 100, 1260)
		after := harness.timeline(t, queryGroup)
		if len(after.segments) != len(before.segments)+1 || after.segments[0].Start != before.segments[0].Start {
			t.Fatalf("segments changed while Progress was unreadable: before=%+v after=%+v", before.segments, after.segments)
		}
		facts := harness.observer.last(t)
		if facts.Result != "success" || facts.SegmentsPruned != 0 || facts.PrunesSkipped["progress_unavailable"] != 1 {
			t.Fatalf("cutover facts=%+v", facts)
		}
		delete(harness.progress.errorsByGroup, queryGroup)
		delete(harness.progress.byGroup, queryGroup)
		harness.progress.byGroup[queryGroup] = execution.ProgressLoadResult{Status: execution.ProgressMissing}
		harness.publishAndActivate(t, 101, 1320)
		if facts := harness.observer.last(t); facts.SegmentsPruned != 0 || facts.PrunesSkipped["progress_missing"] != 1 {
			t.Fatalf("cutover facts=%+v", facts)
		}
	})
	t.Run("pruning past a reactivation tombstone leaves a timeline the validator accepts", func(t *testing.T) {
		harness := newPruneHarness(t, &pruneRetention)
		// Query Group A runs alone, is retired when only B is published, and
		// returns while still draining: its timeline then carries a Segment
		// with a reactivation tombstone. Pruning must eventually drop the
		// Segment before the tombstone and clear it, because a timeline
		// whose first Segment carries one is invalid to every binary.
		a := harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 80, true, false), 60)
		harness.setProgress(a, 120)
		harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 80, false, true), 120)
		harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 81, true, true), 180)
		shape := harness.timeline(t, a)
		if len(shape.segments) != 2 || shape.segments[1].Start != 180 || shape.lastReactivatedAfter == nil || *shape.lastReactivatedAfter != 120 {
			t.Fatalf("reactivation did not append a tombstoned Segment: %+v after=%v", shape.segments, shape.lastReactivatedAfter)
		}
		// At 780 s the Segment [60,120) is dead (745 s) while the tombstoned
		// one [180,240) is not (865 s): the tombstone becomes the first
		// Segment and must be cleared for the activation to be valid.
		for index := 0; index < 10; index++ {
			boundary := int64(240 + 60*index)
			harness.setProgress(a, execution.EvaluationTime(boundary-60))
			harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 82+index, true, true), boundary)
		}
		shape = harness.timeline(t, a)
		if shape.segments[0].Start != 180 || shape.firstReactivatedAfter != nil {
			t.Fatalf("the Segment before the tombstone was not pruned, or the tombstone survived as the first Segment: %+v after=%v", shape.segments, shape.firstReactivatedAfter)
		}
		// Two publications later the once-tombstoned Segment is dead too.
		for index := 10; index < 12; index++ {
			boundary := int64(240 + 60*index)
			harness.setProgress(a, execution.EvaluationTime(boundary-60))
			harness.publishAndActivateCatalog(t, twoGroupCatalog(t, 82+index, true, true), boundary)
		}
		if shape = harness.timeline(t, a); shape.segments[0].Start != 240 {
			t.Fatalf("first retained Segment starts at %d, want 240: %+v", shape.segments[0].Start, shape.segments)
		}
	})
}

// twoGroupCatalog builds a catalog holding Query Group A (the shared fixture
// strategy on business 2) and/or Query Group B (the same strategy shape on
// business 3), so a publication can retire and bring back A.
func twoGroupCatalog(t *testing.T, threshold int, includeA, includeB bool) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	var strategies []controlplane.SourceStrategy
	planner := &perBusinessPlanner{facts: map[string]execution.QueryPlanFacts{}}
	if includeA {
		document := []byte(strings.Replace(string(documents[0]), `"threshold":80`, `"threshold":`+strconv.Itoa(threshold), 1))
		strategies = append(strategies, controlplane.SourceStrategy{SourceID: "1001", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}})
		planner.facts["2"] = queryFactsFor(t, "2", "bkcc__2")
	}
	if includeB {
		var decoded map[string]any
		if err := json.Unmarshal(withWireIdentity(t, documents[1], "tenant-a", "bkcc__3"), &decoded); err != nil {
			t.Fatal(err)
		}
		decoded["bk_biz_id"] = float64(3)
		document, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		strategies = append(strategies, controlplane.SourceStrategy{SourceID: "1002", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}})
		planner.facts["3"] = queryFactsFor(t, "3", "bkcc__3")
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: strategies, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != len(strategies) {
		t.Fatalf("expected %d Query Groups, got %d with dispositions %+v", len(strategies), len(catalog.QueryGroups), catalog.Dispositions)
	}
	return catalog
}

// publishAndActivateCatalog publishes and activates a prepared catalog and
// returns the identity of the Query Group on business 2 when present.
func (harness *pruneHarness) publishAndActivateCatalog(t *testing.T, catalog controlplane.Catalog, boundary int64) execution.QueryGroupIdentity {
	t.Helper()
	snapshot, _, err := harness.repository.PublishCatalog(harness.ctx, catalog)
	if err != nil {
		t.Fatal(err)
	}
	harness.at = time.Unix(boundary, 0)
	if _, err := harness.reconciler.Ensure(harness.ctx, snapshot.Publication); err != nil {
		t.Fatalf("activation at %d: %v", boundary, err)
	}
	for _, group := range catalog.QueryGroups {
		if group.QueryPlan.BusinessID == "2" {
			return group.Identity
		}
	}
	return ""
}

type perBusinessPlanner struct {
	facts map[string]execution.QueryPlanFacts
}

func (planner *perBusinessPlanner) CompilePrimaryQuery(_ context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
	facts, ok := planner.facts[source.Identity.BusinessID]
	if !ok {
		return execution.QueryPlanFacts{}, errors.New("no query facts for business")
	}
	return facts, nil
}

// TestCutoverFenceDistinguishesAbsentKeyFromChangedAndEmptyContent covers
// the two branches of the fence the cutover script applies per timeline:
// an empty expectation demands an absent key, so a key holding empty content
// is a conflict; a digest expectation demands the exact bytes, so a timeline
// changed between the read and the write is a conflict.
func TestCutoverFenceDistinguishesAbsentKeyFromChangedAndEmptyContent(t *testing.T) {
	t.Run("empty content under a new Query Group's key is a conflict, not an absent key", func(t *testing.T) {
		ctx := context.Background()
		client := newControlplaneRedis(t)
		prefix := "alarmd:control:fence-empty"
		repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
		compiler, semantics := runtimePlanCompiler(t)
		at := time.Unix(60, 0)
		reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		first := twoGroupCatalog(t, 80, true, false)
		snapshot, _, err := repository.PublishCatalog(ctx, first)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
			t.Fatal(err)
		}
		both := twoGroupCatalog(t, 80, true, true)
		var added execution.QueryGroupIdentity
		for _, group := range both.QueryGroups {
			if group.Identity != first.QueryGroups[0].Identity {
				added = group.Identity
			}
		}
		if added == "" {
			t.Fatal("the second catalog adds no Query Group")
		}
		if err := client.Set(ctx, prefix+":schedule_timeline:"+string(added), "", 0).Err(); err != nil {
			t.Fatal(err)
		}
		snapshot, _, err = repository.PublishCatalog(ctx, both)
		if err != nil {
			t.Fatal(err)
		}
		at = time.Unix(120, 0)
		_, err = reconciler.Ensure(ctx, snapshot.Publication)
		failure, _ := controlplane.ActivationFailureFromError(err)
		if !errors.Is(err, controlplane.ErrActivationConflict) || failure.Class != controlplane.ActivationFailureClassCASConflict {
			t.Fatalf("activation over an existing empty key: err=%v failure=%+v", err, failure)
		}
	})
	t.Run("a timeline changed between read and write is a conflict", func(t *testing.T) {
		ctx := context.Background()
		client := newControlplaneRedis(t)
		prefix := "alarmd:control:fence-changed"
		repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}}
		compiler, semantics := runtimePlanCompiler(t)
		at := time.Unix(60, 0)
		reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, progress, func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		catalog := validCatalog(t, 80)
		snapshot, _, err := repository.PublishCatalog(ctx, catalog)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
			t.Fatal(err)
		}
		key := prefix + ":schedule_timeline:" + string(catalog.QueryGroups[0].Identity)
		snapshot, _, err = repository.PublishCatalog(ctx, validCatalog(t, 81))
		if err != nil {
			t.Fatal(err)
		}
		// Publication is done, so the next EVAL this client sends is the
		// cutover; the hook changes the timeline between the leader's read
		// and its write.
		auxiliary := redis.NewClient(&redis.Options{Addr: client.Options().Addr})
		t.Cleanup(func() { _ = auxiliary.Close() })
		client.AddHook(&beforeEvalHook{run: func() error {
			// The same JSON with one extra trailing space decodes the same and
			// hashes differently.
			current, err := auxiliary.Get(ctx, key).Result()
			if err != nil {
				return err
			}
			return auxiliary.Set(ctx, key, current+" ", 0).Err()
		}})
		at = time.Unix(120, 0)
		_, err = reconciler.Ensure(ctx, snapshot.Publication)
		if !errors.Is(err, controlplane.ErrActivationConflict) {
			t.Fatalf("activation over a changed timeline: err=%v", err)
		}
	})
}

// batchedProgressReader offers the batched read on top of the per-Query
// Group fixture and counts how it was called.
type batchedProgressReader struct {
	*activationProgressReader
	batches int
	singles int
	sizes   []int
}

func (reader *batchedProgressReader) LoadProgress(ctx context.Context, identity execution.ProgressIdentity) (execution.ProgressLoadResult, error) {
	reader.singles++
	return reader.activationProgressReader.LoadProgress(ctx, identity)
}

func (reader *batchedProgressReader) LoadProgressBatch(ctx context.Context, identities []execution.ProgressIdentity) ([]execution.ProgressLoadResult, []error) {
	reader.batches++
	reader.sizes = append(reader.sizes, len(identities))
	results := make([]execution.ProgressLoadResult, len(identities))
	errs := make([]error, len(identities))
	for index, identity := range identities {
		results[index], errs[index] = reader.activationProgressReader.LoadProgress(ctx, identity)
	}
	return results, errs
}

// TestPublicationCutoverReadsProgressInOneBatch: with two Query Groups both
// carrying dead Segments, a cutover asks the Progress reader once for both
// and never one at a time; a per-entry failure leaves only that Query
// Group's timeline unpruned.
func TestPublicationCutoverReadsProgressInOneBatch(t *testing.T) {
	ctx := context.Background()
	client := newControlplaneRedis(t)
	prefix := "alarmd:control:timeline-prune-batch"
	observer := &cutoverObserver{}
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	repository.ConfigureObserver(observer)
	if err := repository.ConfigureSegmentRetention(pruneRetention); err != nil {
		t.Fatal(err)
	}
	reader := &batchedProgressReader{activationProgressReader: &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{}, errorsByGroup: map[execution.QueryGroupIdentity]error{}}}
	compiler, semantics := runtimePlanCompiler(t)
	at := time.Unix(60, 0)
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(repository, compiler, semantics, reader, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	var a, b execution.QueryGroupIdentity
	activate := func(threshold int, boundary int64) {
		t.Helper()
		catalog := twoGroupCatalog(t, threshold, true, true)
		for _, group := range catalog.QueryGroups {
			if group.QueryPlan.BusinessID == "2" {
				a = group.Identity
			} else {
				b = group.Identity
			}
		}
		snapshot, _, err := repository.PublishCatalog(ctx, catalog)
		if err != nil {
			t.Fatal(err)
		}
		at = time.Unix(boundary, 0)
		if _, err := reconciler.Ensure(ctx, snapshot.Publication); err != nil {
			t.Fatalf("activation at %d: %v", boundary, err)
		}
	}
	activate(80, 60)
	progressAt := func(queryGroup execution.QueryGroupIdentity, nextSlot execution.EvaluationTime) {
		reader.byGroup[queryGroup] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
			Identity: execution.ProgressIdentity{QueryGroup: queryGroup}, NextSlot: nextSlot, LastFullSlot: nextSlot - 60, LastCompletionKind: execution.CompletionFull,
		}}
	}
	for index := 1; index < 14; index++ {
		boundary := int64(60 + 60*index)
		progressAt(a, execution.EvaluationTime(boundary))
		progressAt(b, execution.EvaluationTime(boundary))
		activate(80+index, boundary)
	}
	// From 780 s on every cutover finds a dead Segment on both timelines.
	if reader.singles != 0 || reader.batches == 0 {
		t.Fatalf("progress was read one at a time: singles=%d batches=%d", reader.singles, reader.batches)
	}
	for _, size := range reader.sizes {
		if size != 2 {
			t.Fatalf("a cutover batched %d Query Groups, want both: %v", size, reader.sizes)
		}
	}
	batchesBefore := reader.batches
	reader.errorsByGroup[b] = errors.New("progress store unreachable")
	progressAt(a, 900)
	activate(100, 900)
	if reader.batches != batchesBefore+1 || reader.singles != 0 {
		t.Fatalf("progress reads after a per-entry failure: singles=%d batches=%d", reader.singles, reader.batches)
	}
	facts := observer.last(t)
	if facts.Result != "success" || facts.SegmentsPruned == 0 || facts.PrunesSkipped["progress_unavailable"] != 1 {
		t.Fatalf("cutover facts=%+v", facts)
	}
	key := func(queryGroup execution.QueryGroupIdentity) string {
		return prefix + ":schedule_timeline:" + string(queryGroup)
	}
	var timelineA, timelineB struct {
		Segments []json.RawMessage `json:"segments"`
	}
	rawA, _ := client.Get(ctx, key(a)).Bytes()
	rawB, _ := client.Get(ctx, key(b)).Bytes()
	if err := json.Unmarshal(rawA, &timelineA); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawB, &timelineB); err != nil {
		t.Fatal(err)
	}
	if len(timelineA.Segments) >= len(timelineB.Segments) {
		t.Fatalf("the Query Group whose Progress failed must keep every Segment: a=%d b=%d", len(timelineA.Segments), len(timelineB.Segments))
	}
}
