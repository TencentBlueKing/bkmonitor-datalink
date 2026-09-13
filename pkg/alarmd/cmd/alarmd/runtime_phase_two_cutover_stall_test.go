// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// cutoverStallFixture is a production bundle against Redis whose Query Group
// for strategy 1001 holds the persisted state of a Query Group caught by a
// publication, expressed on a one-minute grid:
//
//	base       first Slot of the initial Segment, executed to a FULL completion
//	base+60    the Worker begins the next Slot under the still-open initial
//	           Segment (BeginSlot persists the unfinished projection) and the
//	           query is deferred by readiness
//	base+59    a publication is activated; the reconciler took its boundary
//	           before the Worker began base+60 and persisted the cutover
//	           afterwards, so the initial Segment closes at base+59 and the
//	           new Segment [base+59, ...) owns base+60
//
// The clock is left at base+60 with the unfinished projection still on the
// initial Segment's contract.
type cutoverStallFixture struct {
	t                *testing.T
	cfg              config.Config
	redisClient      *redis.Client
	bundle           *phaseTwoWorkerBundle
	production       *productionPhaseTwoOwnership
	repository       *controlplane.RedisCatalogRepository
	runner           phaseTwoQueryGroupRuntime
	session          *ownership.Session
	progressStore    *progress.Store
	queryGroup       execution.QueryGroupIdentity
	initialSchedule  execution.FrozenQueryGroupSchedule
	openSchedule     execution.FrozenQueryGroupSchedule
	base             int64
	boundary         execution.EvaluationTime
	firstNewSlot     execution.EvaluationTime
	inFlight         execution.ScheduleProgress
	oldProjection    execution.UnfinishedSlotProjection
	clock            *atomic.Int64
	now              func() time.Time
	uqCalls          *atomic.Int64
	uqCallsAtCutover int64
	// uqHosts selects the series the UQ stub returns for a query ending at
	// the given Unix second; nil serves one constant series.
	uqHosts func(end int64) []string
	// uqPartial marks the UQ answer for a query ending at the given Unix
	// second as partial, so the PRIMARY input is genuinely incomplete; nil
	// answers every query FULL.
	uqPartial      func(end int64) bool
	observationsMu sync.Mutex
	observations   []observability.Observation
}

func (fixture *cutoverStallFixture) observed() []observability.Observation {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	return append([]observability.Observation(nil), fixture.observations...)
}

func (fixture *cutoverStallFixture) progress(ctx context.Context) execution.ScheduleProgress {
	fixture.t.Helper()
	return loadPhaseTwoProgress(fixture.t, ctx, fixture.production, fixture.queryGroup)
}

func newCutoverStalledFixture(t *testing.T, configure func(*config.Config)) *cutoverStallFixture {
	t.Helper()
	fixture := startCutoverFixture(t, configure)
	fixture.stallInFlightAcrossCutover(context.Background(), fixture.base, fixture.redisClient, fixture.bundle)
	return fixture
}

// startCutoverFixture opens the production bundle on the initial publication
// and completes the first Slot of the initial Segment FULL. It returns with
// the cursor at the second grid point and no Slot in flight, so a test can
// choose how the Query Group falls behind before the cutover.
func startCutoverFixture(t *testing.T, configure func(*config.Config)) *cutoverStallFixture {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installCutoverStallStrategies(t, ctx, redisClient, "system.mem", 1725000000)

	base := time.Now().Unix()
	base -= base % 60
	fixture := &cutoverStallFixture{t: t, redisClient: redisClient, base: base, clock: &atomic.Int64{}, uqCalls: &atomic.Int64{}}
	fixture.clock.Store(base * 1000)
	fixture.now = func() time.Time { return time.UnixMilli(fixture.clock.Load()) }

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.uqCalls.Add(1)
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil || end <= 0 {
			end = fixture.clock.Load() / 1000
		}
		if end > 1_000_000_000_000 {
			end /= 1000
		}
		hosts := []string{"127.0.0.1"}
		if fixture.uqHosts != nil {
			hosts = fixture.uqHosts(end)
		}
		series := make([]string, 0, len(hosts))
		for _, host := range hosts {
			series = append(series, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["`+host+`"],"values":[[`+
				strconv.FormatInt((end-1)*1000, 10)+`,5]]}`)
		}
		partial := "false"
		if fixture.uqPartial != nil && fixture.uqPartial(end) {
			partial = "true"
		}
		_, _ = writer.Write([]byte(`{"series":[` + strings.Join(series, ",") + `],"status":null,"trace_id":"cutover-stall","is_partial":` + partial + `,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-cutover-stall"
	withCompatibilityOutput(&cfg, address)
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(500 * time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)
	if configure != nil {
		configure(&cfg)
	}
	fixture.cfg = cfg

	additionalObserver := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		fixture.observationsMu.Lock()
		defer fixture.observationsMu.Unlock()
		fixture.observations = append(fixture.observations, observation)
	})
	events := &recordingPhaseTwoEventSink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: fixture.now, HTTPClient: uqServer.Client(), AdditionalObserver: additionalObserver,
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	t.Cleanup(func() {
		if shutdownErr := bundle.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("phase-two production Shutdown() error = %v", shutdownErr)
		}
	})
	fixture.bundle = bundle
	if len(bundle.queryGroups) != 2 || len(bundle.runners) != 2 {
		t.Fatalf("initial Query Groups/runners = %v/%d, want two", bundle.queryGroups, len(bundle.runners))
	}
	fixture.production = bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	fixture.repository = bundle.dependencies.Control.(*productionPhaseTwoControl).dependencies.Repository.(*controlplane.RedisCatalogRepository)
	for _, candidate := range bundle.queryGroups {
		schedule, scheduleErr := fixture.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, candidate)
		if scheduleErr != nil || len(schedule.Plans) != 1 {
			t.Fatalf("Query Group %s schedule=%+v error=%v", candidate, schedule, scheduleErr)
		}
		if schedule.Plans[0].Identity.StrategyID == "1001" {
			fixture.queryGroup, fixture.initialSchedule = candidate, schedule
		}
	}
	if fixture.queryGroup == "" {
		t.Fatal("strategy 1001 has no Query Group")
	}
	if fixture.initialSchedule.Segment.Start != execution.EvaluationTime(base) || fixture.initialSchedule.Plans[0].Spec.EvaluationIntervalSeconds != 60 {
		t.Fatalf("initial Segment = %+v, want start %d on a 60s grid", fixture.initialSchedule.Segment, base)
	}
	fixture.runner = bundle.runners[fixture.queryGroup].runner
	progressStore, ok := fixture.production.dependencies.Progress.(*progress.Store)
	if !ok {
		t.Fatalf("production Progress store type = %T", fixture.production.dependencies.Progress)
	}
	fixture.progressStore = progressStore
	fixture.session = fixture.runner.(*productionPhaseTwoQueryGroup).session

	// Step 1: the first Slot of the initial Segment completes FULL.
	fixture.clock.Store((base + 1) * 1000)
	result, attempted, err := fixture.runner.RunOne(ctx)
	if err != nil || !attempted || !result.Completed {
		t.Fatalf("first Slot RunOne = (%+v, %t, %v), want a completed Slot", result, attempted, err)
	}
	committed := fixture.progress(ctx)
	if committed.LastFullSlot != execution.EvaluationTime(base) || committed.NextSlot != execution.EvaluationTime(base+60) || committed.UnfinishedSlot != nil {
		t.Fatalf("Progress after first Slot = %+v, want LastFullSlot=%d NextSlot=%d", committed, base, base+60)
	}

	return fixture
}

// stallInFlightAcrossCutover begins the second grid point under the initial
// Segment (the query is deferred by readiness, so the unfinished projection
// stays persisted) and then cuts over to a publication whose boundary
// precedes that Slot.
func (fixture *cutoverStallFixture) stallInFlightAcrossCutover(ctx context.Context, base int64, redisClient *redis.Client, bundle *phaseTwoWorkerBundle) {
	t := fixture.t
	t.Helper()
	// Step 2: the next grid point is begun under the still-open initial
	// Segment; the query is deferred by readiness, so the unfinished projection
	// stays persisted with the initial Segment's contract.
	fixture.firstNewSlot = execution.EvaluationTime(base + 60)
	fixture.clock.Store(int64(fixture.firstNewSlot) * 1000)
	result, attempted, err := fixture.runner.RunOne(ctx)
	if err != nil || !attempted || result.Completed {
		t.Fatalf("deferred Slot RunOne = (%+v, %t, %v), want an attempted, uncompleted Slot", result, attempted, err)
	}
	fixture.inFlight = fixture.progress(ctx)
	if fixture.inFlight.NextSlot != fixture.firstNewSlot || fixture.inFlight.UnfinishedSlot == nil ||
		fixture.inFlight.UnfinishedSlot.Contract.ScheduleSegmentStart != fixture.initialSchedule.Segment.Start {
		t.Fatalf("Progress with in-flight Slot = %+v, want unfinished Slot %d on Segment start %d", fixture.inFlight, fixture.firstNewSlot, fixture.initialSchedule.Segment.Start)
	}
	fixture.oldProjection = *fixture.inFlight.UnfinishedSlot
	fixture.uqCallsAtCutover = fixture.uqCalls.Load()

	// Step 3: a publication that changes the query revision of the sibling
	// strategy cuts over the sibling; this Query Group's Segment is made to cut
	// too by stripping the content it names (stripSegmentContent). The reconciler's
	// boundary is one second before the Slot begun in step 2: in production the
	// reconciler reads its clock before compiling and persists afterwards, so
	// the boundary precedes a Slot the Worker already began. The first refresh
	// publishes the candidate, the second confirms and activates it; the
	// activation boundary is the clock of the second one.
	installCutoverStallStrategies(t, ctx, redisClient, "system.disk", 1725000600)
	stripSegmentContent(t, ctx, redisClient, productionPhaseTwoPrefix(fixture.cfg.Redis.StatePrefix, "catalog"), fixture.queryGroup)
	fixture.boundary = fixture.firstNewSlot - 1
	fixture.clock.Store(int64(fixture.boundary) * 1000)
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("publication refresh error = %v", err)
	}
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("confirming refresh error = %v", err)
	}
	closedSchedule, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, execution.EvaluationTime(base))
	if err != nil || closedSchedule.Segment.End == nil || *closedSchedule.Segment.End != fixture.boundary {
		t.Fatalf("initial Segment after cutover = (%+v, %v), want End=%d", closedSchedule.Segment, err, fixture.boundary)
	}
	fixture.openSchedule, err = fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, fixture.firstNewSlot)
	if err != nil || fixture.openSchedule.Segment.Start != fixture.boundary || fixture.openSchedule.Segment.End != nil ||
		fixture.openSchedule.Segment.Publication.SnapshotRevision == fixture.initialSchedule.Segment.Publication.SnapshotRevision {
		t.Fatalf("new Segment after cutover = (%+v, %v), want an open Segment at %d under a new publication", fixture.openSchedule.Segment, err, fixture.boundary)
	}
	newFirst, ok := fixture.openSchedule.FirstSlot()
	if !ok || newFirst != fixture.firstNewSlot {
		t.Fatalf("new Segment first Slot = (%d, %t), want %d", newFirst, ok, fixture.firstNewSlot)
	}
	fixture.clock.Store(int64(fixture.firstNewSlot) * 1000)
	t.Logf("cutover: old Segment [%d,%d) -> new Segment [%d,open) first Slot %d; Progress NextSlot=%d LastFullSlot=%d gap=%v unfinished Segment start=%d",
		fixture.initialSchedule.Segment.Start, fixture.boundary, fixture.boundary, newFirst, fixture.inFlight.NextSlot, fixture.inFlight.LastFullSlot,
		fixture.inFlight.CurrentOrRecentGap, fixture.oldProjection.Contract.ScheduleSegmentStart)
}

// The newer Segment supersedes the persisted projection: BeginSlot commits,
// the Worker finalizes base+60 on the new Segment and the cursor moves on.
// Before the fix BeginSlot returned a deterministic error for the same Slot
// forever and the Coordinator mapped it to BLOCKED_EXACT_SET_UNAVAILABLE.
func TestProductionPhaseTwoCutoverStallsUnfinishedSlotOnNewSegment(t *testing.T) {
	fixture := newCutoverStalledFixture(t, nil)
	ctx := context.Background()
	runner, production, queryGroup := fixture.runner, fixture.production, fixture.queryGroup
	firstNewSlot, boundary := fixture.firstNewSlot, fixture.boundary

	// Step 4: drive the production SlotSource, BeginSlot and finalization
	// directly through the bundle's (cached) control-plane readers.
	fixture.clock.Store(int64(firstNewSlot)*1000 + 1000)
	cached := cutoverStallProbe{
		label: "cached control reads", catalog: production.dependencies.Catalog, progress: fixture.progressStore,
		repository: fixture.repository,
	}
	cachedOutcome := cached.run(t, ctx, production, fixture.session, queryGroup, fixture.inFlight, fixture.oldProjection, fixture.now)

	// Step 5: the same probes through fresh readers whose control read cache
	// is cold, so every activation and timeline read transfers the body.
	cfg := fixture.cfg
	freshRepository, err := controlplane.NewRedisCatalogRepository(
		fixture.redisClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), cfg.PhaseTwo.Control.CatalogTTL.Duration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	runtimeSemantics, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	freshCatalog, err := controlplane.NewRedisCatalogRuntime(freshRepository, compiler, strategy.StateSemantics{
		StateSchemaVersion:          runtimeSemantics.StateSchemaVersion,
		CodecSemanticsVersion:       runtimeSemantics.CodecSemanticsVersion,
		IdentitySchemaDigest:        runtimeSemantics.IdentitySchemaDigest,
		SourceTimeSemanticsVersion:  runtimeSemantics.SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: runtimeSemantics.HistoryCellSemanticsVersion,
	}, cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration())
	if err != nil {
		t.Fatal(err)
	}
	controlStore, ok := production.dependencies.Store.(progress.ControlStore)
	if !ok {
		t.Fatalf("production ownership store type = %T", production.dependencies.Store)
	}
	freshProgress, err := progress.NewStore(progress.StoreOptions{
		Prefix: productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"), Control: controlStore,
		Slots: freshCatalog, Now: fixture.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := cutoverStallProbe{
		label: "cold control reads", catalog: freshCatalog, progress: freshProgress, repository: freshRepository,
	}
	freshOutcome := fresh.run(t, ctx, production, fixture.session, queryGroup, fixture.inFlight, fixture.oldProjection, fixture.now)
	if cachedOutcome != freshOutcome {
		t.Errorf("outcome differs between cached and cold control reads:\n cached=%+v\n cold=%+v", cachedOutcome, freshOutcome)
	}
	if cachedOutcome.beginErr != "" || cachedOutcome.beginStatus != execution.ProgressCommitted ||
		cachedOutcome.sourceSlot != firstNewSlot || cachedOutcome.sourceSegmentStart != boundary || !cachedOutcome.sourceNewPublication ||
		cachedOutcome.storeNextAfter != firstNewSlot || cachedOutcome.newFinalization != execution.FinalizationQueryRequired {
		t.Fatalf("probe after the cutover = %+v, want the Slot %d re-frozen on Segment %d, BeginSlot committed and QUERY_REQUIRED", cachedOutcome, firstNewSlot, boundary)
	}
	superseded := fixture.progress(ctx)
	if superseded.NextSlot != firstNewSlot || superseded.LastFullSlot != execution.EvaluationTime(fixture.base) || superseded.CurrentOrRecentGap != nil ||
		superseded.UnfinishedSlot == nil || superseded.UnfinishedSlot.Contract.ScheduleSegmentStart != boundary ||
		superseded.UnfinishedSlot.Contract.SnapshotRevision != fixture.openSchedule.Segment.Publication.SnapshotRevision {
		t.Fatalf("Progress after the superseding BeginSlot = %+v, want the unfinished Slot re-projected on Segment %d", superseded, boundary)
	}

	// Step 6: the real Runner path finalizes the Slot on the new Segment.
	completionsBeforeRunner := len(fixture.observed())
	var result execution.SlotExecutionResult
	var attempted bool
	for tick := 1; tick <= 4 && !result.Completed; tick++ {
		nextAt := runner.NextReadyAt()
		at := fixture.now().Add(time.Second)
		if nextAt.After(at) {
			at = nextAt.Add(time.Millisecond)
		}
		fixture.clock.Store(at.UnixMilli())
		result, attempted, err = runner.RunOne(ctx)
		if err != nil {
			t.Fatalf("tick %d RunOne error = %v", tick, err)
		}
		t.Logf("tick %d at %d: attempted=%t completed=%t result=%s reason=%s", tick, at.Unix(), attempted, result.Completed, result.Result, result.ReasonCode)
		if attempted && !result.Completed {
			t.Fatalf("tick %d returned %s %s for Slot %d, want the Slot completed on the new Segment", tick, result.Result, result.ReasonCode, firstNewSlot)
		}
	}
	if !result.Completed || result.CompletionKind != execution.CompletionFull {
		t.Fatalf("Runner result after the cutover = %+v, want a FULL completion of Slot %d", result, firstNewSlot)
	}
	if fixture.uqCalls.Load() != fixture.uqCallsAtCutover+1 {
		t.Fatalf("UQ calls after the cutover = %d, want exactly one query for Slot %d", fixture.uqCalls.Load()-fixture.uqCallsAtCutover, firstNewSlot)
	}
	advanced := fixture.progress(ctx)
	if advanced.NextSlot != firstNewSlot+60 || advanced.LastFullSlot != firstNewSlot || advanced.UnfinishedSlot != nil || advanced.CurrentOrRecentGap != nil {
		t.Fatalf("Progress after the Runner = %+v, want LastFullSlot=%d NextSlot=%d without an unfinished Slot", advanced, firstNewSlot, firstNewSlot+60)
	}

	for _, observation := range fixture.observed()[completionsBeforeRunner:] {
		if observation.Stage != observability.StageSlotCompleted {
			continue
		}
		t.Logf("slot_completed observation: result=%s reason=%s evaluation_time=%d segment_start=%d err=%v",
			observation.Result, observation.ReasonCode, observation.Trace.EvaluationTime, observation.Trace.ScheduleSegmentStart, observation.Err)
		if observation.Result == observability.ResultRetrying || observation.ReasonCode == observability.ReasonCode(contract.ReasonBlockedExactSetUnavailable) ||
			observation.ReasonCode == observability.ReasonCode(contract.ReasonProgressBeginFailed) || observation.ReasonCode == observability.ReasonCode(contract.ReasonProgressBeginRejected) {
			t.Errorf("slot_completed after the cutover = result %s reason %s, want no retrying result", observation.Result, observation.ReasonCode)
		}
	}
}

type cutoverStallOutcome struct {
	sourceSlot           execution.EvaluationTime
	sourceSegmentStart   execution.EvaluationTime
	sourceNewPublication bool
	storeNextAfter       execution.EvaluationTime
	beginStatus          execution.ProgressCommitStatus
	beginErr             string
	beginDeterministic   bool
	newFinalization      execution.FinalizationMode
	newFinalizationErr   string
	oldFinalization      execution.FinalizationMode
	oldFinalizationErr   string
}

type cutoverStallProbe struct {
	label      string
	catalog    productionPhaseTwoSlotCatalog
	progress   *progress.Store
	repository *controlplane.RedisCatalogRepository
}

func (probe cutoverStallProbe) run(
	t *testing.T,
	ctx context.Context,
	production *productionPhaseTwoOwnership,
	session *ownership.Session,
	queryGroup execution.QueryGroupIdentity,
	persisted execution.ScheduleProgress,
	oldProjection execution.UnfinishedSlotProjection,
	now func() time.Time,
) cutoverStallOutcome {
	t.Helper()
	dependencies := production.dependencies
	source, err := scheduler.NewProductionSlotSource(
		queryGroup, dependencies.WorkerID, dependencies.Store, session, probe.catalog, probe.progress, now,
		scheduler.WithRecoveryLimits(dependencies.RecoveryLimits),
		scheduler.WithPostRecoveryTerminalDelay(dependencies.PostRecoveryTerminalDelay),
		scheduler.WithQueryDeadlineReserve(dependencies.QueryDeadlineReserve),
		scheduler.WithSnapshotRetention(dependencies.SnapshotRetention, dependencies.PublicationDelayAllowance),
		scheduler.WithExpiredRangeCreation(dependencies.ExpiredRangeEnabled),
	)
	if err != nil {
		t.Fatal(err)
	}
	slot, due, _, err := source.Next(ctx, queryGroup)
	if err != nil || !due {
		t.Fatalf("[%s] SlotSource.Next = (%+v, %t, %v), want a due Slot", probe.label, slot, due, err)
	}
	outcome := cutoverStallOutcome{
		sourceSlot: slot.Contract.Slot.EvaluationTime, sourceSegmentStart: slot.Contract.ScheduleSegmentStart,
		sourceNewPublication: slot.Contract.SnapshotRevision != oldProjection.Contract.SnapshotRevision,
	}

	// What the store resolves from completed facts, as resolveCurrentNextSlot
	// would if the persisted cursor and the requested Slot differed.
	completed := persisted.LastFullSlot
	if persisted.CurrentOrRecentGap != nil && persisted.CurrentOrRecentGap.LastSlot > completed {
		completed = persisted.CurrentOrRecentGap.LastSlot
	}
	outcome.storeNextAfter, err = probe.catalog.NextSlotAfter(ctx, queryGroup, completed)
	if err != nil {
		t.Fatalf("[%s] NextSlotAfter(%d) error = %v", probe.label, completed, err)
	}

	request := execution.SlotExecutionRequest{
		Contract: slot.Contract, DuePlanTargets: slot.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: slot.EarliestQueryDeadlineUnixMilli,
		RecoveryUntilUnixMilli:         slot.RecoveryUntilUnixMilli, KeepUntilUnixMilli: slot.KeepUntilUnixMilli,
		ReplayExpired: slot.Recovery.Disposition == scheduler.ReplayExpired,
		Operation:     slot.Dispatch.Operation, AttemptNo: 2, OwnerFence: slot.Dispatch.OwnerFence,
		ExpectedNextSlot: slot.ExpectedNextSlot,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("[%s] execution request from the chosen Slot is invalid: %v", probe.label, err)
	}
	begin, beginErr := probe.progress.BeginSlot(ctx, execution.ProgressBeginRequest{
		Identity: execution.ProgressIdentity{QueryGroup: queryGroup}, OwnerFence: request.OwnerFence,
		Projection: request.UnfinishedProjection(),
	})
	outcome.beginStatus = begin.Status
	if beginErr != nil {
		outcome.beginErr = beginErr.Error()
		var deterministic *progress.DeterministicInvalidError
		outcome.beginDeterministic = errors.As(beginErr, &deterministic)
	}

	frozen, err := newProductionFrozenExecution(probe.catalog, probe.repository, now)
	if err != nil {
		t.Fatal(err)
	}
	newFinalization, err := frozen.ResolveFinalization(ctx, request)
	outcome.newFinalization = newFinalization.Mode
	if err != nil {
		outcome.newFinalizationErr = err.Error()
	}
	oldRequest := execution.SlotExecutionRequest{
		Contract: oldProjection.Contract, DuePlanTargets: oldProjection.DuePlanTargets.Clone(),
		EarliestQueryDeadlineUnixMilli: oldProjection.EarliestQueryDeadlineUnixMilli,
		RecoveryUntilUnixMilli:         time.UnixMilli(oldProjection.EarliestQueryDeadlineUnixMilli).Add(dependencies.RecoveryLimits.MaxReplayAge).UnixMilli(),
		KeepUntilUnixMilli:             oldProjection.KeepUntilUnixMilli,
		Operation:                      execution.OperationNormal, AttemptNo: 2, OwnerFence: slot.Dispatch.OwnerFence,
		ExpectedNextSlot: oldProjection.Contract.Slot.EvaluationTime,
	}
	if err := oldRequest.Validate(); err != nil {
		t.Fatalf("[%s] execution request from the persisted projection is invalid: %v", probe.label, err)
	}
	oldFinalization, err := frozen.ResolveFinalization(ctx, oldRequest)
	outcome.oldFinalization = oldFinalization.Mode
	if err != nil {
		outcome.oldFinalizationErr = err.Error()
	}

	t.Logf("[%s] source chose Slot %d on Segment start %d (new publication=%t); persisted NextSlot=%d LastFullSlot=%d; NextSlotAfter(%d)=%d; BeginSlot status=%q err=%q deterministic=%t; ResolveFinalization new contract=(%s, %q) persisted contract=(%s, %q); control cache stats=%+v",
		probe.label, outcome.sourceSlot, outcome.sourceSegmentStart, outcome.sourceNewPublication, persisted.NextSlot, persisted.LastFullSlot,
		completed, outcome.storeNextAfter, outcome.beginStatus, outcome.beginErr, outcome.beginDeterministic,
		outcome.newFinalization, outcome.newFinalizationErr, outcome.oldFinalization, outcome.oldFinalizationErr, probe.repository.ControlReadCacheStats())
	return outcome
}

// cutoverStallTriggerWindow is the trigger check window of the fixture
// strategies; RequiredDetectHistoryPoints follows it (window + recovery - 1).
var cutoverStallTriggerWindow = 1

// installCutoverStallStrategies stores the two strategies of the cutover
// fixtures. The second installation changes the sibling's query and the
// first strategy's update_time only; see stripSegmentContent for how the
// fixtures make the first strategy's Segment cut regardless.
// stripSegmentContent rewrites a Query Group's open Segment so that it names
// no execution content, the shape of a Segment written before Segments named
// their content. A cutover cuts such a Segment once whatever the content did,
// with the Plan records a cut assigns: a new state apply epoch under the same
// state generation, no forced WARMING. That is the one way to cut a Segment
// whose Plans' state stays readable: every edit of the execution content
// itself either moves the Query Group identity (a query edit), forces WARMING
// (a threshold edit) or makes the loaded state unreadable (a trigger, recovery
// or connector edit), and the fixtures here are about a Slot in flight across
// a cut, not about any of those.
func stripSegmentContent(t *testing.T, ctx context.Context, redisClient *redis.Client, catalogPrefix string, queryGroup execution.QueryGroupIdentity) {
	t.Helper()
	key := catalogPrefix + ":schedule_timeline:" + string(queryGroup)
	raw, err := redisClient.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var timeline map[string]any
	if err := json.Unmarshal(raw, &timeline); err != nil {
		t.Fatal(err)
	}
	segments := timeline["segments"].([]any)
	segment := segments[len(segments)-1].(map[string]any)["schedule"].(map[string]any)["Segment"].(map[string]any)
	if _, named := segment["ObjectDigest"]; !named {
		t.Fatalf("the open Segment names no content to strip: %+v", segment)
	}
	delete(segment, "ObjectDigest")
	delete(segment, "OutputContextRefs")
	delete(segment, "OutputContextRevisions")
	stripped, err := json.Marshal(timeline)
	if err != nil {
		t.Fatal(err)
	}
	ttl, err := redisClient.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	if err := redisClient.Set(ctx, key, stripped, ttl).Err(); err != nil {
		t.Fatal(err)
	}
}

func installCutoverStallStrategies(t *testing.T, ctx context.Context, redisClient *redis.Client, secondTable string, updateTime int64) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatal(err)
	}
	first["update_time"] = updateTime
	firstItem := first["items"].([]any)[0].(map[string]any)
	firstItem["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 60
	for _, detect := range first["detects"].([]any) {
		detect.(map[string]any)["trigger_config"].(map[string]any)["check_window"] = cutoverStallTriggerWindow
	}
	firstEncoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var second map[string]any
	if err := json.Unmarshal(firstEncoded, &second); err != nil {
		t.Fatal(err)
	}
	second["id"] = 1002
	item := second["items"].([]any)[0].(map[string]any)
	item["id"], item["query_md5"] = 12, "cutover-stall-"+secondTable
	item["query_configs"].([]any)[0].(map[string]any)["result_table_id"] = secondTable
	secondEncoded, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":  `[1001,1002]`,
		"alarm-config.strategy_1001": firstEncoded,
		"alarm-config.strategy_1002": secondEncoded,
	} {
		if err := redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}
