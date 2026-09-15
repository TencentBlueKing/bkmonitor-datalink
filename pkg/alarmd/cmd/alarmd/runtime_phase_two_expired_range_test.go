package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/go-redis/redis/v8"
)

type expiredRangeBundleFixture struct {
	bundle         *phaseTwoWorkerBundle
	production     *productionPhaseTwoOwnership
	client         *redis.Client
	clock, queries *atomic.Int64
	base           int64
	cfg            config.Config
	events         *recordingPhaseTwoEventSink
}

func TestExpiredRangeBypassesOrdinaryReceipt(t *testing.T) {
	request := execution.SlotExecutionRequest{ExpiredRange: &execution.ExpiredRangeProjectionV1{}}
	called := false
	w := phaseTwoShadowExecutor{emitter: &phaseTwoFinalEmitter{}, next: slotExecutorFunc(func(ctx context.Context, r execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
		called = true
		if ctx.Value(phaseTwoShadowExecutionKey{}) != nil || r.ExpiredRange != request.ExpiredRange {
			t.Fatal("range acquired ordinary Slot evidence context")
		}
		return execution.SlotExecutionResult{Completed: true}, nil
	})}
	result, err := w.Execute(context.Background(), request)
	if err != nil || !result.Completed || !called {
		t.Fatal(result, err, called)
	}
}

func TestExpiredRangeSharesF2WithHealthyFull(t *testing.T) {
	testExpiredRangeSharesF2WithHealthyFull(t, false)
}

func TestExpiredRangeV2DistanceSharesF2WithHealthyFull(t *testing.T) {
	testExpiredRangeSharesF2WithHealthyFull(t, true)
}

func testExpiredRangeSharesF2WithHealthyFull(t *testing.T, distance bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rangeEntered, queryEntered := make(chan struct{}), make(chan struct{})
	var rangeOnce, queryOnce sync.Once
	var mixed atomic.Bool
	var hot execution.QueryGroupIdentity
	var slot int64
	observer := observability.ObserverFunc(func(ctx context.Context, o observability.Observation) {
		if mixed.Load() && o.Stage == observability.StageGapLoaded && observability.TraceFieldsFromContext(ctx).QueryGroupKey == string(hot) {
			rangeOnce.Do(func() { close(rangeEntered) })
			select {
			case <-queryEntered:
			case <-ctx.Done():
			}
		}
	})
	f := newExpiredRangeProductionBundle(t, func(w http.ResponseWriter, r *http.Request) {
		queryOnce.Do(func() { close(queryEntered) })
		select {
		case <-rangeEntered:
		case <-ctx.Done():
			return
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[%d,95]]}],"status":null,"trace_id":"range-healthy","is_partial":false,"result_table_id":["system.mem"]}`, (slot-1)*1000)
	}, observer)
	var healthy execution.QueryGroupIdentity
	for _, qg := range f.bundle.queryGroups {
		s, err := f.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, qg)
		if err != nil {
			t.Fatal(err)
		}
		if s.Plans[0].Identity.StrategyID == "1001" {
			hot = qg
		} else {
			healthy = qg
		}
	}
	if hot == "" || healthy == "" {
		t.Fatal("fixture requires distinct hot and healthy groups")
	}
	slot = f.base + 1800
	f.clock.Store(slot*1000 + 100)
	first, attempted, err := f.bundle.runners[hot].runner.RunOne(ctx)
	if err != nil || !attempted || !first.Completed {
		t.Fatalf("hot initialization %+v %v %v", first, attempted, err)
	}
	if distance {
		age, attempted, err := f.bundle.runners[hot].runner.RunOne(ctx)
		if err != nil || !attempted || !age.Completed || age.CompletionKind != execution.CompletionSnapshotUnavailable {
			t.Fatalf("age prefix initialization %+v %v %v", age, attempted, err)
		}
	}
	// Controlled prior empty execution history initializes the healthy cursor;
	// the measured Slot must itself perform real Query/Evaluate/ACK/State/Progress.
	p := execution.ScheduleProgress{Identity: execution.ProgressIdentity{QueryGroup: healthy}, NextSlot: execution.EvaluationTime(slot), LastFullSlot: execution.EvaluationTime(slot - 1)}
	if err = p.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(struct {
		Schema   string                     `json:"schema"`
		Progress execution.ScheduleProgress `json:"progress"`
	}{"alarmd-schedule-progress-v2", p})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(healthy))
	key := fmt.Sprintf("%s:{%x}:%s:progress", productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "ownership"), digest, productionPhaseTwoPrefix(f.cfg.Redis.StatePrefix, "schedule"))
	if err = f.client.Set(ctx, key, raw, 0).Err(); err != nil {
		t.Fatal(err)
	}
	mixed.Store(true)
	if err = f.bundle.runScheduledOnce(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rangeEntered:
	default:
		t.Fatal("range did not overlap healthy Query")
	}
	select {
	case <-queryEntered:
	default:
		t.Fatal("healthy Query did not overlap range")
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	hp := loadPhaseTwoProgress(t, ctx, f.production, healthy)
	if hp.LastFullSlot != execution.EvaluationTime(slot) || hp.NextSlot != execution.EvaluationTime(slot+1) {
		t.Fatalf("healthy FULL missing: %+v", hp)
	}
	rp := loadPhaseTwoProgress(t, ctx, f.production, hot)
	if rp.UnfinishedRange != nil || rp.CurrentOrRecentGap == nil || rp.CurrentOrRecentGap.Count < 2 || rp.LastFullSlot != 0 {
		t.Fatalf("range completion missing: %+v", rp)
	}
	if distance && rp.CurrentOrRecentGap.Kind != execution.CompletionGapSkipped {
		t.Fatalf("distance prefix lost cause: %+v", rp)
	}
	events := f.events.snapshot()
	if len(events) != 1 || events[0].PlanRef.StrategyID != "1002" {
		t.Fatalf("healthy real ACK events=%+v", events)
	}
	schedule, err := f.production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, healthy)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := f.production.dependencies.Catalog.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{QueryGroup: healthy, ScheduleRevision: schedule.Segment.ScheduleRevision, ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: execution.EvaluationTime(slot), DuePlans: schedule.DuePlanRefs(execution.EvaluationTime(slot))})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := state.NewRedisBackend(f.cfg.RedisBackendOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	router, err := state.NewFixedRouter("range-healthy", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewExecutionStore(state.ExecutionStoreOptions{Prefix: f.cfg.Redis.StatePrefix, Router: router, MaxValueBytes: f.cfg.Limits.Codec.MaxEncodedBytes, MaxItemsPerCall: f.cfg.Limits.Store.MaxKeysPerBatch, MinTTL: f.cfg.Redis.MinTTL.Duration(), MaxTTL: f.cfg.Redis.MaxTTL.Duration(), RestartMargin: f.cfg.Redis.RestartMargin.Duration()})
	if err != nil {
		t.Fatal(err)
	}
	version, err := execution.BuildApplyVersion(frozen.Contract, 1)
	if err != nil {
		t.Fatal(err)
	}
	e := events[0]
	id := execution.StateKeyIdentity{Plan: execution.PlanIdentity{TenantID: e.TenantID, BusinessID: e.BusinessID, StrategyID: e.PlanRef.StrategyID}, StateGeneration: execution.StateGeneration(e.PlanRef.StateCompatibilityHash), SeriesIdentityDigest: execution.SeriesIdentityDigest(e.RecordRef.DimensionIdentityDigest)}
	loaded, err := store.LoadRuntime(ctx, execution.StatePreflightRequest{Contract: frozen.Contract, Items: []execution.StatePreflightItem{{Identity: id, ApplyVersion: version}}})
	if err != nil || len(loaded.Items) != 1 || loaded.Items[0].BlobRevision == 0 || (loaded.Items[0].Status != execution.StateFoundReady && loaded.Items[0].Status != execution.StateFoundWarming) {
		t.Fatalf("healthy persisted State=%+v err=%v", loaded, err)
	}
}

func newExpiredRangeProductionBundle(t *testing.T, response http.HandlerFunc, observer observability.Observer) expiredRangeBundleFixture {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)
	base := time.Now().Unix() / 60 * 60
	var clock atomic.Int64
	clock.Store(base * 1000)
	var queries atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		if response == nil {
			w.WriteHeader(http.StatusInternalServerError)
		} else {
			response(w, r)
		}
	}))
	t.Cleanup(server.Close)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-expired-range-integration"
	cfg.PhaseTwo.Scheduler.ExpiredRangeEnabled = true
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = server.URL
	// The public fixture uses one-second Slots; its reserve must leave a
	// positive frozen query deadline, as in the existing full-path tests.
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(time.Hour)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	events := &recordingPhaseTwoEventSink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, recorder, observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{Now: func() time.Time { return time.UnixMilli(clock.Load()) }, HTTPClient: server.Client(), AdditionalObserver: observer, OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
			return events, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if err = bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bundle.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	production := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	return expiredRangeBundleFixture{bundle: bundle, production: production, client: client, clock: &clock, queries: &queries, base: base, cfg: cfg, events: events}
}

func TestExpiredRangeProductionBundleUsesSingleFlightAndRealRedis(t *testing.T) {
	f := newExpiredRangeProductionBundle(t, nil, nil)
	bundle, production, clock, queries, base := f.bundle, f.production, f.clock, f.queries, f.base
	ctx := context.Background()
	clock.Store((base + 1800) * 1000)
	for _, qg := range bundle.queryGroups {
		schedule, err := production.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, qg)
		if err != nil {
			t.Fatal(err)
		}
		interval := execution.EvaluationTime(schedule.Plans[0].Spec.EvaluationIntervalSeconds)
		runner := bundle.runners[qg].runner
		// Missing Progress remains an ordinary single Slot, then the persisted
		// head permits one bounded range on the next real RunOne invocation.
		first, attempted, err := runner.RunOne(ctx)
		if err != nil || !attempted || !first.Completed {
			t.Fatalf("cold single %+v %v %v", first, attempted, err)
		}
		before, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: qg})
		if err != nil {
			t.Fatal(err)
		}
		result, attempted, err := runner.RunOne(ctx)
		if err != nil || !attempted || !result.Completed {
			t.Fatalf("range %+v %v %v", result, attempted, err)
		}
		after, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: qg})
		if err != nil {
			t.Fatal(err)
		}
		if after.Progress.NextSlot <= before.Progress.NextSlot+interval || after.Progress.UnfinishedRange != nil || after.Progress.UnfinishedSlot != nil || after.Progress.LastFullSlot != before.Progress.LastFullSlot {
			t.Fatalf("range did not advance real production Progress: before=%+v after=%+v", before.Progress, after.Progress)
		}
		gap := after.Progress.CurrentOrRecentGap
		if gap == nil || gap.Count != uint32((gap.LastSlot-gap.FirstSlot)/interval+1) {
			t.Fatalf("logical count drift: %+v", gap)
		}
	}
	if queries.Load() != 0 {
		t.Fatalf("range performed %d queries", queries.Load())
	}
}
