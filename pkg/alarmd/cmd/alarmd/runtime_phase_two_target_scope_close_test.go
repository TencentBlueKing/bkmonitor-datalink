package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdoutput"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scopeclose"
)

// scopeCloseIndex is the link as the copy reads it: one strategy's set,
// calibrated with its alerts and this deployment's source.
type scopeCloseIndex struct {
	source string
	alerts []openalerts.Alert
}

func (s scopeCloseIndex) ReadSet(context.Context, openalerts.StrategyKey) ([]string, error) {
	members := make([]string, 0, len(s.alerts))
	for _, alert := range s.alerts {
		members = append(members, alert.Fingerprint)
	}
	return members, nil
}

func (s scopeCloseIndex) Reconcile(ctx context.Context, key openalerts.StrategyKey) (openalerts.Reconciliation, error) {
	members, _ := s.ReadSet(ctx, key)
	return openalerts.Reconciliation{EventSourceID: s.source, Members: members, Alerts: s.alerts}, nil
}

func (s scopeCloseIndex) Watch(ctx context.Context, ready func(bool), _ func(openalerts.StrategyKey)) error {
	ready(true)
	<-ctx.Done()
	return ctx.Err()
}

// calibratedCopy is a real index copy, running, calibrated for one strategy.
func calibratedCopy(t *testing.T, key openalerts.StrategyKey, index scopeCloseIndex) *openalerts.Cache {
	t.Helper()
	cache, err := openalerts.NewIndex(openalerts.IndexOptions{Source: index, Reconciler: index, Subscriber: index,
		MaxStrategies: 10, MaxMembers: 100, MaxBytes: 1 << 20, MaxLocalEntries: 100, ReadBatch: 2, ReconcileBatch: 1,
		RefreshInterval: time.Hour, IndexInterval: time.Hour, ReconcileInterval: time.Hour, CalibrationMaxAge: 2 * time.Hour,
		LocalRetention: time.Minute, CycleTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.TrackOwned(key); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = cache.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(3 * time.Second)
	for !cache.Snapshot(key).Calibrated {
		if time.Now().After(deadline) {
			t.Fatal("copy did not calibrate")
		}
		time.Sleep(time.Millisecond)
	}
	return cache
}

// Through the production pieces: the admission step's rejections reach the
// close through the observer the bundle installs, are judged against a real
// calibrated copy, and the close goes out through the writer as the shared
// close. Only this deployment's alert is closed, and only after two Slots.
func TestTargetScopeCloseThroughTheProductionWiring(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "123"}
	key := openalerts.StrategyKey{TenantID: plan.TenantID, StrategyID: plan.StrategyID}
	ours, theirs := strings.Repeat("a", 32), strings.Repeat("b", 32)
	cache := calibratedCopy(t, key, scopeCloseIndex{source: "native", alerts: []openalerts.Alert{
		{AlertID: "instance-ours", EventSourceID: "native", Fingerprint: ours},
		{AlertID: "instance-theirs", EventSourceID: "elsewhere", Fingerprint: theirs},
	}})
	closer := scopeclose.New(scopeclose.Options{Send: true})
	writer := &maintenanceTestWriter{}
	closer.Bind(scopeclose.CacheSet(cache), writer)
	sink := scopeDropSink{closer: closer}
	// One Slot as the access path hands it over: a screen per Plan, the
	// cleared Plan's definitive rejections one by one, and the bulk counts.
	slot := func(round int64) {
		if screen := sink.Screen(plan); screen != "" {
			t.Fatalf("Screen = %q for a strategy with open alerts", screen)
		}
		for _, fingerprint := range []string{ours, theirs} {
			sink.Observe(access.ScopeDrop{Plan: plan, Filter: "target_plan", Reason: "out_of_target",
				Fingerprint: fingerprint, StrategyRevision: 4, Round: round})
		}
		sink.Count(access.ScopeDropReporter{Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: execution.EvaluationTime(round)}, Plan: plan}, access.ScopeDropIndefinite, 1)
		sink.Count(access.ScopeDropReporter{Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: execution.EvaluationTime(round)}, Plan: plan}, access.ScopeDropCacheUnavailable, 1)
		sink.Count(access.ScopeDropReporter{Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: execution.EvaluationTime(round)}, Plan: plan}, access.ScopeDropFingerprintUnsupported, 2)
		sink.Count(access.ScopeDropReporter{Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: execution.EvaluationTime(round)}, Plan: plan}, access.ScopeDropNoFingerprint, 3)
		closer.Step(context.Background())
	}
	slot(1700000000)
	if len(writer.batches) != 0 {
		t.Fatalf("closed after one Slot: %v", writer.batches)
	}
	slot(1700000060)
	if len(writer.batches) != 1 || len(writer.batches[0]) != 1 {
		t.Fatalf("batches = %v, want one close", writer.batches)
	}
	request := writer.batches[0][0]
	if request.AlertInstanceID != "instance-ours" || request.Fingerprint != ours || request.Reason != linkdoutput.CloseReasonTargetOutOfScope ||
		request.StrategyID != 123 || request.StrategyRevision != 4 || request.BusinessID != 2 || request.TenantID != "tenant-a" {
		t.Fatalf("request = %+v", request)
	}
	stats := closer.Stats()
	if stats[scopeclose.OutcomeClosed] != 1 || stats[scopeclose.OutcomeProducerForeign] != 1 || stats[scopeclose.OutcomeCacheUnavailable] != 2 || stats[scopeclose.OutcomeIndefinite] != 2 ||
		stats[scopeclose.OutcomeFingerprintUnsupported] != 4 || stats[scopeclose.OutcomeNotMember] != 6 {
		t.Fatalf("stats = %v", stats)
	}
}

// Every field of the close's facts reaches the replica's facts, and they sit
// on the open set's facts: a field dropped on the way would read as zero,
// which here is a reading.
func TestTheTargetScopeCloseFactsAreCarriedFieldForField(t *testing.T) {
	facts := targetScopeCloseFacts(scopeclose.Facts{Armed: true, Pending: 1, Confirmed: 2, MaxEntries: 3,
		Outcomes: map[string]uint64{scopeclose.OutcomeWouldSend: 4},
		Strategies: []scopeclose.StrategyFacts{{TenantID: "t", StrategyID: "s", Pending: 1, Confirmed: 2,
			Outcomes: map[string]uint64{scopeclose.OutcomeUnconfirmed: 5}, PendingSample: []string{"aaaaaaaa"}, DecidedSample: []string{"bbbbbbbb"}}}})
	var zero func(path string, value reflect.Value)
	zero = func(path string, value reflect.Value) {
		switch value.Kind() {
		case reflect.Ptr:
			zero(path, value.Elem())
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				zero(path+"."+value.Type().Field(i).Name, value.Field(i))
			}
		case reflect.Slice, reflect.Map:
			if value.Len() == 0 {
				t.Errorf("%s was not carried", path)
			}
			if value.Kind() == reflect.Slice {
				for i := 0; i < value.Len(); i++ {
					zero(path, value.Index(i))
				}
			}
		default:
			if value.IsZero() {
				t.Errorf("%s was not carried", path)
			}
		}
	}
	zero("target_scope_close", reflect.ValueOf(facts))

	closer := scopeclose.New(scopeclose.Options{})
	source := withTargetScopeClose(func() *fleet.OpenAlertSetFacts { return &fleet.OpenAlertSetFacts{Mode: "authoritative"} }, closer)
	carried := source()
	if carried.TargetScopeClose == nil || len(carried.TargetScopeClose.Outcomes) != len(scopeclose.Outcomes) {
		t.Fatalf("open set facts = %+v, want the close beside them with every outcome", carried)
	}
}

// A deployment without the alert link's Console has nothing the close could
// act on, so nothing of it is wired: no sink on the admission step (and so
// no Screen or Count), no loop, and the outcome counters stay at zero on
// /metrics. With the Console, all three are there.
func TestTheTargetScopeCloseIsNotWiredWithoutTheLinkConsole(t *testing.T) {
	cfg := config.Default()
	cfg.PhaseTwo.Linkd.ConsoleURL = ""
	closer, sink := targetScopeCloseFor(cfg, time.Now)
	// The sink must be a nil interface, not a typed nil: the access path
	// tests the interface, and a typed nil would be screened and counted.
	if closer != nil || sink != nil {
		t.Fatalf("without a Console: closer %v sink %v, want neither", closer, sink)
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle := &phaseTwoWorkerBundle{}
	bindTargetScopeClose(bundle, closer, nil, &maintenanceTestWriter{}, recorder)
	if bundle.dependencies.RunTargetScopeClose != nil {
		t.Fatal("a loop was wired without a Console")
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_target_scope_close_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			found++
			if m.GetCounter().GetValue() != 0 {
				t.Errorf("%v = %v without a Console, want 0", m.GetLabel(), m.GetCounter().GetValue())
			}
		}
	}
	if found != len(scopeclose.Outcomes) {
		t.Fatalf("%d outcome cells on /metrics, want every one of %d pre-registered", found, len(scopeclose.Outcomes))
	}

	cfg.PhaseTwo.Linkd.ConsoleURL = "https://console.example.test"
	closer, sink = targetScopeCloseFor(cfg, time.Now)
	if closer == nil || sink == nil {
		t.Fatal("with a Console the close was not built")
	}
	bindTargetScopeClose(bundle, closer, nil, &maintenanceTestWriter{}, recorder)
	if bundle.dependencies.RunTargetScopeClose == nil {
		t.Fatal("with a Console no loop was wired")
	}
}
