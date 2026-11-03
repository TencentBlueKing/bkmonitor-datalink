package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// The cost roster is what each owned Query Group executes: the Segment its
// Slots are frozen from, with that Segment's Plans. An event carrying its
// Slot's revisions is counted against the group; one carrying the latest
// publication's revisions - which a roster read from the directory's
// PUBLISHED rows would have held - is not the group's. A Query Group whose
// owner is not accepting, or whose identity is not in memory, is left out and
// the roster says it is incomplete.
func TestTheCostRosterIsWhatEachOwnedGroupExecutes(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "t", BusinessID: "b", StrategyID: "858"}
	running := controlplane.ExecutionIdentity{SnapshotRevision: "snapshot-running", QueryRevision: "query-running",
		ScheduleRevision: "schedule-running", Plans: []execution.PlanIdentity{plan},
		Schedules: []execution.FrozenPlanSchedule{{Identity: plan,
			Spec: execution.ScheduleSpec{EvaluationIntervalSeconds: 10, Alignment: 5, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}}}}
	identity := func(qg execution.QueryGroupIdentity, revision uint64, at execution.EvaluationTime) (controlplane.ExecutionIdentity, bool) {
		// The idle group has an identity in memory too: only its lease not
		// accepting keeps it out.
		if (qg == "ours" && revision == 3 || qg == "idle" && revision == 4) && at == 600 {
			return running, true
		}
		return controlplane.ExecutionIdentity{}, false
	}
	owned := []ownedLease{{queryGroup: "ours", revision: 3, accepting: true}, {queryGroup: "idle", revision: 4},
		{queryGroup: "cold", revision: 5, accepting: true}}
	groups, complete := executionCostGroups(owned, identity, 600)
	if complete || len(groups) != 1 {
		t.Fatalf("roster = %+v complete=%v, want only the group that answered, incomplete", groups, complete)
	}
	g := groups[0]
	if g.QueryGroupKey != "ours" || g.SnapshotRevision != "snapshot-running" || g.QueryRevision != "query-running" ||
		g.ScheduleRevision != "schedule-running" || len(g.Members) != 1 || g.Members[0].StrategyID != "858" {
		t.Fatalf("roster group = %+v, want the running Segment's revisions and its Plan", g)
	}
	// When the Plan is due, as the Segment froze it: its interval, alignment
	// and completion offset, which the coverage reads a window against.
	if len(g.Schedules) != 1 || g.Schedules[0] != (observability.CostSchedule{Plan: g.Members[0], IntervalSeconds: 10, AlignmentSeconds: 5, CompletionOffsetSeconds: 30}) {
		t.Fatalf("roster schedules = %+v, want the Plan's frozen schedule", g.Schedules)
	}
	if groups, complete = executionCostGroups([]ownedLease{owned[0], owned[2]}, identity, 600); complete || len(groups) != 1 {
		t.Fatalf("an accepting group with no identity in memory: roster %+v complete=%v, want it left out and incomplete", groups, complete)
	}
	if groups, complete = executionCostGroups(owned[:1], identity, 600); !complete || len(groups) != 1 {
		t.Fatalf("every owned group answered: roster %+v complete=%v, want complete", groups, complete)
	}
	if groups, complete = executionCostGroups(owned[:1], nil, 600); complete || len(groups) != 0 {
		t.Fatalf("no identity source: roster %+v complete=%v, want empty and incomplete", groups, complete)
	}

	now := time.Unix(600, 0)
	cost := observability.NewCostSummary(observability.CostSummaryOptions{ProcessID: "p", Window: time.Minute, GroupCapacity: 4,
		PlanCapacity: 8, MetadataBytes: 4096, TopN: 2, Now: func() time.Time { return now }})
	groups, complete = executionCostGroups(owned[:1], identity, 600)
	cost.Reconcile(groups, complete)
	slot := func(snapshot string) observability.Observation {
		return observability.Observation{Stage: observability.StageSlotCompleted, Result: observability.ResultSuccess, Duration: time.Millisecond,
			Trace: observability.TraceFields{QueryGroupKey: "ours", SnapshotRevision: snapshot, QueryRevision: "query-running",
				ScheduleRevision: "schedule-running", EvaluationTime: 590}}
	}
	cost.Observe(context.Background(), slot("snapshot-running"))
	cost.Observe(context.Background(), slot("snapshot-latest"))
	cost.Publish(now)
	coverage := cost.Snapshot().Coverage
	if !coverage.CatalogComplete || coverage.TrackedGroups != 1 || coverage.TrackedPlans != 1 || coverage.ObservedGroups != 1 ||
		coverage.UntrackedObservations != 1 {
		t.Fatalf("coverage = %+v, want the running Slot counted against its group and the latest publication's not", coverage)
	}
}

// A refresh builds the roster at its own clock's second from the owned leases
// and reconciles it into the summary, whether or not this replica keeps a
// strategy directory: the roster no longer reads one.
func TestARefreshReconcilesTheExecutingRosterAtItsOwnTime(t *testing.T) {
	now := time.Unix(600, 0)
	cost := observability.NewCostSummary(observability.CostSummaryOptions{ProcessID: "p", Window: time.Minute, GroupCapacity: 4,
		PlanCapacity: 8, MetadataBytes: 4096, TopN: 2, Now: func() time.Time { return now }})
	refresh := &observationRefresh{cost: cost, now: func() time.Time { return now }, interval: time.Minute,
		owned: func() []ownedLease { return []ownedLease{{queryGroup: "ours", revision: 3, accepting: true}} },
		identity: func(qg execution.QueryGroupIdentity, revision uint64, at execution.EvaluationTime) (controlplane.ExecutionIdentity, bool) {
			if qg != "ours" || revision != 3 || at != execution.EvaluationTime(now.Unix()) {
				return controlplane.ExecutionIdentity{}, false
			}
			return controlplane.ExecutionIdentity{SnapshotRevision: "s", QueryRevision: "q", ScheduleRevision: "r",
				Plans: []execution.PlanIdentity{{TenantID: "t", BusinessID: "b", StrategyID: "1"}}}, true
		}}
	refresh.publish(context.Background())
	if coverage := cost.Snapshot().Coverage; !coverage.CatalogComplete || coverage.TrackedGroups != 1 || coverage.TrackedPlans != 1 {
		t.Fatalf("coverage after a refresh = %+v, want the owned group tracked at the refresh's second", coverage)
	}
}

// The owned leases are read from each Runner's lease in memory: its timeline
// revision and whether its owner accepts on it. A Runner with no lease to
// read is owned and not accepting, so the roster leaves it out rather than
// charging it under a revision it may not run.
func TestOwnedLeasesAreEachRunnersLeaseFromMemory(t *testing.T) {
	bundle := &phaseTwoWorkerBundle{runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{
		"leased":   {runner: &maintenanceTestRunner{scope: "obj", revision: 7}},
		"unleased": {runner: &fakePhaseTwoQueryGroup{}},
	}}
	got := map[execution.QueryGroupIdentity]ownedLease{}
	for _, lease := range bundle.ownedLeases() {
		got[lease.queryGroup] = lease
	}
	if len(got) != 2 || got["leased"] != (ownedLease{queryGroup: "leased", revision: 7, accepting: true}) ||
		got["unleased"] != (ownedLease{queryGroup: "unleased"}) {
		t.Fatalf("owned leases = %+v, want the leased Runner at revision 7 accepting and the other not accepting", got)
	}
}

func TestObservationRegistryAndCostPaginationCannotStarveReplicas(t *testing.T) {
	client := windowRedis(t)
	ctx := context.Background()
	at := time.Now()
	registry, err := ownership.NewRedisStoreWithClient(client, "test:ob:registry")
	if err != nil {
		t.Fatal(err)
	}
	limits := fleet.CostProjectionLimits{PublishBytes: 16 << 10, ReadBytes: 64 << 10, ReadCommands: 1, Timeout: time.Second, FreshFor: time.Minute, TTL: 2 * time.Minute}
	projection, err := fleet.NewCostProjectionStore(client, "test:ob:cost", limits)
	if err != nil {
		t.Fatal(err)
	}
	cost := observability.CostSnapshot{Enabled: true, ProcessID: "process", Scope: "process_observed_candidates", GeneratedAt: at}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err = registry.RegisterWorker(ctx, ownership.WorkerRegistration{WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy, DeploymentProfile: "profile", CapabilitiesDigest: "capabilities", ExpiresAt: at.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err = projection.Publish(ctx, id, at, cost); err != nil {
			t.Fatal(err)
		}
	}
	r := &observationCostRefresh{store: projection, registry: registry, reader: client, cache: fleet.NewCostCandidatesCache(func() time.Time { return at }, time.Minute), limits: limits, registryLimits: ownership.ObservationRegistryLimits{Bytes: 8 << 10, Commands: 4, Rows: 2, Timeout: time.Second}, replica: "a", interval: time.Second}
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		at = at.Add(time.Second)
		r.publish(ctx, at, cost)
		for _, snapshot := range r.observation.Projection.Snapshots {
			seen[snapshot.Replica] = true
		}
		if r.observation.Projection.Complete {
			t.Fatal("partial projection claimed full fleet")
		}
	}
	if len(seen) != 4 {
		t.Fatalf("independent cursors starved replicas: %v", seen)
	}
}

func TestObservationRedisHasIndependentPoolAndNoHiddenRetries(t *testing.T) {
	o := observationRedisOptions(config.RedisConnectionConfig{PoolSize: 100, ReadTimeout: config.Duration(5 * time.Second), WriteTimeout: config.Duration(5 * time.Second)})
	if o.MaxRetries != -1 || o.PoolSize != phaseTwoDiagnosticsPoolSize || o.ReadTimeout > time.Second || o.WriteTimeout > time.Second || o.PoolTimeout > time.Second {
		t.Fatalf("diagnostics can consume unbounded attempts or execution pool %+v", o)
	}
}

// A replica's projection is sized by what it publishes: every ranking - two
// scopes times the summary's dimensions, not a count of the dimensions it
// once had - at the rows each keeps. A refresh's read is sized by the
// replicas it reads (zero read bounds), and the store takes those limits.
func TestTheProjectionIsSizedByTheRankingsItPublishes(t *testing.T) {
	limits := observationProjectionLimits(30 * time.Second)
	rankings := 2 * len(observability.CostDimensions())
	if limits.PublishBytes != rankings*observationCostTopN*observationProjectionRowBytes || limits.ReadBytes != 0 || limits.ReadCommands != 0 {
		t.Fatalf("limits = %+v, want %d rankings of %d rows of %d bytes and reads sized by the replicas", limits, rankings, observationCostTopN, observationProjectionRowBytes)
	}
	if _, err := fleet.NewCostProjectionStore(windowRedis(t), "test:ob:cost", limits); err != nil {
		t.Fatalf("the store refuses the derived limits: %v", err)
	}
	if o := observationCostOptions("process", time.Now, func(uint64) bool { return true }); o.TopN != observationCostTopN || o.Admit == nil ||
		o.GroupCapacity != 0 || o.PlanCapacity != 0 || o.MetadataBytes != 0 {
		t.Fatalf("cost options = %+v, want sized by the roster through admission", o)
	}
}
