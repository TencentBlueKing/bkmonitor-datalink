package controlplane_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestRedisCatalogRepositoryPublishesImmutableContentAddressedSnapshot(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	publisher, err := controlplane.NewSnapshotPublisher(repository)
	if err != nil {
		t.Fatal(err)
	}

	first, created, err := publisher.Publish(context.Background(), catalog)
	if err != nil || !created {
		t.Fatalf("first publish=(%#v, %t, %v)", first, created, err)
	}
	catalog.ObservationID = strings.Repeat("a", 64)
	catalog.Dispositions = append(catalog.Dispositions, controlplane.ObjectDisposition{
		SourceID: "1001", Scope: "PLAN", Disposition: controlplane.DispositionSourceIncomplete, Reason: "AUDIT_ONLY",
	})
	second, created, err := publisher.Publish(context.Background(), catalog)
	if err != nil || created || second.Publication != first.Publication {
		t.Fatalf("idempotent publish=(%#v, %t, %v), first=%#v", second, created, err, first)
	}
	loaded, err := repository.LoadSnapshot(context.Background(), catalog.SnapshotRevision)
	if err != nil || loaded.Publication != first.Publication || len(loaded.QueryGroups) != 1 {
		t.Fatalf("loaded snapshot=(%#v, %v)", loaded, err)
	}
	group, err := repository.LoadQueryGroup(context.Background(), catalog.SnapshotRevision, catalog.QueryGroups[0].Identity)
	if err != nil || group.Identity != catalog.QueryGroups[0].Identity {
		t.Fatalf("loaded query group=(%#v, %v)", group, err)
	}
	plan, err := repository.LoadPlan(context.Background(), catalog.SnapshotRevision, catalog.QueryGroups[0].Plans[0].Identity)
	if err != nil || plan.Identity != catalog.QueryGroups[0].Plans[0].Identity {
		t.Fatalf("loaded plan=(%#v, %v)", plan, err)
	}
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil || audit.ObservationID != catalog.ObservationID || len(audit.Dispositions) != len(catalog.Dispositions) {
		t.Fatalf("latest audit=(%#v, %v)", audit, err)
	}

	newCatalog := validCatalog(t, 81)
	newSnapshot, created, err := publisher.Publish(context.Background(), newCatalog)
	if err != nil || !created || newSnapshot.Publication.PublicationEpoch <= first.Publication.PublicationEpoch {
		t.Fatalf("new publish=(%#v, %t, %v), first=%#v", newSnapshot, created, err, first)
	}
	latest, err := repository.LoadLatestPublication(context.Background())
	if err != nil || latest != newSnapshot.Publication {
		t.Fatalf("latest=(%#v, %v), want %#v", latest, err, newSnapshot.Publication)
	}
}

func TestLegacyRedisSourceCompilesAndPublishesSharedQueryGroup(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1002,1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	resolver := &recordingIdentityResolver{facts: map[string]controlplane.SourceIdentity{
		"2": {TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	}}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache", resolver)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := controlplane.ObserveStable(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: observation.Strategies, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ObservationID != observation.ObservationID || len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 2 {
		t.Fatalf("compiled catalog=%#v observation=%#v", catalog, observation)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:full", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := controlplane.NewSnapshotPublisher(repository)
	if err != nil {
		t.Fatal(err)
	}
	published, created, err := publisher.Publish(ctx, catalog)
	if err != nil || !created {
		t.Fatalf("publish=(%#v, %t, %v)", published, created, err)
	}
	loaded, err := repository.LoadQueryGroup(ctx, published.Publication.SnapshotRevision, catalog.QueryGroups[0].Identity)
	if err != nil || len(loaded.Plans) != 2 || loaded.QueryPlan.QueryRevision == "" {
		t.Fatalf("loaded query group=(%#v, %v)", loaded, err)
	}
}

func TestRedisCatalogRepositoryRejectsRevisionMismatchAndCollision(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	tampered := catalog
	tampered.SnapshotRevision = "not-the-content-digest"
	if _, _, err := repository.PublishCatalog(context.Background(), tampered); err == nil {
		t.Fatal("tampered snapshot revision was accepted")
	}

	key := "alarmd:control:test:snapshot:" + string(catalog.SnapshotRevision)
	if err := client.Set(context.Background(), key, `{"different":"content"}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.PublishCatalog(context.Background(), catalog); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("collision error=%v", err)
	}
}

func TestRedisCatalogRepositoryPublishesEmptySnapshot(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:empty", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{}, Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := controlplane.NewSnapshotPublisher(repository)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, created, err := publisher.Publish(context.Background(), catalog)
	if err != nil || !created {
		t.Fatalf("empty publish=(%#v, %t, %v)", snapshot, created, err)
	}
	loaded, err := repository.LoadSnapshot(context.Background(), catalog.SnapshotRevision)
	if err != nil || loaded.QueryGroups == nil || len(loaded.QueryGroups) != 0 || loaded.Publication != snapshot.Publication {
		t.Fatalf("empty loaded=(%#v, %v)", loaded, err)
	}
}

func TestRedisCatalogRepositoryActivationCASAndProjection(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	plan := catalog.QueryGroups[0].Plans[0]
	schedule := frozenSchedule(t, snapshot.Publication, catalog.QueryGroups[0], 60, nil)
	state := activationState(t, 1, snapshot, schedule, nil)
	fact := state.Plans[0].Fact
	initial := []execution.InitialScheduleActivationFact{{Segment: schedule.Segment}}
	if err := repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, state, initial); err != nil {
		t.Fatal(err)
	}
	loaded, err := repository.LoadActivation(context.Background())
	if err != nil || loaded.RecordRevision != 1 || len(loaded.Plans) != 1 || loaded.Plans[0].Fact != fact {
		t.Fatalf("loaded activation=(%#v, %v)", loaded, err)
	}
	if err := repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, state, initial); !errors.Is(err, controlplane.ErrActivationConflict) {
		t.Fatalf("stale activation CAS error=%v", err)
	}

	contract := execution.FrozenExecutionContractRef{
		Slot:             execution.SlotIdentity{QueryGroup: catalog.QueryGroups[0].Identity, EvaluationTime: 60},
		SnapshotRevision: snapshot.Publication.SnapshotRevision, QueryRevision: catalog.QueryGroups[0].QueryPlan.QueryRevision,
		ScheduleRevision: catalog.QueryGroups[0].ScheduleRevision, ScheduleSegmentStart: 60, DuePlanSetDigest: "due-plans-v1",
	}
	missingPlan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "9999"}
	activations, err := repository.LoadActivations(context.Background(), execution.PlanActivationRequest{Contract: contract, Plans: []execution.PlanIdentity{plan.Identity, missingPlan}})
	if err != nil || len(activations.Facts) != 2 || activations.Facts[0] != fact || activations.Facts[1].Selection != execution.ActivationNone {
		t.Fatalf("activation projection=(%#v, %v)", activations, err)
	}
}

func TestRedisCatalogRuntimePersistsInitialScheduleAndFreezesExactSlot(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	schedule := frozenSchedule(t, snapshot.Publication, catalog.QueryGroups[0], 60, nil)
	activation := activationState(t, 1, snapshot, schedule, nil)
	initial := execution.InitialScheduleActivationFact{Segment: schedule.Segment}
	if err := repository.CompareAndSetInitialScheduleActivation(
		context.Background(), controlplane.ActivationExpectation{}, activation, []execution.InitialScheduleActivationFact{initial},
	); err != nil {
		t.Fatal(err)
	}

	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.ReadInitialFrozenSchedule(context.Background(), schedule.Segment.QueryGroup)
	if err != nil || loaded.Segment.Start != 60 || loaded.Segment.End != nil {
		t.Fatalf("initial schedule=(%#v, %v)", loaded, err)
	}
	request := execution.FreezeSlotContractRequest{
		QueryGroup: schedule.Segment.QueryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: 60, DuePlans: schedule.DuePlanRefs(60),
	}
	fact, err := runtime.FreezeSlotContract(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := fact.Validate(request); err != nil {
		t.Fatal(err)
	}
	if len(fact.DuePlans) != 1 || len(fact.Requirements) != 1 || len(fact.Requirements[0].Consumers) != 1 {
		t.Fatalf("frozen fact=%#v", fact)
	}
	if fact.Contract.SnapshotRevision != snapshot.Publication.SnapshotRevision ||
		fact.DuePlans[0].StateApplyEpoch != activation.Plans[0].Fact.Selected.StateApplyEpoch {
		t.Fatalf("frozen provenance=%#v activation=%#v", fact, activation)
	}

	// Recreate the adapter to prove the persisted Catalog/Segment/activation,
	// rather than process memory, is sufficient after restart.
	restarted, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := restarted.ReadFrozenSchedule(context.Background(), schedule.Segment.QueryGroup, 60); err != nil || got.Segment.Start != 60 {
		t.Fatalf("restart schedule=(%#v, %v)", got, err)
	}
}

func TestRedisCatalogRuntimeCutoverUsesOneHalfOpenTimelineAndNewGrid(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:cutover", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := validCatalog(t, 80)
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	oldOpen := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, nil)
	oldActivation := activationState(t, 1, oldSnapshot, oldOpen, nil)
	if err := repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, oldActivation,
		[]execution.InitialScheduleActivationFact{{Segment: oldOpen.Segment}}); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithSchedule(t, oldCatalog, 120, 30)
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	boundary := execution.EvaluationTime(180)
	oldClosed := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, &boundary)
	newOpen := frozenSchedule(t, newSnapshot.Publication, newCatalog.QueryGroups[0], boundary, nil)
	newActivation := activationState(t, 2, newSnapshot, newOpen, nil)
	cutover := execution.ScheduleCutoverFact{OldSegment: oldClosed.Segment, NewSegment: newOpen.Segment}
	if err := repository.CompareAndSetScheduleCutover(context.Background(), controlplane.ActivationExpectation{
		RecordRevision: oldActivation.RecordRevision, Current: oldActivation.Current,
	}, newActivation, []execution.ScheduleCutoverFact{cutover}); err != nil {
		t.Fatal(err)
	}

	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	oldLoaded, err := runtime.ReadFrozenSchedule(context.Background(), oldClosed.Segment.QueryGroup, 120)
	if err != nil || oldLoaded.Segment.End == nil || *oldLoaded.Segment.End != boundary {
		t.Fatalf("old schedule=(%#v, %v)", oldLoaded, err)
	}
	newLoaded, err := runtime.ReadFrozenSchedule(context.Background(), newOpen.Segment.QueryGroup, boundary)
	if err != nil || newLoaded.Segment.Start != boundary {
		t.Fatalf("new schedule=(%#v, %v)", newLoaded, err)
	}
	first, ok := newLoaded.FirstSlot()
	if !ok || first != 270 {
		t.Fatalf("new first Slot=(%d, %t), want 270", first, ok)
	}
	next, err := runtime.NextSlotAfter(context.Background(), oldClosed.Segment.QueryGroup, 120)
	if err != nil || next != 270 {
		t.Fatalf("next across cutover=(%d, %v), want 270", next, err)
	}
}

func TestRedisCatalogRepositoryRejectsInitialActivationWithoutScheduleForEveryPlan(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:initial-coverage", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := validCatalog(t, 80)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	schedule := frozenSchedule(t, snapshot.Publication, catalog.QueryGroups[0], 60, nil)
	activation := activationState(t, 1, snapshot, schedule, nil)
	extra := activation.Plans[0]
	extra.Fact.Plan.StrategyID = "9999"
	extra.Fact.Selected.Identity = extra.Fact.Plan
	activation.Plans = append(activation.Plans, extra)

	err = repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, activation,
		[]execution.InitialScheduleActivationFact{{Segment: schedule.Segment}})
	if err == nil || !strings.Contains(err.Error(), "exactly cover") {
		t.Fatalf("incomplete initial Schedule coverage error=%v", err)
	}
}

func TestRedisCatalogRepositoryRejectsCutoverChangingPlanOutsideAffectedQueryGroup(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:cutover-coverage", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := twoQueryGroupCatalog(t)
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	oldSchedules := []execution.FrozenQueryGroupSchedule{
		frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, nil),
		frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[1], 60, nil),
	}
	oldActivation := activationState(t, 1, oldSnapshot, oldSchedules[0], nil, oldSchedules[1:]...)
	initial := []execution.InitialScheduleActivationFact{{Segment: oldSchedules[0].Segment}, {Segment: oldSchedules[1].Segment}}
	if err := repository.CompareAndSetInitialScheduleActivation(context.Background(), controlplane.ActivationExpectation{}, oldActivation, initial); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithSchedule(t, oldCatalog, 120, 30)
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	boundary := execution.EvaluationTime(180)
	oldClosed := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, &boundary)
	newOpen := frozenSchedule(t, newSnapshot.Publication, newCatalog.QueryGroups[0], boundary, nil)
	newGroupActivation := activationState(t, 2, newSnapshot, newOpen, nil)
	newActivation := oldActivation
	newActivation.RecordRevision = 2
	newActivation.Pending = &newSnapshot.Publication
	newGroupActivation.Plans[0].Fact.Selection = execution.ActivationPending
	newActivation.Plans[0] = newGroupActivation.Plans[0]
	// This valid-looking mutation belongs to the other Query Group and must not
	// be smuggled into the same CAS without its own persisted Segment fact.
	newActivation.Plans[1].Fact.Selected.StateApplyEpoch++

	err = repository.CompareAndSetScheduleCutover(context.Background(), controlplane.ActivationExpectation{
		RecordRevision: oldActivation.RecordRevision, Current: oldActivation.Current,
	}, newActivation, []execution.ScheduleCutoverFact{{OldSegment: oldClosed.Segment, NewSegment: newOpen.Segment}})
	if err == nil || !strings.Contains(err.Error(), "outside affected Query Groups") {
		t.Fatalf("unrelated activation mutation error=%v", err)
	}
}

func validCatalog(t *testing.T, threshold int) controlplane.Catalog {
	t.Helper()
	document := realThresholdDocuments(t)[0]
	document = []byte(strings.Replace(string(document), `"threshold":80`, `"threshold":`+strconv.Itoa(threshold), 1))
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func frozenSchedule(
	t *testing.T,
	publication controlplane.SnapshotPublicationRef,
	group controlplane.QueryGroup,
	start execution.EvaluationTime,
	end *execution.EvaluationTime,
) execution.FrozenQueryGroupSchedule {
	t.Helper()
	plans := make([]execution.FrozenPlanSchedule, len(group.Plans))
	for index, plan := range group.Plans {
		plans[index] = execution.FrozenPlanSchedule{
			Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec,
		}
	}
	return execution.FrozenQueryGroupSchedule{Segment: execution.ScheduleSegmentFact{
		Publication: execution.SnapshotPublicationRef{
			SnapshotRevision: publication.SnapshotRevision, PublicationEpoch: execution.PublicationEpoch(publication.PublicationEpoch),
		},
		QueryGroup: group.Identity, QueryRevision: group.QueryPlan.QueryRevision,
		ScheduleRevision: group.ScheduleRevision, Start: start, End: end,
	}, Plans: plans}
}

func activationState(
	t *testing.T,
	revision uint64,
	snapshot controlplane.PublishedSnapshot,
	firstSchedule execution.FrozenQueryGroupSchedule,
	pending *controlplane.SnapshotPublicationRef,
	additionalSchedules ...execution.FrozenQueryGroupSchedule,
) controlplane.ActivationState {
	t.Helper()
	publication := snapshot.Publication
	compiler, stateSemantics := runtimePlanCompiler(t)
	snapshotPlanByID := make(map[execution.PlanIdentity]controlplane.FrozenPlan)
	datasetByPlanID := make(map[execution.PlanIdentity]contract.DatasetContractV2)
	for _, group := range snapshot.QueryGroups {
		for _, plan := range group.Plans {
			snapshotPlanByID[plan.Identity] = plan
			datasetByPlanID[plan.Identity] = group.QueryPlan.Normalization.DatasetContract
		}
	}
	schedules := append([]execution.FrozenQueryGroupSchedule{firstSchedule}, additionalSchedules...)
	records := make([]controlplane.PlanActivationRecord, 0)
	for _, schedule := range schedules {
		for _, planSchedule := range schedule.Plans {
			plan := snapshotPlanByID[planSchedule.Identity]
			result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
				Plan: plan.Plan, DatasetContract: datasetByPlanID[planSchedule.Identity],
				StateSemantics: stateSemantics,
			})
			if err != nil {
				t.Fatal(err)
			}
			compiled, ok := result.Plan()
			if !ok {
				t.Fatalf("plan did not compile: terminal=%#v levels=%#v", result.PlanTerminal(), result.LevelTerminals())
			}
			fact := execution.PlanActivationFact{Plan: plan.Identity, Selection: execution.ActivationCurrent,
				Selected: execution.ActivatedPlan{Identity: plan.Identity,
					StateGeneration:  execution.StateGeneration(compiled.StateCompatibilityHash()),
					StateApplyEpoch:  execution.StateApplyEpoch(publication.PublicationEpoch),
					ScheduleRevision: plan.ScheduleRevision, RequiredFullSlots: 2}}
			records = append(records, controlplane.PlanActivationRecord{Fact: fact, Publication: publication})
		}
	}
	return controlplane.ActivationState{RecordRevision: revision, Current: publication, Pending: pending, Plans: records}
}

func twoQueryGroupCatalog(t *testing.T) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	second := strings.Replace(string(documents[1]), `"bk_biz_id": 2`, `"bk_biz_id": 3`, 1)
	planner := queryPlannerFunc(func(_ context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
		return queryFactsFor(t, source.Identity.BusinessID, source.Identity.SpaceScope), nil
	})
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{
			{SourceID: "1001", Document: documents[0], Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
			{SourceID: "1002", Document: []byte(second), Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
		},
		Planner: planner,
	})
	if err != nil || len(catalog.QueryGroups) != 2 {
		t.Fatalf("two Query Group catalog=(%#v, %v)", catalog, err)
	}
	return catalog
}

type queryPlannerFunc func(context.Context, controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error)

func (planner queryPlannerFunc) CompilePrimaryQuery(ctx context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
	return planner(ctx, source)
}

func catalogWithSchedule(t *testing.T, source controlplane.Catalog, interval int64, alignment execution.EvaluationTime) controlplane.Catalog {
	t.Helper()
	result := source
	result.QueryGroups = append([]controlplane.QueryGroup(nil), source.QueryGroups...)
	group := result.QueryGroups[0]
	group.Plans = append([]controlplane.FrozenPlan(nil), group.Plans...)
	for index := range group.Plans {
		group.Plans[index].Plan.StrategyIR.ExecutionSemantics.EvaluationInterval = uint32(interval)
		group.Plans[index].PlanRevision = mustDigest(t, "alarmd-plan-semantics-v1", group.Plans[index].Plan)
		group.Plans[index].ScheduleSpec = execution.ScheduleSpec{
			EvaluationIntervalSeconds: interval, Alignment: alignment, Timezone: "UTC",
		}
		revision, err := execution.DerivePlanScheduleRevision(group.Plans[index].ScheduleSpec)
		if err != nil {
			t.Fatal(err)
		}
		group.Plans[index].ScheduleRevision = revision
	}
	schedules := make([]execution.FrozenPlanSchedule, len(group.Plans))
	for index, plan := range group.Plans {
		schedules[index] = execution.FrozenPlanSchedule{Identity: plan.Identity, ScheduleRevision: plan.ScheduleRevision, Spec: plan.ScheduleSpec}
	}
	group.ScheduleRevision, _ = execution.DeriveQueryGroupScheduleRevision(schedules)
	result.QueryGroups[0] = group
	result.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", result.QueryGroups))
	return result
}

func runtimePlanCompiler(t *testing.T) (*strategy.PlanCompiler, strategy.StateSemantics) {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return compiler, strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
		IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1",
		HistoryCellSemanticsVersion: "detect-history-cell-v1"}
}

func mustDigest(t *testing.T, domain string, value any) string {
	t.Helper()
	digest, err := contract.DeriveCanonicalDigestV2(domain, value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func newControlplaneRedis(t *testing.T) *redis.Client {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "--bind", "127.0.0.1", "--port", portText,
		"--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() {
		_ = client.Close()
		if command.ProcessState == nil || !command.ProcessState.Exited() {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("redis-server did not become ready: %s", output.String())
	return nil
}
