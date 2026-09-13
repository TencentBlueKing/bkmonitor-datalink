// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

const generationSkewPrefix = "alarmd:control:generation-skew"

// generationSkewPublication is one publication written by a Control Leader
// with the given state semantics: the catalog objects, the initial Segment
// naming them, and the activation records naming the generation the Leader
// derived. It is what a Worker finds in Redis after a release.
type generationSkewPublication struct {
	client     *redis.Client
	compiler   *strategy.PlanCompiler
	semantics  strategy.StateSemantics
	queryGroup execution.QueryGroupIdentity
	recorded   execution.StateGeneration
	schedule   execution.FrozenQueryGroupSchedule
}

func publishWithSemantics(t *testing.T, semantics strategy.StateSemantics) generationSkewPublication {
	t.Helper()
	ctx := context.Background()
	client := newControlplaneRedis(t)
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, generationSkewPrefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, _ := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("pending = (%+v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("publish = (%+v, %v)", published, err)
	}
	activator, err := controlplane.NewInitialScheduleActivator(
		repository, compiler, semantics, func() time.Time { return time.Unix(60, 0) },
	)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := activator.Ensure(ctx, published.Publication)
	if err != nil {
		t.Fatal(err)
	}
	if len(activation.Plans) != 1 || activation.Plans[0].Fact.Selected.StateGeneration == "" {
		t.Fatalf("activation Plans = %+v", activation.Plans)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, execution.SnapshotRevision(published.Publication.SnapshotRevision))
	if err != nil || len(snapshot.QueryGroups) != 1 {
		t.Fatalf("published snapshot = (%+v, %v)", snapshot.QueryGroups, err)
	}
	queryGroup := snapshot.QueryGroups[0].Identity
	schedule, err := runtime.ReadFrozenSchedule(ctx, queryGroup, 60)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.Segment.ObjectDigest == "" || len(schedule.DuePlanRefs(60)) != 1 {
		t.Fatalf("the initial Segment must name its content and be due at 60: %+v", schedule.Segment)
	}
	return generationSkewPublication{
		client: client, compiler: compiler, semantics: semantics, queryGroup: queryGroup,
		recorded: activation.Plans[0].Fact.Selected.StateGeneration, schedule: schedule,
	}
}

func (publication generationSkewPublication) request() execution.FreezeSlotContractRequest {
	return execution.FreezeSlotContractRequest{
		QueryGroup: publication.queryGroup, ScheduleRevision: publication.schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: publication.schedule.Segment.Start, EvaluationTime: 60,
		DuePlans: publication.schedule.DuePlanRefs(60),
	}
}

// workerRuntime is a fresh process on the given state semantics: its own
// repository, so no cache carries anything over, and a sink for the skew it
// reports.
func (publication generationSkewPublication) workerRuntime(t *testing.T, semantics strategy.StateSemantics) (*controlplane.RedisCatalogRuntime, *generationSkewSink) {
	t.Helper()
	repository, err := controlplane.NewRedisCatalogRepository(publication.client, generationSkewPrefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	sink := &generationSkewSink{}
	repository.ConfigureObserver(sink)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, publication.compiler, semantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, sink
}

type generationSkewSink struct {
	mu           sync.Mutex
	observations []observability.Observation
}

func (sink *generationSkewSink) Observe(_ context.Context, observation observability.Observation) {
	if observation.StateGenerationSkew == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.observations = append(sink.observations, observation)
}

func (sink *generationSkewSink) reported() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	kinds := make([]string, 0, len(sink.observations))
	for _, observation := range sink.observations {
		kinds = append(kinds, observation.StateGenerationSkew.Kind)
	}
	return kinds
}

// last returns the most recent skew observation whole: the line has to name
// the Slot and the Plan, not only count the kind.
func (sink *generationSkewSink) last() observability.Observation {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.observations[len(sink.observations)-1]
}

// otherFormula stands for a release that derives the state generation
// differently from the same Plan: the identity schema digest is one of the
// inputs the hash closes over, so moving it moves every generation the way
// a new closure formula does.
func otherFormula(semantics strategy.StateSemantics) strategy.StateSemantics {
	semantics.IdentitySchemaDigest = strings.Repeat("4", 64)
	return semantics
}

func planMaterializeFailure(t *testing.T, err error) string {
	t.Helper()
	var classified *controlplane.FreezeSlotContractError
	if !errors.As(err, &classified) || classified.Class != controlplane.FreezeSlotFailurePlanMaterialize || errors.Unwrap(err) == nil {
		t.Fatalf("err = %v, want a plan_materialize freeze failure", err)
	}
	return errors.Unwrap(err).Error()
}

// TestFreezeSlotContractExecutesUnderTheRecordedGenerationAcrossAFormulaMove:
// the Control Leader that published a Segment derived its Plan's state
// generation by one formula and the Worker freezing a Slot in it derives
// another from the same Plan, which is every Worker's situation for the
// minutes a release that moves the formula takes to roll and republish. The
// Slot freezes under the generation the activation record names, the one
// the state is keyed by, and the disagreement is reported as formula skew.
// Refusing it blocked every Slot of every Query Group from the first Worker
// restart of such a release until the objects written under the old formula
// expired a day later: the Progress cursor cannot pass a Slot that will not
// freeze, and the new Leader's cutover only opens Segments ahead of it.
func TestFreezeSlotContractExecutesUnderTheRecordedGenerationAcrossAFormulaMove(t *testing.T) {
	ctx := context.Background()
	_, leaderSemantics := runtimePlanCompiler(t)
	publication := publishWithSemantics(t, leaderSemantics)

	// A Worker on the Leader's formula: the ordinary case, nothing to report.
	same, sameSink := publication.workerRuntime(t, leaderSemantics)
	if fact, err := same.FreezeSlotContract(ctx, publication.request()); err != nil ||
		len(fact.DuePlans) != 1 || fact.DuePlans[0].StateGeneration != publication.recorded {
		t.Fatalf("same formula: fact=%+v err=%v", fact, err)
	}
	if reported := sameSink.reported(); len(reported) != 0 {
		t.Fatalf("a Worker on the Leader's formula reported skew: %v", reported)
	}

	// A Worker on another formula: the Slot still freezes, under the recorded
	// generation, and the skew is counted once per due Plan.
	moved, movedSink := publication.workerRuntime(t, otherFormula(leaderSemantics))
	fact, err := moved.FreezeSlotContract(ctx, publication.request())
	if err != nil {
		t.Fatalf("a Worker whose formula moved must still freeze the Slot the Leader published: %v", err)
	}
	if len(fact.DuePlans) != 1 || fact.DuePlans[0].StateGeneration != publication.recorded {
		t.Fatalf("the Slot must execute under the recorded generation %q: due Plans %+v", publication.recorded, fact.DuePlans)
	}
	if reported := movedSink.reported(); len(reported) != 1 || reported[0] != "formula" {
		t.Fatalf("formula skew must be reported once for the one due Plan: %v", reported)
	}
	// The same Slot freezes the same way again; this is what the Progress
	// cursor relies on to move past it.
	if repeated, err := moved.FreezeSlotContract(ctx, publication.request()); err != nil || repeated.Contract != fact.Contract {
		t.Fatalf("repeat freeze = (%+v, %v)", repeated, err)
	}

	// The reverse direction of a roll - a Worker on the old formula reading a
	// Segment the new Leader already published - is the same situation.
	reversed := publishWithSemantics(t, otherFormula(leaderSemantics))
	old, oldSink := reversed.workerRuntime(t, leaderSemantics)
	if fact, err := old.FreezeSlotContract(ctx, reversed.request()); err != nil ||
		len(fact.DuePlans) != 1 || fact.DuePlans[0].StateGeneration != reversed.recorded {
		t.Fatalf("old formula on a new publication: fact=%+v err=%v", fact, err)
	}
	if reported := oldSink.reported(); len(reported) != 1 || reported[0] != "formula" {
		t.Fatalf("old formula on a new publication must report formula skew: %v", reported)
	}
}

// TestFreezeSlotContractRefusesARecordItsOwnObjectDoesNotVouchFor: what a
// Worker must still refuse is a real disagreement between the two persisted
// facts, the generation the activation record names and the one carried by
// the Plan in the content-addressed object the Segment names. The object is
// what vouches for the record; no formula does. Such a Slot would mutate
// state keyed by one generation with a Plan of another, and it is refused on
// the same formula the Leader used, so the refusal owes nothing to formula
// agreement.
func TestFreezeSlotContractRefusesARecordItsOwnObjectDoesNotVouchFor(t *testing.T) {
	ctx := context.Background()
	_, leaderSemantics := runtimePlanCompiler(t)
	publication := publishWithSemantics(t, leaderSemantics)

	// Direct key writes bypass the activation header every persisted timeline
	// write advances, so the runtime that reads the tampered record is a
	// fresh process that has not observed the header yet.
	timelineKey := generationSkewPrefix + ":schedule_timeline:" + string(publication.queryGroup)
	timeline := readJSONObject(t, ctx, publication.client, timelineKey)
	segment := timeline["segments"].([]any)[0].(map[string]any)
	selected := segment["plans"].([]any)[0].(map[string]any)["fact"].(map[string]any)["Selected"].(map[string]any)
	if selected["StateGeneration"] != string(publication.recorded) {
		t.Fatalf("persisted record generation = %v, want %q", selected["StateGeneration"], publication.recorded)
	}
	selected["StateGeneration"] = strings.Repeat("f", 64)
	writeJSONObject(t, ctx, publication.client, timelineKey, timeline)

	cold, sink := publication.workerRuntime(t, leaderSemantics)
	_, err := cold.FreezeSlotContract(ctx, publication.request())
	if err == nil {
		t.Fatal("a record naming a generation its object does not carry was frozen")
	}
	if message := planMaterializeFailure(t, err); !strings.Contains(message, "differs from the frozen Plan") {
		t.Fatalf("refusal must name the record, got %q", message)
	}
	if reported := sink.reported(); len(reported) != 1 || reported[0] != "record" {
		t.Fatalf("record skew must be reported once: %v", reported)
	}
	// A refused Slot is only findable from this line: it has to say which
	// Query Group, which Slot and which Plan the record it refused belongs to.
	if last := sink.last(); last.Trace.QueryGroupKey != string(publication.queryGroup) || last.Trace.EvaluationTime == 0 ||
		last.Trace.ScheduleSegmentStart == 0 || last.StateGenerationSkew.StrategyID == "" {
		t.Fatalf("record skew line must name the Slot and the Plan: trace=%+v facts=%+v", last.Trace, last.StateGenerationSkew)
	}
}
