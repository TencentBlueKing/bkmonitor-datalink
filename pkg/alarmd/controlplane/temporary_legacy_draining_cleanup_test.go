// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const temporaryLegacyRuntimeScopeDigest = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

type temporaryLegacyProgressReader struct {
	facts  map[execution.QueryGroupIdentity]controlplane.TemporaryLegacyDrainingProgressFact
	errors map[execution.QueryGroupIdentity]error
}

func (reader *temporaryLegacyProgressReader) LoadTemporaryLegacyDrainingProgress(
	_ context.Context,
	identity execution.ProgressIdentity,
) (controlplane.TemporaryLegacyDrainingProgressFact, error) {
	if err := reader.errors[identity.QueryGroup]; err != nil {
		return controlplane.TemporaryLegacyDrainingProgressFact{}, err
	}
	return reader.facts[identity.QueryGroup], nil
}

func (reader *temporaryLegacyProgressReader) LoadProgress(
	_ context.Context,
	identity execution.ProgressIdentity,
) (execution.ProgressLoadResult, error) {
	if err := reader.errors[identity.QueryGroup]; err != nil {
		return execution.ProgressLoadResult{}, err
	}
	return reader.facts[identity.QueryGroup].Load, nil
}

func TestTemporaryLegacyDrainingCleanupDryRunAndApplyOneAtomicTransition(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:temporary-legacy-draining-cleanup"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	progressReader := &temporaryLegacyProgressReader{facts: make(map[execution.QueryGroupIdentity]controlplane.TemporaryLegacyDrainingProgressFact)}
	clock := []time.Time{time.Unix(60, 0), time.Unix(180, 0)}
	clockCall := 0
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, semantics, progressReader, func() time.Time {
			at := clock[clockCall]
			clockCall++
			return at
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	initialCatalog := twoQueryGroupCatalog(t)
	initialCatalog = catalogWithAllSchedules(t, initialCatalog, 60, 0)
	initialSnapshot, _, err := repository.PublishCatalog(ctx, initialCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, initialSnapshot.Publication); err != nil {
		t.Fatal(err)
	}
	emptyCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{}}
	emptyCatalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", emptyCatalog.QueryGroups))
	emptySnapshot, _, err := repository.PublishCatalog(ctx, emptyCatalog)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := reconciler.Ensure(ctx, emptySnapshot.Publication)
	if err != nil || len(retired.Draining) != 2 {
		t.Fatalf("retirement=(%#v,%v)", retired, err)
	}

	candidateCatalog := twoQueryGroupCatalog(t)
	candidateCatalog = catalogWithAllSchedules(t, candidateCatalog, 60, 0)
	candidateSnapshot, _, err := repository.PublishCatalog(ctx, candidateCatalog)
	if err != nil {
		t.Fatal(err)
	}
	targets := make([]controlplane.TemporaryLegacyDrainingTarget, 0, len(retired.Draining))
	progressBefore := make(map[string][]byte)
	for index, draining := range retired.Draining {
		key := prefix + ":test-progress:" + string(draining.QueryGroup)
		raw := []byte{byte(index + 1), 0x7f}
		if err := client.Set(ctx, key, raw, 0).Err(); err != nil {
			t.Fatal(err)
		}
		progressBefore[key] = append([]byte(nil), raw...)
		progressReader.facts[draining.QueryGroup] = controlplane.TemporaryLegacyDrainingProgressFact{
			RedisKey: key,
			Raw:      raw,
			Load: execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
				Identity: execution.ProgressIdentity{QueryGroup: draining.QueryGroup},
				NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
			}},
		}
		targets = append(targets, controlplane.TemporaryLegacyDrainingTarget{
			QueryGroup: draining.QueryGroup, RetiredBoundary: 180, ProgressNextSlot: 120,
		})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].QueryGroup < targets[j].QueryGroup })
	request := controlplane.TemporaryLegacyDrainingCleanupRequest{
		SchemaVersion:              controlplane.TemporaryLegacyDrainingCleanupSchemaVersion,
		ExpectedActivationRevision: retired.RecordRevision,
		ExpectedCurrent:            emptySnapshot.Publication,
		ExpectedCandidate:          candidateSnapshot.Publication,
		ExpectedActiveQGSetRef:     retired.ActiveQGSetRef,
		CutoverBoundary:            240,
		Targets:                    targets,
	}
	cleanup, err := controlplane.NewTemporaryLegacyDrainingCleanup(
		repository, compiler, semantics, progressReader, temporaryLegacyRuntimeScopeDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := cleanup.DryRun(ctx, request)
	if err != nil || len(plan.Digest) != 64 || len(plan.Targets) != 2 {
		t.Fatalf("DryRun()=(%#v,%v)", plan, err)
	}
	repeated, err := cleanup.DryRun(ctx, request)
	if err != nil || repeated.Digest != plan.Digest {
		t.Fatalf("DryRun(repeated)=(%#v,%v), want digest %s", repeated, err, plan.Digest)
	}
	hook := &temporaryLegacyEvalHook{}
	client.AddHook(hook)
	if _, err := cleanup.Apply(ctx, request, plan.Digest); err != nil {
		t.Fatal(err)
	}
	if hook.evalCalls != 1 || hook.temporaryCalls != 1 {
		t.Fatalf("Apply() eval calls=(all=%d temporary=%d), want one temporary EVAL", hook.evalCalls, hook.temporaryCalls)
	}

	active, err := repository.LoadActivation(ctx)
	if err != nil || active.Current != candidateSnapshot.Publication || len(active.Draining) != 0 || active.ActiveQGSetRef.QGCount != 2 {
		t.Fatalf("Activation after cleanup=(%#v,%v)", active, err)
	}
	groups, err := repository.LoadActiveQueryGroupSet(ctx, active.ActiveQGSetRef)
	if err != nil || len(groups) != 2 {
		t.Fatalf("ActiveQGSet after cleanup=(%#v,%v)", groups, err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		oldSchedule, err := runtime.ReadFrozenSchedule(ctx, target.QueryGroup, 60)
		if err != nil || oldSchedule.Segment.End == nil || *oldSchedule.Segment.End != target.ProgressNextSlot {
			t.Fatalf("old Schedule %s=(%#v,%v)", target.QueryGroup, oldSchedule, err)
		}
		newSchedule, err := runtime.ReadFrozenSchedule(ctx, target.QueryGroup, request.CutoverBoundary)
		if err != nil || newSchedule.Segment.Start != request.CutoverBoundary || newSchedule.Segment.Publication != (execution.SnapshotPublicationRef{
			SnapshotRevision: request.ExpectedCandidate.SnapshotRevision,
			PublicationEpoch: execution.PublicationEpoch(request.ExpectedCandidate.PublicationEpoch),
		}) {
			t.Fatalf("new Schedule %s=(%#v,%v)", target.QueryGroup, newSchedule, err)
		}
		got, err := client.Get(ctx, progressReader.facts[target.QueryGroup].RedisKey).Bytes()
		if err != nil || !reflect.DeepEqual(got, progressBefore[progressReader.facts[target.QueryGroup].RedisKey]) {
			t.Fatalf("Progress %s changed: got=%x err=%v", target.QueryGroup, got, err)
		}
	}
}

func catalogWithAllSchedules(
	t *testing.T,
	source controlplane.Catalog,
	interval int64,
	alignment execution.EvaluationTime,
) controlplane.Catalog {
	t.Helper()
	result := source
	result.QueryGroups = append([]controlplane.QueryGroup(nil), source.QueryGroups...)
	for groupIndex := range result.QueryGroups {
		groupCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{result.QueryGroups[groupIndex]}}
		groupCatalog = catalogWithSchedule(t, groupCatalog, interval, alignment)
		result.QueryGroups[groupIndex] = groupCatalog.QueryGroups[0]
	}
	result.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", result.QueryGroups))
	return result
}

type temporaryLegacyCleanupFixture struct {
	client         *redis.Client
	repository     *controlplane.RedisCatalogRepository
	cleanup        *controlplane.TemporaryLegacyDrainingCleanup
	request        controlplane.TemporaryLegacyDrainingCleanupRequest
	plan           controlplane.TemporaryLegacyDrainingCleanupPlan
	prefix         string
	progressReader *temporaryLegacyProgressReader
}

func newTemporaryLegacyCleanupFixture(t *testing.T) temporaryLegacyCleanupFixture {
	t.Helper()
	return newTemporaryLegacyCleanupFixtureWithCatalog(t, twoQueryGroupCatalog(t), true, 0)
}

func newTemporaryLegacyCleanupFixtureWithCatalog(
	t *testing.T,
	source controlplane.Catalog,
	addAbsentDraining bool,
	nonTargetNextSlot execution.EvaluationTime,
) temporaryLegacyCleanupFixture {
	t.Helper()
	client := newControlplaneRedis(t)
	ctx := context.Background()
	prefix := "alarmd:control:temporary-legacy-draining-fixture"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	progressReader := &temporaryLegacyProgressReader{facts: make(map[execution.QueryGroupIdentity]controlplane.TemporaryLegacyDrainingProgressFact)}
	clock := []time.Time{time.Unix(60, 0), time.Unix(180, 0)}
	clockCall := 0
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, semantics, progressReader, func() time.Time {
			at := clock[clockCall]
			clockCall++
			return at
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	initialCatalog := catalogWithAllSchedules(t, source, 60, 0)
	initialSnapshot, _, err := repository.PublishCatalog(ctx, initialCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(ctx, initialSnapshot.Publication); err != nil {
		t.Fatal(err)
	}
	emptyCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{}}
	emptyCatalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", emptyCatalog.QueryGroups))
	emptySnapshot, _, err := repository.PublishCatalog(ctx, emptyCatalog)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := reconciler.Ensure(ctx, emptySnapshot.Publication)
	if err != nil || len(retired.Draining) < 2 {
		t.Fatalf("retirement=(%#v,%v)", retired, err)
	}
	orderedDraining := append([]controlplane.DrainingQueryGroup(nil), retired.Draining...)
	sort.Slice(orderedDraining, func(i, j int) bool { return orderedDraining[i].QueryGroup < orderedDraining[j].QueryGroup })
	legacyTargets := orderedDraining[:2]
	if addAbsentDraining {
		retired.Draining = append(retired.Draining, controlplane.DrainingQueryGroup{
			QueryGroup: execution.QueryGroupIdentity(strings.Repeat("c", 64)), RetiredBoundary: 180,
		})
		retiredRaw, marshalErr := json.Marshal(retired)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := client.Set(ctx, prefix+":activation", retiredRaw, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	candidateSnapshot, _, err := repository.PublishCatalog(ctx, initialCatalog)
	if err != nil {
		t.Fatal(err)
	}
	targets := make([]controlplane.TemporaryLegacyDrainingTarget, 0, 2)
	for index, draining := range legacyTargets {
		key := prefix + ":test-progress:" + string(draining.QueryGroup)
		raw := []byte{byte(index + 1), 0x7f}
		if err := client.Set(ctx, key, raw, 0).Err(); err != nil {
			t.Fatal(err)
		}
		progressReader.facts[draining.QueryGroup] = controlplane.TemporaryLegacyDrainingProgressFact{
			RedisKey: key, Raw: raw,
			Load: execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
				Identity: execution.ProgressIdentity{QueryGroup: draining.QueryGroup},
				NextSlot: 120, LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
			}},
		}
		targets = append(targets, controlplane.TemporaryLegacyDrainingTarget{
			QueryGroup: draining.QueryGroup, RetiredBoundary: 180, ProgressNextSlot: 120,
		})
	}
	for _, draining := range orderedDraining[2:] {
		progressReader.facts[draining.QueryGroup] = controlplane.TemporaryLegacyDrainingProgressFact{
			Load: execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
				Identity: execution.ProgressIdentity{QueryGroup: draining.QueryGroup},
				NextSlot: nonTargetNextSlot, LastFullSlot: 120, LastCompletionKind: execution.CompletionFull,
			}},
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].QueryGroup < targets[j].QueryGroup })
	request := controlplane.TemporaryLegacyDrainingCleanupRequest{
		SchemaVersion:              controlplane.TemporaryLegacyDrainingCleanupSchemaVersion,
		ExpectedActivationRevision: retired.RecordRevision,
		ExpectedCurrent:            emptySnapshot.Publication,
		ExpectedCandidate:          candidateSnapshot.Publication,
		ExpectedActiveQGSetRef:     retired.ActiveQGSetRef,
		CutoverBoundary:            240,
		Targets:                    targets,
	}
	cleanup, err := controlplane.NewTemporaryLegacyDrainingCleanup(
		repository, compiler, semantics, progressReader, temporaryLegacyRuntimeScopeDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := cleanup.DryRun(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return temporaryLegacyCleanupFixture{client: client, repository: repository, cleanup: cleanup,
		request: request, plan: plan, prefix: prefix, progressReader: progressReader}
}

func TestTemporaryLegacyDrainingCleanupRemovesOnlyExplicitTargets(t *testing.T) {
	fixture := newTemporaryLegacyCleanupFixture(t)
	result, err := fixture.cleanup.Apply(context.Background(), fixture.request, fixture.plan.Digest)
	if err != nil || result.Status != controlplane.TemporaryLegacyDrainingCleanupApplied {
		t.Fatalf("Apply()=(%#v,%v)", result, err)
	}
	activation, err := fixture.repository.LoadActivation(context.Background())
	if err != nil || len(activation.Draining) != 1 || activation.Draining[0].QueryGroup != execution.QueryGroupIdentity(strings.Repeat("c", 64)) {
		t.Fatalf("non-target Draining after Apply=(%#v,%v)", activation.Draining, err)
	}
}

func TestTemporaryLegacyDrainingCleanupReactivatesNormallyDrainedNonTarget(t *testing.T) {
	fixture := newTemporaryLegacyCleanupFixtureWithCatalog(t, threeQueryGroupCatalog(t), false, 180)
	nonTarget := temporaryLegacyNonTargetQueryGroup(t, fixture)
	result, err := fixture.cleanup.Apply(context.Background(), fixture.request, fixture.plan.Digest)
	if err != nil || result.Status != controlplane.TemporaryLegacyDrainingCleanupApplied {
		t.Fatalf("Apply()=(%#v,%v)", result, err)
	}
	activation, err := fixture.repository.LoadActivation(context.Background())
	if err != nil || len(activation.Draining) != 0 || activation.ActiveQGSetRef.QGCount != 3 {
		t.Fatalf("Activation after normal non-target reactivation=(%#v,%v)", activation, err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(fixture.repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	oldSchedule, err := runtime.ReadFrozenSchedule(context.Background(), nonTarget, 120)
	if err != nil || oldSchedule.Segment.End == nil || *oldSchedule.Segment.End != 180 {
		t.Fatalf("normal non-target old Schedule=(%#v,%v), want retirement B=180", oldSchedule, err)
	}
	newSchedule, err := runtime.ReadFrozenSchedule(context.Background(), nonTarget, fixture.request.CutoverBoundary)
	if err != nil || newSchedule.Segment.Start != fixture.request.CutoverBoundary {
		t.Fatalf("normal non-target new Schedule=(%#v,%v)", newSchedule, err)
	}
}

func TestTemporaryLegacyDrainingCleanupFailsClosedForUnsafeNonTarget(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*temporaryLegacyCleanupFixture, execution.QueryGroupIdentity)
	}{
		{name: "not drained", mutate: func(fixture *temporaryLegacyCleanupFixture, queryGroup execution.QueryGroupIdentity) {
			fixture.progressReader.facts[queryGroup].Load.Progress.NextSlot = 120
		}},
		{name: "Progress read failure", mutate: func(fixture *temporaryLegacyCleanupFixture, queryGroup execution.QueryGroupIdentity) {
			fixture.progressReader.errors = map[execution.QueryGroupIdentity]error{queryGroup: errors.New("Progress unavailable")}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTemporaryLegacyCleanupFixtureWithCatalog(t, threeQueryGroupCatalog(t), false, 180)
			nonTarget := temporaryLegacyNonTargetQueryGroup(t, fixture)
			test.mutate(&fixture, nonTarget)
			before := temporaryLegacyRedisValues(t, fixture.client)
			if _, err := fixture.cleanup.Apply(context.Background(), fixture.request, fixture.plan.Digest); err == nil {
				t.Fatalf("Apply(%s) unexpectedly succeeded", test.name)
			}
			if after := temporaryLegacyRedisValues(t, fixture.client); !reflect.DeepEqual(after, before) {
				t.Fatalf("Apply(%s) changed Redis\nbefore=%#v\nafter=%#v", test.name, before, after)
			}
		})
	}
}

func TestTemporaryLegacyDrainingCleanupRequiresPAsNextLegalSlotAfterCompletion(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*execution.ScheduleProgress)
	}{
		{name: "corrupt FULL watermark", mutate: func(progress *execution.ScheduleProgress) {
			progress.LastFullSlot = 30
		}},
		{name: "off-schedule skipped watermark", mutate: func(progress *execution.ScheduleProgress) {
			progress.LastFullSlot = 0
			progress.LastCompletionKind = execution.CompletionGapSkipped
			progress.CurrentOrRecentGap = &execution.ProgressGapSummary{
				Kind: execution.CompletionGapSkipped, ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped),
				FirstSlot: 30, LastSlot: 30, Count: 1,
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTemporaryLegacyCleanupFixture(t)
			target := fixture.request.Targets[0].QueryGroup
			fact := fixture.progressReader.facts[target]
			test.mutate(fact.Load.Progress)
			fixture.progressReader.facts[target] = fact
			before := temporaryLegacyRedisValues(t, fixture.client)
			if _, err := fixture.cleanup.DryRun(context.Background(), fixture.request); err == nil {
				t.Fatal("DryRun() accepted P without proving it is the next legal Slot after completion")
			}
			if after := temporaryLegacyRedisValues(t, fixture.client); !reflect.DeepEqual(after, before) {
				t.Fatalf("DryRun() changed Redis\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestTemporaryLegacyDrainingCleanupApplyRejectsDifferentRuntimeScopeBeforeEval(t *testing.T) {
	fixture := newTemporaryLegacyCleanupFixture(t)
	compiler, semantics := runtimePlanCompiler(t)
	otherScopeCleanup, err := controlplane.NewTemporaryLegacyDrainingCleanup(
		fixture.repository,
		compiler,
		semantics,
		fixture.progressReader,
		"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
	)
	if err != nil {
		t.Fatal(err)
	}
	hook := &temporaryLegacyEvalHook{}
	fixture.client.AddHook(hook)
	before := temporaryLegacyRedisValues(t, fixture.client)
	result, err := otherScopeCleanup.Apply(context.Background(), fixture.request, fixture.plan.Digest)
	if !errors.Is(err, controlplane.ErrTemporaryLegacyDrainingCleanupConflict) ||
		result.Status != controlplane.TemporaryLegacyDrainingCleanupConflictZeroWrite {
		t.Fatalf("Apply(other runtime scope)=(%#v,%v)", result, err)
	}
	if hook.evalCalls != 0 {
		t.Fatalf("Apply(other runtime scope) EVAL calls=%d, want 0", hook.evalCalls)
	}
	if after := temporaryLegacyRedisValues(t, fixture.client); !reflect.DeepEqual(after, before) {
		t.Fatalf("Apply(other runtime scope) changed Redis\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestTemporaryLegacyDrainingCleanupRejectsContractDriftWithZeroWrites(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*controlplane.TemporaryLegacyDrainingCleanupRequest)
	}{
		{name: "target missing", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.Targets = request.Targets[:1]
		}},
		{name: "target extra", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.Targets = append(request.Targets, request.Targets[0])
		}},
		{name: "current", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.ExpectedCurrent.PublicationEpoch--
		}},
		{name: "candidate", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.ExpectedCandidate.SnapshotRevision = execution.SnapshotRevision(strings.Repeat("a", 64))
		}},
		{name: "query group", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.Targets[0].QueryGroup = execution.QueryGroupIdentity(strings.Repeat("a", 64))
		}},
		{name: "progress next Slot", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.Targets[0].ProgressNextSlot = 60
		}},
		{name: "retired boundary", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.Targets[0].RetiredBoundary = 179
		}},
		{name: "cutover boundary", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.CutoverBoundary = 300
		}},
		{name: "active set count", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.ExpectedActiveQGSetRef.QGCount++
		}},
		{name: "active set digest", mutate: func(request *controlplane.TemporaryLegacyDrainingCleanupRequest) {
			request.ExpectedActiveQGSetRef.Digest = strings.Repeat("b", 64)
		}},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTemporaryLegacyCleanupFixture(t)
			before := temporaryLegacyRedisValues(t, fixture.client)
			request := fixture.request
			request.Targets = append([]controlplane.TemporaryLegacyDrainingTarget(nil), request.Targets...)
			test.mutate(&request)
			result, err := fixture.cleanup.Apply(context.Background(), request, fixture.plan.Digest)
			if err == nil {
				t.Fatalf("Apply(%s) unexpectedly succeeded: %#v", test.name, result)
			}
			if got := temporaryLegacyRedisValues(t, fixture.client); !reflect.DeepEqual(got, before) {
				t.Fatalf("Apply(%s) changed Redis facts\nbefore=%#v\nafter=%#v", test.name, before, got)
			}
		})
	}
}

func TestTemporaryLegacyDrainingCleanupCASRejectsTimelineAndProgressRawDrift(t *testing.T) {
	for _, test := range []struct {
		name string
		key  func(temporaryLegacyCleanupFixture) string
	}{
		{name: "timeline", key: func(fixture temporaryLegacyCleanupFixture) string {
			return fixture.prefix + ":schedule_timeline:" + string(fixture.request.Targets[0].QueryGroup)
		}},
		{name: "Progress", key: func(fixture temporaryLegacyCleanupFixture) string {
			return fixture.progressReader.facts[fixture.request.Targets[0].QueryGroup].RedisKey
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTemporaryLegacyCleanupFixture(t)
			ctx := context.Background()
			activationBefore, err := fixture.repository.LoadActivation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			activeSetKey := fixture.prefix + ":active_qg_set:" + fixture.plan.NextActiveQGSet.Digest
			activeSetBefore, err := fixture.client.Get(ctx, activeSetKey).Bytes()
			if err != nil {
				t.Fatal(err)
			}
			key := test.key(fixture)
			original, err := fixture.client.Get(ctx, key).Bytes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.client.AddHook(&temporaryLegacyEvalHook{before: func() error {
				return fixture.client.Set(ctx, key, append(original, ' '), 0).Err()
			}})
			result, err := fixture.cleanup.Apply(ctx, fixture.request, fixture.plan.Digest)
			if !errors.Is(err, controlplane.ErrTemporaryLegacyDrainingCleanupConflict) ||
				result.Status != controlplane.TemporaryLegacyDrainingCleanupConflictZeroWrite {
				t.Fatalf("Apply(%s drift)=(%#v,%v)", test.name, result, err)
			}
			activationAfter, err := fixture.repository.LoadActivation(ctx)
			if err != nil || !reflect.DeepEqual(activationAfter, activationBefore) {
				t.Fatalf("Activation after %s drift=(%#v,%v), before=%#v", test.name, activationAfter, err, activationBefore)
			}
			activeSetAfter, err := fixture.client.Get(ctx, activeSetKey).Bytes()
			if err != nil || !reflect.DeepEqual(activeSetAfter, activeSetBefore) {
				t.Fatalf("target ActiveSet after %s drift=(%q,%v), before=%q", test.name, activeSetAfter, err, activeSetBefore)
			}
		})
	}
}

func TestTemporaryLegacyDrainingCleanupActiveSetIdempotenceAndCollision(t *testing.T) {
	t.Run("same payload", func(t *testing.T) {
		fixture := newTemporaryLegacyCleanupFixture(t)
		ctx := context.Background()
		payload := temporaryLegacyActiveSetPayload(t, fixture.request.Targets)
		key := fixture.prefix + ":active_qg_set:" + fixture.plan.NextActiveQGSet.Digest
		if err := fixture.client.Set(ctx, key, payload, 0).Err(); err != nil {
			t.Fatal(err)
		}
		plan, err := fixture.cleanup.DryRun(ctx, fixture.request)
		if err != nil || plan.Digest != fixture.plan.Digest {
			t.Fatalf("DryRun(existing identical ActiveSet)=(%#v,%v)", plan, err)
		}
		result, err := fixture.cleanup.Apply(ctx, fixture.request, plan.Digest)
		if err != nil || result.Status != controlplane.TemporaryLegacyDrainingCleanupApplied {
			t.Fatalf("Apply(existing identical ActiveSet)=(%#v,%v)", result, err)
		}
	})

	t.Run("different payload", func(t *testing.T) {
		fixture := newTemporaryLegacyCleanupFixture(t)
		ctx := context.Background()
		key := fixture.prefix + ":active_qg_set:" + fixture.plan.NextActiveQGSet.Digest
		if err := fixture.client.Set(ctx, key, []byte(`{"schema_version":"alarmd-active-qg-set-v1","query_groups":[]}`), 0).Err(); err != nil {
			t.Fatal(err)
		}
		before := temporaryLegacyRedisValues(t, fixture.client)
		if _, err := fixture.cleanup.DryRun(ctx, fixture.request); err == nil {
			t.Fatal("DryRun(different ActiveSet payload) unexpectedly succeeded")
		}
		if got := temporaryLegacyRedisValues(t, fixture.client); !reflect.DeepEqual(got, before) {
			t.Fatalf("DryRun(different ActiveSet payload) changed Redis\nbefore=%#v\nafter=%#v", before, got)
		}
	})
}

func TestTemporaryLegacyDrainingCleanupUnknownResultUsesReadbackWithoutRetry(t *testing.T) {
	for _, test := range []struct {
		name       string
		hook       func() redis.Hook
		wantStatus controlplane.TemporaryLegacyDrainingCleanupApplyStatus
	}{
		{name: "applied", hook: func() redis.Hook {
			return &temporaryLegacyEvalHook{after: func() error { return io.ErrUnexpectedEOF }}
		}, wantStatus: controlplane.TemporaryLegacyDrainingCleanupUnknownApplied},
		{name: "zero write", hook: func() redis.Hook {
			return &temporaryLegacyEvalHook{before: func() error { return io.ErrUnexpectedEOF }}
		}, wantStatus: controlplane.TemporaryLegacyDrainingCleanupUnknownZeroWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTemporaryLegacyCleanupFixture(t)
			hook := test.hook()
			fixture.client.AddHook(hook)
			result, err := fixture.cleanup.Apply(context.Background(), fixture.request, fixture.plan.Digest)
			if !errors.Is(err, controlplane.ErrTemporaryLegacyDrainingCleanupUnknown) || result.Status != test.wantStatus {
				t.Fatalf("Apply(unknown %s)=(%#v,%v)", test.name, result, err)
			}
			if evalHook := hook.(*temporaryLegacyEvalHook); evalHook.evalCalls != 1 {
				t.Fatalf("Apply(unknown %s) eval calls=%d, want 1", test.name, evalHook.evalCalls)
			}
		})
	}
}

func TestScheduleActivationEnsureDoesNotUseTemporaryLegacyCleanupCAS(t *testing.T) {
	client := newControlplaneRedis(t)
	hook := &temporaryLegacyEvalHook{}
	client.AddHook(hook)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:temporary-path-isolation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, semantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalogWithAllSchedules(t, twoQueryGroupCatalog(t), 60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(context.Background(), snapshot.Publication); err != nil {
		t.Fatal(err)
	}
	if hook.temporaryCalls != 0 {
		t.Fatalf("normal Ensure invoked temporary cleanup EVAL %d times", hook.temporaryCalls)
	}
}

type temporaryLegacyEvalHook struct {
	mu             sync.Mutex
	before         func() error
	after          func() error
	evalCalls      int
	temporaryCalls int
}

func (hook *temporaryLegacyEvalHook) BeforeProcess(ctx context.Context, command redis.Cmder) (context.Context, error) {
	if !strings.EqualFold(command.Name(), "eval") {
		return ctx, nil
	}
	hook.mu.Lock()
	defer hook.mu.Unlock()
	hook.evalCalls++
	if len(command.Args()) > 1 && strings.Contains(command.Args()[1].(string), "local next_active") {
		hook.temporaryCalls++
	}
	if hook.before == nil {
		return ctx, nil
	}
	before := hook.before
	hook.before = nil
	return ctx, before()
}

func (hook *temporaryLegacyEvalHook) AfterProcess(_ context.Context, command redis.Cmder) error {
	if !strings.EqualFold(command.Name(), "eval") {
		return nil
	}
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if hook.after == nil {
		return nil
	}
	after := hook.after
	hook.after = nil
	return after()
}

func (*temporaryLegacyEvalHook) BeforeProcessPipeline(ctx context.Context, _ []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (*temporaryLegacyEvalHook) AfterProcessPipeline(context.Context, []redis.Cmder) error {
	return nil
}

func temporaryLegacyRedisValues(t *testing.T, client redis.UniversalClient) map[string]string {
	t.Helper()
	keys, err := client.Keys(context.Background(), "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		value, err := client.Get(context.Background(), key).Result()
		if err != nil {
			t.Fatal(err)
		}
		values[key] = value
	}
	return values
}

func temporaryLegacyActiveSetPayload(
	t *testing.T,
	targets []controlplane.TemporaryLegacyDrainingTarget,
) []byte {
	t.Helper()
	queryGroups := make([]execution.QueryGroupIdentity, 0, len(targets))
	for _, target := range targets {
		queryGroups = append(queryGroups, target.QueryGroup)
	}
	sort.Slice(queryGroups, func(i, j int) bool { return queryGroups[i] < queryGroups[j] })
	payload := struct {
		SchemaVersion string                         `json:"schema_version"`
		QueryGroups   []execution.QueryGroupIdentity `json:"query_groups"`
	}{SchemaVersion: "alarmd-active-qg-set-v1", QueryGroups: queryGroups}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func temporaryLegacyNonTargetQueryGroup(
	t *testing.T,
	fixture temporaryLegacyCleanupFixture,
) execution.QueryGroupIdentity {
	t.Helper()
	targets := make(map[execution.QueryGroupIdentity]struct{}, len(fixture.request.Targets))
	for _, target := range fixture.request.Targets {
		targets[target.QueryGroup] = struct{}{}
	}
	for queryGroup := range fixture.progressReader.facts {
		if _, target := targets[queryGroup]; !target {
			return queryGroup
		}
	}
	t.Fatal("non-target Query Group is absent")
	return ""
}

func threeQueryGroupCatalog(t *testing.T) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	identities := []controlplane.SourceIdentity{
		{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"},
		{TenantID: "tenant-a", BusinessID: "4", SpaceScope: "bkcc__4"},
	}
	strategies := make([]controlplane.SourceStrategy, 0, len(identities))
	sourceIDs := []string{"1001", "1002", "1003"}
	for index, identity := range identities {
		var document map[string]any
		if err := json.Unmarshal(documents[index%len(documents)], &document); err != nil {
			t.Fatal(err)
		}
		document["bk_biz_id"] = float64(index + 2)
		document["id"] = float64(1001 + index)
		document["space_uid"] = identity.SpaceScope
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		strategies = append(strategies, controlplane.SourceStrategy{
			SourceID: sourceIDs[index], Document: payload, Identity: identity,
		})
	}
	planner := queryPlannerFunc(func(_ context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
		return queryFactsFor(t, source.Identity.BusinessID, source.Identity.SpaceScope), nil
	})
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: strategies, Planner: planner})
	if err != nil || len(catalog.QueryGroups) != 3 {
		t.Fatalf("three Query Group Catalog=(%#v,%v)", catalog, err)
	}
	return catalog
}
