package main

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scopeclose"
)

// Through the production pieces: the copy and the closer the bundle builds
// from configuration against a stub of the link's control plane, bound as
// the bundle binds them; the admission step's superseded-identity offers
// reach the close through the sink the bundle installs. Only the old
// fingerprint the copy knows open - this process's own acknowledged
// ABNORMAL - is closed, by that fingerprint, under its reason, and only
// after two Slots; an old fingerprint the copy has no word on counts
// nothing.
func TestSupersededCloseThroughTheProductionWiring(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		t.Skip("no redis-server")
	}
	stub := &stubControlPlane{bodies: map[string][]string{}, matches: []string{"instance-ours"}}
	plane := httptest.NewServer(stub)
	t.Cleanup(plane.Close)
	cfg := config.Default()
	cfg.Redis.StatePrefix = "alarmd-test"
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: plane.URL, JWTSecret: "example-secret"}
	link, err := newLinkdManage(cfg, time.Now)
	if err != nil || link == nil {
		t.Fatalf("link client %v (%v)", link, err)
	}
	copy, err := newActiveCopy(cfg, link, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := newFilterCloser(cfg, link, server.Client(), time.Now, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	closer, sink := targetScopeCloseFor(cfg, time.Now)
	bundle := &phaseTwoWorkerBundle{}
	bindTargetScopeClose(bundle, closer, copy, newFilterRoute(cfg, filter), metric.NewRecorder(metric.BuildInfo{}))

	plan := execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "123"}
	ours, theirs := strings.Repeat("a", 32), strings.Repeat("b", 32)
	copy.Acknowledged([]contract.TriggerEventV1{{EventKind: contract.TriggerEventAbnormal, TenantID: plan.TenantID, DedupeMD5: ours,
		PlanRef: contract.RuntimePlanRefV1{StrategyID: plan.StrategyID}, StrategyRef: &contract.StrategySnapshotRef{TenantID: plan.TenantID, BusinessID: 2, StrategyID: 123, Revision: 4}}})
	// One Slot as the access path hands it over: a screen per Plan, then
	// the cleared Plan's offers one by one.
	slot := func(round int64) {
		if !sink.Screen(plan) {
			t.Fatal("Screen refused a strategy with an open alert")
		}
		for _, fingerprint := range []string{ours, theirs} {
			sink.Observe(access.SupersededOffer{Plan: plan, Fingerprint: fingerprint, StrategyRevision: 4, Round: round})
		}
		closer.Step(context.Background())
	}
	slot(1700000000)
	if requests := stub.requests("/api/v1/alerts/close"); len(requests) != 0 {
		t.Fatalf("asked the link to close after one Slot: %v", requests)
	}
	slot(1700000060)
	var closes []string
	for _, body := range stub.requests("/api/v1/alerts/close") {
		if !strings.Contains(body, `"dry_run":true`) {
			closes = append(closes, body)
		}
	}
	if len(closes) != 1 {
		t.Fatalf("closes = %v, want one", closes)
	}
	for _, field := range []string{`"bk_tenant_id":"tenant-a"`, `"labels.strategy_id":"123"`, ours} {
		if !strings.Contains(closes[0], field) {
			t.Fatalf("close %s does not carry %s", closes[0], field)
		}
	}
	if strings.Contains(closes[0], theirs) || strings.Contains(closes[0], "dimensions.") {
		t.Fatalf("close %s names more than the old fingerprint the copy holds", closes[0])
	}
	want := map[string]uint64{scopeclose.OutcomeUnconfirmed: 1, scopeclose.OutcomeCloseSent: 1, scopeclose.OutcomeSupersededCloseSent: 1}
	for outcome, n := range closer.Stats() {
		if n != want[outcome] {
			t.Fatalf("stats = %v, want %v", closer.Stats(), want)
		}
	}
}

// Every field of the close's facts reaches the replica's facts, and they sit
// on the active copy's facts: a field dropped on the way would read as zero,
// which here is a reading.
func TestTheTargetScopeCloseFactsAreCarriedFieldForField(t *testing.T) {
	facts := targetScopeCloseFacts(scopeclose.Facts{Pending: 1, Confirmed: 2, MaxEntries: 3,
		Outcomes: map[string]uint64{scopeclose.OutcomeCloseSent: 4},
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
	source := withTargetScopeClose(func() *fleet.ActiveCopyFacts { return &fleet.ActiveCopyFacts{} }, closer)
	carried := source()
	if carried.TargetScopeClose == nil || len(carried.TargetScopeClose.Outcomes) != len(scopeclose.Outcomes) {
		t.Fatalf("active copy facts = %+v, want the close beside them with every outcome", carried)
	}
}

// A deployment without the link's control plane has nothing the close
// could act on, so nothing of it is wired: no sink on the admission step
// (and so no Screen), no loop, and the outcome counters stay at
// zero on /metrics. With the control plane, all three are there.
func TestTheTargetScopeCloseIsNotWiredWithoutTheLinksControlPlane(t *testing.T) {
	cfg := config.Default()
	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{}
	closer, sink := targetScopeCloseFor(cfg, time.Now)
	// The sink must be a nil interface, not a typed nil: the access path
	// tests the interface, and a typed nil would be screened.
	if closer != nil || sink != nil {
		t.Fatalf("without the control plane: closer %v sink %v, want neither", closer, sink)
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle := &phaseTwoWorkerBundle{}
	bindTargetScopeClose(bundle, closer, nil, nil, recorder)
	if bundle.dependencies.RunTargetScopeClose != nil {
		t.Fatal("a loop was wired without the control plane")
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, family := range families {
		if family.GetName() != "alarmd_target_scope_close_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			found++
			if m.GetCounter().GetValue() != 0 {
				t.Errorf("%v = %v without the control plane, want 0", m.GetLabel(), m.GetCounter().GetValue())
			}
		}
	}
	if found != len(scopeclose.Outcomes) {
		t.Fatalf("%d outcome cells on /metrics, want every one of %d pre-registered", found, len(scopeclose.Outcomes))
	}

	cfg.PhaseTwo.Linkd.ControlPlane = config.LinkdControlPlaneConfig{URL: "https://link.example.test", JWTSecret: "example-secret"}
	closer, sink = targetScopeCloseFor(cfg, time.Now)
	if closer == nil || sink == nil {
		t.Fatal("with the control plane the close was not built")
	}
	bindTargetScopeClose(bundle, closer, nil, nil, recorder)
	if bundle.dependencies.RunTargetScopeClose == nil {
		t.Fatal("with the control plane no loop was wired")
	}
}
