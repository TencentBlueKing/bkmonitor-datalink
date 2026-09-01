package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
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

func TestSourceReconcilerConfirmsChangeAcrossIndependentRefreshAndRestart(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:reconcile", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	first, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || first.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("first refresh=(%#v, %v)", first, err)
	}
	if _, err := repository.LoadLatestPublication(ctx); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("unconfirmed candidate was published: %v", err)
	}
	changed := strings.Replace(string(documents[1]), `"threshold":90`, `"threshold":91`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", changed, 0).Err(); err != nil {
		t.Fatal(err)
	}

	// Recreate the reconciler: confirmation must come from the persisted prior
	// independent refresh, not process memory or a second read in one call.
	restarted, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Refresh(ctx, source, planner)
	if err != nil || second.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("second refresh=(%#v, %v)", second, err)
	}
	if _, err := repository.LoadLatestPublication(ctx); !errors.Is(err, controlplane.ErrSnapshotUnavailable) {
		t.Fatalf("changed second candidate was published: %v", err)
	}
	third, err := restarted.Refresh(ctx, source, planner)
	if err != nil || third.Status != controlplane.SourceRefreshPublished || third.Publication.PublicationEpoch == 0 {
		t.Fatalf("third refresh=(%#v, %v)", third, err)
	}
	fourth, err := restarted.Refresh(ctx, source, planner)
	if err != nil || fourth.Status != controlplane.SourceRefreshUnchanged || fourth.Publication != third.Publication {
		t.Fatalf("unchanged refresh=(%#v, %v), published=%#v", fourth, err, third)
	}
}

func TestSourceReconcilerPublishesOnlyRuntimeExecutablePlans(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := runtimeCompileIsolationDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002,1003]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002", "1003"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime-compile-isolation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompilerWithLimits(t, 2, 1)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("published=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QueryGroups) != 1 || len(snapshot.QueryGroups[0].Plans) != 1 {
		t.Fatalf("runtime executable Query Groups=%#v", snapshot.QueryGroups)
	}
	plan := snapshot.QueryGroups[0].Plans[0]
	if plan.Identity.StrategyID != "1001" || len(plan.Plan.StrategyIR.Levels) != 1 || plan.Plan.StrategyIR.Levels[0].Definition.LevelID != 1 {
		t.Fatalf("runtime executable Plan=%#v", plan)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionUnsupported, contract.ReasonLevelBudgetExceeded)
	assertAuditDisposition(t, repository, "1002", controlplane.DispositionUnsupported, contract.ReasonLevelBudgetExceeded)
	assertAuditDisposition(t, repository, "1003", controlplane.DispositionUnsupported, contract.ReasonPlanBudgetExceeded)
	assertNoAcceptedPlanDisposition(t, repository, "1002")
	assertNoAcceptedPlanDisposition(t, repository, "1003")

	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(180, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	activation, err := activator.Ensure(ctx, published.Publication)
	if err != nil || len(activation.Plans) != 1 || activation.Plans[0].Fact.Plan.StrategyID != "1001" {
		t.Fatalf("healthy initial activation=(%#v, %v)", activation, err)
	}
}

func TestSourceReconcilerRuntimeCompilerKeepsOnlyInvalidLastGood(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime-last-good", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	normal, stateSemantics := runtimePlanCompiler(t)
	strict, _ := runtimePlanCompilerWithBudgets(t, 1, 16, 4096)
	compiler := &revisionTerminalCompiler{normal: normal, strict: strict,
		invalid: map[string]struct{}{"1800000001": {}}, unsupported: map[string]struct{}{"1800000002": {}}}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}
	initialSnapshot, err := repository.LoadSnapshot(ctx, initial.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	initialPlans := plansByStrategy(initialSnapshot)

	changed := []json.RawMessage{
		withStrategyUpdateTime(t, documents[0], 1800000001),
		withStrategyUpdateTime(t, documents[1], 1800000002),
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(changed[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("changed pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("changed publish=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	plans := plansByStrategy(snapshot)
	if len(plans) != 1 || plans["1001"].PlanRevision != initialPlans["1001"].PlanRevision {
		t.Fatalf("runtime last-good Plans=%#v initial=%#v", plans, initialPlans)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionStaleConfig, contract.ReasonPlanInvalid)
	assertAuditDisposition(t, repository, "1002", controlplane.DispositionUnsupported, contract.ReasonPlanBudgetExceeded)
	assertNoAcceptedPlanDisposition(t, repository, "1001")
	assertNoAcceptedPlanDisposition(t, repository, "1002")
}

func TestSourceReconcilerMergesInvalidLevelLastGoodWithoutUnsupportedLevel(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	initialDocument := addThresholdLevels(t, realThresholdDocuments(t)[0], []uint32{2, 3}, []uint32{1, 1})
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(initialDocument), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime-level-last-good", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	normal, stateSemantics := runtimePlanCompiler(t)
	compiler := &revisionTerminalCompiler{normal: normal, strict: normal,
		invalid: map[string]struct{}{}, unsupported: map[string]struct{}{}, mixedLevels: map[string]struct{}{"1800000003": {}}}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}
	initialSnapshot, err := repository.LoadSnapshot(ctx, initial.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	initialPlan := initialSnapshot.QueryGroups[0].Plans[0]
	initialLevels := levelIRByID(initialPlan.Plan)

	changedDocument := withThresholdForLevel(t, withStrategyUpdateTime(t, initialDocument, 1800000003), 1, 81)
	changedDocument = withThresholdForLevel(t, changedDocument, 2, 82)
	changedCatalog, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{
		SourceID: "1001", Document: changedDocument,
		Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	}}, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	changedLevels := levelIRByID(changedCatalog.QueryGroups[0].Plans[0].Plan)
	if reflect.DeepEqual(changedLevels[2], initialLevels[2]) {
		t.Fatal("changed Level 2 fixture does not differ from last-good")
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(changedDocument), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("changed pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("changed publish=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QueryGroups) != 1 || len(snapshot.QueryGroups[0].Plans) != 1 {
		t.Fatalf("mixed Level snapshot=%#v", snapshot.QueryGroups)
	}
	plan := snapshot.QueryGroups[0].Plans[0]
	levels := levelIRByID(plan.Plan)
	if len(levels) != 2 || !reflect.DeepEqual(levels[1], changedLevels[1]) || !reflect.DeepEqual(levels[2], initialLevels[2]) {
		t.Fatalf("mixed Levels=%#v changed=%#v initial=%#v", levels, changedLevels, initialLevels)
	}
	if _, retainedUnsupported := levels[3]; retainedUnsupported {
		t.Fatalf("unsupported Level was retained: %#v", levels[3])
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionStaleConfig, contract.ReasonLevelInvalid)
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionUnsupported, contract.ReasonAlgorithmUnsupported)
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionAccepted, "")

	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(180, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activator.Ensure(ctx, published.Publication); err != nil {
		t.Fatal(err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := runtime.ReadInitialFrozenSchedule(ctx, snapshot.QueryGroups[0].Identity)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := schedule.FirstSlot()
	if !ok {
		t.Fatal("mixed Level schedule has no first Slot")
	}
	if _, err := runtime.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: schedule.Segment.QueryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: first, DuePlans: schedule.DuePlanRefs(first),
	}); err != nil {
		t.Fatalf("mixed Level frozen contract=%v", err)
	}
}

func TestSourceReconcilerExcludesMergedPlanWhenAuthoritativeRecompileFails(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	brokenDocument := addThresholdLevels(t, documents[0], []uint32{2, 3}, []uint32{1, 1})
	healthyDocument := withBusinessScope(t, documents[1], 3, "bkcc__3")
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for id, document := range map[string]json.RawMessage{"1001": brokenDocument, "1002": healthyDocument} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(document), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:runtime-merged-recompile", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	normal, stateSemantics := runtimePlanCompiler(t)
	compiler := &revisionTerminalCompiler{
		normal: normal, strict: normal, invalid: map[string]struct{}{}, unsupported: map[string]struct{}{},
		mixedLevels: map[string]struct{}{"1800000003": {}}, mergedInvalid: map[string]struct{}{"1800000003": {}},
	}
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}
	initialSnapshot, err := repository.LoadSnapshot(ctx, initial.Publication.SnapshotRevision)
	if err != nil || len(initialSnapshot.QueryGroups) != 2 {
		t.Fatalf("initial Query Groups=(%#v, %v)", initialSnapshot.QueryGroups, err)
	}

	changedDocument := withThresholdForLevel(t, withStrategyUpdateTime(t, brokenDocument, 1800000003), 1, 81)
	changedDocument = withThresholdForLevel(t, changedDocument, 2, 82)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(changedDocument), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("changed pending=(%#v, %v)", result, err)
	}
	published, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("changed publish=(%#v, %v)", published, err)
	}
	snapshot, err := repository.LoadSnapshot(ctx, published.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	plans := plansByStrategy(snapshot)
	if len(snapshot.QueryGroups) != 1 || len(plans) != 1 || plans["1002"].Identity.StrategyID != "1002" {
		t.Fatalf("locally excluded merged Plan snapshot=%#v", snapshot.QueryGroups)
	}
	assertNoAcceptedPlanDisposition(t, repository, "1001")
	assertNoAuditDisposition(t, repository, "1001", controlplane.DispositionStaleConfig)
	assertAuditDispositionExact(t, repository, "1001", "LEVEL", 2,
		controlplane.DispositionConfigRejected, contract.ReasonLevelInvalid)
	assertAuditDispositionExact(t, repository, "1001", "LEVEL", 3,
		controlplane.DispositionUnsupported, contract.ReasonAlgorithmUnsupported)
	assertAuditDispositionExact(t, repository, "1001", "PLAN", 0,
		controlplane.DispositionConfigRejected, contract.ReasonPlanInvalid)
	assertAuditDispositionExact(t, repository, "1002", "PLAN", 0,
		controlplane.DispositionAccepted, "")
}

func TestSourceReconcilerRetainsLastGoodForMissingAndInvalidObjectWhileHealthySiblingAdvances(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source := newRedisStrategySource(t, client)
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:last-good", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("initial pending=(%#v, %v)", result, err)
	}
	initial, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || initial.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("initial publish=(%#v, %v)", initial, err)
	}
	initialSnapshot, err := repository.LoadSnapshot(ctx, initial.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	initialPlans := plansByStrategy(initialSnapshot)

	// A strategy whose new cache object loses its authoritative identity fact
	// keeps the persisted last-good Plan. Recreating the reconciler proves that
	// this decision is based on repository state rather than process memory.
	var identityMissing map[string]any
	if err := json.Unmarshal(documents[0], &identityMissing); err != nil {
		t.Fatal(err)
	}
	delete(identityMissing, "space_uid")
	identityMissingPayload, err := json.Marshal(identityMissing)
	if err != nil {
		t.Fatal(err)
	}
	changedSibling := strings.Replace(string(documents[1]), `"threshold":90`, `"threshold":91`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", identityMissingPayload, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", changedSibling, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("identity missing pending=(%#v, %v)", result, err)
	}
	reconciler, err = controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	identityMissingResult, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || identityMissingResult.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("identity missing publish=(%#v, %v)", identityMissingResult, err)
	}
	identityMissingSnapshot, err := repository.LoadSnapshot(ctx, identityMissingResult.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	identityMissingPlans := plansByStrategy(identityMissingSnapshot)
	if identityMissingPlans["1001"].PlanRevision != initialPlans["1001"].PlanRevision ||
		identityMissingPlans["1002"].PlanRevision == initialPlans["1002"].PlanRevision {
		t.Fatalf("identity missing last-good=%#v initial=%#v", identityMissingPlans, initialPlans)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionSourceIncomplete, "SOURCE_IDENTITY_UNAVAILABLE")

	if err := client.Del(ctx, "bkmonitor.cache.strategy_1001").Err(); err != nil {
		t.Fatal(err)
	}
	changedSibling = strings.Replace(string(documents[1]), `"threshold":90`, `"threshold":92`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", changedSibling, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("missing pending=(%#v, %v)", result, err)
	}
	missing, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || missing.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("missing publish=(%#v, %v)", missing, err)
	}
	missingSnapshot, err := repository.LoadSnapshot(ctx, missing.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	missingPlans := plansByStrategy(missingSnapshot)
	if missingPlans["1001"].PlanRevision != initialPlans["1001"].PlanRevision ||
		missingPlans["1002"].PlanRevision == identityMissingPlans["1002"].PlanRevision {
		t.Fatalf("missing object last-good=%#v identity-missing=%#v", missingPlans, identityMissingPlans)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionSourceIncomplete, "SOURCE_OBJECT_INCOMPLETE")

	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", `{"id":1001,`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	changedSibling = strings.Replace(string(documents[1]), `"threshold":90`, `"threshold":93`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", changedSibling, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := reconciler.Refresh(ctx, source, planner); err != nil || result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("invalid pending=(%#v, %v)", result, err)
	}
	invalid, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || invalid.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("invalid publish=(%#v, %v)", invalid, err)
	}
	invalidSnapshot, err := repository.LoadSnapshot(ctx, invalid.Publication.SnapshotRevision)
	if err != nil {
		t.Fatal(err)
	}
	invalidPlans := plansByStrategy(invalidSnapshot)
	if invalidPlans["1001"].PlanRevision != initialPlans["1001"].PlanRevision ||
		invalidPlans["1002"].PlanRevision == missingPlans["1002"].PlanRevision {
		t.Fatalf("invalid object last-good=%#v missing=%#v", invalidPlans, missingPlans)
	}
	assertAuditDisposition(t, repository, "1001", controlplane.DispositionStaleConfig, "STRATEGY_DOCUMENT_INVALID")
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

func TestInitialScheduleActivatorPersistsOneNonAlignedBoundaryAcrossRestart(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:first-activation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := catalog.QueryGroups[0].Identity
	if _, err := runtime.ReadInitialFrozenSchedule(context.Background(), queryGroup); !errors.Is(err, controlplane.ErrScheduleUnavailable) {
		t.Fatalf("missing production activation error=%v", err)
	}

	var clockCalls atomic.Int32
	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		clockCalls.Add(1)
		return time.Unix(83, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := activator.Ensure(context.Background(), snapshot.Publication)
	if err != nil || state.Current != snapshot.Publication || state.RecordRevision != 1 {
		t.Fatalf("initial activation=(%#v, %v)", state, err)
	}
	if len(state.Plans) != 1 || state.Plans[0].Fact.Selected.RequiredFullSlots != 1 {
		t.Fatalf("initial Plan activation=%#v", state.Plans)
	}
	schedule, err := runtime.ReadInitialFrozenSchedule(context.Background(), queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	firstSlot, ok := schedule.FirstSlot()
	if schedule.Segment.Start != 83 || !ok || firstSlot != 120 {
		t.Fatalf("initial segment start=%d first slot=(%d,%v), want start=83 slot=120", schedule.Segment.Start, firstSlot, ok)
	}

	restartedClockCalls := 0
	restarted, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		restartedClockCalls++
		return time.Unix(999, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := restarted.Ensure(context.Background(), snapshot.Publication)
	if err != nil || reloaded.RecordRevision != 1 || reloaded.Current != snapshot.Publication {
		t.Fatalf("restart activation=(%#v, %v)", reloaded, err)
	}
	if clockCalls.Load() != 1 || restartedClockCalls != 0 {
		t.Fatalf("clock calls=(%d,%d), want (1,0)", clockCalls.Load(), restartedClockCalls)
	}
	reloadedSchedule, err := runtime.ReadInitialFrozenSchedule(context.Background(), queryGroup)
	if err != nil || reloadedSchedule.Segment.Start != 83 {
		t.Fatalf("restart schedule=(%#v, %v)", reloadedSchedule, err)
	}
}

func TestInitialScheduleActivatorPersistsEveryPublishedQueryGroup(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:first-activation-multi-qg", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := twoQueryGroupCatalog(t)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	activator, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(83, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := activator.Ensure(context.Background(), snapshot.Publication)
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if len(state.Plans) != 2 {
		t.Fatalf("activated Plans=%d, want 2", len(state.Plans))
	}

	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range catalog.QueryGroups {
		schedule, loadErr := runtime.ReadInitialFrozenSchedule(context.Background(), group.Identity)
		if loadErr != nil {
			t.Fatalf("ReadInitialFrozenSchedule(%s) error = %v", group.Identity, loadErr)
		}
		if schedule.Segment.QueryGroup != group.Identity || schedule.Segment.Start != 83 || len(schedule.Plans) != len(group.Plans) {
			t.Fatalf("schedule(%s) = %#v", group.Identity, schedule)
		}
	}
}

func TestInitialScheduleActivatorConcurrentCASKeepsWinnerFact(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:first-activation-race", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	catalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	snapshot, _, err := repository.PublishCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)

	var entered sync.WaitGroup
	entered.Add(2)
	release := make(chan struct{})
	newClock := func(unix int64) func() time.Time {
		return func() time.Time {
			entered.Done()
			<-release
			return time.Unix(unix, 0)
		}
	}
	first, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, newClock(83))
	if err != nil {
		t.Fatal(err)
	}
	second, err := controlplane.NewInitialScheduleActivator(repository, compiler, stateSemantics, newClock(97))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		state controlplane.ActivationState
		err   error
	}
	results := make(chan result, 2)
	go func() {
		state, runErr := first.Ensure(context.Background(), snapshot.Publication)
		results <- result{state: state, err: runErr}
	}()
	go func() {
		state, runErr := second.Ensure(context.Background(), snapshot.Publication)
		results <- result{state: state, err: runErr}
	}()
	entered.Wait()
	close(release)
	one, two := <-results, <-results
	if one.err != nil || two.err != nil || one.state.RecordRevision != 1 || !reflect.DeepEqual(two.state, one.state) {
		t.Fatalf("concurrent activations=(%#v,%#v)", one, two)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := runtime.ReadInitialFrozenSchedule(context.Background(), catalog.QueryGroups[0].Identity)
	if err != nil || (schedule.Segment.Start != 83 && schedule.Segment.Start != 97) {
		t.Fatalf("winner schedule=(%#v, %v)", schedule, err)
	}
}

func TestScheduleActivationReconcilerPersistsOnePublicationBoundaryAndExactHalfOpenSlots(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:publication-cutover", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(83, 0), time.Unix(90, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		at := clock[clockCalls]
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(context.Background(), oldSnapshot.Publication); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithSchedule(t, validCatalog(t, 81), 60, 0)
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconciler.Ensure(context.Background(), newSnapshot.Publication)
	if err != nil || state.Current != newSnapshot.Publication || state.RecordRevision != 2 {
		t.Fatalf("publication cutover=(%#v, %v)", state, err)
	}
	if clockCalls != 2 {
		t.Fatalf("publication clock calls=%d, want 2", clockCalls)
	}
	if _, err := reconciler.Ensure(context.Background(), newSnapshot.Publication); err != nil || clockCalls != 2 {
		t.Fatalf("winner reload error=%v clock calls=%d", err, clockCalls)
	}

	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := oldCatalog.QueryGroups[0].Identity
	oldSchedule, err := runtime.ReadFrozenSchedule(context.Background(), queryGroup, 83)
	if err != nil || oldSchedule.Segment.End == nil || *oldSchedule.Segment.End != 90 {
		t.Fatalf("old half-open Segment=(%#v, %v)", oldSchedule, err)
	}
	if first, ok := oldSchedule.FirstSlot(); ok || first != 120 {
		t.Fatalf("old [83,90) first Slot=(%d,%t), want candidate 120 without ownership", first, ok)
	}
	newSchedule, err := runtime.ReadFrozenSchedule(context.Background(), queryGroup, 90)
	if err != nil || newSchedule.Segment.Start != 90 || newSchedule.Segment.Publication.SnapshotRevision != newSnapshot.Publication.SnapshotRevision {
		t.Fatalf("new half-open Segment=(%#v, %v)", newSchedule, err)
	}
	if first, ok := newSchedule.FirstSlot(); !ok || first != 120 {
		t.Fatalf("new [90,+inf) first Slot=(%d,%t), want 120", first, ok)
	}
	if oldSchedule.Segment.Contains(90) || oldSchedule.Segment.Contains(120) || newSchedule.Segment.Contains(83) {
		t.Fatalf("Segment ownership overlaps: old=%#v new=%#v", oldSchedule.Segment, newSchedule.Segment)
	}
}

func TestScheduleActivationReconcilerConcurrentCASLoserReadsWinnerBoundary(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:publication-cutover-race", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	initial, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		return time.Unix(60, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initial.Ensure(context.Background(), oldSnapshot.Publication); err != nil {
		t.Fatal(err)
	}
	newCatalog := catalogWithSchedule(t, validCatalog(t, 81), 60, 0)
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}

	var entered sync.WaitGroup
	entered.Add(2)
	release := make(chan struct{})
	newClock := func(unix int64) func() time.Time {
		return func() time.Time {
			entered.Done()
			<-release
			return time.Unix(unix, 0)
		}
	}
	first, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, newClock(90))
	if err != nil {
		t.Fatal(err)
	}
	second, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, newClock(97))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		state controlplane.ActivationState
		err   error
	}
	results := make(chan result, 2)
	go func() {
		state, runErr := first.Ensure(context.Background(), newSnapshot.Publication)
		results <- result{state: state, err: runErr}
	}()
	go func() {
		state, runErr := second.Ensure(context.Background(), newSnapshot.Publication)
		results <- result{state: state, err: runErr}
	}()
	entered.Wait()
	close(release)
	one, two := <-results, <-results
	if one.err != nil || two.err != nil || !reflect.DeepEqual(one.state, two.state) || one.state.RecordRevision != 2 {
		t.Fatalf("concurrent publication activations states=(%#v,%#v) errors=(%v,%v)", one.state, two.state, one.err, two.err)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := runtime.ReadFrozenSchedule(context.Background(), oldCatalog.QueryGroups[0].Identity, 100)
	if err != nil || (schedule.Segment.Start != 90 && schedule.Segment.Start != 97) {
		t.Fatalf("winner cutover boundary=(%#v,%v)", schedule, err)
	}
}

func TestScheduleActivationReconcilerProjectsQueryIdentityChangeAsIndependentNewAndRetiredGroups(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:query-identity-cutover", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	oldCatalog := catalogWithQueryTable(t, "system.cpu")
	oldSnapshot, _, err := repository.PublishCatalog(context.Background(), oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(60, 0), time.Unix(90, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconciler(repository, compiler, stateSemantics, func() time.Time {
		at := clock[clockCalls]
		clockCalls++
		return at
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(context.Background(), oldSnapshot.Publication); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithQueryTable(t, "system.mem")
	newSnapshot, _, err := repository.PublishCatalog(context.Background(), newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconciler.Ensure(context.Background(), newSnapshot.Publication)
	if err != nil {
		t.Fatal(err)
	}
	oldGroup := oldCatalog.QueryGroups[0].Identity
	newGroup := newCatalog.QueryGroups[0].Identity
	if oldGroup == newGroup || len(state.Draining) != 1 || state.Draining[0].QueryGroup != oldGroup || state.Draining[0].RetiredBoundary != 90 {
		t.Fatalf("query identity transition old=%s new=%s draining=%#v", oldGroup, newGroup, state.Draining)
	}
	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	retiredAt, retired, err := runtime.ReadScheduleRetirement(context.Background(), oldGroup)
	if err != nil || !retired || retiredAt != 90 {
		t.Fatalf("old Query Group retirement=(%d,%t,%v)", retiredAt, retired, err)
	}
	newSchedule, err := runtime.ReadInitialFrozenSchedule(context.Background(), newGroup)
	if err != nil || newSchedule.Segment.Start != 90 {
		t.Fatalf("new Query Group activation=(%#v,%v)", newSchedule, err)
	}
	next, err := runtime.NextSlotAfter(context.Background(), oldGroup, 60)
	if err != nil || next != 90 {
		t.Fatalf("retired Query Group terminal Progress watermark=(%d,%v), want 90", next, err)
	}
}

func TestScheduleActivationReconcilerReactivatesDrainedQueryGroupOnSameProgressTimeline(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:same-qg-reactivation", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	initialCatalog := catalogWithSchedule(t, validCatalog(t, 80), 60, 0)
	initialSnapshot, _, err := repository.PublishCatalog(context.Background(), initialCatalog)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := initialCatalog.QueryGroups[0].Identity
	progress := &activationProgressReader{byGroup: map[execution.QueryGroupIdentity]execution.ProgressLoadResult{
		queryGroup: {Status: execution.ProgressMissing},
	}}
	compiler, stateSemantics := runtimePlanCompiler(t)
	clock := []time.Time{time.Unix(60, 0), time.Unix(90, 0), time.Unix(180, 0)}
	clockCalls := 0
	reconciler, err := controlplane.NewScheduleActivationReconcilerWithProgress(
		repository, compiler, stateSemantics, progress, func() time.Time {
			at := clock[clockCalls]
			clockCalls++
			return at
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Ensure(context.Background(), initialSnapshot.Publication); err != nil {
		t.Fatal(err)
	}

	emptyCatalog := controlplane.Catalog{QueryGroups: []controlplane.QueryGroup{}}
	emptyCatalog.SnapshotRevision = execution.SnapshotRevision(mustDigest(t, "alarmd-strategy-snapshot-v1", emptyCatalog.QueryGroups))
	emptySnapshot, _, err := repository.PublishCatalog(context.Background(), emptyCatalog)
	if err != nil {
		t.Fatal(err)
	}
	retired, err := reconciler.Ensure(context.Background(), emptySnapshot.Publication)
	if err != nil || len(retired.Draining) != 1 || retired.Draining[0].QueryGroup != queryGroup || retired.Draining[0].RetiredBoundary != 90 {
		t.Fatalf("retirement=(%#v,%v)", retired, err)
	}
	progress.byGroup[queryGroup] = execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &execution.ScheduleProgress{
		Identity: execution.ProgressIdentity{QueryGroup: queryGroup}, NextSlot: 90, LastFullSlot: 60,
		LastCompletionKind: execution.CompletionFull,
	}}

	reenabledCatalog := catalogWithSchedule(t, validCatalog(t, 82), 60, 0)
	if reenabledCatalog.QueryGroups[0].Identity != queryGroup {
		t.Fatalf("query identity changed across reactivation: old=%s new=%s", queryGroup, reenabledCatalog.QueryGroups[0].Identity)
	}
	reenabledSnapshot, _, err := repository.PublishCatalog(context.Background(), reenabledCatalog)
	if err != nil {
		t.Fatal(err)
	}
	active, err := reconciler.Ensure(context.Background(), reenabledSnapshot.Publication)
	if err != nil || active.RecordRevision != 3 || len(active.Draining) != 0 || len(active.Plans) != 1 {
		t.Fatalf("reactivation=(%#v,%v)", active, err)
	}
	if !active.Plans[0].Fact.Selected.ForceWarming ||
		active.Plans[0].Fact.Selected.StateApplyEpoch != execution.StateApplyEpoch(reenabledSnapshot.Publication.PublicationEpoch) {
		t.Fatalf("reactivated Plan activation=%#v", active.Plans[0])
	}

	runtime, err := controlplane.NewRedisCatalogRuntime(repository, compiler, stateSemantics, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, retiredNow, err := runtime.ReadScheduleRetirement(context.Background(), queryGroup); err != nil || retiredNow {
		t.Fatalf("reactivated retirement=(%t,%v)", retiredNow, err)
	}
	newSchedule, err := runtime.ReadFrozenSchedule(context.Background(), queryGroup, 180)
	if err != nil || newSchedule.Segment.Start != 180 || newSchedule.Segment.Publication.SnapshotRevision != reenabledSnapshot.Publication.SnapshotRevision {
		t.Fatalf("reactivated Schedule=(%#v,%v)", newSchedule, err)
	}
	if _, err := runtime.ReadFrozenSchedule(context.Background(), queryGroup, 120); !errors.Is(err, controlplane.ErrScheduleUnavailable) {
		t.Fatalf("inactive tombstone interval error=%v", err)
	}
	next, err := runtime.NextSlotAfter(context.Background(), queryGroup, 60)
	if err != nil || next != 180 {
		t.Fatalf("same Progress successor=(%d,%v), want 180", next, err)
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
	var secondValue map[string]any
	if err := json.Unmarshal(documents[1], &secondValue); err != nil {
		t.Fatal(err)
	}
	secondValue["bk_biz_id"] = 3
	secondValue["space_uid"] = "bkcc__3"
	second, err := json.Marshal(secondValue)
	if err != nil {
		t.Fatal(err)
	}
	planner := queryPlannerFunc(func(_ context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
		return queryFactsFor(t, source.Identity.BusinessID, source.Identity.SpaceScope), nil
	})
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{
			{SourceID: "1001", Document: documents[0], Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
			{SourceID: "1002", Document: second, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
		},
		Planner: planner,
	})
	if err != nil || len(catalog.QueryGroups) != 2 {
		t.Fatalf("two Query Group catalog=(%#v, %v)", catalog, err)
	}
	return catalog
}

func catalogWithQueryTable(t *testing.T, tableID string) controlplane.Catalog {
	t.Helper()
	document := realThresholdDocuments(t)[0]
	planner := queryPlannerFunc(func(context.Context, controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
		facts := queryFacts(t)
		facts.QueryRevision = ""
		facts.QueryList = append([]execution.QueryClause(nil), facts.QueryList...)
		facts.QueryList[0].TableID = tableID
		return execution.BuildQueryPlanFacts(facts)
	})
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: planner,
	})
	if err != nil || len(catalog.QueryGroups) != 1 {
		t.Fatalf("query table catalog=(%#v,%v)", catalog, err)
	}
	return catalog
}

type queryPlannerFunc func(context.Context, controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error)

type activationProgressReader struct {
	byGroup map[execution.QueryGroupIdentity]execution.ProgressLoadResult
}

func (reader *activationProgressReader) LoadProgress(
	_ context.Context,
	identity execution.ProgressIdentity,
) (execution.ProgressLoadResult, error) {
	result, ok := reader.byGroup[identity.QueryGroup]
	if !ok {
		return execution.ProgressLoadResult{}, errors.New("missing activation Progress fixture")
	}
	return result, nil
}

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
	return runtimePlanCompilerWithLimits(t, 16, 4096)
}

func runtimePlanCompilerWithLimits(
	t *testing.T,
	maxLevels int,
	maxRecoveryWindows uint32,
) (*strategy.PlanCompiler, strategy.StateSemantics) {
	t.Helper()
	return runtimePlanCompilerWithBudgets(t, 64<<10, maxLevels, maxRecoveryWindows)
}

func runtimePlanCompilerWithBudgets(
	t *testing.T,
	maxPlanBytes int,
	maxLevels int,
	maxRecoveryWindows uint32,
) (*strategy.PlanCompiler, strategy.StateSemantics) {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: maxPlanBytes, MaxLevelsPerPlan: maxLevels, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: maxRecoveryWindows, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
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

func runtimeCompileIsolationDocuments(t *testing.T) []json.RawMessage {
	t.Helper()
	documents := realThresholdDocuments(t)
	partial := addThresholdLevels(t, documents[0], []uint32{2}, []uint32{2})
	allLevelsTerminal := documents[1]
	planTerminal := addThresholdLevels(t, documents[0], []uint32{2, 3}, []uint32{1, 1})
	var planTerminalValue map[string]any
	if err := json.Unmarshal(planTerminal, &planTerminalValue); err != nil {
		t.Fatal(err)
	}
	planTerminalValue["id"] = float64(1003)
	planTerminalValue["bk_biz_id"] = float64(3)
	planTerminalValue["space_uid"] = "bkcc__3"
	items := planTerminalValue["items"].([]any)
	items[0].(map[string]any)["id"] = float64(13)
	planTerminal, err := json.Marshal(planTerminalValue)
	if err != nil {
		t.Fatal(err)
	}
	return []json.RawMessage{partial, allLevelsTerminal, planTerminal}
}

type revisionTerminalCompiler struct {
	normal        *strategy.PlanCompiler
	strict        *strategy.PlanCompiler
	invalid       map[string]struct{}
	unsupported   map[string]struct{}
	mixedLevels   map[string]struct{}
	mergedInvalid map[string]struct{}
}

func (compiler *revisionTerminalCompiler) Compile(
	ctx context.Context,
	request strategy.CompileRequest,
) (strategy.CompileResult, error) {
	revision := request.Plan.StrategyRef.Revision
	if _, invalid := compiler.invalid[revision]; invalid {
		request.Plan.StrategyIR.Schema.Name = "invalid-strategy-ir"
		return compiler.normal.Compile(ctx, request)
	}
	if _, unsupported := compiler.unsupported[revision]; unsupported {
		return compiler.strict.Compile(ctx, request)
	}
	if _, mixed := compiler.mixedLevels[revision]; mixed && len(request.Plan.StrategyIR.Levels) == 3 {
		for index := range request.Plan.StrategyIR.Levels {
			level := &request.Plan.StrategyIR.Levels[index]
			switch level.Definition.LevelID {
			case 2:
				level.TriggerPlan.Type = "INVALID_TRIGGER"
			case 3:
				level.DetectPlan.Algorithms[0].Type = "UnsupportedForTest"
			}
		}
	}
	if _, invalid := compiler.mergedInvalid[revision]; invalid && len(request.Plan.StrategyIR.Levels) == 2 {
		request.Plan.StrategyIR.Schema.Name = "invalid-strategy-ir"
	}
	return compiler.normal.Compile(ctx, request)
}

func withStrategyUpdateTime(t *testing.T, document json.RawMessage, updateTime int64) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["update_time"] = float64(updateTime)
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func withThresholdForLevel(t *testing.T, document json.RawMessage, levelID uint32, threshold int) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	algorithms := value["items"].([]any)[0].(map[string]any)["algorithms"].([]any)
	for _, raw := range algorithms {
		algorithm := raw.(map[string]any)
		if uint32(algorithm["level"].(float64)) != levelID {
			continue
		}
		groups := algorithm["config"].([]any)
		conditions := groups[0].([]any)
		conditions[0].(map[string]any)["threshold"] = float64(threshold)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func withBusinessScope(t *testing.T, document json.RawMessage, businessID int, spaceScope string) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["bk_biz_id"] = float64(businessID)
	value["space_uid"] = spaceScope
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func levelIRByID(plan contract.EvaluationPlanV2) map[uint32]contract.LevelIRV2 {
	result := make(map[uint32]contract.LevelIRV2, len(plan.StrategyIR.Levels))
	for _, level := range plan.StrategyIR.Levels {
		result[level.Definition.LevelID] = level
	}
	return result
}

func addThresholdLevels(
	t *testing.T,
	document json.RawMessage,
	levels []uint32,
	recoveryWindows []uint32,
) json.RawMessage {
	t.Helper()
	if len(levels) != len(recoveryWindows) {
		t.Fatal("level fixtures must have one recovery window per Level")
	}
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	item := value["items"].([]any)[0].(map[string]any)
	algorithms := item["algorithms"].([]any)
	detects := value["detects"].([]any)
	for index, levelID := range levels {
		algorithm := cloneJSONMap(t, algorithms[0])
		algorithm["level"] = float64(levelID)
		algorithms = append(algorithms, algorithm)
		detect := cloneJSONMap(t, detects[0])
		detect["level"] = float64(levelID)
		detect["recovery_config"] = map[string]any{"check_window": float64(recoveryWindows[index])}
		detects = append(detects, detect)
	}
	item["algorithms"] = algorithms
	value["detects"] = detects
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func cloneJSONMap(t *testing.T, source any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertNoAcceptedPlanDisposition(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Scope == "PLAN" && item.Disposition == controlplane.DispositionAccepted {
			t.Fatalf("unexpected accepted Plan disposition source=%s: %#v", sourceID, audit)
		}
	}
}

func mustDigest(t *testing.T, domain string, value any) string {
	t.Helper()
	digest, err := contract.DeriveCanonicalDigestV2(domain, value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func newRedisStrategySource(t *testing.T, client redis.Cmdable) *controlplane.LegacyRedisStrategySource {
	t.Helper()
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func plansByStrategy(snapshot controlplane.PublishedSnapshot) map[string]controlplane.FrozenPlan {
	result := make(map[string]controlplane.FrozenPlan)
	for _, group := range snapshot.QueryGroups {
		for _, plan := range group.Plans {
			result[plan.Identity.StrategyID] = plan
		}
	}
	return result
}

func assertAuditDisposition(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
	disposition controlplane.Disposition,
	reason string,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Disposition == disposition && item.Reason == reason {
			return
		}
	}
	t.Fatalf("missing audit disposition source=%s disposition=%s reason=%s: %#v", sourceID, disposition, reason, audit)
}

func assertAuditDispositionExact(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
	scope string,
	levelID uint32,
	disposition controlplane.Disposition,
	reason string,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Scope == scope && item.LevelID == levelID &&
			item.Disposition == disposition && item.Reason == reason {
			return
		}
	}
	t.Fatalf("missing exact audit disposition source=%s scope=%s level=%d disposition=%s reason=%s: %#v",
		sourceID, scope, levelID, disposition, reason, audit)
}

func assertNoAuditDisposition(
	t *testing.T,
	repository *controlplane.RedisCatalogRepository,
	sourceID string,
	disposition controlplane.Disposition,
) {
	t.Helper()
	audit, err := repository.LoadLatestAudit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range audit.Dispositions {
		if item.SourceID == sourceID && item.Disposition == disposition {
			t.Fatalf("unexpected audit disposition source=%s disposition=%s: %#v", sourceID, disposition, audit)
		}
	}
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
