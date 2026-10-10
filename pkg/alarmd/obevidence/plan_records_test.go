package obevidence

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// A Plan's gap marker and absence memory are read by the Plan as strategy.get
// shows it: the published object gives the state generation the keys carry,
// and each key answers whether it is there, how long it has left, how large it
// is and what its header says. Written here by the store's own write paths,
// so what is read is what production leaves behind. Whether a Plan's records
// survive from one round to the next was a question with no read: a write
// gives the key the one-day floor, and for a Plan whose period is longer the
// key is gone before the next load, which nothing could show.
func TestAPlanRecordsReadNamesEachKeyItsLifeSizeAndHeader(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	const prefix = "alarmd"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := state.NewRedisBackendWithClient("fixture", client)
	if err != nil {
		t.Fatal(err)
	}
	router, err := state.NewFixedRouter("primary", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewExecutionStore(state.ExecutionStoreOptions{Prefix: prefix, Router: router, MaxValueBytes: 1 << 20,
		MaxItemsPerCall: 64, MinTTL: time.Minute, MaxTTL: 720 * time.Hour, RestartMargin: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	plan := execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "7"}
	sibling := execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "8"}
	object := controlplane.QueryGroupObject{ContractVersion: "alarmd-query-group-object-v1", Identity: "group", Plans: []controlplane.QueryGroupPlanObject{
		{Identity: plan, PlanID: "p7", StateGeneration: "generation"},
		{Identity: sibling, PlanID: "p8", StateGeneration: "generation"},
		// Written before objects carried the generation.
		{Identity: execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "10"}, PlanID: "p10"},
	}}
	raw, err := contract.CanonicalJSONV2(object)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contract.DeriveCanonicalDigestV2(object.ContractVersion, object)
	if err != nil {
		t.Fatal(err)
	}
	objectKey, err := repository.ObservationQueryGroupKey(execution.ObjectDigest(digest))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, objectKey, raw, 0).Err(); err != nil {
		t.Fatal(err)
	}
	log := &commandLog{}
	client.AddHook(log)
	service := New(Options{Published: binding(client, "runtime", prefix), Catalog: repository})
	gapRequest := StoreRequest{Family: FamilyGapMarker, QueryGroup: "group", ObjectDigest: digest, StrategyID: "7"}
	noDataRequest := gapRequest
	noDataRequest.Family = FamilyNoDataMemory
	// Each read is checked on its own: the store's writes between them go
	// through the same client.
	recordsOf := func(request StoreRequest) []PlanRecord {
		t.Helper()
		log.reset()
		r := service.Store(ctx, request)
		log.assertBounded(t)
		plans, _ := r.Value.([]PlanRecords)
		if r.Status != "ok" || !r.Complete || len(plans) != 1 || plans[0].Plan.StrategyID != request.StrategyID || plans[0].StateGeneration != "generation" {
			t.Fatalf("%s = %+v, want this Plan's records read whole", request.Family, r)
		}
		return plans[0].Records
	}

	// Nothing written: each key is missing, which is an answer.
	if records := recordsOf(gapRequest); len(records) != 1 || records[0].Kind != RecordGapMarker || records[0].Status != "missing" || records[0].TTLMS != nil {
		t.Fatalf("gap marker before any write = %+v, want missing", records)
	}

	version := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 60, SlotDigest: "slot"}
	ref := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 60}, SnapshotRevision: "snapshot",
		QueryRevision: "query", ScheduleRevision: "schedule", ScheduleSegmentStart: 60, DuePlanSetDigest: "plans"}
	// Each Plan's own retention, as the writer carries it: a one-minute Plan
	// whose records live the one-day floor, and a Plan past a day -- thirty
	// hourly points -- whose records live its own lifetime.
	retention := execution.GenerationRetention{ByPlan: map[execution.PlanIdentity][]execution.StateRetentionRequirement{
		plan:    {{LevelID: 1, RetentionPoints: 5, EvaluationInterval: time.Minute}},
		sibling: {{LevelID: 1, RetentionPoints: 30, EvaluationInterval: time.Hour}},
	}}
	lifetime := func(identity execution.PlanIdentity) time.Duration {
		t.Helper()
		levels := retention.ByPlan[identity]
		ttl, err := state.GenerationScopedTTL([]state.LevelRequirement{state.NewLevelRequirement(levels[0], "", 0)}, time.Minute, time.Minute, 720*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return ttl
	}
	// The remaining life in milliseconds, within a minute of the write's.
	wantLife := func(record PlanRecord, want time.Duration) {
		t.Helper()
		if record.TTLMS == nil || *record.TTLMS > want.Milliseconds() || *record.TTLMS <= (want-time.Minute).Milliseconds() {
			t.Fatalf("%s remaining life = %v ms, want within a minute of %d ms", record.Kind, record.TTLMS, want.Milliseconds())
		}
	}
	writeGap := func(identity execution.PlanIdentity) {
		t.Helper()
		gap, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
			Identity:     execution.PlanGapIdentity{Plan: identity, StateGeneration: "generation"},
			ApplyVersion: version, ScheduleRevision: "plan-r1",
			Scopes: []execution.GapScopeMutation{{Kind: execution.GapOpen, ReasonCode: execution.ReasonCode("GAP_SKIPPED"), RequiredFullSlots: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		applied, err := store.ApplyGap(ctx, execution.GapGuardApplyRequest{Contract: ref, Items: []execution.PlanGapMutation{gap}, Retention: retention})
		if err != nil || applied.Items[0].Status != execution.GapGuardApplied {
			t.Fatalf("ApplyGap() = (%+v, %v)", applied, err)
		}
	}
	writeGap(plan)
	gap := recordsOf(gapRequest)[0]
	if gap.Status != "ok" || gap.Type != "string" || gap.Bytes == nil || *gap.Bytes <= 0 || gap.Header == nil || gap.Header.MarkerRevision != 1 ||
		gap.Header.EvaluationTime != 60 || gap.Header.Scopes != 1 || gap.Header.ScheduleRevision != "plan-r1" {
		t.Fatalf("gap marker = %+v header %+v, want it present with its header", gap, gap.Header)
	}
	if lifetime(plan) != state.GenerationScopedFloor {
		t.Fatalf("setup: a one-minute Plan's records live %s, want the floor", lifetime(plan))
	}
	wantLife(gap, state.GenerationScopedFloor)

	noData, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
		DerivedFrom: execution.NoDataRepresentationNone, Identity: execution.PlanNoDataIdentity{Plan: plan, StateGeneration: "generation"},
		ApplyVersion: version, ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1", PresentAsOf: 1000,
		Memory: []execution.NoDataGroupMemory{{GroupKey: "a", LastSeen: 940}, {GroupKey: "b", LastSeen: 1000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{Contract: ref, Items: []execution.PlanNoDataMutation{noData}, Retention: retention}); err != nil ||
		applied.Items[0].Status != execution.NoDataApplied {
		t.Fatalf("ApplyNoData() = (%+v, %v)", applied, err)
	}
	memory := recordsOf(noDataRequest)
	if len(memory) != 2 || memory[0].Kind != RecordNoDataMemory || memory[1].Kind != RecordNoDataMemoryWhole {
		t.Fatalf("absence memory = %+v, want the per-group record and the whole one", memory)
	}
	perGroup := memory[0]
	if perGroup.Status != "ok" || perGroup.Type != "hash" || perGroup.Fields == nil || *perGroup.Fields < 2 || perGroup.Header == nil ||
		perGroup.Header.MarkerRevision != 1 || perGroup.Header.PresentAsOf != 1000 {
		t.Fatalf("per-group memory = %+v header %+v, want it present with its fields and header", perGroup, perGroup.Header)
	}
	wantLife(perGroup, state.GenerationScopedFloor)
	if memory[1].Status != "missing" {
		t.Fatalf("whole memory = %+v, want missing: this build does not write it", memory[1])
	}

	// A Plan past a day: its marker lives its own lifetime, past the floor,
	// which is what reading a sixty-hour strategy's keys has to show.
	writeGap(sibling)
	siblingRequest := gapRequest
	siblingRequest.StrategyID = "8"
	long := recordsOf(siblingRequest)[0]
	if lifetime(sibling) <= state.GenerationScopedFloor {
		t.Fatalf("setup: a thirty-hour Plan's records live %s, want past the floor", lifetime(sibling))
	}
	wantLife(long, lifetime(sibling))

	// A key holding another Plan's record is not this Plan's; one holding the
	// wrong shape says so.
	siblingKey, err := state.PlanGapKeyV2(prefix, execution.PlanGapIdentity{Plan: sibling, StateGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := client.Get(ctx, siblingKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, gap.Key, moved, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if got := recordsOf(gapRequest)[0]; got.Status != "identity_mismatch" || got.Header != nil {
		t.Fatalf("another Plan's marker under this key = %+v, want identity_mismatch", got)
	}
	if err := client.Set(ctx, perGroup.Key, "not a hash", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if got := recordsOf(noDataRequest)[0]; got.Status != "wrong_type" || got.Type != "string" {
		t.Fatalf("a string under the per-group key = %+v, want wrong_type", got)
	}

	// A Plan the object does not name has no keys to read.
	missing := gapRequest
	missing.StrategyID = "9"
	if r := service.Store(ctx, missing); r.Status != "plan_not_in_object" || r.Value != nil {
		t.Fatalf("a Plan outside the object = %+v", r)
	}
	// A Plan the object names without a generation has keys nobody can name
	// from it: said by name, and the answer is not complete.
	unnamed := gapRequest
	unnamed.StrategyID = "10"
	log.reset()
	r := service.Store(ctx, unnamed)
	log.assertBounded(t)
	if plans, _ := r.Value.([]PlanRecords); r.Complete || len(plans) != 1 || plans[0].Status != "generation_unknown" || len(plans[0].Records) != 0 {
		t.Fatalf("a Plan without a generation = %+v, want it named generation_unknown and the answer incomplete", r)
	}
	// The tenant and the business narrow as strategy.config's published view
	// does: the strategy under another business is not this Plan.
	missing.StrategyID, missing.Business = "7", "3"
	if r := service.Store(ctx, missing); r.Status != "plan_not_in_object" {
		t.Fatalf("the strategy under another business = %+v", r)
	}
}
