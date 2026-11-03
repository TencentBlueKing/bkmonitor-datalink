package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type maintenanceTestCatalog struct {
	plans        []controlplane.MaintenancePlan
	uncompilable int
	// until is the end of the Segment the Plans are read from, zero open.
	until execution.EvaluationTime
	reads int
	err   error
	// took, when set, is the read's cost, answered against the caller's
	// deadline.
	took func(context.Context) error
}

func (c *maintenanceTestCatalog) CurrentPlans(ctx context.Context, _ execution.QueryGroupIdentity, _ execution.EvaluationTime) (controlplane.MaintenancePlans, error) {
	c.reads++
	if c.took != nil {
		if err := c.took(ctx); err != nil {
			return controlplane.MaintenancePlans{}, err
		}
	}
	if c.err != nil {
		return controlplane.MaintenancePlans{}, c.err
	}
	return controlplane.MaintenancePlans{Plans: c.plans, Uncompilable: c.uncompilable, Until: c.until}, nil
}

type maintenanceTestWriter struct {
	batches [][]linkdoutput.CloseRequest
	err     error
	// onWrite, when set, runs as a batch is sent.
	onWrite func()
}

func (w *maintenanceTestWriter) WriteCloseBatch(_ context.Context, requests []linkdoutput.CloseRequest) error {
	if w.onWrite != nil {
		w.onWrite()
	}
	w.batches = append(w.batches, append([]linkdoutput.CloseRequest(nil), requests...))
	return w.err
}

type maintenanceTestRunner struct {
	fakePhaseTwoQueryGroup
	check    func(context.Context) error
	lastErr  error
	scope    string
	revision uint64
	entered  int
	// enter, when set, is what withMaintenance answers before running
	// anything: the owner or the view gate refusing the round.
	enter func() error
}

func (r *maintenanceTestRunner) withMaintenance(ctx context.Context, run func(context.Context, func(context.Context) error) error) error {
	r.entered++
	if r.enter != nil {
		if err := r.enter(); err != nil {
			r.lastErr = err
			return err
		}
	}
	r.lastErr = run(ctx, r.check)
	return r.lastErr
}

func (r *maintenanceTestRunner) maintenanceLease() (string, uint64, bool) {
	return r.scope, r.revision, true
}

type maintenanceTestFixture struct {
	m      *effectiveMaintenance
	runner *maintenanceTestRunner
	// stub is the link's control plane the inactive closes reach, through
	// the production closer.
	stub *stubControlPlane
	mu   sync.Mutex
	at   time.Time
}

func (f *maintenanceTestFixture) now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.at }
func (f *maintenanceTestFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = f.at.Add(d)
}

// closed is the bodies of the closes the link received that were not dry
// runs: one per inactive close that found alerts.
func (f *maintenanceTestFixture) closed() []string {
	var closes []string
	for _, body := range f.stub.requests("/api/v1/alerts/close") {
		if !strings.Contains(body, `"dry_run":true`) {
			closes = append(closes, body)
		}
	}
	return closes
}

// matchAgain makes the link match the alerts again, as a new ABNORMAL in
// the strategy's version would.
func (f *maintenanceTestFixture) matchAgain(ids ...string) {
	f.stub.mu.Lock()
	defer f.stub.mu.Unlock()
	f.stub.matches = append([]string(nil), ids...)
}

// maintenanceMatches is one open alert of the strategy's version at the link.
var maintenanceMatches = []string{"instance"}

// newMaintenanceTestFixture is the loop over one Query Group of one Plan on a
// 09:00-17:00 schedule, whose closes go through the production filter
// closer to a stub of the link that matches the alerts given.
func newMaintenanceTestFixture(t *testing.T, snapshot string, at time.Time, matches []string, mutate ...func(*contract.EvaluationPlanV2)) *maintenanceTestFixture {
	t.Helper()
	f := &maintenanceTestFixture{at: at, stub: &stubControlPlane{bodies: map[string][]string{}, matches: append([]string(nil), matches...)},
		runner: &maintenanceTestRunner{check: func(context.Context) error { return nil }, scope: "obj-a", revision: 1}}
	catalog := productionG4Catalog(t, 123, "Threshold", "usage", "system.cpu", []string{"host"}, [][]map[string]any{{{"method": "gte", "threshold": 50}}})
	group := catalog.QueryGroups[0]
	p := group.Plans[0]
	p.Plan.WireFormat = contract.WireFormatStandardRawEvent
	p.Plan.LegacyOutput = nil
	p.Plan.StrategyRef.SnapshotRevision = 4
	p.Plan.StrategyIR.StrategyRef.SnapshotRevision = 4
	p.Plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60,"timezone_ref":"BUSINESS_LOCAL","uptime":{"time_ranges":[{"start":"09:00","end":"17:00"}],"active_calendars":[],"calendars":[]}}`)
	p.Plan.EffectiveTimeSnapshot = json.RawMessage(snapshot)
	for _, m := range mutate {
		m(&p.Plan)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), config.Default().CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: p.Plan, DatasetContract: group.QueryPlan.Normalization.DatasetContract, StateSemantics: strategy.StateSemantics{StateSchemaVersion: "state-v1", CodecSemanticsVersion: "codec-v1", IdentitySchemaDigest: strings.Repeat("c", 64), SourceTimeSemanticsVersion: "seconds-v1", HistoryCellSemanticsVersion: "history-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok || compiled.Levels().Len() == 0 {
		t.Fatalf("compile terminal=%+v levels=%+v", result.PlanTerminal(), result.LevelTerminals())
	}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Now: f.now, Observer: observability.NopObserver{}}, runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{group.Identity: {runner: f.runner}}}
	f.m = &effectiveMaintenance{bundle: bundle, catalog: &maintenanceTestCatalog{plans: []controlplane.MaintenancePlan{{Identity: p.Identity, Compiled: compiled}}},
		filter: newStubFilterRoute(t, f.stub), capacity: config.LinkdCapacity{GroupBatch: 2, CloseBatch: 5, LocalEntries: 100}}
	return f
}

const maintenanceReadySnapshot = `{"schema_version":1,"status":"READY","business_timezone":"UTC","calendars":[]}`

func maintenanceTime(hour, minute int) time.Time {
	return time.Date(2026, 9, 22, hour, minute, 0, 0, time.UTC)
}

// A Plan outside its effective time closes, through the link's filter, the
// active alerts of its strategy's current version from this deployment's
// event source; an active Plan, or one whose effective time is unknown,
// sends nothing.
func TestEffectiveMaintenanceClosesTheInactiveVersionThroughTheLinksFilter(t *testing.T) {
	for _, tc := range []struct {
		name, snapshot string
		at             time.Time
		want           int
	}{
		{"inactive", maintenanceReadySnapshot, maintenanceTime(8, 0), 1},
		{"active", maintenanceReadySnapshot, maintenanceTime(10, 0), 0},
		{"unknown", "", maintenanceTime(8, 0), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, tc.snapshot, tc.at, maintenanceMatches)
			f.m.step(context.Background())
			closes := f.closed()
			if f.runner.lastErr != nil || len(closes) != tc.want {
				t.Fatalf("err=%v closes=%v", f.runner.lastErr, closes)
			}
			if tc.want == 0 {
				if requests := f.stub.requests("/api/v1/alerts/close"); len(requests) != 0 {
					t.Fatalf("asked the link %v for a Plan that is not inactive", requests)
				}
				return
			}
			for _, field := range []string{`"bk_tenant_id":"tenant-a"`, `"event_source_id":"built_in_bk"`, `"labels.strategy_id":"123"`, `"labels.strategy_version":"4"`} {
				if !strings.Contains(closes[0], field) {
					t.Fatalf("close %s does not carry %s", closes[0], field)
				}
			}
			if f.m.Stats()[closeOutcomeDone] != 1 {
				t.Fatalf("stats %v, want one close_done", f.m.Stats())
			}
		})
	}
}

func TestEffectiveMaintenanceRechecksFenceAndActiveBoundaryBeforeClose(t *testing.T) {
	for _, boundary := range []bool{false, true} {
		t.Run(map[bool]string{false: "fence-rejected", true: "became-active"}[boundary], func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 59), maintenanceMatches)
			checkErr := errors.New("owner lost")
			f.runner.check = func(context.Context) error {
				if boundary {
					f.advance(time.Minute)
					return nil
				}
				return checkErr
			}
			f.m.step(context.Background())
			if requests := f.stub.requests("/api/v1/alerts/close"); len(requests) != 0 {
				t.Fatalf("close escaped final check: %v", requests)
			}
			if !boundary && !errors.Is(f.runner.lastErr, checkErr) {
				t.Fatalf("err=%v", f.runner.lastErr)
			}
		})
	}
}

// One inactive period costs one close and one confirming pass after
// inactiveConfirmAfter, then nothing until the Plan is active again; a
// period that starts after an active one closes again. A close the link
// did not settle (busy) is not remembered and runs again on the next step.
func TestEffectiveMaintenanceClosesOncePerInactivePeriodAndConfirmsOnce(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	f.m.step(context.Background())
	f.matchAgain("late")
	f.advance(inactiveConfirmAfter - time.Second)
	f.m.step(context.Background())
	if closes := f.closed(); len(closes) != 1 {
		t.Fatalf("closes %d before the confirming pass is due, want 1", len(closes))
	}
	f.advance(time.Second)
	f.m.step(context.Background())
	if closes := f.closed(); len(closes) != 2 {
		t.Fatalf("closes %d at the confirming pass, want 2: the alert opened at the boundary stays open", len(closes))
	}
	f.matchAgain("later")
	f.advance(10 * time.Minute)
	f.m.step(context.Background())
	// Two passes, each a dry run, the page's dry run and the close.
	if requests := f.stub.requests("/api/v1/alerts/close"); len(f.closed()) != 2 || len(requests) != 6 {
		t.Fatalf("requests %d (closes %d) after the confirming pass, want the version left alone", len(requests), len(f.closed()))
	}
	// Active again, then inactive: a new period.
	f.at = maintenanceTime(10, 0)
	f.m.step(context.Background())
	f.at = maintenanceTime(18, 0)
	f.m.step(context.Background())
	closes := f.closed()
	if len(closes) != 3 {
		t.Fatalf("closes %d in the next inactive period, want 3", len(closes))
	}
	// Each period is its own decision: the next period's close is its own
	// command, not the first period's with its effective_at (the stored
	// command outlives the fake clock's hours in the test Redis, as it
	// would a second window inside the command's life).
	type command struct {
		OperationID string `json:"operation_id"`
	}
	var first, confirming, next command
	_ = json.Unmarshal([]byte(closes[0]), &first)
	_ = json.Unmarshal([]byte(closes[1]), &confirming)
	_ = json.Unmarshal([]byte(closes[2]), &next)
	if first.OperationID == "" || confirming.OperationID != first.OperationID || next.OperationID == first.OperationID {
		t.Fatalf("commands: first %+v, confirming %+v, next period %+v", first, confirming, next)
	}

	g := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	g.stub.busy = true
	g.m.step(context.Background())
	if g.m.Stats()[closeOutcomeNotSettled] != 1 || len(g.closed()) != 0 {
		t.Fatalf("stats %v closes %v, want the busy link counted not settled", g.m.Stats(), g.closed())
	}
	g.stub.busy = false
	g.advance(time.Second)
	g.m.step(context.Background())
	if len(g.closed()) != 1 {
		t.Fatal("a close the link did not settle was remembered as done")
	}

	// A close whose items came back certainly not closed (429 each) is a
	// close sent but not settled: the next step runs it again.
	h := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	h.stub.itemBusy = true
	h.m.step(context.Background())
	if h.m.Stats()[closeOutcomeDone] != 1 || len(h.closed()) != 1 {
		t.Fatalf("stats %v closes %v, want the rederived close counted as sent", h.m.Stats(), h.closed())
	}
	h.stub.itemBusy = false
	h.advance(time.Second)
	h.m.step(context.Background())
	if len(h.closed()) != 2 {
		t.Fatalf("closes %d: a close whose items came back rederived was remembered as settled", len(h.closed()))
	}
}

// A link that cannot close by filter (501) is counted unsupported and the
// Plan's alerts are not closed any other way.
func TestEffectiveMaintenanceCountsALinkThatCannotCloseByFilter(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
	f.stub.unsupported = true
	f.m.step(context.Background())
	if f.m.Stats()[closeOutcomeUnsupported] != 1 || len(f.closed()) != 0 {
		t.Fatalf("stats %v closes %v, want close_unsupported and nothing closed", f.m.Stats(), f.closed())
	}
	f.m.filter = nil
	f.m.step(context.Background())
	if f.m.Stats()[closeOutcomeUnrouted] != 1 {
		t.Fatalf("stats %v, want close_unrouted without the link's control plane", f.m.Stats())
	}
}

// Unlike the group tests' injected check, this exercises the actual Session
// against Redis. A current local lease cannot stand in for the store fence or
// a timeline update that has not reached the lease's next renewal yet.
func TestEffectiveMaintenanceRealSessionRejectsLostFenceAndChangedTimeline(t *testing.T) {
	for _, mode := range []string{"stable", "released-at-store", "timeline-before-renewal", "timeline-after-renewal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			store := newViewGateTestStore(t)
			session, authority := openViewGateTestSessionWithAuthority(t, store, "qg-1", 12, "obj-a")
			now := time.UnixMilli(1_700_000_000_000)
			flights := scheduler.NewFlightCoordinator()
			runtime := &productionPhaseTwoQueryGroup{session: session, queryGroup: "qg-1", flights: flights, now: func() time.Time { return now }}
			wrote := false
			err := runtime.withMaintenance(ctx, func(ctx context.Context, check func(context.Context) error) error {
				// External reads do not occupy the detection flight.
				release, ok := flights.TryMaintenance("qg-1")
				if !ok {
					t.Fatal("maintenance held flight while loading facts")
				}
				release()
				if mode == "released-at-store" {
					lease, _ := session.Current()
					if err := store.Release(ctx, lease.Fence); err != nil {
						t.Fatal(err)
					}
				}
				if strings.HasPrefix(mode, "timeline-") {
					record, err := store.ReadAssignment(ctx, "qg-1")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{QueryGroup: "qg-1", DesiredWorkerID: "worker-1", PlacementReason: ownership.PlacementRendezvous, DecidedAt: now, ContentScope: "obj-a", TimelineRecordRevision: 13, ExpectedRecordRevision: record.RecordRevision}); err != nil {
						t.Fatal(err)
					}
					if mode == "timeline-after-renewal" {
						if err := session.Renew(ctx, now, time.Minute); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := check(ctx); err != nil {
					return err
				}
				if release, ok := flights.TryMaintenance("qg-1"); ok {
					release()
					t.Fatal("final check did not hold flight for write")
				}
				wrote = true
				return nil
			})
			if mode == "stable" {
				if err != nil || !wrote {
					t.Fatalf("err=%v wrote=%v", err, wrote)
				}
			} else if err == nil || wrote {
				t.Fatalf("changed ownership/content admitted: err=%v wrote=%v", err, wrote)
			}
			release, ok := flights.TryMaintenance("qg-1")
			if !ok {
				t.Fatal("maintenance leaked flight after return")
			}
			release()
		})
	}
}

// slowStore spends time against the caller's deadline without sleeping: a
// call of cost d fails as a deadline would when the caller has less than
// what was spent so far plus d left, and otherwise counts d as spent.
type slowStore struct {
	mu    sync.Mutex
	spent time.Duration
}

func (s *slowStore) take(d time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < s.spent+d {
			return context.DeadlineExceeded
		}
		s.spent += d
		return nil
	}
}

// Each call the loop makes is bounded by its own client (the Redis
// command by the configured timeouts, the link's close by its HTTP client), so
// a store slow on every call but answering each one inside its timeout still
// closes: a Plans read and an owner check that together outlast the five
// seconds the whole step used to have, and either one alone outlasting the
// five seconds its own round used to have (two Redis round trips near the
// default three-second read timeout).
func TestEffectiveMaintenanceClosesWhenItsCallsTogetherOutlastTheOldStepBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		read, check time.Duration
	}{
		{"read-and-check-together", 3 * time.Second, 2500 * time.Millisecond},
		{"read-alone", 5500 * time.Millisecond, 0},
		{"check-alone", 0, 5500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
			store := &slowStore{}
			f.m.catalog.(*maintenanceTestCatalog).took = store.take(tc.read)
			f.runner.check = store.take(tc.check)
			f.m.step(context.Background())
			if f.runner.lastErr != nil || len(f.closed()) != 1 {
				t.Fatalf("err=%v closes=%v spent=%s", f.runner.lastErr, f.closed(), store.spent)
			}
		})
	}
}
