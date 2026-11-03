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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

type maintenanceTestCatalog struct {
	plans        []controlplane.MaintenancePlan
	uncompilable int
	reads        int
	err          error
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
	return controlplane.MaintenancePlans{Plans: c.plans, Uncompilable: c.uncompilable}, nil
}

// maintenanceTestIndex answers the copy's reads with alerts; own, when set,
// is the source its calibration names as this deployment's. reconcileErr,
// when set, fails every calibration: the set is read and never calibrated.
// members, when set, is what a read of the set answers in place of the
// alerts' fingerprints; readErr fails every read. trusted gives the copy a
// Console that has confirmed the sets, so that they are read at all.
type maintenanceTestIndex struct {
	alerts       []openalerts.Alert
	own          string
	reconcileErr error
	members      []string
	readErr      error
	trusted      bool
}

// confirmedMaintenanceFacts is a Console that has confirmed where the sets
// are and that they are keyed by our alert ids.
type confirmedMaintenanceFacts struct{}

func (confirmedMaintenanceFacts) LocationConfirmed() bool { return true }
func (confirmedMaintenanceFacts) KeyedByAlertID() (bool, time.Time, bool) {
	return true, time.Time{}, true
}
func (confirmedMaintenanceFacts) KeyingAsked() bool       { return true }
func (confirmedMaintenanceFacts) Refresh(context.Context) {}

func (s maintenanceTestIndex) ReadSet(context.Context, openalerts.StrategyKey) ([]string, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	if s.members != nil {
		return s.members, nil
	}
	var members []string
	for _, a := range s.alerts {
		members = append(members, a.Fingerprint)
	}
	return members, nil
}
func (s maintenanceTestIndex) Reconcile(ctx context.Context, key openalerts.StrategyKey) (openalerts.Reconciliation, error) {
	if s.reconcileErr != nil {
		return openalerts.Reconciliation{}, s.reconcileErr
	}
	members, _ := s.ReadSet(ctx, key)
	return openalerts.Reconciliation{EventSourceID: s.own, Members: members, Alerts: s.alerts}, nil
}
func (s maintenanceTestIndex) Watch(ctx context.Context, ready func(bool), _ func(openalerts.StrategyKey), _ func(openalerts.NoticeRefusal)) error {
	ready(true)
	<-ctx.Done()
	return ctx.Err()
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
	writer *maintenanceTestWriter
	mu     sync.Mutex
	at     time.Time
}

func (f *maintenanceTestFixture) now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.at }
func (f *maintenanceTestFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = f.at.Add(d)
}

func newMaintenanceTestFixture(t *testing.T, snapshot string, at time.Time, alerts []openalerts.Alert, mutate ...func(*contract.EvaluationPlanV2)) *maintenanceTestFixture {
	t.Helper()
	return newMaintenanceTestFixtureFrom(t, snapshot, at, maintenanceTestIndex{alerts: alerts}, mutate...)
}

// newMaintenanceTestFixtureFrom is newMaintenanceTestFixture with the copy's
// index given whole.
func newMaintenanceTestFixtureFrom(t *testing.T, snapshot string, at time.Time, source maintenanceTestIndex, mutate ...func(*contract.EvaluationPlanV2)) *maintenanceTestFixture {
	t.Helper()
	f := &maintenanceTestFixture{at: at, writer: &maintenanceTestWriter{}, runner: &maintenanceTestRunner{check: func(context.Context) error { return nil }, scope: "obj-a", revision: 1}}
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
	var facts openalerts.ConsoleFacts
	if source.trusted {
		facts = confirmedMaintenanceFacts{}
	}
	cache, err := openalerts.NewIndex(openalerts.IndexOptions{Source: source, Reconciler: source, Subscriber: source, Facts: facts, Now: f.now,
		MaxStrategies: 10, MaxMembers: 100, MaxBytes: 1 << 20, MaxLocalEntries: 100, ReadBatch: 2, ReconcileBatch: 1,
		RefreshInterval: time.Hour, IndexInterval: time.Hour, ReconcileInterval: time.Hour, CalibrationMaxAge: 2 * time.Hour, LocalRetention: time.Minute, CycleTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	key := openalerts.StrategyKey{TenantID: p.Identity.TenantID, StrategyID: p.Identity.StrategyID}
	if err := cache.TrackOwned(key); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = cache.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := testwait.Deadline(t)
	// A failing calibration is waited for until it has failed once, and
	// until a trusted copy has read the set, or failed to.
	settled := func(snapshot openalerts.StrategySnapshot) bool {
		if source.reconcileErr == nil {
			return snapshot.Calibrated
		}
		if !source.trusted {
			return snapshot.Reason == "calibration_failed"
		}
		// A failed read and a failed calibration are both counted a read
		// error; with the reads answering, it is the calibration's.
		failed := cache.Stats().Unavailable[openalerts.UnavailableReadError] > 0
		return failed && (source.readErr != nil || snapshot.Loaded)
	}
	for snapshot := cache.Snapshot(key); !settled(snapshot); snapshot = cache.Snapshot(key) {
		if time.Now().After(deadline) {
			t.Fatal("cache did not calibrate")
		}
		if source.trusted {
			// A read waits for the subscription; a round run here does not
			// wait for the loop's next.
			cache.Refresh(context.Background())
		}
		time.Sleep(time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Now: f.now, Observer: observability.NopObserver{}}, runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{group.Identity: {runner: f.runner}}}
	f.m = &effectiveMaintenance{bundle: bundle, catalog: &maintenanceTestCatalog{plans: []controlplane.MaintenancePlan{{Identity: p.Identity, Compiled: compiled}}}, cache: cache, writer: f.writer, sourceID: "native", capacity: config.LinkdCapacity{GroupBatch: 2, CloseBatch: 5, LocalEntries: 100}}
	return f
}

const maintenanceReadySnapshot = `{"schema_version":1,"status":"READY","business_timezone":"UTC","calendars":[]}`

func maintenanceAlert(source, severity string) openalerts.Alert {
	return openalerts.Alert{AlertID: "instance", EventSourceID: source, Severity: severity, Fingerprint: strings.Repeat("a", 32)}
}
func maintenanceTime(hour, minute int) time.Time {
	return time.Date(2026, 9, 22, hour, minute, 0, 0, time.UTC)
}

func TestEffectiveMaintenanceOnlyClosesCurrentInactiveNativeAlerts(t *testing.T) {
	for _, tc := range []struct {
		name, snapshot, source, severity string
		at                               time.Time
		want                             int
	}{
		{"inactive", maintenanceReadySnapshot, "native", "critical", maintenanceTime(8, 0), 1},
		{"active", maintenanceReadySnapshot, "native", "critical", maintenanceTime(10, 0), 0},
		{"unknown", "", "native", "critical", maintenanceTime(8, 0), 0},
		{"foreign-source", maintenanceReadySnapshot, "other", "critical", maintenanceTime(8, 0), 0},
		{"level-not-reported", maintenanceReadySnapshot, "native", "", maintenanceTime(8, 0), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMaintenanceTestFixture(t, tc.snapshot, tc.at, []openalerts.Alert{maintenanceAlert(tc.source, tc.severity)})
			f.m.step(context.Background())
			if f.runner.lastErr != nil || len(f.writer.batches) != tc.want {
				t.Fatalf("err=%v batches=%v", f.runner.lastErr, f.writer.batches)
			}
			if tc.want == 1 {
				r := f.writer.batches[0][0]
				if r.StrategyID != 123 || r.StrategyRevision != 4 || r.BusinessID != 2 || r.TenantID != "tenant-a" || r.AlertInstanceID != "instance" || !r.OccurredAt.Equal(tc.at) {
					t.Fatalf("request=%+v", r)
				}
			}
		})
	}
}

func TestEffectiveMaintenanceRechecksFenceAndActiveBoundaryBeforeClose(t *testing.T) {
	for _, boundary := range []bool{false, true} {
		t.Run(map[bool]string{false: "fence-rejected", true: "became-active"}[boundary], func(t *testing.T) {
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 59), []openalerts.Alert{maintenanceAlert("native", "critical")})
			checkErr := errors.New("owner lost")
			f.runner.check = func(context.Context) error {
				if boundary {
					f.advance(time.Minute)
					return nil
				}
				return checkErr
			}
			f.m.step(context.Background())
			if len(f.writer.batches) != 0 {
				t.Fatal("close escaped final check")
			}
			if !boundary && !errors.Is(f.runner.lastErr, checkErr) {
				t.Fatalf("err=%v", f.runner.lastErr)
			}
		})
	}
}

func TestEffectiveMaintenanceACKDedupExpiresAndFailedWritesRetry(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), []openalerts.Alert{maintenanceAlert("native", "critical")})
	f.m.step(context.Background())
	f.advance(59 * time.Second)
	f.m.step(context.Background())
	if len(f.writer.batches) != 1 {
		t.Fatal("ACK not deduplicated for a minute")
	}
	f.advance(time.Second)
	f.m.step(context.Background())
	if len(f.writer.batches) != 2 {
		t.Fatal("unconfirmed active alert never retried")
	}
	f.advance(time.Minute)
	f.writer.err = errors.New("producer unavailable")
	f.m.step(context.Background())
	if f.runner.lastErr == nil {
		t.Fatal("write error lost")
	}
	f.writer.err = nil
	f.m.step(context.Background())
	if len(f.writer.batches) != 4 {
		t.Fatal("failed write incorrectly recorded as ACK")
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

func TestEffectiveMaintenanceSharesExecutionTrackingWithoutLosingACK(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), nil)
	plan := f.m.catalog.(*maintenanceTestCatalog).plans[0].Identity
	var qg execution.QueryGroupIdentity
	for id := range f.m.bundle.runners {
		qg = id
	}
	key := openalerts.StrategyKey{TenantID: plan.TenantID, StrategyID: plan.StrategyID}
	f.m.cache.Untrack(key)
	// Detection can emit before the first maintenance round has run.
	f.m.registerExecutedPlans(qg, []execution.PlanIdentity{plan})
	fingerprint := strings.Repeat("b", 32)
	f.m.cache.Acknowledged([]contract.TriggerEventV1{{EventKind: contract.TriggerEventAbnormal, TenantID: plan.TenantID, DedupeMD5: fingerprint,
		PlanRef: contract.RuntimePlanRefV1{StrategyID: plan.StrategyID}, StrategyRef: &contract.StrategySnapshotRef{TenantID: plan.TenantID, BusinessID: 2, StrategyID: 123, Revision: 4},
		LevelResults: []contract.LevelResultV1{{LevelID: 1, Priority: 1, Result: contract.LevelResultAbnormal}}}})
	if !f.m.cache.Contains(plan.TenantID, plan.StrategyID, fingerprint) {
		t.Fatal("first ACK was lost before maintenance tracking")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			f.m.registerExecutedPlans(qg, []execution.PlanIdentity{plan})
		}
	}()
	for i := 0; i < 100; i++ {
		f.m.step(context.Background())
	}
	<-done
	if f.m.refs[key] != 1 || !f.m.cache.Contains(plan.TenantID, plan.StrategyID, fingerprint) {
		t.Fatal("repeated/concurrent registration lost member or duplicated references")
	}
	f.m.bundle.mu.Lock()
	delete(f.m.bundle.runners, qg)
	f.m.bundle.mu.Unlock()
	f.m.step(context.Background())
	if f.m.cache.Stats().Tracked != 0 || len(f.m.refs) != 0 {
		t.Fatal("released group retained execution-registered cache entries")
	}
}

// Which alerts are this deployment's comes from the target the link's
// Console lists when the source is not configured; a Console that cannot say
// closes nothing and says so.
func TestEffectiveMaintenanceTakesItsSourceFromTheLinksTarget(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), []openalerts.Alert{maintenanceAlert("native", "")})
	f.m.sourceID = ""
	f.m.sourceOf = func(context.Context) (string, error) { return "native", nil }
	f.m.step(context.Background())
	if len(f.writer.batches) != 1 {
		t.Fatalf("the source read from the link's target was not used: %v", f.writer.batches)
	}
	g := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), []openalerts.Alert{maintenanceAlert("native", "")})
	g.m.sourceID = ""
	g.m.sourceOf = func(context.Context) (string, error) { return "", errors.New("console unreachable") }
	g.m.step(context.Background())
	if len(g.writer.batches) != 0 || g.m.Stats()["source_unknown"] != 1 {
		t.Fatalf("a Console that could not name the source, with none read before, still closed or said nothing: %v %v", g.writer.batches, g.m.Stats())
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
// command by the configured timeouts, the Kafka write by the producer's), so
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
			f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), []openalerts.Alert{maintenanceAlert("native", "critical")})
			store := &slowStore{}
			f.m.catalog.(*maintenanceTestCatalog).took = store.take(tc.read)
			f.runner.check = store.take(tc.check)
			f.m.step(context.Background())
			if f.runner.lastErr != nil || len(f.writer.batches) != 1 {
				t.Fatalf("err=%v batches=%v spent=%s", f.runner.lastErr, f.writer.batches, store.spent)
			}
		})
	}
}

// The source the closes are for is read once a step: with no step deadline
// any more, a Console that does not answer would otherwise be waited on once
// for every owned group in turn, and its failure is one count for the step,
// not one for every group it holds back.
func TestEffectiveMaintenanceReadsTheSourceOnceAStep(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), []openalerts.Alert{maintenanceAlert("native", "")})
	for _, qg := range []execution.QueryGroupIdentity{"query-group-b", "query-group-c"} {
		f.m.bundle.runners[qg] = &phaseTwoQueryGroupLifecycle{runner: f.runner}
	}
	f.m.capacity.GroupBatch = 3
	f.m.sourceID = ""
	asked := 0
	f.m.sourceOf = func(context.Context) (string, error) { asked++; return "", errors.New("console unreachable") }
	f.m.step(context.Background())
	if asked != 1 || len(f.writer.batches) != 0 || f.m.Stats()["source_unknown"] != 1 || f.m.Stats()["unavailable"] != 0 {
		t.Fatalf("asked=%d batches=%v stats=%v, want one read, no close, one source_unknown for the step", asked, f.writer.batches, f.m.Stats())
	}
}

// A Console that fails does not stop the closes once this deployment's
// source has been read: the source is a deployment property, used as a
// filter, and the closes go on with the one last read, counted once a step
// as source_last_known. The failed read waits out its back-off rather than
// being asked again every step.
func TestEffectiveMaintenanceClosesOnTheLastReadSourceWhileTheConsoleFails(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), []openalerts.Alert{maintenanceAlert("native", "")})
	asked := 0
	f.m.sourceOf = func(context.Context) (string, error) { asked++; return "", errors.New("console unreachable") }
	f.m.step(context.Background())
	if asked != 1 || len(f.writer.batches) != 1 || f.m.Stats()["source_last_known"] != 1 {
		t.Fatalf("asked=%d batches=%v stats=%v, want the close sent on the source last read", asked, f.writer.batches, f.m.Stats())
	}
	f.m.step(context.Background())
	if asked != 1 {
		t.Fatalf("the Console was asked again %d times inside its back-off", asked-1)
	}
}

// With no source read by this loop, the open-alert copy's own - the source
// its last calibration named - is the one last read.
func TestEffectiveMaintenanceFallsBackToTheCopysOwnSource(t *testing.T) {
	f := newMaintenanceTestFixtureFrom(t, maintenanceReadySnapshot, maintenanceTime(8, 0),
		maintenanceTestIndex{alerts: []openalerts.Alert{maintenanceAlert("native", "")}, own: "native"})
	f.m.sourceID = ""
	f.m.sourceOf = func(context.Context) (string, error) { return "", errors.New("console unreachable") }
	f.m.step(context.Background())
	if len(f.writer.batches) != 1 || f.m.Stats()["source_last_known"] != 1 || f.m.sourceID != "native" {
		t.Fatalf("batches=%v stats=%v source=%q, want the close sent on the copy's own source", f.writer.batches, f.m.Stats(), f.m.sourceID)
	}
}

// A failed source read backs off from one step, doubling up to the
// coverage, and a read that answers again resets it and is used.
func TestEffectiveMaintenanceSourceReadBacksOffAndRecovers(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), []openalerts.Alert{maintenanceAlert("native", "")})
	f.m.sourceID = ""
	failing, asked := true, 0
	f.m.sourceOf = func(context.Context) (string, error) {
		asked++
		if failing {
			return "", errors.New("console unreachable")
		}
		return "native", nil
	}
	f.m.step(context.Background())
	if asked != 1 || f.m.sourceBackoff != effectiveSourceRetryFloor || len(f.writer.batches) != 0 {
		t.Fatalf("asked=%d backoff=%s batches=%v after the first failure", asked, f.m.sourceBackoff, f.writer.batches)
	}
	for want := 2 * effectiveSourceRetryFloor; want <= effectiveSourceCoverage; want *= 2 {
		f.advance(f.m.sourceBackoff)
		f.m.step(context.Background())
		if f.m.sourceBackoff != min(want, effectiveSourceCoverage) {
			t.Fatalf("backoff %s after %d reads, want %s", f.m.sourceBackoff, asked, min(want, effectiveSourceCoverage))
		}
	}
	f.advance(f.m.sourceBackoff)
	f.m.step(context.Background())
	if f.m.sourceBackoff != effectiveSourceCoverage {
		t.Fatalf("backoff %s, want it held at the coverage %s", f.m.sourceBackoff, effectiveSourceCoverage)
	}
	failing = false
	f.advance(f.m.sourceBackoff)
	f.m.step(context.Background())
	if f.m.sourceBackoff != 0 || f.m.sourceID != "native" || len(f.writer.batches) != 1 {
		t.Fatalf("backoff=%s source=%q batches=%v once the Console answered, want reset and the close sent", f.m.sourceBackoff, f.m.sourceID, f.writer.batches)
	}
}
